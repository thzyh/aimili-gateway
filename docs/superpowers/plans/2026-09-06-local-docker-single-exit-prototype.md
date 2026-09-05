# 本机 Docker 单出口隔离原型实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Windows Docker Desktop 上交付一个不影响 v2rayN、没有 ny 生产依赖、可通过本机回环代理验证真实 VPNGate 出口的 AimiliVPN 单出口原型。

**Architecture:** AimiliVPN 独占专用 Docker bridge 和网络命名空间，只在容器内创建 `tun0`。Windows 仅把 `127.0.0.1:17928` 和 `127.0.0.1:18787` 映射到容器代理与管理页；凭据和运行数据保存在独立命名 volume，容器只有 `NET_ADMIN` 和 `/dev/net/tun`。

**Tech Stack:** Python 3.12 标准库、OpenVPN、Docker Engine 29、Docker Compose、PowerShell 5.1+

**Spec:** `docs/superpowers/specs/2026-09-06-local-docker-single-exit-prototype-design.md`

## Global Constraints

- 不读取或修改 v2rayN 配置、活动节点、TUN、系统代理和日志。
- 不连接 `ssh ny`，不读取、复制或挂载任何生产数据库、配置、证书和密钥。
- 不使用 `privileged`、`network_mode: host`、Docker socket 或宽泛宿主目录挂载。
- 仅发布 Windows 回环端口 `17928` 和 `18787`。
- 只有 AimiliVPN 容器获得 `NET_ADMIN` 与 `/dev/net/tun`。
- `MULTI_EXIT_SLOTS=0`，验收时只允许一个主 OpenVPN 进程。
- 新增 Python 行为必须先有失败测试；Compose 与 PowerShell 合约也必须先写失败测试。
- 自动化输出不得包含 UI 密码、token、VPNGate 候选 ID、节点地址或真实出口 IP。

---

### Task 1: 凭据初始化与容器入口

**Files:**
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/entrypoint.py`
- Test: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_docker_single_exit.py`

**Interfaces:**
- Produces: `ensure_runtime(data_dir: pathlib.Path) -> None`，幂等创建 `ui_auth.json` 和 `control.token`。
- Produces: `main() -> NoReturn`，完成初始化后以 `os.execvp` 启动 `/opt/aimilivpn/vpngate_manager.py`。
- UI 配置固定容器内 `host=0.0.0.0`、`port=8787`、`proxy_port=7928`、`connection_enabled=true`。

- [ ] **Step 1: 写首次生成和幂等测试**

  测试在临时目录调用 `ensure_runtime`，断言两个文件存在、用户名非空、密码长度不少于 32 字符、管理路径不少于 24 字符、token 不少于 43 字符；第二次调用后逐字节内容不变。

- [ ] **Step 2: 运行测试并确认 RED**

  Run: `python -m unittest tests.test_docker_single_exit.DockerEntrypointTests -v`

  Expected: FAIL，原因是 `deploy/docker-single-exit/entrypoint.py` 尚不存在。

- [ ] **Step 3: 实现最小入口**

  使用 `secrets.token_urlsafe` 生成所有随机值，通过同目录临时文件、`flush`、`fsync` 和 `os.replace` 原子写入；Linux 上设置 `0600`。若文件已存在，只接受普通文件、有效 JSON 和完整字段，否则明确失败，不静默覆盖。

- [ ] **Step 4: 运行定向测试并确认 GREEN**

  Run: `python -m unittest tests.test_docker_single_exit.DockerEntrypointTests -v`

  Expected: PASS，且测试输出不包含生成的秘密。

- [ ] **Step 5: 提交 AimiliVPN 本地 Git**

  Commit message: `feat: add isolated Docker runtime bootstrap`

### Task 2: 最小权限镜像与 Compose 合约

