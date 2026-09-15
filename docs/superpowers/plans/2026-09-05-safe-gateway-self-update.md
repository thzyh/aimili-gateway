# Aimili Gateway 后端安全自更新 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在外部 UI 机制稳定后，为 Gateway 增加固定来源低权限下载、无网络 root 离线安装、`control-plane-only` 后端升级、唯一 previous 自动回滚和统一网页操作入口。

**Architecture:** Gateway 只向 root 不可写的 request spool 提交闭集版本请求；低权限 fetcher 从固定 HTTPS 来源下载并初验；root installer 不联网、不信任 fetcher，复制到 root staging 后独立复验并执行 dry-run 或短暂停止/替换/启动 Gateway。最终状态由 root 原子写入，Gateway 重启后按 run ID 继续读取，页面退避轮询且只展示中文脱敏状态。

**Tech Stack:** Go 1.26、Ed25519、systemd path/oneshot、Vue 3/Vitest、SQLite 只读检查、Linux 原子 rename。

**Spec:** `docs/superpowers/specs/2026-09-05-zero-downtime-ui-and-safe-self-update-design.md`

## Global Constraints

- 后端 Release 固定为 Gateway 二进制、`manifest.json`、`manifest.sig` 三个独立资产。
- 第一版只接受 `schemaVersion: 1`、`kind: gateway`、`platform: linux-amd64`、`apiVersion: v1`、`impactClass: control-plane-only`。
- Gateway 主进程保持非 root、无 capability、无任意 URL/路径/命令输入权限。
- fetcher 只能访问配置中的固定 HTTPS origin 和重定向 host 闭集；root installer 使用 `IPAddressDeny=any` 且完全禁止联网。
- root installer 不信任 fetcher 的 staging、文件元数据或成功布尔值，必须重新打开普通文件并复验签名、摘要、属主、链接数、大小和真实路径。
- 数据库迁移、协议、端口、Caddy、AimiliVPN、x-ui/Xray、systemd 权限扩大必须返回 `manual_staged_deploy_required`。
- 后端升级只保留唯一 previous 二进制，不复制数据库、配置、证书或 x-ui 资产。
- updater 不得重启 AimiliVPN、x-ui/Xray 或 Caddy；现有代理数据面必须通过前后不变门证明持续。
- UI 与后端更新共享全局 lease；Gateway 重启后不得自动重放未确认事务。
- 最终状态闭集为 `pending`、`downloading`、`validating`、`switching`、`verifying`、`rolled_back`、`success`、`failed`、`repair_required`。
- 生产先启用 dry-run，只在同版本拒绝/无更新路径稳定后开放真实替换。

---

### Task 1: Gateway manifest、版本命令和只读兼容检查

**Files:**
- Modify: `internal/releaseverify/manifest.go`
- Modify: `internal/releaseverify/manifest_test.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/buildinfo/buildinfo_test.go`
- Modify: `cmd/aimili-gateway/main.go`
- Create: `cmd/aimili-gateway/main_test.go`
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Consumes: 链接变量 `buildinfo.Version`、`buildinfo.Commit`、`buildinfo.BuiltAt`；现有配置与 SQLite 文件。
- Produces: `VerifyGateway(...) (GatewayManifest, error)`；CLI `version --json`、`config validate`、`database check-compatible --min N --max N`；`Store.SchemaVersion(ctx) (int, error)`。

- [ ] **Step 1: 写 manifest 与 CLI RED 测试**

```go
func TestVerifyGatewayAcceptsControlPlaneOnlyLinuxAMD64(t *testing.T) { /* 有效签名/摘要/兼容区间 */ }
func TestVerifyGatewayRejectsRuntimeImpact(t *testing.T) { /* impactClass=runtime => manual_staged_deploy_required */ }
func TestRunVersionJSON(t *testing.T) { /* 输出只含 version/commit/builtAt/apiVersion/platform */ }
func TestDatabaseCompatibilityCheckIsReadOnly(t *testing.T) { /* 文件摘要和 mtime 不变 */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/releaseverify ./internal/buildinfo ./cmd/aimili-gateway ./internal/store -run 'Gateway|Version|Compatibility|SchemaVersion' -count=1`

