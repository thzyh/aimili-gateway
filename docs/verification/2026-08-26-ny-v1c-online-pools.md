# `ny` 新 VPS V1-C 按需资源池验收记录

## 验收范围

- 目标为新建 Ubuntu 24.04、512 MiB、10 GB VPS，域名为 `ny.zouyunhui.cc.cd`。
- 生产容量固定为一个在线出口；全部安全候选可见，其余候选为 `standby`，按需切换。
- AimiliVPN、3x-ui/Xray、Aimili Gateway、Caddy 保持独立服务。
- 本记录不包含密码、Cookie、UUID、私钥、随机后台路径、候选原始 ID或完整连接地址。

## 固定版本与部署方式

- Gateway 功能提交：`3761ff0`，另含本次新机初始化脚本与部署合同修订。
- AimiliVPN 功能提交：`2da76ac`。
- AimiliVPN 未把本地提交伪装为 GitHub 下载地址；基础安装改为上传并验证本地 Git bundle。
- Gateway 与 Admin Linux 二进制在 VPS 上的 SHA-256 与本地构建产物一致。
- AimiliVPN VPS 工作树提交与本地固定提交一致。
- 新 VPS 创建并持久启用 1 GiB Swap；AimiliVPN 与 Gateway 均安装 systemd 内存、任务数和重启保护。

## 部署中发现并修复的问题

### 未发布提交下载 404

旧基础脚本从 `raw.githubusercontent.com` 下载一个仅存在于本地历史的 AimiliVPN 提交，首次部署稳定返回 HTTP 404。修复后 PowerShell 入口显式接收本地 bundle，上传后由远端执行 `git bundle verify` 并核对固定提交，再从 bundle 安装。部署合同先失败、后 6 项全部通过。

### 低内存串行扫描超过旧超时

AimiliVPN 已成功拉取 99 个官方候选，并以单 OpenVPN 进程逐个复验。旧 240 秒部署门槛在扫描仍正常进行时错误失败；服务当时约 26 MiB，存在活跃临时 OpenVPN 测试进程，并非卡死或 OOM。等待后得到 30 个有效节点。部署门槛已改为最多 1800 秒，仍以真实代理出口为提前完成条件。

### Gateway 配置父目录不可穿越

Gateway 首次启动日志明确报告读取 `/etc/aimili-gateway/config.json` 时权限被拒绝。文件本身为正确的 `0600` 且归 Gateway 用户所有，但父目录为 `root:root 0700`。最小修复为父目录 `root:aimili-gateway 0750`；配置仍为 `0600`，3x-ui 自动化凭据仍为 `root:root 0600` 并只通过 systemd credential 挂载。对应部署合同已先失败后通过。

## 服务器端端到端结果

- 四个独立服务均为 `active`，失败 systemd 单元为 0。
- AimiliVPN 管理端、控制 API、3x-ui 管理端与 Gateway 均只监听回环地址。
- HTTPS 根页面返回 200；Caddy 配置验证通过；UFW 启用并开放受管 VLESS 与 mixed 端口范围。
- AimiliVPN 本地代理出口与 VPS 直连出口不同。
- Gateway 公网登录成功，TOTP 默认关闭。
- 资源池显示 28 个安全候选：1 个 `ready`、27 个 `standby`。
- `standby` 行不包含在线端口、实际出口或连接材料。
- 完成至少一次真实 `standby → ready` 切换；切换后仍只有一个 `ready`。
- 当前在线组的公网 VLESS、SOCKS5H、代理端 DNS和目标出口一致性全部通过。
- 未授权 mixed 来源被拒绝。
- 四服务依次重启后，当前组仍为 `ready`，待机候选继续可见；重新检测、公网 VLESS 与公网 SOCKS5H再次通过。
- 3x-ui 中有两条 `agw-` 受管入站；其中一条 VLESS 入站且只有一个客户端。非受管入站仍存在。
- Gateway 账户管理命令连续执行两次均退出 0，固定瞬态 unit 名冲突未复发。

## 512 MiB 资源证据

- 验收时物理内存使用约 263 MiB，可用约 194 MiB。
- Swap 总量约 1023 MiB，使用约 119 MiB。
- AimiliVPN 常驻内存约 30.2 MiB、任务数 14。
- Gateway 常驻内存约 38.1 MiB、任务数 7。
- 最近 30 分钟内核日志没有 OOM kill 记录。
- 生产 `maxProxyGroups` 为 1，没有在 512 MiB 主机上扩展常驻出口。

## 浏览器与外部客户端边界

- 真实 Chromium 成功加载公网前端并跳转登录页；页面包含用户名、密码和登录按钮，不包含 TOTP 字段，并明确专家模式使用 3x-ui 独立账户。
- 唯一浏览器控制台错误是未登录状态读取会话得到预期 HTTP 401；没有静态资源错误。
- Windows 外部网络到当前 VLESS 与 mixed 公网端口的 TCP 连接均可建立，证明 DNS、公网监听、UFW 与云端端口边界可达。
- Windows 外部应用层探针未通过：mixed 来源与部署时记录的 SSH `/32` 不一致，持久新增另一来源被安全审批拒绝；VLESS 本机 `curl` 在 TLS 阶段返回 35，而服务器端同一公网入口已经两次完成真实 HTTPS 验证。外部 Windows 客户端因此不记为通过，也不据此推翻服务器端通过结论。

## 账户边界与后续操作

- Gateway 普通页面使用 Gateway 账户。
- 原版 3x-ui 专家模式仍使用 3x-ui 自身登录；即使测试阶段三套凭据值相同，也不是真正 SSO。
- 正式使用前执行 `sudo aimili-gateway-account` 设置自定义 Gateway 密码并按需登记 TOTP。
- AimiliVPN 与 3x-ui 的正式凭据需分别通过各自本地管理命令重置；不得把密码写入 shell 历史或聊天。
- 若需要从当前 Windows 公网来源使用 mixed，应在 Gateway 高级设置中添加准确的单地址 `/32`，不得使用 `0.0.0.0/0`；保存后切换一次待机候选以重建当前受管资源，再执行外部客户端复测。

## 2026-08-27 后续状态

本节只记录验收后的最新只读事实和已批准设计，不把尚未实现的功能记为通过：

- 最新只读 VPS 检查显示 AimiliVPN、3x-ui、Gateway、Caddy 均为 `active`，失败 systemd 单元为 0；可用内存约 186 MiB，Swap 已使用约 135 MiB。
- V1-C 的服务器端单在线出口验收结论继续有效，但本次没有重新执行完整 VLESS、SOCKS5H 和外部客户端测试，因此不生成新的协议通过结论。
- Windows 外部用户客户端应用层验收仍未完成。
- 512 MiB 生产仍保持一个在线出口；两个在线出口只作为后续单独的阶梯容量实验，尚未授权实施或验证。
- 高级设置、取消近期重新认证、SOCKS5H 来源开关、三账户统一和服务端自动代登录已经完成聊天设计确认，但尚未编码或部署。正式设计见 `docs/superpowers/specs/2026-08-27-advanced-settings-unified-credentials-design.md`。
- 本文前述“原后台独立登录”和 mixed 强制白名单是 2026-08-26 验收时的真实运行边界；只有 V1-D 完成最新端到端验收后才能更新为新行为。
