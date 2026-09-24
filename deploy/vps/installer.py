#!/usr/bin/env python3
"""面向 Ubuntu 24.04 VPS 的可重入安装与域名切换入口。"""

from __future__ import annotations

import argparse
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import sqlite3
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request


ROOT = Path("/var/lib/aimili-gateway")
ETC = Path("/etc/aimili-gateway")
EGRESS = ROOT / "aimili-egress"
XUI_DB = Path("/etc/x-ui/x-ui.db")
LOG = Path("/var/log/aimili-gateway/install.log")
STATE = ROOT / "install-state.json"
REALITY_SNI = "reality.aimili.test"
STOCK_XUI_URL = "https://github.com/MHSanaei/3x-ui/releases/download/v3.7.0/x-ui-linux-amd64.tar.gz"
STOCK_XUI_SHA = "0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5"


class InstallError(Exception):
    pass


def say(message: str) -> None:
    print(message, flush=True)
    LOG.parent.mkdir(parents=True, exist_ok=True)
    with LOG.open("a", encoding="utf-8") as stream:
        stream.write(time.strftime("%Y-%m-%d %H:%M:%S ") + message + "\n")


def run(args: list[str], *, input_text: str | None = None, timeout: int = 900) -> str:
    try:
        result = subprocess.run(args, input=input_text, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        raise InstallError(f"命令超时：{args[0]}") from exc
    if result.returncode:
        with LOG.open("a", encoding="utf-8") as stream:
            stream.write(result.stdout[-4000:] + result.stderr[-4000:])
        raise InstallError(f"命令失败：{args[0]}，退出码 {result.returncode}；详见 {LOG}")
    return result.stdout.strip()


def write(path: Path, data: str | bytes, mode: int = 0o600, owner: tuple[int, int] | None = None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    body = data.encode() if isinstance(data, str) else data
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temporary, mode)
        if owner:
            os.chown(temporary, *owner)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def write_json(path: Path, value: dict, mode: int = 0o600, owner: tuple[int, int] | None = None) -> None:
    write(path, json.dumps(value, ensure_ascii=False, indent=2) + "\n", mode, owner)


def load_json(path: Path, default: dict | None = None) -> dict:
    if not path.exists():
        return {} if default is None else default
    with path.open(encoding="utf-8") as stream:
        return json.load(stream)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def copy_mode(source: Path, target: Path, mode: int = 0o755) -> None:
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_name("." + target.name + ".new")
    shutil.copyfile(source, temporary)
    os.chmod(temporary, mode)
    os.replace(temporary, target)


def wait_for(label: str, probe, seconds: int, interval: int = 3) -> None:
    deadline = time.monotonic() + seconds
    last_error = ""
    while time.monotonic() < deadline:
        try:
            if probe():
                return
        except Exception as exc:
            last_error = type(exc).__name__
        time.sleep(interval)
    raise InstallError(f"{label} 未在 {seconds} 秒内就绪；{last_error}；详见 {LOG}")


def prompt(text: str, default: str = "") -> str:
    with open("/dev/tty", "r+", encoding="utf-8") as terminal:
        terminal.write(text + (f" [{default}]" if default else "") + "：")
        terminal.flush()
        return terminal.readline().strip() or default


def args_from_user() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Aimili Gateway VPS 一键安装")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--domain", help="使用已有 DNS A 记录的域名")
    mode.add_argument("--no-domain", action="store_true", help="先通过服务器 IP 和内部证书部署")
    parser.add_argument("--slots", type=int, help="普通出口位数量，默认 4")
    parser.add_argument("--allowed-source", help="可连接 SOCKS5H mixed 端口的 IPv4 来源")
    parser.add_argument("--status", action="store_true", help="只读检查部署状态")
    parser.add_argument("--asset-root", type=Path, required=True)
    parser.add_argument("--xui-binary", type=Path, required=True)
    args = parser.parse_args()
    if args.status:
        return args
    if not args.domain and not args.no_domain:
        choice = prompt("选择部署模式：1 无域名；2 使用域名", "1")
        if choice == "1":
            args.no_domain = True
        elif choice == "2":
            args.domain = prompt("请输入已解析到本机的域名")
        else:
            raise InstallError("部署模式无效")
    return args


