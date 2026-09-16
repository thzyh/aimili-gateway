import subprocess
import unittest
from unittest import mock

import vpngate_manager as manager


class _ListeningSocket:
    def settimeout(self, _timeout):
        pass

    def connect(self, _address):
        pass

    def close(self):
        pass


class ProxyHealthTests(unittest.TestCase):
    def test_slot_health_keeps_tunnel_when_ip_providers_fail_but_https_works(self):
        def run_curl(command, **_kwargs):
            if "generate_204" in " ".join(command):
                return subprocess.CompletedProcess(command, 0, stdout="204", stderr="")
            return subprocess.CompletedProcess(command, 28, stdout="", stderr="timeout")

        with mock.patch.object(manager.subprocess, "run", side_effect=run_curl):
            ok, exit_ip = manager.check_slot_egress(17928)

        self.assertTrue(ok)
        self.assertEqual(exit_ip, "")

    def test_main_health_uses_cached_ip_when_only_ip_providers_are_unavailable(self):
        def run_curl(command, **_kwargs):
            if "generate_204" in " ".join(command):
                return subprocess.CompletedProcess(command, 0, stdout="204", stderr="")
            return subprocess.CompletedProcess(command, 28, stdout="", stderr="timeout")

        with (
            mock.patch.object(manager.sys, "platform", "win32"),
            mock.patch.object(manager.socket, "socket", return_value=_ListeningSocket()),
            mock.patch.object(manager.subprocess, "run", side_effect=run_curl),
            mock.patch.object(manager, "ensure_policy_routing", return_value=True),
            mock.patch.object(manager, "get_state", return_value={"proxy_ip": "198.51.100.9", "proxy_latency_ms": 250}),
            mock.patch.object(manager, "LOCAL_PROXY_REQUIRED_URL", ""),
        ):
            result = manager.check_proxy_health()

        self.assertTrue(result["ok"])
        self.assertTrue(result["probe_degraded"])
        self.assertEqual(result["ip"], "198.51.100.9")

    def test_required_client_probe_failure_rejects_otherwise_live_egress(self):
        required_url = "https://www.google.com/generate_204"

        def run_curl(command, **_kwargs):
            if required_url in command:
                return subprocess.CompletedProcess(command, 35, stdout="000", stderr="reset")
            return subprocess.CompletedProcess(
                command,
                0,
                stdout="198.51.100.10\n0.125 200\n",
                stderr="",
            )

        with (
            mock.patch.object(manager.sys, "platform", "win32"),
            mock.patch.object(manager.socket, "socket", return_value=_ListeningSocket()),
            mock.patch.object(manager.subprocess, "run", side_effect=run_curl),
            mock.patch.object(
                manager,
                "LOCAL_PROXY_REQUIRED_URL",
                required_url,
                create=True,
            ),
        ):
            result = manager.check_proxy_health()

        self.assertFalse(result["ok"])
        self.assertEqual(result["error_code"], "client_probe_unreachable")
        self.assertIn("ERR_CLIENT_PROBE_UNREACHABLE", result["error"])
        self.assertNotIn(required_url, result["error"])

    def test_required_client_probe_success_preserves_verified_egress(self):
        required_url = "https://www.google.com/generate_204"

        def run_curl(command, **_kwargs):
            if required_url in command:
                return subprocess.CompletedProcess(command, 0, stdout="204", stderr="")
            return subprocess.CompletedProcess(
                command,
                0,
                stdout="198.51.100.10\n0.125 200\n",
                stderr="",
            )

        with (
            mock.patch.object(manager.sys, "platform", "win32"),
            mock.patch.object(manager.socket, "socket", return_value=_ListeningSocket()),
            mock.patch.object(manager.subprocess, "run", side_effect=run_curl),
            mock.patch.object(manager, "LOCAL_PROXY_REQUIRED_URL", required_url),
        ):
            result = manager.check_proxy_health()

        self.assertTrue(result["ok"])
        self.assertEqual(result["ip"], "198.51.100.10")
        self.assertEqual(result["latency_ms"], 125)


if __name__ == "__main__":
    unittest.main()
