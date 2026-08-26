# Aimili Gateway 在线代理池 V1-C 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将单代理组闭环升级为全部 AimiliVPN 有效候选可见、一个出口在线、其余候选按需切换的 VLESS 与 SOCKS5H 资源池，并交付紧凑统一控制台。

**Architecture:** 资源池目录合并全部安全候选和当前在线组。512 MiB 生产容量固定为 1；启用待机候选时回收旧组并建立对应 AimiliVPN 槽位、Xray SOCKS 出站、VLESS 入站和 mixed 入站。只有双协议真实验证通过的出口允许复制或导出。

**Tech Stack:** Go 1.26、SQLite、Vue 3、TypeScript、Vite、Vitest、Python 3 标准库、OpenVPN、3x-ui/Xray、Caddy、systemd。

**Spec:** `docs/superpowers/specs/2026-08-26-online-proxy-pools-design.md`

## 全局约束

- 不重写 Xray 或 3x-ui 核心，不直接写 `x-ui.db`。
- 三个服务保持独立进程和独立更新。
- 所有新行为先写失败测试，再写最小实现。
- 秘密和完整连接地址不得进入日志、测试输出或验收文档。
- 只有 `ready` 记录允许复制和导出。
- VPS 容量按阶梯提升，失败即保留上一级稳定值。

---

### Task 1: AimiliVPN 指定候选槽位契约

**Files:**
- Modify: `aimili-vpngate/control_api.py`
- Modify: `aimili-vpngate/vpngate_manager.py`
- Test: `aimili-vpngate/tests/test_control_api.py`
- Test: `aimili-vpngate/tests/test_exit_slot_types.py`

**Interfaces:**
- Consumes: `safe_candidate_snapshot()`、`add_slot_with_node(node_id)`。
- Produces: `POST /control/v1/slots {country, proxyType, candidateId}`、`GET /control/v1/slots`、`create_managed_slot(country, proxy_type, candidate_id)`。

- [ ] 写失败测试：接受 `candidateId`，拒绝未知或分类不匹配候选，槽位列表只含安全字段。
- [ ] 运行定向测试并确认因契约缺失而失败。
- [ ] 扩展 manager facade 和控制 API，复用现有指定节点锁定能力。
- [ ] 运行定向测试和全部 Python 测试。
- [ ] 提交 `feat: add candidate-pinned exit slot contract`。

### Task 2: Gateway 出口实例模型

**Files:**
- Modify: `internal/domain/proxygroup.go`
- Test: `internal/domain/proxygroup_test.go`
- Create: `internal/store/migrations/005_online_egress_pool.sql`
- Modify: `internal/store/proxygroups.go`
- Test: `internal/store/proxygroups_test.go`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `NewProxyGroupIdentity(country, proxyType, candidateID)`；同分类多出口；容量上限 64。

- [ ] 写失败测试：不同候选生成不同稳定资源名，空候选被拒绝，配置接受 64、拒绝 65。
- [ ] 运行定向测试并确认失败。
- [ ] 添加候选和协议延迟字段、SQLite 迁移及存储读写。
- [ ] 运行定向测试和全套 Go 测试。
- [ ] 提交 `feat: model per-candidate online egresses`。

### Task 3: 全池协调器

**Files:**
- Modify: `internal/adapters/aimili/client.go`
- Test: `internal/adapters/aimili/client_test.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Create: `internal/orchestrator/reconcile.go`
- Create: `internal/orchestrator/reconcile_test.go`

**Interfaces:**
- Consumes: 候选列表、指定候选槽位、3x-ui ensure/delete 和双协议验证器。
- Produces: `Reconcile(ctx) ReconcileResult`，为每个有效候选建立唯一出口并对重复实际出口去重。

- [ ] 写失败测试：同分类多候选均创建，已有 ready 保留，单候选失败不影响其他候选，重复出口回收较差实例。
- [ ] 运行定向测试并确认失败。
- [ ] 实现 Adapter、协调器、有界并发和全局协调锁。
- [ ] 运行协调器、Adapter 和全套 Go 测试。
- [ ] 提交 `feat: reconcile all available egress candidates`。

### Task 4: 资源池 API 与安全导出

**Files:**
- Modify: `internal/httpapi/proxy_handlers.go`
- Test: `internal/httpapi/proxy_handlers_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `web/src/api/client.ts`
- Test: `web/src/api/client.spec.ts`

