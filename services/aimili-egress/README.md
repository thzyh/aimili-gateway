# Aimili 出口引擎

本目录是 Aimili 出口引擎今后的唯一源码来源。代码最初从 `aimili-vpngate` 的本地提交 `ed102e3` 并入，包含主连接、三个普通出口、节点池、控制 API、代理服务及其测试。

## 运行边界

- “一个仓库”只表示 Gateway 与出口引擎一起维护、一起审查版本，不表示合并成一个进程。
- 生产上仍是两个独立服务：`aimili-gateway.service` 负责网页和编排，`aimilivpn.service` 负责 OpenVPN、TUN、策略路由和本地代理。
- Gateway 保持低权限，只通过 `127.0.0.1:8790` 的控制 API 调用出口引擎；只有出口引擎保留网络管理权限。
- 运行数据不放进 Git，统一保存在 `/var/lib/aimili-gateway/aimili-egress`；源码从 `/opt/aimili-gateway/services/aimili-egress` 启动。
- 原 `aimili-vpngate` 仓库只作为待删除的历史副本，不再参与构建、部署或运行；新功能只在本目录修改。
- 历史开发线 `b190c8c`、`4a328c8` 中不与 xjp 行为冲突的真实出口延迟和低争用补池逻辑已经收回本目录；发生冲突时以 xjp 已验证行为为准。

原 AimiliVPN 网页仍是出口引擎内部的兼容能力，但 Gateway 前端不再提供该页面或原后台入口。日常出口管理继续在 Gateway 页面完成，高级设置仅保留 3x-ui 专家入口。

## 本地验证

在本目录执行：

```powershell
python -m py_compile vpngate_manager.py egress_repair.py control_api.py node_pool.py proxy_server.py vpn_utils.py main_assignment.py
python -m unittest discover -s tests -p 'test_*.py'
```

这些测试不需要第三方 Python 包。真实 OpenVPN、TUN 和路由验收必须在单独授权的 Linux 测试机进行，不能用单元测试替代。
