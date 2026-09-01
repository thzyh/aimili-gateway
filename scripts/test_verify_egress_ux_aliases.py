import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent


def load(name):
    path = ROOT / name
    spec = importlib.util.spec_from_file_location(path.stem, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class WindowsBaselineTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.module = load("verify_xui_windows_baseline.py")

    def test_accepts_only_the_three_known_windows_packages_and_tests(self):
        output = """--- FAIL: TestBotContextNamesRealCIJobs (0.01s)
FAIL\tgithub.com/mhsanaei/3x-ui/v3\t1.0s
--- FAIL: TestFileKeySourceRejectsLoosePerms (0.00s)
FAIL\tgithub.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken\t1.0s
--- FAIL: TestUpdateProxyEnvVars (0.00s)
FAIL\tgithub.com/mhsanaei/3x-ui/v3/internal/web/service/panel\t1.0s
FAIL
"""
        self.module.verify(output, 1, "windows")

    def test_rejects_any_new_failure(self):
        output = """--- FAIL: TestAssociationAliasOverridesRawJsonAndClashOnlyForTargetClient (0.00s)
FAIL\tgithub.com/mhsanaei/3x-ui/v3/internal/sub\t1.0s
FAIL
"""
        with self.assertRaisesRegex(ValueError, "unexpected"):
            self.module.verify(output, 1, "windows")

    def test_rejects_known_test_assigned_to_the_wrong_package(self):
        output = """--- FAIL: TestUpdateProxyEnvVars (0.00s)
FAIL\tgithub.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken\t1.0s
FAIL
"""
        with self.assertRaisesRegex(ValueError, "unexpected"):
            self.module.verify(output, 1, "windows")

    def test_rejects_timeout_panic_and_unclassified_failures(self):
        for output, exit_code in (
            ("BOUNDED_COMMAND_TIMEOUT seconds=1\n", 124),
            ("panic: fixture crashed\n", 2),
            ("FAIL\n", 1),
        ):
            with self.subTest(output=output):
                with self.assertRaisesRegex(ValueError, "unexpected"):
                    self.module.verify(output, exit_code, "windows")


class ThreeProcessFixtureTests(unittest.TestCase):
    def test_bounded_runner_preserves_partial_output_and_times_out(self):
        with tempfile.TemporaryDirectory() as root:
            output = Path(root) / "partial.log"
            completed = subprocess.run(
                [sys.executable, str(ROOT / "run_bounded_command.py"), "--timeout-seconds", "1", "--output", str(output), "--", sys.executable, "-c", "import time; print('started', flush=True); time.sleep(30)"],
                capture_output=True,
                text=True,
                timeout=10,
            )
            self.assertEqual(completed.returncode, 124)
            self.assertIn("started", output.read_text(encoding="utf-8"))

    def test_three_process_http_fixture_returns_actual_safe_results(self):
        completed = subprocess.run(
            [sys.executable, str(ROOT / "egress_ux_alias_process_fixture.py")],
            check=True,
            capture_output=True,
            text=True,
            timeout=30,
        )
        result = json.loads(completed.stdout)
        self.assertEqual(result["services"], ["aimili", "xui", "gateway"])
        self.assertEqual([item["result"] for item in result["transactions"]], ["ready", "ready", "rolled_back"])
        self.assertEqual(result["transactions"][0]["result"], "ready")
        self.assertEqual(result["transactions"][1]["alias"], "出口位 1_韩国")
        self.assertEqual(result["transactions"][2]["errorCode"], "candidate_dial_failed")
        self.assertTrue(result["transactions"][2]["candidateExcluded"])
        self.assertEqual(result["transactions"][2]["transaction"], "rolled_back")
        self.assertTrue(result["transactions"][2]["gatewayStatePreserved"])
        self.assertTrue(result["transactions"][2]["aliasesPreserved"])
        lowered = completed.stdout.lower()
        for forbidden in ("subscriptionurl", "authorization", "uuid", "nodeconfig"):
            self.assertNotIn(forbidden, lowered)

    def test_powershell_entry_runs_full_xui_test_and_consumes_fixture_json(self):
        script = (ROOT / "verify-egress-ux-aliases.ps1").read_text(encoding="utf-8")
        self.assertIn("-- go test -p 1 ./... -count=1", script)
        self.assertIn("verify_xui_windows_baseline.py", script)
        self.assertIn("egress_ux_alias_process_fixture.py", script)
        self.assertIn("run_bounded_command.py", script)
        self.assertIn("--timeout-seconds", script)
        self.assertNotIn("Write-Host 'TRANSACTION old-main", script)


if __name__ == "__main__":
    unittest.main()
