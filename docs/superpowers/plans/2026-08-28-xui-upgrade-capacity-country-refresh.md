# 3x-ui 升级、在线容量扩展与按国家刷新实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 将 3x-ui 安全升级到 v3.7.0，为 AimiliVPN 和 Gateway 增加按国家刷新与多地址复制，并在 512 MiB VPS 上把在线容量安全地从 1阶梯尝试到 2、再到 3。

**架构：** AimiliVPN 继续负责完整 VPNGate 目录获取、国家过滤、串行 OpenVPN 精验和节点池原子合并，并通过仅回环 `/control/v1` 暴露版本化后台任务。Gateway 通过适配器触发和观察刷新、同步 standby 目录、保证 ready 组真实出口 IP 唯一，并复用现有导出端点向前端提供聚合复制。3x-ui 使用官方固定 v3.7.0 发布资产升级，数据库和二进制整体备份、整体回滚。

**技术栈：** Python 3 标准库、`unittest`、Go 1.26.x、Vue 3、TypeScript、Vitest、PowerShell、Bash、systemd、Caddy、SQLite、OpenVPN、3x-ui/Xray。

**设计：** `docs/superpowers/specs/2026-08-28-xui-upgrade-capacity-country-refresh-design.md`

## 全局约束

- 3x-ui 只升级到官方稳定版 `v3.7.0`，拒绝 `latest`、`main` 和 `dev-latest`。
- OpenVPN 精验并发保持 1；单国目标 5 个有效节点，单次最多精验 20 个候选。
- `MAX_EXIT_SLOTS=4` 保持不变；生产 `maxProxyGroups` 只按 1→2→3 提升，不尝试 4。
- 所有 ready 组的实际出口 IP 必须唯一；重复时只轮换新槽位，最多 3 次。
- 按国家刷新不得停止当前隧道、删除其他国家节点或删除主连接/受管槽位使用中的节点。
- 三个服务继续独立运行；不改写 Xray/3x-ui 核心，不合并 Python 与 Go 进程。
- 不输出密码、Cookie、UUID、私钥、随机后台路径、控制 token 或完整代理地址。
- `.go-cache/` 和其他用户既有无关文件保持不动。
- 每个完成结论必须有本轮最新测试或真实路径验证依据。

## 文件与责任边界

### AimiliVPN

- 修改 `node_pool.py`：国家过滤和按国家局部合并的纯函数。
- 修改 `vpngate_manager.py`：国家目录、刷新状态、后台任务和受保护节点集合。
- 修改 `control_api.py`：国家目录、启动刷新和读取状态的版本化接口。
- 修改 `tests/test_node_pool.py`：纯函数 TDD。
- 修改 `tests/test_pool_maintenance.py`：刷新目标、上限、互斥和保护规则。
- 修改 `tests/test_control_api.py`：控制 API 鉴权、状态和脱敏合同。
- 修改 `README.md`：新能力和资源边界。

### Aimili Gateway

- 修改 `internal/adapters/aimili/client.go` 与测试：新控制 API 类型和方法。
- 修改 `internal/maintenance/service.go` 与测试：真实刷新、状态读取和完成后 reconcile。
- 修改 `internal/httpapi/settings_handlers.go`、`server.go` 与测试：Gateway 公共 API。
- 修改 `internal/orchestrator/orchestrator.go`、`reconcile.go` 与测试：实际出口 IP 唯一性。
- 修改 `web/src/api/client.ts`：国家目录和刷新状态类型。
- 修改 `web/src/views/PoolView.vue`、`PoolViews.spec.ts`：同步状态、按国家刷新和聚合复制。
- 修改 `web/src/views/AimiliSettingsView.vue`、对应测试：真实维护状态。
- 修改 `web/src/components/PoolFilters.vue`：支持最近官方国家目录。
- 修改 `internal/adapters/xui/*_test.go`：v3.7.0 合同夹具。
- 新建 `scripts/upgrade-xui-v370-remote.sh`：固定资产校验、备份、升级和失败回滚。
- 新建 `scripts/verify-capacity-step-remote.sh`：15 分钟容量采样和阈值判断。
- 新建或更新 `docs/verification/2026-08-28-ny-xui-country-refresh-capacity.md`：本次验证记录。

