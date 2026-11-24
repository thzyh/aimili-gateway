#!/usr/bin/env python3
"""ny 的只读连接跟踪采样，依赖 Linux 和 Python 标准库。"""

import argparse
from collections import Counter, defaultdict
import ipaddress
import json
from pathlib import Path
import socket
import struct
import subprocess
import time


SYSCTL = Path("/proc/sys/net/netfilter")
STATE = Path("/run/aimili-conntrack-watch.json")
HEADER = struct.Struct("=IHHII")
ATTR = struct.Struct("=HH")


def attributes(data):
    result = {}
    while len(data) >= ATTR.size:
        size, kind = ATTR.unpack_from(data)
        if size < ATTR.size or size > len(data):
            raise ValueError("invalid netlink attribute")
        result[kind & 0x3FFF] = data[ATTR.size:size]
        data = data[(size + 3) & ~3:]
    return result


def conntrack_messages(command):
    # NETLINK_NETFILTER, NFNL_SUBSYS_CTNETLINK, NLM_F_REQUEST|NLM_F_DUMP.
    with socket.socket(socket.AF_NETLINK, socket.SOCK_RAW, 12) as netlink:
        netlink.settimeout(2)
        netlink.send(HEADER.pack(20, (1 << 8) | command, 0x301, 1, 0) + bytes(4))
        while True:
            data = netlink.recv(65536)
            while data:
                size, kind, _, _, _ = HEADER.unpack_from(data)
                if size < HEADER.size or size > len(data):
                    raise ValueError("invalid netlink message")
                if kind == 3:  # NLMSG_DONE
                    return
                if kind == 2:  # NLMSG_ERROR
                    error, = struct.unpack_from("=i", data, HEADER.size)
                    if error:
                        raise OSError(-error, "conntrack netlink")
                else:
                    yield attributes(data[20:size])  # Skip the four-byte nfgenmsg.
                data = data[(size + 3) & ~3:]


def number(data, attribute):
    return int.from_bytes(data.get(attribute, b""), "big")


def snapshot():
    interfaces = {}
    for link in json.loads(subprocess.check_output(["/usr/sbin/ip", "-j", "address"], timeout=2)):
        for address in link.get("addr_info", []):
            interfaces[ipaddress.ip_address(address["local"]).packed] = link["ifname"]
    counts = Counter()
    ports = defaultdict(Counter)
    unreplied = Counter()
    for entry in conntrack_messages(1):  # IPCTNL_MSG_CT_GET
        original = attributes(entry.get(1, b""))
        ips = attributes(original.get(1, b""))
        proto = attributes(original.get(2, b""))
        source = interfaces.get(ips.get(1, ips.get(3)))
        dest = interfaces.get(ips.get(2, ips.get(4)))
        if source == dest == "lo":
            direction = "loopback"
        elif source and source.startswith("tun"):
            direction = "vpn_out"
        elif dest and dest.startswith("tun"):
            direction = "vpn_in"
        elif source and not dest:
            direction = "host_out"
        elif dest and not source:
            direction = "host_in"
        else:
            direction = "other"
        counts[direction] += 1
        if not number(entry, 3) & 2:  # IPS_SEEN_REPLY
            unreplied[direction] += 1
        port = number(proto, 3)
        if port:
            ports[direction][f"{number(proto, 1)}/{port}"] += 1
    return {
        direction: {"flows": count, "unreplied": unreplied[direction],
                    "top_protocol_ports": ports[direction].most_common(8)}
        for direction, count in counts.items()
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshot", action="store_true", help="立即输出快照，保留自动采样状态")
    args = parser.parse_args()
    count = int((SYSCTL / "nf_conntrack_count").read_text())
    limit = int((SYSCTL / "nf_conntrack_max").read_text())
    used = 100 * count / limit
    tier = sum(used >= level for level in (25, 70, 85, 95))
    totals = [0, 0]
    for row in conntrack_messages(4):  # IPCTNL_MSG_CT_GET_STATS_CPU
        totals[0] += number(row, 10)  # CTA_STATS_DROP
        totals[1] += number(row, 11)  # CTA_STATS_EARLY_DROP
    try:
        previous = json.loads(STATE.read_text())
    except (FileNotFoundError, ValueError):
        previous = {}
    now = time.monotonic()
    deltas = [max(0, new - old) for new, old in zip(totals, previous.get("totals", totals))]
    warning = tier >= 2 or any(deltas)
    last = previous.get("logged_at", now - 900)
    should_log = (
        args.snapshot or not previous or tier > previous.get("tier", 0)
        or (warning and (not previous.get("warning") or now - last >= 240))
        or (tier and now - last >= 900)
    )
    if should_log:
        event = {
            "count": count, "limit": limit, "used_percent": round(used, 1),
            "drop": deltas[0], "early_drop": deltas[1],
        }
        try:
            event["by_direction"] = snapshot()
        except (OSError, ValueError, subprocess.SubprocessError) as err:
            event["snapshot_error"] = type(err).__name__
        print("<4>" if warning else "<6>", json.dumps(event, separators=(",", ":")), sep="")
        last = now
    if not args.snapshot:
        temporary = STATE.with_suffix(".tmp")
        temporary.write_text(json.dumps({
            "tier": tier, "totals": totals, "warning": warning, "logged_at": last,
        }))
        temporary.replace(STATE)


if __name__ == "__main__":
    main()
