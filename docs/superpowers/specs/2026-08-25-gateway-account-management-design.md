# Aimili Gateway 账户管理与可选 TOTP 设计及实施计划

> **后续修订：** 本文记录 2026-08-25 已实施的 Gateway 自身账户管理。2026-08-27 批准的 `2026-08-27-advanced-settings-unified-credentials-design.md` 将在后续阶段把用户名和密码扩展为 Gateway、AimiliVPN、3x-ui 三服务统一凭据，并增加服务端自动代登录；在新设计实施和验收前，本文描述仍是当前运行事实。

> **执行要求：** 批准后由当前主 Agent 使用 `superpowers:executing-plans` 在本对话内逐项实施；不使用子 Agent。所有步骤使用复选框跟踪，先测试后实现。

**目标：** 为个人单管理员 Gateway 提供一个简单的本地账户管理菜单，并让密码登录与可选 TOTP 在数据库、API、前端和部署层保持一致。

**架构：** 保留现有 Go Gateway、Vue 前端、SQLite 和 systemd 加密凭据边界。新增事务化管理员更新接口和 root 启动的受限本地入口；Web 端只读取是否需要 TOTP，不暴露账户信息。

**技术栈：** Go 1.26、SQLite、Vue 3.5、TypeScript 5.9、Vitest、systemd、PowerShell/Bash 验证脚本。

**规格与计划：** 本文同时作为本次变更的正式设计和唯一实施计划，不再拆分第二份计划文档。

## 1. 文档状态

- 日期：2026-08-25
- 项目：`aimili-gateway`
- 适用增量：V1-A
- 使用范围：个人单管理员
- 状态：已批准并实施；`ny` 部署验证记录见 `docs/verification/2026-08-25-gateway-account-management.md`
- 前置设计：`2026-08-24-unified-console-design.md`

本文定义 Aimili Gateway 管理员的本地管理命令、密码重置、可选 TOTP、登录页面条件显示、数据迁移和安全边界。本文只修订 Gateway 自身认证，不改变 AimiliVPN、3x-ui/Xray、Caddy 的独立服务边界，也不为 3x-ui 专家模式提供统一登录或真正 SSO。

本文已经获得用户一次总批准，并据此连续完成编码、本地验证、提交、合并到本地 `main` 和授权的 `ny` 部署。实际结果、最新验证证据和仍需用户完成的个人密码设置见验证记录。

## 2. 已确认需求

1. 个人使用场景不考虑多租户，继续只支持一个 Gateway 管理员。
2. 采用单一交互式账户管理命令，不要求用户记忆多个复杂子命令或手写 `systemd-run` 参数。
3. 可以查询当前用户名，但不能查询当前明文密码。
4. 密码重置同时支持安全随机密码和用户自定义密码。
5. TOTP 为可选功能；新账户默认关闭。
6. TOTP 关闭时，登录页不显示“动态验证码”；启用时才显示并要求输入。
7. 本地命令支持启用、重新登记和关闭 TOTP。
8. 安全信息变更后撤销全部现有 Gateway 会话。

## 3. 当前实现基线

截至本设计编写时，仓库 `main` 分支的 V1-A 已实现并在授权的 `ny` 测试部署中验证：

- Gateway 密码加 TOTP 登录和重新认证。
- Argon2id 密码哈希、加密 TOTP 密钥和 SQLite 服务端会话。
- `aimili-gateway-admin init` 与 `revoke-sessions` 本地命令。
- systemd 加密凭据加载 Gateway 主密钥。
- Vue 登录页固定显示并强制填写 6 位 TOTP。

当前管理员表要求 `totp_secret_ciphertext` 非空，登录及重新认证 API 也强制要求 TOTP。因此本需求不能只删除前端输入框，必须同步修改数据库、存储层、认证 API、前端和本地管理员工具。

## 4. 范围与非目标

### 4.1 本次范围

- Gateway 单管理员账户状态查询。
- Gateway 用户名修改。
- Gateway 随机密码和自定义密码重置。
- Gateway TOTP 启用、重新登记和关闭。
- Gateway 登录与重新认证根据账户状态选择是否验证 TOTP。
- 登录页根据服务端返回的认证选项条件显示 TOTP。
- 既有数据库的安全迁移和现有会话撤销。
- 简化 systemd 加密凭据环境下的本地账户管理入口。

### 4.2 非目标

- 不提供当前明文密码恢复或显示。
- 不在 Web 控制台提供账户安全设置页面。
- 不增加管理员数量、角色、租户或找回邮箱。
- 不改变 3x-ui 专家模式账户、密码、TOTP 或 Cookie。
- 不把 Gateway 与 3x-ui 的相同密码描述为 SSO。
- 不修改 AimiliVPN、3x-ui/Xray 核心或服务器其他安全配置。
- 不在本设计阶段连接或修改 VPS。

## 5. 用户入口与交互

### 5.1 统一管理命令

对用户公开一个稳定入口：

```bash
sudo aimili-gateway-account
```

