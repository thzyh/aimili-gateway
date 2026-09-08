#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
xui="$repo_root/deploy/local-vm/native/install-xui-caddy.sh"
gateway="$repo_root/deploy/local-vm/native/install-gateway.sh"
grep -Eq 'curl .*--retry 3 .*--retry-all-errors .*--continue-at - .*--max-time 600 ' "$xui" || {
  echo 'x-ui download is missing retry/resume coverage for slow VM NAT links' >&2
  exit 1
}
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

fake_bin="$fixture/bin"
mkdir -p "$fake_bin" "$fixture/x-ui/bin" "$fixture/etc" "$fixture/state"
printf 'ID=ubuntu\nVERSION_ID=24.04\n' > "$fixture/os-release"
printf 'fixture-token\n' > "$fixture/control.token"
printf '#!/bin/sh\n# inboundAliases\nif [ "${1:-}" = version ]; then echo v3.7.0; exit 0; fi\nif [ "${1:-}" = setting ]; then\n  printf "xui-setting\\n" >> "$TRACE_LOG"\n  [ "${FAIL_SETTING:-0}" = 1 ] && exit 9\nfi\nexit 0\n' > "$fixture/x-ui/x-ui"
chmod 0700 "$fixture/x-ui/x-ui"
printf '#!/bin/sh\nexit 0\n' > "$fixture/x-ui/bin/xray-linux-amd64"
chmod 0700 "$fixture/x-ui/bin/xray-linux-amd64"
cat > "$fixture/x-ui.service.debian" <<'UNIT'
[Unit]
Description=x-ui Service
After=network.target
Wants=network.target
StartLimitIntervalSec=180
StartLimitBurst=10

[Service]
EnvironmentFile=-/etc/default/x-ui
Environment="XRAY_VMESS_AEAD_FORCED=false"
Type=simple
WorkingDirectory=/usr/local/x-ui/
ExecStart=/usr/local/x-ui/x-ui
ExecReload=/bin/kill -USR1 $MAINPID
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
UNIT

cat > "$fake_bin/systemctl" <<'SH'
#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$TRACE_LOG"
if [ "${CHECK_SERVICE_FAIL:-0}" = 1 ] && [ "${1:-}" = is-active ]; then
  exit 42
fi
if [ -n "${DISABLED_UNIT:-}" ] && [ "$*" = "is-enabled --quiet $DISABLED_UNIT" ]; then
  exit 44
fi
if { [ "${1:-}" = enable ] && [ "${2:-}" = --now ] && [ "${3:-}" = x-ui.service ]; } || { [ "${1:-}" = restart ] && [ "${2:-}" = x-ui.service ]; }; then
  test -s "$XUI_UNIT_DEST" || exit 42
  touch "$XUI_STARTED"
  printf 'xui-start\n' >> "$TRACE_LOG"
fi
exit 0
SH
cat > "$fake_bin/caddy" <<'SH'
#!/bin/sh
printf 'caddy %s\n' "$*" >> "$CADDY_LOG"
[ "${CADDY_FAIL_VALIDATE:-0}" = 1 ] && [ "${1:-}" = validate ] && exit 42
exit 0
SH
cat > "$fake_bin/apt-get" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$fake_bin/update-ca-certificates" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$fake_bin/curl" <<'SH'
#!/bin/sh
case "$*" in
  *127.0.0.1:2001/xui/csrf-token) printf '{"success":true,"obj":"fixture-csrf"}\n' ;;
  *127.0.0.1:9080/healthz) printf '{"status":"ok"}\n' ;;
  *) exit 1 ;;
esac
SH
cat > "$fake_bin/ss" <<'SH'
#!/bin/sh
printf 'LISTEN 0 128 127.0.0.1:2001 0.0.0.0:*\n'
printf 'LISTEN 0 128 127.0.0.1:2096 0.0.0.0:*\n'
printf 'LISTEN 0 128 127.0.0.1:9080 0.0.0.0:*\n'
SH
cat > "$fake_bin/ufw" <<'SH'
#!/bin/sh
printf 'ufw %s\n' "$*" >> "$TRACE_LOG"
if [ -n "${UFW_FAIL_ALLOW_SOURCE:-}" ] && [ "$*" = "allow from $UFW_FAIL_ALLOW_SOURCE to any port 8080 proto tcp" ]; then
  exit 42
