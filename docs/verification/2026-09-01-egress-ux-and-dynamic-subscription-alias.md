# 出口可见性、中文反馈与动态订阅别名验证记录

日期：2026-09-01（Asia/Shanghai）

## 范围与安全边界

本记录对应已批准设计 `docs/superpowers/specs/2026-09-01-egress-ux-and-dynamic-subscription-alias-design.md` 和实施计划 `docs/superpowers/plans/2026-09-01-egress-ux-and-dynamic-subscription-alias.md`。

- 始终只保留主连接、出口1、出口2、出口3四个逻辑运行出口。
- 每个逻辑出口同时只有一个公网协议；mixed/SOCKS5H 不参与切换。
- 动态别名只作用于 `aimili-gateway-subscription` 与 Gateway 受管公网入站的关联。
- 本地 fixture 仅使用逻辑角色、国家、端口和安全错误码；没有保存或输出生产密码、Cookie、UUID、Auth、私钥、随机后台路径或完整订阅链接。
- 本文先记录本地结果。VPS 阶梯部署、300 秒观察、重启和 v2rayN 原路径在执行前均标记为未完成。

## 本地代码基线

| 仓库 | 分支 | 本轮功能提交基线 | 远程状态（验证前） |
| --- | --- | --- | --- |
| `aimili-gateway` | `feat/main-switch-protocol-modes` | `60f8640` | 比远程功能分支领先；保留未跟踪 `.deploy-assets/` |
| `aimili-vpngate` | `feat/main-switch-protocol-modes` | `5d113ea` | 比远程功能分支领先 2 个提交 |
| `aimili-3xui-deploy` | `feat/main-switch-protocol-modes` | `14cf071` | 没有远程地址，未执行推送 |
| 3x-ui 固定源码 | detached `f727d04f6522bb94a8fb52e8352fdcafb51c11e1` | 应用可复现补丁 | 仅作临时测试和构建源 |

## 验证脚本 TDD

新增 `scripts/verify-egress-ux-aliases.ps1`。

RED 1：在受限账户运行 `-IntegrationOnly` 时，固定源码触发 Git 所有权保护，脚本随后对空提交结果调用 `Trim()`，覆盖了第一失败边界。

修复：固定源码只使用命令级 `safe.directory`，不修改全局 Git；读取失败先检查退出码再解析；Go 缓存固定在工作区 `.tmp`。

RED 2：读取提交已通过，但从本地仓库克隆时源 `.git` 仍被 Git 单独拒绝。

修复：克隆命令同时为工作树和其 `.git` 增加命令级安全目录；不持久化配置。

GREEN：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\verify-egress-ux-aliases.ps1 -IntegrationOnly
```

结果：退出码 0；AimiliVPN 定向 fixture 22 项通过；3x-ui 部署资产 4 项通过；固定补丁的数据库、service、controller、subscription 四包通过；Gateway 的旧主身份协议切换、国家替换动态别名、四类替换失败回滚和候选持久失效事务通过。

## 三仓库全量结果

### Gateway

完整脚本本轮实际通过：

- `npm test --prefix web`：7 个文件、38 项测试通过。
- `npm run build --prefix web`：类型检查和 Vite 生产构建通过。
- `go test ./... -race -count=1`：全部包通过，无竞态报告。
- `go vet ./...`：通过。
- `cmd/aimili-gateway` 与 `cmd/aimili-gateway-admin`：构建通过，产物只写系统临时目录并清理。
- `git diff --check`：显式 `git -C` 后通过。

跨仓 fixture 验证了：

1. Gateway 旧主身份先由健康 AimiliVPN 主状态收敛，再进入严格协议预检。
2. 出口位 1 从日本替换为韩国时，只有目标订阅别名变为 `出口位 1_韩国`。
3. 别名写失败、订阅读取失败、写后不一致会恢复旧 AimiliVPN、Gateway 与别名；别名回滚失败返回 `repair_required`。
4. 明确的候选拨号/出口失效才重读候选集合；外层系统故障不淘汰候选。

### AimiliVPN

- `python -m unittest discover -s tests -v`：157 项通过。
- `python -m py_compile proxy_server.py node_pool.py vpngate_manager.py control_api.py vpn_utils.py`：通过。
- 定向 fixture 证明 VPN 节点入口 IP 与拨号后的公网出口 IP 可以不同；无合法出口 IP 的候选不会进入可用缓存。
- 结构化国家刷新、失败候选 blacklist/节点池原子持久化、重启后仍不可选的测试通过。
- `git diff --check`：通过。

### 固定 3x-ui v3.7.0 与部署资产

- 部署资产统一测试：4 项通过、0 失败。
- 固定提交应用补丁的数据库、web service、controller、subscription 四包定向及完整包测试通过。
- 两个客户端关联同一入站的测试证明只有 Gateway 目标客户端得到别名覆盖，其他客户端继续使用原入站 remark。
- `npm ci`：601 个包安装完成，审计 0 个漏洞。
- 前端 OpenAPI 生成与 Vite 生产构建通过。
- `go build ./...`：通过。
- `git diff --check`：部署资产仓库通过。

当前 Windows 上的 `go test ./... -count=1` 仍只复现固定上游基线问题：根包 CI 文档任务名检查受 CRLF 影响；`internal/crypto/nodetoken` 的 POSIX `0600` 权限断言在 Windows 映射为 `0666`；`internal/web/service/panel` 的环境变量大小写断言受 Windows 环境变量不区分大小写影响。本轮新增/修改的四个包全部通过，且全仓构建通过。这些平台失败未通过修改上游测试或全仓换行符来掩盖。

## 复杂度审查

本轮实现跨 AimiliVPN、Gateway UI/API/编排和 3x-ui 数据/API/订阅层，达到复杂度审查门槛。审查只允许删除重复映射、无消费者抽象和不必要扩展点；不以“简化”为名削弱所有权、只读协议验证或事务回滚。

结果：`Lean already. Ship.`。没有发现可安全删除的依赖、无消费者扩展点或重复抽象，因此未为形式上的“精简”改写已经通过故障注入的事务路径。

## 实际完成、真实验证与未执行

### 实际完成

- Task 1～7 的三仓库本地代码、测试和可复现 3x-ui 补丁已完成。
- 本节列出的本地验证均使用本轮最新代码执行。

### 未执行

- 尚未在 `ssh ny` 执行 Stage 0 基线与备份。
- 尚未安装 AimiliVPN、定制 3x-ui 或 Gateway 资产。
- 尚未执行 Xray 离线生产配置检查、逐协议阶梯切换、300 秒观察和重启验收。
- 尚未通过 v2rayN GUI 刷新订阅并逐条点击四个公网节点与四个 SOCKS5H。

因此，本记录当前只证明本地实现和可复现构建成立，不能据此声称生产或客户端原路径已经验收。
