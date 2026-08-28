"""从 Windows 外部客户端验证当前 V1-C VLESS 与 SOCKS5H 公网入口。

脚本通过 SSH 在 VPS 内部完成 Gateway 登录和重新认证，连接材料仅保存在
进程内存及受限临时 Xray 配置中；标准输出只包含布尔验收结果。
"""

from __future__ import annotations

import ipaddress
import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.parse


REMOTE_HELPER = r'''
import http.cookiejar, urllib.request, json, urllib.parse
base='https://ny.zouyunhui.cc.cd'
account=json.load(open('/opt/aimilivpn/vpngate_data/ui_auth.json',encoding='utf-8'))
jar=http.cookiejar.CookieJar(); op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def call(method,path,payload=None,csrf=''):
 data=None; headers={'Accept':'application/json'}
 if payload is not None: data=json.dumps(payload,separators=(',',':')).encode(); headers['Content-Type']='application/json'
 if method!='GET': headers['Origin']=base; headers.update({'X-CSRF-Token':csrf} if csrf else {})
 req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
 with op.open(req,timeout=180) as r:
  raw=r.read(); return json.loads(raw) if raw else None
call('POST','/api/v1/auth/login',{'username':account['username'],'password':account['password'],'totp':''})
session=call('GET','/api/v1/auth/session'); groups=call('GET','/api/v1/proxy-groups'); ready=[g for g in groups if g['status']=='ready']
if len(ready)!=1: raise SystemExit('ready group count mismatch')
connections=call('GET','/api/v1/proxy-groups/'+urllib.parse.quote(ready[0]['id'],safe='')+'/connections')
print(json.dumps({'exitIp':ready[0]['exitIp'],'vlessUri':connections['vlessUri'],'socks5hUri':connections['socks5hUri']},separators=(',',':')))
'''


def recv_exact(sock: socket.socket, size: int) -> bytes:
    chunks: list[bytes] = []
    remaining = size
    while remaining:
        chunk = sock.recv(remaining)
        if not chunk:
            raise RuntimeError("unexpected SOCKS EOF")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def request_ip_through_socks(host: str, port: int, username: str = "", password: str = "") -> str:
    with socket.create_connection((host, port), timeout=20) as raw:
        raw.settimeout(20)
        method = 2 if username or password else 0
        raw.sendall(bytes((5, 1, method)))
        if recv_exact(raw, 2) != bytes((5, method)):
            raise RuntimeError("SOCKS authentication method rejected")
        if method == 2:
            user = username.encode()
            secret = password.encode()
            raw.sendall(bytes((1, len(user))) + user + bytes((len(secret),)) + secret)
            if recv_exact(raw, 2) != b"\x01\x00":
                raise RuntimeError("SOCKS credentials rejected")
        target = b"api.ipify.org"
        raw.sendall(b"\x05\x01\x00\x03" + bytes((len(target),)) + target + (80).to_bytes(2, "big"))
        header = recv_exact(raw, 4)
        if header[1] != 0:
            raise RuntimeError("SOCKS connect rejected")
        address_size = {1: 4, 4: 16}.get(header[3])
        if header[3] == 3:
            address_size = recv_exact(raw, 1)[0]
        if address_size is None:
            raise RuntimeError("invalid SOCKS address type")
        recv_exact(raw, address_size + 2)
        raw.sendall(b"GET / HTTP/1.1\r\nHost: api.ipify.org\r\nConnection: close\r\n\r\n")
        response = bytearray()
        while True:
            chunk = raw.recv(4096)
            if not chunk:
                break
            response.extend(chunk)
    body = bytes(response).partition(b"\r\n\r\n")[2].decode().strip()
    ipaddress.ip_address(body)
    return body


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def tcp_reachable(host: str, port: int) -> bool:
    try:
        with socket.create_connection((host, port), timeout=10):
            return True
    except OSError:
        return False