Expected: FAIL，接口不存在。

- [ ] **Step 3: 实现严格 Gateway manifest**

```go
type GatewayManifest struct {
    SchemaVersion int `json:"schemaVersion"`
    Kind, Version, Commit, BuiltAt, Platform, APIVersion, ImpactClass string
    Binary File `json:"binary"`
    MinDatabaseSchema, MaxDatabaseSchema int
    UICompatibility *struct { Min, Max string } `json:"uiCompatibility,omitempty"`
}
```

拒绝未知字段、尾随 JSON、非单调版本、错误平台、影响等级和 schema 范围。版本比较固定使用 `vMAJOR.MINOR.PATCH`，不引入第三方 semver 依赖。

- [ ] **Step 4: 实现无副作用 CLI**

把 `main()` 调整为 `os.Exit(run(os.Args[1:], ...))`；无参数保持现有服务启动。三个检查子命令不得启动 HTTP、执行迁移、写数据库或创建运行时凭据。

- [ ] **Step 5: 运行受影响包测试并提交**

Run: `go test ./internal/releaseverify ./internal/buildinfo ./cmd/aimili-gateway ./internal/store -race -count=1`

```bash
git add internal/releaseverify internal/buildinfo cmd/aimili-gateway internal/store/store.go internal/store/store_test.go
git commit -m "feat: validate gateway update manifests"
```

### Task 2: 原子 updater 状态、lease 和 Gateway 请求客户端

**Files:**
- Create: `internal/updatetxn/model.go`
- Create: `internal/updatetxn/files.go`
- Create: `internal/updatetxn/files_test.go`
- Create: `internal/updatetxn/client.go`
- Create: `internal/updatetxn/client_test.go`

**Interfaces:**
- Consumes: Gateway 可写 requests、root 可写 results、显式版本 ID 和 kind。
- Produces: `Request{RunID, Kind, Version, Action, DryRun}`；`Result{RunID, Kind, Version, State, ErrorCode, StartedAt, FinishedAt}`；`Client.Submit(context.Context, Request) (Result, error)`、`Client.Get(runID string) (Result, error)`。

- [ ] **Step 1: 写闭集、幂等和权限 RED 测试**

```go
func TestSubmitAcceptsOnlySignedVersionIdentifier(t *testing.T) { /* URL、路径、空白和大写拒绝 */ }
func TestSubmitSameRunIsIdempotent(t *testing.T) { /* 同请求返回现有；不同请求冲突 */ }
func TestReadResultRejectsSymlinkAndWrongOwnerMetadata(t *testing.T) { /* 不跟随链接 */ }
func TestLeaseRejectsConcurrentUIAndGatewayTransactions(t *testing.T) { /* 全局 lease */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/updatetxn -count=1`

Expected: FAIL，包不存在。

- [ ] **Step 3: 实现原子文件协议**

```go
type Client struct { RequestDir, ResultDir string; Now func() time.Time }
func (c *Client) Submit(ctx context.Context, request Request) (Result, error)
func (c *Client) Get(ctx context.Context, runID string) (Result, error)
```

请求先写同目录 0600 临时普通文件、`fsync`、rename；结果读取限制 32 KiB、禁止链接和未知字段。run ID 为 32 字节随机小写十六进制，不包含秘密。

- [ ] **Step 4: 运行竞态测试并提交**

Run: `go test ./internal/updatetxn -race -count=1`

```bash
git add internal/updatetxn
git commit -m "feat: add durable gateway update transactions"
```

### Task 3: 固定来源低权限 fetcher

**Files:**
- Create: `internal/updatefetch/config.go`
- Create: `internal/updatefetch/fetcher.go`
- Create: `internal/updatefetch/fetcher_test.go`
- Create: `cmd/aimili-gateway-update-fetch/main.go`
- Create: `cmd/aimili-gateway-update-fetch/main_test.go`
- Create: `deploy/config/updater.example.json`