命令显示中文交互菜单：

```text
1. 查看账户状态
2. 修改用户名
3. 生成随机新密码
4. 设置自定义新密码
5. 启用或重新登记 TOTP
6. 关闭 TOTP
7. 撤销全部登录会话
0. 退出
```

安装的入口脚本负责以受限的瞬态 systemd 单元运行内部管理员程序，并自动挂载既有的 Gateway 加密主密钥凭据。用户不需要复制加密凭据、设置环境变量或手写复杂的 `systemd-run` 参数。

瞬态单元使用 `aimili-gateway` 用户和组访问既有配置、SQLite 数据库及运行时凭据；禁止网络访问，不获得 shell、Linux capabilities 或修改 systemd/Caddy 配置的权限。入口脚本与内部二进制由 root 所有且普通用户不可写。

现有 `aimili-gateway-admin` 可以继续作为内部实现和兼容入口，但面向日常使用的文档统一推荐 `aimili-gateway-account`。

### 5.2 查看账户状态

只显示：

- 当前用户名。
- TOTP 是否启用。
- 账户创建时间。
- 最近安全信息更新时间。

不得显示密码哈希、当前密码、TOTP 密钥、加密密文、会话令牌或主密钥路径内容。

### 5.3 修改用户名

- 输入值去除首尾空白后必须非空。
- 最多 128 个 Unicode 字符且 UTF-8 编码不超过 256 字节。
- 更新成功后刷新 `security_updated_at` 并撤销全部现有会话。
- CLI 输出只确认操作结果，不回显其他敏感字段。

### 5.4 生成随机新密码

- 使用操作系统密码学安全随机源生成 24 个随机字节。
- 以无填充 Base64URL 编码为 32 个可复制字符。
- 新密码只在当前终端成功提交后显示一次，不写日志、数据库明文字段、命令参数、环境变量或审计详情。
- 更新密码哈希、更新时间和撤销全部会话必须处于同一数据库事务。
- 随机生成或数据库提交失败时，不显示候选密码，原密码保持有效。

### 5.5 设置自定义新密码

- 终端关闭回显，要求输入两次并完全一致。
- 至少 12 个 Unicode 字符，最多 256 个 UTF-8 字节。
- 不允许首尾空白。
- 明文只保留在当前进程内存中，完成哈希后尽力清零相关字节缓冲区。
- 更新密码哈希、更新时间和撤销全部会话必须处于同一数据库事务。

### 5.6 启用或重新登记 TOTP

1. 在内存中生成新的 TOTP 密钥。
2. 在当前终端一次性显示 enrollment URI，供密码管理器或验证器登记。
3. 要求输入新验证器生成的 6 位验证码。
4. 本地验证成功后才加密密钥并提交数据库。
5. 在同一事务中设置 `totp_enabled = true`、更新时间并撤销全部会话。

如果验证码错误、输入中断或数据库提交失败，旧 TOTP 状态和旧密钥保持不变。新密钥不得写入普通日志、Shell 历史或临时文件。

### 5.7 关闭 TOTP

- 菜单明确提示关闭后 Gateway 将只使用密码认证，并要求输入确认词。
- 确认后在同一事务中设置 `totp_enabled = false`、清空 TOTP 加密密文、更新时间并撤销全部会话。
- 已经关闭时返回幂等提示，不重复修改。

## 6. 数据模型与迁移

新增顺序迁移 `internal/store/migrations/002_optional_totp.sql`，将管理员模型调整为：

```text
admin
├─ id
├─ username
├─ password_hash
├─ totp_enabled
├─ totp_secret_ciphertext（可空）
├─ created_at
└─ security_updated_at
```

约束如下：

- `totp_enabled` 只允许 `0` 或 `1`。
- `totp_enabled = 1` 时，加密密文必须非空。
- `totp_enabled = 0` 时，加密密文必须为空。
- 新建管理员默认 `totp_enabled = 0`。
- 既有管理员在迁移时设置 `totp_enabled = 1` 并保留原加密密文，避免升级过程静默降低认证强度。

由于 SQLite 不能直接放宽既有列的 `NOT NULL` 约束，迁移在单一事务中创建新表、复制并校验旧数据、替换旧表，再记录迁移版本。任何一步失败都必须整体回滚，旧账户仍可按密码加 TOTP 登录。

管理员安全信息更新与 `sessions` 全部撤销由存储层提供一个事务化操作，禁止通过删除整个 SQLite 数据库来重置账户。

## 7. 登录和重新认证 API

### 7.1 认证选项

新增匿名只读端点：

```text
GET /api/v1/auth/options
```

只返回：

```json
{"totpRequired": false}
```

该接口不得返回用户名、账户创建时间或其他可用于账户枚举的信息，并设置 `Cache-Control: no-store`。

### 7.2 登录请求

登录请求保持用户名和密码必填，将 `totp` 改为可选字段：

```json
{
  "username": "...",
  "password": "...",
  "totp": "..."
}
```

