# 出口可见性、中文反馈与动态订阅别名实施计划

> 实施时使用 `superpowers:test-driven-development` 逐任务执行；实现完成后使用 `ponytail-review` 做一次复杂度审查，并用 `superpowers:verification-before-completion` 复核最新证据。每项以 RED → GREEN → 回归 → 独立提交闭环。

**Goal:** 修复主连接身份漂移导致的协议切换失败，交付真实 IP、分页面单端口、中文分区通知、失败候选持久失效和随国家变化的四节点订阅别名。

**Architecture:** AimiliVPN 在既有临时拨号精验中取得候选真实 `exit_ip`，返回结构化刷新/失效结果；Gateway 先通过正式检查收敛主身份，再保留严格协议预检，并把安全状态转换成页面模型和四个动态别名；3x-ui v3.7.0 在 `client_inbounds` 增加默认空的逐关联别名覆盖，保证只影响 Gateway 专属订阅客户端。

**Tech Stack:** Python 3 标准库与 `unittest`、Go、SQLite/GORM、Vue 3、TypeScript、Vitest、PowerShell、Bash、systemd、3x-ui v3.7.0、Xray 26.7.28。

**Spec:** `docs/superpowers/specs/2026-09-01-egress-ux-and-dynamic-subscription-alias-design.md`

## 全局约束

- 只保留主连接、出口1、出口2、出口3四个运行出口。
- 每个逻辑出口同时只有一个公网协议；mixed/SOCKS5H 不参与协议切换。
- 不恢复 `21000`、balancer、observatory 或额外长期公网入站。
- 只修改 Gateway 受管入站和 `aimili-gateway-subscription` 关联，不删除非 Gateway 资源。
- 未启用候选的 `candidate_ip` 不得冒充 `exit_ip`；旧缓存缺口必须明确标记。
- VPN 节点池只显示公网端口，SOCKS5H 代理池只显示 mixed 端口。
- 不记录、提交或输出密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- 所有生产级别失败只回滚本级及其未提交事务。
- Gateway 与 AimiliVPN 在现有 `feat/main-switch-protocol-modes` 分支实施；`.deploy-assets/` 保留未跟踪且不得提交。
- `aimili-3xui-deploy` 最新基线没有远程地址；在该仓库建立本地功能分支并提交可复现补丁，除非实施时已存在明确远程，否则不得声称已推送。

---

### Task 1: AimiliVPN 候选真实出口 IP

**Files:**
- Modify: `aimili-vpngate/vpngate_manager.py`
- Create: `aimili-vpngate/tests/test_candidate_exit_ip.py`
- Modify: `aimili-vpngate/tests/test_pool_maintenance.py`
- Modify: `aimili-vpngate/tests/test_control_api.py`

**Interfaces:**
- Produces: `check_interface_exit_ip(interface: str, timeout: int = 6) -> tuple[bool, str]`。
- Extends cached candidate: `exit_ip`, `exit_ip_checked_at`。
- Extends safe candidate response: `exit_ip`、`exit_ip_checked_at`，继续保留 `ip` 作为节点入口地址。

- [ ] **Step 1: 写失败测试**：模拟临时 OpenVPN 握手成功，断言进程保持到 `check_interface_exit_ip("tunN")` 完成后才停止；覆盖第一个回显端点失败、第二个成功、非法 IP、两个端点都失败、异常时 TUN/进程必回收。
- [ ] **Step 2: 运行 RED**：`python -m unittest tests.test_candidate_exit_ip tests.test_pool_maintenance tests.test_control_api -v`，确认失败来自缺少接口出口实测和安全字段。
- [ ] **Step 3: 最小实现**：让 `_probe_one_node()` 在握手成功后短暂保留进程，通过 `curl --interface <tun>` 访问固定回显端点；使用 `ipaddress.ip_address()` 校验，成功写入时间戳，失败把候选标成 `unavailable`；统一 `finally` 停止进程并释放测试索引。
- [ ] **Step 4: 运行 GREEN**：重跑定向测试，再运行 `python -m unittest discover -s tests -v` 和 `python -m py_compile proxy_server.py node_pool.py vpngate_manager.py control_api.py vpn_utils.py`。
- [ ] **Step 5: 本地提交**：在 AimiliVPN 分支提交 `feat: verify cached candidate exit addresses`。

### Task 2: AimiliVPN 结构化刷新与失败候选持久失效

