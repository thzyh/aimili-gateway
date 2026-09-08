# 本机 VMware 真实部署闭环验证

日期：2026-09-08（Asia/Shanghai）
目标：在本机 VMware Ubuntu 中原生运行 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy，并完成 Windows 外部自动数据面验证与重启复验。

## 最终结果

- VM：`D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`，当前地址 `192.168.88.4`。
- 用户入口：`https://192.168.88.4:8080`。严格使用 Caddy 本地根 CA 验证返回 HTTP 200。Windows 的 `127.0.0.1` 不指向来宾 VM；Gateway 在 VM 内部才监听 `127.0.0.1:9080`。
- 四项服务 `aimilivpn`、`x-ui`、`aimili-gateway`、`caddy` 均为 active 且 enabled。
- 清单当前期望与实际均为 4 个 OpenVPN、1 个 Xray、4 个逻辑出口、3 个普通出口位。数量从 manifest 读取，没有固化为永久上限。
- VM 内最终门禁为 `nativeReady=true`：主连接和三个出口位的 TUN、策略路由、监听及真实出口均通过；数据库、订阅出口集合、协议隔离、Xray 运行时监听和宿主安全门均通过。
- Windows 外部最终结果为 `status=pass`、`ready_groups=4`、`verified_groups=4`：4/4 mixed/SOCKS5H、代理 DNS、来源认证和公网协议均通过，出口 IP 唯一且与各组预期一致；协议覆盖为 3 个 VLESS（含 TCP/Reality 与 XHTTP/Reality）和 1 个 Hysteria2。
- Hysteria2 使用 `allowInsecure=false`、专用 CA 和 `disableSystemRoot=true` 完成严格证书验证，没有降级为跳过校验。
- Windows 默认路由、DNS、防火墙、系统代理、系统证书信任库及 v2rayN 未被修改；外部门禁证据记录 `hostSafetyUnchanged=true`。

最终脱敏外部证据位于 `%LOCALAPPDATA%\AimiliGateway\vmware-local\verification\external-client.json`，采集时间为 `2026-09-08T08:42:59Z`。VM 内证据位于 `/var/lib/aimili-local/verification/native-evidence.json`。

## 重启闭环

1. 来宾系统通过 `systemctl poweroff` 正常关机，`vmrun list` 确认运行 VM 数为 0。
2. VMX 实际引用的 `AimiliGatewayLocal-disk1.vmdk` 在离线状态运行 VMware 磁盘检查，退出码为 0，未报告损坏。
3. VM 以 `vmrun start ... nogui` 启动，地址保持 `192.168.88.4`，固定 SSH 身份与 known_hosts 校验继续有效。
4. 四服务及单 Xray 先恢复；AimiliVPN 完成启动候选扫描后自动恢复 `tun0`、`tun120`、`tun121`、`tun122`。本次从开机到三个固定出口全部就绪约 4 分钟。
5. 重启后的 Windows 外部门禁再次完整通过，随后 VM 内门禁再次得到 `nativeReady=true`。

启动初期只看到 `tun0` 时不能判定失败，也不能把候选测速用的临时 `tun2...tun99` 计入固定出口。应等待候选维护完成，以 manifest、固定 TUN 和真实出口门禁共同判定。

## 本轮解决的阻塞

- VM 时间曾跳到未来，导致 Caddy 叶证书尚未生效。保留根 CA 后重新签发叶证书；旧叶证书可恢复备份位于 `/var/backups/aimili-local/caddy-leaf-reissue-20260908T0639Z`。
- 时间回拨使 AimiliVPN 定时器和 DNS 刷新状态异常；重启 AimiliVPN 后恢复。
- 免费 OpenVPN 出口对嵌套 HTTPS 探针会随机超时或重置。外部出口探针改用 HTTP；VLESS/Reality、XHTTP/Reality 和 Hysteria2 本身仍执行真实加密握手。只对没有返回合法 IP 的明确传输错误做一次有限重试，错误出口 IP不重试。
- 主连接或普通槽位自动漂移后，Gateway 记录可能短暂落后。正式外部验证在同一认证会话中只对 `fixed=true` 的运行组先执行 check，再重新读取连接材料。
- AimiliVPN 候选维护期间 check 会返回 `operation_busy` 或 `maintenance_busy`。验证器仅对这两个明确状态做最长 180 秒有界等待，其他 HTTP 错误立即失败。
- Caddy 信任链 fixture 已改为临时有效根证书、临时 CA bundle 和 no-op `update-ca-certificates`，测试不会修改 WSL 全局信任库。

## 最新测试

- AimiliVPN：`python -m unittest discover -s tests -v`，170 项通过。
- Gateway Go：`go test ./... -count=1` 通过；`go test ./... -race -count=1` 全包通过；`go vet ./...` 通过。
- Gateway 前端：65 项通过，生产构建通过。
- Gateway Python：105 项通过，2 项因 Windows 普通账户不可创建 symlink 按设计跳过。
- 本机 VM 套件：installer behavior/hardening、编排、部署入口、Gateway provision、外部验证入口、运行时与负向 fixture 全部通过。
- 两个 Go 发布二进制构建通过；两仓库 `git diff --check` 通过。
- 过度设计审查：`Lean already. Ship.`

本轮实现提交：Gateway `5e4d507`，AimiliVPN `50be9e7`。远程同步以最终普通 push 后的 fetch/SHA 对比为准。

## 用户验收边界

自动部署与数据面闭环已经完成。浏览器登录、订阅导入和 v2rayN 实际使用由用户执行，本轮未使用 Computer Use，也不声称这些用户步骤已完成。浏览器若尚未信任 Caddy 本地根 CA，会显示本地证书警告；自动 Hysteria2 验证使用临时 CA 文件，不会修改 Windows 系统信任库。
