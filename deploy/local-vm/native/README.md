# 本机原生部署资产

这些脚本在 VM 内执行，属于 Aimili Gateway 本机原生 systemd 部署的受限基础层。

- `deployment.json`：当前部署清单。数量是期望值，不是永久上限。
- `guest-network-preflight.sh`：只读验证路由、上游 TCP、DNS、HTTPS 和 UFW 出站。
- `stage.sh <manifest> <run-id>`：验证严格文件清单、SHA-256、常规文件和相对路径后，原子 promotion 到 `/var/lib/aimili-local/staging/<run-id>`。
- `backup.sh`：按 `aimilivpn`、`xui-caddy`、`gateway` 固定 allowlist 记录存在与缺失目标、类型、权限、owner/group、文件清单和 SHA-256；同一 run/component 仅允许一份备份。
- `rollback.sh`：只恢复备份 metadata 中且仍属于组件 allowlist 的精确目标；本轮新建目标会删除，既有目标保留至多一个 `.previous`，最后执行 `systemctl daemon-reload`。
- `verify-native.sh --json`：读取清单和脱敏验收证据，统一验证 systemd、protocol automation、TUN、路由、监听、真实出口、订阅覆盖、协议隔离与宿主不变性。

`verify-native.sh` 的 `--evidence` JSON 使用 `schemaVersion: 1`，只保存可公开比较的逻辑出口名（`main`、`slot-N`）、公网协议/端口、mixed 端口、订阅是否误含 mixed 的布尔值，以及宿主进程号列表和代理/默认路由的 SHA-256 摘要。不得写入订阅 URL、认证材料、UUID、节点 IP 或随机后台路径。验证结果只返回布尔判定和数量，不回显输入证据。

Windows 端入口为 `../deploy-native.ps1`。`-PlanOnly` 只输出脱敏阶段、清单期望数量和提交；正式执行固定使用 runtime 中的 SSH key/known_hosts，按 `aimilivpn → xui-caddy → gateway → slots → verify` 顺序运行，组件 apply 失败只回滚当前组件。`-ResumeFrom` 必须同时传入原 `-RunId`，并先检查上一个远端 checkpoint。

当 Windows 同时运行其他全隧道 VPN 时，v2rayN/sing-box 可能为来宾地址创建指向该 VPN 的临时 `/32` 路由，优先级高于 VMnet8 的直连网段，表现为 SSH、Gateway 页面和所有订阅节点同时超时。先运行 `../repair-host-route.ps1 -AsJson` 只读确认实际选路；确认冲突后，以管理员身份运行 `../repair-host-route.ps1 -Apply -AsJson`。该脚本只为 `state.json` 中的来宾地址在承载 `allowedSource` 的 VMware 适配器上建立持久 `/32` 路由并降低该适配器自身的 IPv4 metric；它不删除其他 VPN 路由，不断开 VPN，也不修改 v2rayN、系统代理、DNS、防火墙或默认路由。

Windows 重启后使用 `../start-local-vm.ps1` 作为统一手动入口。右键该 `.ps1` 并选择“使用 PowerShell 运行”，或者无参数执行脚本，会显示交互菜单：一键启动、只读状态、修复持久路由、退出。选择一键启动或路由修复时会请求一次 UAC；确认后依次启动 `VMAuthdService`、`VMnetDHCP` 和 `VMware NAT Service`，幂等恢复上述 VMnet8 持久路由，只在 `AimiliGatewayLocal` 尚未运行时执行 `vmrun start ... nogui`，随后等待固定 SSH 和完整 `nativeReady=true`。默认最多等待 SSH 180 秒、业务数据面 600 秒；免费 OpenVPN 节点恢复较慢时会继续有界等待，不把仅能 SSH 的状态误报为完成。

```powershell
& 'D:\CodexProject\Github\aimili-gateway\.worktrees\main-switch-protocol-modes\deploy\local-vm\start-local-vm.ps1'
```

不显示菜单、直接执行完整启动时使用：

```powershell
& 'D:\CodexProject\Github\aimili-gateway\.worktrees\main-switch-protocol-modes\deploy\local-vm\start-local-vm.ps1' -Action Start
```

只读复核当前状态、不启动或修改任何对象时使用：

```powershell
& 'D:\CodexProject\Github\aimili-gateway\.worktrees\main-switch-protocol-modes\deploy\local-vm\start-local-vm.ps1' -Action Status -AsJson
```

持久 `/32` 路由正常情况下会跨 Windows 重启保留，但统一入口仍会在每次启动时验证并按需恢复，防止其他 VPN 后续注入更具体路由。脚本不会启动、停止或切换 v2rayN，也不修改 TUN、系统代理、DNS、防火墙和默认路由。

管理员子进程的最终结果会写入 `%LOCALAPPDATA%\AimiliGateway\vmware-local\startup-last-result.json`。若启动失败，父窗口会显示准确阶段和原始错误，而不是只显示退出码 `1`。透明 TUN 的 `/1` 路由不再被误判为竞争主机路由；门禁会比较同一来宾 `/32` 的有效 metric，因此仍能识别并压过 `qinshi` 等后来注入的竞争 `/32`。

内部验证通过后，必须再运行 `../verify-external.ps1`。该入口显式连接 `aimili@<guest-ip>`，从清单动态读取逻辑出口数量，并使用 Windows 上的 Xray 对每个公网 VLESS/Reality、XHTTP/Reality 或 Hysteria2 入口做真实握手，同时从 Windows 对每个受认证 mixed SOCKS 执行代理 DNS 与出口 IP 验证。它不使用 `ssh ny`，不修改 v2rayN、系统代理、默认路由、DNS 或 Windows 防火墙；脱敏结果保存到 runtime 的 `verification/external-client.json`。`nativeReady=true` 只代表 VM 内部深度检查通过，不能替代该外部数据面门禁。

脚本不负责连接 ny，不安装 Docker，也不修改 Windows 默认路由、DNS、防火墙或 v2rayN。生产调用只使用固定路径、清单值和严格验证的 IP/整数；fixture 的 `AIMILI_STAGING_ROOT`、`AIMILI_BACKUP_ROOT`、`AIMILI_NATIVE_ROOT` 仅用于临时目录行为测试。
