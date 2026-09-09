#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
verify="$repo_root/deploy/local-vm/native/verify-native.sh"
enable="$repo_root/deploy/local-vm/native/enable-exits.sh"
generate="$repo_root/deploy/local-vm/native/generate-native-evidence.py"
fixture="$(mktemp -d)"
subscription_pid=''
cleanup() {
  if [[ -n "$subscription_pid" ]]; then kill "$subscription_pid" >/dev/null 2>&1 || true; wait "$subscription_pid" 2>/dev/null || true; fi
  rm -rf -- "$fixture"
}
trap cleanup EXIT
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
with sqlite3.connect(sys.argv[1]) as db:
    db.executescript('''
      create table main_egress(resource_name text, public_port integer, mixed_port integer, enabled integer);
      create table proxy_groups(resource_name text, aimili_slot integer, public_port integer, mixed_port integer, status text);
      create table egress_protocol_modes(egress_id text, active_mode text, state text);
      create table gateway_subscription(id integer, resource_name text, client_id integer, subscription_id text, updated_at integer);
      insert into main_egress values('agw-main',8443,31000,1);
      insert into proxy_groups values('agw-slot-0',0,20000,31001,'ready'),('agw-slot-1',1,20001,31002,'ready'),('agw-slot-2',2,20002,31003,'ready');
      insert into egress_protocol_modes values('agw-main','vless_tcp_reality_vision','ready'),('agw-slot-0','vless_xhttp_reality','ready'),('agw-slot-1','hysteria2_quic_tls','ready'),('agw-slot-2','vless_tcp_reality_vision','ready');
      insert into gateway_subscription values(1,'aimili-gateway-subscription',42,'stable-sub',0);
    ''')
PY
python3 - "$fixture/xui.db" <<'PY'
import json, sqlite3, sys
rows = [
    (8443, 'vless', {'clients':[{'id':'00000000-0000-0000-0000-000000000001','email':'aimili-gateway-subscription','flow':'xtls-rprx-vision','enable':True}]}, {'network':'tcp','security':'reality','realitySettings':{'serverNames':['reality.aimili.test'],'shortIds':['abcd'],'settings':{'publicKey':'fixture-public'}}}),
    (20000, 'vless', {'clients':[{'id':'00000000-0000-0000-0000-000000000001','email':'aimili-gateway-subscription','flow':'','enable':True}]}, {'network':'xhttp','security':'reality','xhttpSettings':{'path':'/safe-xhttp'},'realitySettings':{'serverNames':['reality.aimili.test'],'shortIds':['abcd'],'settings':{'publicKey':'fixture-public'}}}),
    (20001, 'hysteria', {'version':2,'clients':[{'auth':'fixture-secret','email':'aimili-gateway-subscription','enable':True}]}, {'network':'hysteria','security':'tls','tlsSettings':{'serverName':'192.0.2.20'}}),
    (20002, 'vless', {'clients':[{'id':'00000000-0000-0000-0000-000000000001','email':'aimili-gateway-subscription','flow':'xtls-rprx-vision','enable':True}]}, {'network':'tcp','security':'reality','realitySettings':{'serverNames':['reality.aimili.test'],'shortIds':['abcd'],'settings':{'publicKey':'fixture-public'}}}),
    (31000, 'mixed', {}, {}), (31001, 'mixed', {}, {}), (31002, 'mixed', {}, {}), (31003, 'mixed', {}, {}),
]
with sqlite3.connect(sys.argv[1]) as db:
    db.execute('create table inbounds(port integer, protocol text, settings text, stream_settings text, enable integer)')
    db.executemany('insert into inbounds values(?,?,?,?,1)', [(p, protocol, json.dumps(settings), json.dumps(stream)) for p,protocol,settings,stream in rows])
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted database declarations without a live subscription' >&2
  exit 1
