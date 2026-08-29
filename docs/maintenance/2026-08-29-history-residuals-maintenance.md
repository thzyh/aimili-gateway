# 历史残留审计与维护基线（2026-08-29，Test 风格订阅迁移后）

## 当前架构基线

当前生产没有创建第 4 个 OpenVPN 槽位，而是复用早期主连接：

- 三个 Gateway 受管槽位分别使用独立 AimiliVPN 槽位和 `agw-*` VLESS、mixed、SOCKS 出站。
- 第 4 出口使用 AimiliVPN 主连接 `tun0` 的本地代理 `7928`。
- 早期非受管 VLESS 入站 `aimili-reality`（端口 `8443`）继续保留原客户端身份。
- Gateway 只在精确核对 `aimili-reality → aimili-socks → 127.0.0.1:7928` 后，为主连接增加 `agw-main-mixed`。
- 3x-ui 原生订阅客户端关联 `8443` 与三个受管 VLESS 入站；一个订阅导入后得到四个独立节点。
- 历史单地址聚合入口、`leastPing` balancer 和 observatory 已清理，不再属于生产数据面。

主连接出口会随 AimiliVPN 主连接切换，不等同于固定的受管槽位。Gateway 不对它调用槽位创建、轮换或删除 API。

## 已确认的历史对象分类

| 对象 | 结论 | 处理方式 |
|---|---|---|
| `8443` / `aimili-reality` | 仍在使用，不是无用残留 | 保留；作为第 4 主连接 VLESS |
| `aimili-socks` / `127.0.0.1:7928` | 仍在使用 | 保留；作为主连接 Xray 出站和聚合候选 |
| 三个 Gateway 受管槽位和六个 `agw-*` 入站 | 当前生产资源 | 保留；只由 Gateway 编排 |
| Test 风格订阅客户端 | 当前生产资源 | 保留；只允许关联 `8443` 与受管 VLESS，不关联 mixed |
| 旧聚合 VLESS 入站、客户端引用、balancer、observatory | 已确认历史残留 | 已备份并清理；旧 API 只返回弃用错误 |
| 3x-ui `clients` 中失去入站引用的记录 | 可确认的数据库孤儿 | 先 SQLite 在线备份，再按引用关系删除 |
| 非受管 3x-ui 入站和客户端 | 所有权不属于 Gateway | 不清理 |
| `/var/lib/aimili-gateway/gateway.db` 等旧数据库 | 可能是旧版本或回滚来源 | 不自动清理；先核对当前配置路径、schema 和回滚来源 |
| AimiliVPN Git stash | 可能含部署前现场状态 | 不自动清理；确认对应提交已进入可恢复历史后再处理 |
| `x-ui-caddy-sync.*` 等旧 systemd 单元 | 可能仍影响 Caddy | 不自动清理；先核对当前主机是否加载及 Caddy 是否依赖 |
| Gateway `aggregate_config` 禁用行中的旧资源名、ID、端口 | 迁移墓碑元数据，不是运行资源 | 保留；用于幂等和审计，不得据此重建旧入口 |
| 生产备份目录 | 故障回滚依据 | 保留最新工作回滚点及升级专用回滚集 |
| 本地 `.go-cache`、工作树缓存 | 只影响开发磁盘 | 可以在构建结束后清理，不属于 VPS 运行残留 |

## 自动清理边界

`cleanup-history-remote.sh` 目前只允许删除满足下列全部条件的 3x-ui 客户端行：

1. `clients.inbound_id` 非空；
2. 对应 `inbounds.id` 已不存在；
3. 删除前已用 SQLite backup API 生成权限为 `0600` 的一致性备份；
4. 删除语句再次包含“不存在对应入站”的约束；
5. 删除后重新统计孤儿数。

脚本不根据客户端总数推算要删除多少条，也不读取或输出客户端 UUID。mixed 入站账户不在 `clients` 表中，不受这一清理影响。

## 不建议清理的原因

- `8443` 是早期部署脚本明确创建的真实路径，并非仅因没有 `agw-` 前缀就无用。
- 旧数据库和 stash 可能是唯一可恢复现场；没有提交、schema、服务配置三方证据时删除风险高于磁盘收益。
- 512 MiB VPS 的主要约束是常驻内存，不是少量配置、迁移墓碑或数据库备份占用；清理回滚材料不会改善运行容量。
- 非受管 3x-ui 资源可能由用户或原后台维护，Gateway 没有删除授权边界。

## 建议维护周期

- 每次 Gateway、AimiliVPN 或 3x-ui 升级前后各运行一次只读审计。
- 每月核对一次孤儿客户端数量、failed units、OOM、Swap、当前数据库路径和备份可恢复性。
- 生产备份超过 30 天时可以评估轮换，但必须保留最近一个完整且已完成端到端验证的联合回滚点。
- 主连接换 IP 后重新验证 `8443`、主连接 mixed 和四入站订阅覆盖。

## 回滚要求

若主连接 mixed 或订阅关联变更导致 Xray 无法启动：同时恢复 3x-ui 数据库、`/usr/local/x-ui/bin/config.json`、Gateway 二进制和 Gateway 数据库，然后重启 `x-ui` 与 `aimili-gateway.service`。回滚后必须验证三个受管组、`8443`、主 SOCKS5H 和订阅端口集合，不得仅以 systemd `active` 判断恢复完成。

## 本次已执行清理

- 通过 SQLite backup API 备份后删除 16 个无入站引用的 clients 和 16 个对应的 client_traffics。
- 删除 0 字节且非当前配置的 /var/lib/aimili-gateway/gateway.db。
- Test 风格订阅迁移时清理旧 `21000` 入站、聚合客户端引用、balancer 和 observatory；未删除任何非受管入站。
- 迁移后审计发现旧聚合在独立客户端表留下 1 条孤儿客户端和 1 条孤儿流量；再次使用 SQLite backup API 生成 `0600` 备份后精确删除。
- 最终复审结果：孤儿客户端和孤儿流量均为 0，当前客户端/流量均为 5 条；历史聚合端口、标签和运行时引用均为 0。
- 未删除 8443、任何非受管 3x-ui 资源、AimiliVPN stash、systemd 单元或生产备份。

## 当前明确保留项

- 最近一次通过端到端验收的 Gateway、AimiliVPN、3x-ui 联合回滚备份。
- 非受管的 aimili-reality 入站身份和所有非受管 3x-ui 资源。
- Gateway 数据库中 `enabled=false` 的聚合迁移墓碑；它不是活动入口，保留用于幂等和审计。
- 仓库中的旧部署/回滚脚本；只作为历史恢复依据，不代表当前生产仍使用聚合入口。
- AimiliVPN Git 历史和 stash（当前 stash 数为 0，后续升级仍需先审计）。
- x-ui-caddy-sync.timer/service 等 systemd 单元，即使当前未加载，也不在无依赖证据前删除。
