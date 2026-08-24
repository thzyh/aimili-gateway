# Aimili Gateway V1-A 可运行控制台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Do not dispatch subagents for this project.

**Goal:** 交付一个可独立运行的个人单管理员控制台，具备本地初始化、密码与 TOTP 登录、只读服务探测、统一状态页和独立的 3x-ui 专家模式入口。

**Architecture:** 使用一个 Go 进程提供 `/api/v1` 和内嵌 Vue 静态资源，SQLite 保存控制台账户、会话、兼容性状态和审计事件。V1-A 的 AimiliVPN 与 3x-ui 适配器只做回环地址上的非破坏性探测，不修改底层服务配置。

**Tech Stack:** Go 1.26.x、`net/http`、`database/sql`、`modernc.org/sqlite v1.38.2`、`golang.org/x/crypto v0.41.0`、Node.js 24.x、npm 11.x、Vue 3.5.18、Vue Router 4.5.1、Vite 7.1.3、TypeScript 5.9.2、Vitest 3.2.4。

**Spec:** `docs/superpowers/specs/2026-08-24-unified-console-design.md`

## Global Constraints

- V1 只支持一个个人管理员，不创建租户、组织、邀请、角色继承或资源归属模型。
- Aimili Gateway 默认监听 `127.0.0.1:9080`，不新增公网管理端口。
- 统一控制台登录与 3x-ui 专家模式登录相互独立，不把相同密码或 Caddy 保护描述为 SSO。
- 只有 Caddy 对公网提供 HTTPS；控制台、AimiliVPN 和 3x-ui 管理端保持回环监听。
- 控制台以非 root 专用用户运行，不获得任意命令、systemd、Caddy 或数据库修改权限。
- 不记录或提交密码、Cookie、令牌、TOTP 秘钥、私钥、UUID、随机后台路径或完整订阅链接。
- 未经新的明确授权，不连接或修改生产 VPS。
- 当前机器没有可用的 Go 工具链。执行 Task 1 前必须一次性取得安装 Go 1.26.x 的明确授权；不得自动下载工具链。
- 获得授权后优先把官方 Go 压缩包解压到仓库忽略的 `.tools/go`，只为当前任务会话追加 PATH；不运行系统安装器、不修改全局 PATH 或其他项目环境。
- `go env GOTOOLCHAIN=local` 必须生效，避免 Go 自动下载其他版本。
- 第一次执行 `go mod download` 或 `npm install` 前，集中取得下载本文已声明依赖的授权。
- 每个任务遵循测试先行、最小实现、完整验证和独立提交。

---

## File Structure

V1-A 创建以下结构：

```text
aimili-gateway/
├─ cmd/
│  ├─ aimili-gateway/main.go
│  └─ aimili-gateway-admin/main.go
├─ internal/
│  ├─ adapters/
│  │  ├─ contracts.go
│  │  ├─ aimili/probe.go
│  │  └─ xui/probe.go
│  ├─ app/app.go
│  ├─ auth/
│  │  ├─ password.go
│  │  ├─ secretbox.go
│  │  └─ totp.go
│  ├─ config/config.go
│  ├─ httpapi/
│  │  ├─ auth_handlers.go
│  │  ├─ middleware.go
│  │  ├─ overview_handlers.go
│  │  └─ server.go
│  ├─ webassets/
│  │  └─ embed.go
│  └─ store/
│     ├─ migrations/001_initial.sql
│     ├─ admin.go
│     ├─ audit.go
│     ├─ sessions.go
│     └─ store.go
├─ web/
│  ├─ src/
│  │  ├─ api/client.ts
│  │  ├─ components/ServiceCard.vue
│  │  ├─ router/index.ts
│  │  ├─ views/LoginView.vue
│  │  ├─ views/OverviewView.vue
│  │  ├─ App.vue
│  │  └─ main.ts
│  ├─ package.json
│  ├─ tsconfig.json
│  └─ vite.config.ts
├─ deploy/
│  ├─ caddy/AimiliGateway.Caddyfile
│  ├─ config/config.example.json
│  └─ systemd/aimili-gateway.service
├─ scripts/
│  ├─ verify-v1a.ps1
│  └─ verify-v1a.sh
├─ .gitignore
├─ go.mod
└─ go.sum
```

