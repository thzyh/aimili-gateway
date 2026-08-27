# Aimili Gateway V1-D 高级设置与统一凭据实施计划

> **供代理执行者使用：** 必须使用 `superpowers:executing-plans` 逐任务执行；每个功能或修复必须先使用 `superpowers:test-driven-development`。步骤以复选框跟踪。

**目标：** 交付重新设计的高级设置、可选 SOCKS5H 来源限制、Gateway 原生维护页、三服务统一凭据同步和不向浏览器暴露密码的原后台自动代登录。

**架构：** Gateway 仍是独立 Go 控制台，AimiliVPN 仍是独立 Python 服务，3x-ui/Xray 保持上游进程和数据库。Gateway 通过 AimiliVPN 版本化回环控制 API 与 3x-ui 已验证回环 HTTP 契约完成账户同步、维护摘要和会话桥接；本地 SQLite 只保存 Gateway 登录哈希、用途隔离的加密统一凭据、同步状态及来源策略。该方案是“统一凭据＋服务端自动代登录”，不是真正 SSO。

**技术栈：** Go 1.26、`net/http`、SQLite、Argon2id、AES-GCM/现有 `auth.Seal`、Python 3 标准库、Vue 3、TypeScript、Vitest、systemd、Caddy。

**设计依据：** `docs/superpowers/specs/2026-08-27-advanced-settings-unified-credentials-design.md`

## 全局约束

- 个人单管理员，不增加多租户、RBAC、OIDC、SAML 或 OAuth。
- 不重写 3x-ui、Xray 或 AimiliVPN 核心，不合并 Go 与 Python 进程，不删除原后台功能。
- 不直接读写 `x-ui.db`；3x-ui 更新只走已验证回环 API、CSRF 和 Cookie Jar。
- 不在 DOM、JavaScript、URL、浏览器存储、日志、审计或文档中输出密码、Cookie、UUID、私钥、随机后台路径或完整代理/订阅地址。
- 自动代登录失败时保留手动登录；失败不得影响 Xray、AimiliVPN 或现有代理数据面。
- `sudo aimili-gateway-account` 是统一用户名与密码的唯一受支持修改入口。
- 第一次升级后保留现有 Gateway 登录，但自动代登录为“等待统一重置”，直到用户执行一次统一随机或自定义密码重置。
- 3x-ui 自身 TOTP 必须关闭；Gateway TOTP 继续可选且不与底层服务同步。
- SOCKS5H 来源限制新安装默认开启；旧安装从现有 CIDR 迁移为开启；关闭后仍必须使用强随机用户名和密码。
- 当前 `ny` 为 512 MiB，生产 `maxProxyGroups=1`；V1-D 不提升在线容量。
- 每项完成结论必须附最新自动测试、构建或真实链路证据；局部 API 成功不能替代用户原路径验收。
- Gateway 与 AimiliVPN 仓库分别提交；不推送远端。

## 文件与责任边界

- `internal/store/migrations/006_unified_credentials.sql`：统一凭据同步状态、账户操作记录和显式 SOCKS5H 来源策略。
- `internal/store/accountsync.go`：账户同步状态和原子本地提交。
- `internal/store/mixedpolicy.go`：`enabled + cidrs + apply status` 的持久化接口。
- `internal/accountsync/coordinator.go`：三服务预检、更新、验证、逆序回滚、修复和漂移检查。
- `internal/backendlogin/service.go`：自动代登录编排和 Cookie 白名单输出，不负责 HTTP 响应。
- `internal/maintenance/service.go`：两个 Gateway 原生高级页的只读摘要与受管操作。
- `internal/adapters/aimili/client.go`：AimiliVPN 账户、会话和维护控制契约。
- `internal/adapters/xui/client.go`、`account.go`：3x-ui 能力探测、账户更新、登录会话桥接和受管摘要。
- `internal/orchestrator/mixedpolicy.go`：对所有 `agw-` 在线组应用来源策略并在失败时回滚。
- `internal/httpapi/settings_handlers.go`：来源策略与维护摘要 API。
- `internal/httpapi/backend_handlers.go`：两个固定目标的 POST 自动登录入口。
- `cmd/aimili-gateway-admin/account.go`：统一账户交互菜单，所有秘密只经隐藏终端输入。
- `web/src/views/SettingsView.vue`：紧凑高级设置首页。
- `web/src/views/AimiliSettingsView.vue`、`XUISettingsView.vue`：原生维护页。
- `web/src/components/PoolTable.vue`：简化的四类状态文案。
- `deploy/`：最小权限、迁移、回退与脱敏验收合同。

