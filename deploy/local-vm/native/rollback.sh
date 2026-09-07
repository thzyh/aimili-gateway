#!/usr/bin/env bash
set -euo pipefail
umask 077

component=''
run_id=''
backup_root="${AIMILI_BACKUP_ROOT:-/var/backups/aimili-local}"
requested_target=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --component) component="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --backup-root) backup_root="$2"; shift 2 ;;
    --target-dir) requested_target="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$component" =~ ^[a-z0-9-]{1,40}$ && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'rollback_identity_invalid\n' >&2; exit 2; }
[[ "$backup_root" = /* && "$backup_root" != */.. && "$backup_root" != */../* ]] || { printf 'backup_root_invalid\n' >&2; exit 2; }
native_root="${AIMILI_NATIVE_ROOT:-/}"
native_root="${native_root%/}"
[[ -n "$native_root" ]] || native_root='/'
python3 - "$component" "$run_id" "$backup_root" "$native_root" "$requested_target" <<'PY'
import json, os, pathlib, shutil, sys

component, run_id, backup_root, native_root, requested = sys.argv[1:]
allowed = {
    'aimilivpn': {'/opt/aimilivpn', '/etc/default/aimilivpn', '/etc/systemd/system/aimilivpn.service'},
    'xui-caddy': {'/usr/local/x-ui', '/etc/x-ui', '/etc/systemd/system/x-ui.service', '/etc/caddy', '/etc/systemd/system/caddy.service'},
    'gateway': {'/usr/local/bin/aimili-gateway', '/usr/local/bin/aimili-gateway-admin', '/etc/aimili-gateway', '/etc/credstore.encrypted/aimili-gateway-master-key', '/var/lib/aimili-gateway', '/etc/systemd/system/aimili-gateway.service'},
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
if requested and requested not in {item.get('path') for item in targets}:
    raise SystemExit('rollback_target_not_recorded')
root = pathlib.Path(native_root or '/')
for index, state in enumerate(targets):
    rel = state.get('path')
    if not isinstance(rel, str) or rel not in allowed or '..' in pathlib.PurePosixPath(rel).parts:
        raise SystemExit('rollback_target_invalid')
    target = root / rel.lstrip('/')
    entry = source / 'entries' / f'{index:03d}'
    entry_state = json.loads((entry / 'state.json').read_text(encoding='utf-8'))
    if entry_state != state:
        raise SystemExit('rollback_state_mismatch')
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
    payload = entry / 'payload'
    if state.get('kind') == 'directory':
        shutil.copytree(payload, target, symlinks=False)
        os.chmod(target, int(state['mode']))
        os.chown(target, int(state['uid']), int(state['gid']))
        for item in target.rglob('*'):
            if item.is_symlink(): raise SystemExit('rollback_symlink_rejected')
    elif state.get('kind') == 'file':
        shutil.copy2(payload, target, follow_symlinks=False)
        os.chmod(target, int(state['mode']))
        os.chown(target, int(state['uid']), int(state['gid']))
    else:
        raise SystemExit('rollback_kind_invalid')
print(root)
PY
systemctl daemon-reload >/dev/null 2>&1
