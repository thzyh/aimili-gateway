# V1-A 本地验证记录

## 验证环境

- 验证时间：2026-08-24 23:53:46 +08:00
- Go：`go1.26.7 windows/amd64`
- Node.js：`v24.16.0`
- npm：`11.13.0`
- GCC：`MinGW-w64 16.2.0`（UCRT）

## 自动化验证

- `scripts/verify-v1a.ps1`：PASS。
- Vue 测试、Vue 生产构建、全仓 Go race 测试、`go vet ./...`、两个 Go 二进制构建、`git diff --check`、跟踪文件秘密模式与生成物扫描：PASS。
- `scripts/verify-v1a.sh`：已完成与 PowerShell 脚本同序的 fail-fast 检查定义；本机因 WSL 服务访问被拒绝，未执行 Linux shell 运行验证。该环境限制不代表 Linux 脚本已在本机通过。

## 本地用户路径

- 使用仓库外的一次性配置、数据库和管理员完成本地初始化：PASS。
- 在浏览器中输入一次性用户名、密码和当前 TOTP，登录统一控制台：PASS。
- 总览分别显示 Aimili Gateway、AimiliVPN 和 3x-ui；Gateway 为正常，两个未监听的底层探测目标安全降级为不可用：PASS。
- 页面未使用 iframe；专家模式链接声明新窗口语义，目标页面显示 3x-ui 独立登录表单，并明确不共享 Gateway 会话：PASS。
- 停止 Gateway 后，Gateway 路由不可用，独立专家模式测试服务仍可访问；没有触发底层服务停止或配置修改：PASS。
- 浏览器标签、测试进程、一次性数据库、主密钥和凭据文件均已清理。

## V1-A 范围复核

- AimiliVPN 适配器只有回环 TCP 只读探测，不包含写 API。
- 3x-ui 适配器只有回环 CSRF 端点只读探测，不保存 3x-ui 凭据，不包含写操作。
- 数据模型只有一个本地管理员，不包含多用户、多租户、组织或角色模型。
- 未修改生产 Caddy 路由，未连接或修改生产服务器。
