# 国家代理 V1-B 单代理组闭环实施计划

> **供代理执行者使用：** 必须使用 `superpowers:executing-plans` 逐任务实施本计划。每个步骤使用复选框（`- [ ]`）跟踪。

**目标：** 在不接管非受管资源的前提下，实现一个“国家＋出口类型”代理组从 AimiliVPN 槽位、3x-ui/Xray 受管资源到真实 VLESS 与 SOCKS5H 验证的完整闭环。

**架构：** AimiliVPN Fork 新增仅回环监听、Bearer Token 认证的 `/control/v1` API；Gateway 通过类型化适配器编排 AimiliVPN 与 3x-ui，并将代理组状态和加密凭据保存在 SQLite。V1-B 只开放一个活动代理组的领域 API，全部资源使用 `agw-` 命名空间，失败时反向补偿并记录 `repair_required`。

**技术栈：** Python 3 标准库、Go 1.26、SQLite、`net/http`、3x-ui 本机管理 API、Xray Reality/mixed、Go `testing`、Python `unittest`。

**设计规格：** `docs/superpowers/specs/2026-08-25-country-proxy-console-design.md`

## 全局约束

- 个人单管理员，不实现多租户、角色、计费或账户同步。
- Gateway、AimiliVPN、3x-ui、Xray 和 Caddy 保持独立进程；不重写 OpenVPN、Xray 或 3x-ui 核心。
- 不直接写 `x-ui.db`，只使用 3x-ui 本机管理 API。
- Gateway 只修改 `agw-` 命名空间资源，不接管、覆盖或删除非受管资源。
- AimiliVPN 控制 API 默认只监听 `127.0.0.1:8790`，每个请求必须通过独立 Bearer Token 认证。
- 候选目录只返回 `probe_status=available` 节点的安全字段；不返回 OpenVPN 配置、账户、Cookie、服务令牌或原始错误。
- `mobile` 与 `residential` 映射为 `residential`；`hosting` 与 `proxy` 映射为 `datacenter`；未知类型不可启用。
- V1-B 同时只允许一个活动代理组；端口池为 VLESS `20000-20999`、mixed `30000-30999`。
- mixed 必须有认证且至少配置一个非全网来源 CIDR；拒绝 `0.0.0.0/0` 和 `::/0`。
- 所有 VLESS 入站复用一套全局客户端身份，所有 mixed 入站复用一套全局用户名和密码；明文不写日志、审计或 Git。
- “检测”不改变节点和入口；“换 IP”只能在同国家、同类型内更换，入口端口和身份保持不变。
- 完成必须有真实 VLESS、SOCKS5H 和 DNS 路径验证依据；API 成功不等于端到端完成。
- 未通过本地合同测试和隔离端到端验证前，不修改生产 VPS。

---

## 文件结构

### AimiliVPN Fork

- `control_api.py`：独立控制服务器、认证、请求/响应边界和错误映射。
- `vpngate_manager.py`：为现有槽位补充每槽位出口类型约束、安全快照和控制服务器启动钩子。
- `tests/test_control_api.py`：控制 API 合同、认证、字段脱敏和错误测试。
- `tests/test_exit_slot_types.py`：住宅/机房选择、换 IP 保持约束测试。
- `install.sh`：生成权限为 `0600` 的控制令牌文件，并通过 systemd 环境传入路径。
- `README.md`：记录控制 API 的本机边界、配置键和验证命令，不记录令牌值。

### Aimili Gateway

