# Aimili Gateway V1-B AimiliVPN 管理 Implementation Plan

> **状态：已废止，不得执行。** 用户于 2026-08-25 将后续目标修订为国家代理目录和 VLESS＋mixed 成对编排。本计划被 `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md` 取代；新设计书面批准后重新编写实施计划。

> 本文件只作历史记录，不得调用实施计划流程或执行其中任何复选步骤。

**Goal:** 在 V1-A 可运行控制台上增加版本化 AimiliVPN 控制 API、完整的 AimiliVPN 适配器、节点与路由管理、多出口管理及每次写操作后的真实状态验证。

**Architecture:** AimiliVPN Fork 新增一个最小 `control_api.py`，通过现有 HTTP 服务暴露仅回环可达、独立令牌认证的 `/control/v1` 接口；它只调用现有节点、路由和槽位函数，不复制或重写隧道核心。Aimili Gateway 使用类型化 AimiliVPN Adapter 把该接口转换为统一领域 API，浏览器仍只访问 `/api/v1`。

**Tech Stack:** Python 3.10+ 标准库、AimiliVPN 现有 `ThreadingHTTPServer`、Go 1.26.x、Go `net/http`、Vue 3.5.18、Vitest 3.2.4、现有 SQLite 控制台存储。

**Spec:** `docs/superpowers/specs/2026-08-24-unified-console-design.md`

## Global Constraints

- 必须先完成并新鲜验证 V1-A；本计划不重新设计认证、会话或前端入口。
- AimiliVPN、Aimili Gateway 是两个独立进程，不能合并 Python 与 Go 运行时。
- 不重写 OpenVPN、代理、多出口或节点池核心；新增控制 API 只做认证、参数验证、字段白名单和现有函数调用。
- 控制 API 默认关闭；只有配置了回环监听和独立令牌文件时才启用。
- 控制 API 不复用 AimiliVPN 原生网页账户、Cookie、秘密路径或 30 天会话。
- 控制 API 和 Gateway API 都不得返回管理用户名、网页路径、OpenVPN 配置正文或其他秘密字段。
- 所有状态修改必须在返回成功前重新读取目标状态；仅收到 HTTP 2xx 不算完成。
- AimiliVPN 的 `main` 继续跟踪上游，定制只提交到 `custom`。
- 不修改或部署生产 VPS；生产令牌、systemd drop-in 和 Caddy 变更需要后续新的明确授权。
- 每个仓库分别保持可构建、可测试和可回滚，不能把跨仓库半成品放在同一个提交中。

---

## File Structure

在 `D:\CodexProject\Github\aimili-vpngate`：

```text
control_api.py
tests/test_control_api_auth.py
tests/test_control_api_contract.py
tests/test_control_api_nodes.py
tests/test_control_api_slots.py
vpngate_manager.py
README.md
```

在 `D:\CodexProject\Github\aimili-gateway`：

```text
internal/adapters/aimili/client.go
internal/adapters/aimili/client_test.go
internal/domain/aimili.go
internal/httpapi/aimili_handlers.go
internal/httpapi/aimili_handlers_test.go
web/src/views/AimiliNodesView.vue
web/src/views/AimiliRoutingView.vue
web/src/views/ExitSlotsView.vue
web/src/components/NodeTable.vue
web/src/components/ExitSlotCard.vue
deploy/systemd/aimilivpn-control.conf
docs/verification/v1b.md
scripts/verify-v1b.ps1
scripts/verify-v1b.sh
```

---

## Control API Contract

所有端点要求独立 Bearer 服务令牌，使用 `application/json`，响应包含 `apiVersion: "1.0"`，并设置 `Cache-Control: no-store`。

