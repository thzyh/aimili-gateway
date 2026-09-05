# Gateway 免重启前端与安全自更新设计

日期：2026-09-05

状态：待用户审核

## 1. 背景

Aimili Gateway 当前使用 `//go:embed dist/*` 把 Vue 生产构建嵌入 Go 二进制。这个结构部署简单且有可靠兜底，但即使只修改文案、颜色或通知交互，也必须重新构建并短暂重启 Gateway。

3x-ui 的网页更新把下载预编译包、替换程序和重启服务封装成后台任务，减少了人工 SSH 操作；它不是生产前端热更新。Gateway 借鉴其操作体验，但不照搬下载并执行远端脚本、删除旧程序后再解压等高风险细节。

本设计采用两个按顺序实施、彼此独立验收的层级：

1. 外部前端版本目录加内嵌兜底，使后续纯 UI 发布和回滚无需重启 Gateway；
2. 权限封闭的后端自更新器，使符合安全闭集的 Gateway 后端版本可以一键升级、短暂重启并自动回滚。

第一层是完整两层发布机制的一部分，不是与第二层竞争的替代方案。

## 2. 目标

### 2.1 第一阶段目标

- 保留当前内嵌前端，任何外部资源异常时仍有可用管理入口。
- 纯 UI 发布使用带版本哈希的不可变目录，校验后原子切换，无需重启 Gateway、AimiliVPN、x-ui/Xray 或 Caddy。
- 只保留当前 UI 与上一版 UI，成功或失败后清理临时资产和更旧版本。
- 现有登录、CSRF、API 和 SPA 路由继续由 Gateway 提供，不让 Caddy 直接绕过 Gateway。
- 第一次启用外部前端能力最多只重启 Gateway 一次，实际代理数据面保持运行。

### 2.2 第二阶段目标

- 页面可以检查并应用固定可信发布源中的 Gateway 后端版本。
- VPS 不编译 Gateway，只下载 CI 已构建的 Linux/amd64 产物。
- 发布包必须通过签名、摘要、版本、API 和数据库兼容性校验。
- 只停止并启动 `aimili-gateway.service`；不得重启或改写 AimiliVPN、x-ui/Xray、Caddy及其配置。
- 保留唯一上一版 Gateway 二进制；新版本健康门失败时自动恢复。
- 普通自更新只接受控制面兼容版本；涉及协议、端口、数据库迁移或运行时资源写入的版本必须拒绝并回到现有阶梯部署流程。

## 3. 非目标

- 不实现 Go 代码热补丁、Go plugin 或进程内替换后端逻辑。
- 不实现无人值守自动安装；检查更新可以自动，应用必须由管理员显式触发。
- 不通过网页上传任意 JavaScript、二进制、Shell 脚本、URL、分支名或 Release 标签。
- 不开放新的公网管理端口，不扩大 SOCKS5H 来源，不改变固定公网端口或 mixed 端口。
- 不修改 Gateway 数据库、x-ui 数据库、AimiliVPN 节点数据、证书和连接凭据。
- 不把 AimiliVPN、3x-ui 或 Xray 更新合并到 Gateway 普通自更新事务。
- 不在 512 MiB VPS 上构建前端、Go 或 3x-ui。

## 4. 当前架构约束

- `internal/app/app.go` 在同一 `http.ServeMux` 上先注册 `/healthz`、`/api/v1/`，再把 `/` 交给 `webassets.Handler()`。
- `internal/webassets/embed.go` 内嵌 `dist`，对 `index.html` 使用 `no-cache`，对带哈希资产使用一年 immutable 缓存。
- Gateway systemd unit 以 `aimili-gateway` 非 root 用户运行，`ProtectSystem=strict`，没有 capability，只允许网络访问 localhost。
- `/usr/local/bin/aimili-gateway`、systemd unit 和 root-owned 发布目录不能由 Gateway 进程直接改写。
- 项目已经使用 protocol spool、systemd `.path` 和 root oneshot helper 完成封闭特权操作；自更新沿用相同信任边界，不提升 Gateway 主进程权限。

## 5. 总体架构

```text
CI 构建并签名
    │
    ├── UI 资产 ──> 低权限 fetcher 下载/初验 ──> 无网络 root installer 复验/安装
    │                                                       │
    │                                                       └── 原子切换 current，Gateway 不重启
    │
    └── 后端资产 ─> 低权限 fetcher 下载/初验 ─> 无网络 root installer 复验
                                                            │
                                                            └── 暂停 Gateway / 原子替换 / 健康门
                                                                        │
                                                                        └── 失败恢复唯一 previous

AimiliVPN/OpenVPN ─────────────────────────────────────────────────── 不重启
x-ui/Xray ─────────────────────────────────────────────────────────── 不重启
Caddy ─────────────────────────────────────────────────────────────── 不重启
```