**Interfaces:**
- Consumes: root 提供的只读 updater 配置、Gateway request、可选 systemd credential。
- Produces: `Fetcher.Fetch(ctx, request) (PreflightResult, error)`；只写 `/var/lib/aimili-gateway-update/staging/<runID>` 和 fetch 状态。

- [ ] **Step 1: 写来源、重定向和空间门 RED 测试**

```go
func TestFetcherUsesOnlyConfiguredOrigin(t *testing.T) { /* 请求不能覆盖 URL/channel */ }
func TestFetcherRejectsRedirectOutsideAllowlist(t *testing.T) { /* 302 到其他 host => blocked_redirect */ }
func TestFetcherRequiresHTTPSAndExactAssetNames(t *testing.T) { /* http、query、额外文件拒绝 */ }
func TestFetcherChecksFreeSpaceBeforeBodyDownload(t *testing.T) { /* 三倍包+128MiB 不足 => disk_full */ }
func TestFetcherNeverLogsCredentialOrCompleteURL(t *testing.T) { /* 日志仅 host/channel/version */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/updatefetch ./cmd/aimili-gateway-update-fetch -count=1`

Expected: FAIL，包不存在。

- [ ] **Step 3: 实现固定来源 fetcher**

```go
type Config struct { ManifestOrigin string; RedirectHosts, Channels []string; PublicKeyFile string; StagingRoot string; MaxAssetBytes int64 }
type Fetcher struct { Config Config; Client *http.Client; Verify func(...) error }
func (f *Fetcher) Fetch(ctx context.Context, request updatetxn.Request) (PreflightResult, error)
```

HTTP client 禁止代理继承，TLS 最低 1.3，重定向逐跳检查 host；凭据只从显式 credential 文件读取为 header。下载用 `O_EXCL|O_NOFOLLOW`，限制 body 大小并在初验后原子写 marker。

- [ ] **Step 4: 运行测试并提交**

Run: `go test ./internal/updatefetch ./cmd/aimili-gateway-update-fetch -race -count=1`

```bash
git add internal/updatefetch cmd/aimili-gateway-update-fetch deploy/config/updater.example.json
git commit -m "feat: fetch signed gateway releases safely"
```

### Task 4: 无网络 root dry-run 与后端原子替换

**Files:**
- Create: `internal/gatewayupdate/installer.go`
- Create: `internal/gatewayupdate/installer_test.go`
- Modify: `cmd/aimili-gateway-update-install/main.go`
- Modify: `cmd/aimili-gateway-update-install/main_test.go`

**Interfaces:**
- Consumes: fetcher staging、签名公钥、当前二进制、Gateway config/DB、systemd runner、健康探针与 Stage 0 不变门。
- Produces: `gatewayupdate.DryRun(ctx, Config) Result`、`gatewayupdate.Install(ctx, Config) Result`、`gatewayupdate.Rollback(ctx, Config) Result`；CLI `gateway-dry-run`、`gateway-install`、`gateway-rollback`、`spool`。

- [ ] **Step 1: 写停止前失败和 dry-run 零写 RED 测试**

```go
func TestDryRunPerformsNoProductionWrites(t *testing.T) { /* 当前二进制、DB、config、service runner 均不变 */ }
func TestInstallRejectsBusyProtocolTransaction(t *testing.T) { /* 非终态协议/来源策略 => operation_busy */ }
func TestInstallRejectsMigrationOrRuntimeImpact(t *testing.T) { /* manual_staged_deploy_required */ }
func TestInstallRejectsUntrustedStagingMetadata(t *testing.T) { /* symlink、nlink>1、错误 owner/size */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/gatewayupdate ./cmd/aimili-gateway-update-install -run 'DryRun|Install|Rollback' -count=1`

Expected: FAIL，新 package 和子命令不存在。

- [ ] **Step 3: 实现离线验证和影子检查**

