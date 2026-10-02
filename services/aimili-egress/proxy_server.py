#!/usr/bin/env python3
from __future__ import annotations
import base64
import errno
import os
import secrets
import select
import socket
import threading
import urllib.parse
import time
import ipaddress
from pathlib import Path
from typing import Any

def parse_positive_int(value: str | None, default: int) -> int:
    try:
        return max(1, int(value or default))
    except (TypeError, ValueError):
        return default

def parse_nonnegative_int(value: str | None, default: int) -> int:
    try:
        return max(0, int(value or default))
    except (TypeError, ValueError):
        return default

def parse_positive_float(value: str | None, default: float) -> float:
    try:
        parsed = float(value or default)
        return parsed if parsed > 0 else default
    except (TypeError, ValueError):
        return default

MAX_PROXY_CONNECTIONS = parse_positive_int(os.environ.get("LOCAL_PROXY_MAX_CONNECTIONS"), 256)
MAX_PROXY_CONNECTIONS_PER_LISTENER = parse_positive_int(
    os.environ.get("LOCAL_PROXY_MAX_CONNECTIONS_PER_LISTENER"), 192
)
PROXY_ACQUIRE_WAIT_MS = parse_nonnegative_int(
    os.environ.get("LOCAL_PROXY_ACQUIRE_WAIT_MS"), 250
)