- 当前管理员关闭 TOTP：后端只验证用户名和密码，忽略空的 `totp`。
- 当前管理员启用 TOTP：后端要求 `totp` 为 6 位数字并完成验证。
- 认证失败继续返回统一错误，不区分用户名、密码或 TOTP 哪一项错误。
- 登录速率限制、Cookie 安全属性和会话生命周期保持现有 V1-A 规则。

### 7.3 重新认证

敏感操作的重新认证采用同一账户状态：

- TOTP 关闭时只要求密码。
- TOTP 启用时要求密码和 6 位 TOTP。
- 不因为已有登录会话而绕过重新认证。

## 8. 前端行为

1. 登录页加载时请求 `/api/v1/auth/options`。
2. `totpRequired = false` 时只显示用户名和密码，不渲染“动态验证码”标签或输入框。
3. `totpRequired = true` 时显示动态验证码输入框并进行 6 位数字校验。
4. 获取认证选项失败时采用安全失败策略：不提交猜测的密码模式，显示“暂时无法获取登录方式，请稍后重试”。
5. 登录失败后清空密码和 TOTP，不清空用户名。
6. 密码和 TOTP 不写入 `localStorage`、`sessionStorage`、URL、分析事件或前端日志。

登录页仍明确提示：Gateway 管理员账户与 3x-ui 专家模式账户相互独立。

## 9. 安全与一致性边界

- 当前密码继续使用 Argon2id 单向哈希，产品和文档均不得提供“查询当前密码”功能。
- 管理命令只能在服务器本地通过 `sudo` 使用，不新增公网恢复接口。
- CLI 不接受明文密码命令行参数，避免进入进程列表和 Shell 历史。
- 账户变更、TOTP 状态更新和会话撤销必须原子提交。
- CLI 账户安全操作记录脱敏审计事件，`session_id` 为空；审计只记录动作、结果和错误分类，不记录用户名、密码或 TOTP 数据。
- Gateway 服务可继续运行；CLI 依赖 SQLite 事务和 busy timeout 与服务并发，不通过停止服务规避一致性问题。
- TOTP 关闭只影响 Gateway。3x-ui 专家模式仍使用 3x-ui 自身的认证配置。

## 10. 兼容性、升级与回滚

### 10.1 升级行为

- 数据库迁移先执行且具备回滚原子性。
- 现有账户升级后继续要求 TOTP，现有登录路径不会因部署新版本而失效。
- 用户需要在当前 VPS 去除动态验证码时，部署完成后显式执行 `sudo aimili-gateway-account` 并选择“关闭 TOTP”。
- 新安装或全新数据库初始化的账户默认关闭 TOTP。

### 10.2 应用回滚边界

一旦数据库迁移到可选 TOTP 结构，旧版二进制不应直接重新启用，因为旧存储模型不能读取关闭 TOTP 后的空密文。应用回滚必须同时使用部署前的 SQLite 备份，或先通过新管理命令启用并确认 TOTP，再执行经过验证的兼容回滚流程。

部署过程必须在迁移前创建权限严格的 SQLite 一致性备份，并验证备份可打开；备份文件不得进入 Git 或普通下载目录。

## 11. 验证与验收标准

### 11.1 自动化验证

- 迁移旧数据库后，原管理员仍保持 TOTP 启用且原凭据可登录。
- 新账户默认关闭 TOTP，TOTP 密文为空。
- 数据约束拒绝启用状态与密文状态不一致的数据。
- 密码模式登录与密码加 TOTP 模式登录均覆盖成功和失败路径。
- 重新认证遵循同一 TOTP 状态。
- 登录页分别验证 TOTP 输入框隐藏和显示。
- 认证选项接口不返回用户名或敏感字段并禁止缓存。
- 随机密码来自安全随机源、格式正确且只在成功后输出一次。
- 自定义密码隐藏输入、二次确认并执行长度规则。
- TOTP 重置只有在新验证码确认后提交；失败时旧配置保持有效。
- 修改用户名、密码或 TOTP 与撤销会话保持事务原子性。
- 简化入口正确加载 systemd 加密凭据，普通组可读或 world-readable 主密钥仍被拒绝。
- 完整执行 Go、Vue、部署契约、敏感信息扫描和 `scripts/verify-v1a.ps1`。

### 11.2 授权部署后的真实验证

在实施计划和代码分别批准、且获得对应 VPS 修改授权后，使用真实浏览器验证：

1. 关闭 TOTP 后，登录页不显示动态验证码，用户名和新密码可登录。
2. 随机密码重置后，旧密码失败、新密码成功，旧会话失效。
3. 自定义密码重置后，新密码成功且不会在日志中出现。
4. 启用 TOTP 后，登录页重新显示动态验证码；缺少或错误验证码登录失败，正确验证码成功。
5. TOTP 重置失败不会破坏原 TOTP；成功后旧验证码不再有效。
6. 账户状态查询只显示允许字段。
7. Gateway、AimiliVPN、3x-ui/Xray 和 Caddy 仍保持独立运行；专家模式继续使用 3x-ui 自身登录。

