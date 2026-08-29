# 主连接安全切换与每出口独立协议模式实施计划

状态：执行中；Task 1–10 已完成，等待 Task 11 VPS 阶梯部署

> **执行要求：** 使用 `superpowers:executing-plans` 逐任务实施；所有功能与故障修复必须使用 `superpowers:test-driven-development`，先观察新增测试按预期失败，再写最小实现。完成前使用 `superpowers:verification-before-completion`，并按项目规则执行一次 `ponytail-review`。

**目标：** 在不增加 AimiliVPN 运行出口、不改变 mixed/SOCKS5H、不重载整个 Xray 的前提下，实现主连接两阶段安全切换，以及主连接和三个普通出口各自独立的 TCP/Vision、XHTTP/REALITY、Hysteria2/QUIC/TLS 公网协议模式。

**架构：** AimiliVPN 持久化 `stage → commit/rollback` 主连接事务，Gateway 负责 stage 后的 mixed 与公网端到端验证。公网协议使用同一逻辑出口的同一入站 ID、tag、数值端口原位热替换；Gateway 只写入闭集 spool 请求，root-owned oneshot 助手验证所有权、生成三协议模板、调用回环 Xray HandlerService、原子更新 3x-ui SQLite，并在任何失败时反向恢复。Gateway SQLite 只保存中性公开资源标识、模式状态和脱敏操作状态，连接凭据仅保留在既有受限配置或 root-only 短期快照中。

**技术栈：** Python 3 标准库与 `unittest`、Go 1.26、SQLite、Vue 3 + TypeScript、Vitest/Vite、Xray HandlerService、systemd path/oneshot、UFW。

**设计依据：** `docs/superpowers/specs/2026-08-29-main-switch-protocol-modes-design.md`

**执行记录（2026-08-29）：** Task 1–9 已按提交边界完成；Task 10 已完成部署契约、SQLite 锁故障注入、远程安全门、运行手册、验收模板、双仓库全量本地验证和 Ponytail 复杂度审查。Task 11–12 尚未执行，不得将本地通过解释为生产完成。

## 全局硬约束

- 逻辑出口固定为 `agw-main` 加三个现有普通槽位；不得增加 AimiliVPN 隧道、长期备用公网入站、常驻 Xray 或事务守护进程。
- 每个逻辑出口同时只有一个公网协议配置；切换复用同一入站 ID、tag、数值端口和 SOCKS 路由。
- mixed/SOCKS5H 永不随公网协议切换；不得恢复 `21000` 或 balancer。
- 在线切换不得调用会重载整个 Xray 的 3x-ui 普通写 API；Xray PID 和非目标连接必须持续。
- 不删除非 Gateway 受管入站或全局客户端；主入站是严格绑定的遗留资源，只允许原位修改协议字段，永不删除。
- Hysteria2 使用独立 Auth 和 Caddy 稳定证书路径，不复用 UUID，不占用 `443/UDP`；只允许精确 UDP 端口 `8443`、`20000`、`20001`、`20002`。
- 不在日志、测试输出、文档、HTTP 错误或提交中输出密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- 所有跨服务失败先自动恢复本级旧状态；旧状态恢复失败才进入 `repair_required`。生产每一级失败只回滚该级并停止扩大。

---

### Task 1：建立隔离工作树并记录基线

