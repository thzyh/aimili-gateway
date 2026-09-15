# 出口可见性、中文反馈与动态订阅别名设计

日期：2026-09-01

状态：已批准（方案 1，包含 2026-09-01 用户补充）

## 1. 目标

本增量采用 Gateway、AimiliVPN、3x-ui 三仓库协同修复，在不增加实际运行出口的前提下完成：

1. 主连接可以安全切换 TCP/Vision、XHTTP/REALITY、Hysteria2；状态漂移时先同步真实身份，再执行原有严格预检。
2. 协议切换、国家补充和出口替换使用分区明确、可关闭、颜色可辨的中文反馈。
3. 已启用与未启用节点都显示可核实的 IP；优先显示拨号实测的公网出口 IP，同时明确区分 VPN 节点入口 IP。
4. VPN 节点池只显示公网端口；SOCKS5H 代理池只显示 mixed 端口。
5. 订阅中四个节点的别名随逻辑出口和国家变化，例如 `主连接_日本`、`出口位 1_日本`。
6. 明确 Trojan/WS、VLESS/WS 和自动选协议的后续边界。

本设计继承并补充 `2026-08-29-main-switch-protocol-modes-design.md` 与 `2026-08-31-stable-runtime-order-and-country-cache-design.md`。发生冲突时，以本设计中明确修改的显示、反馈、身份同步和别名规则为准。

## 2. 不变约束

- 始终只有主连接、出口1、出口2、出口3四个逻辑运行出口。
- 每个逻辑出口同时只有一个公网节点协议配置。
- mixed/SOCKS5H 不随公网协议切换，端口和凭据生命周期不因本增量改变。
- 不恢复 `21000`、balancer、observatory 或其他聚合入口。
- Gateway 只修改 `agw-` 受管资源及专属订阅客户端，不删除、不重命名、不接管非 Gateway 3x-ui/Xray 资源。
- 不输出或提交密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- API 继续返回稳定英文错误码供程序判断；中文只用于经过闭集映射的界面文案。

## 3. 已核实事实与第一失败边界

### 3.1 主连接协议切换

第一处明确失败位于 Gateway 协议事务预检，而不是 Xray/helper：

- AimiliVPN 主连接和 `7928` mixed 实际健康，国家与类型匹配。
- Gateway 数据库保留的主候选身份和出口 IP 落后于 AimiliVPN 当前运行状态。
- `verifyEgressReady()` 对候选身份、国家、类型、出口 IP 和 mixed 结果执行严格比对，因旧身份返回 `egress_unavailable`。
- 失败发生在任何 3x-ui/Xray 写入前，因此不是 REALITY、XHTTP、Hysteria2 参数或全入站重载导致。
- 现有 `checkMain(ctx, true)` 已能用“当前公网协议 + mixed + 主出口”实测结果同步 Gateway 主身份。

结论：修复应复用已验证的主连接检查并收敛数据库，不能放宽 `verifyEgressReady()`，更不能在身份不一致时强行写 Xray。

### 3.2 国家补充失败

- 阿根廷当次官方候选为 1，检测 1，通过 0。
- 美国当前有效缓存为 0；系统只保存最近一次刷新摘要，不能反推并展示不存在的逐项历史证据。
- AimiliVPN 后台把异常统一压缩为 `refresh_failed`，Gateway 又直接显示代码和国家代码，用户只能看到“US 刷新失败”。

结论：刷新结果必须返回结构化原因和计数；页面将国家代码转为中文国家名，但不伪造缺失的历史探测细节。

### 3.3 替换失败不可见与坏候选残留

- 当前替换失败写入页面顶部 `notice`。
- 替换弹窗仍覆盖在页面上方，用户看不到顶部信息。
- AimiliVPN 明确拨号或出口检测失败时只写槽位进程内冷却，未同步持久节点池；刷新页面后坏候选仍可能出现。

结论：替换错误在弹窗内显示；只有 AimiliVPN 已证明候选自身拨号或出口失败时，才持久标记不可用并从普通候选集合移除。

### 3.4 订阅别名来源

对固定生产版本 3x-ui v3.7.0 源码的核对结果：

- `internal/database/model/model.go` 中 `ClientInbound` 只有 `client_id`、`inbound_id`、`flow_override`。
- `internal/sub/service.go` 的 `genRemark()` 默认以入站 `Remark` 生成订阅名称。
- Gateway 当前使用一个 `aimili-gateway-subscription` 客户端关联四个受管公网入站。

直接修改入站 `remark` 会同时影响其他关联客户端，不满足最小作用域。正式方案是在 `client_inbounds` 上增加默认空的逐关联别名覆盖。

