#!/usr/bin/env bash
set -euo pipefail

[[ "${EUID}" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }
[[ $# -eq 1 ]] || { printf '%s\n' '用法：deploy-test-style-cleanup-remote.sh <gateway-sha256>' >&2; exit 2; }

readonly GATEWAY_ASSET=/tmp/aimili-gateway-test-style-cleanup
readonly VERIFY=/tmp/verify-test-style-subscription.py
readonly EXPECTED_GATEWAY_SHA="$1"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP="/var/backups/aimili-gateway/test-style-cleanup/${STAMP}"
readonly GATEWAY_DB=/var/lib/aimili-gateway/aimili-gateway.db
readonly XUI_DB=/etc/x-ui/x-ui.db
readonly XRAY_CONFIG=/usr/local/x-ui/bin/config.json
readonly CREDENTIALS="/tmp/aimili-gateway-verification-$$.json"
readonly SECRET_WORK="$(mktemp -d /tmp/aimili-gateway-verification.XXXXXX)"
readonly XRAY=/usr/local/x-ui/bin/xray-linux-amd64

cleanup_secrets() {
    rm -rf "$SECRET_WORK"
    rm -f "$CREDENTIALS"
}
trap cleanup_secrets EXIT
chmod 0700 "$SECRET_WORK"

[[ -s "$GATEWAY_ASSET" && -s "$VERIFY" && -x "$XRAY" ]] || { printf '%s\n' '部署或验收资产不完整。' >&2; exit 1; }
[[ "$(sha256sum "$GATEWAY_ASSET" | awk '{print $1}')" == "$EXPECTED_GATEWAY_SHA" ]] || { printf '%s\n' 'Gateway 摘要不匹配。' >&2; exit 1; }
python3 -m py_compile "$VERIFY"
systemctl is-active --quiet aimili-gateway.service aimilivpn.service x-ui.service caddy.service

systemd-creds decrypt --name=gateway-master-key /etc/credstore.encrypted/aimili-gateway-master-key "$SECRET_WORK/master-key" >/dev/null
chmod 0600 "$SECRET_WORK/master-key"
python3 - "$SECRET_WORK/master-key" "$CREDENTIALS" <<'PY'
import hashlib
import hmac
import json
import os
import pathlib
import sqlite3
import sys
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

master = pathlib.Path(sys.argv[1]).read_bytes()
db = sqlite3.connect('file:/var/lib/aimili-gateway/aimili-gateway.db?mode=ro', uri=True)
def decrypt(purpose):
    ciphertext = bytes(db.execute('SELECT ciphertext FROM encrypted_credentials WHERE purpose=?', (purpose,)).fetchone()[0])
    key = hmac.new(master, b'aimili-gateway/credential/v1\0' + purpose.encode(), hashlib.sha256).digest()
    return AESGCM(key).decrypt(ciphertext[1:13], ciphertext[13:], ciphertext[:1]).decode()
credentials = {'username': decrypt('unified-username'), 'password': decrypt('unified-password')}
db.close()
descriptor = os.open(sys.argv[2], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, 'w', encoding='utf-8') as output:
    json.dump(credentials, output, separators=(',', ':'))
PY

install -d -m 0700 "$BACKUP"
cp -a /usr/local/bin/aimili-gateway "$BACKUP/aimili-gateway"
cp -a "$XRAY_CONFIG" "$BACKUP/xray-config.json"
python3 - "$BACKUP" "$XUI_DB" "$GATEWAY_DB" <<'PY'
import hashlib
import json
import pathlib
import sqlite3
import sys

target = pathlib.Path(sys.argv[1])
for source, name in ((sys.argv[2], "x-ui.db"), (sys.argv[3], "aimili-gateway.db")):
    source_db = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    backup_db = sqlite3.connect(str(target / name))
    source_db.backup(backup_db)
    backup_db.close()
    source_db.close()

db = sqlite3.connect(f"file:{sys.argv[2]}?mode=ro", uri=True)
rows = list(db.execute("SELECT id, tag, remark, protocol, port, settings, stream_settings, sniffing FROM inbounds ORDER BY id"))
unmanaged = []
for row in rows:
    tag = str(row[1] or "")
    if tag == "aimili-reality" or tag.startswith("agw-"):
        continue
    encoded = json.dumps(row, ensure_ascii=False, separators=(",", ":"), default=str).encode()
    unmanaged.append({"id": int(row[0]), "tag": tag, "sha256": hashlib.sha256(encoded).hexdigest()})
(target / "unmanaged-inbounds.json").write_text(json.dumps(unmanaged, sort_keys=True), encoding="utf-8")
db.close()
PY
chmod 0755 "$BACKUP/aimili-gateway"
chmod 0600 "$BACKUP/x-ui.db" "$BACKUP/aimili-gateway.db" "$BACKUP/unmanaged-inbounds.json" "$BACKUP/xray-config.json"

rollback() {
    set +e
    systemctl stop aimili-gateway.service x-ui.service
    cp -a "$BACKUP/aimili-gateway" /usr/local/bin/aimili-gateway
    chmod 0755 /usr/local/bin/aimili-gateway
    cp -a "$BACKUP/aimili-gateway.db" "$GATEWAY_DB"
    cp -a "$BACKUP/x-ui.db" "$XUI_DB"
    cp -a "$BACKUP/xray-config.json" "$XRAY_CONFIG"
    chown aimili-gateway:aimili-gateway "$GATEWAY_DB"
    chmod 0600 "$GATEWAY_DB" "$XUI_DB" "$XRAY_CONFIG"
    cmp -s "$BACKUP/xray-config.json" "$XRAY_CONFIG" || printf '%s\n' '警告：Xray 运行时配置恢复校验失败。' >&2
    systemctl restart x-ui.service
    systemctl restart aimili-gateway.service
}
trap 'rollback; exit 1' ERR

install -o root -g root -m 0755 "$GATEWAY_ASSET" /usr/local/bin/aimili-gateway
systemctl restart aimili-gateway.service
for _ in $(seq 1 45); do
    systemctl is-active --quiet aimili-gateway.service && ss -lntH | grep -q '127.0.0.1:9080' && break
    sleep 2
done
systemctl is-active --quiet aimili-gateway.service aimilivpn.service x-ui.service caddy.service
ss -lntH | grep -q '127.0.0.1:9080'

python3 "$VERIFY" \
    --origin https://ny.zouyunhui.cc.cd \
    --credentials "$CREDENTIALS" \
    --xray "$XRAY" \
    --cleanup-legacy

python3 - "$BACKUP" "$XUI_DB" "$GATEWAY_DB" <<'PY'
import hashlib
import json
import pathlib
import sqlite3
import sys

target = pathlib.Path(sys.argv[1])
xui = sqlite3.connect(f"file:{sys.argv[2]}?mode=ro", uri=True)
remaining = xui.execute("SELECT COUNT(*) FROM inbounds WHERE tag='agw-aggregate-vless-vless' OR port=21000").fetchone()[0]
references = xui.execute("SELECT COUNT(*) FROM inbounds WHERE settings LIKE '%aimili-gateway-aggregate%'").fetchone()[0]
rows = list(xui.execute("SELECT id, tag, remark, protocol, port, settings, stream_settings, sniffing FROM inbounds ORDER BY id"))
unmanaged = []
for row in rows:
    tag = str(row[1] or "")
    if tag == "aimili-reality" or tag.startswith("agw-"):
        continue
    encoded = json.dumps(row, ensure_ascii=False, separators=(",", ":"), default=str).encode()
    unmanaged.append({"id": int(row[0]), "tag": tag, "sha256": hashlib.sha256(encoded).hexdigest()})
xui.close()
before = json.loads((target / "unmanaged-inbounds.json").read_text(encoding="utf-8"))
gateway = sqlite3.connect(f"file:{sys.argv[3]}?mode=ro", uri=True)
enabled = gateway.execute("SELECT enabled FROM aggregate_config WHERE id=1").fetchone()[0]
gateway.close()
runtime = pathlib.Path('/usr/local/x-ui/bin/config.json').read_text(encoding='utf-8')
if remaining or references or enabled or before != unmanaged or 'agw-aggregate' in runtime or '21000' in runtime:
    raise SystemExit('post-cleanup invariant failed')
print(json.dumps({
    "aggregateConfigEnabled": bool(enabled),
    "legacyInboundCount": int(remaining),
    "legacyClientReferenceCount": int(references),
    "nonManagedInboundCount": len(unmanaged),
    "nonManagedInboundsUnchanged": before == unmanaged,
    "runtimeLegacyReference": False,
}, sort_keys=True))
PY

systemctl is-active --quiet aimili-gateway.service aimilivpn.service x-ui.service caddy.service
trap - ERR
printf 'backup=%s\n' "$BACKUP"
printf '%s\n' 'cleanup_deploy=ok'
