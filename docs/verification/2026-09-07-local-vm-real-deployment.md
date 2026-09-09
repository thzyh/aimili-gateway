# 本机 VMware 真实部署闭环验证

日期：2026-09-09（Asia/Shanghai）
目标：在本机 VMware Ubuntu 中原生运行 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy，并完成 Windows 外部自动数据面验证与重启复验。

## 2026-09-09 节点池、来源限制与订阅复查

- Gateway 已兼容旧版 AimiliVPN 国家目录响应：当上游只提供逐国家 `candidateCount` 时，从同一份当前有效候选快照补齐 `officialCandidateTotal`、`validNodeCount` 和 `validCountryCount`。部署后实时结果为官方 99、当前有效 40、5 国，不再显示 0。
- 三服务账户轮换后，Gateway 会在严格确认 mixed 入站仍属于 `agw-` 受管资源后自动同步代理账号；Xray 更新失败会恢复原 mixed 入站配置。来源限制已真实执行关闭、开启、再次关闭和最终恢复开启，所有返回均为 `applyStatus=applied`；最终只允许 `192.168.88.1/32`，3x-ui 受管资源检查为 `ownershipMatches=true`。
- AimiliVPN 重启后五个出口位可能自动换到仍可用候选；本轮重新 provision 后，Gateway 数据库、3x-ui/Xray 入站和六条订阅已重新同步。VM 内门禁再次得到 `nativeReady=true`，四服务 active/enabled、6 个 OpenVPN、1 个 Xray、6 个逻辑出口和 5 个普通出口位全部符合 manifest。
- 服务端严格使用 Caddy 本地根 CA 请求订阅得到 HTTPS 200、`text/plain`，解析出 6 条 `vless`/`hysteria2` 节点。v2rayN 在 `2026-09-09 09:41` 和 `09:49` 的最新日志第一失败边界是 `net_ssl_io_cert_chain_validation, PartialChain`，请求尚未进入订阅内容解析；这是 Windows 当前不信任本机 Caddy 根 CA，不是订阅文档无效。
- 本轮没有修改 Windows 系统证书信任库，也没有代替用户操作 v2rayN。Windows 外部自动门禁在 v2rayN/TUN 进程运行期间出现部分公网协议超时或响应不一致，因此该次结果不作为通过证据；VM 内真实 SOCKS5H、代理 DNS、公网协议检查和来源认证均已通过，最终 v2rayN 导入仍由用户在处理 CA 信任后验收。

## 最终结果

- VM：`D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`，当前地址 `192.168.88.4`。
- 用户入口：`https://192.168.88.4:8080`。严格使用 Caddy 本地根 CA 验证返回 HTTP 200。Windows 的 `127.0.0.1` 不指向来宾 VM；Gateway 在 VM 内部才监听 `127.0.0.1:9080`。
- 四项服务 `aimilivpn`、`x-ui`、`aimili-gateway`、`caddy` 均为 active 且 enabled。
- 清单当前期望与实际均为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。数量从 manifest 读取，没有固化为永久上限，后续可以继续调整。
- VM 内最终门禁为 `nativeReady=true`：主连接和五个出口位的 TUN、策略路由、监听及真实出口均通过；数据库、订阅出口集合、协议隔离、Xray 运行时监听和宿主安全门均通过。
- Windows 外部最终结果为 `status=pass`、`ready_groups=6`、`verified_groups=6`：6/6 mixed/SOCKS5H、代理 DNS、来源认证和公网协议均通过，出口 IP 唯一且与各组预期一致；协议覆盖为 4 个 VLESS（含 TCP/Reality 与 XHTTP/Reality）和 2 个 Hysteria2。
- Hysteria2 使用 `allowInsecure=false`、专用 CA 和 `disableSystemRoot=true` 完成严格证书验证，没有降级为跳过校验。
- Windows 默认路由、DNS、防火墙、系统代理、系统证书信任库及 v2rayN 未被修改；外部门禁证据记录 `hostSafetyUnchanged=true`。

最终脱敏外部证据位于 `%LOCALAPPDATA%\AimiliGateway\vmware-local\verification\external-client.json`，最新采集时间为 `2026-09-08T18:59:49Z`。VM 内证据位于 `/var/lib/aimili-local/verification/native-evidence.json`。

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
- 订阅后端现输出 6 条可解析节点并保持 1–6 排序；3x-ui 的 `subSortIndex` 与逻辑出口一致。此结论来自服务端与外部门禁解析/握手，不代替用户在 v2rayN 中的最终导入验收。
- 高级设置中的 SOCKS5H 来源限制已达到 `enabled/applied`；3x-ui 与 AimiliVPN 原后台检测均为 HTTP 200，自动登录均返回同源 HTTP 303 跳转。
- 账户管理命令可用，状态显示统一账户已同步；生成、修改和修复账户的漂移保护已由自动测试覆盖。TOTP 当前关闭。
- 清理 35 个旧备份和 20 个 staging，只保留 `/var/backups/aimili-local/final-20260909-closed-loop`；根分区使用率由 49% 降至 23%，staging 为空。

## 最新测试

- AimiliVPN：`python -m unittest discover -s tests -v`，69 项通过。
- Gateway Go：`go test ./... -count=1` 通过；`go test ./... -race -count=1` 全包通过；`go vet ./...` 通过。
- Gateway 前端：66 项通过，生产构建通过。
- Gateway Python：108 项通过，2 项因 Windows 普通账户不可创建 symlink 按设计跳过。
- 本机 VM 套件：所有实际存在的 PowerShell、Bash 与 Gateway provision 测试均通过。历史总入口 `deploy/local-vm/tests/run.ps1` 仍引用从未被 Git 跟踪的 `create-vm.ps1`，所以不把该过时总入口本身表述为通过。
- 两仓库 `git diff --check` 通过。
- 过度设计审查：`Lean already. Ship.`

本轮实现提交为 Gateway `c6bca94`、AimiliVPN `bbd277b`。两个功能分支均已普通推送，并在推送后通过 `git fetch` 复核本地实现提交与远端跟踪提交一致；本状态记录的后续文档提交不改变实现内容。

## 用户验收边界

自动部署与数据面闭环已经完成。浏览器登录、订阅导入和 v2rayN 实际使用由用户执行，本轮未使用 Computer Use，也不声称这些用户步骤已完成。服务端订阅已验证可解析且 6 个节点均完成真实协议握手，但 v2rayN 的“更新订阅”仍需用户按原操作路径复测。浏览器若尚未信任 Caddy 本地根 CA，会显示证书警告；自动 Hysteria2 验证使用临时 CA 文件，不会修改 Windows 系统信任库。
