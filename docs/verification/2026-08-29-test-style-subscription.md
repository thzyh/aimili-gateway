# 2026-08-29 Test 风格 VLESS 订阅生产验收

## 验收结论

Aimili Gateway 已在 `ny.zouyunhui.cc.cd` 完成 Test 风格多入站订阅迁移。统一控制台返回一个 3x-ui 原生订阅地址，订阅内容包含四个独立 VLESS 节点：主连接 `8443` 和三个受管入口 `20000–20002`。mixed/SOCKS5H 没有进入该订阅。

旧 `21000` 聚合入站、聚合客户端引用、`agw-aggregate` balancer 和 observatory 已安全清理。旧 API 固定返回 `legacy_removed`，不会重建历史入口。本文不记录用户名、密码、Cookie、UUID、私钥、随机后台路径或完整订阅地址。

## 版本与生产基线

- Gateway 使用 Go `1.26.7` 构建，Linux amd64 二进制 SHA-256 为 `9c7881950119777b527df4815322c1f2921ffc7ba1e000585d80cdfa0c7c048b`。
- AimiliVPN 生产提交为 `99dddccb7175`，控制 API 已包含 `slots.assign`。
- 3x-ui 为 `3.7.0`，Xray 为 `26.7.28`。
- 主机规格约 458 MiB 内存和 1 GiB Swap；迁移前可用内存约 212 MiB、Swap 余量约 919 MiB。
- `aimili-gateway`、`aimilivpn`、`x-ui`、`caddy` 在最终检查中均为 `active`。

## 本地验证

最终代码树完成以下检查：

- `go test ./...`：全部 Go 包通过。
- `go vet ./...`：退出码 0。
- 前端 Vitest：6 个测试文件、24 项测试全部通过。
- `npm run build`：Vue TypeScript 检查与 Vite 生产构建成功。
- AimiliVPN `unittest discover`：63 项测试全部通过。
- `python -m py_compile scripts/verify-test-style-subscription.py`：通过。
- `git diff --check`：通过。

针对第二次部署暴露的时序问题新增了两项回归：

1. 主连接配置已一致时，`EnsureLegacyMain` 不再无条件重写 xray 设置和触发重载。
2. 确需重载时，`CheckMain` 按真实 SOCKS5H 与 VLESS 均成功的条件等待；瞬时 `connection_failed`、`protocol_failed`、`dns_failed` 和 `timeout` 会在就绪窗口内重试。

两项测试均先观察到预期失败，再完成最小实现并转为通过。

## 迁移保护与回滚

生产脚本在写入前创建权限受限的联合备份，包含：

- Gateway 二进制；
- Gateway SQLite 数据库；
- x-ui SQLite 数据库；
- `/usr/local/x-ui/bin/config.json` 运行时配置；
- 非受管入站脱敏哈希清单。

回滚路径会同时恢复数据库和 Xray 运行时配置，避免只恢复 x-ui 数据库却留下运行时漂移。二进制恢复权限固定为 `0755`；数据库和备份保持受限权限。

## 订阅与真实流量验证

验收脚本使用 Gateway 正常登录接口获取临时会话，再以 v2rayN User-Agent 获取订阅。订阅解码结果为四条 VLESS URI，端口集合严格等于：

```text
8443, 20000, 20001, 20002
```

脚本没有输出 URI 内容。每一条订阅记录都启动独立临时 Xray 客户端，通过代理 DNS 和公网 IP 服务核对真实出口；清理前后共完成两轮：

| 端口 | 清理前 | 清理后 | 最终孤儿清理后复测 |
|---:|---|---|---|
| 8443 | 通过 | 通过 | 通过 |
| 20000 | 通过 | 通过 | 通过 |
| 20001 | 通过 | 通过 | 通过 |
| 20002 | 通过 | 通过 | 通过 |

最终复测的单次延迟受公网状态影响，约为 1.2–2.3 秒；验收依据是协议握手、代理 DNS、出口 IP 和预期出口一致，不以固定延迟阈值判定。

3x-ui 数据库独立复核显示，四个 VLESS 入站各有且仅有一个 `aimili-gateway-subscription` 关联。各入站仍可保留其他合法客户端；Gateway 没有删除或改写用户手动客户端。

## 主连接双协议验证

主连接 `8443 → aimili-socks → 127.0.0.1:7928` 与 `agw-main-mixed` 均实测通过。最终孤儿清理后复测结果：

- 主 VLESS 延迟约 1.5 秒；
- 主 SOCKS5H 延迟约 0.5 秒；
- 两种协议的实际出口均与 AimiliVPN 主连接状态一致。

`CheckMain` 的第二次调用没有再触发无必要的 Xray 重载，解决了旧清理后立即检测的瞬时失败。

## 历史残留清理

第一次迁移尝试回滚后形成的真实现场是：x-ui 数据库和运行时均已不含旧聚合资源，但 Gateway `aggregate_config.enabled` 仍为真。适配器增加严格幂等收口：只有旧入站、客户端引用、路由、balancer 和 observatory 全部不存在时才接受“已删除”；存在任一部分残留时仍返回漂移错误，不做猜测性删除。

最终结果：

- `aggregate_config.enabled=false`；
- `21000` 不再监听；
- 旧聚合入站数量为 0；
- 入站中的旧聚合客户端引用为 0；
- Xray 运行时中的旧端口和标签引用为 0；
- 非受管入站哈希清单迁移前后未变化；
- 清理 API 再次调用返回幂等结果，不重复写入数据面。

迁移后全面审计另发现 `clients` 和 `client_traffics` 表各有 1 条失去入站引用的旧聚合孤儿。维护脚本先 dry-run，再通过 SQLite backup API 创建 `0600` 备份并使用引用条件精确删除。最终复审：

- 孤儿客户端：0；
- 孤儿流量：0；
- 当前客户端与流量记录：各 5 条；
- AimiliVPN stash：0；
- 无其他自动清理候选。

保留项与理由记录在 `docs/maintenance/2026-08-29-history-residuals-maintenance.md`，主要包括生产回滚备份、非受管资源、禁用的迁移墓碑和历史恢复脚本。

## 验收边界

- 本次由 VPS 内的真实 Xray 客户端完成等价协议与订阅格式验证，没有自动操作用户 Windows 上正在运行的 v2rayN，也没有终止其 Xray 进程。
- 用户此前已验证 3x-ui 同类 `test` 订阅可由 v2rayN 正常导入；本次 Gateway 订阅使用同一 3x-ui 原生订阅机制。
- 候选替换的成功、失败回滚已有本地与此前生产验收；本次最终清理复测未主动替换在线节点，以避免在历史资源清理之外扩大数据面变量。
- 生产备份仍需按维护策略保留；512 MiB 主机不应通过删除唯一可恢复现场来换取少量磁盘空间。
