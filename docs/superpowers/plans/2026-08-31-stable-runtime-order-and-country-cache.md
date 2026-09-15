# 运行节点稳定排序、切换故障修复与两层节点池实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保持四个运行出口和每出口单一公网协议的前提下，修复资源耗尽导致的状态分叉，并交付固定运行排序、端口展示和 30 条确定性有效节点缓存。

**Architecture:** AimiliVPN 先以全局/监听实例两级许可限制线程容量，并把 OpenVPN 启动改为可回滚事务；节点池使用 `nodes.json` 加安全的 `pool_metadata.json` 做确定性重平衡。Gateway 只消费闭合安全字段，在修改 Xray 前验证槽位身份和 mixed 可用性，前端把运行分区与候选排序分开。

**Tech Stack:** Python 3 标准库、`unittest`、Go 1.26、Vue 3、TypeScript、Vitest、systemd、Xray/3x-ui。

**Spec:** `docs/superpowers/specs/2026-08-31-stable-runtime-order-and-country-cache-design.md`

## 全局约束

- 只保留主连接、出口1、出口2、出口3，共四个 AimiliVPN 运行出口。
- 每个逻辑出口同时只有一个公网协议；mixed/SOCKS5H 不参与协议切换。
- 不恢复 `21000`、balancer 或额外公网入站。
- 有效缓存硬上限固定为 30；全部检测失败的国家不占名额。
- 不删除或修改非 Gateway 受管 3x-ui/Xray 资源。
- 不输出或提交密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- 生产每一级失败只回滚本级。

---

### Task 1: AimiliVPN 代理容量隔离

**Files:**
- Create: `aimili-vpngate/tests/test_proxy_capacity.py`
- Modify: `aimili-vpngate/proxy_server.py`
- Modify: `aimili-vpngate/install.sh`

**Interfaces:**
- Produces: `ProxyCapacity(global_limit: int, per_listener_limit: int)` 与 `start_proxy_server(..., capacity: ProxyCapacity | None = None)`；默认全局 24、每监听 6。

- [ ] **Step 1: 写失败测试**：用两个监听许可验证 A 耗尽不阻塞 B；令线程工厂的 `start()` 抛出 `RuntimeError`，断言客户端关闭且两级许可均可再次获取。
- [ ] **Step 2: 运行 RED**：`python -m unittest tests.test_proxy_capacity -v`，预期因 `ProxyCapacity` 尚不存在而失败。
- [ ] **Step 3: 最小实现**：封装有界两级许可；按“实例→全局→线程”顺序获取；任一步失败关闭 socket 并释放已取许可；工作线程 `finally` 释放许可。
- [ ] **Step 4: 运行 GREEN**：重跑定向测试，并运行 `python -m unittest discover -s tests -v`。
- [ ] **Step 5: 本地提交**：在 AimiliVPN 分支提交 `fix: isolate proxy connection capacity`。

### Task 2: OpenVPN 启动事务与孤儿回收

**Files:**
- Modify: `aimili-vpngate/tests/test_exit_slot_types.py`
- Modify: `aimili-vpngate/vpngate_manager.py`

**Interfaces:**
- Produces: `setup_policy_routing(...) -> bool`；失败退出路径统一终止并等待已启动子进程；同槽位重拨前只回收命令环境含精确 `AIMILI_SLOT=<n>` 的未登记受管进程。

- [ ] **Step 1: 写失败测试**：分别注入日志线程启动失败、策略路由三次失败和未登记同槽位进程，断言子进程已 `terminate/wait`、槽位不进入 `exit_slots`、其他槽位与非受管进程不受影响。
- [ ] **Step 2: 运行 RED**：`python -m unittest tests.test_exit_slot_types -v`，确认失败来自缺失清理或错误返回契约。
- [ ] **Step 3: 最小实现**：让策略路由返回布尔值；在 `run_openvpn_until_ready` 的日志线程、握手、路由异常路径调用统一回收；拨号前精确枚举并清理同槽位受管进程。
- [ ] **Step 4: 运行 GREEN**：定向测试后运行全套 Python 测试，并执行 `python -m py_compile proxy_server.py node_pool.py vpngate_manager.py control_api.py`。
- [ ] **Step 5: 本地提交**：在 AimiliVPN 分支提交 `fix: roll back failed openvpn slot startup`。

