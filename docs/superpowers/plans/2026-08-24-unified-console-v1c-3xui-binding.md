# Aimili Gateway V1-C 3x-ui 管理与出口绑定 Implementation Plan

> **状态：已废止，不得执行。** 用户于 2026-08-25 将后续目标修订为国家代理目录和 VLESS＋mixed 成对编排。本计划被 `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md` 取代；新设计书面批准后重新编写实施计划。

> 本文件只作历史记录，不得调用实施计划流程或执行其中任何复选步骤。

**Goal:** 在已验证的 V1-B 基础上增加 3x-ui 入站与客户端日常管理、敏感连接信息的受控显示，以及 3x-ui 入站到 AimiliVPN 主出口或多出口槽位的可靠绑定。

**Architecture:** Gateway 的 3x-ui Adapter 使用本地 Cookie Jar、CSRF 和已验证的管理 API，不访问 `x-ui.db`。跨服务编排先读取 AimiliVPN 槽位和 3x-ui Xray 配置，只修改 `agw-` 命名空间的 outbound 与 routing rule，写后重新读取；失败时恢复受管配置快照并进入可审计的部分失败锁。

**Tech Stack:** Go 1.26.x、Go `net/http` 与 `encoding/json`、现有 SQLite 存储、Vue 3.5.18、Vitest 3.2.4、官方 3x-ui `v3.6.0` 隔离测试实例。

**Spec:** `docs/superpowers/specs/2026-08-24-unified-console-design.md`

## Global Constraints

- 必须先完成 V1-A 和 V1-B 的全部验证；本计划不绕过前两阶段的认证、适配器或写后验证机制。
- V1 仍只有一个个人管理员，不新增多租户、组织或复杂 RBAC。
- 专家模式继续使用 3x-ui 自身登录；Gateway 不注入 Cookie、不自动填写登录表单、不宣称 SSO。
- 3x-ui Adapter 不直接读取或写入 `x-ui.db`，不修改 3x-ui 源码，不删除上游功能。
- 首个适配目标只声明官方 `v3.6.0`；其他版本必须通过能力探测和合同测试后才能启用写操作。
- 当前本机 Docker CLI 存在但 daemon 未运行，WSL 先前也有环境阻塞。获取 3x-ui 源码、镜像或启动隔离 Linux 实例前，必须集中取得相应下载与环境操作授权。
- V1 不保存 3x-ui TOTP 秘钥。启用 3x-ui 双重验证且没有经验证 API Token 或独立自动化账户时，写能力关闭。
- 服务凭据只通过 systemd 加密凭据或严格权限的运行时文件提供，不保存到 Git、SQLite 业务表或普通日志。
- 客户端 UUID、私钥、完整订阅链接和完整连接 URI 不进入审计、日志或持久化绑定表。
- 删除资源、显示敏感连接信息和解除有依赖的绑定必须要求五分钟内的重新认证。
- 未经新的明确授权，不连接、读取或修改生产 VPS。

---

## File Structure

```text
internal/
├─ adapters/xui/
│  ├─ auth.go
│  ├─ client.go
│  ├─ client_test.go
│  ├─ contract.go
│  ├─ contract_test.go
│  ├─ fixtures/v3.6.0/
│  │  ├─ csrf.json
│  │  ├─ login.json
│  │  ├─ inbounds.json
│  │  └─ xray.json
│  └─ xray_patch.go
├─ bindings/
│  ├─ orchestrator.go
│  ├─ orchestrator_test.go
│  ├─ xray_patch.go
│  └─ xray_patch_test.go
├─ domain/xui.go
├─ httpapi/
│  ├─ binding_handlers.go
│  ├─ binding_handlers_test.go
│  ├─ xui_handlers.go
│  └─ xui_handlers_test.go
└─ store/
   ├─ migrations/002_bindings.sql
   ├─ bindings.go
   └─ bindings_test.go
web/src/
├─ components/ClientTable.vue
├─ components/InboundCard.vue
├─ components/SensitiveReveal.vue
├─ views/BindingsView.vue
├─ views/InboundDetailView.vue
└─ views/InboundsView.vue
deploy/
├─ config/config.example.json
└─ systemd/aimili-gateway.service
scripts/
├─ verify-v1c.ps1
└─ verify-v1c.sh
docs/verification/v1c.md
```

