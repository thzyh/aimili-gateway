import importlib.util
import inspect
import json
import pathlib
import subprocess
import unittest
import urllib.parse
from unittest import mock


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
    def _verify_public_probe(self, curl_results, mode="vless_tcp_reality_vision"):
        attempts = []

        def run_curl(*args, **kwargs):
            attempts.append((args, kwargs))
            return curl_results[len(attempts) - 1]

        process = FakeProcess([0])
        public_uri = (
            "vless://client@example.test:20000?type=tcp&security=reality&"
            "flow=xtls-rprx-vision&fp=chrome&sni=front.example&pbk=public&sid=abcd"
        )
        if mode == "hysteria2_quic_tls":
            public_uri = (
                "hysteria2://opaque@example.test:20001/?"
                "sni=front.example&insecure=0#slot-two"
            )
        payload = {
            "exitIp": "203.0.113.1",
            "protocolMode": mode,
            "publicUri": public_uri,
            "socks5hUri": "socks5h://127.0.0.1:1080",
        }
        with (
            mock.patch.object(MODULE, "tcp_reachable", return_value=True),
            mock.patch.object(MODULE, "free_port", return_value=10808),
            mock.patch.object(MODULE.subprocess, "run", side_effect=run_curl),
            mock.patch.object(MODULE.subprocess, "Popen", return_value=process),
            mock.patch.object(MODULE.socket, "create_connection", return_value=mock.MagicMock()),
        ):
            result = MODULE.verify_group(payload, "xray.exe", probe_public_socks=False)
        return result, attempts

    def test_public_probe_retries_one_transient_transport_failure(self):
        result, attempts = self._verify_public_probe([
            subprocess.CompletedProcess([], 28, "", "timeout"),
            subprocess.CompletedProcess([], 0, "203.0.113.1", ""),
        ])

        self.assertTrue(result["external_public_protocol"])
        self.assertEqual(0, result["public_curl_exit"])
        self.assertEqual(2, len(attempts))

    def test_hysteria2_public_probe_retries_one_transient_transport_failure(self):
        result, attempts = self._verify_public_probe(
            [
                subprocess.CompletedProcess([], 35, "", "tls failure"),
                subprocess.CompletedProcess([], 0, "203.0.113.1", ""),
            ],
            mode="hysteria2_quic_tls",
        )

        self.assertTrue(result["external_public_protocol"])
        self.assertEqual(0, result["public_curl_exit"])
        self.assertEqual(2, len(attempts))

    def test_public_probe_does_not_retry_a_wrong_exit_ip(self):
        result, attempts = self._verify_public_probe([
            subprocess.CompletedProcess([], 0, "203.0.113.99", ""),
            subprocess.CompletedProcess([], 0, "203.0.113.1", ""),
        ])

        self.assertFalse(result["external_public_protocol"])
        self.assertEqual(1, len(attempts))

    def test_public_probe_does_not_retry_an_unlisted_curl_failure(self):
        result, attempts = self._verify_public_probe([
            subprocess.CompletedProcess([], 7, "", "connection failed"),
            subprocess.CompletedProcess([], 0, "203.0.113.1", ""),
        ])

        self.assertFalse(result["external_public_protocol"])
        self.assertEqual(1, len(attempts))

    def test_remote_switch_uses_gateway_cas_and_has_no_unconditional_rollback(self):
        self.assertIn("'expectedProtocolMode':expected_old", MODULE.REMOTE_HELPER)
        self.assertNotIn("{'protocolMode':switch['oldMode']}", MODULE.REMOTE_HELPER)

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
            public_protocol=True,
        ))

    def test_unrestricted_policy_requires_public_socks(self):
        self.assertFalse(MODULE.group_passes(
            source_restriction_enabled=False,
            public_socks=False,
            public_socks_tcp=True,
            authorized_socks=True,
            public_protocol=True,
        ))

    def test_require_ready_materials_accepts_multiple_groups(self):
        materials = MODULE.require_ready_materials([
            {"exitIp": "203.0.113.1", "protocolMode": "vless_tcp_reality_vision", "publicUri": "vless://first", "socks5hUri": "socks5h://first"},
            {"exitIp": "203.0.113.2", "protocolMode": "hysteria2_quic_tls", "publicUri": "hysteria2://second", "socks5hUri": "socks5h://second"},
        ])

        self.assertEqual(2, len(materials))

    def test_require_ready_materials_rejects_empty_list(self):
        with self.assertRaisesRegex(RuntimeError, "no ready groups"):
            MODULE.require_ready_materials([])

    def test_require_ready_materials_rejects_protocol_uri_mismatch(self):
        with self.assertRaisesRegex(RuntimeError, "protocol URI mismatch"):
            MODULE.require_ready_materials([
                {"exitIp": "203.0.113.1", "protocolMode": "hysteria2_quic_tls", "publicUri": "vless://wrong", "socks5hUri": "socks5h://first"},
            ])

    def test_build_public_client_config_uses_tcp_vision(self):
        uri = (
            "vless://client@example.test:20000?type=tcp&security=reality&"
            "flow=xtls-rprx-vision&fp=chrome&sni=front.example&pbk=public&sid=abcd"
        )

        document = MODULE.build_public_client_config(
            uri, "vless_tcp_reality_vision", 10808
        )

        outbound = document["outbounds"][0]
        self.assertEqual("vless", outbound["protocol"])
        self.assertEqual("tcp", outbound["streamSettings"]["network"])
        self.assertEqual(
            "xtls-rprx-vision",
            outbound["settings"]["vnext"][0]["users"][0]["flow"],
        )

    def test_build_public_client_config_uses_xhttp_without_vision(self):
        uri = (
            "vless://client@example.test:20000?type=xhttp&security=reality&"
            "fp=chrome&sni=front.example&pbk=public&sid=abcd&path=%2Fopaque"
        )

        document = MODULE.build_public_client_config(
            uri, "vless_xhttp_reality", 10808
        )

        outbound = document["outbounds"][0]
        self.assertEqual("xhttp", outbound["streamSettings"]["network"])
        self.assertEqual(
            {"path": "/opaque", "mode": "auto"},
            outbound["streamSettings"]["xhttpSettings"],
        )
        self.assertEqual(
            "", outbound["settings"]["vnext"][0]["users"][0]["flow"]
        )

    def test_build_public_client_config_uses_hysteria2_auth_and_tls(self):
        uri = "hysteria2://opaque-auth@example.test:20001/?sni=front.example"

        document = MODULE.build_public_client_config(
            uri, "hysteria2_quic_tls", 10808
        )

        outbound = document["outbounds"][0]
        self.assertEqual("hysteria", outbound["protocol"])
        self.assertEqual(
            {"version": 2, "address": "example.test", "port": 20001},
            outbound["settings"],
        )
        self.assertEqual(
            {"version": 2, "auth": "opaque-auth"},
            outbound["streamSettings"]["hysteriaSettings"],
        )
        self.assertEqual(
            "front.example",
            outbound["streamSettings"]["tlsSettings"]["serverName"],
        )

    def test_decode_subscription_accepts_vless_and_hysteria2(self):
        raw = (
            "vless://client@example.test:20000?type=xhttp\n"
            "hysteria2://opaque@example.test:20001/?sni=front.example\n"
        ).encode()

        entries = MODULE.decode_subscription(raw)

        self.assertEqual(["vless", "hysteria2"], [urllib.parse.urlsplit(item).scheme for item in entries])

    def test_switch_arguments_are_a_closed_set(self):
        self.assertEqual((0, "vless_xhttp_reality"), MODULE.validate_switch_arguments(0, "vless_xhttp_reality"))
        self.assertEqual((1, "vless_xhttp_reality"), MODULE.validate_switch_arguments(1, "vless_xhttp_reality"))
        with self.assertRaisesRegex(ValueError, "invalid slot"):
            MODULE.validate_switch_arguments(4, "vless_xhttp_reality")
        with self.assertRaisesRegex(ValueError, "invalid protocol mode"):
            MODULE.validate_switch_arguments(1, "vless-over-websocket")

    def test_remote_switch_contract_uses_slot_zero_for_main_and_minus_one_for_no_switch(self):
        self.assertIn("str(-1 if slot is None else slot)", inspect.getsource(MODULE.run_remote))
        self.assertIn("if slot >= 0:", MODULE.REMOTE_HELPER)
        self.assertIn("if slot == 0:", MODULE.REMOTE_HELPER)
        self.assertIn("g.get('egressSource')=='main'", MODULE.REMOTE_HELPER)
        self.assertIn("'subscriptionAlias'", MODULE.REMOTE_HELPER)

    def test_remote_helper_source_is_ascii_for_ssh_stdin_compatibility(self):
        self.assertTrue(MODULE.REMOTE_HELPER.isascii())
        self.assertIn(r"'\u4e3b\u8fde\u63a5_'", MODULE.REMOTE_HELPER)

    def test_subscription_coverage_matches_ports_and_protocols(self):
        materials = [
            {"exitIp": "203.0.113.1", "protocolMode": "vless_xhttp_reality", "publicUri": "vless://client@example.test:20000?type=xhttp", "socks5hUri": "socks5h://first"},
            {"exitIp": "203.0.113.2", "protocolMode": "hysteria2_quic_tls", "publicUri": "hysteria2://opaque@example.test:20001/?sni=front.example", "socks5hUri": "socks5h://second"},
        ]
        entries = [
            "vless://client@example.test:20000?type=xhttp",
            "hysteria2://opaque@example.test:20001/?sni=front.example",
        ]

        result = MODULE.validate_subscription_coverage(materials, entries)

        self.assertEqual({"entryCount": 2, "hysteria2": 1, "vless": 1}, result)

    def test_subscription_coverage_rejects_wrong_protocol_on_stable_port(self):
        materials = [
            {"exitIp": "203.0.113.2", "protocolMode": "hysteria2_quic_tls", "publicUri": "hysteria2://opaque@example.test:20001/?sni=front.example", "socks5hUri": "socks5h://second"},
        ]
        with self.assertRaisesRegex(RuntimeError, "subscription coverage mismatch"):
            MODULE.validate_subscription_coverage(
                materials, ["vless://client@example.test:20001?type=tcp"]
            )

    def test_subscription_coverage_accepts_secure_hysteria2_defaults(self):
        materials = [{
            "exitIp": "203.0.113.2",
            "protocolMode": "hysteria2_quic_tls",
            "publicUri": "hysteria2://opaque@example.test:20001/?sni=front.example&insecure=0#slot-two",
            "socks5hUri": "socks5h://second",
        }]

        result = MODULE.validate_subscription_coverage(materials, [
            "hysteria2://opaque@example.test:20001?sni=front.example&alpn=h3&security=tls#slot-two"
        ])

        self.assertEqual({"entryCount": 1, "hysteria2": 1, "vless": 0}, result)

    def test_subscription_coverage_rejects_insecure_or_nondefault_hysteria2(self):
        materials = [{
            "exitIp": "203.0.113.2",
            "protocolMode": "hysteria2_quic_tls",
            "publicUri": "hysteria2://opaque@example.test:20001/?sni=front.example&insecure=0#slot-two",
            "socks5hUri": "socks5h://second",
        }]
        for path, suffix in (
            ("/", "insecure=1"),
            ("/", "alpn=h2"),
            ("/", "security=none"),
            ("/not-root", "insecure=0"),
        ):
            with self.subTest(path=path, suffix=suffix), self.assertRaisesRegex(
                RuntimeError, "subscription coverage mismatch"
            ):
                MODULE.validate_subscription_coverage(materials, [
                    "hysteria2://opaque@example.test:20001"
                    + path
                    + "?sni=front.example&"
                    + suffix
                    + "#slot-two"
                ])

    def test_subscription_coverage_rejects_duplicate_query_parameters(self):
        cases = (
            (
                [{
                    "exitIp": "203.0.113.2",
                    "protocolMode": "hysteria2_quic_tls",
                    "publicUri": "hysteria2://opaque@example.test:20001/?sni=front.example&insecure=0#slot-two",
                    "socks5hUri": "socks5h://second",
                }],
                "hysteria2://opaque@example.test:20001/?sni=front.example&insecure=1&insecure=0#slot-two",
            ),
            (
                [{
                    "exitIp": "203.0.113.1",
                    "protocolMode": "vless_xhttp_reality",
                    "publicUri": "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#slot-one",
                    "socks5hUri": "socks5h://first",
                }],
                "vless://client@example.test:20000?type=xhttp&path=%2Fopaque&host=override.example&host=#slot-one",
            ),
        )

        for materials, entry in cases:
            with self.subTest(entry=entry), self.assertRaisesRegex(
                RuntimeError, "subscription coverage mismatch"
            ):
                MODULE.validate_subscription_coverage(materials, [entry])

    def test_subscription_coverage_rejects_stale_tcp_parameters_for_xhttp(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": (
                "vless://client@example.test:20000?type=xhttp&security=reality&"
                "fp=chrome&sni=front.example&pbk=public&sid=abcd&path=%2Fopaque#slot-one"
            ),
            "socks5hUri": "socks5h://first",
        }]

        with self.assertRaisesRegex(RuntimeError, "subscription coverage mismatch"):
            MODULE.validate_subscription_coverage(materials, [
                "vless://client@example.test:20000?type=tcp&security=reality&"
                "flow=xtls-rprx-vision&fp=chrome&sni=front.example&pbk=public&sid=abcd#slot-one"
            ])

    def test_subscription_coverage_accepts_3xui_default_xhttp_parameters(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#slot-one",
            "socks5hUri": "socks5h://first",
        }]

        try:
            result = MODULE.validate_subscription_coverage(materials, [
                "vless://client@example.test:20000?type=xhttp&path=%2Fopaque&host=&"
                "extra=%7B%22mode%22%3A%22auto%22%7D#slot-one"
            ])
        except RuntimeError as error:
            self.fail(f"3x-ui default XHTTP parameters were rejected: {error}")

        self.assertEqual({"entryCount": 1, "hysteria2": 0, "vless": 1}, result)

    def test_subscription_coverage_rejects_nondefault_xhttp_parameters(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#slot-one",
            "socks5hUri": "socks5h://first",
        }]

        for query in (
            "host=override.example",
            "extra=%7B%22mode%22%3A%22packet-up%22%7D",
        ):
            with self.subTest(query=query), self.assertRaisesRegex(
                RuntimeError, "subscription coverage mismatch"
            ):
                MODULE.validate_subscription_coverage(materials, [
                    "vless://client@example.test:20000?type=xhttp&path=%2Fopaque&"
                    + query
                    + "#slot-one"
                ])

    def test_subscription_coverage_rejects_wrong_logical_name(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#slot-one",
            "socks5hUri": "socks5h://first",
        }]

        with self.assertRaisesRegex(RuntimeError, "subscription coverage mismatch"):
            MODULE.validate_subscription_coverage(materials, [
                "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#wrong-name"
            ])

    def test_subscription_coverage_accepts_exact_dynamic_alias_but_rejects_drift(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#agw-slot-one",
            "socks5hUri": "socks5h://first",
            "subscriptionAlias": "出口位 1_日本",
        }]
        exact = "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#%E5%87%BA%E5%8F%A3%E4%BD%8D%201_%E6%97%A5%E6%9C%AC"
        self.assertEqual(
            {"entryCount": 1, "hysteria2": 0, "vless": 1},
            MODULE.validate_subscription_coverage(materials, [exact]),
        )
        with self.assertRaisesRegex(RuntimeError, "subscription coverage mismatch"):
            MODULE.validate_subscription_coverage(materials, [
                "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#%E5%87%BA%E5%8F%A3%E4%BD%8D%201_%E9%9F%A9%E5%9B%BD"
            ])

    def test_subscription_coverage_accepts_3xui_stable_remarks_and_default_parameters(self):
        materials = [
            {
                "exitIp": "203.0.113.1",
                "protocolMode": "vless_tcp_reality_vision",
                "publicUri": "vless://client@example.test:8443?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&type=tcp#agw-main",
                "socks5hUri": "socks5h://first",
            },
            {
                "exitIp": "203.0.113.2",
                "protocolMode": "vless_tcp_reality_vision",
                "publicUri": "vless://client@example.test:20000?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&type=tcp#agw-slot-one",
                "socks5hUri": "socks5h://second",
            },
        ]
        entries = [
            "vless://client@example.test:8443?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&spx=%2Fstable-spider&type=tcp#Aimili%20Reality-aimili-gateway-subscription",
            "vless://client@example.test:20000?flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&spx=%2Fstable-spider&type=tcp#Aimili%20Gateway%20agw-slot-one%20VLESS",
        ]

        result = MODULE.validate_subscription_coverage(materials, entries)

        self.assertEqual({"entryCount": 2, "hysteria2": 0, "vless": 2}, result)

    def test_subscription_coverage_rejects_a_similar_main_remark_prefix(self):
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_tcp_reality_vision",
            "publicUri": "vless://client@example.test:8443?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&type=tcp#agw-main",
            "socks5hUri": "socks5h://first",
        }]

        with self.assertRaisesRegex(RuntimeError, "subscription coverage mismatch"):
            MODULE.validate_subscription_coverage(materials, [
                "vless://client@example.test:8443?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=public&security=reality&sid=short&sni=example.test&type=tcp#Aimili%20Reality-impostor"
            ])

    def test_bound_materials_use_subscription_entries_for_public_validation(self):
        api_uri = "vless://client@example.test:20000?type=xhttp&path=%2Fopaque#slot-one"
        subscription_uri = "vless://client@example.test:20000?path=%2Fopaque&type=xhttp#slot-one"
        materials = [{
            "exitIp": "203.0.113.1",
            "protocolMode": "vless_xhttp_reality",
            "publicUri": api_uri,
            "socks5hUri": "socks5h://first",
        }]

        bound = MODULE.bind_subscription_entries(materials, [subscription_uri])

        self.assertEqual(subscription_uri, bound[0]["publicUri"])
        self.assertEqual(api_uri, materials[0]["publicUri"])

    def test_failed_switch_verification_rolls_back_and_verifies_old_mode(self):
        calls = []

        inspected = iter(["vless_tcp_reality_vision", "vless_xhttp_reality"])

        def inspect_mode(slot):
            calls.append(("inspect", slot))
            return next(inspected)

        def collect(slot, mode, expected_old):
            calls.append(("collect", slot, mode, expected_old))
            if mode == "vless_xhttp_reality":
                return {"switch": {"slot": 1, "oldMode": "vless_tcp_reality_vision", "newMode": "vless_xhttp_reality"}}
            return {"switch": {"slot": 1, "oldMode": "vless_xhttp_reality", "newMode": "vless_tcp_reality_vision"}}

        def evaluate(remote):
            calls.append(("evaluate", remote["switch"]["newMode"]))
            return ({"status": "failed"}, False) if remote["switch"]["newMode"] == "vless_xhttp_reality" else ({"status": "pass"}, True)

        result, passed = MODULE.run_with_switch_rollback(
            1, "vless_xhttp_reality", inspect_mode, collect, evaluate
        )

        self.assertFalse(passed)
        self.assertEqual("pass", result["rollback"]["status"])
        self.assertEqual([
            ("inspect", 1),
            ("collect", 1, "vless_xhttp_reality", "vless_tcp_reality_vision"),
            ("evaluate", "vless_xhttp_reality"),
            ("inspect", 1),
            ("collect", 1, "vless_tcp_reality_vision", "vless_xhttp_reality"),
            ("evaluate", "vless_tcp_reality_vision"),
        ], calls)

    def test_switch_verification_exception_still_rolls_back_before_reraising(self):
        calls = []

        inspected = iter(["vless_tcp_reality_vision", "hysteria2_quic_tls"])

        def inspect_mode(slot):
            calls.append(("inspect", slot))
            return next(inspected)

        def collect(slot, mode, expected_old):
            calls.append(("collect", slot, mode, expected_old))
            old_mode = "vless_tcp_reality_vision" if mode == "hysteria2_quic_tls" else "hysteria2_quic_tls"
            return {"switch": {"slot": 2, "oldMode": old_mode, "newMode": mode}}

        def evaluate(remote):
            calls.append(("evaluate", remote["switch"]["newMode"]))
            if remote["switch"]["newMode"] == "hysteria2_quic_tls":
                raise RuntimeError("external verification failed")
            return {"status": "pass"}, True

        with self.assertRaisesRegex(RuntimeError, "external verification failed"):
            MODULE.run_with_switch_rollback(
                2, "hysteria2_quic_tls", inspect_mode, collect, evaluate
            )

        self.assertEqual([
            ("inspect", 2),
            ("collect", 2, "hysteria2_quic_tls", "vless_tcp_reality_vision"),
            ("evaluate", "hysteria2_quic_tls"),
            ("inspect", 2),
            ("collect", 2, "vless_tcp_reality_vision", "hysteria2_quic_tls"),
            ("evaluate", "vless_tcp_reality_vision"),
        ], calls)

    def test_switch_response_timeout_uses_preflight_mode_for_rollback(self):
        calls = []

        inspected = iter(["vless_tcp_reality_vision", "vless_tcp_reality_vision"])

        def inspect_mode(slot):
            calls.append(("inspect", slot))
            return next(inspected)

        def collect(slot, mode, expected_old):
            calls.append(("collect", slot, mode, expected_old))
            if mode == "vless_xhttp_reality":
                raise subprocess.TimeoutExpired("ssh", 240)
            return {"switch": None}

        def evaluate(remote):
            calls.append(("evaluate", remote.get("switch")))
            return {"status": "pass"}, True

        with self.assertRaises(subprocess.TimeoutExpired):
            MODULE.run_with_switch_rollback(
                1, "vless_xhttp_reality", inspect_mode, collect, evaluate
            )

        self.assertEqual([
            ("inspect", 1),
            ("collect", 1, "vless_xhttp_reality", "vless_tcp_reality_vision"),
            ("inspect", 1),
            ("collect", 1, "vless_tcp_reality_vision", "vless_tcp_reality_vision"),
            ("evaluate", None),
        ], calls)

    def test_invalid_switch_response_rolls_back_using_preflight_mode(self):
        calls = []

        inspected = iter(["vless_tcp_reality_vision", "vless_xhttp_reality"])

        def inspect_mode(slot):
            calls.append(("inspect", slot))
            return next(inspected)

        def collect(slot, mode, expected_old):
            calls.append(("collect", slot, mode, expected_old))
            return {"switch": None}

        def evaluate(remote):
            calls.append(("evaluate", remote.get("switch")))
            return {"status": "pass"}, True

        with self.assertRaisesRegex(RuntimeError, "invalid switch result"):
            MODULE.run_with_switch_rollback(
                1, "vless_xhttp_reality", inspect_mode, collect, evaluate
            )

        self.assertEqual([
            ("inspect", 1),
            ("collect", 1, "vless_xhttp_reality", "vless_tcp_reality_vision"),
            ("inspect", 1),
            ("collect", 1, "vless_tcp_reality_vision", "vless_xhttp_reality"),
            ("evaluate", None),
        ], calls)

    def test_concurrent_third_mode_is_not_overwritten_by_rollback(self):
        calls = []
        inspected = iter(["vless_tcp_reality_vision", "hysteria2_quic_tls"])

        def inspect_mode(slot):
            calls.append(("inspect", slot))
            return next(inspected)

        def collect(slot, mode, expected_old):
            calls.append(("collect", slot, mode, expected_old))
            raise RuntimeError("expected mode mismatch")

        def evaluate(remote):
            calls.append(("evaluate", remote))
            return {"status": "pass"}, True

        with self.assertRaisesRegex(RuntimeError, "rollback conflict"):
            MODULE.run_with_switch_rollback(
                1, "vless_xhttp_reality", inspect_mode, collect, evaluate
            )

        self.assertEqual([
            ("inspect", 1),
            ("collect", 1, "vless_xhttp_reality", "vless_tcp_reality_vision"),
            ("inspect", 1),
        ], calls)

    def test_stop_process_kills_after_graceful_wait_timeout(self):
        process = FakeProcess([
            subprocess.TimeoutExpired("xray", 5),
            1,
        ])

        MODULE.stop_process(process)

        self.assertEqual(1, process.terminate_calls)
        self.assertEqual(1, process.kill_calls)

    def test_safe_error_category_exposes_only_closed_diagnostic_codes(self):
        self.assertEqual(
            "subscription_coverage_mismatch",
            MODULE.safe_error_category(RuntimeError("subscription coverage mismatch")),
        )
        self.assertEqual(
            "runtime_error",
            MODULE.safe_error_category(RuntimeError("credential-shaped-secret-must-not-escape")),
        )
        remote = subprocess.CalledProcessError(
            1,
            ["ssh", "ny"],
            stderr="RuntimeError: remote_connections_http_500 secret-url",
        )
        self.assertEqual("remote_connections_http_500", MODULE.safe_error_category(remote))
        remote.stderr = "RuntimeError: remote_connections_slot_1_http_409 secret-url"
        self.assertEqual("remote_connections_slot_1_http_409", MODULE.safe_error_category(remote))
        remote.stderr = "RuntimeError: remote_connections_slot_0_http_409_egress_unavailable secret-url"
        self.assertEqual(
            "remote_connections_slot_0_http_409_egress_unavailable",
            MODULE.safe_error_category(remote),
        )
        remote.stderr = "RuntimeError: remote_protocol_switch_http_400_invalid_request secret-url"
        self.assertEqual(
            "remote_protocol_switch_http_400_invalid_request",
            MODULE.safe_error_category(remote),
        )


if __name__ == "__main__":
    unittest.main()
