# 主连接安全切换与独立协议模式验收记录

日期：2026-08-29

> 历史验收说明：本文按 Stage 保留 2026-08-29 当时的通过与未完成边界。后续生产恢复、客户端确认、当前提交与磁盘状态请以 `docs/handoffs/2026-09-05-current-state-handoff.md` 为准；不要把下面的“待完成”当作当前任务清单。

状态：VPS Stage 1–6、8 已通过，主协议往返与最终混合状态已有真实连接证据；Stage 7 的已提交模式、AimiliVPN 历史状态迁移、四出口重启恢复和外部验证通过。未提交主事务自动回滚、两阶段 repair 生产事务演练、Gateway UI 与 v2rayN GUI 最终点击仍待完成

本记录只保存提交、版本、计数、端口、布尔结果、资源指标和脱敏错误码。不得写入连接材料、私钥、后台路径或完整订阅地址。

## 1. 版本与提交

| 项目 | 安全记录 |
| --- | --- |
| Gateway 分支/提交 | `feat/main-switch-protocol-modes`；精确提交 ID 由主 agent 最终提交后补写 |
| AimiliVPN 分支/提交 | `feat/main-switch-protocol-modes`；精确提交 ID 由主 agent 最终提交后补写 |
| 3x-ui 版本 | 生产命令未返回可解析版本；交接基线为 `3.7.0`，待最终补证 |
| Xray 版本 | `26.7.28` |
| v2rayN 版本 | `7.24.4` |

## 2. 本地验证

| 检查 | 结果 |
| --- | --- |
| AimiliVPN 全量 unittest | 通过，95 tests |
| Gateway 全量 Go test | 通过，全部 package |
| 前端 Vitest | 通过，27 tests |
| 前端 production build | 通过 |
| Python helper、故障注入与外部三协议脚本 | 通过，81 tests，2 项仅因 Windows 普通账户不能创建 symlink 而跳过 |
| 部署契约 | 通过 |
| `git diff --check` | 通过 |
| Ponytail 复杂度审查 | 通过；本轮生产修复与回归测试未发现可删除的依赖、抽象层或推测性扩展点，无安全边界删减 |
| 独立代码审查 | 修复波 scoped 复审无 Critical/Important；未知 `proxy_type` 严格闭集修复复审无 Critical/Important，仅有未单独参数化纯空白值的非阻断 Minor；最终全分支审查结论见分支交付记录 |

## 3. 生产前硬门

| 指标 | 结果 |
| --- | --- |
| 联合备份与摘要 | Stage 1 成功；Stage 2 尝试均在受限目录创建联合备份 |
| `MemAvailable` | 预检约 186–204 MiB，通过 |
| Swap 空闲 | 预检约 873–929 MiB，通过 |
| 证书域名/有效期 | 通过；证书对唯一，至少七天有效且 Xray 可读 |
| Hysteria2 Xray 离线配置 | 使用真实 Xray `26.7.28` 通过 |
| 非 Gateway 资源数量 | 0 |
| 非 Gateway 指纹已记录 | 是，验收记录不保存原始资源正文 |
| Xray 初始 PID 已记录 | 是 |

## 4. Stage 1：主事务

| 检查 | 结果 |
| --- | --- |
| 受控失败恢复旧主 | 通过；持久状态为 `rolled_back / assign_failed_rolled_back` |
| `7928` 恢复 | 通过 |
| 真实主切换 | 通过；`stage → 三路径验证 → commit` |
| 主 mixed 与公网一致 | 通过；`7928`、主 mixed、`8443` 出口一致 |
| 三个普通槽位不变 | 通过 |
| 本级回滚 | 受控失败路径已执行并通过；真实切换无需回滚 |

## 5. Stage 2：Gateway 与助手

