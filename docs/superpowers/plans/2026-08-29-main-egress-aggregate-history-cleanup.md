# 主连接第 4 出口与聚合 VLESS 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将早期 `8443 → 127.0.0.1:7928` 主连接纳入 Gateway 可观测资源，新增单个聚合 VLESS 入口，并完成历史残留审计、受控清理与维护记录。

**Architecture:** 保留 AimiliVPN、3x-ui/Xray 和 Gateway 独立进程。第 4 个出口使用 AimiliVPN 主连接而不是新增 OpenVPN 槽位；Gateway 为该出口建立受管元数据和 mixed 入口。聚合 VLESS 使用独立入站、单个客户端和 Xray balancer，仅把连接分配到健康的 Gateway 出站。

**Tech Stack:** Go Gateway、SQLite migrations、3x-ui HTTP API、AimiliVPN loopback control/API、Xray routing balancer、Vue/TypeScript、Bash/Python 只读审计脚本。

**Spec:** `docs/superpowers/specs/2026-08-28-xui-upgrade-capacity-country-refresh-design.md` 及本任务用户批准的 `main/7928` 增量设计。

## Global Constraints

- 不重写 Xray 或 3x-ui 核心，不删除 3x-ui 上游源码。
- 不把 Python AimiliVPN 与 Go Gateway 合成单个进程。
- 不接管、覆盖或删除未明确标记为 Gateway 受管的 3x-ui 资源；早期 `8443` 先保留兼容。
- 不输出或提交密码、Cookie、令牌、TOTP 秘钥、私钥、UUID、完整订阅或代理链接。
- 所有删除操作必须先备份、生成审计清单，并且仅删除确认无引用的历史对象。
- 生产容量不尝试新增 OpenVPN 槽位 4；第 4 组仅复用主连接并以 canary 方式验证。

### Task 1: 建模主连接出口与聚合配置

**Files:**
- Modify: `internal/domain/proxygroup.go`
- Modify: `internal/store/migrations/`
- Modify: `internal/store/proxygroups.go`
- Modify: `internal/config/config.go`
- Test: `internal/domain/*_test.go`, `internal/store/*_test.go`

- [ ] 写失败测试：主连接来源可持久化、槽位来源仍保持唯一约束、聚合配置可读取。
- [ ] 运行定向 Go 测试并确认因缺少字段/迁移失败。
- [ ] 添加 `egress_source`（`slot`/`main`）和聚合配置表，保持旧记录迁移为 `slot`。
- [ ] 运行定向测试，再运行全部 Gateway 单元测试。

### Task 2: 适配主连接状态与 8443 兼容组

**Files:**
- Modify: `internal/adapters/aimili/client.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/orchestrator/reconcile.go`
- Modify: `internal/adapters/xui/client.go`
- Test: 对应 `*_test.go`

- [ ] 写失败测试：主连接状态可读取；主连接组不调用 slot 删除；旧 `8443` 路由可被只读识别。
- [ ] 运行测试确认失败。
- [ ] 实现主连接读取、唯一出口检查和 `main` 资源生命周期。
- [ ] 以非破坏方式识别早期 `aimili-reality`，仅在满足标签、端口、路由和 7928 目标时建立兼容元数据。
- [ ] 为主连接组创建受管 mixed 入口，保留旧 VLESS 客户端兼容性。
- [ ] 运行全部 Go 测试和静态检查。

### Task 3: 单个聚合 VLESS 入站与 balancer/故障切换

**Files:**
- Modify: `internal/adapters/xui/client.go`
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/proxy_handlers.go`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/views/PoolView.vue`
- Test: Go HTTP/XUI/orchestrator tests and Vue tests

- [ ] 写失败测试：聚合 URI 只有一个地址；聚合路由只引用 ready 出站；没有 ready 组时拒绝导出。
- [ ] 运行测试确认失败。
- [ ] 实现固定聚合入站、单独客户端、受管 balancer 和故障切换规则。
- [ ] 增加聚合 URI 查询和复制入口，不把秘密写入普通 JSON 或日志。
- [ ] 运行 Go 与 Web 测试、构建和 `go vet`。

### Task 4: 历史残留审计与受控清理工具

**Files:**
- Create: `scripts/audit-history-remote.sh`
- Create: `scripts/cleanup-history-remote.sh`
- Create: `docs/maintenance/2026-08-29-history-residuals-maintenance.md`
- Create: `docs/maintenance/README.md`
- Test: shell self-tests and static safety checks

- [ ] 写失败测试：审计必须区分受管/非受管、活动/孤儿、可清理/仅记录。
- [ ] 运行测试确认失败。
- [ ] 实现只读审计：入站、Xray 路由、客户端引用、旧 Gateway 数据库、备份、systemd timer、AimiliVPN stash 和临时文件。
- [ ] 实现带明确 `--apply` 的清理：仅清理确认孤儿客户端、过期回滚副本和明确无效旧数据库；默认 dry-run。
- [ ] 维护文档记录“不建议清理”清单、证据、保留期限和回滚方法。
- [ ] 运行 shell 自测，禁止输出敏感字段。

### Task 5: 本地与生产阶梯验收

**Files:**
- Modify: `docs/verification/2026-08-29-main-aggregate-history.md`
- Use: existing deployment/verification scripts

- [ ] 本地运行 Go、Web、AimiliVPN 和部署合同测试。
- [ ] `ny` 只读 preflight，确认 `8443 → 7928` 和历史清单。
- [ ] 备份后部署 Gateway 与受管 mixed/aggregate 配置。
- [ ] 按 4th-main canary 验证 VLESS、SOCKS5H、代理 DNS、出口唯一性、故障切换和内存；失败立即恢复容量 3。
- [ ] 执行历史审计，只有审计清单批准项使用 `--apply` 清理。
- [ ] 重新验证原有三个组、旧 8443 兼容性、聚合入口和非受管资源不变。