---

### 任务 1：Gateway 存储迁移与领域契约

**文件：**
- 新建：`internal/store/migrations/006_unified_credentials.sql`
- 新建：`internal/store/accountsync.go`
- 新建：`internal/store/mixedpolicy.go`
- 修改：`internal/store/store_test.go`
- 修改：`internal/store/proxygroups_test.go`

**接口：**
- 产出：`AccountSyncState{Status, UsernameFingerprint, LastCheckedAt, ErrorCode}`；状态只允许 `reset_required|synced|checking|repair_required|incompatible`。
- 产出：`UnifiedCredentialUpdate{ExpectedSecurityUpdatedAt, Username, PasswordHash, UsernameCiphertext, PasswordCiphertext, Status, UpdatedAt}`。
- 产出：`Store.CommitUnifiedCredentialsAndRevokeSessions(context.Context, UnifiedCredentialUpdate) error`。
- 产出：`MixedSourcePolicy{Enabled bool, CIDRs []netip.Prefix, ApplyStatus string, UpdatedAt time.Time}` 与 `GetMixedSourcePolicy`、`ReplaceMixedSourcePolicy`。

- [ ] **步骤 1：写迁移与存储失败测试**

在 `store_test.go` 写测试，断言旧数据库迁移后：现有 CIDR 得到 `enabled=true`，统一账户状态为 `reset_required`，加密凭据表中不存在明文字段；在 `proxygroups_test.go` 写 `enabled=false` 可保存空 CIDR、`enabled=true` 拒绝空列表和 `/0`、用户名及密码密文按不同 purpose 保存的测试。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/store -run 'Test(Unified|MixedSourcePolicy)' -count=1`

预期：因 006 迁移、类型和方法尚不存在而编译失败。

- [ ] **步骤 3：实现最小迁移和存储接口**

006 迁移创建 `account_sync_state` 单行表、`account_operations` 脱敏阶段表和 `mixed_source_policy` 单行表；用 `EXISTS(SELECT 1 FROM mixed_source_cidrs)` 初始化来源限制。`CommitUnifiedCredentialsAndRevokeSessions` 必须在一个 SQLite 事务中比较 `security_updated_at`、更新管理员哈希、写两个用途隔离密文、写同步状态并撤销 Gateway 会话。

- [ ] **步骤 4：运行存储包测试并检查迁移**

运行：`go test ./internal/store -count=1`

预期：全部通过，SQLite 中只出现密文和安全指纹，没有测试明文标记。

- [ ] **步骤 5：提交**

```text
git add internal/store
git commit -m "feat: persist unified account and mixed policy state"
```

### 任务 2：AimiliVPN 管理账户控制契约

**文件：**
- 修改：`D:/CodexProject/Github/aimili-vpngate/control_api.py`
- 修改：`D:/CodexProject/Github/aimili-vpngate/vpngate_manager.py`
- 修改：`D:/CodexProject/Github/aimili-vpngate/tests/test_control_api.py`
- 新建：`D:/CodexProject/Github/aimili-vpngate/tests/test_managed_account.py`

**接口：**
- 产出：能力 `admin.read`、`admin.update`、`admin.sessions.issue`。
- 产出：`GET /control/v1/admin` 返回 `{username, totpSupported:false}`，不返回密码和后台路径。
- 产出：`PUT /control/v1/admin` 接收封闭字段 `{username,password}`。
- 产出：`POST /control/v1/admin/sessions` 接收空对象，返回 `{cookieName,sessionToken,expiresAt}`，不返回路径。
- 产出：`managed_account_status()`、`update_managed_account(username,password)`、`issue_managed_ui_session()`。

- [ ] **步骤 1：写控制 API 和管理器失败测试**

覆盖未授权、未知字段、短密码、原子替换 `ui_auth.json`、旧 `active_sessions` 清空、新会话存在且短期有效、响应和捕获日志不含密码或 `secret_path`。

- [ ] **步骤 2：运行并确认失败**

运行：`python -m unittest tests.test_control_api tests.test_managed_account -v`

预期：新路由返回 404 或管理器方法不存在。

- [ ] **步骤 3：实现控制契约**

`update_managed_account` 只修改 `username/password`，保留端口与 `secret_path`，以同目录临时文件、`fsync`、`os.replace` 原子落盘，随后清空旧会话。`issue_managed_ui_session` 使用 `secrets.token_urlsafe(32)`，过期时间不超过 5 分钟。控制处理器继续限制 16 KiB、Bearer token、回环监听和封闭字段。

- [ ] **步骤 4：运行 AimiliVPN 全量测试**

运行：`python -m unittest discover -s tests -v`

预期：全部通过；输出不含测试密码与会话值。

- [ ] **步骤 5：在 AimiliVPN 仓库提交**

```text
git add control_api.py vpngate_manager.py tests
git commit -m "feat: expose managed admin control contract"
```

### 任务 3：Gateway AimiliVPN 适配器扩展

**文件：**
- 修改：`internal/adapters/aimili/client.go`
- 修改：`internal/adapters/aimili/client_test.go`

**接口：**
- 消费：任务 2 的三个控制端点。
- 产出：`AdminStatus(context.Context) (AdminStatus, error)`。
- 产出：`UpdateAdmin(context.Context, AdminUpdate) error`。
- 产出：`IssueAdminSession(context.Context) (AdminSession, error)`。
- `AdminSession` 只包含 `CookieName string`、`Token []byte`、`ExpiresAt time.Time`，并提供调用方清零 token 的所有权说明。

- [ ] **步骤 1：写适配器失败测试**

用 `httptest.Server` 断言 Bearer 认证、PUT JSON、封闭响应、16 KiB 限制、缺少能力和不合法 Cookie 名关闭失败；错误只暴露稳定 `AdapterError.Code`。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/adapters/aimili -run Admin -count=1`