| Method | Path | Capability | Purpose |
|---|---|---|---|
| GET | `/control/v1/capabilities` | `probe` | API 版本、服务版本标识和能力集合 |
| GET | `/control/v1/health` | `health.read` | 服务、主隧道、代理和节点维护状态 |
| GET | `/control/v1/nodes` | `nodes.read` | 白名单节点列表和路由状态 |
| POST | `/control/v1/nodes/refresh` | `nodes.refresh` | 启动节点刷新 |
| POST | `/control/v1/nodes/{id}/test` | `nodes.test` | 检测单节点 |
| POST | `/control/v1/nodes/{id}/connect` | `nodes.connect` | 连接指定节点 |
| POST | `/control/v1/disconnect` | `nodes.disconnect` | 断开主连接 |
| GET | `/control/v1/routing` | `routing.read` | 当前路由模式和过滤条件 |
| PUT | `/control/v1/routing` | `routing.write` | 更新路由模式和过滤条件 |
| GET | `/control/v1/exit-slots` | `slots.read` | 槽位配置和状态 |
| POST | `/control/v1/exit-slots` | `slots.create` | 新增一个槽位 |
| PUT | `/control/v1/exit-slots/config` | `slots.configure` | 更新全局槽位配置 |
| POST | `/control/v1/exit-slots/{slot}/start` | `slots.start` | 启动槽位 |
| POST | `/control/v1/exit-slots/{slot}/stop` | `slots.stop` | 停止槽位 |
| POST | `/control/v1/exit-slots/{slot}/switch` | `slots.switch` | 更换槽位节点 |
| PUT | `/control/v1/exit-slots/{slot}/filters` | `slots.configure` | 更新单槽位国家和 ISP |
| PUT | `/control/v1/exit-slots/{slot}/node` | `slots.assign` | 指派节点 |
| DELETE | `/control/v1/exit-slots/{slot}` | `slots.delete` | 删除槽位 |
| POST | `/control/v1/proxy/check` | `proxy.check` | 检测真实代理出口 |
| GET | `/control/v1/logs?limit=200` | `logs.read` | 返回最多 200 条脱敏日志 |

---

### Task 1: AimiliVPN 控制契约与字段白名单

**Files:**
- Create: `../aimili-vpngate/control_api.py`
- Create: `../aimili-vpngate/tests/test_control_api_contract.py`

**Interfaces:**
- Produces: `ControlResponse(status: int, payload: dict[str, Any])`
- Produces: `ControlBackend` protocol used by all later Aimili control handlers
- Produces: `serialize_node`, `serialize_health`, `serialize_slot`, `serialize_log`

- [ ] **Step 1: Write failing serializer and route-contract tests**

Create synthetic inputs containing both allowed and forbidden fields. Tests must prove forbidden fields never survive:

```python
def test_serialize_node_uses_allowlist():
    raw = {
        "id": "node-a", "ip": "203.0.113.1", "country_short": "JP",
        "config_text": "forbidden", "config_file": "forbidden.ovpn",
    }
    result = serialize_node(raw)
    assert result == {"id": "node-a", "ip": "203.0.113.1", "country": "JP"}
    assert "config_text" not in result
```

Also assert the router recognizes every method/path in the contract table and returns 404 for native UI paths.

- [ ] **Step 2: Confirm the red state**

Run from `aimili-vpngate`:

```powershell
python -m unittest tests.test_control_api_contract -v
```

Expected: FAIL because `control_api` does not exist.

- [ ] **Step 3: Define the backend protocol and response envelope**

Create exact protocol methods:

```python
@dataclass(frozen=True)
class ControlResponse:
    status: int
    payload: dict[str, Any]

class ControlBackend(Protocol):
    def health(self) -> dict[str, Any]: ...
    def nodes(self) -> list[dict[str, Any]]: ...
    def refresh_nodes(self) -> dict[str, Any]: ...
    def test_node(self, node_id: str) -> dict[str, Any]: ...
    def connect_node(self, node_id: str) -> dict[str, Any]: ...
    def disconnect(self) -> dict[str, Any]: ...
    def get_routing(self) -> dict[str, Any]: ...
    def set_routing(self, command: dict[str, Any]) -> dict[str, Any]: ...
    def get_slots(self) -> dict[str, Any]: ...
    def slot_command(self, action: str, slot: int, command: dict[str, Any]) -> dict[str, Any]: ...
    def check_proxy(self) -> dict[str, Any]: ...
    def logs(self, limit: int) -> list[dict[str, Any]]: ...
```

