#!/usr/bin/env bash
set -euo pipefail

[[ "${EUID}" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }
[[ $# -eq 3 ]] || { printf '%s\n' '用法：deploy-test-style-subscription-remote.sh <gateway-sha256> <bundle-sha256> <aimili-commit>' >&2; exit 1; }

readonly GATEWAY_ASSET=/tmp/aimili-gateway-test-style
readonly AIMILI_BUNDLE=/tmp/aimili-vpngate-test-style.bundle
readonly EXPECTED_GATEWAY_SHA="$1"
readonly EXPECTED_BUNDLE_SHA="$2"
readonly TARGET_AIMILI_COMMIT="$3"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP="/var/backups/aimili-gateway/test-style-subscription/${STAMP}"
readonly XRAY_CONFIG=/usr/local/x-ui/bin/config.json

[[ -s "$GATEWAY_ASSET" && -s "$AIMILI_BUNDLE" ]] || { printf '%s\n' '部署资产不存在。' >&2; exit 1; }
[[ "$(sha256sum "$GATEWAY_ASSET" | awk '{print $1}')" == "$EXPECTED_GATEWAY_SHA" ]] || { printf '%s\n' 'Gateway 摘要不匹配。' >&2; exit 1; }
[[ "$(sha256sum "$AIMILI_BUNDLE" | awk '{print $1}')" == "$EXPECTED_BUNDLE_SHA" ]] || { printf '%s\n' 'AimiliVPN bundle 摘要不匹配。' >&2; exit 1; }
git -C /opt/aimilivpn bundle verify "$AIMILI_BUNDLE" >/dev/null
[[ -z "$(git -C /opt/aimilivpn status --porcelain)" ]] || { printf '%s\n' 'AimiliVPN 工作区不干净。' >&2; exit 1; }

install -d -m 0700 "$BACKUP"
cp -a /usr/local/bin/aimili-gateway "$BACKUP/aimili-gateway"
cp -a "$XRAY_CONFIG" "$BACKUP/xray-config.json"
git -C /opt/aimilivpn rev-parse HEAD > "$BACKUP/aimilivpn-commit"
python3 - "$BACKUP" <<'PY'
import pathlib
import sqlite3
import sys

target = pathlib.Path(sys.argv[1])
for source, name in (
    ("/etc/x-ui/x-ui.db", "x-ui.db"),
    ("/var/lib/aimili-gateway/aimili-gateway.db", "aimili-gateway.db"),
):
    source_db = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    backup_db = sqlite3.connect(str(target / name))
    source_db.backup(backup_db)
    backup_db.close()
    source_db.close()
PY
chmod 0600 "$BACKUP/x-ui.db" "$BACKUP/aimili-gateway.db" "$BACKUP/aimilivpn-commit" "$BACKUP/xray-config.json"
chmod 0755 "$BACKUP/aimili-gateway"

rollback() {
    set +e
    local old_commit
    old_commit="$(<"$BACKUP/aimilivpn-commit")"
    systemctl stop aimili-gateway.service aimilivpn.service x-ui.service
    cp -a "$BACKUP/aimili-gateway" /usr/local/bin/aimili-gateway
    cp -a "$BACKUP/aimili-gateway.db" /var/lib/aimili-gateway/aimili-gateway.db
    cp -a "$BACKUP/x-ui.db" /etc/x-ui/x-ui.db
    cp -a "$BACKUP/xray-config.json" "$XRAY_CONFIG"
    chown aimili-gateway:aimili-gateway /var/lib/aimili-gateway/aimili-gateway.db
    chmod 0600 /var/lib/aimili-gateway/aimili-gateway.db /etc/x-ui/x-ui.db "$XRAY_CONFIG"
    cmp -s "$BACKUP/xray-config.json" "$XRAY_CONFIG" || printf '%s\n' '警告：Xray 运行时配置恢复校验失败。' >&2
    git -C /opt/aimilivpn reset --hard "$old_commit" >/dev/null
    systemctl reset-failed aimilivpn.service
    systemctl restart x-ui
    systemctl start aimilivpn.service
    systemctl restart aimili-gateway.service
}
trap 'rollback; exit 1' ERR

git -C /opt/aimilivpn fetch "$AIMILI_BUNDLE" custom
[[ "$(git -C /opt/aimilivpn rev-parse FETCH_HEAD)" == "$TARGET_AIMILI_COMMIT" ]]
git -C /opt/aimilivpn merge --ff-only FETCH_HEAD
systemctl reset-failed aimilivpn.service
systemctl restart aimilivpn.service
for _ in $(seq 1 60); do
    systemctl is-active --quiet aimilivpn.service && ss -lntH | grep -q '127.0.0.1:8790' && break
    sleep 2
done
systemctl is-active --quiet aimilivpn.service
ss -lntH | grep -q '127.0.0.1:8790'

python3 - <<'PY'
import json
import pathlib
import urllib.request

token = pathlib.Path('/etc/aimilivpn/control.token').read_text(encoding='utf-8').strip()
request = urllib.request.Request(
    'http://127.0.0.1:8790/control/v1/capabilities',
    headers={'Authorization': f'Bearer {token}', 'Accept': 'application/json'},
)
with urllib.request.urlopen(request, timeout=10) as response:
    data = json.load(response)
if 'slots.assign' not in data.get('data', {}).get('capabilities', []):
    raise SystemExit('AimiliVPN assign capability missing')
PY

install -o root -g root -m 0755 "$GATEWAY_ASSET" /usr/local/bin/aimili-gateway
systemctl restart aimili-gateway.service
for _ in $(seq 1 45); do
    systemctl is-active --quiet aimili-gateway.service && ss -lntH | grep -q '127.0.0.1:9080' && break
    sleep 2
done
systemctl is-active --quiet aimili-gateway.service x-ui aimilivpn.service caddy.service
ss -lntH | grep -q '127.0.0.1:9080'
python3 - <<'PY'
import sqlite3

db = sqlite3.connect('file:/var/lib/aimili-gateway/aimili-gateway.db?mode=ro', uri=True)
versions = {row[0] for row in db.execute('SELECT version FROM schema_migrations')}
if not {8, 9}.issubset(versions):
    raise SystemExit('Gateway schema migration missing')
ready = db.execute("SELECT COUNT(*) FROM proxy_groups WHERE status='ready'").fetchone()[0]
if ready != 3:
    raise SystemExit(f'unexpected ready count: {ready}')
db.close()
PY

trap - ERR
printf 'backup=%s\n' "$BACKUP"
printf '%s\n' 'deploy=ok'
