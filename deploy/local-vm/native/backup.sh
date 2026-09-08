#!/usr/bin/env bash
set -euo pipefail
umask 077

component=''
run_id=''
backup_root="${AIMILI_BACKUP_ROOT:-/var/backups/aimili-local}"
source_dirs=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --component) component="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --backup-root) backup_root="$2"; shift 2 ;;
    --source-dir) source_dirs+=("$2"); shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$component" =~ ^[a-z0-9-]{1,40}$ && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'backup_identity_invalid\n' >&2; exit 2; }
[[ "$backup_root" = /* && "$backup_root" != */.. && "$backup_root" != */../* ]] || { printf 'backup_root_invalid\n' >&2; exit 2; }
native_root="${AIMILI_NATIVE_ROOT:-/}"
native_root="${native_root%/}"
[[ -n "$native_root" ]] || native_root='/'

case "$component" in
  manifest) defaults=(/etc/aimili-local/deployment.json) ;;
  aimilivpn) defaults=(/opt/aimilivpn /etc/default/aimilivpn /etc/systemd/system/aimilivpn.service) ;;
  xui-caddy) defaults=(/usr/local/x-ui /etc/x-ui /etc/systemd/system/x-ui.service /etc/caddy /etc/systemd/system/caddy.service /etc/aimili-local/xui-credentials.json /etc/aimili-local/xui-credentials.json.pending /etc/aimili-local/caddy-firewall.json /etc/aimili-local/xui-install.json /usr/local/share/ca-certificates/aimili-local-caddy.crt) ;;
  gateway) defaults=(/usr/local/bin/aimili-gateway /usr/local/bin/aimili-gateway-admin /usr/local/bin/aimili-xui-protocol-transaction /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py /etc/aimili-gateway /etc/credstore.encrypted/aimili-gateway-master-key /var/lib/aimili-gateway /var/lib/aimili-xui-protocol-transaction /etc/systemd/system/aimili-gateway.service /etc/systemd/system/aimili-xui-protocol-transaction.path /etc/systemd/system/aimili-xui-protocol-transaction.service /etc/systemd/system/aimili-xui-protocol-transaction.timer) ;;
  *) printf 'backup_component_unknown\n' >&2; exit 2 ;;
esac
targets=()
if [[ ${#source_dirs[@]} -eq 0 ]]; then
  targets=("${defaults[@]}")
else
    for source in "${source_dirs[@]}"; do
    [[ "$source" = /* ]] || { printf 'backup_source_path_invalid\n' >&2; exit 2; }
    if [[ "$native_root" == '/' ]]; then rel="$source"; else rel="${source#"$native_root"}"; fi
    [[ "$rel" != "$source" || "$native_root" == '/' ]] || { printf 'backup_source_root_mismatch\n' >&2; exit 2; }
    [[ "$rel" != "$native_root" && "$rel" != */.. && "$rel" != */../* ]] || { printf 'backup_source_path_invalid\n' >&2; exit 2; }
    targets+=("$rel")
  done
fi
python3 - "$component" "$run_id" "$backup_root" "$native_root" "${targets[@]}" <<'PY'
import hashlib, json, os, pathlib, shutil, subprocess, sys, tempfile

component, run_id, backup_root, native_root, *targets = sys.argv[1:]
allowed = {
    'manifest': {'/etc/aimili-local/deployment.json'},
    'aimilivpn': {'/opt/aimilivpn', '/etc/default/aimilivpn', '/etc/systemd/system/aimilivpn.service'},
    'xui-caddy': {'/usr/local/x-ui', '/etc/x-ui', '/etc/systemd/system/x-ui.service', '/etc/caddy', '/etc/systemd/system/caddy.service', '/etc/aimili-local/xui-credentials.json', '/etc/aimili-local/xui-credentials.json.pending', '/etc/aimili-local/caddy-firewall.json', '/etc/aimili-local/xui-install.json', '/usr/local/share/ca-certificates/aimili-local-caddy.crt'},
    'gateway': {'/usr/local/bin/aimili-gateway', '/usr/local/bin/aimili-gateway-admin', '/usr/local/bin/aimili-xui-protocol-transaction', '/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py', '/etc/aimili-gateway', '/etc/credstore.encrypted/aimili-gateway-master-key', '/var/lib/aimili-gateway', '/var/lib/aimili-xui-protocol-transaction', '/etc/systemd/system/aimili-gateway.service', '/etc/systemd/system/aimili-xui-protocol-transaction.path', '/etc/systemd/system/aimili-xui-protocol-transaction.service', '/etc/systemd/system/aimili-xui-protocol-transaction.timer'},
}[component]
unit_names = {
    'manifest': [],
    'aimilivpn': ['aimilivpn.service'],
    'xui-caddy': ['x-ui.service', 'caddy.service'],
    'gateway': ['aimili-gateway.service', 'aimili-xui-protocol-transaction.path', 'aimili-xui-protocol-transaction.service', 'aimili-xui-protocol-transaction.timer'],
}[component]
if not targets or len(set(targets)) != len(targets) or any(t not in allowed for t in targets):
    raise SystemExit('backup_target_not_allowlisted')
root = pathlib.Path(native_root or '/')
backup = pathlib.Path(backup_root).resolve()
dest = backup / run_id / component

def scan(path, base):
    result = []
    if path.is_file():
        result.append({'path': '.', 'kind': 'file', 'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'mode': path.stat().st_mode & 0o7777, 'uid': path.stat().st_uid, 'gid': path.stat().st_gid})
    else:
        for item in sorted(path.rglob('*')):
            if item.is_symlink():
                raise SystemExit('backup_symlink_rejected')
            rel = str(item.relative_to(base)).replace(os.sep, '/')
            st = item.stat()
            record = {'path': rel, 'kind': 'directory' if item.is_dir() else 'file', 'mode': st.st_mode & 0o7777, 'uid': st.st_uid, 'gid': st.st_gid}
            if item.is_file(): record['sha256'] = hashlib.sha256(item.read_bytes()).hexdigest()
            result.append(record)
    return result

def content_identity(records):
    return {(item.get('path'), item.get('kind'), item.get('sha256')) for item in records}

if dest.exists():
    metadata_path = dest / 'metadata.json'
    try:
        existing = json.loads(metadata_path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError):
        raise SystemExit('backup_component_exists_invalid')
    states = existing.get('targets')
    units = existing.get('units')
    if existing.get('schemaVersion') != 1 or existing.get('component') != component or existing.get('runId') != run_id or not isinstance(states, list) or not isinstance(units, dict) or set(units) != set(unit_names) or any(set(value) != {'active', 'enabled'} or not all(isinstance(flag, bool) for flag in value.values()) for value in units.values()):
        raise SystemExit('backup_component_exists_invalid')
    if [state.get('path') for state in states] != targets or len(states) != len(targets):
        raise SystemExit('backup_component_exists_invalid')
    entries = dest / 'entries'
    if not entries.is_dir() or {item.name for item in entries.iterdir()} != {f'{index:03d}' for index in range(len(states))}:
        raise SystemExit('backup_component_exists_invalid')
    for index, state in enumerate(states):
        entry = entries / f'{index:03d}'
        try:
            recorded = json.loads((entry / 'state.json').read_text(encoding='utf-8'))
            recorded_target = (entry / 'target').read_text(encoding='utf-8')
        except (OSError, json.JSONDecodeError):
            raise SystemExit('backup_component_exists_invalid')
        if recorded != state or recorded_target != state.get('path'):
            raise SystemExit('backup_component_exists_invalid')
        if state.get('exists'):
            payload = entry / 'payload'
            expected_files = state.get('files')
            if payload.is_symlink() or not payload.exists() or not isinstance(expected_files, list) or content_identity(scan(payload, payload)) != content_identity(expected_files):
                raise SystemExit('backup_component_exists_invalid')
    print(dest)
    raise SystemExit(0)
dest.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
tmp = pathlib.Path(tempfile.mkdtemp(prefix='.' + component + '.tmp.', dir=str(dest.parent)))
(tmp / 'entries').mkdir(mode=0o700)

metadata = {
    'schemaVersion': 1,
    'component': component,
    'runId': run_id,
    'targets': [],
    'units': {unit: {
        'active': subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode == 0,
        'enabled': subprocess.run(['systemctl', 'is-enabled', '--quiet', unit]).returncode == 0,
    } for unit in unit_names},
}
try:
    for index, rel in enumerate(targets):
        target = root / rel.lstrip('/')
        entry = tmp / 'entries' / f'{index:03d}'
        entry.mkdir(mode=0o700)
        (entry / 'target').write_text(rel, encoding='utf-8')
        if not target.exists():
            state = {'path': rel, 'exists': False}
            (entry / 'state.json').write_text(json.dumps(state, separators=(',', ':')), encoding='utf-8')
            metadata['targets'].append(state)
            continue
        if target.is_symlink():
            raise SystemExit('backup_symlink_rejected')
        st = target.stat()
        state = {'path': rel, 'exists': True, 'kind': 'directory' if target.is_dir() else 'file', 'mode': st.st_mode & 0o7777, 'uid': st.st_uid, 'gid': st.st_gid, 'files': scan(target, target)}
        payload = entry / 'payload'
        if target.is_dir(): shutil.copytree(target, payload, symlinks=False)
        else: shutil.copy2(target, payload, follow_symlinks=False)
        (entry / 'state.json').write_text(json.dumps(state, separators=(',', ':')), encoding='utf-8')
        metadata['targets'].append(state)
    (tmp / 'metadata.json').write_text(json.dumps(metadata, separators=(',', ':')), encoding='utf-8')
    os.replace(tmp, dest)
except BaseException:
    shutil.rmtree(tmp, ignore_errors=True)
    raise
print(dest)
PY