def public_ip() -> str:
    request = urllib.request.Request("https://api.ipify.org", headers={"User-Agent": "aimili-installer/1"})
    with urllib.request.urlopen(request, timeout=10) as response:
        value = response.read(64).decode().strip()
    if ipaddress.ip_address(value).version != 4:
        raise InstallError("无法取得本机公网 IPv4")
    return value


def desired(args: argparse.Namespace) -> tuple[str, str, int, str]:
    ip = public_ip()
    old = load_json(STATE)
    if args.domain:
        domain = args.domain.lower().strip().rstrip(".")
        if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+", domain):
            raise InstallError("域名格式无效")
        records = {item[4][0] for item in socket.getaddrinfo(domain, None, socket.AF_INET)}
        if ip not in records:
            raise InstallError(f"域名 A 记录尚未指向本机公网 IP {ip}")
        origin = "https://" + domain
    else:
        domain = ""
        origin = "https://" + ip
    slots = args.slots or old.get("slots") or 4
    if not 1 <= slots <= 8:
        raise InstallError("出口位数量须为 1–8")
    if old.get("slots") and old["slots"] != slots:
        raise InstallError("已有部署不能在域名切换时改变出口位数量")
    source = args.allowed_source or old.get("allowedSource") or os.environ.get("SSH_CLIENT", "").split(" ")[0]
    if not source:
        source = prompt("请输入允许访问 SOCKS5H 的 IPv4 来源")
    try:
        if ipaddress.ip_address(source).version != 4:
            raise ValueError()
    except ValueError as exc:
        raise InstallError("SOCKS5H 来源必须是有效 IPv4") from exc
    return origin, domain, slots, source


def backup_once(origin: str, slots: int) -> None:
    old = load_json(STATE)
    if old.get("status") == "working" and old.get("origin") == origin and old.get("backup"):
        return
    stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
    target = Path("/var/backups/aimili-gateway/vps-installer") / stamp
    target.mkdir(parents=True, mode=0o700, exist_ok=False)
    for path in (ETC / "config.json", Path("/etc/caddy/Caddyfile"), Path("/usr/local/bin/aimili-gateway"), Path("/usr/local/x-ui/x-ui")):
        if path.is_file():
            shutil.copy2(path, target / path.name)
    for path, name in ((ROOT / "aimili-gateway.db", "gateway.db"), (XUI_DB, "x-ui.db")):
        if path.is_file():
            source = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
            dest = sqlite3.connect(target / name)
            source.backup(dest)
            if dest.execute("PRAGMA quick_check").fetchone()[0] != "ok":
                raise InstallError("部署备份数据库校验失败")
            source.close()
            dest.close()
    write_json(STATE, {"status": "working", "origin": origin, "slots": slots, "backup": str(target), "phase": "backup"})


def checkpoint(phase: str, **extra) -> None:
    value = load_json(STATE)
    value.update(extra)
    value["phase"] = phase
    write_json(STATE, value)
    say(f"阶段完成：{phase}")


def preflight() -> None:
    if os.geteuid() != 0:
        raise InstallError("请使用 sudo")
    release = Path("/etc/os-release").read_text()
    if 'ID=ubuntu' not in release or 'VERSION_ID="24.04"' not in release:
        raise InstallError("仅支持 Ubuntu 24.04")
    if os.uname().machine != "x86_64" or not Path("/dev/net/tun").exists():
        raise InstallError("需要 x86_64 与 /dev/net/tun")
    if shutil.disk_usage("/").free < 2 << 30:
        raise InstallError("可用磁盘空间不足 2 GiB")


def system_packages() -> None:
    say("正在安装 Ubuntu 官方软件包与 Swap……")
    run(["apt-get", "update", "-qq"], timeout=600)
    run(["apt-get", "-o", "DPkg::Lock::Timeout=1800", "install", "-y", "-qq", "openvpn", "caddy", "ufw", "python3-requests", "python3-cryptography"], timeout=2400)
    if not shutil.which("openvpn") and not Path("/usr/sbin/openvpn").exists():
        raise InstallError("OpenVPN 安装后仍不可用")
    if Path("/proc/meminfo").read_text().split("SwapTotal:")[1].split()[0] == "0":
        swap = Path("/swapfile")
        if not swap.exists():
            run(["fallocate", "-l", "1G", str(swap)])
        os.chmod(swap, 0o600)
        run(["mkswap", str(swap)])
        run(["swapon", str(swap)])
        fstab = Path("/etc/fstab")
        if "/swapfile " not in fstab.read_text():
            with fstab.open("a") as stream:
                stream.write("/swapfile none swap sw 0 0\n")
    checkpoint("packages")


