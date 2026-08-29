# 主连接安全切换与独立协议模式运行手册

日期：2026-08-29

适用范围：Aimili Gateway、AimiliVPN、3x-ui/Xray `26.7.28`、Caddy 和 `ssh ny`。

## 1. 目标与不可变边界

本手册只部署和验收以下增量：

- 主连接使用 `stage → Gateway 验证 → commit/rollback` 两阶段事务。
- `agw-main` 与三个普通出口各自选择一个公网协议模式。
- 公网协议切换原位复用同一入站 ID、tag、数值端口和 SOCKS 路由。

任何阶段都必须保持：

- AimiliVPN 运行出口总数为四，不新增隧道或备用出口。
- 公网入站总数为四，mixed 总数为四。
- mixed/SOCKS5H 地址、协议和出口不随公网协议切换。
- 不恢复 `21000`、balancer 或长期备用 XHTTP/Hysteria2 入站。
- 不调用会全局重载 Xray 的 3x-ui 写 API。
- 不删除或修改非 Gateway 受管资源。
- 不在终端、日志或验收记录中输出连接凭据、私钥、后台路径或完整订阅地址。

## 2. 入口文件

- 本地总验证：`scripts/verify-main-switch-protocol-modes.ps1`
- VPS 阶段脚本：`scripts/deploy-main-switch-protocol-modes-remote.sh`
- VPS 安全门：`scripts/verify-main-switch-protocol-modes-remote.py`
- root 事务助手：`scripts/aimili_xui_protocol_transaction.py`
- 事务助手单元与故障注入：`scripts/test_aimili_xui_protocol_transaction.py`
- 部署集成测试：`scripts/test_protocol_transaction_integration.py`
- 验收记录：`docs/verification/2026-08-29-main-switch-protocol-modes.md`

## 3. 生产前硬门

进入 Stage 1 前必须同时满足：

1. Gateway 与 AimiliVPN 功能工作树干净，提交边界清晰。
2. 两仓库全量测试、前端生产构建、部署契约和 Python 故障注入全部通过。
3. `MemAvailable ≥ 160 MiB`。
4. Swap 空闲 `≥ 512 MiB`。
5. Caddy 公共证书至少七天内不过期，域名匹配，Xray 可读取证书和私钥。
6. 使用生产 Xray 对 Hysteria2 完整临时配置执行 `run -test -config` 成功。
7. 记录非 Gateway 入站的脱敏数量与 SHA-256 指纹。
8. 记录 Xray PID、四个公网入站、四个 mixed、Aimili 主与三个槽位的安全摘要。
9. 联合备份成功并完成摘要校验。

安全门失败时停止，不修改服务、数据库或防火墙。

## 4. 部署资产与权限

本地只生成以下资产，并放入一次性系统临时目录：

- Linux Gateway 二进制。
- AimiliVPN feature bundle。
- root helper 启动器和 Python 模块。
- Gateway、path、oneshot、timer 四个 systemd 单元。
- VPS 安全门脚本。

VPS 上的 `/tmp/aimili-main-switch-protocol-modes` 只作为本次传输目录。`protocol-transaction.json` 必须在 VPS root-only 环境根据当前真实 Caddy 稳定证书路径生成，权限为 `0640 root:aimili-gateway`；不得把路径或文件内容回显到聊天和验收记录。

Stage 2 创建的权限边界：

| 目录 | owner:group | mode | 用途 |
| --- | --- | --- | --- |
| `protocol-spool/requests` | `aimili-gateway:aimili-gateway` | `0700` | Gateway 原子写闭集请求 |
| `protocol-spool/results` | `root:aimili-gateway` | `0750` | helper 写、Gateway 只读 |
| `transactions` | `root:root` | `0700` | 含回滚材料的短期快照 |
| `profiles` | `root:root` | `0700` | root-only 派生协议资料 |

Gateway 不取得 `x-ui.db`、证书私钥、Xray HandlerService 或 systemd 的直接写权限。

## 5. 联合备份

每次执行阶段脚本都会建立新的 `0700` 备份目录，至少包含：

- Gateway 二进制、配置和 SQLite。
- AimiliVPN Git bundle、原提交、运行状态与回环控制配置。
- 3x-ui SQLite 和 Xray 当前运行时配置。
- Caddy 配置与当前 UFW 状态。
- 非 Gateway 资源指纹、Xray PID 和非目标探针安全摘要。
- 备份内所有文件的 SHA-256 清单。

备份只保存在 VPS 受限目录。不得复制证书私钥、控制令牌或订阅正文到验收记录。

## 6. 阶梯部署

### Stage 1：AimiliVPN 主事务

1. 部署 AimiliVPN feature bundle，保持普通三个槽位不变。
2. 读取 `main.assignment.read`，确认当前没有未恢复事务。
3. 使用不可用测试候选执行一次受控失败；预期为旧主自动恢复、`7928` 再次通过，状态不是 `repair_required`。
4. 再选择一个未占普通槽位的真实候选执行主替换。
5. 验证 `7928`、主 mixed、`8443` 当前协议、代理 DNS和真实出口一致；三个普通槽位身份、进程、出口和端口不变。