- `internal/config/config.go`：控制 API 地址、令牌文件、3x-ui 自动化凭据文件、容量和端口池配置。
- `internal/adapters/aimili/client.go`：AimiliVPN 类型化客户端。
- `internal/adapters/xui/client.go`：3x-ui 会话、CSRF、能力探测和受管资源读写客户端。
- `internal/domain/proxygroup.go`：代理组、状态、出口类型和稳定资源名模型。
- `internal/store/migrations/003_country_proxy.sql`：代理组、全局凭据、CIDR、操作记录表。
- `internal/store/proxygroups.go`：代理组持久化、乐观版本和操作状态。
- `internal/store/credentials.go`：按用途上下文加密的全局连接凭据。
- `internal/orchestrator/orchestrator.go`：单代理组启用、检测、换 IP、禁用和反向补偿。
- `internal/validator/socks.go`：带认证 SOCKS5H 与远端 DNS 验证。
- `internal/validator/vless.go`：固定模板、回环目标、临时文件和超时受限的 Xray 客户端验证。
- `internal/httpapi/proxy_handlers.go`：受认证、CSRF、重认证保护的领域 API。
- `cmd/aimili-gateway/main.go`：组装新配置、适配器、编排器和验证器。

---

### 任务 1：AimiliVPN 安全候选目录与类型约束

**文件：**

- 修改：`../aimili-vpngate/vpngate_manager.py`
- 新建测试：`../aimili-vpngate/tests/test_exit_slot_types.py`

**接口：**

- 产出：`normalize_proxy_type(ip_type: Any) -> str`
- 产出：`safe_candidate_snapshot() -> list[dict[str, Any]]`
- 产出：`get_slot_type_map() -> dict[str, str]`
- 产出：`set_slot_type(slot: int, proxy_type: Any) -> dict[str, str]`
- 修改：`select_slot_nodes(..., proxy_type: str = "")` 严格执行每槽位类型过滤。

- [ ] **步骤 1：编写失败测试**

```python
class ExitSlotTypeTests(unittest.TestCase):
    def test_type_mapping_is_closed(self):
        self.assertEqual(manager.normalize_proxy_type("mobile"), "residential")
        self.assertEqual(manager.normalize_proxy_type("hosting"), "datacenter")
        self.assertEqual(manager.normalize_proxy_type("proxy"), "datacenter")
        self.assertEqual(manager.normalize_proxy_type("unknown"), "")

    def test_safe_snapshot_excludes_unavailable_and_secrets(self):
        with mock.patch.object(manager, "read_nodes", return_value=[available, unavailable]):
            actual = manager.safe_candidate_snapshot()
        self.assertEqual([item["id"] for item in actual], ["ok"])
        self.assertNotIn("config_text", actual[0])
        self.assertNotIn("config_file", actual[0])
```

- [ ] **步骤 2：运行红灯测试**

运行：`python -m unittest tests.test_exit_slot_types -v`

预期：因 `normalize_proxy_type` 和 `safe_candidate_snapshot` 尚不存在而失败。

- [ ] **步骤 3：实现封闭映射、安全字段白名单和每槽位类型过滤**

安全快照只允许 `id`、`country_short`、`country`、`ip`、`proxy_type`、`owner`、`asn`、`as_name`、`latency_ms`、`score`、`probe_status`、`last_probe_at`；选择函数必须在国家和类型不匹配时排除节点。

- [ ] **步骤 4：运行绿灯和回归测试**

运行：`python -m unittest tests.test_exit_slot_types -v`

运行：`python -m unittest discover -s tests -v`

预期：全部通过。

- [ ] **步骤 5：提交 AimiliVPN 类型约束**

```bash
git add vpngate_manager.py tests/test_exit_slot_types.py
git commit -m "feat: enforce proxy type per exit slot"
```

### 任务 2：AimiliVPN 版本化回环控制 API

**文件：**

- 新建：`../aimili-vpngate/control_api.py`
- 修改：`../aimili-vpngate/vpngate_manager.py`
- 新建测试：`../aimili-vpngate/tests/test_control_api.py`

**接口：**

- 产出：`ControlApplication(manager, token: str)`
- 产出：`start_control_server(manager, address: str, token_file: Path) -> ThreadingHTTPServer`
- 路由：`GET /control/v1/capabilities`
- 路由：`GET /control/v1/candidates`
- 路由：`GET /control/v1/slots/{slot}`
- 路由：`POST /control/v1/slots`
- 路由：`POST /control/v1/slots/{slot}/rotate`
- 路由：`POST /control/v1/slots/{slot}/check`
- 路由：`DELETE /control/v1/slots/{slot}`