**Files:**
- Modify: `aimili-vpngate/vpngate_manager.py`
- Modify: `aimili-vpngate/node_pool.py`
- Modify: `aimili-vpngate/tests/test_pool_maintenance.py`
- Modify: `aimili-vpngate/tests/test_exit_slot_types.py`
- Modify: `aimili-vpngate/tests/test_main_assignment.py`
- Modify: `aimili-vpngate/tests/test_control_api.py`

**Interfaces:**
- Produces: `CountryRefresh.resultCode` 闭集：`success`、`no_official_candidates`、`no_usable_nodes`、`operation_busy`、`maintenance_busy`、`upstream_unavailable`。
- Produces: `mark_candidate_unavailable(candidate_id: str, reason_code: str, now: float | None = None) -> bool`，只接受 `candidate_dial_failed` 和 `candidate_egress_failed`。
- Replacement failure responses include safe `error_code` and `candidate_rejected: bool`。

- [ ] **Step 1: 写失败测试**：分别覆盖官方 0 条、探测后 0 条、维护锁、上游异常和成功计数；对主连接/槽位拨号失败和出口失败断言 blacklist、`nodes.json` 状态、30 条池重平衡和重启后仍不可选；对 Gateway/3x-ui 风格的外层错误断言不得淘汰候选。
- [ ] **Step 2: 运行 RED**：`python -m unittest tests.test_pool_maintenance tests.test_exit_slot_types tests.test_main_assignment tests.test_control_api -v`。
- [ ] **Step 3: 最小实现**：在刷新主路径直接产生结构化结果，不由顶层异常统一压成 `refresh_failed`；用原子写持久化候选失败和 blacklist；主/槽位失败路径只在得到候选自身失败证据时调用该接口并立即重平衡。
- [ ] **Step 4: 运行 GREEN**：运行上述定向测试、全套 `unittest` 和 `py_compile`；检查控制 API 不返回配置正文和异常原文。
- [ ] **Step 5: 本地提交**：提交 `fix: persist failed candidates and refresh results`。

### Task 3: Gateway 消费真实 IP 与结构化结果

**Files:**
- Modify: `aimili-gateway/internal/adapters/aimili/client.go`
- Modify: `aimili-gateway/internal/adapters/aimili/client_test.go`
- Modify: `aimili-gateway/internal/domain/proxygroup.go`
- Modify: `aimili-gateway/internal/domain/proxygroup_test.go`
- Modify: `aimili-gateway/internal/orchestrator/orchestrator.go`
- Modify: `aimili-gateway/internal/orchestrator/orchestrator_test.go`
- Modify: `aimili-gateway/internal/httpapi/proxy_handlers.go`
- Modify: `aimili-gateway/internal/httpapi/proxy_handlers_test.go`

**Interfaces:**
- Extends `aimili.Candidate`: `ExitIP string`、`ExitIPCheckedAt float64`，现有 `IP` 明确为节点入口。
- Extends `aimili.CountryRefresh`: `ResultCode`、`OfficialCount`、`UsableCount`、`RetainedCount`。
- Extends `domain.ProxyGroup`/HTTP response: `candidateIp`、`exitIp`、`exitIpCheckedAt`；候选不再把 `candidateIp` 填入 `exitIp`。

- [ ] **Step 1: 写失败测试**：输入候选 `ip != exit_ip`，断言 API 分字段输出；缺少 `exit_ip` 时保持空而不伪造；覆盖六种刷新结果、字段类型错误和超长 IP/结果码拒绝。
- [ ] **Step 2: 运行 RED**：`go test ./internal/adapters/aimili ./internal/domain ./internal/orchestrator ./internal/httpapi -run 'Candidate|CountryRefresh|ProxyGroup' -count=1`。
- [ ] **Step 3: 最小实现**：扩展闭合 DTO 和映射；保留旧 AimiliVPN 响应兼容，但不做错误语义猜测；候选失效后重新读取集合，让已拒绝候选立即从 Gateway 普通列表消失。
- [ ] **Step 4: 运行 GREEN**：重跑定向测试和 `go test ./... -race -count=1`。
- [ ] **Step 5: 本地提交**：提交 `feat: expose verified candidate exit addresses`。

### Task 4: Gateway 主身份安全同步后再切换协议

**Files:**
- Modify: `aimili-gateway/internal/orchestrator/protocolmode.go`
- Modify: `aimili-gateway/internal/orchestrator/protocolmode_test.go`
- Modify: `aimili-gateway/internal/orchestrator/subscription.go`
- Modify: `aimili-gateway/internal/orchestrator/mainassignment_test.go`

