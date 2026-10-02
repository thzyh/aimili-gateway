#!/usr/bin/env python3
"""在现有隧道上承接新代理连接；不改路由、不停止出口引擎。"""
import argparse
import ipaddress
import json
import re
import shlex
import signal
import socket
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "services" / "aimili-egress"))
import proxy_server as proxy

_process_endpoints = {}


def load_json(path, default):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return default


def endpoint(host, port, proto):
    try:
        port = int(port)
    except (TypeError, ValueError):
        return None
    protocol = "tcp" if str(proto).lower().startswith("tcp") else "udp" if str(proto).lower().startswith("udp") else ""
    return (str(host).lower(), port, protocol) if host and 0 < port < 65536 and protocol else None


def config_endpoint(path):
    remote, protocol = [], "udp"
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            words = shlex.split(line, comments=True)
        except ValueError:
            continue
        if words and words[0] == "proto" and len(words) > 1:
            protocol = words[1]
        if words and words[0] == "remote" and len(words) >= 3:
            remote.append(words[1:])
    if len(remote) != 1:
        return None
    return endpoint(remote[0][0], remote[0][1], remote[0][2] if len(remote[0]) > 2 else protocol)


def engine_marker(pid, proc=Path("/proc")):
    root = proc / str(pid)
    return (root.joinpath("cgroup").read_text(), root.joinpath("stat").read_text().rsplit(")", 1)[1].split()[19])


def engine_tunnels(pid, proc=Path("/proc")):
    cgroup = proc.joinpath(str(pid), "cgroup").read_text()
    tunnels, live_keys = [], set()
    for process in proc.iterdir():
        if not process.name.isdigit():
            continue
        try:
            args = [s.decode(errors="replace") for s in process.joinpath("cmdline").read_bytes().split(b"\0") if s]
            if not args or Path(args[0]).name != "openvpn" or process.joinpath("cgroup").read_text() != cgroup:
                continue
            device, config = args[args.index("--dev") + 1], Path(args[args.index("--config") + 1])
            if not config.is_absolute():
                config = process.joinpath("cwd").resolve() / config
            if not re.fullmatch(r"tun\d+", device):
                continue
            start_ticks = process.joinpath("stat").read_text().rsplit(")", 1)[1].split()[19]
            key = (int(process.name), start_ticks)
            live_keys.add(key)
            actual = _process_endpoints.get(key)
            if actual is None:
                try:
                    actual = config_endpoint(config)
                except OSError:
                    actual = None
                if "--proto" in args and actual:
                    actual = endpoint(actual[0], actual[1], args[args.index("--proto") + 1])
                if actual:
                    # 提升后的 OpenVPN 仍持有旧 .standby_N 路径；不得把后来
                    # 补回备用时覆盖的文件当成此进程原来已加载的 remote。
                    _process_endpoints[key] = actual
            tunnels.append({"device": device, "pid": int(process.name), "stem": config.stem, "endpoint": actual})
        except (OSError, ValueError, IndexError):
            continue
    for key in set(_process_endpoints) - live_keys:
        _process_endpoints.pop(key, None)
    return tunnels


def main_tunnel(active_id, nodes, tunnels, excluded_devices=()):
    if not active_id:
        return None
    tunnels = [t for t in tunnels if t["device"] not in excluded_devices]
    matches = [t for t in tunnels if t["stem"] == active_id]
    if matches:
        return matches[0] if len(matches) == 1 else None
    nodes = [n for n in nodes if n.get("id") == active_id]
    if len(nodes) > 1:
        return None
    node = nodes[0] if nodes else {}
    wanted = endpoint(node.get("remote_host") or node.get("ip"), node.get("remote_port"), node.get("proto"))
    if not wanted:
        parts = re.fullmatch(r"[A-Z]{2}_([0-9.]+)_(\d+)_(udp\w*|tcp[\w-]*)", active_id)
        if parts:
            try:
                wanted = endpoint(str(ipaddress.IPv4Address(parts[1])), parts[2], parts[3])
            except ValueError:
                pass
    matches = [t for t in tunnels if wanted and t["endpoint"] == wanted]
    return matches[0] if len(matches) == 1 else None


def dns_candidates(path):
    candidates = load_json(path, [])
    if not isinstance(candidates, list):
        return []
    result = []
    for value in candidates:
        try:
            address = ipaddress.IPv4Address(value)
            if address.is_private and not (address.is_loopback or address.is_unspecified or address.is_multicast) and str(address) not in result:
                result.append(str(address))
        except ValueError:
            continue
    return result[:4]