**Files:**
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/Dockerfile`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/compose.yaml`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_docker_single_exit.py`

**Interfaces:**
- Produces: Compose project name `aimili-single-exit`。
- Produces: service `aimilivpn-single`，volume `aimili-single-exit-data`，network `aimili-single-exit-net`。
- Publishes: `127.0.0.1:17928:7928/tcp`、`127.0.0.1:18787:8787/tcp`。

- [ ] **Step 1: 写 Compose 静态安全合约测试**

  断言 Compose 包含两个精确回环映射、`cap_drop: ALL`、唯一 `cap_add: NET_ADMIN`、`/dev/net/tun`、`read_only: true`、`no-new-privileges:true`、资源和日志上限；断言不含 `privileged`、host network、Docker socket、`10808`、SSH、ny 路径及宿主业务目录挂载。

- [ ] **Step 2: 运行测试并确认 RED**

  Run: `python -m unittest tests.test_docker_single_exit.DockerComposeContractTests -v`

  Expected: FAIL，原因是 Dockerfile 与 Compose 尚不存在。

- [ ] **Step 3: 实现 Dockerfile 与 Compose**

  Dockerfile 固定 `python:3.12-slim`，安装 `openvpn`、`iproute2`、`iptables`、`ca-certificates`、`curl`、`procps`、`psmisc`，复制当前仓库四个 Python 运行文件和 entrypoint。Compose 设置 `VPNGATE_DATA_DIR=/var/lib/aimilivpn`、`LOCAL_PROXY_HOST=0.0.0.0`、`UI_HOST=0.0.0.0`、`MULTI_EXIT_SLOTS=0`、`AIMILI_CONTROL_ADDRESS=127.0.0.1:8790`，并使用独立 volume、bridge、tmpfs 和资源限制。

- [ ] **Step 4: 运行静态测试和 Compose 配置解析**

  Run: `python -m unittest tests.test_docker_single_exit -v`

  Run: `docker compose -f deploy/docker-single-exit/compose.yaml config --quiet`

  Expected: 两项 PASS；解析结果只发布设计中的两个回环端口。

- [ ] **Step 5: 提交 AimiliVPN 本地 Git**

  Commit message: `feat: define isolated single-exit Compose stack`

### Task 3: 安全启停、验收和凭据查看脚本

**Files:**
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/common.ps1`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/start.ps1`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/verify.ps1`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/show-access.ps1`
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/stop.ps1`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/tests/test_docker_single_exit.py`

**Interfaces:**
- Produces: `Get-HostSafetySnapshot`，仅返回 v2rayN PID、系统代理值、默认路由摘要和经 `127.0.0.1:10808` 的 HTTP 健康结果。
- Produces: `Assert-HostSafetyUnchanged -Before <snapshot> -After <snapshot>`。
- Produces: `start.ps1 [-SkipBuild]`、`verify.ps1 [-TimeoutSeconds 600]`、`show-access.ps1`、`stop.ps1 [-PurgeData]`。

- [ ] **Step 1: 写脚本安全合约测试**

  断言所有脚本不包含 `Set-ItemProperty`、`netsh winhttp set`、`route add/delete/change`、`Stop-Process`、`taskkill`、`ssh`、`scp`；`stop.ps1` 只有显式 `-PurgeData` 分支可执行 `down --volumes`，且 Compose 文件路径和项目名固定。

- [ ] **Step 2: 运行测试并确认 RED**

  Run: `python -m unittest tests.test_docker_single_exit.DockerScriptContractTests -v`

  Expected: FAIL，原因是脚本尚不存在。

- [ ] **Step 3: 实现脚本**

  `start.ps1` 先检查 17928/18787 未占用和 Docker Engine 可用，再保存脱敏基线并执行 `docker compose up -d --build`。`verify.ps1` 轮询容器健康、断言单 OpenVPN 进程，通过显式 `curl.exe --proxy http://127.0.0.1:17928` 获取并校验 IP 形状但不打印值，然后复核安全快照。`show-access.ps1` 是唯一允许把 UI URL、用户名和密码显示给用户的显式入口。`stop.ps1` 默认保留 volume。

