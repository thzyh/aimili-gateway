# 3x-ui 升级、在线容量扩展与按国家刷新设计

日期：2026-08-28

状态：待用户审核

涉及项目：`aimili-gateway`、`aimili-vpngate`、`aimili-3xui-simple-deploy`

目标环境：`ny.zouyunhui.cc.cd`，Ubuntu，512 MiB VPS

## 1. 背景与目标

本次增量在现有“统一控制台＋AimiliVPN＋3x-ui 专家模式”架构上完成三项工作：

1. 将生产 3x-ui 从 `v3.6.0` 受控升级到官方稳定版 `v3.7.0`，确认 Gateway 适配器、统一账户同步、自动进入专家后台和既有代理数据面不受影响。
2. 在 512 MiB VPS 上把 Gateway 在线代理组容量从 1 逐级尝试提升到 2、再到 3，并为多个在线节点提供按当前筛选条件聚合复制地址的能力。
3. 在 AimiliVPN 中增加按国家刷新候选节点的版本化控制能力，使昂贵的 OpenVPN 测试只作用于所选国家，并在 Gateway 中提供对应操作和状态。

本次是一次固定目标版本的受控更新，不建立无人值守的未来自动升级计划。以后出现新的 3x-ui 版本时仍需重新做版本、迁移和合同检查。

## 2. 已核对基线

### 2.1 本地项目

- `aimili-gateway`：`main`，提交 `f24e156`；原有未跟踪 `.go-cache/` 保持不动。
- `aimili-vpngate`：`custom`，提交 `7ddb470`，领先 `origin/custom` 11 个提交。
- `aimili-3xui-simple-deploy`：不是 Git 仓库；首次安装仍固定 3x-ui `v3.6.0`，已有安装不会被部署脚本降级。

### 2.2 生产 VPS

- `aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 均为 active。
- 总内存约 458 MiB，可用内存约 196 MiB；Swap 1 GiB，已使用约 152 MiB。
- Gateway、AimiliVPN、3x-ui 当前内存分别约为 16 MiB、30 MiB、122 MiB。
- 当前有两个 OpenVPN 进程：主连接和一个受管槽位，总 RSS 约 16 MiB。
- 根分区剩余约 4.5 GiB；`/etc/x-ui` 约 2.8 MiB，`/usr/local/x-ui` 约 255 MiB。
- 当前 `maxProxyGroups=1`，AimiliVPN `MAX_EXIT_SLOTS=4`，OpenVPN 节点测试并发为 1。

### 2.3 3x-ui v3.7.0

- 官方稳定版 `v3.7.0` 发布于 2026-08-24，发布提交为 `f727d04f6522bb94a8fb52e8352fdcafb51c11e1`。
- 官方 amd64 发布资产大小约 80 MiB；GitHub 发布元数据提供 SHA-256 摘要，部署时必须逐字节校验。
- Gateway 当前使用的 `csrf-token`、`login`、设置、入站、Xray 和 X25519 API 路径在 v3.7.0 中仍存在。
- v3.7.0 启动时会执行数据库自动迁移，包括登录会话 epoch 和 API token scope/expiry 相关 schema 变更。
- `updateUser` 增加 TOTP 验证；3x-ui 自身 TOTP 关闭时空验证码合法，符合当前“Gateway TOTP 可选、3x-ui TOTP 关闭”的统一账户设计。

### 2.4 VPNGate 国家参数验证

VPNGate 官方 iPhone API 不提供可用的国家过滤参数。对无参数、`country=JP` 和 `CountryShort=JP` 的只读请求返回了相同字节数和相同 SHA-256，说明查询参数被忽略。

因此按国家刷新必须继续下载完整 CSV，但要在 OpenVPN 配置解码、TCP 预筛和真实拨号测试之前按 `CountryShort` 过滤。CSV 下载不是主要资源瓶颈，OpenVPN 精验才是。

## 3. 范围边界

### 3.1 本次包含

- 固定升级生产 3x-ui 到 `v3.7.0`，带备份、合同验证和整体回滚。
- AimiliVPN 按国家刷新任务、状态、国家目录和节点池局部合并。
- Gateway AimiliVPN 适配器、维护服务、HTTP API 和前端刷新交互。
- 在线组实际出口 IP 去重。
- 容量 1→2→3 阶梯测试和自动止损。
- VLESS、SOCKS5H 当前过滤结果的聚合复制。
- 本地自动测试、Linux 验证、生产端到端验收和验证记录更新。

### 3.2 本次不包含

- 不重写 Xray、3x-ui 或 AimiliVPN 核心架构。
- 不把 Python 与 Go 服务合成单进程。
- 不删除 3x-ui 原后台功能或改变专家模式入口。
- 不把相同密码、自动代登录或反向代理描述为真正 SSO。
- 不建立定时自动升级 3x-ui 的后台任务。
- 不尝试生产容量 4，也不提高 OpenVPN 节点测试并发。
- 不引入 Redis、消息队列或额外常驻刷新服务。

## 4. 总体架构

继续保持三个独立进程：

```text
浏览器
  │
  ▼
