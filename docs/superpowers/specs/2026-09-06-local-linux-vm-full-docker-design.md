# 本机 Linux 虚拟机完整 Docker 部署设计

日期：2026-09-06（Asia/Shanghai）。状态：待用户审阅。

## 目标

在当前 Windows 电脑上交付一套与 ny 生产完全隔离的 Aimili Gateway 本地环境。Ubuntu Server 虚拟机通过 VMware 真桥接获得独立局域网出口，并使用第二张 host-only 网卡提供只对 Windows 主机开放的稳定管理地址。虚拟机内使用 Docker Compose 运行 Aimili Gateway、AimiliVPN、定制 3x-ui/Xray 和 Caddy，并支持主连接、出口1、出口2、出口3共四个逻辑出口。

现有 Linux VPS 原生 systemd 部署继续保留，不改成容器部署；本地 Docker 与 VPS 原生部署共享业务代码和行为合约，但分别使用独立部署入口、数据、密钥和验证记录。

## 当前依据

- Windows 主机为 6 核 12 线程、15.87 GiB 内存；调查时可用内存约 3.85 GiB。
- D 盘可用空间约 129 GiB，适合存放动态虚拟磁盘。
- VMware Workstation 16.1 已安装，VMware Bridge 服务正在运行，当前物理以太网已启用 VMware Bridge；没有正在运行的 VMware 虚拟机。
- Docker Desktop 使用 WSL2，现有 AimiliVPN 单出口原型健康运行，但其 OpenVPN 外连可能被 Windows v2rayN TUN 接管，因此不作为正式完整栈的数据面。
- 单出口原型已证明 AimiliVPN 可以在 Linux 容器内使用 `/dev/net/tun`、`CAP_NET_ADMIN` 和专用数据卷运行。
- Gateway 运行时要求 Gateway、AimiliVPN 控制接口、3x-ui 和 Xray 校验入口可通过回环访问。完整容器栈必须保留这项安全约束，不能为了容器互联放宽为任意 bridge 地址。

## 范围

本阶段包含：

- 自动化创建一台无图形桌面的 Ubuntu Server 虚拟机；
- 在虚拟机内安装并固定 Docker Engine 与 Compose plugin；
- 生成本地专用凭据、数据库和证书，不复制任何生产资产；
- 运行 Gateway、AimiliVPN、定制 3x-ui/Xray、Caddy；
- 支持四个逻辑出口，实际运行数量始终不超过主连接加三个普通出口位；
- 从 Windows 通过虚拟机局域网地址访问 Gateway 页面和本地协议端口；
- 提供启动前宿主资源门、脱敏健康检查、单版本回滚和完整停止入口；
- 保持 Linux VPS 原生安装器、systemd unit 和生产行为不变。

本阶段不包含：

- 读取、复制或同步 ny 的 Gateway DB、x-ui DB、配置、证书、Cookie、token、UUID或私钥；
- 修改 v2rayN 活动节点、TUN、系统代理、订阅和日志；
- 修改 Windows 默认路由、DNS或防火墙；
- 把本地端口转发到路由器公网；
- 增加第五个出口或启动第二个 Xray；
- 在虚拟机内编译 3x-ui、Gateway 或前端。

## 资源配置

虚拟机初始资源固定为：

| 资源 | 配置 | 理由 |
| --- | --- | --- |
| vCPU | 2，单插槽双核 | 主机有12个逻辑处理器，避免抢占日常应用 |
| 内存 | 2048 MiB | 当前主机可用内存有限，拒绝直接分配4–6 GiB |
| swap | 虚拟机内1 GiB | 仅吸收更新或数据库检查时的短暂峰值 |
| 系统盘 | D盘24 GiB动态增长 | 不预占24 GiB，设定明确容量上限 |
| 网络 | 1张桥接网卡加1张host-only网卡 | 桥接负责外连，host-only负责稳定且不向局域网开放的管理入口 |
| 图形 | 无桌面环境 | 降低常驻内存和维护面 |

虚拟机启动脚本必须先读取主机总内存、可用内存和运行中的同名 VM。可用内存低于 3.5 GiB 时拒绝自动启动并只给出原因，不结束 v2rayN、Docker Desktop或其他用户程序。虚拟机运行后不自动扩容；任何超过2 GiB内存或2 vCPU的变更都需要重新评估主机余量。

