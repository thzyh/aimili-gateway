#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# -eq 2 && "$1" != --* ]]; then
  manifest="$1"
  run_id="$2"
else
  manifest=''
  run_id=''
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --manifest) manifest="$2"; shift 2 ;;
      --run-id) run_id="$2"; shift 2 ;;
      *) printf 'unknown argument\n' >&2; exit 2 ;;
    esac
  done
fi
[[ "$manifest" = /* && -f "$manifest" ]] || { printf 'manifest_invalid\n' >&2; exit 2; }
[[ "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'run_id_invalid\n' >&2; exit 2; }
staging_root="${AIMILI_STAGING_ROOT:-/var/lib/aimili-local/staging}"
[[ "$staging_root" = /* && "$staging_root" != */.. && "$staging_root" != */../* ]] || { printf 'staging_root_invalid\n' >&2; exit 2; }
install -d -m 0700 "$staging_root"
python3 - "$manifest" "$run_id" "$staging_root" <<'PY'
import hashlib, json, os, pathlib, re, shutil, sys

manifest_path, run_id, staging_root = sys.argv[1:]
with open(manifest_path, encoding='utf-8') as handle:
    manifest = json.load(handle)
if set(manifest) != {'schemaVersion', 'files'} or manifest.get('schemaVersion') != 1 or not isinstance(manifest['files'], list):
    raise SystemExit('manifest_schema_invalid')
source_root = pathlib.Path(manifest_path).resolve().parent
entries = []
seen = set()
for item in manifest['files']:
    if not isinstance(item, dict) or set(item) != {'path', 'sha256'}:
        raise SystemExit('manifest_entry_invalid')
    rel = item['path']
    digest = item['sha256']
    if not isinstance(rel, str) or not rel or rel.startswith('/') or '\\' in rel or any(part in ('', '.', '..') for part in rel.split('/')):
        raise SystemExit('manifest_path_invalid')
    if rel in seen:
        raise SystemExit('manifest_duplicate_path')
    if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-fA-F]{64}', digest):
        raise SystemExit('manifest_digest_invalid')
    seen.add(rel)
    source = source_root.joinpath(*rel.split('/'))
    if source.is_symlink() or not source.is_file():
        raise SystemExit('manifest_source_not_regular')
    resolved_source = source.resolve()
    try:
        resolved_source.relative_to(source_root)
    except ValueError:
        raise SystemExit('manifest_path_escape')
    if resolved_source != source:
        raise SystemExit('manifest_symlink_rejected')
    actual = hashlib.sha256(source.read_bytes()).hexdigest()
    if actual.lower() != digest.lower():
        raise SystemExit('manifest_digest_mismatch')
    entries.append((rel, source, digest))

root = pathlib.Path(staging_root).resolve()
dest = root / run_id
if dest.exists():
    raise SystemExit('staging_run_exists')
tmp = root / ('.' + run_id + '.tmp.' + str(os.getpid()))
if tmp.exists():
    raise SystemExit('staging_temp_exists')
tmp.mkdir(mode=0o700)
try:
    for rel, source, digest in entries:
        target = tmp.joinpath(*rel.split('/'))
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copy2(source, target, follow_symlinks=False)
        if target.is_symlink() or not target.is_file():
            raise SystemExit('staging_target_not_regular')
        if hashlib.sha256(target.read_bytes()).hexdigest().lower() != digest.lower():
            raise SystemExit('staging_copy_digest_mismatch')
        os.chmod(target, source.stat().st_mode & 0o777)
    with open(tmp / 'manifest.json', 'w', encoding='utf-8') as handle:
        json.dump(manifest, handle, separators=(',', ':'))
    os.chmod(tmp / 'manifest.json', 0o600)
    if dest.exists():
        raise SystemExit('staging_run_exists')
    os.replace(tmp, dest)
except BaseException:
    shutil.rmtree(tmp, ignore_errors=True)
    raise
print(dest)
PY
