# ny 出口隔离与统一仓库验证

日期：2026-09-11（Asia/Shanghai）

## 目标

1. 主连接与出口 1–3 故障互不干扰。
2. 每次连续故障最多自动修复一次，服务重启不重新计数。
3. 只在 ny VPS 做生产验证，并只保留一个新增联合备份。
4. 将出口引擎源码并入 `aimili-gateway`，但保持两个独立服务；原 `aimili-vpngate` 仓库保留。
5. Gateway 前端不再显示 AimiliVPN 原后台，只保留 Gateway 页面、高级设置和 3x-ui 专家模式。

## ny 已验证事实

部署版本：

- Gateway：`bed0cbfb7fcfaf6d74db66b9bcfefcde659097b5`
- 出口引擎：`ed102e3`
- 唯一新增备份：`/var/backups/aimili-gateway/egress-isolation-20260910-bed0cbf-ed102e3`

受控停止出口 1 的 `tun120` 后，修复记录为：尝试次数 1、错误 `no_same_country_candidate`、修复状态 `manual_required`、出口状态 `disconnected`。同一时刻主连接及另外两个普通出口仍健康。重启出口引擎后，尝试次数仍为 1，持久记录未变化，没有新的自动修复日志，`tun120` 也没有被偷偷重建。

人工恢复过程中，一个候选明确返回 `ERR_OVPN_AUTH_FAILED/AUTH_FAILED`，系统没有把失败结果标成成功。替换为可用候选后，最终验收为：

