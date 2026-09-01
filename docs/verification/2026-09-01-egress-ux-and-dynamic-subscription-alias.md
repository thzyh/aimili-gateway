# 出口可见性、中文反馈与动态订阅别名验证记录

日期：2026-09-01（Asia/Shanghai）

## 范围与安全边界

本记录对应已批准设计 `docs/superpowers/specs/2026-09-01-egress-ux-and-dynamic-subscription-alias-design.md` 和实施计划 `docs/superpowers/plans/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`。

- 始终只保留主连接、出口1、出口2、出口3四个逻辑运行出口。
- 每个逻辑出口同时只有一个公网协议；mixed/SOCKS5H 不参与切换。
- 动态别名只作用于 `aimili-gateway-subscription` 与 Gateway 受管公网入站的关联。
- 本地 fixture 仅使用逻辑角色、国家、端口和安全错误码；没有保存或输出生产密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- 本文同时记录本地验证与已完成的 VPS 阶梯部署；未执行的人工客户端和专项故障注入证据单独列在末尾，不与自动化生产验收混淆。

## 本地代码基线

| 仓库 | 分支 | 本轮功能提交基线 | 远程状态（验证前） |
| --- | --- | --- | --- |
| `aimili-gateway` | `feat/main-switch-protocol-modes` | `be785dc` | 推送前比远程功能分支领先 3 个提交；保留未跟踪 `.deploy-assets/` |
| `aimili-vpngate` | `feat/main-switch-protocol-modes` | `f33517c` | 最新 `fetch` 后与远程功能分支差异 `0/0` |
| `aimili-3xui-deploy` | `feat/main-switch-protocol-modes` | `5dbe6f0` | 没有远程地址，未执行推送 |
| 3x-ui 固定源码 | detached `f727d04f6522bb94a8fb52e8352fdcafb51c11e1` | 应用可复现补丁 | 仅作临时测试和构建源 |

## 验证脚本 TDD

新增 `scripts/verify-egress-ux-aliases.ps1`。

RED 1：在受限账户运行 `-IntegrationOnly` 时，固定源码触发 Git 所有权保护，脚本随后对空提交结果调用 `Trim()`，覆盖了第一失败边界。

修复：固定源码只使用命令级 `safe.directory`，不修改全局 Git；读取失败先检查退出码再解析；Go 缓存固定在工作区 `.tmp`。

RED 2：读取提交已通过，但从本地仓库克隆时源 `.git` 仍被 Git 单独拒绝。

修复：克隆命令同时为工作树和其 `.git` 增加命令级安全目录；不持久化配置。