## 4. 总体架构

```text
AimiliVPN 探测与运行状态
  ├─ candidate_ip：VPN 节点入口 IP
  ├─ exit_ip：临时/正式拨号后的公网实测 IP
  ├─ 结构化国家刷新结果
  └─ 持久候选失效状态
             │ 安全字段
             ▼
Gateway 严格收敛与事务
  ├─ 主连接 checkMain(true) 身份同步
  ├─ verifyEgressReady 严格预检
  ├─ 中文闭集映射与分区通知
  ├─ 页面相关的单端口展示
  └─ 逻辑出口 + 国家 → 订阅别名映射
             │ 仅专属客户端的关联别名
             ▼
3x-ui client_inbounds.alias_override
  └─ 订阅渲染时非空覆盖 inbound.remark
```

## 5. IP 语义与显示规则

### 5.1 两类 IP

| 字段 | 含义 | 取得方式 | 能否称为出口 IP |
| --- | --- | --- | --- |
| `candidate_ip` | VPNGate/OpenVPN 服务器入口地址 | 官方目录和配置标准化 | 不能 |
| `exit_ip` | 经过该 VPN 隧道访问公网回显服务得到的地址 | 临时探测或运行 mixed 实测 | 可以 |

AimiliVPN 原页面中未启用节点的 `ip` 是节点入口地址。它是真实 IP，但不一定等于连接后的公网出口 IP。Gateway 不得为了填满表格把它冒充为 `exit_ip`。

### 5.2 未启用候选的出口实测

现有候选精验已为每个候选短暂建立测试 OpenVPN 隧道，但只确认握手。增量在握手成功后、销毁临时隧道前：

1. 通过精确测试 TUN 接口访问两个固定公网 IP 回显端点；
2. 只接受格式合法的 IPv4/IPv6 文本；
3. 成功写入 `exit_ip` 与 `exit_ip_checked_at`；
4. 两个端点均失败则本次候选精验失败，不进入“真实可用”缓存；
5. 无论成功或失败都停止临时 OpenVPN 进程并回收测试 TUN。

这不会增加长期运行出口数量，也不开放新端口。影响是每个新候选多一次短公网请求，单候选刷新耗时可能增加最多约 6 秒；并发仍受现有探测上限约束。

### 5.3 兼容与页面行为

- 新探测成功的候选和运行节点都优先显示 `exitIp`，列名为“出口 IP”。
- 升级前已有缓存可能暂时没有 `exit_ip`。迁移期页面显示 `candidateIp` 并加“节点 IP”标记，不显示“等待出口”，也不把它标成出口 IP。
- 普通维护逐步复验并回填旧缓存；手动刷新成功的节点必须已取得 `exit_ip`。
- IP 只在已登录的个人管理员页面显示，不进入日志、审计详情或订阅别名。

## 6. 主连接安全同步

主连接协议切换的预检顺序调整为：

1. 读取 Gateway 当前主记录和 AimiliVPN 主快照。
2. 若目标是 `agw-main`，调用现有 `checkMain(ctx, true)`。
3. `checkMain` 必须完成当前公网协议、主 mixed 和实际出口验证，只有三者都通过才持久化候选身份、国家、类型和出口 IP。
4. 重新读取已持久化主记录并构造 `protocolTarget`。
5. 执行原有 `verifyEgressReady()` 全字段严格比对。
6. 通过后才允许快照和写入 3x-ui/Xray；失败时写安全错误码并保持原协议。

槽位出口继续使用正式 slot check 收敛，不为统一代码路径而绕过槽位身份检查。同步失败不能清空旧身份，也不能把不可验证状态写成 ready。

## 7. 中文反馈与分区通知

### 7.1 统一类型

前端使用单一结构：

```ts
type NoticeKind = 'success' | 'error' | 'progress' | 'info'
type UiNotice = { id: string; kind: NoticeKind; title: string; message: string }
```

- 成功：绿色；失败：红色；进行中：蓝色；普通说明：中性色。
- 所有通知有可访问的关闭按钮和 `aria-label="关闭提示"`。
- 不用同一种颜色表达成功与失败。
- 未知错误码显示安全通用中文，不拼接原始响应正文。

### 7.2 三个显示区域

| 操作 | 显示区域 | 原因 |
| --- | --- | --- |
| 协议切换、全局同步、复制 | 页面标题下方顶部通知 | 属于全页面操作 |
| 手动补充国家 | 国家筛选栏下方独立刷新结果卡 | 与国家目录和计数直接关联 |
| 出口替换 | 替换弹窗内部 | 弹窗打开时仍能立即看到结果 |

