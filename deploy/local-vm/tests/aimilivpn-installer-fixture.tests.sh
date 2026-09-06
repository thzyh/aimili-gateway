#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
script="$repo_root/deploy/local-vm/native/install-aimilivpn.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

fake_bin="$fixture/bin"
mkdir -p "$fake_bin"
systemctl_log="$fixture/systemctl.log"
env_file="$fixture/aimilivpn.default"
cat > "$fixture/os-release" <<'EOF'
ID=ubuntu
VERSION_ID=24.04
EOF

cat > "$fake_bin/systemctl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
if [[ "${1:-}" == restart ]]; then
  grep -qx 'MULTI_EXIT_SLOTS=3' "$AIMILI_ENV_FILE"
  grep -qx 'MAX_EXIT_SLOTS=16' "$AIMILI_ENV_FILE"
fi
SH
chmod 0700 "$fake_bin/systemctl"

cat > "$fake_bin/ip" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod 0700 "$fake_bin/ip"

cat > "$fixture/network-preflight.sh" <<'SH'
#!/usr/bin/env bash
printf '%s\n' '{"gatewayReachable":true,"publicTcp443":true,"dnsResolution":true,"httpsReachable":true,"ufwOutgoingAllowed":true,"failureBoundary":"none"}'
SH
chmod 0700 "$fixture/network-preflight.sh"

cat > "$fixture/installer.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' 'sensitive-canary-stdout'
printf '%s\n' 'sensitive-canary-stderr' >&2
install -m 0700 /bin/true "$FAKE_BIN/openvpn"
SH
chmod 0700 "$fixture/installer.sh"

output="$({
  PATH="$fake_bin:/usr/bin:/bin" \
  FAKE_BIN="$fake_bin" \
  SYSTEMCTL_LOG="$systemctl_log" \
  AIMILI_NETWORK_PROBE="$fixture/network-preflight.sh" \
  AIMILI_OS_RELEASE="$fixture/os-release" \
  AIMILI_ENV_FILE="$env_file" \
  bash "$script" --apply --source-commit edd08172a2ce132f2e1525d7e00b047a56883ff9 --installer "$fixture/installer.sh"
} 2>&1)"

[[ "$output" == 'aimilivpn_apply_ok' ]] || { printf 'unexpected installer wrapper output: %s\n' "$output" >&2; exit 1; }
! grep -q 'sensitive-canary' <<< "$output" || { printf 'installer credentials escaped wrapper output\n' >&2; exit 1; }
grep -qx 'daemon-reload' "$systemctl_log"
grep -qx 'enable aimilivpn.service' "$systemctl_log"
grep -qx 'restart aimilivpn.service' "$systemctl_log"

printf '%s\n' 'PASS AimiliVPN installer apply fixture'