### Task 3: 30 条确定性有效节点池与元数据

**Files:**
- Modify: `aimili-vpngate/node_pool.py`
- Modify: `aimili-vpngate/vpngate_manager.py`
- Modify: `aimili-vpngate/tests/test_node_pool.py`
- Modify: `aimili-vpngate/tests/test_pool_maintenance.py`
- Modify: `aimili-vpngate/tests/test_control_api.py`

**Interfaces:**
- Produces: `rebalance_valid_pool(existing, refreshed, protected_ids, manual_ids, limit=30) -> list[Node]`；`pool_metadata.json` schema v1；国家目录和刷新快照只返回安全统计。

- [ ] **Step 1: 写失败测试**：覆盖硬上限、输入顺序稳定、运行节点保护、每个成功国家锚点、旧健康优先、住宅失败回退机房、单国刷新保留其他国家、手动刷新跨一次维护保护、元数据损坏恢复。
- [ ] **Step 2: 运行 RED**：`python -m unittest tests.test_node_pool tests.test_pool_maintenance tests.test_control_api -v`，确认因重平衡器/元数据字段缺失失败。
- [ ] **Step 3: 最小实现**：固定容量 30；按硬保护→手动成功→国家锚点→旧健康→新住宅→新机房→稳定质量键选择；原子写 `pool_metadata.json`，损坏时保留可验证的 `nodes.json`。
- [ ] **Step 4: 运行 GREEN**：运行三组定向测试和全套 Python 测试；验证安全响应不含配置正文或认证字段。
- [ ] **Step 5: 本地提交**：在 AimiliVPN 分支提交 `feat: add deterministic valid node cache`。

### Task 4: Gateway 安全统计与切换前置门

**Files:**
- Modify: `aimili-gateway/internal/adapters/aimili/client.go`
- Modify: `aimili-gateway/internal/adapters/aimili/client_test.go`
- Modify: `aimili-gateway/internal/httpapi/backend_handlers_test.go`
- Modify: `aimili-gateway/internal/orchestrator/protocolmode.go`
- Modify: `aimili-gateway/internal/orchestrator/protocolmode_test.go`

**Interfaces:**
- Consumes: AimiliVPN 国家/刷新统计与受管槽位安全快照。
- Produces: 扩展的 `CandidateCountry`/`CountryRefresh` 安全字段；协议切换在任何 Xray 写入前执行槽位身份、`up` 状态和 mixed 实测门。

- [ ] **Step 1: 写失败测试**：模拟 pending/down、候选身份不一致和 mixed 不可用，断言 `SwitchProtocolModeExpected` 返回安全错误且 Xray apply/finalize 调用数为 0；验证统计字段转发。
- [ ] **Step 2: 运行 RED**：`go test ./internal/adapters/aimili ./internal/httpapi ./internal/orchestrator -run 'Country|ProtocolMode' -count=1`。
- [ ] **Step 3: 最小实现**：扩展闭合 JSON 类型校验；复用现有 Aimili 客户端与协议验证器，在事务快照前加前置门，不改变既有目标出口单级回滚。
- [ ] **Step 4: 运行 GREEN**：重跑定向测试和 `go test ./... -race -count=1`。
- [ ] **Step 5: 本地提交**：在 Gateway 分支提交 `fix: gate protocol changes on healthy exit state`。

### Task 5: 固定排序、端口列与两层国家界面

**Files:**
- Modify: `aimili-gateway/web/src/api/client.ts`
- Modify: `aimili-gateway/web/src/views/PoolView.vue`
- Modify: `aimili-gateway/web/src/components/PoolTable.vue`
- Modify: `aimili-gateway/web/src/components/PoolFilters.vue`
- Modify: `aimili-gateway/web/src/views/PoolViews.spec.ts`

