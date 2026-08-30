# 主连接安全切换与独立协议模式验收记录

日期：2026-08-29

状态：本地实现已验证；VPS Stage 1、Stage 2 通过；Stage 3 的 XHTTP 空 `listen` 离线配置根因已修复，但生产运行时往返尚未通过；Stage 4–7 未进入

本记录只保存提交、版本、计数、端口、布尔结果、资源指标和脱敏错误码。不得写入连接材料、私钥、后台路径或完整订阅地址。

## 1. 版本与提交

| 项目 | 安全记录 |
| --- | --- |
| Gateway 分支/提交 | `feat/main-switch-protocol-modes`；Stage 3 前基线 `6b04685`，本轮修复见当前分支最新提交 |
| AimiliVPN 分支/提交 | `feat/main-switch-protocol-modes` / `c359ba5` |
| 3x-ui 版本 | 生产命令未返回可解析版本；交接基线为 `3.7.0`，待最终补证 |
| Xray 版本 | `26.7.28` |
| v2rayN 版本 | `7.24.4` |

## 2. 本地验证

| 检查 | 结果 |
| --- | --- |
| AimiliVPN 全量 unittest | 通过，81 tests |
| Gateway 全量 Go test | 通过，全部 package |
| 前端 Vitest | 通过，27 tests |
| 前端 production build | 通过 |
| Python helper、故障注入与外部三协议脚本 | 通过，61 tests，2 项仅因 Windows 普通账户不能创建 symlink 而跳过 |
| 部署契约 | 通过 |
| `git diff --check` | 通过 |
| Ponytail 复杂度审查 | 通过；删除反射式测试脚手架、单行远端包装、重复订阅校验和重复异常包装，无安全边界删减 |
| 独立代码审查 | 无 Critical/Important；确认协议 API 的预期旧模式在 Gateway mutation lock 内原子校验，外部验收恢复对第三方并发切换冲突止写 |

## 3. 生产前硬门

| 指标 | 结果 |
| --- | --- |
| 联合备份与摘要 | Stage 1 成功；Stage 2 尝试均在受限目录创建联合备份 |
| `MemAvailable` | 预检约 186–204 MiB，通过 |
| Swap 空闲 | 预检约 873–929 MiB，通过 |
| 证书域名/有效期 | 通过；证书对唯一，至少七天有效且 Xray 可读 |
| Hysteria2 Xray 离线配置 | 使用真实 Xray `26.7.28` 通过 |
| 非 Gateway 资源数量 | 0 |
| 非 Gateway 指纹已记录 | 是，验收记录不保存原始资源正文 |
| Xray 初始 PID 已记录 | 是 |

## 4. Stage 1：主事务

| 检查 | 结果 |
| --- | --- |
| 受控失败恢复旧主 | 通过；持久状态为 `rolled_back / assign_failed_rolled_back` |
| `7928` 恢复 | 通过 |
| 真实主切换 | 通过；`stage → 三路径验证 → commit` |
| 主 mixed 与公网一致 | 通过；`7928`、主 mixed、`8443` 出口一致 |
| 三个普通槽位不变 | 通过 |
| 本级回滚 | 受控失败路径已执行并通过；真实切换无需回滚 |

## 5. Stage 2：Gateway 与助手

| 检查 | 结果 |
| --- | --- |
| Gateway migration | 通过，migration 10 为 1，`egress_protocol_modes` 四行均初始化为 TCP/Vision ready |
| root helper/path/timer | 已部署；helper 存在，path/timer 在 Stage 2 post-check 时 active |
| 精确 UDP 端口 | 仅 `8443/udp`、`20000/udp`、`20001/udp`、`20002/udp` 四个逻辑规则 |
| 不存在节点用 `443/udp` 规则 | 是 |
| 不存在 UDP 范围规则 | 是 |
| 公网入站数 | 4，仍为 VLESS/TCP 基线 |
| mixed 数 | 4 |
| Xray PID不变 | 是；Stage 2 未重启 x-ui/Xray |
| 非 Gateway 指纹不变 | 是 |

Stage 2 第三次使用修复归档部署通过。独立 post-check 显示 Gateway health 200，四个服务 active，Gateway 二进制 `0755 root:root`、SQLite `0600 aimili-gateway:aimili-gateway`，公网入站数 4、mixed 数 4、非 Gateway 入站数 0，Xray PID 与 Stage 2 前一致。部署后发现出口位 1 的 RU/residential 候选池耗尽，正式检查将其标记 degraded；通过正式 RU 刷新得到 4 个有效候选后，单槽位 rotate 恢复 ready，槽位号、公网端口和 mixed 端口均保持不变。随后 v2rayN 自带 Xray `26.6.1` 对统一订阅四条 TCP/Vision 节点完成外部代理 DNS 和真实出口验证。

## 6. Stage 3：XHTTP 往返

| 检查 | 结果 |
| --- | --- |
| TCP → XHTTP | 未完成；空 `listen` 修复后离线配置通过，运行时请求尚未形成成功往返 |
| v2rayN 识别与真实连接 | TCP 基线曾通过；XHTTP 尚未通过 |
| 代理 DNS/真实出口一致 | TCP 基线曾通过；XHTTP 尚未通过 |
| TCP/Vision 反向恢复 | 最近只读状态仍为四出口 TCP/ready；未完成 XHTTP 运行时应用 |
| 非目标长连接持续 | 运行中配置未修改；收尾检查四公网与四 mixed 均在 |
| Xray PID不变 | 是；仍为单进程 PID `353075` |

