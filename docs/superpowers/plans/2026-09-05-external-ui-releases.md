# Aimili Gateway 外部 UI 免重启发布 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Gateway 增加经过签名校验的外部 UI 版本目录、`current`/`previous` 原子切换和内嵌前端兜底，使首次启用后纯 UI 发布与回滚不再重启 Gateway。

**Architecture:** Gateway 每次请求从固定根目录解析 `current`，只接受根目录内的真实普通文件、兼容 manifest 和完整入口资产；解析失败时退回 `go:embed`。离线 root 安装器独立复验 Ed25519 签名、摘要、闭集路径和空间，再原子切换软链接并执行 HTTP 健康门，只保留 current、previous 和内嵌版本。

**Tech Stack:** Go 1.26、`net/http`、`io/fs`、`crypto/ed25519`、Vue 3/Vite 7、PowerShell 发布脚本、systemd。

**Spec:** `docs/superpowers/specs/2026-09-05-zero-downtime-ui-and-safe-self-update-design.md`

## Global Constraints

- UI Release 固定为 `manifest.json`、`manifest.sig` 和 `ui.tar.gz` 三个独立资产。
- manifest 固定 `schemaVersion: 1`、`kind: ui`、`apiVersion: v1`；版本 ID 是小写十六进制 SHA-256。
- 外部目录只能包含 `index.html`、`manifest.json` 和 `assets/` 下的普通文件；拒绝软链接、硬链接、设备、FIFO、绝对路径和 `..`。
- `current`、`previous` 必须解析到配置根目录下的单层 `releases/<version>`；每个请求只使用一次解析快照。
- index 和 manifest 使用 `Cache-Control: no-cache`；哈希资产使用 `public, max-age=31536000, immutable`。
- 只有无扩展名前端路由允许 SPA fallback；缺失静态文件必须返回 404。
- 日常 UI 切换不得重启 Gateway、AimiliVPN、x-ui/Xray 或 Caddy。
- 首次启用外部 UI 只允许重启 Gateway 一次，并验证四服务、数据库、进程数量、协议状态、端口和资源指纹不变。
- 生产始终只保留 current、previous 和内嵌兜底；不得保存数据库、配置、证书或数据面资产的 UI 回滚副本。
- 不提交签名私钥、真实发布 URL、凭据、Cookie、UUID、令牌或完整订阅链接。

---

### Task 1: 解析并安全提供外部 UI

**Files:**
- Create: `internal/webassets/release.go`
- Modify: `internal/webassets/embed.go`
- Modify: `internal/webassets/embed_test.go`
- Test: `internal/webassets/release_test.go`

**Interfaces:**
- Consumes: 内嵌 `distribution fs.FS`；生产目录结构 `<root>/releases/<version>` 与 `<root>/current`。
- Produces: `type Options struct { ExternalRoot string; APIVersion string }`、`func Handler(Options) http.Handler`、`func openCurrent(root, apiVersion string) (fs.FS, string, error)`。

- [ ] **Step 1: 写外部版本优先和内嵌兜底 RED 测试**

```go
func TestHandlerPrefersValidExternalRelease(t *testing.T) {
    root := makeExternalRelease(t, "aabb", "v1", map[string]string{"index.html": `<div id="external"></div>`, "assets/index-aabb.js": "ok"})
    response := httptest.NewRecorder()
    Handler(Options{ExternalRoot: root, APIVersion: "v1"}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
    if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="external"`) { t.Fatalf("external UI not served: %d %q", response.Code, response.Body.String()) }
}

