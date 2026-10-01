import tempfile
import unittest
from contextlib import ExitStack
from pathlib import Path
from unittest import mock

import vpngate_manager as manager
import capacity
import recovery_policy
from egress_repair import RepairStore


class FakeProcess:
    def __init__(self, alive=True):
        self.alive = alive

    def poll(self):
        return None if self.alive else 1


class RegisteredProcess(FakeProcess):
    def __init__(self, pid, alive=True):
        super().__init__(alive)
        self.pid = pid


class DedicatedStandbyTests(unittest.TestCase):
    def test_resource_selection_excludes_live_slots_and_other_standbys(self):
        slot = {"device": "tun122", "table": 202, "process": FakeProcess()}
        other_standby = {"device": "tun124", "table": 204, "process": FakeProcess()}
        with (
            mock.patch.object(manager, "exit_slots", {2: slot}),
            mock.patch.object(manager, "dedicated_standbys", {0: other_standby}),
            mock.patch.object(manager, "dedicated_standby_resources", {
                3: {"device": "tun122", "table": 202},
            }),
        ):
            device, table = manager._prepare_standby_resource(3)
        self.assertNotIn(device, {"tun122", "tun124"})
        self.assertNotIn(table, {202, 204})

    def test_standby_orphan_cleanup_keeps_registered_active_and_standby_processes(self):
        with tempfile.TemporaryDirectory() as directory:
            proc_root = Path(directory)
            net_root = proc_root / "net"
            (net_root / "tun125").mkdir(parents=True)
            (net_root / "tun126").mkdir()
            commands = {
                101: ["/usr/sbin/openvpn", "--setenv", "AIMILI_STANDBY", "1", "--dev", "tun125"],
                102: ["/usr/sbin/openvpn", "--setenv", "AIMILI_STANDBY", "1", "--dev", "tun120"],
                103: ["/usr/sbin/openvpn", "--setenv", "AIMILI_STANDBY", "2", "--dev", "tun126"],
                104: ["/usr/sbin/openvpn", "--setenv", "NOT_AIMILI_STANDBY", "1"],
            }
            for pid, command in commands.items():
                path = proc_root / str(pid)
                path.mkdir()
                (path / "cmdline").write_bytes(b"\0".join(part.encode() for part in command) + b"\0")
            with (
                mock.patch.object(manager.sys, "platform", "linux"),
                mock.patch.object(manager, "dedicated_standbys", {1: {"process": RegisteredProcess(101)}}),
                mock.patch.object(manager, "exit_slots", {0: {"process": RegisteredProcess(103)}}),
                mock.patch.object(manager.os, "kill") as kill,
                mock.patch.object(manager.time, "sleep"),
                mock.patch.object(manager, "log_to_json"),
            ):
                manager.kill_unregistered_standby_openvpn_processes(1, proc_root=proc_root, net_root=net_root)

        self.assertEqual([call.args[0] for call in kill.call_args_list], [102, 102])

    def test_standby_cleanup_reaps_registered_process_when_tun_device_disappears(self):
        with tempfile.TemporaryDirectory() as directory:
            proc_root = Path(directory)
            net_root = proc_root / "net"
            net_root.mkdir()
            path = proc_root / "101"
            path.mkdir()
            (path / "cmdline").write_bytes(
                b"\0".join(part.encode() for part in [
                    "/usr/sbin/openvpn", "--setenv", "AIMILI_STANDBY", "1",
                    "--dev", "tun120",
                ]) + b"\0"
            )
            with (
                mock.patch.object(manager.sys, "platform", "linux"),
                mock.patch.object(manager, "dedicated_standbys", {1: {"process": RegisteredProcess(101)}}),
                mock.patch.object(manager, "exit_slots", {}),
                mock.patch.object(manager.os, "kill") as kill,
                mock.patch.object(manager.time, "sleep"),
                mock.patch.object(manager, "log_to_json"),
            ):
                manager.kill_unregistered_standby_openvpn_processes(1, proc_root=proc_root, net_root=net_root)

        self.assertEqual([call.args[0] for call in kill.call_args_list], [101, 101])

    def test_repeated_promotion_exchanges_active_and_standby_resources(self):
        index, slot = 3, 2
        active = {"device": manager.slot_device(slot), "table": manager.slot_table(slot),
                  "process": FakeProcess(), "node_id": "active"}
        standby = {"device": f"tun{manager.STANDBY_DEV_BASE + index}",
                   "table": manager.STANDBY_TABLE_BASE + index,
                   "process": FakeProcess(), "node_id": "standby"}
        with ExitStack() as stack:
            stack.enter_context(mock.patch.object(manager, "exit_slots", {slot: active}))
            stack.enter_context(mock.patch.object(manager, "dedicated_standbys", {index: standby}))
            stack.enter_context(mock.patch.object(manager, "dedicated_standby_resources", {}))
            stack.enter_context(mock.patch.object(manager, "_healthy_dedicated_standby_index", return_value=index))
            for name in ["stop_process", "cleanup_policy_routing", "ensure_slot_proxy",
                         "set_slot_pin", "set_slot_country", "set_slot_type", "write_slots_state",
                         "write_dedicated_standby_state", "log_to_json"]:
                stack.enter_context(mock.patch.object(manager, name))
            stack.enter_context(mock.patch.object(manager, "egress_repair_store"))
            stack.enter_context(mock.patch.object(manager.threading, "Thread"))
            stack.enter_context(mock.patch.object(manager, "detach_promoted_standby_config", return_value="unused"))
            self.assertTrue(manager.promote_dedicated_standby_to_slot(slot))
            self.assertEqual(manager.standby_device(index), active["device"])
            manager.dedicated_standbys[index] = {
                **active, "device": manager.standby_device(index),
                "table": manager.standby_table(index), "process": FakeProcess(),
            }
            self.assertTrue(manager.promote_dedicated_standby_to_slot(slot))
            self.assertEqual(manager.standby_device(index), standby["device"])
            self.assertNotEqual(manager.runtime_slot_device(slot), manager.standby_device(index))

    def setUp(self):
        manager.dedicated_standbys.clear()
        manager.dedicated_standby_proxy_stops.clear()
        manager.dedicated_standby_proxy_registries.clear()
        manager.dedicated_standby_fail_counts.clear()
        manager.pending_candidate_ids.clear()
        manager.standby_last_health_check.clear()
        manager.slot_bad_nodes.clear()

    def tearDown(self):
        manager.dedicated_standbys.clear()
        manager.dedicated_standby_proxy_stops.clear()
        manager.dedicated_standby_proxy_registries.clear()
        manager.dedicated_standby_fail_counts.clear()
        manager.pending_candidate_ids.clear()

    def test_promotion_probe_retries_transient_route_failure(self):
        index = 0
        manager.dedicated_standbys[index] = {
            "process": FakeProcess(), "node_id": "standby-node",
            "device": manager.standby_device(index),
            "table": manager.standby_table(index),
            "egress_ok": True,
        }
        with (
            mock.patch.object(manager, "dedicated_standby_config_snapshot", return_value=[
                {"index": index, "target": "slot:0", "countries": []},
            ]),
            mock.patch.object(manager, "ensure_policy_routing", return_value=True),
            mock.patch.object(manager, "check_slot_egress", side_effect=[
                (False, ""), (False, ""), (True, "198.51.100.50"),
            ]) as probe,
            mock.patch.object(manager.time, "sleep"),
        ):
            selected = manager._healthy_dedicated_standby_index("slot:0")

        self.assertEqual(selected, index)
        self.assertEqual(probe.call_count, 3)
        self.assertTrue(manager.dedicated_standbys[index]["egress_ok"])
        self.assertEqual(manager.dedicated_standbys[index]["exit_ip"], "198.51.100.50")

    def test_candidate_reservation_prevents_concurrent_duplicate_assignment(self):
        nodes = [
            {"id": "jp-shared-a", "exit_ip": "203.0.113.9"},
            {"id": "jp-shared-b", "exit_ip": "203.0.113.9"},
        ]
        with mock.patch.object(manager, "read_nodes", return_value=nodes):
            self.assertTrue(manager.reserve_candidate("jp-shared-a", set()))
            self.assertFalse(manager.reserve_candidate("jp-shared-a", set()))
            self.assertFalse(manager.reserve_candidate("jp-shared-b", set()))
            self.assertIn("jp-shared-a", manager.main_reserved_candidate_ids())
            manager.release_candidate_reservation("jp-shared-a")
            self.assertTrue(manager.reserve_candidate("jp-shared-b", set()))
            manager.release_candidate_reservation("jp-shared-b")

    def test_missing_manual_candidate_preserves_healthy_standby_and_recovery(self):
        with tempfile.TemporaryDirectory() as temporary:
            repair = RepairStore(Path(temporary) / 'repair.json')
            repair.mark_healthy('standby:0', 'old')
            manager.dedicated_standbys[0] = {'node_id': 'old', 'process': FakeProcess(), 'egress_ok': True}
            with (
                mock.patch.object(manager, 'egress_repair_store', repair),
                mock.patch.object(manager, 'standby_target_indices', return_value=[0]),
                mock.patch.object(manager, 'dedicated_standby_config_snapshot', return_value=[{'index': 0, 'target': 'main', 'countries': []}]),
                mock.patch.object(manager, 'read_nodes', return_value=[]),
                mock.patch.object(manager, 'provision_dedicated_standby', return_value=False) as provision,
                mock.patch.object(manager, 'write_dedicated_standby_state'),
            ):
                result = manager.assign_dedicated_standby(0, 'missing')
            self.assertEqual(result.get('error_code'), 'candidate_not_found')
            self.assertEqual(repair.get('standby:0')['status'], 'healthy')
            self.assertTrue(manager.dedicated_standbys[0]['egress_ok'])
            provision.assert_not_called()

    def test_four_configs_use_fixed_one_to_one_targets_and_no_country_scope(self):
        with tempfile.TemporaryDirectory() as temporary:
            data_dir = Path(temporary)
            with (
                mock.patch.object(manager, "DATA_DIR", data_dir),
                mock.patch.object(manager, "get_active_slots", return_value=[0, 1, 2]),
                mock.patch.object(manager, "tear_down_dedicated_standby", return_value={}),
                mock.patch.object(manager, "maintain_dedicated_standbys_once"),
            ):
                result = manager.set_dedicated_standby_config(recovery_policy.targets([0, 1, 2]))
                saved = manager.dedicated_standby_config_snapshot()

        self.assertTrue(result["ok"])
        self.assertEqual(saved, recovery_policy.targets([0, 1, 2]))

    def test_selection_prefers_current_country_and_excludes_occupied_exit_ips(self):
        nodes = [
            {"id": "jp-free", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 20},
            {"id": "us-free", "country_short": "US", "ip_type": "residential", "probe_status": "available", "latency_ms": 10},
            {"id": "kr-out", "country_short": "KR", "ip_type": "residential", "probe_status": "available", "latency_ms": 1},
            {"id": "jp-used", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 2},
            {"id": "main-used", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 3, "exit_ip": "203.0.113.20"},
            {"id": "jp-same-ip", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 4, "exit_ip": "203.0.113.20"},
        ]
        for node in nodes:
            node['config_text'] = 'client'
        with manager.dedicated_standby_lock:
            manager.dedicated_standbys[1] = {"node_id": "jp-used"}
        with (
            mock.patch.object(manager, "dedicated_standby_config_snapshot", return_value=[
                {"index": 0, "target": "slot:0", "countries": ["JP", "US"]},
                {"index": 1, "target": "slot:1", "countries": ["JP"]},
            ]),
            mock.patch.object(manager, "_standby_target_profile", return_value={"country": "JP", "proxy_type": "residential"}),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "main_reserved_candidate_ids", return_value={"main-used"}),
        ):
            selected = manager.select_dedicated_standby_candidates(0)

        self.assertEqual([item["id"] for item in selected], ["jp-free", "kr-out", "us-free"])

    def test_manual_assignment_can_retry_candidate_in_automatic_cooldown(self):
        node = {
            "id": "jp-manual",
            "country_short": "JP",
            "ip_type": "hosting",
            "probe_status": "available",
            "latency_ms": 20,
            "config_text": "client",
        }
        with (
            mock.patch.object(manager, "dedicated_standby_config_snapshot", return_value=[
                {"index": 0, "target": "slot:0", "countries": ["JP"]},
                {"index": 1, "target": "", "countries": []},
            ]),
            mock.patch.object(manager, "_standby_target_profile", return_value={"country": "JP", "proxy_type": "datacenter"}),
            mock.patch.object(manager, "read_nodes", return_value=[node]),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "main_reserved_candidate_ids", return_value=set()),
            mock.patch.object(manager, "slot_bad_nodes", {"jp-manual": float("inf")}),
            mock.patch.object(manager, "bring_up_dedicated_standby", return_value=True) as bring_up,
        ):
            self.assertEqual(manager.select_dedicated_standby_candidates(0), [])
            self.assertTrue(manager.provision_dedicated_standby(0, "jp-manual"))

        bring_up.assert_called_once_with(0, node)

    def test_first_health_failure_keeps_standby_and_second_failure_enters_retry_wait(self):
        with tempfile.TemporaryDirectory() as temporary:
            repair_store = RepairStore(Path(temporary) / "repair.json")
            with manager.dedicated_standby_lock:
                manager.dedicated_standbys[0] = {
                    "node_id": "jp-old", "process": FakeProcess(), "egress_ok": True,
                    "device": manager.standby_device(0), "table": manager.standby_table(0),
                }
            with (
                mock.patch.object(manager, "egress_repair_store", repair_store),
                mock.patch.object(manager, "_standby_enabled", return_value=True),
                mock.patch.object(manager, "_standby_target_profile", return_value={"country": "JP"}),
                mock.patch.object(manager.capacity, "read_host_facts", return_value=capacity.HostFacts(512*1048576, 180*1048576, 1, 0)),
                mock.patch.object(manager, "dedicated_standby_config_snapshot", return_value=[
                    {"index": 0, "target": "slot:0", "countries": ["JP"]},
                    {"index": 1, "target": "", "countries": []},
                ]),
                mock.patch.object(manager, "ensure_policy_routing", return_value=True),
                mock.patch.object(manager, "check_slot_egress", return_value=(False, "")),
                mock.patch.object(manager, "mark_candidate_unavailable"),
                mock.patch.object(manager, "tear_down_dedicated_standby", side_effect=lambda index: manager.dedicated_standbys.pop(index, {})),
                mock.patch.object(manager, "provision_dedicated_standby", return_value=False) as provision,
                mock.patch.object(manager, "write_dedicated_standby_state"),
            ):
                manager._maintain_standby({"index": 0, "target": "slot:0", "countries": []})
                self.assertIn(0, manager.dedicated_standbys)
                provision.assert_not_called()
                manager.standby_last_health_check.clear()
                manager._maintain_standby({"index": 0, "target": "slot:0", "countries": []})
                self.assertEqual(repair_store.get("standby:0")["status"], "retry_wait")
                provision.assert_called_once_with(0)

    def test_manual_required_state_does_not_retry_after_restart(self):
        with tempfile.TemporaryDirectory() as temporary:
            repair_store = RepairStore(Path(temporary) / "repair.json")
            repair_store.require_manual("standby:0", "no_standby_candidate")
            with (
                mock.patch.object(manager, "egress_repair_store", repair_store),
                mock.patch.object(manager, "dedicated_standby_config_snapshot", return_value=[
                    {"index": 0, "target": "main", "countries": ["JP"]},
                    {"index": 1, "target": "", "countries": []},
                ]),
                mock.patch.object(manager, "tear_down_dedicated_standby", return_value={}),
                mock.patch.object(manager, "provision_dedicated_standby") as provision,
                mock.patch.object(manager, "write_dedicated_standby_state"),
            ):
                manager._maintain_standby({"index": 0, "target": "main", "countries": []})

        provision.assert_not_called()


if __name__ == "__main__":
    unittest.main()
