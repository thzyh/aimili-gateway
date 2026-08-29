# 主连接安全切换与每出口独立协议模式增量设计

日期：2026-08-29

状态：已批准（2026-08-29）

范围：Aimili Gateway、AimiliVPN、3x-ui/Xray 与统一节点订阅的增量改造

## 1. 设计结论

本增量采用以下组合：

1. AimiliVPN 提供正式的主连接两阶段事务：`stage → commit/rollback`。`stage` 完成新主节点拨号和 `127.0.0.1:7928` 的代理 DNS、真实出口、可用性验证；Gateway 完成主 mixed 与当前公网协议验证后才 `commit`。
2. 每个逻辑出口只有一个公网入站。协议切换原位复用同一 3x-ui 入站 ID、内部 tag、数值端口和 SOCKS 出站路由，不创建长期并存的备用入站。
3. 运行时使用当前仅回环的 Xray HandlerService 对目标入站执行热移除/热加入；持久化由 root-owned、按需运行的窄事务助手更新 3x-ui SQLite。不得使用会重启整个 Xray 的普通写入路径完成在线协议切换。
4. 3x-ui 全局订阅客户端保留一个 email/subId，同时持有 VLESS UUID 与独立 Hysteria2 Auth。统一订阅 URL 不变，条目协议随各逻辑出口的当前模式变化。
5. XHTTP V1 不启用 XTLS-Vision：使用 VLESS/XHTTP/REALITY、`decryption=none`、空 flow，并显式关闭 3x-ui 自动注入 Vision。Hysteria2 使用现有 Caddy 公共证书的稳定文件路径，不占用 `443/UDP`。

本设计不增加 AimiliVPN 运行出口数量，不增加常驻 Xray 进程，不增加公网管理入口，也不恢复已删除的 `21000` 聚合入口。

## 2. 本轮最新事实基线

### 2.1 本地状态

| 项目 | 最新核对结果 |
| --- | --- |
| Aimili Gateway | `main@f1cdb72`，工作树干净；最后提交为本任务交接文档 |
| AimiliVPN | `custom@99dddcc`，工作树干净；相对 `origin/custom` ahead 16 |
| 旧部署资料 | `aimili-3xui-simple-deploy` 仍不是 Git 仓库，只作历史资料 |

仓库内没有额外 `AGENTS.md`。现有 Gateway 已实现普通槽位 `slots.assign`、主连接只读检测、四入站 3x-ui 原生订阅和历史聚合清理。

### 2.2 生产状态

- 主机、Gateway、AimiliVPN、3x-ui、Xray、Caddy 均在运行。
- 3x-ui 为 `3.7.0`，Xray 为 `26.7.28`，AimiliVPN 生产检出为干净的 `99dddccb7175`。
- AimiliVPN 仍只有 `main.read`，没有 `main.assign`。
- 当前运行出口仍是主连接 `7928` 加三个普通槽位 `17928–17930`。
- 公网节点仍为 `8443` 与 `20000–20002` 四个 VLESS/TCP/REALITY 入站；mixed 为 `30000–30002` 与 `31000`。
- `443/UDP` 由 Caddy HTTP/3 占用。UFW 只开放既有 TCP 边界，没有协议节点所需的 UDP 规则。
- Xray 二进制包含 Hysteria 与 XHTTP，3x-ui 前端和全局客户端模型支持 XHTTP、`hysteria2://`、UUID、password、auth。
- 3x-ui 全局订阅客户端目前只有 UUID，没有 Hysteria2 Auth。
- Xray HandlerService 已启用 `adi`、`rmi` 等能力，监听 `127.0.0.1`，不对公网开放。
- 生产历史日志证明普通 3x-ui 配置写入会重载 Xray；因此该路径不能满足“其他出口和 SOCKS5H 不受影响”。
- Caddy 的当前公共证书和私钥位于稳定存储，权限为 `0600`；x-ui/Xray 以 root 运行，可读取这些文件。当前没有 `x-ui-caddy-sync` 服务或定时器。
- 物理内存约 458 MiB，当前可用约 216 MiB；Swap 约 1 GiB，空闲约 902 MiB。主要常驻进程合计 RSS 约 128 MiB。

