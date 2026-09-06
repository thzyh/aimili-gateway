#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
source_commit=''
local_installer=''
slot_count='3'
max_slots='16'
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --source-commit) source_commit="$2"; shift 2 ;;
    --installer) local_installer="$2"; shift 2 ;;
    --slot-count) slot_count="$2"; shift 2 ;;
    --max-slots) max_slots="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$source_commit" == '' || "$source_commit" =~ ^[0-9a-fA-F]{7,64}$ ]] || { printf 'source_commit_invalid\n' >&2; exit 2; }
[[ "$local_installer" == '' || ( "$local_installer" = /* && -f "$local_installer" ) ]] || { printf 'local_installer_invalid\n' >&2; exit 2; }
[[ "$slot_count" =~ ^[0-9]+$ && "$max_slots" =~ ^[0-9]+$ && "$slot_count" -le "$max_slots" ]] || { printf 'slot_count_invalid\n' >&2; exit 2; }

os_release="${AIMILI_OS_RELEASE:-/etc/os-release}"
if [[ ! -f "$os_release" ]]; then printf 'os_release_missing\n' >&2; exit 3; fi
. "$os_release"
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == '24.04' ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ -c /dev/net/tun ]] || { printf 'tun_missing\n' >&2; exit 3; }
for command in python3 ip curl git systemctl; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
openvpn_present=false
command -v openvpn >/dev/null 2>&1 && openvpn_present=true

network_probe="${AIMILI_NETWORK_PROBE:-$(dirname "$0")/guest-network-preflight.sh}"
if [[ -f "$network_probe" ]]; then
  network_json="$(bash "$network_probe" --json)" || { printf 'network_preflight_failed\n' >&2; printf '%s\n' "$network_json" >&2; exit 4; }
else
  printf 'network_preflight_missing\n' >&2
  exit 3
fi

if [[ "$mode" == '--check' ]]; then
  printf 'aimilivpn_check_ok slots=%s maxSlots=%s openvpn_present=%s\n' "$slot_count" "$max_slots" "$openvpn_present"
  exit 0
fi

[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }
[[ -n "$source_commit" ]] || { printf 'source_commit_required\n' >&2; exit 2; }
work_dir="$(mktemp -d /var/tmp/aimili-vpngate-install.XXXXXX)"
trap 'rm -rf -- "$work_dir"' EXIT
installer="$work_dir/install.sh"
if [[ -n "$local_installer" ]]; then
  install -m 0700 "$local_installer" "$installer"
else
  curl -fsSL --connect-timeout 10 --max-time 60 "https://raw.githubusercontent.com/thzyh/aimili-vpngate/${source_commit}/install.sh" -o "$installer"
fi
chmod 0700 "$installer"
install_log="$work_dir/install.log"
if ! bash "$installer" thzyh aimili-vpngate custom "$source_commit" >"$install_log" 2>&1; then
  printf 'aimilivpn_installer_failed\n' >&2
  exit 5
fi
command -v openvpn >/dev/null 2>&1 || { printf 'openvpn_missing_after_install\n' >&2; exit 5; }

env_file="${AIMILI_ENV_FILE:-/etc/default/aimilivpn}"
install -d -m 0700 "$(dirname "$env_file")"
touch "$env_file"
chmod 0600 "$env_file"
sed -i '/^MULTI_EXIT_SLOTS=/d;/^MAX_EXIT_SLOTS=/d' "$env_file"
printf 'MULTI_EXIT_SLOTS=%s\nMAX_EXIT_SLOTS=%s\n' "$slot_count" "$max_slots" >> "$env_file"
systemctl daemon-reload
systemctl enable aimilivpn.service >/dev/null
systemctl restart aimilivpn.service >/dev/null
printf 'aimilivpn_apply_ok\n'