def proxy_resource_headroom() -> int:
    """Estimate safe additional threads, buffers and socket pairs; never use swap as RAM."""
    # Each relay can retain two 256 KiB buffers. Budget 1 MiB including the
    # Python thread/socket overhead, and leave 64 MiB for the rest of the VPS.
    budgets = []
    try:
        info = dict(line.split(":", 1) for line in Path("/proc/meminfo").read_text().splitlines())
        available = int(info["MemAvailable"].split()[0]) * 1024
        budgets.append((available - 64 * 1024**2) // 1024**2)
    except (OSError, ValueError, KeyError):
        budgets.append(32)
    try:
        import resource
        soft, _ = resource.getrlimit(resource.RLIMIT_NOFILE)
        if soft != resource.RLIM_INFINITY:
            budgets.append((soft - len(list(Path("/proc/self/fd").iterdir())) - 32) // 2)
        group = next(line[3:] for line in Path("/proc/self/cgroup").read_text().splitlines() if line.startswith("0::"))
        base = Path("/sys/fs/cgroup")
        current = base / group.lstrip("/")
        while current == base or base in current.parents:
            for kind, reserve, cost in (("pids", 32, 1), ("memory", 32 * 1024**2, 1024**2)):
                try:
                    limit = (current / f"{kind}.max").read_text().strip()
                    if limit != "max":
                        used = int((current / f"{kind}.current").read_text().strip())
                        budgets.append((int(limit) - used - reserve) // cost)
                except (OSError, ValueError):
                    pass
            if current == base:
                break
            current = current.parent
    except (ImportError, OSError, ValueError, StopIteration):
        pass
    return max(0, min(budgets))


class ProxyCapacity:
    """Bound new connections by live resources and reserve space for other listeners."""

    def __init__(self, global_limit: int, per_listener_limit: int, *, resource_sampler: Any = None,
                 wait_ms: int = PROXY_ACQUIRE_WAIT_MS, reserve_per_listener: int = 2) -> None:
        self._configured_limit = max(1, global_limit)
        self.global_limit = max(1, global_limit)
        self.per_listener_limit = max(1, min(per_listener_limit, self.global_limit))
        self._active = 0
        self._listeners: dict[str, int] = {}
        self._registered: set[str] = set()
        self._condition = threading.Condition()
        self._rejection = threading.local()
        self._sampler = resource_sampler
        self._sample_after = 0.0
        self._wait_ms = max(0, wait_ms)
        self._reserve = max(0, reserve_per_listener)

    def register_listener(self, listener_key: str) -> None:
        with self._condition:
            self._registered.add(listener_key)

    def unregister_listener(self, listener_key: str) -> None:
        with self._condition:
            self._registered.discard(listener_key)
            self._condition.notify_all()

    def _refresh(self) -> None:
        if self._sampler and time.monotonic() >= self._sample_after:
            additional = max(0, int(self._sampler()))
            self.global_limit = min(self._configured_limit, self._active + additional)
            self._sample_after = time.monotonic() + 5.0

    def snapshot(self) -> dict[str, Any]:
        with self._condition:
            self._refresh()
            return {"active": self._active, "global_limit": self.global_limit,
                    "per_listener_limit": self.per_listener_limit, "listeners": dict(self._listeners)}

    def try_acquire(self, listener_key: str) -> bool:
        self._rejection.reason = ""
        deadline = time.monotonic() + self._wait_ms / 1000
        with self._condition:
            while True:
                self._refresh()
                own = self._listeners.get(listener_key, 0)
                reserved = sum(max(0, self._reserve - self._listeners.get(key, 0))
                               for key in self._registered if key != listener_key)
                # On a very small VPS permit at least one connection per
                # listener to use available capacity instead of reserving it all.
                reserved = min(reserved, max(0, self.global_limit - 1))
                if own < self.per_listener_limit and self._active < self.global_limit - reserved:
                    self._active += 1
                    self._listeners[listener_key] = own + 1
                    return True
                self._rejection.reason = ("listener_limit" if own >= self.per_listener_limit else
                                          "resource_pressure" if self.global_limit < self._configured_limit else
                                          "global_limit")
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                self._condition.wait(remaining)

    def rejection_reason(self) -> str:
        return str(getattr(self._rejection, "reason", "unknown") or "unknown")

    def release(self, listener_key: str) -> None:
        with self._condition:
            if self._listeners.get(listener_key, 0) <= 0:
                raise ValueError("released unowned proxy capacity")
            self._listeners[listener_key] -= 1
            if not self._listeners[listener_key]:
                self._listeners.pop(listener_key)
            self._active -= 1
            self._condition.notify_all()


proxy_capacity = ProxyCapacity(
    MAX_PROXY_CONNECTIONS, MAX_PROXY_CONNECTIONS_PER_LISTENER, resource_sampler=proxy_resource_headroom
)

_diagnostic_lock = threading.Lock()
_diagnostic_next: dict[str, float] = {}


def diagnostic(key: str, message: str) -> None:
    """Rate limit recurring errors per tunnel/listener, not per visited URL."""
    with _diagnostic_lock:
        now = time.monotonic()
        if now < _diagnostic_next.get(key, 0):
            return
        if len(_diagnostic_next) > 512:
            _diagnostic_next.clear()
        _diagnostic_next[key] = now + 10.0
    print(message, flush=True)

class ConnRegistry:
    """跟踪某个代理监听实例当前活跃的下游客户端连接，支持一次性强制断开。

    用途：主连接节点切换/重连成功后，强制断开该端口上所有下游(如 3x-ui/Xray)连接，
    使其立即重连到新隧道，避免下游复用切换前已黑洞化的连接或连接池，导致
    “切换后无网络、必须重启下游进程才恢复”。close_all 先 shutdown 再 close，
    确保正阻塞在 select 的 relay 线程能被唤醒并退出。"""

    def __init__(self) -> None:
        self._conns: set[socket.socket] = set()
        self._lock = threading.Lock()

    def add(self, sock: socket.socket) -> None:
        with self._lock:
            self._conns.add(sock)

    def discard(self, sock: socket.socket) -> None:
        with self._lock:
            self._conns.discard(sock)

    def close_all(self) -> int:
        with self._lock:
            conns = list(self._conns)
            self._conns.clear()
        closed = 0
        for s in conns:
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                s.close()
                closed += 1
            except OSError:
                pass
        return closed

def parse_int(value: Any) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def resolve_device(device: Any) -> str:
    """Resolve a fixed device name or a thread-safe provider for new connections."""
    value = device() if callable(device) else device
    normalized = str(value or "").strip()
    return normalized or "tun0"

def set_remaining_timeout(sock: socket.socket, deadline: float) -> None:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise socket.timeout("DNS deadline exceeded")
    sock.settimeout(remaining)


def recv_exact(sock: socket.socket, size: int, deadline: float | None = None) -> bytes:
    data = b""
    while len(data) < size:
        if deadline is not None:
            set_remaining_timeout(sock, deadline)
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise ConnectionError("Unexpected disconnect.")
        data += chunk
    return data

def parse_host_port(authority: str, default_port: int) -> tuple[str, int]:
    authority = authority.strip()
    if authority.startswith("["):
        host_part, sep, rest = authority.partition("]")
        host = host_part.lstrip("[")
        port = default_port
        if sep and rest.startswith(":"):
            port_text = rest[1:]
            port = parse_int(port_text) or default_port
        return host, port
    if authority.count(":") == 1:
        host, _, port_text = authority.rpartition(":")
        return host, parse_int(port_text) or default_port
    return authority, default_port

def get_proxy_credentials() -> tuple[str | None, str | None]:
    user = os.environ.get("LOCAL_PROXY_USER") or os.environ.get("LOCAL_PROXY_USERNAME")
    password = os.environ.get("LOCAL_PROXY_PASS") or os.environ.get("LOCAL_PROXY_PASSWORD")
    if user is None and password is None:
        return None, None
    return user or "", password or ""

def proxy_auth_enabled() -> bool:
    user, password = get_proxy_credentials()
    return user is not None and password is not None

def parse_http_basic_auth(lines: list[str]) -> tuple[str | None, str | None]:
    for line in lines:
        name, sep, value = line.partition(":")
        if not sep or name.strip().lower() != "proxy-authorization":
            continue
        scheme, _, token = value.strip().partition(" ")
        if scheme.lower() != "basic" or not token:
            return None, None
        try:
            decoded = base64.b64decode(token, validate=True).decode("utf-8", errors="replace")
        except Exception:
            return None, None
        username, sep, password = decoded.partition(":")
        if not sep:
            return None, None
        return username, password
    return None, None

def check_credentials(username: str | None, password: str | None) -> bool:
    expected_user, expected_pass = get_proxy_credentials()
    if expected_user is None or expected_pass is None:
        return True
    return secrets.compare_digest(username or "", expected_user) and secrets.compare_digest(password or "", expected_pass)

DNS_QUERY_TIMEOUT = parse_positive_float(os.environ.get("LOCAL_PROXY_DNS_TIMEOUT"), 1.0)
DNS_TOTAL_TIMEOUT = parse_positive_float(os.environ.get("LOCAL_PROXY_DNS_TOTAL_TIMEOUT"), 2.5)

def dns_query_over_tun0(host: str, qtype: int, dns_server: str, timeout: float, device: str = "tun0") -> str | None:
    import random
    sock = None
    started = time.monotonic()
    deadline = started + max(0.001, timeout)
    udp_deadline = started + max(0.001, timeout) / 2
    used_tcp = False
    try:
        tx_id = random.getrandbits(16).to_bytes(2, "big")
        flags = b"\x01\x00"
        questions = b"\x00\x01"
        rrs = b"\x00\x00\x00\x00\x00\x00"

        qname = b""
        for part in host.split("."):
            if not part:
                continue
            part_bytes = part.encode("idna")
            if len(part_bytes) > 63:
                return None
            qname += len(part_bytes).to_bytes(1, "big") + part_bytes
        qname += b"\x00"

        qtype_qclass = qtype.to_bytes(2, "big") + b"\x00\x01"
        packet = tx_id + flags + questions + rrs + qname + qtype_qclass

        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, device.encode("utf-8"))
        except OSError as e:
            if "operation not permitted" in str(e).lower() or e.errno == 1:
                print(f"[DNS 绑定失败] [错误代码 3006] DNS 解析绑定 {device} 权限不足，请确保程序以 root 权限运行！", flush=True)
            elif "no such device" in str(e).lower() or e.errno == 19:
                print(f"[DNS 绑定失败] [错误代码 3004] DNS 解析绑定 {device} 失败，网卡设备不存在，请检查 VPN 连接！", flush=True)
            return None
        try:
            set_remaining_timeout(sock, udp_deadline)
            sock.sendto(packet, (dns_server, 53))
            set_remaining_timeout(sock, udp_deadline)
            resp, _ = sock.recvfrom(4096)
        except OSError:
            resp = b""
        # UDP DNS can be dropped or truncated inside a working VPN. Retry over
        # TCP on the same tunnel; never send this fallback through the VPS uplink.
        if len(resp) < 12 or resp[:2] != tx_id or resp[2] & 0x02:
            used_tcp = True
            sock.close()
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, device.encode("utf-8"))
            set_remaining_timeout(sock, deadline)
            sock.connect((dns_server, 53))
            set_remaining_timeout(sock, deadline)
            sock.sendall(len(packet).to_bytes(2, "big") + packet)
            response_size = int.from_bytes(recv_exact(sock, 2, deadline), "big")
            resp = recv_exact(sock, response_size, deadline)
        if used_tcp:
            elapsed_ms = int((time.monotonic() - started) * 1000)
            diagnostic(f"dns-tcp:{device}:{dns_server}",
                f"[DNS回退] device={device} server={dns_server} qtype={qtype} "
                f"UDP无有效响应，已尝试同隧道TCP，耗时={elapsed_ms}ms",
            )
    except Exception as exc:
        diagnostic(f"dns-failed:{device}:{dns_server}",
            f"[DNS失败] device={device} server={dns_server} qtype={qtype} "
            f"阶段异常={type(exc).__name__} 耗时={int((time.monotonic()-started)*1000)}ms",
        )
        return None
    finally:
        if sock is not None:
            try:
                sock.close()
            except Exception:
                pass

    try:
        if len(resp) < 12 or resp[:2] != tx_id:
            return None
        rcode = resp[3] & 0x0F
        if rcode != 0:
            return None

        offset = 12
        while offset < len(resp):
            length = resp[offset]
            if length == 0:
                offset += 1
                break
            if (length & 0xC0) == 0xC0:
                offset += 2
                break
            offset += 1 + length

        offset += 4
        answers_count = int.from_bytes(resp[6:8], "big")
        for _ in range(answers_count):
            if offset >= len(resp):
                break
            while offset < len(resp):
                length = resp[offset]
                if length == 0:
                    offset += 1
                    break
                if (length & 0xC0) == 0xC0:
                    offset += 2
                    break
                offset += 1 + length
            if offset + 10 > len(resp):
                break
            atype = int.from_bytes(resp[offset : offset + 2], "big")
            aclass = int.from_bytes(resp[offset + 2 : offset + 4], "big")
            rdlength = int.from_bytes(resp[offset + 8 : offset + 10], "big")
            offset += 10
            if offset + rdlength > len(resp):
                break
            record = resp[offset : offset + rdlength]
            if atype == qtype and aclass == 1:
                if qtype == 1 and rdlength == 4:
                    return socket.inet_ntoa(record)
                if qtype == 28 and rdlength == 16:
                    return socket.inet_ntop(socket.AF_INET6, record)
            offset += rdlength
    except Exception:
        return None
    return None

DNS_CACHE_TTL = parse_positive_int(os.environ.get("LOCAL_PROXY_DNS_TTL"), 300)
DNS_CACHE_MAX = parse_positive_int(os.environ.get("LOCAL_PROXY_DNS_CACHE_MAX"), 4096)
_dns_cache: dict[str, tuple[str, float]] = {}
_dns_cache_lock = threading.Lock()
_tun_dns_servers: dict[str, tuple[str, ...]] = {}
_dns_pending: dict[str, threading.Event] = {}
_dns_generation: dict[str, int] = {}

def register_tun_dns_servers(device: str, servers: list[str]) -> None:
    """登记某条 OpenVPN 隧道 PUSH_REPLY 提供的 DNS，供该隧道优先使用。"""
    normalized: list[str] = []
    for raw in servers:
        try:
            address = str(ipaddress.IPv4Address(str(raw).strip()))
        except ValueError:
            continue
        if address not in normalized:
            normalized.append(address)
    if not normalized or not device:
        return
    with _dns_cache_lock:
        if _tun_dns_servers.get(device) == tuple(normalized):
            return
        _tun_dns_servers[device] = tuple(normalized)
        _purge_dns_cache_locked(device)
    print(f"[DNS登记] device={device} servers={','.join(normalized)}", flush=True)

def clear_tun_dns_servers(device: str) -> None:
    with _dns_cache_lock:
        _tun_dns_servers.pop(device, None)
        _purge_dns_cache_locked(device)

def get_tun_dns_servers(device: str | None = None) -> list[str]:
    raw = os.environ.get("OPENVPN_TUN_DNS", "8.8.8.8,1.1.1.1")
    configured = [s.strip() for s in raw.split(",") if s.strip()]
    with _dns_cache_lock:
        registered = list(_tun_dns_servers.get(device or "", ()))
    servers: list[str] = []
    for server in registered + configured:
        try:
            server = str(ipaddress.IPv4Address(server))
        except ValueError:
            continue
        if server not in servers:
            servers.append(server)
    return servers or ["8.8.8.8"]

def resolve_dns_over_tun0(host: str, dns_server: str | None = None, timeout: float = DNS_TOTAL_TIMEOUT, device: str = "tun0", ipv4_only: bool = False) -> str | None:
    try:
        socket.inet_aton(host)
        return host
    except OSError:
        pass
    try:
        socket.inet_pton(socket.AF_INET6, host)
        return host
    except OSError:
        pass

    now = time.time()
    deadline = time.monotonic() + max(0.001, timeout)
    cache_key = f"{device}|{host}"
    pending_key = f"{cache_key}|{dns_server or ''}|{'A' if ipv4_only else 'any'}"
    with _dns_cache_lock:
        cached = _dns_cache.get(cache_key)
        if cached and now - cached[1] < DNS_CACHE_TTL and not (ipv4_only and ":" in cached[0]):
            return cached[0]
        pending = _dns_pending.get(pending_key)
        owner = pending is None
        if owner:
            pending = _dns_pending[pending_key] = threading.Event()
        generation = _dns_generation.get(device, 0)

    # Concurrent requests for the same host on the same tunnel share one DNS
    # exchange. Other tunnels remain independent, including failures.
    if not owner:
        pending.wait(max(0.0, deadline - time.monotonic()))
        with _dns_cache_lock:
            cached = _dns_cache.get(cache_key)
            if (cached and time.time() - cached[1] < DNS_CACHE_TTL and
                    generation == _dns_generation.get(device, 0) and
                    not (ipv4_only and ":" in cached[0])):
                return cached[0]
        return None

    # Try every IPv4 resolver before considering IPv6. One failed resolver must
    # not hide a reachable IPv4 address behind an unroutable AAAA response.
    resolved = None
    try:
        servers = [dns_server] if dns_server else get_tun_dns_servers(device)
        for qtype in ([1] if ipv4_only else [1, 28]):
            for server in servers:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                resolved = dns_query_over_tun0(host, qtype, server, min(DNS_QUERY_TIMEOUT, remaining), device)
                if resolved:
                    break
            if resolved:
                break
    finally:
        with _dns_cache_lock:
            if generation != _dns_generation.get(device, 0):
                resolved = None
            if resolved:
                if len(_dns_cache) >= DNS_CACHE_MAX:
                    _dns_cache.clear()
                _dns_cache[cache_key] = (resolved, time.time())
            _dns_pending.pop(pending_key, None)
            pending.set()
    return resolved


def _purge_dns_cache_locked(device: str) -> int:
    prefix = f"{device}|"
    keys = [k for k in _dns_cache if k.startswith(prefix)]
    for key in keys:
        _dns_cache.pop(key, None)
    _dns_generation[device] = _dns_generation.get(device, 0) + 1
    return len(keys)

def purge_dns_cache(device: str | None = None) -> int:
    """清理隧道内 DNS 解析缓存。device 给定时只清该设备(如 tun0)的条目，否则清空全部。

    主连接切换节点后调用：旧出口视角解析到的 IP 可能不再是新出口的最优/可达解析，
    清掉可避免新隧道复用旧解析结果。返回清理条数。"""
    with _dns_cache_lock:
        if device is None:
            n = len(_dns_cache)
            _dns_cache.clear()
            for dev in set(_dns_generation) | set(_tun_dns_servers):
                _dns_generation[dev] = _dns_generation.get(dev, 0) + 1
            return n
        return _purge_dns_cache_locked(device)

def create_connection(address: tuple[str, int], timeout: float = 20, device: str = "tun0") -> socket.socket:
    try:
        return _create_connection(address, timeout, device)
    except OSError as exc:
        host, _ = address
        try:
            socket.inet_pton(socket.AF_INET6, host)
            literal = True
        except OSError:
            try:
                socket.inet_pton(socket.AF_INET, host)
                literal = True
            except OSError:
                literal = False
        if literal or exc.errno not in (errno.ENETUNREACH, errno.EHOSTUNREACH):
            raise
        # Drop only the failed hostname on this tunnel. Existing connections and
        # DNS entries for the other exits are untouched.
        with _dns_cache_lock:
            _dns_cache.pop(f"{device}|{host}", None)
        return _create_connection(address, timeout, device, ipv4_only=True)


def _create_connection(address: tuple[str, int], timeout: float, device: str, ipv4_only: bool = False) -> socket.socket:
    host, port = address
    resolved_ip = resolve_dns_over_tun0(host, device=device, ipv4_only=ipv4_only)
    if not resolved_ip:
        raise socket.gaierror(socket.EAI_AGAIN, f"[ERR_PROXY_DNS_FAILED] 隧道 {device} 内域名解析失败")
    host = resolved_ip

    err = None
    for res in socket.getaddrinfo(host, port, socket.AF_INET if ipv4_only else 0,
                                  socket.SOCK_STREAM, 0, socket.AI_NUMERICHOST):
        af, socktype, proto, canonname, sa = res
        sock = None
        try:
            sock = socket.socket(af, socktype, proto)
            sock.settimeout(timeout)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, device.encode("utf-8"))
            sock.connect(sa)
            return sock
        except OSError as e:
            err = e
            if "operation not permitted" in str(e).lower() or e.errno == 1:
                err = OSError(f"[错误代码 3006] [ERR_PROXY_BIND_TUN_PERM_DENIED] 绑定虚拟网卡 {device} 失败，权限不足！必须以 root 权限运行，或者进程缺少 CAP_NET_RAW 权限。")
            elif "no such device" in str(e).lower() or e.errno == 19:
                err = OSError(f"[错误代码 3004] [ERR_ROUTE_DEV_NOT_FOUND] 绑定虚拟网卡 {device} 失败，找不到设备！这通常是因为 OpenVPN 核心未能成功连接或已被异常终止。")
            if sock is not None:
                sock.close()
    if err is not None:
        raise err
    else:
        raise OSError("getaddrinfo returns empty list")