预期：新类型和方法尚不存在而编译失败。

- [ ] **步骤 3：实现最小客户端方法**

复用现有 `do`，新增明确 DTO；只接受 Cookie 名 `session`，拒绝控制响应中的未知字段、路径字段和超长 token。

- [ ] **步骤 4：运行适配器全量测试**

运行：`go test ./internal/adapters/aimili -count=1`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add internal/adapters/aimili
git commit -m "feat: add AimiliVPN account adapter"
```

### 任务 4：3x-ui 账户更新与会话桥接适配器

**文件：**
- 新建：`internal/adapters/xui/account.go`
- 修改：`internal/adapters/xui/client.go`
- 修改：`internal/adapters/xui/models.go`
- 修改：`internal/adapters/xui/client_test.go`

**接口：**
- 产出：`AdminCapabilities{Version, CanUpdate, CanBridge, TOTPCompatible}`。
- 产出：`VerifyAdmin(context.Context, Credentials) error`。
- 产出：`UpdateAdmin(context.Context, current Credentials, next Credentials) error`，端点固定为 `/panel/api/setting/updateUser`。
- 产出：`IssueAdminSession(context.Context, Credentials) (BrowserSession, error)`；`BrowserSession` 只含白名单 Cookie 名和值，不含路径或重定向。

- [ ] **步骤 1：写 3x-ui 契约失败测试**

模拟 CSRF GET、登录 POST、账户更新、重新登录；分别覆盖旧密码错误、更新 409、未知版本、自身 TOTP、未知 Cookie、跨主机重定向和上游正文含秘密。断言适配器不透传正文且不调用数据库。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/adapters/xui -run 'Admin|Session' -count=1`

预期：新接口不存在而编译失败。

- [ ] **步骤 3：实现最小账户与 Cookie Jar 逻辑**

