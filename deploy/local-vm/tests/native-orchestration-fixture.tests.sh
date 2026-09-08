#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
stage="$repo_root/deploy/local-vm/native/stage.sh"
backup="$repo_root/deploy/local-vm/native/backup.sh"
rollback="$repo_root/deploy/local-vm/native/rollback.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

mkdir -p "$fixture/assets/bin" "$fixture/targets/etc/aimili-gateway" "$fixture/targets/var/lib/aimili-gateway" "$fixture/targets/etc/aimili-local" "$fixture/targets/usr/local/bin" "$fixture/targets/usr/local/share/ca-certificates" "$fixture/targets/usr/lib/aimili-gateway" "$fixture/targets/etc/systemd/system" "$fixture/targets/var/lib/aimili-xui-protocol-transaction"
printf 'gateway-v1\n' > "$fixture/assets/bin/aimili-gateway"
printf 'gateway-admin-v1\n' > "$fixture/assets/bin/aimili-gateway-admin"
printf 'old-config\n' > "$fixture/targets/etc/aimili-gateway/config.json"
printf 'old-state\n' > "$fixture/targets/var/lib/aimili-gateway/state"
printf 'old-helper\n' > "$fixture/targets/usr/local/bin/aimili-xui-protocol-transaction"
printf 'old-helper-script\n' > "$fixture/targets/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py"
printf 'old-protocol-state\n' > "$fixture/targets/var/lib/aimili-xui-protocol-transaction/state"
for unit in path service timer; do printf 'old-%s-unit\n' "$unit" > "$fixture/targets/etc/systemd/system/aimili-xui-protocol-transaction.$unit"; done
printf '{"allowedSource":"192.168.88.1"}\n' > "$fixture/targets/etc/aimili-local/caddy-firewall.json"
sha256="$(sha256sum "$fixture/assets/bin/aimili-gateway" | awk '{print $1}')"
admin_sha256="$(sha256sum "$fixture/assets/bin/aimili-gateway-admin" | awk '{print $1}')"
cat > "$fixture/assets/manifest.json" <<JSON
{"schemaVersion":1,"files":[{"path":"bin/aimili-gateway","sha256":"$sha256"},{"path":"bin/aimili-gateway-admin","sha256":"$admin_sha256"}]}
JSON

if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/manifest.json" run-1 >/dev/null 2>&1; then
  test -s "$fixture/staging/run-1/bin/aimili-gateway"
  test -s "$fixture/staging/run-1/bin/aimili-gateway-admin"
else
  echo 'stage valid manifest rejected' >&2
  exit 1
fi
test ! -e "$fixture/staging/run-1.tmp"

cp "$fixture/assets/manifest.json" "$fixture/assets/bad.json"
sed -i 's/"sha256":"[0-9a-f]*/"sha256":"0000000000000000000000000000000000000000000000000000000000000000/' "$fixture/assets/bad.json"
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/bad.json" run-bad >/dev/null 2>&1; then
  echo 'digest mismatch accepted' >&2
  exit 1
fi
test ! -e "$fixture/staging/run-bad"