## 3. 范围与硬约束

### 3.1 必须实现

- 候选节点的替换目标增加“主连接”，显示名称始终为“主连接”。
- 主连接失败自动恢复旧主节点；旧主也恢复失败时才进入 `repair_required`。
- 主连接、出口位 1、出口位 2、出口位 3 各自独立选择公网协议。
- 协议模式只改变该逻辑出口的公网入站，不改变 AimiliVPN 主连接或槽位，不改变 mixed/SOCKS5H。
- 统一订阅同时支持 VLESS 与 Hysteria2，逻辑名称和订阅 URL 保持稳定。
- 所有失败都恢复旧入站协议、认证材料、订阅关联与验证状态；恢复失败才标记 `repair_required`。

### 3.2 不做

- 不增加 AimiliVPN 出口数量。
- 不增加长期并存的 XHTTP/Hysteria2 备用入站。
- 不恢复单地址 balancer 或 `21000`。
- 不重写 3x-ui、Xray 核心，不把 Python 与 Go 合成单进程。
- 不删除任何非 Gateway 受管的 3x-ui 入站或全局客户端。
- 不让浏览器提交任意 Xray JSON、证书路径、认证材料或防火墙规则。

## 4. 架构取舍

### 4.1 方案 A：两阶段主连接事务 + 原位单入站热切换（推荐）

AimiliVPN 保存待提交的主连接事务；Gateway 验证完整公网链路后提交。协议模式保持一个入站记录，运行时通过 Xray HandlerService 只替换目标 tag，root 事务助手同步 3x-ui 数据库。

优点：完整满足主连接回滚、单协议入站、稳定端口、稳定订阅和其他出口不中断；不增加常驻进程或公网入口。缺点：需要一个严格限权的 root 事务助手和崩溃恢复日志，工程复杂度最高。

### 4.2 方案 B：使用 3x-ui 普通 API 原位更新

仍原位更新同一入站，但直接调用 `panel/api/inbounds/update/{id}`。

优点：实现最短，全部持久化由 3x-ui 完成。缺点：生产证据已确认这类写入会重载 Xray，其他公网节点与 mixed 的既有连接会被连带中断，不满足验收标准。仅可作为维护窗口下的人工恢复路径，不作为在线切换实现。

### 4.3 方案 C：删除重建或并行备用入站

删除旧入站后以同端口创建新协议，或预建协议专用备用入站再切换。

优点：每种协议模板相对独立。缺点：删除重建会破坏主连接的非 Gateway 资源边界和订阅关联；并行备用入站违反“同时只有一个公网节点协议配置”，还会增加端口、资源和回滚面。该方案不采用。

## 5. 逻辑出口与协议模型

### 5.1 逻辑出口

- 主连接：固定 ID `agw-main`、`egressSource=main`、Aimili 本地端口 `7928`。
- 普通出口：现有 `proxy_groups.id`、`egressSource=slot`、Aimili 槽位 `0–2`。
- 每个逻辑出口固定拥有一个 `publicInboundId`、一个 `publicPort`、一个 `mixedInboundId`、一个 `mixedPort` 和一个 SOCKS 出站 tag。
- 现有 `*-vless`、`aimili-reality` tag 作为兼容性的内部资源键保留；代码和 UI 不再从 tag 后缀推断当前协议。

数据库与 Go 模型把 `VLESSInboundID/VLESSPort` 迁移为中性的 `PublicInboundID/PublicPort`。旧列在一次受控迁移中回填，禁止同时维护两个可写事实源。

### 5.2 封闭协议枚举

```text
vless_tcp_reality_vision
vless_xhttp_reality
hysteria2_quic_tls
```

| 模式 | 3x-ui/Xray 形态 | 客户端凭据 | 端口传输 |
| --- | --- | --- | --- |
| `vless_tcp_reality_vision` | VLESS + TCP + REALITY + Vision | VLESS UUID | TCP |
| `vless_xhttp_reality` | VLESS + XHTTP + REALITY，无 Vision | 同一 VLESS UUID | TCP |
| `hysteria2_quic_tls` | Hysteria2/Hysteria + QUIC + TLS | 独立 Auth | UDP |