- `aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 均 active。
- `tun0`、`tun120`、`tun121`、`tun122` 均存在。
- Gateway 四条记录均为 `ready`，订阅包含四个入站。
- 四条 SOCKS5H 链路得到四个不同的真实出口 IP。
- 点击 Gateway“重新检测”前后候选 ID 未变化，证明检测没有暗中换节点。
- Gateway 数据库 `integrity_check=ok`；修复索引前后业务表行数和内容摘要一致。

## 本地统一仓库边界

出口引擎源码位于 `services/aimili-egress`。该目录来自 `aimili-vpngate` 的已修复提交 `ed102e3`，包含运行模块、单元测试、LICENSE、CHANGELOG 和多出口自检脚本，不包含 `.git`、工作树、运行数据或旧的一键安装器。

`deploy/systemd/aimilivpn.service` 从统一仓库中的源码启动，但通过 `VPNGATE_DATA_DIR=/opt/aimilivpn/vpngate_data` 继续使用现有持久数据。这使未来切换源码时无需迁移账号、候选池、槽位和单次修复记录。Gateway 继续使用独立低权限 unit，只能访问回环控制接口；出口引擎单独保留 TUN 与策略路由所需能力。

前端移除了 `/settings/aimilivpn` 路由、AimiliVPN 设置组件和 `/api/v1/backends/aimilivpn/login` 白名单。出口列表、国家补充、人工替换等 Gateway 原生能力仍通过内部 API 工作；`/settings/3x-ui` 和专家模式入口保留。

## 未执行

- 未操作其他 VPS。
- 未删除原 `aimili-vpngate` 仓库。
- 未推送任何远程分支。

## 检测触发一次自动修复增量

### 现场失败边界

用户在 2026-09-11 06:01（Asia/Shanghai）点击出口 1“重新检测”时，ny 最新日志连续出现 `Cannot find device "tun120"` 与 `ERR_ROUTE_TABLE_ADD_FAILED`，随后 `POST /control/v1/slots/0/check` 返回 409。代码核对确认 `check_managed_slot()` 在隧道已消失时仍先添加策略路由，并且检测函数本身没有调用 `repair_slot_once()`。

出口 1 当时的 `manual_required/no_same_country_candidate` 记录产生于 04:14 的上一轮受控故障测试，而不是用户 06:01 的新故障。旧故障人工恢复后没有可靠结束修复记录，导致新故障被“一次修复”保护规则误判为已经尝试过。

### 实现

- `check_managed_slot()` 先判断槽位 OpenVPN 进程。隧道不存在时不再向不存在的 TUN 添加路由，直接进入一次自动修复；隧道存在但真实接口也无法联网时同样修复一次。
- 如果隧道真实出口健康、只是本地 SOCKS5H 或策略路由失败，不更换 IP，只返回本地链路错误。
- 健康检测成功时显式 `mark_healthy()`，结束旧故障；`RepairStore.claim()` 对任何未结束的 `repairing/manual_required` 状态都拒绝重复领取，不再因候选 ID 为空或变化而重新计数。
- 检测响应增加一次性的 `auto_repair_performed` 字段，经 Gateway API 转为 `autoRepairPerformed`。前端等待期间明确显示检测与一次修复规则；结果区分修复成功、无同国候选、候选连接失败和此前已尝试。

### 本地验证

- 出口引擎：Python 编译检查通过，207 项单元测试通过。
- 前端：64 项测试通过，`vue-tsc` 与 Vite 生产构建通过。
- Gateway：`go test ./... -race`、`go vet -buildvcs=false ./...` 通过；Windows 的 Gateway 与 admin、Linux 的 Gateway 构建通过。
- `git diff --check` 通过；复杂度专项审查为 `Lean already. Ship.`。

### ny 部署与真实验证

- Gateway：`v1.0.4`，提交 `e3690f2dcee7cae264650b0341bd3b8c557c0b08`。
- 签名前端：`70f70194bdc0cb1cfd9932df46da0678956af42660ac88ae7dbcf611c6b4805c`。
- `aimilivpn.service` 的工作目录和启动文件均已切换到 `/opt/aimili-gateway/services/aimili-egress`；运行数据仍是 `/opt/aimilivpn/vpngate_data`。
- 第一次写入因旧部署脚本向当前受限 UI 安装器传递过时参数而失败，自动回滚后 Gateway 恢复 `v1.0.2`、AimiliVPN 恢复 `/opt/aimilivpn`。确认四服务 active、旧 UI 指针不变后，改用当前 `run-id + version` spool 合约安装签名 UI，随后完成 Gateway 与出口引擎部署。
- 精确清除出口 1 的陈旧测试记录后，同一检测请求真实返回：`status=disconnected`、`egress_ok=false`、`repair_status=manual_required`、`auto_repair_attempted=true`、`auto_repair_performed=true`、`last_error_code=no_same_country_candidate`。新 `attempted_at` 为当前故障时间，次数为 1。
- 重启 AimiliVPN 并等待超过一个后台检查周期后，出口 1 的 `attempted_at` 不变、次数仍为 1、没有新的自动修复日志，证明服务重启不会重新计数或循环换节点。
- 出口 1 首次真实验证完成时，主连接、出口 2、出口 3 的三条 SOCKS5H 链路均能访问公网，取得三个互不重复的真实出口；出口 1 保留为未连接等待人工替换。
- 随后增加被动同步修正：Gateway 只读取 AimiliVPN 已落盘的 `manual_required` 结果，不调用可能触发修复的 `CheckSlot`。`v1.0.4` 部署后，出口 1 在 Gateway 数据库中的错误从陈旧的 `egress_check_failed` 更新为 `no_same_country_candidate`；同步前后尝试次数和时间戳均未变化。定向测试同时验证相同状态再次同步不重复写库、不触发检查。
- 最新只读核对时，主连接在北京时间 07:13–07:15 独立出现两次真实出口失败，并在唯一一次同国候选替换中明确报 `candidate_dial_failed`，现为 `manual_required/replacement_failed`；出口 2、出口 3 继续健康，出口 1 仍为 `manual_required/no_same_country_candidate`。四服务 active，Gateway/AimiliVPN `NRestarts=0`，Gateway 数据库 `integrity_check=ok`，证明各出口故障互不连带，也没有循环替换。
- 生产 JS 包已核对包含“检测并自动修复”、等待过程和四类结果文案。后续已在 Codex 内置浏览器完成真实登录、点击和状态验收，最终结果见本文末尾的“内置浏览器与 40 节点池最终验收”。
- 没有新增永久备份；既有联合备份继续是 `/var/backups/aimili-gateway/egress-isolation-20260910-bed0cbf-ed102e3`。未操作其他 VPS，未删除 `aimili-vpngate`，未推送远程。

## 原仓库分叉处理

`aimili-vpngate` 的本地 `custom` 工作区在操作前为干净状态，随后通过 `git merge --ff-only feat/main-switch-protocol-modes` 从 `c98aa1e` 快进到 `ed102e3`。快进后 `custom` 与功能分支指向同一提交；没有创建新分支、没有产生新的合并提交、没有强制改写历史。随后 Python 编译检查和 204 项单元测试全部通过。

## 故障出口人工替换与 SOCKS5H 凭据轮换

### 人工替换真实验收

- 前端对 `degraded`、`repair_required` 出口持续显示原逻辑槽位，不再隐藏，并提供“复核状态”和“人工更换”。
- 现场首次点击人工替换时，前端已允许故障出口，后端却只允许 `ready`，请求返回 `conflict`。回归测试复现后，后端改为允许 `ready`、`degraded`、`repair_required` 作为人工替换目标，提交为 `97fb97689104117a85ff589a7bdb6207345b98c1`。
- 部署后选择美国住宅候选替换出口位 1：OpenVPN、`tun120`、策略路由表 200 和临时候选槽位均成功建立，但最终 `/control/v1/slots/0/check` 返回 409。系统自动恢复原俄罗斯候选，恢复后的检查返回 200；Gateway 数据库中出口位 1 为 `RU/residential/ready`，公网端口 20000、mixed 端口 30000，错误字段为空。
- 因此人工替换入口、执行过程和失败回滚均已真实验证；失败的美国候选没有被误标成成功。

### SOCKS5H 凭据轮换真实验收

- 页面新增“随机更换用户名和密码”，请求为 `POST /api/v1/settings/socks5h-credentials/rotate`。新凭据先应用到主连接和所有受管出口；只对当时健康的出口做真实 SOCKS5H、代理 DNS 和出口 IP 验证；全部通过后才原子保存密文。任何步骤失败都会恢复旧的 3x-ui 配置和数据库状态。
- 首次浏览器操作显示执行中反馈，3x-ui 日志证明四组入站都完成了新账号写入，随后又执行了对应回滚。失败发生在“写入后的真实验证”阶段，不是登录、按钮、API 权限或 3x-ui 写入阶段。旧版本没有记录具体出口和验证错误码，因此不把这一次历史失败猜成认证、DNS、超时或出口不一致。
- 提交 `90347f2d6827f21f32e8dda8a8f981998f6ffebd` 增加安全诊断日志，只记录阶段、逻辑出口 ID、槽位和错误码，不输出账号密码。Gateway 部署并恢复监听后，在用户已有登录信息的浏览器标签中再次执行同一操作，页面明确显示“SOCKS5H 用户名和密码已更换；旧代理地址已失效”。本次没有产生失败日志或回滚更新。
- 成功后的数据库证据：`mixed-username` 密文 SHA-256 为 `3ce43452...50a3`，`mixed-password` 密文 SHA-256 为 `7513412e...fb15`，两者 `updated_at=1789109248047`；均不同于失败回滚后的旧摘要，且 `PRAGMA integrity_check=ok`。本文只记录截断密文摘要，不记录任何可用凭据。
- 四服务最终均 active/running 且 `NRestarts=0`；只重启 Gateway，AimiliVPN、x-ui、Caddy PID 不变。仍只有 `/usr/local/bin/aimili-gateway.previous` 一个二进制回滚副本。

### 最新本地验证

- 前端：66/66 测试通过，`vue-tsc --noEmit` 与 Vite 生产构建通过。
- Gateway：`go test ./... -race -count=1` 与 `go vet -buildvcs=false ./...` 通过。
- Windows/Linux 的 Gateway 和 admin 四个构建均通过。
- 未操作其他 VPS，未删除原 `aimili-vpngate`，未推送远程分支。

## 40 节点池、订阅关联与公网 SOCKS5H 收尾（2026-09-12）

### 节点池与刷新结果

- ny 运行 Gateway `v1.0.10` / `fb50cc6`，`TARGET_VALID_POOL_SIZE=40`，但 `maxProxyGroups=3` 未变；40 表示候选池目标，不会创建 40 个 OpenVPN 隧道。
- “现有国家”“补充国家”、国家节点数量、“刷新所有国家”和 SOCKS5H 随机凭据按钮均已进入签名前端；旧“同步代理状态”入口已删除。
- 完成态刷新快照不再被后续实时池数量覆盖。出口引擎 216 项测试通过，提交为 `e4cb26f`；ny 的 `vpngate_manager.py` SHA-256 为 `77018cd7f36964a9dbaceb778d9d68ad2f166bbe70121e783aa7ad284a682aba`。
- VN 定向刷新真实返回：官方 2、可用 2、保留 1、节点池共 40。“保留 1”表示在 40 上限内最终进入池中的新增/更新候选，不是刷新失败。

### 订阅 Vision 标记根因与修复

- 外部验收最初发现 20002 入站在 3x-ui 中为 TCP/REALITY/Vision，但 `client_inbounds.flow_override` 为空，因此 v2rayN 订阅条目缺少 `flow=xtls-rprx-vision`。协议事务此前只更新 `inbounds`，没有同步订阅关联表。
- 事务现在把目标入站所有关联的 `flow_override` 一并纳入快照、写入、应用后校验和回滚；TCP/Vision 写入 `xtls-rprx-vision`，XHTTP 与 Hysteria2 清空。非目标入站、客户端和关联保持不变。
- 生产现场还证明客户端全局 `flow` 不能代表某个入站：同一客户端可关联多个不同协议出口。旧校验因此会错误返回 `managed_resource_drift`；现已取消这一错误的全局前置条件，由每个入站自己的 `flow_override` 决定订阅协议。
- 新增测试先复现失败，再完成最小实现；代码审查后又加入关联旧值 CAS、关联集合校验、旧快照隔离，以及“只回滚本事务实际生成的 auth”约束，避免覆盖并发人工修改、用旧快照清空 Vision 或覆盖共享客户端凭据。协议事务与外部验收脚本合计 101 项通过。ny 事务脚本 SHA-256 为 `72b94bbf8b3a5d2b7dea075548332b5ec88ad4279e475feba0da7a635b773ce6`。
- 出口 3 通过正式协议事务真实切换并恢复 TCP/Vision，两次操作均 `applied`、`finalized`；20002 的两个受管订阅关联最终都带 Vision 标记，没有直接裸改生产数据库。

### 故障出口与主 SOCKS5H 恢复

- 全链路复核时 `tun120`、`tun121` 确实不存在，出口 1、2 是真实故障而非前端误报。出口 1 使用同国 JP 住宅候选恢复；出口 2 先刷新得到 VN 住宅候选，再按同国原则恢复。两者最终均为 `ready`，协议和固定端口未改变。
- 主 SOCKS5H 的 VPS 本机认证通过、公网 TCP 可建连，但 Windows 外部握手超时；对照出口 1–3 后确认 UFW 只允许 `30000:30999/tcp`，遗漏主端口 `31000/tcp`。单独增加 `31000/tcp` 后，主 SOCKS5H、代理 DNS 和出口 IP 外部验证立即通过。
- 新安装脚本已增加 `ufw allow 31000/tcp`，部署合约测试先失败后通过，避免以后重装再次遗漏。
- 最终外部验证为 4/4：四个 SOCKS5H、四个真实公网协议、代理 DNS、唯一出口 IP、订阅四条覆盖、四个 mixed 与四个公网入站、单一 Xray 全部通过。

### 最终运行状态与浏览器验收

- `aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 均 active，`NRestarts=0`；`tun0/tun120/tun121/tun122` 与 `7928/17928/17929/17930` 全部存在，最近两小时没有 OOM。收尾期间出口 3 的上游隧道曾再次消失并进入单次修复后的人工等待；正式协议事务没有因此执行或误报成功。使用剩余同国 JP 住宅候选人工替换后恢复为 `ready`，随后 4/4 外部验收再次通过。
- VPS 约 458 MiB 内存，验证时可用约 177 MiB；1 GiB Swap 余约 855 MiB。没有增加实际运行出口数量。
- ny 只保留 `/var/backups/aimili-gateway/pool-refresh-fb50cc6` 一份正式联合备份；旧备份和 `/tmp/aimili-pool-refresh-fb50cc6` 已删除。
- Codex 内置浏览器实际一直保存着登录信息；直接点击登录即可进入。验收过程没有读取、猜测或输出账号密码，也没有切换到 Edge 或其他外部浏览器。

