import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as manager


class EgressLatencyTests(unittest.TestCase):
    def test_startup_cleanup_removes_only_orphaned_test_policy_tables(self):
        rules = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=(
                "0: from all lookup local\n"
                "32740: from all oif tun0 lookup 100\n"
                "32741: from all oif tun4 [detached] lookup 61004\n"
                "32742: from all oif tun8 [detached] lookup 61008\n"
                "32766: from all lookup main\n"
            ),
            stderr="",
        )
        with (
            mock.patch.object(manager.sys, "platform", "linux"),
            mock.patch.object(manager.subprocess, "run", return_value=rules),
            mock.patch.object(manager, "cleanup_policy_routing") as cleanup,
        ):
            manager.cleanup_orphaned_test_policy_routes()

        self.assertEqual(
            [call.args[0] for call in cleanup.call_args_list],
            [61004, 61008],
        )

    def test_api_fetch_falls_back_to_existing_main_tunnel(self):
        with (
            mock.patch.object(manager.vpn_utils, "get_upstream_proxy", return_value=(None, None, None)),
            mock.patch.object(manager.urllib.request, "urlopen", side_effect=OSError("direct TLS reset")),
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "policy_routing_ready", return_value=True),
            mock.patch.object(
                manager,
                "fetch_api_text_via_interface",
                return_value="#HostName,IP\n",
            ) as tunnel_fetch,
        ):
            actual = manager.fetch_api_text("https://www.vpngate.net/api/iphone/", True)

        self.assertEqual(actual, "#HostName,IP\n")
        tunnel_fetch.assert_called_once_with(
            "https://www.vpngate.net/api/iphone/", "tun0", True
        )

    def test_api_fetch_does_not_use_tunnel_fallback_without_active_main_connection(self):
        with (
            mock.patch.object(manager.vpn_utils, "get_upstream_proxy", return_value=(None, None, None)),
            mock.patch.object(manager.urllib.request, "urlopen", side_effect=OSError("direct TLS reset")),
            mock.patch.object(manager, "active_openvpn_running", return_value=False),
            mock.patch.object(
                manager,
                "fetch_api_text_via_interface",
                create=True,
            ) as tunnel_fetch,
        ):
            with self.assertRaisesRegex(OSError, "direct TLS reset"):
                manager.fetch_api_text("https://www.vpngate.net/api/iphone/", True)

        tunnel_fetch.assert_not_called()

    def test_probe_tunnel_egress_uses_median_total_time(self):
        responses = [
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="198.51.100.10\n__AIMILI_EGRESS_IP__",
                stderr="",
            ),
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="__AIMILI_EGRESS_METRICS__204\t0.100\t0.300",
                stderr="",
            ),
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout="__AIMILI_EGRESS_METRICS__204\t0.200\t0.500",
                stderr="",
            ),
        ]

        with mock.patch.object(manager.subprocess, "run", side_effect=responses) as run:
            result = manager.probe_tunnel_egress(
                "tun7",
                urls=("http://first.invalid", "http://second.invalid"),
                ip_check_urls=("http://ip.invalid",),
                timeout=3,
            )

        self.assertTrue(result["ok"])
        self.assertEqual(result["latency_ms"], 400)
        self.assertEqual(result["connect_latency_ms"], 150)
        self.assertEqual(result["successes"], 2)
        self.assertEqual(result["ip"], "198.51.100.10")
        self.assertEqual(run.call_count, 3)
        for call in run.call_args_list:
            command = call.args[0]
            self.assertEqual(command[command.index("--interface") + 1], "tun7")

    def test_node_quality_key_prefers_real_egress_and_falls_back_to_server_latency(self):
        nodes = [
            {
                "id": "fake-fast",
                "probe_status": "available",
                "latency_ms": 1,
                "score": 100,
            },
            {
                "id": "real-fast-tcp",
                "probe_status": "available",
                "latency_ms": 30,
                "egress_latency_ms": 220,
                "egress_latency_checked_at": 100,
                "proto": "tcp",
                "score": 10,
            },
            {
                "id": "real-fast-udp",
                "probe_status": "available",
                "latency_ms": 40,
                "egress_latency_ms": 220,
                "egress_latency_checked_at": 100,
                "proto": "udp",
                "score": 5,
            },
            {
                "id": "real-slow",
                "probe_status": "available",
                "latency_ms": 2,
                "egress_latency_ms": 800,
                "egress_latency_checked_at": 100,
                "score": 200,
            },
        ]

        with mock.patch.object(manager, "active_openvpn_node_id", ""):
            actual = manager.sort_all_nodes(nodes)

        self.assertEqual(
            [node["id"] for node in actual],
            ["real-fast-udp", "real-fast-tcp", "real-slow", "fake-fast"],
        )

    def test_probe_one_node_records_real_egress_and_cleans_temporary_resources(self):
        process = mock.Mock()
        with tempfile.TemporaryDirectory() as temp_dir:
            config_dir = Path(temp_dir)
            with (
                mock.patch.object(manager, "CONFIG_DIR", config_dir),
                mock.patch.object(manager.vpn_utils, "ping_latency_ms", return_value=5),
                mock.patch.object(manager, "get_free_test_index", return_value=7),
                mock.patch.object(
                    manager,
                    "run_openvpn_until_ready",
                    return_value=(True, "OpenVPN connected", process),
                ) as connect,
                mock.patch.object(manager, "setup_policy_routing", return_value=True) as route,
                mock.patch.object(
                    manager,
                    "check_interface_exit_ip",
                    return_value=(True, "198.51.100.1"),
                ),
                mock.patch.object(
                    manager,
                    "probe_tunnel_egress",
                    side_effect=[
                        {
                            "ok": True,
                            "ip": "198.51.100.1",
                            "latency_ms": 420,
                            "connect_latency_ms": 180,
                            "successes": 2,
                            "message": "ok",
                        },
                        {
                            "ok": True,
                            "ip": "198.51.100.1",
                            "latency_ms": 500,
                            "connect_latency_ms": 220,
                            "successes": 2,
                            "message": "ok",
                        },
                    ],
                ) as egress,
                mock.patch.object(manager, "cleanup_policy_routing") as cleanup,
                mock.patch.object(manager, "stop_process") as stop,
                mock.patch.object(manager, "release_test_index") as release,
                mock.patch.object(manager.time, "time", return_value=1234.5),
            ):
                result = manager._probe_one_node(
                    {
                        "id": "node-a",
                        "config_text": "remote 198.51.100.10 443 tcp\n",
                        "remote_host": "198.51.100.10",
                        "remote_port": 443,
                        "ping": 1,
                    }
                )

            self.assertEqual(list(config_dir.iterdir()), [])

        self.assertEqual(result["probe_status"], "available")
        self.assertEqual(result["latency_ms"], 5)
        self.assertEqual(result["egress_latency_ms"], 460)
        self.assertEqual(result["egress_connect_latency_ms"], 200)
        self.assertEqual(result["egress_probe_successes"], 4)
        self.assertEqual(result["egress_probe_rounds"], 2)
        self.assertEqual(result["egress_latency_checked_at"], 1234.5)
        self.assertEqual(connect.call_count, 1)
        for call in connect.call_args_list:
            self.assertTrue(call.kwargs["keep_alive"])
            self.assertEqual(call.kwargs["timeout"], 12)
        self.assertEqual(route.call_count, 1)
        self.assertEqual(egress.call_count, 2)
        self.assertEqual(cleanup.call_count, 1)
        self.assertEqual(stop.call_count, 1)
        release.assert_called_once_with(7)

    def test_probe_one_node_rejects_openvpn_without_working_egress(self):
        process = mock.Mock()
        with tempfile.TemporaryDirectory() as temp_dir:
            with (
                mock.patch.object(manager, "CONFIG_DIR", Path(temp_dir)),
                mock.patch.object(manager.vpn_utils, "ping_latency_ms", return_value=2),
                mock.patch.object(manager, "get_free_test_index", return_value=9),
                mock.patch.object(
                    manager,
                    "run_openvpn_until_ready",
                    return_value=(True, "OpenVPN connected", process),
                ),
                mock.patch.object(manager, "setup_policy_routing", return_value=True),
                mock.patch.object(
                    manager,
                    "check_interface_exit_ip",
                    return_value=(False, ""),
                ),
                mock.patch.object(
                    manager,
                    "probe_tunnel_egress",
                    return_value={
                        "ok": False,
                        "latency_ms": 0,
                        "connect_latency_ms": 0,
                        "successes": 0,
                        "message": "no valid egress response",
                    },
                ) as egress,
                mock.patch.object(manager, "cleanup_policy_routing"),
                mock.patch.object(manager, "stop_process"),
                mock.patch.object(manager, "release_test_index"),
            ):
                result = manager._probe_one_node(
                    {
                        "id": "node-b",
                        "config_text": "remote 203.0.113.10 443 udp\n",
                        "remote_host": "203.0.113.10",
                        "remote_port": 443,
                    }
                )

        self.assertEqual(result["probe_status"], "unavailable")
        self.assertEqual(result["egress_latency_ms"], 0)
        self.assertEqual(result["probe_message"], "公网出口检测失败")
        egress.assert_not_called()

    def test_slot_selection_uses_real_egress_latency(self):
        nodes = [
            {
                "id": "fake-fast",
                "probe_status": "available",
                "latency_ms": 1,
                "egress_latency_ms": 900,
                "egress_latency_checked_at": 100,
            },
            {
                "id": "real-fast",
                "probe_status": "available",
                "latency_ms": 50,
                "egress_latency_ms": 200,
                "egress_latency_checked_at": 100,
            },
        ]

        with mock.patch.object(manager, "read_nodes", return_value=nodes):
            selected = manager.select_slot_nodes(set(), 1, "", False)

        self.assertEqual(selected[0]["id"], "real-fast")

    def test_auto_switch_uses_real_egress_latency(self):
        nodes = [
            {
                "id": "fake-fast",
                "probe_status": "available",
                "latency_ms": 1,
                "egress_latency_ms": 900,
                "egress_latency_checked_at": 100,
            },
            {
                "id": "real-fast",
                "probe_status": "available",
                "latency_ms": 50,
                "egress_latency_ms": 200,
                "egress_latency_checked_at": 100,
            },
        ]
        ui_config = {
            "connection_enabled": True,
            "routing_mode": "auto",
            "routing_ip_type": "all",
            "routing_isp": "",
        }

        with (
            mock.patch.object(manager, "load_ui_config", return_value=ui_config),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "main_bad_node_ids", return_value=set()),
            mock.patch.object(manager, "connect_node") as connect,
            mock.patch.object(manager, "log_to_json"),
        ):
            manager.auto_switch_node()

        connect.assert_called_once_with("real-fast")


if __name__ == "__main__":
    unittest.main()
