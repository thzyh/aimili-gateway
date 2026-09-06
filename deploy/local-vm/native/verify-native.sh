#!/usr/bin/env bash
set -euo pipefail
umask 077
manifest='/etc/aimili-local/deployment.json'
if [[ "${1:-}" == '--manifest' ]]; then manifest="$2"; fi
[[ -s "$manifest" ]] || { printf 'manifest_missing\n' >&2; exit 3; }
python3 - "$manifest" <<'PY'
import json, os, sqlite3, subprocess, sys
manifest = json.load(open(sys.argv[1], encoding='utf-8'))
expected = manifest['expected']
services = {}
for unit in manifest['services']:
    services[unit.removesuffix('.service')] = subprocess.run(['systemctl','is-active','--quiet',unit]).returncode == 0
openvpn = int((subprocess.run(['pgrep','-cx','openvpn'], capture_output=True, text=True).stdout or '0').strip() or 0)
xray = int((subprocess.run(['pgrep','-cx','xray'], capture_output=True, text=True).stdout or '0').strip() or 0)
slots_path = '/opt/aimilivpn/vpngate_data/slots.json'
slots = 0
if os.path.exists(slots_path):
    try:
        data = json.load(open(slots_path, encoding='utf-8'))
        slots = len(data) if isinstance(data, (dict, list)) else 0
    except Exception:
        slots = -1
actual = {'openvpn': openvpn, 'xray': xray, 'logicalExits': (1 + slots if openvpn else 0), 'exitSlots': slots}
database_readable = False
db_path = '/var/lib/aimili-gateway/aimili-gateway.db'
if os.path.exists(db_path):
    try:
        with sqlite3.connect(db_path) as db:
            database_readable = db.execute('pragma quick_check').fetchone()[0] == 'ok'
    except Exception:
        database_readable = False
ready = all(services.values()) and database_readable and all(actual[k] == int(expected[k]) for k in ('openvpn','xray','logicalExits','exitSlots'))
print(json.dumps({'nativeServices': services, 'expected': expected, 'actual': actual, 'databaseReadable': database_readable, 'nativeReady': ready}, separators=(',', ':')))
raise SystemExit(0 if ready else 1)
PY
