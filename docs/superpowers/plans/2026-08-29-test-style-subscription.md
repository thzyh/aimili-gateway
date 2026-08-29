# Test 风格 VLESS 订阅 Implementation Plan

状态：已执行；最终证据见 `docs/verification/2026-08-29-test-style-subscription.md`。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Gateway 的“复制单地址聚合入口”改为 3x-ui `test` 风格的多入站 VLESS 订阅，并完成主连接检测、出口位替换和旧聚合资源安全迁移。

**Architecture:** Gateway 继续作为 Go 控制面，AimiliVPN 继续作为独立 Python 服务，3x-ui/Xray 继续作为独立数据面。Gateway 通过 3x-ui 适配器维护一个专用订阅客户端与多个 VLESS 入站的关联；通过 AimiliVPN 版本化控制 API 将候选节点装载到指定槽位。旧 `21000` balancer 只在备份、订阅验证和无引用检查通过后清理。

**Tech Stack:** Go 1.26、SQLite、Python 3、3x-ui HTTP API、Vue 3 + TypeScript、Vite、现有 Xray 验证器。

**Spec:** `docs/superpowers/specs/2026-08-29-test-style-subscription-design.md`

## Global Constraints

- 不重写 Xray 或 3x-ui 核心，不删除 3x-ui 源码功能。
- AimiliVPN、Gateway、3x-ui 保持独立服务和独立更新。
- 不使用多租户模型。
- 不输出密码、Cookie、UUID、私钥或完整订阅链接。
- 订阅地址只在认证后的主动复制/导出请求中返回，不写入浏览器持久存储、数据库或日志。
- 旧聚合资源只在完成 SQLite 备份、订阅验证、无引用检查后删除；任一阶段失败立即保留原资源并回滚。
- 主连接不执行普通槽位轮换；候选替换失败必须恢复原节点，恢复失败才进入 `repair_required`。

---

### Task 1: 建立 AimiliVPN assign 控制 API

**Files:**
- Modify: `D:/CodexProject/Github/aimili-vpngate/control_api.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/vpngate_manager.py`
- Test: `D:/CodexProject/Github/aimili-vpngate/tests/test_control_api.py`

**Interfaces:**
- Consumes: 现有 `assign_node_to_slot(slot, node_id)`。
- Produces: `POST /control/v1/slots/{slot}/assign`，请求 `{candidateId, country, proxyType}`，响应槽位安全快照；新增 capability `slots.assign`。

- [ ] **Step 1: Write the failing API tests**

在 `tests/test_control_api.py` 增加：未授权返回 401；非法 candidateId 返回 400；有效请求调用 manager assign 并返回不含敏感字段的槽位快照；manager 返回 `slot_not_found` 时返回 404。

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `python -m pytest D:/CodexProject/Github/aimili-vpngate/tests/test_control_api.py -q`

Expected: 新增 assign 测试失败，因为路由和 manager 方法尚不存在。

- [ ] **Step 3: Implement the minimal manager and route**

在 `vpngate_manager.py` 增加 `assign_managed_slot(slot, candidate_id, country='', proxy_type='')`：校验槽位活动、候选可用、节点未被其他槽位使用；在受管锁内调用现有 `assign_node_to_slot`；需要跨分类时同步槽位国家/IP 类型配置并在失败时恢复旧配置。  
在 `control_api.py` 增加 capability、`_slot_route` 的 `assign` 动作和 JSON 字段白名单，调用 manager 方法并复用 `_manager_result`。

- [ ] **Step 4: Run tests and verify pass**

Run: `python -m pytest D:/CodexProject/Github/aimili-vpngate/tests/test_control_api.py -q`

Expected: 全部通过。

- [ ] **Step 5: Commit**

```bash
git -C D:/CodexProject/Github/aimili-vpngate add control_api.py vpngate_manager.py tests/test_control_api.py
git -C D:/CodexProject/Github/aimili-vpngate commit -m "feat: expose managed slot candidate assignment"
```

### Task 2: 扩展 Go Aimili 适配器和订阅/主连接数据模型

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/aimili/client.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/aimili/client_test.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/aimili/client.go` interfaces as needed
- Create: `D:/CodexProject/Github/aimili-gateway/internal/store/migrations/008_subscription_and_main_probe.sql`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/store/main_egress.go`
- Create: `D:/CodexProject/Github/aimili-gateway/internal/store/subscription.go`
- Test: `D:/CodexProject/Github/aimili-gateway/internal/store/*_test.go`