### 简化部署项目

- 修改 `aimili-3xui-simple-deploy/remote/install.sh`：新安装固定 v3.7.0 与对应提交/摘要。
- 修改 `aimili-3xui-simple-deploy/tests/run.ps1`：固定版本和已有安装不降级合同。
- 修改 `aimili-3xui-simple-deploy/README.md`：新安装版本说明。

---

### 任务 1：AimiliVPN 国家过滤与局部合并纯函数

**文件：**

- 修改：`../aimili-vpngate/node_pool.py`
- 测试：`../aimili-vpngate/tests/test_node_pool.py`

**接口：**

- 产出：`filter_country_rows(rows, country) -> list[dict]`
- 产出：`protected_node_ids(active_node_id, slot_node_ids) -> set[str]`
- 产出：`merge_country_pool(existing_nodes, refreshed_nodes, country, protected_ids, target_size) -> list[Node]`

- [ ] **步骤 1：写失败测试**

覆盖国家过滤先于数量截断、其他国家原样保留、所选国家只保留成功结果、受保护节点即使失败仍保留、按节点 ID 去重。

```python
def test_merge_country_pool_preserves_other_countries_and_active_slots():
    existing = [jp_old, jp_active, us_existing]
    merged = node_pool.merge_country_pool(
        existing, [jp_new], "JP", {jp_active["id"]}, target_size=5
    )
    assert [n["id"] for n in merged] == [us_existing["id"], jp_active["id"], jp_new["id"]]
```

- [ ] **步骤 2：确认测试失败**

运行：`python -m unittest tests.test_node_pool -v`  
预期：新增函数不存在或断言失败。

- [ ] **步骤 3：实现最小纯函数**

实现严格大写国家代码比较、稳定顺序、集合去重和目标数量限制，不读写文件、不启动进程。

- [ ] **步骤 4：确认测试通过**

运行：`python -m unittest tests.test_node_pool -v`  
预期：全部通过。

- [ ] **步骤 5：提交 AimiliVPN 纯函数**

```bash
git add node_pool.py tests/test_node_pool.py
git commit -m "feat: add country scoped node pool merge"
```

### 任务 2：AimiliVPN 国家刷新后台任务

**文件：**

- 修改：`../aimili-vpngate/vpngate_manager.py`
- 测试：`../aimili-vpngate/tests/test_pool_maintenance.py`
- 测试：`../aimili-vpngate/tests/test_probe_batches.py`

**接口：**

- 产出：`country_catalog_snapshot() -> list[dict[str, Any]]`
- 产出：`country_refresh_snapshot() -> dict[str, Any]`
- 产出：`start_country_refresh(country: str) -> dict[str, Any]`
- 内部：`refresh_country_nodes(country: str, target_size: int = 5, max_probes: int = 20) -> None`

- [ ] **步骤 1：写失败测试**

用假 CSV、假 `probe_nodes` 和临时数据目录覆盖：完整目录获取、只解码所选国家、最多精验 20、达到 5 即停止、并发 1、活动主节点和全部槽位节点受保护、其他国家不变、写入失败保持旧文件、维护锁忙时返回 `maintenance_busy`。

```python
def test_country_refresh_caps_real_probes_and_preserves_slots():
    result = manager.refresh_country_nodes("JP", target_size=5, max_probes=20)
    self.assertLessEqual(result["testedCount"], 20)
    self.assertIn(slot_node_id, ids(read_nodes()))
    self.assertEqual(us_before, country_nodes(read_nodes(), "US"))
```

- [ ] **步骤 2：确认测试失败**

运行：`python -m unittest tests.test_pool_maintenance tests.test_probe_batches -v`  
预期：刷新接口不存在或行为不符合断言。

- [ ] **步骤 3：实现后台状态机**

增加进程内锁保护的状态对象，状态仅含 `idle/running/completed/failed`、阶段、国家、计数、时间和稳定错误码；下载全量 CSV 后先记录国家目录，再过滤、解码、预筛和串行精验。文件使用现有原子 JSON 写入方式；配置文件只为最终保留节点落盘。

- [ ] **步骤 4：确认刷新测试通过**

