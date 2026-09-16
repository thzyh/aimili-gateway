import tempfile
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as manager
from egress_repair import RepairStore


class FakeProcess:
    def __init__(self, alive=True):
        self.alive = alive

    def poll(self):
        return None if self.alive else 1


class DedicatedStandbyTests(unittest.TestCase):
    def setUp(self):
        manager.dedicated_standbys.clear()
        manager.dedicated_standby_proxy_stops.clear()
        manager.dedicated_standby_proxy_registries.clear()
        manager.dedicated_standby_fail_counts.clear()
        manager.pending_candidate_ids.clear()

    def tearDown(self):
        manager.dedicated_standbys.clear()
        manager.dedicated_standby_proxy_stops.clear()
        manager.dedicated_standby_proxy_registries.clear()
        manager.dedicated_standby_fail_counts.clear()
        manager.pending_candidate_ids.clear()

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

    def test_two_configs_persist_independent_targets_and_country_scopes(self):
        with tempfile.TemporaryDirectory() as temporary:
            data_dir = Path(temporary)
            with (
                mock.patch.object(manager, "DATA_DIR", data_dir),
                mock.patch.object(manager, "get_active_slots", return_value=[0, 1, 2, 3]),
                mock.patch.object(manager, "tear_down_dedicated_standby", return_value={}),
                mock.patch.object(manager, "maintain_dedicated_standbys_once"),
            ):
                result = manager.set_dedicated_standby_config([
                    {"index": 0, "target": "main", "countries": ["JP", "US"]},
                    {"index": 1, "target": "slot:2", "countries": ["VN"]},
                ])
                saved = manager.dedicated_standby_config_snapshot()

        self.assertTrue(result["ok"])
        self.assertEqual(saved[0], {"index": 0, "target": "main", "countries": ["JP", "US"]})
        self.assertEqual(saved[1], {"index": 1, "target": "slot:2", "countries": ["VN"]})

    def test_selection_honors_chosen_countries_and_excludes_active_or_other_standby(self):
        nodes = [
            {"id": "jp-free", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 20},
            {"id": "us-free", "country_short": "US", "ip_type": "residential", "probe_status": "available", "latency_ms": 10},
            {"id": "kr-out", "country_short": "KR", "ip_type": "residential", "probe_status": "available", "latency_ms": 1},
            {"id": "jp-used", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 2},
            {"id": "main-used", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 3, "exit_ip": "203.0.113.20"},
            {"id": "jp-same-ip", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 4, "exit_ip": "203.0.113.20"},
        ]
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

        self.assertEqual([item["id"] for item in selected], ["us-free", "jp-free"])

    def test_manual_assignment_can_retry_candidate_in_automatic_cooldown(self):
        node = {
            "id": "jp-manual",
            "country_short": "JP",
            "ip_type": "hosting",
            "probe_status": "available",
            "latency_ms": 20,
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

    def test_first_health_failure_keeps_standby_and_second_failure_enters_manual_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            repair_store = RepairStore(Path(temporary) / "repair.json")
            with manager.dedicated_standby_lock:
                manager.dedicated_standbys[0] = {
                    "node_id": "jp-old", "process": FakeProcess(), "egress_ok": True,
                    "device": manager.standby_device(0), "table": manager.standby_table(0),
                }
            with (
                mock.patch.object(manager, "egress_repair_store", repair_store),
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
                manager.maintain_dedicated_standbys_once()
                self.assertIn(0, manager.dedicated_standbys)
                provision.assert_not_called()
                manager.maintain_dedicated_standbys_once()
                self.assertEqual(repair_store.get("standby:0")["status"], "manual_required")
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
                manager.maintain_dedicated_standbys_once()

        provision.assert_not_called()


if __name__ == "__main__":
    unittest.main()
