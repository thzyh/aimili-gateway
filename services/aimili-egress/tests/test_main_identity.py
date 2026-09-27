import unittest
from unittest import mock

import vpngate_manager as manager


class MainIdentityTests(unittest.TestCase):
    def test_live_identity_survives_catalog_eviction_but_never_crosses_nodes(self):
        identity = {"id": "jp-live", "country_short": "JP", "country": "日本", "ip_type": "residential"}
        with (
            mock.patch.object(manager, "active_openvpn_identity", identity, create=True),
            mock.patch.object(manager, "active_openvpn_node_id", "jp-live"),
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "get_state", return_value={"proxy_ok": True, "proxy_ip": "203.0.113.9"}),
        ):
            status = manager.safe_main_status()
            self.assertEqual(status["country"], "JP")
            self.assertEqual(status["country_name"], "日本")
            self.assertEqual(status["proxy_type"], "residential")
            self.assertTrue(status["egress_ok"])
            with mock.patch.object(manager, "active_openvpn_node_id", "different-main"):
                self.assertEqual(manager.safe_main_status()["country"], "")

    def test_identity_does_not_turn_a_failed_tunnel_healthy(self):
        identity = {"id": "jp-live", "country_short": "JP", "country": "日本", "ip_type": "residential"}
        with (
            mock.patch.object(manager, "active_openvpn_identity", identity, create=True),
            mock.patch.object(manager, "active_openvpn_node_id", "jp-live"),
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "active_openvpn_running", return_value=False),
            mock.patch.object(manager, "get_state", return_value={"proxy_ok": False}),
        ):
            status = manager.safe_main_status()
            self.assertFalse(status["active"])
            self.assertFalse(status["egress_ok"])
            self.assertEqual(status["exit_ip"], "")