发布分为 `ui` 和 `gateway` 两种类型。两种类型共用签名清单、状态记录、磁盘门和审计模型，但使用不同安装事务；任何发布不能同时声明两种类型。

## 6. 第一阶段：外部 UI 与内嵌兜底

### 6.1 目录结构

```text
/usr/local/share/aimili-gateway/web/
├── releases/
│   ├── <current-hash>/
│   │   ├── manifest.json
│   │   ├── manifest.sig
│   │   ├── index.html
│   │   └── assets/...
│   └── <previous-hash>/
├── current -> releases/<current-hash>
└── previous -> releases/<previous-hash>
```

- 目录、文件和软链接均由 root 拥有；Gateway 只有读取权限。
- `<hash>` 是规范化 `manifest.json` 的 SHA-256；manifest 已包含前端负载摘要，因此发布标识同时绑定元数据和实际内容，不接受任意目录名。
- `current` 和 `previous` 必须解析到 `releases` 的直接子目录；拒绝路径穿越、嵌套软链接、特殊文件和越界真实路径。
- 临时解压位于同一文件系统的 `releases/.staging-<run-id>`，校验完成后使用原子 rename 落盘。
- 稳定后只保留 `current`、`previous` 两个发布目录；没有 previous 时允许只有 current。

### 6.2 UI manifest

`manifest.json` 至少包含：

- `schemaVersion`：固定为 `1`；
- `kind`：固定为 `ui`；
- `version`：用户可读版本；
- `commit`：构建提交；
- `payloadSha256`：独立 `ui.tar.gz` 负载摘要；
- `payloadBytes`：压缩负载字节数；
- `files`：负载解压后相对路径、字节数和 SHA-256 闭集；
- `apiCompatibility`：该 UI 支持的 Gateway API 主版本闭区间；
- `builtAt`：UTC 构建时间；
- `manifest.sig`：CI 对规范化 `manifest.json` 字节生成的 Ed25519 分离签名。

每个 UI Release 在发布源中固定为三个独立资产：`manifest.json`、`manifest.sig`、`ui.tar.gz`。清单和签名不放入负载，避免清单记录自身压缩包摘要形成循环依赖。负载相对路径只允许 `index.html` 和 `assets/` 下的普通文件；安装完成后 updater 把已验证的清单和签名一并写入版本目录。单文件、文件数、压缩大小和总展开大小均设置上限，防止压缩炸弹与磁盘耗尽。

### 6.3 Gateway 静态资源选择

`webassets.Handler` 扩展为双来源：

1. 外部 `current` 存在、边界合法、manifest 兼容且必需文件可读时，提供外部版本；
2. 任一检查失败时提供编译期内嵌版本，并只记录脱敏错误类别；
3. `index.html` 始终 `no-cache`；带内容哈希的资产保持 `public, max-age=31536000, immutable`；
4. 旧页面在切换后请求旧哈希资产时，按 `current → previous → embedded` 顺序查找，避免正在使用的浏览器因切换得到 404；
5. SPA fallback 只对没有文件扩展名的前端路由返回 index；真实缺失的静态资产不得伪装成 index。

Gateway 不在每个请求中重新计算全部摘要。完整签名和摘要由低权限 fetcher 初验、root installer 在安装前再次校验；Gateway 每次解析发布指针时只验证真实路径、manifest 结构、API 兼容和必需文件。root installer 原子切换软链接后，新请求立即看到新版本，无需信号或进程重启。

### 6.4 UI 发布事务

固定顺序：

1. 本地或 CI 运行前端测试和生产构建；
2. 先生成只含 `index.html` 与 `assets/` 的 `ui.tar.gz`，再生成文件闭集、负载摘要、manifest 和分离签名；
3. 低权限 fetcher 检查可用空间至少为压缩包大小三倍加 64 MiB；
4. fetcher 从固定来源下载到专用 staging；阶段 A 的手工发布则由现有安全上传流程把三个资产放入一次性 root staging；
5. fetcher 完成初验，root installer 使用禁止网络的独立进程重新校验签名、摘要、普通文件、路径、大小、API 兼容和全部引用资产；
6. 原子落盘新 release；
7. 先用临时软链接加 rename 把旧 `current` 原子记录为 `previous`，再以相同方式原子切换 `current`；任何时刻对请求生效的 `current` 都指向一个完整目录；
8. 通过回环地址检查 index、manifest 和 index 引用的全部入口资产；
9. 成功后删除比 previous 更旧的版本和 staging；失败时切回旧 current 并删除失败版本。

