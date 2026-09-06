# 本机 VMware Ubuntu 原生 systemd 部署实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 VMware Ubuntu VM 中实现可回滚、可验证、按当前清单运行的 AimiliVPN、3x-ui/Xray、Gateway 和 Caddy 原生 systemd 部署。

**Architecture:** Windows `deploy/local-vm` 只编排 VM 和 SSH；来宾使用受限的原生脚本、固定目录和 systemd。首期清单是主连接＋3 个出口、期望 4 个 OpenVPN、期望 1 个 Xray，但状态、验证和数据结构均读取清单，不把这些数字编译成永久上限。

**Tech Stack:** PowerShell 5.1+、VMware Workstation 16.1、Ubuntu 24.04、systemd、UFW、Python 标准库、Go Gateway、AimiliVPN 现有 `install.sh`、官方 3x-ui v3.7.0、Caddy。

**Spec:** `docs/superpowers/specs/2026-09-06-local-vm-native-systemd-design.md`

## Global Constraints

- 不连接 ny，不读取或复制 ny 生产资产。
- 不读取或修改 v2rayN 配置、日志、活动节点、TUN 或系统代理。
- 不修改 Windows 默认路由、DNS、防火墙；宿主安全快照必须前后一致。
- VM 固定 2 vCPU、2048 MiB 内存、约 1 GiB swap、24 GiB 动态磁盘。
- VM 单桥接网卡；UFW 管理入口只允许当前 Windows 物理地址。
- 出网前不安装业务；公共 IP TCP 443、DNS 和 HTTPS 必须先通过。
- 不使用 Docker；不删除其他 Docker Desktop 资源。
- 不把当前 4 个 OpenVPN 或 1 个 Xray 写成永久架构上限。
- 凭据、token、UUID、私钥、证书和完整订阅路径不得写入 Git、日志或聊天。
- 两个工作树只创建本地提交，不推送远程。

---

### Task 1: 原生部署清单与状态契约

**Files:**
- Create: `deploy/local-vm/native/deployment.json`
- Modify: `deploy/local-vm/status.ps1`
- Modify: `deploy/local-vm/tests/run.ps1`
- Modify: `deploy/local-vm/lib/AimiliLocalVm.psm1`

**Interfaces:**
- Manifest keys: `schemaVersion`, `services`, `expected.openvpn`, `expected.xray`, `expected.logicalExits`, `expected.exitSlots`, `ports`, `sourceCommit`。
- `status.ps1 -AsJson` produces `nativeServices`, `expected`, `actual`, `nativeReady`; no `dockerActive` or `businessContainerCount` keys.
- `Get-AimiliNativeManifest` validates non-negative integer expectations and service names without imposing a maximum.

- [ ] **Step 1: Write failing manifest and status tests**

Add PowerShell assertions for a manifest with `openvpn=4`, `xray=1`, `logicalExits=4`, `exitSlots=3`, and another fixture with `openvpn=8`, `xray=2`; both must validate. Assert the JSON status contract contains native fields and no Docker fields.

- [ ] **Step 2: Run the focused test and confirm RED**

Run `pwsh -NoProfile -File deploy/local-vm/tests/run.ps1`.
Expected: fail because the manifest reader and native status contract do not exist.

- [ ] **Step 3: Implement the smallest manifest reader and status report**

Add a JSON manifest reader that only validates integer ranges `>=0`, required service names and unique ports. Replace Docker SSH probes with one shell probe that reads `systemctl is-active`, `systemctl is-enabled`, `pgrep -fc openvpn`, `pgrep -fc xray`, and a manifest-aware logical-exit count. Preserve VM/SSH/host safety fields.

- [ ] **Step 4: Run focused and live status tests**

Run the PowerShell test and `pwsh -NoProfile -File deploy/local-vm/status.ps1 -AsJson`; confirm the live VM reports Docker fields absent, all four business services inactive, and actual counts zero.

- [ ] **Step 5: Commit**

`git add deploy/local-vm docs && git commit -m "feat: add native VM deployment status contract"`

### Task 2: VM network preflight and deterministic failure boundary

**Files:**
- Create: `deploy/local-vm/native/guest-network-preflight.sh`
- Modify: `deploy/local-vm/tests/run.ps1`
- Modify: `docs/handoffs/2026-09-05-current-state-handoff.md`