GREEN：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-egress-ux-aliases.ps1 -IntegrationOnly
```

结果：退出码 0；AimiliVPN 定向 fixture 22 项通过；3x-ui 部署资产 4 项通过；固定补丁的数据库、service、controller、subscription 四包通过；Gateway 的旧主身份协议切换、国家替换动态别名、四类替换失败回滚和候选持久失效事务通过。

## 三仓库全量结果

### Gateway

完整脚本本轮实际通过：

- `npm test --prefix web`：7 个文件、38 项测试通过。
- `npm run build --prefix web`：类型检查和 Vite 生产构建通过。
- `go test ./... -race -count=1`：全部包通过，无竞态报告。
- `go vet ./...`：通过。
- `python -m unittest discover -s scripts -p 'test_*.py'`：93 项通过，2 项因普通 Windows 账户不能创建 symlink 按条件跳过。
- `cmd/aimili-gateway` 与 `cmd/aimili-gateway-admin`：构建通过，产物只写系统临时目录并清理。
- `git diff --check`：显式 `git -C` 后通过。

跨仓 fixture 验证了：

1. Gateway 旧主身份先由健康 AimiliVPN 主状态收敛，再进入严格协议预检。
2. 出口位 1 从日本替换为韩国时，只有目标订阅别名变为 `出口位 1_韩国`。
3. 别名写失败、订阅读取失败、写后不一致会恢复旧 AimiliVPN、Gateway 与别名；别名回滚失败返回 `repair_required`。
4. 明确的候选拨号/出口失效才重读候选集合；外层系统故障不淘汰候选。

### AimiliVPN

- `python -m unittest discover -s tests -v`：157 项通过。
- `python -m py_compile proxy_server.py node_pool.py vpngate_manager.py control_api.py vpn_utils.py`：通过。
- 定向 fixture 证明 VPN 节点入口 IP 与拨号后的公网出口 IP 可以不同；无合法出口 IP 的候选不会进入可用缓存。
- 结构化国家刷新、失败候选 blacklist/节点池原子持久化、重启后仍不可选的测试通过。
- `git diff --check`：通过。

### 固定 3x-ui v3.7.0 与部署资产

- 部署资产统一测试：4 项通过、0 失败。
- 固定提交应用补丁的数据库、web service、controller、subscription 四包定向及完整包测试通过。
- 两个客户端关联同一入站的测试证明只有 Gateway 目标客户端得到别名覆盖，其他客户端继续使用原入站 remark。
- `npm ci`：601 个包安装完成，审计 0 个漏洞。
- 前端 OpenAPI 生成与 Vite 生产构建通过。
- `go build ./...`：通过。
- `git diff --check`：部署资产仓库通过。

当前 Windows 上的 `go test ./... -count=1` 仍只复现固定上游基线问题：根包 CI 文档任务名检查受 CRLF 影响；`internal/crypto/nodetoken` 的 POSIX `0600` 权限断言在 Windows 映射为 `0666`；`internal/web/service/panel` 的环境变量大小写断言受 Windows 环境变量不区分大小写影响。本轮新增/修改的四个包全部通过，且全仓构建通过。这些平台失败未通过修改上游测试或全仓换行符来掩盖。

## 复杂度审查

本轮实现跨 AimiliVPN、Gateway UI/API/编排和 3x-ui 数据/API/订阅层，达到复杂度审查门槛。审查只允许删除重复映射、无消费者抽象和不必要扩展点；不以“简化”为名削弱所有权、只读协议验证或事务回滚。

结果：发现验证脚本一度先调用既有构建入口、随后又在同一固定补丁临时检出中重复前端构建和 Go 验证。删除这层重复调用（净减少 5 行），保留单一临时检出的四包测试、前端构建和 `go build ./...`；未改写已经通过故障注入的事务路径。

## 生产阶梯部署（已完成）

### Stage 0：基线与备份

- `aimili-gateway`、AimiliVPN、x-ui/Xray、Caddy 均为 `active`，启动后错误计数为 0。
- 稳态为 1 个 Xray、4 个 OpenVPN；主连接和三个槽位均有真实出口，四个公网监听与四个 mixed 监听存在。
- 升级前协议为主连接 TCP/Vision、三个槽位 XHTTP/REALITY；`client_inbounds` 尚无 `alias_override` 列。
- 已建立并校验 0700 备份目录；备份包含 Gateway 二进制/数据库/配置、AimiliVPN 代码与数据、x-ui 二进制/数据库、Caddy 片段和 systemd 覆盖。

### Stage 1：AimiliVPN

第一次尝试在正式三槽 helper 处返回 `upstream_rejected`。首个失败边界是当前 helper 已调用动态别名 API，而生产 x-ui 尚未进入 Stage 2；本级完整回滚。回滚复核同时发现普通 SQLite 文件复制遗漏 WAL 的风险，回滚脚本随后改为停服恢复并精确清除对应 `-wal/-shm`，再次复核恢复完整。

第二次使用不调用动态别名 API 的兼容 helper，三槽检查通过；但验收采样发生在普通节点池维护中，看到一个临时候选探测 OpenVPN，`safe_main_status.active=false`，候选真实出口尚未持久化，因此按失败门完整回滚。

根因由代码、失败窗口日志类别和 TDD 回归共同确认：普通节点池维护复用全局 `is_connecting`，而 `safe_main_status()` 错把该通用维护标志解释为主连接正在切换。新增测试先证明“主 OpenVPN 仍运行时，候选探测不得把主状态报告为 inactive”会失败；最小修复改为以实际主 OpenVPN 进程判定 active。AimiliVPN 本地 158 项全量测试和 `py_compile` 通过，修复提交 `f33517c` 已正常推送，`fetch` 后本地/远程差异为 `0/0`。

第三次部署的远端归档语法检查和 147 项定向测试通过。SSH 等待连接中途断开后未重放操作，而是核对远端代码哈希、服务、进程和部署进程；原部署仍在后台等待稳定门。最终结果：

- 主连接 active 且出口正常，三个槽位均为 up；
- 稳态恢复为 4 个 OpenVPN，临时候选探测进程已退出；
- 缓存为 23 个候选，其中 9 个具有已校验并持久化的真实出口 IP；
- 兼容 helper 对三个槽位全部返回 ready，Gateway 三槽身份与 AimiliVPN 收敛；
- 复核仍为 1 个 Xray、四个公网监听、四个 mixed 监听，四服务启动后错误计数为 0。

Stage 1 已通过。Gateway 主身份仍保留升级前记录，计划在 Stage 3 通过正式主检查收敛，未手改数据库。

### Stage 2：3x-ui

- 固定源码前端构建及数据库、web service、controller、subscription 四包定向测试通过。
- VPS 原生编译连续三次分别在模块工作目录、`/tmp` 空间和内存边界失败；第三次峰值约 267 MiB 内存、570 MiB Swap。按失败门停止该路径，没有进行第四次 VPS 编译。
- 本机 Docker Desktop 使用官方 `golang:1.27.0-bookworm` 在 Linux/amd64 + CGO 环境构建成功；二进制版本为 3x-ui `v3.7.0`，上传后摘要一致。
- 影子实例使用生产 SQLite 在线副本和仅回环面板/订阅监听；`alias_override` 迁移、旧订阅读取、别名覆盖渲染及入站/客户端/关联资源指纹检查通过。影子进程退出后删除了含生产数据库副本的临时目录。
- 首次生产安装按预期自动回滚。日志时间线证明单一根因是健康检查竞态：systemd 标记启动约 95 ms 后部署脚本立即单次请求，而面板和订阅实际在约 0.9～1.1 秒后监听；不是数据库、权限、证书、监听冲突或 Xray 配置错误。
- 部署脚本新增“前两次健康失败、第三次成功不得回滚”的 TDD 回归，旧实现 RED；改为最多 60 秒条件轮询后 GREEN。部署资产全套 4 项通过，本地提交 `5dbe6f0`；仓库没有远程，未执行推送。
- 第二次生产安装通过：运行二进制摘要匹配、迁移列存在、SQLite 回滚备份 `quick_check` 通过、单 Xray、四服务 active。
- 300 秒观察取得 30 个样本：服务重启数、单 Xray、订阅响应、受管与非受管资源指纹均保持；结束时 `MemAvailable=199128 KiB`、`SwapUsed=139992 KiB`。

### Stage 3：Gateway 后端与生产契约修复

- 当前功能工作树重新执行前端 38 项测试、生产构建和 `go test ./... -race -count=1`；使用项目内 Go 缓存规避全局缓存 ACL，不修改全局 Go 环境。
- 新 Gateway 二进制通过摘要校验和 Gateway 本级 SQLite 在线备份后原子安装；健康、单 Xray、四 OpenVPN 和 x-ui 资源指纹为失败回滚门。
- 首次正式三槽检查发现槽位 2 为 `pending`，不是 Gateway/x-ui 故障。通过 AimiliVPN 受限 `rotate` 恢复运行态后，再以正式 `check-slots` 验证三个槽位全部 `ready` 并写回 Gateway DB。
- 正式主检查首次返回 `invalid_response`。沿真实链路定位到 3x-ui 补丁 GET 响应的 `inboundAliases` 是“入站 ID → 别名”的对象映射，而 Gateway 只接受数组。对象映射回归先 RED，兼容解析后 GREEN，提交 `468839a`。
- 第二次主检查仍返回 `invalid_response`。进一步确认 3x-ui 会为尚未覆盖的关联返回空字符串；Gateway 在首次写入前错误拒绝空默认值。含空默认映射的测试先 RED，只忽略空覆盖值后 GREEN，提交 `743a2bf`。
- 两轮修复均重跑 Gateway 全量竞态测试并使用摘要校验的 Linux/amd64 二进制阶梯部署。最终正式主检查通过，Gateway 主真实出口从旧记录安全收敛到 AimiliVPN 当前身份；未手改数据库。
- 3x-ui `client_inbounds` 中 4 个 Gateway 关联均有非空动态别名；非 Gateway 资源仍为 0 个，安全指纹保持 `4f53cda1…02b945`。

### Stage 4：无前台浏览器的页面资产验收

- 通过回环 HTTP 解析生产 `index.html` 和两个静态资产，不使用 computer use。
- `lang=zh-CN`、`#app`、中文错误/刷新/切换提示、关闭按钮、`VPN 节点`、`SOCKS5H 代理池`、`节点 IP` 及 success/error/progress 三种通知样式均存在。
- 生产页面与静态资产组合摘要为 `e83302a1…95360`；Gateway 重启后摘要未变化。

