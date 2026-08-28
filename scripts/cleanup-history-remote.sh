#!/usr/bin/env bash
set -euo pipefail

mode="${1:---dry-run}"
case "$mode" in
    --self-test)
        grep -q 'mode="${1:---dry-run}"' "$0"
        grep -q 'backup(' "$0"
        grep -q 'DELETE FROM clients' "$0"
        printf '%s\n' 'cleanup-history self-test: ok'
        exit 0
        ;;
    --dry-run|--apply) ;;
    *) printf '%s\n' '用法：cleanup-history-remote.sh [--dry-run|--apply|--self-test]' >&2; exit 2 ;;
esac

apply=0
[[ "$mode" == "--apply" ]] && apply=1

python3 - "$apply" <<'PY'
import datetime
import json
import os
import pathlib
import sqlite3
import sys

apply = sys.argv[1] == "1"
path = pathlib.Path("/etc/x-ui/x-ui.db")
if not path.is_file():
    raise SystemExit("3x-ui database not found")

db = sqlite3.connect(str(path))
tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
if not {"inbounds", "clients"}.issubset(tables):
    raise SystemExit("required 3x-ui tables are missing")
columns = {row[1] for row in db.execute("PRAGMA table_info(clients)")}
traffic_columns = {row[1] for row in db.execute("PRAGMA table_info(client_traffics)")} if "client_traffics" in tables else set()
if not {"id", "email"}.issubset(columns) or not {"id", "email", "inbound_id"}.issubset(traffic_columns):
    raise SystemExit("unsupported 3x-ui client reference schema")
orphans = [int(row[0]) for row in db.execute("""
    SELECT c.id FROM clients AS c
    WHERE NOT EXISTS (
        SELECT 1 FROM client_traffics AS t
        JOIN inbounds AS i ON i.id = t.inbound_id
        WHERE t.email = c.email
    ) ORDER BY c.id
""")]
orphan_traffics = [int(row[0]) for row in db.execute("SELECT id FROM client_traffics WHERE inbound_id NOT IN (SELECT id FROM inbounds) ORDER BY id")]
summary = {"mode": "apply" if apply else "dry-run", "orphan_client_count": len(orphans), "orphan_traffic_count": len(orphan_traffics), "backup_created": False, "deleted_clients": 0, "deleted_traffics": 0}
legacy_db = pathlib.Path("/var/lib/aimili-gateway/gateway.db")
configured_db = ""
try:
    configured_db = str(json.loads(pathlib.Path("/etc/aimili-gateway/config.json").read_text(encoding="utf-8")).get("databasePath") or "")
except Exception:
    pass
empty_legacy = legacy_db.is_file() and legacy_db.stat().st_size == 0 and str(legacy_db) != configured_db
summary["empty_legacy_database"] = empty_legacy
if not apply or (not orphans and not orphan_traffics and not empty_legacy):
    print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
    db.close()
    raise SystemExit(0)

stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
backup_dir = pathlib.Path("/var/backups/aimili-gateway/history-cleanup") / stamp
backup_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
backup_path = backup_dir / "x-ui.db"
backup = sqlite3.connect(str(backup_path))
db.backup(backup)
backup.close()
os.chmod(backup_path, 0o600)
summary["backup_created"] = True

db.execute("BEGIN IMMEDIATE")
if orphan_traffics:
    traffic_placeholders = ",".join("?" for _ in orphan_traffics)
    traffic_cursor = db.execute(f"DELETE FROM client_traffics WHERE id IN ({traffic_placeholders}) AND inbound_id NOT IN (SELECT id FROM inbounds)", orphan_traffics)
    summary["deleted_traffics"] = traffic_cursor.rowcount
if orphans:
    placeholders = ",".join("?" for _ in orphans)
    cursor = db.execute(f"""DELETE FROM clients WHERE id IN ({placeholders}) AND NOT EXISTS (
        SELECT 1 FROM client_traffics AS t JOIN inbounds AS i ON i.id = t.inbound_id WHERE t.email = clients.email
    )""", orphans)
    summary["deleted_clients"] = cursor.rowcount
db.commit()
remaining = db.execute("""SELECT COUNT(*) FROM clients AS c WHERE NOT EXISTS (
    SELECT 1 FROM client_traffics AS t JOIN inbounds AS i ON i.id = t.inbound_id WHERE t.email = c.email
)""").fetchone()[0]
summary["remaining_orphans"] = int(remaining)
summary["remaining_orphan_traffics"] = int(db.execute("SELECT COUNT(*) FROM client_traffics WHERE inbound_id NOT IN (SELECT id FROM inbounds)").fetchone()[0])
db.close()
if empty_legacy:
    legacy_db.unlink()
    summary["deleted_empty_legacy_database"] = True
print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
PY
