#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

die() {
    printf 'Gateway 初始化失败：%s\n' "$*" >&2
    exit 1
}

[[ $# -eq 4 && "$1" == "install" ]] \
    || die "用法：install <gateway_sha> <admin_sha> <mixed_client_cidr>"
gateway_sha="$2"
admin_sha="$3"
mixed_client_cidr="$4"
for value in "$gateway_sha" "$admin_sha"; do
    [[ "$value" =~ ^[0-9a-f]{64}$ ]] || die "二进制哈希格式无效"
done
python3 - "$mixed_client_cidr" <<'PY'
import ipaddress
import sys

network = ipaddress.ip_network(sys.argv[1], strict=True)
if network.prefixlen == 0:
    raise SystemExit("mixed 来源不能是全网段")
PY

check_hash() {
    local expected="$1" path="$2" actual
    [[ -f "$path" ]] || die "缺少暂存文件"
    actual="$(sha256sum "$path" | awk '{print $1}')"
    [[ "$actual" == "$expected" ]] || die "暂存文件哈希不匹配"
}
check_hash "$gateway_sha" /tmp/aimili-gateway
check_hash "$admin_sha" /tmp/aimili-gateway-admin
[[ -f /tmp/aimili-gateway.service && -f /tmp/aimili-gateway-account ]] \
    || die "缺少 Gateway 部署资产"
grep -q 'Environment=GATEWAY_CONFIG=/etc/aimili-gateway/config.json' /tmp/aimili-gateway.service \
    || die "Gateway systemd 配置资产无效"
[[ -s /etc/aimilivpn/control.token ]] || die "AimiliVPN 控制令牌不存在"
[[ -x /usr/local/x-ui/x-ui ]] || die "3x-ui 尚未安装"
[[ -f /opt/aimilivpn/vpngate_data/ui_auth.json ]] || die "AimiliVPN 账户配置不存在"

backup_dir="/var/backups/aimili-gateway-bootstrap/$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 0700 "$backup_dir"
for path in /etc/aimili-gateway/config.json /etc/aimili-gateway/xui-automation.json /var/lib/aimili-gateway/aimili-gateway.db /etc/caddy/Caddyfile; do
    [[ -e "$path" ]] && cp -a -- "$path" "$backup_dir/"
done

if ! id -u aimili-gateway >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/aimili-gateway --shell /usr/sbin/nologin aimili-gateway
fi
install -d -o aimili-gateway -g aimili-gateway -m 0700 /var/lib/aimili-gateway
install -d -m 0750 -o root -g aimili-gateway /etc/aimili-gateway
install -d -m 0700 /etc/credstore.encrypted
install -m 0755 /tmp/aimili-gateway /usr/local/bin/aimili-gateway
install -m 0755 /tmp/aimili-gateway-admin /usr/local/bin/aimili-gateway-admin

shown="$(/usr/local/x-ui/x-ui setting -show true 2>/dev/null)"
xui_port="$(printf '%s\n' "$shown" | sed -nE 's/^[[:space:]]*port:[[:space:]]*([0-9]+).*$/\1/p' | head -n1)"
xui_path="$(printf '%s\n' "$shown" | sed -nE 's/^[[:space:]]*webBasePath:[[:space:]]*([^[:space:]]+).*$/\1/p' | head -n1)"
[[ "$xui_port" =~ ^[0-9]+$ && -n "$xui_path" ]] || die "无法读取 3x-ui 本地管理入口"
xui_path="/${xui_path#/}"
xui_path="${xui_path%/}/"

python3 - "$xui_port" "$xui_path" "$mixed_client_cidr" <<'PY'
import ipaddress
import json
import os
import sys

xui_port, xui_path, client_cidr = sys.argv[1:]
with open('/opt/aimilivpn/vpngate_data/ui_auth.json', encoding='utf-8') as handle:
    account = json.load(handle)

cidrs = [str(ipaddress.ip_network(client_cidr, strict=True))]
public_ip_path = '/opt/aimilivpn/vpngate_data/public_ip.txt'
if os.path.exists(public_ip_path):
    raw = open(public_ip_path, encoding='utf-8').read().strip()
    try:
        address = ipaddress.ip_address(raw)
        own = str(ipaddress.ip_network(f'{address}/{address.max_prefixlen}', strict=True))
        if own not in cidrs:
            cidrs.append(own)
    except ValueError:
        pass

credentials = {'username': str(account['username']), 'password': str(account['password'])}
with open('/etc/aimili-gateway/xui-automation.json', 'w', encoding='utf-8') as handle:
    json.dump(credentials, handle, ensure_ascii=False, separators=(',', ':'))
os.chmod('/etc/aimili-gateway/xui-automation.json', 0o600)

config = {
    'listenAddress': '127.0.0.1:9080',
    'publicOrigin': 'https://ny.zouyunhui.cc.cd',
    'databasePath': '/var/lib/aimili-gateway/aimili-gateway.db',
    'masterKeyFile': '/run/credentials/aimili-gateway.service/gateway-master-key',
    'aimiliAddress': '127.0.0.1:8787',
    'aimiliControlUrl': 'http://127.0.0.1:8790/',
    'aimiliControlTokenFile': '/run/credentials/aimili-gateway.service/aimili-control-token',
    'xuiBaseUrl': f'http://127.0.0.1:{xui_port}{xui_path}',
    'xuiCredentialsFile': '/run/credentials/aimili-gateway.service/xui-automation',
    'maxProxyGroups': 1,
    'vlessPortStart': 20000,
    'vlessPortEnd': 20999,
    'mixedPortStart': 30000,
    'mixedPortEnd': 30999,
    'xrayPath': '/usr/local/x-ui/bin/xray-linux-amd64',
    'probeHost': 'api.ipify.org',
    'mixedSourceCidrs': cidrs,
    'expertModeUrl': xui_path,
}
with open('/etc/aimili-gateway/config.json', 'w', encoding='utf-8') as handle:
    json.dump(config, handle, ensure_ascii=False, indent=2)
os.chmod('/etc/aimili-gateway/config.json', 0o600)
PY
chown aimili-gateway:aimili-gateway /etc/aimili-gateway/config.json

encrypted_key=/etc/credstore.encrypted/aimili-gateway-master-key
raw_key="$(mktemp /tmp/aimili-gateway-master.XXXXXX)"
init_config="$(mktemp /tmp/aimili-gateway-init.XXXXXX.json)"
cleanup() {
    rm -f -- "$raw_key" "$init_config" /tmp/aimili-gateway /tmp/aimili-gateway-admin \
        /tmp/aimili-gateway.service /tmp/aimili-gateway-account
}
trap cleanup EXIT
if [[ ! -s "$encrypted_key" ]]; then
    head -c 32 /dev/urandom > "$raw_key"
    systemd-creds encrypt --name=gateway-master-key "$raw_key" "$encrypted_key" >/dev/null
else
    systemd-creds decrypt "$encrypted_key" "$raw_key" >/dev/null
fi
chmod 0600 "$encrypted_key" "$raw_key"
chown aimili-gateway:aimili-gateway "$raw_key"
python3 - "$raw_key" "$init_config" <<'PY'
import json
import os
import sys

raw_key, output = sys.argv[1:]
with open('/etc/aimili-gateway/config.json', encoding='utf-8') as handle:
    config = json.load(handle)
config['masterKeyFile'] = raw_key
with open(output, 'w', encoding='utf-8') as handle:
    json.dump(config, handle, ensure_ascii=False)
os.chmod(output, 0o600)
PY
chown aimili-gateway:aimili-gateway "$init_config"

if [[ ! -s /var/lib/aimili-gateway/aimili-gateway.db ]]; then
    python3 - "$init_config" <<'PY'
import json
import subprocess
import sys

with open('/opt/aimilivpn/vpngate_data/ui_auth.json', encoding='utf-8') as handle:
    account = json.load(handle)
username = str(account['username'])
secret = str(account['password'])
result = subprocess.run(
    ['runuser', '-u', 'aimili-gateway', '--', 'env', f'GATEWAY_CONFIG={sys.argv[1]}',
     '/usr/local/bin/aimili-gateway-admin', 'init'],
    input=f'{username}\n{secret}\n{secret}\n', text=True,
    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
)
if result.returncode:
    raise SystemExit('aimili-gateway-admin init failed')
PY
fi

install -m 0644 /tmp/aimili-gateway.service /etc/systemd/system/aimili-gateway.service
install -m 0755 /tmp/aimili-gateway-account /usr/local/sbin/aimili-gateway-account

python3 - <<'PY'
import re

path = '/etc/caddy/Caddyfile'
text = open(path, encoding='utf-8').read()
if 'reverse_proxy 127.0.0.1:9080' not in text:
    block = '''    handle /api/v1/* {
        header {
            X-Content-Type-Options nosniff
            X-Frame-Options DENY
            Referrer-Policy no-referrer
            Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' https://api.github.com; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
        }
        reverse_proxy 127.0.0.1:9080
    }

    handle {
        header {
            X-Content-Type-Options nosniff
            X-Frame-Options DENY
            Referrer-Policy no-referrer
            Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' https://api.github.com; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
        }
        reverse_proxy 127.0.0.1:9080
    }'''
    text, count = re.subn(r'(?m)^[ \t]*respond "Not Found" 404[ \t]*$', block, text, count=1)
    if count != 1:
        raise SystemExit('Caddy fallback marker is missing')
    with open(path, 'w', encoding='utf-8') as handle:
        handle.write(text)
PY
caddy fmt --overwrite /etc/caddy/Caddyfile >/dev/null
caddy validate --config /etc/caddy/Caddyfile >/dev/null

ufw allow 20000:20999/tcp >/dev/null
ufw allow 30000:30999/tcp >/dev/null
ufw allow 31000/tcp >/dev/null
systemctl daemon-reload
systemctl enable --now aimili-gateway.service >/dev/null
systemctl reload caddy
for _ in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:9080/healthz >/dev/null; then
        break
    fi
    sleep 1
done
curl -fsS http://127.0.0.1:9080/healthz >/dev/null || die "Gateway 健康检查失败"
systemctl is-active --quiet aimilivpn.service x-ui caddy aimili-gateway.service \
    || die "至少一个独立服务未运行"
printf 'gateway_bootstrap=true\n'
