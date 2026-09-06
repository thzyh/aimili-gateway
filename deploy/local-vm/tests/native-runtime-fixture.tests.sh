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
{"slots":[{"slot":0,"device":"tun120","port":17928,"status":"up","egress_ok":true},{"slot":1,"device":"tun121","port":17929,"status":"ready","egress_ok":true},{"slot":2,"device":"tun122","port":17930,"status":"up","egress_ok":true}]}
JSON
cat > "$fixture/ui_auth.json" <<'JSON'
{"secret_path":"fixture-secret-never-print"}
JSON
cat > "$fixture/evidence.json" <<'JSON'
{"schemaVersion":1,"subscription":{"entries":[{"exit":"main","port":8443,"protocol":"vless_tcp_reality_vision"},{"exit":"slot-0","port":20000,"protocol":"vless_xhttp_reality"},{"exit":"slot-1","port":20001,"protocol":"hysteria2_quic_tls"},{"exit":"slot-2","port":20002,"protocol":"vless_tcp_reality_vision"}]},"protocolIsolation":{"exits":[{"exit":"main","publicPort":8443,"publicProtocol":"vless_tcp_reality_vision","mixedPort":31000,"mixedProtocol":"socks5h","mixedInSubscription":false},{"exit":"slot-0","publicPort":20000,"publicProtocol":"vless_xhttp_reality","mixedPort":31001,"mixedProtocol":"socks5h","mixedInSubscription":false},{"exit":"slot-1","publicPort":20001,"publicProtocol":"hysteria2_quic_tls","mixedPort":31002,"mixedProtocol":"socks5h","mixedInSubscription":false},{"exit":"slot-2","publicPort":20002,"publicProtocol":"vless_tcp_reality_vision","mixedPort":31003,"mixedProtocol":"socks5h","mixedInSubscription":false}]},"hostSafety":{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}
JSON
python3 - "$fixture/gateway.db" <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db: db.execute('create table fixture (id integer)')
PY

cat > "$fake_bin/systemctl" <<'SH'
#!/usr/bin/env bash
[[ "${1:-}" == is-active || "${1:-}" == is-enabled ]] || exit 1
[[ "${1:-}" != is-enabled || "${3:-}" != "${DISABLED_UNIT:-}" ]] || exit 1
SH
cat > "$fake_bin/pgrep" <<'SH'
#!/usr/bin/env bash
case "$*" in *openvpn*) echo 4 ;; *xray-linux-amd64*) echo 1 ;; *) echo 0 ;; esac
SH
cat > "$fake_bin/ss" <<'SH'
#!/usr/bin/env bash
for port in 8787 7928 8790 2001 2096 9080 8080 17928 17929 17930; do
  [[ "$port" != "${FAIL_LISTENER_PORT:-}" ]] || continue
  host=127.0.0.1; [[ "$port" != 8080 ]] || host=0.0.0.0; [[ "$port" != "${NON_LOOPBACK_PORT:-}" ]] || host=0.0.0.0
  printf 'LISTEN 0 128 %s:%s 0.0.0.0:*\n' "$host" "$port"
done
SH
cat > "$fake_bin/ip" <<'SH'
#!/usr/bin/env bash
if [[ "$*" == *'link show'* ]]; then
  device="${@: -1}"
  if [[ "$device" == "${DOWN_DEVICE:-}" ]]; then echo "1: $device: <POINTOPOINT> mtu 1500 state DOWN"; else echo "1: $device: <POINTOPOINT,UP,LOWER_UP> mtu 1500 state UNKNOWN"; fi
  exit 0
fi
if [[ "${1:-}" == route && "${2:-}" == show && "${3:-}" == table ]]; then
  table="${4:-}"; [[ "$table" != "${FAIL_ROUTE_TABLE:-}" ]] || exit 0
  case "$table" in 100) device=tun0 ;; 200) device=tun120 ;; 201) device=tun121 ;; 202) device=tun122 ;; *) device=wrong0 ;; esac
  [[ "$table" != "${WRONG_ROUTE_TABLE:-}" ]] || device=wrong0
  echo "default dev $device"; exit 0
