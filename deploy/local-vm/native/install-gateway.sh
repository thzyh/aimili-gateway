#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
binary=''
admin_binary=''
config_template=''
allowed_source=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --binary) binary="$2"; shift 2 ;;
    --admin-binary) admin_binary="$2"; shift 2 ;;
    --config-template) config_template="$2"; shift 2 ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$binary" = /* && -x "$binary" ]] || { printf 'gateway_binary_invalid\n' >&2; exit 3; }
[[ "$admin_binary" = /* && -x "$admin_binary" ]] || { printf 'gateway_admin_binary_invalid\n' >&2; exit 3; }
[[ "$config_template" = /* && -s "$config_template" ]] || { printf 'gateway_config_template_invalid\n' >&2; exit 3; }
[[ "$allowed_source" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { printf 'allowed_source_invalid\n' >&2; exit 3; }
for command in python3 systemctl install; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
if [[ "$mode" == '--check' ]]; then
  printf 'gateway_check_ok\n'
  exit 0
fi
[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }

if ! id -u aimili-gateway >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/aimili-gateway --shell /usr/sbin/nologin aimili-gateway
fi
install -d -o root -g root -m 0750 /etc/aimili-gateway /var/lib/aimili-gateway
install -d -o aimili-gateway -g aimili-gateway -m 0700 /var/lib/aimili-gateway
install -m 0755 "$binary" /usr/local/bin/aimili-gateway
install -m 0755 "$admin_binary" /usr/local/bin/aimili-gateway-admin
if [[ ! -s /etc/aimili-gateway/master.key ]]; then
  python3 -c 'import secrets; print(secrets.token_urlsafe(32))' > /etc/aimili-gateway/master.key
  chmod 0600 /etc/aimili-gateway/master.key
fi
if [[ ! -s /etc/aimili-gateway/aimili-control-token ]]; then
  install -m 0600 /etc/aimilivpn/control.token /etc/aimili-gateway/aimili-control-token
fi
install -m 0600 /etc/aimili-local/xui-credentials /etc/aimili-gateway/xui-automation
python3 - "$config_template" /etc/aimili-gateway/config.json "$allowed_source" <<'PY'
import json, sys
source = sys.argv[3]
with open(sys.argv[1], encoding='utf-8') as handle:
    cfg = json.load(handle)
cfg.update({
    'listenAddress': '127.0.0.1:9080',
    'publicOrigin': 'http://127.0.0.1:8080',
    'databasePath': '/var/lib/aimili-gateway/aimili-gateway.db',
    'masterKeyFile': '/etc/aimili-gateway/master.key',
    'aimiliControlTokenFile': '/etc/aimili-gateway/aimili-control-token',
    'xuiCredentialsFile': '/etc/aimili-gateway/xui-automation',
    'xuiBaseUrl': 'http://127.0.0.1:2001/xui/',
    'mixedSourceCidrs': [source + '/32'],
    'expertModeUrl': '/xui/',
    'aimiliBackendUrl': '/vpngate/',
})
with open(sys.argv[2], 'w', encoding='utf-8') as handle:
    json.dump(cfg, handle, ensure_ascii=False, indent=2)
PY
chmod 0600 /etc/aimili-gateway/config.json
username="$(python3 -c 'import secrets; print("local" + secrets.token_hex(4))')"
password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
printf '%s\n%s\n' "$username" "$password" | GATEWAY_CONFIG=/etc/aimili-gateway/config.json GATEWAY_MASTER_KEY_FILE=/etc/aimili-gateway/master.key /usr/local/bin/aimili-gateway-admin init >/dev/null
printf '%s\n%s\n' "$username" "$password" > /etc/aimili-gateway/credentials
chmod 0600 /etc/aimili-gateway/credentials
cat > /etc/systemd/system/aimili-gateway.service <<'UNIT'
[Unit]
Description=Aimili Gateway local native service
After=network-online.target aimilivpn.service x-ui.service
Wants=network-online.target

[Service]
Type=simple
User=aimili-gateway
Group=aimili-gateway
WorkingDirectory=/var/lib/aimili-gateway
ExecStart=/usr/local/bin/aimili-gateway
Environment=GATEWAY_CONFIG=/etc/aimili-gateway/config.json
Environment=GATEWAY_MASTER_KEY_FILE=/etc/aimili-gateway/master.key
Environment=GATEWAY_AIMILI_CONTROL_TOKEN_FILE=/etc/aimili-gateway/aimili-control-token
Environment=GATEWAY_XUI_CREDENTIALS_FILE=/etc/aimili-gateway/xui-automation
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/aimili-gateway
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now aimili-gateway.service >/dev/null
printf 'gateway_apply_ok\n'
