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
[[ "$slot" =~ ^[0-9]+$ && "$slot" -gt 0 ]] || { printf 'slot_invalid\n' >&2; exit 2; }
[[ -s "$manifest" ]] || { printf 'manifest_missing\n' >&2; exit 3; }
for command in python3 curl; do command -v "$command" >/dev/null 2>&1 || { printf 'dependency_missing:%s\n' "$command" >&2; exit 3; }; done
auth_file=/opt/aimilivpn/vpngate_data/ui_auth.json
slots_file=/opt/aimilivpn/vpngate_data/slots.json
[[ -s "$auth_file" ]] || { printf 'aimilivpn_auth_missing\n' >&2; exit 3; }
secret="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["secret_path"])' "$auth_file")"
response="$(curl -fsS --connect-timeout 5 --max-time 15 -X POST -H 'Content-Type: application/json' -d "{\"slot\":$slot}" "http://127.0.0.1:8787/$secret/api/start_slot")" || { printf 'slot_start_request_failed\n' >&2; exit 4; }
python3 -c 'import json,sys; raise SystemExit(0 if json.loads(sys.argv[1]).get("ok") else 1)' "$response" || { printf 'slot_start_rejected\n' >&2; exit 4; }
for _ in $(seq 1 90); do
  if [[ -s "$slots_file" ]] && python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); s=d.get(str(sys.argv[2]),{}) if isinstance(d,dict) else next((x for x in d if str(x.get("slot"))==sys.argv[2]),{}); raise SystemExit(0 if str(s.get("status","")).lower() in ("ready","active","running") else 1)' "$slots_file" "$slot"; then
    printf 'exit_slot_ready slot=%s\n' "$slot"
    exit 0
  fi
  sleep 2
done
printf 'exit_slot_timeout slot=%s\n' "$slot" >&2
exit 5