func TestHandlerFallsBackWhenExternalReleaseIsInvalid(t *testing.T) {
    root := makeExternalRelease(t, "aabb", "v2", map[string]string{"index.html": "wrong API"})
    response := httptest.NewRecorder()
    Handler(Options{ExternalRoot: root, APIVersion: "v1"}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
    if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="app"></div>`) { t.Fatal("embedded fallback missing") }
}
```

- [ ] **Step 2: 运行定向测试并确认 RED**

Run: `go test ./internal/webassets -run 'TestHandler(PrefersValidExternalRelease|FallsBackWhenExternalReleaseIsInvalid)' -count=1`

Expected: FAIL，原因是 `Options` 或新 `Handler` 签名尚不存在。

- [ ] **Step 3: 实现一次请求一次快照的最小解析器**

```go
type Options struct { ExternalRoot, APIVersion string }
type uiManifest struct { SchemaVersion int `json:"schemaVersion"`; Kind, Version, APIVersion string }

func openCurrent(root, apiVersion string) (fs.FS, string, error) {
    // Lstat current；EvalSymlinks 后用 filepath.Rel 证明目标为 releases/<version>；
    // 拒绝越界、嵌套版本、非小写十六进制 version、manifest 不兼容和入口资产缺失；
    // 返回 os.DirFS(realRelease) 与 version，不缓存跨请求指针。
}
```

`Handler(Options)` 在请求开始时调用一次 `openCurrent`，成功则本请求固定使用该 `fs.FS`，失败则固定使用内嵌 `distribution`。

- [ ] **Step 4: 增加越界、损坏和 SPA/404 RED 测试**

```go
func TestOpenCurrentRejectsEscapingSymlink(t *testing.T) { /* current 指向 root 外，期望 error */ }
func TestOpenCurrentRejectsMissingReferencedAsset(t *testing.T) { /* index 引用不存在的 /assets/x.js，期望 error */ }
func TestHandlerReturnsNotFoundForMissingStaticAsset(t *testing.T) { /* GET /assets/missing.js => 404，不返回 index */ }
func TestHandlerFallsBackOnlyForExtensionlessRoute(t *testing.T) { /* /settings => index；/robots.txt => 404 */ }
func TestHandlerUsesOneReleaseSnapshotPerRequest(t *testing.T) { /* 切换 current 时单响应不混读两个版本 */ }
```

- [ ] **Step 5: 最小实现路径闭集、缓存头和 fallback 语义**

`release.go` 只解析 manifest 和 index 的本地 `/assets/` 引用；`embed.go` 把“选择文件系统”和“提供响应”分离。真实缺失文件返回 `http.NotFound`，只有 `path.Ext(requestedPath)==""` 才改读 `index.html`。

- [ ] **Step 6: 运行 webassets 完整测试**

Run: `go test ./internal/webassets -race -count=1`

Expected: PASS。

- [ ] **Step 7: 提交解析器**

```bash
git add internal/webassets/embed.go internal/webassets/embed_test.go internal/webassets/release.go internal/webassets/release_test.go
git commit -m "feat: serve verified external ui releases"
```

### Task 2: 配置和 Gateway 启动接线

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `deploy/config/config.example.json`
- Modify: `deploy/systemd/aimili-gateway.service`
- Modify: `deploy/deploy_contract_test.go`

**Interfaces:**
- Consumes: `webassets.Options` 与只读生产根 `/var/lib/aimili-gateway/ui`。
- Produces: `Config.ExternalUIRoot string`（JSON：`externalUiRoot`），生产默认空表示只用内嵌 UI。

- [ ] **Step 1: 写配置和接线 RED 测试**

```go
func TestLoadAcceptsAbsoluteExternalUIRoot(t *testing.T) { /* externalUiRoot=/var/lib/aimili-gateway/ui，期望保留 */ }
func TestValidateRejectsRelativeExternalUIRootOutsideLocalTest(t *testing.T) { /* 期望 externalUiRoot must be absolute */ }
func TestAppServesConfiguredExternalUI(t *testing.T) { /* 临时合法 release，经 App.Handler GET / 命中 external */ }
```

部署契约增加断言：示例配置为 `/var/lib/aimili-gateway/ui`，Gateway unit 含 `ReadOnlyPaths=/var/lib/aimili-gateway/ui`，不增加对 `/usr/local/bin`、`/etc` 或 updater spool 的写权限。

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/config ./internal/app ./deploy -run 'ExternalUI|ExternalUi' -count=1`

Expected: FAIL，字段与接线不存在。

- [ ] **Step 3: 实现最小配置和接线**

```go
type Config struct {
    // existing fields...
    ExternalUIRoot string `json:"externalUiRoot"`
}

// app.New
mux.Handle("/", webassets.Handler(webassets.Options{ExternalRoot: cfg.ExternalUIRoot, APIVersion: "v1"}))
```

空值继续使用内嵌 UI；非空值必须为 clean 的绝对路径。systemd 只读开放固定 UI 根。

- [ ] **Step 4: 运行配置、应用和部署契约测试**

Run: `go test ./internal/config ./internal/app ./deploy -race -count=1`

Expected: PASS。

- [ ] **Step 5: 提交配置接线**

```bash
git add internal/config/config.go internal/config/config_test.go internal/app/app.go internal/app/app_test.go deploy/config/config.example.json deploy/systemd/aimili-gateway.service deploy/deploy_contract_test.go
git commit -m "feat: configure external gateway ui"
```

### Task 3: 离线签名校验和原子 UI 安装器

**Files:**
- Create: `internal/releaseverify/manifest.go`
- Create: `internal/releaseverify/manifest_test.go`
- Create: `internal/uirelease/installer.go`
- Create: `internal/uirelease/installer_test.go`
- Create: `cmd/aimili-gateway-update-install/main.go`
- Create: `cmd/aimili-gateway-update-install/main_test.go`

**Interfaces:**
- Consumes: staging 中三个普通文件、Ed25519 公钥文件、目标 UI 根、回环健康地址。
- Produces: `releaseverify.VerifyUI(manifest, signature, archive []byte, publicKey ed25519.PublicKey) (UIManifest, error)`；`uirelease.Install(ctx context.Context, cfg Config) (Result, error)`；CLI `ui-install` 与 `ui-rollback`。

- [ ] **Step 1: 写签名、摘要和文件闭集 RED 测试**

```go
func TestVerifyUIAcceptsDetachedSignature(t *testing.T) { /* 对规范 manifest 原始字节签名，摘要/大小一致 */ }
func TestVerifyUIRejectsChangedManifest(t *testing.T) { /* 改一字节，期望 invalid_signature */ }
func TestVerifyUIRejectsDigestOrSizeMismatch(t *testing.T) { /* 期望 invalid_payload */ }
func TestInstallRejectsArchiveLinksAndTraversal(t *testing.T) { /* tar 含 ../、symlink、hardlink，目标零写入 */ }
```

- [ ] **Step 2: 运行 RED**

Run: `go test ./internal/releaseverify ./internal/uirelease ./cmd/aimili-gateway-update-install -count=1`

Expected: FAIL，包尚不存在。

- [ ] **Step 3: 实现严格 manifest 和 tar 校验**

```go
type UIManifest struct {
    SchemaVersion int `json:"schemaVersion"`
    Kind, Version, Commit, BuiltAt, APIVersion string
    Archive struct { SHA256 string `json:"sha256"`; Bytes int64 `json:"bytes"` } `json:"archive"`
    Files []File `json:"files"`
}
func VerifyUI(manifest, signature, archive []byte, publicKey ed25519.PublicKey) (UIManifest, error)
```

校验使用收到的 manifest 原始字节；JSON 禁止未知字段和尾随数据；文件列表排序、无重复且与解包闭集完全相等。解包逐项 `Lstat`，只接受 mode 0644 普通文件，解压总字节与 manifest 相符。

- [ ] **Step 4: 写原子 current/previous、唯一保留和回滚 RED 测试**

```go
func TestInstallSwitchesCurrentAndPreviousAtomically(t *testing.T) { /* current=A，安装B后 current=B previous=A */ }
func TestInstallFailureLeavesCurrentUntouched(t *testing.T) { /* HTTP 门失败，current 仍A且B清理 */ }
func TestRollbackSwapsCurrentAndPrevious(t *testing.T) { /* 回滚后 current=A previous=B */ }
func TestSuccessfulInstallKeepsOnlyCurrentAndPrevious(t *testing.T) { /* 更老 releases 被精确删除 */ }
```

- [ ] **Step 5: 实现安装事务和 CLI**

```go
type Config struct { StagingDir, Root, PublicKeyFile, HealthURL string; MinimumFreeBytes int64 }
type Result struct { Version, PreviousVersion, State string }
func Install(ctx context.Context, cfg Config) (Result, error)
func Rollback(ctx context.Context, cfg Config) (Result, error)
```

空间门为压缩包三倍加 64 MiB；新目录先落在同一文件系统临时目录，`fsync` 后 rename。临时软链接经 `Rename` 切换。清理只遍历已验证的 `<root>/releases/<version>` 绝对路径，绝不使用通配符。

- [ ] **Step 6: 运行 installer 全测和竞态测试**

Run: `go test ./internal/releaseverify ./internal/uirelease ./cmd/aimili-gateway-update-install -race -count=1`

Expected: PASS。

- [ ] **Step 7: 提交离线安装器**

```bash
git add internal/releaseverify internal/uirelease cmd/aimili-gateway-update-install
git commit -m "feat: install signed ui releases atomically"
```

### Task 4: 可复现构建、签名与手工发布契约

**Files:**
- Create: `cmd/aimili-gateway-release-tool/main.go`
- Create: `cmd/aimili-gateway-release-tool/main_test.go`
- Create: `scripts/build-ui-release.ps1`
- Create: `scripts/deploy-external-ui-stage-a-remote.sh`
- Modify: `deploy/deploy_contract_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `web/dist`、显式私钥文件路径、显式 staging 目录；生产目标固定路径。
- Produces: `release-tool ui --dist <dir> --private-key <file> --out <dir> --commit <sha>`；Stage A 安装脚本只调用离线 installer，不联网。

- [ ] **Step 1: 写确定性打包和密钥安全 RED 测试**

```go
func TestUIBundleIsDeterministic(t *testing.T) { /* 相同 dist 两次打包的 manifest 和 tar 摘要相同 */ }
func TestReleaseToolRejectsKeyFromEnvironmentOrOutputTree(t *testing.T) { /* 私钥必须为显式 root-only 普通文件且不能位于仓库/输出目录 */ }
```

部署契约要求脚本包含 SHA 校验、固定 staging、`ui-install`、健康失败回滚、精确保留两版；禁止 `curl`、`wget`、`systemctl restart aimilivpn`、`systemctl restart x-ui`、`systemctl restart caddy`、通配删除和输出私钥。

- [ ] **Step 2: 运行 RED**

Run: `go test ./cmd/aimili-gateway-release-tool ./deploy -run 'UIBundle|ExternalUI|StageA' -count=1`

Expected: FAIL，工具与脚本不存在。

- [ ] **Step 3: 实现构建工具和 PowerShell 入口**

```powershell
npm --prefix web test -- --run
npm --prefix web run build
go run ./cmd/aimili-gateway-release-tool ui --dist web/dist --private-key $SigningKey --out $Output --commit (git rev-parse HEAD)
```

tar 固定路径顺序、mtime=Unix epoch、uid/gid=0、mode=0644。脚本只输出版本 ID、文件名、大小和 SHA 摘要，不输出私钥路径内容。

- [ ] **Step 4: 实现 Stage A 阶梯脚本**

脚本参数固定为已上传 staging 绝对路径；依次执行磁盘/服务/quick check/进程与资源指纹预检、安装同构 external UI、安装 Gateway 二进制与 unit、只重启 Gateway、HTTP/数据库/四服务/进程/协议/指纹后检。失败只恢复 Gateway 唯一 previous、旧 unit 和 UI 指针。

- [ ] **Step 5: 运行工具、部署契约和本地完整验证**

Run: `go test ./cmd/aimili-gateway-release-tool ./deploy -race -count=1`

Run: `npm test --prefix web -- --run`

Run: `npm run build --prefix web`

Run: `go test ./... -race -count=1`

Run: `go vet ./...`

Run: `git diff --check`

Expected: 全部 PASS。

- [ ] **Step 6: 提交发布入口和文档**

```bash
git add cmd/aimili-gateway-release-tool scripts/build-ui-release.ps1 scripts/deploy-external-ui-stage-a-remote.sh deploy/deploy_contract_test.go README.md
git commit -m "feat: package and deploy signed gateway ui"
```

### Task 5: Stage A 生产首次启用与免重启验收

**Files:**
- Create: `.deploy-assets/external-ui-stage-a/`（Git 忽略的本次构建与脱敏记录）
- Modify: `docs/handoffs/2026-09-05-current-state-handoff.md`
- Create: `docs/verification/2026-09-05-external-ui-releases.md`

**Interfaces:**
- Consumes: 本地通过验证的 Linux 二进制、UI 三资产、离线 installer、Stage A 脚本。
- Produces: 生产 external UI、唯一 previous 回滚、脱敏验收记录。

- [ ] **Step 1: 记录 Stage 0 不变门**

通过一次合并的脱敏只读检查记录：四服务、Gateway DB quick check、Gateway/Xray/OpenVPN PID 或进程数、四逻辑出口状态与协议、固定端口集合、`agw-` 与非受管资源指纹、根分区和当前唯一备份。

- [ ] **Step 2: 构建 Linux 资产并验证本地摘要**

Run: `$env:GOOS='linux'; $env:GOARCH='amd64'; go build -trimpath -o .deploy-assets/external-ui-stage-a/aimili-gateway ./cmd/aimili-gateway`

Run: `$env:GOOS='linux'; $env:GOARCH='amd64'; go build -trimpath -o .deploy-assets/external-ui-stage-a/aimili-gateway-update-install ./cmd/aimili-gateway-update-install`

Expected: 两个文件均为非空 Linux amd64 ELF；SHA-256 与上传后值一致。

- [ ] **Step 3: 上传到一次性绝对 staging 并执行 preflight**

上传目标固定为 `/tmp/aimili-gateway-ui-stage-a-20260905`；执行前解析真实路径并确认它位于 `/tmp`，确认没有进程占用同名旧目录。preflight 只读且不得触碰 current、服务或数据库。

- [ ] **Step 4: 执行首次启用事务**

执行 Stage A 脚本；只允许 `aimili-gateway.service` 的 PID 变化。安装成功后立即读取脚本结果、实际软链接、二进制摘要和 systemd 状态；SSH 或审批中断时先读状态，不重放事务。

- [ ] **Step 5: 证明日常 UI 切换和回滚免重启**

安装一个只改变 `manifest.version` 且内容可识别的第二个签名 UI，记录切换前 Gateway PID；安装、HTTP 读取、`ui-rollback`、再次 HTTP 读取后 Gateway PID 必须完全相同，AimiliVPN、x-ui/Xray、Caddy PID/进程数也不变。

- [ ] **Step 6: 执行最终不变门和清理**

重跑 Stage 0 检查并比较。只删除已解析的本次 `/tmp/aimili-gateway-ui-stage-a-20260905`；生产 releases 只能留下 current 与 previous。重新检查根分区、四服务、数据库和四出口状态。

- [ ] **Step 7: 更新交接与验证记录并提交**

```bash
git add docs/handoffs/2026-09-05-current-state-handoff.md docs/verification/2026-09-05-external-ui-releases.md
git commit -m "docs: verify external ui releases in production"
```