认证流程固定为取 CSRF、登录、再取 CSRF；账户更新只发送 `oldUsername`、`oldPassword`、`newUsername`、`newPassword` 和 CSRF。会话桥接只接受经测试确认的认证 Cookie 名，拒绝未知 `Domain`、TOTP挑战和非回环重定向。

- [ ] **步骤 4：运行 3x-ui 适配器全量测试**

运行：`go test ./internal/adapters/xui -count=1`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add internal/adapters/xui
git commit -m "feat: bridge 3x-ui accounts and sessions"
```

### 任务 5：统一账户协调器

**文件：**
- 新建：`internal/accountsync/coordinator.go`
- 新建：`internal/accountsync/coordinator_test.go`

**接口：**
- 消费：任务 1、3、4 的存储与两个适配器接口。
- 产出：`ChangeRequest{Username string, Password []byte}`。
- 产出：`Coordinator.Status`、`Check`、`Change`、`Repair`。
- 产出：稳定错误码 `service_unavailable|version_incompatible|account_drift|rollback_failed|repair_required`。

- [ ] **步骤 1：写表驱动失败测试**

测试预检无副作用、AimiliVPN 首步失败、3x-ui 第二步失败且 AimiliVPN 回滚、外部成功但本地事务失败时逆序回滚、回滚失败进入 `repair_required`、三方成功才提交并撤销 Gateway 会话、首次 `reset_required` 可用新凭据强制对齐。伪实现记录精确调用顺序，禁止把密码格式化进错误。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/accountsync -count=1`

预期：包尚不存在或测试编译失败。

- [ ] **步骤 3：实现协调器状态机**

全局互斥锁包住单次操作；先检查能力和旧凭据，再验证新凭据交集，按 AimiliVPN → 3x-ui → 本地事务更新，失败时 3x-ui → AimiliVPN 逆序回滚。`Repair` 必须接受新密码，不能查询旧密码。

- [ ] **步骤 4：运行竞态测试**

运行：`go test -race ./internal/accountsync -count=1`

预期：全部通过且无竞态。

- [ ] **步骤 5：提交**

```text
git add internal/accountsync
git commit -m "feat: coordinate unified account changes"
```

### 任务 6：统一账户管理命令与 systemd 限权

**文件：**
- 修改：`cmd/aimili-gateway-admin/account.go`
- 修改：`cmd/aimili-gateway-admin/account_test.go`
- 修改：`cmd/aimili-gateway-admin/main.go`
- 修改：`deploy/bin/aimili-gateway-account`
- 修改：`deploy/deploy_contract_test.go`

**接口：**
- 消费：`accountsync.Coordinator`。
- 产出：8 项中文菜单；查看只显示用户名、Gateway TOTP 和三个同步状态，不查询密码。
- systemd transient unit 只增加 `AF_INET AF_INET6`、`IPAddressAllow=localhost` 和 Aimili 控制 token credential。

- [ ] **步骤 1：写 CLI 与部署合同失败测试**

覆盖随机密码只显示一次、自定义密码隐藏且二次确认、用户名修改也要求统一密码、修复必须输入新密码、任何输出都不含传给伪适配器的旧秘密；部署合同断言 localhost 允许且公网仍拒绝。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./cmd/aimili-gateway-admin ./deploy -count=1`

预期：旧菜单和 `AF_UNIX` 合同不符合新断言。

- [ ] **步骤 3：接入协调器并修复 transient unit**

账户命令从 systemd credentials 读取主密钥和 Aimili token，从加密统一凭据加载 3x-ui 认证；密码不进入 CLI 参数或环境变量。为 transient unit 使用固定实例名冲突问题采用 `--unit=aimili-gateway-account-$(date +%s)-$$` 或不指定固定 unit，并保留 `--collect`。

- [ ] **步骤 4：运行命令和部署合同测试**

运行：`go test ./cmd/aimili-gateway-admin ./deploy -count=1`

预期：全部通过；静态合同不包含公网允许规则。

- [ ] **步骤 5：提交**

```text
git add cmd/aimili-gateway-admin deploy
git commit -m "feat: manage all service accounts from one command"
```

### 任务 7：删除近期重新认证业务门槛

**文件：**
- 修改：`internal/httpapi/server.go`
- 修改：`internal/httpapi/auth_handlers.go`
- 修改：`internal/httpapi/auth_handlers_test.go`
- 修改：`internal/httpapi/proxy_handlers.go`
- 修改：`internal/httpapi/proxy_handlers_test.go`
- 修改：`internal/store/sessions.go`
- 修改：`web/src/views/PoolView.vue`

**接口：**
- 继续保留：登录会话、CSRF、Origin、幂等键、`Cache-Control: no-store`。
- 删除：`POST /api/v1/auth/reauth` 和复制、导出、启用、换 IP、回收、设置保存的 `reauthentication_required` 分支。

- [ ] **步骤 1：先改测试为新行为并确认旧实现失败**

删除测试准备中的 reauth 调用，断言刚登录会话可直接执行敏感操作；断言 reauth 路由 404，同时跨源或无 CSRF 操作仍 403。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/httpapi -run 'Reauth|Proxy|Export|Mixed' -count=1`

