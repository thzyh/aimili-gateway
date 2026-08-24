# Aimili Gateway

Aimili Gateway 是 AimiliVPN 与 3x-ui 的轻量统一控制台项目。统一控制台负责日常管理、统一登录和跨服务编排；AimiliVPN、3x-ui 与 Xray 继续作为独立服务运行、更新和排障。

## 当前阶段

项目设计与 V1-A 实施计划已经批准。当前分支正在实现 V1-A 可运行基础：个人单管理员、密码与 TOTP 登录、服务端会话、只读服务探测、统一状态页和独立的 3x-ui 专家模式入口。

V1-A 不修改 AimiliVPN 或 3x-ui 配置，不连接生产 VPS，也不包含 V1-B/V1-C 的管理写操作。

## 重要文件

- `docs/superpowers/specs/2026-08-24-unified-console-design.md`：统一控制台 V1 的需求、架构、认证、安全、适配器、兼容性和验收设计。
- `docs/superpowers/plans/2026-08-24-unified-console-v1a-foundation.md`：单管理员登录、只读探测、统一状态页和专家模式入口。
- `docs/superpowers/plans/2026-08-24-unified-console-v1b-aimili-management.md`：AimiliVPN 版本化控制 API、适配器和日常管理功能。
- `docs/superpowers/plans/2026-08-24-unified-console-v1c-3xui-binding.md`：3x-ui 管理、客户端操作和跨服务出口绑定。
- `cmd/aimili-gateway`：统一控制台服务进程。
- `cmd/aimili-gateway-admin`：本地管理员初始化和会话撤销命令。
- `web`：Vue 登录页和服务总览页。
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

首次初始化会输出一次 TOTP enrollment URI。不要把该输出写入仓库、日志或聊天记录。生产部署前，应复制 `deploy/config/config.example.json`，替换保留域名和大写路径占位符，并将配置文件权限限制到专用管理员可读。

## 验证

V1-A 的完整验收脚本位于 Task 9；日常修改至少完成以下检查：

1. `npm test --prefix web` 和 `npm run build --prefix web`。
2. `go test ./... -race`、`go vet ./...` 和两个 Go 二进制构建。
3. 统一控制台登录、3x-ui 专家模式登录和真正 SSO 的边界没有混淆。
4. V1 仍限制为个人单管理员，不包含多租户和复杂 RBAC。
5. 不输出或提交密码、Cookie、令牌、TOTP 秘钥、私钥、UUID、随机后台路径或完整订阅链接。
6. 未经明确授权，不连接或修改生产 VPS。

## 依赖与限制

- 后端使用 Go 1.26.x、SQLite 和标准 HTTP 接口；前端依赖版本固定在 `web/package-lock.json`。
- V1 不重写 AimiliVPN、3x-ui 或 Xray 核心，不把它们合并成单个进程。
- 原版 3x-ui 后台保留为专家模式，并继续使用 3x-ui 自身认证。
- 统一控制台的一套账户只覆盖 Gateway 页面。专家模式仍使用 3x-ui 登录；相同密码或反向代理保护都不是真正 SSO。
- 示例 systemd 单元只允许 Gateway 访问回环网络和自身数据目录，不授予任意命令、systemd、Caddy 或底层数据库修改权限。
- 部署资产尚未应用到生产环境。