替换成功后关闭弹窗并显示顶部成功通知；替换失败时保留弹窗、保留用户所选目标并显示红色弹窗错误。

### 7.3 闭集中文映射

至少覆盖：

| 错误码 | 中文文案 |
| --- | --- |
| `egress_unavailable` | 当前出口不可用或身份已经变化，请同步状态后重试 |
| `operation_busy` | 当前有维护或切换任务正在进行，请稍后重试 |
| `maintenance_busy` | 节点维护正在进行，请稍后重试 |
| `no_official_candidates` | 该国家当前没有官方候选节点 |
| `no_usable_nodes` | 检测完成，但没有找到可用节点 |
| `upstream_unavailable` | 官方节点服务暂时不可用，请稍后重试 |
| `candidate_dial_failed` | 该节点无法建立 VPN 连接，已从可用候选中移除 |
| `candidate_egress_failed` | 该节点无法访问公网，已从可用候选中移除 |
| `request_failed` | 请求未完成，请稍后重试 |

国家显示使用 Gateway 已有国家目录名称；例如 `US` 显示“美国”，未知代码显示“未知国家（代码）”。

## 8. 结构化刷新结果与候选淘汰

AimiliVPN 的 `CountryRefresh` 增加稳定结果语义：

- `resultCode`: `success`、`no_official_candidates`、`no_usable_nodes`、`operation_busy`、`maintenance_busy`、`upstream_unavailable`。
- `officialCount`、`testedCount`、`usableCount`、`retainedCount`、`cacheTotal`。
- `country`、`startedAt`、`finishedAt`。

旧 `errorCode` 在一个兼容周期内保留，但 Gateway 优先使用 `resultCode`。后台异常必须分层映射，不把异常字符串返回浏览器。

候选失效遵循单一证据边界：

- AimiliVPN 明确返回 `candidate_dial_failed` 或 `candidate_egress_failed`：原子写入 blacklist 和节点池不可用状态，立即重平衡，候选不再出现在普通可用列表。
- Gateway API、3x-ui、Xray、订阅生成或浏览器失败：不得淘汰候选，因为这些故障没有证明 VPN 节点自身失效。
- 失败候选仍遵循已有冷却和后续复验规则，不永久删除官方目录记录。

## 9. 页面端口规则

同一逻辑出口的两个端口仍存在，但按页面只显示用户当前需要的一个：

| 页面 | 主连接 | 出口1～3 | 列名 |
| --- | ---: | ---: | --- |
| VPN 节点池 | `8443` | `20000`～`20002` | VPN 节点端口 |
| SOCKS5H 代理池 | `31000` | `30000`～`30002` | SOCKS5H 端口 |

前端选择规则：VPN 页面用 `publicPort ?? vlessPort`；SOCKS5H 页面用 `mixedPort`。不在同一格同时显示“公网”和“mixed”。候选未装载到运行出口时端口显示 `—`。

此变化只影响展示，不修改监听、路由、防火墙、订阅或凭据。

## 10. 动态订阅别名

### 10.1 目标格式

| 逻辑出口 | 国家 | 别名 |
| --- | --- | --- |
| 主连接 | 日本 | `主连接_日本` |
| 出口1 | 日本 | `出口位 1_日本` |
| 出口2 | 美国 | `出口位 2_美国` |
| 出口3 | 韩国 | `出口位 3_韩国` |

国家变化后更新别名；协议变化不改变别名。稳定入站 ID、tag、端口、订阅客户端身份和订阅地址均不改变。

### 10.2 3x-ui 最小扩展

固定 v3.7.0 源码增加：

- `ClientInbound.AliasOverride string`，数据库列 `alias_override`，默认空。
- 已认证客户端 API 读取和原子设置某个 client 的 `{inboundId: alias}` 映射。
- 订阅请求按 `sub_id` 加载当前关联别名；`alias_override` 非空时覆盖 `inbound.Remark`，为空时完全保持官方行为。
- 原始订阅、JSON 和 Clash 共用同一覆盖规则。
- 别名最大 96 个 Unicode 字符；拒绝控制字符、换行和空白首尾。

该变更以固定源码补丁和可复现构建资产保存在 `aimili-3xui-deploy`，不直接编辑生产数据库。GORM `AutoMigrate` 只增加可空/默认空列；回退旧二进制时多余列被忽略。

### 10.3 Gateway 所有权限制

Gateway 的 `SubscriptionDesired` 增加 `Aliases map[int64]string`。适配器只允许：