def main() -> int:
    completed = subprocess.run(
        ["ssh", "ny", "python3", "-"], input=REMOTE_HELPER, text=True,
        capture_output=True, timeout=240, check=True,
    )
    payload = json.loads(completed.stdout)
    expected = str(ipaddress.ip_address(payload["exitIp"]))
    socks = urllib.parse.urlparse(payload["socks5hUri"])
    proxy_environment = os.environ.copy()
    proxy_environment["ALL_PROXY"] = payload["socks5hUri"]
    proxy_environment["HTTPS_PROXY"] = payload["socks5hUri"]
    proxy_environment["NO_PROXY"] = ""
    socks_result = subprocess.run(
        ["curl.exe", "-4", "-fsS", "--max-time", "25", "https://api.ipify.org"],
        capture_output=True, text=True, env=proxy_environment,
    )
    try:
        socks_response_is_ip = bool(ipaddress.ip_address(socks_result.stdout.strip()))
    except ValueError:
        socks_response_is_ip = False
    socks_ok = socks_result.returncode == 0 and socks_response_is_ip and socks_result.stdout.strip() == expected

    vless = urllib.parse.urlparse(payload["vlessUri"])
    query = urllib.parse.parse_qs(vless.query)
    public_socks_tcp = tcp_reachable(str(socks.hostname), int(socks.port or 0))
    public_vless_tcp = tcp_reachable(str(vless.hostname), int(vless.port or 0))
    local_port = free_port()
    config = {
        "log": {"loglevel": "warning"},
        "inbounds": [{"listen": "127.0.0.1", "port": local_port, "protocol": "socks", "settings": {"udp": False}}],
        "outbounds": [{
            "protocol": "vless",
            "settings": {"vnext": [{"address": vless.hostname, "port": vless.port, "users": [{
                "id": urllib.parse.unquote(vless.username or ""), "encryption": "none", "flow": query["flow"][0],
            }]}]},
            "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
                "fingerprint": query["fp"][0], "serverName": query["sni"][0],
                "password": query["pbk"][0], "shortId": query["sid"][0], "spiderX": "/",
            }},
        }],
    }
    xray = r"E:\SoftWare\v2rayN-windows-64\bin\xray\xray.exe"
    handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", suffix=".json", delete=False)
    try:
        json.dump(config, handle, separators=(",", ":"))
        handle.close()
        process = subprocess.Popen([xray, "run", "-config", handle.name], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        vless_error_category = "none"
        try:
            for _ in range(50):
                try:
                    with socket.create_connection(("127.0.0.1", local_port), timeout=0.2):
                        break
                except OSError:
                    time.sleep(0.2)
            result = subprocess.run(
                ["curl.exe", "-4", "-fsS", "--socks5-hostname", f"127.0.0.1:{local_port}",
                 "--max-time", "25", "https://api.ipify.org"],
                capture_output=True, text=True,
            )
            try:
                vless_response_is_ip = bool(ipaddress.ip_address(result.stdout.strip()))
            except ValueError:
                vless_response_is_ip = False
            vless_ok = result.returncode == 0 and vless_response_is_ip and result.stdout.strip() == expected
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            diagnostic = (process.stderr.read() if process.stderr is not None else "").lower()
            for category, markers in (
                ("timeout", ("timeout", "deadline exceeded")),
                ("reality_rejected", ("reality", "rejected")),
                ("connection_rejected", ("rejected", "reset by peer", "forcibly closed")),
                ("dns", ("failed to lookup", "no such host")),
                ("config", ("failed to load config", "unknown field", "failed to parse")),
            ):
                if any(marker in diagnostic for marker in markers):
                    vless_error_category = category
                    break
    finally:
        try:
            os.unlink(handle.name)
        except FileNotFoundError:
            pass
    print(json.dumps({
        "external_socks5h": socks_ok, "external_socks5h_proxy_dns": socks_ok,
        "external_socks_tcp": public_socks_tcp, "socks_curl_exit": socks_result.returncode,
        "socks_response_is_ip": socks_response_is_ip, "external_vless": vless_ok,
        "external_vless_tcp": public_vless_tcp, "vless_curl_exit": result.returncode,
        "vless_response_is_ip": vless_response_is_ip, "vless_error_category": vless_error_category,
    }, sort_keys=True))
    return 0 if socks_ok and vless_ok else 2


if __name__ == "__main__":
    raise SystemExit(main())
