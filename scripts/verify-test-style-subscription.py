#!/usr/bin/env python3
"""在 VPS 内执行脱敏的 Test 风格 VLESS 订阅验收。"""

from __future__ import annotations

import argparse
import base64
import contextlib
import hashlib
import http.cookiejar
import ipaddress
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class GatewayError(RuntimeError):
    def __init__(self, method: str, path: str, code: str) -> None:
        self.code = code
        super().__init__(f"Gateway API {method} {path} failed: {code}")


class GatewayClient:
    def __init__(self, origin: str, username: str, password: str) -> None:
        self.origin = origin.rstrip("/")
        self.username = username
        self.password = password
        self.csrf = ""
        self.subscription_metadata: dict[str, object] = {}
        jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(jar)
        )

    def request(
        self,
        method: str,
        path: str,
        payload: object | None = None,
        *,
        idempotent: bool = False,
        expected_error: str = "",
    ) -> object | None:
        data = None if payload is None else json.dumps(payload).encode()
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if method not in {"GET", "HEAD", "OPTIONS"}:
            headers["Origin"] = self.origin
            if self.csrf:
                headers["X-CSRF-Token"] = self.csrf
        if idempotent:
            headers["Idempotency-Key"] = str(uuid.uuid4())
        request = urllib.request.Request(
            self.origin + path, data=data, headers=headers, method=method
        )
        try:
            with self.opener.open(request, timeout=180) as response:
                raw = response.read(1 << 20)
                if expected_error:
                    raise RuntimeError("request unexpectedly succeeded")
                if response.status == 204:
                    return None
                return json.loads(raw)
        except urllib.error.HTTPError as error:
            raw = error.read(1 << 16)
            try:
                code = str(json.loads(raw).get("error", "request_failed"))
            except Exception:
                code = "invalid_error_response"
            if expected_error and code == expected_error:
                return {"error": code}
            raise GatewayError(method, path, code) from None

    def authenticate(self) -> None:
        options = self.request("GET", "/api/v1/auth/options")
        if options and options.get("totpRequired"):
            raise RuntimeError("Gateway TOTP must be disabled for VPS verification")
        self.request(
            "POST",
            "/api/v1/auth/login",
            {"username": self.username, "password": self.password},
        )
        session = self.request("GET", "/api/v1/auth/session")
        self.csrf = str(session["csrfToken"])

    def fetch_subscription(self, url: str) -> bytes:
        parsed = urllib.parse.urlsplit(url)
        origin = urllib.parse.urlsplit(self.origin)
        if (
            parsed.scheme != "https"
            or parsed.hostname != origin.hostname
            or parsed.query
            or parsed.fragment
        ):
            raise RuntimeError("subscription URL crossed the configured origin")
        request = urllib.request.Request(
            url,
            headers={"Accept": "text/plain", "User-Agent": "v2rayN/7"},
        )
        with self.opener.open(request, timeout=30) as response:
            raw = response.read(1 << 20)
            stripped = raw.lstrip()
            compact = b"".join(raw.split())
            self.subscription_metadata = {
                "status": response.status,
                "contentType": response.headers.get_content_type(),
                "contentEncoding": response.headers.get("Content-Encoding", ""),
                "length": len(raw),
                "lineCount": len(raw.splitlines()),
                "startsVless": stripped.startswith(b"vless://"),
                "looksJSON": stripped.startswith((b"{", b"[")),
                "looksHTML": stripped.startswith((b"<", b"<!")),
                "looksBase64": bool(compact)
                and all(
                    byte
                    in b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_+/="
                    for byte in compact
                ),
            }
            return raw


def decode_subscription(raw: bytes) -> list[str]:
    text = raw.decode("utf-8").strip()
    if not text:
        raise RuntimeError("empty subscription")
    if not text.startswith("vless://"):
        compact = "".join(text.split())
        padded = compact + "=" * (-len(compact) % 4)
        try:
            text = base64.urlsafe_b64decode(padded).decode("utf-8")
        except (ValueError, UnicodeDecodeError) as error:
            raise RuntimeError("subscription is not a v2rayN document") from error
    entries = [line.strip() for line in text.splitlines() if line.strip()]
    if not entries or any(not line.startswith("vless://") for line in entries):
        raise RuntimeError("subscription contains a non-VLESS entry")
    return entries


