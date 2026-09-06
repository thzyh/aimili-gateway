#!/usr/bin/env bash
set -euo pipefail
umask 077

manifest='/etc/aimili-local/deployment.json'
evidence='/var/lib/aimili-local/verification/native-evidence.json'
json_requested=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --json) json_requested=true; shift ;;
    --manifest) [[ $# -ge 2 && "$2" != --* ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; manifest="$2"; shift 2 ;;
    --evidence) [[ $# -ge 2 && "$2" != --* ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; evidence="$2"; shift 2 ;;
    *) printf 'unknown_argument\n' >&2; exit 2 ;;
  esac
done
[[ "$json_requested" == true ]] || { printf 'json_mode_required\n' >&2; exit 2; }
[[ -s "$manifest" ]] || { printf 'manifest_missing\n' >&2; exit 3; }
[[ -s "$evidence" ]] || { printf 'evidence_missing\n' >&2; exit 3; }

python3 - "$manifest" "$evidence" <<'PY'
import ipaddress
import json
import os
import re
import sqlite3
import subprocess
import sys

manifest = json.load(open(sys.argv[1], encoding='utf-8'))
evidence = json.load(open(sys.argv[2], encoding='utf-8'))
expected = manifest['expected']

def run(command):
    return subprocess.run(command, capture_output=True, text=True)

def succeeds(command):
    return run(command).returncode == 0

def output(command):
    result = run(command)
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
def listener_hosts(port):
    hosts = []
    for line in socket_lines:
        fields = line.split()
        if len(fields) < 4:
            continue
        address = fields[3]
        if address.startswith('[') and ']:' in address:
            host, raw_port = address[1:].split(']:', 1)
        elif ':' in address:
            host, raw_port = address.rsplit(':', 1)
        else:
            continue
        if raw_port == str(int(port)):
            hosts.append(host)
    return hosts

def loopback_listener(port):
    hosts = listener_hosts(port)
    if not hosts:
        return False
    for host in hosts:
        try:
            if not ipaddress.ip_address(host).is_loopback:
                return False
        except ValueError:
            return False
    return True

def any_listener(port):
    return bool(listener_hosts(port))

listeners = {}
for name, port in manifest.get('ports', {}).items():
    listeners[name] = any_listener(port) if name == 'caddy' else loopback_listener(port)

def tun_up(device):
    if not device:
        return False
    state = output(['ip', '-o', 'link', 'show', 'dev', device])
    match = re.search(r'<([^>]*)>', state)
    return bool(match and 'UP' in match.group(1).split(','))

def default_route_uses(table, device):
    for line in output(['ip', 'route', 'show', 'table', str(table)]).splitlines():
        fields = line.split()
        if fields and fields[0] == 'default' and any(fields[index:index + 2] == ['dev', device] for index in range(len(fields) - 1)):
            return True
    return False

def live_egress(port):
    candidate = output(['curl', '-fsS', '--socks5-hostname', '127.0.0.1:' + str(port), '--max-time', '10', 'http://api.ipify.org']).strip()
    try:
        ipaddress.ip_address(candidate)
        return True
    except ValueError:
        return False

main_checks = {
    'tun': tun_up('tun0'),
    'route': default_route_uses(int(os.environ.get('AIMILI_MAIN_ROUTE_TABLE', '100')), 'tun0'),
    'listener': loopback_listener(manifest['ports']['aimilivpnProxy']),
}
main_checks['egress'] = main_checks['listener'] and live_egress(manifest['ports']['aimilivpnProxy'])
main_ready = all(main_checks.values())

slots_path = os.environ.get('AIMILI_SLOTS_FILE', '/opt/aimilivpn/vpngate_data/slots.json')
slot_checks = []
slots_valid = False
try:
    document = json.load(open(slots_path, encoding='utf-8'))
    rows = document.get('slots', []) if isinstance(document, dict) else []
    table_base = int(os.environ.get('AIMILI_SLOT_TABLE_BASE', '200'))
    for row in rows if isinstance(rows, list) else []:
        if not isinstance(row, dict):
            continue
        slot = int(row.get('slot'))
        device = str(row.get('device') or '')
        port = int(row.get('port') or 0)
        check = {
            'slot': slot,
            'ready': str(row.get('status') or '').lower() in ('ready', 'up'),
            'tun': tun_up(device),
            'route': default_route_uses(table_base + slot, device),
            'listener': 0 < port <= 65535 and loopback_listener(port),
            'egress': row.get('egress_ok') is True and 0 < port <= 65535 and live_egress(port),
        }
        slot_checks.append(check)
    slot_checks.sort(key=lambda item: item['slot'])
    declared = list(range(int(expected['exitSlots'])))
    slots_valid = [item['slot'] for item in slot_checks] == declared and all(all(value for key, value in item.items() if key != 'slot') for item in slot_checks)
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    slot_checks = []

ready_slots = sum(1 for item in slot_checks if item['ready'])
actual = {'openvpn': openvpn, 'xray': xray, 'logicalExits': (1 + ready_slots if main_ready else ready_slots), 'exitSlots': ready_slots}

database_readable = False
db_path = os.environ.get('AIMILI_GATEWAY_DB', '/var/lib/aimili-gateway/aimili-gateway.db')
if os.path.exists(db_path):
    try:
        with sqlite3.connect('file:' + db_path + '?mode=ro', uri=True) as db:
            database_readable = db.execute('pragma quick_check').fetchone()[0] == 'ok'
    except (OSError, sqlite3.Error):
        pass

allowed_protocols = {'vless_tcp_reality_vision', 'vless_xhttp_reality', 'hysteria2_quic_tls'}
evidence_schema = isinstance(evidence, dict) and evidence.get('schemaVersion') == 1
expected_exits = ['main'] + ['slot-' + str(index) for index in range(int(expected['exitSlots']))]
subscription_entries = evidence.get('subscription', {}).get('entries', []) if isinstance(evidence, dict) else []
subscription_by_exit = {}
subscription_exit_set = False
if isinstance(subscription_entries, list):
    try:
        for item in subscription_entries:
            exit_id = str(item['exit'])
            port = int(item['port'])
            protocol = str(item['protocol'])
            if exit_id in subscription_by_exit or not 0 < port <= 65535 or protocol not in allowed_protocols:
                raise ValueError
            subscription_by_exit[exit_id] = (port, protocol)
        subscription_exit_set = sorted(subscription_by_exit) == sorted(expected_exits)
    except (KeyError, TypeError, ValueError):
        subscription_by_exit = {}

isolation_rows = evidence.get('protocolIsolation', {}).get('exits', []) if isinstance(evidence, dict) else []
protocol_isolation = False
if isinstance(isolation_rows, list):
    try:
        isolation_by_exit = {}
        public_ports = set()
        mixed_ports = set()
        for item in isolation_rows:
            exit_id = str(item['exit'])
            public_port = int(item['publicPort'])
            mixed_port = int(item['mixedPort'])
            public_protocol = str(item['publicProtocol'])
            if exit_id in isolation_by_exit or public_protocol not in allowed_protocols or str(item['mixedProtocol']) != 'socks5h' or item['mixedInSubscription'] is not False:
                raise ValueError
            if not (0 < public_port <= 65535 and 0 < mixed_port <= 65535) or public_port == mixed_port:
                raise ValueError
            if subscription_by_exit.get(exit_id) != (public_port, public_protocol):
                raise ValueError
            isolation_by_exit[exit_id] = True
            public_ports.add(public_port)
            mixed_ports.add(mixed_port)
        protocol_isolation = sorted(isolation_by_exit) == sorted(expected_exits) and len(public_ports) == len(expected_exits) and len(mixed_ports) == len(expected_exits) and public_ports.isdisjoint(mixed_ports)
    except (KeyError, TypeError, ValueError):
        protocol_isolation = False

host_safety = False
try:
    before = evidence['hostSafety']['before']
    after = evidence['hostSafety']['after']
    hashes_valid = all(re.fullmatch(r'[0-9a-f]{64}', str(snapshot[key])) for snapshot in (before, after) for key in ('proxy', 'defaultRoute'))
    pids_valid = all(isinstance(pid, int) and pid >= 0 for snapshot in (before, after) for pid in snapshot['clientPids'])
    host_safety = hashes_valid and pids_valid and sorted(before['clientPids']) == sorted(after['clientPids']) and before['proxy'] == after['proxy'] and before['defaultRoute'] == after['defaultRoute']
except (KeyError, TypeError):
    pass

ready = all(services.values()) and all(enabled.values()) and all(listeners.values()) and database_readable and main_ready and slots_valid and evidence_schema and subscription_exit_set and protocol_isolation and host_safety and all(actual[key] == int(expected[key]) for key in ('openvpn','xray','logicalExits','exitSlots'))
report = {'nativeServices':services,'nativeEnabled':enabled,'expected':expected,'actual':actual,'listeners':listeners,'mainChecks':main_checks,'slotChecks':slot_checks,'databaseReadable':database_readable,'evidenceSchema':evidence_schema,'subscriptionExitSet':subscription_exit_set,'protocolIsolation':protocol_isolation,'hostSafety':host_safety,'nativeReady':ready}
print(json.dumps(report, separators=(',', ':')))
raise SystemExit(0 if ready else 1)
PY