fi
[ "${UFW_FAIL_ALLOW_ALL:-0}" = 1 ] && [ "${1:-}" = allow ] && exit 42
if [ "${UFW_FAIL_DELETE_ALL:-0}" = 1 ] && [ "${1:-}" = delete ]; then
  exit 43
fi
if [ "${1:-}" = status ]; then
  printf 'Status: active\n\nTo                         Action      From\n'
  for rule in 8080/tcp 8443/tcp 8443/udp 20000/tcp 20000/udp 20001/tcp 20001/udp 20002/tcp 20002/udp 31000/tcp 30000/tcp 30001/tcp 30002/tcp; do
    [ "$rule" = "${UFW_MISSING_RULE:-}" ] || printf '%-27s ALLOW IN    %s\n' "$rule" "${UFW_STATUS_SOURCE:-192.0.2.10}"
  done
  [ "${UFW_BROAD_RULE:-0}" = 1 ] && printf '%-27s ALLOW IN    Anywhere\n' '8080/tcp'
fi
exit 0
SH
cat > "$fake_bin/python3" <<'SH'
#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$PYTHON_ARGS_LOG"; done
exec /usr/bin/python3 "$@"
SH
cat > "$fake_bin/aimili-xui-helper" <<'PY'
#!/usr/bin/env python3
import json, os, sys
assert not sys.argv[1:]
assert os.path.exists(os.environ['XUI_STARTED'])
payload = json.load(sys.stdin)
assert payload['baseUrl'] == 'http://127.0.0.1:2001/xui/'
open(os.environ['XUI_HELPER_INPUT'], 'w').write(json.dumps(payload))
with open(os.environ['TRACE_LOG'], 'a') as handle:
    handle.write('helper\n')
PY
cat > "$fake_bin/systemd-creds" <<'SH'
#!/bin/sh
test "${1:-}" = encrypt
shift
case "${1:-}" in --name=*) shift ;; esac
cat "$1" > "$2"
cp "$1" "$SYSTEMD_CRED_SOURCE_CAPTURE"
SH
cat > "$fake_bin/runuser" <<'SH'
#!/bin/sh
while [ "${1:-}" != -- ]; do shift; done
shift
cat > "$ADMIN_INPUT_CAPTURE"
cat "$ADMIN_INPUT_CAPTURE" | "$@"
SH
cat > "$fake_bin/id" <<'SH'
#!/bin/sh
exit 0
SH
cat > "$fake_bin/install" <<'SH'
#!/usr/bin/env bash
printf 'install %s\n' "$*" >> "$TRACE_LOG"
args=()
while [ $# -gt 0 ]; do
  case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
done
/usr/bin/install "${args[@]}"
SH
cat > "$fake_bin/chown" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$CHOWN_LOG"
case "${1:-}" in
  *:aimili-gateway) exit 0 ;;
