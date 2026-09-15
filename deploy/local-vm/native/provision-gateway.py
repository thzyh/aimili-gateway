#!/usr/bin/env python3
import argparse
import http.cookiejar
import json
import os
import pathlib
import ssl
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


PROTOCOL_MODES = (
    "vless_tcp_reality_vision",
    "vless_xhttp_reality",
    "hysteria2_quic_tls",
)


class ProvisionError(RuntimeError):
    pass


def api_error_code(payload):
    if not isinstance(payload, dict):
        return "gateway_request_failed"
    error = payload.get("error")
    if isinstance(error, str) and error:
        return error
    if isinstance(error, dict):
        code = error.get("code")
        if isinstance(code, str) and code:
            return code
    return "gateway_request_failed"


class GatewayClient:
    def __init__(self, base_url, origin, username, password, timeout, ca_certificate):
        parsed = urllib.parse.urlsplit(base_url)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ProvisionError("gateway_endpoint_invalid")
        self.base_url = base_url.rstrip("/")
        self.origin = origin
        self.username = username
        self.password = password
        self.timeout = timeout
        self.csrf_token = ""
        try:
            tls_context = ssl.create_default_context(cafile=ca_certificate)
        except (OSError, ssl.SSLError):
            raise ProvisionError("gateway_ca_invalid") from None
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=tls_context),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def login(self):
        self._send(
            "POST",
            "/api/v1/auth/login",
            {"username": self.username, "password": self.password, "totp": ""},
            origin=True,
        )
        session = self._send("GET", "/api/v1/auth/session")
        self.csrf_token = str(session.get("csrfToken", "")).strip()
        if not self.csrf_token:
            raise ProvisionError("gateway_session_invalid")

    def request(self, method, path, payload=None, idempotency_key=""):
        if not self.csrf_token:
            raise ProvisionError("gateway_session_missing")
        return self._send(method, path, payload, origin=method != "GET", idempotency_key=idempotency_key)

    def _send(self, method, path, payload=None, origin=False, idempotency_key=""):
        body = None
        headers = {"Accept": "application/json", "User-Agent": "aimili-native-provision/1"}
        if payload is not None:
            body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if origin:
            headers["Origin"] = self.origin
        if self.csrf_token and method != "GET":
            headers["X-CSRF-Token"] = self.csrf_token
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        request = urllib.request.Request(self.base_url + path, data=body, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                raw = response.read((1 << 20) + 1)
        except urllib.error.HTTPError as error:
            raw = error.read(16 << 10)
            try:
                payload = json.loads(raw.decode("utf-8"))
                code = api_error_code(payload)
            except (UnicodeDecodeError, json.JSONDecodeError):
                code = "gateway_request_failed"
            raise ProvisionError(code) from None
        except (OSError, urllib.error.URLError):
            raise ProvisionError("gateway_unreachable") from None
        if len(raw) > 1 << 20:
            raise ProvisionError("gateway_response_too_large")
        if not raw:
            return {}
        try:
            result = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            raise ProvisionError("gateway_response_invalid") from None
        if not isinstance(result, (dict, list)):
            raise ProvisionError("gateway_response_invalid")
        return result


def _slot_groups(groups, expected_slots):
    selected = {}
    for group in groups:
        if not isinstance(group, dict) or group.get("fixed") is not True or group.get("status") != "ready":
            continue
        slot_number = group.get("slotNumber")
        if not isinstance(slot_number, int) or slot_number < 1 or slot_number > expected_slots:
            continue
        if group.get("protocolState") != "ready" or not isinstance(group.get("id"), str) or not group["id"].startswith("agw-"):
            continue
        if slot_number in selected:
            raise ProvisionError("gateway_slot_duplicate")
        selected[slot_number] = group
    return selected


def _wait_for_groups(client, expected_slots, deadline, wait):
    while True:
        groups = client.request("GET", "/api/v1/proxy-groups")
        if not isinstance(groups, list):
            raise ProvisionError("gateway_groups_invalid")
        slots = _slot_groups(groups, expected_slots)
        if set(slots) == set(range(1, expected_slots + 1)):
            return groups, slots
        if time.monotonic() >= deadline:
            raise ProvisionError("gateway_slots_not_ready")
        wait(2)


def _ordered_logical_groups(groups, slots):
    mains = [
        group
        for group in groups
        if isinstance(group, dict)
        and group.get("id") == "agw-main"
        and group.get("fixed") is True
        and group.get("status") == "ready"
        and group.get("protocolState") == "ready"
    ]
    if len(mains) != 1:
        raise ProvisionError("gateway_main_not_ready")
    return [mains[0]] + [slots[number] for number in sorted(slots)]


def provision_gateway(client, expected_slots, expected_logical_exits, wait=time.sleep, timeout=300):
    if not isinstance(expected_slots, int) or expected_slots < 1 or expected_slots > 64 or expected_logical_exits != expected_slots + 1:
        raise ProvisionError("gateway_topology_invalid")
    deadline = time.monotonic() + timeout
    client.request("POST", "/api/v1/proxy-groups/agw-main/check", {}, idempotency_key="native-provision-main-v1")
    client.request("POST", "/api/v1/proxy-groups/reconcile", {})
    groups, slots = _wait_for_groups(client, expected_slots, deadline, wait)
    ordered = _ordered_logical_groups(groups, slots)
    if len(ordered) != expected_logical_exits:
        raise ProvisionError("gateway_logical_exit_count_invalid")
    for index, group in enumerate(ordered):
        if group["id"] != "agw-main":
            group_id = urllib.parse.quote(group["id"], safe="")
            checked = client.request(
                "POST",
                f"/api/v1/proxy-groups/{group_id}/check",
                {},
                idempotency_key=f"native-provision-check-{index}-v1",
            )
            if not isinstance(checked, dict) or checked.get("status") != "ready":
                raise ProvisionError("gateway_slot_not_ready")
    groups, slots = _wait_for_groups(client, expected_slots, deadline, wait)
    ordered = _ordered_logical_groups(groups, slots)
    for index, group in enumerate(ordered):
        target = PROTOCOL_MODES[index % len(PROTOCOL_MODES)]
        current = group.get("protocolMode")
        if current == target and group.get("protocolState") == "ready":
            continue
        if current not in PROTOCOL_MODES:
            raise ProvisionError("gateway_protocol_state_invalid")
        group_id = urllib.parse.quote(group["id"], safe="")
        result = client.request(
            "PUT",
            f"/api/v1/proxy-groups/{group_id}/protocol-mode",
            {"protocolMode": target, "expectedProtocolMode": current},
            idempotency_key=f"native-provision-mode-{index}-v1",
        )
        if result.get("protocolMode") != target or result.get("protocolState") != "ready":
            raise ProvisionError("gateway_protocol_switch_failed")
    subscription = client.request("GET", "/api/v1/proxy-groups/subscription")
    inbound_count = subscription.get("inboundCount") if isinstance(subscription, dict) else None
    if inbound_count != expected_logical_exits:
        raise ProvisionError("gateway_subscription_incomplete")
    return {"logicalExits": len(ordered), "exitSlots": len(slots), "subscriptionInbounds": inbound_count}


def _load_json(path, restricted=False):
    target = pathlib.Path(path)
    try:
        info = target.lstat()
    except OSError:
        raise ProvisionError("provision_input_missing") from None
    if target.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_size < 2 or info.st_size > 1 << 20:
        raise ProvisionError("provision_input_invalid")
    if restricted and (info.st_mode & 0o077):
        raise ProvisionError("provision_credentials_permissions_invalid")
    try:
        value = json.loads(target.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        raise ProvisionError("provision_input_invalid") from None
    if not isinstance(value, dict):
        raise ProvisionError("provision_input_invalid")
    return value


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--config", required=True)
    parser.add_argument("--credentials", required=True)
    parser.add_argument("--ca-certificate", default="/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
    parser.add_argument("--timeout", type=int, default=300)
    args = parser.parse_args()
    manifest = _load_json(args.manifest)
    config = _load_json(args.config)
    credentials = _load_json(args.credentials, restricted=True)
    try:
        expected_slots = manifest["expected"]["exitSlots"]
        expected_logical = manifest["expected"]["logicalExits"]
        origin = config["publicOrigin"]
        username = credentials["username"]
        password = credentials["password"]
    except (KeyError, TypeError):
        raise ProvisionError("provision_input_invalid") from None
    if not all(isinstance(value, str) and value.strip() for value in (origin, username, password)) or args.timeout < 30 or args.timeout > 1800:
        raise ProvisionError("provision_input_invalid")
    client = GatewayClient(origin, origin, username, password, args.timeout, args.ca_certificate)
    client.login()
    summary = provision_gateway(client, expected_slots, expected_logical, timeout=args.timeout)
    print(json.dumps({"status": "gateway_provision_ok", **summary}, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except ProvisionError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(5)
