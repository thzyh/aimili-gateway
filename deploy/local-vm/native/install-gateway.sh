#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
binary=''
admin_binary=''
config_template=''
allowed_source=''
public_origin=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --binary) binary="$2"; shift 2 ;;
    --admin-binary) admin_binary="$2"; shift 2 ;;
    --config-template) config_template="$2"; shift 2 ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    --public-origin) public_origin="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$binary" = /* && -x "$binary" ]] || { printf 'gateway_binary_invalid\n' >&2; exit 3; }
[[ "$admin_binary" = /* && -x "$admin_binary" ]] || { printf 'gateway_admin_binary_invalid\n' >&2; exit 3; }
[[ "$config_template" = /* && -s "$config_template" ]] || { printf 'gateway_config_template_invalid\n' >&2; exit 3; }
[[ "$allowed_source" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { printf 'allowed_source_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys,urllib.parse; u=urllib.parse.urlparse(sys.argv[1]); ip=ipaddress.ip_address(u.hostname or ""); nets=tuple(map(ipaddress.ip_network,("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"))); raise SystemExit(0 if u.scheme=="https" and u.path in ("", "/") and not u.query and not u.fragment and u.port==8080 and ip.version==4 and any(ip in n for n in nets) else 1)' "$public_origin" || { printf 'public_origin_invalid\n' >&2; exit 3; }
for command in python3 systemctl install systemd-creds; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
if [[ "$mode" == '--check' ]]; then
  printf '%s\n' '{"mode":"check","publicOriginValid":true,"masterKeyBytes":32}'
  exit 0
fi
[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }

etc_root="${AIMILI_GATEWAY_ETC:-/etc/aimili-gateway}"
state_root="${AIMILI_GATEWAY_STATE:-/var/lib/aimili-gateway}"
credstore="${AIMILI_CREDSTORE:-/etc/credstore.encrypted}"
config_path="${AIMILI_GATEWAY_CONFIG:-$etc_root/config.json}"
db_path="${AIMILI_GATEWAY_DB:-$state_root/aimili-gateway.db}"
unit_source="${AIMILI_GATEWAY_UNIT:-$(dirname "$0")/../../systemd/aimili-gateway.service}"
unit_dest="${AIMILI_GATEWAY_UNIT_DEST:-/etc/systemd/system/aimili-gateway.service}"
bin_dir="${AIMILI_GATEWAY_BIN_DIR:-/usr/local/bin}"
if ! id -u aimili-gateway >/dev/null 2>&1; then
  useradd --system --home-dir "$state_root" --shell /usr/sbin/nologin aimili-gateway
fi
install -d -o root -g root -m 0750 "$etc_root" "$state_root" "$credstore"
install -d -o aimili-gateway -g aimili-gateway -m 0700 "$state_root"
install -d -m 0755 "$bin_dir"
install -m 0755 "$binary" "$bin_dir/aimili-gateway"
install -m 0755 "$admin_binary" "$bin_dir/aimili-gateway-admin"
control_source="${AIMILI_CONTROL_TOKEN_SOURCE:-/etc/aimilivpn/control.token}"
install -m 0600 "$control_source" "$etc_root/aimili-control-token"
xui_source="${AIMILI_XUI_CREDENTIALS:-/etc/aimili-local/xui-credentials.json}"
install -m 0600 "$xui_source" "$etc_root/xui-automation.json"
raw_key=''; init_config=''; admin_input=''
cleanup() { rm -f -- "$raw_key" "$init_config" "$admin_input" /tmp/aimili-gateway-master-init; }
trap cleanup EXIT
encrypted="$credstore/aimili-gateway-master-key"
if [[ ! -s "$encrypted" ]]; then
  raw_key="$(mktemp /tmp/aimili-gateway-master.XXXXXX)"
  head -c 32 /dev/urandom > "$raw_key"
  systemd-creds encrypt --name=gateway-master-key "$raw_key" "$encrypted" >/dev/null
  chmod 0600 "$encrypted"; rm -f -- "$raw_key"; raw_key=''
fi
python3 - "$config_template" "$config_path" "$public_origin" "$db_path" "$allowed_source" <<'PY'
import json, sys
source = sys.argv[3]
with open(sys.argv[1], encoding='utf-8') as handle:
    cfg = json.load(handle)
cfg.update({
    'listenAddress': '127.0.0.1:9080',
    'publicOrigin': sys.argv[3],
    'databasePath': sys.argv[4],
    'masterKeyFile': '/run/credentials/aimili-gateway.service/gateway-master-key',
    'aimiliControlTokenFile': '/run/credentials/aimili-gateway.service/aimili-control-token',
    'xuiCredentialsFile': '/run/credentials/aimili-gateway.service/xui-automation',
    'xuiBaseUrl': 'http://127.0.0.1:2001/xui/',
    'mixedSourceCidrs': [sys.argv[5] + '/32'],
    'expertModeUrl': '/xui/',
    'aimiliBackendUrl': '/vpngate/',
})
with open(sys.argv[2] + '.tmp', 'w', encoding='utf-8') as handle:
    json.dump(cfg, handle, ensure_ascii=False, indent=2)
import os
os.chmod(sys.argv[2] + '.tmp', 0o640); os.replace(sys.argv[2] + '.tmp', sys.argv[2])
PY
chown root:aimili-gateway "$config_path"
if [[ ! -s "$db_path" ]]; then
init_config="$(mktemp /tmp/aimili-gateway-init.XXXXXX.json)"
cp "$config_path" "$init_config"; chmod 0600 "$init_config"
head -c 32 /dev/urandom > /tmp/aimili-gateway-master-init; chmod 0600 /tmp/aimili-gateway-master-init
python3 - "$init_config" <<'PY'
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d['masterKeyFile']='/tmp/aimili-gateway-master-init'; json.dump(d,open(p,'w'))
PY
username="$(python3 -c 'import secrets; print("local" + secrets.token_hex(4))')"
password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
admin_input="${AIMILI_GATEWAY_INIT_INPUT:-$(mktemp /tmp/aimili-gateway-admin.XXXXXX)}"
printf '%s\n%s\n%s\n' "$username" "$password" "$password" > "$admin_input"; chmod 0600 "$admin_input"
runuser -u aimili-gateway -- env GATEWAY_CONFIG="$init_config" < "$admin_input" "$bin_dir/aimili-gateway-admin" init >/dev/null
python3 - "$etc_root/admin-credentials.json" "$username" "$password" <<'PY'
import json,os,sys
p,u,pw=sys.argv[1:]; t=p+'.tmp'; json.dump({'username':u,'password':pw},open(t,'w'),separators=(',',':')); os.chmod(t,0o600); os.replace(t,p)
PY
rm -f -- /tmp/aimili-gateway-master-init
fi
install -m 0644 "$unit_source" "$unit_dest"
grep -q '^LoadCredentialEncrypted=gateway-master-key:' "$unit_source" || { printf 'gateway_unit_not_hardened\n' >&2; exit 5; }
systemctl daemon-reload
systemctl enable --now aimili-gateway.service >/dev/null
printf 'gateway_apply_ok\n'