def download_stock_xui() -> Path:
    cache = Path("/var/cache/aimili-gateway/xui-v3.7.0")
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / "x-ui-linux-amd64.tar.gz"
    if not archive.exists() or sha256(archive) != STOCK_XUI_SHA:
        run(["curl", "-fL", "--retry", "3", "--connect-timeout", "10", "--max-time", "900", "-o", str(archive), STOCK_XUI_URL])
    if sha256(archive) != STOCK_XUI_SHA:
        raise InstallError("3x-ui 官方发布包摘要不匹配")
    extracted = cache / "stock"
    if not (extracted / "x-ui/bin/xray-linux-amd64").exists():
        extracted.mkdir(exist_ok=True)
        run(["tar", "-xzf", str(archive), "-C", str(extracted)])
    return extracted / "x-ui"


def install_xui(args: argparse.Namespace, credentials: dict) -> None:
    say("正在安装固定版本 3x-ui/Xray……")
    source = download_stock_xui()
    target = Path("/usr/local/x-ui")
    if not target.exists():
        shutil.copytree(source, target)
    if not (target / "bin/xray-linux-amd64").is_file():
        raise InstallError("Xray 运行资产缺失")
    if b"inboundAliases" not in args.xui_binary.read_bytes():
        raise InstallError("3x-ui 二进制缺少逐关联订阅别名 API")
    if not (target / "x-ui").exists() or sha256(target / "x-ui") != sha256(args.xui_binary):
        copy_mode(args.xui_binary, target / "x-ui")
    (ETC).mkdir(parents=True, exist_ok=True)
    if not XUI_DB.exists():
        run([str(target / "x-ui"), "setting", "-port", "2001", "-webBasePath", "/xui/", "-listenIP", "127.0.0.1"])
    with sqlite3.connect(XUI_DB) as db:
        db.execute("DELETE FROM settings WHERE key='subListen'")
        db.execute("INSERT INTO settings(key,value) VALUES('subListen','127.0.0.1')")
    unit = args.asset_root / "deploy/local-vm/native/x-ui.service.debian"
    copy_mode(unit, Path("/etc/systemd/system/x-ui.service"), 0o644)
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", "--now", "x-ui.service"])
    run(["systemctl", "restart", "x-ui.service"])
    wait_for("3x-ui 面板", lambda: http_ok("http://127.0.0.1:2001/xui/csrf-token"), 60)
    legacy = ETC / "xui-automation.json"
    if not legacy.exists():
        payload = {"baseUrl": "http://127.0.0.1:2001/xui/", "oldUsername": "admin", "oldPassword": "admin", "newUsername": credentials["username"], "newPassword": credentials["password"]}
        run(["python3", str(args.asset_root / "deploy/local-vm/native/rotate-xui-account.py")], input_text=json.dumps(payload), timeout=30)
        write_json(legacy, credentials)
    if "alias_override" not in {row[1] for row in sqlite3.connect(XUI_DB).execute("PRAGMA table_info(client_inbounds)")}:
        raise InstallError("3x-ui 数据库未完成 alias_override 迁移")
    checkpoint("xui")


def http_ok(url: str, cafile: str | None = None) -> bool:
    try:
        context = ssl.create_default_context(cafile=cafile) if url.startswith("https:") else None
        with urllib.request.urlopen(url, timeout=5, context=context) as response:
            return response.status == 200
    except Exception:
        return False


