#!/bin/sh
set -eu
umask 077
state=/var/lib/aimili-gateway/freesub
url=https://raw.githubusercontent.com/thzyh/freesub-gateway/main/output/gateway-candidates.json
tmp="$state/.gateway-candidates.json.$$"
mkdir -p "$state"
trap 'rm -f "$tmp"' EXIT
curl --fail --silent --show-error --location --max-time 30 --retry 2 "$url" -o "$tmp"
test "$(wc -c < "$tmp")" -gt 32
/usr/bin/python3 - "$tmp" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1], encoding="utf-8"))
candidates = payload.get("candidates")
assert payload.get("schema_version") == 1 and isinstance(candidates, list)
identities = [item.get("candidate_id") for item in candidates]
assert all(
    isinstance(identity, str)
    and len(item.get("country", "")) == 2
    and item.get("country") == item.get("country", "").upper()
    and item.get("protocol") in {"vless", "vmess", "trojan", "shadowsocks"}
    and isinstance(item.get("config"), dict)
    for identity, item in zip(identities, candidates)
)
assert len(identities) == len(set(identities))
PY
chmod 0600 "$tmp"
mv -f "$tmp" "$state/gateway-candidates.json"
