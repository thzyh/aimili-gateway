#!/usr/bin/env python3
"""在 VPS 内执行脱敏的国家代理真实协议验收。"""

from __future__ import annotations

import argparse
import contextlib
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


class GatewayClient:
    def __init__(self, origin: str, username: str, password: str) -> None:
        self.origin = origin.rstrip("/")
        self.username = username
        self.password = password
        self.csrf = ""
        jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def request(self, method: str, path: str, payload: object | None = None, *, idempotent: bool = False) -> object | None:
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
        request = urllib.request.Request(self.origin + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=180) as response:
                raw = response.read(1 << 20)
                if response.status == 204:
                    return None
                return json.loads(raw)
        except urllib.error.HTTPError as error:
            raw = error.read(1 << 16)
            try:
                code = json.loads(raw).get("error", "request_failed")
            except Exception:
                code = "invalid_error_response"
            raise RuntimeError(f"Gateway API {method} {path} failed: {code}") from None

    def authenticate(self) -> None:
        options = self.request("GET", "/api/v1/auth/options")
        if options and options.get("totpRequired"):
            raise RuntimeError("Gateway TOTP must be disabled for unattended VPS verification")
        self.request("POST", "/api/v1/auth/login", {"username": self.username, "password": self.password})
        session = self.request("GET", "/api/v1/auth/session")
        self.csrf = str(session["csrfToken"])
        self.request("POST", "/api/v1/auth/reauth", {"password": self.password})


def choose_country(countries: list[dict[str, object]]) -> tuple[str, str]:
    ranked: list[tuple[int, str, str]] = []
    for country in countries:
        code = str(country.get("code", ""))
        ranked.append((int(country.get("datacenterCount", 0)), code, "datacenter"))
        ranked.append((int(country.get("residentialCount", 0)), code, "residential"))
    count, code, proxy_type = max(ranked, default=(0, "", ""))
    if count < 1 or len(code) != 2:
        raise RuntimeError("no available country/type candidate")
    return code, proxy_type


def public_ipv4() -> str:
    with urllib.request.urlopen("http://api.ipify.org", timeout=10) as response:
        value = response.read(64).decode().strip()
    return str(ipaddress.ip_address(value))


def curl_through_proxy(proxy_uri: str, *, interface: str | None = None, expect_success: bool = True) -> str:
    command = ["curl", "--silent", "--show-error", "--fail", "--max-time", "25", "--proxy", proxy_uri]
    if interface:
        command.extend(["--interface", interface])
    command.append("http://api.ipify.org")
    result = subprocess.run(command, capture_output=True, text=True, timeout=35)
    if expect_success:
        if result.returncode != 0:
            raise RuntimeError("SOCKS5H public traffic failed")
        return str(ipaddress.ip_address(result.stdout.strip()))
    if result.returncode == 0 and result.stdout.strip():
        raise RuntimeError("unauthorized mixed source unexpectedly carried traffic")
    return ""


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