**Files:**
- Gateway worktree: `D:/CodexProject/Github/aimili-gateway/.worktrees/main-switch-protocol-modes`
- AimiliVPN worktree: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes`
- Modify: `docs/superpowers/plans/2026-08-29-main-switch-protocol-modes.md`（仅更新执行勾选与实际偏差）

- [ ] **Step 1：确认工作树目录被忽略并创建功能分支**

```powershell
git -C D:/CodexProject/Github/aimili-gateway check-ignore -q .worktrees/probe
git -C D:/CodexProject/Github/aimili-gateway worktree add .worktrees/main-switch-protocol-modes -b feat/main-switch-protocol-modes
git -C D:/CodexProject/Github/aimili-vpngate check-ignore -q .worktrees/probe
git -C D:/CodexProject/Github/aimili-vpngate worktree add .worktrees/main-switch-protocol-modes -b feat/main-switch-protocol-modes
```

- [ ] **Step 2：运行 Gateway 基线**

```powershell
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
```

Expected：全部通过；如果失败，先区分既有失败与本任务回归，不在未确认原因时进入实现。

- [ ] **Step 3：运行 AimiliVPN 基线**

```powershell
python -m unittest discover -s tests -v
```

Expected：全部通过，不安装新解释器或未声明依赖。

---

### Task 2：TDD 实现 AimiliVPN 主事务状态与统一变更互斥

**Files:**
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/main_assignment.py`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_main_assignment.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/vpngate_manager.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/node_pool.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_node_pool.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_exit_slot_types.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_pool_maintenance.py`

**接口：** `MainAssignmentCoordinator` 负责持久化状态、请求哈希、180 秒期限、恢复状态和全局 mutation lease；manager 暴露 `main_assignment_snapshot()`、`stage_main_assignment(...)`、`commit_main_assignment(operation_id)`、`rollback_main_assignment(operation_id)`。

- [ ] **Step 1：先写状态机、幂等和恢复失败测试**

覆盖：空闲到 `pending_commit`；相同幂等键/相同请求重放；相同键/不同请求冲突；预期当前节点不匹配；过期事务自动 rollback；新主拨号、代理 DNS、真实出口、可用性任一失败恢复旧主；旧主恢复失败进入 `repair_required`；持久化内容和异常文本不包含节点配置正文。

- [ ] **Step 2：运行聚焦测试并确认红灯**

```powershell
python -m unittest tests.test_main_assignment -v
```

Expected：因模块、状态机和 manager 门面不存在而失败。

- [ ] **Step 3：写最小事务实现**

使用原子 JSON 替换持久化安全元数据；旧/新连接正文继续引用现有受限节点数据。stage 固定顺序为保存旧状态、停止旧主、拨新主、验证 `127.0.0.1:7928` 的代理 DNS/出口/可用性、清理旧下游连接、进入 `pending_commit`。失败时立即按保存的旧身份恢复并复验。

- [ ] **Step 4：先写互斥和候选唯一性失败测试**

覆盖主事务期间刷新、create/assign/rotate/delete、后台 supervise/漂移返回 `operation_busy` 或安全跳过；当前主、待提交新主和三个槽位候选都在保护集合；当前主不得进入 standby，不得分配给普通槽位。

- [ ] **Step 5：运行互斥测试确认红灯并实现统一 mutation lease**

```powershell
python -m unittest tests.test_main_assignment tests.test_node_pool tests.test_exit_slot_types tests.test_pool_maintenance -v
```

锁顺序固定为主事务协调器 → maintenance → slot supervisor → 普通状态锁；不得在持有普通锁时反向取得协调器。

- [ ] **Step 6：运行聚焦与全量测试并提交**

```powershell
python -m unittest tests.test_main_assignment tests.test_node_pool tests.test_exit_slot_types tests.test_pool_maintenance -v
python -m unittest discover -s tests -v
git add main_assignment.py vpngate_manager.py node_pool.py tests
git commit -m "feat: add transactional main assignment"
```

---

### Task 3：TDD 暴露 AimiliVPN 主事务控制契约

**Files:**
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/control_api.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_control_api.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_project_contract.py`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/README.md`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/CHANGELOG.md`

**产生：** `GET /control/v1/main/assignment`、`POST /control/v1/main/assign`、`POST /control/v1/main/assign/{operationId}/commit`、`POST /control/v1/main/assign/{operationId}/rollback`，以及四项封闭 capability。

- [ ] **Step 1：写路由、鉴权、闭集字段和安全响应失败测试**

覆盖未知字段、缺字段、过长幂等键、非法 operation ID、未授权、错误码到 HTTP 状态映射、重复 commit/rollback、安全响应字段白名单；明确断言响应不包含配置、凭据或连接秘密。

- [ ] **Step 2：运行测试确认红灯**

```powershell
python -m unittest tests.test_control_api tests.test_project_contract -v
```

