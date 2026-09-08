#!/usr/bin/env python3
import argparse
import base64
import ipaddress
import json
import os
import pathlib
import re
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid


def valid_host_safety(value):
    try:
        for snapshot in (value["before"], value["after"]):
            if not all(isinstance(pid, int) and pid >= 0 for pid in snapshot["clientPids"]):
                return False
            if not all(re.fullmatch(r"[0-9a-f]{64}", str(snapshot[key])) for key in ("proxy", "defaultRoute")):
                return False
        return True
    except (KeyError, TypeError):
        return False


def load_gateway_state(database):
    query = """
        SELECT 'main', m.public_port, m.mixed_port, p.active_mode
          FROM main_egress m JOIN egress_protocol_modes p ON p.egress_id=m.resource_name
         WHERE m.resource_name='agw-main' AND m.enabled=1 AND p.state='ready'
        UNION ALL
        SELECT 'slot-' || g.aimili_slot, g.public_port, g.mixed_port, p.active_mode
          FROM proxy_groups g JOIN egress_protocol_modes p ON p.egress_id=g.resource_name
         WHERE g.status='ready' AND p.state='ready'
         ORDER BY 1
    """
    with sqlite3.connect(f"file:{database}?mode=ro", uri=True) as connection:
        exits = [(str(exit_id), int(public), int(mixed), str(protocol)) for exit_id, public, mixed, protocol in connection.execute(query)]
        subscription = connection.execute(
            "SELECT subscription_id FROM gateway_subscription WHERE id=1 AND resource_name='aimili-gateway-subscription'"
        ).fetchone()
    if not subscription or not isinstance(subscription[0], str) or not re.fullmatch(r"[A-Za-z0-9._~-]{1,256}", subscription[0]):
        raise SystemExit("subscription_identity_invalid")
    return exits, subscription[0]


def decode_subscription(raw):
    try:
        text = raw.decode("utf-8").strip()
    except UnicodeDecodeError:
        raise SystemExit("subscription_document_invalid")
    if not text:
        raise SystemExit("subscription_document_invalid")
    if not text.startswith(("vless://", "hysteria2://", "hy2://")):
        compact = "".join(text.split()).encode("ascii", "strict")
        try:
            text = base64.b64decode(compact + b"=" * (-len(compact) % 4), altchars=b"-_", validate=True).decode("utf-8")
        except (ValueError, UnicodeDecodeError):
            raise SystemExit("subscription_document_invalid")
    entries = [line.strip() for line in text.splitlines() if line.strip()]
    if not entries:
        raise SystemExit("subscription_document_invalid")
    return entries


