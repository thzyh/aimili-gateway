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
  aimilivpn) defaults=(/opt/aimilivpn /etc/default/aimilivpn /etc/systemd/system/aimilivpn.service) ;;
  xui-caddy) defaults=(/usr/local/x-ui /etc/x-ui /etc/systemd/system/x-ui.service /etc/caddy /etc/systemd/system/caddy.service) ;;
  gateway) defaults=(/usr/local/bin/aimili-gateway /usr/local/bin/aimili-gateway-admin /etc/aimili-gateway /etc/credstore.encrypted/aimili-gateway-master-key /var/lib/aimili-gateway /etc/systemd/system/aimili-gateway.service) ;;
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
import hashlib, json, os, pathlib, shutil, sys, tempfile

component, run_id, backup_root, native_root, *targets = sys.argv[1:]
allowed = {
    'aimilivpn': {'/opt/aimilivpn', '/etc/default/aimilivpn', '/etc/systemd/system/aimilivpn.service'},
    'xui-caddy': {'/usr/local/x-ui', '/etc/x-ui', '/etc/systemd/system/x-ui.service', '/etc/caddy', '/etc/systemd/system/caddy.service'},
    'gateway': {'/usr/local/bin/aimili-gateway', '/usr/local/bin/aimili-gateway-admin', '/etc/aimili-gateway', '/etc/credstore.encrypted/aimili-gateway-master-key', '/var/lib/aimili-gateway', '/etc/systemd/system/aimili-gateway.service'},
}[component]
if not targets or len(set(targets)) != len(targets) or any(t not in allowed for t in targets):
    raise SystemExit('backup_target_not_allowlisted')
root = pathlib.Path(native_root or '/')
backup = pathlib.Path(backup_root).resolve()
dest = backup / run_id / component
if dest.exists():
    raise SystemExit('backup_component_exists')
dest.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
tmp = pathlib.Path(tempfile.mkdtemp(prefix='.' + component + '.tmp.', dir=str(dest.parent)))
(tmp / 'entries').mkdir(mode=0o700)

def scan(path, base):
    result = []
    if path.is_file():
        result.append({'path': '.', 'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'mode': path.stat().st_mode & 0o7777, 'uid': path.stat().st_uid, 'gid': path.stat().st_gid})
    else:
        for item in sorted(path.rglob('*')):
            if item.is_symlink():
                raise SystemExit('backup_symlink_rejected')
            if item.is_file():
                rel = str(item.relative_to(base)).replace(os.sep, '/')
                st = item.stat()
                result.append({'path': rel, 'sha256': hashlib.sha256(item.read_bytes()).hexdigest(), 'mode': st.st_mode & 0o7777, 'uid': st.st_uid, 'gid': st.st_gid})
    return result

metadata = {'schemaVersion': 1, 'component': component, 'runId': run_id, 'targets': []}
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
