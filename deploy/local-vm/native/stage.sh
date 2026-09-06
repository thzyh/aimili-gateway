#!/usr/bin/env bash
set -euo pipefail
umask 077

source_dir=''
staging_root=''
run_id=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source-dir) source_dir="$2"; shift 2 ;;
    --staging-root) staging_root="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ -d "$source_dir" && "$source_dir" = /* ]] || { printf 'source_dir_invalid\n' >&2; exit 2; }
[[ "$staging_root" = /* && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'staging_target_invalid\n' >&2; exit 2; }
install -d -m 0700 "$staging_root"
dest="$staging_root/$run_id"
tmp="$staging_root/.${run_id}.tmp.$$"
case "$dest" in "$staging_root"/*) ;; *) printf 'staging_path_escape\n' >&2; exit 2 ;; esac
[[ ! -e "$dest" ]] || { printf 'staging_run_exists\n' >&2; exit 3; }
install -d -m 0700 "$tmp"
cp -a "$source_dir"/. "$tmp"/
mv -T "$tmp" "$dest"
printf '%s\n' "$dest"