**Interfaces:**
- Consumes: AimiliVPN `/control/v1/slots/{slot}/assign`。
- Produces: `AssignSlotNode(ctx, slot, candidateID, country, proxyType) (Slot, error)`；订阅客户端元数据存取；主连接探测字段存取。

- [ ] **Step 1: Add failing adapter and store tests**

测试 assign 请求路径、字段校验和响应解包；测试迁移后订阅元数据幂等保存；测试主连接延迟与错误字段可读写。

- [ ] **Step 2: Run focused Go tests and verify failure**

Run: `go test ./internal/adapters/aimili ./internal/store`

Expected: 编译失败，缺少 assign 方法、订阅 store 类型和 migration 字段。

- [ ] **Step 3: Implement adapter and schema**

在 Aimili 客户端新增 assign 方法并严格限制 candidateId 长度、国家码和 proxyType；新增订阅状态表（单行资源名、3x-ui client id/sub id 元数据、更新时间）；扩展 `main_egress` 保存 candidate/VLESS/SOCKS 延迟、检测时间、错误码。敏感地址仅以内存值传递，不写入日志。

- [ ] **Step 4: Run focused tests and verify pass**

Run: `go test ./internal/adapters/aimili ./internal/store`

Expected: 全部通过。

- [ ] **Step 5: Commit**

```bash
git -C D:/CodexProject/Github/aimili-gateway add internal/adapters/aimili internal/store
git -C D:/CodexProject/Github/aimili-gateway commit -m "feat: add slot assignment and probe storage"
```

### Task 3: 实现 3x-ui 多入站订阅客户端适配器

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/models.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/client.go`
- Create: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/subscription.go`
- Test: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/subscription_test.go`

**Interfaces:**
- Consumes: Gateway 当前 VLESS 入站快照、主连接 `aimili-reality`、受管 `agw-*-vless` 标签。
- Produces: `EnsureSubscriptionClient(ctx, desired) (Subscription, error)`；`SubscriptionURL(ctx, subscription) (string, error)`；旧聚合资源所有权检查和删除方法。

- [ ] **Step 1: Write failing adapter tests**

覆盖：创建专用订阅客户端；四个 VLESS 入站关联为一个客户端；mixed 入站被排除；重复 reconcile 不新增客户端；用户 `test` 客户端和非受管入站不被修改；订阅验证端口集合不一致时拒绝迁移。

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/adapters/xui -run Subscription -v`

Expected: FAIL，因为订阅适配器尚不存在。

- [ ] **Step 3: Implement subscription client management**

使用当前 3x-ui API 的客户端增删/关联接口，创建名称 `aimili-gateway-subscription`；只允许 Gateway 明确拥有的 VLESS 入站；读取 3x-ui 的订阅路径和订阅 ID；对返回订阅做数量和端口校验。若当前 3x-ui API 不支持直接关联，则使用入站更新接口保持相同客户端身份，并在响应中重新读取关联结果。完整订阅地址只作为返回值，不记录。

- [ ] **Step 4: Implement guarded legacy aggregate cleanup**

仅匹配 `agw-aggregate-vless-vless`、`agw-aggregate-vless` 和 `agw-aggregate`，确认 remark、标签、资源引用均属于 Gateway；先由调用方完成备份和订阅验证，再删除旧入站、客户端和 balancer；非受管资源一律跳过。

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/adapters/xui -run Subscription -v`

```bash
git -C D:/CodexProject/Github/aimili-gateway add internal/adapters/xui
git -C D:/CodexProject/Github/aimili-gateway commit -m "feat: manage test-style multi-inbound subscription"
```

### Task 4: 编排订阅、候选替换和主连接检测

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/orchestrator/orchestrator.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/orchestrator/reconcile.go`
- Create: `D:/CodexProject/Github/aimili-gateway/internal/orchestrator/subscription.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/httpapi/proxy_handlers.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/httpapi/server.go`
- Test: `D:/CodexProject/Github/aimili-gateway/internal/orchestrator/*_test.go`
- Test: `D:/CodexProject/Github/aimili-gateway/internal/httpapi/proxy_handlers_test.go`