fi
subscription_port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
python3 - "$fixture/deployment.json" "$subscription_port" <<'PY'
import json,sys
p,port=sys.argv[1:]; d=json.load(open(p)); d['ports']['xuiSubscription']=int(port); json.dump(d,open(p,'w'))
PY
mkdir -p "$fixture/http/sub"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64, pathlib, sys
entries = [
    'vless://00000000-0000-0000-0000-000000000001@192.0.2.20:8443?flow=xtls-rprx-vision&fp=chrome&pbk=fixture-public&security=reality&sid=abcd&sni=reality.aimili.test&spx=%2F&type=tcp#main',
    'vless://00000000-0000-0000-0000-000000000001@192.0.2.20:20000?extra=%7B%22mode%22%3A%22auto%22%7D&fp=chrome&host=&mode=auto&path=%2Fsafe-xhttp&pbk=fixture-public&security=reality&sid=abcd&sni=reality.aimili.test&spx=%2F&type=xhttp#slot-0',
    'hysteria2://fixture-secret@192.0.2.20:20001?alpn=h3&security=tls&sni=192.0.2.20#slot-1',
    'vless://00000000-0000-0000-0000-000000000001@192.0.2.20:20002?flow=xtls-rprx-vision&fp=chrome&pbk=fixture-public&security=reality&sid=abcd&sni=reality.aimili.test&type=tcp#slot-2',
]
pathlib.Path(sys.argv[1]).write_bytes(base64.urlsafe_b64encode(('\n'.join(entries) + '\n').encode()))
PY
cat > "$fixture/subscription-server.py" <<'PY'
import http.server
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
port = int(sys.argv[2])

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/sub/stable-sub':
            self.send_error(404)
            return
        payload = (root / 'sub' / 'stable-sub').read_bytes()
        if self.headers.get('Host', '').split(':', 1)[0] != '192.0.2.20':
            import base64
            text = base64.urlsafe_b64decode(payload).decode().replace('@192.0.2.20:', '@localhost:')
            payload = base64.urlsafe_b64encode(text.encode())
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_):
        pass

http.server.ThreadingHTTPServer(('127.0.0.1', port), Handler).serve_forever()
PY
python3 "$fixture/subscription-server.py" "$fixture/http" "$subscription_port" >/dev/null 2>&1 &
subscription_pid=$!
for _ in $(seq 1 50); do
  python3 - "$subscription_port" <<'PY' >/dev/null 2>&1 && break
import sys, urllib.request
urllib.request.urlopen('http://127.0.0.1:' + sys.argv[1] + '/sub/stable-sub', timeout=.2).read(1)
PY
  sleep .1
done
python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON'
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
test "$(stat -c '%a' "$fixture/evidence.json")" = 600
if grep -Eq 'stable-sub|fixture-secret|fixture-public|safe-xhttp|00000000-0000-0000-0000-000000000001|abcd' "$fixture/evidence.json"; then
  echo 'native evidence exposed subscription or protocol credentials' >&2
  exit 1
fi
cp "$fixture/http/sub/stable-sub" "$fixture/http/sub/stable-sub.good"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64,pathlib,sys
p=pathlib.Path(sys.argv[1]); text=base64.urlsafe_b64decode(p.read_bytes()).decode(); text=text.replace('00000000-0000-0000-0000-000000000001@192.0.2.20:20002','00000000-0000-0000-0000-000000000099@192.0.2.20:20002'); p.write_bytes(base64.urlsafe_b64encode(text.encode()))
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted subscription identity drift from x-ui runtime' >&2
  exit 1
fi
mv "$fixture/http/sub/stable-sub.good" "$fixture/http/sub/stable-sub"
cp "$fixture/http/sub/stable-sub" "$fixture/http/sub/stable-sub.good"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64,pathlib,sys
p=pathlib.Path(sys.argv[1]); text=base64.urlsafe_b64decode(p.read_bytes()).decode(); text=text.replace('&type=tcp#slot-2','&unexpected=1&type=tcp#slot-2'); p.write_bytes(base64.urlsafe_b64encode(text.encode()))
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted an unknown VLESS query field' >&2
  exit 1
fi
mv "$fixture/http/sub/stable-sub.good" "$fixture/http/sub/stable-sub"
cp "$fixture/http/sub/stable-sub" "$fixture/http/sub/stable-sub.good"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64,pathlib,sys
p=pathlib.Path(sys.argv[1]); text=base64.urlsafe_b64decode(p.read_bytes()).decode(); text=text.replace('&sni=192.0.2.20#slot-1','&sni=192.0.2.20&unexpected=1#slot-1'); p.write_bytes(base64.urlsafe_b64encode(text.encode()))
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted an unknown Hysteria2 query field' >&2
  exit 1
fi
mv "$fixture/http/sub/stable-sub.good" "$fixture/http/sub/stable-sub"
cp "$fixture/http/sub/stable-sub" "$fixture/http/sub/stable-sub.good"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64,pathlib,sys
p=pathlib.Path(sys.argv[1]); text=base64.urlsafe_b64decode(p.read_bytes()).decode(); text=text.replace('&type=xhttp#slot-0','&flow=xtls-rprx-vision&type=xhttp#slot-0'); p.write_bytes(base64.urlsafe_b64encode(text.encode()))
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted Vision flow on an XHTTP subscription entry' >&2
  exit 1
