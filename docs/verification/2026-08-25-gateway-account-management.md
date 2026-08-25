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

## 待完成的授权部署验证

以下项目在合并回本地 `main` 并部署 `ny` 后补充最新证据：

- 数据库一致性备份与迁移 002。
- `sudo aimili-gateway-account` 真实交互和 TOTP 关闭。
- 登录页不显示动态验证码及密码模式真实登录。
- Gateway、AimiliVPN、3x-ui/Xray、Caddy 四服务状态和独立认证边界。
- 部署后二进制哈希、HTTPS、安全响应头和日志脱敏检查。
