#!/usr/bin/env bash
set -euo pipefail
umask 077

component=''
run_id=''
backup_root="${AIMILI_BACKUP_ROOT:-/var/backups/aimili-local}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --component) component="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --backup-root) backup_root="$2"; shift 2 ;;
    --target-dir) printf 'rollback_target_argument_forbidden\n' >&2; exit 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$component" =~ ^[a-z0-9-]{1,40}$ && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'rollback_identity_invalid\n' >&2; exit 2; }
[[ "$backup_root" = /* && "$backup_root" != */.. && "$backup_root" != */../* ]] || { printf 'backup_root_invalid\n' >&2; exit 2; }
native_root="${AIMILI_NATIVE_ROOT:-/}"
native_root="${native_root%/}"
[[ -n "$native_root" ]] || native_root='/'
python3 - "$component" "$run_id" "$backup_root" "$native_root" <<'PY'
import json, os, pathlib, shutil, subprocess, sys

component, run_id, backup_root, native_root = sys.argv[1:]
allowed = {
    'manifest': {'/etc/aimili-local/deployment.json'},
    'aimilivpn': {'/opt/aimilivpn', '/etc/default/aimilivpn', '/etc/systemd/system/aimilivpn.service'},
    'xui-caddy': {'/usr/local/x-ui', '/etc/x-ui', '/etc/systemd/system/x-ui.service', '/etc/caddy', '/etc/systemd/system/caddy.service', '/etc/aimili-local/xui-credentials.json', '/etc/aimili-local/xui-credentials.json.pending', '/etc/aimili-local/caddy-firewall.json', '/etc/aimili-local/xui-install.json', '/usr/local/share/ca-certificates/aimili-local-caddy.crt'},
    'gateway': {'/usr/local/bin/aimili-gateway', '/usr/local/bin/aimili-gateway-admin', '/usr/local/sbin/aimili-gateway-account', '/usr/local/bin/aimili-xui-protocol-transaction', '/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py', '/etc/aimili-gateway', '/etc/credstore.encrypted/aimili-gateway-master-key', '/var/lib/aimili-gateway', '/var/lib/aimili-xui-protocol-transaction', '/etc/systemd/system/aimili-gateway.service', '/etc/systemd/system/aimili-xui-protocol-transaction.path', '/etc/systemd/system/aimili-xui-protocol-transaction.service', '/etc/systemd/system/aimili-xui-protocol-transaction.timer'},
}[component]
unit_names = {
    'manifest': [],
    'aimilivpn': ['aimilivpn.service'],
    'xui-caddy': ['x-ui.service', 'caddy.service'],
    'gateway': ['aimili-gateway.service', 'aimili-xui-protocol-transaction.path', 'aimili-xui-protocol-transaction.service', 'aimili-xui-protocol-transaction.timer'],
}[component]
backup = pathlib.Path(backup_root).resolve()
source = backup / run_id / component
metadata_path = source / 'metadata.json'
if not metadata_path.is_file():
    raise SystemExit('rollback_backup_missing')
with metadata_path.open(encoding='utf-8') as handle:
    metadata = json.load(handle)
if metadata.get('schemaVersion') != 1 or metadata.get('component') != component or metadata.get('runId') != run_id:
    raise SystemExit('rollback_metadata_invalid')
targets = metadata.get('targets')
if not isinstance(targets, list) or len({item.get('path') for item in targets}) != len(targets):
    raise SystemExit('rollback_targets_invalid')
units = metadata.get('units')
if not isinstance(units, dict) or set(units) != set(unit_names) or any(set(value) != {'active', 'enabled'} or not all(isinstance(flag, bool) for flag in value.values()) for value in units.values()):
    raise SystemExit('rollback_units_invalid')
root = pathlib.Path(native_root or '/')
def firewall_policy(path):
    try:
        document = json.loads(path.read_text(encoding='utf-8'))
        value = document.get('allowedSource', '')
        import ipaddress
        if ipaddress.ip_address(value).version != 4:
            return []
        rules = document.get('rules')
        if rules is None:
            rules = [{'port': 8080, 'proto': 'tcp'}]
        result = []
        for rule in rules:
            port, protocol = int(rule['port']), str(rule['proto'])
            if not 0 < port <= 65535 or protocol not in ('tcp', 'udp') or (value, port, protocol) in result:
                return []
            result.append((value, port, protocol))
        return result
    except (OSError, ValueError, TypeError, json.JSONDecodeError):
        return []
def restore_firewall(current, previous):
    if current == previous:
        return
    def command(action, rule, check):
        source, port, protocol = rule
        arguments = ['ufw'] + ([action] if action else []) + ['allow', 'from', source, 'to', 'any', 'port', str(port), 'proto', protocol]
        return subprocess.run(arguments, check=check, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for rule in current:
            command('delete', rule, True)
        for rule in previous:
            command('', rule, True)
    except (OSError, subprocess.CalledProcessError):
        recovered = True
        for rule in previous:
            try: recovered = command('delete', rule, False).returncode == 0 and recovered
            except OSError: recovered = False
        for rule in current:
            try: recovered = command('', rule, False).returncode == 0 and recovered
            except OSError: recovered = False
        if not recovered:
            raise SystemExit('rollback_firewall_recovery_failed')
        raise SystemExit('rollback_firewall_command_failed')
def validate_payload(payload, expected):
    import hashlib
    if payload.is_symlink() or not payload.exists():
        raise SystemExit('rollback_payload_invalid')
    records = []
    if payload.is_file():
        records.append({'path': '.', 'kind': 'file', 'sha256': hashlib.sha256(payload.read_bytes()).hexdigest()})
    else:
        for item in sorted(payload.rglob('*')):
            if item.is_symlink():
                raise SystemExit('rollback_payload_invalid')
            rel = str(item.relative_to(payload)).replace(os.sep, '/')
            record = {'path': rel, 'kind': 'directory' if item.is_dir() else 'file'}
            if item.is_file():
                record['sha256'] = hashlib.sha256(item.read_bytes()).hexdigest()
            records.append(record)
    actual = {(item['path'], item['kind'], item.get('sha256')) for item in records}
    wanted = {(item['path'], item['kind'], item.get('sha256')) for item in expected}
    if actual != wanted:
        raise SystemExit('rollback_payload_digest_mismatch')
validated = []
for index, state in enumerate(targets):
    rel = state.get('path')
    if not isinstance(rel, str) or rel not in allowed or '..' in pathlib.PurePosixPath(rel).parts:
        raise SystemExit('rollback_target_invalid')
    target = root / rel.lstrip('/')
    entry = source / 'entries' / f'{index:03d}'
    entry_state = json.loads((entry / 'state.json').read_text(encoding='utf-8'))
    if entry_state != state:
        raise SystemExit('rollback_state_mismatch')
    payload = None
    if state.get('exists'):
        if state.get('kind') not in ('file', 'directory') or not all(isinstance(state.get(key), int) and state.get(key) >= 0 for key in ('mode', 'uid', 'gid')) or not isinstance(state.get('files'), list):
            raise SystemExit('rollback_state_invalid')
        payload = entry / 'payload'
        validate_payload(payload, state['files'])
    validated.append((rel, state, target, payload))
for rel, state, target, payload in validated:
    is_firewall = rel == '/etc/aimili-local/caddy-firewall.json'
    current_firewall = firewall_policy(target) if is_firewall else []
    restored_firewall = firewall_policy(payload) if is_firewall and payload is not None else []
    if is_firewall:
        restore_firewall(current_firewall, restored_firewall)
    if not state.get('exists'):
        if target.exists() or target.is_symlink():
            if target.is_dir() and not target.is_symlink(): shutil.rmtree(target)
            else: target.unlink()
        continue
    previous = pathlib.Path(str(target) + '.previous')
    if previous.exists() or previous.is_symlink():
        if previous.is_dir() and not previous.is_symlink(): shutil.rmtree(previous)
        else: previous.unlink()
    if target.exists() or target.is_symlink():
        os.replace(target, previous)
    if state.get('kind') == 'directory':
        shutil.copytree(payload, target, symlinks=False)
        os.chmod(target, int(state['mode']))
        os.chown(target, int(state['uid']), int(state['gid']))
        for record in state.get('files', []):
            if record.get('path') == '.': continue
            item = target.joinpath(*record['path'].split('/'))
            if item.is_symlink() or not item.exists(): raise SystemExit('rollback_payload_invalid')
            os.chmod(item, int(record['mode']))
            os.chown(item, int(record['uid']), int(record['gid']))
    elif state.get('kind') == 'file':
        shutil.copy2(payload, target, follow_symlinks=False)
        os.chmod(target, int(state['mode']))
        os.chown(target, int(state['uid']), int(state['gid']))
    else:
        raise SystemExit('rollback_kind_invalid')
subprocess.run(['systemctl', 'daemon-reload'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
if component == 'xui-caddy':
    subprocess.run(['update-ca-certificates', '--fresh'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
for unit in unit_names:
    state = units[unit]
    if state['enabled']:
        subprocess.run(['systemctl', 'enable', unit], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        subprocess.run(['systemctl', 'disable', unit], check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if subprocess.run(['systemctl', 'is-enabled', '--quiet', unit]).returncode == 0:
            raise SystemExit('rollback_unit_enable_state_failed')
    if state['active']:
        subprocess.run(['systemctl', 'restart', unit], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        subprocess.run(['systemctl', 'stop', unit], check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode == 0:
            raise SystemExit('rollback_unit_active_state_failed')
print(root)
PY