def local_ipv4(device):
    try:
        import fcntl
        import struct
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            return socket.inet_ntoa(fcntl.ioctl(sock, 0x8915, struct.pack("256s", device.encode()[:15]))[20:24])
    except (ImportError, OSError):
        return ""


class Binding:
    def __init__(self, port):
        self.port, self.identity, self.generation = port, None, 0
        self.lock, self.registry = threading.Lock(), proxy.ConnRegistry()
        self.probing, self.retry_at, self.reported = False, 0.0, False

    def device(self):
        with self.lock:
            if self.identity is None:
                raise RuntimeError("[ERR_BRIDGE_BINDING_UNRESOLVED] 无法唯一核对目标隧道，拒绝使用其他出口")
            return self.identity[0]

    def update(self, identity, candidates):
        with self.lock:
            if identity != self.identity or not self.reported:
                old = self.identity
                self.identity, self.generation = identity, self.generation + 1
                self.reported = True
                self.registry.close_all()
                if old:
                    proxy.clear_tun_dns_servers(old[0])
                self.probing, self.retry_at = False, 0.0
                print(f"[代理承接] original_port={self.port} device={identity[0] if identity else 'unresolved'}", flush=True)
            if identity is None or self.probing or time.monotonic() < self.retry_at:
                return
            self.probing, self.retry_at = True, time.monotonic() + 30
            generation = self.generation
        threading.Thread(target=self.verify_dns, args=(identity, generation, candidates), daemon=True).start()

    def verify_dns(self, identity, generation, candidates):
        device = identity[0]
        local = local_ipv4(device).split(".")[:2]
        ordered = sorted(candidates, key=lambda server: server.split(".")[:2] != local)
        verified = []
        for server in ordered:
            if proxy.dns_query_over_tun0("www.gstatic.com", 1, server, 1.0, device):
                verified.append(server)
            if verified:
                break
        with self.lock:
            if generation != self.generation:
                return
            if verified:
                proxy.register_tun_dns_servers(device, verified)
            self.probing = False
        print(f"[DNS候选校验] device={device} checked={len(ordered)} verified={len(verified)}", flush=True)


def refresh_bindings(data, pid, offset, candidates, bindings, stop):
    state, nodes = load_json(data / "state.json", {}), load_json(data / "nodes.json", [])
    slots = load_json(data / "slots.json", {}).get("slots", [])
    tunnels = engine_tunnels(pid)
    active_id = str(state.get("active_openvpn_node_id") or "")
    selected = {7928: (active_id, main_tunnel(active_id, nodes, tunnels, {row.get("device") for row in slots}))}
    for row in slots:
        try:
            port = int(row.get("port"))
        except (TypeError, ValueError):
            continue
        if not 1024 <= port + offset <= 65535 or port == 7928:
            continue
        matches = [t for t in tunnels if t["device"] == row.get("device")]
        value = (str(row.get("node_id") or ""), matches[0] if len(matches) == 1 else None)
        selected[port] = ("", None) if port in selected else value
    for port in set(bindings) | set(selected) | set(range(17928, 17932)):
        if port not in bindings:
            bindings[port] = Binding(port)
            threading.Thread(target=proxy.start_proxy_server, args=("127.0.0.1", port + offset, bindings[port].device, stop, bindings[port].registry, proxy.proxy_capacity), daemon=True).start()
        node_id, tunnel = selected.get(port, ("", None))
        try:
            identity = (tunnel["device"], socket.if_nametoindex(tunnel["device"]), node_id, tunnel["pid"]) if tunnel and node_id else None
        except OSError:
            identity = None
        bindings[port].update(identity, candidates)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, required=True)
    parser.add_argument("--engine-pid", type=int, required=True)
    parser.add_argument("--port-offset", type=int, default=20000)
    parser.add_argument("--dns-candidates", type=Path, required=True)
    args = parser.parse_args()
    if args.engine_pid < 1 or not 1024 <= 7928 + args.port_offset <= 65535 or not 1024 <= 17931 + args.port_offset <= 65535:
        parser.error("无效的引擎 PID 或端口偏移")
    stop, bindings = threading.Event(), {}
    marker = engine_marker(args.engine_pid)
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    try:
        while not stop.is_set() and engine_marker(args.engine_pid) == marker:
            refresh_bindings(args.data, args.engine_pid, args.port_offset, dns_candidates(args.dns_candidates), bindings, stop)
            stop.wait(1)
    except OSError:
        print("[代理承接] 原出口引擎已退出，停止临时承接", flush=True)
    finally:
        stop.set()
        for binding in bindings.values():
            binding.update(None, [])


if __name__ == "__main__":
    main()
