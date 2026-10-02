# 2026-10-02 公共出口 DNS 与代理连接容量修复

## 范围与证据边界

用户授权按上一阶段的实际调查修复 jjs 各出口访问网站慢、间歇超时的公共代码，并同步 GitHub。本次不发布 Release 或签名包，不改独立 aimili-vpngate 仓库，不切换或重启本机现用代理，不重启 jjs 完整出口引擎或更换活动节点。

前三天的 v2rayN GUI 日志没有普通 `ping0.cc` 网页请求记录；RealPing 日志缺少节点归属字段，不能据此宣称找到了用户每次网页超时的唯一原因。此前隧道实测已经确认公网 UDP DNS 间歇无答复，而该隧道推送的内部 DNS 稳定响应。原代码已有 TCP DNS 回退，但会先等待默认 3 秒，且忽略 `PUSH_REPLY` DNS。所有代理监听同时受每监听 64、全局 128 的固定门禁限制，满额立即断开；这些是本次修复的明确公共问题。

## 实现

- `proxy_server.py`：按设备登记推送 IPv4 DNS，变化或移除时只清该设备缓存；公网回退仍绑定相同 TUN。UDP、TCP connect/send/逐片 recv 共用单个解析器截止时间，各解析器与查询类型共用一次解析截止时间。全失败抛 `ERR_PROXY_DNS_FAILED`，数值地址保留正常连接语义，禁止系统 DNS 接管失败。并发相同域名使用一个正在进行的解析，设备之间独立。
- `vpngate_manager.py`：OpenVPN reader 解析 `PUSH_REPLY`；以进程生命周期标识隔离同名设备，旧 reader 和退出 watcher 不能改掉新进程 DNS。启动、失败、短探测结束、重连 DNS 改变及进程退出清理对应映射和缓存；热备用提升保留正在运行的设备映射。
- 连接容量：短等待最多 250 ms；每 5 秒采样 RAM、各级 cgroup PID/内存限制及 FD 余量，保留主机 64 MiB、cgroup 32 MiB/32 任务和 32 FD。以每条连接 1 MiB、两个 FD 估算额外容量，不把 Swap 当可用内存。默认配置上界全局 256、单监听 192；每个其他已登记监听预留最多两条连接。资源紧张拒绝新连接，保留已有连接。日志区分 `listener_limit`、`global_limit`、`resource_pressure`，循环错误限频。
- 无新增第三方依赖。复杂度审查已执行，无需新增抽象或删减的建议。

### 环境参数

| 参数 | 默认 | 含义 |
| --- | ---: | --- |
| `LOCAL_PROXY_MAX_CONNECTIONS` | 256 | 配置上界，实际仍受资源预算限制 |
| `LOCAL_PROXY_MAX_CONNECTIONS_PER_LISTENER` | 192 | 每监听配置上界 |
| `LOCAL_PROXY_ACQUIRE_WAIT_MS` | 250 | 新连接等待空位的最大毫秒数 |
| `LOCAL_PROXY_DNS_TIMEOUT` | 1.0 | 单解析器 UDP + TCP 的总秒数 |
| `LOCAL_PROXY_DNS_TOTAL_TIMEOUT` | 2.5 | 一次解析跨解析器的总秒数 |
| `OPENVPN_TUN_DNS` | `8.8.8.8,1.1.1.1` | 推送 DNS 后的同隧道回退 |

## 定向验证

最新 Python 回归命令：

```text
cd services/aimili-egress
python -m unittest tests.test_proxy_dns tests.test_proxy_capacity tests.test_proxy_health tests.test_pushed_dns tests.test_main_identity tests.test_main_assignment tests.test_exit_slot_types -q
cd ../../deploy/vps
python -m unittest test_runtime_proxy_bridge -q
```

188 项出口回归、11 项承接测试通过，语法编译与 `git diff --check` 通过。新增 DNS 根因测试曾在未完成实现时复现 7 个失败，修复后全部通过；覆盖严格预算、并发解析、缓存隔离、禁止系统 DNS 回退和设备 DNS 生命周期。容量覆盖实时资源夹紧、并发计数、跨监听预留和失败释放。测试使用受控 socket、进程和临时数据目录，不拨号真实 OpenVPN。此前全量测试产生的 40 个未跟踪 `jp-*.ovpn` 已核对并清理，未提交生产节点配置。