### 主连接最终恢复复验

- 最终收尾期间，原 TH 住宅主节点再次真实失效：`7928` 对 `api.ipify.org` 与 `ip.sb` 均超时，随后 `tun0` 消失；Gateway 同时把 `agw-main` 标记为 `degraded/egress_unavailable`，出口 1–3 仍保持 `ready`。因此本次失败边界是主上游节点，不是浏览器、订阅或主 SOCKS5H 防火墙。
- 节点池维护期间发起替换得到 `operation_busy`，没有执行节点变更。维护结束后，同国 TH 候选明确返回 `candidate_dial_failed`，事务进入 `repair_required`，没有把失败结果误标为成功或继续循环尝试。
- 人工从现有候选池选择低延迟 RU 住宅节点，通过正式 `/replace` 修复流程完成主连接恢复。修复后 AimiliVPN 主状态为 `active/egress_ok/healthy`，主事务回到 `idle`，`tun0` 恢复。
- 最新 `scripts/verify-external-client-v1c.py` 返回 `status=pass`：主连接加出口 1–3 共 4 条 SOCKS5H、4 条公网协议、代理 DNS、订阅 4/4、四个不同出口 IP、四个 mixed 与公网入站、单一 Xray 均通过。
- 最终复核确认四服务均 `active/running` 且 `NRestarts=0`，四个 TUN 与 `7928/30000/30001/30002/31000` 均存在，最近两小时无 OOM；ny 仍只保留 `/var/backups/aimili-gateway/pool-refresh-fb50cc6` 一份正式备份。