def build_vless_document(parsed: urllib.parse.SplitResult, required: dict[str, str], port: int) -> dict[str, object]:
    return {
        "log": {"loglevel": "none"},
        "inbounds": [{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": {"udp": False}}],
        "outbounds": [{
            "protocol": "vless",
            "settings": {"vnext": [{"address": parsed.hostname, "port": parsed.port, "users": [{"id": parsed.username, "encryption": "none", "flow": required["flow"]}]}]},
            "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {"fingerprint": required["fp"], "serverName": required["sni"], "password": required["pbk"], "shortId": required["sid"], "spiderX": "/"}},
        }],
    }


def validate_vless(uri: str, expected_exit: str, xray_path: str) -> None:
    parsed = urllib.parse.urlsplit(uri)
    query = urllib.parse.parse_qs(parsed.query)
    required = {key: values[0] for key, values in query.items() if values}
    if parsed.scheme != "vless" or not parsed.username or not parsed.hostname or not parsed.port:
        raise RuntimeError("invalid VLESS connection document")
    port = reserve_port()
    document = build_vless_document(parsed, required, port)
    descriptor, config_path = tempfile.mkstemp(prefix="aimili-public-vless-", suffix=".json")
    os.fchmod(descriptor, 0o600)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as config_file:
            json.dump(document, config_file)
        process = subprocess.Popen([xray_path, "run", "-config", config_path], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            wait_port(port, process)
            actual = curl_through_proxy(f"socks5h://127.0.0.1:{port}")
            if actual != expected_exit:
                raise RuntimeError("VLESS public traffic used an unexpected exit")
        finally:
            process.terminate()
            with contextlib.suppress(subprocess.TimeoutExpired):
                process.wait(timeout=3)
            if process.poll() is None:
                process.kill()
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.remove(config_path)


def validate_protocols(connections: dict[str, str], expected_exit: str, xray_path: str) -> None:
    socks_uri = connections["socks5hUri"]
    parsed = urllib.parse.urlsplit(socks_uri)
    if parsed.scheme != "socks5h" or not parsed.username or not parsed.password or not parsed.hostname or not parsed.port:
        raise RuntimeError("invalid SOCKS5H connection document")
    if curl_through_proxy(socks_uri) != expected_exit:
        raise RuntimeError("SOCKS5H public traffic used an unexpected exit")
    unauthorized = urllib.parse.urlunsplit((parsed.scheme, parsed.netloc.replace(parsed.hostname, "127.0.0.1"), parsed.path, parsed.query, parsed.fragment))
    curl_through_proxy(unauthorized, interface="127.0.0.2", expect_success=False)
    validate_vless(connections["vlessUri"], expected_exit, xray_path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--origin", required=True)
    parser.add_argument("--credentials", required=True)
    parser.add_argument("--config", required=True)
    parser.add_argument("--xray", required=True)
    arguments = parser.parse_args()

    credentials = json.loads(pathlib.Path(arguments.credentials).read_text())
    configuration = json.loads(pathlib.Path(arguments.config).read_text())
    client = GatewayClient(arguments.origin, credentials["username"], credentials["password"])
    client.authenticate()

    countries = client.request("GET", "/api/v1/countries")
    groups = client.request("GET", "/api/v1/proxy-groups")
    if groups:
        group = groups[0]
    else:
        prefixes = list(configuration.get("mixedSourceCidrs", []))
        server_prefix = public_ipv4() + "/32"
        if server_prefix not in prefixes:
            prefixes.append(server_prefix)
        client.request("PUT", "/api/v1/settings/mixed-cidrs", {"cidrs": prefixes})
        country, proxy_type = choose_country(countries)
        group = client.request("POST", "/api/v1/proxy-groups", {"countryCode": country, "proxyType": proxy_type}, idempotent=True)

    if group["status"] != "ready":
        raise RuntimeError(f"proxy group not ready: {group['status']}")
    connections = client.request("GET", f"/api/v1/proxy-groups/{group['id']}/connections")
    validate_protocols(connections, group["exitIp"], arguments.xray)

    checked = client.request("POST", f"/api/v1/proxy-groups/{group['id']}/check", {})
    if checked["exitIp"] != group["exitIp"] or checked["vlessPort"] != group["vlessPort"] or checked["mixedPort"] != group["mixedPort"]:
        raise RuntimeError("check changed stable proxy state")

    rotated = client.request("POST", f"/api/v1/proxy-groups/{group['id']}/rotate", {}, idempotent=True)
    if rotated["countryCode"] != group["countryCode"] or rotated["proxyType"] != group["proxyType"] or rotated["vlessPort"] != group["vlessPort"] or rotated["mixedPort"] != group["mixedPort"]:
        raise RuntimeError("rotate changed stable entry identity")
    if rotated["status"] != "ready":
        raise RuntimeError(f"rotated proxy group not ready: {rotated['status']}")
    validate_protocols(connections, rotated["exitIp"], arguments.xray)

    print(json.dumps({
        "authenticated": True,
        "countryCount": len(countries),
        "countryCode": group["countryCode"],
        "proxyType": group["proxyType"],
        "status": rotated["status"],
        "vlessPublicTraffic": True,
        "socks5hPublicTraffic": True,
        "proxyDNS": True,
        "unauthorizedMixedRejected": True,
        "checkKeptExit": True,
        "rotateKeptEntry": True,
        "exitChanged": rotated["exitIp"] != group["exitIp"],
    }, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