| 检查 | 结果 |
| --- | --- |
| Gateway migration | 通过，migration 10 为 1，`egress_protocol_modes` 四行均初始化为 TCP/Vision ready |
| root helper/path/timer | 已部署；helper 存在，path/timer 在 Stage 2 post-check 时 active |
| 精确 UDP 端口 | 仅 `8443/udp`、`20000/udp`、`20001/udp`、`20002/udp` 四个逻辑规则 |
| 不存在节点用 `443/udp` 规则 | 是 |
| 不存在 UDP 范围规则 | 是 |
| 公网入站数 | 4，仍为 VLESS/TCP 基线 |
| mixed 数 | 4 |
| Xray PID不变 | 是；Stage 2 未重启 x-ui/Xray |
| 非 Gateway 指纹不变 | 是 |

Stage 2 第三次使用修复归档部署通过。独立 post-check 显示 Gateway health 200，四个服务 active，Gateway 二进制 `0755 root:root`、SQLite `0600 aimili-gateway:aimili-gateway`，公网入站数 4、mixed 数 4、非 Gateway 入站数 0，Xray PID 与 Stage 2 前一致。部署后发现出口位 1 的 RU/residential 候选池耗尽，正式检查将其标记 degraded；通过正式 RU 刷新得到 4 个有效候选后，单槽位 rotate 恢复 ready，槽位号、公网端口和 mixed 端口均保持不变。随后 v2rayN 自带 Xray `26.6.1` 对统一订阅四条 TCP/Vision 节点完成外部代理 DNS 和真实出口验证。

## 6. Stage 3：XHTTP 往返

| 检查 | 结果 |
| --- | --- |
| TCP → XHTTP | 通过；出口位 1 原位热切换为 XHTTP/REALITY |
| v2rayN 兼容与真实连接 | 通过兼容路径：以 `v2rayN/7.24.4` User-Agent 拉取订阅，并由本机 v2rayN 自带 Xray 逐条验证；GUI 点击待人工完成 |
| 代理 DNS/真实出口一致 | 通过 |
| TCP/Vision 反向恢复 | 通过；出口位 1 `XHTTP → TCP → XHTTP` 往返均成功 |
| 非目标长连接持续 | 通过；另外三个公网入口和四个 mixed 探针持续 |
| Xray PID不变 | 通过；在线往返期间保持同一单 Xray 进程 |

Stage 3 首次将出口位 1 请求切到 XHTTP 时，helper 未应用新入站。根因链先定位到 helper 缺少读取受限路径所需的最小 capability；唯一孤立 apply 请求经闭集校验后移入 `0700 root:root` 备份隔离，正式 unit 调整为仅保留 `CapabilityBoundingSet=CAP_DAC_OVERRIDE`、`AmbientCapabilities=`。Gateway 启动恢复随后验证通过，旧订阅、mixed、Aimili 槽位和公网链路均收敛到 TCP/Vision `ready`。

第二次试切已进入 helper，但完整 XHTTP 配置的生产 Xray 离线校验返回 `xray_command_failed`，helper 自动恢复，运行中配置和 Xray PID 未改变。本地 Xray `26.6.1` 最小配置曾提示 `sockopt.trustedXForwardedFor`，但生产复测否定了它是唯一根因。随后直接读取生产 Xray `26.7.28` panic 栈，定位到 helper 把 3x-ui 数据库空监听序列化为 `"listen":""`；3x-ui 正常运行配置会省略该字段。按 TDD 改为仅在监听非空时写入 `listen` 后，生产同版本完整配置离线校验返回 `Configuration OK`，最小 helper 修复已备份并安装。

继续运行时试切时，外部验证器先收到 Gateway HTTP 400，且 helper 请求、结果和事务目录均未新增。版本核对确认 `expectedProtocolMode` 只在提交 `18c6852` 引入，而生产 Gateway 二进制不含该字段；严格 JSON 解码因此在写 spool 前拒绝请求。生产旧二进制已备份，Gateway 已更新到 `18c6852` 并通过服务启动检查。主连接经既有检查入口完成三路径复验并同步后，四出口 TCP 基线恢复；出口位 1 随后完成 `TCP → XHTTP → TCP`，最终再次切到 XHTTP。helper apply/finalize、订阅覆盖、代理 DNS、真实出口、Xray PID 与非目标探针均通过。

