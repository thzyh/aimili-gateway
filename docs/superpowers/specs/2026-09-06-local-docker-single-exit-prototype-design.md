# 本机 Docker 单出口隔离原型设计

日期：2026-09-06（Asia/Shanghai）。状态：已批准实施。

## 目标

交付一个可在 Windows Docker Desktop 上独立启动的 AimiliVPN 单出口原型，使用真实 VPNGate 节点建立 OpenVPN 隧道，并通过本机回环 HTTP/SOCKS 代理验证真实出口。该原型是“本机 Docker 完整独立部署，同时保留 Linux VPS 原生部署”的第一阶段数据面切片。

本阶段不接入 Gateway、3x-ui/Xray 和四出口协议事务。只有单出口容器隔离、真实拨号和本机回环代理验证通过后，下一阶段才把 x-ui/Xray 与 Gateway 加入同一个专用 Linux 网络命名空间。

## 安全边界

- 不读取、复制或挂载 v2rayN 配置、日志、进程目录和订阅数据。
- 不连接 `ssh ny`，不读取或复制 ny 的数据库、配置、证书、令牌和生产密钥。
- 不使用 Docker `host` 网络，不使用 `privileged`，不挂载 Docker socket。
- 仅 AimiliVPN 容器获得 `NET_ADMIN` 和 `/dev/net/tun`；宿主 Windows 不创建 TUN，不修改宿主路由或 DNS。
- 只把容器端口发布到 Windows 回环：代理 `127.0.0.1:17928`，管理页 `127.0.0.1:18787`。
- 运行数据、UI 凭据和控制令牌只保存在原型专用 Docker volume；不复用任何已有目录或 volume。
- 默认 `MULTI_EXIT_SLOTS=0`，只允许一条主 OpenVPN 隧道。
- 容器根文件系统只读，运行时只允许写专用数据 volume、`/tmp` 与 `/run`；日志启用大小和文件数上限。

## 方案选择

### 采用：独立业务容器和专用 bridge

本阶段只运行 AimiliVPN，因此使用一个专用 bridge 网络。AimiliVPN 在自己的 Linux 网络命名空间内创建 `tun0` 和策略路由；Windows 只通过 Docker 的回环端口映射访问代理及管理页。

下一阶段加入 Gateway 和 x-ui/Xray 时，三个业务容器共享一个专用 Linux 网络命名空间，从而继续使用当前代码要求的 `127.0.0.1` 接口，同时让每个进程保留独立容器和最小权限。

### 未采用：修改服务允许容器 DNS 名称

这会修改 Gateway 和 AimiliVPN 当前的回环安全约束，扩大 Linux VPS 原生部署的回归面，不适合作为第一阶段原型。

### 未采用：全部进程放入特权容器

该方式权限过大、故障隔离弱，也无法证明最终多容器边界可行。

## 组件与文件

原型实现位于 AimiliVPN 工作树的 `deploy/docker-single-exit/`：

- `Dockerfile`：以固定 Python slim 基础镜像构建运行镜像，只安装 OpenVPN、路由及最小诊断工具。
- `entrypoint.py`：在专用 volume 中幂等生成本地 UI 凭据和控制令牌，随后启动现有 `vpngate_manager.py`；不输出秘密。
- `compose.yaml`：定义最小权限、只读根文件系统、资源限制、专用网络、专用 volume 和回环端口映射。
- `start.ps1`：检查端口冲突和 Docker 状态，记录 v2rayN/系统代理/默认路由基线，再构建并启动原型。
- `verify.ps1`：不改系统设置，轮询真实 OpenVPN 出口，经 `127.0.0.1:17928` 发起显式代理请求，并比较启动前后的宿主安全基线。
- `show-access.ps1`：仅在用户主动运行时，从容器读取本地管理页 URL 和测试账号；不会被自动验证调用。
- `stop.ps1`：停止原型但默认保留专用数据；显式 `-PurgeData` 才删除原型 volume。
- `README.md`：说明启动、查看本地凭据、用户验收与安全清理方法。

## 数据流

```text
用户显式测试请求
  -> Windows 127.0.0.1:17928
  -> Docker 回环端口映射
  -> AimiliVPN 容器代理 0.0.0.0:7928
  -> 容器 tun0
  -> 真实 VPNGate 节点
  -> 公网
```

管理页流量只通过 `127.0.0.1:18787` 进入容器 `8787`。容器内部绑定 `0.0.0.0` 是为了接收 Docker DNAT 流量；由于端口只发布到 Windows 回环且容器位于专用 bridge，该绑定不构成局域网或公网监听。

## 凭据与运行数据

首次启动时 `entrypoint.py` 在专用 volume 中生成：

- 随机 UI 用户名；
- 至少 24 字节随机 UI 密码；
- 随机管理路径；
- 独立控制 API token。

已存在的有效文件不会被覆盖，因此重启不会改变用户入口。启动与验证脚本不得输出这些值。用户需要验收管理页时，主动执行 `show-access.ps1`；秘密只显示在用户自己的终端。

## 启动、失败和恢复

`start.ps1` 在启动前拒绝以下情况：Docker 不可用、回环端口已占用、项目路径不完整。启动失败时保留容器日志和专用 volume，便于诊断，但不修改系统代理和 v2rayN。

`verify.ps1` 最长等待真实出口建立，不把 VPNGate 候选暂时失败误报为宿主网络损坏。验证仅报告稳定状态和脱敏结果，不输出候选 ID、VPN 节点地址、出口 IP、密码或 token。

停止操作默认仅执行 Compose down 并保留数据。只有用户明确执行 `stop.ps1 -PurgeData` 时才删除准确命名的原型 volume；脚本必须先解析 Compose 项目名并拒绝宽泛删除。

## 测试与验收

自动测试采用 TDD，覆盖：

1. entrypoint 首次生成、幂等重启、权限和不输出秘密；
2. Compose 不含 `privileged`、`network_mode: host`、Docker socket和宽泛宿主挂载；
3. 仅发布两个 Windows 回环端口，且只授予 `NET_ADMIN` 与 `/dev/net/tun`；
4. 启停脚本不会调用修改系统代理、Windows 路由、v2rayN 或 SSH 的命令；
5. 镜像构建、Compose 配置解析、容器健康、单 OpenVPN 进程及真实代理出口；
6. 启动前后 v2rayN 进程、系统代理值、默认路由和现用代理 HTTP 204 健康不被本轮操作改变。

最终由用户自行执行浏览器或 v2rayN 验收。自动化不会操作 v2rayN，也不使用 computer use。

## 本阶段完成标准

- 原型文件和自动测试已提交到 AimiliVPN 当前功能分支。
- Docker Compose 可在本机启动，容器健康且只有一条主 OpenVPN 隧道。
- 经 `127.0.0.1:17928` 的显式 HTTP/SOCKS 请求获得有效公网响应，并确认走容器 VPN 出口。
- `127.0.0.1:18787` 管理页可访问，用户可通过 `show-access.ps1` 查看本地测试入口。
- v2rayN 活动进程、系统代理、Windows 默认路由与现用代理健康未被原型改变。
- 未连接 ny，未使用任何生产数据或生产密钥。
- 原型保持运行，等待用户验收；同时提供不丢数据和彻底清理两种停止方式。