容器采用以下初始上限：

| 服务 | 内存上限 | CPU上限 |
| --- | --- | --- |
| AimiliVPN | 640 MiB | 0.80 |
| 3x-ui/Xray | 384 MiB | 0.70 |
| Aimili Gateway | 192 MiB | 0.25 |
| Caddy | 96 MiB | 0.15 |
| 网络命名空间与初始化服务 | 32 MiB | 0.10 |

这些上限是故障隔离值而非预留值。首次验收必须记录实际峰值；只有出现可复现 OOM 且主机仍有足够余量时，才单独调整发生问题的服务。

## 虚拟机与网络

采用现有 VMware Workstation，不启用新的 Hyper-V 管理组件，不要求 Windows 重启。虚拟机目录固定在 `D:\VirtualMachines\AimiliGatewayLocal`，仓库只保存可审查的创建脚本和校验清单，不提交虚拟磁盘、ISO、OVA、生成的密钥或运行数据库。

第一张虚拟网卡使用 bridged/VMnet0，并承担虚拟机默认路由和所有OpenVPN外连。自动化必须确认当前桥接出口是物理以太网，不能桥接到 `singbox_tun`、`vEthernet (WSL)`、VMnet1或VMnet8。桥接地址由局域网DHCP分配并脱敏记录；不猜测静态地址，不修改路由器。

第二张虚拟网卡使用现有host-only/VMnet1，仅用于Windows主机访问Gateway、订阅和本地协议端口。它没有默认路由，不承担OpenVPN外连，也不向其他局域网设备开放。安装脚本从VMnet1当前子网中选择并核对未占用的固定地址，随后把Docker发布端口绑定到该地址；地址只写入本地忽略文件和运行配置，不写入公共文档。

Windows 到虚拟机的数据流为：

```text
Windows 浏览器或用户主动配置的 v2rayN 节点
  -> VMware VMnet1 host-only 网络
  -> Ubuntu VM 稳定管理地址
  -> Docker 发布端口
  -> 共享 Linux 网络命名空间
  -> Gateway / Caddy / 3x-ui / AimiliVPN
  -> tun0 或 tun120/tun121/tun122
  -> VPNGate 节点
  -> 公网
```

虚拟机的 OpenVPN 外连从第一张桥接网卡直接进入物理局域网，管理请求通过第二张host-only网卡进入，两条路径不得互换。验收必须证明OpenVPN外连没有进入Windows的v2rayN代理连接表；若仍被接管，则停止在单出口阶段，不启用其余三个出口。

## 容器网络与组件边界

Compose 使用一个常驻、最小化的网络命名空间容器作为稳定网络锚点。AimiliVPN、Gateway、3x-ui/Xray 和 Caddy通过 `network_mode: service:<network-anchor>` 共享该网络命名空间，因此继续使用当前 `127.0.0.1` 接口，不修改 Gateway 的回环校验。

只有 AimiliVPN 容器获得 `CAP_NET_ADMIN` 和 `/dev/net/tun`。Gateway、3x-ui/Xray、Caddy和初始化容器均不获得该 capability，不使用 `privileged`、host network或Docker socket。网络锚点只负责保持命名空间和发布端口，不包含业务逻辑。

网络锚点先于其他服务启动，平时不随单个业务镜像更新而重建。若确需重建网络锚点，生命周期脚本必须停止完整栈并按固定顺序重新创建，不能让旧容器引用失效的命名空间。

组件职责：

- AimiliVPN：维护有效节点池、主连接和三个槽位隧道，提供本地管理页、控制 API和四个固定代理出口。
- 3x-ui/Xray：运行一个Xray进程，维护 `agw-` 命名空间内的公网入站、mixed入站、出站和订阅客户端；不得接管非受管资源。
- Aimili Gateway：维护四个逻辑出口身份、协议事务、订阅别名、来源策略和管理 UI。
- Caddy：只暴露本地管理入口和必要协议路由；不添加公网端口映射或自动路由器转发。
- 初始化服务：首次创建本地凭据和目录权限；已存在的有效文件必须原样复用。

## 数据、凭据与镜像

