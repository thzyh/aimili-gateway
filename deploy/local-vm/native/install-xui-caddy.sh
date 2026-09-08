#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
manifest=''
allowed_source=''
public_origin=''
xui_binary=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --manifest) manifest="$2"; shift 2 ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    --public-origin) public_origin="$2"; shift 2 ;;
    --binary) xui_binary="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$manifest" = /* && -s "$manifest" ]] || { printf 'deployment_manifest_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys; ip=ipaddress.ip_address(sys.argv[1]); raise SystemExit(0 if ip.version==4 else 1)' "$allowed_source" >/dev/null 2>&1 || { printf 'allowed_source_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys,urllib.parse; raw=sys.argv[1]; u=urllib.parse.urlsplit(raw); ip=ipaddress.ip_address(u.hostname or ""); nets=tuple(map(ipaddress.ip_network,("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"))); raise SystemExit(0 if u.scheme=="https" and not u.username and not u.password and not any(ord(c)<32 or ord(c)==127 for c in raw) and u.path in ("", "/") and not u.query and not u.fragment and u.port==8080 and ip.version==4 and any(ip in n for n in nets) else 1)' "$public_origin" || { printf 'public_origin_invalid\n' >&2; exit 3; }
os_release="${AIMILI_OS_RELEASE:-/etc/os-release}"
[[ -f "$os_release" ]] || { printf 'os_release_missing\n' >&2; exit 3; }
. "$os_release"
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == '24.04' ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ "$(uname -m)" == x86_64 ]] || { printf 'unsupported_architecture\n' >&2; exit 3; }
for command in curl sha256sum systemctl python3 install tar ufw ss cmp update-ca-certificates openssl; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
free_kb="$(df -Pk / | awk 'NR==2 {print $4}')"
[[ "$free_kb" =~ ^[0-9]+$ && "$free_kb" -ge 524288 ]] || { printf 'disk_floor_rejected\n' >&2; exit 3; }

if [[ "${AIMILI_SKIP_NETWORK_PREFLIGHT:-0}" != 1 ]]; then
  network_probe="$(dirname "$0")/guest-network-preflight.sh"
  [[ -f "$network_probe" ]] || { printf 'network_preflight_missing\n' >&2; exit 3; }
  bash "$network_probe" --json >/dev/null || { printf 'network_preflight_failed\n' >&2; exit 4; }
fi

[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }
if [[ -n "$xui_binary" ]]; then
  [[ "$xui_binary" = /* && -f "$xui_binary" && -x "$xui_binary" ]] || { printf 'xui_custom_binary_invalid\n' >&2; exit 5; }
  grep -a -q 'inboundAliases' "$xui_binary" || { printf 'xui_alias_api_missing\n' >&2; exit 5; }
fi

if [[ "$mode" == '--apply' ]] && ! command -v caddy >/dev/null 2>&1; then
  command -v apt-get >/dev/null 2>&1 || { printf 'dependency_missing:apt-get\n' >&2; exit 3; }
  apt-get update -qq
  apt-get install -y -qq caddy
fi
command -v caddy >/dev/null 2>&1 || { printf 'dependency_missing:caddy_after_install\n' >&2; exit 3; }

readonly XUI_VERSION='v3.7.0'
readonly XUI_COMMIT='f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
readonly XUI_ASSET_SHA256='0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5'
xui_root="${AIMILI_XUI_ROOT:-/usr/local/x-ui}"
xui_db="${AIMILI_XUI_DB:-/etc/x-ui/x-ui.db}"
credentials_file="${AIMILI_XUI_CREDENTIALS:-/etc/aimili-local/xui-credentials.json}"
caddy_file="${AIMILI_CADDYFILE:-/etc/caddy/Caddyfile}"
firewall_state="${AIMILI_CADDY_FIREWALL_STATE:-/etc/aimili-local/caddy-firewall.json}"
version_state="${AIMILI_XUI_VERSION_STATE:-/etc/aimili-local/xui-install.json}"
caddy_root_certificate="${AIMILI_CADDY_ROOT_CERTIFICATE:-/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt}"
caddy_trust_certificate="${AIMILI_CADDY_TRUST_CERTIFICATE:-/usr/local/share/ca-certificates/aimili-local-caddy.crt}"
system_ca_bundle="${AIMILI_SYSTEM_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}"
helper="${AIMILI_XUI_HELPER:-$(dirname "$0")/rotate-xui-account.py}"
unit_source="${AIMILI_XUI_UNIT_SOURCE:-$(dirname "$0")/x-ui.service.debian}"
unit_dest="${AIMILI_XUI_UNIT_DEST:-/etc/systemd/system/x-ui.service}"
readonly XUI_UNIT_SHA256='513f84fd2be16e3eec41e61acdd72c32cc639715eb4105e6dd9dc5ff190e5aec'
[[ -s "$unit_source" ]] || { printf 'xui_unit_missing\n' >&2; exit 5; }
[[ "$(sha256sum "$unit_source" | awk '{print $1}')" == "$XUI_UNIT_SHA256" ]] || { printf 'xui_unit_digest_mismatch\n' >&2; exit 5; }
grep -q '^ExecStart=/usr/local/x-ui/x-ui$' "$unit_source" || { printf 'xui_unit_invalid\n' >&2; exit 5; }
desired_firewall="$(mktemp /var/tmp/aimili-firewall-policy.XXXXXX)"
python3 - "$manifest" "$allowed_source" "$desired_firewall" <<'PY'
import json,os,sys
manifest,source,target=sys.argv[1:]; document=json.load(open(manifest)); slots=int(document['expected']['exitSlots'])
if slots < 0: raise SystemExit(1)
public_ports=[8443]+list(range(20000,20000+slots))
mixed_ports=[31000]+list(range(30000,30000+slots))
policy={'allowedSource':source,'rules':[{'port':8080,'proto':'tcp'}]+[{'port':port,'proto':proto} for port in public_ports for proto in ('tcp','udp')]+[{'port':port,'proto':'tcp'} for port in mixed_ports]}
with open(target,'w',encoding='utf-8') as handle: json.dump(policy,handle,separators=(',',':'))
os.chmod(target,0o600)
PY
desired_caddy="$(mktemp /var/tmp/aimili-caddy-config.XXXXXX)"
cat > "$desired_caddy" <<CADDY
$public_origin {
    tls internal
    @xui path /xui/*
    handle @xui {
        reverse_proxy 127.0.0.1:2001
    }
    @subscription path /sub/*
    handle @subscription {
        reverse_proxy 127.0.0.1:2096
    }
    @vpngate path /vpngate/*
    handle @vpngate {
        reverse_proxy 127.0.0.1:8787
    }
    handle {
        reverse_proxy 127.0.0.1:9080
    }
}

https://reality.aimili.test:443 {
    bind 127.0.0.1
    tls internal
    respond 204
}
CADDY
firewall_policy_matches() {
  local expected_policy="$1" managed_policy="$2" status_file result
  status_file="$(mktemp /var/tmp/aimili-firewall-status.XXXXXX)" || return 1
  if ! LC_ALL=C ufw status > "$status_file"; then
    rm -f -- "$status_file"
    return 1
  fi
  if python3 - "$expected_policy" "$managed_policy" "$status_file" <<'PY'
import json,re,sys
expected=json.load(open(sys.argv[1])); managed=json.load(open(sys.argv[2]))
source=str(expected['allowedSource'])
wanted={(int(rule['port']),str(rule['proto'])) for rule in expected['rules']}
managed_rules=wanted | {(int(rule['port']),str(rule['proto'])) for rule in managed['rules']}
lines=open(sys.argv[3],encoding='utf-8').read().splitlines()
if not lines or lines[0].strip().lower() != 'status: active': raise SystemExit(1)
actual={}
for line in lines[1:]:
    match=re.match(r'^\s*(\d+)/(tcp|udp)(?: \(v6\))?\s+ALLOW(?: IN)?\s+(.+?)\s*$',line)
    if not match: continue
    rule=(int(match.group(1)),match.group(2))
    if rule not in managed_rules: continue
    if rule not in wanted or match.group(3) != source: raise SystemExit(1)
    actual[rule]=actual.get(rule,0)+1
if set(actual) != wanted or any(count != 1 for count in actual.values()): raise SystemExit(1)
PY
  then result=0; else result=1; fi
  rm -f -- "$status_file" || result=1
  return "$result"
}
if [[ "$mode" == '--check' ]]; then
  trap 'rm -f -- "$desired_firewall" "$desired_caddy"' EXIT
  [[ -x "$xui_root/x-ui" && -x "$xui_root/bin/xray-linux-amd64" && -s "$xui_db" && -s "$caddy_file" && -s "$version_state" ]] || { printf 'installed_files_missing\n' >&2; exit 5; }
  grep -a -q 'inboundAliases' "$xui_root/x-ui" || { printf 'xui_alias_api_missing\n' >&2; exit 5; }
  if [[ -n "$xui_binary" ]]; then cmp -s "$xui_binary" "$xui_root/x-ui" || { printf 'xui_custom_binary_mismatch\n' >&2; exit 5; }; fi
  python3 - "$version_state" "$xui_root/x-ui" "$xui_root/bin/xray-linux-amd64" "$XUI_VERSION" "$XUI_COMMIT" <<'PY' || { printf 'xui_version_state_invalid\n' >&2; exit 5; }
import hashlib,json,sys
state=json.load(open(sys.argv[1])); paths=sys.argv[2:4]
if state.get('version')!=sys.argv[4] or state.get('commit')!=sys.argv[5]: raise SystemExit(1)
if state.get('xuiSha256')!=hashlib.sha256(open(paths[0],'rb').read()).hexdigest() or state.get('xraySha256')!=hashlib.sha256(open(paths[1],'rb').read()).hexdigest(): raise SystemExit(1)
PY
  python3 - "$xui_db" <<'PY' || { printf 'xui_subscription_listener_setting_invalid\n' >&2; exit 5; }
import sqlite3, sys
with sqlite3.connect(f'file:{sys.argv[1]}?mode=ro', uri=True) as connection:
    rows = connection.execute("SELECT value FROM settings WHERE key='subListen'").fetchall()
if rows != [('127.0.0.1',)]: raise SystemExit(1)
PY
  python3 - "$firewall_state" "$desired_firewall" <<'PY' || { printf 'firewall_state_invalid\n' >&2; exit 5; }
import json,os,stat,sys
actual=json.load(open(sys.argv[1])); expected=json.load(open(sys.argv[2]))
state=os.stat(sys.argv[1])
if actual != expected or state.st_uid != 0 or state.st_gid != 0 or stat.S_IMODE(state.st_mode) != 0o600: raise SystemExit(1)
PY
  firewall_policy_matches "$desired_firewall" "$desired_firewall" || { printf 'firewall_rules_invalid\n' >&2; exit 5; }
  cmp -s "$desired_caddy" "$caddy_file" || { printf 'caddy_config_mismatch\n' >&2; exit 5; }
  [[ -s "$caddy_root_certificate" && -s "$caddy_trust_certificate" && -s "$system_ca_bundle" ]] || { printf 'caddy_ca_trust_missing\n' >&2; exit 5; }
  cmp -s "$caddy_root_certificate" "$caddy_trust_certificate" || { printf 'caddy_ca_trust_mismatch\n' >&2; exit 5; }
  openssl verify -CAfile "$system_ca_bundle" "$caddy_root_certificate" >/dev/null || { printf 'caddy_ca_trust_invalid\n' >&2; exit 5; }
  command -v caddy >/dev/null 2>&1 || { printf 'dependency_missing:caddy\n' >&2; exit 5; }
  systemctl is-active --quiet x-ui.service || { printf 'xui_service_inactive\n' >&2; exit 5; }
  systemctl is-active --quiet caddy.service || { printf 'caddy_service_inactive\n' >&2; exit 5; }
  caddy validate --config "$caddy_file" >/dev/null || { printf 'caddy_config_invalid\n' >&2; exit 5; }
  ss -ltnH | awk '$4 == "127.0.0.1:2001" {panel=1} $4 == "127.0.0.1:2096" {subscription=1} END {exit(panel && subscription ? 0 : 1)}' || { printf 'xui_listeners_not_loopback\n' >&2; exit 5; }
  curl -fsS --connect-timeout 1 --max-time 2 http://127.0.0.1:2001/xui/csrf-token | python3 -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if d.get("success") is True and isinstance(d.get("obj"),str) and d["obj"] else 1)' || { printf 'xui_health_failed\n' >&2; exit 5; }
  printf '%s\n' '{"mode":"check","publicOriginValid":true,"xuiVersion":"v3.7.0"}'
  exit 0
fi
work_dir="$(mktemp -d /var/tmp/aimili-xui-install.XXXXXX)"
previous_firewall="$work_dir/firewall-state.previous"
previous_policy="$work_dir/firewall-policy.previous"
had_previous_firewall=0
if [[ -s "$firewall_state" ]]; then
  cp "$firewall_state" "$previous_firewall"
  cp "$firewall_state" "$previous_policy"
  had_previous_firewall=1
else
  python3 - "$desired_firewall" "$previous_policy" <<'PY'
import json,os,sys
source=json.load(open(sys.argv[1]))['allowedSource']
with open(sys.argv[2],'w',encoding='utf-8') as handle: json.dump({'allowedSource':source,'rules':[]},handle,separators=(',',':'))
os.chmod(sys.argv[2],0o600)
PY
fi
policy_rules() {
  python3 - "$1" <<'PY'
import ipaddress,json,sys
d=json.load(open(sys.argv[1])); source=str(d.get('allowedSource','')); ipaddress.ip_address(source)
rules=d.get('rules')
if rules is None: rules=[{'port':8080,'proto':'tcp'}]
seen=set()
for rule in rules:
    port=int(rule['port']); proto=str(rule['proto'])
    if not 0 < port <= 65535 or proto not in ('tcp','udp') or (port,proto) in seen: raise SystemExit(1)
    seen.add((port,proto)); print(f'{source}\t{port}\t{proto}')
PY
}
desired_rules=()
if ! desired_output="$(policy_rules "$desired_firewall" 2>/dev/null)"; then
  printf 'firewall_state_invalid\n' >&2
  rm -rf -- "$work_dir"
  rm -f -- "$desired_firewall" "$desired_caddy"
  exit 5
fi
if [[ -n "$desired_output" ]]; then mapfile -t desired_rules <<< "$desired_output"; fi
previous_rules=()
if [[ -s "$previous_firewall" ]]; then
  if ! previous_output="$(policy_rules "$previous_firewall" 2>/dev/null)"; then
    printf 'firewall_state_invalid\n' >&2
    rm -rf -- "$work_dir"
    rm -f -- "$desired_firewall" "$desired_caddy"
    exit 5
  fi
  if [[ -n "$previous_output" ]]; then mapfile -t previous_rules <<< "$previous_output"; fi
fi
firewall_changed=0
restore_firewall() {
  local failed=0
  for rule in "${desired_rules[@]}"; do
    IFS=$'\t' read -r source port proto <<< "$rule"
    ufw delete allow from "$source" to any port "$port" proto "$proto" >/dev/null 2>&1 || failed=1
  done
  for rule in "${previous_rules[@]}"; do
    IFS=$'\t' read -r source port proto <<< "$rule"
    ufw allow from "$source" to any port "$port" proto "$proto" >/dev/null 2>&1 || failed=1
  done
  ufw --force reload >/dev/null 2>&1 || failed=1
  if [[ "$failed" == 0 ]] && firewall_policy_matches "$previous_policy" "$desired_firewall"; then
    if [[ "$had_previous_firewall" == 1 ]]; then
      install -D -o root -g root -m 0600 "$previous_firewall" "$firewall_state" || failed=1
    else
      rm -f -- "$firewall_state" || failed=1
    fi
    [[ "$failed" == 0 ]] && return 0
  fi

  local fallback_failed=0
  for rule in "${previous_rules[@]}"; do IFS=$'\t' read -r source port proto <<< "$rule"; ufw delete allow from "$source" to any port "$port" proto "$proto" >/dev/null 2>&1 || fallback_failed=1; done
  for rule in "${desired_rules[@]}"; do IFS=$'\t' read -r source port proto <<< "$rule"; ufw delete allow from "$source" to any port "$port" proto "$proto" >/dev/null 2>&1 || fallback_failed=1; done
  for rule in "${desired_rules[@]}"; do IFS=$'\t' read -r source port proto <<< "$rule"; ufw allow from "$source" to any port "$port" proto "$proto" >/dev/null 2>&1 || fallback_failed=1; done
  ufw --force reload >/dev/null 2>&1 || fallback_failed=1
  local fallback_verified=0
  if firewall_policy_matches "$desired_firewall" "$previous_policy"; then
    if install -D -o root -g root -m 0600 "$desired_firewall" "$firewall_state"; then fallback_verified=1; else fallback_failed=1; fi
  else fallback_failed=1; fi
  if [[ "$fallback_verified" == 0 ]]; then
    local unrecovered="$work_dir/firewall-state.unrecovered"
    printf '%s\n' '{"schemaVersion":1,"status":"unrecovered"}' > "$unrecovered" || return 1
    install -D -o root -g root -m 0600 "$unrecovered" "$firewall_state" || return 1
  fi
  [[ "$fallback_failed" == 0 ]] || printf 'firewall_fallback_command_failed\n' >&2
  return 1
}
cleanup_install() {
  local status=$?
  trap - EXIT
  if [[ "$firewall_changed" == 1 ]] && ! restore_firewall; then status=6; fi
  rm -rf -- "$work_dir"
  rm -f -- "$desired_firewall" "$desired_caddy"
  exit "$status"
}
trap cleanup_install EXIT
install -d -m 0700 "$(dirname "$credentials_file")"
install -d -o root -g root -m 0755 "$(dirname "$caddy_file")"
installed_version=''
if [[ -x "$xui_root/x-ui" ]]; then installed_version="$("$xui_root/x-ui" version 2>&1 || true)"; fi
if [[ "$installed_version" != *"$XUI_VERSION"* ]]; then
  asset="$work_dir/x-ui-linux-amd64.tar.gz"
  curl -fsSL --connect-timeout 10 --retry 3 --retry-delay 2 --retry-all-errors --continue-at - --max-time 600 "https://github.com/MHSanaei/3x-ui/releases/download/${XUI_VERSION}/x-ui-linux-amd64.tar.gz" -o "$asset"
  [[ "$(sha256sum "$asset" | awk '{print $1}')" == "$XUI_ASSET_SHA256" ]] || { printf 'xui_asset_digest_mismatch\n' >&2; exit 5; }
  tar -xzf "$asset" -C "$work_dir"
  [[ -x "$work_dir/x-ui/x-ui" && -x "$work_dir/x-ui/bin/xray-linux-amd64" ]] || { printf 'xui_asset_invalid\n' >&2; exit 5; }
  rm -rf -- "$xui_root"; mv "$work_dir/x-ui" "$xui_root"
fi
[[ -x "$xui_root/x-ui" && -x "$xui_root/bin/xray-linux-amd64" ]] || { printf 'xui_binary_missing\n' >&2; exit 5; }
if [[ -n "$xui_binary" ]]; then
  xui_tmp="$xui_root/.x-ui.aimili.$$"
  install -o root -g root -m 0755 "$xui_binary" "$xui_tmp"
  mv -f -- "$xui_tmp" "$xui_root/x-ui"
fi
grep -a -q 'inboundAliases' "$xui_root/x-ui" || { printf 'xui_alias_api_missing\n' >&2; exit 5; }
python3 - "$version_state" "$xui_root/x-ui" "$xui_root/bin/xray-linux-amd64" "$XUI_VERSION" "$XUI_COMMIT" <<'PY'
import hashlib,json,os,sys
target,xui,xray,version,commit=sys.argv[1:]; os.makedirs(os.path.dirname(target),mode=0o700,exist_ok=True); temporary=target+'.tmp'
data={'version':version,'commit':commit,'xuiSha256':hashlib.sha256(open(xui,'rb').read()).hexdigest(),'xraySha256':hashlib.sha256(open(xray,'rb').read()).hexdigest()}
with open(temporary,'w',encoding='utf-8') as handle: json.dump(data,handle,separators=(',',':'))
os.chmod(temporary,0o600); os.replace(temporary,target)
PY
chown root:root "$version_state"
if [[ -s "$credentials_file" ]]; then
  chown root:root "$credentials_file"
fi
if [[ -s "$credentials_file" ]]; then
  if ! python3 - "$credentials_file" <<'PY'
import json, os, stat, sys
p=sys.argv[1]; d=json.load(open(p))
st=os.stat(p)
if st.st_uid != 0 or st.st_gid != 0 or set(d) != {'username','password'} or not isinstance(d['username'],str) or not isinstance(d['password'],str) or not d['username'] or not d['password'] or stat.S_IMODE(st.st_mode) != 0o600:
    raise SystemExit(1)
PY
  then printf 'xui_credentials_invalid\n' >&2; exit 5; fi
fi
pending="${AIMILI_XUI_PENDING:-${credentials_file}.pending}"
if [[ ! -s "$credentials_file" ]]; then
  if [[ ! -s "$pending" ]]; then
    username="$(python3 -c 'import secrets; print("aimili" + secrets.token_hex(5))')"
    password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
    python3 -c 'import json,os,sys; p=sys.argv[1]; d=json.load(sys.stdin); t=p+".tmp"; json.dump(d,open(t,"w"),separators=(",",":")); os.chmod(t,0o600); os.replace(t,p)' "$pending" <<EOF
{"baseUrl":"http://127.0.0.1:2001/xui/","oldUsername":"admin","oldPassword":"admin","newUsername":"$username","newPassword":"$password"}
EOF
  fi
  python3 - "$pending" <<'PY'
import json, os, stat, sys
p=sys.argv[1]; d=json.load(open(p)); st=os.stat(p)
if st.st_uid != 0 or st.st_gid != 0 or stat.S_IMODE(st.st_mode) != 0o600 or set(d) != {'baseUrl','oldUsername','oldPassword','newUsername','newPassword'}:
    raise SystemExit('xui_pending_invalid')
PY
fi
"$xui_root/x-ui" setting -port 2001 -webBasePath /xui/ -listenIP 127.0.0.1 >/dev/null 2>&1
python3 - "$xui_db" <<'PY'
import os, sqlite3, sys
path = sys.argv[1]
if not os.path.isfile(path): raise SystemExit('xui_database_missing_after_setting')
with sqlite3.connect(path) as connection:
    table = connection.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='settings'").fetchone()
    if table is None: raise SystemExit('xui_settings_table_missing')
    connection.execute("DELETE FROM settings WHERE key='subListen'")
    connection.execute("INSERT INTO settings(key,value) VALUES('subListen','127.0.0.1')")
PY
unit_tmp="${unit_dest}.tmp.$$"
install -D -o root -g root -m 0644 "$unit_source" "$unit_tmp"
mv -f "$unit_tmp" "$unit_dest"
systemctl daemon-reload >/dev/null
systemctl enable x-ui.service >/dev/null
systemctl restart x-ui.service
ready=0
for _ in $(seq 1 30); do
  if curl -fsS --connect-timeout 1 --max-time 2 http://127.0.0.1:2001/xui/csrf-token | python3 -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if d.get("success") is True and isinstance(d.get("obj"),str) and d["obj"] else 1)'; then ready=1; break; fi
  sleep 1
done
[[ "$ready" == 1 ]] || { printf 'xui_readiness_failed\n' >&2; exit 5; }
ss -ltnH | awk '$4 == "127.0.0.1:2001" {found=1} END {exit(found ? 0 : 1)}' || { printf 'xui_listener_not_loopback\n' >&2; exit 5; }
if [[ ! -s "$credentials_file" ]]; then
  python3 "$helper" < "$pending"
  python3 - "$pending" "$credentials_file" <<'PY'
import json, os, sys
pending, path = sys.argv[1:]; d=json.load(open(pending)); t=path+'.tmp'
with open(t,'w',encoding='utf-8') as h: json.dump({'username':d['newUsername'],'password':d['newPassword']},h,separators=(',',':'))
os.chmod(t,0o600); os.chown(t,0,0); os.replace(t,path)
PY
  rm -f -- "$pending"
fi
chown root:root "$credentials_file"
install -o root -g root -m 0644 "$desired_caddy" "$caddy_file"
if ! cmp -s "$previous_firewall" "$desired_firewall"; then
  firewall_changed=1
  for rule in "${previous_rules[@]}"; do IFS=$'\t' read -r source port proto <<< "$rule"; ufw delete allow from "$source" to any port "$port" proto "$proto" >/dev/null; done
  for rule in "${desired_rules[@]}"; do IFS=$'\t' read -r source port proto <<< "$rule"; ufw allow from "$source" to any port "$port" proto "$proto" >/dev/null; done
  install -D -o root -g root -m 0600 "$desired_firewall" "$firewall_state"
fi
ufw --force reload >/dev/null
systemctl enable caddy.service >/dev/null
caddy validate --config "$caddy_file" >/dev/null
systemctl restart caddy.service
root_ready=0
for _ in $(seq 1 30); do
  if [[ -s "$caddy_root_certificate" ]]; then root_ready=1; break; fi
  sleep 1
done
[[ "$root_ready" == 1 ]] || { printf 'caddy_root_certificate_missing\n' >&2; exit 5; }
install -D -o root -g root -m 0644 "$caddy_root_certificate" "$caddy_trust_certificate"
update-ca-certificates --fresh >/dev/null
[[ -s "$system_ca_bundle" ]] || { printf 'system_ca_bundle_missing\n' >&2; exit 5; }
openssl verify -CAfile "$system_ca_bundle" "$caddy_root_certificate" >/dev/null || { printf 'caddy_ca_trust_invalid\n' >&2; exit 5; }
firewall_changed=0
printf 'xui_caddy_apply_ok\n'