RELAY_HIGH_WATER = parse_positive_int(os.environ.get("LOCAL_PROXY_RELAY_BUFFER"), 262144)
RELAY_IDLE_TIMEOUT = parse_positive_int(os.environ.get("LOCAL_PROXY_RELAY_TIMEOUT"), 300)

def relay(left: socket.socket, right: socket.socket) -> None:
    left.setblocking(False)
    right.setblocking(False)
    peer = {left: right, right: left}
    out_buf: dict[socket.socket, bytearray] = {left: bytearray(), right: bytearray()}
    read_open = {left: True, right: True}
    write_shutdown = {left: False, right: False}

    while True:
        rlist = [s for s in (left, right) if read_open[s] and len(out_buf[peer[s]]) < RELAY_HIGH_WATER]
        wlist = [s for s in (left, right) if out_buf[s]]
        if not rlist and not wlist:
            return
        try:
            readable, writable, errored = select.select(rlist, wlist, (left, right), RELAY_IDLE_TIMEOUT)
        except (OSError, ValueError):
            return
        if errored:
            return
        if not readable and not writable:
            return

        for source in readable:
            try:
                data = source.recv(65536)
            except (BlockingIOError, InterruptedError):
                continue
            except OSError:
                return
            if not data:
                read_open[source] = False
            else:
                out_buf[peer[source]].extend(data)

        for target in writable:
            if not out_buf[target]:
                continue
            try:
                sent = target.send(out_buf[target])
            except (BlockingIOError, InterruptedError):
                continue
            except OSError:
                return
            if sent:
                del out_buf[target][:sent]

        # 单方向 EOF 且其数据已全部转发后，向对端 shutdown 写入以传播半关闭
        for s in (left, right):
            if not read_open[s] and not out_buf[peer[s]] and not write_shutdown[peer[s]]:
                try:
                    peer[s].shutdown(socket.SHUT_WR)
                except OSError:
                    pass
                write_shutdown[peer[s]] = True

        left_to_right_done = not read_open[left] and not out_buf[right]
        right_to_left_done = not read_open[right] and not out_buf[left]
        if left_to_right_done and right_to_left_done:
            return

