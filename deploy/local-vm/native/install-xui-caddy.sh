#!/usr/bin/env bash
set -euo pipefail
umask 077

mode=''
allowed_source=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check|--apply) mode="$1"; shift ;;
    --allowed-source) allowed_source="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done
[[ "$mode" == '--check' || "$mode" == '--apply' ]] || { printf 'mode_required\n' >&2; exit 2; }
[[ "$allowed_source" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { printf 'allowed_source_invalid\n' >&2; exit 3; }
[[ -f /etc/os-release ]] || { printf 'os_release_missing\n' >&2; exit 3; }
. /etc/os-release
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == '24.04' ]] || { printf 'unsupported_ubuntu\n' >&2; exit 3; }
[[ "$(uname -m)" == x86_64 ]] || { printf 'unsupported_architecture\n' >&2; exit 3; }
for command in curl sha256sum tar systemctl ss; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done
free_kb="$(df -Pk / | awk 'NR==2 {print $4}')"
[[ "$free_kb" =~ ^[0-9]+$ && "$free_kb" -ge 524288 ]] || { printf 'disk_floor_rejected\n' >&2; exit 3; }

network_probe="$(dirname "$0")/guest-network-preflight.sh"
[[ -f "$network_probe" ]] || { printf 'network_preflight_missing\n' >&2; exit 3; }
bash "$network_probe" --json >/dev/null || { printf 'network_preflight_failed\n' >&2; exit 4; }

if [[ "$mode" == '--check' ]]; then
  printf 'xui_caddy_check_ok\n'
  exit 0
fi
[[ "$EUID" -eq 0 ]] || { printf 'root_required\n' >&2; exit 3; }

if ! command -v caddy >/dev/null 2>&1; then
  apt-get update -qq
  apt-get install -y -qq caddy
fi

readonly XUI_VERSION='v3.7.0'
readonly XUI_COMMIT='f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
readonly XUI_INSTALL_SHA256='a7f4fedcea3abe8987508d00f29834b8872e4e4e5059159eb19460d474b37cdc'
work_dir="$(mktemp -d /var/tmp/aimili-xui-install.XXXXXX)"
trap 'rm -rf -- "$work_dir"' EXIT
install -d -m 0700 /etc/aimili-local
if [[ ! -x /usr/local/x-ui/x-ui ]]; then
  installer="$work_dir/x-ui-install.sh"
  curl -fsSL --connect-timeout 10 --max-time 60 "https://raw.githubusercontent.com/MHSanaei/3x-ui/${XUI_COMMIT}/install.sh" -o "$installer"
  [[ "$(sha256sum "$installer" | awk '{print $1}')" == "$XUI_INSTALL_SHA256" ]] || { printf 'xui_installer_digest_mismatch\n' >&2; exit 5; }
  chmod 0700 "$installer"
  bash "$installer" "$XUI_VERSION" >/var/log/aimili-local-xui-install.log 2>&1
fi
[[ -x /usr/local/x-ui/x-ui ]] || { printf 'xui_binary_missing\n' >&2; exit 5; }
if [[ ! -s /etc/aimili-local/xui-credentials ]]; then
  username="$(python3 -c 'import secrets; print("aimili" + secrets.token_hex(5))')"
  password="$(python3 -c 'import secrets; print(secrets.token_urlsafe(18))')"
  printf '%s\n%s\n' "$username" "$password" > /etc/aimili-local/xui-credentials
  chmod 0600 /etc/aimili-local/xui-credentials
  /usr/local/x-ui/x-ui setting -username "$username" -password "$password" -port 2001 -webBasePath /xui/ -listenIP 127.0.0.1 >/dev/null
else
  /usr/local/x-ui/x-ui setting -listenIP 127.0.0.1 >/dev/null
fi
systemctl enable --now x-ui.service >/dev/null
install -m 0644 "$(dirname "$0")/local-caddy.Caddyfile" /etc/caddy/Caddyfile
ufw allow from "$allowed_source" to any port 8080 proto tcp >/dev/null
ufw --force reload >/dev/null
systemctl enable caddy.service >/dev/null
caddy validate --config /etc/caddy/Caddyfile >/dev/null
systemctl restart caddy.service
printf 'xui_caddy_apply_ok\n'