XHTTP V1 明确不启用 Vision，也不启用 VLESS Encryption。3x-ui 入站设置使用空 flow、`decryption=none`、`disable_flow=true`；订阅条目不得携带 `xtls-rprx-vision`。以后若要评估“XHTTP + Vision + VLESS Encryption”，必须另开设计，不在本增量隐式开启。

### 5.3 协议状态表

新增 `egress_protocol_modes`，每个逻辑出口一行：

- `egress_id`：`agw-main` 或普通组 ID；
- `active_mode`、`desired_mode`：封闭枚举；
- `state`：`ready`、`switching`、`subscription_pending`、`rolling_back`、`repair_required`；
- `last_operation_id`、`last_request_hash`：支持跨重启幂等，不保存浏览器原始幂等键；
- `last_error_code`、`version`、`updated_at`。

另增 `egress_operations` 保存操作阶段、请求哈希和 root 事务日志引用。包含入站私钥、UUID、Auth 或完整订阅地址的回滚快照不得明文进入 Gateway SQLite；完整快照只存在 root-only `0600` 临时事务日志，提交后删除，失败恢复完成后删除，`repair_required` 时保留到人工修复完成。

## 6. AimiliVPN 主连接事务

### 6.1 控制 API

所有接口继续只监听回环地址并使用现有 Bearer 控制令牌。

```text
GET  /control/v1/main
GET  /control/v1/main/assignment
POST /control/v1/main/assign
POST /control/v1/main/assign/{operationId}/commit
POST /control/v1/main/assign/{operationId}/rollback
```

`capabilities` 增加：

```text
main.assign
main.assign.commit
main.assign.rollback
main.assignment.read
```

`POST /main/assign` 请求字段：

```json
{
  "candidateId": "<候选标识>",
  "country": "JP",
  "proxyType": "datacenter",
  "expectedCurrentCandidateId": "<当前主节点标识>",
  "idempotencyKey": "<不透明键>"
}
```

响应只返回安全状态：操作 ID、`pending_commit/rolled_back/repair_required`、旧/新候选标识与分类、`7928` 端口、DNS/出口验证布尔值、脱敏错误码和过期时间。不得返回 OpenVPN 配置、令牌、Cookie 或连接秘密。

### 6.2 stage 算法

1. 严格校验字段、幂等键和 `expectedCurrentCandidateId`。
2. 拒绝当前普通槽位已占用的候选；同时把旧主、新主和三个槽位节点加入保留集合。
3. 持久化旧主候选标识、连接开关、路由模式、固定节点设置和事务期限。节点配置正文继续引用 AimiliVPN 已有受限数据，不复制到 API 响应。
4. 停旧主、拨新主、配置 `tun0`，验证 `7928` 的代理 DNS、真实出口与可用性，并清理旧下游连接。
5. 验证通过后写入 `pending_commit`。此时旧主仍被事务保留，不能被刷新淘汰或分配给普通槽位。
6. 新主拨号或 `7928` 验证失败时立即重连旧主并重新验证。旧主恢复成功返回 `assign_failed_rolled_back`；旧主恢复失败返回 `rollback_failed` 并进入 `repair_required`。

### 6.3 commit、rollback 与崩溃恢复

- `commit` 只接受匹配的 `pending_commit` 操作；提交后新主成为当前主节点，旧主解除保留并自然回到候选池。
- `rollback` 重连旧主并完成 `7928` 验证后返回 `rolled_back`。
- `stage` 默认期限为 180 秒。AimiliVPN 启动或后台守护发现超期的 `pending_commit` 时自动 rollback；恢复期间拒绝新的变更操作。
- 重复相同幂等键和相同请求返回原操作状态；相同键配不同请求返回 `idempotency_conflict`。

### 6.4 互斥

所有以下变更入口统一检查 `main_assignment` 状态并通过同一个变更协调器：

- 主连接 assign/自动切换；
- 候选刷新和维护；
- 槽位 create/assign/rotate/delete；
- 槽位后台自动漂移。

只读候选、主状态、槽位状态和健康检查可以继续。锁顺序固定为“主事务协调器 → maintenance → slot supervisor → 普通状态锁”，不得反向获取。

