# Aimili Gateway 当前状态交接

日期：2026-09-06（Asia/Shanghai）。状态：当前权威入口。

## 2026-09-06 本机 Docker 单出口原型

AimiliVPN 单出口数据面原型已在 Windows Docker Desktop 实际运行并通过真实出口验证。容器使用专用 bridge/volume、只读根文件系统、`CAP_NET_ADMIN` 和 `/dev/net/tun`，只向 Windows 回环发布 `17928` 代理与 `18787` 管理页；`tun0`、单 OpenVPN、代理出口与宿主直连出口差异均已验证。

启动和验证安全门确认 v2rayN PID、系统代理、Windows 默认路由及现用代理 HTTP 204 健康不变。没有连接 ny 或读取任何生产资产。原型当前保持运行，等待用户执行 `aimili-vpngate/deploy/docker-single-exit/show-access.ps1` 后自行验收。

本阶段尚未容器化 Gateway、3x-ui/Xray、订阅和四出口协议事务。完整事实和本地提交见 `docs/verification/2026-09-06-local-docker-single-exit-prototype.md`；不能把单出口原型表述为完整本机 Docker 部署。

## 2026-09-06 出口检测与恢复 UI 修复

ny 出口3的 `tun122` 消失后，AimiliVPN 对槽位2检查明确返回 `egress_check_failed`，但旧前端把“重新检测并同步”按钮错误映射为单纯 `/check`，因此只能重复报告检测失败，不能调用已有的受保护恢复事务。Gateway后端、AimiliVPN、x-ui和Caddy当时均为active；这不是Hysteria2参数或Gateway进程故障。

本地提交 `f52db98` 修复了这处前端编排：正常出口只检测并显示可关闭的处理中、成功或失败通知；只有 `degraded` 出口且后端明确返回 `egress_check_failed` 或 `candidate_egress_failed` 时，才按 `check → rotate` 顺序在同一逻辑槽位恢复。`repair_required` 和其他错误不会盲目换节点。定向测试30项、前端全量65项、生产构建及 `git diff --check` 均通过。

签名UI `d4ee05c847064514e86898c3994b42a38341646a7421ca115b1246453b623b34` 已通过固定installer免重启发布；生产清单绑定完整提交 `f52db98cce58206efdd14d3a1f2f6da0c9c842e7`，实际JS摘要与本机构建一致。用户要求自行执行页面验收，因此发布后没有代点恢复按钮：出口3仍保持 `degraded/egress_check_failed`，等待用户刷新页面后点击“重新检测并同步”。

## 当前任务与阻断

两层发布机制已完成本地实现与代码复审；生产Gateway后端仍为 `v1.0.0/b0dcc39`，本轮只发布了绑定 `f52db98` 的外部UI，没有重启Gateway。后端 `v1.0.1` 的真实升级在停止服务前被数据库完整性门拒绝，Gateway仍运行v1.0.0。

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

- `f52db98`：故障出口受限恢复与正常检测反馈UI。
- `2f87fce`：签名绑定、持久安装记录及恢复。
- `4ac4787`：固定受限systemd入口、跨UID请求与队列生命周期。
- `b0dcc39`：候选权限、UI回滚版保留和不确定状态诊断资产保留。

2026-09-06本轮 `git fetch --prune origin` 后，`f52db98` 所在分支领先远程39个提交；本次文档提交另增加一个。尚未推送GitHub、尚未合并main。工作树中的既有 `.deploy-assets/`、测试缓存、Windows可执行文件、`scripts/__pycache__/` 保留未跟踪，不纳入文档提交。

AimiliVPN功能工作树本轮只读核对为 `9e0d566`、领先远程10个提交，未修改；3x-ui补丁仓库本轮未重新联网核对。需要操作它时再核对Git，不把背景记录当作最新远程同步证据。

## 生产最新状态

2026-09-06 01:15 UTC：

- Gateway/AimiliVPN/x-ui/Caddy均active，四项 `NRestarts=0`；本轮UI发布没有重启任何服务。
- 3个OpenVPN、1个Xray。`tun0`、`tun120`、`tun121`存在，`tun122`缺失；Gateway DB只读结果为槽位0/1 ready、槽位2 degraded且错误码为 `egress_check_failed`。
- 本轮没有切换公网协议、固定公网/mixed端口或SOCKS5H来源策略；出口3等待用户通过新UI执行受限恢复和最终验收。
- x-ui DB quick_check正常、资源指纹不变、4个订阅alias。Gateway DB可读，但完整性检查失败，不能表述为数据库健康。
- 来源限制关闭，applyStatus=applied；本轮未改变SOCKS5H策略或v2rayN状态。
- 根分区52%，可用约4.2 GiB；本轮签名UI staging已由installer清理，没有遗留上传目录。
- 唯一 `/usr/local/bin/aimili-gateway.previous` 保留。原 `/var/backups/aimili-gateway/20260905-external-ui` 约17.64 MB暂留，待后端回滚验收后才能退休。
- 当前UI：`d4ee05c847064514e86898c3994b42a38341646a7421ca115b1246453b623b34`；previous为 `80e689af1a5b8003bd3a3bb68e40106bf05c245d07b5215bdf6fb68926f355bf`。发布目录严格保留两版，staging为空。
- `allowGatewayInstall=false`、fetcher marker缺失、网页updateEnabled默认false；request/staging均为空、无active journal，保留6条终态结果。

## 恢复执行顺序

1. 用户刷新Gateway页面并点击出口3“重新检测并同步”；确认绿色恢复通知、出口3 ready、`tun122`恢复，并复测对应公网节点。不要代替用户执行浏览器验收。
2. 按recovering-interrupted-tasks核对本记录、本轮未提交差异及生产只读状态。
3. 取得会话索引限定修复授权后，保留一个一致性DB恢复副本，仅重建两个会话索引；核对业务表摘要、完整integrity_check和四服务/四出口。
4. 重新上传本地已签名后端发布包或按最终提交重建；先核对归档及逐文件摘要。解压后显式恢复上传根目录0700。
5. 通过固定installer执行新的v1.0.1 dry-run/install，使用新的run ID，不重放已有失败结果。
6. 验证Gateway回滚/恢复、唯一previous、数据面不变，再清理真正无用的旧回滚资产。
7. 本轮已经完成UI发布/回滚/恢复与公网资源摘要验证，除非代码/状态变化，不从头重复。
8. 没有可信HTTPS来源/catalog时保持网页mutation禁用，不虚构发布URL或宣称一键下载已验收。

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
