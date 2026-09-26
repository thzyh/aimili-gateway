import unittest

import recovery_policy as policy


def node(identity, country='JP', kind='residential', checked=990):
    return dict(id=identity, country_short=country, ip_type=kind,
                config_text='client', probe_status='available', exit_ip=identity,
                probed_at=checked, latency_ms=20)


class RecoveryPolicyTests(unittest.TestCase):
    def test_stable_one_to_one_targets_include_main(self):
        self.assertEqual(policy.targets([0, 1, 2]), [
            dict(index=0, target='main', countries=[]),
            dict(index=1, target='slot:0', countries=[]),
            dict(index=2, target='slot:1', countries=[]),
            dict(index=3, target='slot:2', countries=[]),
        ])
        self.assertEqual(policy.targets([0, 2])[-1]['index'], 3)

    def test_current_country_then_best_country_and_residential_first(self):
        nodes = [node('jp-dc', kind='hosting'), node('kr-1', 'KR'),
                 node('jp-home', checked=1), node('us-1', 'US'), node('kr-2', 'KR')]
        selected = policy.rank_candidates(nodes, 'JP', policy.DEFAULTS, now=1000)
        self.assertEqual([n['id'] for n in selected], ['jp-home', 'jp-dc', 'kr-1', 'kr-2', 'us-1'])

    def test_statistics_do_not_invent_quality_or_freshness(self):
        stats = policy.country_statistics([node('a'), node('b', checked=1)], 1000, 120)
        self.assertEqual(stats[0]['dialableCount'], 2)
        self.assertEqual(stats[0]['freshEgressCount'], 1)
        self.assertIsNone(stats[0]['ipQualityPassCount'])
        self.assertEqual(stats[0]['ipQualityStatus'], 'not_implemented')

    def test_bad_duplicate_does_not_hide_healthy_and_bad_numbers_are_safe(self):
        good = node('good')
        bad = {**good, 'probe_status': 'unavailable', 'probed_at': 'corrupt'}
        result = policy.country_statistics([bad, good], 1000, 120)
        self.assertEqual(result[0]['dialableCount'], 1)
        self.assertEqual(result[0]['freshEgressCount'], 1)
        selected = policy.rank_candidates([{**good, 'latency_ms': 'unknown'}], 'JP', policy.DEFAULTS, 1000)
        self.assertEqual(len(selected), 1)

    def test_disabled_fallbacks_exclude_other_countries_and_datacenters(self):
        options = {**policy.DEFAULTS, 'allowCrossCountry': False, 'allowDatacenter': False}
        selected = policy.rank_candidates([node('a'), node('b', 'KR'), node('c', kind='hosting')], 'JP', options, 1000)
        self.assertEqual([n['id'] for n in selected], ['a'])

    def test_settings_reject_bool_as_number_unknown_fields_and_invalid_order(self):
        for change in [{'dialTimeoutSeconds': True}, {'secret': 'x'},
                       {'retryInitialSeconds': 100, 'retryMaxSeconds': 10}, {'maxConcurrentDials': 100}]:
            with self.subTest(change=change), self.assertRaises(ValueError):
                policy.validate({**policy.DEFAULTS, **change})

    def test_retry_is_bounded_and_budget_eventually_requires_manual(self):
        state = policy.failed_round({}, 1000, policy.DEFAULTS, 'no_candidate')
        self.assertEqual(state['nextAttemptAt'], 1010)
        state = policy.failed_round(state, 1010, policy.DEFAULTS, 'no_candidate')
        self.assertEqual(state['nextAttemptAt'], 1030)
        state = policy.failed_round(state, 1000 + policy.DEFAULTS['recoveryBudgetSeconds'], policy.DEFAULTS, 'no_candidate')
        self.assertEqual(state['status'], 'manual_required')


if __name__ == '__main__':
    unittest.main()
