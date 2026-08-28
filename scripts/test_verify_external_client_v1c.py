import importlib.util
import pathlib
import subprocess
import unittest


SCRIPT = pathlib.Path(__file__).with_name("verify-external-client-v1c.py")
SPEC = importlib.util.spec_from_file_location("verify_external_client_v1c", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class FakeProcess:
    def __init__(self, wait_results):
        self.wait_results = list(wait_results)
        self.terminate_calls = 0
        self.kill_calls = 0

    def poll(self):
        return None

    def terminate(self):
        self.terminate_calls += 1

    def kill(self):
        self.kill_calls += 1

    def wait(self, timeout):
        result = self.wait_results.pop(0)
        if isinstance(result, BaseException):
            raise result
        return result


class VerificationHelperTests(unittest.TestCase):
    def test_select_materials_can_limit_verification_to_one_index(self):
        materials = [{"name": "first"}, {"name": "second"}, {"name": "third"}]
        self.assertEqual([{"name": "second"}], MODULE.select_materials(materials, 1))
        with self.assertRaisesRegex(ValueError, "out of range"):
            MODULE.select_materials(materials, 3)

    def test_public_socks_probe_is_skipped_only_when_source_restriction_is_enabled(self):
        self.assertFalse(MODULE.should_probe_public_socks(True))
        self.assertTrue(MODULE.should_probe_public_socks(False))

    def test_restricted_policy_accepts_authorized_socks_and_public_rejection(self):
        self.assertTrue(MODULE.group_passes(
            source_restriction_enabled=True,
            public_socks=False,
            public_socks_tcp=True,
            authorized_socks=True,
            vless=True,
        ))

    def test_unrestricted_policy_requires_public_socks(self):
        self.assertFalse(MODULE.group_passes(
            source_restriction_enabled=False,
            public_socks=False,
            public_socks_tcp=True,
            authorized_socks=True,
            vless=True,
        ))

    def test_require_ready_materials_accepts_multiple_groups(self):
        materials = MODULE.require_ready_materials([
            {"exitIp": "203.0.113.1", "vlessUri": "vless://first", "socks5hUri": "socks5h://first"},
            {"exitIp": "203.0.113.2", "vlessUri": "vless://second", "socks5hUri": "socks5h://second"},
        ])

        self.assertEqual(2, len(materials))

    def test_require_ready_materials_rejects_empty_list(self):
        with self.assertRaisesRegex(RuntimeError, "no ready groups"):
            MODULE.require_ready_materials([])

    def test_stop_process_kills_after_graceful_wait_timeout(self):
        process = FakeProcess([
            subprocess.TimeoutExpired("xray", 5),
            1,
        ])

        MODULE.stop_process(process)

        self.assertEqual(1, process.terminate_calls)
        self.assertEqual(1, process.kill_calls)


if __name__ == "__main__":
    unittest.main()
