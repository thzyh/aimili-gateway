# 2026-08-29 主连接聚合与历史残留验收

> 历史记录：本文记录当时的单入口聚合阶段。该方案已被同日完成的 Test 风格多入站订阅取代；当前生产状态以 `2026-08-29-test-style-subscription.md` 为准，旧 `21000` 聚合资源已清理。

## 验收范围

- 复用 AimiliVPN 主连接 tun0 / 127.0.0.1:7928 作为第 4 出口，不创建第 4 个 OpenVPN 槽位。
- 旧 8443 / aimili-reality 保留客户端身份，并迁移旧 Reality 伪装目标到本机 Caddy。
- 单个聚合 VLESS 入口使用 Xray leastPing balancer 和 observatory。
- 审计并清理已确认的 3x-ui 数据库孤儿，不删除非受管资源。

## 本地验证

- Gateway：go test ./... -race -count=1，全部通过。
- Gateway：go vet ./...，退出码 0。
- Web：6 个测试文件、23 项测试通过；npm run build 成功。
- Linux amd64 Gateway 二进制构建成功，部署摘要已在 VPS 预检查中核对。

## 生产验证（ssh ny）

最新脱敏结果：

    aggregate_error: ""
    aggregate_healthy_exit: true
    aggregate_single_uri: true
    main_socks5h: true
    main_vless: true
    main_vless_error: ""
    ready_groups: 4
    unique_ready_exits: true

服务 aimili-gateway.service、aimilivpn.service、x-ui.service、caddy.service 均为 active。主机约 458 MiB 内存中可用约 201 MiB，Swap 约 926 MiB 可用；本次时间窗没有 OOM/被杀进程记录。

## Reality 兼容迁移

审计确认旧入站只有一个启用客户端，flow=xtls-rprx-vision，Reality 公私钥匹配，旧目标为 www.microsoft.com:443。通过 3x-ui 入站更新 API 将目标改为 127.0.0.1:443、SNI 改为生产域名；客户端 ID、私钥、短 ID、端口、路由及其他入站均保持不变。迁移后 main_vless=true。

## 历史残留清理

清理脚本先以 dry-run 列出 16 个无现存入站引用的 clients、16 个对应的 client_traffics 和 0 字节旧数据库。随后 --apply 使用 SQLite backup API 创建权限为 0600 的备份，删除后复审孤儿客户端和孤儿流量均为 0，当前客户端/流量均为 5 条。非受管 8443 和其他非受管资源未删除。

## 异常与修复记录

第一次迁移脚本的回滚路径曾把恢复的二进制和数据库权限设置得过严，导致 203/EXEC 和 SQLite 打开失败；已通过修正文件属主/权限恢复，后续脚本改为分别设置二进制 0755、数据库 0600，并使用 rollback_and_exit 确保失败不会打印成功。恢复后的基线验证再次通过。

## 未做事项

- 未创建第 4 个 AimiliVPN 槽位。
- 未删除任何非受管 3x-ui 入站、客户端或源码。
- 未尝试容量 4，也未连接或修改其他主机。