**Interfaces:**
- Consumes: Task 1-3 的 assign、订阅客户端和存储接口。
- Produces: `Subscription(ctx)`、`ReplaceCandidate(ctx, candidateRef, targetGroupID)`、`CheckMain(ctx)`；API `GET /api/v1/proxy-groups/subscription`、`POST /api/v1/proxy-groups/{id}/replace`、`POST /api/v1/proxy-groups/agw-main/check`。

- [ ] **Step 1: Write failing orchestrator/API tests**

测试 standby 动态对象可解析；替换目标必须是受管 ready 槽位且不能是主连接；assign 成功后重测两个协议并更新同一出口位；失败时恢复旧节点；主连接检测保存两种协议延迟；订阅只包含 8443 和启用的 Gateway VLESS。

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/orchestrator ./internal/httpapi -run 'Subscription|Replace|Main|ProxyGroup' -v`

Expected: FAIL，因为方法和路由尚不存在。

- [ ] **Step 3: Implement orchestration**

在 reconcile 完成后确保订阅客户端；删除旧聚合资源的动作放在订阅验证之后。`ReplaceCandidate` 解析候选 opaque ref，保存旧槽位快照，调用 assign，等待并调用既有 VLESS/SOCKS 验证器；任何失败都按原节点 assign 回去。`CheckMain` 读取 legacy main 公开 Reality 参数和 Aimili 主状态，调用两个协议验证器并写入 `main_egress`。

- [ ] **Step 4: Implement HTTP handlers**

所有变更接口继续使用现有会话、Origin、CSRF 和幂等键；订阅 GET 只返回主动请求所需的地址，禁止服务端日志记录；错误统一映射 `not_found`、`conflict`、`repair_required`。

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/orchestrator ./internal/httpapi -run 'Subscription|Replace|Main|ProxyGroup' -v`

```bash
git -C D:/CodexProject/Github/aimili-gateway add internal/orchestrator internal/httpapi
git -C D:/CodexProject/Github/aimili-gateway commit -m "feat: orchestrate subscription and candidate replacement"
```

### Task 5: 更新前端 VLESS 订阅和出口位交互

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/web/src/api/client.ts`
- Modify: `D:/CodexProject/Github/aimili-gateway/web/src/views/PoolView.vue`
- Modify: `D:/CodexProject/Github/aimili-gateway/web/src/components/PoolTable.vue`
- Modify: `D:/CodexProject/Github/aimili-gateway/web/src/components/PoolFilters.vue`
- Test: `D:/CodexProject/Github/aimili-gateway/web/src/**/*.spec.ts`

**Interfaces:**
- Consumes: API subscription、replace、main check；新的 `slotNumber`/`fixed` 字段。
- Produces: “复制 VLESS 订阅”按钮、固定显示开关、出口位标志、替换弹窗取消/关闭、主连接检测按钮。

- [ ] **Step 1: Write failing component/API tests**

覆盖按钮文案；订阅复制调用新 endpoint；ready 节点固定显示且没有换 IP；standby 节点显示替换按钮；弹窗可取消；目标出口位下拉框只显示当前启用槽位；主连接显示检测和真实延迟。

- [ ] **Step 2: Run frontend tests and verify failure**

Run: `npm --prefix D:/CodexProject/Github/aimili-gateway/web test -- --run`

Expected: 新增断言失败。

- [ ] **Step 3: Implement API types and UI**

将 `copyAggregate` 改为 `copySubscription`；增加替换状态、目标槽位、取消函数和错误提示；固定节点默认开启并置顶；使用 `slotNumber` 显示“出口位 N”，主连接显示“主连接”；导出接口仍按筛选条件输出独立地址。

- [ ] **Step 4: Run frontend tests and build**

Run: `npm --prefix D:/CodexProject/Github/aimili-gateway/web test -- --run`  
Run: `npm --prefix D:/CodexProject/Github/aimili-gateway/web run build`

Expected: 测试通过，Vite 构建成功。

- [ ] **Step 5: Commit**

```bash
git -C D:/CodexProject/Github/aimili-gateway add web/src
git -C D:/CodexProject/Github/aimili-gateway commit -m "feat: add test-style subscription and slot replacement UI"
```

### Task 6: Reality ML-DSA 能力门控和验证器兼容

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/client.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/adapters/xui/models.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/validator/vless.go`
- Modify: `D:/CodexProject/Github/aimili-gateway/internal/orchestrator/orchestrator.go`
- Test: `D:/CodexProject/Github/aimili-gateway/internal/validator/vless_test.go`