预期：旧实现仍暴露 reauth 路由或要求 428。

- [ ] **步骤 3：删除业务门槛**

移除路由和 `requireRecentReauthentication` 调用；保留数据库历史字段以避免破坏旧迁移，但不再写入或作为授权条件。

- [ ] **步骤 4：运行 HTTP API 全量测试**

运行：`go test ./internal/httpapi -count=1`

预期：全部通过，CSRF 与同源负面测试仍通过。

- [ ] **步骤 5：提交**

```text
git add internal/httpapi internal/store/sessions.go web/src/views/PoolView.vue
git commit -m "refactor: remove recent reauthentication gate"
```

### 任务 8：可选 SOCKS5H 来源策略与全组回滚

**文件：**
- 新建：`internal/orchestrator/mixedpolicy.go`
- 新建：`internal/orchestrator/mixedpolicy_test.go`
- 修改：`internal/orchestrator/orchestrator.go`
- 修改：`internal/adapters/contracts.go`
- 修改：`internal/adapters/xui/models.go`
- 修改：`internal/httpapi/proxy_handlers.go`
- 修改：`internal/httpapi/proxy_handlers_test.go`

**接口：**
- 产出：`SetMixedPolicy(context.Context, store.MixedSourcePolicy) error`。
- HTTP：`GET /api/v1/settings/mixed-source-policy` 和 `PUT` 同路径，JSON 为 `{enabled,cidrs,applyStatus}`。

- [ ] **步骤 1：写协调器失败和回滚测试**

用两个 ready 组断言开启时生成白名单＋黑洞路由，关闭时移除这两类路由但保留 mixed 用户密码；第二组验证失败时第一组恢复旧指纹和旧策略。再写空 CIDR、`0.0.0.0/0`、`::/0` 和非规范前缀的 API 负面测试。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/orchestrator ./internal/httpapi -run 'Mixed.*Policy' -count=1`

预期：新接口不存在或旧 `SetMixedCIDRs` 不支持关闭。

- [ ] **步骤 3：实现策略计算、全组应用和回滚**

按稳定 ID 排序在线 `agw-` 组，记录旧模型与指纹，逐组调用 3x-ui upsert 并执行 SOCKS5H 代理 DNS 检测；失败后逆序恢复已修改组，只有全部验证成功才提交策略状态 `applied`。

- [ ] **步骤 4：运行相关包竞态测试**

运行：`go test -race ./internal/orchestrator ./internal/httpapi -count=1`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add internal/orchestrator internal/adapters internal/httpapi
git commit -m "feat: make SOCKS5H source restrictions optional"
```

### 任务 9：维护摘要、同步状态和自动代登录 HTTP 后端

**文件：**
- 新建：`internal/maintenance/service.go`
- 新建：`internal/maintenance/service_test.go`
- 新建：`internal/backendlogin/service.go`
- 新建：`internal/backendlogin/service_test.go`
- 新建：`internal/httpapi/settings_handlers.go`
- 新建：`internal/httpapi/backend_handlers.go`
- 新建：`internal/httpapi/backend_handlers_test.go`
- 修改：`internal/httpapi/server.go`