- [ ] **Step 4: 运行测试并确认 GREEN**

  Run: `python -m unittest tests.test_docker_single_exit -v`

  Expected: PASS。

- [ ] **Step 5: 提交 AimiliVPN 本地 Git**

  Commit message: `feat: add safe local Docker lifecycle scripts`

### Task 4: 本地构建和真实出口验收

**Files:**
- Runtime only; no production files.

**Interfaces:**
- Consumes: Tasks 1–3 的 Compose 和脚本。
- Produces: 运行中的 `aimili-single-exit-aimilivpn-single-1`，等待用户验收。

- [ ] **Step 1: 记录宿主基线**

  Run: `powershell -NoProfile -ExecutionPolicy Bypass -File deploy/docker-single-exit/start.ps1`

  Expected: v2rayN 仍运行、系统代理值可读、现用代理健康返回 HTTP 204；脚本不输出秘密。

- [ ] **Step 2: 构建并启动容器**

  由 `start.ps1` 使用 Compose 构建。若基础镜像缺失，仅拉取 Dockerfile 明确固定的官方镜像；不安装或修改 Windows/全局 Python 依赖。

- [ ] **Step 3: 验证容器权限和网络**

  检查 `docker inspect`：`Privileged=false`、`NetworkMode` 不是 host、capability 只有 `NET_ADMIN`、设备只包含 `/dev/net/tun`、端口仅绑定 `127.0.0.1`。

- [ ] **Step 4: 验证真实单出口**

  Run: `powershell -NoProfile -ExecutionPolicy Bypass -File deploy/docker-single-exit/verify.ps1 -TimeoutSeconds 900`

  Expected: 一个 OpenVPN 进程、`tun0` 存在、代理请求返回有效 IP 且与非原型直连结果不同；输出只报告布尔结果和稳定状态。

- [ ] **Step 5: 验证宿主未受影响**

  Expected: v2rayN 进程仍存在，系统代理键值与启动前一致，Windows 默认路由摘要一致，经现用代理访问 `generate_204` 仍返回 204。

### Task 5: 用户文档和最终记录

**Files:**
- Create: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/deploy/docker-single-exit/README.md`
- Modify: `D:/CodexProject/Github/aimili-vpngate/.worktrees/main-switch-protocol-modes/README.md`
- Create: `D:/CodexProject/Github/aimili-gateway/.worktrees/main-switch-protocol-modes/docs/verification/2026-09-06-local-docker-single-exit-prototype.md`

**Interfaces:**
- Produces: 用户验收入口和可恢复清理说明。

- [ ] **Step 1: 写文档合约测试并确认 RED**

  在 `tests/test_docker_single_exit.py` 断言 README 明确包含 `17928`、`18787`、`show-access.ps1`、不改系统代理、默认保留数据、`-PurgeData` 不可恢复。

- [ ] **Step 2: 编写中文使用说明**

  说明用户先运行 `show-access.ps1` 查看管理页入口；代理验收时只把测试请求显式指向 `127.0.0.1:17928`。明确不要把 17928 配成无来源限制的局域网代理，也不要把 volume 中的凭据提交 Git。

- [ ] **Step 3: 运行最终验证**

  Run: `python -m unittest discover -s tests -p "test_*.py" -v`

  Run: `docker compose -f deploy/docker-single-exit/compose.yaml config --quiet`

  Run: `git diff --check`

  Expected: 全部 PASS；原型继续运行。

- [ ] **Step 4: 提交两个仓库的本地 Git**

  AimiliVPN commit message: `docs: document local Docker single-exit prototype`

  Gateway commit message: `docs: record local Docker prototype verification`

- [ ] **Step 5: 最终汇报**

  分别说明本地实现与测试、容器实际状态、真实出口验证、v2rayN/系统代理/默认路由保护结果、用户验收命令、两个工作区状态、本地提交 ID 和远程推送状态。不得把本地提交表述为已经推送。
