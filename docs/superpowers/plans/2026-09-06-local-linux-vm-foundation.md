# 本机 Linux VM 基础与安全门实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不修改 v2rayN、Windows 路由和 ny 生产的前提下，创建一台受限为2 vCPU、2048 MiB内存、24 GiB动态磁盘的Ubuntu Server VMware虚拟机，完成物理桥接、来源受限SSH和Docker运行时验证。

**Architecture:** Windows脚本只编排现有VMware CLI，使用固定摘要的Ubuntu 24.04 cloud OVA和cloud-init公钥登录。单张桥接网卡承担默认路由和管理连接，Linux防火墙把SSH限制到当前Windows物理地址；本阶段只安装Docker，不导入Aimili业务镜像。

**Tech Stack:** PowerShell 5.1+、VMware Workstation 16.1、Ubuntu Server 24.04 cloud image、cloud-init、OpenSSH、Docker Engine、Docker Compose v2

**Spec:** `docs/superpowers/specs/2026-09-06-local-linux-vm-full-docker-design.md`

## Global Constraints

- VM固定为2 vCPU、2048 MiB内存和24 GiB动态磁盘；不得自动扩容。
- 主机可用内存低于3.5 GiB时拒绝启动VM，不结束任何用户程序。
- VM目录固定为 `D:\VirtualMachines\AimiliGatewayLocal`，私钥目录固定为当前用户 `%LOCALAPPDATA%\AimiliGateway\vmware-local`。
- 只使用 `E:\SoftWare\Vmware16` 中现有VMware工具，不安装或升级虚拟化软件。
- OVA固定为Ubuntu 24.04 `release-20260826`，SHA256固定为 `de6c3a9dde3769dd9a60354800680e6e2307c06bf3d73a5efc165cbcd487adad`。
- 不读取或修改v2rayN配置、日志、活动节点、TUN和系统代理；只读取进程ID、系统代理和默认路由摘要作为安全基线。
- 不连接 `ssh ny`，不复制任何生产数据库、配置、证书或密钥。
- 本阶段不启动AimiliVPN、Gateway、3x-ui/Xray或Caddy。
- 自动化不得输出私钥、完整局域网地址、Windows用户名或其他秘密。

---

### Task 1: 主机资源与安全基线门

**Files:**
- Create: `deploy/local-vm/lib/AimiliLocalVm.psm1`
- Create: `deploy/local-vm/host-preflight.ps1`
- Create: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- Produces: `Test-AimiliHostCapacity -Facts <pscustomobject> -> pscustomobject`
- Produces: `Get-AimiliHostFacts -> pscustomobject`
- Produces: `Get-AimiliHostSafetySnapshot -> pscustomobject`
- Produces: `Assert-AimiliHostSafetyUnchanged -Before <object> -After <object>`
- Produces: `New-AimiliVmPlan -Facts <object> -BridgePlan <object> -> pscustomobject`，供脚本与测试共同消费。
- Produces: `host-preflight.ps1 [-AsJson]`，成功退出0，资源或虚拟化不满足时退出2。

- [ ] **Step 1: 写资源门和宿主不变性失败测试**

  `tests/run.ps1` 先加载尚不存在的模块，并用固定fixture断言低于3.5 GiB失败、2 vCPU/2048 MiB/24 GiB通过、系统代理或默认路由变化会失败：

  ```powershell
  Import-Module "$PSScriptRoot\..\lib\AimiliLocalVm.psm1" -Force
  $good = [pscustomobject]@{ LogicalProcessors=12; FreeMemoryGiB=3.85; DFreeGiB=129; HypervisorPresent=$true; VmwareRoot='E:\SoftWare\Vmware16'; RunningVmCount=0 }
  if (-not (Test-AimiliHostCapacity -Facts $good).Passed) { throw 'good capacity fixture rejected' }
  $low = $good.PSObject.Copy(); $low.FreeMemoryGiB = 3.49
  if ((Test-AimiliHostCapacity -Facts $low).Passed) { throw 'low memory fixture accepted' }
  $before = [pscustomobject]@{ V2rayNPids=@(10); Proxy='1|127.0.0.1:10808'; DefaultRoute='route-a' }
  $after = [pscustomobject]@{ V2rayNPids=@(10); Proxy='1|127.0.0.1:10808'; DefaultRoute='route-b' }
  try { Assert-AimiliHostSafetyUnchanged -Before $before -After $after; throw 'route mutation accepted' } catch { }
  ```