- [ ] **步骤 1：编写失败的认证和合同测试**

```python
def test_rejects_missing_bearer_token(self):
    response = self.request("GET", "/control/v1/capabilities")
    self.assertEqual(response.status, 401)

def test_create_slot_passes_country_and_type(self):
    response = self.request("POST", "/control/v1/slots", {"country": "JP", "proxyType": "datacenter"})
    self.assertEqual(response.status, 201)
    self.manager.create_managed_slot.assert_called_once_with("JP", "datacenter")
```

- [ ] **步骤 2：运行红灯测试**

运行：`python -m unittest tests.test_control_api -v`

预期：因 `control_api` 模块不存在而失败。

- [ ] **步骤 3：实现控制服务器**

请求体上限 16 KiB；响应统一 `{ "data": ... }` 或 `{ "error": { "code": "..." } }`；Bearer 比较使用 `hmac.compare_digest`；不回显 token、节点配置或内部异常；只接受回环监听地址。

- [ ] **步骤 4：在管理器中提供受管操作门面**

`create_managed_slot(country, proxy_type)` 必须先保存国家和类型约束，再选择匹配节点；失败时删除刚预留槽位。`rotate_managed_slot` 复用同一约束并排除当前节点；`check_managed_slot` 返回槽位状态、本地 SOCKS 端口、实际出口 IP 和检测时间。

- [ ] **步骤 5：运行绿灯和回归测试**

运行：`python -m unittest tests.test_control_api -v`

运行：`python -m unittest discover -s tests -v`

预期：全部通过。

- [ ] **步骤 6：提交控制 API**

```bash
git add control_api.py vpngate_manager.py tests/test_control_api.py
git commit -m "feat: add authenticated aimili control api"
```

### 任务 3：AimiliVPN 安装与运行安全合同

**文件：**

- 修改：`../aimili-vpngate/install.sh`
- 修改：`../aimili-vpngate/README.md`
- 修改测试：`../aimili-vpngate/tests/test_project_contract.py`

**接口：**

- 配置：`AIMILI_CONTROL_ADDRESS=127.0.0.1:8790`
- 配置：`AIMILI_CONTROL_TOKEN_FILE=/etc/aimilivpn/control.token`

- [ ] **步骤 1：写入失败合同测试**

测试安装脚本必须创建 `/etc/aimilivpn/control.token`、使用 `umask 077`、写入 systemd 环境；README 必须说明控制 API 只允许回环访问且不得把令牌写入命令行或 Git。

- [ ] **步骤 2：运行红灯测试**

运行：`python -m unittest tests.test_project_contract.ProjectContractTests -v`

预期：缺少控制 API 安装合同而失败。

- [ ] **步骤 3：实现安装合同和文档**

安装时只在令牌文件不存在时生成 32 字节随机值；更新不得轮换令牌；文件权限固定 `0600`，目录 `0700`。

- [ ] **步骤 4：运行全部测试并提交**

运行：`python -m unittest discover -s tests -v`

```bash
git add install.sh README.md tests/test_project_contract.py
git commit -m "feat: install loopback control api credentials"
```

### 任务 4：Gateway AimiliVPN 类型化适配器

**文件：**

- 修改：`internal/config/config.go`
- 修改测试：`internal/config/config_test.go`
- 新建：`internal/adapters/aimili/client.go`
- 新建测试：`internal/adapters/aimili/client_test.go`

**接口：**

- 产出：`aimili.NewClient(baseURL string, token []byte) (*Client, error)`
- 产出：`Capabilities(ctx) (Capabilities, error)`
- 产出：`Candidates(ctx) ([]Candidate, error)`
- 产出：`CreateSlot(ctx, CreateSlotRequest) (Slot, error)`
- 产出：`GetSlot(ctx, int) (Slot, error)`
- 产出：`RotateSlot(ctx, int) (Slot, error)`
- 产出：`CheckSlot(ctx, int) (SlotCheck, error)`
- 产出：`DeleteSlot(ctx, int) error`

