# 主连接安全切换与独立协议模式验收记录

日期：2026-08-29

状态：本地验证通过，等待 VPS 阶梯验收

本记录只保存提交、版本、计数、端口、布尔结果、资源指标和脱敏错误码。不得写入连接材料、私钥、后台路径或完整订阅地址。

## 1. 版本与提交

| 项目 | 安全记录 |
| --- | --- |
| Gateway 分支/提交 | 待填写 |
| AimiliVPN 分支/提交 | 待填写 |
| 3x-ui 版本 | 待填写 |
| Xray 版本 | 待填写 |
| v2rayN 版本 | `7.24.4` |

## 2. 本地验证

| 检查 | 结果 |
| --- | --- |
| AimiliVPN 全量 unittest | 通过，81 tests |
| Gateway 全量 Go test | 通过，全部 package |
| 前端 Vitest | 通过，27 tests |
| 前端 production build | 通过 |
| Python helper 与故障注入 | 通过，42 tests；新增 SQLite 锁恢复分支通过 |
| 部署契约 | 通过 |
| `git diff --check` | 通过 |
| Ponytail 复杂度审查 | 通过；删除仅测试使用的假阶段执行器和单行包装，无安全边界删减 |

## 3. 生产前硬门

| 指标 | 结果 |
| --- | --- |
| 联合备份与摘要 | 待填写 |
| `MemAvailable` | 待填写 MiB |
| Swap 空闲 | 待填写 MiB |
| 证书域名/有效期 | 待填写 |
| Hysteria2 Xray 离线配置 | 待填写 |
| 非 Gateway 资源数量 | 待填写 |
| 非 Gateway 指纹已记录 | 待填写 |
| Xray 初始 PID 已记录 | 待填写 |

## 4. Stage 1：主事务

| 检查 | 结果 |
| --- | --- |
| 受控失败恢复旧主 | 待填写 |
| `7928` 恢复 | 待填写 |
| 真实主切换 | 待填写 |
| 主 mixed 与公网一致 | 待填写 |
| 三个普通槽位不变 | 待填写 |
| 本级回滚 | 未执行/已执行：待填写 |

## 5. Stage 2：Gateway 与助手

| 检查 | 结果 |
| --- | --- |
| Gateway migration | 待填写 |
| root helper/path/timer | 待填写 |
| 精确 UDP 端口 | 待填写 |
| 不存在节点用 `443/udp` 规则 | 待填写 |
| 不存在 UDP 范围规则 | 待填写 |
| 公网入站数 | 待填写，期望 4 |
| mixed 数 | 待填写，期望 4 |
| Xray PID不变 | 待填写 |
| 非 Gateway 指纹不变 | 待填写 |

## 6. Stage 3：XHTTP 往返

| 检查 | 结果 |
| --- | --- |
| TCP → XHTTP | 待填写 |
| v2rayN 识别与真实连接 | 待填写 |
| 代理 DNS/真实出口一致 | 待填写 |
| TCP/Vision 反向恢复 | 待填写 |
| 非目标长连接持续 | 待填写 |
| Xray PID不变 | 待填写 |

## 7. Stage 4–5：Hysteria2 与资源

| 检查 | 结果 |
| --- | --- |
| 外部 QUIC/TLS | 待填写 |
| v2rayN 识别与真实连接 | 待填写 |
| UFW 包到达 | 待填写 |
| 云 UDP 边界 | 正常/`upstream_udp_blocked`/待填写 |
| 观察时长 | 待填写 秒，期望 ≥ 300 |
| Xray RSS 峰值 | 待填写 MiB，期望 ≤ 96 |
| `MemAvailable` 最低值 | 待填写 MiB |
| Swap 使用增量 | 待填写 MiB，期望 ≤ 128 |
| OOM | 待填写，期望否 |

## 8. Stage 6 与重启恢复

| 逻辑出口 | 协议 | 真实连接 | mixed/SOCKS5H |
| --- | --- | --- | --- |
| 主连接 | TCP/Vision | 待填写 | 待填写 |
| 出口位 1 | XHTTP/REALITY | 待填写 | 待填写 |
| 出口位 2 | Hysteria2/QUIC/TLS | 待填写 | 待填写 |
| 出口位 3 | TCP/Vision | 待填写 | 待填写 |

| 检查 | 结果 |
| --- | --- |
| x-ui/Xray 重启重建混合模式 | 待填写 |
| Gateway 重启恢复 | 待填写 |
| AimiliVPN 未提交事务恢复 | 待填写 |
| Gateway UI 主替换原路径 | 待填写 |
| Gateway UI 协议切换原路径 | 待填写 |
| “复制节点订阅”原路径 | 待填写 |
| v2rayN 四节点刷新与逐条连接 | 待填写 |

## 9. 最终不变量

| 不变量 | 结果 |
| --- | --- |
| AimiliVPN 运行出口总数 = 4 | 待填写 |
| 公网入站总数 = 4 | 待填写 |
| mixed 总数 = 4 | 待填写 |
| 每逻辑出口只有一个公网协议 | 待填写 |
| mixed/SOCKS5H 未随协议切换 | 待填写 |
| 非 Gateway 资源未删除或修改 | 待填写 |
| `21000` 与 balancer 未恢复 | 待填写 |
| 无未决 `repair_required` | 待填写 |

## 10. 未决项

- 待 VPS 阶梯部署后填写。
