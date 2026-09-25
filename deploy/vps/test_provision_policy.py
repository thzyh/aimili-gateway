import unittest

import provision


class FakeClient:
    def __init__(self, policy):
        self.policy = policy
        self.writes = []

    def request(self, method, path, body=None):
        self.assert_path(path)
        if method == "PUT":
            self.writes.append(body)
            self.policy = {**body, "applyStatus": "applied"}
        return self.policy

    def assert_path(self, path):
        if path != "/api/v1/settings/mixed-source-policy":
            raise AssertionError(path)


class SourcePolicyTests(unittest.TestCase):
    def test_blank_source_disables_existing_restriction(self):
        client = FakeClient({"enabled": True, "cidrs": ["198.51.100.23/32"], "applyStatus": "applied"})
        provision.ensure_source_policy(client, "")
        self.assertEqual(client.writes, [{"enabled": False, "cidrs": []}])

    def test_changed_source_is_applied_even_if_old_policy_says_applied(self):
        client = FakeClient({"enabled": True, "cidrs": ["198.51.100.23/32"], "applyStatus": "applied"})
        provision.ensure_source_policy(client, "203.0.113.17")
        self.assertEqual(client.writes, [{"enabled": True, "cidrs": ["203.0.113.17/32"]}])

    def test_matching_policy_does_not_reapply(self):
        client = FakeClient({"enabled": False, "cidrs": [], "applyStatus": "applied"})
        provision.ensure_source_policy(client, "")
        self.assertEqual(client.writes, [])


if __name__ == "__main__":
    unittest.main()