错误码至少包括：`invalid_request`、`candidate_not_found`、`candidate_unavailable`、`candidate_in_use`、`current_mismatch`、`operation_busy`、`idempotency_conflict`、`assign_failed_rolled_back`、`rollback_failed`。

## 7. Gateway 主连接切换

现有候选“替换”API 保持入口不变：

```text
POST /api/v1/proxy-groups/{candidate}/replace
{
  "targetGroupId": "agw-main"
}
```

请求继续要求登录、同源、CSRF 与 `Idempotency-Key`。Gateway 将浏览器幂等键哈希后持久化到操作记录，再生成 AimiliVPN 控制幂等键；现有内存缓存只作快速重放，不再是唯一幂等依据。

执行顺序：

1. 从最新 Aimili 状态读取实际主候选标识；候选池去重，当前主节点不再重复显示成 standby。
2. 创建 `main_assign` 操作，状态为 `switching`。
3. 调用 AimiliVPN `stage`，确认 `7928` 已通过 DNS、出口和可用性验证。
4. 使用主 mixed 实测代理 DNS 与出口，再按 `agw-main` 当前协议模式验证 `8443` 公网链路；三条结果必须与 Aimili 新主出口一致。
5. 调用 `commit`，更新 `main_egress` 的真实候选身份、出口、检测结果和版本；旧主这时才重新显示为候选。
6. 任一步失败调用 `rollback`，并对旧主重新执行 `7928`、主 mixed、当前公网协议三重验证。恢复成功时操作记为失败但主连接回到 `ready`；恢复失败才把主连接标记为 `repair_required`。

主公网端口、主 mixed 端口、内部入站 ID、订阅 URL 和客户端显示名不因主节点变化而改变。

## 8. 公网协议原位切换

### 8.1 Gateway API

```text
PUT /api/v1/proxy-groups/{id}/protocol-mode
{
  "mode": "vless_xhttp_reality"
}
```

`id` 可以是 `agw-main` 或一个 ready 的普通出口组。请求需要登录、同源、CSRF 与 `Idempotency-Key`。相同目标模式且当前为 `ready` 时返回幂等成功，不重写 3x-ui 或重载 Xray。

响应增加：`protocolMode`、`protocolState`、`subscriptionState`、`availableProtocolModes`、`lastErrorCode`。连接接口使用中性 `publicUri`，旧 `vlessUri` 在兼容期只对 VLESS 模式返回，Hysteria2 模式返回明确的 `protocol_changed`，不得拼造 VLESS 地址。

### 8.2 窄事务助手

新增 root-owned 按需执行助手，例如 `aimili-xui-protocol-transaction`。它通过 root-owned systemd path/oneshot 接收 Gateway 写入受限 spool 的封闭请求；没有常驻进程、没有网络监听。请求只允许：逻辑出口 ID、预期入站 ID/tag/端口、旧模式、新模式、操作 ID 和预期指纹。

助手必须自行从 root-owned 配置和 3x-ui 数据库验证：

- 普通入站必须同时匹配既有 ID、tag、端口、`Aimili Gateway` remark、对应 SOCKS 出站和路由；
- 主入站必须匹配既有 ID、`aimili-reality`、`8443`、`Aimili Reality` 和 `aimili-socks → 127.0.0.1:7928`；
- 目标端口只能属于 `8443, 20000, 20001, 20002` 的部署白名单；
- mixed 入站、其他 tag、非 Gateway 入站和任意 Xray JSON 一律不能作为请求目标。

主入站属于“严格绑定的遗留资源”，允许为本功能原位改变协议字段，但永远不允许删除。其 ID、tag、端口和全局客户端记录必须保留。

### 8.3 原子顺序