**Interfaces:**
- Consumes: Xray/3x-ui capability probe。
- Produces: ML-DSA Seed/Verify 的能力门控、失败回退 X25519，不改变非受管 Reality。

- [ ] **Step 1: Write failing capability tests**

测试 Xray 不支持时保持空字段；支持且导出链路确认时写入配对字段；握手失败后恢复 X25519；敏感 Seed 不出现在错误和日志。

- [ ] **Step 2: Run validator tests and verify failure**

Run: `go test ./internal/validator ./internal/adapters/xui -run 'MLDSA|Reality' -v`

Expected: 新增 ML-DSA 断言失败。

- [ ] **Step 3: Implement gated support**

调用受支持的 Xray 生成命令获取 Seed/Verify；读取 3x-ui 返回字段并验证长度、配对和客户端配置格式；仅在全部能力满足时启用，否则保持现有 X25519 模板。

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/validator ./internal/adapters/xui -run 'MLDSA|Reality' -v`

```bash
git -C D:/CodexProject/Github/aimili-gateway add internal/validator internal/adapters/xui internal/orchestrator
git -C D:/CodexProject/Github/aimili-gateway commit -m "feat: gate Reality ML-DSA support"
```

### Task 7: 全量本地验证、部署脚本和生产迁移

**Files:**
- Modify: `D:/CodexProject/Github/aimili-gateway/scripts/verify-vps-country-proxy.py`
- Create: `D:/CodexProject/Github/aimili-gateway/scripts/verify-test-style-subscription.py`
- Modify: `D:/CodexProject/Github/aimili-gateway/README.md`
- Create: `D:/CodexProject/Github/aimili-gateway/docs/verification/2026-08-29-test-style-subscription.md`

**Interfaces:**
- Consumes: 完成的 Gateway/AimiliVPN/3x-ui 版本和用户提供的 `ssh ny`。
- Produces: 可复现本地测试、VPS 阶梯验收证据和迁移回滚记录。

- [ ] **Step 1: Run all local tests**

Run: `go test ./...`  
Run: `python -m pytest D:/CodexProject/Github/aimili-vpngate/tests -q`  
Run: `npm --prefix D:/CodexProject/Github/aimili-gateway/web test -- --run`  
Run: `npm --prefix D:/CodexProject/Github/aimili-gateway/web run build`

Expected: 全部通过；若失败先修复，不进入部署。

- [ ] **Step 2: Build and prepare artifacts**

构建 Gateway 二进制和前端静态资源，生成带校验和的临时部署包；不把订阅地址或密钥写入包内。

- [ ] **Step 3: Perform read-only VPS preflight**

通过 `ssh ny` 核对当前 systemd、3x-ui 数据库路径、Gateway 配置、AimiliVPN 控制 API capability、端口和 Swap；生成 SQLite backup API 备份并确认权限为 0600。

- [ ] **Step 4: Deploy with guarded migration**

按顺序：升级 AimiliVPN 控制 API → 部署 Gateway → 创建订阅客户端 → 验证订阅多条目 → 验证独立 VLESS/SOCKS5H/主连接检测 → 确认无外部引用后清理旧 `21000` 聚合资源。任一步失败立即恢复二进制、数据库和 Xray 设置并重启相关服务；不删除非受管资源。

- [ ] **Step 5: Run original-user-path verification**

使用 3x-ui QR/订阅入口和 Gateway “复制 VLESS 订阅”入口分别获取订阅，在 v2rayN 导入并确认多个节点；验证固定出口位、候选替换取消/成功/失败回滚、主连接延迟和 SOCKS5H 复制。

- [ ] **Step 6: Commit verification evidence**

只记录非敏感字段：端口、标签、数量、状态、延迟、时间和错误码；禁止记录 UUID、密码、Cookie、私钥和完整订阅链接。

```bash
git -C D:/CodexProject/Github/aimili-gateway add README.md scripts docs/verification/2026-08-29-test-style-subscription.md
git -C D:/CodexProject/Github/aimili-gateway commit -m "test: verify test-style subscription migration"
```