- [ ] **步骤 1：编写 `httptest.Server` 失败测试**

测试 Authorization 头、16 KiB 响应上限、未知 JSON 字段拒绝、上游错误到稳定 `AdapterError.Code` 的映射，以及响应中不存在原始正文。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/adapters/aimili -run TestClient -v`

预期：客户端类型不存在而编译失败。

- [ ] **步骤 3：实现配置和客户端**

配置新增 `aimiliControlUrl` 与 `aimiliControlTokenFile`，URL 必须是回环 HTTP；令牌从权限受限文件读取，不进入 `Config` 的可序列化字段和错误文本。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/config ./internal/adapters/aimili -v`

```bash
git add internal/config internal/adapters/aimili
git commit -m "feat: add typed aimili control adapter"
```

### 任务 5：Gateway 代理组状态与加密凭据

**文件：**

- 新建：`internal/domain/proxygroup.go`
- 新建测试：`internal/domain/proxygroup_test.go`
- 新建：`internal/store/migrations/003_country_proxy.sql`
- 新建：`internal/store/proxygroups.go`
- 新建：`internal/store/credentials.go`
- 修改测试：`internal/store/store_test.go`

**接口：**

- 产出：`domain.ProxyGroup`、`domain.ProxyType`、`domain.ProxyGroupStatus`
- 产出：`Store.CreateProxyGroup(ctx, group) error`
- 产出：`Store.GetProxyGroup(ctx, id) (domain.ProxyGroup, error)`
- 产出：`Store.UpdateProxyGroup(ctx, group, expectedVersion int64) error`
- 产出：`Store.ListProxyGroups(ctx) ([]domain.ProxyGroup, error)`
- 产出：`Store.PutCredential(ctx, purpose string, plaintext, masterKey []byte) error`
- 产出：`Store.GetCredential(ctx, purpose string, masterKey []byte) ([]byte, error)`
- 产出：`Store.ReplaceMixedCIDRs(ctx, []netip.Prefix) error`

- [ ] **步骤 1：编写状态机、迁移、唯一键和密文测试**

测试 `(country_code, proxy_type)` 唯一、状态枚举封闭、乐观版本冲突、数据库中不存在凭据明文、不同用途密文不可互换解密，以及拒绝全网 CIDR。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/domain ./internal/store -v`

预期：新包和迁移接口不存在而失败。

- [ ] **步骤 3：实现最小状态和存储层**

稳定资源名由 `agw-<country-lower>-<res|dc>` 派生；状态只允许 `provisioning`、`ready`、`rotating`、`degraded`、`repair_required`、`disabling`。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/domain ./internal/store -v`

```bash
git add internal/domain internal/store
git commit -m "feat: persist proxy groups and encrypted credentials"
```

### 任务 6：3x-ui 合同客户端与 `agw-` 所有权保护

**文件：**

- 新建：`internal/adapters/xui/client.go`
- 新建：`internal/adapters/xui/models.go`
- 新建测试：`internal/adapters/xui/client_test.go`
- 修改：`internal/config/config.go`

**接口：**

- 产出：`xui.NewClient(baseURL string, credentials Credentials) (*Client, error)`
- 产出：`Client.ProbeCapabilities(ctx) (Capabilities, error)`
- 产出：`Client.Snapshot(ctx) (Snapshot, error)`
- 产出：`Client.EnsureManagedGroup(ctx, DesiredGroup) (ManagedGroup, error)`
- 产出：`Client.DeleteManagedGroup(ctx, ManagedGroup) error`

- [ ] **步骤 1：编写模拟 3x-ui 的失败合同测试**