验证记录不得包含密码、Cookie、TOTP 密钥、UUID、私钥、随机后台路径或完整订阅链接。

## 12. 一次总批准的授权边界

用户批准本文后，授权连续执行以下范围：

- 在隔离工作树内修改本文列出的 Gateway 文件并下载锁文件已声明的项目依赖（仅在本机缓存缺失时）。
- 运行 Go、Vue、部署契约、构建、敏感信息扫描和真实浏览器测试。
- 创建功能提交，通过验证后合并回本地 `main`；不推送远端仓库。
- 连接 `ssh ny`，备份并更新 Aimili Gateway 二进制、管理员工具和账户命令，执行数据库迁移，关闭当前 Gateway TOTP，重启并验证 Gateway。
- 只读核对 AimiliVPN、3x-ui/Xray 和 Caddy 仍独立运行；除恢复 Gateway 路由所必需外，不修改这三个服务。

本文不授权重写 AimiliVPN、3x-ui/Xray、修改 SSH/防火墙/内核、升级操作系统或输出任何秘密。若实际状态要求超出上述边界的动作，将停止扩展并汇报；普通测试失败和本文范围内的代码修复不再逐项询问。

---

## 13. 实施文件清单

### 13.1 新建文件

- `internal/store/migrations/002_optional_totp.sql`：迁移管理员表并保持既有账户启用 TOTP。
- `cmd/aimili-gateway-admin/account.go`：中文菜单、状态查询、用户名/密码/TOTP 操作。
- `cmd/aimili-gateway-admin/account_test.go`：菜单和秘密处理测试。
- `deploy/bin/aimili-gateway-account`：用户执行的稳定入口和瞬态 systemd 单元参数。
- `docs/verification/2026-08-25-gateway-account-management.md`：不含秘密的本地与 `ny` 最新验证记录。

### 13.2 修改文件

- `internal/store/admin.go`、`internal/store/store_test.go`：可选 TOTP 模型与事务化安全更新。
- `cmd/aimili-gateway-admin/main.go`、`main_test.go`：保留旧子命令，新增 `account` 菜单并让新账户默认关闭 TOTP。
- `internal/httpapi/server.go`、`auth_handlers.go`、`auth_handlers_test.go`：认证选项、可选 TOTP 登录和重新认证。
- `web/src/api/client.ts`：新增 `AuthOptionsPayload` 类型。
- `web/src/views/LoginView.vue`、`LoginView.spec.ts`：加载认证选项并条件渲染动态验证码。
- `deploy/deploy_contract_test.go`：约束账户入口的权限和 systemd 隔离参数。
- `scripts/verify-v1a.ps1`、`scripts/verify-v1a.sh`：构建并验证账户入口资产。
- `README.md`：中文账户管理和密码不可恢复说明。
- 本文：执行时更新复选框和最终状态，不记录秘密。

不修改 `go.mod`、`go.sum`、`web/package.json` 或 `web/package-lock.json`；本功能不引入新依赖。

## 14. Task 1：隔离工作树和基线

**接口：** 本任务不修改业务接口；产出可回滚的功能分支和基线结果。

- [ ] **Step 1：创建隔离工作树**

  使用 `superpowers:using-git-worktrees`，从最新本地 `main` 创建 `feat/gateway-account-management`，工作树放在仓库既有 `.worktrees/` 下。先确认主工作树只有已批准的本文变更，再把本文带入功能分支。

- [ ] **Step 2：记录精确基线**

  ```powershell
  git status --short --branch
  git log -5 --oneline
  $env:GOTOOLCHAIN = 'local'
  & 'E:\SoftWare\Go\go1.26.0\bin\go.exe' version
  node --version
  npm --version
  ```

  如果 Go 1.26 的实际安装目录与上式不同，只使用 `E:\SoftWare\Go` 下已安装的 1.26.x，并在验证记录中写版本号，不下载新 Go。

- [ ] **Step 3：运行未修改代码的完整基线**

  ```powershell
  powershell -ExecutionPolicy Bypass -File scripts\verify-v1a.ps1
  ```

  预期：脚本输出 `V1-A verification passed`。如基线失败，先按最新错误做最小诊断，不把旧验证记录当作通过证据。

## 15. Task 2：SQLite 可选 TOTP 与原子安全更新

**文件：**

- 新建：`internal/store/migrations/002_optional_totp.sql`
- 修改：`internal/store/admin.go`
- 测试：`internal/store/store_test.go`

**接口：**

```go
type Admin struct {
    Username             string
    PasswordHash         []byte
    TOTPEnabled          bool
    TOTPSecretCiphertext []byte
    CreatedAt            time.Time
    SecurityUpdatedAt    time.Time
}

type AdminSecurityUpdate struct {
    ExpectedSecurityUpdatedAt time.Time
    Username                  string
    PasswordHash              []byte
    TOTPEnabled               bool
    TOTPSecretCiphertext      []byte
    Action                    string
    UpdatedAt                 time.Time
}

func (s *Store) UpdateAdminSecurityAndRevokeSessions(ctx context.Context, update AdminSecurityUpdate) error
```