cat > "$fixture/assets/escape.json" <<'JSON'
{"schemaVersion":1,"files":[{"path":"../escape","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/escape.json" run-escape >/dev/null 2>&1; then
  echo 'path escape accepted' >&2
  exit 1
fi

cat > "$fixture/assets/unknown.json" <<JSON
{"schemaVersion":1,"files":[{"path":"bin/aimili-gateway","sha256":"$sha256"}],"unknown":true}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/unknown.json" run-unknown >/dev/null 2>&1; then
  echo 'unknown manifest field accepted' >&2
  exit 1
fi

ln -s bin/aimili-gateway "$fixture/assets/link"
cat > "$fixture/assets/link.json" <<JSON
{"schemaVersion":1,"files":[{"path":"link","sha256":"$sha256"}]}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/link.json" run-link >/dev/null 2>&1; then
  echo 'symlink asset accepted' >&2
  exit 1
fi

printf '#!/bin/sh\nprintf "%%s\\n" "$*" >> "$SYSTEMCTL_LOG"\n' > "$fixture/systemctl"
chmod 0700 "$fixture/systemctl"
printf '#!/bin/sh\nprintf "%%s\\n" "$*" >> "$CA_UPDATE_LOG"\n' > "$fixture/update-ca-certificates"
chmod 0700 "$fixture/update-ca-certificates"
printf 'before\n' > "$fixture/targets/etc/aimili-gateway/config.json"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component gateway --run-id run-1 --backup-root "$fixture/backups"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component gateway --run-id run-1 --backup-root "$fixture/backups" >/dev/null
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component gateway --run-id run-corrupt --backup-root "$fixture/backups" >/dev/null
corrupt_payload="$(find "$fixture/backups/run-corrupt/gateway/entries" -mindepth 2 -maxdepth 2 -name payload -print -quit)"
rm -rf -- "$corrupt_payload"
if env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component gateway --run-id run-corrupt --backup-root "$fixture/backups" >/dev/null 2>&1; then
  echo 'existing backup with missing payload accepted' >&2
  exit 1
fi
printf 'after\n' > "$fixture/targets/etc/aimili-gateway/config.json"
printf 'new-helper\n' > "$fixture/targets/usr/local/bin/aimili-xui-protocol-transaction"
printf 'new-helper-script\n' > "$fixture/targets/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py"
printf 'new-protocol-state\n' > "$fixture/targets/var/lib/aimili-xui-protocol-transaction/state"
rm -f "$fixture/targets/var/lib/aimili-gateway/state"
mkdir -p "$fixture/targets/etc/systemd/system"
printf 'new-unit\n' > "$fixture/targets/etc/systemd/system/aimili-gateway.service"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$rollback" --component gateway --run-id run-1 --backup-root "$fixture/backups"
grep -qx 'before' "$fixture/targets/etc/aimili-gateway/config.json"
grep -qx 'old-helper' "$fixture/targets/usr/local/bin/aimili-xui-protocol-transaction"
grep -qx 'old-helper-script' "$fixture/targets/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py"
grep -qx 'old-protocol-state' "$fixture/targets/var/lib/aimili-xui-protocol-transaction/state"
test -s "$fixture/targets/var/lib/aimili-gateway/state"
test ! -e "$fixture/targets/etc/systemd/system/aimili-gateway.service"
grep -qx daemon-reload "$fixture/systemctl.log"
grep -qx 'enable aimili-gateway.service' "$fixture/systemctl.log"
grep -qx 'restart aimili-gateway.service' "$fixture/systemctl.log"
grep -qx 'enable aimili-xui-protocol-transaction.path' "$fixture/systemctl.log"
grep -qx 'restart aimili-xui-protocol-transaction.timer' "$fixture/systemctl.log"

cat > "$fixture/ufw" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$UFW_LOG"
if [ -n "${UFW_FAIL_ALLOW_SOURCE:-}" ] && [ "$*" = "allow from $UFW_FAIL_ALLOW_SOURCE to any port 8080 proto tcp" ]; then
  exit 42
fi
if [ -n "${UFW_FAIL_DELETE_SOURCE:-}" ] && [ "$*" = "delete allow from $UFW_FAIL_DELETE_SOURCE to any port 8080 proto tcp" ]; then
  exit 43
fi
SH
chmod 0700 "$fixture/ufw"
rm -f "$fixture/systemctl.log"
printf '{"allowedSource":"192.168.88.4","rules":[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"}]}\n' > "$fixture/targets/etc/aimili-local/caddy-firewall.json"
printf 'old-caddy-root\n' > "$fixture/targets/usr/local/share/ca-certificates/aimili-local-caddy.crt"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" UFW_LOG="$fixture/ufw.log" SYSTEMCTL_LOG="$fixture/systemctl.log" CA_UPDATE_LOG="$fixture/ca-update.log" bash "$backup" --component xui-caddy --run-id run-xui --backup-root "$fixture/backups" >/dev/null
printf '{"allowedSource":"192.168.88.9","rules":[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"}]}\n' > "$fixture/targets/etc/aimili-local/caddy-firewall.json"
printf 'new-caddy-root\n' > "$fixture/targets/usr/local/share/ca-certificates/aimili-local-caddy.crt"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" UFW_LOG="$fixture/ufw.log" SYSTEMCTL_LOG="$fixture/systemctl.log" CA_UPDATE_LOG="$fixture/ca-update.log" bash "$rollback" --component xui-caddy --run-id run-xui --backup-root "$fixture/backups" >/dev/null
grep -qx '{"allowedSource":"192.168.88.4","rules":\[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"}\]}' "$fixture/targets/etc/aimili-local/caddy-firewall.json"
grep -q 'delete allow from 192.168.88.9' "$fixture/ufw.log"
grep -q 'allow from 192.168.88.4' "$fixture/ufw.log"
grep -q 'delete allow from 192.168.88.9 to any port 8443 proto udp' "$fixture/ufw.log"
grep -q 'allow from 192.168.88.4 to any port 8443 proto tcp' "$fixture/ufw.log"
grep -qx 'old-caddy-root' "$fixture/targets/usr/local/share/ca-certificates/aimili-local-caddy.crt"
grep -qx -- '--fresh' "$fixture/ca-update.log"
rm -f -- "$fixture/targets/etc/aimili-local/caddy-firewall.json.previous"

env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" UFW_LOG="$fixture/ufw.log" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component xui-caddy --run-id run-xui-fail --backup-root "$fixture/backups" >/dev/null
printf '{"allowedSource":"192.168.88.9","rules":[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"}]}\n' > "$fixture/targets/etc/aimili-local/caddy-firewall.json"
if env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" UFW_LOG="$fixture/ufw.log" UFW_FAIL_ALLOW_SOURCE=192.168.88.4 UFW_FAIL_DELETE_SOURCE=192.168.88.4 SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$rollback" --component xui-caddy --run-id run-xui-fail --backup-root "$fixture/backups" > /dev/null 2> "$fixture/rollback-error.log"; then
  echo 'firewall rollback ignored failure restoring previous allow rule' >&2
  exit 1
fi
grep -q 'rollback_firewall_recovery_failed' "$fixture/rollback-error.log"
grep -qx '{"allowedSource":"192.168.88.9","rules":\[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"}\]}' "$fixture/targets/etc/aimili-local/caddy-firewall.json"
test ! -e "$fixture/targets/etc/aimili-local/caddy-firewall.json.previous"

if env AIMILI_NATIVE_ROOT="$fixture/targets" bash "$rollback" --component gateway --run-id run-1 --backup-root "$fixture/backups" --target-dir /etc/passwd >/dev/null 2>&1; then
  echo 'caller supplied arbitrary rollback target accepted' >&2
  exit 1
fi

echo 'PASS native orchestration fixture'
