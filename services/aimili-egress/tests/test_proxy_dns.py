import errno
import socket
import threading
import unittest
from unittest import mock

import proxy_server as proxy


def dns_answer() -> bytes:
    return (
        b"\x12\x34\x81\x80\x00\x01\x00\x01\x00\x00\x00\x00"
        b"\x01x\x00\x00\x01\x00\x01"
        b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c"
        b"\x00\x04\xcb\x00\x71\x07"
    )


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def monotonic(self):
        return self.now

    def time(self):
        return 1000.0 + self.now


class DeadlineSocket:
    """逐阶段消耗虚拟时间，并遵守最近一次 settimeout。"""

    def __init__(self, clock, kind, response=b""):
        self.clock = clock
        self.kind = kind
        self.response = response
        self.timeout = None
        self.closed = False
        self.bindings = []
        self.timeouts = []

    def settimeout(self, value):
        self.timeout = value
        self.timeouts.append(value)

    def setsockopt(self, *args):
        self.bindings.append(args)

    def _delay(self, seconds):
        if self.timeout is not None and seconds > self.timeout:
            self.clock.now += self.timeout
            raise socket.timeout()
        self.clock.now += seconds

    def sendto(self, _packet, _address):
        return None

    def recvfrom(self, _size):
        self.clock.now += self.timeout
        raise socket.timeout()

    def connect(self, _address):
        self._delay(0.2)

    def sendall(self, _packet):
        self._delay(0.2)

    def recv(self, size):
        self._delay(0.03)
        # 碎片化 TCP DNS 响应不能让每次 recv 重置整段超时。
        chunk, self.response = self.response[:1], self.response[1:]
        return chunk

    def close(self):
        self.closed = True