测试 fixture 必须是人工构造的最小 JSON，只保留响应结构，不复制生产或真实用户数据。

---

## Expected 3x-ui v3.6.0 Contract

Task 1 必须在隔离实例中验证以下端点；任何方法或路径不匹配都停止执行并修订本计划，不能猜测调用：

| Method | Path | Purpose |
|---|---|---|
| GET | `/csrf-token` | 获取 CSRF Token |
| POST | `/login` | 建立管理会话 |
| POST | `/panel/api/inbounds/list` | 读取入站 |
| POST | `/panel/api/inbounds/add` | 新增入站 |
| POST | `/panel/api/inbounds/update/{id}` | 更新入站 |
| POST | `/panel/api/inbounds/del/{id}` | 删除入站 |
| POST | `/panel/api/inbounds/addClient` | 新增客户端 |
| POST | `/panel/api/inbounds/updateClient/{clientId}` | 更新客户端 |
| POST | `/panel/api/inbounds/{id}/delClient/{clientId}` | 删除客户端 |
| POST | `/panel/api/inbounds/{id}/resetClientTraffic/{email}` | 重置客户端流量 |
| POST | `/panel/api/xray/` | 读取 Xray 模板 |
| POST | `/panel/api/xray/update` | 更新 Xray 模板 |
| POST | `/panel/api/server/status` | 读取服务状态，若版本实际使用其他只读端点则以源码验证结果修订计划 |

API 成功响应必须同时满足 HTTP 成功和 JSON `success != false`；任何 HTML 登录页、重定向或字段缺失都视为认证失败或不兼容。

---

### Task 1: 固化官方 3x-ui v3.6.0 合同证据

**Files:**
- Create: `internal/adapters/xui/fixtures/v3.6.0/csrf.json`
- Create: `internal/adapters/xui/fixtures/v3.6.0/login.json`
- Create: `internal/adapters/xui/fixtures/v3.6.0/inbounds.json`
- Create: `internal/adapters/xui/fixtures/v3.6.0/xray.json`
- Create: `internal/adapters/xui/contract.go`
- Create: `internal/adapters/xui/contract_test.go`
- Create: `docs/verification/3xui-v3.6.0-contract.md`

**Interfaces:**
- Produces: `xui.Contract` and `xui.V360Contract()`
- Produces: synthetic schema fixtures used by all adapter tests
- Consumes: official 3x-ui source at the already identified `v3.6.0` commit in an isolated environment

- [ ] **Step 1: Obtain authorization and prepare an isolated source inspection**

Request one authorization covering download of official 3x-ui `v3.6.0` source or image and startup of a disposable local container/VM. Do not use `ny`. Record the source tag and commit hash, but do not record credentials or generated service data.

- [ ] **Step 2: Verify route definitions in official source**

Search the checked-out source for each method/path in the contract table. Record source file names and line numbers in `docs/verification/3xui-v3.6.0-contract.md`. If the service-status or client route differs, update the contract table and all later steps in this plan before implementation.

- [ ] **Step 3: Run a disposable instance and capture only response shapes**

Create temporary test credentials inside the disposable environment, call CSRF/login/read-only endpoints, and manually construct minimal synthetic fixture files with fake IDs, example-domain addresses and zero traffic. Do not copy Cookie headers, client UUIDs, private keys, random panel paths or subscription data.

- [ ] **Step 4: Write failing contract tests**

Define:

```go
type Contract struct {
    Version       string
    CSRFFlow      bool
    Routes        map[Capability]Route
    RequiredPaths map[PayloadKind][]string
}

func V360Contract() Contract
func (c Contract) ValidateFixture(kind PayloadKind, raw []byte) error
```