def observed_subscription(manifest, subscription_id, public_host):
    try:
        port = int(manifest["ports"]["xuiSubscription"])
    except (KeyError, TypeError, ValueError):
        raise SystemExit("subscription_endpoint_invalid")
    if not 0 < port <= 65535:
        raise SystemExit("subscription_endpoint_invalid")
    try:
        if ipaddress.ip_address(public_host).version != 4:
            raise ValueError
    except ValueError:
        raise SystemExit("subscription_public_host_invalid")
    path = "/sub/" + urllib.parse.quote(subscription_id, safe="")
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}",
        headers={"Accept": "text/plain", "User-Agent": "v2rayN/7", "Host": public_host},
    )
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            raw = response.read((1 << 20) + 1)
    except (OSError, urllib.error.URLError):
        raise SystemExit("subscription_fetch_failed")
    if len(raw) > 1 << 20:
        raise SystemExit("subscription_document_invalid")
    observed = []
    for entry in decode_subscription(raw):
        try:
            parsed = urllib.parse.urlsplit(entry)
            port = int(parsed.port or 0)
            pairs = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True, strict_parsing=True)
        except (UnicodeError, ValueError):
            raise SystemExit("subscription_document_invalid")
        if parsed.hostname != public_host or parsed.password is not None or not parsed.fragment or not 0 < port <= 65535 or len({key for key, _ in pairs}) != len(pairs):
            raise SystemExit("subscription_document_invalid")
        query = dict(pairs)
        if parsed.scheme == "vless" and query.get("security") == "reality" and query.get("type") == "xhttp":
            protocol = "vless_xhttp_reality"
        elif parsed.scheme == "vless" and query.get("security") == "reality" and query.get("type") == "tcp" and query.get("flow") == "xtls-rprx-vision":
            protocol = "vless_tcp_reality_vision"
        elif parsed.scheme in ("hysteria2", "hy2"):
            protocol = "hysteria2_quic_tls"
        else:
            raise SystemExit("subscription_protocol_invalid")
        allowed_query = {
            "vless_tcp_reality_vision": {"encryption", "flow", "fp", "pbk", "pqv", "security", "sid", "sni", "spx", "type"},
            "vless_xhttp_reality": {"encryption", "extra", "fp", "host", "mode", "path", "pbk", "pqv", "security", "sid", "sni", "spx", "type"},
            "hysteria2_quic_tls": {"alpn", "insecure", "security", "sni"},
        }[protocol]
        if set(query) - allowed_query:
            raise SystemExit("subscription_protocol_invalid")
        if protocol.startswith("vless_"):
            try:
                identity = str(uuid.UUID(urllib.parse.unquote(parsed.username or "")))
            except ValueError:
                raise SystemExit("subscription_identity_invalid")
            if query.get("encryption", "none") != "none" or query.get("fp") != "chrome" or not all(query.get(key) for key in ("pbk", "sid", "sni")):
                raise SystemExit("subscription_protocol_invalid")
            if "pqv" in query and not query["pqv"]:
                raise SystemExit("subscription_protocol_invalid")
            if "spx" in query and (not query["spx"].startswith("/") or any(ord(character) < 32 or ord(character) == 127 for character in query["spx"])):
                raise SystemExit("subscription_protocol_invalid")
            if protocol == "vless_xhttp_reality" and (not query.get("path") or query.get("mode") != "auto" or query.get("flow", "")):
                raise SystemExit("subscription_protocol_invalid")
            if protocol == "vless_xhttp_reality" and (query.get("host", "") or ("extra" in query and json.loads(query["extra"]) != {"mode": "auto"})):
                raise SystemExit("subscription_protocol_invalid")
        else:
            identity = urllib.parse.unquote(parsed.username or "")
            if not identity or query.get("insecure", "0") != "0" or not query.get("sni") or query.get("alpn", "h3") != "h3" or query.get("security", "tls") != "tls":
                raise SystemExit("subscription_protocol_invalid")
        observed.append({"port": port, "protocol": protocol, "identity": identity, "query": query, "alias": urllib.parse.unquote(parsed.fragment)})
    if len({item["alias"] for item in observed}) != len(observed):
        raise SystemExit("subscription_alias_invalid")
    return observed


