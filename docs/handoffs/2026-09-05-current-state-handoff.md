# Aimili Gateway 当前状态交接

日期：2026-09-09（Asia/Shanghai）。状态：当前权威入口。

## 2026-09-09 节点池与来源限制追加修复

本机 VMware 的 Gateway 已部署兼容修复：旧版 AimiliVPN 国家目录缺少汇总字段时，Gateway 从当前有效候选快照补齐统计；实时 UI 数据为官方 99、当前有效 40，国家数随当前候选刷新变化且不再显示 0，最终复核为 3 国。统一账户变更导致 mixed 入站保留旧代理账号时，Gateway 在严格确认 `agw-` 所有权后自动同步账号，并在后续 Xray 更新失败时恢复原配置。来源限制已完成关闭、开启、再次关闭和最终恢复开启的真实往返验证，最终为 `enabled=true`、`applyStatus=applied`、仅允许 `192.168.88.1/32`；3x-ui 受管资源 `ownershipMatches=true`。

AimiliVPN 重启后五个出口位重新选择了可用候选，已再次 provision 并同步 Gateway、3x-ui/Xray 和六条订阅。VM 内门禁最新为 `nativeReady=true`，四服务 active/enabled，实际数量仍为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。服务端用 Caddy 本地根 CA 严格请求订阅为 HTTPS 200、`text/plain`、6 条可解析 `vless`/`hysteria2` 节点。

AimiliVPN 已增加 OpenVPN/TUN 重连后的策略路由自愈。在 VM 上清空出口位 5 的表 204 后，守护线程于 20 秒内自动恢复 `tun124` 的默认路由与选表规则，没有重启 OpenVPN 或替换节点；重新 provision 后完整门禁仍为 `nativeReady=true`。最新 AimiliVPN 全量测试 71 项通过，实现提交为 `12d0588`。

v2rayN `2026-09-09 09:41` 与 `09:49` 日志的第一失败边界是 `net_ssl_io_cert_chain_validation, PartialChain`，尚未进入订阅内容解析。Windows 当前未信任本机 Caddy 根 CA；本轮没有修改系统证书信任库，也没有代替用户操作 v2rayN。最终订阅导入仍由用户在处理 CA 信任后验收。

## 2026-09-09 本机 VMware 五出口真实部署闭环

本机 VMware Ubuntu 部署已完成自动闭环，ny VPS 本轮未连接、未读取、未修改。VMX 为 `D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`，当前 VMnet8 地址为 `192.168.88.4`；Windows 用户入口是 `https://192.168.88.4:8080`，不是 `127.0.0.1`。Caddy 本地根 CA 严格请求返回 HTTP 200。

最终门禁通过：四服务 active/enabled；manifest 期望与实际均为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位；`nativeReady=true`。数量来自 manifest，不是永久上限。Windows 外部验证为 `status=pass`、6/6 mixed/SOCKS5H 与代理 DNS 通过、6/6 公网协议通过、6 个唯一出口与来源认证通过、6 条订阅覆盖完整、单一 Xray。Hysteria2 保持严格证书验证，没有使用 `allowInsecure=true`。宿主默认路由、DNS、防火墙、系统代理、系统证书信任库及 v2rayN 未修改。

本轮新增修复包括：Gateway 单槽位检测使用 75 秒操作超时；外部验证读取当前统一账户而不是 bootstrap 账户；动态订阅按 1–6 排序；出口位在进程重启后优先恢复仍可用的上次节点，失败候选进入冷却；主连接与出口位 1 的重复出口已通过仅轮换该槽位消除。SOCKS5H 来源限制为 `enabled/applied`；3x-ui 和 AimiliVPN 原后台检测为 HTTP 200，自动登录返回同源 HTTP 303；账户管理显示三服务已同步。

磁盘已删除 35 个旧备份和 20 个 staging，只保留 `/var/backups/aimili-local/final-20260909-closed-loop`；根分区由 49% 降至 23%，约 18 GB 可用。最新脱敏外部证据采集于 `2026-09-08T18:59:49Z`，完整证据和阻塞处理见 `docs/verification/2026-09-07-local-vm-real-deployment.md`。

