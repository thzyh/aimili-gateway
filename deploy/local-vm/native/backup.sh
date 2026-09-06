#!/usr/bin/env bash
set -euo pipefail
umask 077

component=''
run_id=''
source_dir=''
backup_root=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --component) component="$2"; shift 2 ;;
    --run-id) run_id="$2"; shift 2 ;;
    --source-dir) source_dir="$2"; shift 2 ;;
    --backup-root) backup_root="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$component" =~ ^[a-z0-9-]{1,40}$ && "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { printf 'backup_identity_invalid\n' >&2; exit 2; }
[[ "$source_dir" = /* && -e "$source_dir" && "$backup_root" = /* ]] || { printf 'backup_path_invalid\n' >&2; exit 2; }
case "$source_dir" in /etc/aimili-local/*|/etc/aimili-gateway/*|/etc/systemd/system/aimili-*|/var/lib/aimili-gateway/*|/var/lib/aimili-local/*|/usr/local/bin/aimili-*) ;; *) printf 'backup_source_not_allowlisted\n' >&2; exit 2 ;; esac
install -d -m 0700 "$backup_root/$run_id"
dest="$backup_root/$run_id/$component"
tmp="$backup_root/$run_id/.${component}.tmp.$$"
case "$dest" in "$backup_root/$run_id"/*) ;; *) printf 'backup_path_escape\n' >&2; exit 2 ;; esac
[[ ! -e "$dest" ]] || { printf 'backup_component_exists\n' >&2; exit 3; }
install -d -m 0700 "$tmp"
cp -a "$source_dir"/. "$tmp"/
find "$source_dir" -printf '%P|%m|%u|%g|%s\n' | LC_ALL=C sort > "$tmp/file-manifest.txt"
printf '%s\n' "$component" > "$tmp/component"
mv -T "$tmp" "$dest"
printf '%s\n' "$dest"