- [ ] **Step 2: 运行测试并确认RED**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Expected: FAIL，原因是 `AimiliLocalVm.psm1` 不存在。

- [ ] **Step 3: 实现最小资源门和只读基线**

  模块使用CIM读取CPU/内存，读取D盘空间、`HypervisorPresent`、VMware工具准确路径和 `vmrun list` 数量；资源判断核心为：

  ```powershell
  function Test-AimiliHostCapacity {
      param([Parameter(Mandatory)]$Facts)
      $reasons = @()
      if ([int]$Facts.LogicalProcessors -lt 4) { $reasons += 'logical_processors_below_4' }
      if ([double]$Facts.FreeMemoryGiB -lt 3.5) { $reasons += 'free_memory_below_3_5_gib' }
      if ([double]$Facts.DFreeGiB -lt 30) { $reasons += 'd_drive_free_below_30_gib' }
      if (-not [bool]$Facts.HypervisorPresent) { $reasons += 'hypervisor_not_present' }
      if (-not (Test-Path -LiteralPath $Facts.VmwareRoot)) { $reasons += 'vmware_root_missing' }
      [pscustomobject]@{ Passed=($reasons.Count -eq 0); Reasons=$reasons }
  }
  ```

  安全快照只记录v2rayN进程ID、当前用户Internet Settings中的ProxyEnable/ProxyServer和IPv4默认路由接口/跃点摘要；禁止读取v2rayN配置或日志。

- [ ] **Step 4: 运行测试和真实预检并确认GREEN**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Run: `pwsh -NoProfile -File deploy/local-vm/host-preflight.ps1 -AsJson`

  Expected: 测试PASS；真实预检报告2 vCPU/2048 MiB/24 GiB目标和当前资源门通过，不输出完整IP或用户路径。

- [ ] **Step 5: 提交本地Git**

  ```powershell
  git add deploy/local-vm/lib/AimiliLocalVm.psm1 deploy/local-vm/host-preflight.ps1 deploy/local-vm/tests/run.ps1
  git commit -m "feat: add local VM host safety gate"
  ```

### Task 2: 固定Ubuntu镜像下载与校验

**Files:**
- Create: `deploy/local-vm/image-lock.json`
- Create: `deploy/local-vm/download-image.ps1`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- Produces: `image-lock.json`，字段为 `url`、`fileName`、`sha256`、`sizeBytes`。
- Produces: `download-image.ps1`，幂等返回已校验OVA的绝对路径；下载中断只保留准确 `.part` 文件。

- [ ] **Step 1: 写镜像锁和摘要校验失败测试**

  增加行为测试：调用 `Test-AimiliImageLock` 验证固定release锁；将临时fixture文件交给 `Confirm-AimiliFileDigest`，正确长度/摘要返回true，错误摘要返回false；非法文件名和 `/current/` URL被拒绝。测试不通过搜索脚本文本判断成功。

- [ ] **Step 2: 运行测试并确认RED**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Expected: FAIL，原因是镜像锁和下载脚本不存在。

- [ ] **Step 3: 实现固定锁和幂等下载**

  `image-lock.json` 使用：

  ```json
  {
    "url": "https://cloud-images.ubuntu.com/releases/noble/release-20260826/ubuntu-24.04-server-cloudimg-amd64.ova",
    "fileName": "ubuntu-24.04-server-cloudimg-amd64-release-20260826.ova",
    "sha256": "de6c3a9dde3769dd9a60354800680e6e2307c06bf3d73a5efc165cbcd487adad",
    "sizeBytes": 593940480
  }
  ```

  下载脚本创建准确缓存目录，已有目标文件先校验；新下载写入同目录 `.part`，校验摘要和长度后原子改名。任何失败不覆盖已校验目标。

- [ ] **Step 4: 运行测试并下载真实OVA**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Run: `pwsh -NoProfile -File deploy/local-vm/download-image.ps1`

  Expected: PASS；约566.4 MiB OVA下载完成，长度和SHA256均与锁文件相同。

- [ ] **Step 5: 提交本地Git**

  Commit message: `feat: pin local VM Ubuntu image`

### Task 3: 幂等创建桥接VM与cloud-init SSH