本轮最新测试：AimiliVPN 69 项、前端 66 项、Gateway Python 108 项（2 项按平台能力跳过）、Go 全包与 race、vet、所有实际存在的本机 VM PowerShell/Bash/Python 测试均通过。过时总入口仍引用未跟踪且不存在的 `create-vm.ps1`，不作为当前通过项。复杂度审查结论为 `Lean already. Ship.`。实现提交为 Gateway `c6bca94`、AimiliVPN `bbd277b`；两个功能分支均已普通推送，并在推送后通过 `git fetch` 复核本地实现提交与远端跟踪提交一致。本状态记录的后续文档提交不改变实现内容。

剩余唯一用户步骤是浏览器登录、订阅导入和 v2rayN 验收。本轮未使用 Computer Use，不代替用户操作 v2rayN。服务端订阅已解析并完成 6/6 真实握手，但用户原始 v2rayN“更新订阅”操作仍应亲自复测。浏览器未信任本地 CA 时可能显示证书警告；自动验证没有修改 Windows 系统信任库。

## 2026-09-06 本机 VMware 原生部署恢复检查

当前本机目标已改为 Ubuntu VM 内原生 systemd；不保留容器第二方案。ny 保持现状，本轮未连接或读取生产资产。VM、24 GiB 动态磁盘、固定 OVA、NoCloud seed、SSH 密钥和宿主安全门继续复用。

精确匹配 Compose 标签和绝对配置路径后，已删除单出口原型容器、镜像、数据卷和网络；其他容器、镜像、卷、网络的 ID 集合均保持一致。卷数据约 3.558 MB，删除不可恢复；镜像逻辑大小 144,173,968 字节，约 143.7 MB 为共享层，不能视为实际释放空间。未清理共享构建缓存或压缩 Docker 虚拟磁盘。原型代码、测试、设计、计划与验证入口同步删除，历史由 Git 保留，不重写历史。

VM 本轮检查为运行中且 SSH 可达，2 vCPU、约 2 GiB 内存、约 1 GiB swap，根文件系统约 23.84 GB。唯一默认路由走 ens192；UFW active、默认允许出站、只有一条入站允许规则。VM 未安装 Docker，四项业务服务均 inactive，OpenVPN/Xray 进程数均为 0。

当前失败边界：局域网网关 ping 成功，公共 IP TCP 443/53 与 DNS 均失败；尚未确认根因，没有修改网络。出网验证前禁止业务安装。宿主 v2rayN PID、系统代理和默认路由摘要在只读检查前后相同。

下一步：按 architectural brainstorming 审核原生部署设计，然后制定逐文件计划。既有 Task 3 VM 基础修改与未跟踪构建资产保留，status.ps1 的旧状态字段待新设计批准后以行为测试驱动替换。最终浏览器、订阅及 v2rayN 验收由用户执行，当前均未执行。两个仓库不推送。

原生部署开发已开始。`deploy/local-vm/native/deployment.json` 当前清单声明主连接＋3 个出口、期望 4 个 OpenVPN、期望 1 个 Xray；状态脚本按清单与运行时实际值比较，不把这些数字写成永久上限。VM 网络预检最新第一失败边界为 `upstream_tcp`：局域网网关可达、UFW 出站允许，但公共 TCP 443 被拒绝，DNS/HTTPS 因此尚未通过。未修改网络配置，未安装业务。

## 2026-09-06 出口3候选耗尽恢复

用户复测证明提交 `f52db98` 的 `check → rotate` 分支已真实执行，但三次 rotate 都在约一秒内返回409且没有产生OpenVPN拨号日志。只读核对定位到第一处失败边界：AimiliVPN槽位2已是 `pending`，`tun122`、原候选身份和pin均不存在；槽位约束仍为 `JP + residential`，当时本地20个节点中符合该约束的候选为0。国家目录同时记录42至43个JP官方候选，因此不是协议参数、3x-ui、Gateway DB或日本无官方节点，而是恢复操作没有在本地候选耗尽时补充该槽位国家。

生产先通过现有受限接口补充JP候选：43个官方候选中精验8个，得到7个可用节点，其中3个为住宅；缓存由20增至25。随后既有rotate返回200，`tun122`恢复，出口3真实SOCKS5H返回204。旧的常驻check-slots helper因不认识新增 `externalUiRoot` 配置字段曾返回 `config_failed`，不是DB或槽位故障；它已原子更新为当前构建并保留唯一previous。正式helper最终连续检查三个槽位并安全同步为ready；中间一次出口3 Hysteria2公网探测瞬时 `timeout`，当时 `tun122` 和SOCKS5H始终正常，下一次完整检查通过。