```go
type Runner interface { Stop(context.Context, string) error; Start(context.Context, string) error; IsActive(context.Context, string) (bool, error) }
type Config struct { StagingDir, BinaryPath, PreviousPath, ConfigPath, DatabasePath, PublicKeyFile string; Runner Runner; Probe Probe }
```

复制到同文件系统 root-only staging 后再校验；新二进制依次运行 `version --json`、`config validate`、`database check-compatible`，均设置超时、空网络和只读路径。

- [ ] **Step 4: 写替换后失败与回滚 RED 测试**

```go
func TestInstallStartFailureRestoresPrevious(t *testing.T) { /* stop→rename→start失败→restore→start */ }
func TestInstallHealthFailureRestoresPrevious(t *testing.T) { /* 健康门失败后同样回滚 */ }
func TestRollbackFailurePreservesBothBinariesAndRepairState(t *testing.T) { /* repair_required，不清理 */ }
func TestSuccessfulInstallKeepsExactlyOnePrevious(t *testing.T) { /* 不创建 DB/config/cert 备份 */ }
func TestInstallNeverRestartsDataPlaneServices(t *testing.T) { /* runner 调用中只有 aimili-gateway.service */ }
```

- [ ] **Step 5: 实现替换、健康门与自动回滚**

事务顺序固定为 preflight→唯一 previous→stop Gateway→原子 binary rename→start Gateway→health/data-plane invariant；任一步失败执行相反操作。结果先写临时 JSON 再 rename，回滚失败保留新旧二进制和 staging。

- [ ] **Step 6: 运行竞态与故障注入测试并提交**

Run: `go test ./internal/gatewayupdate ./cmd/aimili-gateway-update-install -race -count=1`

```bash
git add internal/gatewayupdate cmd/aimili-gateway-update-install
git commit -m "feat: update gateway control plane with rollback"
```

### Task 5: systemd 权限隔离和部署契约

**Files:**
- Create: `deploy/systemd/aimili-gateway-update-fetch.path`
- Create: `deploy/systemd/aimili-gateway-update-fetch.service`
- Create: `deploy/systemd/aimili-gateway-update-install.path`
- Create: `deploy/systemd/aimili-gateway-update-install.service`
- Create: `deploy/bin/aimili-gateway-update-rollback`
- Create: `scripts/deploy-gateway-updater-remote.sh`
- Modify: `deploy/systemd/aimili-gateway.service`
- Modify: `deploy/deploy_contract_test.go`

**Interfaces:**
- Consumes: `/var/lib/aimili-gateway/update-spool/requests`、fetch staging、updater.json、公钥和可选下载 credential。
- Produces: 两层 path/oneshot 服务、无网页 root 回滚入口、可重入安装脚本。

- [ ] **Step 1: 写 systemd capability RED 测试**

断言 fetcher：专用无登录用户、仅 staging 可写、`RestrictAddressFamilies=AF_INET AF_INET6`、固定 credential、无 root。断言 installer：root、`IPAddressDeny=any`、仅二进制/previous/results/UI 根可写、不得写 DB/config/cert，不含 shell/curl/wget，不得调用其他三项服务。

- [ ] **Step 2: 运行 RED**

Run: `go test ./deploy -run 'Update|Updater|Rollback' -count=1`

Expected: FAIL，unit 和脚本不存在。

- [ ] **Step 3: 创建最小权限 units 和回滚入口**

installer service 固定执行：

```ini
ExecStart=/usr/local/bin/aimili-gateway-update-install spool --config /etc/aimili-gateway/updater.json
IPAddressDeny=any
RestrictAddressFamilies=AF_UNIX
ReadOnlyPaths=/etc/aimili-gateway /var/lib/aimili-gateway/aimili-gateway.db
ReadWritePaths=/usr/local/bin/aimili-gateway /var/lib/aimili-gateway-update /var/lib/aimili-gateway/update-spool/results
```

回滚 wrapper 只允许固定 `gateway-rollback`/`ui-rollback`，不接收路径或命令。

- [ ] **Step 4: 实现幂等部署脚本**

