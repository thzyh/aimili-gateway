# 本机 VMware Ubuntu 真实部署闭环实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 VMware Ubuntu VM 中完成 AimiliVPN、3x-ui/Xray、Aimili Gateway 和 Caddy 的真实原生部署、内外双重自动验证、重启复验及 GitHub 推送；v2rayN 最终验收由用户亲自执行。

**Architecture:** Windows 通过 `deploy/local-vm` 使用固定 SSH 身份编排 VMnet8 NAT 来宾；VM 内按 `aimilivpn → xui-caddy → gateway → slots → verify` 顺序部署，并以同一 `RunId`、组件备份和 checkpoint 支持可验证续作。验收分为 VM 内部 `nativeReady`、Windows 外部协议数据面验证、VM 重启恢复三个门禁。

**Tech Stack:** PowerShell 5.1+、VMware Workstation 16.1、Ubuntu 24.04、systemd、UFW、Go、Python 标准库、AimiliVPN、3x-ui/Xray、Caddy。

**Spec:** `docs/superpowers/specs/2026-09-06-local-vm-native-systemd-design.md`

## Current Execution Checkpoint (2026-09-07)

- Task 1 root cause confirmed: an orphaned `vmware-vmx` process and three stale `.lck` directories were blocking lifecycle control; the orphan process and validated stale locks were removed.
- The VM was started again with `vmrun`, but Guest boot stopped at `systemd-networkd-wait-online` because host service `VMnetDHCP` is `Stopped`.
- `VMware NAT Service` and `VMAuthdService` are running; `VMnetuserif` is running; the current Codex token is medium-integrity and cannot open/start `VMnetDHCP`.
- The VM is currently powered off after the failed network boot; no native deployment command has been run.
- Resume gate: a real administrator must start `VMnetDHCP`; then rerun Task 1 from the VM start/IP/SSH checks. Do not modify application code or rerun deployment before that gate passes.

## Global Constraints

- VM 使用 VMnet8 NAT；Windows 从 `https://<guest-ip>:8080` 访问，不能把 Windows 的 `127.0.0.1` 当作 Guest 地址。
- 不连接或修改 ny，不读取或复制 ny 生产资产。
- 不使用 Computer Use 操作或验收 v2rayN；不读取或修改 v2rayN 配置、日志、活动节点、TUN 或系统代理。
- 不修改 Windows 默认路由、DNS、防火墙；自动验证只比较脱敏宿主快照。
- 当前清单期望 4 个 OpenVPN、1 个 Xray，但数量从 manifest 读取，不是永久上限。
- 非幂等步骤不得在状态未确认时重放；中断后使用原 `RunId` 和 checkpoint 续作。
- 不提交 `deploy/local-vm/host-preflight.ps1`、`deploy/local-vm/create-vm.ps1`、`.deploy-assets/`、`.test-gocache/`、`.test-gomodcache/`、根目录 exe 或 `**/__pycache__/`。
- 禁止 `git add .` 和任何形式的 force push。
- 只有真实部署、外部自动验证和重启复验通过后才创建最终本地提交并推送功能分支。

---

### Task 1: 恢复 VMware 控制面与 Guest 可达性

**Files:**
- Read: `D:\VirtualMachines\AimiliGatewayLocal\AimiliGatewayLocal.vmx`
- Read: `D:\VirtualMachines\AimiliGatewayLocal\guest-serial-current.log`
- Reuse: `deploy/local-vm/host-preflight.ps1`
- Reuse: `deploy/local-vm/status.ps1`

**Interfaces:**
- VMware CLI: `E:\SoftWare\Vmware16\vmrun.exe`
- Guest identity: `aimili@<guest-ip>` with the fixed runtime SSH key and known_hosts.
- Success gate: VM is listed as running, Guest has a current `192.168.88.0/24` address, SSH succeeds, and host/guest preflight passes.

- [ ] Stop any stale VMware control state through the supported VMware CLI/UI lifecycle; do not kill the VM process before confirming state.
- [ ] Start `AimiliGatewayLocal.vmx` with `vmrun start ... nogui` when it is not listed as running.
- [ ] Discover the current Guest IP from VMware Tools, serial evidence, or the current DHCP lease; reject stale lease-only evidence.
- [ ] Verify TCP 22 and the fixed SSH identity, then run host and Guest network preflight.
- [ ] Record the first failing boundary if gateway, public TCP 443, DNS, HTTPS, UFW, time, disk or memory fails; fix only the confirmed VM-local cause and rerun the full gate.

### Task 2: 构建、校验和暂存部署资产

**Files:**
- Reuse: `deploy/local-vm/deploy-native.ps1`
- Reuse: `deploy/local-vm/native/deployment.json`
- Reuse: `deploy/local-vm/native/stage.sh`
- Build: `aimili-gateway`, `aimili-gateway-admin` for Linux amd64.

**Interfaces:**
- A new unique `RunId` identifies staging, backups, checkpoints and verification evidence.
- Staging verifies the strict manifest and SHA-256 before promotion.