测试与实现文件放在同一 Go 包的 `_test.go` 文件中；Vue 测试使用相邻的 `*.spec.ts` 文件，避免建立与源码重复的测试目录树。

---

### Task 1: 工具链门槛、配置模型与最小进程

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `cmd/aimili-gateway/main.go`

**Interfaces:**
- Produces: `config.Load(path string) (config.Config, error)`
- Produces: `config.Config{ListenAddress, PublicOrigin, DatabasePath, MasterKeyFile, AimiliAddress, XUIBaseURL, ExpertModeURL string}`
- Consumes: no project interfaces

- [ ] **Step 1: Verify the approved Go toolchain exists**

Run:

```powershell
go version
go env GOTOOLCHAIN
```

Expected: Go reports `go1.26.x` and `GOTOOLCHAIN` is `local`. If `go` is missing, stop and request the already identified authorization to download the official archive into `.tools/go`; do not continue with Node-only substitution or modify the global environment.

- [ ] **Step 2: Create the module manifest**

Create `go.mod`:

```go
module github.com/thzyh/aimili-gateway

go 1.26

require (
    golang.org/x/crypto v0.41.0
    modernc.org/sqlite v1.38.2
)
```

Create `.gitignore` with `/.tools/`, `/bin/`, `/data/`, `/web/node_modules/`, `/internal/webassets/dist/`, `*.db`, `*.db-shm`, `*.db-wal`, `.env`, and secret credential files.

- [ ] **Step 3: Write failing configuration tests**

Create `internal/config/config_test.go` with table tests that assert:

```go
func TestLoadAppliesSafeDefaults(t *testing.T) {
    cfg, err := Load("")
    if err != nil { t.Fatal(err) }
    if cfg.ListenAddress != "127.0.0.1:9080" { t.Fatalf("listen=%q", cfg.ListenAddress) }
    if cfg.DatabasePath == "" || cfg.MasterKeyFile == "" { t.Fatal("required local paths missing") }
}

func TestValidateRejectsPublicListenAddress(t *testing.T) {
    cfg := Config{ListenAddress: "0.0.0.0:9080", DatabasePath: "gateway.db", MasterKeyFile: "master.key"}
    if err := cfg.Validate(); err == nil { t.Fatal("expected public listen rejection") }
}
```

- [ ] **Step 4: Run the tests and confirm the red state**

Run: `go test ./internal/config -v`

Expected: FAIL because `Load`, `Config`, and `Validate` do not exist.

- [ ] **Step 5: Implement the minimal configuration package**

Implement JSON loading with safe defaults, explicit file permission checks on non-Windows systems, loopback-only listener validation, URL parsing without logging paths, and environment overrides limited to documented keys. `PublicOrigin` may be empty only in explicit local-test mode; production requires one exact HTTPS origin without path, query or fragment. Do not accept unknown JSON fields; use `json.Decoder.DisallowUnknownFields()`.

The public type must be:

```go
type Config struct {
    ListenAddress string `json:"listenAddress"`
    PublicOrigin  string `json:"publicOrigin"`
    DatabasePath  string `json:"databasePath"`
    MasterKeyFile string `json:"masterKeyFile"`
    AimiliAddress string `json:"aimiliAddress"`
    XUIBaseURL    string `json:"xuiBaseUrl"`
    ExpertModeURL string `json:"expertModeUrl"`
}
```

- [ ] **Step 6: Add the minimal process entrypoint**

