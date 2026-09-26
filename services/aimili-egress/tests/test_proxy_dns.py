import errno
import socket
import unittest
from unittest import mock

import proxy_server as proxy


class ProxyDNSTests(unittest.TestCase):
    def tearDown(self):
        proxy.purge_dns_cache()

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


if __name__ == "__main__":
    unittest.main()