def install_egress(args: argparse.Namespace, slots: int, credentials: dict) -> None:
    say("正在初始化 aimili-egress 与 OpenVPN……")
    target = Path("/opt/aimili-gateway/services/aimili-egress")
    source = args.asset_root / "services/aimili-egress"
    target.mkdir(parents=True, exist_ok=True)
    files = list(source.glob("*.py"))
    unit_source = args.asset_root / "deploy/systemd/aimilivpn.service"
    unit_target = Path("/etc/systemd/system/aimilivpn.service")
    changed = any(not (target / path.name).exists() or sha256(path) != sha256(target / path.name) for path in files)
    changed = changed or not unit_target.exists() or sha256(unit_source) != sha256(unit_target)
    was_running = subprocess.run(["systemctl", "is-active", "--quiet", "aimilivpn.service"]).returncode == 0
    for path in files:
        shutil.copy2(path, target / path.name)
    EGRESS.mkdir(parents=True, exist_ok=True)
    os.chown(EGRESS, 0, 0)
    os.chmod(EGRESS, 0o700)
    control = Path("/etc/aimilivpn/control.token")
    if not control.exists():
        write(control, secrets.token_urlsafe(32) + "\n")
    ui = EGRESS / "ui_auth.json"
    if not ui.exists():
        write_json(ui, {"username": credentials["username"], "password": credentials["password"], "secret_path": secrets.token_urlsafe(12).replace("-", "_"), "host": "127.0.0.1", "port": 8787, "proxy_port": 7928, "routing_mode": "auto", "force_country": "", "routing_ip_type": "all", "connection_enabled": True, "fixed_node_id": "", "favorite_node_ids": [], "fav_fail_fallback": True, "exit_slot_active": list(range(slots)), "exit_slot_count": slots, "exit_slot_paused": []})
    else:
        active = load_json(ui).get("exit_slot_active")
        if active != list(range(slots)) and load_json(STATE).get("status") == "complete":
            raise InstallError(f"现有 egress 槽位 {active} 与目标 0–{slots-1} 不一致，安装器不会自动删除已交付的运行出口")
    write(Path("/etc/default/aimilivpn"), f"MULTI_EXIT_SLOTS={slots}\nMAX_EXIT_SLOTS=16\nTARGET_VALID_POOL_SIZE=64\nMAX_VALID_POOL_SIZE=150\nOPENVPN_TEST_CONCURRENCY=4\nMAIN_EGRESS_FAIL_THRESHOLD=3\nSLOT_EGRESS_FAIL_THRESHOLD=3\nCOLLECTOR_BUSY_RETRY_SECONDS=600\nUI_HOST=127.0.0.1\n")
    copy_mode(unit_source, unit_target, 0o644)
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", "--now", "aimilivpn.service"])
    if was_running and changed:
        say("aimili-egress 文件已变化，正在重启运行服务……")
        run(["systemctl", "restart", "aimilivpn.service"])
    wait_for("egress 控制接口", lambda: socket_open("127.0.0.1", 8790), 60)
    checkpoint("egress")


def socket_open(host: str, port: int) -> bool:
    try:
        with socket.create_connection((host, port), timeout=3):
            return True
    except OSError:
        return False


def control(path: str) -> dict | list:
    token = Path("/etc/aimilivpn/control.token").read_text().strip()
    request = urllib.request.Request("http://127.0.0.1:8790/control/v1/" + path, headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)["data"]


def wait_egress(slots: int) -> None:
    say("等待主出口和普通出口真实出网校验；官方候选探测可能需要几分钟……")
    def ready() -> bool:
        main = control("main")
        active = control("slots")
        return bool(main.get("active") and main.get("egress_ok") and all(any(s.get("slot") == number and s.get("egress_ok") for s in active) for number in range(slots)))
    wait_for("主连接与全部出口位", ready, 1200, 10)
    checkpoint("egress-ready")


def write_caddy(origin: str, domain: str) -> None:
    import grp
    host = origin.removeprefix("https://")
    secret = load_json(EGRESS / "ui_auth.json")["secret_path"]
    caddy = f"""{host} {{
{'' if domain else '    tls internal'}
    @xui path /xui/*
    handle @xui {{ reverse_proxy 127.0.0.1:2001 }}
    @sub path /sub/*
    handle @sub {{ reverse_proxy 127.0.0.1:2096 }}
    @egress path /vpngate/*
    handle @egress {{
        uri replace /vpngate /{secret}
        reverse_proxy 127.0.0.1:8787
    }}
    handle {{ reverse_proxy 127.0.0.1:9080 }}
}}

{REALITY_SNI} {{
    tls internal
    respond "OK" 200
}}
"""
    # Keep readable multi-line Caddyfile blocks; Caddy 2.6 rejects inline handle bodies.
    caddy = caddy.replace("handle @xui { reverse_proxy 127.0.0.1:2001 }", "handle @xui {\n        reverse_proxy 127.0.0.1:2001\n    }")
    caddy = caddy.replace("handle @sub { reverse_proxy 127.0.0.1:2096 }", "handle @sub {\n        reverse_proxy 127.0.0.1:2096\n    }")
    caddy = caddy.replace("handle { reverse_proxy 127.0.0.1:9080 }", "handle {\n        reverse_proxy 127.0.0.1:9080\n    }")
    target = Path("/etc/caddy/Caddyfile")
    write(target, caddy, 0o640, (0, grp.getgrnam("caddy").gr_gid))
    run(["caddy", "validate", "--config", str(target)])
    run(["systemctl", "enable", "--now", "caddy.service"])
    run(["systemctl", "restart", "caddy.service"])
    wait_for("Caddy HTTPS", lambda: socket_open("127.0.0.1", 443), 90)
    if not domain:
        ca = Path("/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt")
        wait_for("本地 CA", ca.is_file, 60)
        shutil.copy2(ca, "/usr/local/share/ca-certificates/aimili-gateway-local.crt")
        run(["update-ca-certificates"])
    else:
        wait_for("域名证书", lambda: http_ok(origin), 180)
    checkpoint("caddy")


