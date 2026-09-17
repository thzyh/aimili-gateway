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
assert payload.get("schema_version") == 2 and isinstance(candidates, list)
identities = [item.get("candidate_id") for item in candidates]
assert all(
    isinstance(identity, str)
    and len(item.get("country", "")) == 2
    and item.get("country") == item.get("country", "").upper()
    and item.get("protocol") in {"vless", "vmess", "trojan", "shadowsocks"}
    and item.get("network_type") in {"residential", "mobile"}
    and isinstance(item.get("risk_score"), int)
    and 0 <= item.get("risk_score") <= 15
    and item.get("quality", {}).get("source") == "ping0"
    and item.get("quality", {}).get("risk_score") == item.get("risk_score")
    and item.get("quality", {}).get("native_ip") is True
    and item.get("quality", {}).get("native_label") == "原生 IP"
    and all(
        isinstance(item.get("quality", {}).get("scenario_stars", {}).get(scene), int)
        and item.get("quality", {}).get("scenario_stars", {}).get(scene) >= 4
        for scene in {"tiktok", "cross_border_ecommerce", "social_media", "ai"}
    )
    and isinstance(item.get("config"), dict)
    for identity, item in zip(identities, candidates)
)
assert len(identities) == len(set(identities))
PY
chmod 0600 "$tmp"
mv -f "$tmp" "$state/gateway-candidates.json"