All successful responses use `{"ok": true, "apiVersion": "1.0", "data": ...}`. All failures use a stable `error.code` and a redacted Chinese `error.message`.

- [ ] **Step 4: Implement strict serializers**

Serialize only fields required by the unified console. Exclude native UI username, password state, secret path, OpenVPN text, local file paths, environment variables, stack traces and raw exception text.

- [ ] **Step 5: Verify contract tests**

Run: `python -m unittest tests.test_control_api_contract -v`

Expected: PASS.

- [ ] **Step 6: Commit in the AimiliVPN repository**

```bash
git add control_api.py tests/test_control_api_contract.py
git commit -m "feat: define versioned Aimili control contract"
```

---

### Task 2: 独立服务令牌认证与 HTTP 桥接

**Files:**
- Modify: `../aimili-vpngate/control_api.py`
- Create: `../aimili-vpngate/tests/test_control_api_auth.py`
- Modify: `../aimili-vpngate/vpngate_manager.py`

**Interfaces:**
- Produces: `load_control_token(path: str) -> bytes`
- Produces: `is_control_authorized(headers: Mapping[str, str], token: bytes) -> bool`
- Produces: `handle_control_request(handler, backend, token) -> bool`
- Consumes: existing `Handler` and control contract

- [ ] **Step 1: Write failing authentication tests**

Cover missing token file, non-regular file, permissive Unix permissions, missing header, wrong scheme, wrong token, valid token, request body larger than 64 KiB, and non-loopback bind configuration.

```python
def auth_headers(token: str) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}

def test_authorization_uses_exact_bearer_token():
    expected = secrets.token_urlsafe(32)
    wrong = expected[:-1] + ("A" if expected[-1] != "A" else "B")
    assert is_control_authorized(auth_headers(expected), expected.encode("ascii"))
    assert not is_control_authorized(auth_headers(wrong), expected.encode("ascii"))
```

- [ ] **Step 2: Confirm the red state**

Run: `python -m unittest tests.test_control_api_auth -v`

Expected: FAIL because token loading and authorization are absent.

- [ ] **Step 3: Implement fail-closed token loading**

Read `AIMILI_CONTROL_TOKEN_FILE`. If unset, missing, empty, shorter than 32 bytes, not a regular file, or group/world-readable on Linux, leave the control API disabled and emit one redacted startup warning. Never generate or print a token inside AimiliVPN.

- [ ] **Step 4: Implement constant-time Bearer authorization**

Parse exactly one `Authorization` header, require the `Bearer` scheme, compare bytes with `hmac.compare_digest`, and return generic HTTP 401 without distinguishing missing and incorrect tokens.

- [ ] **Step 5: Add the minimal Handler bridge**

Before native secret-path validation, inspect `urlsplit(self.path).path`. Dispatch only paths beginning `/control/v1/` to `handle_control_request`; leave all existing native UI behavior unchanged. Cap JSON request bodies at 64 KiB and set `Cache-Control: no-store` on every response.

- [ ] **Step 6: Verify native UI regression safety**

Run:

```powershell
python -m unittest tests.test_control_api_auth tests.test_project_contract -v
python -m py_compile control_api.py vpngate_manager.py
```

Expected: PASS; native UI routes still use their existing authentication, and the control API is unavailable without a valid token file.

- [ ] **Step 7: Commit in the AimiliVPN repository**

```bash
git add control_api.py vpngate_manager.py tests/test_control_api_auth.py
git commit -m "feat: secure Aimili control API"
```

---

### Task 3: 健康、节点和路由控制端点

**Files:**
- Modify: `../aimili-vpngate/control_api.py`
- Modify: `../aimili-vpngate/vpngate_manager.py`
- Create: `../aimili-vpngate/tests/test_control_api_nodes.py`