### Stage 5～7：协议阶梯与最终混合状态

- Hysteria2 前置安全门通过：生产证书/域名和 Xray 离线配置有效；内存 193 MiB、Swap 空闲 875 MiB；UDP 规则精确为 `8443/udp`、`20000/udp`、`20001/udp`、`20002/udp`；非 Gateway 指纹不变。
- 出口位 2 从 XHTTP/REALITY 切到 Hysteria2/QUIC/TLS，事务返回 `ready`；随后三槽正式检查全部通过。
- 出口位 3 从 XHTTP/REALITY 切回 TCP/Vision，事务返回 `ready`。
- 最终协议矩阵：主连接 TCP/Vision；出口位 1 XHTTP/REALITY；出口位 2 Hysteria2/QUIC/TLS；出口位 3 TCP/Vision。每个逻辑出口只有一个公网协议，mixed 未参与切换。
- 两条 x-ui error-priority 日志均为旧 XHTTP 监听在 20001/20002 切换时正常关闭的 `use of closed network connection`；没有数据库、权限、启动、配置、监听冲突或 panic。
- 最终混合状态 300 秒观察取得 30 个样本：四服务无重启、单 Xray、订阅持续可读、资源指纹未变化；结束时 `MemAvailable=192640 KiB`、`SwapUsed=143160 KiB`。

