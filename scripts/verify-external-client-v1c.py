"""从 Windows 外部客户端验证当前 V1-C VLESS 与 SOCKS5H 公网入口。

脚本通过 SSH 在 VPS 内部完成 Gateway 登录和重新认证，连接材料仅保存在
进程内存及受限临时 Xray 配置中；标准输出只包含布尔验收结果。
"""

from __future__ import annotations

import argparse
import base64
import ipaddress
import json
import os
import re
import socket
import subprocess
import tempfile
import time
import urllib.parse


PROTOCOL_MODES = {
    "vless_tcp_reality_vision",
    "vless_xhttp_reality",
    "hysteria2_quic_tls",
}
EGRESS_PROBE_URL = "http://api.ipify.org"
MAIN_SUBSCRIPTION_REMARKS = {
    "Aimili Reality",
    "Aimili Reality-aimili-gateway-subscription",
}

SAFE_RUNTIME_ERRORS = {
    "no ready groups": "no_ready_groups",
    "incomplete ready group material": "incomplete_ready_group_material",
    "invalid protocol mode": "invalid_protocol_mode",
    "protocol URI mismatch": "protocol_uri_mismatch",
    "empty subscription": "empty_subscription",
    "subscription is not a v2rayN document": "subscription_document_invalid",
    "subscription contains an unsupported entry": "subscription_entry_unsupported",
    "subscription coverage mismatch": "subscription_coverage_mismatch",
    "invalid public connection document": "public_connection_invalid",
    "invalid VLESS connection document": "vless_connection_invalid",
    "invalid XHTTP connection document": "xhttp_connection_invalid",
    "invalid Hysteria2 connection document": "hysteria2_connection_invalid",
    "invalid remote payload": "remote_payload_invalid",
    "invalid switch inspection": "switch_inspection_invalid",
    "rollback verification failed": "rollback_verification_failed",
    "invalid switch result": "switch_result_invalid",
}


def safe_error_category(error: BaseException) -> str:
    if isinstance(error, subprocess.CalledProcessError):
        slot_match = re.search(
            r"\bremote_(connections|group_check)_slot_([0-9]+)_http_([45][0-9]{2})(?:_([a-z0-9_]{1,64}))?\b",
            error.stderr or "",
        )
        if slot_match is not None:
            category = "remote_" + slot_match.group(1) + "_slot_" + slot_match.group(2) + "_http_" + slot_match.group(3)
            if slot_match.group(4):
                category += "_" + slot_match.group(4)
            return category
        phase_match = re.search(
            r"\bremote_(auth_login|auth_session|groups|mixed_policy|group_check|connections|subscription|protocol_switch)_http_([45][0-9]{2})(?:_([a-z0-9_]{1,64}))?\b",
            error.stderr or "",
        )
        if phase_match is not None:
            category = "remote_" + phase_match.group(1) + "_http_" + phase_match.group(2)
            if phase_match.group(3):
                category += "_" + phase_match.group(3)
            return category
        match = re.search(r"HTTP Error ([45][0-9]{2})\b", error.stderr or "")
        if match is not None:
            return "remote_http_" + match.group(1)
        return "remote_action_failed"
    if isinstance(error, RuntimeError):
        return SAFE_RUNTIME_ERRORS.get(str(error), "runtime_error")
    return type(error).__name__


class RollbackConflict(RuntimeError):
    pass