**Files:**
- Create: `deploy/local-vm/create-vm.ps1`
- Create: `deploy/local-vm/status.ps1`
- Modify: `deploy/local-vm/lib/AimiliLocalVm.psm1`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- Produces: `Get-AimiliPhysicalBridgePlan -> pscustomobject`，返回唯一物理桥接适配器和Windows来源地址。
- Produces: `New-AimiliCloudInitPayload -PublicKey <string> -WanMac <string> -AllowedSource <string> -> pscustomobject`。
- Produces: `create-vm.ps1 [-PlanOnly]`；PlanOnly只输出脱敏VM计划，正式模式创建或验证 `D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`。
- Produces: 当前用户专用SSH私钥和known_hosts，位于 `%LOCALAPPDATA%\AimiliGateway\vmware-local`。

- [ ] **Step 1: 写VM计划和cloud-init失败测试**

  直接调用 `New-AimiliVmPlan` 和 `New-AimiliCloudInitPayload`：断言计划为2 CPU、2048 MiB、24 GiB且只有一张bridged网卡；cloud-init只含fixture公钥、禁用root和密码SSH，并把SSH来源限制为fixture地址。调用 `create-vm.ps1 -PlanOnly` 并断言退出0、没有创建VM目录，也没有改变宿主安全快照。

- [ ] **Step 2: 运行测试并确认RED**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Expected: FAIL，原因是VM创建脚本和新增模块函数不存在。

- [ ] **Step 3: 实现桥接网卡和cloud-init生成**

  脚本执行顺序：真实主机预检；确认同名VM未运行；生成一个本地管理的VMware MAC和仅含公钥用户的cloud-init user-data；使用OVA已经声明的 `instance-id`、`hostname`、`public-keys`、base64 `user-data` 属性调用 `ovftool.exe --diskMode=thin` 导入已校验OVA；用 `vmware-vdiskmanager.exe -x 24GB` 扩展唯一系统VMDK；随后在VMX中固定资源和桥接网卡。不得依赖未声明的私有guestinfo属性。

  VMX必须包含：

  ```text
  memsize = "2048"
  numvcpus = "2"
  ethernet0.connectionType = "bridged"
  ethernet0.vnet = "VMnet0"
  ```

  cloud-init让桥接网卡使用DHCP和默认路由，并用UFW只允许创建时记录的Windows物理地址访问SSH。SSH只接受生成的Ed25519公钥，`ssh_pwauth: false`、`disable_root: true`。

- [ ] **Step 4: 运行测试、创建VM并验证SSH**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Run: `pwsh -NoProfile -File deploy/local-vm/create-vm.ps1`

  Expected: VM以nogui启动；脚本从VMware Tools获取桥接DHCP地址并通过来源受限SSH连接；`nproc=2`、内存约2 GiB、根磁盘约24 GiB；业务容器数为0。

- [ ] **Step 5: 提交本地Git**

  Commit message: `feat: provision isolated local Linux VM`

### Task 4: 安装并固定Docker运行时

**Files:**
- Create: `deploy/local-vm/assets/install-docker.sh`
- Create: `deploy/local-vm/provision-runtime.ps1`
- Modify: `deploy/local-vm/status.ps1`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- Produces: `install-docker.sh [--check]`；正式模式安装Ubuntu仓库中的 `docker.io`、`docker-compose-v2`、`ca-certificates`、`curl`、`jq`，配置有界json-file日志并hold Docker包；check模式只验证环境和打印脱敏安装计划。
- Produces: `provision-runtime.ps1`，通过隔离SSH key上传、校验并执行脚本。

- [ ] **Step 1: 写运行时安装行为失败测试**

  在WSL中运行 `install-docker.sh --check`：Ubuntu Noble fixture返回计划中的五个包、日志轮转和hold列表；非Noble fixture退出2。随后在真实VM运行正式模式，断言Docker配置、hold状态、用户组和监听套接字符合预期，不通过grep源码判断成功。

- [ ] **Step 2: 运行测试并确认RED**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Expected: FAIL，原因是运行时安装脚本不存在。