Stage 3 首次将出口位 1 请求切到 XHTTP 时，helper 未应用新入站。根因链先定位到 helper 缺少读取受限路径所需的最小 capability；唯一孤立 apply 请求经闭集校验后移入 `0700 root:root` 备份隔离，正式 unit 调整为仅保留 `CapabilityBoundingSet=CAP_DAC_OVERRIDE`、`AmbientCapabilities=`。Gateway 启动恢复随后验证通过，旧订阅、mixed、Aimili 槽位和公网链路均收敛到 TCP/Vision `ready`。

第二次试切已进入 helper，但完整 XHTTP 配置的生产 Xray 离线校验返回 `xray_command_failed`，helper 自动恢复，运行中配置和 Xray PID 未改变。本地 Xray `26.6.1` 最小配置曾提示 `sockopt.trustedXForwardedFor`，但生产复测否定了它是唯一根因。随后直接读取生产 Xray `26.7.28` panic 栈，定位到 helper 把 3x-ui 数据库空监听序列化为 `"listen":""`；3x-ui 正常运行配置会省略该字段。按 TDD 改为仅在监听非空时写入 `listen` 后，生产同版本完整配置离线校验返回 `Configuration OK`，最小 helper 修复已备份并安装。

继续运行时试切时，外部验证器先收到 Gateway HTTP 400，且 helper 请求、结果和事务目录均未新增。版本核对确认 `expectedProtocolMode` 只在提交 `18c6852` 引入，而生产 Gateway 二进制不含该字段；严格 JSON 解码因此在写 spool 前拒绝请求。生产旧二进制已备份，Gateway 已更新到 `18c6852` 并通过服务启动检查。更新后的首次 TCP 基线在主连接 `connections` 返回闭集错误 `not_ready`；此时三个普通出口绑定仍逐项一致，四条协议状态仍为 TCP/ready，helper 事务与请求目录均为空。由于宿主自动审批服务达到用量上限，尚未完成 Gateway 主快照与 AimiliVPN 主状态的只读布尔对比，因此未再发起 XHTTP 或 Hysteria2 变更。

最终只读核验：四条 `egress_protocol_modes` 均为 `active_mode=desired_mode=vless_tcp_reality_vision` 且 `state=ready`；请求目录为空；四个公网入站均为 VLESS/TCP，mixed 数为 4，非 Gateway 入站数为 0；Gateway、x-ui、AimiliVPN、helper path/timer 均 active；无 `repair_required`。

## 7. Stage 4–5：Hysteria2 与资源

未进入。Stage 4–7 依赖 Stage 3 硬门通过，本轮按失败止损要求不继续扩大部署。

| 检查 | 结果 |
| --- | --- |
| 外部 QUIC/TLS | 待填写 |
| v2rayN 识别与真实连接 | 待填写 |
| UFW 包到达 | 待填写 |
| 云 UDP 边界 | 正常/`upstream_udp_blocked`/待填写 |
| 观察时长 | 待填写 秒，期望 ≥ 300 |
| Xray RSS 峰值 | 待填写 MiB，期望 ≤ 96 |
| `MemAvailable` 最低值 | 待填写 MiB |
| Swap 使用增量 | 待填写 MiB，期望 ≤ 128 |
| OOM | 待填写，期望否 |

## 8. Stage 6 与重启恢复

| 逻辑出口 | 协议 | 真实连接 | mixed/SOCKS5H |
| --- | --- | --- | --- |
| 主连接 | TCP/Vision | 待填写 | 待填写 |
| 出口位 1 | XHTTP/REALITY | 待填写 | 待填写 |
| 出口位 2 | Hysteria2/QUIC/TLS | 待填写 | 待填写 |
| 出口位 3 | TCP/Vision | 待填写 | 待填写 |

| 检查 | 结果 |
| --- | --- |
| x-ui/Xray 重启重建混合模式 | 待填写 |
| Gateway 重启恢复 | 待填写 |
| AimiliVPN 未提交事务恢复 | 待填写 |
| Gateway UI 主替换原路径 | 待填写 |
| Gateway UI 协议切换原路径 | 待填写 |
| “复制节点订阅”原路径 | 待填写 |
| v2rayN 四节点刷新与逐条连接 | 待填写 |

## 9. 最终不变量

| 不变量 | 结果 |
| --- | --- |
| AimiliVPN 运行出口总数 = 4 | 是 |
| 公网入站总数 = 4 | 是，当前全部 TCP/Vision |
| mixed 总数 = 4 | 是 |
| 每逻辑出口只有一个公网协议 | 是 |
| mixed/SOCKS5H 未随协议切换 | 是 |
| 非 Gateway 资源未删除或修改 | 是；当前数量为 0 |
| `21000` 与 balancer 未恢复 | 是 |
| 无未决 `repair_required` | 是 |

## 10. 未决项

- XHTTP 空 `listen` 离线配置根因已经修复并通过生产 Xray `26.7.28 run -test`；尚未完成运行时热替换、外部连接与反向切回。
- 当前第一阻塞是 Gateway 更新后主连接 `connections=not_ready`。恢复 SSH 只读检查后必须先比较 Gateway 主快照与 AimiliVPN 主状态，并恢复四出口 TCP 基线；不得直接重试 XHTTP。
- Gateway SQLite 中保留两条早期失败请求的 `started` 操作记录，但当前协议状态、helper 请求队列和事务目录均已收敛；需要在后续代码修复中让失败幂等操作进入终态，避免长期悬挂。
- Stage 4–7 未执行，不能宣称混合协议最终状态、五分钟资源观察或 UI/v2rayN 四协议原路径已验收。