- [ ] Capture the pre-deployment host safety snapshot without accessing v2rayN content.
- [ ] Build fresh Linux amd64 Gateway/Admin binaries from the current worktree.
- [ ] Assemble only manifest-declared assets and verify hashes and source commits.
- [ ] Run `deploy-native.ps1 -PlanOnly` and review the redacted order, expected counts and commits.
- [ ] Upload and stage the validated asset set under the new `RunId`.

### Task 3: 执行可回滚的原生部署

**Files:**
- Reuse: `deploy/local-vm/native/install-aimilivpn.sh`
- Reuse: `deploy/local-vm/native/install-xui-caddy.sh`
- Reuse: `deploy/local-vm/native/install-gateway.sh`
- Reuse: `deploy/local-vm/native/backup.sh`
- Reuse: `deploy/local-vm/native/rollback.sh`

**Interfaces:**
- Fixed order: `aimilivpn → xui-caddy → gateway → slots → verify`.
- Each component creates one backup, applies once, verifies, and writes a checkpoint before the next component.

- [ ] Apply AimiliVPN and verify its service, control API, TUN and current manifest topology.
- [ ] Apply 3x-ui/Xray and Caddy, then verify units, listeners, panel/subscription confinement and actual Xray count.
- [ ] Apply Gateway, initialize the independent local database/credentials, and verify health, protocol automation and reverse proxy.
- [ ] Enable the current manifest slots in order, verifying tunnel, policy route, loopback proxy and real egress after each slot.
- [ ] On failure, stop downstream work, preserve evidence, roll back only the failed component, and verify the preceding checkpoint.

### Task 4: VM 内部深度验收

**Files:**
- Reuse: `deploy/local-vm/native/verify-native.sh`
- Reuse: `deploy/local-vm/status.ps1`

**Interfaces:**
- `verify-native.sh --json` produces redacted `nativeReady`, expected/actual counts and protocol automation results.

- [ ] Verify all service/path/timer units are active and enabled as required.
- [ ] Verify UFW, SQLite readability, file permissions, TUN/routes and public/mixed listener binding.
- [ ] Verify subscription coverage, protocol isolation and manifest-derived logical exits.
- [ ] Require `nativeReady=true`; do not treat partial service health as completion.

### Task 5: Windows 外部自动数据面验证

**Files:**
- Reuse: `deploy/local-vm/verify-external.ps1`
- Reuse: `scripts/verify-external-client-v1c.py`
- Evidence: runtime `verification/external-client.json` only.

**Interfaces:**
- The verifier dynamically reads public protocols and mixed SOCKS endpoints from the manifest/evidence.
- The verifier must not start, stop, read or operate v2rayN.

- [ ] Execute a real external handshake for every VLESS/Reality, XHTTP/Reality and Hysteria2 entry.
- [ ] Execute proxy DNS and egress verification for every authenticated mixed SOCKS endpoint.
- [ ] Confirm the redacted evidence contains no subscription URL, credentials, UUID, node IP or random admin path.
- [ ] Compare the post-deployment host safety snapshot with the baseline.

### Task 6: VM 重启恢复复验

**Files:**
- Reuse: `deploy/local-vm/status.ps1`
- Reuse: `deploy/local-vm/native/verify-native.sh`
- Reuse: `deploy/local-vm/verify-external.ps1`

**Interfaces:**
- Guest IP is rediscovered after reboot; no cached IP is assumed.

- [ ] Reboot the VM through the supported Guest/VMware lifecycle only after Tasks 1–5 pass.
- [ ] Rediscover Guest IP and re-establish the pinned SSH identity.
- [ ] Repeat native service, database, route, UFW, listener and `nativeReady` checks.
- [ ] Repeat all external protocol and mixed SOCKS checks.
- [ ] Confirm the final host safety snapshot matches the baseline.

### Task 7: 最新测试、精确提交和 GitHub 推送

**Files:**
- Modify: `docs/verification/2026-09-07-local-vm-real-deployment.md`
- Modify: applicable handoff/status documentation.

**Interfaces:**
- Separate Gateway and AimiliVPN commits.
- Ordinary push to `origin/feat/main-switch-protocol-modes`; remote SHA must be verified after push.

- [ ] Write the redacted deployment and verification record; explicitly mark browser/subscription import/v2rayN acceptance as user-executed and still pending.
- [ ] Run all affected PowerShell, Bash, Python, Go and AimiliVPN tests fresh, plus `git diff --check`.
- [ ] Inspect both worktrees and exclude every prohibited artifact; stage only explicit approved paths.
- [ ] Create local commits and report their SHAs separately from remote status.
- [ ] Fetch both repositories, compare divergence, and resolve only normal non-destructive conflicts.
- [ ] Push both feature branches without force and verify the remote SHAs.

## Completion Gate

- [ ] VM is reachable through its current VMnet8 Guest IP and `https://<guest-ip>:8080` is available for user testing.
- [ ] `nativeReady=true`, external automatic verification passes, reboot recovery passes, and host safety snapshots match.
- [ ] Current OpenVPN/Xray/logical-exit actual counts match manifest expectations without introducing permanent maxima.
- [ ] Gateway and AimiliVPN worktrees are clean after explicit commits.
- [ ] Both feature branches are present on GitHub at the verified local commit SHAs.
- [ ] Browser, subscription import and v2rayN acceptance are handed to the user and are not claimed as automatically completed.
