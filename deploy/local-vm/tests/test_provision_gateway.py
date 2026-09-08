import importlib.util
import pathlib
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "native" / "provision-gateway.py"


def load_module():
    if not SCRIPT.is_file():
        raise AssertionError("native Gateway provisioning implementation is missing")
    spec = importlib.util.spec_from_file_location("provision_gateway", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeGatewayClient:
    def __init__(self, slots):
        self.calls = []
        self.groups = []
        self.slots = slots

    def request(self, method, path, payload=None, idempotency_key=""):
        self.calls.append((method, path, payload, idempotency_key))
        if (method, path) == ("POST", "/api/v1/proxy-groups/reconcile"):
            if not any(group.get("slotNumber", 0) > 0 for group in self.groups):
                self.groups.extend([
                    {
                        "id": f"agw-slot-{slot}",
                        "status": "ready",
                        "egressSource": "slot",
                        "slotNumber": slot + 1,
                        "fixed": True,
                        "protocolMode": "vless_tcp_reality_vision",
                        "protocolState": "ready",
                    }
                    for slot in reversed(range(self.slots))
                ])
            return {"status": "accepted"}
        if (method, path) == ("POST", "/api/v1/proxy-groups/agw-main/check"):
            if not any(group.get("id") == "agw-main" for group in self.groups):
                self.groups.append(
                    {
                        "id": "agw-main",
                        "status": "ready",
                        "egressSource": "main",
                        "slotNumber": 0,
                        "fixed": True,
                        "protocolMode": "vless_tcp_reality_vision",
                        "protocolState": "ready",
                    }
                )
            return {"id": "agw-main", "enabled": True}
        if (method, path) == ("GET", "/api/v1/proxy-groups"):
            return list(self.groups)
        if method == "POST" and path.endswith("/check"):
            group_id = path.removeprefix("/api/v1/proxy-groups/").removesuffix("/check")
            for group in self.groups:
                if group["id"] == group_id:
                    return {"id": group_id, "status": "ready"}
            raise AssertionError(f"unknown group {group_id}")
        if method == "PUT" and path.endswith("/protocol-mode"):
            group_id = path.removeprefix("/api/v1/proxy-groups/").removesuffix("/protocol-mode")
            for group in self.groups:
                if group["id"] == group_id:
                    group["protocolMode"] = payload["protocolMode"]
                    group["protocolState"] = "ready"
                    return {"protocolMode": payload["protocolMode"], "protocolState": "ready"}
            raise AssertionError(f"unknown group {group_id}")
        if (method, path) == ("GET", "/api/v1/proxy-groups/subscription"):
            return {"url": "https://fixture.invalid/sub/redacted", "inboundCount": len(self.groups)}
        raise AssertionError(f"unexpected request {(method, path)}")


class ProvisionGatewayTests(unittest.TestCase):
    def test_extracts_gateway_error_code_from_supported_api_shapes(self):
        module = load_module()
        self.assertEqual("not_ready", module.api_error_code({"error": "not_ready"}))
        self.assertEqual("not_ready", module.api_error_code({"error": {"code": "not_ready"}}))
        self.assertEqual("gateway_request_failed", module.api_error_code({"unexpected": True}))

    def test_provisions_existing_slots_main_and_round_robin_protocols_idempotently(self):
        module = load_module()
        client = FakeGatewayClient(slots=3)

        summary = module.provision_gateway(client, expected_slots=3, expected_logical_exits=4, wait=lambda _: None)

        self.assertEqual(
            {
                "agw-main": "vless_tcp_reality_vision",
                "agw-slot-0": "vless_xhttp_reality",
                "agw-slot-1": "hysteria2_quic_tls",
                "agw-slot-2": "vless_tcp_reality_vision",
            },
            {group["id"]: group["protocolMode"] for group in client.groups},
        )
        self.assertEqual({"logicalExits": 4, "exitSlots": 3, "subscriptionInbounds": 4}, summary)
        paths = [call[1] for call in client.calls]
        self.assertLess(paths.index("/api/v1/proxy-groups/agw-main/check"), paths.index("/api/v1/proxy-groups/reconcile"))
        for slot in range(3):
            check_path = f"/api/v1/proxy-groups/agw-slot-{slot}/check"
            mode_path = f"/api/v1/proxy-groups/agw-slot-{slot}/protocol-mode"
            self.assertIn(check_path, paths)
            if mode_path in paths:
                self.assertLess(paths.index(check_path), paths.index(mode_path))
        self.assertEqual("/api/v1/proxy-groups/subscription", paths[-1])

        protocol_calls_before = sum(1 for call in client.calls if call[0] == "PUT")
        module.provision_gateway(client, expected_slots=3, expected_logical_exits=4, wait=lambda _: None)
        protocol_calls_after = sum(1 for call in client.calls if call[0] == "PUT")
        self.assertEqual(protocol_calls_before, protocol_calls_after)

    def test_protocol_distribution_scales_with_manifest_slot_count(self):
        module = load_module()
        client = FakeGatewayClient(slots=5)

        module.provision_gateway(client, expected_slots=5, expected_logical_exits=6, wait=lambda _: None)

        ordered = sorted(client.groups, key=lambda group: group["slotNumber"])
        self.assertEqual(
            [
                "vless_tcp_reality_vision",
                "vless_xhttp_reality",
                "hysteria2_quic_tls",
                "vless_tcp_reality_vision",
                "vless_xhttp_reality",
                "hysteria2_quic_tls",
            ],
            [group["protocolMode"] for group in ordered],
        )


if __name__ == "__main__":
    unittest.main()
