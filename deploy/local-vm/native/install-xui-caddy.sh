#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
allowed_source=''
public_origin=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    --public-origin) public_origin="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$allowed_source" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { printf 'allowed_source_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys,urllib.parse; raw=sys.argv[1]; u=urllib.parse.urlsplit(raw); ip=ipaddress.ip_address(u.hostname or ""); nets=tuple(map(ipaddress.ip_network,("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"))); raise SystemExit(0 if u.scheme=="https" and not u.username and not u.password and "\\r" not in raw and "\\n" not in raw and u.path in ("", "/") and not u.query and not u.fragment and u.port==8080 and ip.version==4 and any(ip in n for n in nets) else 1)' "$public_origin" || { printf 'public_origin_invalid\n' >&2; exit 3; }
os_release="${AIMILI_OS_RELEASE:-/etc/os-release}"
[[ -f "$os_release" ]] || { printf 'os_release_missing\n' >&2; exit 3; }
. "$os_release"
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == '24.04' ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ "$(uname -m)" == x86_64 ]] || { printf 'unsupported_architecture\n' >&2; exit 3; }
for command in curl sha256sum systemctl python3 install tar ufw ss; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
free_kb="$(df -Pk / | awk 'NR==2 {print $4}')"
[[ "$free_kb" =~ ^[0-9]+$ && "$free_kb" -ge 524288 ]] || { printf 'disk_floor_rejected\n' >&2; exit 3; }

if [[ "${AIMILI_SKIP_NETWORK_PREFLIGHT:-0}" != 1 ]]; then
  network_probe="$(dirname "$0")/guest-network-preflight.sh"
  [[ -f "$network_probe" ]] || { printf 'network_preflight_missing\n' >&2; exit 3; }
  bash "$network_probe" --json >/dev/null || { printf 'network_preflight_failed\n' >&2; exit 4; }
fi

if [[ "$mode" == '--check' ]]; then
  printf '%s\n' '{"mode":"check","publicOriginValid":true,"xuiVersion":"v3.7.0"}'
  exit 0
fi
[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }

if ! command -v caddy >/dev/null 2>&1; then
  command -v apt-get >/dev/null 2>&1 || { printf 'dependency_missing:apt-get\n' >&2; exit 3; }
  apt-get update -qq
  apt-get install -y -qq caddy
fi
command -v caddy >/dev/null 2>&1 || { printf 'dependency_missing:caddy_after_install\n' >&2; exit 3; }

readonly XUI_VERSION='v3.7.0'
readonly XUI_COMMIT='f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
readonly XUI_ASSET_SHA256='0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5'
xui_root="${AIMILI_XUI_ROOT:-/usr/local/x-ui}"
credentials_file="${AIMILI_XUI_CREDENTIALS:-/etc/aimili-local/xui-credentials.json}"
caddy_file="${AIMILI_CADDYFILE:-/etc/caddy/Caddyfile}"
helper="${AIMILI_XUI_HELPER:-$(dirname "$0")/rotate-xui-account.py}"
unit_source="${AIMILI_XUI_UNIT_SOURCE:-$(dirname "$0")/x-ui.service.debian}"
unit_dest="${AIMILI_XUI_UNIT_DEST:-/etc/systemd/system/x-ui.service}"
readonly XUI_UNIT_SHA256='513f84fd2be16e3eec41e61acdd72c32cc639715eb4105e6dd9dc5ff190e5aec'
[[ -s "$unit_source" ]] || { printf 'xui_unit_missing\n' >&2; exit 5; }
[[ "$(sha256sum "$unit_source" | awk '{print $1}')" == "$XUI_UNIT_SHA256" ]] || { printf 'xui_unit_digest_mismatch\n' >&2; exit 5; }
grep -q '^ExecStart=/usr/local/x-ui/x-ui$' "$unit_source" || { printf 'xui_unit_invalid\n' >&2; exit 5; }
work_dir="$(mktemp -d /var/tmp/aimili-xui-install.XXXXXX)"
trap 'rm -rf -- "$work_dir"' EXIT
install -d -m 0700 "$(dirname "$credentials_file")" "$(dirname "$caddy_file")"
if [[ ! -x "$xui_root/x-ui" ]]; then
  installer="$work_dir/x-ui-install.sh"
  asset="$work_dir/x-ui-linux-amd64.tar.gz"
  curl -fsSL --connect-timeout 10 --max-time 120 "https://github.com/MHSanaei/3x-ui/releases/download/${XUI_VERSION}/x-ui-linux-amd64.tar.gz" -o "$asset"
  [[ "$(sha256sum "$asset" | awk '{print $1}')" == "$XUI_ASSET_SHA256" ]] || { printf 'xui_asset_digest_mismatch\n' >&2; exit 5; }
  tar -xzf "$asset" -C "$work_dir"
  [[ -x "$work_dir/x-ui/x-ui" && -x "$work_dir/x-ui/bin/xray-linux-amd64" ]] || { printf 'xui_asset_invalid\n' >&2; exit 5; }
  rm -rf -- "$xui_root"; mv "$work_dir/x-ui" "$xui_root"
fi
[[ -x "$xui_root/x-ui" ]] || { printf 'xui_binary_missing\n' >&2; exit 5; }
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
unit_tmp="${unit_dest}.tmp.$$"
install -D -o root -g root -m 0644 "$unit_source" "$unit_tmp"
mv -f "$unit_tmp" "$unit_dest"
systemctl daemon-reload >/dev/null
systemctl enable --now x-ui.service >/dev/null
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
cat > "$caddy_file" <<CADDY
$public_origin {
    tls internal
    @xui path /xui/*
    handle @xui { reverse_proxy 127.0.0.1:2001 }
    @vpngate path /vpngate/*
    handle @vpngate { reverse_proxy 127.0.0.1:8787 }
    handle { reverse_proxy 127.0.0.1:9080 }
}
CADDY
ufw allow from "$allowed_source" to any port 8080 proto tcp >/dev/null
ufw --force reload >/dev/null
systemctl enable caddy.service >/dev/null
caddy validate --config "$caddy_file" >/dev/null
systemctl restart caddy.service
printf 'xui_caddy_apply_ok\n'