## jjs 同隧道对照

北京时间 15:31，原端口与独立修复端口使用相同活动设备、相同目标请求；尚未导流。下列为总耗时：

| 目标 | 原端口 | 修复端口 |
| --- | ---: | ---: |
| 主连接 → Google 204 | 3.780 s | 0.682 s |
| 出口 2 → Google 204 | 3.804 s | 0.702 s |
| 主连接 → ChatGPT HTTP 403 | 3.754 s | 0.736 s |

五个修复端口共 15 个 HTTPS 请求完成，Google 返回 204、ping0 返回 200、ChatGPT 返回 403。403 只证明已到达网站并收到拒绝响应，不代表 ChatGPT 页面可用。原本已缓存或没有 DNS 等待的普通出口改善不显著，不能宣称所有请求都会降低三秒。

有限并发测试在一个修复监听上同时建立 72 条真实 SOCKS 连接，72/72 完成；同一时刻另一出口仍返回 Google 204（0.720 s）。承接进程内存约 23 MiB、78 任务。上一轮有两条因实时预算及其他监听预留被拒绝，日志为 `resource_pressure`、活动 70、全局当时 78；后续测试没有为了得到成功结果手工提高预算。容量会随同机其他项目和候选维护的占用变化，采样不能保证资源瞬间耗尽时绝对无失败。

## 不重启现用代理的应用

- 修复源码原子落盘到 `/opt/aimili-gateway/services/aimili-egress`，没有创建人工备份。`proxy_server.py` SHA256 为 `51484c787c1a0b8a1ecf208e583066b7d8b9816a18c28a0cfd9796597ee43c66`；manager 为 `e4f8d24fab3300c734b8aa34ccc68c0d5633e8a31a2e3f00471ce35a14d5fbdd`。
- 当前旧进程继续运行，临时 `aimili-proxy-recovery.service` 用修复代码承接新连接。仅五个本机 `OUTPUT` NAT 规则：7928→27928，17928–17931→37928–37931，目的地址限定 `127.0.0.1`、标签 `aimili-proxy-recovery`。已有连接保持原 conntrack 路径，没有公开新端口。
- 临时模块 `deploy/vps/runtime_proxy_bridge.py` 根据实际进程 cgroup、PID/启动时间、配置身份及槽位设备识别出口；捕获进程首次 endpoint，防止 `.standby_N.ovpn` 被补回备用覆盖后误认旧进程。无法唯一核对主连接时拒绝请求并输出 `unresolved`，不猜测其他设备。设备/身份改变会清旧连接缓存。
- 当前旧引擎的 API 不暴露设备 DNS。承接服务从实际 `PUSH_REPLY` 日志提取候选内部 DNS，并在每条真实隧道上验证 A 查询后才登记；本次五条均验证 `10.211.254.254`。正常新引擎直接读取自己的 OpenVPN reader，不依赖这些候选探测。
- 单元及承接文件在 `/run`，绑定 `aimilivpn.service`，未设置开机 enable。`MemoryMax=192M`、`TasksMax=256`、`LimitNOFILE=4096`。引擎下次正常停止时承接服务跟随停止，`ExecStopPost` 删除自己的五条转发；正常启动加载落盘修复。未为验证这个过程实际重启现用引擎或机器。不能删除当前仍在使用的 `/run/aimili-proxy-recovery`。
- 手动撤销临时承接可执行 `systemctl stop aimili-proxy-recovery`，它只删除自己的规则；正在经该临时进程使用的连接会被关闭，因此不能在保持代理不中断的验收中执行。本次没有实际执行撤销，清理逻辑已按精确规则核对。

## 客户端真实配置验证

从 v2rayN 只读 SQLite 持久配置复用 jjs 的 8443、20000–20003 五条 VLESS：主为 XHTTP/Reality，普通出口为 raw/Reality；UUID 使用 `ProfileItem.Password`，协议/传输扩展按 v2rayN 源码映射。身份材料只存在内存及自动清理的临时配置，未输出。独立临时 Xray 监听 24839–24843，结束后只终止自己的进程并清理临时文件。15:17 读取到 jjs `/32` 路由使用 `qinshi`；16:08 最新读取时该接口已不存在，实际路由为 `singbox_tun` 的 `128.0.0.0/1`。本次未更改这些路由，无法声称两次隔离测试网络路径完全一致，也不声称是物理直连。