## 7. Stage 4–5：Hysteria2 与资源

| 检查 | 结果 |
| --- | --- |
| 外部 QUIC/TLS | 通过；出口位 2 Hysteria2/QUIC/TLS 真实出口一致 |
| v2rayN 兼容与真实连接 | 通过兼容路径；GUI 点击待人工完成 |
| UFW 包到达 | 通过；只使用出口位 2 对应的精确 UDP 规则 |
| 云 UDP 边界 | 正常 |
| 观察时长 | 300 秒 |
| Xray RSS 峰值 | 25 MiB |
| `MemAvailable` 最低值 | 未单独保存最小样本；观察期未触发连续 30 秒低于 96 MiB 的硬门 |
| Swap 使用增量 | 39 MiB |
| OOM | 否 |

最终混合状态另执行一次完整 301 秒观察，结果为 `status=pass`：Xray RSS 峰值 22 MiB，`MemAvailable` 最低 191 MiB，Swap 增量 0 MiB；单 Xray PID 和 Gateway、AimiliVPN、x-ui、Caddy 四项服务全程稳定，未发现内核 OOM。该结果用于 Task 12 的最终资源门，不替代上表单出口试切阶段的历史样本。

## 8. Stage 6 与重启恢复

| 逻辑出口 | 协议 | 真实连接 | mixed/SOCKS5H |
| --- | --- | --- | --- |
| 主连接 | TCP/Vision | `TCP → XHTTP → TCP` 往返和最终无切换验收通过 | 保持可用 |
| 出口位 1 | XHTTP/REALITY | 通过 | 保持可用 |
| 出口位 2 | Hysteria2/QUIC/TLS | 通过 | 保持可用 |
| 出口位 3 | TCP/Vision | 通过 | 保持可用 |

| 检查 | 结果 |
| --- | --- |
| x-ui/Xray 重启重建混合模式 | 通过；SQLite 重建相同协议组合 |
| Gateway 重启恢复 | 通过；四协议状态不漂移 |
| AimiliVPN 未提交事务恢复 | 未通过最终自动验收；旧/新节点均不可拨时正确进入 `repair_required`。历史 repair 只验证 `7928` 后即提交，不能证明 Gateway 主 mixed/公网；本轮两阶段修复尚未生产复测，且仍需一次旧主在线条件下的自动回滚成功证据 |
| Gateway UI 主替换原路径 | 待人工点击；后端同源、CSRF、幂等主替换路径已在 Stage 1 和自动化测试通过 |
| Gateway UI 协议切换原路径 | 待人工点击；生产后端切换和回滚路径已通过 |
| “复制节点订阅”原路径 | 待人工点击；认证订阅接口与四条覆盖已通过 |
| v2rayN 四节点刷新与逐条连接 | 兼容路径通过；本机版本确认为 7.24.4，GUI 刷新与点击待人工完成 |

AimiliVPN 单独重启演练先后暴露启动阶段 `is_connecting` 占位、事务候选被节点池过滤等问题。历史 `repair-commit` / `repair-replace` 在 `7928` 验证后直接提交，绕过 Gateway 主 mixed 与当前公网协议验证，因此只记录为恢复 AimiliVPN 本地出口的历史事实，不作为完整主链路验收。本轮代码已改为 `pending_gateway_validation → Gateway 三路径验证 → commit`，仍待生产复测。