`cmd/aimili-gateway/main.go` must load `GATEWAY_CONFIG`, create a minimal `http.ServeMux` with only `GET /healthz`, listen only on the validated address, handle `SIGINT`/`SIGTERM`, and use a 10-second graceful shutdown timeout. Task 6 replaces this temporary mux with `app.New`; Task 1 must commit a fully building process without importing a future package.

- [ ] **Step 7: Verify configuration and module state**

Run:

```powershell
go mod download
go test ./internal/config -v
go test ./...
git diff --check
```

Expected: all tests PASS; no Go toolchain is downloaded implicitly; no secret or database file appears in `git status`.

- [ ] **Step 8: Commit**

```bash
git add .gitignore go.mod go.sum internal/config cmd/aimili-gateway
git commit -m "feat: add safe gateway configuration"
```

---

### Task 2: SQLite 迁移、单管理员和会话存储

**Files:**
- Create: `internal/store/migrations/001_initial.sql`
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`
- Create: `internal/store/admin.go`
- Create: `internal/store/sessions.go`
- Create: `internal/store/audit.go`

**Interfaces:**
- Consumes: `config.Config.DatabasePath`
- Produces: `store.Open(ctx context.Context, path string) (*store.Store, error)`
- Produces: `store.Admin`, `store.Session`, `store.AuditEvent`
- Produces: single-admin, session, and audit CRUD methods used by Tasks 4 and 6

- [ ] **Step 1: Write failing migration and invariant tests**

Create tests using `t.TempDir()` and a real SQLite file:

```go
func TestStoreAllowsExactlyOneAdmin(t *testing.T) {
    s := openTestStore(t)
    ctx := context.Background()
    if err := s.CreateAdmin(ctx, Admin{Username: "owner", PasswordHash: []byte("hash"), TOTPSecretCiphertext: []byte("cipher")}); err != nil { t.Fatal(err) }
    err := s.CreateAdmin(ctx, Admin{Username: "second", PasswordHash: []byte("hash"), TOTPSecretCiphertext: []byte("cipher")})
    if !errors.Is(err, ErrAdminExists) { t.Fatalf("err=%v", err) }
}