- [ ] **Step 3：实现最小 API 与启动恢复钩子**

复用现有 Bearer 鉴权与 JSON 大小限制；启动时装载未完成事务并执行过期恢复，恢复期间变更接口返回 `operation_busy`。

- [ ] **Step 4：验证并提交**

```powershell
python -m unittest discover -s tests -v
git add control_api.py tests/test_control_api.py tests/test_project_contract.py README.md CHANGELOG.md
git commit -m "feat: expose main assignment transaction api"
```

---

### Task 4：TDD 建立 Gateway 中性出口、协议状态与 Aimili 适配器

**Files:**
- Create: `internal/store/migrations/010_public_protocol_modes.sql`
- Create: `internal/domain/protocolmode.go`
- Create: `internal/domain/protocolmode_test.go`
- Create: `internal/store/protocolmodes.go`
- Create: `internal/store/protocolmodes_test.go`
- Modify: `internal/domain/proxygroup.go`
- Modify: `internal/domain/proxygroup_test.go`
- Modify: `internal/store/proxygroups.go`
- Modify: `internal/store/proxygroups_test.go`
- Modify: `internal/store/main_egress.go`
- Modify: `internal/store/store_test.go`
- Modify: `internal/adapters/aimili/client.go`
- Modify: `internal/adapters/aimili/client_test.go`

**数据模型：** `PublicInboundID/PublicPort` 成为唯一可写事实源；migration 由旧 `vless_*` 回填并重建表。新增 `egress_protocol_modes` 与 `egress_operations`，只保存模式、阶段、哈希、版本和脱敏错误码。

- [ ] **Step 1：写迁移和协议状态机失败测试**

覆盖旧库升级保留 ID/端口、重复迁移、闭集枚举、合法/非法状态转移、乐观并发、操作幂等哈希、`agw-main` 与普通组读写，以及 schema 中不出现 UUID/Auth/私钥/完整 URI 列。

- [ ] **Step 2：写 Aimili stage/commit/rollback 适配器失败测试**

覆盖路径、请求闭集、控制幂等键、响应安全解包、超时和脱敏错误映射。

- [ ] **Step 3：运行聚焦测试确认红灯**

```powershell
go test ./internal/domain ./internal/store ./internal/adapters/aimili -run 'Protocol|Public|MainAssign|Migration|Operation' -v
```

- [ ] **Step 4：实现最小模型、迁移、store 和适配器**

旧字段不能继续作为可写别名；迁移后所有编排、API 和适配器改读中性字段。浏览器幂等键只存 SHA-256 请求哈希，Aimili 控制幂等键由服务端生成。

- [ ] **Step 5：运行包测试并提交**

```powershell
go test ./internal/domain ./internal/store ./internal/adapters/aimili
git add internal/domain internal/store internal/adapters/aimili
git commit -m "feat: add neutral public protocol state"
```

---

### Task 5：TDD 实现 Gateway 主连接 stage 验证与回滚编排

**Files:**
- Create: `internal/orchestrator/mainassignment.go`
- Create: `internal/orchestrator/mainassignment_test.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/orchestrator/locks.go`
- Modify: `internal/orchestrator/reconcile.go`
- Modify: `internal/orchestrator/orchestrator_test.go`
- Modify: `internal/httpapi/proxy_handlers.go`
- Modify: `internal/httpapi/proxy_handlers_test.go`
- Modify: `internal/httpapi/server.go`

**入口：** 保持 `POST /api/v1/proxy-groups/{candidate}/replace`，允许 `targetGroupId=agw-main`；候选列表把当前主从 standby 去重，替换目标按“主连接、出口位 1、出口位 2、出口位 3”排序。

- [ ] **Step 1：写成功、失败和持久化幂等测试**

成功序列必须是 `stage → 7928/mixed/public 验证 → commit → store`；公网验证失败必须 `rollback → 旧 7928/mixed/public 复验`；旧链路恢复成功时操作失败但状态 ready，恢复失败才 `repair_required`。覆盖进程重启后的相同请求重放、当前主不重复显示、普通槽位身份不变。

