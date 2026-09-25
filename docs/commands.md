# VPS 常用命令与管理菜单

## 首次一键部署会问什么

在 Ubuntu 24.04、x86_64 的 SSH 终端执行：

```bash
curl -fsSL https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/install.sh | sudo bash
```

1. **下载并验证发布包。** 引导脚本下载发布清单、签名、Gateway 完整包及定制 3x-ui 二进制，验证 Ed25519 签名和 SHA-256；这一阶段不会修改服务。
2. **选择部署模式。** `1 无域名` 是默认值，回车即可使用 VPS 公网 IP 和 Caddy 本地 CA。`2 使用域名` 会继续询问域名；域名的 A 记录必须已经指向本机公网 IPv4，且 80、443 端口可从公网访问，Caddy 才能申请公网证书。域名无需填写 `https://`。
3. **选择 SOCKS5H 允许来源。** 这里填写的是**连接 SOCKS5H 的客户端来源 IPv4**，不是 VPS IP、域名或出口节点 IP。安装器会先尝试读取当前 SSH 客户端地址，并在方括号中显示；直接回车即采用该地址。若无法识别，方括号显示 `127.0.0.1`，回车表示仅允许 VPS 本机连接；需要其他设备使用时，应输入该设备对 VPS 可见的公网 IPv4。已有部署重新运行时沿用已保存的允许来源，不重复询问。来源限制保持开启，安装器不会因回车而放开所有公网地址。
4. **自动安装与验收。** 安装器检查平台与 DNS，备份已有状态，安装系统依赖、3x-ui/Xray、AimiliVPN/OpenVPN、Caddy 和 Gateway，设置受管防火墙、账户及更新入口；最后验收主连接、出口位、订阅、SOCKS5H 规则和数据库。普通出口位默认 `4`，交互部署不另设槽位选择；已有部署的槽位数量保持不变。完整日志在 `/var/log/aimili-gateway/install.log`。
5. **完成后。** 屏幕显示访问地址；初始账户文件位于 `/root/aimili-gateway/credentials.json`，只在自己的 SSH 会话中查看，不要粘贴到聊天或公开日志。无域名模式的本地 CA 不会自动受浏览器信任，后续可用管理菜单切换域名。

非交互部署需明确提供模式和 SOCKS5H 客户端来源，例如：

```bash
curl -fsSL https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/install.sh | sudo bash -s -- --domain fl.zouyunhui.cc.cd --slots 4 --allowed-source 你的客户端公网IPv4
```

把最后一个占位符替换为真实 IPv4；不使用域名时把 `--domain ...` 换成 `--no-domain`。如安装中断，先查看安装日志和状态，再用同一目标参数续作。

## 安装后的统一管理

安装或网页更新到包含管理入口的版本后，在 VPS 的 SSH 终端运行一条命令：

```bash
aimili
```

菜单使用中文，输入数字选择操作，`0` 退出。普通状态和版本查看无需预先切换 root；账户、域名切换、重启及受限日志在需要时调用 `sudo`。已经处于 root 会话时直接运行即可。

| 菜单 | 功能 | 实际操作与边界 |
| --- | --- | --- |
| 1 | 部署状态与访问地址 | 读取本机安装状态和四项服务状态，不下载发布包，不修改服务。 |
| 2 | 当前版本 | 读取 Gateway 二进制的版本与提交。 |
| 3 | 服务状态 | 查看 Gateway、AimiliVPN、3x-ui、Caddy 的 systemd 状态。 |
| 4 | 查看日志 | 选择安装、Gateway、AimiliVPN、3x-ui、Caddy 或项目更新日志，显示最近 100 行。 |
| 5 | 管理账户与密码 | 打开原有 `aimili-gateway-account` 中文菜单；可修改统一账户、重置密码、管理 TOTP、撤销会话。 |
| 6 | 更换域名 | 输入已解析到本机的域名，输入“确认”后运行签名安装器。会备份数据并短暂重启部分服务。 |
| 7 | 重启指定服务 | 选择服务并输入“重启”后执行；只检查服务恢复为 active，业务连接仍需网页验证。 |
| 8 | 项目更新说明 | 提示使用网页“高级设置 → 检测更新”确认新版本、观察升级进度。 |

## 更换域名

1. 先把域名的 DNS A 记录指向 VPS 的公网 IPv4，并确保公网可访问 80、443 端口。可用 `curl -4fsS https://api.ipify.org` 和 `getent ahostsv4 你的域名` 核对。
2. 运行 `aimili`，选择 `6`，输入域名，再输入“确认”。菜单先读取当前 Gateway 版本，再下载当前安装入口；两个版本不一致时立即停止，避免用旧安装包覆盖已经网页升级的程序。此时按菜单提示先核对网页更新或项目的[升级路径](upgrade.md)，待安装入口与本机版本一致后重试。
3. 安装器验证 DNS，备份现有配置和数据库，切换 Caddy 与 Gateway 公共地址、证书及订阅入口，并验收运行状态。安装日志为 `/var/log/aimili-gateway/install.log`；备份位于 `/var/backups/aimili-gateway/vps-installer`。切换失败不保证自动回滚，应先查看日志和当前状态，不要连续尝试不同部署模式。
4. 成功后选菜单 `1` 核对新地址，并在浏览器登录新域名。使用旧 IP 地址的客户端应刷新或重新导入新域名的订阅。

域名切换保留已有 Gateway/3x-ui 数据库、账户、UUID、Reality 材料和出口位。安装器会重写它管理的 Gateway 配置和 Caddyfile；手工改过这些文件时，应先备份并确认影响。

## 安装器与网页更新的区别

安装器用于首次部署、明确的无域名或域名模式切换。部署成功后再次运行原始 `curl … | sudo bash` 命令仍会进入安装流程：默认选择 `1 无域名`；如果当前已经使用域名，直接按回车会切回 IP 模式。每次重新执行还会创建备份、检查系统包、执行防火墙和资源验收、重写受管配置，并重启 3x-ui、Caddy、Gateway。它不是只读检查，也不是日常升级入口。

日常新功能更新应在网页“高级设置 → 检测更新”中确认。若安装入口固定的版本落后于网页已安装版本，重新运行安装器可能造成程序降级与数据库不兼容；菜单的域名切换操作会拦截这种版本不一致情况。

## 可用于脚本的只读命令

```bash
aimili status
aimili status --json
aimili version
aimili version --json
```

`status --json` 只读取本地安装状态与服务状态，输出 `state`、`phase`、`origin`、`slots` 和四项服务布尔值；它不下载发布包。无参数 `aimili` 才进入交互菜单。账户管理的原入口 `sudo aimili-gateway-account` 仍保留。