主协议往返补证先执行 `TCP → XHTTP` 并通过四出口联合验收。首次 `XHTTP → TCP` 时 AimiliVPN 后台已自动选择另一个可用主节点；helper 对协议事务返回 `rolled_back`，但 Gateway 的旧候选/出口绑定验证失败，正确进入 `repair_required`，没有把不一致状态标为成功。旧免费节点已失效，受认证同候选重连被拒绝，当前 Aimili 主链路仍保持 active、`7928` 和出口可用。随后按 TDD 增加并部署恢复规则：仅在 helper 幂等 rollback 成功后，绑定 Aimili 当前候选、国家、类型和出口，完成 `7928 + mixed + 当前公网协议` 验证并再次读取相同身份，才同步 `main_egress` 并清回 `ready`；任一不一致仍保留 `repair_required`。生产恢复后主状态为 ready 且错误码清空，`XHTTP → TCP` 和最终无切换四出口验收均返回 pass。

分支交付前又收紧了恢复身份类型校验：对 Aimili 原始 `proxy_type` 去空白并转小写后，只接受 `residential` 或 `datacenter`，未知值不得再通过默认规范化降级。Linux amd64、`CGO_ENABLED=0` 的最新 Gateway 二进制经本地与远端 SHA-256 一致性硬门安装；部署脚本保留 root-only 回滚备份，首次健康探针过早时自动恢复旧版，改为有界就绪轮询后只重启 Gateway 并成功。部署后 Gateway、AimiliVPN、x-ui、Caddy 均为 active，四条协议状态均为 ready 且无错误码；最终无协议切换验收再次得到四个 ready、四公网、四 mixed、四条订阅、四个唯一出口、四公网真实协议和四个授权 mixed 全部通过。

生产恢复后，三个普通槽位通过既有 Gateway `rotate` 原路径恢复，主与三个普通出口再经 `check` 完成 mixed 与对应公网协议复验和出口同步。最新外部验收得到四组 ready、四公网协议、四个授权 mixed、单 Xray、订阅四条和四个唯一出口全部通过；最终协议仍为主 TCP、出口位 1 XHTTP、出口位 2 Hysteria2、出口位 3 TCP。

## 9. 2026-08-31 AimiliVPN 历史终态兼容收尾

| 检查 | 结果 |
| --- | --- |
| AimiliVPN 修复提交 | `2cada1f`，已非强制推送至私有功能分支 |
| 兼容边界 | 仅 `active=None`、全部 history 为终态、`committed` 携带两个旧 resolution 且无 repair hash 时 canonicalize；其他非法形态继续 fail-closed |
| 本地测试 | AimiliVPN 119/119；`git diff --check` 通过；增量敏感扫描为 0 |
| scoped 复审 | Critical 0、Important 0、Minor 0 |
| 生产部署 | 私有 GitHub 精确 fetch；仅原子安装 `control_api.py`、`main_assignment.py`、`vpngate_manager.py`；生产仓库 HEAD 保持 `c359ba5` |
| 备份与恢复边界 | 三文件与 `vpngate_data` 已备份到新建的 `/var/backups/aimili-final-stack-*`；未 reset/clean，未删除 `.codex-backups` |
| canonical migration | 7 条历史均为 `committed/rolled_back`，`active=None`，旧 resolution 已移除，`mutation_lease=None` |
| mutation lease | acquire → renew → release 通过；未输出 lease ID |
| 重启恢复 | AimiliVPN 与 Gateway 重启；x-ui/Xray/Caddy 未重启且保持 active |
| 身份同步 | 主与三个普通槽位均经 Gateway 正式 `check` 原路径验证并同步；没有直接修改生产数据库 |
| 最终外部验收 | `status=pass`：4 ready、4 公网、4 mixed、单 Xray、4 条订阅、4 个唯一出口、4 个授权 mixed 和四公网真实协议全部通过 |
| 新资源观察 | 300 秒、61 个样本；Xray RSS 峰值 25 MiB，`MemAvailable` 最低 173 MiB，Swap 增量 0 MiB，OOM 否，四服务与单 Xray PID 全程稳定 |

部署后的第一次 lease acquire 返回 `409 operation_busy`。状态文件已完成 canonical migration，且错误不再是 `state_corrupt`；后续脱敏诊断确认候选刷新 idle、没有测试隧道，失败发生在 AimiliVPN 重启后的槽位恢复与延迟节点池维护共用 mutation lock 的连续繁忙窗口。互斥空闲后相同闭集烟测立即完成 acquire/renew/release，未修改锁语义或放宽 fail-closed 边界。

