#!/usr/bin/env bash
set -euo pipefail

mode="${1:---audit}"

if [[ "$mode" == "--self-test" ]]; then
    grep -q "SELECT id, tag, remark, protocol, port" "$0"
    grep -q "orphan_client_count" "$0"
    ! grep -Eq 'SELECT[[:space:]].*(password|uuid|private|cookie|token)' "$0"
    printf '%s\n' 'audit-history self-test: ok'
    exit 0
fi
[[ "$mode" == "--audit" ]] || { printf '%s\n' '用法：audit-history-remote.sh [--audit|--self-test]' >&2; exit 2; }

python3 - <<'PY'
import json
import pathlib
import sqlite3
import subprocess

result = {"schema": 1, "checks": {}, "cleanup_candidates": [], "retain": []}

def service(name):
    run = subprocess.run(["systemctl", "is-active", name], text=True, capture_output=True)
    return run.stdout.strip() or "unknown"

result["checks"]["services"] = {name: service(name) for name in ("aimilivpn.service", "x-ui.service", "aimili-gateway.service", "caddy.service")}

xui = pathlib.Path("/etc/x-ui/x-ui.db")
if xui.is_file():
    db = sqlite3.connect(f"file:{xui}?mode=ro", uri=True)
    tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    inbounds = []
    if "inbounds" in tables:
        columns = {row[1] for row in db.execute("PRAGMA table_info(inbounds)")}
        wanted = [name for name in ("id", "tag", "remark", "protocol", "port") if name in columns]
        if wanted:
            inbounds = [dict(zip(wanted, row)) for row in db.execute(f"SELECT {', '.join(wanted)} FROM inbounds ORDER BY id")]
    result["checks"]["xui_inbounds"] = inbounds
    if "clients" in tables:
        columns = {row[1] for row in db.execute("PRAGMA table_info(clients)")}
        result["checks"]["clients_columns"] = sorted(columns)
        result["checks"]["clients_count"] = int(db.execute("SELECT COUNT(*) FROM clients").fetchone()[0])
        if "inbound_id" in columns and "id" in columns:
            active = {int(row.get("id", -1)) for row in inbounds}
            refs = [row[0] for row in db.execute("SELECT inbound_id FROM clients WHERE inbound_id IS NOT NULL")]
            orphan_count = sum(1 for value in refs if int(value) not in active)
            result["checks"]["orphan_client_count"] = orphan_count
            if orphan_count:
                result["cleanup_candidates"].append({"kind": "xui_orphan_clients", "count": orphan_count, "reason": "client inbound_id no longer references an inbound"})
    if "client_traffics" in tables:
        traffic_columns = {row[1] for row in db.execute("PRAGMA table_info(client_traffics)")}
        result["checks"]["client_traffics_columns"] = sorted(traffic_columns)
        result["checks"]["client_traffics_count"] = int(db.execute("SELECT COUNT(*) FROM client_traffics").fetchone()[0])
        if "clients" in tables and {"email", "inbound_id"}.issubset(traffic_columns) and "email" in columns:
            orphan_count = int(db.execute("""
                SELECT COUNT(*) FROM clients AS c
                WHERE NOT EXISTS (
                    SELECT 1 FROM client_traffics AS t
                    JOIN inbounds AS i ON i.id = t.inbound_id
                    WHERE t.email = c.email
                )
            """).fetchone()[0])
            orphan_traffic_count = int(db.execute("SELECT COUNT(*) FROM client_traffics WHERE inbound_id NOT IN (SELECT id FROM inbounds)").fetchone()[0])
            result["checks"]["orphan_client_count"] = orphan_count
            result["checks"]["orphan_client_traffic_count"] = orphan_traffic_count
            if orphan_count or orphan_traffic_count:
                result["cleanup_candidates"].append({"kind": "xui_orphan_clients", "count": orphan_count, "traffic_count": orphan_traffic_count, "reason": "client email has no traffic reference to an existing inbound"})
    db.close()

config_path = pathlib.Path("/etc/aimili-gateway/config.json")
current_db = ""
if config_path.is_file():
    try:
        current_db = str(json.loads(config_path.read_text(encoding="utf-8")).get("databasePath") or "")
    except Exception:
        result["checks"]["gateway_config"] = "unreadable"
for path in (pathlib.Path("/var/lib/aimili-gateway/gateway.db"), pathlib.Path("/var/lib/aimili-gateway/aimili-gateway.db")):
    if path.is_file():
        item = {"path": str(path), "current": str(path) == current_db, "bytes": path.stat().st_size}
        result["checks"].setdefault("gateway_databases", []).append(item)
        if not item["current"]:
            if item["bytes"] == 0:
                result["cleanup_candidates"].append({"kind": "empty_legacy_gateway_database", "path": str(path), "reason": "zero-byte file is not the configured database"})
            else:
                result["retain"].append({"kind": "legacy_gateway_database", "path": str(path), "reason": "keep until schema and rollback provenance are manually confirmed"})

for unit in ("x-ui-caddy-sync.timer", "x-ui-caddy-sync.service"):
    loaded = subprocess.run(["systemctl", "show", unit, "-p", "LoadState", "--value"], text=True, capture_output=True).stdout.strip()
    if loaded and loaded != "not-found":
        result["retain"].append({"kind": "systemd_unit", "name": unit, "reason": "do not remove until current Caddy dependency is proven absent"})

repo = pathlib.Path("/opt/aimilivpn")
if (repo / ".git").exists():
    stash = subprocess.run(["git", "-C", str(repo), "stash", "list", "--format=%gd"], text=True, capture_output=True).stdout.splitlines()
    result["checks"]["aimilivpn_stash_count"] = len(stash)
    if stash:
        result["retain"].append({"kind": "aimilivpn_stashes", "count": len(stash), "reason": "review commit provenance before dropping"})

backup_roots = [pathlib.Path("/var/backups/aimili-gateway"), pathlib.Path("/var/backups/aimili-gateway-bootstrap"), pathlib.Path("/var/backups/aimili-v1c")]
result["checks"]["backup_roots"] = [{"path": str(path), "exists": path.exists()} for path in backup_roots]
result["retain"].append({"kind": "production_backups", "reason": "retain newest working rollback point and any upgrade-specific rollback set"})

print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
PY