本地提交 `eab2e9f` 把根因处理固化到“重新检测并同步”：仅当degraded出口已确认运行时故障、首次rotate返回 `slot_rotate_failed` 时，才自动补充该出口原国家，轮询完成后在同一逻辑槽位重试rotate。不放宽国家或代理类型，不改变端口，也不影响正常检测和手动替换。TDD定向30项、前端全量65项、生产构建和 `git diff --check` 均通过。

签名UI `2186f2e88858d1329a03c2210ae7d4c778476a75527aa543953b81114c5ba3fc` 已通过固定installer免重启发布；生产清单绑定完整提交 `eab2e9f116ec712b838826c5dcdd4ea54fc4dd1f`。current/previous严格保留两版，staging和active journal均为空。用户仍负责最终页面点击验收；本轮没有使用computer use。

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

## 2026-09-06 Git状态（历史记录）

Gateway最新业务提交：

- `eab2e9f`：故障出口候选耗尽时自动补充原国家并重试恢复。
- `f52db98`：故障出口受限恢复与正常检测反馈UI。
- `2f87fce`：签名绑定、持久安装记录及恢复。
- `4ac4787`：固定受限systemd入口、跨UID请求与队列生命周期。
- `b0dcc39`：候选权限、UI回滚版保留和不确定状态诊断资产保留。

2026-09-06本轮 `git fetch --prune origin` 后，提交 `eab2e9f` 所在分支领先远程41个提交；本次文档提交会再增加一个。尚未推送GitHub、尚未合并main。工作树中的既有 `.deploy-assets/`、测试缓存、Windows可执行文件、`scripts/__pycache__/` 保留未跟踪，不纳入文档提交。

AimiliVPN功能工作树本轮只读核对为 `9e0d566`、领先远程10个提交，未修改；3x-ui补丁仓库本轮未重新联网核对。需要操作它时再核对Git，不把背景记录当作最新远程同步证据。

## 生产最新状态

2026-09-06 02:03 UTC：

- Gateway/AimiliVPN/x-ui/Caddy均active，四项 `NRestarts=0`；本轮UI发布没有重启任何服务。
- 4个OpenVPN、1个Xray；`tun0`、`tun120`、`tun121`、`tun122`均存在。主连接和三个槽位的真实SOCKS5H请求均返回204。
- Gateway DB只读结果：主连接TH/住宅/XHTTP ready；出口1 VN/住宅/Hysteria2 ready；出口2 KR/机房/TCP Vision ready；出口3 JP/住宅/Hysteria2 ready。四项错误字段均为空。
- 本轮只为恢复出口3执行了JP候选补充和同槽位rotate；没有切换公网协议、固定公网/mixed端口或SOCKS5H来源策略。用户仍需执行最终页面验收。
- x-ui DB quick_check正常、资源指纹不变、4个订阅alias。Gateway DB可读，但完整性检查失败，不能表述为数据库健康。
- 来源限制关闭，applyStatus=applied；本轮未改变SOCKS5H策略或v2rayN状态。
- 根分区52%，可用约4.2 GiB；本轮签名UI staging已由installer清理，没有遗留上传目录。
- check-slots新helper安装后删除了精确 `/tmp/aimili-check-slots-current-d5c8dcac` 上传副本，释放15,605,006字节；正式helper和唯一previous保留，可用于回滚。
- 唯一 `/usr/local/bin/aimili-gateway.previous` 保留。原 `/var/backups/aimili-gateway/20260905-external-ui` 约17.64 MB暂留，待后端回滚验收后才能退休。
- 当前UI：`2186f2e88858d1329a03c2210ae7d4c778476a75527aa543953b81114c5ba3fc`；previous为 `d4ee05c847064514e86898c3994b42a38341646a7421ca115b1246453b623b34`。发布目录严格保留两版，staging为空。
- `allowGatewayInstall=false`、fetcher marker缺失、网页updateEnabled默认false；没有待处理JSON请求，staging为空、无active journal，保留8条有界终态结果。

## 恢复执行顺序

1. 用户刷新Gateway页面后确认出口3已显示ready并复测对应公网节点；若以后候选再次耗尽，点击“重新检测并同步”应显示候选补充进度并自动重试。不要代替用户执行浏览器验收。
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