## 10. 最终不变量

| 不变量 | 结果 |
| --- | --- |
| AimiliVPN 运行出口总数 = 4 | 是 |
| 公网入站总数 = 4 | 是；TCP/Vision 2、XHTTP/REALITY 1、Hysteria2/QUIC/TLS 1 |
| mixed 总数 = 4 | 是 |
| 每逻辑出口只有一个公网协议 | 是 |
| mixed/SOCKS5H 未随协议切换 | 是 |
| 非 Gateway 资源未删除或修改 | 是；当前数量为 0 |
| `21000` 与 balancer 未恢复 | 是 |
| 无未决 `repair_required` | 是 |

## 11. 未决项

- Gateway UI 与 v2rayN GUI 的最终点击验收尚未完成：Codex 的浏览器/Windows 应用控制宿主在初始化时发生本机运行时资源路径错误。不得用后端/API 证据冒充该 GUI 验收。
- 未提交主事务的重启自动回滚尚缺一次“旧主在 180 秒后仍可拨”条件下的成功证据。最新失败是外部 VPNGate 节点离线；历史 repair 恢复不得冒充自动 rollback 或 Gateway 三路径验证通过。
- 最终脱敏数据库检查仍有 2 条早期失败请求保留为历史 `started`；它们只约束各自旧幂等键，不阻塞新请求。当前操作计数为 `completed=18 / failed=4 / started=2`，helper 请求队列为空，四条协议状态均为 `ready`，无 `repair_required`。如需清理历史审计行，应作为独立数据维护任务处理，不在本次协议部署中直接改生产数据库。

## 12. 2026-09-02 最终混合状态补证

本节是在前述记录基础上的最新生产补证。除明确说明的 Gateway 正式 `check` 原路径外，未直接修改生产数据库；没有增加 AimiliVPN 运行出口，也没有切换 mixed/SOCKS5H。

| 检查 | 结果 |
| --- | --- |
| 3x-ui 专属别名修复 | 通过。仅对存在 `alias_override` 的 Gateway 关联返回专属别名，其他入站继续使用全局模板；安装器备份二进制与数据库、重启失败自动回滚，并验证服务 active、关联指纹不变、Xray=1。 |
| 主连接自动漂移处理 | 通过。确认原 OpenVPN 节点连接重置且重连失败后，AimiliVPN 既有保护自动选择健康节点；Gateway 随后经正式主连接检查完成 SOCKS、公网协议验证和身份同步。 |
| 普通槽位身份漂移处理 | 通过。确认两个槽位实际出口与 Gateway 记录不一致后，分别经 Gateway 正式 `check` 原路径验证并同步；未更换节点、未修改 3x-ui 路由。 |
| 四个授权 SOCKS5H | 通过，四条实际出口均与各自运行节点一致。 |
| 完整外部客户端验证 | `status=pass`：4 个 ready、4 个唯一出口、4 条订阅、4 个公网入站、4 个 mixed 入站、单 Xray 均通过；公网协议实际拨号为 TCP/Vision 2、XHTTP/REALITY 1、Hysteria2/QUIC/TLS 1。公开 SOCKS5H 受来源限制策略保护，未将其当作失败。 |
| 五分钟稳定性观察 | 通过：30 个采样、300 秒；Gateway、AimiliVPN、x-ui、Caddy 均 active 且无重启；Xray=1、OpenVPN=4、专属别名=4；协议与非 Gateway 受管资源指纹均无漂移。 |

本次补证完成了“每出口独立使用 TCP、XHTTP、Hysteria2，并形成最终混合状态”的生产外部验证。第 11 节列出的 GUI 人工点击与旧主在线条件下的未提交事务自动回滚，仍是独立未决验收项，不能由本节替代。
