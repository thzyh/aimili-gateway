#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
verify="$repo_root/deploy/local-vm/native/verify-native.sh"
enable="$repo_root/deploy/local-vm/native/enable-exits.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

fake_bin="$fixture/bin"
mkdir -p "$fake_bin"

cat > "$fixture/deployment.json" <<'JSON'
{"schemaVersion":1,"services":["aimilivpn.service","x-ui.service","aimili-gateway.service","caddy.service"],"expected":{"openvpn":4,"xray":1,"logicalExits":4,"exitSlots":3},"ports":{"aimilivpnUi":8787,"aimilivpnProxy":7928,"control":8790,"xuiPanel":2001,"xuiSubscription":2096,"gateway":9080,"caddy":8080},"sourceCommit":"fixture"}
JSON
cat > "$fixture/slots.json" <<'JSON'
{"updated_at":1,"desired_count":3,"slots":[{"slot":0,"device":"tun120","port":17928,"status":"up","egress_ok":true},{"slot":1,"device":"tun121","port":17929,"status":"ready","egress_ok":true},{"slot":2,"device":"tun122","port":17930,"status":"up","egress_ok":true}]}
JSON
cat > "$fixture/ui_auth.json" <<'JSON'
{"secret_path":"fixture-secret-never-print"}
JSON
python3 - "$fixture/gateway.db" <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    db.execute('create table fixture (id integer)')
PY

cat > "$fake_bin/systemctl" <<'SH'
#!/usr/bin/env bash
[[ "${1:-}" == is-active || "${1:-}" == is-enabled ]] || exit 1
[[ "${1:-}" != is-enabled || "${3:-}" != "${DISABLED_UNIT:-}" ]] || exit 1
exit 0
SH
cat > "$fake_bin/pgrep" <<'SH'
#!/usr/bin/env bash
case "$*" in
  *openvpn*) printf '%s\n' 4 ;;
  *xray-linux-amd64*) printf '%s\n' 1 ;;
  *) printf '%s\n' 0 ;;
esac
SH
cat > "$fake_bin/ss" <<'SH'
#!/usr/bin/env bash
if [[ -n "${FAIL_LISTENER_PORT:-}" ]]; then omitted=":$FAIL_LISTENER_PORT "; else omitted='never'; fi
for port in 8787 7928 8790 2001 2096 9080 8080 17928 17929 17930; do
  [[ ":$port " == "$omitted" ]] || printf 'LISTEN 0 128 127.0.0.1:%s 0.0.0.0:*\n' "$port"
done
SH
cat > "$fake_bin/ip" <<'SH'
#!/usr/bin/env bash
if [[ "${1:-}" == link && "${2:-}" == show ]]; then
  [[ "${3:-}" != "${FAIL_DEVICE:-}" ]] || exit 1
  printf '1: %s: <UP>\n' "${3:-unknown}"
  exit 0
fi
if [[ "${1:-}" == route && "${2:-}" == show && "${3:-}" == table ]]; then
  [[ "${4:-}" != "${FAIL_ROUTE_TABLE:-}" ]] || exit 0
  printf 'default dev fixture\n'
  exit 0
fi
exit 1
SH
cat > "$fake_bin/curl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CURL_ARGS_LOG"
if [[ "$*" == *'--config -'* ]]; then
  config="$(cat)"
  printf '%s\n' "$config" >> "$CURL_CONFIG_LOG"
  printf '%s\n' '{"ok":true}'
else
  printf '%s\n' '192.0.2.10'
fi
SH
chmod 0700 "$fake_bin"/*

common_env=(
  PATH="$fake_bin:/usr/bin:/bin"
  AIMILI_SLOTS_FILE="$fixture/slots.json"
  AIMILI_GATEWAY_DB="$fixture/gateway.db"
  AIMILI_UI_AUTH_FILE="$fixture/ui_auth.json"
  AIMILI_SLOT_WAIT_ATTEMPTS=1
  AIMILI_SLOT_WAIT_INTERVAL=0
  CURL_ARGS_LOG="$fixture/curl.args.log"
  CURL_CONFIG_LOG="$fixture/curl.config.log"
)

report="$(env "${common_env[@]}" bash "$verify" --manifest "$fixture/deployment.json")"
python3 - "$report" <<'PY'
import json, sys
r = json.loads(sys.argv[1])
assert r['nativeReady'] is True, r
assert all(r['nativeEnabled'].values()), r
assert all(r['listeners'].values()), r
assert [s['slot'] for s in r['slotChecks']] == [0, 1, 2], r
assert all(s['ready'] and s['tun'] and s['route'] and s['listener'] and s['egress'] for s in r['slotChecks']), r
PY

env "${common_env[@]}" bash "$enable" --slot 0 --manifest "$fixture/deployment.json"
if env "${common_env[@]}" bash "$enable" --slot 3 --manifest "$fixture/deployment.json" 2>/dev/null; then
  printf '%s\n' 'undeclared zero-based slot was accepted' >&2
  exit 1
fi
if env "${common_env[@]}" FAIL_ROUTE_TABLE=200 bash "$enable" --slot 0 --manifest "$fixture/deployment.json" >/dev/null 2>&1; then
  printf '%s\n' 'slot activation accepted a missing slot route' >&2
  exit 1
fi
! grep -q 'fixture-secret-never-print' "$fixture/curl.args.log" || { printf '%s\n' 'secret path exposed in curl process arguments' >&2; exit 1; }

if env "${common_env[@]}" DISABLED_UNIT=caddy.service bash "$verify" --manifest "$fixture/deployment.json" >/dev/null 2>&1; then
  printf '%s\n' 'verification accepted a disabled service' >&2
  exit 1
fi
if env "${common_env[@]}" FAIL_LISTENER_PORT=9080 bash "$verify" --manifest "$fixture/deployment.json" >/dev/null 2>&1; then
  printf '%s\n' 'verification accepted a missing manifest listener' >&2
  exit 1
fi
if env "${common_env[@]}" FAIL_ROUTE_TABLE=201 bash "$verify" --manifest "$fixture/deployment.json" >/dev/null 2>&1; then
  printf '%s\n' 'verification accepted a missing per-slot route' >&2
  exit 1
fi

python3 - "$fixture/slots.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d['slots'][1]['status'] = 'active'
json.dump(d, open(p, 'w'))
PY
if env "${common_env[@]}" bash "$verify" --manifest "$fixture/deployment.json" >/dev/null 2>&1; then
  printf '%s\n' 'verification accepted a non-ready slot status' >&2
  exit 1
fi

printf '%s\n' 'PASS native runtime behavior fixture'
