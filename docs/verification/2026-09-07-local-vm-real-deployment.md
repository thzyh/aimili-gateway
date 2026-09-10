# 本机 VMware 真实部署闭环验证

日期：2026-09-09（Asia/Shanghai）
目标：在本机 VMware Ubuntu 中原生运行 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy，并完成 Windows 外部自动数据面验证与重启复验。

## 2026-09-10 启动管理器等待与托盘修复

- 本次右键运行启动管理器后，VM、Gateway 页面和节点数据面已经恢复，但菜单停留在“正在执行”。`startup-last-result.json` 的最新失败边界为 `ssh-readiness`：来宾启动初期第一次 SSH 连接超时，Windows PowerShell 5.1 在全局 `ErrorActionPreference=Stop` 下把 `ssh.exe` 的 stderr 转成终止错误，导致设计中的 180 秒重试没有执行。
- `Test-SshReadiness` 现局部按原生命令退出码判断预期的暂不可达状态，并恢复调用方错误策略；不可达 SSH fixture 已在 Windows PowerShell 5.1 中确认返回 `false` 而不是抛出终止错误。
- `vmware-tray.exe` 原先位于 SSH 与完整业务就绪检查之后，因此上述提前退出也跳过托盘启动。托盘现于 VM 启动/确认运行后立即在当前交互会话启动，不再依赖来宾 SSH 或业务探针；管理员子任务等待期间同步显示已等待秒数，完成后自动返回中文结果。
- 真实幂等 `-Action Start -AsJson` 返回 `success=true`、`vmStarted=false`、`nativeReady=true`、`trayRunning=true`，当前托盘位于用户 Session 1；三项 VMware 服务、VMnet8 持久 `/32` 路由、6 个 OpenVPN、1 个 Xray 和 6 个逻辑出口保持就绪。

## 2026-09-10 节点延迟分段诊断

- Windows 以 `192.168.88.1` 为源地址到 VM `192.168.88.4` 连续 12 次 ICMP 均小于 1 ms、0% 丢包，证明 VMware 本地链路不是截图中 `637–1036 ms` 的来源。
- VM 同期 load average 为 `0.01/0.03/0.00`，约 2 GB 内存中仍有 1472 MB 可用；没有 CPU 或内存饱和证据。
- v2rayN 当前 `SpeedPingTestUrl` 为 `https://www.google.com/generate_204`。从 VM 分别绑定 6 条固定 TUN 请求相同地址，TCP 建连为 `0.55–0.91 秒`，完整 TLS 至首字节为 `2.0–2.9 秒`；更换为 gstatic 与 Cloudflare 204 目标后 TCP 建连仍为 `0.49–1.27 秒`，排除单一测速网址导致的假高。
- 当前 6 条固定 OpenVPN 配置全部使用 TCP。实际链路为“Windows 客户端 → 本机 VM → Xray 入站 → TCP OpenVPN → VPNGate 免费海外出口 → 测速目标”；高延迟主要位于 OpenVPN 远端及其公网路径，TCP 套 TCP/加密握手会进一步放大排队和丢包恢复成本。
- 本轮没有为美化数字而修改测速 URL，也没有切换或激活 v2rayN 节点。真实优化应按端到端延迟轮换更健康的 VPNGate 候选，并在另行设计和验证后优先采用可用的 UDP OpenVPN 配置；若需要稳定低延迟，应使用质量可控的自有或付费上游，VMware 距离和扩容本机 CPU 对该瓶颈帮助有限。

## 2026-09-09 23:40 v2rayN `local` 路由冲突最终闭环