func TestRevokedSessionCannotBeLoaded(t *testing.T) {
    s := openTestStore(t)
    // Insert, revoke, then assert GetSession returns ErrSessionNotFound.
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/store -v`

Expected: FAIL because the store types and migrations do not exist.

- [ ] **Step 3: Write the initial migration**

`001_initial.sql` must create `schema_migrations`, `admin` with `CHECK (id = 1)`, `sessions`, `audit_events`, and `compatibility_status`. Store only SHA-256 session token hashes. Enable WAL, foreign keys, and a busy timeout when opening the database.

- [ ] **Step 4: Implement transaction-safe store methods**

Expose exact methods:

```go
func (s *Store) CreateAdmin(ctx context.Context, admin Admin) error
func (s *Store) GetAdmin(ctx context.Context) (Admin, error)
func (s *Store) CreateSession(ctx context.Context, session Session) error
func (s *Store) GetSession(ctx context.Context, tokenHash [32]byte, now time.Time) (Session, error)
func (s *Store) TouchSession(ctx context.Context, id int64, now time.Time) error
func (s *Store) RevokeSession(ctx context.Context, tokenHash [32]byte) error
func (s *Store) RevokeAllSessions(ctx context.Context) error
func (s *Store) AppendAudit(ctx context.Context, event AuditEvent) error
```

Map uniqueness and missing-row errors to exported sentinel errors without returning raw SQL text to callers.

Use these exact types:

```go
type Admin struct {
    Username             string
    PasswordHash         []byte
    TOTPSecretCiphertext []byte
    CreatedAt            time.Time
    SecurityUpdatedAt    time.Time
}

type Session struct {
    ID                int64
    TokenHash         [32]byte
    CSRFTokenHash     [32]byte
    CreatedAt         time.Time
    LastActiveAt      time.Time
    ExpiresAt         time.Time
    ReauthenticatedAt *time.Time
    RevokedAt         *time.Time
}
```

- [ ] **Step 5: Verify migration idempotence and concurrency**

Add tests that reopen the same database twice, run two concurrent session inserts, and verify a failed transaction leaves no partial audit row.

Run: `go test ./internal/store -race -v`

Expected: PASS with no race reports.

- [ ] **Step 6: Commit**

```bash
git add internal/store
git commit -m "feat: add single-admin gateway store"
```

---

### Task 3: 密码、TOTP 和秘钥加密原语

**Files:**
- Create: `internal/auth/password.go`
- Create: `internal/auth/password_test.go`
- Create: `internal/auth/totp.go`
- Create: `internal/auth/totp_test.go`
- Create: `internal/auth/secretbox.go`
- Create: `internal/auth/secretbox_test.go`

**Interfaces:**
- Produces: `auth.HashPassword(password []byte) (string, error)`
- Produces: `auth.VerifyPassword(encoded string, password []byte) (bool, error)`
- Produces: `auth.GenerateTOTPSecret() ([]byte, error)` and `auth.ValidateTOTP(secret []byte, code string, now time.Time) bool`
- Produces: `auth.Seal(masterKey, plaintext []byte) ([]byte, error)` and `auth.Open(masterKey, ciphertext []byte) ([]byte, error)`

- [ ] **Step 1: Write failing security primitive tests**

Cover password round-trip and rejection, malformed hash rejection, deterministic RFC 6238 vectors, one-step clock skew, AES-GCM tamper rejection, and unique nonces:

```go
func TestValidateTOTPUsesRFC6238Window(t *testing.T) {
    secret := []byte("12345678901234567890")
    if !ValidateTOTP(secret, "94287082"[2:], time.Unix(59, 0)) { t.Fatal("valid code rejected") }
}

func TestSealRejectsTampering(t *testing.T) {
    key := bytes.Repeat([]byte{1}, 32)
    sealed, _ := Seal(key, []byte("secret"))
    sealed[len(sealed)-1] ^= 1
    if _, err := Open(key, sealed); err == nil { t.Fatal("tampering accepted") }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/auth -v`

Expected: FAIL because the auth primitives do not exist.

- [ ] **Step 3: Implement Argon2id password encoding**

Use a 16-byte random salt, Argon2id parameters memory `64*1024`, iterations `3`, parallelism `2`, output length `32`, and the standard encoded form containing the version and parameters. Compare with `subtle.ConstantTimeCompare` and zero temporary password copies when practical.

- [ ] **Step 4: Implement RFC 6238 TOTP**

Use HMAC-SHA1, 30-second steps, six digits, and a validation window of current step plus one step before and after. Reject non-six-digit strings before computing HMAC. The enrollment URI is built only by the admin CLI and must percent-encode issuer and account.

- [ ] **Step 5: Implement AES-256-GCM secret storage**

Require exactly 32 master-key bytes, generate a new random nonce for every seal, prefix a one-byte format version, and reject unknown versions or truncated ciphertexts.

- [ ] **Step 6: Verify all primitive tests**

Run: `go test ./internal/auth -race -v`

Expected: PASS, including tamper and malformed-input tests.

- [ ] **Step 7: Commit**

```bash
git add internal/auth
git commit -m "feat: add gateway authentication primitives"
```

---

### Task 4: 本地管理员初始化命令

**Files:**
- Create: `cmd/aimili-gateway-admin/main.go`
- Create: `cmd/aimili-gateway-admin/main_test.go`
- Modify: `internal/store/admin.go`

**Interfaces:**
- Consumes: `config.Load`, `store.Open`, `auth.HashPassword`, `auth.GenerateTOTPSecret`, `auth.Seal`
- Produces: local command `aimili-gateway-admin init`
- Produces: local command `aimili-gateway-admin revoke-sessions`

- [ ] **Step 1: Write failing CLI tests around an injected terminal**

Define `run(args []string, in io.Reader, out, errOut io.Writer, now func() time.Time) int` and test that initialization creates one admin, never echoes the password, emits the enrollment URI exactly once, and rejects a second initialization.

```go
func TestInitDoesNotEchoPassword(t *testing.T) {
    // Point config at a temporary database and master key.
    // Feed username, password, and confirmation through the injected reader.
    // Assert output contains the enrollment scheme but not the password bytes.
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./cmd/aimili-gateway-admin -v`

Expected: FAIL because `run` does not exist.

- [ ] **Step 3: Implement interactive initialization**

Read passwords without terminal echo when stdin is a terminal; require at least 12 characters; reject leading or trailing whitespace; create the admin in one transaction. Print the TOTP enrollment URI once to stdout and a warning that losing the second factor requires local recovery. Never accept password or TOTP secret in command-line arguments.

- [ ] **Step 4: Implement session revocation**

`revoke-sessions` loads the same config and invokes `Store.RevokeAllSessions`. It must not modify the administrator password or TOTP secret.

- [ ] **Step 5: Verify CLI behavior**

Run:

```powershell
go test ./cmd/aimili-gateway-admin -v
go test ./internal/auth ./internal/store -v
```

Expected: PASS; captured output contains no supplied test password.

- [ ] **Step 6: Commit**

```bash
git add cmd/aimili-gateway-admin internal/store/admin.go
git commit -m "feat: add local gateway admin initialization"
```

---

### Task 5: 会话、CSRF、登录与退出 HTTP 流程

**Files:**
- Create: `internal/httpapi/middleware.go`
- Create: `internal/httpapi/auth_handlers.go`
- Create: `internal/httpapi/auth_handlers_test.go`
- Create: `internal/httpapi/server.go`

**Interfaces:**
- Consumes: `store.Store`, auth primitives, `config.Config`
- Produces: `httpapi.NewServer(deps httpapi.Dependencies) http.Handler`
- Produces: `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/session`, `POST /api/v1/auth/reauth`

- [ ] **Step 1: Write failing authentication flow tests**

Use `httptest.Server` and a cookie jar. Cover wrong password, wrong TOTP, successful login, secure cookie attributes, session expiry, logout, CSRF rejection, origin rejection, and login throttling.

```go
func TestMutationRequiresCSRFAndSameOrigin(t *testing.T) {
    env := newAuthTestEnvironment(t)
    req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
    req.AddCookie(env.sessionCookie)
    rr := httptest.NewRecorder()
    env.handler.ServeHTTP(rr, req)
    if rr.Code != http.StatusForbidden { t.Fatalf("status=%d", rr.Code) }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/httpapi -run Auth -v`

Expected: FAIL because the server and handlers do not exist.

- [ ] **Step 3: Implement opaque server-side sessions**

Generate 32 random bytes, return the base64url token only in a cookie, and store only its SHA-256 hash. Set cookie name `aimili_gateway_session`, `Path=/`, `Secure`, `HttpOnly`, `SameSite=Strict`, and no JavaScript-readable auth token. Use a 30-minute idle timeout and 12-hour absolute lifetime.

- [ ] **Step 4: Implement CSRF and origin checks**

Generate one random CSRF token per session, return it from `GET /api/v1/auth/session`, require it in `X-CSRF-Token` for all state-changing API requests, and compare in constant time. Accept only the configured public origin; in local test mode accept the exact `httptest` origin supplied by the test dependency.

- [ ] **Step 5: Implement login throttling and reauthentication**

Allow five failed attempts per normalized remote IP and username over 15 minutes, then return HTTP 429 without indicating whether the username exists. Successful reauthentication stores a five-minute `reauthenticated_at` timestamp in the session; later destructive handlers consume this property.

- [ ] **Step 6: Verify authentication behavior**

Run: `go test ./internal/httpapi -race -v`

Expected: PASS for authentication, expiry, CSRF, origin, logout, and throttling cases.

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi
git commit -m "feat: add secure gateway sessions"
```

---

### Task 6: 只读适配器探测与统一状态 API

**Files:**
- Create: `internal/adapters/contracts.go`
- Create: `internal/adapters/aimili/probe.go`
- Create: `internal/adapters/aimili/probe_test.go`
- Create: `internal/adapters/xui/probe.go`
- Create: `internal/adapters/xui/probe_test.go`
- Create: `internal/httpapi/overview_handlers.go`
- Create: `internal/httpapi/overview_handlers_test.go`
- Create: `internal/app/app.go`
- Modify: `cmd/aimili-gateway/main.go`

**Interfaces:**
- Produces: `type adapters.Prober interface { Probe(context.Context) adapters.ProbeResult }`
- Produces: `GET /api/v1/overview`
- Consumes: authenticated session middleware and `config.Config`

- [ ] **Step 1: Define the shared probe contract and failing tests**

Create:

```go
type Health string
const (
    HealthHealthy Health = "healthy"
    HealthDegraded Health = "degraded"
    HealthUnavailable Health = "unavailable"
)

type ProbeResult struct {
    Service      string   `json:"service"`
    Health       Health   `json:"health"`
    Version      string   `json:"version,omitempty"`
    Capabilities []string `json:"capabilities"`
    ErrorCode    string   `json:"errorCode,omitempty"`
    CheckedAt    time.Time `json:"checkedAt"`
}

type Prober interface { Probe(context.Context) ProbeResult }
```

Write tests for reachable, refused, timeout, malformed response, and redacted URL behavior.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/adapters/... -v`

Expected: FAIL because the probes do not exist.

- [ ] **Step 3: Implement the V1-A AimiliVPN probe**

Dial only the configured loopback TCP address with a one-second timeout. Report `healthy` when the listener accepts, `unavailable` on refusal, and no version or write capability. Do not call the secret-path native UI or authenticate with its human account.

- [ ] **Step 4: Implement the V1-A 3x-ui probe**

Call the configured local CSRF endpoint with a two-second HTTP client timeout and disabled redirects. Treat a structurally valid CSRF response as reachable, but expose no write capability until V1-C. Never log or return the configured base path.

- [ ] **Step 5: Implement the overview handler and application wiring**

`GET /api/v1/overview` returns:

```json
{
  "gateway": {"health":"healthy"},
  "services": [],
  "expertModeAvailable": true
}
```

The actual expert-mode URL is returned only to an authenticated browser in a separate `GET /api/v1/navigation` response with `Cache-Control: no-store`; it is never included in audit details.

Implement `app.New(ctx context.Context, cfg config.Config) (*app.App, error)`, `(*app.App).Handler() http.Handler`, and `(*app.App).Close() error`. `New` opens the store, builds probes and handlers, and owns closable resources. `main` passes `application.Handler()` to `http.Server` and calls `Close` after graceful shutdown.

- [ ] **Step 6: Verify safe degradation**

Run:

```powershell
go test ./internal/adapters/... ./internal/httpapi -race -v
go test ./...
```

Expected: PASS; a stopped service produces an `unavailable` card without failing the gateway process.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters internal/app internal/httpapi/overview_handlers* cmd/aimili-gateway/main.go
git commit -m "feat: add read-only service overview"
```

---

### Task 7: Vue 登录页、总览页和专家模式入口

**Files:**
- Create: `web/package.json`
- Create: `web/package-lock.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/index.html`
- Create: `web/src/api/client.ts`
- Create: `web/src/router/index.ts`
- Create: `web/src/components/ServiceCard.vue`
- Create: `web/src/components/ServiceCard.spec.ts`
- Create: `web/src/views/LoginView.vue`
- Create: `web/src/views/LoginView.spec.ts`
- Create: `web/src/views/OverviewView.vue`
- Create: `web/src/views/OverviewView.spec.ts`
- Create: `web/src/App.vue`
- Create: `web/src/main.ts`
- Create: `internal/webassets/embed.go`
- Modify: `internal/httpapi/server.go`

**Interfaces:**
- Consumes: V1-A auth, overview, and navigation APIs
- Produces: compiled SPA under `internal/webassets/dist`
- Produces: same-origin `apiFetch<T>(path, init) Promise<T>` with CSRF support

- [ ] **Step 1: Create the exact frontend manifest**

`web/package.json` must pin exact versions and scripts:

```json
{
  "private": true,
  "type": "module",
  "scripts": {
    "build": "vue-tsc --noEmit && vite build",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "vue": "3.5.18",
    "vue-router": "4.5.1"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "6.0.1",
    "@vue/test-utils": "2.4.6",
    "happy-dom": "18.0.1",
    "typescript": "5.9.2",
    "vite": "7.1.3",
    "vitest": "3.2.4",
    "vue-tsc": "3.0.6"
  }
}
```

Run `npm install` only after dependency-download authorization and commit the generated lock file.

- [ ] **Step 2: Write failing component tests**

Tests must verify login submits password and six-digit TOTP without persisting them, overview renders healthy/degraded/unavailable separately, and expert mode uses a normal new-window link rather than an iframe.

```ts
it('does not persist credentials', async () => {
  const setItem = vi.spyOn(Storage.prototype, 'setItem')
  // Submit the form through a mocked apiFetch.
  expect(setItem).not.toHaveBeenCalled()
})
```

- [ ] **Step 3: Confirm the red state**

Run: `cd web; npm test`

Expected: FAIL because the components do not exist.

- [ ] **Step 4: Implement the API client and router guards**

`apiFetch` uses `credentials: 'same-origin'`, obtains CSRF from the session response, sets `X-CSRF-Token` only for mutations, rejects non-JSON error bodies, and redirects to login on HTTP 401. Never use `localStorage` or `sessionStorage` for authentication data.

- [ ] **Step 5: Implement the minimal responsive UI**

Use plain CSS variables and native controls, no component library. The overview must show three distinct layers: gateway process, AimiliVPN reachability, and 3x-ui reachability. Expert mode opens the authenticated navigation URL in a new tab and visibly states that 3x-ui will ask for its own login.

- [ ] **Step 6: Embed the production assets**

Set Vite `build.outDir` to `../internal/webassets/dist`. Use `//go:embed dist/*` in the focused `internal/webassets` package; serve immutable hashed assets with long cache lifetime and `index.html` with `no-cache`. API routes must be registered before the SPA fallback. The generated `dist` directory remains ignored and is recreated by `npm run build` before every clean Go build.

- [ ] **Step 7: Verify frontend and backend builds**

Run:

```powershell
Set-Location web
npm test
npm run build
Set-Location ..
go test ./...
go build ./cmd/aimili-gateway
```

Expected: all tests and both builds PASS.

- [ ] **Step 8: Commit**

```bash
git add web internal/httpapi
git commit -m "feat: add gateway login and overview UI"
```

---

### Task 8: systemd、Caddy 和示例配置资产

**Files:**
- Create: `deploy/config/config.example.json`
- Create: `deploy/systemd/aimili-gateway.service`
- Create: `deploy/caddy/AimiliGateway.Caddyfile`
- Create: `deploy/deploy_contract_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: built `aimili-gateway` and `aimili-gateway-admin` binaries
- Produces: non-root systemd unit and Caddy route fragment

- [ ] **Step 1: Write failing deployment contract tests**

Read deployment assets as text and assert loopback binding, `User=aimili-gateway`, `NoNewPrivileges=true`, `PrivateTmp=true`, encrypted credential loading, no hardcoded domain or secret path, specific-route-before-fallback Caddy order, and no shell execution capability.

```go
func TestSystemdUnitIsUnprivileged(t *testing.T) {
    unit := readAsset(t, "systemd/aimili-gateway.service")
    for _, required := range []string{"User=aimili-gateway", "NoNewPrivileges=true", "PrivateTmp=true"} {
        if !strings.Contains(unit, required) { t.Fatalf("missing %s", required) }
    }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./deploy -v`

Expected: FAIL because the assets do not exist.

- [ ] **Step 3: Implement the systemd unit and example config**

The unit must use `LoadCredentialEncrypted` for the master key, `Environment=GATEWAY_CONFIG=/etc/aimili-gateway/config.json`, `ProtectSystem=strict`, `ProtectHome=true`, `ReadWritePaths=/var/lib/aimili-gateway`, and restart only the gateway process. The example config uses loopback addresses and contains no real panel path.

- [ ] **Step 4: Implement the Caddy fragment**

The fragment must place existing expert and subscription routes before:

```caddyfile
handle /api/v1/* {
    reverse_proxy 127.0.0.1:9080
}

handle {
    reverse_proxy 127.0.0.1:9080
}
```

Document that the fragment is merged into the existing site block only after `caddy validate`; it must not add Basic Auth or claim SSO.

- [ ] **Step 5: Verify deployment contracts**

Run:

```powershell
go test ./deploy -v
go test ./...
git diff --check
```

Expected: PASS. Do not run systemctl, Caddy reload, SSH, or production commands in V1-A implementation without separate authorization.

- [ ] **Step 6: Commit**

```bash
git add deploy README.md
git commit -m "docs: add secure gateway deployment assets"
```

---

### Task 9: V1-A 完整验证脚本与审核门槛

**Files:**
- Create: `scripts/verify-v1a.ps1`
- Create: `scripts/verify-v1a.sh`
- Create: `docs/verification/v1a.md`

**Interfaces:**
- Consumes: all V1-A source, tests, build scripts, and deployment assets
- Produces: one-command local verification on Windows and Linux

- [ ] **Step 1: Write the verification scripts with fail-fast behavior**

Both scripts must run, in order:

```text
npm test --prefix web
npm run build --prefix web
go test ./... -race
go vet ./...
go build ./cmd/aimili-gateway
go build ./cmd/aimili-gateway-admin
git diff --check
```

They must set `GOTOOLCHAIN=local`, stop on the first failure, write no secrets, and return the failing command's exit code.

- [ ] **Step 2: Add a secret-pattern and artifact check**

Scan tracked files for private-key headers, cookie assignment, connection URI schemes, UUID-shaped literals, committed `.db` files, `.env`, and credential files. Allow documented words such as “Cookie” but reject actual credential syntax and generated artifacts.

- [ ] **Step 3: Run the full V1-A verification**

Run: `pwsh -NoProfile -File .\scripts\verify-v1a.ps1`

Expected: exit 0 with all Go tests, race checks, vet, Go builds, Vue tests, Vue build, diff check, and secret scan passing.

- [ ] **Step 4: Perform the local user-path smoke test**

Using a temporary config and database outside the repository:

1. Run `aimili-gateway-admin init` interactively.
2. Start `aimili-gateway` on loopback.
3. Open the local page through a temporary local reverse proxy or direct loopback access.
4. Log in with password and TOTP.
5. Confirm the overview distinguishes gateway, AimiliVPN, and 3x-ui state.
6. Confirm expert mode opens a separate page that still requires 3x-ui login.
7. Stop the gateway and confirm no bottom service was stopped or modified.

Record only timestamps, command versions, pass/fail results, and redacted error categories in `docs/verification/v1a.md`.

- [ ] **Step 5: Re-read the spec and verify V1-A scope**

Confirm V1-A contains no AimiliVPN write API, no 3x-ui credential storage, no 3x-ui write action, no production routing change, and no multi-user model.

- [ ] **Step 6: Commit**

```bash
git add scripts docs/verification/v1a.md
git commit -m "test: verify runnable gateway foundation"
```

## V1-A Completion Gate

V1-A is complete only when Task 9 passes with fresh output and the local browser path is verified. Completion authorizes planning review for V1-B execution; it does not authorize connecting to `ny`, installing on production, or starting V1-C early.