脚本先 dry-run，再安装用户/目录/二进制/config/unit；`daemon-reload` 后先启用 path，再用“同版本拒绝或无更新”请求验证。任何失败精确恢复 unit、二进制和目录权限，不重启数据面。

- [ ] **Step 5: 运行部署契约并提交**

Run: `go test ./deploy -race -count=1`

```bash
git add deploy/systemd deploy/bin/aimili-gateway-update-rollback scripts/deploy-gateway-updater-remote.sh deploy/deploy_contract_test.go
git commit -m "feat: isolate gateway updater privileges"
```

### Task 6: 更新 API、重新认证和脱敏状态

**Files:**
- Create: `internal/httpapi/update_handlers.go`
- Create: `internal/httpapi/update_handlers_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/middleware.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `updatetxn.Client`、当前 build info、管理员 session/CSRF 与重新认证密码。
- Produces: 设计中的六个 `/api/v1/system/updates` endpoint；响应不含内部路径、URL、body、签名或英文错误详情。

- [ ] **Step 1: 写授权和输入闭集 RED 测试**

```go
func TestUpdateGETRequiresAuthenticatedAdmin(t *testing.T) { /* 401 */ }
func TestUpdatePOSTRequiresCSRFAndFreshPassword(t *testing.T) { /* 无重认证 => 403 */ }
func TestUpdateApplyRejectsURLPathOrUnknownVersion(t *testing.T) { /* 400 */ }
func TestUpdateStatusRedactsInternalFields(t *testing.T) { /* JSON 不含 origin/path/detail */ }
func TestUpdateApplyIsIdempotentByRunID(t *testing.T) { /* 重复返回同结果 */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/httpapi -run 'Update' -count=1`

Expected: FAIL，路由和 handler 不存在。

- [ ] **Step 3: 实现 API 和依赖注入**

```go
type UpdateManager interface {
    List(context.Context) (UpdateSummary, error)
    Submit(context.Context, UpdateRequest) (UpdateResult, error)
    Get(context.Context, string) (UpdateResult, error)
}
```

POST body 只接受 `password`、`runId`；路径 version 必须先出现在签名可用版本列表。重新认证复用现有 Argon2 校验，不记录密码。

- [ ] **Step 4: 运行 httpapi/config/app 测试并提交**

Run: `go test ./internal/httpapi ./internal/config ./internal/app -race -count=1`

```bash
git add internal/httpapi internal/app/app.go internal/config/config.go internal/config/config_test.go
git commit -m "feat: expose safe gateway update api"
```

### Task 7: 高级设置更新页面和可恢复轮询

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/views/SettingsView.vue`
- Modify: `web/src/views/SettingsView.spec.ts`
- Modify: `web/src/components/errorMessages.ts`

**Interfaces:**
- Consumes: updates API、`UiNotice`、现有管理员会话。
- Produces: 当前/可用 UI 与 Gateway 版本、重新认证弹层、apply/rollback 按钮、退避轮询和中文通知。

- [ ] **Step 1: 写 UI RED 测试**

```ts
it('distinguishes no-restart UI updates from control-plane restart', async () => { /* 两类影响文案 */ })
it('requires password reauthentication before an update POST', async () => { /* body 只含 password/runId */ })
it('retries transient polling errors and resolves the original run', async () => { /* 断线不先显示失败 */ })
it('renders success rollback and repair states with closable UiNotice', async () => { /* 中文且无内部 error code */ })
it('does not allow arbitrary version urls or paths', async () => { /* select 来自 GET 闭集 */ })
```

- [ ] **Step 2: 运行 RED**

Run: `npm --prefix web test -- --run web/src/views/SettingsView.spec.ts`

Expected: FAIL，更新区不存在。

- [ ] **Step 3: 实现最小更新区和退避轮询**

```ts
export interface UpdateResultPayload { runId: string; kind: 'ui'|'gateway'; version: string; state: UpdateState; errorCode?: string }
```

轮询间隔 1s、2s、4s、8s，最大 10s；网络失败继续读取同一 run ID，不创建新请求。`UiNotice` 标题固定区分“界面更新”和“Gateway 控制面更新”。

- [ ] **Step 4: 运行前端定向与全量验证并提交**

Run: `npm --prefix web test -- --run web/src/views/SettingsView.spec.ts`

Run: `npm test --prefix web -- --run`

Run: `npm run build --prefix web`

```bash
git add web/src/api/client.ts web/src/views/SettingsView.vue web/src/views/SettingsView.spec.ts web/src/components/errorMessages.ts
git commit -m "feat: manage gateway updates from settings"
```

### Task 8: 全量验证、生产 dry-run、开放替换和最终验收

**Files:**
- Modify: `README.md`
- Modify: `docs/handoffs/2026-09-05-current-state-handoff.md`
- Create: `docs/verification/2026-09-05-safe-gateway-self-update.md`
- Create: `.deploy-assets/gateway-updater-20260905/`（Git 忽略的资产与脱敏记录）

**Interfaces:**
- Consumes: 前七项通过验证的二进制、配置、units 和 UI。
- Produces: 生产 updater、dry-run 证据、一次 `control-plane-only` 升级/回滚演练和最终状态记录。

- [ ] **Step 1: 执行提交前全量验证**

Run: `npm test --prefix web -- --run`

Run: `npm run build --prefix web`

Run: `go test ./... -race -count=1`

Run: `go vet ./...`

Run: `go build ./cmd/aimili-gateway ./cmd/aimili-gateway-admin ./cmd/aimili-gateway-update-fetch ./cmd/aimili-gateway-update-install`

Run: `git diff --check`

Expected: 全部 PASS，未跟踪既有构建/缓存不加入提交。

- [ ] **Step 2: Stage 0 生产只读预检**

记录四服务/PID、Gateway DB quick check、四出口及协议、固定端口、受管与非受管指纹、磁盘、唯一 previous、update/protocol/source-policy transaction 空闲。任一不满足即停止生产写入。

- [ ] **Step 3: 安装 updater 但保持 gateway replace 禁用**

配置 `allowGatewayInstall=false`，部署 fetcher/installer/path units；提交同版本 dry-run，期望明确 `same_version` 或 `success` 且生产二进制摘要、PID、DB 和数据面完全不变。

- [ ] **Step 4: 开放 control-plane-only 并执行一次受控升级**

只对本轮已签名、schema 兼容、无运行时影响的 Gateway 包设置 `allowGatewayInstall=true`。记录 run ID；中断后按 run ID、二进制摘要和 service 状态恢复，不重放。

- [ ] **Step 5: 验证唯一 previous 和无页面回滚入口**

确认仅一个 previous 二进制，无 DB/config/cert 备份。用固定 root rollback helper 回退一次，再更新回当前版；两次都必须只改变 Gateway PID/二进制，其他服务 PID/进程、协议、端口和资源指纹不变。

- [ ] **Step 6: 用户原页面路径复测**

从高级设置读取可用版本、提交重新认证、观察断线重连和最终中文 `UiNotice`；UI 更新说明“不影响节点”，后端更新说明“控制面短暂重启，代理节点继续运行”。不自行改变 v2rayN 当前活动代理。

- [ ] **Step 7: 清理与最终状态检查**

精确删除本次 `/tmp` staging 和超过最近一个终态的状态；只保留 current、previous、内嵌 UI 和唯一 Gateway previous。重新执行磁盘、四服务、DB、四出口、协议、端口、进程与指纹检查。

- [ ] **Step 8: 更新 README、交接和验证记录并提交**

```bash
git add README.md docs/handoffs/2026-09-05-current-state-handoff.md docs/verification/2026-09-05-safe-gateway-self-update.md
git commit -m "docs: verify safe gateway self updates"
```

- [ ] **Step 9: 远程同步检查**

Run: `git fetch --prune origin`

Run: `git rev-list --left-right --count origin/feat/main-switch-protocol-modes...HEAD`

只有 `git push origin feat/main-switch-protocol-modes` 成功且远程 SHA 与本地 HEAD 一致后，才能报告“已推送 GitHub”；否则明确报告未推送。