1. Gateway 将协议状态置为 `switching`，助手创建 root-only `0600` 联合快照：目标入站行、关联全局客户端的相关字段、当前运行时入站和订阅关联摘要。
2. 使用完整临时配置执行 Xray 离线语法测试；临时文件权限 `0600`，完成后立即删除。
3. 通过仅回环 HandlerService `rmi` 移除目标 tag，再用 `adi` 加入同 tag、同数值端口的新协议入站。运行时空窗只影响该公网入站。
4. 热加入成功后，在一个 SQLite 事务中持久化目标入站和所需全局客户端协议凭据；不得修改 mixed、其他入站、其他路由或非相关客户端字段。
5. 重新读取 Xray `lsi` 与 3x-ui SQLite，确认运行时和持久化指纹一致。x-ui/Xray 后续重启必须能从数据库重建同一模式。
6. Gateway 重建并验证原生订阅，再执行目标公网协议的外部真实连接、代理 DNS 和出口一致性验证；普通槽位同时核对 Aimili slot，主连接核对 `7928` 与主 mixed。
7. 全部通过后删除 root 事务快照，状态进入 `ready`。

若热加入、数据库持久化、订阅或真实流量验证失败，助手按快照执行反向热替换并恢复数据库；Gateway 再验证旧协议。恢复成功返回原失败码并回到旧模式 `ready`；恢复失败进入 `repair_required`，保留 root 事务快照。

普通 3x-ui API 全量写入只用于离线修复和阶梯部署中的重启恢复验证，不用于日常在线切换。

### 8.4 其他出口不受影响的判定

协议切换期间必须持续对另外三个逻辑出口和全部 mixed 入口建立探针：

- Xray PID 不改变；
- 非目标入站持续存在，tag、端口、协议和路由指纹不变；
- 已建立的非目标探针连接不被断开；
- AimiliVPN 主连接和三个槽位进程、节点身份与出口不变。

任一条件失败即判定实现路径不合格并回滚，不把“短暂全局重载”视为成功。

## 9. 协议模板

### 9.1 VLESS/TCP/REALITY/Vision

- 保留当前 VLESS UUID、Reality 密钥、short ID、SNI、客户端显示名和 TCP 数值端口。
- `flow=xtls-rprx-vision`、`decryption=none`、`network=tcp`、`security=reality`。

### 9.2 VLESS/XHTTP/REALITY

- 复用同一 VLESS UUID、Reality 密钥和数值端口。
- `network=xhttp`、`security=reality`、XHTTP mode 使用 3x-ui/Xray `auto`。
- XHTTP path 从稳定逻辑出口 ID 派生并持久化，不在日志、验收文档或错误响应中输出其完整值。
- `flow` 为空、`disable_flow=true`、`decryption=none`，不宣称与 TCP/Vision 等价。

### 9.3 Hysteria2/QUIC/TLS

- Xray/3x-ui 内部使用其 Hysteria 服务端协议表示，对外订阅生成 `hysteria2://`。
- 使用独立高熵 Auth，不复用 VLESS UUID；同一全局客户端同时保留 UUID 和 Auth。
- 复用逻辑出口数值端口，但监听从 TCP 变为 UDP；`443/UDP` 永不作为候选。
- TLS 直接引用 Caddy 当前域名证书的稳定文件路径，Gateway 和日志不读取或输出私钥正文。
- `oneTimeLoading=false`，让 Xray 使用 Caddy 续期后的文件；部署前验证证书域名、有效期、文件所有者/权限和 Xray 可读性，续期后通过外部握手核对新证书。

部署只开放四个逻辑公网端口的精确 UDP 白名单：`8443/udp`、`20000/udp`、`20001/udp`、`20002/udp`。不开放 UDP 端口范围，不开放 `443/udp` 给 Hysteria2。处于 TCP 模式的端口没有 UDP 监听；该精确集合是独立切换功能的固定安全边界。

VPS 内无法只读证明云厂商防火墙规则。Hysteria2 阶梯部署以外部 QUIC/TLS 握手为硬门：若 UFW 计数与抓取表明数据包未到达主机，判定上游云防火墙阻断，立即恢复旧 VLESS 模式并停止扩大，不重建服务器。

## 10. 统一订阅与客户端身份

按钮从“复制 VLESS 订阅”改为“复制节点订阅”。订阅 URL、全局客户端 email 和 subId 不变。

3x-ui 全局客户端在同一行保存不同协议凭据：

- VLESS 入站读取 UUID；
- Hysteria2 入站读取独立 Auth；
- UUID 与 Auth 均不得显示在 Gateway 列表、审计、日志或设计/验收文档中。

对当前已附着到目标入站的客户端：