**Interfaces:**
- `guest-network-preflight.sh --json` returns only redacted booleans and error classes: `gatewayReachable`, `publicTcp443`, `dnsResolution`, `httpsReachable`, `ufwOutgoingAllowed`, `failureBoundary`.
- Exit 0 requires all five gates; exit 2 identifies the first failed boundary without printing full addresses.

- [ ] **Step 1: Add fixture tests for each boundary**

Use a temporary fake command directory to feed gateway, TCP, DNS and HTTPS results; assert the script reports `gateway`, `upstream_tcp`, `dns`, or `https` as the first failure and never prints an address.

- [ ] **Step 2: Run tests to confirm RED**

Run the focused shell fixture through `bash`; expect missing script/function failure.

- [ ] **Step 3: Implement read-only probes**

Use `ip route get`, `ping`, `getent ahostsv4`, `curl --connect-timeout`, and `ufw status`; do not alter routes, resolv.conf, UFW or VMware settings. Treat a refused or timed-out public connection as an upstream boundary, not as permission to switch to NAT.

- [ ] **Step 4: Run live preflight and record evidence**

Run through SSH. If only the current known boundary remains, stop business installation and record it. If a VM-local rule is the first failure, make one minimal VM-local fix, then rerun the complete preflight and host safety snapshot.

- [ ] **Step 5: Commit**

`git add deploy/local-vm docs && git commit -m "feat: add native guest network preflight"`

### Task 3: Native staging, backup and rollback primitives

**Files:**
- Create: `deploy/local-vm/native/stage.sh`
- Create: `deploy/local-vm/native/backup.sh`
- Create: `deploy/local-vm/native/rollback.sh`
- Create: `deploy/local-vm/native/README.md`

**Interfaces:**
- Staging root: `/var/lib/aimili-local/staging/<run-id>`; backup root: `/var/backups/aimili-local/<run-id>`.
- `stage.sh <manifest> <run-id>` verifies file digests before moving assets into staging.
- `backup.sh <component> <run-id>` records a manifest, unit files, config, database and ownership/mode metadata.
- `rollback.sh <component> <run-id>` restores only the recorded component and runs `systemctl daemon-reload` without touching unrelated services.

- [ ] **Step 1: Write shell contract tests**

Test path containment, required manifest fields, atomic directory creation, backup contents and refusal to restore an unknown run ID.

- [ ] **Step 2: Confirm RED**

Run the shell tests; expect missing scripts.

- [ ] **Step 3: Implement least-privilege primitives**

Use `umask 077`, `install -d`, temporary sibling directories, `mv` atomic promotion, fixed allowlisted component paths, and explicit owner/mode capture. Never use a broad recursive deletion.

- [ ] **Step 4: Verify with throwaway fixture paths**

Run shell syntax checks and fixture tests; ensure no files are created under `/etc`, `/var/lib`, or `/var/backups` during fixture mode.

- [ ] **Step 5: Commit**

`git add deploy/local-vm/native && git commit -m "feat: add native deployment backup primitives"`

### Task 4: AimiliVPN native installation and current topology manifest

**Files:**
- Create: `deploy/local-vm/native/install-aimilivpn.sh`
- Modify: `deploy/local-vm/native/deployment.json`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- `install-aimilivpn.sh --check` is read-only and reports OS, TUN, OpenVPN, Python, disk and current service state.
- `install-aimilivpn.sh --apply --source-commit <hash>` invokes the existing AimiliVPN installer with the pinned fork/branch/commit, preserves existing data, writes only local `/etc/default/aimilivpn` values, and enables `aimilivpn.service`.

- [ ] **Step 1: Write preflight and idempotency tests**

Assert `--check` rejects a missing TUN or unsupported Ubuntu, accepts a fixture with four expected OpenVPN processes, and `--apply` refuses to run when public network gates are false.

- [ ] **Step 2: Confirm RED**

Run the fixture tests before adding the installer wrapper.

- [ ] **Step 3: Implement wrapper around existing installer**

Pass the fork, branch and pinned commit as arguments; do not embed passwords in arguments. Preserve `MULTI_EXIT_SLOTS`, `MAX_EXIT_SLOTS` and existing data. Apply only the current manifest slot count after installation.

- [ ] **Step 4: Verify first service checkpoint**

Run check/apply in the VM only after Task 2 passes, then verify `systemctl is-enabled --quiet aimilivpn`, loopback control API, one main tunnel and the manifest slot count.

- [ ] **Step 5: Commit in AimiliVPN and Gateway**