测试 CSRF→登录→新 CSRF 顺序、Cookie 仅保存在内存、只接受已覆盖能力、拒绝修改非 `agw-` 标签、保留未知出站和路由、写后重读指纹一致、上游正文不进入错误。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/adapters/xui -run TestClient -v`

预期：客户端接口不存在而编译失败。

- [ ] **步骤 3：实现固定 3x-ui API 合同**

使用 `/csrf-token`、`/login`、`/panel/api/xray/`、`/panel/api/xray/update`、`/panel/api/inbounds/list`、`/panel/api/inbounds/add` 和合同测试确认的更新/删除端点。所有合并按稳定标签匹配，遇到同名但不符合受管指纹的资源返回 `ownership_conflict`。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/adapters/xui -v`

```bash
git add internal/adapters/xui internal/config
git commit -m "feat: manage namespaced xui resources"
```

### 任务 7：真实 SOCKS5H 和 VLESS 验证器

**文件：**

- 新建：`internal/validator/contracts.go`
- 新建：`internal/validator/socks.go`
- 新建测试：`internal/validator/socks_test.go`
- 新建：`internal/validator/vless.go`
- 新建测试：`internal/validator/vless_test.go`

**接口：**

- 产出：`Validator.ValidateSOCKS5H(ctx, SOCKSTarget) (Result, error)`
- 产出：`Validator.ValidateVLESS(ctx, VLESSTarget) (Result, error)`
- 结果：观察到的出口 IP、DNS 路径标记、延迟和稳定错误码。

- [ ] **步骤 1：编写失败测试**

SOCKS 测试使用本地假 SOCKS5 服务验证用户名密码和域名型 CONNECT；VLESS 测试用假 `xray` 可执行文件验证秘密只进入 `0600` 临时配置、不进入 argv，目标只允许回环，超时后进程被终止且临时文件删除。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/validator -v`

预期：验证器不存在而编译失败。

- [ ] **步骤 3：实现验证器**

固定探测域名和响应上限；VLESS 配置由结构体编码，不接受任意 JSON；错误只返回 `timeout`、`authentication_failed`、`dns_failed`、`egress_mismatch`、`protocol_failed`。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/validator -v`

```bash
git add internal/validator
git commit -m "feat: validate real proxy protocols"
```

### 任务 8：单代理组编排和反向补偿

**文件：**

- 新建：`internal/orchestrator/orchestrator.go`
- 新建：`internal/orchestrator/locks.go`
- 新建测试：`internal/orchestrator/orchestrator_test.go`

**接口：**

- 产出：`Enable(ctx, EnableRequest) (domain.ProxyGroup, error)`
- 产出：`Check(ctx, groupID string) (domain.ProxyGroup, error)`
- 产出：`Rotate(ctx, groupID string) (domain.ProxyGroup, error)`
- 产出：`Disable(ctx, groupID string) error`

- [ ] **步骤 1：编写表驱动失败测试**

覆盖容量超限、重复国家类型、Aimili 创建失败、3x-ui 创建失败时删除槽位、VLESS 失败时反向删除 Xray 后删除槽位、回滚失败进入 `repair_required`、检测不调用 Rotate、换 IP 不改变端口/身份/标签、并发同组只允许一个操作。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/orchestrator -v`

预期：编排器不存在而编译失败。

- [ ] **步骤 3：实现显式阶段状态机**

顺序固定为：预留数据库→创建并实测槽位→确保 Xray 资源→写后重读指纹→SOCKS5H 验证→VLESS 验证→`ready`。补偿顺序严格相反；补偿失败保存稳定错误码和非秘密资源标识。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/orchestrator -v`

```bash
git add internal/orchestrator
git commit -m "feat: orchestrate one country proxy group"
```

### 任务 9：Gateway 领域 API 与安全边界

**文件：**

- 新建：`internal/httpapi/proxy_handlers.go`
- 新建测试：`internal/httpapi/proxy_handlers_test.go`
- 修改：`internal/httpapi/server.go`
- 修改：`cmd/aimili-gateway/main.go`
- 修改：`deploy/config/config.example.json`

