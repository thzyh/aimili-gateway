#!/usr/bin/env bash
set -euo pipefail
umask 077

slot=''
manifest='/etc/aimili-local/deployment.json'
while [[ $# -gt 0 ]]; do
  case "$1" in
    --slot) slot="$2"; shift 2 ;;
    --manifest) manifest="$2"; shift 2 ;;
    *) printf 'unknown argument\n' >&2; exit 2 ;;
  esac
done

[[ "$slot" =~ ^[0-9]+$ ]] || { printf 'slot_invalid\n' >&2; exit 2; }
[[ -s "$manifest" ]] || { printf 'manifest_missing\n' >&2; exit 3; }
max_slots="$(python3 -c 'import json,sys; print(int(json.load(open(sys.argv[1]))["expected"]["exitSlots"]))' "$manifest")"
[[ "$slot" -lt "$max_slots" ]] || { printf 'slot_not_declared\n' >&2; exit 2; }
for command in python3 curl ip ss; do
  command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }
done

slots_file="${AIMILI_SLOTS_FILE:-/opt/aimilivpn/vpngate_data/slots.json}"
wait_attempts="${AIMILI_SLOT_WAIT_ATTEMPTS:-90}"
wait_interval="${AIMILI_SLOT_WAIT_INTERVAL:-2}"
table_base="${AIMILI_SLOT_TABLE_BASE:-200}"
[[ "$wait_attempts" =~ ^[1-9][0-9]*$ && "$table_base" =~ ^[0-9]+$ ]] || { printf 'verification_config_invalid\n' >&2; exit 2; }
link_has_up_flag() {
  local state="$1" flags
  [[ "$state" == *'<'*'>'* ]] || return 1
  flags="${state#*<}"
  flags="${flags%%>*}"
  [[ ",$flags," == *,UP,* ]]
}

for _ in $(seq 1 "$wait_attempts"); do
  slot_state=''
  if [[ -s "$slots_file" ]]; then
    slot_state="$(python3 - "$slots_file" "$slot" <<'PY'
import json, sys
document = json.load(open(sys.argv[1], encoding='utf-8'))
items = document.get('slots', []) if isinstance(document, dict) else []
item = next((row for row in items if isinstance(row, dict) and str(row.get('slot')) == sys.argv[2]), None)
if item is None or str(item.get('status', '')).lower() not in ('ready', 'up') or item.get('egress_ok') is not True:
    raise SystemExit(1)
device = str(item.get('device') or '')
port = int(item.get('port') or 0)
if not device or port < 1 or port > 65535:
    raise SystemExit(1)
print(device)
print(port)
PY
)" || slot_state=''
  fi
  if [[ -n "$slot_state" ]]; then
    mapfile -t fields <<< "$slot_state"
    device="${fields[0]:-}"
    port="${fields[1]:-0}"
    route_table=$((table_base + slot))
    link_state="$(ip -o link show dev "$device" 2>/dev/null || true)"
    route_state="$(ip route show table "$route_table" 2>/dev/null || true)"
    if [[ "$device" =~ ^[A-Za-z0-9_.:-]+$ ]] && link_has_up_flag "$link_state" &&
       awk -v device="$device" '$1 == "default" { for (i=2; i<=NF; i++) if ($i == "dev" && $(i+1) == device) found=1 } END { exit(found ? 0 : 1) }' <<< "$route_state" &&
       ss -lntH 2>/dev/null | awk -v port="$port" '{ address=$4; if (address ~ ":" port "$") { if (address ~ "^127\\.0\\.0\\.1:" port "$" || address == "[::1]:" port) found=1; else bad=1 } } END { exit(found && !bad ? 0 : 1) }' &&
       exit_ip="$(curl -fsS --socks5-hostname "127.0.0.1:$port" --max-time 10 http://api.ipify.org 2>/dev/null)" &&
       python3 -c 'import ipaddress,sys; ipaddress.ip_address(sys.argv[1])' "$exit_ip" >/dev/null 2>&1; then
      printf 'exit_slot_ready slot=%s\n' "$slot"
      exit 0
    fi
  fi
  sleep "$wait_interval"
done
printf 'exit_slot_timeout slot=%s\n' "$slot" >&2
exit 5
