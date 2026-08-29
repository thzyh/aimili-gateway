#!/usr/bin/env python3
"""在 VPS 上执行主连接与协议模式的脱敏安全门检查。"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import sqlite3
import subprocess
import sys
import tempfile
from collections.abc import Callable, Iterable, Mapping
from typing import Any


MIN_MEMORY_KIB = 160 * 1024
MIN_SWAP_FREE_KIB = 512 * 1024
EXPECTED_UDP_PORTS = (8443, 20000, 20001, 20002)
SECRET_KEYS = {
    "auth",
    "authorization",
    "cookie",
    "password",
    "privatekey",
    "private_key",
    "subscriptionurl",
    "subscription_url",
    "token",
    "uuid",
}
NORMALIZED_SECRET_KEYS = {
    key.replace("_", "").replace("-", "").lower() for key in SECRET_KEYS
}
UUID_PATTERN = re.compile(
    r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-"
    r"[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}\b"
)


def evaluate_resource_gate(values: Mapping[str, int]) -> dict[str, int | str]:
    memory = int(values.get("MemAvailable", 0))
    swap_free = int(values.get("SwapFree", 0))
    if memory < MIN_MEMORY_KIB:
        raise ValueError("memory_below_threshold")
    if swap_free < MIN_SWAP_FREE_KIB:
        raise ValueError("swap_below_threshold")
    return {
        "memoryMiB": memory // 1024,
        "swapFreeMiB": swap_free // 1024,
        "status": "pass",
    }


def parse_meminfo(contents: str) -> dict[str, int]:
    values: dict[str, int] = {}
    for line in contents.splitlines():
        match = re.fullmatch(r"([A-Za-z_]+):\s+(\d+)\s+kB", line.strip())
        if match:
            values[match.group(1)] = int(match.group(2))
    return values


def validate_udp_rules(rules: Iterable[str]) -> dict[str, object]:
    normalized = []
    for rule in rules:
        text = str(rule).strip().lower()
        match = re.fullmatch(r"(\d+)/udp", text)
        if not match:
            raise ValueError("udp_rules_invalid")
        normalized.append(int(match.group(1)))
    if sorted(normalized) != list(EXPECTED_UDP_PORTS) or len(normalized) != len(
        set(normalized)
    ):
        raise ValueError("udp_rules_invalid")
    return {"ports": list(EXPECTED_UDP_PORTS), "status": "pass"}


def fingerprint_unmanaged_resources(database_path: pathlib.Path) -> dict[str, object]:
    database = sqlite3.connect(f"file:{database_path}?mode=ro", uri=True)
    try:
        rows = database.execute(
            "SELECT id, tag, remark, protocol, port, settings, stream_settings, sniffing "
            "FROM inbounds ORDER BY id"
        ).fetchall()
    finally:
        database.close()
    unmanaged = []
    for row in rows:
        tag = str(row[1] or "")
        if tag == "aimili-reality" or tag.startswith("agw-"):
            continue
        unmanaged.append(list(row))
    encoded = json.dumps(
        unmanaged, ensure_ascii=True, separators=(",", ":"), sort_keys=False
    ).encode()
    return {"count": len(unmanaged), "sha256": hashlib.sha256(encoded).hexdigest()}


def safe_summary(value: Any) -> Any:
    if isinstance(value, Mapping):
        result = {}
        for key, item in value.items():
            if (
                str(key).replace("-", "").replace("_", "").lower()
                in NORMALIZED_SECRET_KEYS
            ):
                result[str(key)] = "[redacted]"
            else:
                result[str(key)] = safe_summary(item)
        return result
    if isinstance(value, list):
        return [safe_summary(item) for item in value]
    if isinstance(value, tuple):
        return [safe_summary(item) for item in value]
    if isinstance(value, str):
        return UUID_PATTERN.sub("[redacted]", value)
    return value


def verify_hysteria_certificate(
    certificate_path: pathlib.Path,
    private_key_path: pathlib.Path,
    server_name: str,
    xray_path: pathlib.Path,
    *,
    runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, str]:
    paths = (certificate_path, private_key_path, xray_path)
    if (
        not server_name
        or any(path.is_symlink() or not path.is_file() for path in paths)
        or not os.access(certificate_path, os.R_OK)
        or not os.access(private_key_path, os.R_OK)
    ):
        raise ValueError("certificate_invalid")
    commands = (
        ["openssl", "x509", "-checkend", "604800", "-noout", "-in", str(certificate_path)],
        ["openssl", "x509", "-in", str(certificate_path), "-noout", "-checkhost", server_name],
    )
    for command in commands:
        runner(command, capture_output=True, text=True, check=True, timeout=15)
    config = {
        "log": {"loglevel": "none"},
        "inbounds": [
            {
                "listen": "127.0.0.1",
                "port": 20002,
                "protocol": "hysteria",
                "settings": {
                    "version": 2,
                    "clients": [{"auth": "offline-validation-placeholder"}],
                },
                "streamSettings": {
                    "network": "hysteria",
                    "security": "tls",
                    "hysteriaSettings": {"version": 2, "udpIdleTimeout": 60},
                    "tlsSettings": {
                        "serverName": server_name,
                        "minVersion": "1.2",
                        "maxVersion": "1.3",
                        "alpn": ["h3"],
                        "certificates": [
                            {
                                "certificateFile": str(certificate_path),
                                "keyFile": str(private_key_path),
                                "oneTimeLoading": False,
                                "usage": "encipherment",
                            }
                        ],
                    },
                },
            }
        ],
        "outbounds": [{"protocol": "freedom"}],
    }
    descriptor, temporary_path = tempfile.mkstemp(prefix="aimili-hysteria-test-", suffix=".json")
    try:
        if os.name != "nt":
            os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            descriptor = -1
            json.dump(config, output, separators=(",", ":"))
        runner(
            [str(xray_path), "run", "-test", "-config", temporary_path],
            capture_output=True,
            text=True,
            check=True,
            timeout=30,
        )
    finally:
        pathlib.Path(temporary_path).unlink(missing_ok=True)
        if descriptor >= 0:
            os.close(descriptor)
    return {"status": "pass", "xrayConfig": "valid"}


def verify_runtime_continuity(
    before_pid: int,
    after_pid: int,
    before_probes: Mapping[str, object],
    after_probes: Mapping[str, object],
) -> dict[str, object]:
    if before_pid <= 0 or after_pid != before_pid:
        raise ValueError("xray_pid_changed")
    if before_probes != after_probes or any(
        not isinstance(value, Mapping) or value.get("connected") is not True
        for value in after_probes.values()
    ):
        raise ValueError("non_target_probe_changed")
    return {
        "nonTargetProbes": len(after_probes),
        "status": "pass",
        "xrayPidChanged": False,
    }


def evaluate_observation(
    samples: Iterable[Mapping[str, object]], *, baseline_swap_used_mib: int
) -> dict[str, int | str]:
    ordered = sorted(samples, key=lambda item: int(item["elapsed"]))
    if not ordered:
        raise ValueError("duration_below_threshold")
    duration = int(ordered[-1]["elapsed"]) - int(ordered[0]["elapsed"])
    if duration < 300:
        raise ValueError("duration_below_threshold")
    if any(bool(item.get("oom")) for item in ordered):
        raise ValueError("oom_detected")
    peak_rss = max(int(item["xrayRssMiB"]) for item in ordered)
    if peak_rss > 96:
        raise ValueError("xray_rss_above_threshold")
    swap_delta = max(int(item["swapUsedMiB"]) for item in ordered) - int(
        baseline_swap_used_mib
    )
    if swap_delta > 128:
        raise ValueError("swap_growth_above_threshold")
    low_started: int | None = None
    for sample in ordered:
        elapsed = int(sample["elapsed"])
        if int(sample["memoryAvailableMiB"]) < 96:
            if low_started is None:
                low_started = elapsed
            if elapsed - low_started >= 30:
                raise ValueError("memory_below_runtime_threshold")
        else:
            low_started = None
    return {
        "durationSeconds": duration,
        "status": "pass",
        "xrayRssPeakMiB": peak_rss,
        "swapDeltaMiB": swap_delta,
    }


def parse_ufw_udp_rules(contents: str) -> list[str]:
    rules = set()
    for line in contents.splitlines():
        first = line.strip().split(maxsplit=1)[0] if line.strip() else ""
        if not first.lower().endswith("/udp"):
            if "/udp" in first.lower():
                raise ValueError("udp_rules_invalid")
            continue
        if not re.fullmatch(r"[0-9]+/udp", first.lower()):
            raise ValueError("udp_rules_invalid")
        rules.add(first.lower())
    return sorted(rules, key=lambda value: int(value.split("/", 1)[0]))


def _read_ufw_udp_rules() -> list[str]:
    completed = subprocess.run(
        ["ufw", "status"], capture_output=True, text=True, check=True, timeout=15
    )
    return parse_ufw_udp_rules(completed.stdout)


def _protocol_config_paths(path: pathlib.Path) -> tuple[pathlib.Path, pathlib.Path, str, pathlib.Path]:
    document = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise ValueError("protocol_config_invalid")
    return (
        pathlib.Path(str(document["certificatePath"])),
        pathlib.Path(str(document["privateKeyPath"])),
        str(document["tlsServerName"]),
        pathlib.Path(str(document["xrayBinary"])),
    )


def _read_json(path: pathlib.Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def _preflight(args: argparse.Namespace) -> dict[str, object]:
    meminfo = parse_meminfo(pathlib.Path(args.meminfo).read_text(encoding="utf-8"))
    resource = evaluate_resource_gate(meminfo)
    udp_rules: dict[str, object]
    if args.require_udp:
        udp_rules = validate_udp_rules(
            pathlib.Path(args.udp_rules).read_text(encoding="utf-8").splitlines()
            if args.udp_rules
            else _read_ufw_udp_rules()
        )
    else:
        udp_rules = {"status": "not_required"}
    unmanaged = fingerprint_unmanaged_resources(pathlib.Path(args.xui_db))
    certificate, private_key, server_name, xray = _protocol_config_paths(
        pathlib.Path(args.protocol_config)
    )
    certificate_gate = verify_hysteria_certificate(
        certificate, private_key, server_name, xray
    )
    return {
        "certificateGate": certificate_gate,
        "resourceGate": resource,
        "udpRules": udp_rules,
        "unmanagedResources": unmanaged,
        "status": "pass",
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="action", required=True)
    preflight = subparsers.add_parser("preflight")
    preflight.add_argument("--meminfo", default="/proc/meminfo")
    preflight.add_argument("--udp-rules")
    preflight.add_argument("--xui-db", default="/etc/x-ui/x-ui.db")
    preflight.add_argument(
        "--protocol-config", default="/etc/aimili-gateway/protocol-transaction.json"
    )
    preflight.add_argument("--require-udp", action="store_true")
    fingerprint = subparsers.add_parser("fingerprint")
    fingerprint.add_argument("--xui-db", default="/etc/x-ui/x-ui.db")
    continuity = subparsers.add_parser("continuity")
    continuity.add_argument("--before-pid", required=True, type=int)
    continuity.add_argument("--after-pid", required=True, type=int)
    continuity.add_argument("--before-probes", required=True)
    continuity.add_argument("--after-probes", required=True)
    observation = subparsers.add_parser("observation")
    observation.add_argument("--samples", required=True)
    observation.add_argument("--baseline-swap-used-mib", required=True, type=int)
    args = parser.parse_args(argv)
    try:
        if args.action == "preflight":
            result = _preflight(args)
        elif args.action == "fingerprint":
            result = fingerprint_unmanaged_resources(pathlib.Path(args.xui_db))
        elif args.action == "continuity":
            result = verify_runtime_continuity(
                args.before_pid,
                args.after_pid,
                _read_json(pathlib.Path(args.before_probes)),
                _read_json(pathlib.Path(args.after_probes)),
            )
        elif args.action == "observation":
            result = evaluate_observation(
                _read_json(pathlib.Path(args.samples)),
                baseline_swap_used_mib=args.baseline_swap_used_mib,
            )
        else:  # pragma: no cover - argparse 保证闭集。
            raise ValueError("invalid_action")
        print(json.dumps(safe_summary(result), sort_keys=True, separators=(",", ":")))
        return 0
    except (
        KeyError,
        OSError,
        TypeError,
        sqlite3.Error,
        subprocess.SubprocessError,
        ValueError,
    ):
        print(json.dumps({"status": "failed", "errorCode": "preflight_failed"}))
        return 1


if __name__ == "__main__":
    sys.exit(main())