Tests must assert every expected capability has a concrete method/path and every fixture contains required structural fields.

- [ ] **Step 5: Confirm the red state**

Run: `go test ./internal/adapters/xui -run Contract -v`

Expected: FAIL because contract implementation is incomplete.

- [ ] **Step 6: Implement and verify the versioned contract**

Implement only the routes and required fields confirmed in the official source and disposable instance.

Run: `go test ./internal/adapters/xui -run Contract -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/xui docs/verification/3xui-v3.6.0-contract.md
git commit -m "test: capture verified 3x-ui v3.6.0 contract"
```

---

### Task 2: 3x-ui 会话认证与能力探测

**Files:**
- Create: `internal/adapters/xui/auth.go`
- Create: `internal/adapters/xui/client.go`
- Create: `internal/adapters/xui/client_test.go`
- Modify: `internal/adapters/contracts.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Produces: `xui.NewClient(baseURL string, credential Credential, contract Contract, httpClient *http.Client) (*Client, error)`
- Produces: `Client.Probe(ctx context.Context) adapters.ProbeResult`
- Produces: `Credential{Username string, Password []byte}` loaded only from runtime credentials
- Consumes: V1-A probe contract and Task 1 contract

- [ ] **Step 1: Write failing authentication state-machine tests**

Use an `httptest.Server` with exact CSRF/login behavior. Cover initial login, cookie reuse, CSRF refresh, session expiry followed by one re-login, rejected credentials, HTML redirect, two-factor-required response, redirect to a different host, and repeated-login suppression.

```go
func TestClientFailsClosedWhenTwoFactorIsRequired(t *testing.T) {
    fixture := newXUIAuthFixture(t, authModeTwoFactorRequired)
    client := fixture.Client(t)
    result := client.Probe(context.Background())
    if slices.Contains(result.Capabilities, "inbounds.write") { t.Fatal("write enabled with unsupported 2FA") }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/adapters/xui -run 'Auth|Probe' -v`

Expected: FAIL because authentication is not implemented.

- [ ] **Step 3: Implement the CSRF and Cookie Jar flow**

Use one cookie jar per adapter instance, disabled cross-host redirects, five-second timeout, two-megabyte response cap, form encoding where required, `X-Requested-With: XMLHttpRequest`, and `X-CSRF-Token` on mutations. Never expose or log cookies.

- [ ] **Step 4: Implement capability probing**

Probe CSRF, login and read-only endpoints. Enable a capability only when its route exists in the verified contract and the response contains all required fields. Return `incompatible` for unknown shapes and `forbidden` for unsupported two-factor flow.

- [ ] **Step 5: Load runtime credentials without persistence**

Read username and password from systemd credential files at process startup into private byte slices. Do not insert them into SQLite or the config JSON. Clear replaced password bytes during credential reload.

- [ ] **Step 6: Verify authentication and secret handling**

Run: `go test ./internal/adapters/xui -race -v`

Expected: PASS; tests assert errors and captured logs contain no supplied password or Cookie value.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters internal/app
git commit -m "feat: add secure 3x-ui adapter authentication"
```

---

### Task 3: 入站与客户端只读领域模型

**Files:**
- Create: `internal/domain/xui.go`
- Modify: `internal/adapters/xui/client.go`
- Modify: `internal/adapters/xui/client_test.go`

**Interfaces:**
- Produces: `Client.ListInbounds(ctx context.Context) ([]domain.Inbound, error)`
- Produces: `Client.GetInbound(ctx context.Context, opaqueID string) (domain.Inbound, error)`
- Produces: opaque resource handles derived by HMAC and matched against freshly read upstream data

- [ ] **Step 1: Write failing mapping and opaque-ID tests**

Tests must parse string-encoded nested `settings`, `streamSettings`, and `sniffing`; reject malformed JSON; map only VLESS Reality fields; preserve unknown protocol as read-only; and prove raw client UUIDs never appear in domain JSON.

```go
func TestClientUUIDBecomesOpaqueHandle(t *testing.T) {
    inbound, rawClientID := parseSyntheticInbound(t)
    body, _ := json.Marshal(inbound)
    if bytes.Contains(body, rawClientID) {
        t.Fatal("raw client identifier leaked")
    }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/adapters/xui -run 'Inbound|Opaque' -v`

Expected: FAIL because domain mapping does not exist.

- [ ] **Step 3: Define domain models**

`domain.Inbound` contains opaque ID, name, enabled state, protocol, port, traffic totals, supported-template flag, and clients. `domain.XUIClient` contains opaque handle, display name, enabled state, quota, usage and expiry, but no UUID, subscription ID, private key or complete connection URI.

- [ ] **Step 4: Implement stateless opaque handle matching**

Generate handles as base64url HMAC-SHA256 over resource kind plus raw upstream identity using a dedicated key derived from the Gateway master key. On every action, re-read the upstream list, recompute handles and locate exactly one match. Never persist the raw client identity.

- [ ] **Step 5: Verify read and mapping behavior**

Run: `go test ./internal/adapters/xui -race -v`

Expected: PASS; unknown protocols appear read-only rather than causing a server error.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/xui.go internal/adapters/xui
git commit -m "feat: add safe 3x-ui read models"
```

---

### Task 4: 受限入站与客户端写操作

**Files:**
- Modify: `internal/adapters/xui/client.go`
- Modify: `internal/adapters/xui/client_test.go`
- Create: `internal/adapters/xui/commands.go`
- Create: `internal/adapters/xui/commands_test.go`

**Interfaces:**
- Produces: `CreateRealityInbound`, `UpdateRealityInbound`, `SetInboundEnabled`, `DeleteInbound`
- Produces: `AddClient`, `UpdateClient`, `SetClientEnabled`, `ResetClientTraffic`, `DeleteClient`
- Consumes: Task 3 opaque identity resolution and Task 1 verified routes

- [ ] **Step 1: Write failing command validation tests**

Cover port range, duplicate port, unsupported protocol, invalid quota, past expiry, oversized display name, client handle mismatch, concurrent upstream change, delete confirmation flag, and response-success-without-state-change.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/adapters/xui -run Command -v`

Expected: FAIL because commands do not exist.

- [ ] **Step 3: Implement constrained command types**

Expose exact command shapes:

```go
type CreateRealityInbound struct {
    Name string
    Port uint16
}

type ClientCommand struct {
    DisplayName string
    Enabled bool
    QuotaBytes int64
    ExpiresAt *time.Time
}
```

Reality target, fingerprint, flow and sniffing use one reviewed server-side template. The browser cannot submit arbitrary nested Xray JSON.

- [ ] **Step 4: Implement minimal upstream payload merges**

Before updating, fetch the latest inbound and preserve fields outside the supported template. Reject an update if the upstream fingerprint changed since the form was loaded. Generate any required client identity server-side with `crypto/rand`; return only an opaque handle.

- [ ] **Step 5: Implement write-after-read verification**

After each write, refetch the inbound list and compare requested name, port, enabled state, quota, expiry or deletion. Map a successful HTTP response with mismatched state to `verification_failed`.

- [ ] **Step 6: Verify commands**

Run: `go test ./internal/adapters/xui -race -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/xui
git commit -m "feat: add verified 3x-ui management commands"
```

---

### Task 5: 受管 Xray outbound 与 routing rule 补丁器

**Files:**
- Create: `internal/bindings/xray_patch.go`
- Create: `internal/bindings/xray_patch_test.go`

**Interfaces:**
- Produces: `bindings.ApplyManagedBinding(raw json.RawMessage, command BindingCommand) (updated json.RawMessage, snapshot ManagedSnapshot, err error)`
- Produces: `bindings.RestoreManagedSnapshot(raw json.RawMessage, snapshot ManagedSnapshot) (json.RawMessage, error)`
- Produces: managed outbound tags `agw-aimili-main` and `agw-aimili-slot-{n}`

- [ ] **Step 1: Write failing pure patch tests**

Use synthetic Xray JSON. Cover preserving unrelated outbounds/rules byte-equivalently after normalization, replacing only `agw-` entries, binding and unbinding, duplicate managed rules, malformed config, missing arrays, slot number bounds, idempotence, and snapshot restoration.

```go
func TestPatchPreservesExpertRules(t *testing.T) {
    before := fixtureWithExpertAndManagedRules(t)
    slot := 2
    command := BindingCommand{InboundTag: "inbound-a", ExitType: ExitSlot, SlotNumber: &slot}
    after, _, err := ApplyManagedBinding(before, command)
    if err != nil { t.Fatal(err) }
    assertExpertRulesUnchanged(t, before, after)
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/bindings -run Patch -v`

Expected: FAIL because the patcher does not exist.

- [ ] **Step 3: Implement deterministic managed patching**

Identify managed outbounds by exact `agw-` tag prefix. Identify managed routing rules only when `outboundTag` uses that prefix; do not add nonstandard Xray fields. Sort only newly generated managed entries and preserve unrelated array order.

Use these exact command types:

```go
type ExitType string

const (
    ExitDirect ExitType = "direct"
    ExitMain   ExitType = "aimili_main"
    ExitSlot   ExitType = "aimili_slot"
)

type BindingCommand struct {
    InboundTag string
    ExitType   ExitType
    SlotNumber *int
}

type ManagedSnapshot struct {
    Outbounds         []json.RawMessage
    Rules             []json.RawMessage
    ConfigFingerprint [32]byte
}
```

- [ ] **Step 4: Implement snapshot and restore**

Snapshot only the previous managed outbounds and rules plus a SHA-256 fingerprint of the full pre-write config. Restore replaces current managed entries while preserving unrelated current entries; refuse restore if unrelated portions changed unexpectedly.

- [ ] **Step 5: Verify idempotence and preservation**

Run: `go test ./internal/bindings -race -v`

Expected: PASS; applying the same binding twice yields equivalent JSON and no duplicate rules.

- [ ] **Step 6: Commit**

```bash
git add internal/bindings/xray_patch*
git commit -m "feat: add owned Xray routing patcher"
```

---

### Task 6: 绑定持久化与跨服务编排

**Files:**
- Create: `internal/store/migrations/002_bindings.sql`
- Create: `internal/store/bindings.go`
- Create: `internal/store/bindings_test.go`
- Create: `internal/bindings/orchestrator.go`
- Create: `internal/bindings/orchestrator_test.go`

**Interfaces:**
- Produces: `store.Binding{InboundID, ExitType, SlotNumber, State, ConfigFingerprint}`
- Produces: exact `Orchestrator` methods shown below
- Consumes: Aimili adapter, XUI adapter, Task 5 patcher, store transactions and audit

- [ ] **Step 1: Write failing store and orchestrator tests**

Cover one binding per inbound, no client UUID column, direct/main/slot exits, missing or stopped slot, write success, write failure, verification failure with successful rollback, rollback failure, partial-failure lock, unbind, and slot dependency lookup.

```go
func TestBindLocksAfterRollbackFailure(t *testing.T) {
    env := newBindingEnvironment(t)
    env.xui.FailUpdateAt(2)
    err := env.orchestrator.Bind(context.Background(), bindCommand())
    if !errors.Is(err, ErrPartialFailure) { t.Fatalf("err=%v", err) }
    if !env.store.IsBindingLocked(t.Context(), env.inboundID) { t.Fatal("binding not locked") }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/store ./internal/bindings -run Binding -v`

Expected: FAIL because the migration and orchestrator do not exist.

- [ ] **Step 3: Implement the binding migration and methods**

Store upstream inbound stable numeric identity or equivalent stable non-secret ID, exit type, optional slot number, state, config fingerprint and timestamps. Add constraints so `slot` requires a nonnegative slot number and other exit types require NULL. Do not add tenant, role or client UUID columns.

Use these exact interfaces:

```go
type Orchestrator interface {
    Bind(ctx context.Context, command BindCommand) (store.Binding, error)
    Unbind(ctx context.Context, inboundID string) error
    List(ctx context.Context) ([]store.Binding, error)
    DependenciesForSlot(ctx context.Context, slot int) ([]store.Binding, error)
}

type BindCommand struct {
    InboundID  string
    ExitType   ExitType
    SlotNumber *int
}
```

- [ ] **Step 4: Implement the seven-step orchestration flow**

1. Fetch fresh Aimili slot and XUI inbound state.
2. Validate the selected exit and check existing partial-failure locks.
3. Fetch the latest Xray config and create a managed snapshot.
4. Apply the pure patch and call XUI update.
5. Refetch Xray config and verify managed entries.
6. Persist the binding and audit event in one local transaction.
7. On failure after step 4, attempt managed snapshot restore; lock the binding on restore failure.

- [ ] **Step 5: Implement slot dependency protection**

Expose `DependenciesForSlot(ctx context.Context, slot int) ([]store.Binding, error)`. V1-B slot stop/delete handlers must call it before upstream mutation and return HTTP 409 with opaque inbound summaries when dependencies exist.

- [ ] **Step 6: Verify orchestration and race safety**

Run: `go test ./internal/store ./internal/bindings -race -v`

Expected: PASS; two concurrent writes to the same inbound serialize and leave one verified binding.

- [ ] **Step 7: Commit**

```bash
git add internal/store internal/bindings internal/httpapi/aimili_handlers.go
git commit -m "feat: orchestrate managed exit bindings"
```

---

### Task 7: 3x-ui 与绑定业务 API

**Files:**
- Create: `internal/httpapi/xui_handlers.go`
- Create: `internal/httpapi/xui_handlers_test.go`
- Create: `internal/httpapi/binding_handlers.go`
- Create: `internal/httpapi/binding_handlers_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Produces: `/api/v1/xui/*` and `/api/v1/bindings/*`
- Consumes: XUI client, binding orchestrator, session/CSRF/reauth middleware

- [ ] **Step 1: Write failing handler tests**

Cover read-only incompatible mode, supported inbound listing, all constrained write commands, CSRF, reauthentication, stale fingerprint conflict, opaque handle mismatch, sensitive reveal no-store response, bind/unbind, slot dependency conflict, rollback failure, and audit redaction.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/httpapi -run 'XUI|Binding' -v`

Expected: FAIL because routes are not registered.

- [ ] **Step 3: Implement exact 3x-ui routes**

Expose:

```text
GET    /api/v1/xui/health
GET    /api/v1/xui/inbounds
POST   /api/v1/xui/inbounds
GET    /api/v1/xui/inbounds/{id}
PUT    /api/v1/xui/inbounds/{id}
POST   /api/v1/xui/inbounds/{id}/enable
POST   /api/v1/xui/inbounds/{id}/disable
DELETE /api/v1/xui/inbounds/{id}
POST   /api/v1/xui/inbounds/{id}/clients
PUT    /api/v1/xui/inbounds/{id}/clients/{client}
DELETE /api/v1/xui/inbounds/{id}/clients/{client}
POST   /api/v1/xui/inbounds/{id}/clients/{client}/reset-traffic
POST   /api/v1/xui/inbounds/{id}/clients/{client}/reveal
```

The reveal route requires recent reauthentication, returns `Cache-Control: no-store`, and does not emit an audit detail containing the revealed value.

- [ ] **Step 4: Implement exact binding routes**

Expose `GET /api/v1/bindings`, `PUT /api/v1/bindings/{inbound}`, `DELETE /api/v1/bindings/{inbound}`, and `GET /api/v1/aimili/exit-slots/{slot}/dependencies`. Do not expose raw Xray configuration.

- [ ] **Step 5: Verify handlers and complete backend**

Run:

```powershell
go test ./internal/httpapi ./internal/adapters/xui ./internal/bindings -race -v
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi internal/app
git commit -m "feat: expose verified 3x-ui and binding APIs"
```

---

### Task 8: 入站、客户端和出口绑定前端

**Files:**
- Create: `web/src/components/ClientTable.vue`
- Create: `web/src/components/ClientTable.spec.ts`
- Create: `web/src/components/InboundCard.vue`
- Create: `web/src/components/InboundCard.spec.ts`
- Create: `web/src/components/SensitiveReveal.vue`
- Create: `web/src/components/SensitiveReveal.spec.ts`
- Create: `web/src/views/InboundsView.vue`
- Create: `web/src/views/InboundsView.spec.ts`
- Create: `web/src/views/InboundDetailView.vue`
- Create: `web/src/views/InboundDetailView.spec.ts`
- Create: `web/src/views/BindingsView.vue`
- Create: `web/src/views/BindingsView.spec.ts`
- Modify: `web/src/router/index.ts`
- Modify: `web/src/api/client.ts`

**Interfaces:**
- Consumes: `/api/v1/xui/*`, `/api/v1/bindings/*`, Aimili slot summaries and reauthentication API
- Produces: constrained daily 3x-ui workflow and binding UI

- [ ] **Step 1: Write failing component tests**

Cover version-incompatible read-only mode, VLESS Reality-only create form, unsupported protocol expert-mode prompt, client quota/expiry validation, delete reauthentication, sensitive reveal timeout, copy without persistence, binding to direct/main/slot, dependency conflict, verification failure and partial-failure lock.

```ts
it('clears revealed connection data after sixty seconds', async () => {
  vi.useFakeTimers()
  const wrapper = mount(SensitiveReveal, { props: { loader: async () => 'sensitive-value' } })
  await wrapper.get('[data-test=reveal]').trigger('click')
  vi.advanceTimersByTime(60_000)
  await nextTick()
  expect(wrapper.text()).not.toContain('sensitive-value')
})
```

- [ ] **Step 2: Confirm the red state**

Run: `npm test --prefix web -- Inbound Client Binding SensitiveReveal`

Expected: FAIL because the views do not exist.

- [ ] **Step 3: Implement constrained inbound and client workflows**

Forms expose only reviewed template fields. Unsupported protocols remain visible but read-only with an expert-mode button. Use opaque IDs in routes and API payloads. Never store reveal data in router state, browser storage or cached query objects.

- [ ] **Step 4: Implement binding workflow**

Show current exit, available healthy exits and affected dependencies. Require explicit confirmation for unbind or fallback. Display `verification_failed` and `partial_failure` as distinct states; partial failure blocks further mutation and links to expert mode recovery instructions.

- [ ] **Step 5: Verify all frontend behavior**

Run:

```powershell
npm test --prefix web
npm run build --prefix web
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat: add 3x-ui and exit binding UI"
```

---

### Task 9: 3x-ui 运行时凭据与兼容性部署配置

**Files:**
- Modify: `deploy/systemd/aimili-gateway.service`
- Modify: `deploy/config/config.example.json`
- Modify: `deploy/deploy_contract_test.go`
- Modify: `README.md`

**Interfaces:**
- Produces: runtime 3x-ui endpoint metadata and credential-file mapping
- Consumes: verified 3x-ui auth mode and Gateway config loader

- [ ] **Step 1: Extend failing deployment contract tests**

Assert username/password are separate systemd credentials, config JSON contains only local endpoint metadata, the expert path is never printed by verification scripts, no TOTP credential exists, and disabling Gateway leaves the existing 3x-ui Caddy route unchanged.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./deploy -v`

Expected: FAIL because 3x-ui credential wiring is incomplete.

- [ ] **Step 3: Add runtime credential mapping**

Use separate `LoadCredentialEncrypted` entries for 3x-ui username and password. Pass only file paths through environment variables. Keep the 3x-ui base URL in `/etc/aimili-gateway/config.json` with strict permissions; never log it because it can contain the existing private panel path.

- [ ] **Step 4: Document compatibility states**

Document `healthy`, `read_only`, `authentication_failed`, `two_factor_unsupported`, `incompatible`, and `unavailable`. State exactly which UI controls are disabled in each state and that expert mode remains independent.

- [ ] **Step 5: Verify deployment contracts and secret scan**

Run:

```powershell
go test ./deploy -v
pwsh -NoProfile -File .\scripts\verify-v1a.ps1
git diff --check
```

Expected: PASS; no credential values, random paths or TOTP data appear in tracked files.

- [ ] **Step 6: Commit**

```bash
git add deploy README.md
git commit -m "docs: add secure 3x-ui adapter configuration"
```

---

### Task 10: V1-C 完整隔离端到端验收与生产前门槛

**Files:**
- Create: `scripts/verify-v1c.ps1`
- Create: `scripts/verify-v1c.sh`
- Create: `docs/verification/v1c.md`

**Interfaces:**
- Consumes: complete V1-A, V1-B and V1-C system
- Produces: automated verification and isolated browser-path evidence

- [ ] **Step 1: Write fail-fast verification scripts**

Run frontend tests/build first so embedded assets exist, then all Go tests with race detection, Go vet, Go builds, AimiliVPN Python tests/compilation, deployment contracts, diff checks and secret scans. Fail if Docker/isolated integration was requested but the actual integration evidence file is missing.

- [ ] **Step 2: Run the complete automated suite**

Run: `pwsh -NoProfile -File .\scripts\verify-v1c.ps1`

Expected: exit 0 with every required suite passing and no skipped contract test.

- [ ] **Step 3: Execute the full isolated user path**

Against disposable AimiliVPN and official 3x-ui `v3.6.0` instances:

1. Initialize and log in to Gateway with password and TOTP.
2. Confirm both adapters are compatible.
3. Create a temporary Reality inbound and client through Gateway.
4. Create or select a temporary AimiliVPN exit slot.
5. Bind the inbound to the slot.
6. Send test traffic through the generated test connection and verify the exit matches the selected AimiliVPN slot rather than the VPS direct exit.
7. Reset traffic, disable/enable the client, and verify state after each action.
8. Reveal connection information, confirm no-cache behavior, then confirm it disappears from the UI after sixty seconds.
9. Open expert mode and confirm 3x-ui requires its own login.
10. Stop Gateway and confirm AimiliVPN, 3x-ui, Xray and the expert route remain running.

- [ ] **Step 4: Exercise failure and rollback paths**

Repeat binding with injected 3x-ui update failure, verification mismatch and rollback failure. Confirm each produces the expected stable error, unrelated expert rules remain unchanged, and rollback failure creates a partial-failure lock.

- [ ] **Step 5: Exercise compatibility downgrade**

Point the adapter at a fixture with an unknown version or missing field. Confirm reads degrade as designed, every 3x-ui write control is disabled, AimiliVPN remains usable, and expert mode still opens.

- [ ] **Step 6: Record redacted evidence**

`docs/verification/v1c.md` records versions, commit IDs, test counts, timestamps, state transitions, masked endpoints and pass/fail results. It must not contain generated credentials, client UUIDs, private keys, random paths or complete connection information.

- [ ] **Step 7: Commit**

```bash
git add scripts docs/verification/v1c.md
git commit -m "test: verify unified console end to end"
```

## V1-C Completion Gate

V1-C is complete only after the automated suite and disposable end-to-end flow both pass with fresh evidence. Production remains a separate authorized phase: before any `ny` action, perform a new read-only baseline, confirm actual 3x-ui version and authentication mode, create a fresh backup, present the exact deployment and rollback scope, and obtain explicit approval.