Aimili Gateway
  ├─ 3x-ui 适配器 ──► 3x-ui / Xray
  └─ AimiliVPN 适配器 ──► /control/v1（仅回环）──► AimiliVPN
                                                ├─ 全量 CSV 获取
                                                ├─ 国家过滤
                                                ├─ 串行节点精验
                                                └─ 局部合并 nodes.json
```

Gateway 负责账户边界、用户操作、聚合展示和代理组编排；AimiliVPN 负责候选获取、OpenVPN 验证和槽位生命周期；3x-ui 继续负责 Xray 入站、出站和路由。按国家刷新通过版本化控制 API 接入，不调用 AimiliVPN 原网页的未版本化接口。

## 5. 3x-ui v3.7.0 升级设计

### 5.1 固定版本与资产验证

- 只接受官方稳定标签 `v3.7.0`，拒绝 `latest`、`main`、`dev` 和 `dev-latest`。
- 部署前读取官方发布元数据，验证标签、非 draft、非 prerelease、目标架构、文件大小和 SHA-256。
- 下载和校验在停止服务前完成，避免把维护窗口耗在网络下载上。
- 使用官方发布资产和官方安装流程，不在 VPS 编译源码；v3.7.0 源码所需 Go 版本不影响二进制更新。

### 5.2 一致性备份

进入升级维护窗口后停止 `x-ui`，再创建同一时间点的完整回退包，至少包含：

- `/etc/x-ui`，包括 SQLite 数据库；
- `/usr/local/x-ui`，包括当前面板和 Xray 文件；
- `x-ui.service` 的实际 unit 与 drop-in；
- 当前文件权限、所有者、版本、摘要和监听基线。

备份必须是 root-only，验证归档可列出且关键文件存在，但不输出数据库、账户或配置秘密。磁盘预检要求至少 1 GiB 可用空间。

数据库迁移和新二进制必须作为一个整体提交或回滚：不得让 v3.6.0 二进制继续使用已经被 v3.7.0 迁移的数据库。

### 5.3 升级后合同验证

按以下顺序验证，前一项失败即停止后续写操作：

1. `x-ui` 启动成功，无数据库迁移错误，服务无重启循环。
2. Xray 启动，现有受管入站、出站和路由仍存在。
3. Gateway 能获取 CSRF、使用统一账户登录并读取设置、入站和 Xray 配置。
4. Gateway 能签发新的 3x-ui 浏览器会话，点击专家模式后直接进入后台。
5. 账户能力探测确认 3x-ui 自身 TOTP 关闭；只做凭据验证，不在升级验证中主动重置密码。
6. 现有 VLESS 和 SOCKS5H 地址继续通过真实数据面测试。
7. Gateway、AimiliVPN、3x-ui、Caddy 全部 active，内核无本次维护窗口之后的新 OOM。

Gateway 增加 v3.7.0 合同测试夹具。若真实响应结构仍兼容，则不为版本号制造无意义分支；若必需字段或会话行为变化，只在 3x-ui 适配器内修复。

### 5.4 回滚条件

出现以下任一情况立即回滚，不继续扩容或国家刷新部署：

- 3x-ui 启动、迁移或 Xray 启动失败；
- Gateway 关键读写合同失败；
- 自动进入 3x-ui 专家后台失败；
- 受管入站、出站或路由缺失；
- 现有 VLESS 或 SOCKS5H 数据面失败；
- 内存失控、OOM 或服务重启循环。

回滚时停止新版本，恢复 `/etc/x-ui`、`/usr/local/x-ui` 和 unit 的同一备份，再启动旧版本，并重复原有数据面和自动登录验证。

## 6. AimiliVPN 按国家刷新

### 6.1 控制 API

AimiliVPN `/control/v1` 新增能力：

- `candidate-countries.read`
- `candidates.refresh.country`
- `candidates.refresh.status`

新增接口：

- `GET /control/v1/candidates/countries`：返回最近一次官方 CSV 中观察到的国家代码、名称、候选数量和观察时间。
- `POST /control/v1/candidates/refresh`：请求体只允许 `country`，值为 ISO 两位大写代码；成功启动返回 HTTP 202。
- `GET /control/v1/candidates/refresh`：返回当前或最近一次刷新状态。

系统只允许一个刷新任务，不使用任务 UUID。状态包括：

- `state`：`idle`、`running`、`completed`、`failed`；
- `country`；
- `phase`：`fetching`、`filtering`、`prescreening`、`probing`、`merging`；
- `catalogCount`、`countryCandidateCount`、`testedCount`、`validCount`、`preservedCount`；
- `startedAt`、`finishedAt`；
- 脱敏 `errorCode`。

响应不包含 OpenVPN 配置、节点完整日志、异常原文、账户、令牌或连接秘密。

### 6.2 刷新流程

1. 非阻塞获取现有 `maintenance_lock`；全局 collector 或其他国家刷新运行时返回 `maintenance_busy`。
2. 下载完整官方 CSV，解析国家元数据并原子更新安全的国家目录快照。
3. 在候选数量上限和配置解码前按所选 `CountryShort` 过滤。
4. 排除仍在冷却期内的黑名单节点。
5. 优先复验该国已有有效节点，再测试新候选。
6. TCP 预筛后以 `OPENVPN_TEST_CONCURRENCY=1` 串行进行真实出口验证。
7. 单国目标为 5 个有效节点；一次最多精验 20 个候选，达到目标或上限即停止。
8. 原子合并节点池、更新黑名单和配置文件，记录脱敏统计并释放锁。

### 6.3 局部合并规则

按国家刷新不得用所选国家结果覆盖整个 `nodes.json`：

- 其他国家的已有节点原样保留。
- 主 OpenVPN 当前活动节点和所有受管槽位引用的节点均为受保护节点。
- 所选国家的非受保护旧节点由本轮成功结果更新；失败节点进入冷却黑名单。
- 即使受保护节点本轮无法复验，也不因目录刷新而停止或删除其正在运行的隧道；槽位健康检查继续负责运行态判定。
- 写入使用临时文件、`fsync` 和原子替换；写入失败保持旧目录。

全局 collector 继续存在并负责长期补池。国家刷新和全局 collector 共用同一把维护锁，避免在 512 MiB VPS 上并发创建测试隧道。

## 7. Gateway 同步与前端交互

### 7.1 适配器与维护服务

Gateway AimiliVPN 适配器新增：

- 读取国家目录；
- 启动按国家刷新；
- 读取刷新状态；
- 对缺少能力、忙、超时和上游失败进行稳定错误映射。

当前只重新读取旧快照的 `RefreshAimiliVPN()` 被替换为真实控制 API 调用。Gateway HTTP 接口为：

- `GET /api/v1/settings/aimilivpn/countries`
- `POST /api/v1/settings/aimilivpn/refresh`
- `GET /api/v1/settings/aimilivpn/refresh`

POST 请求只接受 `country`，要求登录、同源、CSRF 和幂等键，成功返回 202。Gateway 后台等待 AimiliVPN 刷新完成后执行一次现有 `Reconcile()`，把新增有效候选同步为可启用的 standby 条目。Gateway 在等待期间重启时，不影响 AimiliVPN 任务；用户可通过“同步代理状态”补做 reconcile。

### 7.2 页面设计

VPN 节点池和 SOCKS5H 代理池共用以下交互：

- 国家下拉框使用最近官方目录，因此即使当前没有 ready 组，也能选择该国家刷新。
- 只有选中国家后才能点击“刷新所选国家”。
- 刷新期间显示阶段与统计，不显示原始日志；页面轮询间隔不小于 2 秒。
- 刷新完成后重新加载候选目录和代理组，不自动启用、停用或替换在线组。
- 现有“刷新节点池”改名为“同步代理状态”，准确表达它只做 Gateway reconcile。
- AimiliVPN 高级页面同步显示相同刷新状态和最近完成时间。

刷新页面或关闭浏览器不取消服务器后台任务。

## 8. 在线容量与出口唯一性

### 8.1 永久约束

- 生产目标上限最多为 3，不尝试 4。
- 节点创建、轮换和激活继续串行执行，不并发创建多个组。
- `MAX_EXIT_SLOTS=4` 保持不变，它只是槽位安全上限；实际 Gateway 上限由 `maxProxyGroups` 控制。
- 每个 ready 组必须有不同的实际出口 IP。候选 ID 或 VPNGate 服务器 IP 不作为最终唯一性依据。

### 8.2 去重行为

新组的槽位完成真实出口检测后，Gateway 将其 `ExitIP` 与其他 ready 组比较：

- 不重复：继续创建 3x-ui 入站、出站和路由。
- 重复：只轮换新槽位，最多尝试 3 次。
- 三次仍重复或没有其他候选：删除新槽位并返回 `duplicate_exit_ip`，不影响已有组。
- reconcile 发现历史 ready 组出口重复时保留较早稳定组，把后出现的组标记为 degraded；不会在后台无限自动轮换。

该检查位于 Gateway 编排层，因为只有 Gateway 能同时看到全部受管组和它们的实际出口结果。

### 8.3 阶梯扩容

每一级先修改 `maxProxyGroups` 并重启 Gateway，再只创建一个新组：

1. 从 1 提升到 2，创建第二个真实出口，完成数据面验证后观察 15 分钟。
2. 只有容量 2 全部通过时才提升到 3，再创建第三个出口并观察 15 分钟。
3. 容量 3 未通过时回退到 2；容量 2 未通过时回退到 1。

每 15 秒采样一次。任一条件触发回退：

- `MemAvailable` 连续两次低于 80 MiB；
- Swap 使用超过 512 MiB；
- 五分钟内 Swap 持续增加超过 64 MiB且未回落；
- 出现本阶段开始时间之后的新 OOM；
- 任一核心服务退出、重启计数增加或出现 failed unit；
- SSH 无法在 10 秒内完成只读探测；
- 任一已有 ready 组、VLESS 或 SOCKS5H 数据面失效。

回退只删除本阶新增的受管组并恢复前一容量配置，不删除原有正常组。

## 9. 多地址聚合复制

现有 `GET /api/v1/proxy-groups/export` 已能把所有符合过滤条件的 ready 地址按行返回。本次复用同一接口，不增加第二套秘密拼接逻辑。

两个池页面分别增加“复制全部可用地址”：

- VPN 节点池复制当前国家、IP 类型和状态过滤下的 VLESS 地址；
- SOCKS5H 代理池复制相同过滤条件下的 SOCKS5H 地址；
- 每行一个地址，顺序与当前列表一致；
- 只包含 ready 组；空结果不覆盖剪贴板并明确提示；
- 复制内容不写入 `localStorage`、`sessionStorage`、日志、分析事件或错误报告；
- 原有单行复制和文本文件导出继续保留。

## 10. 安全与错误处理

- AimiliVPN 控制 API 继续仅监听 loopback，并使用 systemd credential 中的 bearer token。
- Gateway 国家刷新写操作继续要求登录、CSRF、Origin 和幂等键。
- 国家代码采用严格两位大写白名单；请求体拒绝未知字段和超限长度。
- 所有错误对外只返回稳定错误码；原始上游响应和异常仅进入受限服务日志，且必须先脱敏。
- 3x-ui、AimiliVPN 和 Gateway 的账户模型不因本次任务改变；三个服务保持相同用户名和密码，Gateway TOTP 独立可选，3x-ui 自身 TOTP 保持关闭。
- 自动进入原后台仍是 Gateway 受控签发底层会话，不宣称覆盖全部页面的真正 SSO。
- 不输出密码、Cookie、UUID、私钥、随机后台路径、控制 token 或完整连接地址。

## 11. 测试设计

### 11.1 AimiliVPN

- CSV 国家过滤发生在候选数量截断和配置解码之前。
- 国家目录统计、空国家、无候选、黑名单和损坏配置。
- 单国目标 5、最多精验 20、批次并发 1。
- 保护主活动节点与所有受管槽位节点。
- 其他国家不变、所选国家局部替换、失败写入保持旧文件。
- 全局 collector 与国家刷新互斥，busy 状态稳定。
- 控制 API 鉴权、字段白名单、202、状态和脱敏错误。

### 11.2 Gateway 后端

- 新 AimiliVPN 能力和接口合同测试。
- 刷新启动、轮询、失败、busy、能力缺失和 Gateway 重启降级。
- 刷新成功后 reconcile 生成 standby 条目。
- 实际出口 IP 去重、三次轮换、补偿删除和历史重复降级。
- 聚合导出继续只包含 ready 组，不泄露到 JSON 列表接口。
- 3x-ui v3.7.0 登录、账户、会话、入站和 Xray 合同夹具。

### 11.3 前端

- 国家目录下拉、未选国家禁用、刷新阶段和完成状态。
- “同步代理状态”与“刷新所选国家”语义分离。
- VLESS、SOCKS5H 分别聚合复制并跟随过滤器。
- 空结果不覆盖剪贴板，API 失败显示稳定提示。
- 单行复制、导出、移动端布局和无浏览器持久化回归。

### 11.4 生产端到端

完成声明必须沿真实用户路径验证：

1. 登录 Gateway，打开 3x-ui 和 AimiliVPN 高级后台，确认自动进入。
2. 选择一个国家启动刷新，观察后台状态并确认其他国家和在线隧道不中断。
3. 刷新完成后看到 standby 候选并启用一个新组。
4. 验证每个在线组实际出口 IP 不同。
5. 分别通过 VLESS 和 SOCKS5H 访问外部出口检测服务。
6. 在两个池页面按过滤条件复制全部地址，并由独立客户端逐行验证。
7. 重启 Gateway，确认底层服务和已创建代理继续运行。
8. 核对资源阈值、服务重启计数、failed units 和本阶段之后的内核 OOM 日志。

## 12. 发布顺序与回滚边界

实施顺序固定为：

1. 本地 TDD 完成 AimiliVPN、Gateway 后端和前端功能。
2. 本地完整测试、构建和静态检查。
3. 生产只读预检并生成回退点。
4. 单独升级 3x-ui v3.7.0，完成合同和现有数据面验证。
5. 部署 AimiliVPN 新控制能力，验证旧槽位和新国家刷新接口。
6. 部署 Gateway，保持 `maxProxyGroups=1` 完成功能回归。
7. 执行一次低负载国家刷新。
8. 容量提升到 2并验收；通过后才尝试 3。
9. 更新验证记录并记录最终稳定容量。

每一阶段只回滚该阶段新增变更。3x-ui 失败恢复整套旧二进制和旧数据库；AimiliVPN 失败恢复旧 Python 源码和 service 配置；Gateway 失败恢复旧二进制、Web 资源、配置和数据库备份。任何一层回滚不得删除另外两个独立服务的数据。

## 13. 验收标准

本次任务只有同时满足以下条件才可声明完成：

1. 3x-ui 确认为官方 `v3.7.0`，Gateway 所需合同与自动进入专家后台通过真实验证。
2. 现有账户同步、VLESS、SOCKS5H、入站、出站和路由没有回归。
3. 按国家刷新只精验所选国家，其他国家和活动槽位不被删除或中断。
4. Gateway 显示真实刷新状态，完成后可见新 standby 候选。
5. 已完整执行容量 2 的阶梯测试；只有资源门槛全部通过时才保留 2并继续尝试 3，最终容量可以因安全回退为 1，但必须记录触发阈值和回退验证。
6. 所有 ready 组的实际出口 IP 唯一。
7. VLESS 和 SOCKS5H 均可聚合复制当前过滤结果，且逐行真实可用。
8. 四个服务 active、无新增 OOM、无异常重启、SSH 正常。
9. 本地仓库提交清晰，未覆盖用户原有未跟踪或无关文件。
10. 验证文档记录最新命令时间、结果、最终容量和任何已执行回滚，不以历史结果替代本次验收。

## 14. 审核门槛

本文覆盖本次增量的全部设计决定，没有待定项。用户批准本文后，下一步是编写详细实施计划；实施计划再次确认后，才开始业务代码、3x-ui 更新和生产阶梯部署。
