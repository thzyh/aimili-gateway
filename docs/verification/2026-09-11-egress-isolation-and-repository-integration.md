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
- 生产 JS 包已核对包含“检测并自动修复”、等待过程和四类结果文案。Codex 内置浏览器因本机 Codex 授权令牌不可用，未执行真实鼠标点击；用户刷新页面后的视觉确认仍是最后一步。
- 没有新增永久备份；既有联合备份继续是 `/var/backups/aimili-gateway/egress-isolation-20260910-bed0cbf-ed102e3`。未操作其他 VPS，未删除 `aimili-vpngate`，未推送远程。

## 原仓库分叉处理

`aimili-vpngate` 的本地 `custom` 工作区在操作前为干净状态，随后通过 `git merge --ff-only feat/main-switch-protocol-modes` 从 `c98aa1e` 快进到 `ed102e3`。快进后 `custom` 与功能分支指向同一提交；没有创建新分支、没有产生新的合并提交、没有强制改写历史。随后 Python 编译检查和 204 项单元测试全部通过。
