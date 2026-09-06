#!/usr/bin/env bash
set -euo pipefail
umask 077

component=''
run_id=''
backup_root=''
target_dir=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --component) component="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --backup-root) backup_root="$2"; shift 2 ;;
    --target-dir) target_dir="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$component" =~ ^[a-z0-9-]{1,40}$ && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'rollback_identity_invalid\n' >&2; exit 2; }
[[ "$backup_root" = /* && "$target_dir" = /* ]] || { printf 'rollback_path_invalid\n' >&2; exit 2; }
source_dir="$backup_root/$run_id/$component"
[[ -d "$source_dir" ]] || { printf 'rollback_backup_missing\n' >&2; exit 3; }
previous="${target_dir}.previous"
case "$source_dir" in "$backup_root"/*) ;; *) printf 'rollback_source_escape\n' >&2; exit 2 ;; esac
install -d -m 0700 "$(dirname "$target_dir")"
tmp="${target_dir}.restore.$$"
install -d -m 0700 "$tmp"
cp -a "$source_dir"/. "$tmp"/
if [[ -e "$target_dir" ]]; then
  [[ "$target_dir" = /* && "$target_dir" != '/' ]] || { printf 'rollback_target_invalid\n' >&2; exit 2; }
  rm -rf -- "$previous"
  mv -T "$target_dir" "$previous"
fi
mv -T "$tmp" "$target_dir"
printf '%s\n' "$target_dir"