def socks5_client(client: socket.socket, first_byte: bytes, device: str = "tun0") -> None:
    upstream = None
    try:
        methods_count = recv_exact(client, 1)[0]
        methods = recv_exact(client, methods_count)
        if proxy_auth_enabled():
            if 2 not in methods:
                client.sendall(b"\x05\xff")
                return
            client.sendall(b"\x05\x02")
            auth_version = recv_exact(client, 1)[0]
            if auth_version != 1:
                client.sendall(b"\x01\x01")
                return
            username = recv_exact(client, recv_exact(client, 1)[0]).decode("utf-8", errors="replace")
            password = recv_exact(client, recv_exact(client, 1)[0]).decode("utf-8", errors="replace")
            if not check_credentials(username, password):
                client.sendall(b"\x01\x01")
                return
            client.sendall(b"\x01\x00")
        else:
            client.sendall(b"\x05\x00")
        version, command, _, address_type = recv_exact(client, 4)
        if version != 5 or command != 1:
            client.sendall(b"\x05\x07\x00\x01\x00\x00\x00\x00\x00\x00")
            return
        if address_type == 1:
            host = socket.inet_ntoa(recv_exact(client, 4))
        elif address_type == 3:
            host = recv_exact(client, recv_exact(client, 1)[0]).decode("idna")
        elif address_type == 4:
            host = socket.inet_ntop(socket.AF_INET6, recv_exact(client, 16))
        else:
            client.sendall(b"\x05\x08\x00\x01\x00\x00\x00\x00\x00\x00")
            return
        port = int.from_bytes(recv_exact(client, 2), "big")
        try:
            upstream = create_connection((host, port), timeout=20, device=device)
        except Exception as e:
            print(f"[SOCKS5 代理失败] 目标 {host}:{port} 连接失败: {e}", flush=True)
            try:
                client.sendall(b"\x05\x04\x00\x01\x00\x00\x00\x00\x00\x00")
            except OSError:
                pass
            raise
        client.sendall(b"\x05\x00\x00\x01\x00\x00\x00\x00\x00\x00")
        relay(client, upstream)
    finally:
        client.close()
        if upstream:
            upstream.close()