- 21:27–21:30 的最新 v2rayN 测试配置把 6 个 local 节点都指向 `192.168.88.4`，且没有 `sendThrough`。故障窗口内 Windows 首选动态 `192.168.88.4/32 → qinshi`，覆盖 VMnet8 直连网段；同一配置未绑定源地址时 6/6 超时，仅绑定 `192.168.88.1` 后 6/6 成功。第一失败边界因此是宿主到来宾的错误接口选路，不是订阅、TLS“不安全”、Reality、Hysteria2 或 Caddy。
- 用户以管理员身份执行 `repair-host-route.ps1 -Apply -AsJson` 后，活动和持久路由均为 `192.168.88.4/32 → VMware Network Adapter VMnet8`，VMnet8 IPv4 metric 为 5；没有删除 `qinshi` 路由、断开 VPN 或修改 v2rayN、DNS、防火墙、系统代理和默认路由。
- 最新 VM 内门禁为 `nativeReady=true`：四服务 active/enabled，主连接与出口位 0–4 的 TUN、策略路由、监听和真实出口全部通过，实际为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。
- 23:31 的 Windows 外部门禁在当前 TUN 存在且系统代理关闭时，通过独立临时 Xray 验证 6/6 节点的公网协议、mixed/SOCKS5H、代理 DNS、认证和出口 IP，结果 `status=pass`、`unique_exit_ips=true`；协议为 4 个 VLESS、2 个 Hysteria2。
- 另以逐节点临时 Xray HTTP 入站和仅限测试进程的 `HTTP_PROXY` 模拟“关闭 TUN＋自动配置系统代理”应用路径，6/6 节点均返回各自预期出口。测试没有操作用户 v2rayN；前后 v2rayN PID、TUN 接口、注册表系统代理与默认路由摘要一致，临时配置和测试进程均已清理。
- `status.ps1` 的恢复检查曾因 PowerShell 向原生命令 stdin 附加 CRLF，在 VM 端得到 `base64: invalid input`。修复后不再经 stdin 传输 Base64，并显式绑定 VMnet8 源地址；Windows PowerShell 5.1 与 pwsh 7.6 均已返回完整 `nativeReady=true`。
- 按用户要求，助手未在现有 v2rayN 中激活 `local`，未切换 TUN/系统代理，也未使用 Computer Use。服务器与隔离客户端已闭环，用户仍负责最后一次交互式选择节点体验验收。

## 2026-09-09 17:40 最终故障定位与恢复验证

- 主连接、出口 2 与 v2rayN 六节点同时失败的共同边界是 VM 数据面，不是订阅正文或 Reality/Hysteria2 参数。来宾内核日志在故障时段连续记录 `ens192` Link Down/Up，VMware 日志同时存在 Link State Propagation 事件；六条 OpenVPN 连接随之重置。VMX 已持久设置 `ethernet0.linkStatePropagation.enable = "FALSE"`，本次启动后 `ens192: NIC Link is Down` 计数为 0。
- 旧 OpenVPN 命令固定 `--connect-retry-max 1`，会把宿主侧短暂链路抖动放大为整条隧道退出。AimiliVPN 现通过 `OPENVPN_CONNECT_RETRY_MAX` 配置该值，默认 3、允许 1–10；VM 当前 OpenVPN 进程已使用 `--connect-retry-max 3`。对应全量测试 72 项通过。
- 本机部署比 ny VPS 多一层 VMware NAT：来宾 OpenVPN 在 Windows 宿主上的实际承载进程是 `vmnat.exe`。v2rayN TUN 若把 `vmnat.exe` 再送入 `local`，会形成“VMware NAT → local 节点 → VM 内 OpenVPN 上游 → VMware NAT”的递归依赖；ny VPS 没有这一层。v2rayN 三个路由模式现均在首条保存唯一的 `vmnat.exe → direct` 规则，运行环境数据库修改前备份保存在 `%LOCALAPPDATA%\AimiliGateway\vmware-local\backups\v2rayN-guiNDB-before-vmnat-direct.db`。
- 故障槽位恢复时还发现出口 2 原先固定的 `JP + residential` 当时没有可用候选，因此保持原国家、改用当前存在的 `JP + datacenter` 候选并完成真实出口检查；另一个假活候选也经受管 rotate 后通过。没有增加出口数量、改变固定端口或接管非 `agw-` 资源。
- `status.ps1 -AsJson` 于 17:35 后再次得到 `nativeReady=true`：四服务 active/enabled，实际 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位；主连接及出口位 0–4 的 TUN、策略路由、监听和真实出口全部为 true。
- `verify-external.ps1 -AsJson` 于 17:39 再次得到 `status=pass`、`ready_groups=6`、`verified_groups=6`、`unique_exit_ips=true`；6/6 mixed/SOCKS5H、代理 DNS、来源认证和公网协议全部通过，协议分布为 4 个 VLESS、2 个 Hysteria2。该验证会逐组调用与页面“重新检测并同步”相同的 `/api/v1/proxy-groups/{id}/check` 路径，因此同时证明同步操作已真实执行，而不是只检查静态端口。
- 最新外部门禁读取到来源限制当前为关闭；本轮没有改变该开关。此前“最终恢复开启”是较早快照，不代表 17:39 的当前配置。
- 本轮没有激活 v2rayN `local`，没有切换 TUN 或系统代理，也没有使用 Computer Use。用户最终验收前应正常重启一次 v2rayN，使新路由规则从持久数据库进入当前进程，然后自行激活 `local`，分别验证两种代理模式。