esac
/usr/bin/chown "$@"
SH
chmod 0700 "$fake_bin"/*
for command_name in bash sha256sum systemctl install chmod mkdir cp mv rm df awk uname dirname mktemp sed grep stat curl tar ufw apt-get id chown ss update-ca-certificates openssl; do
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
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=Aimili fixture root' \
  -keyout "$fixture/caddy-root.key" -out "$fixture/caddy-root.crt" >/dev/null 2>&1
cp "$fixture/caddy-root.crt" "$fixture/ca-certificates.crt"

common=(PATH="$fake_bin:/usr/bin:/bin" FAKE_BIN="$fake_bin" AIMILI_SKIP_NETWORK_PREFLIGHT=1 AIMILI_OS_RELEASE="$fixture/os-release" AIMILI_XUI_ROOT="$fixture/x-ui" AIMILI_XUI_DB="$fixture/x-ui.db" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_XUI_VERSION_STATE="$fixture/xui-install.json" AIMILI_CADDYFILE="$fixture/caddy/Caddyfile" AIMILI_CADDY_FIREWALL_STATE="$fixture/caddy-firewall.json" AIMILI_CADDY_ROOT_CERTIFICATE="$fixture/caddy-root.crt" AIMILI_CADDY_TRUST_CERTIFICATE="$fixture/aimili-local-caddy.crt" AIMILI_SYSTEM_CA_BUNDLE="$fixture/ca-certificates.crt" AIMILI_XUI_HELPER="$fake_bin/aimili-xui-helper" AIMILI_XUI_UNIT_SOURCE="$fixture/x-ui.service.debian" AIMILI_XUI_UNIT_DEST="$fixture/x-ui.service.installed" XUI_HELPER_INPUT="$fixture/xui-helper.json" XUI_STARTED="$fixture/xui-started" XUI_UNIT_DEST="$fixture/x-ui.service.installed" TRACE_LOG="$fixture/trace.log" PYTHON_ARGS_LOG="$fixture/python-args.log" CADDY_LOG="$fixture/caddy.log" CHOWN_LOG="$fixture/chown.log" SYSTEMCTL_LOG="$fixture/systemctl.log")
env "${common[@]}" bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
test -s "$fixture/x-ui.service.installed"
test "$(stat -c '%a' "$fixture/caddy/Caddyfile")" = 644
test "$(stat -c '%a' "$fixture/caddy")" = 755
grep -q '@subscription path /sub/\*' "$fixture/caddy/Caddyfile"
grep -q 'reverse_proxy 127.0.0.1:2096' "$fixture/caddy/Caddyfile"
grep -q '^https://reality\.aimili\.test:443 {' "$fixture/caddy/Caddyfile"
grep -q 'bind 127.0.0.1' "$fixture/caddy/Caddyfile"
grep -q 'respond 204' "$fixture/caddy/Caddyfile"
if grep -Eq '\{[[:space:]]+[^}]' "$fixture/caddy/Caddyfile"; then
  echo 'generated Caddyfile contains an inline block rejected by Caddy 2.6' >&2
  exit 1
fi
python3 - "$fixture/x-ui.db" <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as connection:
    rows = connection.execute("SELECT value FROM settings WHERE key='subListen'").fetchall()
assert rows == [('127.0.0.1',)], rows
PY
grep -q '192.0.2.10' "$fixture/caddy-firewall.json"
test -s "$fixture/xui-install.json"
for port in 8443 20000 20001 20002; do
  grep -q "ufw allow from 192.0.2.10 to any port $port proto tcp" "$fixture/trace.log"
  grep -q "ufw allow from 192.0.2.10 to any port $port proto udp" "$fixture/trace.log"
done
for port in 31000 30000 30001 30002; do
  grep -q "ufw allow from 192.0.2.10 to any port $port proto tcp" "$fixture/trace.log"
  ! grep -q "ufw allow from 192.0.2.10 to any port $port proto udp" "$fixture/trace.log"
done
grep -q 'systemctl restart x-ui.service' "$fixture/trace.log"
test "$(grep -n '^xui-setting$' "$fixture/trace.log" | cut -d: -f1)" -lt "$(grep -n '^xui-start$' "$fixture/trace.log" | cut -d: -f1)"
test "$(grep -n '^xui-start$' "$fixture/trace.log" | cut -d: -f1)" -lt "$(grep -n '^helper$' "$fixture/trace.log" | cut -d: -f1)"
env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null
cp "$fixture/caddy/Caddyfile" "$fixture/caddy/Caddyfile.good"
sed -i '/@subscription/d;/127\.0\.0\.1:2096/d' "$fixture/caddy/Caddyfile"
if env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy check accepted a Caddyfile without the subscription route' >&2
  exit 1
fi
mv "$fixture/caddy/Caddyfile.good" "$fixture/caddy/Caddyfile"
chmod 0644 "$fixture/caddy-firewall.json"
if env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy check accepted an over-readable firewall state' >&2
  exit 1
fi
chmod 0600 "$fixture/caddy-firewall.json"
if env "${common[@]}" UFW_MISSING_RULE=20002/udp bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy check accepted a missing live firewall rule' >&2
  exit 1
fi
if env "${common[@]}" UFW_BROAD_RULE=1 bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy check accepted a broad live firewall rule on a managed port' >&2
  exit 1
fi
printf '{"allowedSource":"192.0.2.9","rules":[{"port":8080,"proto":"tcp"}]}' > "$fixture/caddy-firewall.json"
: > "$fixture/trace.log"
if env "${common[@]}" CADDY_FAIL_VALIDATE=1 UFW_FAIL_ALLOW_SOURCE=192.0.2.9 bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy installer hid a failed firewall restoration' >&2
  exit 1
fi
grep -q '"allowedSource":"192.0.2.10"' "$fixture/caddy-firewall.json"
grep -q '^ufw allow from 192.0.2.10 to any port 8080 proto tcp$' "$fixture/trace.log"
printf '{"allowedSource":"192.0.2.9","rules":[{"port":8080,"proto":"tcp"}]}' > "$fixture/caddy-firewall.json"
: > "$fixture/trace.log"
if env "${common[@]}" UFW_FAIL_DELETE_ALL=1 bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy installer hid an early firewall mutation failure' >&2
  exit 1
fi
grep -q '"allowedSource":"192.0.2.10"' "$fixture/caddy-firewall.json"
grep -q '^ufw allow from 192.0.2.10 to any port 8080 proto tcp$' "$fixture/trace.log"
printf '{"allowedSource":"192.0.2.9","rules":[{"port":8080,"proto":"tcp"}]}' > "$fixture/caddy-firewall.json"
: > "$fixture/trace.log"
if env "${common[@]}" CADDY_FAIL_VALIDATE=1 UFW_FAIL_DELETE_ALL=1 UFW_FAIL_ALLOW_ALL=1 UFW_STATUS_SOURCE=203.0.113.9 bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy installer hid an unrecovered firewall state' >&2
  exit 1
fi
grep -q '"status":"unrecovered"' "$fixture/caddy-firewall.json"
printf '{"allowedSource":"192.0.2.10","rules":[{"port":8080,"proto":"tcp"},{"port":8443,"proto":"tcp"},{"port":8443,"proto":"udp"},{"port":20000,"proto":"tcp"},{"port":20000,"proto":"udp"},{"port":20001,"proto":"tcp"},{"port":20001,"proto":"udp"},{"port":20002,"proto":"tcp"},{"port":20002,"proto":"udp"},{"port":31000,"proto":"tcp"},{"port":30000,"proto":"tcp"},{"port":30001,"proto":"tcp"},{"port":30002,"proto":"tcp"}]}' > "$fixture/caddy-firewall.json"
cp "$fixture/caddy-firewall.json" "$fixture/caddy-firewall.good"
printf '{invalid\n' > "$fixture/caddy-firewall.json"
: > "$fixture/trace.log"
if env "${common[@]}" bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy installer accepted corrupt previous firewall state' >&2
  exit 1
fi
! grep -q '^ufw ' "$fixture/trace.log"
mv "$fixture/caddy-firewall.good" "$fixture/caddy-firewall.json"
python3 - "$fixture/xui-credentials.json" "$fixture/python-args.log" <<'PY'
import json, os, stat, sys
credentials = json.load(open(sys.argv[1]))
assert set(credentials) == {'username', 'password'}
assert stat.S_IMODE(os.stat(sys.argv[1]).st_mode) == 0o600
args = open(sys.argv[2]).read()
assert credentials['username'] not in args
assert credentials['password'] not in args
PY

if env "${common[@]}" CHECK_SERVICE_FAIL=1 bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui/Caddy check accepted inactive services' >&2
  exit 1
fi

chown 65534:65534 "$fixture/xui-credentials.json"
env "${common[@]}" bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
test "$(stat -c '%u:%g' "$fixture/xui-credentials.json")" = '0:0'

if env "${common[@]}" FAIL_SETTING=1 AIMILI_XUI_CREDENTIALS="$fixture/setting-failure.json" bash "$xui" --apply --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui setting failure was ignored' >&2
  exit 1
fi
test ! -e "$fixture/setting-failure.json"

if env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://user@192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui origin userinfo accepted' >&2
  exit 1
fi
if env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin $'https://192.168.1.20\n:8080' >/dev/null 2>&1; then
  echo 'x-ui origin control character accepted' >&2
  exit 1
fi
if xui_origin_error="$(env "${common[@]}" bash "$xui" --check --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin $'https://192.168.1.20:8080\n' 2>&1)"; then
  echo 'x-ui origin trailing control character accepted' >&2
  exit 1
fi
grep -q 'public_origin_invalid' <<< "$xui_origin_error" || { echo 'x-ui origin control character was rejected only after origin validation' >&2; exit 1; }

admin_bin="$fixture/admin"
cat > "$admin_bin" <<'SH'
#!/bin/sh
/usr/bin/python3 - <<'PY'
import json, os, shutil
with open(os.environ['GATEWAY_CONFIG']) as handle:
    path = json.load(handle)['masterKeyFile']
shutil.copyfile(path, os.environ['ADMIN_KEY_CAPTURE'])
PY
cat > "$ADMIN_DB"
SH
chmod 0700 "$admin_bin"
printf '#!/bin/sh\nexit 0\n' > "$fixture/gateway"
chmod 0700 "$fixture/gateway"
gateway_common=(PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_BIN_DIR="$fixture/bin-install" AIMILI_ACCOUNT_WRAPPER_DEST="$fixture/account-wrapper" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_GATEWAY_ETC="$fixture/gateway-etc" AIMILI_GATEWAY_STATE="$fixture/gateway-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit" AIMILI_CREDSTORE="$fixture/credstore" AIMILI_GATEWAY_CONFIG="$fixture/config.json" AIMILI_GATEWAY_INIT_INPUT="$fixture/admin-input" AIMILI_GATEWAY_DB="$fixture/gateway.db" AIMILI_PROTOCOL_WRAPPER_SOURCE="$repo_root/deploy/bin/aimili-xui-protocol-transaction" AIMILI_PROTOCOL_SCRIPT_SOURCE="$repo_root/scripts/aimili_xui_protocol_transaction.py" AIMILI_PROTOCOL_CONFIG_TEMPLATE="$repo_root/deploy/config/protocol-transaction.example.json" AIMILI_PROTOCOL_PATH_SOURCE="$repo_root/deploy/systemd/aimili-xui-protocol-transaction.path" AIMILI_PROTOCOL_SERVICE_SOURCE="$repo_root/deploy/systemd/aimili-xui-protocol-transaction.service" AIMILI_PROTOCOL_TIMER_SOURCE="$repo_root/deploy/systemd/aimili-xui-protocol-transaction.timer" AIMILI_PROTOCOL_BIN_DEST="$fixture/protocol-bin" AIMILI_PROTOCOL_LIB_DIR="$fixture/protocol-lib" AIMILI_PROTOCOL_PATH_DEST="$fixture/protocol.path" AIMILI_PROTOCOL_SERVICE_DEST="$fixture/protocol.service" AIMILI_PROTOCOL_TIMER_DEST="$fixture/protocol.timer" AIMILI_PROTOCOL_STATE="$fixture/protocol-state" AIMILI_PROTOCOL_CONFIG="$fixture/gateway-etc/protocol-transaction.json" AIMILI_PROTOCOL_CERTIFICATE="$fixture/caddy.crt" AIMILI_PROTOCOL_PRIVATE_KEY="$fixture/caddy.key" ADMIN_INPUT_CAPTURE="$fixture/admin-capture" ADMIN_KEY_CAPTURE="$fixture/admin-key" ADMIN_DB="$fixture/gateway.db" SYSTEMD_CRED_SOURCE_CAPTURE="$fixture/systemd-key" PYTHON_ARGS_LOG="$fixture/python-args.log" CHOWN_LOG="$fixture/chown.log" SYSTEMCTL_LOG="$fixture/systemctl.log" TRACE_LOG="$fixture/trace.log")
env "${gateway_common[@]}" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
cmp "$fixture/credstore/aimili-gateway-master-key" "$fixture/admin-key"
grep -Fq "install -d -o root -g aimili-gateway -m 0750 $fixture/gateway-etc" "$fixture/trace.log"
test "$(stat -c '%a' "$fixture/config.json")" = 400
grep -Fq "aimili-gateway:aimili-gateway $fixture/config.json" "$fixture/chown.log"
grep -Eq '^aimili-gateway:aimili-gateway /tmp/aimili-gateway-init\.[^/]+/master\.key$' "$fixture/chown.log"
grep -Eq '^aimili-gateway:aimili-gateway /tmp/aimili-gateway-init\.[^/]+/config\.json$' "$fixture/chown.log"
for path in protocol-spool/requests protocol-spool/results ui update-spool/requests update-spool/results; do
  test -d "$fixture/gateway-state/$path"
done
test -x "$fixture/protocol-bin"
cmp "$repo_root/deploy/bin/aimili-gateway-account" "$fixture/account-wrapper"
test -s "$fixture/protocol-lib/aimili_xui_protocol_transaction.py"
test -s "$fixture/protocol.path" && test -s "$fixture/protocol.service" && test -s "$fixture/protocol.timer"
test -d "$fixture/protocol-state/transactions" && test -d "$fixture/protocol-state/profiles"
grep -q 'enable --now aimili-xui-protocol-transaction.path aimili-xui-protocol-transaction.timer' "$fixture/trace.log"
grep -q 'systemctl restart aimili-gateway.service' "$fixture/trace.log"
python3 - "$fixture/gateway-etc/admin-credentials.json" "$fixture/python-args.log" <<'PY'
import json, sys
credentials = json.load(open(sys.argv[1]))
args = open(sys.argv[2]).read()
assert credentials['username'] not in args
assert credentials['password'] not in args
PY

cp "$fixture/gateway" "$fixture/gateway-staged"
cp "$admin_bin" "$fixture/admin-staged"
chmod 0600 "$fixture/gateway-staged" "$fixture/admin-staged"
env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway-staged" --admin-binary "$fixture/admin-staged" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null

cp "$fixture/config.json" "$fixture/config.json.good"
python3 - "$fixture/config.json" <<'PY'
import json, sys
path=sys.argv[1]; document=json.load(open(path)); document['publicOrigin']='https://192.168.1.21:8080'; json.dump(document,open(path,'w'))
PY
if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway check accepted a stale public origin' >&2
  exit 1
fi
mv "$fixture/config.json.good" "$fixture/config.json"

cp "$fixture/gateway-etc/protocol-transaction.json" "$fixture/gateway-etc/protocol-transaction.json.good"
python3 - "$fixture/gateway-etc/protocol-transaction.json" <<'PY'
import json, sys
path=sys.argv[1]; document=json.load(open(path)); document['tlsServerName']='192.168.1.21'; json.dump(document,open(path,'w'))
PY
if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway check accepted stale protocol TLS material' >&2
  exit 1
fi
mv "$fixture/gateway-etc/protocol-transaction.json.good" "$fixture/gateway-etc/protocol-transaction.json"

if env "${gateway_common[@]}" CHECK_SERVICE_FAIL=1 bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway check accepted an inactive service' >&2
  exit 1
fi
if env "${gateway_common[@]}" DISABLED_UNIT=aimili-xui-protocol-transaction.timer bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway check accepted a disabled protocol timer' >&2
  exit 1
fi
cp "$fixture/protocol.timer" "$fixture/protocol.timer.good"
printf '\n# drift\n' >> "$fixture/protocol.timer"
if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway check accepted a drifted protocol timer unit' >&2
  exit 1
fi
mv "$fixture/protocol.timer.good" "$fixture/protocol.timer"

if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://user@192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway origin userinfo accepted' >&2
  exit 1
fi
if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin $'https://192.168.1.20\n:8080' >/dev/null 2>&1; then
  echo 'Gateway origin control character accepted' >&2
  exit 1
fi
if gateway_origin_error="$(env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin $'https://192.168.1.20:8080\n' 2>&1)"; then
  echo 'Gateway origin trailing control character accepted' >&2
  exit 1
fi
grep -q 'public_origin_invalid' <<< "$gateway_origin_error" || { echo 'Gateway origin control character was rejected only after origin validation' >&2; exit 1; }

printf 'sentinel\n' > "$fixture/unit-sentinel"
printf 'not a hardened unit\n' > "$fixture/bad-unit"
if env "${gateway_common[@]}" AIMILI_GATEWAY_UNIT="$fixture/bad-unit" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit-sentinel" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --manifest "$fixture/deployment.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'bad gateway unit was accepted' >&2
  exit 1
fi
grep -qx 'sentinel' "$fixture/unit-sentinel"

python3 "$repo_root/deploy/local-vm/tests/rotate_xui_account_test.py"

echo 'PASS native installer hardening fixture'