- 客户端 email 精确为 `aimili-gateway-subscription`；
- 入站 ID 位于 `ownedPublicIDs()` 返回的 Gateway 公网入站集合；
- 别名逻辑角色只能是 `主连接` 或 `出口位 1..3`；
- 国家名称来自当前已验证 Gateway/AimiliVPN 状态。

任何额外入站 ID、所有权冲突或返回映射不一致都中止事务，不触碰非 Gateway 资源。

### 10.4 更新与回滚事务

候选替换会改变国家，因此别名属于替换事务的一部分：

1. 快照目标 AimiliVPN 身份、Gateway 组记录、受管入站关联和当前别名。
2. 执行目标出口替换并验证公网协议、mixed、真实出口及其他三个出口。
3. 生成四个逻辑别名，调用 3x-ui 设置专属客户端关联别名。
4. 重新读取别名和订阅，验证四个入站一一对应且名称正确。
5. 全部通过后提交 Gateway 状态。
6. 任何一步失败，按“别名 → 受管入站/关联 → Gateway 状态 → AimiliVPN 身份”逆序恢复；恢复后重新验证。
7. 恢复不完整则标记 `repair_required`，不得报告替换成功。

普通协议切换不改变国家，但订阅重建仍验证别名覆盖未丢失。周期 reconcile 可以修复缺失别名，但只能写专属客户端关联。

## 11. 协议扩展结论

本轮不加入 Trojan/WS 和 VLESS/WS：

- 当前 TCP/Vision、XHTTP/REALITY、Hysteria2 已覆盖直连 TCP、类 HTTP 和 QUIC 三类主要网络特征。
- VLESS/WS 的主要新增价值是 CDN/特定网络兼容；当前没有明确场景证据。
- Trojan/WS 还会引入独立密码生命周期和更多订阅/回滚分支，收益不足以抵消复杂度。

本轮也不实现“根据 VPN 节点自动选择公网协议”。公网协议质量主要由客户端到 VPS 的路径决定，而不是 VPS 后方的 VPN 出口节点。自动推荐若后续实施，应基于客户端侧分协议测速；服务端继续做切换前后真实协议检测，不能把它冒充为客户端体验预测。

## 12. 安全、测试与部署边界

### 12.1 测试

- AimiliVPN：候选临时 TUN 出口实测、进程清理、结构化刷新、持久淘汰和旧缓存兼容。
- Gateway Go：主身份同步后严格预检、字段转换、候选淘汰边界、别名所有权、更新和回滚。
- Gateway Vue：两页面单端口、IP 类型标记、三类通知区域、颜色、关闭按钮、中文错误和中文国家名。
- 3x-ui：数据库迁移、原始/JSON/Clash 别名覆盖、空值兼容、API 校验和非目标客户端不变。
- 集成：替换日本为韩国后订阅名称变化，四个入站 ID/tag/端口和 mixed 全部不变。

### 12.2 阶梯部署

部署按 AimiliVPN → 定制 3x-ui → Gateway 后端 → Gateway 前端 → 客户端验收分级进行。每一级先备份、离线校验和本级验证，失败只回滚本级及其未提交事务。正式部署不得重跑会接管整机的旧初始安装器。

3x-ui 部署前后必须比较非 Gateway 资源的脱敏指纹；AimiliVPN 和 Gateway 部署前后必须确认仍只有四个逻辑运行出口、四个公网入站、四个 mixed 和单 Xray 进程。

## 13. 完成标准

只有同时满足以下条件才可宣布增量完成：

1. 主连接从当前协议切换到另两种协议再切回，预检不再因可安全收敛的旧身份失败；真实不可用时仍被严格拒绝。
2. 所有用户可见操作不再显示裸错误码或英文国家代码；成功/失败/进行中颜色清晰且可关闭。
3. 替换失败在弹窗内可见；被 AimiliVPN 证明失效的候选持久退出可用列表。
4. 已启用节点显示实测 `exit_ip`；未启用健康候选也有探测所得 `exit_ip`，迁移缺口明确标成“节点 IP”。
5. VPN 节点池只显示公网端口，SOCKS5H 代理池只显示 mixed 端口。
6. v2rayN 刷新后四个别名与逻辑出口和国家一致，替换国家后名称随之变化。
7. 四个公网节点、四个 mixed、代理端 DNS、真实出口、重启和 300 秒观察均通过。
8. 非 Gateway 3x-ui/Xray 资源脱敏指纹不变；没有增加运行出口、长期入站、凭据或订阅入口。