fi
mv "$fixture/http/sub/stable-sub.good" "$fixture/http/sub/stable-sub"
cp "$fixture/http/sub/stable-sub" "$fixture/http/sub/stable-sub.good"
python3 - "$fixture/http/sub/stable-sub" <<'PY'
import base64,pathlib,sys
p=pathlib.Path(sys.argv[1]); text=base64.urlsafe_b64decode(p.read_bytes()).decode(); text=text.replace('00000000-0000-0000-0000-000000000001@192.0.2.20:20002','00000000-0000-0000-0000-000000000001:ignored@192.0.2.20:20002'); p.write_bytes(base64.urlsafe_b64encode(text.encode()))
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted subscription userinfo with a password component' >&2
  exit 1
fi
mv "$fixture/http/sub/stable-sub.good" "$fixture/http/sub/stable-sub"
python3 - "$fixture/xui.db" <<'PY'
import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db: db.execute("update inbounds set protocol='vless' where port=31003")
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted a non-mixed runtime inbound on a mixed port' >&2
  exit 1
fi
python3 - "$fixture/xui.db" <<'PY'
import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db: db.execute("update inbounds set protocol='mixed' where port=31003")
PY
python3 - "$fixture/xui.db" <<'PY'
import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db: db.execute('update inbounds set enable=0 where port=20002')
PY
if python3 "$generate" --manifest "$fixture/deployment.json" --gateway-db "$fixture/gateway.db" --xui-db "$fixture/xui.db" --public-host 192.0.2.20 --output "$fixture/evidence.json" <<'JSON' >/dev/null 2>&1
{"before":{"clientPids":[10,20],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"after":{"clientPids":[20,10],"proxy":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","defaultRoute":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
JSON
then
  echo 'evidence generator accepted a disabled public runtime inbound' >&2
  exit 1
fi
python3 - "$fixture/xui.db" <<'PY'
import sqlite3,sys
with sqlite3.connect(sys.argv[1]) as db: db.execute('update inbounds set enable=1 where port=20002')
PY

cat > "$fake_bin/systemctl" <<'SH'
#!/usr/bin/env bash
[[ "${1:-}" == is-active || "${1:-}" == is-enabled ]] || exit 1
[[ "${1:-}" != is-active || "${3:-}" != "${INACTIVE_UNIT:-}" ]] || exit 1
[[ "${1:-}" != is-enabled || "${3:-}" != "${DISABLED_UNIT:-}" ]] || exit 1
SH
cat > "$fake_bin/pgrep" <<'SH'
#!/usr/bin/env bash
case "$*" in *openvpn*) echo 4 ;; *xray-linux-amd64*) echo 1 ;; *) echo 0 ;; esac
SH
cat > "$fake_bin/ps" <<'SH'
#!/usr/bin/env bash
printf '%s\n' 'bin/xray-linux-amd64 -c bin/config.json'
SH
cat > "$fake_bin/ss" <<'SH'
#!/usr/bin/env bash
if [[ "$*" == *u* ]]; then ports=(20001); else ports=(8787 7928 8790 2001 "${SUBSCRIPTION_PORT:-2096}" 9080 8080 17928 17929 17930 8443 20000 20002 31000 31001 31002 31003); fi
for port in "${ports[@]}"; do
  [[ "$port" != "${FAIL_LISTENER_PORT:-}" ]] || continue
  host=127.0.0.1
  case "$port" in 8080|8443|20000|20001|20002|31000|31001|31002|31003) host=0.0.0.0 ;; esac
  [[ "$port" != "${NON_LOOPBACK_PORT:-}" ]] || host=0.0.0.0
  [[ "$port" != "${LOOPBACK_PUBLIC_PORT:-}" ]] || host=127.0.0.1
  printf 'LISTEN 0 128 %s:%s 0.0.0.0:*\n' "$host" "$port"
