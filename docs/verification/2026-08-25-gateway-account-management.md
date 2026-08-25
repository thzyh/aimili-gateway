# Gateway 账户管理与可选 TOTP 验证记录

## 范围

- 验证日期：2026-08-25（Asia/Shanghai）。
- 功能分支：`feat/gateway-account-management`。
- 基线提交：`53df5d2`。
- 本记录不包含密码、Cookie、TOTP 密钥、UUID、私钥、随机后台路径或完整订阅链接。

## 本地工具链

- Go：`go1.26.7 windows/amd64`，来自 `E:\SoftWare\Go\current`。
- Node.js：`v24.16.0`。
- npm：`11.13.0`。
- 前端依赖：严格使用 `web/package-lock.json` 执行 `npm ci`，未修改锁文件或依赖版本。

## 测试驱动验证

- SQLite 迁移测试先因 `TOTPEnabled` 和事务接口不存在而失败；实现后通过。
- 账户菜单测试先因 `account` 命令不存在而失败；实现后覆盖状态查询、用户名修改、随机密码、自定义密码、TOTP 启用确认、错误验证码不提交和关闭 TOTP。
- 认证测试先得到认证选项 404、密码模式登录 400；实现后密码模式和密码加 TOTP 模式均通过。
- Vue 测试先确认原页面固定显示 TOTP 且不读取认证选项；实现后按服务端状态条件显示。
- 部署契约测试先因简单账户入口文件不存在而失败；实现后通过。

## 本地完整验证

在功能提交 `b907e92` 上执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\verify-v1a.ps1
```

最新结果：PASS。

- Vue：4 个测试文件、11 项测试通过。
- Vue 类型检查和生产构建：PASS。
- Go：12 个含测试的包、96 项测试通过。
- `go test ./... -race -count=1`：PASS。
- `go vet ./...`：PASS。
- Gateway 和管理员 CLI 构建：PASS。
- 账户命令 Bash 语法：PASS。
- Git 空白检查和敏感信息扫描：PASS。

本机 Go module 缓存在受限目录中尝试写入统计缓存时输出权限警告，但两个构建命令均以退出码 0 完成；编译缓存已改用系统临时目录，未改变全局 Go 配置。

## `ny` 授权部署验证

- 部署前 Gateway、AimiliVPN、3x-ui/Xray 和 Caddy 均为 `active`，HTTPS 返回 200。
- 在 root 专用 `0700` 目录创建旧 Gateway、管理员工具和 SQLite 一致性备份。
- 备份数据库与部署后数据库的 `PRAGMA integrity_check` 均为 `ok`。
- 上传产物与本机构建 SHA-256 一致；Gateway 和管理员工具为 Linux `x86-64` 静态 ELF。
- 数据库迁移版本 2 只存在一次，既有账户迁移时先保持 TOTP 启用。
- 通过 `sudo aimili-gateway-account` 真实查询账户状态；输出只包含用户名、TOTP 状态和时间字段。
- 通过同一命令关闭 Gateway TOTP 后，`totp_enabled = 0`、加密密文为空、未撤销会话数量为 0。
- `/api/v1/auth/options` 返回 `totpRequired: false`。
- 真实浏览器登录页只有用户名和密码各一个输入框，TOTP 输入框数量为 0，登录按钮可用，浏览器警告和错误日志为空。
- HTTPS 返回 200，CSP、`X-Frame-Options`、`X-Content-Type-Options` 和 `Referrer-Policy` 保持生效。
- Gateway、AimiliVPN、3x-ui/Xray 和 Caddy 均为 `active`，没有 failed unit；Gateway、AimiliVPN 和 3x-ui 管理端继续监听回环地址。
- Gateway 和 Caddy 自本次部署后的 warning 级日志为空；账户瞬态单元日志未匹配到 32 字符随机密码模式。

## 最新边界与待用户动作

真实测试已经执行过“生成随机新密码”，从而验证安全信息更新时间、TOTP 关闭状态保持和全部会话撤销；随机值未输出到本记录或对话，也没有保存供后续使用。因此当前 Gateway 管理员密码是用户未知的一次性随机值。

为避免再次传播测试密码，本次未声称真实密码登录已经完成。用户应执行：

```bash
sudo aimili-gateway-account
```

选择“设置自定义新密码”，隐藏输入两次。设置后旧随机密码失效、全部会话再次撤销；登录页继续不要求动态验证码。该待用户动作不影响四个服务运行，也不改变 3x-ui 专家模式的独立账户和认证设置。

## 部署提交

- 功能与本地验证：`8fb1e84`。
- Linux 脚本 LF 行尾契约修复：`8f447cd`。

## 重复运行缺陷修复

- 用户原路径复现：再次执行 `sudo aimili-gateway-account` 时，`systemd-run` 报告同名服务已经加载或存在 fragment file，账户管理程序尚未启动。
- 根因证据：`aimili-gateway-account.service` 为 `/run/systemd/transient/` 下已成功退出但仍处于 loaded 状态的瞬态 unit；入口脚本同时固定传入 `--unit=aimili-gateway-account`，第二次创建同名 unit 被 systemd 拒绝。该故障与账户、密码、TOTP、SQLite 和 Gateway 常驻服务无关。
- 回归测试：部署契约先明确因入口包含固定 `--unit=` 而失败；移除固定命名后定向测试通过。
- 最小修复：保留 `--wait --collect --pty`、`aimili-gateway` 受限用户、加密凭据加载和全部安全隔离属性，仅让 `systemd-run` 为每次调用自动生成唯一 unit 名。
- 本地验证：在提交 `6b53335` 上重新执行 `scripts\verify-v1a.ps1`，Vue 11 项测试、前端生产构建、Go race、Go vet、两个二进制构建、Bash 语法、Git 空白检查和敏感产物扫描均通过。
- VPS 部署：上传文件、安装文件与本地脚本 SHA-256 一致；连续两次按用户原命令进入中文菜单并选择退出，退出码均为 0，没有执行账户查询、密码重置或 TOTP 变更。
- 部署后健康检查：Gateway、AimiliVPN、3x-ui 和 Caddy 均为 `active/running`，failed unit 为空；`https://ny.zouyunhui.cc.cd/` 返回 200，认证选项仍为 `totpRequired: false`。