**Interfaces:**
- Adds internal `syncProtocolTargetEgress(ctx context.Context, target protocolTarget) (protocolTarget, error)`。
- Reuses: `checkMain(ctx, true)`；does not weaken `verifyEgressReady(ctx, *protocolTarget)`。

- [ ] **Step 1: 写失败测试**：构造 Gateway 旧主身份、AimiliVPN 当前健康身份，断言先调用 `checkMain(true)`、重新读取目标、再进入 `verifyEgressReady`，并允许事务继续；构造 mixed/当前公网协议/出口任一失败，断言 3x-ui prepare/apply/finalize 调用数为 0、原协议不变。
- [ ] **Step 2: 运行 RED**：`go test ./internal/orchestrator -run 'Protocol.*Main|Main.*Protocol|EgressReady' -count=1`。
- [ ] **Step 3: 最小实现**：只对 `agw-main` 执行正式同步；同步成功后重新加载 store 状态，仍使用全字段严格预检；同步失败保持旧记录和协议，返回稳定安全码。
- [ ] **Step 4: 运行 GREEN**：定向测试后运行 `go test ./internal/orchestrator -race -count=1` 与 `go test ./... -race -count=1`。
- [ ] **Step 5: 本地提交**：提交 `fix: reconcile main identity before protocol switch`。

### Task 5: Gateway 中文通知、IP 和分页面端口

**Files:**
- Create: `aimili-gateway/web/src/components/UiNotice.vue`
- Create: `aimili-gateway/web/src/components/errorMessages.ts`
- Create: `aimili-gateway/web/src/components/errorMessages.spec.ts`
- Modify: `aimili-gateway/web/src/api/client.ts`
- Modify: `aimili-gateway/web/src/views/PoolView.vue`
- Modify: `aimili-gateway/web/src/components/PoolTable.vue`
- Modify: `aimili-gateway/web/src/views/PoolViews.spec.ts`

**Interfaces:**
- Produces: `UiNotice { id, kind, title, message }`；`kind` 为 `success | error | progress | info`。
- Produces: `messageForCode(code: string, fallback: string): string` 和 `countryDisplayName(code, catalog)`。
- `PoolTable` uses `publicPort ?? vlessPort` only for `protocol="vless"`, `mixedPort` only for `protocol="socks5h"`。

- [ ] **Step 1: 写失败测试**：断言 `egress_unavailable` 不出现在可见文本、`US` 显示“美国”；成功绿、失败红、进行中蓝且均可关闭；国家刷新卡与顶部通知分离；替换失败留在弹窗内；候选显示 `exitIp`，缺失时显示带“节点 IP”标记的 `candidateIp`；VPN/SOCKS5H 页面各只出现自己的端口。
- [ ] **Step 2: 运行 RED**：`npm test --prefix web -- errorMessages.spec.ts PoolViews.spec.ts`。
- [ ] **Step 3: 最小实现**：用类型化通知替换字符串 `notice`；集中闭集映射；三个区域分别维护通知状态；弹窗错误不关闭弹窗；表格按页面选择 IP 标签和单一端口。
- [ ] **Step 4: 运行 GREEN**：`npm test --prefix web` 与 `npm run build --prefix web`；检查移动端标签和关闭按钮可访问名称。
- [ ] **Step 5: 本地提交**：提交 `feat: localize egress feedback and simplify ports`。

### Task 6: 3x-ui v3.7.0 逐关联订阅别名补丁

**Files（`aimili-3xui-deploy` 跟踪资产）:**
- Create: `patches/3x-ui-v3.7.0-client-inbound-alias.patch`
- Create: `scripts/build-xui-custom.ps1`
- Create: `scripts/deploy-xui-custom-remote.sh`
- Create: `tests/powershell/test-xui-custom-patch.ps1`
- Create: `tests/test-deploy-xui-custom.sh`
- Modify: `tests/run-all.ps1`
- Modify: `README.md`

**Patched upstream files（固定提交 `f727d04f6522bb94a8fb52e8352fdcafb51c11e1`）:**
- Modify: `internal/database/model/model.go`
- Modify: `internal/database/db_settled_test.go`
- Create: `internal/web/service/client_alias.go`
- Create: `internal/web/service/client_alias_test.go`
- Modify: `internal/web/controller/client.go`
- Create: `internal/web/controller/client_alias_test.go`
- Modify: `internal/sub/service.go`
- Create: `internal/sub/service_alias_test.go`