- [ ] **Step 3: 实现最小Docker安装**

  Docker daemon配置固定为：

  ```json
  {
    "log-driver": "json-file",
    "log-opts": { "max-size": "10m", "max-file": "3" },
    "live-restore": true
  }
  ```

  安装后启动并enable Docker，hold实际安装的 `docker.io`、`containerd`、`runc`、`docker-compose-v2`，输出只包含版本和服务状态。

- [ ] **Step 4: 运行本地与VM真实验证**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Run: `wsl bash -n /mnt/d/CodexProject/Github/aimili-gateway/.worktrees/main-switch-protocol-modes/deploy/local-vm/assets/install-docker.sh`

  Run: `pwsh -NoProfile -File deploy/local-vm/provision-runtime.ps1`

  Expected: Docker active、Compose v2可用、`/dev/net/tun`存在、没有Aimili业务容器。

- [ ] **Step 5: 提交本地Git**

  Commit message: `feat: provision local VM Docker runtime`

### Task 5: 安全状态、停止与文档

**Files:**
- Create: `deploy/local-vm/stop-vm.ps1`
- Create: `deploy/local-vm/README.md`
- Modify: `deploy/local-vm/status.ps1`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- Produces: `status.ps1 [-AsJson]`，脱敏报告主机资源、VM电源、SSH、Docker、网卡角色和业务容器数。
- Produces: `stop-vm.ps1 [-WhatIf]`，正式模式软停止并保留虚拟磁盘和密钥；WhatIf只返回目标和动作。

- [ ] **Step 1: 写生命周期失败测试并列出文档验收项**

  对不存在VM和已停止VM分别运行 `stop-vm.ps1 -WhatIf`，断言返回幂等soft-stop计划且磁盘/密钥仍存在；真实软停止后重新启动并验证数据不变。README由本任务人工复核资源限制、启动门、物理桥接、来源受限SSH、无生产依赖、默认保留数据和后续阶段，不为人类文档增加字符串测试。

- [ ] **Step 2: 运行测试并确认RED**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Expected: FAIL，原因是停止脚本和README不存在。

- [ ] **Step 3: 实现脱敏状态和软停止入口**

  状态脚本不得打印完整管理地址、SSH key路径或网卡MAC；只报告 `managementReachable`、`defaultRouteOnBridge`、`dockerActive` 和资源数值。停止脚本先确认VMX绝对路径严格位于设计目录，再执行soft stop；VM未运行时幂等成功。

- [ ] **Step 4: 运行最终阶段验证**

  Run: `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`

  Run: `pwsh -NoProfile -File deploy/local-vm/status.ps1 -AsJson`

  Run: `git diff --check`

  Expected: 全部PASS；状态显示VM运行、2 CPU、约2 GiB、24 GiB磁盘、Docker健康、业务容器0。

- [ ] **Step 5: 提交本地Git**

  Commit message: `docs: document local VM foundation`

### Task 6: 宿主不变性和阶段检查点

**Files:**
- Runtime only; no production files.
- Create: `docs/verification/2026-09-06-local-linux-vm-foundation.md`

**Interfaces:**
- Consumes: Tasks 1–5全部入口。
- Produces: 第一阶段权威验证记录，供完整Compose栈计划使用。

- [ ] **Step 1: 对比宿主安全基线**

  在VM创建前保存 `Get-AimiliHostSafetySnapshot`，阶段结束后重新读取并调用 `Assert-AimiliHostSafetyUnchanged`。

  Expected: v2rayN PID集合、系统代理和Windows默认路由摘要完全一致。

- [ ] **Step 2: 验证VM网络角色**

  通过桥接SSH读取脱敏网卡/路由：默认路由经唯一桥接网卡；SSH只允许创建时记录的Windows来源地址；Windows到管理地址可达。

- [ ] **Step 3: 验证没有越界副作用**

  Expected: 没有Aimili业务容器、没有OpenVPN/Xray进程、没有ny连接、没有Windows新监听端口、没有修改系统代理或路由。

- [ ] **Step 4: 写中文验证记录并复核**

  记录宿主资源、VM资源、镜像摘要、SSH/Docker结果、未执行项、Git状态和下一阶段入口；不记录完整IP、MAC、用户名或私钥路径。

- [ ] **Step 5: 提交本地Git并进入第二阶段**

  Commit message: `docs: record local VM foundation verification`

  完成后以该验证记录为依据，为“镜像与单出口完整栈”创建下一份实施计划，不重新扫描项目或修改ny生产。