**Interfaces:**
- Produces: 资源池查询、异步刷新、单条连接信息和筛选 TXT 导出。

- [ ] 写失败测试：筛选正确，非 ready 不返回秘密，连接和导出要求近期重新认证并带 `no-store`。
- [ ] 运行 Go 和 Vitest 定向测试并确认失败。
- [ ] 实现资源池 DTO、筛选、刷新状态、连接和导出响应。
- [ ] 运行定向测试及全部 Gateway/Web 测试。
- [ ] 提交 `feat: expose secure online pool APIs`。

### Task 5: 紧凑统一控制台

**Files:**
- Create: `web/src/components/AppShell.vue`
- Create: `web/src/components/PoolFilters.vue`
- Create: `web/src/components/PoolTable.vue`
- Create: `web/src/views/VpnPoolView.vue`
- Create: `web/src/views/SocksPoolView.vue`
- Create: `web/src/views/SettingsView.vue`
- Test: `web/src/views/PoolViews.spec.ts`
- Modify: `web/src/App.vue`
- Modify: `web/src/router/index.ts`
- Replace: `web/src/views/OverviewView.vue`
- Delete: `web/src/components/ServiceCard.vue`
- Delete: `web/src/components/ServiceCard.spec.ts`

**Interfaces:**
- Consumes: Task 4 的资源池、连接、导出、刷新和安全确认 API。
- Produces: VPN 节点池、SOCKS5H 代理池、高级设置三个紧凑响应式页面。

- [ ] 写失败测试：服务卡消失，两个池共用筛选，非 ready 禁止复制，复制和导出调用正确。
- [ ] 运行 Vitest 定向测试并确认失败。
- [ ] 实现 56px 顶栏、紧凑工具栏、52–56px 表格行、语义状态和响应式布局。
- [ ] 将安全确认、白名单和专家模式移动到高级设置。
- [ ] 运行 Vitest 和 `npm run build`。
- [ ] 提交 `feat: redesign gateway as compact proxy pools`。

### Task 6: 部署、本地验收与 512 MiB VPS 安全部署

**Files:**
- Modify: `deploy/config/config.example.json`
- Modify: `deploy/deploy_contract_test.go`
- Modify: `README.md`
- Modify: `aimili-vpngate/README.md`
- Modify: `aimili-3xui-simple-deploy/remote/install.sh`
- Modify: `aimili-3xui-simple-deploy/tests/run.ps1`
- Create: `scripts/verify-online-pools-v1c.ps1`
- Create: `docs/verification/2026-08-26-ny-v1c-online-pools.md`

**Interfaces:**
- Produces: 固定提交部署、容量配置、脱敏端到端验证、生产稳定容量和回滚证据。

- [ ] 写失败部署契约测试，要求固定新提交、容量配置且 Aimili 控制端口仍只监听回环。
- [ ] 更新配置、部署脚本和中文文档，运行三个项目全部测试及构建。
- [ ] 备份 VPS 当前部署并先以容量 1 验证兼容。
- [ ] 固定容量 1，验证待机候选可见、按需切换、代理、DNS、来源拒绝、重启恢复和资源指标。
- [ ] 验证 AimiliVPN 串行探测、启动延迟、失败退避和 systemd 资源保护；不在 512 MiB 上扩展常驻槽位。
- [ ] 写入脱敏验收记录并提交。
- [ ] 按既定选择本地合并回 Gateway `main` 和 AimiliVPN `custom`。
