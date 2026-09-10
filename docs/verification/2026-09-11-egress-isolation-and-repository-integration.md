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

- 统一仓库布局尚未再次部署到 ny；ny 继续运行已通过真实故障注入验证的两服务版本，避免在稳定性验证刚完成后引入第二次无关生产变更。
- 未操作其他 VPS。
- 未删除原 `aimili-vpngate` 仓库。
- 未推送任何远程分支。

## 原仓库分叉处理

`aimili-vpngate` 的本地 `custom` 工作区在操作前为干净状态，随后通过 `git merge --ff-only feat/main-switch-protocol-modes` 从 `c98aa1e` 快进到 `ed102e3`。快进后 `custom` 与功能分支指向同一提交；没有创建新分支、没有产生新的合并提交、没有强制改写历史。随后 Python 编译检查和 204 项单元测试全部通过。
