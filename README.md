# Aimili Gateway

Aimili Gateway 是 Gateway 控制台与 Aimili 出口引擎的统一源码仓库。Gateway 负责日常管理和跨服务编排；出口引擎位于 `services/aimili-egress`，仍以独立的 `aimilivpn.service` 运行并承担 OpenVPN、TUN、策略路由和本地代理。Gateway 只通过回环控制 API 调用它，不获得高权限网络能力；3x-ui 与 Xray 也继续作为独立服务运行。

## 当前阶段

## 网页检测与确认更新

安装了项目更新入口的服务器，在“高级设置 → 检测更新”中从服务端查询最新正式 `vX.Y.Z-vps` 发布并验签。发现新版本后弹出确认框；选择“暂不更新”不会安装。确认后依次显示下载、复验、备份、安装、重启和身份核对进度，刷新页面会继续读取同一事务。

项目更新适用于统一目录布局，更新 Gateway/内嵌前端、内置出口引擎、已签名的定制 3x-ui 和随包辅助脚本；保留现有域名、Caddy 配置、账户、订阅及连接身份。它不运行首次部署初始化逻辑，也不改系统包、Xray 独立版本、端口布局或防火墙。更新会短暂中断服务及代理连接；安装失败时恢复升级前程序和数据库。需要至少 1 GiB 可用空间，备份与事务记录保存在 `/var/lib/aimili-gateway-project-update/transactions`。

首次安装会同时安装独立更新入口。已安装 `v0.2.13-vps` 的服务器可以通过网页安装一次兼容旧协议的签名过渡版；之后固定入口由引导器管理两个更新引擎槽。目标版本需要较新协议时，更新器先验签升级自身，再续办同一个更新事务。页面在预检失败时显示具体原因、受影响组件和[升级路径](docs/upgrade.md)，不会为了显示成功而忽略兼容性或验签。早于 `v0.2.13-vps`、尚无项目更新入口的服务器仍需按现有专用过渡流程安装入口；不能把首次部署脚本直接用于覆盖旧数据。设计与验证边界见[更新器 v2 设计](docs/superpowers/specs/2026-09-25-project-updater-v2.md)。

## VPS 一条命令部署

在 Ubuntu 24.04、x86_64 的空白 VPS 上以 root 执行：

```bash
curl -fsSL https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/install.sh | sudo bash
```

安装器会用中文询问无域名或域名模式，并可重复执行。无域名模式使用本机公网 IP 与本地 CA；域名 A 记录生效后，再执行同一条命令并选择域名模式，保留 Gateway/3x-ui 数据库、账户、UUID、Reality 材料与出口位。非交互用法是在同一条命令末尾附加 `--no-domain --slots 4` 或 `--domain bj.zouyunhui.cc.cd --slots 4`。发布资产由固定的 Ed25519 公钥验证签名和 SHA-256；完整安装记录在 `/var/log/aimili-gateway/install.log`。只读状态检查使用同一入口：`curl -fsSL https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/install.sh | sudo bash -s -- --status`。

3x-ui 保持独立上游源码。`deploy/vps/3x-ui-v3.7.0-alias.patch` 固定在上游提交 `f727d04f6522bb94a8fb52e8352fdcafb51c11e1`，通过 `deploy/vps/build-xui-custom.ps1` 可复现构建。安装器从固定的 `v0.2.5-vps` Release 下载定制 3x-ui 二进制，或复用本机已校验的缓存，并按当前版本签名清单中的 SHA-256 验证；Gateway 安装包来自安装脚本固定的当前 Release。Gateway 仓库只保存补丁、版本锁与发布构建，不把 3x-ui 的整个源码并入 Go module；上游许可证随官方 3x-ui 运行包保留。

`freesub` 备用功能已从 Gateway 前端、API、运行时和订阅生成中移除；迁移 013 清理旧业务表，部署升级前请先执行数据库备份。旧版本的历史文档保留原时间点事实。

自 2026-09-11 起，`services/aimili-egress` 是出口引擎的唯一源码来源。独立的 `aimili-vpngate` GitHub 仓库已删除，历史保留在本地 Git bundle；不再与本目录双向开发。Gateway 前端只保留 Gateway 页面、高级设置和 3x-ui 专家模式，不再注册 AimiliVPN 设置页或原后台登录入口。融合边界与验证结果见 `docs/verification/2026-09-11-egress-isolation-and-repository-integration.md`。

