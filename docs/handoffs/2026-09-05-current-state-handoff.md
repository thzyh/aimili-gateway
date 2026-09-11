# Aimili Gateway 当前状态交接

日期：2026-09-11（Asia/Shanghai）。状态：当前权威入口。

## 2026-09-11 ny 出口隔离与统一仓库

### 14:58 最终浏览器验收（取代本节较早快照）

- ny Gateway 当前功能版本为 `v1.0.6`。人工替换后端修复提交为 `97fb97689104117a85ff589a7bdb6207345b98c1`；安全诊断日志提交为 `90347f2d6827f21f32e8dda8a8f981998f6ffebd`。诊断日志只记录操作阶段、逻辑出口 ID、槽位和安全错误码，不记录用户名或密码。
- Codex 已接管用户现有的已登录 Edge 标签 `https://ny.zouyunhui.cc.cd/socks5h` 完成真实页面验收。此前“卡在登录”不是账号或网站故障，而是误操作了另一个空白浏览器标签；现已直接复用保存凭据的正确标签。
- 故障出口没有隐藏：当前主连接和出口位 2 的故障行均保留，并同时提供“复核状态”和“人工更换”。出口位 1 的人工替换弹窗、固定槽位、候选选择、执行中反馈和失败回滚均已真实走通；所选美国候选未通过最终出口检查后，系统恢复原俄罗斯节点，出口位 1 当前为 `ready`。
- SOCKS5H 页面存在“随机更换用户名和密码”。首次验收时 3x-ui 已完成四组新账号写入，但写入后的真实代理验证失败，随后完整回滚；当时旧版本未记录具体出口和错误码，因此不能把失败原因猜成某一种网络或认证错误。增加安全阶段日志并仅重启 Gateway 后，同一浏览器操作真实成功，页面提示旧地址已失效。
- 成功后数据库中 `mixed-username` 和 `mixed-password` 的密文摘要、`updated_at` 均已变化，时间戳为 `1789109248047`，`PRAGMA integrity_check=ok`。这证明不是只有前端提示变化，新的凭据已持久保存。轮换事务自身对两个健康出口完成 SOCKS5H、代理 DNS 和预期出口 IP 验证；故障出口保留新设置但不参与健康验证。
- 最新服务门禁：`aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 均为 active/running，四项 `NRestarts=0`。只按需重启了 Gateway；AimiliVPN、x-ui 和 Caddy 的 PID 未变化。唯一 `/usr/local/bin/aimili-gateway.previous` 继续保留，没有新建第二个永久备份。
- 本地最新验证：前端 66/66、生产构建、`go test ./... -race -count=1`、`go vet -buildvcs=false ./...`、Windows/Linux 的 Gateway 和 admin 构建均通过。没有操作其他 VPS，没有删除 `aimili-vpngate`，没有推送远程。

- ny 当前 Gateway 已部署 `v1.0.4`，源码提交为 `e3690f2dcee7cae264650b0341bd3b8c557c0b08`；签名前端版本仍为 `70f70194bdc0cb1cfd9932df46da0678956af42660ac88ae7dbcf611c6b4805c`。出口引擎已正式从 `/opt/aimili-gateway/services/aimili-egress` 启动，持久数据继续使用 `/opt/aimilivpn/vpngate_data`，实现“一个仓库、两个独立服务”的生产布局。
- 出口检测现会先区分“VPN 隧道/真实出口失效”和“本地代理或路由异常”。前者在同一个检测请求内领取本次故障唯一一次同国替换机会；后者只报告本地链路故障，不更换 IP。前端故障按钮显示“检测并自动修复”，等待期间说明一次性规则，并分别显示修复成功、无同国候选、替换失败和此前已尝试。
- 2026-09-11 06:50（Asia/Shanghai）纠正了出口 1 属于旧受控测试的修复记录，并通过 Control API 执行当前故障的一次真实检测与修复。响应明确为 `auto_repair_performed=true`，结果为 `no_same_country_candidate`；出口 1 保留为 `disconnected/manual_required`，没有消失。随后重启 `aimilivpn.service` 并跨过后台检查周期，`attempt_count` 仍为 1、`attempted_at` 完全不变，新增自动修复日志为 0。
- `v1.0.4` 增加被动状态同步：Gateway 读取 AimiliVPN 已落盘的 `manual_required` 终态，不再为同步状态调用一次可能触发修复的检查。部署后出口 1 的 Gateway 错误已从旧的 `egress_check_failed` 收敛为真实的 `no_same_country_candidate`；同步前后 AimiliVPN 的 `attempt_count=1`、`attempted_at=1789080656.334448` 完全不变。
- 最新现状（2026-09-11 07:25，Asia/Shanghai）：出口 2、出口 3 健康；出口 1 等待人工选择新 IP。主连接在 07:13–07:15 独立发生两次真实出网失败，唯一一次同国替换候选又明确 `candidate_dial_failed`，因此主连接也保留为 `manual_required/replacement_failed` 等待人工处理。日志和运行时均确认 `tun0` 不存在、7928 无法出网，而 `tun121/tun122` 与出口 2、3 正常，证明故障相互隔离。四服务均 active，Gateway/AimiliVPN 的 `NRestarts=0`，Gateway 数据库 `integrity_check=ok`。
- 本次部署未新增永久备份，继续只使用既有联合备份 `/var/backups/aimili-gateway/egress-isolation-20260910-bed0cbf-ed102e3`。第一次正式写入因服务器 UI 安装器参数版本不匹配而触发自动回滚，核对 Gateway、unit 和服务均恢复后，改用当前受限 spool 安装入口完成签名前端发布；临时回滚目录已在成功后删除。
- Codex 内置浏览器因本机 Codex 授权令牌不可用，未完成真实鼠标点击验收；已验证生产发布的 JS 包含全部新按钮和进度/结果文案，Control API 返回真实一次修复结果，前端 64 项测试覆盖等待与结果展示。用户刷新页面后可完成最终视觉验收。
- ny 已部署 Gateway `bed0cbfb7fcfaf6d74db66b9bcfefcde659097b5` 与出口引擎 `ed102e3` 对应修复。主连接和三个普通出口分别检测、分别记录故障；`operation_busy`、`maintenance_busy` 和检测超时不再被写成节点损坏。
- 受控断开单一出口后，只有该出口进入 `manual_required/disconnected`，其他三个出口继续健康；系统只自动尝试一次同国候选。重启 `aimilivpn.service` 后尝试次数仍为 1、修复记录未变化、没有再次换节点，故障行也未消失。
- 人工替换失败的候选明确返回 `AUTH_FAILED`，没有被标成成功；完成有效替换后，主连接加三个出口均恢复。最终四服务 active、四条 TUN 存在、Gateway 有四条 ready 记录、订阅含四个入站，四个真实代理出口 IP 互不重复。
- 在 `20cc122` 增量部署前，Gateway 的“重新检测”只检测、不执行换 IP；该历史行为已由上方“检测并最多自动修复一次”规则取代。
- ny 本轮只新增一个联合备份：`/var/backups/aimili-gateway/egress-isolation-20260910-bed0cbf-ed102e3`。Gateway 数据库备份在生成前修复了两个损坏索引，`PRAGMA integrity_check` 为 `ok`，各业务表行数与内容摘要未变化。
- 本地 `aimili-gateway` 的现有 `feat/main-switch-protocol-modes` 分支已引入 `services/aimili-egress`，以后它是出口引擎唯一源码来源。运行时仍是 `aimili-gateway.service` 与 `aimilivpn.service` 两个进程，不把高权限网络操作放入 Gateway。
- Gateway 前端不再显示 AimiliVPN 设置页和原后台入口；高级设置只保留 3x-ui 设置/专家模式。Gateway 内部的出口管理 API 保留。
- 原 `aimili-vpngate` 仓库保留。本地 `custom` 已通过 `--ff-only` 快进到 `ed102e3`，与现有功能分支收敛到同一提交；没有创建新分支或改写历史。快进后重新执行 204 项测试，全部通过。

详细验证见 `docs/verification/2026-09-11-egress-isolation-and-repository-integration.md`。

## 2026-09-10 开机启动入口与排障经验固化

- 追加交互改进：菜单、进度、确认、成功、失败和下一步提示全部改为中文；脚本使用 UTF-8 BOM，并已由 Windows PowerShell 5.1 实际解析执行。启动完成后显式启动 `E:\SoftWare\Vmware16\vmware-tray.exe`；本轮从“VM 正常运行但托盘进程不存在”恢复为 Session 1 中单一托盘进程，状态入口返回 `trayRunning=true`，没有打开 Workstation 主窗口。
- 菜单新增安全关闭：二次确认后对目标 VM 执行 soft stop；只有 `vmrun list` 确认没有其他 VM 时才停止托盘及 NAT、DHCP、Authorization 三项共享服务，存在其他 VM 时保留共享服务。持久 VMnet8 `/32` 不删除，后续启动继续复用。
- 新增个人 Skill `interactive-local-scripts`：只针对人工运行且存在多个合理动作的本地脚本，优先考虑无参数中文交互菜单，同时保留 `-Action`/JSON 等非交互入口；明确排除 CI、库函数、无人值守自动化和真正的单用途脚本，因此不修改或冲突于其他全局设置。
- 新增 `deploy/local-vm/start-local-vm.ps1`。右键“使用 PowerShell 运行”或无参数执行会进入交互菜单；可选择完整启动、只读状态、单独修复路由或安全关闭。完整启动会请求 UAC，随后幂等启动 `VMAuthdService`、`VMnetDHCP`、`VMware NAT Service`，恢复 VMnet8 持久 `/32` 路由，只在 VM 未运行时执行 `vmrun start ... nogui`，最后等待 SSH 与 `nativeReady=true` 并确保托盘运行。它不操作 v2rayN、TUN、系统代理、DNS、防火墙或默认路由。
- 首版自提升运行只返回 `local_vm_elevated_start_failed:1`。实时复核确认活动和持久 VMnet8 `/32` 均存在，但 `Find-NetRoute` 在透明 TUN 工作时返回 sing-box `/1`，旧门禁因此误报路由未选择并使管理员子进程退出 1。门禁现按全部同目标 `/32` 的 route metric＋interface metric 选择最佳主机路由：继续覆盖 `qinshi /32` 冲突，但不把透明 TUN `/1` 当成同类竞争路由。
- 自提升结果现原子写入 `%LOCALAPPDATA%\AimiliGateway\vmware-local\startup-last-result.json`；父窗口会回显失败阶段、原始错误和诊断路径，不再丢失管理员子进程错误。
- `-ValidateOnly -AsJson` 已在当前运行环境真实通过：三项 VMware 服务 Running，持久路由选择 VMnet8，SSH 可达，VM 内实际为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位，`nativeReady=true`。
- 本轮使用 VM 真实关机状态验证了 `vmrun start ... nogui`，VM 从 0 台运行恢复为 1 台；SSH 随后恢复，约数分钟后 6 个 OpenVPN、1 个 Xray、6 个逻辑出口和 5 个出口位全部达到 `nativeReady=true`。无参数菜单通过重定向输入选择退出的冒烟测试，返回码为 0；管理员结果文件链也已验证成功写入、读取和清理。
- Windows 重启后持久路由原则上仍在，但 VM 本身和两个 Manual VMware 服务不能仅靠该事实推断已启动；统一入口会检查并按需恢复全部启动前提，因此无需再手动逐条执行 `Start-Service`、路由和 `vmrun` 命令。
- 重复失败的核心教训已写入个人 Skill `trace-client-data-path`，仅用于代理、VPN、订阅、虚拟机和路由的客户端数据流故障：以故障时刻真实客户端配置/日志为起点，用同配置隔离 A/B 找第一失败边界，并强制保护现有代理基线；没有修改其他全局设置。

## 2026-09-09 23:40 `local` 节点 `-1` 根因修复与隔离闭环（当前状态）

用户在 v2rayN 激活 `local` 后，无论“开启 TUN＋清除系统代理”还是“关闭 TUN＋自动配置系统代理”，节点测试均为 `-1`。第一失败边界已通过故障时段日志和同配置 A/B 测试确认并修复；本轮没有切换用户节点、TUN、系统代理或默认路由，也没有使用 Computer Use。

已确认的第一失败边界不是订阅、Reality、Caddy 或 VM 数据面，而是 Windows 到来宾的接口选路：

- `configTest*.json` 的 6 个 local 出站目标为 `192.168.88.4`，21:27–21:30 的真实配置均未设置 `sendThrough`；当前主 ny 配置仍连接公网地址。
- `Find-NetRoute 192.168.88.4` 在故障窗口命中动态 `192.168.88.4/32 → qinshi`（接口 53、metric 1），覆盖 VMnet8 的 `192.168.88.0/24`；故障时连 SSH 22、Gateway 443/8443 也同时超时。
- 同一份 6 节点 local 配置启动隔离 Xray：未绑定源接口时 6/6 超时；仅把出站源地址绑定为 VMnet8 的 `192.168.88.1` 后 6/6 返回 HTTP 204。该 A/B 复现未触碰用户 v2rayN。
- `qinshi` 仍显示为 RAS 已连接，并会在 local 连接尝试时重新注入该 `/32`；因此“偶尔看到 VMnet8 选路”不是稳定修复。用户以管理员身份执行修复后，当前活动路由和持久路由均为 `192.168.88.4/32 → VMware Network Adapter VMnet8`，源地址为 `192.168.88.1`，VMnet8 IPv4 metric 为 5。
- VM 同期日志显示 6 个受管 OpenVPN、主连接、5 个出口位的 TUN/路由/监听/真实出口均通过；修订后的门禁精确排除 5 个临时候选探测进程，当前 VM 验收为 `nativeReady=true`。

最小修复实现位于本地提交 `10a54fb`，验证说明位于 `3f5f241`；本节更新时二者尚未推送：

- 新增 `deploy/local-vm/repair-host-route.ps1`，只校验 `state.json` 的来宾地址和 `allowedSource` 对应的 VMware 适配器；`-Apply` 仅将来宾 `/32` 持久绑定到 VMnet8，并把该适配器 IPv4 metric 设为 5，不断开 `qinshi`，不修改 v2rayN、系统代理、DNS、防火墙或默认路由。
- `verify-native.sh` 改为按 `tun0` 和 `AIMILI_SLOT` 计数受管 OpenVPN，避免节点池候选探测导致健康门禁误报。
- 新增主机路由合同测试；VM 内 `native-runtime-fixture.tests.sh` 与 `native-verification.tests.ps1` 通过。
- 恢复检查另发现 `status.ps1` 将 Base64 通过 PowerShell 原生命令 stdin 发送时附加 CRLF，GNU `base64` 在解码完正文后报 `invalid input`。现改为将只含安全字符的 Base64 作为 SSH 参数传入，并显式绑定 `192.168.88.1`；Windows PowerShell 5.1 与 pwsh 7.6 均已得到完整 `nativeReady=true` JSON。

最新真实验证：

- VM 内深度门禁为 `nativeReady=true`：主连接和出口位 0–4 的 TUN、策略路由、监听与真实出口全部为 true；实际数量为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。
- 23:31 的 Windows 外部门禁在用户当前 TUN 存在、系统代理关闭的环境中，以独立临时 Xray 对 6/6 订阅节点完成真实公网协议、mixed/SOCKS5H、代理 DNS、授权和预期出口 IP 验证；4 个 VLESS、2 个 Hysteria2 全部通过。
- 进程级 HTTP 代理隔离测试为每个节点启动独立临时 HTTP 入站，模拟“自动配置系统代理”的应用流量，6/6 均取得对应预期出口。测试前后 v2rayN PID、TUN 接口、系统代理和默认路由摘要一致，临时配置和测试 Xray 均已清理。

服务器和隔离客户端闭环已经完成。唯一未由助手执行的是用户现有 v2rayN 中选择 `local` 后的交互体验验收；这是按用户要求保留的最终步骤，不能把它误写成已由自动化切换验证。

## 2026-09-09 17:40 本机 VMware 故障闭环

主连接、出口 2 和 v2rayN `local` 节点测速 `-1` 的共同根因已经定位并修复。故障时来宾 `ens192` 连续 Link Down/Up，VMware 同期记录 Link State Propagation，六条 OpenVPN 随之重置；VMX 现持久设置 `ethernet0.linkStatePropagation.enable = "FALSE"`，本次启动后的内核 Link Down 计数为 0。AimiliVPN 原先固定的 `--connect-retry-max 1` 已改为可配置 `OPENVPN_CONNECT_RETRY_MAX`、默认 3，VM 运行进程已经使用新值。

本机与 ny VPS 的关键差异是 VMware NAT。来宾连接的宿主承载进程为 `vmnat.exe`；v2rayN TUN 若把它再次送入 `local` 会产生递归依赖，而 ny VPS 不经过该层。v2rayN 三个路由模式现均在首条保存唯一的 `vmnat.exe → direct` 规则，修改前 SQLite 备份位于 `%LOCALAPPDATA%\AimiliGateway\vmware-local\backups\v2rayN-guiNDB-before-vmnat-direct.db`。没有激活 `local`，没有切换 TUN/系统代理，也没有使用 Computer Use。

故障槽位已通过受管 Control API 在原逻辑槽位恢复；出口 2 原 `JP + residential` 约束当时没有当前有效候选，改用存在的 `JP + datacenter` 候选并通过真实出口检查，假活候选也已 rotate。最新 VM 内门禁为 `nativeReady=true`：四服务 active/enabled、6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位，主连接与五个出口位的 TUN、策略路由、监听、真实出口全部通过。17:39 的 Windows 外部门禁为 `status=pass`、6/6 组验证完成、出口 IP 全部唯一、4 个 VLESS 与 2 个 Hysteria2 公网握手通过。外部门禁逐组调用与页面“重新检测并同步”相同的 `/check` 接口，因此按钮背后的服务端操作已真实验证。来源限制当前为关闭，本轮没有改变该开关；较早记录中的“最终恢复开启”仅代表当时快照。

当前只剩用户客户端体验验收：正常重启一次 v2rayN，使持久路由规则进入当前进程；更新 `local` 后由用户自行激活节点，分别测试“开启 TUN＋清除系统代理”和“关闭 TUN＋自动配置系统代理”。

## 2026-09-09 15:00 故障快照（已由 17:40 状态取代）

本轮复测期间 VM 发生一次真实的 OpenVPN 节点重置/超时，SSH 也短暂失联；执行一次有边界的 `vmrun stop soft`/`start nogui` 后来宾恢复。最新只读状态显示四服务 active/enabled，主连接和出口位 0、2、3 就绪，出口位 1（JP）与 4（KR）因当前没有可用住宅候选而 pending；最近一次门禁为 5 个 OpenVPN、4 个逻辑出口、3 个就绪出口位。Windows 外部验证的明确失败边界是 `subscription_coverage_mismatch`：旧订阅仍有 6 条，而当前只有 4 条可用数据面；没有激活 v2rayN `local`，没有切换 TUN/系统代理。待候选恢复后需刷新订阅再做用户验收。

本轮没有把 Xray `publicKey` 改写为 `password` 作为修复：同一配置的 A/B 结果受 VM 数据面状态影响，不能据此认定字段名是根因。Gateway 主连接持久记录兜底修复已部署，重连期间主连接保留为 degraded，不再消失；相关 Go 回归测试已通过。

## 2026-09-09 节点池与来源限制追加修复

本机 VMware 的 Gateway 已部署兼容修复：旧版 AimiliVPN 国家目录缺少汇总字段时，Gateway 从当前有效候选快照补齐统计；实时 UI 数据为官方 99、当前有效 40，国家数随当前候选刷新变化且不再显示 0，最终复核为 3 国。统一账户变更导致 mixed 入站保留旧代理账号时，Gateway 在严格确认 `agw-` 所有权后自动同步账号，并在后续 Xray 更新失败时恢复原配置。来源限制已完成关闭、开启、再次关闭和最终恢复开启的真实往返验证，最终为 `enabled=true`、`applyStatus=applied`、仅允许 `192.168.88.1/32`；3x-ui 受管资源 `ownershipMatches=true`。

AimiliVPN 重启后五个出口位重新选择了可用候选，已再次 provision 并同步 Gateway、3x-ui/Xray 和六条订阅。VM 内门禁最新为 `nativeReady=true`，四服务 active/enabled，实际数量仍为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位。服务端用 Caddy 本地根 CA 严格请求订阅为 HTTPS 200、`text/plain`、6 条可解析 `vless`/`hysteria2` 节点。

AimiliVPN 已增加 OpenVPN/TUN 重连后的策略路由自愈。在 VM 上清空出口位 5 的表 204 后，守护线程于 20 秒内自动恢复 `tun124` 的默认路由与选表规则，没有重启 OpenVPN 或替换节点；重新 provision 后完整门禁仍为 `nativeReady=true`。最新 AimiliVPN 全量测试 71 项通过，实现提交为 `12d0588`。

v2rayN `2026-09-09 09:41`、`09:49` 与 `12:40` 日志的第一失败边界均为 TLS `PartialChain`，尚未进入订阅解析；`local` 保存 URL 与 Gateway 当前 URL 的 SHA-256 完全一致。已将固定 SSH 身份取回并核对指纹的 Caddy 根 CA 导入 `Cert:\CurrentUser\Root`，没有修改 `LocalMachine\Root`。Windows 系统信任直连订阅随后为 HTTP 200；v2rayN 7.24.4 自身成功更新 `local` 为 6 个节点，13:04 后日志无 TLS 或解析错误，临时自动更新间隔已恢复为 0。本轮未使用 Computer Use。

## 2026-09-09 本机 VMware 五出口真实部署闭环

本机 VMware Ubuntu 部署已完成自动闭环，ny VPS 本轮未连接、未读取、未修改。VMX 为 `D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`，当前 VMnet8 地址为 `192.168.88.4`；Windows 用户入口是 `https://192.168.88.4:8080`，不是 `127.0.0.1`。Caddy 本地根 CA 严格请求返回 HTTP 200。