**接口：**
- HTTP：`GET /api/v1/settings/summary`、`GET /api/v1/settings/aimilivpn`、`POST /api/v1/settings/aimilivpn/refresh`、`POST /api/v1/settings/aimilivpn/check`。
- HTTP：`GET /api/v1/settings/3x-ui`、`POST /api/v1/settings/3x-ui/check`、`POST /api/v1/settings/3x-ui/repair`。
- HTTP：`POST /api/v1/backends/aimilivpn/login`、`POST /api/v1/backends/3x-ui/login`；不接受请求体，成功返回带固定 303 Location 的响应。

- [ ] **步骤 1：写服务和 HTTP 失败测试**

断言摘要只含批准字段；自动登录必须认证、同源、CSRF、POST、`no-store`，拒绝 GET、请求体和不同步状态。断言响应 Cookie 强制 `Secure; HttpOnly; SameSite=Strict` 且 Path 为服务端固定配置精确子路径；Location 只能是固定入口。捕获响应 JSON、HTML和日志，确认没有密码、Cookie 值或随机路径。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/maintenance ./internal/backendlogin ./internal/httpapi -run 'Maintenance|Backend|Settings' -count=1`

预期：包或路由尚不存在。

- [ ] **步骤 3：实现固定目标服务和处理器**

`backendlogin.Service` 只接受枚举目标，先执行 `accountsync.Check`，再调用对应会话适配器。HTTP 层从服务器配置读取路径，构造 `http.Cookie` 后立即清零临时 token；错误只映射为五类稳定码并提供手动登录布尔值，不返回上游正文。

- [ ] **步骤 4：运行 HTTP 和新服务竞态测试**

运行：`go test -race ./internal/maintenance ./internal/backendlogin ./internal/httpapi -count=1`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add internal/maintenance internal/backendlogin internal/httpapi
git commit -m "feat: add native maintenance and backend login APIs"
```

### 任务 10：重新设计高级设置前端

**文件：**
- 修改：`web/src/api/client.ts`
- 修改：`web/src/router/index.ts`
- 修改：`web/src/views/SettingsView.vue`
- 新建：`web/src/views/SettingsView.spec.ts`
- 新建：`web/src/views/AimiliSettingsView.vue`
- 新建：`web/src/views/AimiliSettingsView.spec.ts`
- 新建：`web/src/views/XUISettingsView.vue`
- 新建：`web/src/views/XUISettingsView.spec.ts`
- 修改：`web/src/App.vue`

**接口：**
- 消费：任务 8、9 的 JSON 契约。
- 产出：`/settings`、`/settings/aimilivpn`、`/settings/3x-ui` 三个路由；自动登录用同源 POST 后由浏览器跟随 303。

- [ ] **步骤 1：写组件失败测试**

断言不再出现“安全确认”、Gateway 密码或 TOTP；来源开关关闭时隐藏 CIDR 编辑器并显示风险说明；开启时要求 CIDR。断言两个原生页只显示设计批准字段，两个原后台按钮只调用固定 POST API，错误时显示手动登录回退且不自动重试。

- [ ] **步骤 2：运行并确认失败**

运行：`npm test -- --run web/src/views/SettingsView.spec.ts web/src/views/AimiliSettingsView.spec.ts web/src/views/XUISettingsView.spec.ts`

工作目录：`web`

预期：旧页面仍显示安全确认，新组件不存在。

- [ ] **步骤 3：实现紧凑响应式页面**

复用现有颜色、间距和按钮 token；桌面双列、窄屏单列。保存来源策略期间禁用控件并展示“正在应用”；只在服务端返回 `applied` 后显示“已生效”。自动入口不使用密码字段、iframe 或前端脚本填表。

- [ ] **步骤 4：运行前端测试与生产构建**

运行：`npm test -- --run && npm run build`

工作目录：`web`

预期：全部测试和 TypeScript/Vite 构建通过。

- [ ] **步骤 5：提交**

```text
git add web/src
git commit -m "feat: redesign advanced settings pages"
```

### 任务 11：代理池状态文案简化

**文件：**
- 修改：`web/src/components/PoolTable.vue`
- 修改：`web/src/components/PoolFilters.vue`
- 修改：`web/src/views/PoolViews.spec.ts`