当前权威入口是 `docs/handoffs/2026-09-05-current-state-handoff.md`。它记录实际功能工作树路径、最新本地提交、生产摘要、回滚资产和仍未实现的发布机制；历史设计、计划和验证文档保留各自时间点的事实，不再承担“当前状态”职责。

截至 2026-09-09，本机 VMware Ubuntu 的 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy 原生部署已经完成内外门禁、重启复验及 v2rayN 订阅导入；用户入口为 `https://192.168.88.4:8080`。当前 manifest 使用 6 个 OpenVPN、1 个 Xray 和 6 个逻辑出口，但数量不是永久上限。v2rayN 的 `local` 已通过当前用户 Caddy 根 CA 信任成功更新为 6 个节点；用户仍负责选择节点后的实际使用体验验收。完整证据见 `docs/verification/2026-09-07-local-vm-real-deployment.md`。

ny VPS 使用统一仓库布局：Gateway 仍作为独立低权限服务运行，出口引擎从 `/opt/aimili-gateway/services/aimili-egress` 启动，持久数据统一放在 `/var/lib/aimili-gateway/aimili-egress`。纯 UI 外部静态资源免重启发布、回退与恢复均已通过生产验证，内嵌 UI 仍作为安全兜底；具体版本和当前出口状态以交接与验证记录为准，不能用本机 VM 结果替代生产验收。

外部 UI Stage A 使用 `scripts/build-ui-release.ps1` 生成 `manifest.json`、`manifest.sig` 和 `ui.tar.gz`，由离线 `aimili-gateway-update-install` 校验并原子切换 `current`/`previous`。首次启用需要随 Gateway 二进制部署并只重启 Gateway；启用后日常签名 UI 发布和回退不重启 Gateway，也不触碰 AimiliVPN、x-ui/Xray 或 Caddy。

旧的单组件 UI 发布机制见 `docs/superpowers/specs/2026-09-05-zero-downtime-ui-and-safe-self-update-design.md`。现在的整项目发布由低权限 fetcher 下载，无网络 root installer 复验签名及 SHA256，并在停服前检查平台、布局、数据库和组件动作。当前受限执行器可更新 Gateway、aimili-egress、3x-ui 及辅助脚本；需改变 Caddy、Xray、OpenVPN 或 systemd 运行配置的版本须先发布签名引擎过渡版并增加该组件的预检、快照与恢复执行器。未满足条件时网页会给出阻止原因和升级路径。

后端更新的逐文件实施计划是 `docs/superpowers/plans/2026-09-05-safe-gateway-self-update.md`，当前验证入口是 `docs/verification/2026-09-05-safe-gateway-self-update.md`。网页更新必须具有已验证的发布来源与可用版本，未配置时保持禁用。普通 UI 发布只切换静态资源；后端发布会短暂重启 Gateway 控制台，代理数据面需通过部署前后检查。

以下段落是项目历史演进，用于理解设计来源，不代表当前生产快照。

2026-08-29 最新生产基线已经完成：Gateway 使用 3x-ui 原生多入站订阅客户端，将主连接 `8443` 和三个受管 VLESS 入站组成一个订阅；导入兼容客户端后得到四个独立 VLESS 节点，mixed/SOCKS5H 不进入该订阅。旧 `21000` 聚合入站、客户端引用、balancer 和 observatory 已安全清理，原聚合接口固定返回弃用错误，不会重新创建历史入口。

V1-C 低内存按需资源池继续运行在 512 MiB VPS：三个受管出口位分别提供 VLESS 与 mixed/SOCKS5H，AimiliVPN 主连接通过 `8443` 和 `agw-main-mixed` 作为第 4 个出口。页面提供固定出口位、候选替换、按国家刷新、真实延迟检测和“复制 VLESS 订阅”；只有真实协议验证为 `ready` 的记录允许复制或导出。最新生产证据见 `docs/verification/2026-08-29-test-style-subscription.md`。