最终门禁通过：四服务 active/enabled；manifest 期望与实际均为 6 个 OpenVPN、1 个 Xray、6 个逻辑出口、5 个普通出口位；`nativeReady=true`。数量来自 manifest，不是永久上限。Windows 外部验证为 `status=pass`、6/6 mixed/SOCKS5H 与代理 DNS 通过、6/6 公网协议通过、6 个唯一出口与来源认证通过、6 条订阅覆盖完整、单一 Xray。Hysteria2 保持严格证书验证，没有使用 `allowInsecure=true`。宿主默认路由、DNS、防火墙、系统代理及本机级根证书库未修改；后续仅新增当前用户 Caddy 根 CA 信任以修复 v2rayN `PartialChain`。

本轮新增修复包括：Gateway 单槽位检测使用 75 秒操作超时；外部验证读取当前统一账户而不是 bootstrap 账户；动态订阅按 1–6 排序；出口位在进程重启后优先恢复仍可用的上次节点，失败候选进入冷却；主连接与出口位 1 的重复出口已通过仅轮换该槽位消除。SOCKS5H 来源限制为 `enabled/applied`；3x-ui 和 AimiliVPN 原后台检测为 HTTP 200，自动登录返回同源 HTTP 303；账户管理显示三服务已同步。

磁盘已删除 35 个旧备份和 20 个 staging，只保留 `/var/backups/aimili-local/final-20260909-closed-loop`；根分区由 49% 降至 23%，约 18 GB 可用。最新脱敏外部证据采集于 `2026-09-08T18:59:49Z`，完整证据和阻塞处理见 `docs/verification/2026-09-07-local-vm-real-deployment.md`。

本轮最新测试：AimiliVPN 71 项、前端 66 项、Gateway Python 108 项（2 项按平台能力跳过）、Go 全包与 race、vet、所有实际存在的本机 VM PowerShell/Bash/Python 测试均通过。过时总入口仍引用未跟踪且不存在的 `create-vm.ps1`，不作为当前通过项。复杂度审查结论为 `Lean already. Ship.`。最新实现提交包括 Gateway `bcf5e99` 与 AimiliVPN `12d0588`；两个功能分支均已普通推送并在推送后复核远端跟踪提交一致。

v2rayN 自身的 `local` 订阅更新已经通过，6 个节点已写入客户端数据库；用户剩余步骤是选择节点后的日常使用体验验收。浏览器若仍显示旧的“不安全”状态，应刷新或重新建立 HTTPS 连接以使用新增的当前用户根 CA 信任。本轮未使用 Computer Use。

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
