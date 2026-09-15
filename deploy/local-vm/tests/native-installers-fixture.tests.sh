#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
xui="$repo_root/deploy/local-vm/native/install-xui-caddy.sh"
gateway="$repo_root/deploy/local-vm/native/install-gateway.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT
fake_bin="$fixture/bin"
mkdir -p "$fake_bin" "$fixture/x-ui/bin" "$fixture/etc" "$fixture/state"
printf 'ID=ubuntu\nVERSION_ID=24.04\n' > "$fixture/os-release"
printf 'fixture-token\n' > "$fixture/control.token"
printf '#!/bin/sh\nif [ "${1:-}" = version ]; then echo v3.7.0; fi\nexit 0\n' > "$fixture/x-ui/x-ui"
chmod 0700 "$fixture/x-ui/x-ui"
printf '#!/bin/sh\n# inboundAliases\nif [ "${1:-}" = version ]; then echo v3.7.0; fi\nexit 0\n' > "$fixture/x-ui-custom"
chmod 0700 "$fixture/x-ui-custom"
printf '#!/bin/sh\nexit 0\n' > "$fixture/x-ui/bin/xray-linux-amd64"
chmod 0700 "$fixture/x-ui/bin/xray-linux-amd64"
cat > "$fake_bin/systemctl" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
exit 0
SH
cat > "$fake_bin/ufw" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$UFW_LOG"
exit 0
SH
cat > "$fake_bin/caddy" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$CADDY_LOG"
exit 0
SH
cat > "$fake_bin/update-ca-certificates" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$CA_UPDATE_LOG"
exit 0
SH
cat > "$fake_bin/openssl" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$OPENSSL_LOG"
exit 0
SH
cat > "$fake_bin/curl" <<'SH'
#!/bin/sh
case "$*" in
  *127.0.0.1:2001/xui/csrf-token) printf '{"success":true,"obj":"fixture-csrf"}\n' ;;
  *) exit 1 ;;