行为性 JavaScript 缺陷不一定能被 HTTP 检查识别，因此必须保留不依赖网页的 root helper 回滚命令。任何自动失败只回滚本次 UI 指针，不改后端或数据面。

### 6.5 首次启用

首次部署包含新的双来源 handler 和 systemd 只读路径，需要按现有 Gateway 安装器完成一次二进制替换和 Gateway 重启。部署前把与新二进制内嵌内容相同的 UI bundle 放入 `current`，部署后验证：

- Gateway `/healthz`；
- 登录页和两个入口资产；
- Gateway 数据库只读检查；
- 四项服务仍 active；
- Xray 和 OpenVPN 进程数量不变；
- 四条协议状态与受管资源指纹不变。

首次启用不得调用 AimiliVPN、3x-ui 写 API或协议 helper。失败只恢复 Gateway 二进制、systemd unit 和 UI 指针。

## 7. 第二阶段：后端一键安全升级

### 7.1 权限模型

Gateway 主进程保持非 root 和无 capability。下载与安装拆成两个权限域：

- `update-spool/requests`：Gateway 只能创建闭集请求；
- `update-spool/results`：root installer 写、Gateway 只读；
- `aimili-gateway-update-fetch.path/service`：监视闭集请求，以专用无登录低权限用户运行，允许出站 HTTPS，只能写 updater staging 和初验状态；
- `aimili-gateway-update-install.path/service`：只监视已下载标记，以 root oneshot 运行，`IPAddressDeny=any`，只能读取 staging，并写固定安装目标、发布目录和结果目录；
- `/etc/aimili-gateway/updater.json`：`root:aimili-gateway-updater` 0640，保存固定发布源、重定向 host 闭集和允许通道，不包含下载凭据；
- fetcher systemd credential：可选的最小只读发布下载凭据，不进入环境变量、日志、Gateway 配置或 root installer。

低权限 fetcher 写入的 staging 和“下载完成”标记均视为不可信输入。root installer 必须以禁止跟随软链接的方式打开普通文件、重新检查真实路径/属主/链接数/大小，并独立复验签名和摘要后才允许安装；不能信任 fetcher 已给出的布尔结论。

网页请求只能选择后端报告的一个已签名版本 ID，不能提交 URL、文件路径、分支、命令、校验和或发布通道。

### 7.2 发布来源与签名

- 只有低权限 fetcher 可以访问配置中固定的 HTTPS manifest origin 和明确的重定向 host 闭集；root installer 完全禁止网络。
- CI 使用独立 Ed25519 私钥签名规范化 manifest；VPS fetcher 和 root installer 都只内嵌公钥并分别验证。
- 下载凭据使用最小只读权限并通过 systemd credential 注入；日志不得出现请求头、查询令牌或完整下载 URL。
- 签名、SHA-256、文件大小、目标平台、构建提交、版本单调性任一不符即拒绝。
- 不下载或执行远端 Shell 脚本。每个后端 Release 固定为三个独立资产：Gateway 二进制、`manifest.json` 和 `manifest.sig`；manifest 记录二进制摘要，不把三者再次封入会造成自摘要循环的包。配套 UI 作为独立 `ui` 发布先行安装，后端 manifest 只引用允许的 UI 兼容区间，不在同一发布事务内混装。

### 7.3 Gateway manifest

后端 manifest 至少包含：

- `schemaVersion: 1`；
- `kind: gateway`；
- `version`、`commit`、`builtAt`；
- `platform: linux-amd64`；
- 二进制 SHA-256 和字节数；
- `apiVersion`；
- `minDatabaseSchema`、`maxDatabaseSchema`；
- `impactClass: control-plane-only`；
- 可选配套 UI 的版本哈希与兼容区间；
- 对应的 `manifest.sig` 分离签名。

第一版 updater 只接受 `impactClass=control-plane-only` 且当前数据库已经处于兼容区间的包。需要数据库迁移、systemd 权限扩大、Caddy 修改、协议 helper 修改、端口或运行时资源变化的包必须返回 `manual_staged_deploy_required`。

### 7.4 后端升级事务

固定顺序：