done
SH
cat > "$fake_bin/ip" <<'SH'
#!/usr/bin/env bash
if [[ "$*" == *'link show'* ]]; then
  device="${@: -1}"
  if [[ "$device" == "${DOWN_DEVICE:-}" ]]; then
    flags="${DOWN_LINK_FLAGS:-POINTOPOINT,LOWER_UP}"
  else
    flags="${LINK_FLAGS:-POINTOPOINT,UP,LOWER_UP}"
  fi
  echo "1: $device: <$flags> mtu 1500 state UNKNOWN"
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
if [[ "$*" == *'--config -'* ]]; then cat >> "$CURL_CONFIG_LOG"; exit 99; fi
port="$(sed -n 's/.*127\.0\.0\.1:\([0-9][0-9]*\).*/\1/p' <<< "$*" | head -n1)"
[[ "$port" != "${FAIL_EGRESS_PORT:-}" ]] || exit 1
if [[ "$port" == "${NON_IP_EGRESS_PORT:-}" ]]; then echo '<html>'; else echo '192.0.2.10'; fi
SH
chmod 0700 "$fake_bin"/*
protocol_fixture="$fixture/protocol"
mkdir -p "$protocol_fixture"
for name in wrapper script.py config.json protocol.path protocol.service protocol.timer; do
  printf 'fixture\n' > "$protocol_fixture/$name"
done
chmod 0700 "$protocol_fixture/wrapper"

common_env=(PATH="$fake_bin:/usr/bin:/bin" SUBSCRIPTION_PORT="$subscription_port" AIMILI_SLOTS_FILE="$fixture/slots.json" AIMILI_GATEWAY_DB="$fixture/gateway.db" AIMILI_UI_AUTH_FILE="$fixture/ui_auth.json" AIMILI_PROTOCOL_WRAPPER="$protocol_fixture/wrapper" AIMILI_PROTOCOL_SCRIPT="$protocol_fixture/script.py" AIMILI_PROTOCOL_CONFIG="$protocol_fixture/config.json" AIMILI_PROTOCOL_PATH_UNIT="$protocol_fixture/protocol.path" AIMILI_PROTOCOL_SERVICE_UNIT="$protocol_fixture/protocol.service" AIMILI_PROTOCOL_TIMER_UNIT="$protocol_fixture/protocol.timer" AIMILI_SLOT_WAIT_ATTEMPTS=1 AIMILI_SLOT_WAIT_INTERVAL=0 CURL_ARGS_LOG="$fixture/curl.args.log" CURL_CONFIG_LOG="$fixture/curl.config.log")
verify_args=(--json --manifest "$fixture/deployment.json" --evidence "$fixture/evidence.json")
report="$(env "${common_env[@]}" bash "$verify" "${verify_args[@]}")"
python3 - "$report" <<'PY'
import json, sys
r=json.loads(sys.argv[1]); assert r['nativeReady'] is True and r['evidenceSchema'] is True, r
assert r['subscriptionExitSet'] and r['protocolIsolation'] and r['hostSafety'], r
assert all(r['nativeEnabled'].values()) and all(r['listeners'].values()), r
assert all(r['protocolAutomation'].values()), r
assert all(s['ready'] and s['tun'] and s['route'] and s['listener'] and s['egress'] for s in r['slotChecks']), r
PY

env "${common_env[@]}" LINK_FLAGS='POINTOPOINT,UP,LOWER_UP' bash "$enable" --slot 0 --manifest "$fixture/deployment.json"
env "${common_env[@]}" LINK_FLAGS='UP' bash "$enable" --slot 0 --manifest "$fixture/deployment.json"
if env "${common_env[@]}" bash "$enable" --slot 3 --manifest "$fixture/deployment.json" 2>/dev/null; then echo 'undeclared slot accepted' >&2; exit 1; fi
! grep -q 'fixture-secret-never-print' "$fixture/curl.args.log" || { echo 'secret exposed in argv' >&2; exit 1; }

expect_verify_failure() { if env "${common_env[@]}" "$@" bash "$verify" "${verify_args[@]}" >/dev/null 2>&1; then echo "verification accepted failure: $*" >&2; exit 1; fi; }
expect_enable_failure() { if env "${common_env[@]}" "$@" bash "$enable" --slot 1 --manifest "$fixture/deployment.json" >/dev/null 2>&1; then echo "slot activation accepted failure: $*" >&2; exit 1; fi; }
expect_verify_failure DISABLED_UNIT=caddy.service
expect_verify_failure INACTIVE_UNIT=aimili-xui-protocol-transaction.path
expect_verify_failure DISABLED_UNIT=aimili-xui-protocol-transaction.timer
expect_verify_failure AIMILI_PROTOCOL_WRAPPER="$fixture/missing-protocol-wrapper"
expect_verify_failure FAIL_LISTENER_PORT=9080
expect_verify_failure FAIL_LISTENER_PORT=20002
expect_verify_failure FAIL_LISTENER_PORT=20001
expect_verify_failure FAIL_LISTENER_PORT=31003
expect_verify_failure LOOPBACK_PUBLIC_PORT=20002
expect_verify_failure LOOPBACK_PUBLIC_PORT=8080
expect_verify_failure LOOPBACK_PUBLIC_PORT=31003
expect_verify_failure DOWN_DEVICE=tun0
expect_verify_failure DOWN_DEVICE=tun121
expect_verify_failure WRONG_ROUTE_TABLE=201
expect_verify_failure FAIL_LISTENER_PORT=17929
expect_verify_failure NON_LOOPBACK_PORT=17929
expect_verify_failure FAIL_EGRESS_PORT=17929
expect_verify_failure NON_IP_EGRESS_PORT=17929
expect_enable_failure DOWN_DEVICE=tun121
expect_enable_failure DOWN_DEVICE=tun121 DOWN_LINK_FLAGS=POINTOPOINT,LOWER_UP
expect_enable_failure DOWN_DEVICE=tun121 DOWN_LINK_FLAGS=SETUP
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