def certificate(origin: str, domain: str) -> tuple[Path, Path]:
    if not domain:
        cert = ETC / "protocol-ip.crt"
        key = ETC / "protocol-ip.key"
        if not cert.exists() or not key.exists():
            run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "365", "-subj", "/CN=" + REALITY_SNI, "-addext", "subjectAltName=DNS:" + REALITY_SNI + ",IP:" + origin.removeprefix("https://"), "-keyout", str(key), "-out", str(cert)])
            os.chmod(key, 0o600)
        return cert, key
    root = Path("/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory") / domain
    key = root / (domain + ".key")
    leaf = root / (domain + ".crt")
    wait_for("Caddy 公网证书", lambda: key.is_file() and leaf.is_file(), 180)
    result = subprocess.run(["openssl", "s_client", "-connect", "127.0.0.1:443", "-servername", domain, "-showcerts"], input=b"", capture_output=True, timeout=15)
    chain = re.findall(rb"-----BEGIN CERTIFICATE-----\s+.*?-----END CERTIFICATE-----", result.stdout, re.S)
    if len(chain) < 2:
        raise InstallError("Caddy 未返回完整证书链")
    cert = ETC / "protocol-fullchain.crt"
    write(cert, b"\n".join(chain) + b"\n", 0o644)
    run(["openssl", "verify", "-CAfile", "/etc/ssl/certs/ca-certificates.crt", "-untrusted", str(cert), str(leaf)])
    return cert, key