运行：`python -m unittest tests.test_pool_maintenance tests.test_probe_batches -v`  
预期：全部通过，测试中没有真实网络或 OpenVPN 调用。

- [ ] **步骤 5：提交后台任务**

```bash
git add vpngate_manager.py tests/test_pool_maintenance.py tests/test_probe_batches.py
git commit -m "feat: refresh VPNGate nodes by country"
```

### 任务 3：AimiliVPN 版本化控制 API

**文件：**

- 修改：`../aimili-vpngate/control_api.py`
- 修改：`../aimili-vpngate/tests/test_control_api.py`
- 修改：`../aimili-vpngate/README.md`

**接口：**

- `GET /control/v1/candidates/countries`
- `POST /control/v1/candidates/refresh`，正文 `{"country":"JP"}`，成功 HTTP 202
- `GET /control/v1/candidates/refresh`
- capabilities 增加 `candidate-countries.read`、`candidates.refresh.country`、`candidates.refresh.status`

- [ ] **步骤 1：写失败合同测试**

覆盖 bearer 鉴权、国家格式、未知字段、202、busy 映射、状态结构以及响应中不含配置、token、密码或异常原文。

```python
status, body = self.request("POST", "/control/v1/candidates/refresh", {"country": "JP"})
self.assertEqual(status, HTTPStatus.ACCEPTED)
self.assertEqual(body["data"]["state"], "running")
```

- [ ] **步骤 2：确认合同测试失败**

运行：`python -m unittest tests.test_control_api -v`  
预期：新路径返回 404。

- [ ] **步骤 3：实现端点和错误映射**

复用 `_read_object`、`_manager_result` 和现有控制 token；严格验证 `[A-Z]{2}`，将 `maintenance_busy` 映射为 HTTP 409，其余内部异常映射为脱敏 500。

- [ ] **步骤 4：运行 AimiliVPN 全量测试**

运行：`python -m unittest discover -s tests -v`  
预期：全部通过。

- [ ] **步骤 5：提交控制合同**

```bash
git add control_api.py tests/test_control_api.py README.md
git commit -m "feat: expose country refresh control API"
```

### 任务 4：Gateway AimiliVPN 适配器与维护服务

**文件：**

- 修改：`internal/adapters/aimili/client.go`
- 修改：`internal/adapters/aimili/client_test.go`
- 修改：`internal/maintenance/service.go`
- 修改：`internal/maintenance/service_test.go`

**接口：**

- `Client.CandidateCountries(ctx context.Context) ([]CandidateCountry, error)`
- `Client.StartCountryRefresh(ctx context.Context, country string) (CountryRefresh, error)`
- `Client.CountryRefresh(ctx context.Context) (CountryRefresh, error)`
- `Service.StartAimiliVPNRefresh(ctx context.Context, country string) (CountryRefresh, error)`
- `Service.AimiliVPNRefresh(ctx context.Context) (CountryRefresh, error)`

- [ ] **步骤 1：写失败适配器测试**

断言准确 method/path/body、202/409/401/结构缺失映射、未知响应字段可忽略、原始上游文本不会进入错误。

- [ ] **步骤 2：确认适配器测试失败**

运行：`go test ./internal/adapters/aimili ./internal/maintenance -count=1`  
预期：新类型或方法不存在。

- [ ] **步骤 3：实现类型、适配器和维护服务**

复用现有 loopback 限制和 bearer token client；维护服务的旧 `RefreshAimiliVPN()` 不再返回旧快照。刷新启动后使用有界后台轮询，完成时调用注入的 `Reconcile(context.Context) error`；Gateway 退出会取消轮询，AimiliVPN 任务继续运行。

- [ ] **步骤 4：确认适配器与服务测试通过**

运行：`go test ./internal/adapters/aimili ./internal/maintenance -count=1`  
预期：全部通过。

- [ ] **步骤 5：提交 Gateway 适配层**

```bash
git add internal/adapters/aimili internal/maintenance
git commit -m "feat: integrate country scoped VPN refresh"
```

### 任务 5：Gateway 刷新 HTTP API

**文件：**

- 修改：`internal/httpapi/settings_handlers.go`
- 修改：`internal/httpapi/backend_handlers_test.go`
- 修改：`internal/httpapi/server.go`

**接口：**

