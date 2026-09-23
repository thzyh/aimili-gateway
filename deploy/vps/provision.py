#!/usr/bin/env python3
"""使用正式 Gateway API 验证新部署与域名切换。"""

import http.cookiejar
import json
import sqlite3
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


class APIError(Exception):
    pass


class GatewayClient:
    def __init__(self, origin: str, credentials: dict):
        self.origin = origin
        self.csrf = ""
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ssl.create_default_context()),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )
        self.credentials = credentials

    def request(self, method: str, path: str, body=None, key: str = ""):
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if method != "GET":
            headers["Origin"] = self.origin
            if self.csrf:
                headers["X-CSRF-Token"] = self.csrf
        if key:
            headers["Idempotency-Key"] = key
        request = urllib.request.Request(self.origin + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=90) as response:
                raw = response.read(1 << 20)
                if response.status >= 400:
                    raise APIError(f"HTTP {response.status}")
        except urllib.error.HTTPError as error:
            try:
                code = json.loads(error.read(4096)).get("error", {}).get("code", "request_failed")
            except (ValueError, AttributeError):
                code = "request_failed"
            raise APIError(str(code)) from None
        except (OSError, urllib.error.URLError) as error:
            raise APIError(type(error).__name__) from None
        if not raw:
            return {}
        try:
            return json.loads(raw)
        except ValueError as error:
            raise APIError("invalid_response") from error

    def login(self):
        self.request("POST", "/api/v1/auth/login", {"username": self.credentials["username"], "password": self.credentials["password"], "totp": ""})
        session = self.request("GET", "/api/v1/auth/session")
        self.csrf = session.get("csrfToken", "")
        if not self.csrf:
            raise APIError("session_missing")


def logical_groups(groups: list, slots: int) -> tuple[dict, dict[int, dict]]:
    main = next((item for item in groups if item.get("id") == "agw-main" and item.get("status") == "ready" and item.get("protocolState") == "ready"), None)
    regular = {}
    for item in groups:
        if not item.get("fixed") or item.get("id") == "agw-main":
            continue
        number = item.get("slotNumber")
        if number in regular:
            raise APIError("duplicate_slot")
        regular[number] = item
    if main is None or set(regular) != set(range(1, slots + 1)):
        raise APIError("slots_not_ready")
    if any(item.get("status") != "ready" or item.get("protocolState") != "ready" for item in regular.values()):
        raise APIError("slots_not_ready")
    return main, regular


def provision(origin: str, slots: int, credentials: dict, source: str) -> None:
    client = GatewayClient(origin, credentials)
    client.login()
    deadline = time.monotonic() + 900
    last_error = ""
    complete = False
    for attempt in range(1, 151):
        if time.monotonic() >= deadline:
            break
        try:
            if attempt == 1 or attempt % 10 == 0:
                client.request("POST", "/api/v1/proxy-groups/reconcile", {})
            try:
                client.request("POST", "/api/v1/proxy-groups/agw-main/check", {}, key=f"vps-install-main-{attempt}")
            except APIError as error:
                last_error = str(error)
            groups = client.request("GET", "/api/v1/proxy-groups")
            main, regular = logical_groups(groups, slots)
            subscription = client.request("GET", "/api/v1/proxy-groups/subscription")
            if subscription.get("inboundCount") != slots + 1:
                raise APIError("subscription_coverage_mismatch")
            client.request("POST", "/api/v1/proxy-groups/agw-main/check", {}, key=f"vps-install-main-verified-{attempt}")
            for number, item in sorted(regular.items()):
                value = client.request("POST", "/api/v1/proxy-groups/" + urllib.parse.quote(item["id"], safe="") + "/check", {})
                if value.get("status") != "ready":
                    raise APIError(f"slot_{number}_not_ready")
            complete = True
            break
        except APIError as error:
            last_error = str(error)
            time.sleep(5)
    if not complete:
        raise RuntimeError("Gateway 业务链路未收敛：" + last_error)
    policy = client.request("GET", "/api/v1/settings/mixed-source-policy")
    if policy.get("applyStatus") != "applied":
        client.request("PUT", "/api/v1/settings/mixed-source-policy", {"enabled": True, "cidrs": [source + "/32"]})
    summary = client.request("GET", "/api/v1/settings/3x-ui")
    if not summary.get("ownershipMatches"):
        raise RuntimeError("3x-ui 受管资源核对失败")
    db = sqlite3.connect("file:/etc/x-ui/x-ui.db?mode=ro", uri=True)
    count, named = db.execute("SELECT COUNT(*),SUM(alias_override <> '') FROM client_inbounds ci JOIN clients c ON ci.client_id=c.id WHERE c.email='aimili-gateway-subscription'").fetchone()
    if count != slots + 1 or named != count:
        raise RuntimeError("订阅逐关联别名不完整")
    gateway = sqlite3.connect("file:/var/lib/aimili-gateway/aimili-gateway.db?mode=ro", uri=True)
    if gateway.execute("PRAGMA quick_check").fetchone()[0] != "ok" or db.execute("PRAGMA quick_check").fetchone()[0] != "ok":
        raise RuntimeError("数据库完整性检查失败")
    print(json.dumps({"status": "ready", "logicalExits": slots + 1, "regularSlots": slots, "subscriptionInbounds": count, "xuiOwnership": True}, ensure_ascii=False))


def remove_unneeded_slots(origin: str, slots: int, credentials: dict) -> bool:
    client = GatewayClient(origin, credentials)
    client.login()
    for group in client.request("GET", "/api/v1/proxy-groups"):
        number = group.get("slotNumber")
        if group.get("fixed") and group.get("id") != "agw-main" and isinstance(number, int) and number > slots:
            client.request("DELETE", "/api/v1/proxy-groups/" + urllib.parse.quote(group["id"], safe=""))
    config_path = Path("/var/lib/aimili-gateway/aimili-egress/ui_auth.json")
    current = json.loads(config_path.read_text())
    changed = current.get("exit_slot_active") != list(range(slots))
    if changed:
        current["exit_slot_active"] = list(range(slots))
        current["exit_slot_count"] = slots
        current["exit_slot_paused"] = []
        temporary = config_path.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(current))
        temporary.chmod(0o600)
        temporary.replace(config_path)
    return changed
