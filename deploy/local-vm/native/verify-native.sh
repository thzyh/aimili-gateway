#!/usr/bin/env bash
set -euo pipefail
umask 077

manifest='/etc/aimili-local/deployment.json'
if [[ "${1:-}" == '--manifest' ]]; then manifest="$2"; fi
[[ -s "$manifest" ]] || { printf 'manifest_missing\n' >&2; exit 3; }

python3 - "$manifest" <<'PY'
import ipaddress
import json
import os
import re
import sqlite3
import subprocess
import sys

manifest = json.load(open(sys.argv[1], encoding='utf-8'))
expected = manifest['expected']

def succeeds(command):
    return subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

def output(command):
    result = subprocess.run(command, capture_output=True, text=True)
    return result.stdout if result.returncode == 0 else ''

services = {}
enabled = {}
for unit in manifest['services']:
    name = unit[:-8] if unit.endswith('.service') else unit
    services[name] = succeeds(['systemctl', 'is-active', '--quiet', unit])
    enabled[name] = succeeds(['systemctl', 'is-enabled', '--quiet', unit])

def process_count(arguments):
    try:
        return int((output(arguments) or '0').strip() or 0)
    except ValueError:
        return -1

openvpn = process_count(['pgrep', '-cx', 'openvpn'])
xray = process_count(['pgrep', '-fc', r'(^|/)(xray-linux-amd64|xray)([[:space:]]|$)'])

socket_lines = output(['ss', '-lntH']).splitlines()
def listening(port):
    suffix = re.compile(r':' + re.escape(str(int(port))) + r'$')
    return any(len(line.split()) >= 4 and suffix.search(line.split()[3]) for line in socket_lines)

listeners = {name: listening(port) for name, port in manifest.get('ports', {}).items()}

def has_tun(device):
    return bool(device) and succeeds(['ip', 'link', 'show', device])

def has_route(table):
    return bool(output(['ip', 'route', 'show', 'table', str(table)]).strip())

def live_egress(port):
    candidate = output(['curl', '-fsS', '--socks5-hostname', '127.0.0.1:' + str(port), '--max-time', '10', 'http://api.ipify.org']).strip()
    try:
        ipaddress.ip_address(candidate)
        return True
    except ValueError:
        return False

main_checks = {
    'tun': has_tun('tun0'),
    'route': has_route(int(os.environ.get('AIMILI_MAIN_ROUTE_TABLE', '100'))),
    'listener': listening(manifest['ports']['aimilivpnProxy']),
}
main_checks['egress'] = main_checks['listener'] and live_egress(manifest['ports']['aimilivpnProxy'])
main_ready = all(main_checks.values())

slots_path = os.environ.get('AIMILI_SLOTS_FILE', '/opt/aimilivpn/vpngate_data/slots.json')
slot_checks = []
slots_valid = False
try:
    document = json.load(open(slots_path, encoding='utf-8'))
    rows = document.get('slots', []) if isinstance(document, dict) else []
    if not isinstance(rows, list):
        rows = []
    table_base = int(os.environ.get('AIMILI_SLOT_TABLE_BASE', '200'))
    for row in rows:
        if not isinstance(row, dict):
            continue
        slot = int(row.get('slot'))
        port = int(row.get('port') or 0)
        status_ready = str(row.get('status') or '').lower() in ('ready', 'up')
        check = {
            'slot': slot,
            'ready': status_ready,
            'tun': has_tun(str(row.get('device') or '')),
            'route': has_route(table_base + slot),
            'listener': 0 < port <= 65535 and listening(port),
            'egress': row.get('egress_ok') is True and 0 < port <= 65535 and live_egress(port),
        }
        slot_checks.append(check)
    slot_checks.sort(key=lambda item: item['slot'])
    declared = list(range(int(expected['exitSlots'])))
    slots_valid = [item['slot'] for item in slot_checks] == declared and all(all(value for key, value in item.items() if key != 'slot') for item in slot_checks)
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    slot_checks = []

ready_slots = sum(1 for item in slot_checks if item['ready'])
actual = {
    'openvpn': openvpn,
    'xray': xray,
    'logicalExits': (1 + ready_slots if main_ready else ready_slots),
    'exitSlots': ready_slots,
}

database_readable = False
db_path = os.environ.get('AIMILI_GATEWAY_DB', '/var/lib/aimili-gateway/aimili-gateway.db')
if os.path.exists(db_path):
    try:
        with sqlite3.connect('file:' + db_path + '?mode=ro', uri=True) as db:
            database_readable = db.execute('pragma quick_check').fetchone()[0] == 'ok'
    except (OSError, sqlite3.Error):
        database_readable = False

ready = (
    all(services.values()) and
    all(enabled.values()) and
    all(listeners.values()) and
    database_readable and
    main_ready and
    slots_valid and
    all(actual[key] == int(expected[key]) for key in ('openvpn', 'xray', 'logicalExits', 'exitSlots'))
)
report = {
    'nativeServices': services,
    'nativeEnabled': enabled,
    'expected': expected,
    'actual': actual,
    'listeners': listeners,
    'mainChecks': main_checks,
    'slotChecks': slot_checks,
    'databaseReadable': database_readable,
    'nativeReady': ready,
}
print(json.dumps(report, separators=(',', ':')))
raise SystemExit(0 if ready else 1)
PY