### 内置浏览器与 40 节点池最终验收

- 在 Codex 内置浏览器真实确认“现有国家”“补充国家”及其国家节点数量、“刷新所有国家”和“随机更换用户名和密码”均已上线；VPN 与 SOCKS5H 两个页面均能显示主连接和出口 1–3。
- 真实点击 SOCKS5H 凭据轮换后，页面显示“SOCKS5H 用户名和密码已更换；旧代理地址已失效”；轮换后再次执行外部验收，4/4 全部通过。
- 真实点击“刷新所有国家”后，页面先显示刷新过程，随后显示“最后刷新：所有国家 · 成功”：官方候选 100、本次检测 76、刷新后可用 40、节点池共 40。低内存配置保持 `OPENVPN_TEST_CONCURRENCY=1` 和 `NODE_TEST_BATCH_SIZE=1`，没有把 40 个候选变成 40 条同时运行的隧道。
- 刷新期间免费主节点真实失效，系统只自动尝试一次同国替换，失败后前端保留主连接并显示“自动修复已失败，等待人工更换”；随后通过候选行的“替换到出口位”把住宅节点装载到主连接，页面显示“主连接替换成功”。该节点后来再次失效时，系统只处理这一条主连接，出口 1–3 始终保持在线。
- 最后一次前端“检测并自动修复”完整显示了检测过程，结束时显示“主连接检测成功；真实出口、SOCKS5H 和当前公网协议链路正常”；主连接和出口 1–3 均回到“已启用”。基于当时实际主节点再次运行 `scripts/verify-external-client-v1c.py`，结果仍为 `status=pass`、`ready_groups=4`、`verified_groups=4`、`unique_exit_ips=true`。
- 主节点失效会先从候选池移除坏节点，使“当前有效”短暂从 40 变为 39；后台随后按单并发补回目标 40。刷新完成提示记录的是那次刷新结束时的快照，不代表免费节点此后永不失效。