该方法校验完整的新管理员状态，用 `security_updated_at` 做乐观并发检查，然后在一个事务中更新管理员、撤销所有未撤销会话并插入脱敏审计事件。并发冲突返回新增的 `ErrAdminChanged`。

- [ ] **Step 1：编写旧库迁移失败测试**

  在 `store_test.go` 创建只包含迁移 001 结构和一条假管理员数据的 SQLite 文件，再调用 `Open`，断言迁移后的 `TOTPEnabled` 为真、加密测试字节未改变、迁移版本 2 只出现一次。首次运行预期因迁移 002 不存在或字段不存在而失败。

  ```go
  if !admin.TOTPEnabled {
      t.Fatal("migrated administrator silently lost TOTP")
  }
  if count != 1 {
      t.Fatalf("migration 2 count = %d", count)
  }
  ```

- [ ] **Step 2：编写状态约束和事务原子性失败测试**

  覆盖新账户关闭 TOTP 时密文必须为空、启用时密文必须非空、预期时间戳冲突不改变数据、更新成功后旧会话不可读取、审计事件不含用户名或秘密。首次运行预期因新接口不存在而失败。

- [ ] **Step 3：实现迁移和存储接口**

  `002_optional_totp.sql` 在迁移事务中创建 `admin_v2`、复制旧数据并固定 `totp_enabled = 1`、删除旧表、重命名新表。表级 `CHECK` 保证以下等价关系：

  ```sql
  CHECK (
      (totp_enabled = 0 AND totp_secret_ciphertext IS NULL) OR
      (totp_enabled = 1 AND length(totp_secret_ciphertext) > 0)
  )
  ```

  `CreateAdmin` 和 `GetAdmin` 同步读写 `totp_enabled`。`UpdateAdminSecurityAndRevokeSessions` 使用一笔 `BEGIN` 事务完成条件更新、`sessions.revoked_at` 更新和 `audit_events` 插入，任何错误都回滚。

- [ ] **Step 4：验证存储层**

  ```powershell
  go test ./internal/store -run 'Test.*(Admin|Migration|Session)' -race -count=1 -v
  ```

  预期：新增迁移、约束、并发冲突和事务原子性测试全部通过。

- [ ] **Step 5：提交存储层**

  ```powershell
  git add internal/store/admin.go internal/store/store_test.go internal/store/migrations/002_optional_totp.sql
  git commit -m "feat: support optional gateway TOTP"
  ```

## 16. Task 3：本地中文账户管理菜单

**文件：**

- 新建：`cmd/aimili-gateway-admin/account.go`
- 新建：`cmd/aimili-gateway-admin/account_test.go`
- 修改：`cmd/aimili-gateway-admin/main.go`
- 修改：`cmd/aimili-gateway-admin/main_test.go`

**接口：**

```go
type commandDependencies struct {
    Now    func() time.Time
    Random io.Reader
}

func runWithDependencies(args []string, in io.Reader, out, errOut io.Writer, dependencies commandDependencies) int
func runAccountMenu(ctx context.Context, database *store.Store, masterKeyPath string, prompts *promptReader, out io.Writer, dependencies commandDependencies) error
func generateRandomPassword(random io.Reader) ([]byte, error)
```

`run` 保留现有测试签名，并使用 `time.Now` 和 `crypto/rand.Reader` 调用 `runWithDependencies`。命令接受 `init`、`revoke-sessions` 和 `account`；没有参数仍返回用法错误，公开包装脚本始终传入 `account`。

- [ ] **Step 1：编写菜单与状态查询失败测试**

  用内存输入 `1\n0\n` 调用菜单，断言输出包含用户名、TOTP 状态、创建时间和更新时间，但不包含密码哈希、TOTP 密文、主密钥字节或 enrollment URI。首次运行预期因 `account` 不存在而失败。

- [ ] **Step 2：编写用户名和两种密码重置失败测试**

  覆盖：用户名边界、24 个固定随机字节生成 32 字符 Base64URL、随机密码只在事务提交成功后输出一次、自定义密码二次确认、首尾空白和长度拒绝、成功后旧密码失败且全部会话撤销。测试不得把生成密码写入失败消息。

  ```go
  if strings.Count(output.String(), generated) != 1 {
      t.Fatal("random password was not emitted exactly once")
  }
  if _, err := database.GetSession(ctx, tokenHash, now); !errors.Is(err, store.ErrSessionNotFound) {
      t.Fatal("security update did not revoke sessions")
  }
  ```

- [ ] **Step 3：编写 TOTP 生命周期失败测试**

  固定随机源产生已知测试密钥，覆盖启用前关闭状态、错误验证码不提交、正确验证码启用、重新登记失败保留旧密钥、关闭时清空密文和撤销会话。测试只在内存解析 enrollment URI，不把密钥写入测试日志。

