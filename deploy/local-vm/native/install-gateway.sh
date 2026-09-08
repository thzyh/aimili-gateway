#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
binary=''
admin_binary=''
config_template=''
manifest=''
allowed_source=''
public_origin=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --binary) binary="$2"; shift 2 ;;
    --admin-binary) admin_binary="$2"; shift 2 ;;
    --config-template) config_template="$2"; shift 2 ;;
    --manifest) manifest="$2"; shift 2 ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    --public-origin) public_origin="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$binary" = /* && -s "$binary" ]] || { printf 'gateway_binary_invalid\n' >&2; exit 3; }
[[ "$admin_binary" = /* && -s "$admin_binary" ]] || { printf 'gateway_admin_binary_invalid\n' >&2; exit 3; }
[[ "$config_template" = /* && -s "$config_template" ]] || { printf 'gateway_config_template_invalid\n' >&2; exit 3; }
[[ "$manifest" = /* && -s "$manifest" ]] || { printf 'gateway_manifest_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys; ip=ipaddress.ip_address(sys.argv[1]); raise SystemExit(0 if ip.version==4 else 1)' "$allowed_source" >/dev/null 2>&1 || { printf 'allowed_source_invalid\n' >&2; exit 3; }
python3 -c 'import ipaddress,sys,urllib.parse; raw=sys.argv[1]; u=urllib.parse.urlsplit(raw); ip=ipaddress.ip_address(u.hostname or ""); nets=tuple(map(ipaddress.ip_network,("10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"))); raise SystemExit(0 if u.scheme=="https" and not u.username and not u.password and not any(ord(c)<32 or ord(c)==127 for c in raw) and u.path in ("", "/") and not u.query and not u.fragment and u.port==8080 and ip.version==4 and any(ip in n for n in nets) else 1)' "$public_origin" || { printf 'public_origin_invalid\n' >&2; exit 3; }
for command in python3 systemctl install systemd-creds grep curl ss sha256sum cmp; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }

etc_root="${AIMILI_GATEWAY_ETC:-/etc/aimili-gateway}"
state_root="${AIMILI_GATEWAY_STATE:-/var/lib/aimili-gateway}"
credstore="${AIMILI_CREDSTORE:-/etc/credstore.encrypted}"
config_path="${AIMILI_GATEWAY_CONFIG:-$etc_root/config.json}"
db_path="${AIMILI_GATEWAY_DB:-$state_root/aimili-gateway.db}"
unit_source="${AIMILI_GATEWAY_UNIT:-$(dirname "$0")/../../systemd/aimili-gateway.service}"
unit_dest="${AIMILI_GATEWAY_UNIT_DEST:-/etc/systemd/system/aimili-gateway.service}"
bin_dir="${AIMILI_GATEWAY_BIN_DIR:-/usr/local/bin}"
account_wrapper_source="${AIMILI_ACCOUNT_WRAPPER_SOURCE:-$(dirname "$0")/../../bin/aimili-gateway-account}"
account_wrapper_dest="${AIMILI_ACCOUNT_WRAPPER_DEST:-/usr/local/sbin/aimili-gateway-account}"
protocol_wrapper_source="${AIMILI_PROTOCOL_WRAPPER_SOURCE:-$(dirname "$0")/../../bin/aimili-xui-protocol-transaction}"
protocol_script_source="${AIMILI_PROTOCOL_SCRIPT_SOURCE:-$(dirname "$0")/../../../scripts/aimili_xui_protocol_transaction.py}"
protocol_config_template="${AIMILI_PROTOCOL_CONFIG_TEMPLATE:-$(dirname "$0")/../../config/protocol-transaction.example.json}"
protocol_path_source="${AIMILI_PROTOCOL_PATH_SOURCE:-$(dirname "$0")/../../systemd/aimili-xui-protocol-transaction.path}"
protocol_service_source="${AIMILI_PROTOCOL_SERVICE_SOURCE:-$(dirname "$0")/../../systemd/aimili-xui-protocol-transaction.service}"
protocol_timer_source="${AIMILI_PROTOCOL_TIMER_SOURCE:-$(dirname "$0")/../../systemd/aimili-xui-protocol-transaction.timer}"
protocol_bin_dest="${AIMILI_PROTOCOL_BIN_DEST:-/usr/local/bin/aimili-xui-protocol-transaction}"
protocol_lib_dir="${AIMILI_PROTOCOL_LIB_DIR:-/usr/lib/aimili-gateway}"
protocol_path_dest="${AIMILI_PROTOCOL_PATH_DEST:-/etc/systemd/system/aimili-xui-protocol-transaction.path}"
protocol_service_dest="${AIMILI_PROTOCOL_SERVICE_DEST:-/etc/systemd/system/aimili-xui-protocol-transaction.service}"
protocol_timer_dest="${AIMILI_PROTOCOL_TIMER_DEST:-/etc/systemd/system/aimili-xui-protocol-transaction.timer}"
protocol_state="${AIMILI_PROTOCOL_STATE:-/var/lib/aimili-xui-protocol-transaction}"
protocol_config="${AIMILI_PROTOCOL_CONFIG:-$etc_root/protocol-transaction.json}"
[[ -s "$unit_source" ]] || { printf 'gateway_unit_missing\n' >&2; exit 5; }
[[ -s "$account_wrapper_source" ]] || { printf 'gateway_account_wrapper_missing\n' >&2; exit 5; }
grep -q '^LoadCredentialEncrypted=gateway-master-key:' "$unit_source" || { printf 'gateway_unit_not_hardened\n' >&2; exit 5; }
for source in "$protocol_wrapper_source" "$protocol_script_source" "$protocol_config_template" "$protocol_path_source" "$protocol_service_source" "$protocol_timer_source"; do
  [[ -s "$source" ]] || { printf 'protocol_asset_missing\n' >&2; exit 5; }
done
public_host="$(python3 -c 'import sys,urllib.parse; print(urllib.parse.urlsplit(sys.argv[1]).hostname or "")' "$public_origin")"
certificate="${AIMILI_PROTOCOL_CERTIFICATE:-/var/lib/caddy/.local/share/caddy/certificates/local/$public_host/$public_host.crt}"
private_key="${AIMILI_PROTOCOL_PRIVATE_KEY:-/var/lib/caddy/.local/share/caddy/certificates/local/$public_host/$public_host.key}"
encrypted="$credstore/aimili-gateway-master-key"
if [[ "$mode" == '--check' ]]; then
  for installed in "$config_path" "$unit_dest" "$encrypted" "$account_wrapper_dest" "$protocol_bin_dest" "$protocol_lib_dir/aimili_xui_protocol_transaction.py" "$protocol_config" "$protocol_path_dest" "$protocol_service_dest" "$protocol_timer_dest"; do
    [[ -s "$installed" ]] || { printf 'installed_gateway_files_missing\n' >&2; exit 5; }
  done
  [[ "$(sha256sum "$binary" | awk '{print $1}')" == "$(sha256sum "$bin_dir/aimili-gateway" | awk '{print $1}')" ]] || { printf 'gateway_binary_mismatch\n' >&2; exit 5; }
  [[ "$(sha256sum "$admin_binary" | awk '{print $1}')" == "$(sha256sum "$bin_dir/aimili-gateway-admin" | awk '{print $1}')" ]] || { printf 'gateway_admin_binary_mismatch\n' >&2; exit 5; }
  cmp -s "$unit_source" "$unit_dest" && cmp -s "$account_wrapper_source" "$account_wrapper_dest" && cmp -s "$protocol_wrapper_source" "$protocol_bin_dest" && cmp -s "$protocol_script_source" "$protocol_lib_dir/aimili_xui_protocol_transaction.py" && cmp -s "$protocol_path_source" "$protocol_path_dest" && cmp -s "$protocol_service_source" "$protocol_service_dest" && cmp -s "$protocol_timer_source" "$protocol_timer_dest" || { printf 'installed_gateway_asset_mismatch\n' >&2; exit 5; }
  python3 - "$config_path" "$protocol_config" "$manifest" "$public_origin" "$allowed_source" "$db_path" "$certificate" "$private_key" "$public_host" <<'PY'
import json, os, sys
gateway=json.load(open(sys.argv[1])); protocol=json.load(open(sys.argv[2])); manifest=json.load(open(sys.argv[3]))
slots=int(manifest['expected']['exitSlots']); expected=[8443]+list(range(20000,20000+slots))
gateway_expected={'listenAddress':'127.0.0.1:9080','publicOrigin':sys.argv[4],'databasePath':sys.argv[6],'masterKeyFile':'/run/credentials/aimili-gateway.service/gateway-master-key','aimiliControlTokenFile':'/run/credentials/aimili-gateway.service/aimili-control-token','xuiCredentialsFile':'/run/credentials/aimili-gateway.service/xui-automation','xuiBaseUrl':'http://127.0.0.1:2001/xui/','mixedSourceCidrs':[sys.argv[5]+'/32'],'expertModeUrl':'/xui/','aimiliBackendUrl':'/vpngate/','protocolRequestDir':'/var/lib/aimili-gateway/protocol-spool/requests','protocolResultDir':'/var/lib/aimili-gateway/protocol-spool/results','protocolTimeoutSeconds':180}
protocol_expected={'certificatePath':sys.argv[7],'privateKeyPath':sys.argv[8],'tlsServerName':sys.argv[9],'allowedPorts':expected,'spoolRequestDir':'/var/lib/aimili-gateway/protocol-spool/requests','spoolResultDir':'/var/lib/aimili-gateway/protocol-spool/results'}
if any(gateway.get(key)!=value for key,value in gateway_expected.items()): raise SystemExit(1)
if any(protocol.get(key)!=value for key,value in protocol_expected.items()) or not os.path.isfile(sys.argv[7]) or not os.path.isfile(sys.argv[8]): raise SystemExit(1)
PY
  systemctl is-active --quiet aimili-gateway.service || { printf 'gateway_service_inactive\n' >&2; exit 5; }
  systemctl is-active --quiet aimili-xui-protocol-transaction.path || { printf 'protocol_path_inactive\n' >&2; exit 5; }
  systemctl is-active --quiet aimili-xui-protocol-transaction.timer || { printf 'protocol_timer_inactive\n' >&2; exit 5; }
  for unit in aimili-gateway.service aimili-xui-protocol-transaction.path aimili-xui-protocol-transaction.timer; do
    systemctl is-enabled --quiet "$unit" || { printf 'gateway_units_disabled\n' >&2; exit 5; }
  done
  ss -ltnH | awk '$4 == "127.0.0.1:9080" {found=1} END {exit(found ? 0 : 1)}' || { printf 'gateway_listener_not_loopback\n' >&2; exit 5; }
  curl -fsS --connect-timeout 2 --max-time 5 http://127.0.0.1:9080/healthz >/dev/null || { printf 'gateway_health_failed\n' >&2; exit 5; }
  printf '%s\n' '{"mode":"check","publicOriginValid":true,"masterKeyBytes":32}'
  exit 0
fi
if ! id -u aimili-gateway >/dev/null 2>&1; then
  useradd --system --home-dir "$state_root" --shell /usr/sbin/nologin aimili-gateway
fi
install -d -o root -g aimili-gateway -m 0750 "$etc_root"
install -d -o root -g root -m 0750 "$state_root" "$credstore"
install -d -o aimili-gateway -g aimili-gateway -m 0700 "$state_root"
install -d -o aimili-gateway -g aimili-gateway -m 0750 "$state_root/protocol-spool"
install -d -o aimili-gateway -g aimili-gateway -m 0700 "$state_root/protocol-spool/requests"
install -d -o root -g aimili-gateway -m 0750 "$state_root/protocol-spool/results"
for path in ui update-spool/requests update-spool/results; do
  install -d -o aimili-gateway -g aimili-gateway -m 0700 "$state_root/$path"
done
install -d -o root -g root -m 0700 "$protocol_state/transactions" "$protocol_state/profiles"
install -d -o root -g root -m 0755 "$protocol_lib_dir"
install -d -m 0755 "$bin_dir"
install -m 0755 "$binary" "$bin_dir/aimili-gateway"
install -m 0755 "$admin_binary" "$bin_dir/aimili-gateway-admin"
control_source="${AIMILI_CONTROL_TOKEN_SOURCE:-/etc/aimilivpn/control.token}"
install -m 0600 "$control_source" "$etc_root/aimili-control-token"
xui_source="${AIMILI_XUI_CREDENTIALS:-/etc/aimili-local/xui-credentials.json}"
install -m 0600 "$xui_source" "$etc_root/xui-automation.json"
raw_key=''; init_config=''; admin_input=''; init_dir=''
cleanup() { rm -f -- "$raw_key"; rm -rf -- "$init_dir"; rm -f -- "$admin_input"; }
trap cleanup EXIT
if [[ ! -s "$db_path" ]]; then
  init_dir="$(mktemp -d /tmp/aimili-gateway-init.XXXXXX)"
  chown aimili-gateway:aimili-gateway "$init_dir"
  chmod 0700 "$init_dir"
  raw_key="$init_dir/master.key"
  install -o aimili-gateway -g aimili-gateway -m 0400 /dev/null "$raw_key"
fi
if [[ ! -s "$encrypted" ]]; then
  [[ ! -s "$db_path" ]] || { printf 'encrypted_credential_missing_for_database\n' >&2; exit 5; }
  head -c 32 /dev/urandom > "$raw_key"
  [[ "$(wc -c < "$raw_key")" -eq 32 ]] || { printf 'master_key_length_invalid\n' >&2; exit 5; }
  chown aimili-gateway:aimili-gateway "$raw_key"
  chmod 0400 "$raw_key"
  systemd-creds encrypt --name=gateway-master-key "$raw_key" "$encrypted" >/dev/null
  chmod 0600 "$encrypted"
elif [[ ! -s "$db_path" ]]; then
  systemd-creds decrypt "$encrypted" "$raw_key" >/dev/null
  [[ "$(wc -c < "$raw_key")" -eq 32 ]] || { printf 'master_key_length_invalid\n' >&2; exit 5; }
  chown aimili-gateway:aimili-gateway "$raw_key"
  chmod 0400 "$raw_key"
fi
python3 - "$config_template" "$config_path" "$public_origin" "$db_path" "$allowed_source" <<'PY'
import json, sys
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
    'protocolRequestDir': '/var/lib/aimili-gateway/protocol-spool/requests',
    'protocolResultDir': '/var/lib/aimili-gateway/protocol-spool/results',
    'protocolTimeoutSeconds': 180,
})
with open(sys.argv[2] + '.tmp', 'w', encoding='utf-8') as handle:
    json.dump(cfg, handle, ensure_ascii=False, indent=2)
import os
os.chmod(sys.argv[2] + '.tmp', 0o400); os.replace(sys.argv[2] + '.tmp', sys.argv[2])
PY
chown aimili-gateway:aimili-gateway "$config_path"
python3 - "$protocol_config_template" "$protocol_config" "$public_origin" "$manifest" "$certificate" "$private_key" <<'PY'
import json, os, sys, urllib.parse
source,target,origin,manifest_path,certificate,key=sys.argv[1:]
config=json.load(open(source)); deployment=json.load(open(manifest_path)); slots=int(deployment['expected']['exitSlots'])
config.update({'certificatePath':certificate,'privateKeyPath':key,'tlsServerName':urllib.parse.urlsplit(origin).hostname,'allowedPorts':[8443]+list(range(20000,20000+slots)),'spoolRequestDir':'/var/lib/aimili-gateway/protocol-spool/requests','spoolResultDir':'/var/lib/aimili-gateway/protocol-spool/results'})
if not os.path.isfile(certificate) or not os.path.isfile(key): raise SystemExit('protocol_tls_material_missing')
temporary=target+'.tmp'
with open(temporary,'w',encoding='utf-8') as handle: json.dump(config,handle,separators=(',',':'))
os.chmod(temporary,0o640); os.replace(temporary,target)
PY
chown root:aimili-gateway "$protocol_config"
if [[ ! -s "$db_path" ]]; then
init_config="$init_dir/config.json"
cp "$config_path" "$init_config"; chmod 0400 "$init_config"; chown aimili-gateway:aimili-gateway "$init_config"
python3 - "$init_config" "$raw_key" <<'PY'
import json,sys
p,key=sys.argv[1:]; d=json.load(open(p)); d['masterKeyFile']=key; json.dump(d,open(p,'w'))
PY
username="$(python3 -c 'import secrets; print("local" + secrets.token_hex(4))')"
password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
admin_input="${AIMILI_GATEWAY_INIT_INPUT:-$(mktemp /tmp/aimili-gateway-admin.XXXXXX)}"
printf '%s\n%s\n%s\n' "$username" "$password" "$password" > "$admin_input"; chmod 0600 "$admin_input"
runuser -u aimili-gateway -- env GATEWAY_CONFIG="$init_config" < "$admin_input" "$bin_dir/aimili-gateway-admin" init >/dev/null
printf '{"username":"%s","password":"%s"}\n' "$username" "$password" | python3 -c 'import json,os,sys; p=sys.argv[1]; d=json.load(sys.stdin); t=p+".tmp"; json.dump(d,open(t,"w"),separators=(",",":")); os.chmod(t,0o600); os.chown(t,0,0); os.replace(t,p)' "$etc_root/admin-credentials.json"
unset username password
fi
install -m 0644 "$unit_source" "$unit_dest"
install -D -o root -g root -m 0755 "$account_wrapper_source" "$account_wrapper_dest"
install -D -o root -g root -m 0755 "$protocol_wrapper_source" "$protocol_bin_dest"
install -o root -g root -m 0644 "$protocol_script_source" "$protocol_lib_dir/aimili_xui_protocol_transaction.py"
install -D -o root -g root -m 0644 "$protocol_path_source" "$protocol_path_dest"
install -D -o root -g root -m 0644 "$protocol_service_source" "$protocol_service_dest"
install -D -o root -g root -m 0644 "$protocol_timer_source" "$protocol_timer_dest"
systemctl daemon-reload
systemctl enable --now aimili-xui-protocol-transaction.path aimili-xui-protocol-transaction.timer >/dev/null
systemctl enable aimili-gateway.service >/dev/null
systemctl restart aimili-gateway.service
printf 'gateway_apply_ok\n'