**Interfaces:**
- Adds `ClientInbound.AliasOverride string` → `client_inbounds.alias_override`，默认空。
- Adds authenticated endpoint `POST /panel/api/clients/:email/inboundAliases` with body `{"aliases":[{"inboundId":1,"alias":"..."}]}`。
- Extends client GET response with `inboundAliases`。
- Subscription render rule: non-empty association alias overrides `Inbound.Remark`; empty value preserves upstream behavior.

- [ ] **Step 1: 建立 RED 测试**：在固定 v3.7.0 临时源码检出中加入数据库、service、controller 和 raw/JSON/Clash 订阅测试；覆盖默认空兼容、逐客户端隔离、控制字符/超过 96 字符拒绝、未知关联拒绝、非目标客户端仍用原 remark。
- [ ] **Step 2: 运行 RED**：`go test ./internal/database ./internal/web/service ./internal/web/controller ./internal/sub -run 'Alias|ClientInbound' -count=1`，确认因模型、API和渲染覆盖缺失失败。
- [ ] **Step 3: 最小实现**：增加列和查询缓存；别名更新在单个数据库事务中只更新既有 `client_inbounds` 行；订阅请求按 `sub_id` 一次加载关联别名，避免逐链接查询；不修改入站 remark 和全局模板。
- [ ] **Step 4: 运行 GREEN**：重跑定向测试、`go test ./... -count=1` 和 `go build ./...`；从固定基线导出单一补丁，构建脚本验证提交哈希后才应用。
- [ ] **Step 5: 部署资产测试**：运行 `powershell -NoProfile -ExecutionPolicy Bypass -File .\tests\run-all.ps1`；shell 测试断言部署脚本先备份二进制/数据库、离线启动测试新二进制、原子安装、健康失败自动恢复，并且不打印敏感配置。
- [ ] **Step 6: 本地提交**：在 `aimili-3xui-deploy` 本地功能分支提交 `feat: add per-inbound subscription aliases`；准确记录该仓库当前无远程，未执行推送。

### Task 7: Gateway 设置、验证并回滚动态别名

**Files:**
- Modify: `aimili-gateway/internal/adapters/xui/models.go`
- Modify: `aimili-gateway/internal/adapters/xui/subscription.go`
- Modify: `aimili-gateway/internal/adapters/xui/subscription_test.go`
- Modify: `aimili-gateway/internal/orchestrator/subscription.go`
- Modify: `aimili-gateway/internal/orchestrator/subscription_test.go`
- Modify: `aimili-gateway/internal/orchestrator/mainassignment_test.go`
- Modify: `aimili-gateway/internal/orchestrator/protocolmode_test.go`

**Interfaces:**
- Extends `xui.SubscriptionDesired`: `Aliases map[int64]string`。
- Extends `xui.Subscription`: `Aliases map[int64]string`。
- Adds adapter operations `setSubscriptionAliases(...)` and exact `ValidateSubscriptionAliases(actual, expected)`。
- Adds `subscriptionAliases(main store.MainEgress, groups []domain.ProxyGroup) (map[int64]string, error)`。

- [ ] **Step 1: 写失败测试**：断言生成 `主连接_日本`、`出口位 1_日本` 等四个别名；国家替换后只改变目标别名；传入非受管入站、非专属客户端、重复逻辑角色或空国家时拒绝；3x-ui 返回映射不完整时事务失败。
- [ ] **Step 2: 运行 RED**：`go test ./internal/adapters/xui ./internal/orchestrator -run 'Subscription.*Alias|Replace.*Alias|Protocol.*Alias' -count=1`。
- [ ] **Step 3: 最小实现**：`EnsureSubscriptionClient` 在确认 `ownedPublicIDs` 后设置别名并重新读取精确验证；替换事务快照旧别名，目标公网/mixed/出口验证通过后更新别名，失败逆序恢复；协议切换只验证别名未丢失。
- [ ] **Step 4: 故障注入**：分别让别名 API 写入失败、写后读不一致、订阅拉取失败和别名回滚失败；断言前三种恢复旧 AimiliVPN/Gateway/别名，回滚失败标记 `repair_required`，非目标三出口与非 Gateway 资源调用数为 0。
- [ ] **Step 5: 运行 GREEN**：定向测试后运行 `go test ./... -race -count=1`。
- [ ] **Step 6: 本地提交**：提交 `feat: transact dynamic subscription aliases`。

