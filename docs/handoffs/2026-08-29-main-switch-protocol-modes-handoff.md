# 主连接安全切换与独立协议模式交接

日期：2026-08-29

目标任务：在不增加 AimiliVPN 运行出口数量的前提下，实现主连接安全切换，以及主连接和每个普通出口独立切换公网节点协议。

## 1. 使用说明

新任务必须从本文开始，并重新只读核对本地仓库和 `ssh ny` 的实际状态。本文记录的是交接时刻的事实与已确认需求，不替代新任务开始时的最新检查。

本文不得扩展为服务器全面改造。不得输出密码、Cookie、UUID、私钥、随机后台路径或完整订阅链接。

## 2. 当前本地状态

交接前最新只读检查结果：

| 项目 | 路径 | 分支与提交 | 状态 |
| --- | --- | --- | --- |
| Aimili Gateway | `D:\CodexProject\Github\aimili-gateway` | `main` / `3e23238` | 工作树干净 |
| AimiliVPN | `D:\CodexProject\Github\aimili-vpngate` | `custom` / `99dddcc` | 工作树干净；相对 `origin/custom` ahead 16 |
| 旧部署资料 | `D:\CodexProject\Github\aimili-3xui-simple-deploy` | 非 Git 仓库 | 保留最初交接和历史部署资料，不作为新代码提交位置 |

Gateway 当前最后一项完整实现是 Test 风格多入站 VLESS 订阅迁移。相关入口：

- `docs/superpowers/specs/2026-08-29-test-style-subscription-design.md`
- `docs/superpowers/plans/2026-08-29-test-style-subscription.md`
- `docs/verification/2026-08-29-test-style-subscription.md`
- `docs/maintenance/2026-08-29-history-residuals-maintenance.md`

## 3. 生产环境只读快照

交接前于 2026-08-29 09:06 UTC 通过 `ssh ny` 完成只读核对：

- 主机：`ubuntu-s-1vcpu-512mb-10gb-nyc2`
- 域名：`ny.zouyunhui.cc.cd`
- 3x-ui：`3.7.0`
- Xray-core：`26.7.28`
- Gateway、3x-ui、Caddy 均为 active
- AimiliVPN 由 Python 进程提供主代理和控制监听；不要根据不存在或 inactive 的同名 systemd unit 直接判定程序未运行
- 物理内存总量约 458 MiB，可用约 188 MiB
- Swap 约 1 GiB，已使用约 113 MiB

当前监听与路由结构：

- 主 VLESS：`8443/TCP`
- 普通受管 VLESS：`20000–20002/TCP`
- 普通 mixed：`30000–30002/TCP`
- 主 mixed：`31000/TCP`
- AimiliVPN 主本地代理：`127.0.0.1:7928`
- AimiliVPN 普通槽位本地代理：`127.0.0.1:17928–17930`
- Caddy：`443/TCP` 和 `443/UDP`，其中 UDP 由 HTTP/3 占用
- UFW 已开放现有 TCP 端口，但未开放 Hysteria2 所需的新 UDP 边界

当前 Xray 配置有四个 VLESS/TCP/REALITY 入站，没有 XHTTP 或 Hysteria2 入站。新任务部署前必须重新核对这些数据。

## 4. 已确认的最新目标

### 4.1 主连接安全切换

候选节点的“替换到出口位”目标列表增加“主连接”，但主连接仍命名为“主连接”，不得伪装为“出口位 4”。

切换主连接必须满足：

1. AimiliVPN 增加正式、版本化、仅回环控制能力，例如 `main.assign`；Gateway 不得调用原后台未受保护的内部页面接口代替正式适配器契约。
2. 候选节点不得同时占用普通出口位，继续保持受管出口唯一性。
3. 切换前记录旧主节点身份和安全状态。
4. 切换后先验证 `127.0.0.1:7928` 的代理 DNS、真实出口和可用性。
5. Gateway 再验证主 mixed 与 `8443` 公网 VLESS 链路。
6. 全部验证通过才提交新主连接；旧主节点重新进入候选池。
7. 新节点失败必须自动恢复旧主节点；旧主节点也恢复失败时才进入 `repair_required`。
8. 主 VLESS 端口、主 mixed 端口、订阅中的逻辑名称和客户端可见身份尽量保持稳定。
9. 切换期间必须与候选刷新、槽位轮换、删除和另一主连接切换互斥。