V1-B 单代理组闭环已于 2026-08-26 完成真实 VPS 端到端验收：在 V1-A 个人单管理员、可选 TOTP、服务端会话和独立 3x-ui 专家模式基础上，增加国家候选目录、住宅/机房分类、AimiliVPN 槽位与 `agw-` Xray 资源编排、VLESS Reality、mixed/SOCKS5H、同类型换 IP、反向补偿和真实协议验证器。验收证据见 `docs/verification/2026-08-26-ny-v1b.md`。

V1-D 已按批准设计完成本地实现：删除近期安全确认，把 SOCKS5H 来源限制改为开关，增加 Gateway 原生 AimiliVPN/3x-ui 高级页面，并采用“统一凭据＋服务端自动代登录”。VPS 部署和真实端到端结果以 `docs/verification/2026-08-27-ny-v1d.md` 为准；未在该记录中标为通过的层级仍视为尚未验收。

Gateway 只管理 `agw-` 命名空间；不接管非受管 3x-ui/Xray 资源，也不直接写 `x-ui.db`。

## 重要文件

- `docs/handoffs/2026-09-05-current-state-handoff.md`：当前本地路径、Git、生产摘要、最新修复与未完成事项。
- `AGENTS.md`：项目指导与变更分级，规定 UI、Gateway 和运行时修改的开发、验证及生产边界。
- `docs/superpowers/specs/2026-09-05-zero-downtime-ui-and-safe-self-update-design.md`：已批准的外部 UI 原子切换、内嵌兜底与后端一键安全升级设计。
- `docs/superpowers/plans/2026-09-05-safe-gateway-self-update.md`：后端安全更新逐文件实施计划。
- `docs/verification/2026-09-05-safe-gateway-self-update.md`：本轮更新器实现、Linux 验证、部署与剩余限制。
- `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md`：当前后续设计，定义国家代理目录、VLESS＋mixed 成对编排、容量、安全、统一高级设置和真实出口验收。
- `docs/superpowers/specs/2026-08-26-online-proxy-pools-design.md`：V1-C 正式设计，定义每候选出口实例、在线双协议资源池、紧凑前端和阶梯容量门槛。
- `docs/superpowers/specs/2026-08-27-advanced-settings-unified-credentials-design.md`：已批准的 V1-D 正式设计，定义高级设置、三账户同步、服务端自动代登录、SOCKS5H 来源开关和状态简化。
- `docs/superpowers/specs/2026-08-28-xui-upgrade-capacity-country-refresh-design.md`：3x-ui v3.7.0、按国家刷新、出口去重和 512 MiB 容量阶梯的批准设计。
- `docs/superpowers/specs/2026-08-29-test-style-subscription-design.md`：已实现的 3x-ui 原生多入站 VLESS 订阅、主连接检测和旧聚合迁移设计。
- `docs/superpowers/specs/2026-08-29-main-switch-protocol-modes-design.md`：已批准的主连接两阶段事务、每出口单协议模式与混合协议订阅增量设计。
- `docs/superpowers/specs/2026-09-01-egress-ux-and-dynamic-subscription-alias-design.md`：真实出口 IP、中文反馈、主身份同步和逐关联动态订阅别名设计。
- `docs/superpowers/plans/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`：三仓库 TDD、回滚故障注入和阶梯部署计划。
- `docs/verification/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`：本增量的脱敏本地、部署和最终客户端验收记录。
- `docs/superpowers/plans/2026-08-29-main-switch-protocol-modes.md`：本增量的 TDD、本地故障注入和 VPS 阶梯实施计划。
- `docs/runbooks/main-switch-protocol-modes.md`：本增量的备份、回滚、证书、UDP 边界与 `repair_required` 运维手册。
- `docs/verification/2026-08-29-main-switch-protocol-modes.md`：本增量的脱敏本地与生产验收记录。
- `docs/superpowers/plans/2026-08-29-test-style-subscription.md`：Test 风格订阅的实施与生产迁移计划。
- `docs/verification/2026-08-29-test-style-subscription.md`：四节点订阅、双协议、历史清理和低内存生产验收记录。
- `docs/superpowers/plans/2026-08-28-xui-upgrade-capacity-country-refresh.md`：本次增量的实施与生产验收计划。
- `docs/superpowers/plans/2026-08-27-advanced-settings-unified-credentials-v1d.md`：V1-D 实施、迁移、回退和真实 VPS 验收计划。
- `docs/superpowers/plans/2026-08-26-online-proxy-pools-v1c.md`：V1-C 实施与真实 VPS 验收计划。
- `docs/superpowers/plans/2026-08-25-country-proxy-v1b-single-group.md`：V1-B 单代理组控制面、协议验证与部署验收计划。
- `docs/superpowers/specs/2026-08-24-unified-console-design.md`：V1-A 历史设计基线；其中未实施的旧 V1-B/V1-C 已被取代。
- `docs/superpowers/plans/2026-08-24-unified-console-v1a-foundation.md`：单管理员登录、只读探测、统一状态页和专家模式入口。
- `docs/superpowers/plans/2026-08-24-unified-console-v1b-aimili-management.md`：已废止的旧 AimiliVPN 管理计划，仅保留历史。
- `docs/superpowers/plans/2026-08-24-unified-console-v1c-3xui-binding.md`：已废止的旧 3x-ui 绑定计划，仅保留历史。
- `cmd/aimili-gateway`：统一控制台服务进程。
- `cmd/aimili-gateway-admin`：本地管理员初始化、账户安全管理和会话撤销命令。
- `services/aimili-egress`：独立运行的 Aimili 出口引擎源码、测试和自检脚本。
- `web`：Vue 登录页、代理池操作、紧凑高级设置页和 3x-ui 专家维护页。
- `deploy`：示例配置、systemd 单元和待合并的 Caddy 路由片段。

