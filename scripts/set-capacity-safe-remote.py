#!/usr/bin/env python3
"""Safely change Gateway capacity between the approved levels 1..4."""

from __future__ import annotations

import copy
import datetime
import json
import os
import pathlib
import shutil
import sqlite3
import sys
import tempfile


CONFIG = pathlib.Path("/etc/aimili-gateway/config.json")
PROTOCOL_CONFIG = pathlib.Path("/etc/aimili-gateway/protocol-transaction.json")
DATABASE_CANDIDATES = (
    pathlib.Path("/var/lib/aimili-gateway/gateway.db"),
    pathlib.Path("/var/lib/aimili-gateway/aimili-gateway.db"),
)
BACKUP_ROOT = pathlib.Path("/var/backups/aimili-gateway")


def updated_config(document: dict[str, object], capacity: int) -> dict[str, object]:
    if capacity not in (1, 2, 3, 4):
        raise ValueError("capacity must be 1, 2, 3, or 4")
    result = copy.deepcopy(document)
    result["maxProxyGroups"] = capacity
    return result


def updated_protocol_config(document: dict[str, object], capacity: int) -> dict[str, object]:
    if capacity not in (1, 2, 3, 4):
        raise ValueError("capacity must be 1, 2, 3, or 4")
    result = copy.deepcopy(document)
    result["allowedPorts"] = [8443, *range(20000, 20000 + capacity)]
    return result


def fsync_directory(path: pathlib.Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def backup_database(source: pathlib.Path, destination: pathlib.Path) -> None:
    source_db = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    destination_db = sqlite3.connect(destination)
    try:
        source_db.backup(destination_db)
        destination_db.commit()
    finally:
        destination_db.close()
        source_db.close()
    os.chmod(destination, 0o600)


def atomic_write_json(path: pathlib.Path, document: dict[str, object]) -> None:
    stat = path.stat()
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}-", suffix=".tmp", dir=path.parent)
    temporary = pathlib.Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(document, handle, ensure_ascii=False, indent=2)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, stat.st_mode & 0o777)
        os.chown(temporary, stat.st_uid, stat.st_gid)
        os.replace(temporary, path)
        fsync_directory(path.parent)
    finally:
        temporary.unlink(missing_ok=True)


def main() -> int:
    if os.geteuid() != 0 or len(sys.argv) != 2:
        raise SystemExit("usage: sudo set-capacity-safe-remote.py <1|2|3|4>")
    capacity = int(sys.argv[1])
    before = json.loads(CONFIG.read_text(encoding="utf-8"))
    after = updated_config(before, capacity)
    protocol_before = json.loads(PROTOCOL_CONFIG.read_text(encoding="utf-8"))
    protocol_after = updated_protocol_config(protocol_before, capacity)
    old_capacity = int(before.get("maxProxyGroups", 0))
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    backup = BACKUP_ROOT / f"capacity-{old_capacity}-to-{capacity}-{stamp}"
    backup.mkdir(mode=0o700, parents=True, exist_ok=False)
    shutil.copy2(CONFIG, backup / "config.json")
    shutil.copy2(PROTOCOL_CONFIG, backup / "protocol-transaction.json")
    for database in DATABASE_CANDIDATES:
        if database.is_file():
            backup_database(database, backup / database.name)

    atomic_write_json(CONFIG, after)
    atomic_write_json(PROTOCOL_CONFIG, protocol_after)

    verified = json.loads(CONFIG.read_text(encoding="utf-8"))
    if verified.get("maxProxyGroups") != capacity:
        raise RuntimeError("capacity write verification failed")
    verified_protocol = json.loads(PROTOCOL_CONFIG.read_text(encoding="utf-8"))
    if verified_protocol.get("allowedPorts") != [8443, *range(20000, 20000 + capacity)]:
        raise RuntimeError("protocol port write verification failed")
    print(json.dumps({"old_capacity": old_capacity, "new_capacity": capacity, "backup_created": True}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