1. 拒绝并发更新，分配不可猜测但不含秘密的 run ID；
2. 检查四项服务、Gateway 数据库 quick check、唯一回滚位置和 update/protocol transaction 空闲，并要求可用空间至少为后端包大小三倍加 128 MiB；
3. 低权限 fetcher 把 manifest、分离签名和二进制分别下载到 updater staging 并完成初验；root installer 复制到同文件系统的 root-only staging 后再次离线验证；
4. root installer 对新二进制运行 `version`、`config validate` 和数据库兼容只读检查；不得在影子检查中迁移生产数据库；
5. 记录当前 Gateway 二进制为唯一 `previous`，不复制 Gateway 数据库、x-ui 数据库、配置和证书；
6. 停止 `aimili-gateway.service`，原子替换二进制，立即启动 Gateway；
7. 在限定时间内验证 `/healthz`、Gateway 数据库只读、管理 API 版本、四项服务、Xray/OpenVPN 数量和四条协议状态未变化；
8. 成功后保留上一版二进制作为唯一回滚，删除 staging 和更旧备份；
9. 任一失败时停止 Gateway、恢复 previous、重新启动并执行相同健康门；
10. 回滚也失败时保留状态和两个二进制，不继续清理，不触碰数据面，并输出脱敏的 `repair_required` 运维入口。

升级窗口只影响 Gateway 页面和控制 API。AimiliVPN/OpenVPN 与 x-ui/Xray 继续运行，现有 v2rayN 代理连接不依赖 Gateway 进程。

### 7.5 API 与页面

管理员 API 使用现有登录、CSRF 和审计机制：

- `GET /api/v1/system/updates`：返回当前后端/UI 版本、可用版本和兼容性；
- `POST /api/v1/system/updates/ui/{version}/apply`：请求 UI 切换；
- `POST /api/v1/system/updates/gateway/{version}/apply`：请求后端升级；
- `POST /api/v1/system/updates/ui/rollback`：回退 UI；
- `POST /api/v1/system/updates/gateway/rollback`：回退 Gateway；
- `GET /api/v1/system/updates/{runId}`：读取脱敏状态。

所有 POST 必须重新认证管理员。页面明确显示“仅更新界面，不影响节点”或“Gateway 控制面将短暂重启，代理节点继续运行”。通知复用 `UiNotice`，处理中、成功、失败可区分且可关闭；不显示内部路径、URL、响应正文或英文错误码。

后端升级开始后，原页面轮询可能暂时失败；页面使用退避重试，Gateway 恢复后用 run ID 读取 root helper 结果，不把连接中断误报为升级失败。

## 8. 状态、幂等与并发

- UI 与后端更新共享一把全局 update lease，同一时间只允许一个事务。
- 相同 run ID 和相同版本重复提交返回现有结果；相同 run ID 指向不同请求时拒绝。
- updater 状态使用原子写入的 root-owned JSON；低权限 fetcher 只能写独立初验状态。对 Gateway 可见的最终状态闭集为 `pending`、`downloading`、`validating`、`switching`、`verifying`、`rolled_back`、`success`、`failed`、`repair_required`。
- Gateway 重启不丢失 updater 状态；启动后只读取，不擅自重放 root 操作。
- 协议事务、来源策略事务或受管资源修复处于非终态时，后端升级拒绝开始；UI 静态切换可以执行，但不调用这些事务。
- 状态文件设置数量和保留时间上限，只保留当前和最近一个终态摘要。

## 9. 数据面影响边界

| 操作 | Gateway | AimiliVPN/OpenVPN | x-ui/Xray | 现有节点流量 |
| --- | --- | --- | --- | --- |
| 本地开发与构建 | 不变 | 不变 | 不变 | 不影响 |
| 后续 UI 发布/回滚 | 不重启 | 不变 | 不变 | 不影响 |
| 首次启用外部 UI | 重启一次 | 不重启 | 不重启 | 应持续；必须以进程与真实状态门验证 |
| `control-plane-only` 后端升级 | 短暂重启 | 不重启 | 不重启 | 已建立连接持续；控制台暂不可操作 |
| 协议、端口、数据库迁移或运行时变更 | 不允许一键升级 | 走专项计划 | 走专项计划 | 按专项影响说明 |

“节点不受影响”必须由部署前后固定端口、协议状态、Xray/OpenVPN 进程数量和受管/非受管资源指纹证明，不能只根据 systemd active 推断。

## 10. 故障与恢复

- 外部 UI 缺失、越界、manifest 损坏或 API 不兼容：自动使用内嵌 UI。
- UI HTTP 资产验证失败：切回 previous；Gateway 不重启。
- UI 行为缺陷：使用 root helper 回滚，不能依赖已损坏页面。
- 下载失败、签名失败或磁盘不足：生产文件零写入；删除对应 staging 和临时标记。
- 新 Gateway 启动或健康失败：恢复 previous 二进制并重启 Gateway。
- 回滚 Gateway 后仍不健康：停止自动重试，保留诊断资产和唯一可确认旧版本，不重启其他服务。
- SSH、宿主审批或网络中断：先读取 run ID 状态、实际二进制摘要和 systemd 状态；不得因客户端无输出重复升级。

