"""复现新部署只有机房候选、专属备用就绪而普通出口空置的场景。"""

from contextlib import ExitStack
import threading
import unittest
from unittest import mock

import vpngate_manager as manager


class InitialExitSupplyTests(unittest.TestCase):
    def setUp(self):
        self.patches = ExitStack()
        self.addCleanup(self.patches.close)
        self.nodes = [
            {"id": "dc", "ip": "198.51.100.20", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 10},
            {"id": "home", "ip": "198.51.100.21", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 100},
        ]
        for name, value in {
            "read_nodes": self.nodes, "load_ui_config": {}, "get_active_slots": [0, 1],
            "get_paused_slots": set(), "get_slot_pin_map": {}, "persisted_slot_node_id": "",
            "reserved_slot_candidate_ids": set(), "main_reserved_candidate_ids": set(),
            "candidate_ip_identities": set(), "per_slot_type": "", "per_slot_country": "", "per_slot_isp": "",
        }.items():
            self.patches.enter_context(mock.patch.object(manager, name, return_value=value))
        self.patches.enter_context(mock.patch.object(manager, "slot_bad_nodes", {}))
        self.patches.enter_context(mock.patch.object(manager, "slot_reconnect_hints", {}))
        self.patches.enter_context(mock.patch.object(manager, "dedicated_standbys", {2: {"node_id": "dc"}}))
        self.patches.enter_context(mock.patch.object(manager, "log_to_json"))

    def test_unconfigured_slots_allow_datacenter_fallback(self):
        self.assertFalse(manager.get_exit_slot_config()["residential_only"])
        with mock.patch.object(manager, "read_nodes", return_value=[self.nodes[0]]):
            self.assertEqual(manager.pick_slot_node(1, set())["id"], "dc")

    def test_explicit_residential_only_remains_strict(self):
        with mock.patch.object(manager, "load_ui_config", return_value={"exit_slot_residential_only": True}), \
             mock.patch.object(manager, "read_nodes", return_value=[self.nodes[0]]):
            self.assertIsNone(manager.pick_slot_node(1, set()))

    def test_default_selection_prefers_residential_even_if_datacenter_is_faster(self):
        selected = manager.select_slot_nodes(set(), 2, "", False)
        self.assertEqual([n["id"] for n in selected], ["home", "dc"])

    def test_unknown_classification_is_not_used_as_datacenter_fallback(self):
        unknown = {**self.nodes[0], "id": "unknown", "ip_type": "unknown", "latency_ms": 1}
        with mock.patch.object(manager, "read_nodes", return_value=[unknown, self.nodes[0]]):
            self.assertEqual([n["id"] for n in manager.select_slot_nodes(set(), 2, "", False)], ["dc"])

    def test_initial_slot_can_promote_its_own_reserved_standby(self):
        with mock.patch.object(manager, "reserved_slot_candidate_ids", return_value={"dc"}), \
             mock.patch.object(manager, "promote_dedicated_standby_to_slot", return_value=True) as promote:
            self.assertTrue(manager.promote_initial_slot_standby(1))
        promote.assert_called_once_with(1)

    def test_initial_standby_promotion_preserves_explicit_constraints(self):
        cases = [
            ("load_ui_config", {"exit_slot_residential_only": True}),
            ("per_slot_country", "US"),
            ("per_slot_type", "residential"),
            ("per_slot_isp", "unmatched-provider"),
            ("get_slot_pin_map", {"1": "pinned-other"}),
        ]
        for name, value in cases:
            with self.subTest(constraint=name), mock.patch.object(manager, name, return_value=value), \
                 mock.patch.object(manager, "promote_dedicated_standby_to_slot") as promote:
                self.assertFalse(manager.promote_initial_slot_standby(1))
                promote.assert_not_called()

    def test_supervisor_checks_own_standby_before_cold_dial(self):
        repair = mock.Mock()
        repair.get.return_value = {}
        with ExitStack() as patches:
            for name in ("kill_unregistered_slot_openvpn_processes", "tear_down_slot", "write_slots_state"):
                patches.enter_context(mock.patch.object(manager, name))
            patches.enter_context(mock.patch.object(manager, "exit_slots_supervise_lock", threading.Lock()))
            patches.enter_context(mock.patch.object(manager, "get_active_slots", return_value=[1]))
            patches.enter_context(mock.patch.object(manager, "slot_operation_locks", {}))
            patches.enter_context(mock.patch.object(manager, "exit_slots", {}))
            patches.enter_context(mock.patch.object(manager, "exit_slot_proxy_stops", {}))
            patches.enter_context(mock.patch.object(manager, "egress_repair_store", repair))
            patches.enter_context(mock.patch.object(manager, "slot_process_alive", return_value=False))
            promote = patches.enter_context(mock.patch.object(manager, "promote_initial_slot_standby", return_value=True, create=True))
            pick = patches.enter_context(mock.patch.object(manager, "pick_slot_node", return_value=None))
            patches.enter_context(mock.patch.object(manager, "mark_slot_pending"))
            manager.supervise_exit_slots_once()
        promote.assert_called_once_with(1)
        pick.assert_not_called()

    def test_partial_pool_scan_retries_promptly_when_an_active_slot_is_missing(self):
        with mock.patch.object(manager, "COLLECTOR_INITIAL_DELAY_SECONDS", 0), \
             mock.patch.object(manager, "maintain_valid_nodes", return_value="Fetched 98 candidates. Stop: batch_budget."), \
             mock.patch.object(manager, "active_openvpn_running", return_value=True), \
             mock.patch.object(manager, "slot_process_alive", side_effect=lambda slot: slot == 0), \
             mock.patch.object(manager, "log_to_json"), \
             mock.patch.object(manager.time, "sleep", side_effect=InterruptedError) as sleep:
            with self.assertRaises(InterruptedError):
                manager.collector_loop()
        sleep.assert_called_once_with(manager.COLLECTOR_FAILURE_BACKOFF_SECONDS)

    def test_steady_state_and_pressure_keep_normal_retry_delay(self):
        with mock.patch.object(manager, "active_openvpn_running", return_value=True), \
             mock.patch.object(manager, "slot_process_alive", return_value=True):
            self.assertEqual(manager.collector_retry_delay("Fetched 98 candidates. Stop: batch_budget."),
                             manager.COLLECTOR_BUSY_RETRY_SECONDS)
        with mock.patch.object(manager, "slot_process_alive", return_value=False):
            for result in ["operation_busy", "Fetched 98 candidates. Stop: resource_pressure."]:
                self.assertEqual(manager.collector_retry_delay(result), manager.COLLECTOR_BUSY_RETRY_SECONDS)


if __name__ == "__main__":
    unittest.main()