def validate_xui_runtime(database, by_id, observed_by_port):
    expected = {}
    for exit_id, (public, mixed, protocol) in by_id.items():
        expected[public] = (exit_id, "public", protocol)
        expected[mixed] = (exit_id, "mixed", "socks5h")
    placeholders = ",".join("?" for _ in expected)
    with sqlite3.connect(f"file:{database}?mode=ro", uri=True) as connection:
        rows = connection.execute(
            f"SELECT port, protocol, settings, stream_settings FROM inbounds WHERE enable=1 AND port IN ({placeholders})",
            tuple(expected),
        ).fetchall()
    if len(rows) != len(expected) or len({int(row[0]) for row in rows}) != len(expected):
        raise SystemExit("xui_runtime_coverage_invalid")
    for raw_port, inbound_protocol, raw_settings, raw_stream in rows:
        port = int(raw_port)
        _, kind, expected_protocol = expected.get(port, (None, None, None))
        try:
            settings = json.loads(raw_settings or "{}")
            stream = json.loads(raw_stream or "{}")
        except (TypeError, json.JSONDecodeError):
            raise SystemExit("xui_runtime_protocol_invalid")
        if kind == "mixed":
            valid = inbound_protocol == "mixed"
        else:
            observed = observed_by_port.get(port, {})
            query = observed.get("query", {})
            clients = settings.get("clients", [])
            matching = [client for client in clients if isinstance(client, dict) and client.get("email") == "aimili-gateway-subscription" and client.get("enable", True) is True]
            if expected_protocol.startswith("vless_"):
                matching = [client for client in matching if client.get("id") == observed.get("identity")]
                reality = stream.get("realitySettings", {})
                reality_client = reality.get("settings", {})
                common = inbound_protocol == "vless" and stream.get("security") == "reality" and len(matching) == 1 and reality.get("serverNames") == [query.get("sni")] and query.get("sid") in reality.get("shortIds", []) and reality_client.get("publicKey") == query.get("pbk")
                if expected_protocol == "vless_tcp_reality_vision":
                    valid = common and stream.get("network") == "tcp" and matching[0].get("flow") == "xtls-rprx-vision"
                else:
                    valid = common and stream.get("network") == "xhttp" and matching[0].get("flow", "") == "" and stream.get("xhttpSettings", {}).get("path") == query.get("path")
            else:
                matching = [client for client in matching if client.get("auth") == observed.get("identity")]
                valid = inbound_protocol == "hysteria" and settings.get("version") == 2 and len(matching) == 1 and stream.get("network") == "hysteria" and stream.get("security") == "tls" and stream.get("tlsSettings", {}).get("serverName") == query.get("sni")
        if not valid:
            raise SystemExit("xui_runtime_protocol_invalid")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--gateway-db", required=True)
    parser.add_argument("--xui-db", required=True)
    parser.add_argument("--public-host", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    manifest = json.load(open(args.manifest, encoding="utf-8"))
    host_safety = json.load(sys.stdin)
    if not valid_host_safety(host_safety):
        raise SystemExit("host_safety_invalid")
    exits, subscription_id = load_gateway_state(args.gateway_db)
    expected_ids = ["main"] + [f"slot-{index}" for index in range(int(manifest["expected"]["exitSlots"]))]
    by_id = {exit_id: (public, mixed, protocol) for exit_id, public, mixed, protocol in exits}
    allowed_protocols = {"vless_tcp_reality_vision", "vless_xhttp_reality", "hysteria2_quic_tls"}
    if sorted(by_id) != sorted(expected_ids) or len(by_id) != len(exits):
        raise SystemExit("evidence_exit_set_invalid")
    if any(not 0 < public <= 65535 or not 0 < mixed <= 65535 or public == mixed or protocol not in allowed_protocols for public, mixed, protocol in by_id.values()):
        raise SystemExit("evidence_protocol_state_invalid")
    public_ports = {value[0] for value in by_id.values()}
    mixed_ports = {value[1] for value in by_id.values()}
    if len(public_ports) != len(by_id) or len(mixed_ports) != len(by_id) or public_ports & mixed_ports:
        raise SystemExit("evidence_port_isolation_invalid")
    observed = observed_subscription(manifest, subscription_id, args.public_host)
    observed_by_port = {item["port"]: item for item in observed}
    if len(observed_by_port) != len(observed) or set(observed_by_port) != public_ports:
        raise SystemExit("subscription_coverage_invalid")
    if any(observed_by_port[public]["protocol"] != protocol for public, _, protocol in by_id.values()):
        raise SystemExit("subscription_protocol_invalid")
    validate_xui_runtime(args.xui_db, by_id, observed_by_port)
    evidence = {
        "schemaVersion": 1,
        "subscription": {"entries": [{"exit": exit_id, "port": by_id[exit_id][0], "protocol": by_id[exit_id][2]} for exit_id in expected_ids]},
        "protocolIsolation": {"exits": [{"exit": exit_id, "publicPort": by_id[exit_id][0], "publicProtocol": by_id[exit_id][2], "mixedPort": by_id[exit_id][1], "mixedProtocol": "socks5h", "mixedInSubscription": False} for exit_id in expected_ids]},
        "hostSafety": host_safety,
    }
    output = pathlib.Path(args.output)
    output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = output.with_name(output.name + f".tmp.{os.getpid()}")
    temporary.write_text(json.dumps(evidence, separators=(",", ":")), encoding="utf-8")
    os.chmod(temporary, 0o600)
    os.replace(temporary, output)


if __name__ == "__main__":
    main()