北京时间 15:44–15:45，调用桌面修正版相同 `RealPingProbe` 代码并发检测三轮，15/15 成功，全部 HTTP 204：

| 出口 | 第 1 轮 ms | 第 2 轮 ms | 第 3 轮 ms |
| --- | ---: | ---: | ---: |
| 主连接 | 323 | 302 | 295 |
| 出口 1 | 299 | 307 | 312 |
| 出口 2 | 289 | 880 | 307 |
| 出口 3 | 310 | 298 | 316 |
| 出口 4 | 289 | 323 | 278 |

同一客户端配置访问 ping0 五条均 HTTP 200，总耗时 2.08–3.24 秒。修复前同样的隔离请求为 2.28–5.48 秒，这种前后对比还包含网络波动，不能把全部差额归因于 DNS。Windows curl 的额外 Google TLS 请求仍五条超时，即便设置进程内 `--ssl-no-revoke`；它与 .NET 的 30 次 Google 204 响应不同，不能计作成功，也不能用它否定已经完成的 .NET 测速。此工具差异未定位，作为剩余限制保留，未为此更改系统或客户端 TLS 设置。

运行配置 SHA256 保持：`config.json` 为 `d7d07718ef2fe74c6ec6f41253460661e15d6d1c0ca2a09e11cd91c8ec9fe2f8`，`configPre.json` 为 `b02df1f94e3bb161aca74f79f4a10979fbdd259876cae87215630144e5f0610a`。v2rayN/Xray/sing-box PID 分别为 15328/32332/31768，启动时间均保持。本次没有在真实桌面窗口点击按钮，已验证相同检测代码与真实五节点配置的等价路径。

## 最后状态与交付

15:57 回读：Gateway `/healthz` HTTP 200；Gateway、出口引擎、x-ui、Xray、Caddy 均运行，原服务 PID 保持 3331802/3522960/3331799/3331837/16103，`NRestarts=0`。承接服务 PID 3544986，约 10.7 MiB，6 任务；主机 `MemAvailable` 154 MiB，Swap 使用 165 MiB。最后五分钟承接日志无新增限流、SOCKS 失败、线程失败或未识别设备。

16:02 正确提取控制 API `data` 后再次回读：主连接 `active=true / egress_ok=true / repair_status=healthy`；四个普通出口全部 `up / egress_ok=true`，错误码为空；五个专属备用全部 `ready / egress_ok=true`。首次读取脚本未展开 `data` 得到空提取结果，该空结果不作为状态证据。

16:09 增量复测：curl HTTP/1.1 五条 Google 请求返回 204、gstatic 4/5 返回 204（出口 4 一次 TLS 后等待超时）。相同 `RealPingProbe` 第二组三轮为 14/15 成功：主连接第一轮两次 ConnectTimeout 后 -1，第二轮 300 ms，第三轮重试后 1062 ms；其余四出口三轮全部成功。随后 16:20–16:21 只测主连接五轮，第三轮为 -1，前后轮恢复。对应时刻 jjs `aimilivpn` 日志出现 `TLS: soft reset sec=3600/3600` 以及随后完整证书重协商；承接代理没有 DNS 失败、限流、SOCKS 或 Gateway 错误。第一失败边界是活动主隧道 TLS 重协商期间的短暂数据面阻塞，而不是本次 DNS/容量修复。v2rayN 的检测器已经用两次请求，但在整个重协商窗口内仍可能两次失败；要让桌面界面不出现 -1，应单独给客户端检测增加遇到这类瞬时重协商时的有界退避重试，不能靠修改 Gateway 返回成功或关闭故障检测。Gateway 本轮不调整 OpenVPN 重协商参数，避免破坏现有会话。此前 15/15 是当时真实结果，不代表后续持续全部通过。

本次无 UI 修改，未做内置浏览器 UI 验收。源码提交和远程同步以 Git 实际结果为准；本次未创建 Release、未发布签名包，现有正式发布入口保持不变。xjp、bj、ny 未部署本次代码。公开网络、上游 VPN 和网站仍可能产生新的故障，本次结果不构成永不超时的保证。