### Stage 8：重启验收

- AimiliVPN 重启后恢复 4 个 OpenVPN，三槽与主连接正式检查通过。
- x-ui/Xray 重启后最终三协议、4 个动态别名、单 Xray、所有监听和资源指纹恢复。采样碰到一次普通候选池临时探测，待日志明确出现“周期节点检测完成”且 OpenVPN 恢复为 4 后，三槽检查再次全部通过。
- Gateway 重启后健康恢复，三槽、主连接和 HTTP/DOM 页面资产检查通过。
- Caddy 重启后公共 HTTPS 返回 200 且为 Gateway HTML。
- 最终权威采样：四服务 active、启动后错误 0、1 个 Xray、4 个 OpenVPN；主与三槽全部 ready；8 个固定监听存在；4 个动态别名非空；最终协议矩阵及非 Gateway 指纹均未漂移。

### Stage 9：中断事务崩溃恢复加固

- 最终代码审查在推送前发现两个可复现的崩溃窗口：AimiliVPN 已进入 `pending_commit` 时，Gateway 重放可能只验证后快速返回而不 commit；helper 已 finalize、Gateway 尚未保存 `ready` 时，启动恢复会对无回滚快照的终态 tombstone 重试 rollback 并进入 `repair_required`。
- 两项均按 TDD 先取得 RED：主事务测试证明 commit 次数为 0；协议恢复测试证明启动进入 `repair_required`；helper 测试证明 finalized tombstone 被误当普通快照并返回 `snapshot_invalid`。
- 最小修复提交 `be785dc`：`pending_commit` 纳入既有恢复闭集，目标身份、AimiliVPN 验证标志、mixed、公网协议、订阅和出口验证通过后才幂等 commit；finalized tombstone 的 rollback 返回安全终态码，Gateway 仅在 operation/request 指纹、目标运行态、订阅和真实出口全部匹配后收敛 `DesiredMode`，否则继续 fail closed 为 `repair_required`。
- 修复后 Gateway 全包竞态测试、`go vet`、前端 38 项测试与生产构建、scripts 93 项测试全部通过；限定复核结论为 Critical 0、Important 0，复杂度审查未发现可安全删除的依赖、配置或抽象。
- Stage 9 上传资产不含生产配置或凭据。VPS `bash -n` 与上传摘要通过；安装前四服务 active、错误 0、单 Xray、4 个 OpenVPN、4 个动态别名、非 Gateway 指纹和最终协议矩阵均正常。
- 第一次安装在任何写入前由 `transaction_not_idle` 门安全停止。只读阶段统计确认 helper 快照全为终态，实际阻挡源是 2 条历史 `protocol_switch started` HTTP 账本；协议状态均为 `ready`。安装门随后改为在停 Gateway 前后分别核对真实协议状态、AimiliVPN 主事务与 helper 快照终态，保留历史账本不删库。
- 第二次安装通过：Gateway、root helper 和 Gateway SQLite 均有 `/var/backups` 回滚副本；新二进制/helper 摘要、健康、数据库、协议矩阵、4 个别名、非 Gateway 指纹、单 Xray、4 个 OpenVPN 与 mixed 不变门全部通过。
- 正式三槽检查验证三个槽位全部 `ready`。旧 HTTP 主检查资产因使用 x-ui 自动化账户登录 Gateway UI 返回 401，失败发生在登录边界且未触发主检查；改用当前提交构建、直接加载 Gateway 运行依赖的受限 helper 后，正式主检查通过并安全收敛 Gateway DB 身份。
- 首轮 300 秒观察因 SSH 断开丢失最终退出状态，没有冒充通过；确认远端原进程自然结束且四服务无重启后，使用 transient systemd 单元重新执行可恢复观察。第二轮取得 30 个完整样本：四服务无重启、1 个 Xray、4 个 OpenVPN、4 个别名、协议矩阵和非 Gateway 指纹均无漂移。
- 观察后最终采样再次确认四服务 active、启动后错误 0、主与三槽运行正常、8 个固定监听存在；公共 HTTPS 返回 200 且为 Gateway HTML。

