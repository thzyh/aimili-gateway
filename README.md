# Aimili Gateway

Aimili Gateway 是 AimiliVPN 与 3x-ui 的轻量统一控制台项目。统一控制台负责日常管理、统一登录和跨服务编排；AimiliVPN、3x-ui 与 Xray 继续作为独立服务运行、更新和排障。

## 当前阶段

2026-08-28 增量正在执行：AimiliVPN 和 Gateway 已增加按国家刷新、刷新状态轮询和当前筛选结果批量复制；Gateway 在新建代理组前保证实际出口 IP 唯一。3x-ui 升级目标固定为官方 `v3.7.0`，使用固定 amd64 资产大小与 SHA-256、整体备份和整体回滚。生产结果必须以本次最新验证记录为准，在 VPS 阶梯验收完成前不把本地通过描述为生产完成。

V1-C 低内存按需资源池已经完成服务器端验收：统一控制台主界面改为“VPN 节点池、SOCKS5H 代理池、高级设置”，移除旧服务状态卡和国家大卡片。Gateway 显示 AimiliVPN 当前有效候选，并在 512 MiB 生产环境中维持一个成对的 VLESS Reality、mixed/SOCKS5H 在线出口；只有双协议真实验证为 `ready` 的记录允许复制或导出。Windows 外部用户客户端应用层最终验收仍未完成。

V1-B 单代理组闭环已于 2026-08-26 完成真实 VPS 端到端验收：在 V1-A 个人单管理员、可选 TOTP、服务端会话和独立 3x-ui 专家模式基础上，增加国家候选目录、住宅/机房分类、AimiliVPN 槽位与 `agw-` Xray 资源编排、VLESS Reality、mixed/SOCKS5H、同类型换 IP、反向补偿和真实协议验证器。验收证据见 `docs/verification/2026-08-26-ny-v1b.md`。

V1-D 已按批准设计完成本地实现：删除近期安全确认，把 SOCKS5H 来源限制改为开关，增加 Gateway 原生 AimiliVPN/3x-ui 高级页面，并采用“统一凭据＋服务端自动代登录”。VPS 部署和真实端到端结果以 `docs/verification/2026-08-27-ny-v1d.md` 为准；未在该记录中标为通过的层级仍视为尚未验收。

Gateway 只管理 `agw-` 命名空间；不接管非受管 3x-ui/Xray 资源，也不直接写 `x-ui.db`。

## 重要文件

- `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md`：当前后续设计，定义国家代理目录、VLESS＋mixed 成对编排、容量、安全、统一高级设置和真实出口验收。
- `docs/superpowers/specs/2026-08-26-online-proxy-pools-design.md`：V1-C 正式设计，定义每候选出口实例、在线双协议资源池、紧凑前端和阶梯容量门槛。
- `docs/superpowers/specs/2026-08-27-advanced-settings-unified-credentials-design.md`：已批准的 V1-D 正式设计，定义高级设置、三账户同步、服务端自动代登录、SOCKS5H 来源开关和状态简化。
- `docs/superpowers/specs/2026-08-28-xui-upgrade-capacity-country-refresh-design.md`：3x-ui v3.7.0、按国家刷新、出口去重和 512 MiB 容量阶梯的批准设计。
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
- `web`：Vue 登录页、代理池操作、紧凑高级设置页和两个原生维护页。
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

本次 512 MiB 生产验证使用 `scripts/verify-capacity-step-remote.sh`：容量只按 1→2→3 提升，每级默认采样 15 分钟，触发低可用内存、Swap、服务重启、failed unit、OOM 或 SSH 探测门槛就回退；不尝试 4。3x-ui 固定升级使用 `scripts/upgrade-xui-v370-remote.sh`，必须先运行 `--preflight`，再运行 `--apply`，必要时用生成的固定备份目录执行 `--rollback`。

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