- [ ] **Step 4：让新初始化默认关闭 TOTP**

  修改 `initializeAdmin`：继续创建 32 字节 Gateway 主密钥和 Argon2id 密码哈希，但创建 `TOTPEnabled: false`、`TOTPSecretCiphertext: nil`，不输出 enrollment URI。更新原初始化测试，断言主密钥存在、TOTP 关闭且输出不含 `otpauth://`。

- [ ] **Step 5：实现中文菜单**

  `account.go` 采用循环菜单。秘密输入复用现有终端回显关闭逻辑；随机密码在更新事务提交后才打印；TOTP 先验证再加密提交；关闭 TOTP 要求输入完整确认词 `关闭 TOTP`。所有明文字节在返回前尽力清零。

- [ ] **Step 6：验证管理员工具**

  ```powershell
  go test ./cmd/aimili-gateway-admin -race -count=1 -v
  go build ./cmd/aimili-gateway-admin
  ```

  预期：菜单、初始化、密码、TOTP、并发冲突、秘密不回显和旧子命令兼容测试全部通过。

- [ ] **Step 7：提交管理员工具**

  ```powershell
  git add cmd/aimili-gateway-admin internal/store
  git commit -m "feat: add gateway account menu"
  ```

## 17. Task 4：认证选项与可选 TOTP API

**文件：**

- 修改：`internal/httpapi/server.go`
- 修改：`internal/httpapi/auth_handlers.go`
- 测试：`internal/httpapi/auth_handlers_test.go`

**接口：**

```go
type authOptionsResponse struct {
    TOTPRequired bool `json:"totpRequired"`
}

func (s *server) handleAuthOptions(http.ResponseWriter, *http.Request)
```

路由为 `GET /api/v1/auth/options`。`verifyCredentials` 读取 `Admin.TOTPEnabled`：关闭时不解密密文并将 TOTP 判断为真；启用时要求严格 6 位数字并验证。

- [ ] **Step 1：编写认证选项失败测试**

  分别创建 TOTP 开启、关闭和未初始化环境，断言响应只含一个布尔键、状态 200、`Cache-Control: no-store`，响应正文不含用户名。未初始化时返回 `totpRequired: false`，但登录仍为 401。

- [ ] **Step 2：编写两种登录和重新认证失败测试**

  覆盖关闭 TOTP 后省略 `totp` 可登录、错误密码仍失败、启用 TOTP 后省略或格式错误返回 400、错误 6 位码返回统一 401、正确码成功；重新认证执行相同规则。

- [ ] **Step 3：实现最小 API 变更**

  注册选项路由；把请求结构的 `totp` 保持为字符串但取消无条件非空校验；先读取管理员状态再决定格式要求。认证失败不得指出是密码还是 TOTP 错误，现有限速键和 Cookie/CSRF 行为不变。

- [ ] **Step 4：验证认证 API**

  ```powershell
  go test ./internal/httpapi -run 'Test(Auth|Login|Reauth)' -race -count=1 -v
  ```

  预期：密码模式、TOTP 模式、选项隐私、限速、Cookie 和 CSRF 测试全部通过。

- [ ] **Step 5：提交认证 API**

  ```powershell
  git add internal/httpapi/server.go internal/httpapi/auth_handlers.go internal/httpapi/auth_handlers_test.go
  git commit -m "feat: make gateway TOTP optional"
  ```

## 18. Task 5：登录页条件显示动态验证码

**文件：**

- 修改：`web/src/api/client.ts`
- 修改：`web/src/views/LoginView.vue`
- 测试：`web/src/views/LoginView.spec.ts`

**接口：**

```ts
export interface AuthOptionsPayload {
  totpRequired: boolean
}
```

- [ ] **Step 1：编写条件渲染失败测试**

  第一次 `apiFetch` 调用模拟 `/api/v1/auth/options`。返回 `false` 时断言不存在 `input[name="totp"]`，登录正文不含 `totp`；返回 `true` 时断言输入框存在、缺少或非 6 位码不会调用登录 API。

- [ ] **Step 2：编写失败安全和秘密不持久化测试**

  认证选项请求拒绝时，断言显示“暂时无法获取登录方式，请稍后重试”、登录按钮不可提交；两种模式均断言未调用 Storage API，失败或成功后密码/TOTP 被清空、用户名保留。

- [ ] **Step 3：实现前端行为**

  `LoginView.vue` 在 `onMounted` 中读取选项，使用 `optionsLoaded` 和 `totpRequired` 控制表单；TOTP 标签使用 `v-if="totpRequired"`。请求体只在启用时加入 `totp`：

  ```ts
  const credentials: Record<string, string> = {
    username: username.value,
    password: password.value,
  }
  if (totpRequired.value) credentials.totp = totp.value
  ```

  错误提示在密码模式使用“账户或密码”，TOTP 模式使用“账户、密码或动态验证码”，仍不泄露具体失败项。