## 2026-09-09 节点池、来源限制与订阅复查

## 2026-09-09 15:00 故障快照（已由 17:40 恢复结果取代）

- v2rayN `local` 六节点测速 `-1` 的第一失败边界不是订阅编码或 TLS：VM 在复测期间发生了 OpenVPN 免费节点远端重置/超时，随后来宾 SSH 也短暂失联。AimiliVPN 日志明确记录 `connection-reset`、`server_poll timeout` 和 `ERR_OVPN_NODE_UNREACHABLE`；重启 VM 后 SSH 恢复。
- VM 重启后的槽位状态为：主连接、出口位 0、2、3 就绪；出口位 1、4 为 `pending`，明确原因分别为 `暂无可用住宅节点（JP）`、`暂无可用住宅节点（KR）`。随后主连接恢复且一个额外 OpenVPN 进程已启动，但两个槽位仍未就绪；最近一次门禁为 5 个 OpenVPN、4 个逻辑出口、3 个就绪出口位，不能把旧的 6 条客户端节点当作当前全部可用。
- Windows 外部验证在该状态下按事实失败于 `subscription_coverage_mismatch`；没有修改 v2rayN、没有激活 `local`、没有切换 TUN/系统代理。当前订阅仍保留此前生成的 6 条配置，用户在槽位恢复并刷新订阅前不应据此判断客户端核心故障。
- Gateway 主连接持久记录兜底修复已部署：AimiliVPN 重连或实时状态不可用时，池页面继续显示主连接并标记 degraded/`egress_unavailable`，不会整行消失；对应回归测试已通过。

- Gateway 已兼容旧版 AimiliVPN 国家目录响应：当上游只提供逐国家 `candidateCount` 时，从同一份当前有效候选快照补齐 `officialCandidateTotal`、`validNodeCount` 和 `validCountryCount`。部署后实时结果为官方 99、当前有效 40，国家数随当前候选刷新变化且不再显示 0；最终复核为 3 国。
- 三服务账户轮换后，Gateway 会在严格确认 mixed 入站仍属于 `agw-` 受管资源后自动同步代理账号；Xray 更新失败会恢复原 mixed 入站配置。来源限制已真实执行关闭、开启、再次关闭和最终恢复开启，所有返回均为 `applyStatus=applied`；最终只允许 `192.168.88.1/32`，3x-ui 受管资源检查为 `ownershipMatches=true`。
- AimiliVPN 重启后五个出口位可能自动换到仍可用候选；本轮重新 provision 后，Gateway 数据库、3x-ui/Xray 入站和六条订阅已重新同步。VM 内门禁再次得到 `nativeReady=true`，四服务 active/enabled、6 个 OpenVPN、1 个 Xray、6 个逻辑出口和 5 个普通出口位全部符合 manifest。
- OpenVPN 免费节点重连会重建 TUN，Linux 同时会删除绑定旧 TUN 的策略路由。AimiliVPN 现会在主连接、受管出口位和周期出口探测前检查并按需恢复对应路由。部署后清空出口位 5 的表 204 做现场故障注入，守护线程在 20 秒内自动恢复 `tun124` 的默认路由和选表规则，OpenVPN 进程与节点均未重启或替换；随后完整 VM 门禁仍为 `nativeReady=true`。
- 服务端严格使用 Caddy 本地根 CA 请求订阅得到 HTTPS 200、`text/plain`，Base64 解码出 6 条 `vless`/`hysteria2` 节点。v2rayN 在 `2026-09-09 09:41`、`09:49` 以及 `12:40` 的第一失败边界均为 TLS `PartialChain`，调用栈停在 `DownloadService`，尚未进入订阅正文解析；v2rayN 中 `local` 保存 URL 的 SHA-256 与 Gateway 当前生成 URL 完全一致。
- 已通过固定 SSH 身份取回并核对 Caddy 根 CA，将精确指纹 `46924F9DFCE0D6FD3C8FD52C12BDCA212C507B18` 导入 `Cert:\CurrentUser\Root`；没有修改 `LocalMachine\Root`。同一订阅随后用 Windows 系统信任直连得到 HTTP 200。v2rayN 7.24.4 自身的订阅任务于 13:04 后成功更新，`local` 从 0 个节点变为 6 个，日志只有 `Update subscription end`，没有新的 TLS 或解析错误。临时自动更新间隔已恢复为 0。

