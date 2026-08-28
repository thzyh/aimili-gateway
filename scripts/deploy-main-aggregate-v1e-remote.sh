#!/usr/bin/env bash
set -euo pipefail

[[ "${EUID}" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }
readonly GATEWAY_ASSET="/tmp/aimili-gateway-v1e"
readonly AIMILI_BUNDLE="/tmp/aimili-vpngate-v1e.bundle"
readonly TARGET_AIMILI_COMMIT="45ca00c2ae73d5db935956bb976f11f266df1e1a"
readonly TARGET_GATEWAY_SHA="4f801eb765d513d9321cecdfa90360180b7917454f79a1e6cfc54b0c291bb8ab"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP="/var/backups/aimili-gateway/v1e-main-aggregate/${STAMP}"

[[ -s "$GATEWAY_ASSET" && -s "$AIMILI_BUNDLE" ]] || { printf '%s\n' '部署资产不存在。' >&2; exit 1; }
[[ "$(sha256sum "$GATEWAY_ASSET" | awk '{print $1}')" == "$TARGET_GATEWAY_SHA" ]] || { printf '%s\n' 'Gateway 资产摘要不匹配。' >&2; exit 1; }
git -C /opt/aimilivpn bundle verify "$AIMILI_BUNDLE" >/dev/null
[[ -z "$(git -C /opt/aimilivpn status --porcelain)" ]] || { printf '%s\n' 'AimiliVPN 工作区不干净。' >&2; exit 1; }

install -d -m 0700 "$BACKUP"
cp -a /usr/local/bin/aimili-gateway "$BACKUP/aimili-gateway"
cp -a /etc/aimili-gateway/config.json "$BACKUP/config.json"
printf '%s\n' "$(git -C /opt/aimilivpn rev-parse HEAD)" > "$BACKUP/aimilivpn-commit"
python3 - "$BACKUP" <<'PY'
import pathlib, sqlite3, sys
target = pathlib.Path(sys.argv[1])
for source, name in (("/etc/x-ui/x-ui.db", "x-ui.db"), ("/var/lib/aimili-gateway/aimili-gateway.db", "aimili-gateway.db")):
    source_db = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    backup_db = sqlite3.connect(str(target / name))
    source_db.backup(backup_db)
    backup_db.close(); source_db.close()
PY
chmod -R go-rwx "$BACKUP"

rollback() {
    set +e
    systemctl stop aimili-gateway.service aimilivpn.service
    cp -a "$BACKUP/aimili-gateway" /usr/local/bin/aimili-gateway
    cp -a "$BACKUP/config.json" /etc/aimili-gateway/config.json
    cp -a "$BACKUP/aimili-gateway.db" /var/lib/aimili-gateway/aimili-gateway.db
    cp -a "$BACKUP/x-ui.db" /etc/x-ui/x-ui.db
    old_commit="$(<"$BACKUP/aimilivpn-commit")"
    git -C /opt/aimilivpn reset --hard "$old_commit"
    systemctl restart x-ui aimilivpn.service aimili-gateway.service
}
trap 'rollback' ERR

git -C /opt/aimilivpn fetch "$AIMILI_BUNDLE" custom
[[ "$(git -C /opt/aimilivpn rev-parse FETCH_HEAD)" == "$TARGET_AIMILI_COMMIT" ]] || false
git -C /opt/aimilivpn merge --ff-only FETCH_HEAD

python3 - <<'PY'
import json, os, pathlib, tempfile
path=pathlib.Path('/etc/aimili-gateway/config.json')
cfg=json.loads(path.read_text(encoding='utf-8'))
cfg['aggregateVlessPort']=21000
cfg['mainMixedPort']=31000
fd, tmp=tempfile.mkstemp(prefix='.config-v1e-', dir=str(path.parent))
try:
    with os.fdopen(fd,'w',encoding='utf-8') as handle:
        json.dump(cfg,handle,ensure_ascii=False,indent=2); handle.write('\n'); handle.flush(); os.fsync(handle.fileno())
    os.chmod(tmp, path.stat().st_mode & 0o777)
    os.chown(tmp, path.stat().st_uid, path.stat().st_gid)
    os.replace(tmp,path)
finally:
    if os.path.exists(tmp): os.unlink(tmp)
PY
install -o root -g root -m 0755 "$GATEWAY_ASSET" /usr/local/bin/aimili-gateway
systemctl restart aimilivpn.service
for _ in $(seq 1 90); do
    systemctl is-active --quiet aimilivpn.service && ss -lntH | grep -q '127.0.0.1:7928' && break
    sleep 2
done
systemctl is-active --quiet aimilivpn.service
ss -lntH | grep -q '127.0.0.1:7928'
systemctl restart aimili-gateway.service
for _ in $(seq 1 30); do
    systemctl is-active --quiet aimili-gateway.service && ss -lntH | grep -q '127.0.0.1:9080' && break
    sleep 2
done
systemctl is-active --quiet aimili-gateway.service x-ui caddy
ss -lntH | grep -q '127.0.0.1:9080'
python3 - <<'PY'
import sqlite3
db=sqlite3.connect('file:/var/lib/aimili-gateway/aimili-gateway.db?mode=ro',uri=True)
assert db.execute('SELECT COUNT(*) FROM schema_migrations WHERE version=7').fetchone()[0] == 1
PY
trap - ERR
printf 'backup=%s\n' "$BACKUP"
printf '%s\n' 'deploy=ok'