- [ ] **Step 4：验证并构建前端**

  ```powershell
  npm test --prefix web -- LoginView.spec.ts
  npm run build --prefix web
  ```

  预期：登录页测试通过，Vue 类型检查和生产构建通过，构建产物进入现有 `internal/webassets/dist` 且不被 Git 跟踪。

- [ ] **Step 5：提交前端**

  ```powershell
  git add web/src/api/client.ts web/src/views/LoginView.vue web/src/views/LoginView.spec.ts
  git commit -m "feat: show TOTP only when required"
  ```

## 19. Task 6：简单命令入口、部署契约和使用说明

**文件：**

- 新建：`deploy/bin/aimili-gateway-account`
- 修改：`deploy/deploy_contract_test.go`
- 修改：`scripts/verify-v1a.ps1`
- 修改：`scripts/verify-v1a.sh`
- 修改：`README.md`

**入口契约：** `deploy/bin/aimili-gateway-account` 是 root 所有的 Bash 脚本，最终安装到 `/usr/local/sbin/aimili-gateway-account`。脚本只执行 `systemd-run --pty --wait --collect`，以 `aimili-gateway` 用户运行 `/usr/local/bin/aimili-gateway-admin account`，加载 `/etc/credstore.encrypted/aimili-gateway-master-key`，传入既有配置位置，并设置与常驻服务相称的文件系统、权限和网络隔离。入口不得为可重复调用的瞬态服务指定固定 unit 名，必须由 `systemd-run` 为每次调用生成唯一名称并在退出后收集，避免已加载的旧瞬态 unit 阻断后续调用。

- [ ] **Step 1：编写部署契约失败测试**

  断言脚本包含 `--pty`、`--wait`、`--collect`、`User=aimili-gateway`、`Group=aimili-gateway`、`LoadCredentialEncrypted`、`IPAddressDeny=any` 和内部 `account` 子命令；拒绝 `bash -c`、`sh -c`、`eval`、明文 credential 值、网络下载命令和任意用户传入命令。

- [ ] **Step 2：实现入口脚本**

  脚本使用固定绝对路径和 `exec`，拒绝任何位置参数。`systemd-run --pty` 让密码和 enrollment URI 只进入当前终端，不通过环境变量或命令行传递；部署验证还要确认 journald 未保存 CLI 的终端秘密输出。

- [ ] **Step 3：扩展两套验证脚本**

  两套脚本继续构建 `aimili-gateway-admin`，增加 Bash 语法检查（Linux 脚本使用 `bash -n`）和部署契约测试。敏感扫描继续拒绝私钥、完整代理 URI、UUID、HTTP 凭据头、数据库和 credential 文件。

- [ ] **Step 4：更新 README**

  写明唯一推荐命令和七项中文菜单；明确当前密码不可查询，只能重置；说明关闭 TOTP 只影响 Gateway，专家模式继续使用 3x-ui 登录。

- [ ] **Step 5：验证部署资产**

  ```powershell
  go test ./deploy -count=1 -v
  git diff --check
  ```

  预期：部署契约通过且没有空白错误。

- [ ] **Step 6：提交入口和文档**

  ```powershell
  git add deploy scripts README.md
  git commit -m "feat: add simple gateway account command"
  ```

## 20. Task 7：本地全量验证、复核和合并

- [ ] **Step 1：运行完整验证**

  ```powershell
  powershell -ExecutionPolicy Bypass -File scripts\verify-v1a.ps1
  ```

  必须看到 Vue 测试、生产构建、Go race、Go vet、两个二进制构建、部署契约、空白检查和敏感扫描全部通过，以及最终 `V1-A verification passed`。

- [ ] **Step 2：按规格逐项自审**

  对照本文第 2、4～11 节核对每个需求均有实现和测试；运行以下扫描并检查无明文秘密或未计划文件：

  ```powershell
  git status --short
  git diff main...HEAD --check
  git diff main...HEAD --stat
  git log --oneline main..HEAD
  ```

- [ ] **Step 3：创建本地验证记录**

  在 `docs/verification/2026-08-25-gateway-account-management.md` 记录 Git 提交、工具版本、测试命令、测试数量和结果；不得记录密码、Cookie、TOTP 密钥、UUID、私钥、随机后台路径或完整订阅链接。

- [ ] **Step 4：提交验证记录**

  ```powershell
  git add docs/verification/2026-08-25-gateway-account-management.md docs/superpowers/specs/2026-08-25-gateway-account-management-design.md
  git commit -m "docs: verify gateway account management"
  ```

- [ ] **Step 5：合并回本地 main**

  按用户已经选择的“合并回本地 main”方式执行 `superpowers:finishing-a-development-branch`。合并前确认主工作树无额外变化；合并后在 `main` 再运行一次 `scripts/verify-v1a.ps1`。不推送远端。

## 21. Task 8：授权的 ny 备份、部署与真实验证

本任务只在前七项通过且本文已获一次总批准后执行。部署不依赖新的密码、TOTP 或随机路径出现在本地文件或对话输出中。