## 使用方法

本地开发构建：

```powershell
npm ci --prefix web
npm test --prefix web
npm run build --prefix web
go test ./... -race
go build ./cmd/aimili-gateway
go build ./cmd/aimili-gateway-admin
```

`npm run build --prefix web` 会先生成 Git 忽略的 `internal/webassets/dist`；干净检出后必须先执行该步骤，Go 才能嵌入生产前端。

正式 Gateway 发布包使用仓库外的 Ed25519 私钥生成；私钥不得进入 Git、GitHub Release 或 VPS：

```powershell
.\scripts\build-gateway-release.ps1 -Version v1.2.3 -PrivateKey C:\secure\aimili-gateway-release.key -OutputDirectory C:\release\v1.2.3
gh release create v1.2.3 C:\release\v1.2.3\manifest.json C:\release\v1.2.3\manifest.sig C:\release\v1.2.3\aimili-gateway --title "Aimili Gateway v1.2.3" --notes "本版本更新说明"
```

上述单组件更新页只识别非草稿、非预发布且同时具有 `manifest.json`、`manifest.sig`、`aimili-gateway` 三项资产的正式版本。新版整项目入口使用 `deploy/vps/build-release.ps1` 生成完整包，不能将两种发布格式混用。

本地管理员只能通过交互式命令初始化。密码和 TOTP 秘钥不能通过命令行参数传入：

```powershell
$env:GATEWAY_CONFIG = "C:\path\to\local-config.json"
go run ./cmd/aimili-gateway-admin init
go run ./cmd/aimili-gateway
```

首次初始化默认只启用密码登录。需要 TOTP 时，通过服务器本地账户管理命令登记。不要把密码或 TOTP enrollment URI 写入仓库、日志或聊天记录。生产部署前，应复制 `deploy/config/config.example.json`，替换保留域名和大写路径占位符，并将配置文件权限限制到专用管理员可读。

### Gateway 账户管理

服务器安装后统一使用：

```bash
sudo aimili-gateway-account
```

中文菜单提供以下功能：

1. 查看当前用户名、Gateway TOTP 状态和三个服务的同步状态。
2. 统一修改用户名。
3. 生成并应用安全随机新密码。
4. 设置并应用自定义新密码。
5. 启用或重新登记 Gateway TOTP。
6. 关闭 Gateway TOTP。
7. 使用新密码修复三服务账户同步。
8. 撤销全部 Gateway 登录会话。
0. 退出。