**接口：**

- `GET /api/v1/countries`
- `GET /api/v1/proxy-groups`
- `POST /api/v1/proxy-groups`
- `POST /api/v1/proxy-groups/{id}/check`
- `POST /api/v1/proxy-groups/{id}/rotate`
- `DELETE /api/v1/proxy-groups/{id}`
- `GET /api/v1/proxy-groups/{id}/connections`

- [ ] **步骤 1：编写失败的 HTTP 安全测试**

覆盖未登录 401、CSRF 403、启用/换 IP/禁用要求五分钟内重新认证、`Idempotency-Key` 重放返回同一结果、连接信息仅 `ready` 可取、响应 `Cache-Control: no-store`、错误中不包含 UUID/密码/上游正文。

- [ ] **步骤 2：运行红灯测试**

运行：`go test ./internal/httpapi -run 'Test(Countries|ProxyGroups)' -v`

预期：路由返回 404。

- [ ] **步骤 3：实现领域 API 和依赖组装**

HTTP 层只接受闭合字段，国家代码必须是两个 ASCII 字母，类型只允许 `residential|datacenter`；输出候选与组状态的安全 DTO，不透传上游结构。

- [ ] **步骤 4：运行绿灯并提交**

运行：`go test ./internal/httpapi ./cmd/aimili-gateway -v`

```bash
git add internal/httpapi cmd/aimili-gateway deploy/config
git commit -m "feat: expose single proxy group api"
```

### 任务 10：双仓库合同验证与隔离端到端门槛

**文件：**

- 新建：`scripts/verify-country-proxy-v1b.ps1`
- 新建：`docs/verification/2026-08-25-country-proxy-v1b.md`
- 修改：`README.md`

**接口：**

- 产出：一次执行 Python 测试、Go 测试、前端测试、静态秘密扫描和隔离服务合同测试的验证脚本。

- [ ] **步骤 1：运行格式化和静态检查**

运行：`gofmt -w` 仅覆盖本计划新增或修改的 Go 文件。

运行：`python -m compileall -q control_api.py vpngate_manager.py tests`

预期：无语法错误。

- [ ] **步骤 2：运行双仓库完整测试**

运行：`python -m unittest discover -s tests -v`

运行：`go test ./...`

运行：`npm test -- --run`

预期：全部通过。

- [ ] **步骤 3：运行隔离合同测试**

以假 AimiliVPN 和假 3x-ui HTTP 服务启动 Gateway，创建一个代理组；断言调用顺序、`agw-` 所有权、补偿行为、检测不换 IP、换 IP 保持入口稳定，且测试日志不出现测试秘密。

- [ ] **步骤 4：记录尚未满足的真实协议门槛**

若本机没有 OpenVPN/Xray 完整网络环境，验证文档必须明确标记“合同闭环通过，真实 VLESS/SOCKS5H/DNS 尚待生产前隔离环境验证”，不得写成 V1-B 完成。

- [ ] **步骤 5：提交验证材料**

```bash
git add scripts/verify-country-proxy-v1b.ps1 docs/verification README.md
git commit -m "test: verify country proxy v1b contracts"
```

---

## 计划自检

- 规格覆盖：候选目录、类型映射、槽位生命周期、独立认证、加密凭据、`agw-` 所有权、3x-ui 合同、真实协议验证、编排补偿、领域 API 和更新兼容门槛均有对应任务。
- 明确延后：完整前端、多个并发代理组、目录刷新任务、高级设置全页面、TXT 批量导出和生产部署不属于本单组 V1-B 计划；这些只在单组真实协议闭环通过后进入后续计划。
- 占位符检查：未发现禁止的占位内容或未定义接口。
- 类型一致性：外部出口类型统一为 `residential|datacenter`；代理组状态和适配器方法在所有任务中命名一致。
- 安全检查：所有秘密只从受限文件或加密存储读取，所有浏览器和控制 API 响应均禁止缓存，不记录原始上游响应。
