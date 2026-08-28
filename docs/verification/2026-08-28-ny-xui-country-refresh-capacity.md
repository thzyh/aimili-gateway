# ny：3x-ui 升级、按国家刷新与容量阶梯验证

日期：2026-08-28

## 范围与约束

- 目标主机：`ny`，512 MiB 规格。
- 生产容量只按 `1 → 2 → 3` 验证，不尝试容量 4。
- 任一级失败立即回到上一级稳定容量。
- 只管理 Aimili Gateway 创建的 AimiliVPN 槽位与 3x-ui `Aimili Gateway` 入站；不删除或改写非受管 3x-ui 资源。
- 本文不记录账户密码、Cookie、UUID、私钥、随机后台路径或完整代理/订阅地址。

## 已部署版本

| 组件 | 生产版本或提交 | 最新核对 |
| --- | --- | --- |
| 3x-ui | `v3.7.0` | 生产二进制 `-v` 返回 `3.7.0` |
| AimiliVPN | `1f1d8312d4e9` | 生产仓库 `HEAD` |
| Aimili Gateway | `f54d066` | 多组槽位唯一预留修复已部署 |

Gateway 本地修复验证：

- `go test ./... -count=1`：全部包通过。
- `go test -race ./internal/orchestrator -count=1`：通过。
- `go vet ./...`：退出码 0。
- Linux amd64、`CGO_ENABLED=0` 构建：退出码 0；上传前后 SHA-256 一致。

## 已确认的兼容性修复

### 3x-ui v3.7.0

- 受管 VLESS 客户端邮箱改为资源级唯一，适配 v3.7.0 的全局邮箱去重。
- 回滚或删除受管入站时，同时调用完整客户端删除接口，避免残留全局客户端记录。
- 3x-ui 的 Xray 目录权限不再直接授予 Gateway；Gateway 使用受限的专用 Xray 副本。

### 多组 AimiliVPN 槽位

容量提升到 2 后，第二组首次创建返回 `already_exists`。最新证据链：

1. Gateway 数据库只有 1 条 JP ready，无 KR 历史记录。
2. AimiliVPN 只有 1 个 JP 槽位，无 KR 残留槽位。
3. `proxy_groups.aimili_slot` 有唯一约束，而旧实现创建任何新组时都先写占位槽位 0；现有 JP 组正使用槽位 0。

修复为创建记录前综合 Gateway 持久组和 AimiliVPN 运行槽位，预留最小未使用槽位。新增回归测试先复现第二组唯一约束冲突，再验证第二组使用槽位 1；首组和出口唯一轮换回归同时通过。

## 按国家刷新

- JP 刷新完成：目录候选 41、精验 5、有效 5。
- 刷新范围仅为 JP；目标有效节点上限 5，OpenVPN 精验并发为 1。
- 刷新完成后 JP standby 候选可见，随后建立 JP 机房 ready 组。

## 异常受管组清理

- 已删除不可恢复的 `VN / residential / degraded` 受管组及对应 AimiliVPN 槽位。
- 已删除本次自动 reconcile 产生的唯一 `KR / datacenter / degraded` 异常记录及对应槽位。
- 清理后重新查询未发现 KR degraded 自动重建。
- 非受管 3x-ui 入站数量在容量 1 与容量 2 核对中始终为 1。

## SOCKS5H 来源限制的验收语义

生产策略保持 `enabled=true`，现有 CIDR 白名单不放宽：

- 公网 mixed 端口必须可达。
- 未授权公网来源必须被受管黑洞规则拒绝。
- 从 VPS 回环授权来源进入同一个 mixed 入站时，用户名密码、SOCKS5H、代理 DNS和实际 VPN 出口必须一致。
- VLESS 继续从 Windows 外部客户端经公网真实验证。

因此，来源限制开启时，不把未授权客户端无法直接使用 SOCKS5H 误报为代理故障；同时也不以 Gateway 回环探测代替公网端口和外部 VLESS 验证。

## 容量 1

资源与数据面：

