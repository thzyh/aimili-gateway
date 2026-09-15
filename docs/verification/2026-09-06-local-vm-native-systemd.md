# 本机 VMware Ubuntu 原生 systemd 开发审计记录

日期：2026-09-06（Asia/Shanghai）。状态：原生部署脚本已开发并通过静态/语法验证；真实业务安装等待 VM 上游 TCP 出网恢复。

## 当前目标

本机 VM 使用原生 systemd 运行 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy。当前部署清单声明主连接＋3 个出口、期望 4 个 OpenVPN、期望 1 个 Xray；这些是可调整的当前期望值，不是代码硬上限。

## 已完成开发

- 删除本机 Docker 单出口原型及其容器、镜像、卷、网络、代码、测试和专用文档；没有删除其他 Docker Desktop 资源。
- 增加原生部署清单与动态状态契约：服务 active/enabled、实际 OpenVPN/Xray/逻辑出口/槽位数均与清单比较。
- 增加只读 VM 出网预检，输出脱敏失败边界。
- 增加 staging、单组件备份、精确回滚和 allowlist 路径保护。
- 增加 AimiliVPN pinned installer wrapper、3x-ui/Caddy native installer、Gateway native installer、逐槽启用和动态验证脚本。
- Caddy 本机端口仅向当前 Windows 物理来源开放；不申请公网证书，不复用 ny 证书。
- 所有 apply 路径都先经过网络预检；没有通过公共 TCP/DNS/HTTPS 时不会安装业务。

## 最新验证证据

- `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`：通过。
- `network-preflight.tests.ps1`、`native-primitives.tests.ps1`、`aimilivpn-installer.tests.ps1`、`xui-caddy-installer.tests.ps1`、`gateway-installer.tests.ps1`、`native-verification.tests.ps1`：全部通过。
- 所有 `deploy/local-vm/native/*.sh` 已在 VM 内用 `bash -n` 验证通过。
- `status.ps1 -AsJson`：VM 存在、运行、SSH 可达；四项原生服务均 `inactive/not-found`；实际 OpenVPN/Xray/逻辑出口/槽位均为 0，`nativeReady=false`，符合尚未安装状态。
- 宿主直接 TCP 443 和 DNS 均通过；宿主 v2rayN PID、系统代理和默认路由安全快照未变。

## 当前阻塞

VM 内：局域网网关可达，UFW 出站允许；公共 TCP 443 被拒绝，DNS/HTTPS 尚未通过。宿主直连正常，因此第一失败边界是物理桥接后的上游准入/第二 MAC 路径，而不是宿主整体断网、VM 默认路由或 VM UFW 出站规则。

本轮没有修改 Windows 路由、DNS、防火墙、v2rayN 或 VM 网络模式，也没有切换 NAT；没有安装 AimiliVPN、x-ui、Gateway 或 Caddy，没有创建本机业务凭据和数据库。

## 本地 Git

本轮新增本地提交：

- `06d349a`：原生 systemd 设计；
- `f2b7b37`：逐文件实施计划；
- `d715d8c`：动态原生状态契约；
- `ba20a56`：VM 出网预检；
- `91acd35`：staging/备份/回滚原语；
- `d2182ae`：AimiliVPN 原生安装器；
- `9d9635d`：x-ui、Caddy、Gateway 安装器；
- `52fc036`：动态出口启用与验证；
- `15bdf93`：服务 enablement 与来源防火墙验证；
- `e4dcab5`：路径安全收紧。

Gateway 工作树仍保留交接前的未提交 VM 基础修改和未跟踪构建资产；本轮没有覆盖或清理它们。两个仓库均未推送 GitHub。

## 未执行

VM 上游网络恢复、四项 systemd 实际安装、主连接与三个出口阶梯启用、重启恢复、真实订阅验证、浏览器页面验收和 v2rayN 客户端验收均未执行。
