#!/usr/bin/env bash
set -u

json=0
if [[ "${1:-}" == "--json" ]]; then
  json=1
fi

gateway="$(ip route show default 2>/dev/null | awk 'NR==1 {print $3}')"
gateway_reachable=false
public_tcp443=false
dns_resolution=false
https_reachable=false
ufw_outgoing_allowed=false
failure_boundary=route

if [[ -n "$gateway" ]]; then
  failure_boundary=gateway
  if ping -c 1 -W 2 "$gateway" >/dev/null 2>&1; then
    gateway_reachable=true
    failure_boundary=upstream_tcp
    if python3 -c 'import socket; s=socket.create_connection(("1.1.1.1", 443), 3); s.close()' >/dev/null 2>&1; then
      public_tcp443=true
      failure_boundary=dns
      if getent ahostsv4 archive.ubuntu.com >/dev/null 2>&1; then
        dns_resolution=true
        failure_boundary=https
        if curl -4 -fsS --connect-timeout 5 --max-time 10 -o /dev/null https://archive.ubuntu.com/ubuntu/; then
          https_reachable=true
          failure_boundary=ufw
        fi
      fi
    fi
  fi
fi

if command -v ufw >/dev/null 2>&1; then
  if sudo -n ufw status verbose 2>/dev/null | grep -Fqi 'allow (outgoing)'; then
    ufw_outgoing_allowed=true
  fi
fi

if [[ "$gateway_reachable" == true && "$public_tcp443" == true && "$dns_resolution" == true && "$https_reachable" == true && "$ufw_outgoing_allowed" == true ]]; then
  failure_boundary=none
fi

if (( json )); then
  printf '{"gatewayReachable":%s,"publicTcp443":%s,"dnsResolution":%s,"httpsReachable":%s,"ufwOutgoingAllowed":%s,"failureBoundary":"%s"}\n' \
    "$gateway_reachable" "$public_tcp443" "$dns_resolution" "$https_reachable" "$ufw_outgoing_allowed" "$failure_boundary"
else
  printf 'gatewayReachable=%s publicTcp443=%s dnsResolution=%s httpsReachable=%s ufwOutgoingAllowed=%s failureBoundary=%s\n' \
    "$gateway_reachable" "$public_tcp443" "$dns_resolution" "$https_reachable" "$ufw_outgoing_allowed" "$failure_boundary"
fi

[[ "$failure_boundary" == none ]]
