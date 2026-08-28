#!/usr/bin/env bash
set -euo pipefail

[[ "${EUID}" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }

readonly ASSET="/tmp/aimili-gateway-v1e-reality"
readonly VERIFY="/tmp/verify-main-aggregate-v1e-remote.py"
readonly EXPECTED_SHA="21a407b645b0952685cf0e3d71abfa33b65539e786294163d13c7595de0535b9"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP="/var/backups/aimili-gateway/v1e-reality-migration/${STAMP}"

[[ -s "$ASSET" && -s "$VERIFY" ]] || { printf '%s\n' '部署或验证资产不存在。' >&2; exit 1; }
[[ "$(sha256sum "$ASSET" | awk '{print $1}')" == "$EXPECTED_SHA" ]] || { printf '%s\n' 'Gateway 资产摘要不匹配。' >&2; exit 1; }

install -d -m 0700 "$BACKUP"
cp -a /usr/local/bin/aimili-gateway "$BACKUP/aimili-gateway"
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
chmod 0700 "$BACKUP"
chmod 0600 "$BACKUP/x-ui.db" "$BACKUP/aimili-gateway.db"
chmod 0755 "$BACKUP/aimili-gateway"

rollback() {
    set +e
    systemctl stop aimili-gateway.service x-ui
    cp -a "$BACKUP/aimili-gateway" /usr/local/bin/aimili-gateway
    cp -a "$BACKUP/aimili-gateway.db" /var/lib/aimili-gateway/aimili-gateway.db
    cp -a "$BACKUP/x-ui.db" /etc/x-ui/x-ui.db
    chown aimili-gateway:aimili-gateway /var/lib/aimili-gateway/aimili-gateway.db
    chmod 0600 /var/lib/aimili-gateway/aimili-gateway.db
    chmod 0755 /usr/local/bin/aimili-gateway
    systemctl restart x-ui aimili-gateway.service
}
rollback_and_exit() {
    rollback
    exit 1
}
trap 'rollback_and_exit' ERR

install -o root -g root -m 0755 "$ASSET" /usr/local/bin/aimili-gateway
systemctl restart aimili-gateway.service
for _ in $(seq 1 45); do
    if systemctl is-active --quiet aimili-gateway.service x-ui &&
       ss -lntH | grep -q '127.0.0.1:9080' &&
       ss -lntH | grep -q ':8443' &&
       ss -lntH | grep -q ':21000' &&
       ss -lntH | grep -q ':31000'; then
        break
    fi
    sleep 2
done
systemctl is-active --quiet aimili-gateway.service x-ui aimilivpn.service caddy
python3 "$VERIFY"

python3 - "$BACKUP/x-ui.db" <<'PY'
import json
import sqlite3
import sys

before = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
after = sqlite3.connect("file:/etc/x-ui/x-ui.db?mode=ro", uri=True)
columns = [row[1] for row in before.execute("PRAGMA table_info(inbounds)")]
stable_columns = [name for name in columns if name not in {"up", "down", "total", "expiry_time", "traffic_reset", "traffic_reset_day", "updated_at"}]
selection = ", ".join(stable_columns)
rows_before = {row[stable_columns.index("id")]: row for row in before.execute(f"SELECT {selection} FROM inbounds")}
rows_after = {row[stable_columns.index("id")]: row for row in after.execute(f"SELECT {selection} FROM inbounds")}
if set(rows_before) != set(rows_after):
    raise SystemExit("inbound set changed outside migration")
target_id = next(
    inbound_id
    for inbound_id, row in rows_before.items()
    if row[stable_columns.index("tag")] == "aimili-reality"
)
for inbound_id, row in rows_before.items():
    if inbound_id != target_id and row != rows_after[inbound_id]:
        raise SystemExit("unrelated inbound changed")

before_target = dict(zip(stable_columns, rows_before[target_id]))
after_target = dict(zip(stable_columns, rows_after[target_id]))
before_stream = json.loads(before_target.pop("stream_settings"))
after_stream = json.loads(after_target.pop("stream_settings"))
if before_target != after_target:
    raise SystemExit("legacy inbound identity changed")
before_reality = before_stream["realitySettings"]
after_reality = after_stream["realitySettings"]
before_profile = (before_reality.pop("target"), before_reality.pop("serverNames"))
after_profile = (after_reality.pop("target"), after_reality.pop("serverNames"))
if before_profile != ("www.microsoft.com:443", ["www.microsoft.com"]):
    raise SystemExit("unexpected historical Reality profile")
if after_profile[0] != "127.0.0.1:443" or len(after_profile[1]) != 1:
    raise SystemExit("Reality profile was not migrated")
if before_stream != after_stream or before_reality != after_reality:
    raise SystemExit("Reality identity material changed")
stable_client_refs = {
    "clients": "id, email, uuid",
    "client_traffics": "id, email, inbound_id",
}
for table, fields in stable_client_refs.items():
    if before.execute(f"SELECT {fields} FROM {table} ORDER BY id").fetchall() != after.execute(f"SELECT {fields} FROM {table} ORDER BY id").fetchall():
        raise SystemExit(f"{table} identity references changed during Reality migration")
before.close()
after.close()
print("migration_scope=ok")
PY

printf 'backup=%s\n' "$BACKUP"
printf '%s\n' 'deploy=ok'