- [ ] **Step 2：运行测试确认红灯**

```powershell
go test ./internal/orchestrator ./internal/httpapi -run 'MainAssign|Replace|Candidate|Idempot' -v
```

- [ ] **Step 3：实现最小编排和 API 映射**

使用一个全局 mutation gate 协调主替换、普通槽位变化和协议切换；仍保留每出口细粒度锁处理只读和验证。主公网验证按 `agw-main` 当前协议模式选择验证器，不假设永远是 VLESS/TCP。

- [ ] **Step 4：验证并提交**

```powershell
go test ./internal/orchestrator ./internal/httpapi
git add internal/orchestrator internal/httpapi
git commit -m "feat: orchestrate safe main replacement"
```

---

### Task 6：TDD 实现 root 协议事务助手核心

**Files:**
- Create: `scripts/aimili_xui_protocol_transaction.py`
- Create: `scripts/test_aimili_xui_protocol_transaction.py`
- Create: `deploy/config/protocol-transaction.example.json`
- Create: `deploy/bin/aimili-xui-protocol-transaction`

**输入闭集：** operation ID、逻辑出口 ID、预期入站 ID/tag/端口、旧/新模式、预期指纹；不得接受任意 JSON、任意路径、认证材料或防火墙规则。完整模板和凭据由助手从 root-owned 配置及 3x-ui SQLite 读取/派生。

- [ ] **Step 1：写所有权、白名单与模板失败测试**

使用临时 SQLite 和 fake command runner 覆盖：普通受管入站五要素；严格绑定主入站；拒绝 mixed、未知 tag、未知字段、符号链接、路径穿越、非白名单端口和非 Gateway remark；三个模板的 protocol/network/security/flow/decryption；XHTTP `disable_flow=true`；Hysteria2 独立 Auth 且不覆盖已有值；非目标行和非 Gateway 客户端逐字节不变。

- [ ] **Step 2：运行测试确认红灯**

```powershell
python -m unittest scripts.test_aimili_xui_protocol_transaction -v
```

- [ ] **Step 3：实现模板、快照和 SQLite 原子更新**

快照目录与文件使用 root-only `0700/0600`、原子创建并拒绝链接；快照包含目标入站、相关全局客户端字段、运行时摘要和反向操作所需材料，日志只包含脱敏阶段和错误码。`apply` 成功后保留快照，只有 Gateway 完成外部验证并请求 `finalize` 才删除；`repair_required` 始终保留。

- [ ] **Step 4：写命令顺序和崩溃恢复失败测试**

覆盖完整临时 Xray 配置离线测试、`rmi`/`adi` 只操作目标 tag、SQLite 失败反向热替换、热加入失败恢复、指纹不一致恢复、每个持久化阶段的故障注入、启动时恢复未完成日志；断言没有调用 3x-ui 写 API或全局 Xray restart。

- [ ] **Step 5：实现最小事务顺序**

固定为 snapshot → offline test → `rmi` → `adi` → SQLite transaction → `lsi`/DB 指纹确认。异常固定走反向 `rmi/adi` 和数据库恢复；恢复失败返回 `rollback_failed`。

- [ ] **Step 6：验证并提交**

```powershell
python -m unittest scripts.test_aimili_xui_protocol_transaction -v
git add scripts/aimili_xui_protocol_transaction.py scripts/test_aimili_xui_protocol_transaction.py deploy/config/protocol-transaction.example.json deploy/bin/aimili-xui-protocol-transaction
git commit -m "feat: add xray protocol transaction helper"
```

---

### Task 7：TDD 接入受限 spool、systemd oneshot 与 Gateway 协议编排