**Interfaces:**
- Produces: capability and endpoints through `routing.write`
- Consumes: existing `read_nodes`, `get_state`, `maintain_valid_nodes`, `test_node_by_id`, `connect_node`, `stop_active_openvpn`, and routing config functions

- [ ] **Step 1: Write failing endpoint tests with a fake backend**

Tests must cover health serialization, node list filtering, refresh already running, unknown node, invalid routing mode, successful connect/disconnect, and write-after-read verification.

```python
def test_set_routing_rejects_unknown_mode():
    response = api.request("PUT", "/control/v1/routing", {"mode": "raw-xray"})
    assert response.status == 400
    assert response.payload["error"]["code"] == "invalid"
```

- [ ] **Step 2: Confirm the red state**

Run: `python -m unittest tests.test_control_api_nodes -v`

Expected: FAIL because endpoint dispatch is incomplete.

- [ ] **Step 3: Implement read endpoints and capabilities**

Return a sorted capability list and stable status fields. Node output includes only stable ID, display IP, country, score, latency, speed, ISP, type, favorite, availability and active state.

- [ ] **Step 4: Implement node commands**

Validate node IDs against the current node list before invoking operations. Refresh returns HTTP 202 while the maintenance thread runs. Connect, disconnect and test operations must re-read state before returning `ok: true`.

- [ ] **Step 5: Implement routing commands**

Accept only modes `auto`, `fixed_ip`, `fixed_region`, and `favorites`; accept only IP types `all`, `residential`, and `hosting`; normalize country codes to uppercase; cap ISP filter length at 128 characters. Re-read routing configuration and return `verification_failed` if the requested fields differ.

- [ ] **Step 6: Verify endpoints and existing node-pool behavior**

Run:

```powershell
python -m unittest tests.test_control_api_nodes -v
python -m unittest discover -s tests -v
python -m py_compile control_api.py vpngate_manager.py
```

Expected: all AimiliVPN tests PASS; no existing node-pool test is skipped.

- [ ] **Step 7: Commit in the AimiliVPN repository**

```bash
git add control_api.py vpngate_manager.py tests/test_control_api_nodes.py
git commit -m "feat: expose Aimili node and routing controls"
```

---

### Task 4: 多出口、代理检测和脱敏日志端点

**Files:**
- Modify: `../aimili-vpngate/control_api.py`
- Modify: `../aimili-vpngate/vpngate_manager.py`
- Create: `../aimili-vpngate/tests/test_control_api_slots.py`
- Modify: `../aimili-vpngate/README.md`

**Interfaces:**
- Produces: remaining control contract endpoints and capabilities
- Consumes: existing slot lifecycle, filter, assignment, proxy check and log functions

- [ ] **Step 1: Write failing slot and log tests**

Cover valid and out-of-range slot numbers, maximum slot count, start/stop/delete/switch, filter normalization, unknown node assignment, proxy check success/failure, log limit bounds, and removal of sensitive log patterns.

```python
def test_logs_are_limited_and_redacted():
    backend.log_rows = synthetic_rows_with_sensitive_fields(250)
    response = api.request("GET", "/control/v1/logs?limit=200")
    assert len(response.payload["data"]) == 200
    assert not contains_sensitive_pattern(response.payload)
```

- [ ] **Step 2: Confirm the red state**

Run: `python -m unittest tests.test_control_api_slots -v`

Expected: FAIL because slot and log routing is absent.

- [ ] **Step 3: Implement slot commands with dependency-free validation**

Use existing `MAX_EXIT_SLOTS`; parse slot numbers with strict decimal syntax; cap country and ISP filters; return HTTP 409 for lifecycle conflicts. After every command, re-read `get_exit_slot_config` and the slot snapshot to verify the expected state.

- [ ] **Step 4: Implement proxy health and log reads**

Proxy check may perform the existing outbound check but returns only boolean health, latency, a masked exit-address summary and a stable error code. Logs accept limits 1–200 and whitelist timestamp, level, module and a redacted message.

- [ ] **Step 5: Document the control API boundary**