class ProxyDNSTests(unittest.TestCase):
    def setUp(self):
        proxy.purge_dns_cache()
        proxy.clear_tun_dns_servers("tun120")
        proxy.clear_tun_dns_servers("tun121")

    def tearDown(self):
        proxy.purge_dns_cache()
        proxy.clear_tun_dns_servers("tun120")
        proxy.clear_tun_dns_servers("tun121")

    def test_udp_timeout_uses_tcp_bound_to_same_tunnel(self):
        udp, tcp = mock.MagicMock(), mock.MagicMock()
        udp.recvfrom.side_effect = socket.timeout()
        answer = b"\x12\x34\x81\x80\x00\x01\x00\x01\x00\x00\x00\x00" + b"\x01x\x00\x00\x01\x00\x01" + b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04\xcb\x00\x71\x07"
        tcp.recv.side_effect = [len(answer).to_bytes(2, "big"), answer[:8], answer[8:]]
        with mock.patch.object(proxy.socket, "socket", side_effect=[udp, tcp]), mock.patch("random.getrandbits", return_value=0x1234), mock.patch.object(socket, "SO_BINDTODEVICE", 25, create=True):
            self.assertEqual(proxy.dns_query_over_tun0("x", 1, "8.8.8.8", 1, "tun120"), "203.0.113.7")
        tcp.setsockopt.assert_called_once_with(socket.SOL_SOCKET, 25, b"tun120")
        tcp.connect.assert_called_once_with(("8.8.8.8", 53))
        self.assertTrue(udp.close.called)
        self.assertTrue(tcp.close.called)

    def test_ipv4_on_second_resolver_precedes_ipv6(self):
        def query(host, kind, server, *_):
            if kind == 28:
                self.fail("Must try the second IPv4 resolver first")
            return "203.0.113.7" if server == "1.1.1.1" else None
        with mock.patch.object(proxy, "dns_query_over_tun0", side_effect=query):
            self.assertEqual(proxy.resolve_dns_over_tun0("x", device="tun120"), "203.0.113.7")

    def test_unreachable_cached_address_retries_ipv4_without_flushing_other_exits(self):
        proxy._dns_cache.update({"tun120|x": ("2001:db8::1", 0), "tun121|x": ("203.0.113.8", 0)})
        result = object()
        with mock.patch.object(proxy, "_create_connection", side_effect=[OSError(errno.ENETUNREACH, "unreachable"), result]) as connect:
            self.assertIs(proxy.create_connection(("x", 443), device="tun120"), result)
        self.assertNotIn("tun120|x", proxy._dns_cache)
        self.assertIn("tun121|x", proxy._dns_cache)
        self.assertTrue(connect.call_args.kwargs["ipv4_only"])

    def test_literal_ipv6_is_not_silently_converted(self):
        with mock.patch.object(proxy, "_create_connection", side_effect=OSError(errno.ENETUNREACH, "unreachable")) as connect:
            with self.assertRaises(OSError):
                proxy.create_connection(("2001:db8::1", 443))
        self.assertEqual(connect.call_count, 1)

    def test_tcp_failure_stays_failure(self):
        udp, tcp = mock.MagicMock(), mock.MagicMock()
        udp.recvfrom.side_effect = socket.timeout()
        tcp.connect.side_effect = socket.timeout()
        with mock.patch.object(proxy.socket, "socket", side_effect=[udp, tcp]), mock.patch.object(socket, "SO_BINDTODEVICE", 25, create=True):
            self.assertIsNone(proxy.dns_query_over_tun0("x", 1, "8.8.8.8", 1, "tun120"))

    def test_each_device_prefers_its_pushed_ipv4_dns_and_deduplicates_fallbacks(self):
        with mock.patch.dict(proxy.os.environ, {"OPENVPN_TUN_DNS": "8.8.8.8,1.1.1.1,8.8.8.8,bad"}):
            proxy.register_tun_dns_servers(
                "tun120", ["10.211.254.254", "8.8.8.8", "10.211.254.254", "bad", "::1"]
            )
            proxy.register_tun_dns_servers("tun121", ["10.212.254.254"])
            self.assertEqual(
                proxy.get_tun_dns_servers("tun120"),
                ["10.211.254.254", "8.8.8.8", "1.1.1.1"],
            )
            self.assertEqual(
                proxy.get_tun_dns_servers("tun121"),
                ["10.212.254.254", "8.8.8.8", "1.1.1.1"],
            )

    def test_pushed_dns_failure_falls_back_to_public_dns_on_same_device(self):
        proxy.register_tun_dns_servers("tun120", ["10.211.254.254"])
        calls = []

        def query(host, kind, server, timeout, device):
            calls.append((kind, server, device))
            return "203.0.113.7" if server == "8.8.8.8" else None

        with mock.patch.dict(proxy.os.environ, {"OPENVPN_TUN_DNS": "8.8.8.8,1.1.1.1"}), mock.patch.object(
            proxy, "dns_query_over_tun0", side_effect=query
        ):
            self.assertEqual(proxy.resolve_dns_over_tun0("x", device="tun120"), "203.0.113.7")
        self.assertEqual(calls, [(1, "10.211.254.254", "tun120"), (1, "8.8.8.8", "tun120")])

    def test_registration_change_invalidates_only_that_device_cache(self):
        proxy.register_tun_dns_servers("tun120", ["10.211.254.254"])
        proxy._dns_cache.update({
            "tun120|x": ("203.0.113.7", 1000),
            "tun121|x": ("203.0.113.8", 1000),
        })
        proxy.register_tun_dns_servers("tun120", ["10.212.254.254"])
        self.assertNotIn("tun120|x", proxy._dns_cache)
        self.assertIn("tun121|x", proxy._dns_cache)

    def test_clear_registration_invalidates_only_that_device_cache(self):
        proxy.register_tun_dns_servers("tun120", ["10.211.254.254"])
        proxy._dns_cache.update({
            "tun120|x": ("203.0.113.7", 1000),
            "tun121|x": ("203.0.113.8", 1000),
        })
        proxy.clear_tun_dns_servers("tun120")
        self.assertNotIn("tun120|x", proxy._dns_cache)
        self.assertIn("tun121|x", proxy._dns_cache)
        self.assertNotIn("10.211.254.254", proxy.get_tun_dns_servers("tun120"))

    def test_udp_and_fragmented_tcp_response_share_one_strict_deadline(self):
        clock = FakeClock()
        answer = dns_answer()
        udp = DeadlineSocket(clock, socket.SOCK_DGRAM)
        tcp = DeadlineSocket(clock, socket.SOCK_STREAM, len(answer).to_bytes(2, "big") + answer)
        with mock.patch.object(proxy.time, "monotonic", side_effect=clock.monotonic), mock.patch.object(
            proxy.socket, "socket", side_effect=[udp, tcp]
        ), mock.patch("random.getrandbits", return_value=0x1234), mock.patch.object(
            socket, "SO_BINDTODEVICE", 25, create=True
        ):
            self.assertIsNone(proxy.dns_query_over_tun0("x", 1, "8.8.8.8", 1.0, "tun120"))
        self.assertLessEqual(clock.now, 1.0 + 1e-6)
        self.assertTrue(udp.closed)
        self.assertTrue(tcp.closed)
        self.assertIn((socket.SOL_SOCKET, 25, b"tun120"), tcp.bindings)

    def test_small_query_budget_is_not_rounded_up_past_deadline(self):
        clock = FakeClock()
        sockets = []

        def make_socket(_family, kind, *_args):
            result = DeadlineSocket(clock, kind)
            sockets.append(result)
            return result

        with mock.patch.object(proxy.time, "monotonic", side_effect=clock.monotonic), mock.patch.object(
            proxy.socket, "socket", side_effect=make_socket
        ), mock.patch.object(socket, "SO_BINDTODEVICE", 25, create=True):
            self.assertIsNone(proxy.dns_query_over_tun0("x", 1, "8.8.8.8", 0.05, "tun120"))
        self.assertLessEqual(clock.now, 0.05 + 1e-6)
        self.assertTrue(all(sock.closed for sock in sockets))

    def test_resolver_shares_total_budget_across_servers_and_query_types(self):
        clock = FakeClock()
        attempts = []

        def query(host, kind, server, timeout, device):
            attempts.append((kind, server, timeout))
            clock.now += timeout
            return None

        with mock.patch.object(proxy.time, "monotonic", side_effect=clock.monotonic), mock.patch.object(
            proxy.time, "time", side_effect=clock.time
        ), mock.patch.object(proxy, "get_tun_dns_servers", return_value=["10.211.254.254", "8.8.8.8", "1.1.1.1"]), mock.patch.object(
            proxy, "dns_query_over_tun0", side_effect=query
        ):
            self.assertIsNone(proxy.resolve_dns_over_tun0("x", timeout=2.5, device="tun120"))
        self.assertLessEqual(clock.now, 2.5 + 1e-6)
        self.assertTrue(attempts)
        self.assertTrue(all(timeout > 0 for _, _, timeout in attempts))
        self.assertLessEqual(sum(timeout for _, _, timeout in attempts), 2.5 + 1e-6)

    def test_failed_tunnel_dns_is_explicit_and_never_uses_system_dns(self):
        with mock.patch.object(proxy, "resolve_dns_over_tun0", return_value=None), mock.patch.object(
            proxy.socket, "getaddrinfo", side_effect=AssertionError("系统 DNS 不应接管失败的隧道解析")
        ) as getaddrinfo, mock.patch.object(proxy.socket, "socket") as make_socket:
            with self.assertRaises(socket.gaierror):
                proxy.create_connection(("ping0.cc", 443), device="tun120")
        getaddrinfo.assert_not_called()
        make_socket.assert_not_called()

    def test_literal_ip_connects_without_dns_query(self):
        for address, family, sockaddr in (
            ("203.0.113.7", socket.AF_INET, ("203.0.113.7", 443)),
            ("2001:db8::7", socket.AF_INET6, ("2001:db8::7", 443, 0, 0)),
        ):
            with self.subTest(address=address):
                upstream = mock.MagicMock()
                with mock.patch.object(proxy, "dns_query_over_tun0") as query, mock.patch.object(
                    proxy.socket, "getaddrinfo", return_value=[(family, socket.SOCK_STREAM, 0, "", sockaddr)]
                ), mock.patch.object(proxy.socket, "socket", return_value=upstream), mock.patch.object(
                    socket, "SO_BINDTODEVICE", 25, create=True
                ):
                    self.assertIs(proxy.create_connection((address, 443), device="tun120"), upstream)
                query.assert_not_called()
                self.assertEqual(upstream.connect.call_args.args[0][0], address)
                upstream.setsockopt.assert_called_with(socket.SOL_SOCKET, 25, b"tun120")

    def test_concurrent_same_device_hostname_shares_one_resolution(self):
        started = threading.Barrier(6)
        release = threading.Event()
        query_started = threading.Event()
        calls = []
        results = []
        errors = []
        calls_lock = threading.Lock()

        def query(host, kind, server, timeout, device):
            with calls_lock:
                calls.append((host, kind, server, device))
            query_started.set()
            if not release.wait(2):
                raise AssertionError("测试没有释放 DNS 查询")
            return "203.0.113.7"

        def worker():
            try:
                started.wait(2)
                results.append(proxy.resolve_dns_over_tun0("x", device="tun120"))
            except Exception as exc:
                errors.append(exc)

        with mock.patch.object(proxy, "get_tun_dns_servers", return_value=["8.8.8.8"]), mock.patch.object(
            proxy, "dns_query_over_tun0", side_effect=query
        ):
            workers = [threading.Thread(target=worker) for _ in range(6)]
            for thread in workers:
                thread.start()
            try:
                self.assertTrue(query_started.wait(1))
                # 给 Barrier 同时放行的线程进入解析器；查询本身仍由 Event 控制。
                release.wait(0.05)
            finally:
                release.set()
                for thread in workers:
                    thread.join(2)
        self.assertTrue(all(not thread.is_alive() for thread in workers))
        self.assertEqual(errors, [])
        self.assertEqual(results, ["203.0.113.7"] * 6)
        self.assertEqual(len(calls), 1)

    def test_same_hostname_on_different_devices_resolves_independently(self):
        both_queries = threading.Barrier(2)
        results = {}
        errors = []

        def query(host, kind, server, timeout, device):
            both_queries.wait(1)
            return "203.0.113.7" if device == "tun120" else "203.0.113.8"

        def worker(device):
            try:
                results[device] = proxy.resolve_dns_over_tun0("x", device=device)
            except Exception as exc:
                errors.append(exc)

        with mock.patch.object(proxy, "get_tun_dns_servers", return_value=["8.8.8.8"]), mock.patch.object(
            proxy, "dns_query_over_tun0", side_effect=query
        ):
            workers = [threading.Thread(target=worker, args=(device,)) for device in ("tun120", "tun121")]
            for thread in workers:
                thread.start()
            for thread in workers:
                thread.join(2)
        self.assertTrue(all(not thread.is_alive() for thread in workers))
        self.assertEqual(errors, [])
        self.assertEqual(results, {"tun120": "203.0.113.7", "tun121": "203.0.113.8"})


if __name__ == "__main__":
    unittest.main()