## 实际完成、真实验证与未执行

### 实际完成

- Task 1～8 的三仓库本地代码、测试和可复现 3x-ui 补丁已完成；最终审查发现的两项中断事务恢复缺口已在 Stage 9 TDD 修复并部署。
- Stage 0～9 的代码部署、3x-ui 迁移、Gateway 主身份收敛、动态别名、最终混合协议、完整 300 秒观察和服务重启验收已按本节最新证据完成。
- 最终仍只有主连接、出口1、出口2、出口3四个逻辑运行出口；没有新增长期公网入站或运行出口。

### 未执行与剩余限制

- 用户明确要求本任务不使用 computer use，因此未执行 Gateway 页面人工点击，也未操作 v2rayN GUI 刷新订阅并逐条点击四个公网节点与四个 SOCKS5H。生产 HTTP/DOM、服务端真实协议验证和订阅持续读取不能替代这两项人工客户端证据。
- 未在生产注入 `repair_required` 或人为制造两阶段写入故障；对应回滚由本地故障注入测试覆盖，但不能表述为生产演练已通过。
- 未执行“旧主仍在线时制造未提交主事务并等待自动回滚”的专项生产演练；正常服务重启和已提交状态恢复已通过。

以上剩余项不改变当前生产最终混合协议状态，但在补充实际证据前不得宣称 Gateway 人工交互、v2rayN GUI 或专项故障演练已经验收。