**Interfaces:**
- Produces: 运行排序键 `agw-main=0`、`slotNumber 1..3`；候选排序从 100 开始；端口显示使用 `publicPort ?? vlessPort` 与 `mixedPort`；筛选国家和补充国家使用不同数据源。

- [ ] **Step 1: 写失败测试**：对国家、IP、协议、延迟、状态、更新时间排序逐一断言前四行为固定逻辑顺序；断言端口文案；断言两个国家入口的数据源、统计和刷新结果。
- [ ] **Step 2: 运行 RED**：`npm test --prefix web -- PoolViews.spec.ts`，确认现有排序/界面使新增断言失败。
- [ ] **Step 3: 最小实现**：先分运行/候选，再分别排序并拼接；加入端口列；将“只看现有国家”与“手动补充国家”拆开；展示安全统计。
- [ ] **Step 4: 运行 GREEN**：`npm test --prefix web` 与 `npm run build --prefix web`。
- [ ] **Step 5: 本地提交**：在 Gateway 分支提交 `feat: stabilize runtime rows and country controls`。

### Task 6: 本地回归、文档与 Git 推送

**Files:**
- Modify: `aimili-gateway/docs/verification/2026-08-31-stable-runtime-order-and-country-cache.md`
- Modify: `aimili-gateway/README.md`
- Modify: `aimili-vpngate/README.md`

- [ ] **Step 1: 全量验证**：AimiliVPN 运行全套 `unittest`、`py_compile`；Gateway 运行 `npm test --prefix web`、`npm run build --prefix web`、`go test ./... -race -count=1`、`go vet ./...`、两个二进制构建和 `git diff --check`。
- [ ] **Step 2: 复杂度审查**：按 `ponytail-review` 只检查可删除的多余抽象，修正后重跑受影响测试。
- [ ] **Step 3: 记录证据**：只写脱敏命令、通过数、失败边界与未验证项，不记录秘密或完整订阅。
- [ ] **Step 4: 本地提交并推送**：分别提交两个仓库，非强制推送功能分支；`git fetch` 后比较本地 HEAD 与远程跟踪分支。

### Task 7: `ssh ny` 阶梯部署和最终验收

**Files:**
- Modify: `aimili-gateway/docs/verification/2026-08-31-stable-runtime-order-and-country-cache.md`

- [ ] **Step 1: Stage 0**：只读核对四服务、四槽位、四公网入站、四 mixed、单 Xray PID、磁盘/内存/Swap；备份数据库、配置和部署文件；记录非 Gateway 3x-ui 资源脱敏指纹。
- [ ] **Step 2: Stage 1**：上传不含事务配置与秘密的 AimiliVPN 资产，部署容量/事务/节点池修复；精确清理未登记受管进程，重启 AimiliVPN，验证四槽位身份和 mixed。
- [ ] **Step 3: Stage 2**：部署 Gateway 后端；仅通过正式 check/repair 路径收敛状态，确认未触碰非 Gateway 资源。
- [ ] **Step 4: Stage 3**：部署前端，浏览器验收固定顺序、两类端口、两个国家入口和统计。
- [ ] **Step 5: Stage 4**：刷新订阅，在 v2rayN 7.24.4 逐条测试四公网节点；同时验证四 mixed、代理 DNS、真实出口和非目标出口不变。
- [ ] **Step 6: Stage 5**：重启 AimiliVPN、Gateway、x-ui/Xray 后复验；连续 300 秒观察 Tasks、线程、内存、Swap、服务重启、错误日志、单 Xray PID和四出口数量。
- [ ] **Step 7: 生产证据提交与推送**：补全脱敏验收文档，本地提交并正常推送；最终 `fetch` 比较远程状态。

## 计划自检

- 设计中的排序、端口、两层国家入口、30 条上限、保护/回退/持久化、资源隔离、进程回收、切换前置门和生产验收均有对应任务。
- 函数与字段命名在相邻任务间一致；没有增加新的服务、运行出口、公网入站或第三方依赖。
- 计划没有未定义的占位步骤；所有实现任务均包含 RED、GREEN、全量回归和独立提交。