- `GET /api/v1/settings/aimilivpn/countries`
- `POST /api/v1/settings/aimilivpn/refresh`
- `GET /api/v1/settings/aimilivpn/refresh`

- [ ] **步骤 1：写失败 HTTP 测试**

覆盖认证、Origin、CSRF、幂等键、国家校验、202、409 busy、状态查询和秘密过滤。

```go
response := environment.requestWithHeaders(t, http.MethodPost,
    "/api/v1/settings/aimilivpn/refresh", []byte(`{"country":"JP"}`),
    environment.origin, csrf, map[string]string{"Idempotency-Key": "refresh-jp"})
assertResponseStatus(t, response, http.StatusAccepted)
```

- [ ] **步骤 2：确认 HTTP 测试失败**

运行：`go test ./internal/httpapi -count=1`  
预期：路由 404 或接口不匹配。

- [ ] **步骤 3：实现处理器**

POST 使用现有 mutation 授权和幂等缓存；GET 只需有效登录。响应仅输出设计文档列出的国家和刷新字段。

- [ ] **步骤 4：确认 HTTP 测试通过**

运行：`go test ./internal/httpapi -count=1`  
预期：全部通过。

- [ ] **步骤 5：提交 HTTP API**

```bash
git add internal/httpapi
git commit -m "feat: add Gateway country refresh endpoints"
```

### 任务 6：实际出口 IP 唯一性

**文件：**

- 修改：`internal/orchestrator/orchestrator.go`
- 修改：`internal/orchestrator/orchestrator_test.go`
- 修改：`internal/orchestrator/reconcile.go`
- 修改：`internal/orchestrator/reconcile_test.go`

**接口：**

- 内部：`readyExitIPs(ctx context.Context, exceptID string) (map[string]struct{}, error)`
- 内部：`ensureUniqueExit(ctx context.Context, groupID string, slot aimili.Slot) (aimili.Slot, error)`
- 稳定错误码：`duplicate_exit_ip`

- [ ] **步骤 1：写失败编排测试**

覆盖新出口不重复、重复后轮换成功、连续三次重复后删除新槽位、已有组不变、reconcile 对历史重复只降级后创建的组。

- [ ] **步骤 2：确认编排测试失败**

运行：`go test ./internal/orchestrator -count=1`  
预期：重复出口仍被接受。

- [ ] **步骤 3：实现最小去重**

在槽位真实 egress check 完成后、创建 3x-ui 资源前检查 `net.ParseIP` 规范化结果。最多调用 `RotateSlot` 3 次；失败时执行现有补偿删除路径。reconcile 按持久组创建/版本顺序保留较早 ready 组并标记后者 degraded。

- [ ] **步骤 4：确认编排测试通过**

运行：`go test ./internal/orchestrator -count=1`  
预期：全部通过。

- [ ] **步骤 5：提交出口去重**

```bash
git add internal/orchestrator
git commit -m "feat: keep managed exit IPs unique"
```

### 任务 7：Gateway 前端按国家刷新与聚合复制

**文件：**

- 修改：`web/src/api/client.ts`
- 修改：`web/src/components/PoolFilters.vue`
- 修改：`web/src/views/PoolView.vue`
- 修改：`web/src/views/PoolViews.spec.ts`
- 修改：`web/src/views/AimiliSettingsView.vue`
- 修改：`web/src/views/AimiliSettingsView.spec.ts`

**接口：**

- `CandidateCountryPayload`
- `CountryRefreshPayload`
- 复用 `apiDownloadText('/api/v1/proxy-groups/export?...')`

- [ ] **步骤 1：写失败组件测试**

断言官方国家目录驱动下拉、未选国家禁止刷新、POST 只发国家代码、状态轮询、原按钮改名“同步代理状态”、复制 VLESS/SOCKS5H 当前过滤结果、空文本不覆盖剪贴板。

```ts
mocks.apiDownloadText.mockResolvedValue('vless://one\nvless://two\n')
await wrapper.get('[data-copy-all]').trigger('click')
expect(mocks.clipboard).toHaveBeenCalledWith('vless://one\nvless://two')
```

- [ ] **步骤 2：确认组件测试失败**