## 50 节点扩容验收（2026-09-12）

### 容量与资源边界

- ny 的候选池目标由 40 调整为 50；`maxProxyGroups=3`、主连接和三个普通出口的实际运行数量没有增加。生产三处配置均为 `TARGET_VALID_POOL_SIZE=50`，继续保持 `OPENVPN_TEST_CONCURRENCY=1`、`NODE_TEST_BATCH_SIZE=1`、`CPUQuota=50%`、`MemoryHigh=180M`、`MemoryMax=220M`。
- Codex 内置浏览器真实显示官方候选 99、当前有效 50、20 个国家，刷新完成提示为“官方候选 99、本次检测 62、刷新后可用 50、节点池共 50”。
- 全国家刷新期间 AimiliVPN 内存峰值约 63 MiB，`NRestarts=0`，没有 OOM。主连接现场失效时，出口 1–3 及 `tun120/tun121/tun122` 全程保持在线，单次自动修复保护也没有因服务重启或补池循环重复领取。

### 扩容后暴露并修复的问题

- 重新平衡前会把本轮刚失败的旧记录重新混入有效池。现在先排除本轮黑名单节点，再执行保留和补齐；新增回归测试证明失败节点不会被旧记录恢复。
- 50 节点安全响应约 17 KiB，超过 Gateway 原 16 KiB 读取上限。本机受认证的 AimiliVPN 控制接口响应上限调整为 64 KiB，字段白名单和异常响应校验保持不变；测试同时覆盖 50 节点正常响应和超过 64 KiB 继续拒绝。
- 主连接人工恢复时，Gateway 原先只按“旧主节点 + 新候选”生成 AimiliVPN 幂等编号。主连接为空且再次选择历史上用过的同一候选时，会命中旧的已提交记录，AimiliVPN 不会拨号，Gateway 因收到旧状态显示“服务返回了无法识别的结果”。日志证明失败请求只有一次 `POST /control/v1/main/assign`，没有 OpenVPN 拨号；`main_assignment.json` 同时存在该候选的旧提交记录。
- 修复后，Gateway 将当前 HTTP 人工替换操作的持久唯一编号传给 AimiliVPN；同一次请求重放仍保持幂等，不同故障批次即使选择相同候选也会创建新操作。新增测试覆盖“两个独立 HTTP 操作使用两个不同 AimiliVPN 幂等编号”和“中断后的 HTTP 操作继续传递原编号”。

### 生产恢复与最终验收

- 新 Gateway 部署后，在 Codex 内置浏览器再次选择泰国住宅候选替换主连接。AimiliVPN 最新日志显示实际连接 `27.145.187.221:4013`、创建 `tun0`、完成策略路由、验证代理出口，并提交新的主连接事务；前端显示“主连接替换成功”。
- 最终页面同时显示主连接和出口 1–3 四条“已启用”，候选池仍为当前有效 50。服务器上 `tun0/tun120/tun121/tun122` 同时存在；主状态为 `active=true`、`egress_ok=true`、`repair_status=healthy`，Gateway 与 AimiliVPN 均 active 且 `NRestarts=0`。
- `scripts/verify-external-client-v1c.py` 最新结果为 `status=pass`、`ready_groups=4`、`verified_groups=4`、`unique_exit_ips=true`：四个 SOCKS5H、四个公网协议、代理 DNS、订阅覆盖和四个不同出口 IP 全部通过。
- 最新本地验证：全部 Go 测试通过；AimiliVPN 218 项测试通过；前端 67 项测试、`vue-tsc --noEmit` 和 Vite 生产构建通过；WSL root 安装器夹具通过。
