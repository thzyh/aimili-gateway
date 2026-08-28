# 账户同步后自动登录修复验证

## 结论

2026-08-28 已修复“账户命令显示三服务同步成功，但 AimiliVPN 和 3x-ui 自动入口同时失败”的问题。统一用户名或密码发生变化后，账户管理程序现在会立即结束，由稳定入口脚本马上重载 Gateway；用户不再需要返回菜单后另行选择退出。

VPS 修复后的真实路径验证结果：

- Gateway 健康检查通过，账户同步状态恢复为 `synced`。
- Gateway 登录成功后，AimiliVPN 自动入口成功签发后台会话并打开原后台页面。
- Gateway 登录成功后，3x-ui 自动入口成功签发后台会话并打开专家模式页面。
- 验收程序只输出稳定通过状态；未输出密码、Cookie、TOTP、UUID、私钥、后台路径或完整代理地址。
- 一次性 Gateway 验收会话已撤销，本地和 VPS 的一次性验收程序均已清理。

## 故障证据链

本次复现后的最新证据如下：

1. 账户同步操作于 `2026-08-28 03:17:23 UTC` 完成，数据库账户操作记录为 `change / complete / success`。
2. 账户管理瞬态单元从 `2026-08-28 03:07:48 UTC` 起仍处于运行状态，程序已经返回菜单并等待下一次输入。
3. 当时 Gateway 主进程仍是 `2026-08-27 17:20:36 UTC` 启动的旧进程，账户包装脚本尚未执行菜单退出后的 `systemctl try-restart aimili-gateway.service`。
4. 用户点击两个自动入口时，旧 Gateway 继续使用旧的 3x-ui 适配器凭据进行同步检查，3x-ui 最新日志明确记录连续登录失败并触发临时限流。
5. 结束旧账户任务、重启 Gateway，并等待 3x-ui 限流窗口结束后，启动检查于 `2026-08-28 03:35:17 UTC` 将同步状态恢复为 `synced`。

第一处失败边界因此确定为：账户管理程序在统一凭据提交成功后继续显示菜单，导致外层重载动作未执行。3x-ui 临时限流是该问题造成的后续结果，不是密码同步事务失败。

## 最小修复

- `cmd/aimili-gateway-admin/account.go`：选项 2、3、4、7 成功修改统一凭据后，显示重载说明并立即返回成功；失败时仍保留菜单，方便用户修正输入或重试。
- `cmd/aimili-gateway-admin/account_test.go`：新增回归测试，使用没有尾随退出选项的输入验证修复成功后程序直接结束，确保外层脚本能够继续重载 Gateway。
- `README.md`：修正菜单编号，并明确统一凭据更新成功后自动退出和重载的行为。

未修改 AimiliVPN、3x-ui 或 Xray 核心，也未改变三个服务独立运行的架构。

## 本地验证

| 检查 | 结果 |
| --- | --- |
| 回归测试先失败 | 修复前退出码为 1，证明测试能够复现菜单等待问题 |
| `go test ./... -count=1` | 通过 |
| `go test -race ./cmd/aimili-gateway-admin ./internal/accountsync ./internal/backendlogin ./internal/adapters/xui -count=1` | 通过 |
| `go vet ./...` | 通过 |
| Linux `amd64` 管理二进制构建 | 通过 |
| `git diff --check` | 通过 |

## VPS 部署与验收

- 更新后的 `/usr/local/bin/aimili-gateway-admin` 使用临时目标校验后原子替换，旧二进制已保留为本地回滚副本。
- 部署后二进制 SHA-256 为 `45cf6600971a28b68fd4067bb7ebfe18adc637c42e08d83bb25b3effd2718c11`。
- `aimili-gateway.service`、`aimilivpn.service`、`x-ui.service`、`caddy.service` 均为 `active`。
- 受限瞬态单元内的真实端到端验收输出为：AimiliVPN 自动入口通过、3x-ui 自动入口通过、整体通过，退出码为 0。

本次没有再次修改用户刚设置的统一密码。今后运行 `sudo aimili-gateway-account` 并成功执行选项 2、3、4 或 7 后，命令会直接返回终端；等待数秒后使用新凭据登录 Gateway 即可。