**Files:**
- Create: `internal/protocoltxn/client.go`
- Create: `internal/protocoltxn/client_test.go`
- Create: `internal/orchestrator/protocolmode.go`
- Create: `internal/orchestrator/protocolmode_test.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/orchestrator/locks.go`
- Create: `deploy/systemd/aimili-xui-protocol-transaction.path`
- Create: `deploy/systemd/aimili-xui-protocol-transaction.service`
- Modify: `deploy/systemd/aimili-gateway.service`
- Modify: `deploy/deploy_contract_test.go`
- Modify: `deploy/config/config.example.json`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**契约：** Gateway 只在受限 spool 原子写请求，并读取同 operation ID 的安全结果；systemd path/oneshot 以 root 按需执行，无网络监听、无常驻进程。Gateway 服务不得取得 3x-ui SQLite、证书私钥或 Xray HandlerService 的直接写权限。

- [ ] **Step 1：写 spool 安全与部署契约失败测试**

覆盖文件权限、固定目录、operation ID 格式、未知结果字段、超时、取消、重复请求、结果脱敏、systemd sandbox、oneshot 无常驻、Gateway 权限不扩大。

- [ ] **Step 2：写协议编排失败测试**

成功顺序：状态 `switching` → helper apply → 订阅重建/读取验证 → 当前协议真实公网验证 → helper finalize → ready。失败顺序：helper rollback → 旧订阅/旧公网复验 → ready 或 `repair_required`。相同目标且 ready 必须零写成功；主连接额外核对 `7928` 和主 mixed；普通槽位核对 Aimili slot；非目标状态不变。

- [ ] **Step 3：运行测试确认红灯**

```powershell
go test ./internal/protocoltxn ./internal/orchestrator ./internal/config ./deploy -run 'Protocol|Transaction|Spool|Systemd' -v
```

- [ ] **Step 4：实现最小客户端、编排和单元契约**

将 helper 设计为 `apply` 后保留可回滚快照，只有 Gateway 完成订阅和真实流量验证后才 `finalize`；若 Gateway 崩溃或超时，helper 恢复旧协议。全局 mutation gate 拒绝同时进行的主切换、槽位变更、候选刷新和另一协议切换。

- [ ] **Step 5：验证并提交**

```powershell
go test ./internal/protocoltxn ./internal/orchestrator ./internal/config ./deploy
git add internal/protocoltxn internal/orchestrator internal/config deploy
git commit -m "feat: orchestrate isolated protocol switching"
```

---

### Task 8：TDD 升级混合协议订阅、验证器和连接 API

**Files:**
- Modify: `internal/adapters/xui/models.go`
- Modify: `internal/adapters/xui/subscription.go`
- Modify: `internal/adapters/xui/subscription_test.go`
- Create: `internal/validator/public.go`
- Create: `internal/validator/public_test.go`
- Modify: `internal/validator/vless.go`
- Modify: `internal/validator/vless_test.go`
- Modify: `internal/orchestrator/subscription.go`
- Modify: `internal/orchestrator/subscription_test.go`
- Modify: `internal/httpapi/proxy_handlers.go`
- Modify: `internal/httpapi/proxy_handlers_test.go`

**产生：** `PUT /api/v1/proxy-groups/{id}/protocol-mode`；payload 返回 `protocolMode`、`protocolState`、`subscriptionState`、`availableProtocolModes`、`lastErrorCode`；连接响应以 `publicUri` 为中性字段。

- [ ] **Step 1：写纯 VLESS、XHTTP、Hysteria2 与四出口混合订阅失败测试**

按逻辑出口验证入站 ID、显示名、协议、端口及必要参数；mixed 永不进入订阅；同一全局 email/subId 保持；UUID/Auth 使用正确协议字段但测试失败输出必须脱敏；非 Gateway 客户端不删除、不清空其他字段。

- [ ] **Step 2：写公网验证和 API 失败测试**

三模式分别验证真实握手、代理 DNS、出口一致性；Hysteria2 UDP/QUIC；连接 API 在 Hysteria2 下不得拼造 `vlessUri`，兼容字段返回明确 `protocol_changed`；API 继续要求登录、同源、CSRF 和 `Idempotency-Key`。

- [ ] **Step 3：运行测试确认红灯**

```powershell
go test ./internal/adapters/xui ./internal/validator ./internal/orchestrator ./internal/httpapi -run 'Subscription|Public|Protocol|Connection' -v
```

- [ ] **Step 4：实现最小订阅模型、验证器和 API**