AimiliVPN 当前只有 `main.read`。内部 `connect_node()` 会先停止旧 `tun0`，新连接失败时不会自动恢复旧节点，因此仅把主连接显示到前端下拉框是不安全且不允许的。

### 4.2 每个运行出口独立选择协议

“每个运行出口”包含主连接和当前启用的普通出口位。每个出口独立选择协议，允许同时出现：

- 主连接：VLESS/TCP/REALITY/XTLS-Vision
- 出口位 1：VLESS/XHTTP/REALITY
- 出口位 2：Hysteria2/QUIC/TLS
- 出口位 3：VLESS/TCP/REALITY/XTLS-Vision

必须遵守：

1. 不增加 AimiliVPN 运行出口数量。
2. 每个逻辑出口同时只有一个公网节点协议配置；不得用额外长期并存的 XHTTP 或 Hysteria2 入站冒充“切换”。
3. 协议切换不得改变 AimiliVPN 槽位及其真实出口。
4. mixed/SOCKS5H 入站不参与协议切换，继续使用同一个 AimiliVPN 出口。
5. TCP 与 XHTTP 都属于 VLESS，可以复用 VLESS 客户端 UUID；Hysteria2 使用独立 Auth/密码，不能复用 VLESS UUID，但 Gateway 中的逻辑出口身份和显示名称保持不变。
6. VLESS 与 Hysteria2切换后允许使用同一数值端口，但监听协议会在 TCP 和 UDP 之间变化；正式设计必须核对 3x-ui/Xray、UFW、云防火墙和回滚是否支持这种做法。
7. 切换会中断该节点的现有连接，其他出口和 SOCKS5H 不应受影响。
8. 失败必须恢复旧入站协议、认证材料、订阅内容和验证状态。

### 4.3 v2rayN 与统一订阅

交接前已确认本机实际客户端：

- v2rayN `7.24.4`
- Xray `26.6.1`
- sing-box `1.13.16`
- Mihomo `1.19.25`

官方 v2rayN 7.24.4 源码已确认：

- 支持解析和生成 XHTTP 参数；XHTTP 由 Xray 核心运行，sing-box 路径不支持 XHTTP。
- 支持导入 `hysteria2://` / `hy2://`，并生成 Hysteria2客户端配置。
- 同一个订阅可以包含 VLESS 与 Hysteria2条目。

因此统一控制台应把“复制 VLESS 订阅”升级为“复制节点订阅”。刷新订阅后：

- TCP → XHTTP：条目仍为 VLESS，但传输参数改变。
- VLESS → Hysteria2：条目类型变为 Hysteria2，v2rayN 使用对应核心运行。
- 节点显示名称继续对应原逻辑出口。

当前 `scripts/verify-test-style-subscription.py` 强制全部条目为 VLESS，必须改造成安全的多协议订阅验证，不得把 Hysteria2误判为非法条目。

## 5. 已废弃或被最新需求替代的决定

以下内容不得继续作为新设计依据：

- “主连接永远不出现在替换目标中”已被替代。
- “把主连接称为出口位 4”仍然不采用。
- “为 XHTTP 新增一个长期并存的备用入口试点”已被用户明确否定。
- “协议扩展需要增加 VPN 运行出口数量”已被用户明确否定。
- “统一订阅只能包含 VLESS”已被替代为混合协议订阅。
- “单地址聚合 VLESS balancer”不是当前目标。
- 不恢复已经迁移删除的 `21000` 聚合入口。
- 不重写 Xray 或 3x-ui 核心，不把 Python 与 Go 合成单进程。