Update the AimiliVPN README to state that the control API is optional, disabled without an independent token file, loopback-only, versioned, and not a replacement for native UI authentication. Do not include a real token or deployed URL.

- [ ] **Step 6: Run the full AimiliVPN verification**

Run:

```powershell
python -m unittest discover -s tests -v
python -m py_compile control_api.py vpngate_manager.py vpn_utils.py proxy_server.py
git diff --check
```

Expected: all tests PASS with zero failures.

- [ ] **Step 7: Commit in the AimiliVPN repository**

```bash
git add control_api.py vpngate_manager.py tests/test_control_api_slots.py README.md
git commit -m "feat: complete Aimili control API"
```

---

### Task 5: Gateway AimiliVPN 领域模型和 HTTP 客户端

**Files:**
- Create: `internal/domain/aimili.go`
- Create: `internal/adapters/aimili/client.go`
- Create: `internal/adapters/aimili/client_test.go`
- Modify: `internal/adapters/contracts.go`

**Interfaces:**
- Produces: `aimili.Client` methods `Capabilities`, `Health`, `Nodes`, `RefreshNodes`, `TestNode`, `ConnectNode`, `Disconnect`, `Routing`, `UpdateRouting`, `ExitSlots`, `CreateExitSlot`, `ConfigureExitSlots`, `ExitSlotCommand`, `CheckProxy`, and `Logs`
- Produces: domain types `AimiliHealth`, `Node`, `Routing`, `ExitSlot`, `AimiliLog`
- Consumes: V1-A probe contract, `http.Client`, and token bytes supplied at runtime

- [ ] **Step 1: Write failing client contract tests against `httptest.Server`**

Cover Bearer header presence without logging it, API version mismatch, missing capability, timeout, 401, 409, malformed JSON, unknown extra fields, missing required fields, and a successful node list.

```go
func TestClientRejectsUnsupportedAPIVersion(t *testing.T) {
    server := newControlFixture(t, `{"ok":true,"apiVersion":"2.0","data":{}}`)
    client := NewClient(server.URL, fixtureToken(t), server.Client())
    _, err := client.Capabilities(context.Background())
    if !errors.Is(err, adapters.ErrIncompatible) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/adapters/aimili -v`

Expected: FAIL because the client and domain types do not exist.

- [ ] **Step 3: Implement typed models and strict decoding**

Use JSON DTOs inside the adapter and map them to domain types. Require API major version 1. Sort capabilities and expose them as a set. Ignore unknown JSON fields for forward compatibility but reject missing identifiers, invalid slot numbers and impossible enum values.

- [ ] **Step 4: Implement authenticated HTTP methods**

Use one client with five-second default timeout, context cancellation, disabled redirects and connection reuse. Limit response bodies to 2 MiB. Map stable upstream error codes to gateway sentinel errors and never include raw response bodies in errors.

- [ ] **Step 5: Verify client behavior**

Run: `go test ./internal/adapters/aimili -race -v`

Expected: PASS, including timeout and secret-redaction assertions.

- [ ] **Step 6: Commit in the Gateway repository**

```bash
git add internal/domain internal/adapters
git commit -m "feat: add typed Aimili adapter client"
```

---

### Task 6: Gateway AimiliVPN 业务 API 与写后验证

**Files:**
- Create: `internal/httpapi/aimili_handlers.go`
- Create: `internal/httpapi/aimili_handlers_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/app/app.go`
- Modify: `internal/store/audit.go`

**Interfaces:**
- Consumes: authenticated session, CSRF, reauthentication, `aimili.Client`
- Produces: `/api/v1/aimili/*` browser API

- [ ] **Step 1: Write failing handler tests with a fake Aimili client**