订阅 URL 只在认证后的主动请求中返回，不持久化、不记录。订阅未验证时状态为 `subscription_pending` 且不允许复制；超时回滚旧协议。

- [ ] **Step 5：验证并提交**

```powershell
go test ./internal/adapters/xui ./internal/validator ./internal/orchestrator ./internal/httpapi
git add internal/adapters/xui internal/validator internal/orchestrator internal/httpapi
git commit -m "feat: support mixed public protocol subscriptions"
```

---

### Task 9：TDD 更新前端协议与主连接交互

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.spec.ts`
- Modify: `web/src/components/PoolTable.vue`
- Modify: `web/src/components/poolStatus.ts`
- Modify: `web/src/views/PoolView.vue`
- Modify: `web/src/views/PoolViews.spec.ts`

- [ ] **Step 1：写组件与 API 失败测试**

覆盖主连接出现在替换目标首位、无“出口位 4”、当前主不显示 standby、ready 出口三模式下拉、`switching/subscription_pending/repair_required` 禁用规则、失败回滚后恢复旧模式、按钮文案“复制节点订阅”、协议切换成功提示 mixed/SOCKS5H 未变化。

- [ ] **Step 2：运行测试确认红灯**

```powershell
npm --prefix web test -- --run
```

- [ ] **Step 3：实现最小类型与 UI**

浏览器只提交闭集 mode 和目标 ID；不接收/缓存 Xray JSON、证书路径、UUID/Auth 或完整订阅 URL。复制动作仅在用户主动点击且订阅 ready 时调用。

- [ ] **Step 4：验证、构建并提交**

```powershell
npm --prefix web test -- --run
npm --prefix web run build
git add web
git commit -m "feat: add per-egress protocol controls"
```

---

### Task 10：本地集成、故障注入、文档与部署脚本

**Files:**
- Create: `scripts/test_protocol_transaction_integration.py`
- Create: `scripts/deploy-main-switch-protocol-modes-remote.sh`
- Create: `scripts/verify-main-switch-protocol-modes-remote.py`
- Create: `scripts/verify-main-switch-protocol-modes.ps1`
- Create: `docs/runbooks/main-switch-protocol-modes.md`
- Create: `docs/verification/2026-08-29-main-switch-protocol-modes.md`
- Modify: `README.md`

- [x] **Step 1：写部署与回滚脚本契约测试**

先测试脚本必须：受限联合备份、资源门检查、精确 UFW UDP 规则、禁止 `443/udp` 和端口范围、逐级开关、失败自动回滚、非 Gateway 资源前后指纹、Xray PID/非目标探针、敏感输出过滤。

- [x] **Step 2：运行契约测试确认红灯并实现脚本**

```powershell
go test ./deploy -run 'MainSwitch|Protocol|Rollback' -v
python -m unittest scripts.test_protocol_transaction_integration -v
```

- [x] **Step 3：运行本地 fake-Xray/fake-3x-ui 故障注入**

覆盖每个事务阶段崩溃、重复请求、数据库锁、订阅失败、公网验证失败、非目标长连接持续、Gateway/AimiliVPN 重启恢复。集成夹具只使用伪凭据，结束后清理临时文件。

- [x] **Step 4：更新运维文档和验证记录模板**

说明备份、恢复、`repair_required` 处置、证书续期验证、云 UDP 边界诊断；验证记录只写安全状态、计数、端口、PID 是否变化和脱敏错误码。

- [x] **Step 5：两个仓库全量本地验证**

```powershell
python -m unittest discover -s tests -v
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
python -m unittest discover -s scripts -p "test_*.py" -v
git diff --check
```

- [x] **Step 6：执行复杂度审查、修正后再验证并提交**

使用 `ponytail-review` 只检查可删除的推测性抽象、重复封装和不必要扩展点；逐项核对设计硬约束后再修改，不能为减行删除事务边界、安全检查或回滚。若发生结构调整，重跑本任务全部本地验证。

```powershell
git add README.md docs scripts deploy
git commit -m "docs: add protocol switch deployment runbook"
```

---

### Task 11：`ssh ny` 阶梯部署与真实路径验收

**前置硬门：** 两仓库工作树干净、全量本地测试通过、联合备份成功、`MemAvailable ≥ 160 MiB`、Swap 空闲 `≥ 512 MiB`、证书有效且 Xray 可读、非 Gateway 资源已记录安全指纹。所有 SSH 输出必须经过脱敏过滤。

- [ ] **Stage 1：只部署 AimiliVPN 主事务**

先受控制造新主失败并确认自动恢复旧主，再执行一次真实主切换。验证 `7928`、主 mixed、`8443` 当前协议、代理 DNS、真实出口；三个普通槽位节点/进程/出口不变。任一失败恢复 AimiliVPN 代码与状态并停止。

- [ ] **Stage 2：部署 Gateway 中性模型、助手和精确 UDP 白名单**

所有出口仍保持 TCP/Vision。验证 Gateway 数据迁移、四公网入站/四 mixed 数量、Xray PID、3x-ui 重启可重建现状、UFW 只有 `8443/udp` 与 `20000–20002/udp` 的四条精确规则，没有 `443/udp` 节点规则。

- [ ] **Stage 3：普通出口 TCP → XHTTP → TCP**

保持另外三个公网节点和四个 mixed 的长连接探针。验证 Xray PID 不变、订阅更新、v2rayN `7.24.4` 识别、代理 DNS和真实出口；切回 TCP 验证反向路径。任一失败只回滚目标出口。

- [ ] **Stage 4：普通出口 VLESS → Hysteria2**

完成外部 QUIC/TLS、证书、代理 DNS、真实出口、UFW 计数和资源观察。若包未到主机则判定上游云防火墙阻断，立即回 TCP 并停止扩大，不重建服务器。

- [ ] **Stage 5：观察资源硬门**

单出口切换后观察至少 5 分钟：无 OOM；Xray RSS 峰值 ≤ 96 MiB；`MemAvailable` 不连续 30 秒低于 96 MiB；Swap 增量 ≤ 128 MiB。越界立即回滚。

- [ ] **Stage 6：主出口协议试切与最终混合模式**

普通出口稳定后才允许主协议试切，并同时验证 `7928` 与主 mixed。最终状态：主 TCP/Vision、出口位 1 XHTTP、出口位 2 Hysteria2、出口位 3 TCP/Vision；出口总数四、公网入站总数四、mixed 总数四。

- [ ] **Stage 7：重启恢复和原用户路径验收**

受控重启 x-ui/Xray，确认 SQLite 重建相同混合模式；再重启 Gateway 与 AimiliVPN，确认未提交事务自动回滚、已提交模式不漂移。通过 Gateway UI 实际执行候选替换到“主连接”、独立协议切换与“复制节点订阅”，用 v2rayN `7.24.4` 刷新并逐条验证。只有原用户路径通过后才能声明完成。

- [ ] **Stage 8：更新脱敏验证记录**

在 `docs/verification/2026-08-29-main-switch-protocol-modes.md` 记录提交、服务版本、测试命令结果、状态计数、回滚演练、PID/资源指标和未决外部边界；不得记录连接秘密或完整订阅地址。

---

### Task 12：最终完整性检查与分支交付

- [ ] **Step 1：使用 `verification-before-completion` 从干净工作树重跑全部完成证据**

```powershell
python -m unittest discover -s tests -v
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
python -m unittest discover -s scripts -p "test_*.py" -v
git diff --check
git status --short --branch
```

- [ ] **Step 2：核对设计完成标准**

逐项核对：四逻辑出口且每个只有一个公网入站；mixed/SOCKS 不变；主失败自动回滚；Xray PID 不变；非目标长连接持续；非 Gateway 资源未删除；三协议与订阅真实可用；敏感信息未泄露。

- [ ] **Step 3：使用 `finishing-a-development-branch` 决定集成方式**

先确保 Gateway 与 AimiliVPN 功能分支各自无未提交改动、提交边界清晰，再根据用户选择合并、保留分支或创建 PR；不得擅自推送远端。
