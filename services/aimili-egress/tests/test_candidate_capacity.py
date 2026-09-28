import time
import unittest
from contextlib import ExitStack
from unittest import mock

import vpngate_manager as manager


class CandidateCapacityTests(unittest.TestCase):
    def setUp(self):
        self.patches = ExitStack()
        self.addCleanup(self.patches.close)
        self.addCleanup(manager.pool_resume_requested.clear)
        facts = manager.capacity.HostFacts(458 * 1048576, 160 * 1048576, 1, 0.2)
        for name, value in {
            "TARGET_VALID_POOL_SIZE": 200, "MAX_VALID_POOL_SIZE": 200,
            "active_openvpn_node_id": "",
        }.items():
            self.patches.enter_context(mock.patch.object(manager, name, value))
        for name, value in {
            "current_slot_node_ids": [], "reserved_slot_candidate_ids": set(),
            "get_slot_pin_map": {}, "pool_maintenance_should_yield": False,
            "active_openvpn_running": True, "load_blacklist": {},
        }.items():
            self.patches.enter_context(mock.patch.object(manager, name, return_value=value))
        for name in ("refresh_capacity_limits", "ensure_dirs", "set_state", "log_to_json", "store_pool_metadata", "_set_country_refresh"):
            self.patches.enter_context(mock.patch.object(manager, name))
        self.patches.enter_context(mock.patch.object(manager.capacity, "read_host_facts", return_value=facts))
        self.patches.enter_context(mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()))

    def test_bounded_scan_is_serial_and_preserves_over_64_across_rounds(self):
        nodes = [{"id": str(i), "probe_status": "available", "probed_at": time.time()} for i in range(85)]
        candidates = [{"id": str(i)} for i in range(300)]
        batches = []

        def probe(batch):
            batches.append(len(batch))
            return [{**n, "probe_status": "available", "probed_at": time.time()} for n in batch]

        for expected in (117, 149, 181, 200):
            nodes, _, stats = manager.replenish_valid_pool(
                nodes, candidates, {}, probe, bounded=True, fresh_ids={n["id"] for n in nodes})
            self.assertEqual(len(nodes), expected)
            self.assertLessEqual(stats["tested"], 32)
        self.assertEqual(set(batches), {1})
        self.assertEqual(stats["stop_reason"], "target_reached")

    def test_pressure_or_missing_telemetry_starts_no_probe(self):
        for available in (0, 20 * 1048576):
            with mock.patch.object(manager.capacity, "read_host_facts", return_value=manager.capacity.HostFacts(458 * 1048576, available, 1, 0.1)):
                probe = mock.Mock()
                _, _, stats = manager.replenish_valid_pool([], [{"id": "new"}], {}, probe, bounded=True)
            probe.assert_not_called()
            self.assertEqual(stats["stop_reason"], "resource_pressure")

    def test_elapsed_budget_does_not_mark_unchecked_nodes_failed(self):
        probe = mock.Mock()
        with mock.patch.object(manager.time, "monotonic", side_effect=[10, 131]):
            _, blacklist, stats = manager.replenish_valid_pool([], [{"id": "new"}], {}, probe, bounded=True)
        probe.assert_not_called()
        self.assertEqual(blacklist, {})
        self.assertEqual(stats["stop_reason"], "batch_budget")

    def test_full_pool_rotates_stale_checks_and_keeps_updated_timestamps(self):
        stored = [{"id": str(i), "probe_status": "available", "probed_at": 1} for i in range(200)]
        checked = []

        def probe(batch):
            checked.extend(n["id"] for n in batch)
            return [{**n, "probed_at": time.time()} for n in batch]

        def save(path, value):
            if path == manager.NODES_FILE:
                stored[:] = value

        with mock.patch.object(manager, "read_nodes", side_effect=lambda: list(stored)), \
             mock.patch.object(manager, "fetch_candidates", return_value=[]), \
             mock.patch.object(manager, "probe_nodes", side_effect=probe), \
             mock.patch.object(manager, "write_json", side_effect=save), \
             mock.patch.object(manager, "load_pool_metadata", side_effect=manager.default_pool_metadata):
            for _ in range(7):
                manager.maintain_valid_nodes()
                self.assertEqual(len(stored), 200)
        self.assertEqual(len(checked), 200)
        self.assertEqual(len(set(checked)), 200)
        self.assertTrue(all(n["probed_at"] > 1 for n in stored))

    def test_manual_partial_refresh_wakes_sleeping_collector(self):
        manager.pool_resume_requested.set()
        with mock.patch.object(manager, "COLLECTOR_INITIAL_DELAY_SECONDS", 0), \
             mock.patch.object(manager, "maintain_valid_nodes", return_value="Fetched 20 candidates. Stop: target_reached."), \
             mock.patch.object(manager.time, "sleep", side_effect=InterruptedError) as sleep:
            with self.assertRaises(InterruptedError):
                manager.collector_loop()
        sleep.assert_called_once_with(manager.COLLECTOR_BUSY_RETRY_SECONDS)
        self.assertFalse(manager.pool_resume_requested.is_set())

    def test_success_removes_expired_failure_cooldown(self):
        result = {"id": "recovered", "probe_status": "available"}
        _, blacklist, _ = manager.replenish_valid_pool(
            [], [result], {"recovered": {"until": 1}}, lambda _: [result], now=2, bounded=True)
        self.assertNotIn("recovered", blacklist)