- 保留全局客户端行、email、subId、已有 UUID/Auth 和 enable 状态；
- 进入 Hysteria2 时，缺少 Auth 的客户端只新增独立 Auth，不覆盖非空字段；
- 不删除非 Gateway 客户端，不改变其其他入站关联；
- 回滚恢复其协议字段与目标入站关联快照。

协议切换后 3x-ui 原生订阅仍精确关联四个公网入站 ID。验证器不再要求全部条目是 VLESS，而是按逻辑出口验证：条目数、入站 ID、显示名、当前协议类型、端口和必要参数均与 `egress_protocol_modes` 一致。mixed/SOCKS5H 永不进入节点订阅。

订阅重建或获取失败时，公网入站暂处 `subscription_pending`，不提交协议模式；超时后自动回滚旧模式。不能出现“入站已切换但订阅仍长期发布旧协议”的成功状态。

## 11. 前端状态

- ready 的主连接和普通出口显示协议下拉框；standby 候选只显示替换目标。
- 候选替换目标按“主连接、出口位 1、出口位 2、出口位 3”显示，不出现“出口位 4”。
- `switching`：显示正在切换，禁用本出口协议切换、候选刷新、槽位轮换/删除和另一主连接切换。
- `subscription_pending`：显示“协议已应用，正在验证订阅”，仍不允许复制未验证节点。
- 切换失败且回滚成功：显示原协议和本次脱敏错误码，状态恢复 ready。
- `repair_required`：禁止新的切换/删除，显示“需要修复”，保留检测和修复入口。
- 协议切换成功后明确提示该公网节点旧连接会中断；mixed/SOCKS5H 地址和出口未变化。

## 12. 资源预算与安全边界

- 不增加 AimiliVPN 隧道、常驻 Xray 进程或常驻事务守护进程。
- root 协议助手按需执行，正常状态不占 RSS；spool、配置和事务日志分别使用最小权限，拒绝符号链接、路径穿越、未知字段和非白名单端口。
- 阶梯部署前要求 `MemAvailable ≥ 160 MiB`、Swap 空闲 `≥ 512 MiB`；不满足则不开始协议试切。
- 单出口切换后观察至少 5 分钟：不得有 OOM，Xray RSS 峰值不得超过 96 MiB，`MemAvailable` 不得连续 30 秒低于 96 MiB，Swap 增量不得超过 128 MiB。越界立即回滚该出口。
- 操作日志只记录操作类型、逻辑出口、旧/新模式、阶段、耗时和脱敏错误码；不记录密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。

## 13. TDD 与验证矩阵

### 13.1 AimiliVPN

- `main.assign` 请求闭集、鉴权、能力枚举和安全响应。
- 新主成功进入 `pending_commit`，commit 后旧主回候选池。
- 新主拨号失败、DNS 失败、真实出口失败均自动恢复旧主。
- 旧主恢复失败进入 `repair_required`。
- 相同幂等键重放、不同请求冲突、预期主节点不匹配。
- 与刷新、槽位 assign/rotate/delete、后台漂移的互斥。
- 崩溃、重启和 180 秒超时自动 rollback。
- 主候选与普通槽位候选双向唯一性。

### 13.2 Gateway 与事务助手

- 协议枚举、数据库迁移和状态机合法转移。
- 普通受管入站和严格绑定主入站的所有权检查。
- 三种模板的离线 Xray 配置测试。
- HandlerService 只热替换目标 tag，非目标连接持续存活。
- SQLite 持久化失败、热加入失败、订阅失败、外部验证失败的反向恢复。
- 事务助手崩溃点注入和 root 日志恢复。
- 非 Gateway 入站永不被更新或删除；非 Gateway 全局客户端不被删除。
- mixed、SOCKS 路由、Aimili 槽位身份和出口不变。
- 主连接 stage 后公网失败触发 Aimili rollback，并重新验证旧链路。

### 13.3 订阅与前端

