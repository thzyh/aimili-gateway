import json
import tempfile
import threading
import unittest
from contextlib import ExitStack
from pathlib import Path
from unittest import mock

import runtime_proxy_bridge as bridge


class RuntimeProxyBridgeTests(unittest.TestCase):
    def setUp(self):
        self.patches = ExitStack()
        self.addCleanup(self.patches.close)
        self.directory = Path(self.patches.enter_context(tempfile.TemporaryDirectory()))
        self.threads = self.patches.enter_context(mock.patch.object(bridge.threading, "Thread"))
        self.clear_dns = self.patches.enter_context(mock.patch.object(bridge.proxy, "clear_tun_dns_servers"))
        self.register_dns = self.patches.enter_context(mock.patch.object(bridge.proxy, "register_tun_dns_servers"))
        self.patches.enter_context(mock.patch.object(bridge, "_process_endpoints", {}))

    def write(self, name, data):
        path = self.directory / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(data), encoding="utf-8")
        return path

    def tunnel(self, device="tun121", node="JP_114.150.253.138_1105_udp", pid=101):
        return {"device": device, "stem": node, "pid": pid,
                "endpoint": ("114.150.253.138", 1105, "udp")}

    def test_main_exact_config_stem_does_not_require_candidate_catalog(self):
        tunnel = self.tunnel()
        self.assertIs(bridge.main_tunnel(tunnel["stem"], [], [tunnel]), tunnel)
        self.assertIsNone(bridge.main_tunnel("", [], [tunnel]))
        self.assertIsNone(bridge.main_tunnel(tunnel["stem"], [], [tunnel, {**tunnel, "pid": 102}]))

    def test_promoted_main_matches_endpoint_and_id_fallback_but_rejects_ambiguity(self):
        active_id = "JP_114.150.253.138_1105_udp"
        promoted = self.tunnel(node=".promoted_main-old")
        node = {"id": active_id, "remote_host": "114.150.253.138", "remote_port": 1105, "proto": "udp"}
        self.assertIs(bridge.main_tunnel(active_id, [node], [promoted]), promoted)
        self.assertIs(bridge.main_tunnel(active_id, [], [promoted]), promoted)
        self.assertIsNone(bridge.main_tunnel(active_id, [node], [promoted, {**promoted, "pid": 102}]))
        self.assertIsNone(bridge.main_tunnel("node-without-endpoint", [], [promoted]))

    def test_tunnel_scan_requires_engine_cgroup_and_unique_config_remote(self):
        proc = self.directory / "proc"
        config = self.directory / "candidate.ovpn"
        config.write_text("proto udp\nremote 114.150.253.138 1105\n", encoding="utf-8")
        for pid, group in [(100, "0::/system.slice/aimilivpn.service\n"), (101, "0::/system.slice/aimilivpn.service\n"), (102, "0::/other.service\n")]:
            root = proc / str(pid)
            root.mkdir(parents=True)
            root.joinpath("cgroup").write_text(group)
            root.joinpath("stat").write_text(f"{pid} (openvpn) " + " ".join(["S"] + ["0"] * 18 + ["100"]))
            root.joinpath("cmdline").write_bytes(b"\0".join(s.encode() for s in ["/usr/sbin/openvpn", "--dev", "tun121", "--config", str(config)]))
        proc.joinpath("100", "cmdline").write_bytes(b"python3\0manager.py\0")
        tunnels = bridge.engine_tunnels(100, proc)
        self.assertEqual([t["pid"] for t in tunnels], [101])
        self.assertEqual(tunnels[0]["endpoint"], ("114.150.253.138", 1105, "udp"))
        config.write_text("remote one.example 1194\nremote two.example 1194\n")
        self.assertIsNone(bridge.config_endpoint(config))

    def test_missing_promoted_config_keeps_slot_device_and_retries_endpoint(self):
        proc, config = self.directory / "proc", self.directory / ".standby_0.ovpn"
        for pid in (100, 101):
            root = proc / str(pid)
            root.mkdir(parents=True)
            root.joinpath("cgroup").write_text("0::/system.slice/aimilivpn.service\n")
            root.joinpath("cmdline").write_bytes(b"python3\0manager.py\0" if pid == 100 else b"\0".join(s.encode() for s in ["openvpn", "--dev", "tun124", "--config", str(config)]))
            root.joinpath("stat").write_text(f"{pid} (openvpn) " + " ".join(["S"] + ["0"] * 18 + ["100"]))
        first = bridge.engine_tunnels(100, proc)
        self.assertEqual(first[0]["device"], "tun124")
        self.assertIsNone(first[0]["endpoint"])
        self.assertEqual(bridge._process_endpoints, {})
        config.write_text("proto udp\nremote 114.150.253.138 1105\n")
        self.assertEqual(bridge.engine_tunnels(100, proc)[0]["endpoint"], ("114.150.253.138", 1105, "udp"))

    def test_process_endpoint_survives_reused_standby_file_until_pid_exit(self):
        proc, config = self.directory / "proc", self.directory / ".standby_0.ovpn"
        for pid in (100, 101):
            root = proc / str(pid)
            root.mkdir(parents=True)
            root.joinpath("cgroup").write_text("0::/system.slice/aimilivpn.service\n")
            root.joinpath("cmdline").write_bytes(b"python3\0manager.py\0" if pid == 100 else b"\0".join(s.encode() for s in ["openvpn", "--dev", "tun124", "--config", str(config)]))
            root.joinpath("stat").write_text(f"{pid} (openvpn) " + " ".join(["S"] + ["0"] * 18 + ["100"]))
        config.write_text("proto udp\nremote 114.150.253.138 1105\n")
        first = bridge.engine_tunnels(100, proc)
        config.write_text("proto tcp-client\nremote 203.0.113.9 1194\n")
        after_cover = bridge.engine_tunnels(100, proc)
        self.assertEqual(first[0]["endpoint"], after_cover[0]["endpoint"])
        self.assertIsNotNone(bridge.main_tunnel("JP_114.150.253.138_1105_udp", [], after_cover))
        proc.joinpath("101", "stat").write_text("101 (openvpn) " + " ".join(["S"] + ["0"] * 18 + ["200"]))
        reused_pid = bridge.engine_tunnels(100, proc)
        self.assertEqual(reused_pid[0]["endpoint"], ("203.0.113.9", 1194, "tcp"))
        self.assertNotIn((101, "100"), bridge._process_endpoints)

    def test_slots_follow_persisted_device_port_and_preserve_unaffected_connections(self):
        self.write("state.json", {})
        self.write("nodes.json", [])
        self.write("slots.json", {"slots": [{"slot": 1, "port": 17929, "device": "tun121", "node_id": "node-a"}]})
        bindings, stop = {}, threading.Event()
        with mock.patch.object(bridge, "engine_tunnels", return_value=[self.tunnel()]), mock.patch.object(bridge.socket, "if_nametoindex", return_value=41):
            bridge.refresh_bindings(self.directory, 100, 20000, [], bindings, stop)
        self.assertEqual(bindings[17929].device(), "tun121")
        self.assertEqual(bindings[17929].identity, ("tun121", 41, "node-a", 101))
        listener_args = [call.kwargs.get("args", ()) for call in self.threads.call_args_list]
        self.assertTrue(any(args[:2] == ("127.0.0.1", 37929) and args[-1] is bridge.proxy.proxy_capacity for args in listener_args))
        registry = mock.Mock()
        bindings[17929].registry = registry
        self.write("slots.json", {"slots": [{"slot": 1, "port": 17929, "device": "tun126", "node_id": "node-b"}]})
        with mock.patch.object(bridge, "engine_tunnels", return_value=[self.tunnel("tun126", pid=105)]), mock.patch.object(bridge.socket, "if_nametoindex", return_value=51):
            bridge.refresh_bindings(self.directory, 100, 20000, [], bindings, stop)
        self.assertEqual(bindings[17929].device(), "tun126")
        registry.close_all.assert_called_once()
        self.clear_dns.assert_called_once_with("tun121")

    def test_ambiguous_main_fails_closed_while_slot_keeps_its_device(self):
        active_id = "JP_114.150.253.138_1105_udp"
        self.write("state.json", {"active_openvpn_node_id": active_id})
        self.write("nodes.json", [])
        self.write("slots.json", {"slots": [{"slot": 0, "port": 17928, "device": "tun120", "node_id": "slot-a"}]})
        tunnels = [self.tunnel(), self.tunnel("tun125", pid=102), self.tunnel("tun120", node="slot-a", pid=103)]
        bindings = {}
        with mock.patch.object(bridge, "engine_tunnels", return_value=tunnels), mock.patch.object(bridge.socket, "if_nametoindex", return_value=41):
            bridge.refresh_bindings(self.directory, 100, 20000, [], bindings, threading.Event())
        with self.assertRaisesRegex(RuntimeError, "ERR_BRIDGE_BINDING_UNRESOLVED"):
            bindings[7928].device()
        self.assertEqual(bindings[17928].device(), "tun120")

    def test_identity_changes_close_connections_and_only_clear_old_device(self):
        binding = bridge.Binding(17929)
        binding.registry = mock.Mock()
        identity = ("tun121", 41, "node-a", 101)
        binding.update(identity, [])
        binding.registry.close_all.reset_mock()
        binding.update(identity, [])
        binding.registry.close_all.assert_not_called()
        for changed in [("tun121", 42, "node-a", 101), ("tun121", 42, "node-b", 101)]:
            binding.update(changed, [])
        self.assertEqual(binding.registry.close_all.call_count, 2)
        self.assertEqual(self.clear_dns.call_args_list, [mock.call("tun121"), mock.call("tun121")])

    def test_observed_dns_is_sorted_but_registered_only_after_bound_query_success(self):
        binding = bridge.Binding(17929)
        binding.identity, binding.generation = ("tun121", 41, "node-a", 101), 1
        with mock.patch.object(bridge, "local_ipv4", return_value="10.223.1.17"), mock.patch.object(bridge.proxy, "dns_query_over_tun0", side_effect=[None, "203.0.113.7"]) as query:
            binding.verify_dns(binding.identity, 1, ["10.211.254.254", "10.223.254.254"])
        self.assertEqual(query.call_args_list, [
            mock.call("www.gstatic.com", 1, "10.223.254.254", 1.0, "tun121"),
            mock.call("www.gstatic.com", 1, "10.211.254.254", 1.0, "tun121"),
        ])
        self.register_dns.assert_called_once_with("tun121", ["10.211.254.254"])

    def test_stale_dns_worker_cannot_register_on_changed_binding(self):
        binding = bridge.Binding(17929)
        binding.identity, binding.generation = ("tun121", 41, "node-a", 101), 1

        def change_binding(*_):
            binding.update(("tun126", 42, "node-b", 105), [])
            return "203.0.113.7"

        with mock.patch.object(bridge, "local_ipv4", return_value="10.211.1.17"), mock.patch.object(bridge.proxy, "dns_query_over_tun0", side_effect=change_binding):
            binding.verify_dns(("tun121", 41, "node-a", 101), 1, ["10.211.254.254"])
        self.register_dns.assert_not_called()

    def test_dns_candidates_keep_only_observed_private_ipv4_and_limit_probes(self):
        path = self.write("dns.json", ["10.211.254.254", "10.223.254.254", "8.8.8.8", "127.0.0.1", "2001:db8::1", "invalid", "10.211.254.254"])
        self.assertEqual(bridge.dns_candidates(path), ["10.211.254.254", "10.223.254.254"])


if __name__ == "__main__":
    unittest.main()
