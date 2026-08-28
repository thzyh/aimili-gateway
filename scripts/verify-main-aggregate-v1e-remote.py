"""在 VPS 内验证第 4 主连接和单地址聚合 VLESS，不输出连接秘密。"""

from __future__ import annotations

import http.cookiejar
import ipaddress
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

BASE = "https://ny.zouyunhui.cc.cd"
XRAY = "/usr/local/x-ui/bin/xray-linux-amd64"


def call(opener, method: str, path: str, payload=None):
    data = None
    headers = {"Accept": "application/json"}
    if method != "GET":
        headers["Origin"] = BASE
    if payload is not None:
        data = json.dumps(payload, separators=(",", ":")).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    with opener.open(request, timeout=180) as response:
        raw = response.read()
        return json.loads(raw) if raw else None


def recv_exact(sock: socket.socket, size: int) -> bytes:
    result = b""
    while len(result) < size:
        chunk = sock.recv(size - len(result))
        if not chunk:
            raise RuntimeError("unexpected eof")
        result += chunk
    return result


def socks_exit(uri: str) -> str:
    parsed = urllib.parse.urlparse(uri)
    with socket.create_connection(("127.0.0.1", parsed.port), timeout=30) as sock:
        sock.settimeout(30)
        sock.sendall(b"\x05\x01\x02")
        if recv_exact(sock, 2) != b"\x05\x02":
            raise RuntimeError("SOCKS auth method rejected")
        user = urllib.parse.unquote(parsed.username or "").encode()
        password = urllib.parse.unquote(parsed.password or "").encode()
        sock.sendall(bytes((1, len(user))) + user + bytes((len(password),)) + password)
        if recv_exact(sock, 2) != b"\x01\x00":
            raise RuntimeError("SOCKS credentials rejected")
        host = b"api.ipify.org"
        sock.sendall(b"\x05\x01\x00\x03" + bytes((len(host),)) + host + (80).to_bytes(2, "big"))
        header = recv_exact(sock, 4)
        if header[1] != 0:
            raise RuntimeError("SOCKS connect rejected")
        size = {1: 4, 4: 16}.get(header[3])
        if header[3] == 3:
            size = recv_exact(sock, 1)[0]
        if size is None:
            raise RuntimeError("invalid SOCKS response")
        recv_exact(sock, size + 2)
        sock.sendall(b"GET / HTTP/1.1\r\nHost: api.ipify.org\r\nConnection: close\r\n\r\n")
        response = b""
        while True:
            chunk = sock.recv(4096)
            if not chunk:
                break
            response += chunk
    value = response.partition(b"\r\n\r\n")[2].decode().strip()
    return str(ipaddress.ip_address(value))


def vless_exit(uri: str) -> str:
    parsed = urllib.parse.urlparse(uri)
    query = urllib.parse.parse_qs(parsed.query)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    config = {
        "log": {"loglevel": "warning"},
        "inbounds": [{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": {"udp": False}}],
        "outbounds": [{
            "protocol": "vless",
            "settings": {"vnext": [{"address": "127.0.0.1", "port": parsed.port, "users": [{
                "id": urllib.parse.unquote(parsed.username or ""), "encryption": "none", "flow": query["flow"][0],
            }]}]},
            "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
                "fingerprint": query["fp"][0], "serverName": query["sni"][0], "password": query["pbk"][0],
                "shortId": query["sid"][0], "spiderX": "/",
            }},
        }],
    }
    fd, name = tempfile.mkstemp(prefix="aimili-v1e-", suffix=".json")
    process = None
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(config, handle, separators=(",", ":"))
        os.chmod(name, 0o600)
        process = subprocess.Popen([XRAY, "run", "-config", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(50):
            if process.poll() is not None:
                raise RuntimeError("Xray test client exited")
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.2)
        completed = subprocess.run(["curl", "-4", "-fsS", "--socks5-hostname", f"127.0.0.1:{port}", "--max-time", "30", "https://api.ipify.org"], capture_output=True, text=True, timeout=40)
        if completed.returncode != 0:
            raise RuntimeError("VLESS probe failed")
        return str(ipaddress.ip_address(completed.stdout.strip()))
    finally:
        if process is not None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill(); process.wait(timeout=5)
        pathlib.Path(name).unlink(missing_ok=True)


def main() -> int:
    account = json.loads(pathlib.Path("/opt/aimilivpn/vpngate_data/ui_auth.json").read_text(encoding="utf-8"))
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    call(opener, "POST", "/api/v1/auth/login", {"username": account["username"], "password": account["password"], "totp": ""})
    groups = call(opener, "GET", "/api/v1/proxy-groups")
    ready = [group for group in groups if group.get("status") == "ready"]
    main_group = next((group for group in ready if group.get("egressSource") == "main"), None)
    if main_group is None:
        raise RuntimeError("main egress is not ready")
    exits = {str(ipaddress.ip_address(group["exitIp"])) for group in ready}
    if len(exits) != len(ready):
        raise RuntimeError("ready egress IPs are not unique")
    main_connections = call(opener, "GET", "/api/v1/proxy-groups/agw-main/connections")
    aggregate = call(opener, "GET", "/api/v1/proxy-groups/aggregate/connections")
    main_socks_exit = socks_exit(main_connections["socks5hUri"])
    main_vless_exit = vless_exit(main_connections["vlessUri"])
    aggregate_exit = vless_exit(aggregate["vlessUri"])
    result = {
        "ready_groups": len(ready),
        "main_socks5h": main_socks_exit == main_group["exitIp"],
        "main_vless": main_vless_exit == main_group["exitIp"],
        "aggregate_single_uri": isinstance(aggregate.get("vlessUri"), str) and "\n" not in aggregate["vlessUri"],
        "aggregate_healthy_exit": aggregate_exit in exits,
        "unique_ready_exits": len(exits) == len(ready),
    }
    print(json.dumps(result, sort_keys=True))
    return 0 if all(value for key, value in result.items() if key != "ready_groups") and len(ready) == 4 else 2


if __name__ == "__main__":
    raise SystemExit(main())