- 纯 VLESS、VLESS+XHTTP、VLESS+Hysteria2 和四出口混合模式订阅。
- 同一订阅 URL 下 UUID/Auth 各自用于正确协议，验证器不输出连接材料。
- v2rayN `7.24.4` 刷新后识别 VLESS/XHTTP 与 Hysteria2，并对每条执行代理 DNS、真实出口和逻辑出口一致性验证。
- 前端目标列表包含“主连接”，协议状态和回滚状态正确，按钮改为“复制节点订阅”。

## 14. VPS 阶梯部署

每一级都先联合备份 Gateway 二进制/数据库、AimiliVPN 代码与状态、3x-ui SQLite、Xray 运行时配置和 UFW 规则；备份只保存在受限目录。

1. 部署 AimiliVPN 主事务契约，先用受控失败验证新主失败能恢复旧主，再执行一次真实主切换；验证 `7928`、主 mixed、`8443`、代理 DNS、真实出口和三个普通槽位不变。
2. 部署中性公网模型、协议状态表、root 协议助手和精确 UDP 白名单，但所有出口仍保持 TCP。
3. 选择一个普通出口执行 TCP → XHTTP，验证订阅、v2rayN、真实流量和其他出口连接持续；再切回 TCP 验证反向恢复。
4. 选择一个普通出口执行 VLESS → Hysteria2，完成外部 QUIC/TLS、代理 DNS、出口、证书、UFW/云边界和资源观察；失败立即回 TCP。
5. 在普通出口稳定后才允许主连接试切协议；主连接每次同时验证 `7928` 与主 mixed。
6. 最终验证混合状态：主连接 TCP/Vision、出口位 1 XHTTP、出口位 2 Hysteria2、出口位 3 TCP/Vision。四个逻辑出口数不变，公开入站总数仍为四，mixed 总数仍为四。
7. 重启恢复验证：受控重启 x-ui/Xray 后确认 3x-ui SQLite 能重建混合协议状态；随后重启 Gateway 和 AimiliVPN，确认未提交事务自动恢复、已提交模式不漂移。

任一级失败只回滚该级，不升级为服务器重建、3x-ui 全量清理或跨模块改造。

## 15. 完成标准

### 主连接

- 候选可选择“主连接”，当前主候选不重复显示为 standby。
- 成功后 `7928`、主 mixed 和 `8443` 当前协议均走新出口，三个普通槽位不变。
- 受控失败自动恢复旧主；恢复失败才为 `repair_required`。

### 独立协议

- 四个逻辑出口可独立选择三种模式，每个逻辑出口始终只有一个公网入站。
- 协议切换不改变 AimiliVPN 节点/槽位、mixed 地址、SOCKS5H 地址和真实出口。
- 非目标入站的连接在切换期间持续存在，Xray PID 不变。
- TCP、XHTTP、Hysteria2 都通过外部真实连接、代理 DNS、出口一致性和 v2rayN `7.24.4` 验证。
- 回滚恢复旧协议、认证、订阅和验证状态。

### 安全与资源

- 不删除非 Gateway 入站或全局客户端，不扩大管理后台暴露面。
- 只开放四个固定逻辑公网端口的 UDP 白名单，不使用 `443/UDP`，不开放 UDP 范围。
- 内存、Swap、进程峰值、重启恢复和证书续期路径均通过验证。
- 设计、日志、审计和验收记录保持脱敏；连接秘密只保存在既有受限运行配置或短期 root 事务快照中，不向用户输出。

## 16. 审核决策

批准本文即表示同意以下关键取舍：

1. 主连接使用跨 AimiliVPN/Gateway 的 stage、commit、rollback 事务，而不是一次性“切过去再补救”。
2. 协议切换使用同一 3x-ui 入站原位热替换，并引入无常驻进程的 root 窄事务助手；不接受会重载整个 Xray 的简化实现。
3. XHTTP V1 不启用 Vision/VLESS Encryption；Hysteria2 使用独立 Auth 和 Caddy 证书稳定路径。
4. 3x-ui 全局客户端保留并补齐协议凭据，不删除非 Gateway 客户端；统一订阅 URL 与逻辑名称保持稳定。
5. UDP 安全边界为四个固定逻辑公网端口，不占 `443/UDP`，不开放范围。

本文获批后，下一步是编写详细实施计划；随后按 TDD 先完成本地实现与故障注入验证，再按第 14 节执行 VPS 阶梯部署。