## 11. TDD 与验证

### 11.1 第一阶段

- RED：外部 current 合法时仍提供内嵌资源；最小实现后 GREEN。
- RED：外部 manifest 损坏、越界软链接、API 不兼容、缺入口资产、静态资产缺失错误返回 index；分别最小修复。
- 验证 current/previous/embedded 查找顺序、缓存头、SPA fallback、并发切换和 Windows/Linux 路径差异。
- 前端测试、生产构建、`go test ./internal/webassets ./internal/app -race`、`go vet` 和部署契约测试。
- 生产首次启用前构造失败包验证零写、切换失败回退和唯一 previous 清理。

### 11.2 第二阶段

- RED/GREEN 覆盖非法 URL/版本、签名错误、摘要错误、平台错误、数据库不兼容、影响等级越界、磁盘不足、并发事务和协议事务非空闲。
- 使用临时目录和假 systemd runner 覆盖停止前失败、替换后启动失败、健康失败、回滚成功和回滚失败。
- 部署契约测试固定 root helper 的 capability、读写路径、网络来源闭集、凭据加载和不得触碰的服务/数据库。
- 前端覆盖重新认证、轮询期间连接中断、成功、回滚成功和 `repair_required` 中文通知。
- 生产先执行只读预检和无写 dry-run，再安装 updater，最后用“同版本拒绝/无更新”路径验证，不以首次生产测试制造真实故障。

## 12. 分阶段交付与停止门

### 阶段 A：外部 UI 读取与手工签名发布

- 只修改 webassets、配置/部署契约和测试；不增加网页更新按钮。
- 本地构建 UI 包，由现有安全上传路径交给 root 安装器。
- 首次生产部署只重启 Gateway；通过完整不变门后才能结束。

### 阶段 B：UI 隔离更新器与页面操作

- 增加分离签名 manifest、低权限 fetcher、无网络 root installer、UI 安装/回滚事务、spool、状态 API 和页面入口。
- 完成后纯 UI 日常发布无需重启 Gateway。

### 阶段 C：后端 dry-run updater

- 增加低权限固定发布源下载、两次签名验证和全部预检，但不允许替换二进制。
- 生产只读 dry-run 能稳定识别当前版本、兼容性和磁盘条件后才进入下一阶段。

### 阶段 D：后端一键升级

- 开放 `control-plane-only` 二进制替换、Gateway 重启、健康门和自动回滚。
- 首次只部署不改变协议、端口、数据库 schema 和 systemd 权限模型的版本。

任一阶段发现必须修改 AimiliVPN、x-ui/Xray、Caddy、数据库 schema 或网络安全边界，立即停止并升级为独立设计，不并入本计划。

## 13. 文档与仓库治理

- `README.md` 只说明用户可见能力、入口和当前稳定发布方式。
- `AGENTS.md` 继续作为开发与生产边界，不新增重复的项目指导文件。
- `deploy/config/config.example.json` 只增加 Gateway 读取外部 UI 所需的非敏感配置；固定发布源和允许通道放在独立的 `deploy/config/updater.example.json`，下载凭据只在 fetcher systemd unit 示例中以 credential 路径声明，不进入 JSON。
- 真实令牌、签名私钥、下载凭据、生产 URL、Cookie 和订阅信息不得进入仓库。
- 本设计批准后再用 `writing-plans` 编写逐文件实施计划；未批准前不得写实现代码或部署生产。

## 14. 完成标准

第一阶段完成必须证明：

- 内嵌兜底仍可用；
- 合法外部 UI 生效且 Gateway PID 不变；
- UI 原子切换和回滚无需重启；
- current/previous 之外没有历史 UI 资产；
- 四服务、Xray/OpenVPN 数量、四出口状态、端口、协议与资源指纹不变。

第二阶段完成必须证明：

- 只能安装固定来源、有效签名、兼容且标记为 `control-plane-only` 的版本；
- Gateway 短暂重启后恢复，数据库、配置和凭据未变；
- AimiliVPN、x-ui/Xray、Caddy 未重启，当前节点代理流量持续；
- 失败时自动恢复唯一 previous，成功后 staging 和更旧资产被删除；
- 页面原操作、无页面 root 回滚入口、服务重启恢复和中断续接均通过；
- 工作区、本地提交和 GitHub 推送状态分别准确报告。