Gateway DB、x-ui DB、AimiliVPN节点缓存、Caddy数据、Gateway主密钥、AimiliVPN控制token和3x-ui自动化凭据分别保存在明确命名的 Docker volume。任何 volume 都不与 Windows Docker单出口原型、ny或其他项目共用。

首次启动由初始化服务生成随机本地凭据并写入只读挂载给消费容器。秘密不写入 Compose、Git、普通日志或验证报告；只有用户主动运行本地 `show-access` 命令时才显示管理入口。

Gateway、AimiliVPN与定制3x-ui镜像在 Windows Docker Desktop 中按受限资源串行构建，校验摘要后导出为归档，再导入虚拟机。虚拟机不执行Go、前端或3x-ui源码构建。所有镜像使用不可变提交标签，不使用未固定的 `latest` 作为验收依据。

## 多出口与节点池

多出口继续沿用现有运行模型：

- 主连接使用 `tun0`；
- 出口1、出口2、出口3分别使用既有固定槽位网卡和策略路由表；
- 只有一个Xray进程；
- 每个逻辑出口只有一个公网协议；
- mixed/SOCKS5H端口不随公网协议切换；
- 固定端口不因容器化而重新编号。

启用顺序为：

1. 主连接单出口；
2. 主连接加出口1；
3. 再启用出口2；
4. 最后启用出口3。

每一步都必须验证已有出口未中断后才能继续。任何一步出现路由串线、Xray多实例或宿主v2rayN干扰时，回退到上一阶段。

节点池先使用 `TARGET_VALID_POOL_SIZE=10`、`MAX_FETCH_ROWS=120`、`NODE_TEST_BATCH_SIZE=1`、`OPENVPN_TEST_CONCURRENCY=1` 验证稳定性。四出口稳定后再调整为与ny容量相同的 `TARGET_VALID_POOL_SIZE=30`、`MAX_FETCH_ROWS=300`，但精验并发仍保持1。虚拟机节点数量可能因本地运营商链路而少于ny，不能把30当作保证成功数。

## 本地访问与安全边界

初次验收只允许Windows主机通过VMnet1 host-only地址访问。Docker发布端口和Linux防火墙都绑定该host-only地址；桥接网卡不监听管理页、订阅和本地协议端口。不允许 `0.0.0.0/0` 或 `::/0` 来源规则，也不配置路由器端口转发。

Gateway要求精确HTTPS origin。本地Caddy使用仅服务于host-only地址的内部CA证书，初始化时固定Gateway的 `publicOrigin`。自动化只导出CA公钥证书和SHA256指纹，不自动写入Windows信任库。浏览器和v2rayN需要无警告订阅时，由用户审阅指纹后显式运行单独的“信任本地CA”脚本；该脚本只能写当前Windows用户的证书库，并提供精确撤销命令。未取得这项单独确认前，完整栈仍可完成服务端和命令行TLS验证，但不能宣称v2rayN订阅验收通过。

由于来源策略测试依赖浏览器实际来源地址，本地部署必须把桥接地址和反向代理转发头纳入单独验证。来源限制默认关闭；只有确认Gateway识别到Windows主机来源且四个mixed入站可以事务回滚后，才允许用户启用。

自动化不得自行把任何新节点设为 v2rayN 活动节点。最终页面、订阅导入、延迟测试和节点切换由用户手动验收。

## 启动、升级与回滚

生命周期分为：

1. 主机资源与桥接预检；
2. 虚拟机启动和SSH健康；
3. volume与凭据初始化；
4. 镜像导入；
5. 网络锚点、AimiliVPN、3x-ui/Xray、Gateway、Caddy依次启动；
6. 主连接到三个出口逐级验收。

应用更新使用不可变镜像标签和原子Compose配置切换。每次更新只保留当前版和一个previous镜像/配置清单；数据库变更前只保留一个最新一致性备份。健康门失败时恢复previous清单和备份，不循环保留历史归档。

UI继续使用Gateway已实现的版本化外部静态资源和原子软链接切换，可在不重启Gateway后端的情况下更新纯前端。后端、AimiliVPN或3x-ui二进制变化仍通过镜像升级和健康门完成。

