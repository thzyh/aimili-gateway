# Aimili Gateway

Aimili Gateway 是 AimiliVPN 与 3x-ui 的轻量统一控制台项目。统一控制台负责日常管理、统一登录和跨服务编排；AimiliVPN、3x-ui 与 Xray 继续作为独立服务运行、更新和排障。

## 当前阶段

V1-C 在线资源池正在交付：统一控制台主界面改为“VPN 节点池、SOCKS5H 代理池、高级设置”，移除旧服务状态卡和国家大卡片。Gateway 会为 AimiliVPN 当前可用的每个候选出口建立独立槽位及成对的 VLESS Reality、mixed/SOCKS5H 资源；同一实际出口 IP 只保留协议综合延迟更优的实例。只有双协议真实验证为 `ready` 的记录允许复制或导出。

V1-B 单代理组闭环已于 2026-08-26 完成真实 VPS 端到端验收：在 V1-A 个人单管理员、可选 TOTP、服务端会话和独立 3x-ui 专家模式基础上，增加国家候选目录、住宅/机房分类、AimiliVPN 槽位与 `agw-` Xray 资源编排、VLESS Reality、mixed/SOCKS5H、同类型换 IP、反向补偿和真实协议验证器。验收证据见 `docs/verification/2026-08-26-ny-v1b.md`。

统一控制台页面可执行资源池筛选、异步刷新、检测、换 IP、mixed 来源 CIDR 设置，以及重新认证后的单条复制和筛选导出。Gateway 只管理 `agw-` 命名空间；不接管非受管 3x-ui/Xray 资源，也不直接写 `x-ui.db`。

## 重要文件

- `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md`：当前后续设计，定义国家代理目录、VLESS＋mixed 成对编排、容量、安全、统一高级设置和真实出口验收。
- `docs/superpowers/specs/2026-08-26-online-proxy-pools-design.md`：V1-C 正式设计，定义每候选出口实例、在线双协议资源池、紧凑前端和阶梯容量门槛。
- `docs/superpowers/plans/2026-08-26-online-proxy-pools-v1c.md`：V1-C 实施与真实 VPS 验收计划。
- `docs/superpowers/plans/2026-08-25-country-proxy-v1b-single-group.md`：V1-B 单代理组控制面、协议验证与部署验收计划。
- `docs/superpowers/specs/2026-08-24-unified-console-design.md`：V1-A 历史设计基线；其中未实施的旧 V1-B/V1-C 已被取代。
- `docs/superpowers/plans/2026-08-24-unified-console-v1a-foundation.md`：单管理员登录、只读探测、统一状态页和专家模式入口。
- `docs/superpowers/plans/2026-08-24-unified-console-v1b-aimili-management.md`：已废止的旧 AimiliVPN 管理计划，仅保留历史。
- `docs/superpowers/plans/2026-08-24-unified-console-v1c-3xui-binding.md`：已废止的旧 3x-ui 绑定计划，仅保留历史。
- `cmd/aimili-gateway`：统一控制台服务进程。
- `cmd/aimili-gateway-admin`：本地管理员初始化、账户安全管理和会话撤销命令。
- `web`：Vue 登录页、国家代理目录、代理组操作、安全确认和高级设置入口。
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

1. 查看当前用户名、TOTP 状态和安全信息更新时间。
2. 修改用户名。
3. 生成安全随机新密码。
4. 设置自定义新密码。
5. 启用或重新登记 TOTP。
6. 关闭 TOTP。
7. 撤销全部 Gateway 登录会话。

当前密码使用 Argon2id 单向哈希保存，无法查询或恢复明文，只能重置。随机新密码只在当前终端显示一次；自定义密码采用隐藏输入和二次确认。关闭 Gateway TOTP 后，统一控制台登录页不再显示动态验证码，但 3x-ui 专家模式仍使用 3x-ui 自己的账户和认证设置，两者不是 SSO。

## 验证

V1-C 本地验证入口为 `scripts/verify-online-pools-v1c.ps1`。生产配置可将 `maxProxyGroups` 设置到 `64`，但它只是代码上限；实际值必须从 1 开始逐级验证，不能把配置上限当作稳定在线数量。

V1-B 的本地验证入口为 `scripts/verify-country-proxy-v1b.ps1`；日常修改至少完成以下检查：

1. `npm test --prefix web` 和 `npm run build --prefix web`。
2. `go test ./... -race`、`go vet ./...` 和两个 Go 二进制构建。
3. 统一控制台登录、3x-ui 专家模式登录和真正 SSO 的边界没有混淆。
4. V1 仍限制为个人单管理员，不包含多租户和复杂 RBAC。
5. 不输出或提交密码、Cookie、令牌、TOTP 秘钥、私钥、UUID、随机后台路径或完整订阅链接。
6. 生产完成声明必须包含真实 VLESS、SOCKS5H 与代理端 DNS 路径证据；API 返回成功不能替代端到端验收。

## 依赖与限制

- 后端使用 Go 1.26.x、SQLite 和标准 HTTP 接口；前端依赖版本固定在 `web/package-lock.json`。
- V1 不重写 AimiliVPN、3x-ui 或 Xray 核心，不把它们合并成单个进程。
- Gateway 普通页面和由 Gateway 自己实现的 AimiliVPN、3x-ui 高级设置统一使用 Gateway 账户。
- 原版 3x-ui 后台保留为专家维护入口，并继续使用 3x-ui 自身认证；相同密码或反向代理保护都不是真正 SSO。
- 示例 systemd 单元只允许 Gateway 访问回环网络和自身数据目录，不授予任意命令、systemd、Caddy 或底层数据库修改权限。
- 部署资产已经在用户授权的 `ny` 测试部署中应用并验证；其他环境仍须单独取得授权、刷新基线并验证。