V1-D 中该命令是三服务统一用户名和密码的唯一受支持修改入口。密码不能查询或恢复，只能生成随机新密码或设置自定义新密码；首次升级后必须执行一次统一密码重置，自动代登录才会从“等待统一重置”进入可用状态。修改统一用户名或密码、或修复三账户同步成功后，命令会自动退出并立即重载 Gateway，避免运行中的适配器继续使用旧凭据；无需再次选择“退出”。Gateway TOTP 仍是独立的可选第二因素，不同步到底层后台。

## 验证

本增量的本地总验证入口为 `scripts/verify-egress-ux-aliases.ps1`。它从固定 3x-ui v3.7.0 提交应用可复现补丁，验证逐客户端别名隔离、Gateway 三类跨仓事务、前端、Go race/vet/双二进制和 AimiliVPN 全套测试；`-IntegrationOnly` 只运行脱敏 fixture 和定向事务。脚本只使用工作区 `.tmp` 缓存，不修改系统 Go 或全局 Git 配置。

当前 Test 风格订阅生产验证使用 `scripts/verify-test-style-subscription.py`：核对订阅只包含 `8443`、`20000–20002`，逐条启动临时 Xray 客户端验证公网出口，并复测主 VLESS 与 SOCKS5H。`scripts/deploy-test-style-cleanup-remote.sh` 在迁移前备份 Gateway 二进制、Gateway 数据库、x-ui 数据库和 Xray 运行时配置，失败时整体恢复。

512 MiB 容量阶梯验证使用 `scripts/verify-capacity-step-remote.sh`：容量只按 1→2→3 提升，每级默认采样 15 分钟，触发低可用内存、Swap、服务重启、failed unit、OOM 或 SSH 探测门槛就回退。3x-ui 固定升级使用 `scripts/upgrade-xui-v370-remote.sh`，必须先运行 `--preflight`，再运行 `--apply`，必要时用生成的固定备份目录执行 `--rollback`。

生产容量修改使用 `scripts/set-capacity-safe-remote.py`：只接受 1、2、3，修改前以 0700 目录备份 Gateway 配置和数据库，原子写回并保持原 owner/mode。`scripts/verify-external-client-v1c.py` 会逐个验证 ready 组；来源限制开启时，分别核对公网 mixed 端口、授权回环 SOCKS5H/代理 DNS和公网 VLESS，不会把未授权来源被黑洞规则拒绝误报为代理失效。

V1-C 既有本地验证入口仍为 `scripts/verify-online-pools-v1c.ps1`。配置中的代码上限不等于稳定在线数量，生产实际值必须由上述阶梯采样决定。

V1-B 的本地验证入口为 `scripts/verify-country-proxy-v1b.ps1`；日常修改至少完成以下检查：

1. `npm test --prefix web` 和 `npm run build --prefix web`。
2. `go test ./... -race`、`go vet ./...` 和两个 Go 二进制构建。
3. 统一凭据、服务端自动代登录和真正 SSO 的边界没有混淆。
4. V1 仍限制为个人单管理员，不包含多租户和复杂 RBAC。
5. 不输出或提交密码、Cookie、令牌、TOTP 秘钥、私钥、UUID、随机后台路径或完整订阅链接。
6. 生产完成声明必须包含真实 VLESS、SOCKS5H 与代理端 DNS 路径证据；API 返回成功不能替代端到端验收。

## 依赖与限制

- 后端使用 Go 1.26.x、SQLite 和标准 HTTP 接口；前端依赖版本固定在 `web/package-lock.json`。
- V1 不重写 AimiliVPN、3x-ui 或 Xray 核心，不把它们合并成单个进程。
- Gateway 普通页面和由 Gateway 自己实现的 AimiliVPN、3x-ui 高级设置统一使用 Gateway 账户。
- V1-D 让三服务使用相同用户名和密码，并由 Gateway 服务端代登录原后台；三个后台仍签发独立会话，因此相同密码、反向代理保护或自动代登录都不是真正 SSO。
- 示例 systemd 单元只允许 Gateway 访问回环网络和自身数据目录，不授予任意命令、systemd、Caddy 或底层数据库修改权限。
- 部署资产已经在用户授权的 `ny` 测试部署中应用并验证；其他环境仍须单独取得授权、刷新基线并验证。