## 部署基线结果（历史通过快照；最新后续状态见上节）

- VM：`D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`，当前地址 `192.168.88.4`。
- 用户入口：`https://192.168.88.4:8080`。严格使用 Caddy 本地根 CA 验证返回 HTTP 200。Windows 的 `127.0.0.1` 不指向来宾 VM；Gateway 在 VM 内部才监听 `127.0.0.1:9080`。
- 四项服务 `aimilivpn`、`x-ui`、`aimili-gateway`、`caddy` 均为 active 且 enabled。
- 清单当前期望与实际均为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。数量从 manifest 读取，没有固化为永久上限，后续可以继续调整。
- VM 内最终门禁为 `nativeReady=true`：主连接和五个出口位的 TUN、策略路由、监听及真实出口均通过；数据库、订阅出口集合、协议隔离、Xray 运行时监听和宿主安全门均通过。
- Windows 外部最终结果为 `status=pass`、`ready_groups=6`、`verified_groups=6`：6/6 mixed/SOCKS5H、代理 DNS、来源认证和公网协议均通过，出口 IP 唯一且与各组预期一致；协议覆盖为 4 个 VLESS（含 TCP/Reality 与 XHTTP/Reality）和 2 个 Hysteria2。
- Hysteria2 使用 `allowInsecure=false`、专用 CA 和 `disableSystemRoot=true` 完成严格证书验证，没有降级为跳过校验。
- 初始外部门禁未修改 Windows 默认路由、DNS、防火墙、系统代理、证书信任库及 v2rayN，证据记录 `hostSafetyUnchanged=true`。为修复后续确认的 v2rayN `PartialChain`，仅新增当前用户 Caddy 根 CA 信任；默认路由、DNS、防火墙、系统代理和本机级根证书库仍未修改。

最终脱敏外部证据位于 `%LOCALAPPDATA%\AimiliGateway\vmware-local\verification\external-client.json`，最新采集时间为 `2026-09-09T17:39:38+08:00`。VM 内证据位于 `/var/lib/aimili-local/verification/native-evidence.json`。

## 重启闭环

1. 来宾系统通过 `systemctl poweroff` 正常关机，`vmrun list` 确认运行 VM 数为 0。
2. VMX 实际引用的 `AimiliGatewayLocal-disk1.vmdk` 在离线状态运行 VMware 磁盘检查，退出码为 0，未报告损坏。
3. VM 以 `vmrun start ... nogui` 启动，地址保持 `192.168.88.4`，固定 SSH 身份与 known_hosts 校验继续有效。
4. 四服务及单 Xray 先恢复；AimiliVPN 完成启动候选扫描后自动恢复主连接和五个固定出口位。出口位会优先使用仍然可用的上次节点；拨号失败候选会进入冷却，避免监督循环反复命中同一个坏节点。
5. 重启后的 Windows 外部门禁再次完整通过，随后 VM 内门禁再次得到 `nativeReady=true`。

启动初期只看到 `tun0` 时不能判定失败，也不能把候选测速用的临时 `tun2...tun99` 计入固定出口。应等待候选维护完成，以 manifest、固定 TUN 和真实出口门禁共同判定。

## 本轮解决的阻塞