## 6. 正式设计必须集中解决的事项

新任务不得直接编码，先完成一份增量正式设计并让用户集中审核。设计必须明确：

1. AimiliVPN `main.assign` 的请求、响应、错误码、互斥、旧主快照和恢复算法。
2. Gateway 的协议枚举、状态机、幂等键和切换 API。
3. 3x-ui 入站是原位更新还是受管资源替换，以及如何保证所有权和回滚。
4. XHTTP 模式是否启用 XTLS-Vision。3x-ui 3.7.0 文档要求 XHTTP 搭配 Vision 时同步考虑 VLESS 加密；不得把当前 `decryption=none` 的 TCP 配置机械改成 XHTTP 后仍宣称配置等价。
5. Hysteria2 TLS 证书来源、续期、权限、UDP 端口和防火墙边界；不得抢占 Caddy 已使用的 `443/UDP`。
6. 3x-ui 订阅客户端如何跨 VLESS UUID 与 Hysteria2 Auth 保持同一逻辑订阅身份。
7. 协议切换成功、失败、回滚失败和订阅待刷新时的前端呈现。
8. 512 MiB VPS 上的资源预算、单出口试切、回滚条件和逐步扩大验收方法。

推荐的实现顺序是：

1. 先完成主连接安全切换契约、回滚和真实验证。
2. 再抽象每个逻辑出口的协议模式与统一订阅。
3. 先验证 TCP ↔ XHTTP，再验证 VLESS ↔ Hysteria2。
4. 最后验证四个出口同时采用不同协议的混合状态。

这个顺序不代表增加备用入口；试验阶段切换的是既有逻辑出口，失败后立即恢复原协议。

## 7. 验收标准

任何“完成”结论必须有本轮最新证据，至少包括：

### 主连接切换

- 候选可以选择“主连接”。
- 成功切换后 `7928`、主 mixed、`8443` 和代理 DNS 均走新出口。
- 主端口和订阅逻辑名称不变。
- 受控失败能自动恢复旧主连接。
- 主连接切换不改变三个普通 AimiliVPN 槽位。

### 独立协议切换

- 每个运行出口可独立选择协议，不触发其他出口切换。
- 协议切换前后 AimiliVPN 出口位和真实出口保持一致。
- 切换后受管公网节点数量不增加。
- mixed/SOCKS5H 地址和出口不变。
- TCP、XHTTP、Hysteria2 均完成外部真实连接、代理 DNS 和出口一致性验证。
- v2rayN 7.24.4 从统一订阅刷新后能识别并连接混合协议节点。
- 切换失败能恢复旧协议并重新通过真实连接验证。

### 资源与安全

- 重新检查内存、Swap、进程峰值和重启恢复。
- Hysteria2只开放必要 UDP 端口，不扩大管理后台暴露面。
- 不删除或修改任何非 Gateway 受管的 3x-ui 资源。
- 验收记录只保存脱敏状态，不保存连接秘密。

## 8. 新任务工作流

1. 完整阅读本文。
2. 只读核对三个本地目录、分支、提交、工作树和现有设计/验证文件。
3. 通过 `ssh ny` 重新只读核对生产版本、配置、端口、内存、UDP 边界和服务状态。
4. 提供 2–3 种增量架构取舍并推荐“正式主连接事务＋每出口单协议模式＋混合协议订阅”。
5. 完成并提交正式增量设计文档，等待用户明确批准。
6. 批准后制定详细实施计划，再进入 TDD、本地验证和生产阶梯部署。
7. 任一生产阶段失败立即回滚，不把失败扩大为全面服务器重建。
8. 完成后更新验证文档与历史残留维护记录。

用户已经批准本次交接，并明确要求实现这两个功能；但尚未审核本增量的正式设计文档。新任务必须保留设计审核门，不得把交接批准误当成正式设计批准。
