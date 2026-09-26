import tempfile
import unittest
from pathlib import Path
from unittest import mock

import recovery_policy
import capacity
import vpngate_manager as m
from egress_repair import RepairStore


class RecoveryRuntimeTests(unittest.TestCase):
    def test_resource_pressure_defers_without_dialing_or_stopping_active(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(m, 'egress_repair_store', RepairStore(Path(root) / 'repair.json')), mock.patch.object(
            m, '_standby_enabled', return_value=True
        ), mock.patch.object(m, '_standby_target_profile', return_value={'country': 'JP'}), mock.patch.object(
            m.capacity, 'read_host_facts', return_value=capacity.HostFacts(512*1048576, 40*1048576, 1, 0)
        ), mock.patch.object(m, 'tear_down_dedicated_standby'), mock.patch.object(m, 'write_dedicated_standby_state'), mock.patch.object(
            m, 'provision_dedicated_standby'
        ) as dial, mock.patch.object(m, 'stop_active_openvpn') as stop:
            m._maintain_standby({'index': 0, 'target': 'main', 'countries': []})
            dial.assert_not_called()
            stop.assert_not_called()
            self.assertEqual(m.egress_repair_store.get('standby:0')['error_code'], 'resource_pressure')

    def test_capacity_reserves_one_probe_beside_paired_connections(self):
        facts = capacity.HostFacts(4*1024*1048576, 3*1024*1048576, 8, 0)
        self.assertEqual(capacity.limits_for(facts, 3, process_limit=9).regular_exit_slots_max, 3)

    def test_settings_atomic_readback_and_invalid_write_preserves_values(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(m, 'RECOVERY_SETTINGS_FILE', Path(root) / 'settings.json'), mock.patch.object(m, 'get_active_slots', return_value=[0, 1, 2]):
            values = {**recovery_policy.DEFAULTS, 'retryInitialSeconds': 20}
            result = m.update_recovery_settings(values)
            self.assertTrue(result['ok'])
            self.assertEqual(result['standbyTargetCount'], 4)
            self.assertFalse(m.update_recovery_settings({**values, 'maxConcurrentDials': True})['ok'])
            self.assertEqual(m._recovery_settings(), values)

    def test_mapping_ignores_legacy_pair_and_handles_sparse_slots(self):
        with mock.patch.object(m, 'get_active_slots', return_value=[0, 2]), mock.patch.object(
            m, 'load_ui_config', return_value={'dedicated_standbys': [{'index': 0, 'target': 'slot:2'}]}
        ):
            self.assertEqual(m.dedicated_standby_config_snapshot(), recovery_policy.targets([0, 2]))

    def test_paused_slots_do_not_advertise_live_standby_targets(self):
        with mock.patch.object(m, 'get_active_slots', return_value=[0, 1, 2]), mock.patch.object(
            m, 'get_paused_slots', return_value={1}
        ):
            snapshot = m.recovery_settings_snapshot()
            self.assertEqual(snapshot['activeTargets'], recovery_policy.targets([0, 2]))
            self.assertEqual(snapshot['activeTargetCount'], 3)
            self.assertEqual(snapshot['standbyTargetCount'], 3)
            self.assertEqual(m.dedicated_standby_config_snapshot(), recovery_policy.targets([0, 2]))

    def test_retry_state_survives_restart_and_backoff_has_budget(self):
        with tempfile.TemporaryDirectory() as root:
            clock = [1000.0]
            path = Path(root) / 'repairs.json'
            store = RepairStore(path, now=lambda: clock[0])
            self.assertTrue(store.begin_round('standby:2', 'JP', recovery_policy.DEFAULTS))
            store.finish_round('standby:2', recovery_policy.DEFAULTS, 'no_candidate')
            self.assertEqual(store.get('standby:2')['next_attempt_at'], 1010)
            store = RepairStore(path, now=lambda: clock[0])
            self.assertFalse(store.begin_round('standby:2', 'JP', recovery_policy.DEFAULTS))
            clock[0] = 1010
            self.assertTrue(store.begin_round('standby:2', 'JP', recovery_policy.DEFAULTS))
            store.recover_interrupted()
            self.assertEqual(store.get('standby:2')['status'], 'retry_wait')
            clock[0] = 3000
            self.assertFalse(store.begin_round('standby:2', 'JP', recovery_policy.DEFAULTS))
            self.assertEqual(store.get('standby:2')['status'], 'manual_required')

    def test_failure_without_ready_standby_queues_without_cold_replacement(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(m, 'egress_repair_store', RepairStore(Path(root) / 'repair.json')), mock.patch.object(
            m, 'promote_dedicated_standby_to_main', return_value=False
        ), mock.patch.object(m, 'mark_main_bad_node'), mock.patch.object(m, 'connect_node') as connect, mock.patch.object(m, 'set_state'), mock.patch.object(m, 'replenish_repair_country'), mock.patch.object(m, 'validated_repair_candidate', return_value=None), mock.patch.object(m, 'stop_active_openvpn'), mock.patch.object(m, 'automatic_main_candidates', return_value=[]):
            result = m.repair_main_once({'candidate_id': 'failed', 'country': 'JP'})
            self.assertEqual(result['error_code'], 'recovery_pending')
            connect.assert_not_called()
            self.assertEqual(m.egress_repair_store.get('main')['status'], 'waiting_standby')

    def test_sparse_standby_candidates_keep_residential_priority(self):
        nodes = [dict(id='dc', country_short='JP', ip_type='hosting', probe_status='available', config_text='client'),
                 dict(id='home', country_short='JP', ip_type='residential', probe_status='available', config_text='client')]
        with mock.patch.object(m, 'dedicated_standby_config_snapshot', return_value=recovery_policy.targets([2])), mock.patch.object(
            m, '_standby_target_profile', return_value={'country': 'JP'}
        ), mock.patch.object(m, 'reserved_slot_candidate_ids', return_value=set()), mock.patch.object(
            m, 'main_reserved_candidate_ids', return_value=set()
        ), mock.patch.object(m, 'read_nodes', return_value=nodes), mock.patch.object(m, 'slot_bad_nodes', {}):
            self.assertEqual([n['id'] for n in m.select_dedicated_standby_candidates(3)], ['home', 'dc'])


if __name__ == '__main__':
    unittest.main()