- `JP / datacenter / ready = 1`，实际出口唯一。
- AimiliVPN 槽位 1；3x-ui 受管入站 2（VLESS 1、mixed 1）。
- VLESS 入站只有 1 个客户端，mixed 入站只有 1 个账户。
- Windows 公网 VLESS、授权 SOCKS5H、代理 DNS和出口一致性通过。

900 秒采样：

- 实际结束于 913 秒，`PASS expected_capacity=1`。
- 全程服务重启增长 0、失败单元 0、OOM 0、SSH 探测正常、ready=1。
- 阶段末可用内存约 167 MiB，Swap 使用约 169 MiB，未触发回退门槛。

## 容量 2

资源与数据面：

- `JP / datacenter / ready = 1`、`KR / datacenter / ready = 1`。
- 两个实际出口 IP 唯一。
- AimiliVPN 槽位 2；3x-ui 受管入站 4。
- 两个 VLESS 入站各 1 个客户端；两个 mixed 入站各 1 个账户。
- 非受管 3x-ui 入站仍为 1。
- Windows 公网 VLESS两组均通过；授权 SOCKS5H、代理 DNS两组均通过；公网 mixed 端口两组均可达。
- 聚合导出 VLESS 2 行、SOCKS5H 2 行，未输出内容。

900 秒采样：

- 实际结束于 912 秒，`PASS expected_capacity=2`。
- 全程服务重启增长 0、失败单元 0、OOM 0、SSH 探测正常、ready=2。
- 阶段末可用内存约 218 MiB，Swap 使用约 133 MiB，未触发回退门槛。

## 容量 3

资源与数据面：

- `JP / datacenter / ready = 1`、`KR / datacenter / ready = 1`、`KR / residential / ready = 1`。
- 三个实际出口 IP 唯一。
- AimiliVPN 槽位 3；3x-ui 受管入站 6。
- 三个 VLESS 入站各 1 个客户端；三个 mixed 入站各 1 个账户。
- 非受管 3x-ui 入站仍为 1。
- 三组分别从 Windows 外部客户端验证：公网 VLESS/TCP、授权 SOCKS5H、代理 DNS均通过，退出码均为 0。
- 聚合导出 VLESS 3 行、SOCKS5H 3 行，未输出内容。
- 提升容量后自动 reconcile 曾创建一条临时 KR 机房 provisioning；该尝试失败后自动补偿清理，未留下 degraded 或额外槽位。随后显式启用 KR 住宅组成功。

900 秒采样：

- 实际结束于 914 秒，`PASS expected_capacity=3`。
- 全程服务重启增长 0、失败单元 0、OOM 0、SSH 探测正常、ready=3。
- 阶段末可用内存约 209 MiB，Swap 使用约 129 MiB，未触发回退门槛。

## 重启恢复与最终容量

最终重启恢复已执行：

- 仅重启 Gateway；AimiliVPN、3x-ui、Caddy 的 invocation 未变化。
- Gateway 健康检查恢复；3 个 AimiliVPN 槽位、6 个受管入站、3 个 ready 和唯一出口状态恢复。
- 非受管 3x-ui 入站仍为 1，服务失败单元为 0。
- 重启后第 1、2 个 ready 组的外部 VLESS 与授权 SOCKS5H 复测通过；第 3 组单独复测受 Windows 外部 Xray 进程冲突影响未取得退出结果。第 3 组在重启前已通过逐组 VLESS、授权 SOCKS5H和代理 DNS验收，重启后的服务/资源状态已通过核对。

最终稳定容量：3。未发生容量回退，未尝试容量 4。

## 未完成项

- 仅剩一项工具环境限制：重启后第 3 组的 Windows 外部 VLESS 单独复测没有获得可观测退出结果；没有据此宣称该次复测通过，也没有终止用户原有 v2rayN Xray 进程。
- 该限制不改变 VPS 当前生产状态；如需补做，可在关闭用户 v2rayN 或使用另一台独立客户端后运行 `scripts/verify-external-client-v1c.py --index 2`。
