# 本机 Linux VM 基础与安全门实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不修改 v2rayN、Windows 路由和 ny 生产的前提下，创建一台受限为2 vCPU、2048 MiB内存、24 GiB动态磁盘的Ubuntu Server VMware虚拟机，完成物理桥接、来源受限SSH和原生 systemd 部署前置验证。

**Architecture:** Windows脚本只编排现有VMware CLI，使用固定摘要的Ubuntu 24.04 cloud OVA和cloud-init公钥登录。单张桥接网卡承担默认路由和管理连接，Linux防火墙把SSH限制到当前Windows物理地址；本阶段验证基础系统，不安装业务；原生服务部署须先完成新设计审核。

**Tech Stack:** PowerShell 5.1+、VMware Workstation 16.1、Ubuntu Server 24.04 cloud image、cloud-init、OpenSSH、systemd

**设计状态：** 旧容器方案已撤销，新的原生 systemd 设计待审核。本文件保留既有 VM 基础步骤，不授权后续业务部署。

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

  脚本执行顺序：真实主机预检；确认同名VM未运行；生成一个本地管理的VMware MAC和仅含公钥用户的cloud-init user-data；使用OVA已经声明的 `instance-id`、`hostname`、`public-keys`、base64 `user-data` 属性调用 `ovftool.exe --diskMode=monolithicSparse` 导入已校验OVA；用 `vmware-vdiskmanager.exe -x 24GB` 扩展唯一系统VMDK；随后在VMX中固定资源和桥接网卡。`thin`只适用于VI目标，Workstation VMX目标必须使用动态增长的 `monolithicSparse`。不得依赖未声明的私有guestinfo属性。

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

### 后续阶段：等待原生设计审核

旧容器运行时阶段已删除，不执行。后续计划在原生设计批准后逐文件补充：基础出网验证、原生服务状态、阶梯部署、唯一备份回滚、重启与四出口验证。

2026-09-06 恢复检查：VM 正在运行且 SSH 可达；2 vCPU、约 2 GiB 内存、约 24 GiB 虚拟磁盘、约 1 GiB swap。网关 ping 成功，公共 IP TCP 443/53 与 DNS 失败，UFW 默认允许出站。未安装业务，不能标记基础验证全部通过。
