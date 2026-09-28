import tempfile
import threading
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as m
from egress_repair import RepairStore


class ScanRecoveryCoordinationTests(unittest.TestCase):
    def test_pool_scan_does_not_suspend_main_health_checks(self):
        with mock.patch.object(m, 'is_connecting', True), mock.patch.object(
            m, 'main_mutation_allowed', return_value=True
        ):
            m.maintenance_scan_active.set()
            try:
                self.assertFalse(m.main_health_check_deferred())
                m.main_connection_in_progress.set()
                self.assertTrue(m.main_health_check_deferred())
            finally:
                m.maintenance_scan_active.clear()
                m.main_connection_in_progress.clear()

    def test_confirmed_failure_survives_busy_scan_and_requests_yield(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(
            m, 'egress_repair_store', RepairStore(Path(root) / 'repair.json')
        ), mock.patch.object(m, 'active_openvpn_node_id', 'broken'), mock.patch.object(
            m, 'main_mutation_allowed', return_value=True
        ), mock.patch.object(m, '_acquire_runtime_mutation', return_value=False):
            result = m.repair_main_once({'candidate_id': 'broken', 'country': 'JP'})
            self.assertEqual(result['error_code'], 'recovery_pending')
            self.assertEqual(m.egress_repair_store.get('main')['status'], 'waiting_standby')

    def test_last_probe_batch_cannot_commit_stale_pool_after_recovery_request(self):
        stop = threading.Event()
        def probe(batch):
            stop.set()
            return [dict(n, probe_status='available') for n in batch]
        with mock.patch.object(m, 'TARGET_VALID_POOL_SIZE', 1), mock.patch.object(
            m, 'current_slot_node_ids', return_value=[]
        ), mock.patch.object(m, 'get_slot_pin_map', return_value={}):
            _, _, stats = m.replenish_valid_pool([], [{'id': 'one'}], {}, probe,
                                                stop_requested=stop.is_set)
        self.assertEqual(stats['stop_reason'], 'assignment_priority')

    def test_duplicate_country_request_joins_existing_job(self):
        with mock.patch.object(m, 'country_refresh_state', {
            'state': 'running', 'country': 'JP', 'phase': 'probing', 'testedCount': 12
        }), mock.patch.object(m.threading, 'Thread') as worker:
            result = m.start_country_refresh('JP')
        self.assertEqual(result['state'], 'running')
        self.assertEqual(result['testedCount'], 12)
        worker.assert_not_called()

    def test_expected_route_nopull_push_options_are_not_errors(self):
        line = "Options error: option 'redirect-gateway' cannot be used in this context ([PUSH-OPTIONS])"
        self.assertEqual(m._openvpn_log_level(line, route_nopull=True), 'INFO')
        self.assertEqual(m._openvpn_log_level(line, route_nopull=False), 'ERROR')


if __name__ == '__main__':
    unittest.main()