Cover all read routes, CSRF on mutations, capability denial, refresh HTTP 202, invalid routing input, destructive slot actions requiring recent reauthentication, verification failure, timeout mapping, and audit redaction.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./internal/httpapi -run Aimili -v`

Expected: FAIL because handlers are not registered.

- [ ] **Step 3: Implement exact browser routes**

Expose:

```text
GET    /api/v1/aimili/health
GET    /api/v1/aimili/nodes
POST   /api/v1/aimili/nodes/refresh
POST   /api/v1/aimili/nodes/{id}/test
POST   /api/v1/aimili/nodes/{id}/connect
POST   /api/v1/aimili/disconnect
GET    /api/v1/aimili/routing
PUT    /api/v1/aimili/routing
GET    /api/v1/aimili/exit-slots
POST   /api/v1/aimili/exit-slots
PUT    /api/v1/aimili/exit-slots/config
POST   /api/v1/aimili/exit-slots/{slot}/{action}
PUT    /api/v1/aimili/exit-slots/{slot}/filters
PUT    /api/v1/aimili/exit-slots/{slot}/node
DELETE /api/v1/aimili/exit-slots/{slot}
POST   /api/v1/aimili/proxy/check
GET    /api/v1/aimili/logs
```

Do not create a generic proxy endpoint.

- [ ] **Step 4: Enforce capability and verification gates**

Before every write, fetch capabilities and current state without cache. After the write, fetch the target state again and compare only requested fields. Return HTTP 502 with `verification_failed` if upstream claims success but state differs.

- [ ] **Step 5: Add audit events**

Record action, resource type, opaque resource fingerprint, result and stable error category. Never store node configuration, service token, full IP where masking is configured, or raw upstream bodies.

- [ ] **Step 6: Verify all Gateway tests**

Run:

```powershell
go test ./internal/httpapi ./internal/adapters/aimili -race -v
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit in the Gateway repository**

```bash
git add internal/httpapi internal/app internal/store/audit.go
git commit -m "feat: expose verified Aimili management API"
```

---

### Task 7: AimiliVPN 节点、路由和槽位前端

**Files:**
- Create: `web/src/components/NodeTable.vue`
- Create: `web/src/components/NodeTable.spec.ts`
- Create: `web/src/components/ExitSlotCard.vue`
- Create: `web/src/components/ExitSlotCard.spec.ts`
- Create: `web/src/views/AimiliNodesView.vue`
- Create: `web/src/views/AimiliNodesView.spec.ts`
- Create: `web/src/views/AimiliRoutingView.vue`
- Create: `web/src/views/AimiliRoutingView.spec.ts`
- Create: `web/src/views/ExitSlotsView.vue`
- Create: `web/src/views/ExitSlotsView.spec.ts`
- Modify: `web/src/router/index.ts`
- Modify: `web/src/api/client.ts`

**Interfaces:**
- Consumes: `/api/v1/aimili/*`
- Produces: daily AimiliVPN management UI

- [ ] **Step 1: Write failing component tests**

Cover node filtering, refresh-in-progress state, connect/disconnect confirmation, routing enum validation, slot dependency warning placeholder supplied by API, reauthentication prompt for delete, unavailable capability disabling, and no sensitive value persistence.

- [ ] **Step 2: Confirm the red state**

Run: `npm test --prefix web -- Aimili ExitSlot NodeTable`

Expected: FAIL because the components and routes do not exist.

- [ ] **Step 3: Implement node and routing views**

Use server-provided stable node IDs as keys. Keep filters in component memory or non-sensitive URL query parameters. Show refresh as background activity and distinguish connection requested, connecting, connected and verification failed.

- [ ] **Step 4: Implement exit-slot views**

Render stable slot numbers, lifecycle, filter, node summary and proxy health. Require explicit confirmation for stop/delete and call `/api/v1/auth/reauth` before delete. Disable actions not declared by capabilities.

- [ ] **Step 5: Verify frontend behavior**

Run:

```powershell
npm test --prefix web
npm run build --prefix web
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit in the Gateway repository**

```bash
git add web/src
git commit -m "feat: add Aimili management UI"
```

---

### Task 8: Aimili 控制令牌的部署边界

**Files:**
- Create: `deploy/systemd/aimilivpn-control.conf`
- Modify: `deploy/systemd/aimili-gateway.service`
- Modify: `deploy/config/config.example.json`
- Modify: `deploy/deploy_contract_test.go`
- Modify: `README.md`

**Interfaces:**
- Produces: runtime credential mapping for AimiliVPN and Gateway
- Consumes: `AIMILI_CONTROL_TOKEN_FILE` and Gateway credential-file configuration

- [ ] **Step 1: Extend failing deployment contract tests**

Assert one encrypted credential is loaded into both services at distinct runtime paths, AimiliVPN receives only `AIMILI_CONTROL_TOKEN_FILE`, Gateway receives only its adapter token path, neither unit contains a token value, and both management listeners remain loopback-only.

- [ ] **Step 2: Confirm the red state**

Run: `go test ./deploy -v`

Expected: FAIL because the drop-in and token wiring do not exist.

- [ ] **Step 3: Add systemd credential wiring**

The drop-in must use `LoadCredentialEncrypted` and set the token-file environment variable to the systemd credential directory. It may restart AimiliVPN only when the operator explicitly installs the drop-in; creating the file in the repository performs no service action.

- [ ] **Step 4: Document token creation without exposing it**

Document an operator procedure using `systemd-creds encrypt` with random stdin, strict terminal handling, and no shell history. Do not put an example token, complete deployed path, or production command output in the repository.

- [ ] **Step 5: Verify deployment assets**

Run:

```powershell
go test ./deploy -v
rg -n "AIMILI_CONTROL_TOKEN_FILE|LoadCredentialEncrypted" deploy README.md
git diff --check
```

Expected: contract tests PASS and searches show only configuration keys, not values.

- [ ] **Step 6: Commit in the Gateway repository**

```bash
git add deploy README.md
git commit -m "docs: wire Aimili adapter credentials"
```

---

### Task 9: V1-B 双仓库验证与本地用户路径

**Files:**
- Create: `scripts/verify-v1b.ps1`
- Create: `scripts/verify-v1b.sh`
- Create: `docs/verification/v1b.md`

**Interfaces:**
- Consumes: complete V1-B changes in both repositories
- Produces: one-command verification and redacted evidence

- [ ] **Step 1: Write fail-fast cross-repository verification scripts**

The scripts must verify the expected AimiliVPN `custom` branch, run all Python tests and compilation, run all Gateway Go and Vue tests/builds, run deployment contract tests, run secret-pattern scans, and print both Git HEAD values without printing remote URLs or credentials.

- [ ] **Step 2: Run the complete automated verification**

Run from `aimili-gateway`:

```powershell
pwsh -NoProfile -File .\scripts\verify-v1b.ps1
```

Expected: exit 0; all AimiliVPN and Gateway tests pass with no skips caused by code failures.

- [ ] **Step 3: Run an isolated local integration**

Start AimiliVPN with a temporary control token and data directory in an isolated local/Linux environment, start the Gateway with the same runtime credential, then verify through the browser:

1. Node and routing state loads.
2. A harmless test node action reports a verified result.
3. A temporary test slot can be created, inspected and deleted.
4. Stopping AimiliVPN changes only the Aimili status card.
5. The native AimiliVPN user credential is never requested by Gateway.

Do not use production VPS or production data.

- [ ] **Step 4: Record redacted evidence**

Write test counts, tool versions, local timestamps, commit IDs, state transitions and error categories to `docs/verification/v1b.md`. Do not record node configuration text, full service token, native UI path or complete connection information.

- [ ] **Step 5: Verify each repository is clean**

Run:

```powershell
git -C ..\aimili-vpngate status --short
git status --short
```

Expected: both outputs empty after committed verification evidence.

- [ ] **Step 6: Commit the Gateway verification artifacts**

```bash
git add scripts docs/verification/v1b.md
git commit -m "test: verify Aimili gateway management"
```

## V1-B Completion Gate

V1-B is complete only when both repositories are clean, all tests pass with fresh output, and the isolated browser path confirms write-after-read behavior. It does not authorize `ny`, 3x-ui write access, Caddy production changes or V1-C deployment.