**接口：**
- 面向用户只显示：`可选节点|已启用|处理中|故障`。
- 详情和数据属性仍保留精确后端状态；只有 `ready` 允许复制和导出。

- [ ] **步骤 1：写状态映射失败测试**

逐个断言 `standby → 可选节点`、`ready → 已启用`、三个过渡态 → `处理中`、`degraded/repair_required → 故障`；断言故障详情仍区分“链路检测失败”和“需要修复”。

- [ ] **步骤 2：运行并确认失败**

运行：`npm test -- --run web/src/views/PoolViews.spec.ts`

工作目录：`web`

预期：旧文案“待启用/可用/创建中/异常/需修复”导致失败。

- [ ] **步骤 3：实现集中状态映射**

在组件中使用一个只读映射函数供表格和筛选器复用；按钮可用性继续按精确状态判断，不把 `standby` 误当成可复制节点。

- [ ] **步骤 4：运行前端全量测试**

运行：`npm test -- --run && npm run build`

工作目录：`web`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add web/src/components web/src/views/PoolViews.spec.ts
git commit -m "refactor: simplify proxy pool status labels"
```

### 任务 12：应用装配、迁移和定时漂移检查

**文件：**
- 修改：`internal/app/app.go`
- 修改：`internal/app/app_test.go`
- 修改：`cmd/aimili-gateway/main.go`
- 修改：`cmd/aimili-gateway/main_test.go`
- 修改：`internal/config/config.go`
- 修改：`internal/config/config_test.go`
- 修改：`deploy/config/config.example.json`
- 修改：`deploy/systemd/aimili-gateway.service`

**接口：**
- 应用启动时完成无副作用能力探测；旧 `xui-automation.json` 只作一次迁移输入。
- 每 6 小时执行账户漂移检查；首次检查加入启动抖动，关闭应用时取消 context。

- [ ] **步骤 1：写装配失败测试**

覆盖旧凭据文件迁移为用途隔离密文但状态仍为 `reset_required`、缺少账户能力时代理协调器仍可启动、自动登录关闭、定时检查不阻塞启动、关闭时 goroutine 退出。断言 `maxProxyGroups` 配置不被本任务改为 2。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./internal/app ./cmd/aimili-gateway ./internal/config -count=1`

预期：新服务尚未装配或迁移断言失败。

- [ ] **步骤 3：完成依赖注入和兼容迁移**

将 store、主密钥和两个适配器注入 `accountsync`、`maintenance`、`backendlogin`；能力失败只记录脱敏状态。迁移成功前不删除旧文件，运行时不再直接从旧文件读取密码。

- [ ] **步骤 4：运行应用竞态测试和二进制构建**

运行：`go test -race ./internal/app ./cmd/aimili-gateway ./internal/config -count=1`

运行：`go build ./cmd/aimili-gateway ./cmd/aimili-gateway-admin`

预期：全部通过。

- [ ] **步骤 5：提交**

```text
git add internal/app internal/config cmd/aimili-gateway deploy/config deploy/systemd
git commit -m "feat: wire unified account services into Gateway"
```

### 任务 13：部署迁移、回退资产和验收合同

**文件：**
- 修改：`deploy/deploy_contract_test.go`
- 新建：`deploy/bin/aimili-gateway-v1d-preflight`
- 新建：`deploy/bin/aimili-gateway-v1d-verify`
- 修改：`deploy/caddy/AimiliGateway.Caddyfile`
- 修改：`README.md`
- 修改：`docs/verification/2026-08-26-ny-v1b.md`
- 修改：`docs/verification/2026-08-26-ny-v1c-online-pools.md`
- 新建：`docs/verification/2026-08-27-ny-v1d.md`

**接口：**
- 预检只读取版本、服务、备份目标和接口能力，不打印配置内容。
- 验证脚本只输出 PASS/FAIL、HTTP 状态和稳定错误码，不输出秘密或随机路径。

- [ ] **步骤 1：写部署合同失败测试**