def fingerprint(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()[:12]


def reserve_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_port(port: int, process: subprocess.Popen[bytes]) -> None:
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("validation Xray exited before listening")
        with socket.socket() as connection:
            connection.settimeout(0.2)
            if connection.connect_ex(("127.0.0.1", port)) == 0:
                return
        time.sleep(0.1)
    raise RuntimeError("validation Xray did not listen in time")


def wait_port_closed(port: int) -> None:
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        with socket.socket() as connection:
            connection.settimeout(0.2)
            if connection.connect_ex(("127.0.0.1", port)) != 0:
                return
        time.sleep(0.2)
    raise RuntimeError("legacy aggregate port remains open")


def validate_vless(uri: str, expected_exit: str, xray_path: str) -> int:
    parsed = urllib.parse.urlsplit(uri)
    query = urllib.parse.parse_qs(parsed.query)
    required = {
        key: values[0] for key, values in query.items() if values and values[0]
    }
    required_keys = {"flow", "fp", "sni", "pbk", "sid"}
    if (
        parsed.scheme != "vless"
        or not parsed.username
        or not parsed.hostname
        or not parsed.port
        or not required_keys.issubset(required)
    ):
        raise RuntimeError("invalid VLESS subscription entry")
    local_port = reserve_port()
    reality = {
        "fingerprint": required["fp"],
        "serverName": required["sni"],
        "password": required["pbk"],
        "shortId": required["sid"],
        "spiderX": "/",
    }
    document = {
        "log": {"loglevel": "none"},
        "inbounds": [
            {
                "listen": "127.0.0.1",
                "port": local_port,
                "protocol": "socks",
                "settings": {"udp": False},
            }
        ],
        "outbounds": [
            {
                "protocol": "vless",
                "settings": {
                    "vnext": [
                        {
                            "address": parsed.hostname,
                            "port": parsed.port,
                            "users": [
                                {
                                    "id": parsed.username,
                                    "encryption": "none",
                                    "flow": required["flow"],
                                }
                            ],
                        }
                    ]
                },
                "streamSettings": {
                    "network": "tcp",
                    "security": "reality",
                    "realitySettings": reality,
                },
            }
        ],
    }
    descriptor, config_path = tempfile.mkstemp(
        prefix="aimili-subscription-", suffix=".json"
    )
    os.fchmod(descriptor, 0o600)
    process = None
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as config_file:
            json.dump(document, config_file, separators=(",", ":"))
        started = time.monotonic()
        process = subprocess.Popen(
            [xray_path, "run", "-config", config_path],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        wait_port(local_port, process)
        completed = subprocess.run(
            [
                "curl",
                "-4",
                "--silent",
                "--show-error",
                "--fail",
                "--max-time",
                "30",
                "--socks5-hostname",
                f"127.0.0.1:{local_port}",
                "https://api.ipify.org",
            ],
            capture_output=True,
            text=True,
            timeout=40,
        )
        if completed.returncode != 0:
            raise RuntimeError("VLESS public traffic failed")
        actual = str(ipaddress.ip_address(completed.stdout.strip()))
        if actual != str(ipaddress.ip_address(expected_exit)):
            raise RuntimeError(
                "VLESS public traffic used an unexpected exit: "
                f"port={parsed.port} expected={fingerprint(expected_exit)} "
                f"actual={fingerprint(actual)}"
            )
        return max(1, round((time.monotonic() - started) * 1000))
    finally:
        if process is not None:
            process.terminate()
            with contextlib.suppress(subprocess.TimeoutExpired):
                process.wait(timeout=3)
            if process.poll() is None:
                process.kill()
                process.wait(timeout=3)
        pathlib.Path(config_path).unlink(missing_ok=True)


def ready_by_port(groups: list[dict[str, object]]) -> dict[int, dict[str, object]]:
    result = {}
    for group in groups:
        is_main = group.get("egressSource") == "main"
        if group.get("status") != "ready" and not is_main:
            continue
        port = int(group.get("vlessPort", 0))
        if port > 0 and group.get("exitIp"):
            result[port] = group
    return result


def current_slot_candidate(slot_number: int) -> str:
    token = pathlib.Path("/etc/aimilivpn/control.token").read_text(
        encoding="utf-8"
    ).strip()
    if not token or slot_number < 1:
        raise RuntimeError("invalid AimiliVPN control state")
    request = urllib.request.Request(
        f"http://127.0.0.1:8790/control/v1/slots/{slot_number - 1}",
        headers={"Authorization": f"Bearer {token}", "Accept": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        document = json.load(response)
    slot = document.get("data", document)
    candidate = str(slot.get("node_id", "")).strip()
    if not candidate:
        raise RuntimeError("target slot has no active candidate")
    return candidate


def exercise_replace(
    client: GatewayClient, groups: list[dict[str, object]]
) -> dict[str, bool]:
    target = next(
        group
        for group in groups
        if group.get("status") == "ready"
        and group.get("egressSource") != "main"
        and group.get("fixed")
    )
    standbys = sorted(
        (group for group in groups if group.get("status") == "standby"),
        key=lambda group: int(group.get("candidateLatencyMs", 0)) or 1_000_000,
    )
    target_id = str(target["id"])
    original_port = int(target["vlessPort"])
    original_slot = int(target["slotNumber"])
    original_candidate = current_slot_candidate(original_slot)

    client.request(
        "POST",
        "/api/v1/proxy-groups/nonexistent-candidate/replace",
        {"targetGroupId": target_id},
        idempotent=True,
        expected_error="not_found",
    )
    after_failure = client.request("GET", "/api/v1/proxy-groups")
    unchanged = next(group for group in after_failure if group.get("id") == target_id)
    if (
        int(unchanged.get("vlessPort", 0)) != original_port
        or int(unchanged.get("slotNumber", 0)) != original_slot
    ):
        raise RuntimeError("failed replacement changed the target slot")

    replaced = None
    recovered_failures = 0
    for standby in standbys[:3]:
        try:
            replaced = client.request(
                "POST",
                f"/api/v1/proxy-groups/{urllib.parse.quote(str(standby['id']), safe='')}/replace",
                {"targetGroupId": target_id},
                idempotent=True,
            )
            break
        except GatewayError as error:
            if error.code not in {
                "egress_unavailable",
                "assign_failed",
                "protocol_failed",
                "connection_failed",
                "timeout",
            }:
                raise
            current = client.request("GET", "/api/v1/proxy-groups")
            recovered = next(group for group in current if group.get("id") == target_id)
            if (
                recovered.get("status") != "ready"
                or int(recovered.get("vlessPort", 0)) != original_port
                or int(recovered.get("slotNumber", 0)) != original_slot
                or current_slot_candidate(original_slot) != original_candidate
            ):
                raise RuntimeError("failed candidate did not restore the original slot")
            recovered_failures += 1
    if replaced is None:
        raise RuntimeError(
            f"no replacement candidate passed after {recovered_failures} recovered failures"
        )
    if (
        replaced.get("status") != "ready"
        or int(replaced.get("vlessPort", 0)) != original_port
        or int(replaced.get("slotNumber", 0)) != original_slot
    ):
        raise RuntimeError("successful replacement changed the stable entry")

    restored_group = client.request(
        "POST",
        f"/api/v1/proxy-groups/{urllib.parse.quote(original_candidate, safe='')}/replace",
        {"targetGroupId": target_id},
        idempotent=True,
    )
    restored = (
        restored_group.get("status") == "ready"
        and int(restored_group.get("vlessPort", 0)) == original_port
        and int(restored_group.get("slotNumber", 0)) == original_slot
        and current_slot_candidate(original_slot) == original_candidate
    )
    if not restored:
        raise RuntimeError("replacement succeeded but the original node was not restored")
    return {
        "failureNoOp": True,
        "recoveredCandidateFailures": recovered_failures,
        "success": True,
        "restored": True,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--origin", required=True)
    parser.add_argument("--credentials", required=True)
    parser.add_argument("--xray", required=True)
    parser.add_argument("--exercise-replace", action="store_true")
    parser.add_argument("--cleanup-legacy", action="store_true")
    arguments = parser.parse_args()

    account = json.loads(
        pathlib.Path(arguments.credentials).read_text(encoding="utf-8")
    )
    client = GatewayClient(
        arguments.origin, account["username"], account["password"]
    )
    client.authenticate()
    groups = client.request("GET", "/api/v1/proxy-groups")
    for group in groups:
        if group.get("status") == "ready" and group.get("egressSource") != "main":
            client.request(
                "POST",
                "/api/v1/proxy-groups/"
                + urllib.parse.quote(str(group["id"]), safe="")
                + "/check",
                {},
            )
    groups = client.request("GET", "/api/v1/proxy-groups")
    expected = ready_by_port(groups)
    subscription = client.request("GET", "/api/v1/proxy-groups/subscription")
    raw_subscription = client.fetch_subscription(subscription["url"])
    try:
        entries = decode_subscription(raw_subscription)
    except RuntimeError:
        print(
            json.dumps(
                {"subscriptionResponse": client.subscription_metadata},
                sort_keys=True,
            )
        )
        raise
    parsed_entries = [urllib.parse.urlsplit(entry) for entry in entries]
    ports = sorted(int(entry.port or 0) for entry in parsed_entries)
    expected_ports = sorted(expected)
    if ports != expected_ports or int(subscription["inboundCount"]) != len(entries):
        print(
            json.dumps(
                {
                    "coverage": {
                        "expectedPorts": expected_ports,
                        "subscriptionPorts": ports,
                        "reportedInboundCount": int(subscription["inboundCount"]),
                        "groupStates": [
                            {
                                "port": int(group.get("vlessPort", 0)),
                                "status": str(group.get("status", "")),
                                "error": str(group.get("lastErrorCode", "")),
                            }
                            for group in groups
                            if int(group.get("vlessPort", 0)) > 0
                        ],
                    }
                },
                sort_keys=True,
            )
        )
        raise RuntimeError("subscription coverage does not match ready VLESS entries")

    latencies = {}
    for uri, parsed in zip(entries, parsed_entries):
        port = int(parsed.port or 0)
        latencies[str(port)] = validate_vless(
            uri, str(expected[port]["exitIp"]), arguments.xray
        )

    checked = client.request(
        "POST",
        "/api/v1/proxy-groups/agw-main/check",
        {},
        idempotent=True,
    )
    if int(checked.get("vlessLatencyMs", 0)) < 1 or int(
        checked.get("socksLatencyMs", 0)
    ) < 1:
        raise RuntimeError("main dual-protocol latency was not measured")

    replacement = {"failureNoOp": False, "success": False, "restored": False}
    if arguments.exercise_replace:
        replacement = exercise_replace(client, groups)

    cleanup = {"requested": False, "removed": False, "portClosed": False}
    post_cleanup_latencies: dict[str, int] = {}
    if arguments.cleanup_legacy:
        cleanup_result = client.request(
            "POST",
            "/api/v1/proxy-groups/legacy-aggregate/cleanup",
            {},
            idempotent=True,
        )
        client.request(
            "GET",
            "/api/v1/proxy-groups/aggregate/connections",
            expected_error="legacy_removed",
        )
        wait_port_closed(21000)
        post_subscription = client.request(
            "GET", "/api/v1/proxy-groups/subscription"
        )
        post_entries = decode_subscription(
            client.fetch_subscription(str(post_subscription["url"]))
        )
        post_parsed = [urllib.parse.urlsplit(entry) for entry in post_entries]
        post_ports = sorted(int(entry.port or 0) for entry in post_parsed)
        if post_ports != expected_ports or int(
            post_subscription["inboundCount"]
        ) != len(post_entries):
            raise RuntimeError("subscription coverage changed after legacy cleanup")
        for uri, parsed in zip(post_entries, post_parsed):
            port = int(parsed.port or 0)
            post_cleanup_latencies[str(port)] = validate_vless(
                uri, str(expected[port]["exitIp"]), arguments.xray
            )
        post_main = client.request(
            "POST",
            "/api/v1/proxy-groups/agw-main/check",
            {},
            idempotent=True,
        )
        if int(post_main.get("vlessLatencyMs", 0)) < 1 or int(
            post_main.get("socksLatencyMs", 0)
        ) < 1:
            raise RuntimeError("main validation failed after legacy cleanup")
        cleanup = {
            "requested": True,
            "removed": bool(cleanup_result.get("removed")),
            "portClosed": True,
            "subscriptionEntries": len(post_entries),
            "subscriptionPorts": post_ports,
            "vlessLatencyMs": post_cleanup_latencies,
            "mainVlessLatencyMs": int(post_main["vlessLatencyMs"]),
            "mainSocksLatencyMs": int(post_main["socksLatencyMs"]),
        }

    print(
        json.dumps(
            {
                "authenticated": True,
                "subscriptionEntries": len(entries),
                "subscriptionPorts": ports,
                "vlessPublicTraffic": {port: True for port in latencies},
                "vlessLatencyMs": latencies,
                "mainVlessLatencyMs": int(checked["vlessLatencyMs"]),
                "mainSocksLatencyMs": int(checked["socksLatencyMs"]),
                "replacement": replacement,
                "legacyCleanup": cleanup,
            },
            ensure_ascii=False,
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
