# Aimili Gateway 当前状态交接

日期：2026-09-05（Asia/Shanghai）。状态：当前权威入口。

## 当前任务与阻断

两层发布机制已完成本地实现与代码复审；最新源码 `b0dcc39` 已部署为Gateway `v1.0.0`。新外部UI已在生产完成免重启发布、回退和恢复。后端 `v1.0.1` 的真实升级在停止服务前被数据库完整性门拒绝，Gateway仍运行v1.0.0。

明确阻断是两个会话索引不一致：sessions表429行、索引418项；Python的旧quick_check未检出，完整integrity_check与Go校验均失败。root私有副本中仅重建 `sessions_expiry_idx` 和 `sqlite_autoindex_sessions_1` 后校验通过，所有表的数据摘要不变。生产DB未修复。

用户此前明确禁止直接编辑生产Gateway DB，因此必须获得仅限这两个索引的新增修复授权；不要重放失败升级、绕过完整性检查或执行现有revoke-sessions冒充修复。

最新证据：`docs/verification/2026-09-05-safe-gateway-self-update.md`。

## 当前路径

| 项目 | 路径 |
| --- | --- |
| Gateway功能工作树 | `D:\CodexProject\Github\aimili-gateway\.worktrees\main-switch-protocol-modes` |
| AimiliVPN功能工作树 | `D:\CodexProject\Github\aimili-vpngate\.worktrees\main-switch-protocol-modes` |
| 3x-ui补丁工作树 | `D:\CodexProject\Github\aimili-3xui-deploy\.worktrees\main-switch-protocol-modes` |
| 历史简化部署资料 | `D:\CodexProject\Github\aimili-3xui-simple-deploy` |

功能分支为 `feat/main-switch-protocol-modes`。旧的同级 `aimili-gateway.worktrees` 路径已失效；沿用当前工作树，不重新创建或扫描全仓库。

## Git状态

Gateway最新业务提交：

- `2f87fce`：签名绑定、持久安装记录及恢复。
- `4ac4787`：固定受限systemd入口、跨UID请求与队列生命周期。
- `b0dcc39`：候选权限、UI回滚版保留和不确定状态诊断资产保留。

2026-09-05本轮 `git fetch --prune origin` 后，代码HEAD领先远程33个提交；本次文档提交另增加一个。尚未推送GitHub、尚未合并main。工作树中的既有 `.deploy-assets/`、测试缓存、Windows可执行文件、`scripts/__pycache__/` 保留未跟踪，不纳入文档提交。

其他仓库本轮未更改或重新联网核对。上轮记录为AimiliVPN `88be2fb`、3x-ui补丁 `5dbe6f0`；需要操作它们时再核对Git，不把这些背景记录当成本轮远程同步证据。

## 生产最新状态

2026-09-05 14:06:43 UTC：

- Gateway/AimiliVPN/x-ui/Caddy均active，PID为989591/916096/916107/916125。只有Gateway在本轮bootstrap时重启。
- 4 OpenVPN、1 Xray。主连接active/egress正常，三个槽位up/egress正常。
- 主连接XHTTP/REALITY；出口1、出口2 TCP/Vision；出口3 Hysteria2。四条协议ready；固定公网/mixed端口不变。
- x-ui DB quick_check正常、资源指纹不变、4个订阅alias。Gateway DB可读，但完整性检查失败，不能表述为数据库健康。
- 来源限制关闭，applyStatus=applied；本轮未改变SOCKS5H策略或v2rayN状态。
- 根分区51%，可用4,603,858,944字节；本轮清理138,117,120字节临时上传/测试/诊断资产。
- 唯一 `/usr/local/bin/aimili-gateway.previous` 保留。原 `/var/backups/aimili-gateway/20260905-external-ui` 约17.64 MB暂留，待后端回滚验收后才能退休。
- 当前UI：`80e689af1a5b8003bd3a3bb68e40106bf05c245d07b5215bdf6fb68926f355bf`；previous为 `af05c9f205d7a51078ddf75e86c2df0443350d476074d7d974025d7550f790aa`。
- `allowGatewayInstall=false`、fetcher marker缺失、网页updateEnabled默认false；request/staging均为空、无active journal，保留6条终态结果。

## 恢复执行顺序

1. 按recovering-interrupted-tasks核对本记录、本轮未提交差异及生产只读状态。
2. 取得会话索引限定修复授权后，保留一个一致性DB恢复副本，仅重建两个会话索引；核对业务表摘要、完整integrity_check和四服务/四出口。
3. 重新上传本地已签名发布包或按最终提交重建；先核对归档及逐文件摘要。解压后显式恢复上传根目录0700。
4. 通过固定installer执行新的v1.0.1 dry-run/install，使用新的run ID，不重放已有失败结果。
5. 验证Gateway回滚/恢复、唯一previous、数据面不变，再清理真正无用的旧回滚资产。
6. 本轮已经完成UI发布/回滚/恢复与公网资源摘要验证，除非代码/状态变化，不从头重复。
7. 没有可信HTTPS来源/catalog时保持网页mutation禁用，不虚构发布URL或宣称一键下载已验收。

## 本地资产与执行入口

- 实施计划：`docs/superpowers/plans/2026-09-05-safe-gateway-self-update.md`。
- 最新签名包：`.deploy-assets/gateway-updater-b0dcc39-20260905/package.tgz`；归档SHA256为 `05b8b3eaf015761d4faae0e60741852780e9ff58391acfa189dc2785a6eb1391`。
- 可重复打包脚本：`.deploy-assets/build-safe-updater-package.ps1`。
- 只读预检：`.deploy-assets/stage0-gateway-update-safe.py`，已增加完整integrity_check与有效busy门。
- 原 `.deploy-assets/prepare-offline-updater-run.py` 固定指向本轮已删除的上传目录；后续必须按新的准确路径调整，不能直接重放。
- 持久签名密钥位于 `%LOCALAPPDATA%\AimiliGateway\release-signing`，私钥不入仓库、不上传、不输出。
- 临时生产diagnostic unit、程序、私有DB副本和本轮远端上传目录均已清理。

## 历史成果与边界

用户此前确认v2rayN使用正常。主连接切换、每出口协议、订阅关联恢复、候选失败处理、国家名称规范化和刷新通知持久关闭等历史成果继续保留；本轮没有重新替换节点、切协议或导入客户端订阅。

旧设计、计划和专题验证记录保留各自发生时的事实；当前状态以本记录及本轮最新代码/Git/生产检查为准。README与AGENTS.md分别提供项目入口与开发部署边界，无需新增另一套重复的状态文件。