断言脚本先备份 Gateway DB、主密钥引用、AimiliVPN UI 配置、3x-ui 配置和 Caddy；断言 Caddy 只代理固定精确子路径且不增加 Basic Auth；断言回退顺序恢复二进制、配置、数据库并逐服务启动，不删除非 `agw-` 资源。

- [ ] **步骤 2：运行并确认失败**

运行：`go test ./deploy -count=1`

预期：新脚本不存在或合同字符串缺失。

- [ ] **步骤 3：实现预检、验证和中文文档**

脚本使用 `set -euo pipefail`、固定绝对路径和 root-only 临时目录；不使用 `set -x`。历史验收文档保留 2026-08-26 当时事实，新增后续状态链接，不把后续完成写成历史时点已经完成。

- [ ] **步骤 4：运行部署合同和秘密扫描**

运行：`go test ./deploy -count=1`

运行：`rg -n '(password|cookie|uuid|secret_path).*(=|:).+' deploy docs/verification README.md`

预期：合同测试通过；扫描结果仅为字段名、脱敏说明或测试占位值，不含真实秘密。

- [ ] **步骤 5：提交**

```text
git add deploy README.md docs/verification
git commit -m "deploy: prepare V1-D migration and verification"
```

### 任务 14：本地总验证、VPS 阶梯部署与端到端验收

**文件：**
- 修改：`docs/verification/2026-08-27-ny-v1d.md`

**接口：**
- 消费：前 13 个任务的已提交产物。
- 产出：不含秘密的本地、服务器、浏览器和外部链路四层证据；任何未验证层必须明确写“尚未验证”。

- [ ] **步骤 1：执行本地总验证**

Gateway 仓库运行：

```text
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/aimili-gateway ./cmd/aimili-gateway-admin
```

`web` 运行：

```text
npm test -- --run
npm run build
```

AimiliVPN 仓库运行：`python -m unittest discover -s tests -v`。

预期：所有命令退出码 0；若竞态测试受 Windows 环境限制，必须记录具体限制并在 Linux VPS 补跑，不能写“通过”。

- [ ] **步骤 2：只读预检 VPS 和生成回退点**

通过 `ssh ny` 运行 V1-D preflight，核对四服务、512 MiB 内存与 swap、监听、已验证 3x-ui 版本、备份可创建空间和 `maxProxyGroups=1`。备份只记录文件存在、权限和校验摘要，不读取或输出内容。

- [ ] **步骤 3：阶梯部署 AimiliVPN 与 Gateway**

先部署 AimiliVPN 控制 API，重启后验证旧代理数据面和新能力；再部署 Gateway 二进制、Web 静态资源、迁移和 systemd/Caddy，逐个重启并在每一步检查 failed unit、内存、swap 和内核 OOM。任一门槛失败立即按任务 13 回退，不继续下一阶。

- [ ] **步骤 4：执行统一重置与用户原路径验收**

通过受限终端执行一次统一密码重置，输出只确认三个服务验证结果。随后验证 Gateway 登录、两个原生高级页、两个 POST 自动入口、手动登录回退、旧会话失效、来源限制关闭/开启、无需近期确认的复制/导出/启用/换 IP，以及单在线节点 VLESS、SOCKS5H、代理 DNS和出口一致性。

- [ ] **步骤 5：记录最新证据并提交**

验证文档按“本地自动测试 / 本地浏览器 / VPS 服务端 / 外部客户端”分节，记录时间、脱敏命令、结果和未完成项。

```text
git add docs/verification/2026-08-27-ny-v1d.md
git commit -m "docs: record V1-D end-to-end verification"
```

## 计划自检

- 设计第 3～17 节均映射到任务 1～13；自动测试和 VPS 验收映射到任务 14。
- 文件接口名称在前后任务一致：`AccountSyncState`、`MixedSourcePolicy`、`Coordinator`、`backendlogin.Service`。
- 计划不提升容量、不实现真正 SSO、不直接写 `x-ui.db`，也不把自动登录变成数据面依赖。
- 没有未定义的实现占位项；未知 3x-ui 版本或契约在任务 4 与任务 14 通过无副作用探测关闭失败。
- 用户已选择内联执行；计划提交后直接使用 `executing-plans`，不再询问执行方式。