- [ ] **Step 1：读取部署前最新状态**

  通过 `ssh ny` 只读核对服务器时间、架构、Gateway 当前提交/哈希、四个 systemd 服务状态、Gateway 回环监听、HTTPS 状态、数据库路径与权限。只记录非敏感摘要；不得读取或输出 3x-ui 随机后台路径、Cookie、密码或 TOTP 密钥。

- [ ] **Step 2：构建 Linux 产物**

  先构建 Vue，再使用本机 Go 1.26.x 交叉编译两个 Linux AMD64 二进制：

  ```powershell
  npm run build --prefix web
  $env:CGO_ENABLED = '0'
  $env:GOOS = 'linux'
  $env:GOARCH = 'amd64'
  go build -trimpath -o $temporaryGateway ./cmd/aimili-gateway
  go build -trimpath -o $temporaryAdmin ./cmd/aimili-gateway-admin
  ```

  产物放在系统临时目录，计算 SHA-256；部署后只比较哈希，不在仓库留下二进制。

- [ ] **Step 3：创建一致性备份**

  在 VPS 上创建 root 专用 `0700` 备份目录。使用服务器现有 Python 3 标准库 `sqlite3.Connection.backup` 对运行中的 Gateway 数据库创建一致性备份，再以只读连接执行 `PRAGMA integrity_check` 并只报告结果；同时备份旧 Gateway 和管理员二进制。不得安装 Python 包。

- [ ] **Step 4：原子安装并迁移**

  上传到 root 专用临时目录，核对哈希后使用 `install` 写入临时目标并原子替换 `/usr/local/bin/aimili-gateway`、`/usr/local/bin/aimili-gateway-admin` 和 `/usr/local/sbin/aimili-gateway-account`。重启 Gateway 触发迁移 002，检查 unit 为 `active`、数据库迁移版本存在、既有账户仍为 TOTP 开启。

- [ ] **Step 5：使用新命令关闭当前 Gateway TOTP**

  运行 `sudo aimili-gateway-account`，先选择“查看账户状态”，确认输出不包含秘密；再选择“关闭 TOTP”并完成确认。随后再次查询状态，确认 TOTP 已关闭且旧 Gateway 会话已撤销。该操作不修改 3x-ui TOTP。

- [ ] **Step 6：真实浏览器验证**

  通过 `https://ny.zouyunhui.cc.cd` 验证登录页不再显示动态验证码，并确认页面仍说明专家模式使用独立账户。若服务器仍保存受保护的现有测试登录凭据，则在不输出其值的前提下完成密码登录、总览和退出；若已不存在可恢复明文凭据，则通过账户命令在远端进程内生成一次性测试密码，仅在内存中完成登录验证，验证后撤销会话，并在最终交付中要求用户立即运行同一命令设置个人自定义密码。

- [ ] **Step 7：服务隔离与日志验证**

  核对 Gateway、AimiliVPN、3x-ui/Xray、Caddy 均为 `active`，HTTPS 页面和 Gateway API 正常，底层管理端仍只监听回环。检查本次时间点之后的 Gateway、Caddy 和瞬态账户单元日志，确认无错误且没有保存密码或 TOTP 终端输出；只汇报脱敏结论。

- [ ] **Step 8：失败回滚**

  如果新 Gateway 无法启动、迁移失败或真实登录失败，停止继续修改，恢复旧二进制和部署前 SQLite 一致性备份，重启 Gateway，验证原密码加 TOTP 登录路径和四个服务状态。AimiliVPN、3x-ui/Xray 与 Caddy 不随 Gateway 回滚而重装。

- [ ] **Step 9：清理和更新验证记录**

  成功后删除 VPS 上传临时目录和本地临时二进制，保留 root 专用备份用于短期回滚；在验证记录中加入部署时间、非敏感版本/哈希、迁移结果、页面条件显示、会话撤销和四服务状态。提交该记录到本地 `main`，再运行 `git status` 和文档敏感扫描。

## 22. 完成标准与用户交付

只有以下事实都有本次最新证据时，才报告完成：

- 本地 `main` 全套验证通过且没有未说明的工作树修改。
- `ny` 上 Gateway 新二进制、数据库迁移和简单账户命令已实际生效。
- 当前 Gateway TOTP 已关闭，登录页不渲染动态验证码。
- 密码模式登录经过真实路径验证；若使用一次性测试密码，明确告知用户仍需本地设置个人密码。
- 安全信息变更确实撤销旧会话。
- 3x-ui 专家模式仍使用独立认证，未声称实现 SSO。
- AimiliVPN、3x-ui/Xray、Caddy 和 Gateway 四个服务保持独立且状态正常。
- 回复和验证文档没有泄露任何密码、Cookie、TOTP 密钥、UUID、私钥、随机后台路径或完整订阅链接。

最终只给用户一个日常命令：

```bash
sudo aimili-gateway-account
```

并说明选择“生成随机新密码”会只显示一次，选择“设置自定义新密码”会隐藏输入；当前密码无法查询。