def install_gateway(args: argparse.Namespace, origin: str, domain: str, slots: int, source: str, credentials: dict) -> None:
    say("正在安装 Gateway、账户工具和协议事务服务……")
    import pwd
    try:
        uid = pwd.getpwnam("aimili-gateway").pw_uid
        gid = pwd.getpwnam("aimili-gateway").pw_gid
    except KeyError:
        run(["useradd", "--system", "--home-dir", str(ROOT), "--shell", "/usr/sbin/nologin", "aimili-gateway"])
        uid = pwd.getpwnam("aimili-gateway").pw_uid
        gid = pwd.getpwnam("aimili-gateway").pw_gid
    ETC.mkdir(parents=True, exist_ok=True)
    os.chown(ETC, 0, gid)
    os.chmod(ETC, 0o750)
    ROOT.mkdir(parents=True, exist_ok=True)
    os.chown(ROOT, uid, gid)
    os.chmod(ROOT, 0o711)
    for path, mode, owner in ((ROOT / "ui", 0o700, (uid,gid)), (ROOT / "protocol-spool/requests", 0o700, (uid,gid)), (ROOT / "protocol-spool/results", 0o750, (0,gid)), (ROOT / "update-spool/requests", 0o700, (uid,gid)), (ROOT / "update-spool/results", 0o700, (uid,gid)), (Path("/var/lib/aimili-xui-protocol-transaction/transactions"), 0o700, (0,0)), (Path("/var/lib/aimili-xui-protocol-transaction/profiles"), 0o700, (0,0))):
        path.mkdir(parents=True, exist_ok=True)
        os.chown(path, *owner)
        os.chmod(path, mode)
    copy_mode(args.asset_root / "bin/aimili-gateway", Path("/usr/local/bin/aimili-gateway"))
    copy_mode(args.asset_root / "bin/aimili-gateway-admin", Path("/usr/local/bin/aimili-gateway-admin"))
    copy_mode(args.asset_root / "deploy/bin/aimili-gateway-account", Path("/usr/local/sbin/aimili-gateway-account"))
    copy_mode(args.asset_root / "deploy/bin/aimili-xui-protocol-transaction", Path("/usr/local/bin/aimili-xui-protocol-transaction"))
    copy_mode(args.asset_root / "scripts/aimili_xui_protocol_transaction.py", Path("/usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py"), 0o644)
    for name in ("aimili-xui-protocol-transaction.path", "aimili-xui-protocol-transaction.service", "aimili-xui-protocol-transaction.timer"):
        copy_mode(args.asset_root / "deploy/systemd" / name, Path("/etc/systemd/system") / name, 0o644)
    encrypted = Path("/etc/credstore.encrypted/aimili-gateway-master-key")
    db = ROOT / "aimili-gateway.db"
    if not encrypted.exists():
        if db.exists():
            raise InstallError("Gateway 数据库已存在但 master key 凭据缺失，拒绝更换身份")
        master = ETC / "master.key"
        if master.exists():
            if len(master.read_bytes()) != 32:
                raise InstallError("已有 Gateway master key 长度异常，拒绝覆盖")
        else:
            write(master, os.urandom(32), 0o400, (uid,gid))
        encrypted.parent.mkdir(parents=True, exist_ok=True)
        run(["systemd-creds", "encrypt", "--name=gateway-master-key", str(master), str(encrypted)])
        os.chmod(encrypted, 0o600)
    else:
        master = ETC / "master.key" if (ETC / "master.key").exists() else None
        if not db.exists():
            result = subprocess.run(["systemd-creds", "decrypt", "--name=gateway-master-key", str(encrypted), "-"], capture_output=True)
            if result.returncode or len(result.stdout) != 32:
                raise InstallError("无法恢复已有 Gateway master key，拒绝重建身份")
            if master is None:
                master = ETC / "master.key"
                write(master, result.stdout, 0o400, (uid,gid))
            elif master.read_bytes() != result.stdout:
                raise InstallError("明文与加密 master key 不一致，拒绝继续")
    cert, key = certificate(origin, domain)
    cfg = {"listenAddress":"127.0.0.1:9080","publicOrigin":origin,"realityServerName":REALITY_SNI,"databasePath":str(ROOT/"aimili-gateway.db"),"masterKeyFile":"/run/credentials/aimili-gateway.service/gateway-master-key","aimiliAddress":"127.0.0.1:8787","aimiliControlUrl":"http://127.0.0.1:8790/","aimiliControlTokenFile":"/run/credentials/aimili-gateway.service/aimili-control-token","xuiBaseUrl":"http://127.0.0.1:2001/xui/","xuiCredentialsFile":"/run/credentials/aimili-gateway.service/xui-automation","aimiliBackendUrl":"/vpngate/","maxProxyGroups":slots,"maxAimiliSlots":slots,"vlessPortStart":20000,"vlessPortEnd":20999,"mixedPortStart":30000,"mixedPortEnd":30999,"aggregateVlessPort":21000,"mainMixedPort":31000,"xrayPath":"/usr/local/x-ui/bin/xray-linux-amd64","probeHost":"api.ipify.org","mixedSourceCidrs":[source+"/32"],"expertModeUrl":"/xui/","protocolRequestDir":str(ROOT/"protocol-spool/requests"),"protocolResultDir":str(ROOT/"protocol-spool/results"),"protocolTimeoutSeconds":180,"externalUiRoot":str(ROOT/"ui")}
    if not db.exists():
        init = dict(cfg)
        init["masterKeyFile"] = str(master)
        init["aimiliControlTokenFile"] = "/etc/aimilivpn/control.token"
        init["xuiCredentialsFile"] = str(ETC/"xui-automation.json")
        path = ETC / "config-init.json"
        write_json(path, init, 0o400, (uid,gid))
        run(["runuser", "-u", "aimili-gateway", "--", "env", "GATEWAY_CONFIG="+str(path), "/usr/local/bin/aimili-gateway-admin", "init"], input_text=credentials["username"]+"\n"+credentials["password"]+"\n"+credentials["password"]+"\n")
        path.unlink()
        os.chown(db, uid, gid)
        os.chmod(db, 0o600)
    if master is not None:
        master.unlink()
    write_json(ETC / "config.json", cfg, 0o400, (uid,gid))
    proto = {"databasePath":str(XUI_DB),"snapshotDir":"/var/lib/aimili-xui-protocol-transaction/transactions","profileDir":"/var/lib/aimili-xui-protocol-transaction/profiles","runtimeConfigPath":"/usr/local/x-ui/bin/config.json","certificatePath":str(cert),"privateKeyPath":str(key),"tlsServerName":domain or REALITY_SNI,"spoolRequestDir":str(ROOT/"protocol-spool/requests"),"spoolResultDir":str(ROOT/"protocol-spool/results"),"xrayBinary":"/usr/local/x-ui/bin/xray-linux-amd64","apiServer":"127.0.0.1:62789","allowedPorts":[8443]+list(range(20000,20000+slots))}
    write_json(ETC / "protocol-transaction.json", proto, 0o640, (0,gid))
    copy_mode(args.asset_root / "deploy/systemd/aimili-gateway.service", Path("/etc/systemd/system/aimili-gateway.service"), 0o644)
    run(["systemctl", "daemon-reload"])
    for unit in ("aimili-xui-protocol-transaction.path", "aimili-xui-protocol-transaction.timer", "aimili-gateway.service"):
        run(["systemctl", "enable", "--now", unit])
    run(["systemctl", "restart", "aimili-gateway.service"])
    wait_for("Gateway 健康接口", lambda: http_ok("http://127.0.0.1:9080/healthz"), 90)
    checkpoint("gateway")