fi
exit 1
SH
cat > "$fake_bin/curl" <<'SH'
#!/usr/bin/env bash
echo "$*" >> "$CURL_ARGS_LOG"
if [[ "$*" == *'--config -'* ]]; then cat >> "$CURL_CONFIG_LOG"; echo '{"ok":true}'; exit 0; fi
port="$(sed -n 's/.*127\.0\.0\.1:\([0-9][0-9]*\).*/\1/p' <<< "$*" | head -n1)"
[[ "$port" != "${FAIL_EGRESS_PORT:-}" ]] || exit 1
if [[ "$port" == "${NON_IP_EGRESS_PORT:-}" ]]; then echo '<html>'; else echo '192.0.2.10'; fi
SH
chmod 0700 "$fake_bin"/*

common_env=(PATH="$fake_bin:/usr/bin:/bin" AIMILI_SLOTS_FILE="$fixture/slots.json" AIMILI_GATEWAY_DB="$fixture/gateway.db" AIMILI_UI_AUTH_FILE="$fixture/ui_auth.json" AIMILI_SLOT_WAIT_ATTEMPTS=1 AIMILI_SLOT_WAIT_INTERVAL=0 CURL_ARGS_LOG="$fixture/curl.args.log" CURL_CONFIG_LOG="$fixture/curl.config.log")
verify_args=(--json --manifest "$fixture/deployment.json" --evidence "$fixture/evidence.json")
report="$(env "${common_env[@]}" bash "$verify" "${verify_args[@]}")"
python3 - "$report" <<'PY'
import json, sys
r=json.loads(sys.argv[1]); assert r['nativeReady'] is True and r['evidenceSchema'] is True, r
assert r['subscriptionExitSet'] and r['protocolIsolation'] and r['hostSafety'], r
assert all(r['nativeEnabled'].values()) and all(r['listeners'].values()), r
assert all(s['ready'] and s['tun'] and s['route'] and s['listener'] and s['egress'] for s in r['slotChecks']), r
PY

env "${common_env[@]}" bash "$enable" --slot 0 --manifest "$fixture/deployment.json"
if env "${common_env[@]}" bash "$enable" --slot 3 --manifest "$fixture/deployment.json" 2>/dev/null; then echo 'undeclared slot accepted' >&2; exit 1; fi
! grep -q 'fixture-secret-never-print' "$fixture/curl.args.log" || { echo 'secret exposed in argv' >&2; exit 1; }

expect_verify_failure() { if env "${common_env[@]}" "$@" bash "$verify" "${verify_args[@]}" >/dev/null 2>&1; then echo "verification accepted failure: $*" >&2; exit 1; fi; }
expect_enable_failure() { if env "${common_env[@]}" "$@" bash "$enable" --slot 1 --manifest "$fixture/deployment.json" >/dev/null 2>&1; then echo "slot activation accepted failure: $*" >&2; exit 1; fi; }
expect_verify_failure DISABLED_UNIT=caddy.service
expect_verify_failure FAIL_LISTENER_PORT=9080
expect_verify_failure DOWN_DEVICE=tun0
expect_verify_failure DOWN_DEVICE=tun121
expect_verify_failure WRONG_ROUTE_TABLE=201
expect_verify_failure FAIL_LISTENER_PORT=17929
expect_verify_failure NON_LOOPBACK_PORT=17929
expect_verify_failure FAIL_EGRESS_PORT=17929
expect_verify_failure NON_IP_EGRESS_PORT=17929
expect_enable_failure DOWN_DEVICE=tun121
expect_enable_failure WRONG_ROUTE_TABLE=201
expect_enable_failure FAIL_LISTENER_PORT=17929
expect_enable_failure NON_LOOPBACK_PORT=17929
expect_enable_failure FAIL_EGRESS_PORT=17929
expect_enable_failure NON_IP_EGRESS_PORT=17929

python3 - "$fixture/evidence.json" <<'PY'
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d['subscription']['entries'].pop(); json.dump(d,open(p,'w'))
PY
expect_verify_failure
python3 - "$fixture/evidence.json" <<'PY'
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d['subscription']['entries'].append({'exit':'slot-2','port':20002,'protocol':'vless_tcp_reality_vision'}); d['protocolIsolation']['exits'][0]['mixedInSubscription']=True; json.dump(d,open(p,'w'))
PY
expect_verify_failure
python3 - "$fixture/evidence.json" <<'PY'
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d['protocolIsolation']['exits'][0]['mixedInSubscription']=False; d['hostSafety']['after']['proxy']='c'*64; json.dump(d,open(p,'w'))
PY
expect_verify_failure

if env "${common_env[@]}" bash "$verify" --json --manifest "$fixture/deployment.json" --evidence 2>/dev/null; then echo 'missing evidence value accepted' >&2; exit 1; fi
if env "${common_env[@]}" bash "$verify" --json --manifest "$fixture/deployment.json" --evidence "$fixture/evidence.json" --unknown 2>/dev/null; then echo 'unknown argument accepted' >&2; exit 1; fi
echo 'PASS native runtime, control evidence, and negative fixtures'