REMOTE_HELPER = r'''
import base64, http.cookiejar, json, pathlib, re, socket, sqlite3, subprocess, sys, time, urllib.error, urllib.parse, urllib.request, uuid
base=json.load(open('/etc/aimili-gateway/config.json',encoding='utf-8'))['publicOrigin']
endpoint='http://127.0.0.1:9080'
manifest=json.load(open('/etc/aimili-local/deployment.json',encoding='utf-8')); slot_count=int(manifest['expected']['exitSlots']); expected_groups=1+slot_count
# ui_auth.json is atomically updated by the unified-account transaction.  The
# install-time admin-credentials.json is only a bootstrap recovery artifact and
# intentionally becomes stale after the operator rotates the shared account.
account=json.load(open('/opt/aimilivpn/vpngate_data/ui_auth.json',encoding='utf-8'))
jar=http.cookiejar.CookieJar(); op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def call(method,path,payload=None,csrf='',idempotent=False):
 data=None; headers={'Accept':'application/json'}
 if payload is not None: data=json.dumps(payload,separators=(',',':')).encode(); headers['Content-Type']='application/json'
 if method!='GET': headers['Origin']=base; headers.update({'X-CSRF-Token':csrf} if csrf else {})
 if idempotent: headers['Idempotency-Key']=str(uuid.uuid4())
 cookie_header='; '.join(cookie.name+'='+cookie.value for cookie in jar)
 if cookie_header: headers['Cookie']=cookie_header
 req=urllib.request.Request(endpoint+path,data=data,headers=headers,method=method)
 try:
  with op.open(req,timeout=180) as r:
   raw=r.read(); return json.loads(raw) if raw else None
 except urllib.error.HTTPError as error:
  safe_code=''
  try:
   error_document=json.loads(error.read(4096))
   candidate=error_document.get('error','')
   if isinstance(candidate,dict): candidate=candidate.get('code','')
   if isinstance(candidate,str) and re.fullmatch(r'[a-z0-9_]{1,64}',candidate): safe_code='_'+candidate
  except BaseException: pass
  if path=='/api/v1/auth/login': phase='auth_login'
  elif path=='/api/v1/auth/session': phase='auth_session'
  elif path=='/api/v1/proxy-groups': phase='groups'
  elif path=='/api/v1/settings/mixed-source-policy': phase='mixed_policy'
  elif path.endswith('/check'): phase='group_check'
  elif path.endswith('/connections'): phase='connections'
  elif path=='/api/v1/proxy-groups/subscription': phase='subscription'
  elif path.endswith('/protocol-mode'): phase='protocol_switch'
  else: phase='unknown'
  raise RuntimeError('remote_'+phase+'_http_'+str(error.code)+safe_code) from None
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
session=call('GET','/api/v1/auth/session'); csrf=session['csrfToken']
slot=int(sys.argv[1]); requested=sys.argv[2]; expected_old=sys.argv[3]
allowed={'vless_tcp_reality_vision','vless_xhttp_reality','hysteria2_quic_tls'}
switch=None
if slot >= 0:
 if slot > slot_count: raise RuntimeError('invalid_switch_request')
 groups=call('GET','/api/v1/proxy-groups')
 if slot == 0:
  target=next(g for g in groups if g.get('egressSource')=='main' and g.get('status')=='ready')
  target_id='agw-main'
 else:
  target=next(g for g in groups if int(g.get('slotNumber') or 0)==slot and g.get('egressSource')!='main' and g.get('status')=='ready')
  target_id=target['id']
 old=target['protocolMode']; target_path='/api/v1/proxy-groups/'+urllib.parse.quote(target_id,safe='')+'/protocol-mode'
 if requested=='inspect':
  print(json.dumps({'inspection':{'slot':slot,'mode':old}},separators=(',',':'))); raise SystemExit(0)
 if requested not in allowed or (expected_old!='any' and old!=expected_old): raise RuntimeError('invalid_switch_request')
 if old!=requested:
  switch={'slot':slot,'oldMode':old,'newMode':requested}
try:
 if switch is not None:
  call('PUT',target_path,{'protocolMode':requested,'expectedProtocolMode':expected_old},csrf,True)
 groups=call('GET','/api/v1/proxy-groups')
 for group in groups:
  if not group.get('fixed'): continue
  group_id='agw-main' if group.get('egressSource')=='main' else str(group['id'])
  for attempt in range(37):
   try:
    call('POST','/api/v1/proxy-groups/'+urllib.parse.quote(group_id,safe='')+'/check',{},csrf,True); break
   except RuntimeError as error:
    marker=str(error)
    if marker in {'remote_group_check_http_409_operation_busy','remote_group_check_http_409_maintenance_busy'} and attempt < 36:
     time.sleep(5); continue
    match=re.fullmatch(r'remote_group_check_http_([45][0-9]{2})(?:_([a-z0-9_]{1,64}))?',marker)
    if match:
     suffix='_'+match.group(2) if match.group(2) else ''
     raise RuntimeError('remote_group_check_slot_'+str(int(group.get('slotNumber') or 0))+'_http_'+match.group(1)+suffix) from None
    raise
 groups=call('GET','/api/v1/proxy-groups'); ready=[g for g in groups if g.get('status')=='ready']
 policy=call('GET','/api/v1/settings/mixed-source-policy')
 materials=[]
 for group in ready:
  try:
   connections=call('GET','/api/v1/proxy-groups/'+urllib.parse.quote(group['id'],safe='')+'/connections')
  except RuntimeError as error:
   marker=str(error)
   match=re.fullmatch(r'remote_connections_http_([45][0-9]{2})(?:_([a-z0-9_]{1,64}))?',marker)
   if match:
    suffix='_'+match.group(2) if match.group(2) else ''
    raise RuntimeError('remote_connections_slot_'+str(int(group.get('slotNumber') or 0))+'_http_'+match.group(1)+suffix) from None
   raise
  alias=('\u4e3b\u8fde\u63a5_' if group.get('egressSource')=='main' else '\u51fa\u53e3\u4f4d '+str(int(group.get('slotNumber') or 0))+'_')+str(group.get('countryName') or '')
  materials.append({'exitIp':group['exitIp'],'protocolMode':group['protocolMode'],'publicUri':connections['publicUri'],'socks5hUri':connections['socks5hUri'],'subscriptionAlias':alias,'slotNumber':int(group.get('slotNumber') or 0),'publicPort':int(group.get('publicPort') or group.get('vlessPort') or 0),'mixedPort':int(group.get('mixedPort') or 0),'authorizedSocks5h':authorized_socks(connections['socks5hUri'],group['exitIp'])})
 subscription=call('GET','/api/v1/proxy-groups/subscription'); subscription_url=urllib.parse.urlsplit(subscription['url'])
 request=urllib.request.Request('http://127.0.0.1:'+str(int(manifest['ports']['xuiSubscription']))+subscription_url.path,headers={'Accept':'text/plain','User-Agent':'v2rayN/7.24.4','Host':subscription_url.hostname})
 with op.open(request,timeout=30) as response: subscription_raw=response.read(1<<20)
 expected=pathlib.Path('/usr/local/x-ui/bin/xray-linux-amd64').resolve(); pids=[]
 for item in pathlib.Path('/proc').iterdir():
  if not item.name.isdigit(): continue
  try:
   if (item/'exe').resolve()==expected: pids.append(int(item.name))
  except OSError: pass
 xui=sqlite3.connect('file:/etc/x-ui/x-ui.db?mode=ro',uri=True); rows=xui.execute('SELECT tag,port FROM inbounds').fetchall(); xui.close()
 public_ports={int(item['publicPort']) for item in materials}; mixed_ports={int(item['mixedPort']) for item in materials}
 ca_paths=[pathlib.Path('/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt'),pathlib.Path('/var/lib/caddy/.local/share/caddy/pki/authorities/local/intermediate.crt')]
 tls_ca=''
 if all(path.is_file() and path.stat().st_size <= 32768 for path in ca_paths):
  tls_ca=base64.b64encode(b'\n'.join(path.read_bytes().rstrip()+b'\n' for path in ca_paths)).decode()
 print(json.dumps({'expectedGroups':expected_groups,'sourceRestrictionEnabled':bool(policy.get('enabled')),'groups':materials,'subscription':base64.b64encode(subscription_raw).decode(),'tlsCa':tls_ca,'xrayPid':pids[0] if len(pids)==1 else 0,'publicCount':sum(int(r[1]) in public_ports for r in rows),'mixedCount':sum(int(r[1]) in mixed_ports for r in rows),'switch':switch},separators=(',',':')))
except BaseException:
 raise
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
    required = ("exitIp", "protocolMode", "publicUri", "socks5hUri")
    for material in materials:
        if not isinstance(material, dict) or any(not material.get(field) for field in required):
            raise RuntimeError("incomplete ready group material")
        parsed = urllib.parse.urlsplit(str(material["publicUri"]))
        mode = str(material["protocolMode"])
        if mode not in PROTOCOL_MODES:
            raise RuntimeError("invalid protocol mode")
        if (mode == "hysteria2_quic_tls") != (parsed.scheme in {"hysteria2", "hy2"}):
            raise RuntimeError("protocol URI mismatch")
        if mode != "hysteria2_quic_tls" and parsed.scheme != "vless":
            raise RuntimeError("protocol URI mismatch")
    return materials


def decode_subscription(raw: bytes) -> list[str]:
    text = raw.decode("utf-8").strip()
    if not text:
        raise RuntimeError("empty subscription")
    if not text.startswith(("vless://", "hysteria2://", "hy2://")):
        compact = "".join(text.split())
        try:
            text = base64.urlsafe_b64decode(compact + "=" * (-len(compact) % 4)).decode("utf-8")
        except (ValueError, UnicodeDecodeError) as error:
            raise RuntimeError("subscription is not a v2rayN document") from error
    entries = [line.strip() for line in text.splitlines() if line.strip()]
    if not entries or any(urllib.parse.urlsplit(item).scheme not in {"vless", "hysteria2", "hy2"} for item in entries):
        raise RuntimeError("subscription contains an unsupported entry")
    return entries


def validate_subscription_coverage(
    materials: list[dict[str, str]], entries: list[str]
) -> dict[str, int]:
    def canonical(uri: str, ignore_fragment: bool = False) -> tuple[object, ...]:
        parsed = urllib.parse.urlsplit(uri)
        if not parsed.hostname or not parsed.port:
            raise RuntimeError("subscription coverage mismatch")
        scheme = "hysteria2" if parsed.scheme in {"hysteria2", "hy2"} else parsed.scheme
        query_pairs = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
        if len({key for key, _ in query_pairs}) != len(query_pairs):
            raise RuntimeError("subscription coverage mismatch")
        query_values = dict(query_pairs)
        if scheme == "vless":
            query_values.setdefault("encryption", "none")
            query_values.pop("spx", None)
            if query_values.get("type") == "xhttp":
                if query_values.get("host") == "":
                    query_values.pop("host", None)
                try:
                    default_extra = json.loads(query_values.get("extra", ""))
                except (TypeError, json.JSONDecodeError):
                    default_extra = None
                if default_extra == {"mode": "auto"}:
                    query_values.pop("extra", None)
        path = urllib.parse.unquote(parsed.path)
        if scheme == "hysteria2":
            query_values.setdefault("alpn", "h3")
            query_values.setdefault("insecure", "0")
            query_values.setdefault("security", "tls")
            if path in {"", "/"}:
                path = ""
        query = tuple(sorted(query_values.items()))
        fragment = ""
        if not ignore_fragment:
            fragment = urllib.parse.unquote(parsed.fragment)
            if fragment in MAIN_SUBSCRIPTION_REMARKS:
                fragment = "agw-main"
            else:
                match = re.fullmatch(r"Aimili Gateway (agw-[A-Za-z0-9_-]+) VLESS", fragment)
                if match is not None:
                    fragment = match.group(1)
        return (
            scheme,
            urllib.parse.unquote(parsed.username or ""),
            urllib.parse.unquote(parsed.password or ""),
            parsed.hostname.lower(),
            parsed.port,
            path,
            query,
            fragment,
        )

    expected = {urllib.parse.urlsplit(str(material["publicUri"])).port: material for material in materials}
    actual = {urllib.parse.urlsplit(entry).port: entry for entry in entries}
    if None in expected or None in actual or len(expected) != len(materials) or len(actual) != len(entries) or set(actual) != set(expected):
        raise RuntimeError("subscription coverage mismatch")
    for port, material in expected.items():
        expected_alias = material.get("subscriptionAlias")
        if expected_alias is not None:
            actual_alias = urllib.parse.unquote(urllib.parse.urlsplit(actual[port]).fragment)
            if (
                isinstance(expected_alias, str)
                and type(material.get("slotNumber")) is int
                and material["slotNumber"] == 0
                and actual_alias == expected_alias + "-aimili-gateway-subscription"
            ):
                actual_alias = expected_alias
            if not isinstance(expected_alias, str) or actual_alias != expected_alias:
                raise RuntimeError("subscription coverage mismatch")
            if canonical(actual[port], ignore_fragment=True) != canonical(str(material["publicUri"]), ignore_fragment=True):
                raise RuntimeError("subscription coverage mismatch")
        elif canonical(actual[port]) != canonical(str(material["publicUri"])):
            raise RuntimeError("subscription coverage mismatch")
    return {
        "entryCount": len(entries),
        "hysteria2": sum(canonical(value)[0] == "hysteria2" for value in actual.values()),
        "vless": sum(canonical(value)[0] == "vless" for value in actual.values()),
    }


def bind_subscription_entries(
    materials: list[dict[str, str]], entries: list[str]
) -> list[dict[str, str]]:
    by_port = {urllib.parse.urlsplit(entry).port: entry for entry in entries}
    return [
        {**material, "publicUri": by_port[urllib.parse.urlsplit(str(material["publicUri"])).port]}
        for material in materials
    ]


def validate_switch_arguments(slot: int, mode: str) -> tuple[int, str]:
    if slot not in {0, 1, 2, 3}:
        raise ValueError("invalid slot")
    if mode not in PROTOCOL_MODES:
        raise ValueError("invalid protocol mode")
    return slot, mode


def build_public_client_config(
    uri: str, mode: str, local_port: int, ca_certificate_path: str = ""
) -> dict[str, object]:
    parsed = urllib.parse.urlsplit(uri)
    query = {key: values[0] for key, values in urllib.parse.parse_qs(parsed.query).items() if values}
    if not parsed.hostname or not parsed.port or not parsed.username or local_port < 1 or local_port > 65535 or mode not in PROTOCOL_MODES:
        raise RuntimeError("invalid public connection document")
    outbound: dict[str, object] = {"tag": "validation-public"}
    if mode in {"vless_tcp_reality_vision", "vless_xhttp_reality"}:
        required = {"fp", "sni", "pbk", "sid"}
        if parsed.scheme != "vless" or not required.issubset(query):
            raise RuntimeError("invalid VLESS connection document")
        flow = "xtls-rprx-vision"
        stream: dict[str, object] = {
            "network": "tcp",
            "security": "reality",
            "realitySettings": {
                "fingerprint": query["fp"],
                "serverName": query["sni"],
                "password": query["pbk"],
                "shortId": query["sid"],
                "spiderX": "/",
            },
        }
        if mode == "vless_xhttp_reality":
            path = query.get("path", "")
            if not path.startswith("/"):
                raise RuntimeError("invalid XHTTP connection document")
            flow = ""
            stream["network"] = "xhttp"
            stream["xhttpSettings"] = {"path": path, "mode": "auto"}
        outbound.update({
            "protocol": "vless",
            "settings": {"vnext": [{"address": parsed.hostname, "port": parsed.port, "users": [{"id": urllib.parse.unquote(parsed.username), "encryption": "none", "flow": flow}]}]},
            "streamSettings": stream,
        })
    else:
        if parsed.scheme not in {"hysteria2", "hy2"} or not query.get("sni"):
            raise RuntimeError("invalid Hysteria2 connection document")
        tls_settings: dict[str, object] = {
            "serverName": query["sni"],
            "allowInsecure": False,
            "fingerprint": "chrome",
        }
        if ca_certificate_path:
            tls_settings["disableSystemRoot"] = True
            tls_settings["certificates"] = [{
                "certificateFile": ca_certificate_path,
                "usage": "verify",
            }]
        outbound.update({
            "protocol": "hysteria",
            "settings": {"version": 2, "address": parsed.hostname, "port": parsed.port},
            "streamSettings": {
                "network": "hysteria",
                "security": "tls",
                "hysteriaSettings": {"version": 2, "auth": urllib.parse.unquote(parsed.username)},
                "tlsSettings": tls_settings,
            },
        })
    return {
        "log": {"loglevel": "none"},
        "inbounds": [{"tag": "validation-socks", "listen": "127.0.0.1", "port": local_port, "protocol": "socks", "settings": {"udp": False}}],
        "outbounds": [outbound],
        "routing": {"domainStrategy": "AsIs", "rules": [{"type": "field", "inboundTag": ["validation-socks"], "outboundTag": "validation-public"}]},
    }


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
                 public_socks_tcp: bool, authorized_socks: bool, public_protocol: bool) -> bool:
    socks_ok = authorized_socks and public_socks_tcp
    if not source_restriction_enabled:
        socks_ok = socks_ok and public_socks
    return socks_ok and public_protocol


def should_probe_public_socks(source_restriction_enabled: bool) -> bool:
    return not source_restriction_enabled


def select_materials(materials: list[dict[str, str]], index: int | None) -> list[dict[str, str]]:
    if index is None:
        return materials
    if index < 0 or index >= len(materials):
        raise ValueError("ready group index is out of range")
    return [materials[index]]


def verify_group(
    payload: dict[str, str], xray: str, probe_public_socks: bool = True,
    ca_certificate: str = "",
) -> dict[str, object]:
    expected = str(ipaddress.ip_address(payload["exitIp"]))
    socks = urllib.parse.urlparse(payload["socks5hUri"])
    proxy_environment = os.environ.copy()
    proxy_environment["ALL_PROXY"] = payload["socks5hUri"]
    proxy_environment["HTTP_PROXY"] = payload["socks5hUri"]
    proxy_environment["HTTPS_PROXY"] = payload["socks5hUri"]
    proxy_environment["NO_PROXY"] = ""
    socks_result = subprocess.CompletedProcess([], -1, "", "")
    if probe_public_socks:
        socks_result = subprocess.run(
            ["curl.exe", "-4", "-fsS", "--max-time", "25", EGRESS_PROBE_URL],
            capture_output=True, text=True, env=proxy_environment,
        )
        try:
            first_socks_response_is_ip = bool(
                ipaddress.ip_address(socks_result.stdout.strip())
            )
        except ValueError:
            first_socks_response_is_ip = False
        if (
            socks_result.returncode in {28, 35, 56}
            and not first_socks_response_is_ip
        ):
            socks_result = subprocess.run(
                ["curl.exe", "-4", "-fsS", "--max-time", "25", EGRESS_PROBE_URL],
                capture_output=True, text=True, env=proxy_environment,
            )
    try:
        socks_response_is_ip = bool(ipaddress.ip_address(socks_result.stdout.strip()))
    except ValueError:
        socks_response_is_ip = False
    socks_ok = socks_result.returncode == 0 and socks_response_is_ip and socks_result.stdout.strip() == expected

    public = urllib.parse.urlparse(payload["publicUri"])
    public_socks_tcp = tcp_reachable(str(socks.hostname), int(socks.port or 0))
    public_protocol_tcp = (
        tcp_reachable(str(public.hostname), int(public.port or 0))
        if payload["protocolMode"] != "hysteria2_quic_tls"
        else False
    )
    local_port = free_port()
    ca_handle = None
    if payload["protocolMode"] == "hysteria2_quic_tls" and ca_certificate:
        if (
            len(ca_certificate) > 65536
            or not ca_certificate.startswith("-----BEGIN CERTIFICATE-----\n")
            or not ca_certificate.rstrip().endswith("-----END CERTIFICATE-----")
        ):
            raise RuntimeError("invalid TLS CA certificate")
        ca_handle = tempfile.NamedTemporaryFile(
            mode="w", encoding="ascii", suffix=".crt", delete=False
        )
        ca_handle.write(ca_certificate)
        ca_handle.close()
        os.chmod(ca_handle.name, 0o600)
    config = build_public_client_config(
        payload["publicUri"], payload["protocolMode"], local_port,
        ca_certificate_path=ca_handle.name if ca_handle is not None else "",
    )
    handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", suffix=".json", delete=False)
    process: subprocess.Popen[str] | None = None
    result = subprocess.CompletedProcess([], 125, "", "not started")
    diagnostic = ""
    try:
        json.dump(config, handle, separators=(",", ":"))
        handle.close()
        os.chmod(handle.name, 0o600)
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
                 "--max-time", "25", EGRESS_PROBE_URL],
                capture_output=True, text=True,
            )
            try:
                first_response_is_ip = bool(ipaddress.ip_address(result.stdout.strip()))
            except ValueError:
                first_response_is_ip = False
            if (
                (public_protocol_tcp or payload["protocolMode"] == "hysteria2_quic_tls")
                and process.poll() is None
                and result.returncode in {28, 35, 56}
                and not first_response_is_ip
            ):
                result = subprocess.run(
                    ["curl.exe", "-4", "-fsS", "--socks5-hostname", f"127.0.0.1:{local_port}",
                     "--max-time", "25", EGRESS_PROBE_URL],
                    capture_output=True, text=True,
                )
            try:
                public_response_is_ip = bool(ipaddress.ip_address(result.stdout.strip()))
            except ValueError:
                public_response_is_ip = False
            public_ok = result.returncode == 0 and public_response_is_ip and result.stdout.strip() == expected
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
        if ca_handle is not None:
            try:
                os.unlink(ca_handle.name)
            except FileNotFoundError:
                pass
    public_error_category = "none"
    for category, markers in (
        ("timeout", ("timeout", "deadline exceeded")),
        ("reality_rejected", ("reality", "rejected")),
        ("connection_rejected", ("rejected", "reset by peer", "forcibly closed")),
        ("dns", ("failed to lookup", "no such host")),
        ("config", ("failed to load config", "unknown field", "failed to parse")),
    ):
        if any(marker in diagnostic for marker in markers):
            public_error_category = category
            break
    return {
        "external_socks5h": socks_ok, "external_socks5h_proxy_dns": socks_ok,
        "external_socks_tcp": public_socks_tcp, "socks_curl_exit": socks_result.returncode,
        "socks_response_is_ip": socks_response_is_ip, "external_public_protocol": public_ok,
        "external_public_tcp": public_protocol_tcp, "public_curl_exit": result.returncode,
        "public_response_is_ip": public_response_is_ip, "public_error_category": public_error_category,
        "protocol_mode": payload["protocolMode"],
    }


def run_remote(
    slot: int | None,
    mode: str,
    expected_old: str | None = None,
    *,
    ssh_target: str = "ny",
    ssh_options: list[str] | None = None,
) -> dict[str, object]:
    completed = subprocess.run(
        ["ssh", *(ssh_options or []), ssh_target, "sudo", "python3", "-", str(-1 if slot is None else slot), mode, expected_old or "any"], input=REMOTE_HELPER, text=True,
        capture_output=True, timeout=240, check=True,
    )
    remote = json.loads(completed.stdout)
    if not isinstance(remote, dict):
        raise RuntimeError("invalid remote payload")
    return remote


def inspect_remote_mode(
    slot: int, *, ssh_target: str = "ny", ssh_options: list[str] | None = None
) -> str:
    remote = run_remote(slot, "inspect", ssh_target=ssh_target, ssh_options=ssh_options)
    inspection = remote.get("inspection")
    if (
        not isinstance(inspection, dict)
        or inspection.get("slot") != slot
        or inspection.get("mode") not in PROTOCOL_MODES
    ):
        raise RuntimeError("invalid switch inspection")
    return str(inspection["mode"])


def evaluate_remote(
    remote: dict[str, object], index: int | None, xray: str,
    probe_public_socks: bool | None = None,
) -> tuple[dict[str, object], bool]:
    source_restriction_enabled = bool(remote.get("sourceRestrictionEnabled"))
    all_materials = require_ready_materials(remote.get("groups"))
    subscription_raw = base64.b64decode(str(remote.get("subscription", "")), validate=True)
    subscription_entries = decode_subscription(subscription_raw)
    coverage = validate_subscription_coverage(all_materials, subscription_entries)
    materials = select_materials(bind_subscription_entries(all_materials, subscription_entries), index)
    public_socks_probe = should_probe_public_socks(source_restriction_enabled) if probe_public_socks is None else probe_public_socks
    tls_ca = ""
    if remote.get("tlsCa"):
        try:
            tls_ca = base64.b64decode(str(remote["tlsCa"]), validate=True).decode("ascii")
        except (ValueError, UnicodeDecodeError) as error:
            raise RuntimeError("invalid TLS CA certificate") from error
    verified = [verify_group(material, xray, public_socks_probe, tls_ca) for material in materials]
    results = []
    for material, result in zip(materials, verified):
        result["slot_number"] = int(material.get("slotNumber") or 0)
        result["authorized_socks5h"] = bool(material.get("authorizedSocks5h"))
        results.append(result)
    exit_ips = [str(ipaddress.ip_address(material["exitIp"])) for material in all_materials]
    passed = all(group_passes(
        source_restriction_enabled=source_restriction_enabled,
        public_socks=bool(result["external_socks5h"]),
        public_socks_tcp=bool(result["external_socks_tcp"]),
        authorized_socks=bool(result["authorized_socks5h"]),
        public_protocol=bool(result["external_public_protocol"]),
    ) for result in results)
    expected_groups = int(remote.get("expectedGroups", 0))
    invariants = {
        "ready_groups": len(all_materials) == expected_groups,
        "mixed_count": int(remote.get("mixedCount", 0)) == expected_groups,
        "public_count": int(remote.get("publicCount", 0)) == expected_groups,
        "single_xray": int(remote.get("xrayPid", 0)) > 0,
        "subscription_entries": coverage["entryCount"] == expected_groups,
    }
    passed = passed and all(invariants.values()) and len(set(exit_ips)) == len(exit_ips)
    return ({
        "status": "pass" if passed else "failed",
        "ready_groups": len(all_materials),
        "verified_groups": len(results),
        "source_restriction_enabled": source_restriction_enabled,
        "unique_exit_ips": len(set(exit_ips)) == len(exit_ips),
        "all_public_socks5h": all(result["external_socks5h"] for result in results),
        "all_authorized_socks5h": all(result["authorized_socks5h"] for result in results),
        "all_external_public_protocol": all(result["external_public_protocol"] for result in results),
        "protocol_counts": {"vless": coverage["vless"], "hysteria2": coverage["hysteria2"]},
        "switch_requested": remote.get("switch") is not None,
        "invariants": invariants,
        "groups": results,
    }, passed)


def run_with_switch_rollback(slot, mode, inspect_mode, collect, evaluate):
    old_mode = inspect_mode(slot) if slot is not None else None
    switch_expected = old_mode is not None and old_mode != mode

    def rollback_and_verify() -> dict[str, str]:
        current_mode = inspect_mode(slot)
        if current_mode == old_mode:
            rollback_expected = old_mode
        elif current_mode == mode:
            rollback_expected = mode
        else:
            raise RollbackConflict("rollback conflict")
        restored, restored_ok = evaluate(collect(slot, old_mode, rollback_expected))
        if not restored_ok or restored.get("status") != "pass":
            raise RuntimeError("rollback verification failed")
        return {"status": "pass", "mode": old_mode}

    def rollback_or_raise() -> None:
        try:
            rollback_and_verify()
        except RollbackConflict:
            raise
        except BaseException as rollback_error:
            raise RuntimeError("rollback verification failed") from rollback_error

    try:
        remote = collect(slot, mode, old_mode)
    except BaseException:
        if switch_expected:
            rollback_or_raise()
        raise
    switch = remote.get("switch")
    if (switch_expected and switch is None) or (switch is not None and (
        not isinstance(switch, dict)
        or switch.get("slot") != slot
        or switch.get("oldMode") != old_mode
        or switch.get("newMode") != mode
    )):
        if switch_expected:
            rollback_or_raise()
        raise RuntimeError("invalid switch result")

    try:
        result, passed = evaluate(remote)
    except BaseException:
        if switch is not None:
            rollback_or_raise()
        raise
    if not passed and switch is not None:
        result = dict(result)
        result["rollback"] = rollback_and_verify()
    return result, passed


def main(
    index: int | None = None,
    slot: int | None = None,
    mode: str | None = None,
    xray: str = r"E:\SoftWare\v2rayN-windows-64\bin\xray\xray.exe",
    ssh_target: str = "ny",
    ssh_options: list[str] | None = None,
    probe_public_socks: bool | None = None,
) -> int:
    if (slot is None) != (mode is None):
        raise ValueError("slot and protocol mode must be provided together")
    if slot is not None and mode is not None:
        validate_switch_arguments(slot, mode)
    if slot is not None and index is not None:
        raise ValueError("index cannot limit a protocol switch verification")
    result, passed = run_with_switch_rollback(
        slot,
        mode,
        lambda requested_slot: inspect_remote_mode(requested_slot, ssh_target=ssh_target, ssh_options=ssh_options),
        lambda requested_slot, requested_mode, expected_old: run_remote(
            requested_slot, requested_mode or "none", expected_old,
            ssh_target=ssh_target, ssh_options=ssh_options,
        ),
        lambda remote: evaluate_remote(remote, index, xray, probe_public_socks),
    )
    print(json.dumps(result, sort_keys=True))
    return 0 if passed else 2


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--index", type=int)
    parser.add_argument("--slot", type=int)
    parser.add_argument("--set-mode", choices=sorted(PROTOCOL_MODES))
    parser.add_argument("--xray", default=r"E:\SoftWare\v2rayN-windows-64\bin\xray\xray.exe")
    parser.add_argument("--ssh-target", default="ny")
    parser.add_argument("--ssh-bind")
    parser.add_argument("--ssh-key")
    parser.add_argument("--known-hosts")
    parser.add_argument("--probe-public-socks", action="store_true")
    arguments = parser.parse_args()
    try:
        ssh_options = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=yes"]
        if arguments.ssh_bind:
            if ipaddress.ip_address(arguments.ssh_bind).version != 4:
                raise ValueError("invalid SSH bind address")
            ssh_options[0:0] = ["-b", arguments.ssh_bind]
        if arguments.ssh_key:
            ssh_options[0:0] = ["-i", arguments.ssh_key]
        if arguments.known_hosts:
            ssh_options.extend(["-o", "UserKnownHostsFile=" + arguments.known_hosts])
        raise SystemExit(main(arguments.index, arguments.slot, arguments.set_mode, arguments.xray, arguments.ssh_target, ssh_options, True if arguments.probe_public_socks else None))
    except Exception as error:
        category = safe_error_category(error)
        print(json.dumps({"status": "failed", "error_category": category}, sort_keys=True))
        raise SystemExit(2)