AimiliVPN changes, if any, get a separate local commit; Gateway wrapper gets `feat: install AimiliVPN natively in local VM`.

### Task 5: 3x-ui/Xray and Caddy native installation

**Files:**
- Create: `deploy/local-vm/native/install-xui-caddy.sh`
- Create: `deploy/local-vm/native/local-caddy.Caddyfile`
- Modify: `deploy/local-vm/native/deployment.json`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- `install-xui-caddy.sh --check` validates architecture, disk, ports and absence/presence of `x-ui.service` and `caddy.service`.
- `--apply` installs the pinned official x-ui version using the existing verified installation model, configures loopback-only panel/subscription listeners, installs Caddy without ny certificates, and writes the manifest-declared current Xray instance expectation.

- [ ] **Step 1: Write tests for loopback ports, service units and dynamic Xray count**
- [ ] **Step 2: Run tests RED**
- [ ] **Step 3: Implement check/apply with one component backup per run**
- [ ] **Step 4: Verify x-ui API, Caddy config, loopback listeners and actual Xray process count**
- [ ] **Step 5: Commit**

### Task 6: Gateway native installation and service wiring

**Files:**
- Create: `deploy/local-vm/native/install-gateway.sh`
- Modify: `deploy/local-vm/native/deployment.json`
- Reuse: `deploy/systemd/aimili-gateway.service`, `deploy/config/config.example.json`, `deploy/caddy/AimiliGateway.Caddyfile`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- `install-gateway.sh --check` validates a locally built Gateway binary, config schema and credentials directory.
- `--apply` installs the binary, creates an independent SQLite/config/credential set, installs the existing hardened unit, runs the local admin initializer interactively or via a protected stdin file, and starts `aimili-gateway.service`.

- [ ] **Step 1: Write tests for config paths, unit hardening and no-ny references**
- [ ] **Step 2: Run tests RED**
- [ ] **Step 3: Implement staging, config generation and systemd installation**
- [ ] **Step 4: Verify Gateway health, database readability, control-token access and Caddy reverse proxy**
- [ ] **Step 5: Commit**

### Task 7: Ordered four-exit activation and dynamic verification

**Files:**
- Create: `deploy/local-vm/native/verify-native.sh`
- Create: `deploy/local-vm/native/enable-exits.sh`
- Modify: `deploy/local-vm/status.ps1`
- Modify: `deploy/local-vm/tests/run.ps1`

**Interfaces:**
- `enable-exits.sh --slot <n>` enables one manifest-declared slot and verifies its TUN, policy route, local port and real egress before continuing.
- `verify-native.sh --json` compares expected and actual values from the manifest, checks all four services, database readability, subscription exit set, protocol isolation, and host-safety evidence.

- [ ] **Step 1: Write failing tests for ordered activation, mismatch reporting and dynamic counts**
- [ ] **Step 2: Run tests RED**
- [ ] **Step 3: Implement one-slot-at-a-time activation and redacted verification**
- [ ] **Step 4: Run main connection, exit1, exit2, exit3 checks in order**
- [ ] **Step 5: Commit**

### Task 8: Restart recovery, final audit and local handoff

**Files:**
- Modify: `deploy/local-vm/native/verify-native.sh`
- Modify: `docs/handoffs/2026-09-05-current-state-handoff.md`
- Create: `docs/verification/2026-09-06-local-vm-native-systemd.md`

- [ ] **Step 1: Add restart/recovery assertions**
- [ ] **Step 2: Reboot the VM only after all previous checkpoints pass**
- [ ] **Step 3: Verify service enablement, current topology counts, databases, routes, disk and UFW**
- [ ] **Step 4: Run `git diff --check`, focused tests, AimiliVPN full tests and Gateway affected tests**
- [ ] **Step 5: Run a final host safety snapshot and write a redacted verification record**
- [ ] **Step 6: Create separate local commits; do not push**

## Final review checklist

- [ ] No Docker-specific local deployment files, tests or current README references remain.
- [ ] Manifest numbers are current expectations only; no permanent OpenVPN/Xray maximum was introduced.
- [ ] VM network gate passed or a precise external blocker is recorded.
- [ ] Four native services are enabled and healthy for the current deployment.
- [ ] Current four logical exits pass ordered data-plane verification.
- [ ] VM restart recovery passes.
- [ ] Host PID, proxy and default-route safety snapshots are unchanged.
- [ ] User browser, subscription import and v2rayN acceptance remain explicitly marked as user-executed/not executed.