运行：`npm test --prefix web -- PoolViews.spec.ts AimiliSettingsView.spec.ts`  
预期：新按钮、类型或请求不存在。

- [ ] **步骤 3：实现前端交互**

轮询间隔 2 秒并在卸载时清理 timer；只在 `running` 时轮询；复制前移除末尾换行，空字符串显示“当前没有可用地址”；不写浏览器持久存储。

- [ ] **步骤 4：运行前端测试和构建**

运行：`npm test --prefix web`  
运行：`npm run build --prefix web`  
预期：全部通过且 TypeScript 构建成功。

- [ ] **步骤 5：提交前端**

```bash
git add web/src
git commit -m "feat: refresh countries and copy pooled addresses"
```

### 任务 8：3x-ui v3.7.0 合同、固定升级和新安装版本

**文件：**

- 修改：`internal/adapters/xui/client_test.go`
- 修改：`internal/adapters/xui/account_test.go`
- 新建：`scripts/upgrade-xui-v370-remote.sh`
- 修改：`../aimili-3xui-simple-deploy/remote/install.sh`
- 修改：`../aimili-3xui-simple-deploy/tests/run.ps1`
- 修改：`../aimili-3xui-simple-deploy/README.md`

**接口：**

- 脚本：`sudo bash upgrade-xui-v370-remote.sh --preflight|--apply|--rollback <backup-dir>`
- 固定标签：`v3.7.0`
- 固定发布资产 SHA-256：从官方发布元数据逐字节匹配设计阶段已验证值。

- [ ] **步骤 1：写失败合同和脚本静态测试**

3x-ui fixture 模拟 v3.7.0 的 CSRF、login epoch 会话、`updateUser` 空 TOTP、入站和 Xray 响应。简化部署测试断言新安装版本为 v3.7.0、已有安装仍跳过、脚本拒绝移动标签。

- [ ] **步骤 2：确认测试失败**

运行：`go test ./internal/adapters/xui -count=1`  
运行：`powershell -NoProfile -File ..\aimili-3xui-simple-deploy\tests\run.ps1`  
预期：v3.7.0 夹具或固定版本断言失败。

- [ ] **步骤 3：实现固定升级脚本**

`--preflight` 只读检查版本、磁盘、服务、数据库和资产摘要；`--apply` 在停止 x-ui 后备份 `/etc/x-ui`、`/usr/local/x-ui` 和 unit，执行固定发布更新并跑本机健康检查；失败自动恢复同一备份；`--rollback` 显式恢复指定备份。日志只记录路径存在、版本、摘要和状态。

- [ ] **步骤 4：确认合同和静态测试通过**

运行：`go test ./internal/adapters/xui -count=1`  
运行：`powershell -NoProfile -File ..\aimili-3xui-simple-deploy\tests\run.ps1`  
运行：`bash -n scripts/upgrade-xui-v370-remote.sh`  
预期：全部通过。

- [ ] **步骤 5：分别保存项目变更**

Gateway：

```bash
git add internal/adapters/xui scripts/upgrade-xui-v370-remote.sh
git commit -m "chore: prepare verified 3x-ui v3.7.0 upgrade"
```

`aimili-3xui-simple-deploy` 不是 Git 仓库，因此只保留经过测试的文件差异并在最终报告中明确列出，不伪造提交。

### 任务 9：容量采样脚本与本地完整验证

**文件：**

- 新建：`scripts/verify-capacity-step-remote.sh`
- 修改：`README.md`

**接口：**

- 脚本：`sudo bash verify-capacity-step-remote.sh <stage-start-utc> <expected-capacity> [duration-seconds]`
- 默认持续 900 秒、每 15 秒采样。

- [ ] **步骤 1：写脚本自测模式和失败断言**

增加 `--self-test`，用固定样本验证：连续两次 `MemAvailable<80 MiB`、Swap>512 MiB、五分钟增量>64 MiB、重启计数增加、failed unit、OOM 和 SSH 探测超时均返回非零。

- [ ] **步骤 2：确认自测先失败**

运行：`bash scripts/verify-capacity-step-remote.sh --self-test`  
预期：脚本尚不存在或用例失败。

- [ ] **步骤 3：实现采样和脱敏摘要**

