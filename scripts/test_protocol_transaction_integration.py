#!/usr/bin/env python3
"""主连接与协议模式部署安全边界的本地集成测试。"""

from __future__ import annotations

import importlib.util
import json
import os
import pathlib
import sqlite3
import subprocess
import tempfile
import unittest


SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
MODULE_PATH = SCRIPT_DIR / "verify-main-switch-protocol-modes-remote.py"


def load_verifier():
    spec = importlib.util.spec_from_file_location("main_switch_verifier", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("无法加载远程验证模块")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ProtocolDeploymentIntegrationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.verifier = load_verifier()

    def test_resource_gate_requires_memory_and_swap_thresholds(self) -> None:
        accepted = self.verifier.evaluate_resource_gate(
            {"MemAvailable": 160 * 1024, "SwapFree": 512 * 1024}
        )
        self.assertEqual(
            accepted,
            {"memoryMiB": 160, "swapFreeMiB": 512, "status": "pass"},
        )
        with self.assertRaisesRegex(ValueError, "memory_below_threshold"):
            self.verifier.evaluate_resource_gate(
                {"MemAvailable": 160 * 1024 - 1, "SwapFree": 800 * 1024}
            )
        with self.assertRaisesRegex(ValueError, "swap_below_threshold"):
            self.verifier.evaluate_resource_gate(
                {"MemAvailable": 200 * 1024, "SwapFree": 512 * 1024 - 1}
            )

    def test_udp_rules_accept_only_four_exact_public_ports(self) -> None:
        exact = ["8443/udp", "20000/udp", "20001/udp", "20002/udp"]
        self.assertEqual(
            self.verifier.validate_udp_rules(exact),
            {"ports": [8443, 20000, 20001, 20002], "status": "pass"},
        )
        for unsafe in (
            exact + ["443/udp"],
            ["8443/udp", "20000:20002/udp"],
            ["8443/udp", "20000-20002/udp"],
            exact[:-1],
        ):
            with self.subTest(unsafe=unsafe):
                with self.assertRaisesRegex(ValueError, "udp_rules_invalid"):
                    self.verifier.validate_udp_rules(unsafe)

    def test_ufw_parser_collapses_ipv4_ipv6_twins_without_accepting_ranges(self) -> None:
        contents = """
Status: active
8443/udp                  ALLOW       Anywhere
20000/udp                 ALLOW       Anywhere
20001/udp                 ALLOW       Anywhere
20002/udp                 ALLOW       Anywhere
8443/udp (v6)             ALLOW       Anywhere (v6)
20000/udp (v6)            ALLOW       Anywhere (v6)
20001/udp (v6)            ALLOW       Anywhere (v6)
20002/udp (v6)            ALLOW       Anywhere (v6)
"""
        self.assertEqual(
            self.verifier.parse_ufw_udp_rules(contents),
            ["8443/udp", "20000/udp", "20001/udp", "20002/udp"],
        )
        with self.assertRaisesRegex(ValueError, "udp_rules_invalid"):
            self.verifier.parse_ufw_udp_rules("20000:20002/udp ALLOW Anywhere")

    def test_unmanaged_fingerprint_detects_any_non_gateway_change(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            database_path = pathlib.Path(temporary) / "x-ui.db"
            database = sqlite3.connect(database_path)
            database.execute(
                "CREATE TABLE inbounds (id INTEGER PRIMARY KEY, tag TEXT, remark TEXT, "
                "protocol TEXT, port INTEGER, settings TEXT, stream_settings TEXT, sniffing TEXT)"
            )
            database.executemany(
                "INSERT INTO inbounds VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                [
                    (1, "aimili-reality", "Aimili Reality", "vless", 8443, "{}", "{}", "{}"),
                    (2, "agw-slot-1-vless", "Aimili Gateway 1", "vless", 20000, "{}", "{}", "{}"),
                    (90, "personal", "Personal", "trojan", 9443, '{"clients":1}', "{}", "{}"),
                ],
            )
            database.commit()
            database.close()

            before = self.verifier.fingerprint_unmanaged_resources(database_path)
            self.assertEqual(before["count"], 1)
            database = sqlite3.connect(database_path)
            database.execute("UPDATE inbounds SET port=9444 WHERE id=90")
            database.commit()
            database.close()
            after = self.verifier.fingerprint_unmanaged_resources(database_path)
            self.assertNotEqual(before["sha256"], after["sha256"])

    def test_safe_summary_redacts_secret_values_and_urls(self) -> None:
        document = {
            "status": "failed",
            "password": "forbidden-password",
            "Cookie": "forbidden-cookie",
            "uuid": "11111111-1111-1111-1111-111111111111",
            "auth": "forbidden-auth",
            "privateKey": "forbidden-key",
            "subscriptionUrl": "https://example.invalid/private/path?token=forbidden",
            "nested": {"errorCode": "public_validation_failed"},
        }
        encoded = json.dumps(self.verifier.safe_summary(document), sort_keys=True)
        for secret in (
            "forbidden-password",
            "forbidden-cookie",
            "11111111-1111-1111-1111-111111111111",
            "forbidden-auth",
            "forbidden-key",
            "private/path",
        ):
            self.assertNotIn(secret, encoded)
        self.assertIn("public_validation_failed", encoded)
        self.assertIn("[redacted]", encoded)

    def test_hysteria_certificate_gate_checks_expiry_hostname_and_xray_config(self) -> None:
        commands: list[list[str]] = []

        def runner(command, **kwargs):
            commands.append(list(command))
            if command[0].endswith("xray"):
                config_path = pathlib.Path(command[-1])
                if os.name != "nt":
                    self.assertEqual(config_path.stat().st_mode & 0o777, 0o600)
                document = json.loads(config_path.read_text(encoding="utf-8"))
                inbound = document["inbounds"][0]
                self.assertEqual(inbound["protocol"], "hysteria")
                self.assertEqual(inbound["port"], 20002)
                self.assertEqual(
                    inbound["streamSettings"]["tlsSettings"]["serverName"],
                    "proxy.example.test",
                )
            return subprocess.CompletedProcess(command, 0, "", "")

        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            certificate = root / "certificate.pem"
            private_key = root / "private-key.pem"
            xray = root / "xray"
            for path in (certificate, private_key, xray):
                path.write_text("fixture", encoding="utf-8")
            os.chmod(xray, 0o700)
            result = self.verifier.verify_hysteria_certificate(
                certificate,
                private_key,
                "proxy.example.test",
                xray,
                runner=runner,
            )
        self.assertEqual(result, {"status": "pass", "xrayConfig": "valid"})
        self.assertEqual(commands[0][:3], ["openssl", "x509", "-checkend"])
        self.assertIn("-checkhost", commands[1])
        self.assertEqual(commands[2][1:4], ["run", "-test", "-config"])

    def test_runtime_continuity_requires_same_pid_and_unchanged_live_probes(self) -> None:
        before = {
            "egress-a": {"connected": True, "fingerprint": "a"},
            "egress-b": {"connected": True, "fingerprint": "b"},
        }
        self.assertEqual(
            self.verifier.verify_runtime_continuity(123, 123, before, before),
            {"nonTargetProbes": 2, "status": "pass", "xrayPidChanged": False},
        )
        with self.assertRaisesRegex(ValueError, "xray_pid_changed"):
            self.verifier.verify_runtime_continuity(123, 124, before, before)
        disconnected = dict(before)
        disconnected["egress-b"] = {"connected": False, "fingerprint": "b"}
        with self.assertRaisesRegex(ValueError, "non_target_probe_changed"):
            self.verifier.verify_runtime_continuity(123, 123, before, disconnected)

    def test_observation_gate_enforces_memory_swap_xray_and_duration(self) -> None:
        samples = [
            {"elapsed": second, "memoryAvailableMiB": 150, "swapUsedMiB": 120, "xrayRssMiB": 80, "oom": False}
            for second in range(0, 301, 30)
        ]
        self.assertEqual(
            self.verifier.evaluate_observation(samples, baseline_swap_used_mib=110),
            {"durationSeconds": 300, "status": "pass", "xrayRssPeakMiB": 80, "swapDeltaMiB": 10},
        )
        mutations = {
            "duration_below_threshold": samples[:-1],
            "xray_rss_above_threshold": [dict(samples[0], xrayRssMiB=97), *samples[1:]],
            "swap_growth_above_threshold": [*samples[:-1], dict(samples[-1], swapUsedMiB=239)],
            "oom_detected": [dict(samples[0], oom=True), *samples[1:]],
        }
        for error_code, values in mutations.items():
            with self.subTest(error_code=error_code):
                with self.assertRaisesRegex(ValueError, error_code):
                    self.verifier.evaluate_observation(values, baseline_swap_used_mib=110)

        low_memory = [
            {"elapsed": second, "memoryAvailableMiB": 95, "swapUsedMiB": 110, "xrayRssMiB": 80, "oom": False}
            for second in (0, 10, 20, 30)
        ] + samples[2:]
        with self.assertRaisesRegex(ValueError, "memory_below_runtime_threshold"):
            self.verifier.evaluate_observation(low_memory, baseline_swap_used_mib=110)


if __name__ == "__main__":
    unittest.main()