def firewall(slots: int, source: str) -> None:
    say("正在设置受管防火墙规则……")
    for port in (22, 80, 443, 8443, *range(20000, 20000+slots)):
        run(["ufw", "allow", f"{port}/tcp"])
    for port in (31000, *range(30000, 30000+slots)):
        run(["ufw", "allow", "from", source, "to", "any", "port", str(port), "proto", "tcp"])
    run(["ufw", "--force", "enable"])
    checkpoint("firewall")


def status() -> None:
    state = load_json(STATE)
    units = {name: subprocess.run(["systemctl", "is-active", "--quiet", name]).returncode == 0 for name in ("aimili-gateway", "aimilivpn", "x-ui", "caddy")}
    print(json.dumps({"state": state.get("status", "absent"), "phase": state.get("phase", ""), "origin": state.get("origin", ""), "slots": state.get("slots", 0), "services": units}, ensure_ascii=False))


def main() -> int:
    args = args_from_user()
    if args.status:
        status()
        return 0
    lock_path = Path("/run/aimili-gateway-vps-install.lock")
    with lock_path.open("w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise InstallError("另一部署进程正在运行") from exc
        preflight()
        origin, domain, slots, source = desired(args)
        current = load_json(STATE)
        backup_once(origin, slots)
        say(f"目标：{'域名 '+domain if domain else '公网 IP'}；普通出口位 {slots}；已有数据将保留。")
        system_packages()
        creds = Path("/root/aimili-gateway/credentials.json")
        if not creds.exists():
            credentials = {"username":"agw_"+secrets.token_hex(4), "password":secrets.token_urlsafe(24)}
            write_json(creds, credentials)
        else:
            credentials = load_json(creds)
        install_xui(args, credentials)
        install_egress(args, slots, credentials)
        if not current.get("origin"):
            wait_egress(slots)
        firewall(slots, source)
        write_caddy(origin, domain)
        install_gateway(args, origin, domain, slots, source, credentials)
        from importlib.util import module_from_spec, spec_from_file_location
        provision_path = args.asset_root / "deploy/vps/provision.py"
        spec = spec_from_file_location("aimili_vps_provision", provision_path)
        if spec is None or spec.loader is None:
            raise InstallError("provision 模块缺失")
        module = module_from_spec(spec)
        spec.loader.exec_module(module)
        try:
            if current.get("status") != "complete":
                if module.remove_unneeded_slots(origin, slots, credentials):
                    run(["systemctl", "restart", "aimilivpn.service"])
                module.stabilize_initial_slots(slots)
                wait_egress(slots)
            module.provision(origin, slots, credentials, source)
        except InstallError:
            raise
        except Exception as error:
            raise InstallError(f"Gateway 业务验收失败：{type(error).__name__}: {error}") from error
        checkpoint("verified", origin=origin, domain=domain, slots=slots, allowedSource=source, status="complete")
        say(f"安装成功：{origin}；账户信息位于 {creds}（仅 root 可读）。")
        return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except InstallError as error:
        say("安装未完成：" + str(error))
        raise SystemExit(5)