脚本只输出时间、内存、Swap、服务状态、重启计数、OpenVPN 进程数、ready 组数和门槛结果，不输出配置或地址。

- [ ] **步骤 4：运行所有本地验证**

AimiliVPN：`python -m unittest discover -s tests -v`  
Gateway：`npm test --prefix web`、`npm run build --prefix web`、`go test ./... -race`、`go vet ./...`、构建两个 Go 二进制。  
脚本：`bash -n scripts/*.sh`、容量脚本 `--self-test`、简化部署 `tests/run.ps1`。  
预期：全部退出 0；Windows 不支持的竞态测试必须在 VPS 补跑并如实记录。

- [ ] **步骤 5：提交本地验证工具**

```bash
git add scripts/verify-capacity-step-remote.sh README.md
git commit -m "test: add guarded capacity verification"
```

### 任务 10：生产阶梯部署与端到端验收

**文件：**

- 新建：`docs/verification/2026-08-28-ny-xui-country-refresh-capacity.md`

**接口：**

- 部署主机：`ssh ny`
- 公网域名：`ny.zouyunhui.cc.cd`

- [ ] **步骤 1：最新只读预检**

记录部署开始 UTC 时间；核对四服务、版本、监听、磁盘、内存、Swap、重启计数、failed units、OpenVPN 进程、当前 ready 组和本时间之后的 OOM。任何关键基线异常先停止并实施当前故障的最小修复。

- [ ] **步骤 2：上传构建产物和回滚包**

只上传经过本地测试的 AimiliVPN bundle、Gateway 二进制/Web 资源、固定升级脚本和容量验证脚本；为三个服务各自建立带时间戳、权限 0700 的回滚目录并验证关键文件摘要。

- [ ] **步骤 3：升级 3x-ui v3.7.0**

运行固定升级脚本 `--preflight` 后执行 `--apply`。验证数据库迁移、Xray、Gateway 适配器、统一账户、自动进入专家后台和原有两种代理数据面；失败立即整体恢复 v3.6.0 二进制和旧数据库。

- [ ] **步骤 4：部署 AimiliVPN 与 Gateway 功能，容量保持 1**

先 AimiliVPN、后 Gateway；逐个重启，验证旧槽位、控制 capabilities、国家目录、刷新状态、账户同步、页面和现有代理。运行 Linux `go test ./... -race` 或等价已构建测试套件补足 Windows 限制。

- [ ] **步骤 5：执行一次按国家刷新**

从目录选择候选量适中的国家，启动刷新；验证只精验该国、并发 1、测试数≤20、目标≤5、其他国家和现有槽位不变、完成后 Gateway 出现 standby 候选。

- [ ] **步骤 6：容量从 1提升到 2**

修改 `maxProxyGroups=2`，重启 Gateway，启用一个出口不同的新组；验证 VLESS/SOCKS5H 后运行 900 秒容量脚本。任一阈值失败删除本阶新增组并恢复 1。

- [ ] **步骤 7：通过后从 2提升到 3**

仅当上一步全部通过时执行相同流程。失败恢复 2；不尝试 4。

- [ ] **步骤 8：验证聚合复制和重启恢复**

按国家/IP 类型筛选，分别复制全部 VLESS 和 SOCKS5H 地址，在独立客户端逐行验证；随后重启 Gateway，确认底层隧道和代理不中断、页面状态恢复。

- [ ] **步骤 9：写入最新验证记录并提交**

验证文档记录时间、非敏感命令、版本、阈值采样摘要、最终稳定容量、实际回滚和未完成项。不得记录任何完整连接地址或凭据。

```bash
git add docs/verification/2026-08-28-ny-xui-country-refresh-capacity.md
git commit -m "docs: verify xui upgrade and country refresh on ny"
```

## 完成检查

- 设计第 5 节由任务 8、10 覆盖。
- 设计第 6 节由任务 1～3、10 覆盖。
- 设计第 7 节由任务 4、5、7、10 覆盖。
- 设计第 8 节由任务 6、9、10 覆盖。
- 设计第 9 节由任务 7、10 覆盖。
- 安全、测试、发布和验收由所有任务的失败映射、任务 9、任务 10 覆盖。
- 计划没有依赖新增第三方运行库；所有新增实现使用现有标准库和项目依赖。