### Task 8: 三仓库本地集成与文档

**Files:**
- Create: `aimili-gateway/scripts/verify-egress-ux-aliases.ps1`
- Create: `aimili-gateway/docs/verification/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`
- Modify: `aimili-gateway/README.md`
- Modify: `aimili-vpngate/README.md`

- [ ] **Step 1: AimiliVPN 全量验证**：运行全套 `unittest`、`py_compile`；用脱敏 fixture 证明候选入口 IP 与出口 IP 可不同、结构化刷新和持久失效重启后成立。
- [ ] **Step 2: 3x-ui 全量验证**：从干净固定提交应用补丁，运行全套 Go 测试和构建；创建两个订阅客户端关联同一入站，证明只有 Gateway 客户端得到覆盖别名。
- [ ] **Step 3: Gateway 全量验证**：运行 `npm test --prefix web`、`npm run build --prefix web`、`go test ./... -race -count=1`、`go vet ./...`、两个 Go 二进制构建和 `git diff --check`。
- [ ] **Step 4: 本地跨仓库测试**：启动临时 AimiliVPN/3x-ui fixture 和 Gateway；执行“旧主身份→协议切换”“候选国家替换→别名变化”“失败替换→候选淘汰/事务回滚”；只记录别名、逻辑角色、国家、端口和安全错误码。
- [ ] **Step 5: 复杂度审查**：调用 `ponytail-review`，只删除多余抽象、重复映射或不必要扩展点；修正后重跑受影响测试和全量验证。
- [ ] **Step 6: 文档与提交**：记录本轮最新命令、通过数和未验证生产层级；分别提交 Gateway 与 AimiliVPN 文档更新。
- [ ] **Step 7: 推送与远程核对**：正常推送 Gateway、AimiliVPN 功能分支；执行 `git fetch` 后比较本地 HEAD 与 `origin/feat/main-switch-protocol-modes`。不得强制推送。

### Task 9: `ssh ny` Stage 0～Stage 2 基础部署

**Files:**
- Modify: `aimili-gateway/docs/verification/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`

- [ ] **Step 1: Stage 0 只读基线**：核对 Gateway、AimiliVPN、x-ui/Xray、Caddy 状态；读取最新部署后日志；记录四个逻辑出口、四个公网入站、四个 mixed、单 Xray PID、主/槽位候选身份、协议、端口、脱敏非 Gateway 资源指纹、内存/Swap/Tasks 和客户端当前订阅元数据。
- [ ] **Step 2: Stage 0 备份**：建立 0700 时间戳目录，备份 Gateway 二进制/数据库/配置、AimiliVPN 代码与数据、x-ui 二进制/数据库、Caddy 片段和 systemd 覆盖；校验文件存在、owner/mode 和摘要。备份清单不得包含秘密正文。
- [ ] **Step 3: Stage 1 AimiliVPN**：上传不含生产配置和秘密的修复资产到 `/tmp`，先运行 `py_compile` 和定向测试，再原子安装；重启 AimiliVPN，只通过正式 check-slots/helper 恢复三槽位身份，验证主 `7928` 与三个槽位 mixed、真实出口、候选 `exit_ip` 和四出口数量。
- [ ] **Step 4: Stage 1 失败门**：任何主/槽位身份、mixed、进程数量或持久池异常立即恢复 AimiliVPN 本级备份；不继续 3x-ui。
- [ ] **Step 5: Stage 2 3x-ui 影子验证**：在 VPS 临时目录从固定源码/补丁构建或上传已验证同架构二进制；对生产数据库副本运行迁移，启动仅回环临时实例，验证旧订阅、别名覆盖和非 Gateway 记录未变。
- [ ] **Step 6: Stage 2 3x-ui 安装**：停止 x-ui 的最短窗口内原子替换二进制，启动后核对数据库迁移、面板回环、订阅回环、Xray 单进程和全部入站；失败立即恢复旧二进制和数据库。
- [ ] **Step 7: Stage 2 观察**：至少 300 秒观察 x-ui/Xray 重启次数、错误日志、内存/Swap、订阅响应和非 Gateway 资源指纹；未通过不得部署 Gateway 写别名能力。

### Task 10: `ssh ny` Stage 3～最终混合状态验收

**Files:**
- Modify: `aimili-gateway/docs/verification/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`