停止操作默认关闭虚拟机或Compose但保留所有volume。删除虚拟磁盘、volume或本地凭据必须使用单独的显式purge入口，核对准确绝对路径后执行，并明确提示不可恢复。

## 失败处理

- 主机可用内存不足：拒绝启动VM，不结束任何用户程序。
- 桥接落到虚拟适配器或TUN：拒绝启动业务栈。
- 镜像摘要不匹配：拒绝导入或启动。
- AimiliVPN主连接失败：保留日志和节点缓存，不启动出口槽位。
- 某个槽位失败：只回退该槽位，已经健康的出口继续运行。
- Gateway或3x-ui健康失败：数据面保持上一版，恢复previous镜像和唯一数据库备份。
- 单Xray或路由隔离检查失败：停止阶梯扩容，不能以页面显示ready代替真实链路。
- SSH或工具中断：按 `recovering-interrupted-tasks` 核对VM、容器、volume和日志状态后续作，不盲目重放创建或删除。

## 实施分解

完整目标跨越虚拟机基础设施、容器运行时、Gateway/3x-ui集成和多出口数据面，不作为一个不可回退的大步骤实施。批准本设计后按以下四个独立计划推进，每个计划都交付可运行检查点：

1. **VM基础与安全门**：创建受限资源的Ubuntu VM，完成双网卡、SSH、Docker和宿主不变性验证；不启动Aimili业务。
2. **镜像与单出口完整栈**：建立本地镜像打包/导入、网络锚点和持久卷，先运行Gateway、AimiliVPN主连接、3x-ui/Xray和Caddy；不启用普通出口槽位。
3. **三个出口阶梯启用**：逐个加入出口1、出口2、出口3，验证策略路由、mixed、公网协议、订阅和单Xray。
4. **运维与用户验收**：实现current/previous、唯一数据库备份、停止/恢复、可选本地CA信任和脱敏验收报告，由用户执行浏览器及v2rayN最终验收。

任何阶段失败都停留在上一个可运行检查点，不提前创建后续阶段依赖。各计划分别使用TDD和独立本地提交；不因本地Docker工作修改Linux VPS原生部署行为。

## 测试与验收

实现必须使用TDD并覆盖：

1. VM资源不超过2 vCPU、2048 MiB和24 GiB动态磁盘；低内存时拒绝启动。
2. VMX使用一张桥接网卡和一张host-only网卡；默认路由只经过物理桥接网卡，管理端口只绑定host-only地址。
3. Compose不含privileged、host network、Docker socket和生产路径挂载。
4. 只有AimiliVPN获得NET_ADMIN和TUN设备，其他服务共享稳定网络命名空间。
5. 所有镜像和volume均使用本地专用命名，不包含ny地址、生产路径或生产凭据。
6. 容器启动顺序、健康门、previous回滚和单备份保留正确。
7. 主连接及三个槽位分别通过真实AimiliVPN出口、mixed和公网协议验证。
8. Gateway DB、x-ui DB可读且身份顺序一致，订阅包含四个当前逻辑出口。
9. 只有一个Xray进程和最多四个OpenVPN进程。
10. Windows默认路由、系统代理和v2rayN进程未被改变；自动化未切换活动节点。
11. VM OpenVPN外连不进入Windows v2rayN代理链路。
12. 本地HTTPS证书指纹可核对，默认不修改Windows信任库；显式信任和撤销都只影响当前用户。
13. 停止后数据可恢复，显式purge只删除准确命名的本地VM和volume。

最终由用户自行在浏览器和v2rayN中验收。自动化不使用computer use，不代替用户切换节点。

## 完成标准

- Ubuntu VM在2 vCPU、2 GiB内存和24 GiB动态磁盘下稳定运行完整Compose栈。
- Gateway、AimiliVPN、3x-ui/Xray和Caddy均健康，重启后可恢复。
- 主连接、出口1、出口2、出口3顺序和身份一致，真实代理出口互不串线。
- 节点池可从3逐级扩到10和30；实际成功数如实记录。
- 订阅包含四个逻辑出口，用户可手动导入v2rayN测试。
- Windows v2rayN、系统代理和默认路由保持原状，ny生产没有被连接或修改。
- 工作区、本地提交和远程推送状态分别准确汇报。