def read_http_header(client: socket.socket, first_byte: bytes) -> bytes:
    data = first_byte
    while b"\r\n\r\n" not in data and len(data) < 65536:
        chunk = client.recv(4096)
        if not chunk:
            break
        data += chunk
    return data

def http_client(client: socket.socket, first_byte: bytes, device: str = "tun0") -> None:
    upstream = None
    try:
        header = read_http_header(client, first_byte)
        if b"\r\n\r\n" not in header:
            client.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
            return
        head, rest = header.split(b"\r\n\r\n", 1)
        lines = head.decode("iso-8859-1", errors="replace").split("\r\n")
        try:
            method, target, version = lines[0].split(" ", 2)
        except ValueError:
            client.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
            return
        if not version.startswith("HTTP/"):
            client.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
            return
        if proxy_auth_enabled():
            username, password = parse_http_basic_auth(lines[1:])
            if not check_credentials(username, password):
                client.sendall(
                    b"HTTP/1.1 407 Proxy Authentication Required\r\n"
                    b"Proxy-Authenticate: Basic realm=\"AimiliVPN Proxy\"\r\n"
                    b"Content-Length: 0\r\n\r\n"
                )
                return
        if method.upper() == "CONNECT":
            host, port = parse_host_port(target, 443)
            upstream = create_connection((host, port), timeout=20, device=device)
            client.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            if rest:
                upstream.sendall(rest)
            relay(client, upstream)
            return

        try:
            parsed = urllib.parse.urlsplit(target)
        except ValueError:
            client.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
            return
        hostname = parsed.hostname
        port = parsed.port
        scheme = parsed.scheme
        if not hostname:
            # Fallback to Host header
            for line in lines[1:]:
                if line.lower().startswith("host:"):
                    host_val = line.split(":", 1)[1].strip()
                    if "[" in host_val and "]" in host_val:
                        host_part, _, port_part = host_val.rpartition("]")
                        hostname = host_part.lstrip("[")
                        if port_part.startswith(":"):
                            p_val = port_part.lstrip(":")
                            port = int(p_val) if p_val.isdigit() else None
                        else:
                            port = None
                    else:
                        hostname, parsed_port = parse_host_port(host_val, 0)
                        port = parsed_port or None
                    break
        if not hostname:
            client.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
            return
        port = port or (443 if scheme == "https" else 80)
        path = urllib.parse.urlunsplit(("", "", parsed.path or "/", parsed.query, ""))
        headers = [line for line in lines[1:] if not line.lower().startswith(("proxy-connection:", "connection:", "proxy-authorization:"))]
        request = f"{method} {path} {version}\r\n" + "\r\n".join(headers) + "\r\nConnection: close\r\n\r\n"
        upstream = create_connection((hostname, port), timeout=20, device=device)
        upstream.sendall(request.encode("iso-8859-1") + rest)
        relay(client, upstream)
    except Exception as e:
        print(f"[HTTP 代理失败] 代理请求目标连接失败: {e}", flush=True)
        try:
            client.sendall(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
        except OSError:
            pass
    finally:
        client.close()
        if upstream:
            upstream.close()

def proxy_client(client: socket.socket, address: tuple[str, int], device: str = "tun0") -> None:
    try:
        client.settimeout(30)
        first = recv_exact(client, 1)
        if first == b"\x05":
            socks5_client(client, first, device)
        else:
            http_client(client, first, device)
    except Exception as e:
        err_msg = str(e)
        if "[错误代码" in err_msg:
            print(f"[代理客户端连接失败] 客户端 {address} 遭遇系统性阻碍: {err_msg}", flush=True)
        try:
            client.close()
        except OSError:
            pass

def start_proxy_client_thread(
    client: socket.socket,
    address: tuple[str, int],
    device: Any,
    listener_key: str,
    capacity: ProxyCapacity,
    registry: ConnRegistry | None = None,
    thread_factory: Any = threading.Thread,
) -> bool:
    if not capacity.try_acquire(listener_key):
        try:
            client.close()
        except OSError:
            pass
        return False

    def run_client() -> None:
        try:
            if registry is not None:
                registry.add(client)
            proxy_client(client, address, resolve_device(device))
        except Exception as exc:
            diagnostic(f"proxy-client:{listener_key}",
                       f"[代理处理失败] listener={listener_key} stage={type(exc).__name__}")
        finally:
            if registry is not None:
                registry.discard(client)
            try:
                client.close()
            except OSError:
                pass
            capacity.release(listener_key)

    try:
        thread_factory(target=run_client, daemon=True).start()
    except Exception:
        try:
            client.close()
        except OSError:
            pass
        capacity.release(listener_key)
        diagnostic(f"proxy-thread:{listener_key}", f"[代理线程失败] listener={listener_key}")
        return False
    return True


def start_proxy_server(host: str, port: int, device: Any = "tun0", stop_event: threading.Event | None = None, registry: ConnRegistry | None = None, capacity: ProxyCapacity | None = None) -> None:
    is_ipv6 = ":" in host or host == ""
    af = socket.AF_INET6 if is_ipv6 else socket.AF_INET
    server = None
    try:
        server = socket.socket(af, socket.SOCK_STREAM)
        server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        if is_ipv6:
            try:
                server.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
            except OSError:
                pass
        server.bind((host, port))
        server.listen(256)
        print(f"HTTP/SOCKS5 proxy listening on {host}:{port}", flush=True)
    except Exception as e:
        if server is not None:
            try:
                server.close()
            except Exception:
                pass
        if is_ipv6 and host in ("::", ""):
            print(f"[警告] 绑定 IPv6 {host}:{port} 失败 ({e})，正在尝试回退至 IPv4 0.0.0.0 ...", flush=True)
            try:
                server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                server.bind(("0.0.0.0", port))
                server.listen(256)
                print(f"HTTP/SOCKS5 proxy listening on 0.0.0.0:{port} (仅 IPv4)", flush=True)
            except Exception as ex:
                import vpn_utils
                diag = vpn_utils.diagnose_local_obstructions(port, host="0.0.0.0")
                diag_msg = diag[1] if diag else str(ex)
                print(f"[ERROR] Failed to start HTTP/SOCKS5 proxy on 0.0.0.0:{port}: {diag_msg}", flush=True)
                return
        elif is_ipv6 and host == "::1":
            print(f"[警告] 绑定 IPv6 {host}:{port} 失败 ({e})，正在尝试回退至 IPv4 127.0.0.1 ...", flush=True)
            try:
                server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                server.bind(("127.0.0.1", port))
                server.listen(256)
                print(f"HTTP/SOCKS5 proxy listening on 127.0.0.1:{port} (仅 IPv4)", flush=True)
            except Exception as ex:
                import vpn_utils
                diag = vpn_utils.diagnose_local_obstructions(port, host="127.0.0.1")
                diag_msg = diag[1] if diag else str(ex)
                print(f"[ERROR] Failed to start HTTP/SOCKS5 proxy on 127.0.0.1:{port}: {diag_msg}", flush=True)
                return
        else:
            import vpn_utils
            diag = vpn_utils.diagnose_local_obstructions(port, host=host)
            diag_msg = diag[1] if diag else str(e)
            print(f"[ERROR] Failed to start HTTP/SOCKS5 proxy on {host}:{port}: {diag_msg}", flush=True)
            return

    if stop_event is not None:
        server.settimeout(1.0)

    capacity = capacity or proxy_capacity
    listener_key = f"{host}:{port}"
    capacity.register_listener(listener_key)
    print(f"[代理容量] listener={listener_key} {capacity.snapshot()}", flush=True)
    while True:
        if stop_event is not None and stop_event.is_set():
            try:
                server.close()
            except OSError:
                pass
            print(f"[代理网关] 已停止监听 {host}:{port} ({resolve_device(device)})", flush=True)
            capacity.unregister_listener(listener_key)
            return
        try:
            client, address = server.accept()
            if not start_proxy_client_thread(
                client, address, device, listener_key, capacity, registry
            ):
                diagnostic(f"proxy-limit:{listener_key}",
                    f"[代理限流] listener={listener_key} reason={capacity.rejection_reason()} "
                    f"wait_budget_ms={PROXY_ACQUIRE_WAIT_MS} {capacity.snapshot()}",
                )
        except socket.timeout:
            continue
        except Exception as e:
            if stop_event is not None and stop_event.is_set():
                try:
                    server.close()
                except OSError:
                    pass
                capacity.unregister_listener(listener_key)
                return
            print(f"[ERROR] Proxy accept failed: {e}", flush=True)
            time.sleep(0.5)