- [ ] **Step 1: Stage 3 Gateway 后端**：部署新二进制但先不执行替换；运行主/三槽位正式检查，确认 Gateway DB 与 AimiliVPN 身份收敛；逐个协议读检查，确认 mixed 未变。
- [ ] **Step 2: Stage 3 主协议阶梯**：主连接按当前协议→另一个协议→第三个协议→目标最终协议逐级切换，每级验证 Xray 离线配置、单目标公网协议、主 mixed、真实出口、订阅覆盖和其他三出口；任一级失败恢复该级旧协议。
- [ ] **Step 3: Stage 4 Gateway 前端**：部署静态资产；使用终端 HTTP/DOM 测试而非前台 computer use，验证顶部通知、国家刷新卡、弹窗错误、中文映射、关闭按钮、真实 IP/节点 IP标记和两页面单端口。
- [ ] **Step 4: Stage 5 国家刷新与失败替换**：选择有成功候选的国家验证结构化成功结果；选择当次无可用候选的国家验证中文失败卡；注入一个可控候选拨号/出口失败，证明弹窗内可见且候选重启后仍不在可用列表，然后恢复测试状态。
- [ ] **Step 5: Stage 5 动态别名**：将一个出口位从原国家替换到另一个已验证国家，确认只有对应别名变化；主/其余出口别名、四个入站 ID/tag/端口、mixed 和非 Gateway 资源不变；再验证失败事务能恢复旧别名与身份。
- [ ] **Step 6: Stage 6 客户端原路径**：刷新 v2rayN 订阅，逐条点击四个节点；核对别名、协议、端口、延迟和真实公网出口。再逐条验证四个 SOCKS5H、代理端 DNS；不得在记录中保存完整订阅链接或认证材料。
- [ ] **Step 7: Stage 7 最终混合状态**：按用户既定目标形成 TCP/Vision、XHTTP/REALITY、Hysteria2 共存且每出口单协议的四节点状态；连续 300 秒观察 Tasks、线程、内存、Swap、服务重启、最新错误、单 Xray PID和四出口数量。
- [ ] **Step 8: Stage 8 重启验收**：依次重启 AimiliVPN、x-ui/Xray、Gateway、Caddy；每次等待服务稳定后复验四公网、四 mixed、代理 DNS、动态别名、节点池持久化和非 Gateway 指纹。
- [ ] **Step 9: 生产证据提交与推送**：在验证文档中按“实际完成/真实验证/未执行/剩余限制”记录脱敏结果；提交 Gateway、AimiliVPN 和本地 3x-ui 部署资产；正常推送有远程的功能分支并在 `fetch` 后比较提交。

## 回滚矩阵

| 失败层 | 立即动作 | 必须保留 |
| --- | --- | --- |
| AimiliVPN 候选探测/持久化 | 恢复 AimiliVPN 代码与数据备份，重建原四出口 | 原槽位身份、mixed 端口 |
| 3x-ui 数据迁移/二进制 | 恢复旧二进制与 x-ui 数据库，启动并复核 Xray | 非 Gateway 资源、原订阅 |
| Gateway 后端身份同步 | 恢复 Gateway 二进制/数据库，不修改当前 Xray | 当前协议和 AimiliVPN 运行态 |
| 协议切换 | 使用现有目标级事务恢复旧协议 | 其他三出口、全部 mixed |
| 候选替换/别名 | 逆序恢复别名、受管关联、Gateway 状态、AimiliVPN 身份 | 稳定入站 ID/tag/端口 |
| Gateway 前端 | 恢复旧静态资源 | 后端已验证运行态 |

回滚后必须重新读取实际状态；数据库或页面显示不能替代公网协议、mixed 和真实出口复验。恢复不完整时保留 `repair_required` 并停止后续级别。

## 计划自检

- 主身份同步、严格预检、真实候选出口 IP、结构化国家刷新、失败候选持久失效、三类通知、中文映射、分页面端口、动态订阅别名和事务回滚均有独立 RED/GREEN 任务。
- 动态别名基于已核实的 3x-ui v3.7.0 `client_inbounds` 与 `genRemark()`，没有假定不存在的上游字段。
- 每个实现任务列出具体文件、接口、定向测试、全量回归和提交边界。
- 部署含 Stage 0 基线/备份、本级失败门、影子迁移、阶梯协议、v2rayN 原路径、300 秒观察与重启验收。
- 计划没有未定义占位步骤，不增加第三方依赖、运行出口、长期公网入站或订阅入口。
- Trojan/WS、VLESS/WS 和服务端自动选协议明确不在本轮范围。
