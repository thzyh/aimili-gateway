import unittest
import tempfile
from pathlib import Path
from unittest import mock

import capacity
import vpngate_manager as manager


class CapacityTests(unittest.TestCase):
    def test_small_host_keeps_current_four_slots_but_does_not_offer_unbounded_growth(self):
        facts = capacity.HostFacts(458 * 1024 * 1024, 191 * 1024 * 1024, 1, 0.2)
        limits = capacity.limits_for(facts, 4)
        self.assertEqual(limits.regular_exit_slots_max, 4)
        self.assertEqual(limits.target_valid_nodes_max, 64)
        self.assertEqual(limits.emergency_valid_nodes_max, 150)

    def test_pressure_reduces_future_limit_but_never_below_current_slots(self):
        facts = capacity.HostFacts(458 * 1024 * 1024, 20 * 1024 * 1024, 1, 2.0)
        limits = capacity.limits_for(facts, 4)
        self.assertEqual(limits.regular_exit_slots_max, 4)
        self.assertEqual(limits.target_valid_nodes_max, 16)
        self.assertEqual(limits.emergency_valid_nodes_max, 54)

    def test_effective_limits_recover_after_temporary_pressure(self):
        healthy = capacity.HostFacts(458 * 1024 * 1024, 191 * 1024 * 1024, 1, 0.2)
        pressure = capacity.HostFacts(458 * 1024 * 1024, 20 * 1024 * 1024, 1, 2.0)
        with tempfile.TemporaryDirectory() as folder:
            with mock.patch.object(manager, "CAPACITY_FILE", Path(folder) / "capacity.json"), \
                mock.patch.object(manager, "_active_regular_slot_count", return_value=4), \
                mock.patch.object(manager.capacity, "read_host_facts", side_effect=[healthy, pressure, healthy]), \
                mock.patch.object(manager, "_capacity_settings", {}), \
                mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 64), \
                mock.patch.object(manager, "TARGET_VALID_NODES", 64), \
                mock.patch.object(manager, "MAX_VALID_POOL_SIZE", 150):
                self.assertEqual(manager.refresh_capacity_limits().target_valid_nodes_max, 64)
                self.assertEqual(manager.TARGET_VALID_POOL_SIZE, 64)
                self.assertEqual(manager.refresh_capacity_limits().target_valid_nodes_max, 16)
                self.assertEqual(manager.TARGET_VALID_POOL_SIZE, 16)
                self.assertEqual(manager.refresh_capacity_limits().target_valid_nodes_max, 64)
                self.assertEqual(manager.TARGET_VALID_POOL_SIZE, 64)

    def test_over_limit_request_rejects_before_changing_slots(self):
        facts = capacity.HostFacts(2048 * 1024 * 1024, 1000 * 1024 * 1024, 2, 0.1)
        with mock.patch.object(manager, "refresh_capacity_limits", return_value=capacity.limits_for(facts, 4)), \
            mock.patch.object(manager, "_active_regular_slot_count", return_value=4), \
            mock.patch.object(manager, "set_exit_slot_config") as set_slots:
            result = manager.update_capacity(target=999, regular_slots=5)
        self.assertEqual(result["error_code"], "capacity_limit_exceeded")
        set_slots.assert_not_called()

    def test_expansion_requires_installed_protocol_and_firewall_ports(self):
        facts = capacity.HostFacts(2048 * 1024 * 1024, 1000 * 1024 * 1024, 2, 0.1)
        with mock.patch.object(manager, "refresh_capacity_limits", return_value=capacity.limits_for(facts, 4)), \
            mock.patch.object(manager, "_active_regular_slot_count", return_value=4), \
            mock.patch.object(manager, "_slot_expansion_ready", return_value=False), \
            mock.patch.object(manager, "set_exit_slot_config") as set_slots:
            result = manager.update_capacity(regular_slots=5)
        self.assertEqual(result["error_code"], "capacity_upgrade_required")
        set_slots.assert_not_called()

    def test_capacity_update_persists_targets_and_reports_pending_exit(self):
        facts = capacity.HostFacts(2048 * 1024 * 1024, 1000 * 1024 * 1024, 2, 0.1)
        with tempfile.TemporaryDirectory() as folder:
            capacity_file = Path(folder) / "capacity.json"
            with mock.patch.object(manager, "CAPACITY_FILE", capacity_file), \
                mock.patch.object(manager, "refresh_capacity_limits", return_value=capacity.limits_for(facts, 4)), \
                mock.patch.object(manager, "_active_regular_slot_count", return_value=4), \
                mock.patch.object(manager, "_slot_expansion_ready", return_value=True), \
                mock.patch.object(manager, "set_exit_slot_config", return_value={"count": 5}) as set_slots, \
                mock.patch.object(manager, "capacity_snapshot", return_value={"regularExitSlots": 5, "readyRegularExitSlots": 4}), \
                mock.patch.object(manager, "schedule_valid_pool_replenishment"), \
                mock.patch.object(manager.threading, "Thread"), \
                mock.patch.object(manager, "_capacity_settings", {}), \
                mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 64), \
                mock.patch.object(manager, "TARGET_VALID_NODES", 64), \
                mock.patch.object(manager, "MAX_VALID_POOL_SIZE", 150):
                result = manager.update_capacity(target=80, emergency=160, regular_slots=5)
                self.assertEqual(manager.TARGET_VALID_POOL_SIZE, 80)
            self.assertEqual(result["readyRegularExitSlots"], 4)
            self.assertEqual(set_slots.call_args.kwargs, {"count": 5})
            self.assertEqual(capacity_file.read_text(encoding="utf-8").count('"targetValidNodeCount": 80'), 1)

    def test_capacity_file_failure_restores_previous_slot_count(self):
        facts = capacity.HostFacts(2048 * 1024 * 1024, 1000 * 1024 * 1024, 2, 0.1)
        with mock.patch.object(manager, "refresh_capacity_limits", return_value=capacity.limits_for(facts, 4)), \
            mock.patch.object(manager, "_active_regular_slot_count", return_value=4), \
            mock.patch.object(manager, "_slot_expansion_ready", return_value=True), \
            mock.patch.object(manager, "set_exit_slot_config", side_effect=[{"count": 5}, {"count": 4}]) as set_slots, \
            mock.patch.object(manager, "write_json", side_effect=OSError("disk full")):
            result = manager.update_capacity(regular_slots=5)
        self.assertEqual(result["error_code"], "capacity_storage_failed")
        self.assertEqual([call.kwargs["count"] for call in set_slots.call_args_list], [5, 4])

    def test_large_host_scales_in_steps(self):
        facts = capacity.HostFacts(4096 * 1024 * 1024, 3000 * 1024 * 1024, 4, 0.4)
        limits = capacity.limits_for(facts, 4)
        self.assertEqual(limits.regular_exit_slots_max, 16)
        self.assertEqual(limits.target_valid_nodes_max, 256)
        self.assertEqual(limits.emergency_valid_nodes_max, 512)

    def test_busy_host_stops_new_exits_and_recovers_without_reconfiguration(self):
        total = 4096 * 1024 * 1024
        calm = capacity.limits_for(capacity.HostFacts(total, 3000 * 1024 * 1024, 4, 0.4), 4)
        busy = capacity.limits_for(capacity.HostFacts(total, 3000 * 1024 * 1024, 4, 8.0), 4)
        self.assertEqual(calm.regular_exit_slots_max, 16)
        self.assertEqual(busy.regular_exit_slots_max, 4)
        self.assertLess(busy.target_valid_nodes_max, calm.target_valid_nodes_max)

    def test_process_semaphore_override_restricts_new_exits(self):
        facts = capacity.HostFacts(4096 * 1024 * 1024, 3000 * 1024 * 1024, 4, 0.4)
        limits = capacity.limits_for(facts, 4, process_limit=9)
        self.assertEqual(limits.regular_exit_slots_max, 6)

    def test_clamp_never_allows_emergency_below_target(self):
        facts = capacity.HostFacts(1024 * 1024 * 1024, 500 * 1024 * 1024, 2, 0.1)
        limits = capacity.limits_for(facts, 2)
        self.assertEqual(capacity.clamp_settings(999, 1, limits), (limits.target_valid_nodes_max, limits.target_valid_nodes_max))


if __name__ == "__main__":
    unittest.main()