任一检查失败只回滚 AimiliVPN 本级代码和状态，然后停止。

### Stage 2：Gateway 模型、root helper 与 UDP 白名单

1. 部署 Gateway、root helper、systemd 单元和 root-only 事务配置。
2. 创建 spool 与事务目录，更新 Gateway 的三个 protocol spool 配置键。
3. 只执行四条精确 UFW 规则：`8443/udp`、`20000/udp`、`20001/udp`、`20002/udp`。
4. 禁止新增节点用 `443/udp` 规则，禁止 UDP 范围规则。
5. 启动 Gateway、path 与 timer；所有公网出口此时仍为 TCP/Vision。
6. 核对 migration、四公网/四 mixed、Xray PID 与非 Gateway 指纹。

Stage 2 不重启 x-ui/Xray。失败只恢复 Gateway 本级资产、数据库、配置和本级新增 UFW 规则。

### Stage 3：普通出口 TCP → XHTTP → TCP

1. 对非目标三个公网出口和全部 mixed 建立持续探针，记录 Xray PID。
2. 通过 Gateway UI 把一个普通 ready 出口切到 XHTTP。
3. 验证订阅条目仍为 VLESS、transport 为 XHTTP、无 Vision flow；代理 DNS、真实出口和逻辑出口一致。
4. 使用 v2rayN `7.24.4` 从原订阅刷新、识别并连接该节点。
5. 确认 Xray PID不变，非目标探针持续，Aimili 四出口身份不变。
6. 将同一出口切回 TCP/Vision并重复验证。

失败时只回滚目标出口并停止。

### Stage 4：普通出口 TCP → Hysteria2

1. 先重跑证书、离线 Xray 配置和精确 UDP 安全门。
2. 将一个普通出口切到 Hysteria2。
3. 从外部完成 QUIC/TLS、代理 DNS、真实出口和统一订阅验证。
4. 使用 v2rayN `7.24.4` 刷新、识别并连接 Hysteria2 条目。
5. 核对 mixed/SOCKS5H 与另外三个公网出口不变。

若外部包没有到达 VPS，记录为 `upstream_udp_blocked`，回滚目标出口到 TCP 并停止；不得重建服务器或扩大防火墙范围。

### Stage 5：至少五分钟资源观察

每 10–30 秒记录一次脱敏样本，连续至少 300 秒：

- 无 OOM。
- Xray RSS 峰值 `≤ 96 MiB`。
- `MemAvailable` 不连续 30 秒低于 `96 MiB`。
- Swap 使用增量 `≤ 128 MiB`。

任一阈值越界立即回滚 Stage 4 的目标出口并停止。

### Stage 6：主出口试切与最终混合模式

普通出口稳定后才允许主协议试切。每次主试切都同时验证 `7928`、主 mixed 与主公网链路。最终目标：

| 逻辑出口 | 最终公网协议 |
| --- | --- |
| 主连接 | TCP/Vision |
| 出口位 1 | XHTTP/REALITY |
| 出口位 2 | Hysteria2/QUIC/TLS |
| 出口位 3 | TCP/Vision |

最后执行受控 x-ui/Xray 重启恢复验证，再分别重启 Gateway 与 AimiliVPN，确认已提交状态不漂移、未提交事务自动恢复。只有 Gateway UI 和 v2rayN 原用户路径都通过后才能声明完成。

## 7. `repair_required` 处置

1. 立即停止新的替换、协议切换、删除和候选维护。
2. 保留 root 事务目录，不移动、不修改其中快照。
3. 记录逻辑出口、阶段和脱敏错误码；不得记录快照内容。
4. 核对 Xray 当前 tag、3x-ui 目标入站行和 Gateway 协议状态的安全指纹。
5. 优先运行 helper 的封闭 `rollback` 或 `recover`；不得使用 3x-ui 全局写 API。
6. 只有旧协议、旧订阅和真实流量全部恢复后才删除快照并恢复 `ready`。

## 8. 证书续期验证

Caddy 续期后不主动重载整个 Xray。先执行证书域名、有效期、文件权限和 Xray 离线配置检查；随后只选择一个 Hysteria2 逻辑出口做外部握手。若 Xray 尚未读取新证书，安排受控维护窗口验证 x-ui/Xray 重启恢复，不能在线协议切换时顺带触发全局重启。

## 9. 完成声明

最终报告必须分开陈述：

- 本地测试与构建是否通过。
- VPS 各阶段是否通过、是否发生本级回滚。
- Gateway UI 原用户路径是否通过。
- v2rayN `7.24.4` 原订阅刷新和四节点真实连接是否通过。
- 是否仍有云 UDP 边界或 `repair_required` 未决项。

局部 API 成功、单个服务 active 或单个公网节点可连都不能单独视为任务完成。
