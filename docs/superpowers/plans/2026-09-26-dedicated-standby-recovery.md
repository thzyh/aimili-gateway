# 专属备用与持续故障恢复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为 4 个活动连接实现 4 个专属备用、统一主/出口恢复逻辑、可配置恢复参数和国家可用性统计，并在 bj 上验证。

**Architecture:** 在 `vpngate_manager.py` 内保留现有 OpenVPN/TUN 生命周期，新增按保护目标的备用映射、统一候选排序和持续恢复调度；通过控制 API 暴露运行参数与国家统计，Gateway Go 适配器和 Vue 高级设置负责展示与保存。外部 IP 质量接口只返回未启用状态。

**Tech Stack:** Python 标准库、Go HTTP adapter/orchestrator、Vue 3/TypeScript、现有 unittest/Vitest。

**Spec:** `docs/superpowers/specs/2026-09-26-dedicated-standby-recovery-design.md`

## Global Constraints

- 目标配置为 4 个活动连接 + 4 个专属备用；容量不足时拒绝扩容。
- 主连接和普通出口共用住宅优先、机房兜底逻辑。
- 不接入 ping0.cc；IP 质量字段为 `null`/`not_implemented`。
- 候选恢复使用冷却、退避、并发限制和恢复时间预算。
- 不修改客户端仓库，不提交凭据和 OpenVPN 配置。

## Review Focus

- 备用映射与活动槽位变化：活动槽位减少或重排时不会把备用提升到错误出口。
- 住宅候选验证失败：机房只有在住宅候选全部不可用时才被选中。
- 资源不足：4+4 不满足容量时不会停止已有出口。
- 持久化参数损坏或越界：读取时恢复安全默认值，保存时返回明确错误。
- IP 质量未启用：API 和 UI 不把未知显示成 0 或通过。

### Task 1: Python 恢复策略与参数模型

**Files:**
- Modify: `services/aimili-egress/vpngate_manager.py`
- Modify: `services/aimili-egress/capacity.py`
- Create/Modify: `services/aimili-egress/tests/test_recovery_policy.py`

- [ ] 先写测试：4 个活动目标生成 4 个备用；主/槽位使用同一候选排序；住宅优先、机房兜底；失败候选冷却和指数退避；参数越界被拒绝。
- [ ] 运行定向测试确认新增测试先失败。
- [ ] 实现持久化恢复参数、活动目标映射、候选统计和统一恢复调度。
- [ ] 运行定向 Python 测试确认通过。

### Task 2: 控制 API 与 Go 适配器

**Files:**
- Modify: `services/aimili-egress/control_api.py`
- Modify: `internal/adapters/aimili/client.go`
- Modify: `internal/adapters/aimili/client_test.go`
- Modify: `internal/orchestrator/reconcile.go` or the existing capacity settings path

- [ ] 先写 API/适配器测试：读取和保存恢复参数、国家三项统计、IP 质量未启用字段。
- [ ] 运行测试确认先失败。
- [ ] 添加严格 JSON 校验、脱敏错误码和容量拒绝映射。
- [ ] 运行 Go 定向测试。

### Task 3: Vue 高级设置

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/views/SettingsView.vue`
- Modify: `web/src/views/SettingsView.spec.ts`

- [ ] 先写组件测试：显示恢复参数、自动上限、备用状态、国家三项统计和“IP 质量暂未启用”。
- [ ] 运行 Vitest 确认先失败。
- [ ] 实现分组表单、保存反馈、容量拒绝提示和恢复事务摘要。
- [ ] 运行 Vitest 与生产构建。

### Task 4: 文档和 bj 阶梯验证

**Files:**
- Modify: `README.md`
- Create: `docs/verification/2026-09-26-bj-standby-4x4.md`

- [x] 更新高级设置参数和恢复策略说明。
- [x] 本地完整受影响测试与 `git diff --check`。
- [ ] 在 bj 只读检查资源基线，分阶段启用 4+4；记录内存、CPU、磁盘、进程、服务和四个备用状态。
- [ ] 触发一个受控出口故障，验证对应备用提升、其他出口不变、备用后台补齐。
- [ ] 复查日志和重启恢复条件；不执行破坏性数据库或证书操作。

### Task 5: 提交和推送

- [ ] 复核工作区只包含源码、测试和文档，不包含 `.ovpn`、缓存或凭据。
- [ ] 提交本地 Git。
- [ ] 推送 `origin/main`，fetch 后核对本地与远程提交一致。