esac
SH
cat > "$fake_bin/ss" <<'SH'
#!/bin/sh
printf 'LISTEN 0 128 127.0.0.1:2001 0.0.0.0:*\n'
SH
cat > "$fake_bin/aimili-xui-helper" <<'PY'
#!/usr/bin/env python3
import os,sys
assert not sys.argv[1:]
open(os.environ['XUI_HELPER_INPUT'],'w').write(sys.stdin.read())
PY
cat > "$fake_bin/systemd-creds" <<'SH'
#!/bin/sh
if [ "$1" != encrypt ]; then exit 1; fi
shift
case "$1" in --name=*) shift ;; esac
cat "$1" > "$2"
exit 0
SH
cat > "$fake_bin/runuser" <<'SH'
#!/bin/sh
while [ "$1" != -- ]; do shift; done
shift
payload="$(cat)"
printf '%s\n' "$payload" > "$ADMIN_INPUT_CAPTURE"
printf '%s\n' "$payload" | "$@"
SH
cat > "$fake_bin/id" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$fake_bin/install" <<'SH'
#!/usr/bin/env bash
args=()
while [ $# -gt 0 ]; do
  case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
done
/usr/bin/install "${args[@]}"
SH
cat > "$fake_bin/chown" <<'SH'
#!/bin/sh
exit 0
SH
chmod 0700 "$fake_bin"/*
for command_name in bash python3 curl sha256sum systemctl install chmod mkdir cp mv rm df awk uname dirname mktemp sed grep ss; do
  [ -e "$fake_bin/$command_name" ] || ln -s "$(command -v "$command_name")" "$fake_bin/$command_name"
done
cat > "$fixture/template.json" <<'JSON'
{"publicOrigin":"https://example.invalid:8080","databasePath":"old.db"}
JSON
cat > "$fixture/deployment.json" <<'JSON'
{"schemaVersion":1,"expected":{"exitSlots":3}}
JSON
python3 - "$fixture/x-ui.db" <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as connection:
    connection.execute('CREATE TABLE settings (id INTEGER PRIMARY KEY AUTOINCREMENT, key TEXT, value TEXT)')
PY
printf 'fixture certificate\n' > "$fixture/caddy.crt"
printf 'fixture private key\n' > "$fixture/caddy.key"
printf 'fixture root certificate\n' > "$fixture/caddy-root.crt"
printf 'fixture system CA bundle\n' > "$fixture/ca-certificates.crt"
common=(PATH="$fake_bin:/usr/bin:/bin" AIMILI_SKIP_NETWORK_PREFLIGHT=1 AIMILI_OS_RELEASE="$fixture/os-release" AIMILI_XUI_ROOT="$fixture/x-ui" AIMILI_XUI_DB="$fixture/x-ui.db" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_XUI_VERSION_STATE="$fixture/xui-install.json" AIMILI_XUI_HELPER="$fake_bin/aimili-xui-helper" AIMILI_XUI_UNIT_DEST="$fixture/x-ui.service.installed" XUI_HELPER_INPUT="$fixture/xui-helper.json" AIMILI_CADDYFILE="$fixture/Caddyfile" AIMILI_CADDY_FIREWALL_STATE="$fixture/caddy-firewall.json" AIMILI_CADDY_ROOT_CERTIFICATE="$fixture/caddy-root.crt" AIMILI_CADDY_TRUST_CERTIFICATE="$fixture/aimili-local-caddy.crt" AIMILI_SYSTEM_CA_BUNDLE="$fixture/ca-certificates.crt" SYSTEMCTL_LOG="$fixture/systemctl.log" UFW_LOG="$fixture/ufw.log" CADDY_LOG="$fixture/caddy.log" CA_UPDATE_LOG="$fixture/ca-update.log" OPENSSL_LOG="$fixture/openssl.log")
env "${common[@]}" bash "$xui" --apply --binary "$fixture/x-ui-custom" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
cmp -s "$fixture/x-ui-custom" "$fixture/x-ui/x-ui"
grep -q 'tls internal' "$fixture/Caddyfile"
grep -q 'reverse_proxy 127.0.0.1:8787' "$fixture/Caddyfile"
grep -q '@subscription path /sub/\*' "$fixture/Caddyfile"
grep -q 'reverse_proxy 127.0.0.1:2096' "$fixture/Caddyfile"
cmp -s "$fixture/caddy-root.crt" "$fixture/aimili-local-caddy.crt"
grep -qx -- '--fresh' "$fixture/ca-update.log"
grep -q '^verify -CAfile ' "$fixture/openssl.log"
! grep -q -- '-username\|-password' "$fixture/xui-helper.json"
python3 - "$fixture/xui-helper.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); assert d['baseUrl']=='http://127.0.0.1:2001/xui/'
PY
python3 - "$fixture/xui-credentials.json" <<'PY'
import json,os,sys,stat
d=json.load(open(sys.argv[1])); assert set(d)=={'username','password'}
assert stat.S_IMODE(os.stat(sys.argv[1]).st_mode)==0o600
PY
if env "${common[@]}" bash "$xui" --check --binary "$fixture/x-ui-custom" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin http://192.168.1.20:8080 >/dev/null 2>&1; then echo 'HTTP origin accepted' >&2; exit 1; fi
if env "${common[@]}" bash "$xui" --check --binary "$fixture/x-ui-custom" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://127.0.0.1:8080 >/dev/null 2>&1; then echo 'non-RFC1918 origin accepted' >&2; exit 1; fi

admin_bin="$fixture/admin"
cat > "$admin_bin" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$ADMIN_ARGS_LOG"
cat > "$ADMIN_DB"
exit 0
SH
chmod 0700 "$admin_bin"
printf '#!/bin/sh\nexit 0\n' > "$fixture/gateway"
chmod 0700 "$fixture/gateway"
env PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_BIN_DIR="$fixture/bin-install" AIMILI_ACCOUNT_WRAPPER_DEST="$fixture/account-wrapper" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_GATEWAY_ETC="$fixture/gateway-etc" AIMILI_GATEWAY_STATE="$fixture/gateway-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit" AIMILI_CREDSTORE="$fixture/credstore" AIMILI_GATEWAY_CONFIG="$fixture/config.json" AIMILI_GATEWAY_INIT_INPUT="$fixture/admin-input" AIMILI_GATEWAY_DB="$fixture/gateway.db" AIMILI_PROTOCOL_BIN_DEST="$fixture/protocol-bin" AIMILI_PROTOCOL_LIB_DIR="$fixture/protocol-lib" AIMILI_PROTOCOL_PATH_DEST="$fixture/protocol.path" AIMILI_PROTOCOL_SERVICE_DEST="$fixture/protocol.service" AIMILI_PROTOCOL_TIMER_DEST="$fixture/protocol.timer" AIMILI_PROTOCOL_STATE="$fixture/protocol-state" AIMILI_PROTOCOL_CONFIG="$fixture/gateway-etc/protocol-transaction.json" AIMILI_PROTOCOL_CERTIFICATE="$fixture/caddy.crt" AIMILI_PROTOCOL_PRIVATE_KEY="$fixture/caddy.key" SYSTEMCTL_LOG="$fixture/systemctl.log" ADMIN_INPUT_CAPTURE="$fixture/admin-capture" ADMIN_DB="$fixture/gateway.db" ADMIN_ARGS_LOG="$fixture/admin-args.log" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
python3 - "$fixture/config.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); assert d['publicOrigin']=='https://192.168.1.20:8080'; assert d['masterKeyFile'].startswith('/run/credentials/aimili-gateway.service/')
PY
test "$(wc -c < "$fixture/credstore/aimili-gateway-master-key")" -eq 32
test "$(wc -l < "$fixture/admin-capture")" -eq 3
! grep -q 'http://127.0.0.1:8080' "$fixture/config.json"
test "$(stat -c '%a' "$fixture/config.json")" = 400
grep -q 'LoadCredentialEncrypted=gateway-master-key:' "$fixture/unit"
cmp "$repo_root/deploy/bin/aimili-gateway-account" "$fixture/account-wrapper"
if env PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_ETC="$fixture/bad" AIMILI_GATEWAY_STATE="$fixture/bad-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin http://192.168.1.20:8080 >/dev/null 2>&1; then echo 'Gateway HTTP origin accepted' >&2; exit 1; fi
if env PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_ETC="$fixture/bad" AIMILI_GATEWAY_STATE="$fixture/bad-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://127.0.0.1:8080 >/dev/null 2>&1; then echo 'Gateway non-RFC1918 origin accepted' >&2; exit 1; fi
before_admin="$(sha256sum "$fixture/admin-capture" | awk '{print $1}')"
env PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_BIN_DIR="$fixture/bin-install" AIMILI_ACCOUNT_WRAPPER_DEST="$fixture/account-wrapper" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_GATEWAY_ETC="$fixture/gateway-etc" AIMILI_GATEWAY_STATE="$fixture/gateway-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit" AIMILI_CREDSTORE="$fixture/credstore" AIMILI_GATEWAY_CONFIG="$fixture/config.json" AIMILI_GATEWAY_INIT_INPUT="$fixture/admin-input" AIMILI_GATEWAY_DB="$fixture/gateway.db" AIMILI_PROTOCOL_BIN_DEST="$fixture/protocol-bin" AIMILI_PROTOCOL_LIB_DIR="$fixture/protocol-lib" AIMILI_PROTOCOL_PATH_DEST="$fixture/protocol.path" AIMILI_PROTOCOL_SERVICE_DEST="$fixture/protocol.service" AIMILI_PROTOCOL_TIMER_DEST="$fixture/protocol.timer" AIMILI_PROTOCOL_STATE="$fixture/protocol-state" AIMILI_PROTOCOL_CONFIG="$fixture/gateway-etc/protocol-transaction.json" AIMILI_PROTOCOL_CERTIFICATE="$fixture/caddy.crt" AIMILI_PROTOCOL_PRIVATE_KEY="$fixture/caddy.key" SYSTEMCTL_LOG="$fixture/systemctl.log" ADMIN_INPUT_CAPTURE="$fixture/admin-capture" ADMIN_DB="$fixture/gateway.db" ADMIN_ARGS_LOG="$fixture/admin-args.log" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
after_admin="$(sha256sum "$fixture/admin-capture" | awk '{print $1}')"
test "$before_admin" = "$after_admin"
echo 'PASS native installer behavior fixture'
