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
    --source-commit) [[ $# -ge 2 ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; source_commit="$2"; shift 2 ;;
    --installer) [[ $# -ge 2 ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; local_installer="$2"; shift 2 ;;
    --slot-count) [[ $# -ge 2 ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; slot_count="$2"; shift 2 ;;
    --max-slots) [[ $# -ge 2 ]] || { printf 'argument_value_missing\n' >&2; exit 2; }; max_slots="$2"; shift 2 ;;
    *) printf 'unknown_argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$source_commit" == '' || "$source_commit" =~ ^[0-9a-fA-F]{7,64}$ ]] || { printf 'source_commit_invalid\n' >&2; exit 2; }
[[ "$local_installer" == '' || ( "$local_installer" = /* && -f "$local_installer" ) ]] || { printf 'local_installer_invalid\n' >&2; exit 2; }
[[ "$slot_count" =~ ^[0-9]+$ && "$max_slots" =~ ^[0-9]+$ && "$slot_count" -le "$max_slots" ]] || { printf 'slot_count_invalid\n' >&2; exit 2; }

os_release="${AIMILI_OS_RELEASE:-/etc/os-release}"
tun_path="${AIMILI_TUN_PATH:-/dev/net/tun}"
disk_path="${AIMILI_DISK_PATH:-/opt}"
[[ -e "$disk_path" ]] || disk_path='/'
os_id='unknown'
os_version='unknown'
if [[ -f "$os_release" ]]; then
  . "$os_release"
  os_id="${ID:-unknown}"
  os_version="${VERSION_ID:-unknown}"
fi
os_supported=false
[[ "$os_id" == ubuntu && "$os_version" == '24.04' ]] && os_supported=true
tun_present=false
[[ -c "$tun_path" ]] && tun_present=true
python_present=false
command -v python3 >/dev/null 2>&1 && python_present=true
disk_free_mib="$(df -Pm "$disk_path" 2>/dev/null | awk 'NR==2 {print $4}')"
[[ "$disk_free_mib" =~ ^[0-9]+$ ]] || disk_free_mib=0
service_status="$(systemctl is-active aimilivpn.service 2>/dev/null || true)"
case "$service_status" in active|activating|inactive|deactivating|failed) ;; *) service_status=unknown ;; esac
openvpn_count="$(pgrep -cx openvpn 2>/dev/null || true)"
[[ "$openvpn_count" =~ ^[0-9]+$ ]] || openvpn_count=0
expected_openvpn=$((slot_count + 1))
openvpn_matches=false
[[ "$openvpn_count" -eq "$expected_openvpn" ]] && openvpn_matches=true

if [[ "$mode" == '--check' ]]; then
  printf '{"mode":"check","os":"%s-%s","osSupported":%s,"tunPresent":%s,"pythonPresent":%s,"diskFreeMiB":%s,"serviceStatus":"%s","openvpnCount":%s,"expectedOpenvpn":%s,"openvpnMatchesExpected":%s}\n' \
    "$os_id" "$os_version" "$os_supported" "$tun_present" "$python_present" "$disk_free_mib" "$service_status" "$openvpn_count" "$expected_openvpn" "$openvpn_matches"
  [[ "$os_supported" == true ]] || exit 3
  [[ "$tun_present" == true ]] || exit 3
  [[ "$python_present" == true ]] || exit 3
fi

for required in python3 ip curl systemctl; do
  command -v "$required" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$required" >&2; exit 3; }
done
[[ "$os_supported" == true ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ "$tun_present" == true ]] || { printf 'tun_missing\n' >&2; exit 3; }

network_probe="${AIMILI_NETWORK_PROBE:-$(dirname "$0")/guest-network-preflight.sh}"
if [[ -f "$network_probe" ]]; then
  network_json="$(bash "$network_probe" --json)" || { printf 'network_preflight_failed\n' >&2; printf '%s\n' "$network_json" >&2; exit 4; }
else
  printf 'network_preflight_missing\n' >&2
  exit 3
fi
[[ "$mode" == '--check' ]] && exit 0

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
apt_config="$work_dir/apt.conf"
printf '%s\n' 'DPkg::Lock::Timeout "120";' > "$apt_config"
chmod 0600 "$apt_config"
if ! APT_CONFIG="$apt_config" bash "$installer" thzyh aimili-vpngate custom "$source_commit" >"$work_dir/install.log" 2>&1; then
  printf 'aimilivpn_installer_failed\n' >&2
  exit 5
fi
command -v openvpn >/dev/null 2>&1 || { printf 'openvpn_missing_after_install\n' >&2; exit 5; }

env_file="${AIMILI_ENV_FILE:-/etc/default/aimilivpn}"
install -d -m 0700 "$(dirname "$env_file")"
touch "$env_file"
chmod 0600 "$env_file"
sed -i '/^MULTI_EXIT_SLOTS=/d;/^MAX_EXIT_SLOTS=/d;/^TARGET_VALID_POOL_SIZE=/d;/^UI_HOST=/d' "$env_file"
printf 'MULTI_EXIT_SLOTS=%s\nMAX_EXIT_SLOTS=%s\nTARGET_VALID_POOL_SIZE=50\nUI_HOST=127.0.0.1\n' "$slot_count" "$max_slots" >> "$env_file"
ui_config="${AIMILI_UI_CONFIG:-/opt/aimilivpn/vpngate_data/ui_auth.json}"
AIMILI_SLOT_COUNT="$slot_count" python3 - "$ui_config" <<'PY'
import json, os, pathlib, tempfile, sys

path = pathlib.Path(sys.argv[1])
if path.is_symlink() or not path.is_file():
    raise SystemExit('aimilivpn_ui_config_missing')
document = json.loads(path.read_text(encoding='utf-8'))
if not isinstance(document, dict):
    raise SystemExit('aimilivpn_ui_config_invalid')
state = path.stat()
document['host'] = '127.0.0.1'
document['exit_slot_active'] = list(range(int(os.environ.get('AIMILI_SLOT_COUNT', '0'))))
document['exit_slot_count'] = len(document['exit_slot_active'])
document['exit_slot_paused'] = [item for item in document.get('exit_slot_paused', []) if item in document['exit_slot_active']]
descriptor, temporary = tempfile.mkstemp(prefix='.' + path.name + '.', dir=str(path.parent))
try:
    with os.fdopen(descriptor, 'w', encoding='utf-8') as output:
        json.dump(document, output, separators=(',', ':'))
        output.flush()
        os.fsync(output.fileno())
    os.chmod(temporary, state.st_mode & 0o7777)
    os.chown(temporary, state.st_uid, state.st_gid)
    os.replace(temporary, path)
finally:
    try:
        os.unlink(temporary)
    except FileNotFoundError:
        pass
PY
systemctl daemon-reload
systemctl enable aimilivpn.service >/dev/null
systemctl restart aimilivpn.service >/dev/null
printf 'aimilivpn_apply_ok\n'
