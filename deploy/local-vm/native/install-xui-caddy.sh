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
python3 -c 'import ipaddress,sys,urllib.parse; u=urllib.parse.urlparse(sys.argv[1]); ip=ipaddress.ip_address(u.hostname or ""); nets=tuple(map(ipaddress.ip_network,("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"))); raise SystemExit(0 if u.scheme=="https" and u.path in ("", "/") and not u.query and not u.fragment and u.port==8080 and ip.version==4 and any(ip in n for n in nets) else 1)' "$public_origin" || { printf 'public_origin_invalid\n' >&2; exit 3; }
os_release="${AIMILI_OS_RELEASE:-/etc/os-release}"
[[ -f "$os_release" ]] || { printf 'os_release_missing\n' >&2; exit 3; }
. "$os_release"
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == '24.04' ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ "$(uname -m)" == x86_64 ]] || { printf 'unsupported_architecture\n' >&2; exit 3; }
for command in curl sha256sum systemctl python3 install tar ufw caddy; do
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
  apt-get update -qq
  apt-get install -y -qq caddy
fi

readonly XUI_VERSION='v3.7.0'
readonly XUI_COMMIT='f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
readonly XUI_ASSET_SHA256='0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5'
xui_root="${AIMILI_XUI_ROOT:-/usr/local/x-ui}"
credentials_file="${AIMILI_XUI_CREDENTIALS:-/etc/aimili-local/xui-credentials.json}"
caddy_file="${AIMILI_CADDYFILE:-/etc/caddy/Caddyfile}"
helper="${AIMILI_XUI_HELPER:-$(dirname "$0")/rotate-xui-account.py}"
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
  if ! python3 - "$credentials_file" <<'PY'
import json, os, stat, sys
p=sys.argv[1]; d=json.load(open(p))
if set(d) != {'username','password'} or not isinstance(d['username'],str) or not isinstance(d['password'],str) or not d['username'] or not d['password'] or stat.S_IMODE(os.stat(p).st_mode) != 0o600:
    raise SystemExit(1)
PY
  then printf 'xui_credentials_invalid\n' >&2; exit 5; fi
fi
if [[ ! -s "$credentials_file" ]]; then
  username="$(python3 -c 'import secrets; print("aimili" + secrets.token_hex(5))')"
  password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
  pending="$work_dir/account.json"
  python3 - "$pending" "${AIMILI_XUI_BOOTSTRAP_USERNAME:-admin}" "${AIMILI_XUI_BOOTSTRAP_PASSWORD:-admin}" "$username" "$password" <<'PY'
import json, os, sys
path, old_user, old_pass, new_user, new_pass = sys.argv[1:]
with open(path, 'w', encoding='utf-8') as h: json.dump({'baseUrl':'http://127.0.0.1:2001/xui/','oldUsername':old_user,'oldPassword':old_pass,'newUsername':new_user,'newPassword':new_pass}, h)
os.chmod(path, 0o600)
PY
  python3 "$helper" < "$pending"
  python3 - "$credentials_file" "$username" "$password" <<'PY'
import json, os, sys
path, user, password = sys.argv[1:]; tmp=path+'.tmp'
with open(tmp,'w',encoding='utf-8') as h: json.dump({'username':user,'password':password},h,separators=(',',':'))
os.chmod(tmp,0o600); os.replace(tmp,path)
PY
fi
"$xui_root/x-ui" setting -port 2001 -webBasePath /xui/ -listenIP 127.0.0.1 >/dev/null 2>&1 || true
systemctl enable --now x-ui.service >/dev/null
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
