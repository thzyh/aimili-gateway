"""从 Windows 外部客户端验证当前 V1-C VLESS 与 SOCKS5H 公网入口。

脚本通过 SSH 在 VPS 内部完成 Gateway 登录和重新认证，连接材料仅保存在
进程内存及受限临时 Xray 配置中；标准输出只包含布尔验收结果。
"""

from __future__ import annotations

import argparse
import ipaddress
import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.parse


REMOTE_HELPER = r'''
import http.cookiejar, urllib.request, json, urllib.parse, socket
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
def recv_exact(sock,size):
 chunks=[]
 while size:
  chunk=sock.recv(size)
  if not chunk: raise RuntimeError('unexpected eof')
  chunks.append(chunk); size-=len(chunk)
 return b''.join(chunks)
def authorized_socks(uri,expected):
 u=urllib.parse.urlparse(uri)
 with socket.create_connection(('127.0.0.1',u.port),timeout=30) as s:
  s.settimeout(30); s.sendall(b'\x05\x01\x02')
  if recv_exact(s,2)!=b'\x05\x02': return False
  user=urllib.parse.unquote(u.username or '').encode(); password=urllib.parse.unquote(u.password or '').encode()
  s.sendall(bytes((1,len(user)))+user+bytes((len(password),))+password)
  if recv_exact(s,2)!=b'\x01\x00': return False
  host=b'api.ipify.org'; s.sendall(b'\x05\x01\x00\x03'+bytes((len(host),))+host+(80).to_bytes(2,'big'))
  h=recv_exact(s,4)
  if h[1]!=0: return False
  n={1:4,4:16}.get(h[3]); n=recv_exact(s,1)[0] if h[3]==3 else n
  if n is None: return False
  recv_exact(s,n+2); s.sendall(b'GET / HTTP/1.1\r\nHost: api.ipify.org\r\nConnection: close\r\n\r\n')
  response=b''
  while True:
   chunk=s.recv(4096)
   if not chunk: break
   response+=chunk
  return response.partition(b'\r\n\r\n')[2].decode().strip()==expected
call('POST','/api/v1/auth/login',{'username':account['username'],'password':account['password'],'totp':''})
session=call('GET','/api/v1/auth/session'); groups=call('GET','/api/v1/proxy-groups'); ready=[g for g in groups if g['status']=='ready']
policy=call('GET','/api/v1/settings/mixed-source-policy')
materials=[]
for group in ready:
 connections=call('GET','/api/v1/proxy-groups/'+urllib.parse.quote(group['id'],safe='')+'/connections')
 materials.append({'exitIp':group['exitIp'],'vlessUri':connections['vlessUri'],'socks5hUri':connections['socks5hUri'],'authorizedSocks5h':authorized_socks(connections['socks5hUri'],group['exitIp'])})
print(json.dumps({'sourceRestrictionEnabled':bool(policy.get('enabled')),'groups':materials},separators=(',',':')))
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


def require_ready_materials(materials: object) -> list[dict[str, str]]:
    if not isinstance(materials, list) or not materials:
        raise RuntimeError("no ready groups")
    required = ("exitIp", "vlessUri", "socks5hUri")
    for material in materials:
        if not isinstance(material, dict) or any(not material.get(field) for field in required):
            raise RuntimeError("incomplete ready group material")
    return materials


def stop_process(process: subprocess.Popen[str]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def group_passes(*, source_restriction_enabled: bool, public_socks: bool,
                 public_socks_tcp: bool, authorized_socks: bool, vless: bool) -> bool:
    socks_ok = authorized_socks and public_socks_tcp
    if not source_restriction_enabled:
        socks_ok = socks_ok and public_socks
    return socks_ok and vless


def should_probe_public_socks(source_restriction_enabled: bool) -> bool:
    return not source_restriction_enabled


def select_materials(materials: list[dict[str, str]], index: int | None) -> list[dict[str, str]]:
    if index is None:
        return materials
    if index < 0 or index >= len(materials):
        raise ValueError("ready group index is out of range")
    return [materials[index]]


def verify_group(payload: dict[str, str], probe_public_socks: bool = True) -> dict[str, object]:
    expected = str(ipaddress.ip_address(payload["exitIp"]))
    socks = urllib.parse.urlparse(payload["socks5hUri"])
    proxy_environment = os.environ.copy()
    proxy_environment["ALL_PROXY"] = payload["socks5hUri"]
    proxy_environment["HTTPS_PROXY"] = payload["socks5hUri"]
    proxy_environment["NO_PROXY"] = ""
    socks_result = subprocess.CompletedProcess([], -1, "", "")
    if probe_public_socks:
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
    process: subprocess.Popen[str] | None = None
    result = subprocess.CompletedProcess([], 125, "", "not started")
    diagnostic = ""
    try:
        json.dump(config, handle, separators=(",", ":"))
        handle.close()
        creationflags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as error_log:
            process = subprocess.Popen(
                [xray, "run", "-config", handle.name],
                stdout=subprocess.DEVNULL, stderr=error_log, text=True,
                creationflags=creationflags,
            )
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
            stop_process(process)
            process = None
            error_log.seek(0)
            diagnostic = error_log.read().lower()
    finally:
        if process is not None:
            stop_process(process)
        try:
            os.unlink(handle.name)
        except FileNotFoundError:
            pass
    vless_error_category = "none"
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
    return {
        "external_socks5h": socks_ok, "external_socks5h_proxy_dns": socks_ok,
        "external_socks_tcp": public_socks_tcp, "socks_curl_exit": socks_result.returncode,
        "socks_response_is_ip": socks_response_is_ip, "external_vless": vless_ok,
        "external_vless_tcp": public_vless_tcp, "vless_curl_exit": result.returncode,
        "vless_response_is_ip": vless_response_is_ip, "vless_error_category": vless_error_category,
    }


def main(index: int | None = None) -> int:
    completed = subprocess.run(
        ["ssh", "ny", "python3", "-"], input=REMOTE_HELPER, text=True,
        capture_output=True, timeout=240, check=True,
    )
    remote = json.loads(completed.stdout)
    if not isinstance(remote, dict):
        raise RuntimeError("invalid remote payload")
    source_restriction_enabled = bool(remote.get("sourceRestrictionEnabled"))
    all_materials = require_ready_materials(remote.get("groups"))
    materials = select_materials(all_materials, index)
    public_socks_probe = should_probe_public_socks(source_restriction_enabled)
    verified = [verify_group(material, public_socks_probe) for material in materials]
    results = []
    for material, result in zip(materials, verified):
        result["authorized_socks5h"] = bool(material.get("authorizedSocks5h"))
        results.append(result)
    exit_ips = [str(ipaddress.ip_address(material["exitIp"])) for material in all_materials]
    passed = all(group_passes(
        source_restriction_enabled=source_restriction_enabled,
        public_socks=bool(result["external_socks5h"]),
        public_socks_tcp=bool(result["external_socks_tcp"]),
        authorized_socks=bool(result["authorized_socks5h"]),
        vless=bool(result["external_vless"]),
    ) for result in results)
    print(json.dumps({
        "ready_groups": len(all_materials),
        "verified_groups": len(results),
        "source_restriction_enabled": source_restriction_enabled,
        "unique_exit_ips": len(set(exit_ips)) == len(exit_ips),
        "all_public_socks5h": all(result["external_socks5h"] for result in results),
        "all_authorized_socks5h": all(result["authorized_socks5h"] for result in results),
        "all_external_vless": all(result["external_vless"] for result in results),
        "groups": results,
    }, sort_keys=True))
    return 0 if passed and len(set(exit_ips)) == len(exit_ips) else 2


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--index", type=int)
    arguments = parser.parse_args()
    raise SystemExit(main(arguments.index))
