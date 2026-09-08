#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
script="$repo_root/deploy/local-vm/native/install-aimilivpn.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

fake_bin="$fixture/bin"
mkdir -p "$fake_bin"
systemctl_log="$fixture/systemctl.log"
installer_log="$fixture/installer.log"
env_file="$fixture/aimilivpn.default"
ui_config="$fixture/ui_auth.json"
data_marker="$fixture/existing-data"
printf '%s\n' preserved > "$data_marker"
printf '%s\n' 'OTHER_SETTING=preserved' 'MULTI_EXIT_SLOTS=1' 'MAX_EXIT_SLOTS=2' 'UI_HOST=0.0.0.0' > "$env_file"
printf '%s\n' '{"host":"::","port":8787,"password":"preserved-secret"}' > "$ui_config"
chmod 0600 "$ui_config"
cat > "$fixture/os-release" <<'EOF'
ID=ubuntu
VERSION_ID=24.04
EOF
cat > "$fixture/unsupported-os-release" <<'EOF'
ID=debian
VERSION_ID=12
EOF

cat > "$fake_bin/systemctl" <<'SH'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
if [[ "${1:-}" == is-active ]]; then printf '%s\n' active; exit 0; fi
if [[ "${1:-}" == restart ]]; then
  grep -qx 'MULTI_EXIT_SLOTS=3' "$AIMILI_ENV_FILE"
  grep -qx 'MAX_EXIT_SLOTS=16' "$AIMILI_ENV_FILE"
  grep -qx 'TARGET_VALID_POOL_SIZE=40' "$AIMILI_ENV_FILE"
  grep -qx 'UI_HOST=127.0.0.1' "$AIMILI_ENV_FILE"
  python3 - "$AIMILI_UI_CONFIG" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); assert d['host']=='127.0.0.1' and d['port']==8787 and d['password']=='preserved-secret', d
assert d['exit_slot_active']==[0,1,2] and d['exit_slot_count']==3 and d['exit_slot_paused']==[], d
PY
fi
SH
cat > "$fake_bin/ip" <<'SH'
#!/bin/bash
exit 0
SH
cat > "$fake_bin/pgrep" <<'SH'
#!/bin/bash
printf '%s\n' 4
SH
cat > "$fake_bin/df" <<'SH'
#!/bin/bash
printf '%s\n' 'Filesystem 1048576-blocks Used Available Capacity Mounted on' 'fixture 4096 1024 3072 25% /'
SH
chmod 0700 "$fake_bin"/*
for command_name in bash python3 curl awk dirname mktemp rm install chmod sed touch grep; do
  ln -s "$(command -v "$command_name")" "$fake_bin/$command_name"
done

cat > "$fixture/network-preflight.sh" <<'SH'
#!/bin/bash
printf '%s\n' '{"gatewayReachable":true,"publicTcp443":true,"dnsResolution":true,"httpsReachable":true,"ufwOutgoingAllowed":true,"failureBoundary":"none"}'
SH
chmod 0700 "$fixture/network-preflight.sh"

cat > "$fixture/installer.sh" <<'SH'
#!/bin/bash
set -euo pipefail
test -f "${APT_CONFIG:-}"
grep -qx 'DPkg::Lock::Timeout "120";' "$APT_CONFIG"
printf '%s\n' invocation >> "$INSTALLER_LOG"
printf '%s\n' 'sensitive-canary-stdout'
printf '%s\n' 'sensitive-canary-stderr' >&2
install -m 0700 /bin/true "$FAKE_BIN/openvpn"
SH
chmod 0700 "$fixture/installer.sh"

common_env=(PATH="$fake_bin" FAKE_BIN="$fake_bin" SYSTEMCTL_LOG="$systemctl_log" INSTALLER_LOG="$installer_log" AIMILI_NETWORK_PROBE="$fixture/network-preflight.sh" AIMILI_OS_RELEASE="$fixture/os-release" AIMILI_TUN_PATH=/dev/null AIMILI_DISK_PATH="$fixture" AIMILI_ENV_FILE="$env_file" AIMILI_UI_CONFIG="$ui_config")

check_output="$(env "${common_env[@]}" bash "$script" --check --slot-count 3)"
python3 - "$check_output" <<'PY'
import json, sys
r = json.loads(sys.argv[1])
assert r == {'mode':'check','os':'ubuntu-24.04','osSupported':True,'tunPresent':True,'pythonPresent':True,'diskFreeMiB':3072,'serviceStatus':'active','openvpnCount':4,'expectedOpenvpn':4,'openvpnMatchesExpected':True}, r
PY
if env "${common_env[@]}" AIMILI_OS_RELEASE="$fixture/unsupported-os-release" bash "$script" --check >/dev/null 2>&1; then echo 'unsupported OS was accepted' >&2; exit 1; fi
if env "${common_env[@]}" AIMILI_TUN_PATH="$fixture/missing-tun" bash "$script" --check >/dev/null 2>&1; then echo 'missing TUN was accepted' >&2; exit 1; fi

for _ in 1 2; do
  output="$(env "${common_env[@]}" bash "$script" --apply --source-commit edd08172a2ce132f2e1525d7e00b047a56883ff9 --installer "$fixture/installer.sh" 2>&1)"
  [[ "$output" == 'aimilivpn_apply_ok' ]] || { printf 'unexpected installer wrapper output: %s\n' "$output" >&2; exit 1; }
  ! grep -q 'sensitive-canary' <<< "$output" || { echo 'installer credentials escaped wrapper output' >&2; exit 1; }
done
[[ "$(grep -c '^invocation$' "$installer_log")" -eq 2 ]]
grep -qx preserved "$data_marker"
grep -qx 'OTHER_SETTING=preserved' "$env_file"
[[ "$(grep -c '^MULTI_EXIT_SLOTS=3$' "$env_file")" -eq 1 ]]
[[ "$(grep -c '^MAX_EXIT_SLOTS=16$' "$env_file")" -eq 1 ]]
[[ "$(grep -c '^TARGET_VALID_POOL_SIZE=40$' "$env_file")" -eq 1 ]]
[[ "$(grep -c '^UI_HOST=127.0.0.1$' "$env_file")" -eq 1 ]]
python3 - "$ui_config" <<'PY'
import json,os,sys
d=json.load(open(sys.argv[1])); assert d=={'host':'127.0.0.1','port':8787,'password':'preserved-secret','exit_slot_active':[0,1,2],'exit_slot_count':3,'exit_slot_paused':[]}, d
assert os.stat(sys.argv[1]).st_mode & 0o777 == 0o600
PY
[[ "$(grep -c '^restart aimilivpn.service$' "$systemctl_log")" -eq 2 ]]
printf '%s\n' 'PASS AimiliVPN installer check and idempotent apply fixture'