- VM 时间曾跳到未来，导致 Caddy 叶证书尚未生效。保留根 CA 后重新签发叶证书；旧叶证书可恢复备份位于 `/var/backups/aimili-local/caddy-leaf-reissue-20260908T0639Z`。
- 时间回拨使 AimiliVPN 定时器和 DNS 刷新状态异常；重启 AimiliVPN 后恢复。
- 免费 OpenVPN 出口对嵌套 HTTPS 探针会随机超时或重置。外部出口探针改用 HTTP；VLESS/Reality、XHTTP/Reality 和 Hysteria2 本身仍执行真实加密握手。只对没有返回合法 IP 的明确传输错误做一次有限重试，错误出口 IP不重试。
- 主连接或普通槽位自动漂移后，Gateway 记录可能短暂落后。正式外部验证在同一认证会话中只对 `fixed=true` 的运行组先执行 check，再重新读取连接材料。
- AimiliVPN 候选维护期间 check 会返回 `operation_busy` 或 `maintenance_busy`。验证器仅对这两个明确状态做最长 180 秒有界等待，其他 HTTP 错误立即失败。
- Caddy 信任链 fixture 已改为临时有效根证书、临时 CA bundle 和 no-op `update-ca-certificates`，测试不会修改 WSL 全局信任库。
- Gateway 的单槽位真实出口检查最长约需 16 秒，旧的 8 秒 HTTP 读取超时会把仍在运行的检查误判为失败。`CheckSlot` 现使用 75 秒操作超时，回归测试覆盖旧失败和新成功路径。
- 外部门禁不再读取部署时的 bootstrap 凭据，而读取会随改密更新的当前统一账户文件，修复账户变更后的 401。
- 主连接与出口位 1 曾出现出口 IP 重复；仅轮换出口位 1 后恢复 6 个唯一出口，固定端口和其他出口不变。
- 订阅后端现输出 6 条可解析节点并保持 1–6 排序；3x-ui 的 `subSortIndex` 与逻辑出口一致。补齐当前用户 Caddy 根 CA 信任后，v2rayN 自身已成功写入 6 个 `local` 节点：2 个 Reality/raw、2 个 Reality/xhttp、2 个 Hysteria2/TLS，全部保持 `allowInsecure=false`。
- 高级设置中的 SOCKS5H 来源限制已达到 `enabled/applied`；3x-ui 与 AimiliVPN 原后台检测均为 HTTP 200，自动登录均返回同源 HTTP 303 跳转。
- 账户管理命令可用，状态显示统一账户已同步；生成、修改和修复账户的漂移保护已由自动测试覆盖。TOTP 当前关闭。
- 清理 35 个旧备份和 20 个 staging，只保留 `/var/backups/aimili-local/final-20260909-closed-loop`；根分区使用率由 49% 降至 23%，staging 为空。

## 最新测试

- AimiliVPN：`python -m unittest discover -s tests -v`，72 项通过。
- Gateway Go：`go test ./... -count=1` 通过；`go test ./... -race -count=1` 全包通过；`go vet ./...` 通过。
- Gateway 前端：66 项通过，生产构建通过。
- Gateway Python：108 项通过，2 项因 Windows 普通账户不可创建 symlink 按设计跳过。
- 本机 VM 套件：所有实际存在的 PowerShell、Bash 与 Gateway provision 测试均通过。历史总入口 `deploy/local-vm/tests/run.ps1` 仍引用从未被 Git 跟踪的 `create-vm.ps1`，所以不把该过时总入口本身表述为通过。
- 两仓库 `git diff --check` 通过。
- 过度设计审查：`Lean already. Ship.`

本轮实现提交包括 Gateway `bcf5e99` 和 AimiliVPN `12d0588`；前者修复池统计与 mixed 来源策略漂移，后者修复 TUN 重连后的策略路由自愈。本状态记录的后续文档提交不改变实现内容。

## 用户验收边界

自动部署、服务端数据面、页面同步接口和 v2rayN 订阅导入闭环已经完成，本轮未使用 Computer Use。v2rayN 自身已成功更新 `local` 并保存 6 个节点，三个路由模式均已持久加入 `vmnat.exe → direct`；用户仍负责重启 v2rayN 后选择节点的日常使用体验验收。当前用户根证书信任已经添加，浏览器刷新或重新建立连接后不应再因该根 CA 显示证书链警告；本机级根证书库未修改。
