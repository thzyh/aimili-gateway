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
printf '#!/bin/sh\nif [ "${1:-}" = setting ]; then\n  printf "xui-setting\\n" >> "$TRACE_LOG"\n  [ "${FAIL_SETTING:-0}" = 1 ] && exit 9\nfi\nexit 0\n' > "$fixture/x-ui/x-ui"
chmod 0700 "$fixture/x-ui/x-ui"
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
if [ "${1:-}" = enable ] && [ "${2:-}" = --now ] && [ "${3:-}" = x-ui.service ]; then
  test -s "$XUI_UNIT_DEST" || exit 42
  touch "$XUI_STARTED"
  printf 'xui-start\n' >> "$TRACE_LOG"
fi
exit 0
SH
cat > "$fake_bin/apt-get" <<'SH'
#!/bin/sh
if [ "${1:-}" = install ]; then
  cat > "$FAKE_BIN/caddy" <<'CADDY'
#!/bin/sh
printf 'caddy %s\n' "$*" >> "$CADDY_LOG"
exit 0
CADDY
  chmod 0700 "$FAKE_BIN/caddy"
fi
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
cat > "$fake_bin/ufw" <<'SH'
#!/bin/sh
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
for command_name in bash sha256sum systemctl install chmod mkdir cp mv rm df awk uname dirname mktemp sed grep stat curl tar ufw apt-get id chown ss; do
  [ -e "$fake_bin/$command_name" ] || ln -s "$(command -v "$command_name")" "$fake_bin/$command_name"
done
cat > "$fixture/template.json" <<'JSON'
{"publicOrigin":"https://example.invalid:8080","databasePath":"old.db"}
JSON

common=(PATH="$fake_bin:/usr/bin:/bin" FAKE_BIN="$fake_bin" AIMILI_SKIP_NETWORK_PREFLIGHT=1 AIMILI_OS_RELEASE="$fixture/os-release" AIMILI_XUI_ROOT="$fixture/x-ui" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_XUI_HELPER="$fake_bin/aimili-xui-helper" AIMILI_XUI_UNIT_SOURCE="$fixture/x-ui.service.debian" AIMILI_XUI_UNIT_DEST="$fixture/x-ui.service.installed" XUI_HELPER_INPUT="$fixture/xui-helper.json" XUI_STARTED="$fixture/xui-started" XUI_UNIT_DEST="$fixture/x-ui.service.installed" TRACE_LOG="$fixture/trace.log" PYTHON_ARGS_LOG="$fixture/python-args.log" CADDY_LOG="$fixture/caddy.log" CHOWN_LOG="$fixture/chown.log" SYSTEMCTL_LOG="$fixture/systemctl.log")
env "${common[@]}" bash "$xui" --apply --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
test -s "$fixture/x-ui.service.installed"
test "$(grep -n '^xui-setting$' "$fixture/trace.log" | cut -d: -f1)" -lt "$(grep -n '^xui-start$' "$fixture/trace.log" | cut -d: -f1)"
test "$(grep -n '^xui-start$' "$fixture/trace.log" | cut -d: -f1)" -lt "$(grep -n '^helper$' "$fixture/trace.log" | cut -d: -f1)"
python3 - "$fixture/xui-credentials.json" "$fixture/python-args.log" <<'PY'
import json, os, stat, sys
credentials = json.load(open(sys.argv[1]))
assert set(credentials) == {'username', 'password'}
assert stat.S_IMODE(os.stat(sys.argv[1]).st_mode) == 0o600
args = open(sys.argv[2]).read()
assert credentials['username'] not in args
assert credentials['password'] not in args
PY

chown 65534:65534 "$fixture/xui-credentials.json"
env "${common[@]}" bash "$xui" --apply --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
test "$(stat -c '%u:%g' "$fixture/xui-credentials.json")" = '0:0'

if env "${common[@]}" FAIL_SETTING=1 AIMILI_XUI_CREDENTIALS="$fixture/setting-failure.json" bash "$xui" --apply --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui setting failure was ignored' >&2
  exit 1
fi
test ! -e "$fixture/setting-failure.json"

if env "${common[@]}" bash "$xui" --check --allowed-source 192.0.2.10 --public-origin https://user@192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'x-ui origin userinfo accepted' >&2
  exit 1
fi

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
gateway_common=(PATH="$fake_bin:/usr/bin:/bin" AIMILI_GATEWAY_BIN_DIR="$fixture/bin-install" AIMILI_CONTROL_TOKEN_SOURCE="$fixture/control.token" AIMILI_XUI_CREDENTIALS="$fixture/xui-credentials.json" AIMILI_GATEWAY_ETC="$fixture/gateway-etc" AIMILI_GATEWAY_STATE="$fixture/gateway-state" AIMILI_GATEWAY_UNIT="$repo_root/deploy/systemd/aimili-gateway.service" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit" AIMILI_CREDSTORE="$fixture/credstore" AIMILI_GATEWAY_CONFIG="$fixture/config.json" AIMILI_GATEWAY_INIT_INPUT="$fixture/admin-input" AIMILI_GATEWAY_DB="$fixture/gateway.db" ADMIN_INPUT_CAPTURE="$fixture/admin-capture" ADMIN_KEY_CAPTURE="$fixture/admin-key" ADMIN_DB="$fixture/gateway.db" SYSTEMD_CRED_SOURCE_CAPTURE="$fixture/systemd-key" PYTHON_ARGS_LOG="$fixture/python-args.log" CHOWN_LOG="$fixture/chown.log" SYSTEMCTL_LOG="$fixture/systemctl.log" TRACE_LOG="$fixture/trace.log")
env "${gateway_common[@]}" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080
cmp "$fixture/credstore/aimili-gateway-master-key" "$fixture/admin-key"
python3 - "$fixture/gateway-etc/admin-credentials.json" "$fixture/python-args.log" <<'PY'
import json, sys
credentials = json.load(open(sys.argv[1]))
args = open(sys.argv[2]).read()
assert credentials['username'] not in args
assert credentials['password'] not in args
PY

if env "${gateway_common[@]}" bash "$gateway" --check --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --allowed-source 192.0.2.10 --public-origin https://user@192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'Gateway origin userinfo accepted' >&2
  exit 1
fi

printf 'sentinel\n' > "$fixture/unit-sentinel"
printf 'not a hardened unit\n' > "$fixture/bad-unit"
if env "${gateway_common[@]}" AIMILI_GATEWAY_UNIT="$fixture/bad-unit" AIMILI_GATEWAY_UNIT_DEST="$fixture/unit-sentinel" bash "$gateway" --apply --binary "$fixture/gateway" --admin-binary "$admin_bin" --config-template "$fixture/template.json" --allowed-source 192.0.2.10 --public-origin https://192.168.1.20:8080 >/dev/null 2>&1; then
  echo 'bad gateway unit was accepted' >&2
  exit 1
fi
grep -qx 'sentinel' "$fixture/unit-sentinel"

python3 "$repo_root/deploy/local-vm/tests/rotate_xui_account_test.py"

echo 'PASS native installer hardening fixture'
