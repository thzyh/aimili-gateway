#!/usr/bin/env python3
"""3x-ui/Xray 单入站协议热切换事务助手。

该模块只接受封闭的逻辑出口请求。连接秘密只会从 root-owned 配置、
3x-ui SQLite 和 root-only 事务快照读取，不会进入结果或异常文本。
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import pathlib
import re
import secrets
import shutil
import sqlite3
import subprocess
import sys
import time
from contextlib import closing
from dataclasses import dataclass
from typing import Any, Callable, Iterable


VLESS_TCP_REALITY_VISION = "vless_tcp_reality_vision"
VLESS_XHTTP_REALITY = "vless_xhttp_reality"
HYSTERIA2_QUIC_TLS = "hysteria2_quic_tls"
PROTOCOL_MODES = {
    VLESS_TCP_REALITY_VISION,
    VLESS_XHTTP_REALITY,
    HYSTERIA2_QUIC_TLS,
}
REQUEST_FIELDS = {
    "operationId",
    "egressId",
    "inboundId",
    "inboundTag",
    "port",
    "oldMode",
    "newMode",
    "expectedFingerprint",
}
FINGERPRINT_FIELDS = REQUEST_FIELDS - {"expectedFingerprint"}
SAFE_OPERATION_ID = re.compile(r"^[A-Za-z0-9_-]{8,128}$")
SAFE_EGRESS_ID = re.compile(r"^agw-[a-z0-9][a-z0-9_-]{0,95}$")
SAFE_HEX_256 = re.compile(r"^[0-9a-f]{64}$")
MUTATED_INBOUND_COLUMNS = (
    "protocol",
    "settings",
    "stream_settings",
    "sniffing",
    "disable_flow",
)


class TransactionError(Exception):
    """只携带稳定、脱敏错误码的预期失败。"""

    def __init__(self, code: str):
        self.code = code if re.fullmatch(r"[a-z0-9_]{1,64}", code or "") else "transaction_failed"
        super().__init__(self.code)


class SimulatedCrash(BaseException):
    """仅用于故障注入，模拟进程在未执行异常恢复前终止。"""


@dataclass(frozen=True)
class ProtocolTransactionConfig:
    database_path: pathlib.Path
    snapshot_dir: pathlib.Path
    runtime_config_path: pathlib.Path
    certificate_path: pathlib.Path
    private_key_path: pathlib.Path
    xray_binary: pathlib.Path
    api_server: str
    allowed_ports: tuple[int, ...] = (8443, 20000, 20001, 20002)
    profile_dir: pathlib.Path | None = None
    tls_server_name: str = ""
    spool_request_dir: pathlib.Path | None = None
    spool_result_dir: pathlib.Path | None = None

    def __post_init__(self) -> None:
        for name in (
            "database_path",
            "snapshot_dir",
            "runtime_config_path",
            "certificate_path",
            "private_key_path",
            "xray_binary",
        ):
            object.__setattr__(self, name, pathlib.Path(getattr(self, name)))
        object.__setattr__(self, "allowed_ports", tuple(int(port) for port in self.allowed_ports))
        profile = self.profile_dir or (self.snapshot_dir.parent / "profiles")
        object.__setattr__(self, "profile_dir", pathlib.Path(profile))
        if self.spool_request_dir is not None:
            object.__setattr__(self, "spool_request_dir", pathlib.Path(self.spool_request_dir))
        if self.spool_result_dir is not None:
            object.__setattr__(self, "spool_result_dir", pathlib.Path(self.spool_result_dir))

    def as_dict(self) -> dict[str, Any]:
        return {
            "database_path": self.database_path,
            "snapshot_dir": self.snapshot_dir,
            "runtime_config_path": self.runtime_config_path,
            "certificate_path": self.certificate_path,
            "private_key_path": self.private_key_path,
            "xray_binary": self.xray_binary,
            "api_server": self.api_server,
            "allowed_ports": self.allowed_ports,
            "profile_dir": self.profile_dir,
            "tls_server_name": self.tls_server_name,
            "spool_request_dir": self.spool_request_dir,
            "spool_result_dir": self.spool_result_dir,
        }

    @classmethod
    def from_json(cls, document: dict[str, Any]) -> "ProtocolTransactionConfig":
        allowed = {
            "databasePath",
            "snapshotDir",
            "runtimeConfigPath",
            "certificatePath",
            "privateKeyPath",
            "xrayBinary",
            "apiServer",
            "allowedPorts",
            "profileDir",
            "tlsServerName",
            "spoolRequestDir",
            "spoolResultDir",
        }
        required = allowed - {"profileDir", "tlsServerName", "spoolRequestDir", "spoolResultDir"}
        if not isinstance(document, dict) or set(document) - allowed or not required.issubset(document):
            raise TransactionError("invalid_config")
        return cls(
            database_path=pathlib.Path(document["databasePath"]),
            snapshot_dir=pathlib.Path(document["snapshotDir"]),
            runtime_config_path=pathlib.Path(document["runtimeConfigPath"]),
            certificate_path=pathlib.Path(document["certificatePath"]),
            private_key_path=pathlib.Path(document["privateKeyPath"]),
            xray_binary=pathlib.Path(document["xrayBinary"]),
            api_server=str(document["apiServer"]),
            allowed_ports=tuple(document["allowedPorts"]),
            profile_dir=pathlib.Path(document["profileDir"]) if document.get("profileDir") else None,
            tls_server_name=str(document.get("tlsServerName", "")),
            spool_request_dir=pathlib.Path(document["spoolRequestDir"]) if document.get("spoolRequestDir") else None,
            spool_result_dir=pathlib.Path(document["spoolResultDir"]) if document.get("spoolResultDir") else None,
        )


def request_fingerprint(request: dict[str, Any]) -> str:
    material = {key: request[key] for key in sorted(FINGERPRINT_FIELDS) if key in request}
    encoded = json.dumps(material, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _canonical_fingerprint(value: Any) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _json_object(value: str, code: str = "managed_resource_drift") -> dict[str, Any]:
    try:
        document = json.loads(value)
    except (TypeError, json.JSONDecodeError) as error:
        raise TransactionError(code) from error
    if not isinstance(document, dict):
        raise TransactionError(code)
    return document


def _reject_symlink_components(path: pathlib.Path) -> None:
    absolute = pathlib.Path(os.path.abspath(path))
    current = pathlib.Path(absolute.anchor)
    for part in absolute.parts[1:] if absolute.anchor else absolute.parts:
        current = current / part
        try:
            if current.is_symlink():
                raise TransactionError("unsafe_path")
        except OSError as error:
            raise TransactionError("unsafe_path") from error


def _require_regular_file(path: pathlib.Path) -> None:
    _reject_symlink_components(path)
    try:
        if path.is_symlink() or not path.is_file():
            raise TransactionError("unsafe_path")
    except OSError as error:
        raise TransactionError("unsafe_path") from error


def _ensure_private_directory(path: pathlib.Path) -> None:
    _reject_symlink_components(path.parent)
    try:
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        if path.is_symlink() or not path.is_dir():
            raise TransactionError("unsafe_path")
        if os.name != "nt":
            os.chmod(path, 0o700)
    except TransactionError:
        raise
    except OSError as error:
        raise TransactionError("unsafe_path") from error


def _private_atomic_json(path: pathlib.Path, document: Any) -> None:
    _ensure_private_directory(path.parent)
    if path.exists() and path.is_symlink():
        raise TransactionError("unsafe_path")
    temporary = path.parent / ("." + path.name + "." + secrets.token_hex(8) + ".tmp")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = None
    try:
        descriptor = os.open(temporary, flags, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            descriptor = None
            json.dump(document, output, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        if os.name != "nt":
            os.chmod(path, 0o600)
    except TransactionError:
        raise
    except OSError as error:
        raise TransactionError("snapshot_write_failed") from error
    finally:
        if descriptor is not None:
            os.close(descriptor)
        try:
            if temporary.exists():
                temporary.unlink()
        except OSError:
            pass


def _spool_atomic_result(path: pathlib.Path, document: dict[str, str]) -> None:
    _reject_symlink_components(path.parent)
    try:
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o750)
        if path.parent.is_symlink() or not path.parent.is_dir():
            raise TransactionError("unsafe_path")
        if os.name != "nt":
            os.chmod(path.parent, 0o750)
        if path.exists() and path.is_symlink():
            raise TransactionError("unsafe_path")
        temporary = path.parent / ("." + path.name + "." + secrets.token_hex(8) + ".tmp")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        if hasattr(os, "O_NOFOLLOW"):
            flags |= os.O_NOFOLLOW
        descriptor = os.open(temporary, flags, 0o640)
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                descriptor = -1
                json.dump(document, output, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, path)
            if os.name != "nt":
                os.chmod(path, 0o640)
        finally:
            if descriptor >= 0:
                os.close(descriptor)
            temporary.unlink(missing_ok=True)
    except TransactionError:
        raise
    except OSError as error:
        raise TransactionError("result_write_failed") from error


SPOOL_NAME = re.compile(r"^(?P<operation>[A-Za-z0-9_-]{8,128})\.(?P<action>apply|finalize|rollback)\.json$")


def process_spool(manager: Any, request_dir: pathlib.Path, result_dir: pathlib.Path) -> list[dict[str, str]]:
    request_dir = pathlib.Path(request_dir)
    result_dir = pathlib.Path(result_dir)
    _reject_symlink_components(request_dir)
    _reject_symlink_components(result_dir)
    if request_dir.is_symlink() or not request_dir.is_dir() or result_dir.is_symlink() or not result_dir.is_dir():
        raise TransactionError("unsafe_path")
    processed: list[dict[str, str]] = []
    for request_path in sorted(request_dir.glob("*.json"), key=lambda item: item.name):
        match = SPOOL_NAME.fullmatch(request_path.name)
        if match is None:
            continue
        operation_id = match.group("operation")
        action = match.group("action")
        result_path = result_dir / request_path.name
        result = {"operationId": operation_id, "status": "failed", "errorCode": "invalid_request"}
        try:
            _require_regular_file(request_path)
            if request_path.stat().st_size <= 0 or request_path.stat().st_size > 16 * 1024:
                raise TransactionError("invalid_request")
            envelope = json.loads(request_path.read_text(encoding="utf-8"))
            expected_fields = {"action", "operationId", "request"} if action == "apply" else {"action", "operationId"}
            if not isinstance(envelope, dict) or set(envelope) != expected_fields or envelope.get("action") != action or envelope.get("operationId") != operation_id:
                raise TransactionError("invalid_request")
            if action == "apply":
                if not isinstance(envelope.get("request"), dict) or envelope["request"].get("operationId") != operation_id:
                    raise TransactionError("invalid_request")
                candidate = manager.apply(envelope["request"])
            elif action == "finalize":
                candidate = manager.finalize(operation_id)
            else:
                candidate = manager.rollback(operation_id)
            if not isinstance(candidate, dict):
                raise TransactionError("transaction_failed")
            status = candidate.get("status")
            error_code = candidate.get("errorCode", "")
            if candidate.get("operationId") != operation_id or not isinstance(status, str) or not isinstance(error_code, str) or not re.fullmatch(r"[a-z0-9_]{0,64}", error_code):
                raise TransactionError("transaction_failed")
            allowed_status = {
                "apply": {"applied", "failed", "repair_required"},
                "finalize": {"finalized", "failed", "repair_required"},
                "rollback": {"rolled_back", "failed", "repair_required"},
            }
            if status not in allowed_status[action]:
                raise TransactionError("transaction_failed")
            result = {"operationId": operation_id, "status": status, "errorCode": error_code}
        except TransactionError as error:
            result = {"operationId": operation_id, "status": "failed", "errorCode": error.code}
        except (OSError, json.JSONDecodeError):
            result = {"operationId": operation_id, "status": "failed", "errorCode": "invalid_request"}
        except Exception:
            result = {"operationId": operation_id, "status": "failed", "errorCode": "transaction_failed"}
        _spool_atomic_result(result_path, result)
        try:
            request_path.unlink()
        except OSError as error:
            raise TransactionError("request_cleanup_failed") from error
        processed.append(result)
    return processed


def _read_private_json(path: pathlib.Path) -> dict[str, Any]:
    _require_regular_file(path)
    try:
        with path.open("r", encoding="utf-8") as source:
            document = json.load(source)
    except (OSError, json.JSONDecodeError) as error:
        raise TransactionError("snapshot_invalid") from error
    if not isinstance(document, dict):
        raise TransactionError("snapshot_invalid")
    return document


class SubprocessRunner:
    """生产 Xray CLI 边界；不会把 stdout/stderr 拼入异常。"""

    def __init__(self, config: ProtocolTransactionConfig):
        self.config = config

    def _run(self, arguments: list[str]) -> str:
        try:
            result = subprocess.run(
                arguments,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=30,
                check=False,
            )
        except (OSError, subprocess.SubprocessError) as error:
            raise TransactionError("xray_command_failed") from error
        if result.returncode != 0:
            raise TransactionError("xray_command_failed")
        return result.stdout

    def offline_test(self, config_path: pathlib.Path) -> None:
        self._run([str(self.config.xray_binary), "run", "-test", "-config", str(config_path)])

    def remove_inbound(self, tag: str) -> None:
        self._run([str(self.config.xray_binary), "api", "rmi", "--server=" + self.config.api_server, tag])

    def add_inbound(self, inbound_path: pathlib.Path) -> None:
        self._run([str(self.config.xray_binary), "api", "adi", "--server=" + self.config.api_server, str(inbound_path)])

    def list_inbound_tags(self) -> set[str]:
        output = self._run(
            [str(self.config.xray_binary), "api", "lsi", "--server=" + self.config.api_server, "--isOnlyTags=true"]
        )
        tags: set[str] = set()
        try:
            document = json.loads(output)
            tags.update(_collect_tags(document))
        except json.JSONDecodeError:
            for line in output.splitlines():
                candidate = line.strip().strip('"')
                if re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", candidate):
                    tags.add(candidate)
        return tags


def _collect_tags(value: Any) -> Iterable[str]:
    if isinstance(value, dict):
        for key, item in value.items():
            if key.lower() == "tag" and isinstance(item, str):
                yield item
            else:
                yield from _collect_tags(item)
    elif isinstance(value, list):
        for item in value:
            yield from _collect_tags(item)
    elif isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", value):
        yield value


class ProtocolTransactionManager:
    def __init__(
        self,
        config: ProtocolTransactionConfig,
        runner: Any,
        *,
        fault_injector: Callable[[str], None] | None = None,
        token_factory: Callable[[], str] | None = None,
    ):
        self.config = config
        self.runner = runner
        self.fault_injector = fault_injector or (lambda _phase: None)
        self.token_factory = token_factory or (lambda: secrets.token_urlsafe(32))

    def _fault(self, phase: str) -> None:
        self.fault_injector(phase)

    def _validate_config_paths(self) -> None:
        for path in (
            self.config.database_path,
            self.config.runtime_config_path,
            self.config.certificate_path,
            self.config.private_key_path,
            self.config.xray_binary,
        ):
            _require_regular_file(path)
        if not re.fullmatch(r"127\.0\.0\.1:[1-9][0-9]{0,4}", self.config.api_server):
            raise TransactionError("invalid_config")
        if tuple(sorted(set(self.config.allowed_ports))) != (8443, 20000, 20001, 20002):
            raise TransactionError("invalid_config")
        _ensure_private_directory(self.config.snapshot_dir)
        _ensure_private_directory(self.config.profile_dir)

    def validate_request(self, request: dict[str, Any]) -> dict[str, Any]:
        self._validate_config_paths()
        if not isinstance(request, dict) or set(request) != REQUEST_FIELDS:
            raise TransactionError("invalid_request")
        operation_id = request.get("operationId")
        egress_id = request.get("egressId")
        inbound_id = request.get("inboundId")
        inbound_tag = request.get("inboundTag")
        port = request.get("port")
        old_mode = request.get("oldMode")
        new_mode = request.get("newMode")
        expected = request.get("expectedFingerprint")
        if (
            not isinstance(operation_id, str)
            or not SAFE_OPERATION_ID.fullmatch(operation_id)
            or not isinstance(egress_id, str)
            or not SAFE_EGRESS_ID.fullmatch(egress_id)
            or not isinstance(inbound_id, int)
            or isinstance(inbound_id, bool)
            or inbound_id <= 0
            or not isinstance(inbound_tag, str)
            or not isinstance(port, int)
            or isinstance(port, bool)
            or port not in self.config.allowed_ports
            or old_mode not in PROTOCOL_MODES
            or new_mode not in PROTOCOL_MODES
            or old_mode == new_mode
            or not isinstance(expected, str)
            or not SAFE_HEX_256.fullmatch(expected)
            or request_fingerprint(request) != expected
        ):
            raise TransactionError("invalid_request")
        if egress_id == "agw-main":
            if inbound_tag != "aimili-reality" or port != 8443:
                raise TransactionError("invalid_request")
        elif inbound_tag != egress_id + "-vless" or port == 8443:
            raise TransactionError("invalid_request")
        self.load_target(request, already_validated=True)
        return dict(request)

    def load_target(self, request: dict[str, Any], *, already_validated: bool = False) -> dict[str, Any]:
        if not already_validated:
            self._validate_request_shape(request)
            self._validate_config_paths()
        runtime = self._load_runtime_config()
        row = self._load_inbound_row(int(request["inboundId"]))
        self._validate_ownership(request, row, runtime)
        clients = self._load_attached_clients(int(request["inboundId"]))
        if not clients:
            raise TransactionError("managed_resource_missing")
        current_mode = self._mode_for_row(row)
        if current_mode != request["oldMode"]:
            raise TransactionError("expected_state_mismatch")
        if current_mode == VLESS_TCP_REALITY_VISION:
            enabled = [client for client in clients if int(client.get("enable", 0)) == 1]
            if not enabled or any(client.get("flow") != "xtls-rprx-vision" for client in enabled):
                raise TransactionError("managed_resource_drift")
        profile = self._load_or_capture_profile(request["egressId"], row, current_mode)
        return {
            "request": dict(request),
            "row": row,
            "clients": clients,
            "runtime": runtime,
            "profile": profile,
        }

    def _validate_request_shape(self, request: dict[str, Any]) -> None:
        if not isinstance(request, dict) or set(request) != REQUEST_FIELDS:
            raise TransactionError("invalid_request")
        expected = request.get("expectedFingerprint")
        if not isinstance(expected, str) or request_fingerprint(request) != expected:
            raise TransactionError("invalid_request")

    def _load_runtime_config(self) -> dict[str, Any]:
        try:
            with self.config.runtime_config_path.open("r", encoding="utf-8") as source:
                document = json.load(source)
        except (OSError, json.JSONDecodeError) as error:
            raise TransactionError("runtime_config_invalid") from error
        if not isinstance(document, dict):
            raise TransactionError("runtime_config_invalid")
        return document

    def _connect(self) -> sqlite3.Connection:
        try:
            database = sqlite3.connect(str(self.config.database_path), timeout=5)
            database.row_factory = sqlite3.Row
            return database
        except sqlite3.Error as error:
            raise TransactionError("database_open_failed") from error

    def _load_inbound_row(self, inbound_id: int) -> dict[str, Any]:
        with closing(self._connect()) as database:
            try:
                row = database.execute("SELECT * FROM inbounds WHERE id=?", (inbound_id,)).fetchone()
            except sqlite3.Error as error:
                raise TransactionError("database_read_failed") from error
        if row is None:
            raise TransactionError("managed_resource_missing")
        return dict(row)

    def _load_attached_clients(self, inbound_id: int) -> list[dict[str, Any]]:
        with closing(self._connect()) as database:
            try:
                rows = database.execute(
                    """
                    SELECT c.* FROM clients c
                    JOIN client_inbounds ci ON ci.client_id=c.id
                    WHERE ci.inbound_id=? ORDER BY c.id
                    """,
                    (inbound_id,),
                ).fetchall()
            except sqlite3.Error as error:
                raise TransactionError("database_read_failed") from error
        return [dict(row) for row in rows]

    def _validate_ownership(self, request: dict[str, Any], row: dict[str, Any], runtime: dict[str, Any]) -> None:
        if (
            row.get("id") != request["inboundId"]
            or row.get("tag") != request["inboundTag"]
            or row.get("port") != request["port"]
            or int(row.get("enable", 0)) != 1
            or row.get("node_id") is not None
            or row.get("protocol") == "mixed"
        ):
            raise TransactionError("ownership_conflict")
        if request["egressId"] == "agw-main":
            expected_remark = "Aimili Reality"
            outbound_tag = "aimili-socks"
            allowed_socks_ports = {7928}
        else:
            expected_remark = "Aimili Gateway " + request["egressId"] + " VLESS"
            outbound_tag = request["egressId"] + "-socks"
            allowed_socks_ports = {17928, 17929, 17930}
        if row.get("remark") != expected_remark:
            raise TransactionError("ownership_conflict")
        inbounds = runtime.get("inbounds")
        outbounds = runtime.get("outbounds")
        routing = runtime.get("routing")
        rules = routing.get("rules") if isinstance(routing, dict) else None
        if not isinstance(inbounds, list) or not isinstance(outbounds, list) or not isinstance(rules, list):
            raise TransactionError("runtime_config_invalid")
        runtime_matches = [item for item in inbounds if isinstance(item, dict) and item.get("tag") == request["inboundTag"]]
        if len(runtime_matches) != 1 or runtime_matches[0].get("port") != request["port"]:
            raise TransactionError("managed_resource_drift")
        outbound_matches = [item for item in outbounds if isinstance(item, dict) and item.get("tag") == outbound_tag]
        if len(outbound_matches) != 1 or outbound_matches[0].get("protocol") != "socks":
            raise TransactionError("ownership_conflict")
        settings = outbound_matches[0].get("settings")
        servers = settings.get("servers") if isinstance(settings, dict) else None
        if (
            not isinstance(servers, list)
            or len(servers) != 1
            or not isinstance(servers[0], dict)
            or servers[0].get("address") != "127.0.0.1"
            or servers[0].get("port") not in allowed_socks_ports
        ):
            raise TransactionError("ownership_conflict")
        owned_rules = [
            rule
            for rule in rules
            if isinstance(rule, dict)
            and rule.get("outboundTag") == outbound_tag
            and isinstance(rule.get("inboundTag"), list)
            and request["inboundTag"] in rule["inboundTag"]
        ]
        if len(owned_rules) != 1:
            raise TransactionError("ownership_conflict")

    def _mode_for_row(self, row: dict[str, Any]) -> str:
        protocol = row.get("protocol")
        stream = _json_object(row.get("stream_settings", ""))
        settings = _json_object(row.get("settings", ""))
        if (
            protocol == "vless"
            and stream.get("network") == "tcp"
            and stream.get("security") == "reality"
            and settings.get("decryption") == "none"
            and not bool(row.get("disable_flow"))
        ):
            return VLESS_TCP_REALITY_VISION
        if (
            protocol == "vless"
            and stream.get("network") == "xhttp"
            and stream.get("security") == "reality"
            and settings.get("decryption") == "none"
            and bool(row.get("disable_flow"))
        ):
            return VLESS_XHTTP_REALITY
        if (
            protocol == "hysteria"
            and stream.get("network") == "hysteria"
            and stream.get("security") == "tls"
            and settings.get("version") == 2
        ):
            return HYSTERIA2_QUIC_TLS
        raise TransactionError("managed_resource_drift")

    def _profile_path(self, egress_id: str) -> pathlib.Path:
        if not SAFE_EGRESS_ID.fullmatch(egress_id):
            raise TransactionError("invalid_request")
        return self.config.profile_dir / (egress_id + ".json")

    def _load_or_capture_profile(self, egress_id: str, row: dict[str, Any], mode: str) -> dict[str, Any]:
        path = self._profile_path(egress_id)
        if mode in {VLESS_TCP_REALITY_VISION, VLESS_XHTTP_REALITY}:
            stream = _json_object(row["stream_settings"])
            reality = stream.get("realitySettings")
            if not isinstance(reality, dict) or not reality.get("privateKey") or not reality.get("shortIds"):
                raise TransactionError("managed_resource_drift")
            profile = {
                "egressId": egress_id,
                "realitySettings": reality,
                "xhttpPath": "/" + hashlib.sha256(("aimili-xhttp:" + egress_id).encode()).hexdigest()[:24],
            }
            _private_atomic_json(path, profile)
            return profile
        if not path.exists():
            raise TransactionError("managed_profile_missing")
        profile = _read_private_json(path)
        if profile.get("egressId") != egress_id or not isinstance(profile.get("realitySettings"), dict):
            raise TransactionError("managed_profile_invalid")
        return profile

    def build_template(self, source: dict[str, Any], mode: str) -> dict[str, Any]:
        if mode not in PROTOCOL_MODES:
            raise TransactionError("invalid_request")
        row = source["row"]
        clients = [client for client in source["clients"] if int(client.get("enable", 0)) == 1]
        if not clients:
            raise TransactionError("managed_resource_missing")
        template: dict[str, Any] = {
            "id": row["id"],
            "remark": row["remark"],
            "listen": row.get("listen", ""),
            "port": row["port"],
            "tag": row["tag"],
            "sniffing": _json_object(row["sniffing"]),
            "disableFlow": False,
            "_clientAuthUpdates": {},
        }
        if mode in {VLESS_TCP_REALITY_VISION, VLESS_XHTTP_REALITY}:
            reality = copy.deepcopy(source["profile"]["realitySettings"])
            flow = "xtls-rprx-vision" if mode == VLESS_TCP_REALITY_VISION else ""
            client_settings = []
            for client in clients:
                client_id = str(client.get("uuid") or "")
                email = str(client.get("email") or "")
                if not client_id or not email:
                    raise TransactionError("managed_resource_drift")
                entry = {"id": client_id, "email": email}
                if flow:
                    entry["flow"] = flow
                client_settings.append(entry)
            template["protocol"] = "vless"
            template["settings"] = {"clients": client_settings, "decryption": "none"}
            if mode == VLESS_TCP_REALITY_VISION:
                template["streamSettings"] = {
                    "network": "tcp",
                    "security": "reality",
                    "tcpSettings": {},
                    "realitySettings": reality,
                }
            else:
                template["disableFlow"] = True
                template["streamSettings"] = {
                    "network": "xhttp",
                    "security": "reality",
                    "xhttpSettings": {"path": source["profile"]["xhttpPath"], "mode": "auto"},
                    "realitySettings": reality,
                }
        else:
            hysteria_clients = []
            auth_updates: dict[str, str] = {}
            for client in clients:
                email = str(client.get("email") or "")
                auth = str(client.get("auth") or "")
                if not email:
                    raise TransactionError("managed_resource_drift")
                if not auth:
                    auth = self.token_factory()
                    if not isinstance(auth, str) or len(auth) < 16 or len(auth) > 256 or any(ch.isspace() for ch in auth):
                        raise TransactionError("credential_generation_failed")
                    auth_updates[str(client["id"])] = auth
                hysteria_clients.append({"auth": auth, "email": email})
            server_name = self.config.tls_server_name
            if not server_name:
                names = source["profile"]["realitySettings"].get("serverNames", [])
                server_name = names[0] if isinstance(names, list) and names else ""
            if not server_name:
                raise TransactionError("invalid_config")
            template["protocol"] = "hysteria"
            template["settings"] = {"version": 2, "clients": hysteria_clients}
            template["streamSettings"] = {
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
                            "certificateFile": str(self.config.certificate_path),
                            "keyFile": str(self.config.private_key_path),
                            "oneTimeLoading": False,
                            "usage": "encipherment",
                            "ocspStapling": 0,
                            "buildChain": False,
                        }
                    ],
                },
            }
            template["_clientAuthUpdates"] = auth_updates
        return template

    def _runtime_inbound(self, template: dict[str, Any]) -> dict[str, Any]:
        stream = copy.deepcopy(template["streamSettings"])
        for key in ("tlsSettings", "realitySettings"):
            settings = stream.get(key)
            if isinstance(settings, dict):
                settings.pop("settings", None)
        return {
            "listen": template["listen"],
            "port": template["port"],
            "protocol": template["protocol"],
            "settings": copy.deepcopy(template["settings"]),
            "streamSettings": stream,
            "tag": template["tag"],
            "sniffing": copy.deepcopy(template["sniffing"]),
        }

    def _runtime_from_source(self, source: dict[str, Any]) -> dict[str, Any]:
        mode = self._mode_for_row(source["row"])
        return self._runtime_inbound(self.build_template(source, mode))

    def _full_test_config(self, source: dict[str, Any], template: dict[str, Any]) -> dict[str, Any]:
        document = copy.deepcopy(source["runtime"])
        inbounds = document.get("inbounds")
        desired = self._runtime_inbound(template)
        replaced = 0
        for index, inbound in enumerate(inbounds):
            if isinstance(inbound, dict) and inbound.get("tag") == desired["tag"]:
                inbounds[index] = desired
                replaced += 1
        if replaced != 1:
            raise TransactionError("managed_resource_drift")
        return document

    def _write_temporary_json(self, operation_dir: pathlib.Path, name: str, document: Any) -> pathlib.Path:
        path = operation_dir / name
        if path.exists() or path.is_symlink():
            raise TransactionError("unsafe_path")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        if hasattr(os, "O_NOFOLLOW"):
            flags |= os.O_NOFOLLOW
        descriptor = None
        try:
            descriptor = os.open(path, flags, 0o600)
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                descriptor = None
                json.dump(document, output, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
            if os.name != "nt":
                os.chmod(path, 0o600)
            return path
        except OSError as error:
            raise TransactionError("temporary_write_failed") from error
        finally:
            if descriptor is not None:
                os.close(descriptor)

    def _snapshot_path(self, operation_id: str) -> pathlib.Path:
        if not SAFE_OPERATION_ID.fullmatch(operation_id or ""):
            raise TransactionError("invalid_request")
        return self.config.snapshot_dir / operation_id / "snapshot.json"

    def _new_snapshot(self, source: dict[str, Any], template: dict[str, Any]) -> dict[str, Any]:
        return {
            "version": 1,
            "phase": "snapshot",
            "createdAt": int(time.time()),
            "request": source["request"],
            "row": source["row"],
            "clients": source["clients"],
            "oldRuntimeInbound": self._runtime_from_source(source),
            "desiredTemplate": template,
        }

    def _save_snapshot(self, path: pathlib.Path, snapshot: dict[str, Any], phase: str) -> None:
        snapshot["phase"] = phase
        _private_atomic_json(path, snapshot)

    def persist_template(self, source: dict[str, Any], template: dict[str, Any]) -> None:
        row_id = source["row"]["id"]
        settings = json.dumps(template["settings"], sort_keys=True, separators=(",", ":"))
        stream = json.dumps(template["streamSettings"], sort_keys=True, separators=(",", ":"))
        sniffing = json.dumps(template["sniffing"], sort_keys=True, separators=(",", ":"))
        with closing(self._connect()) as database:
            try:
                database.execute("BEGIN IMMEDIATE")
                cursor = database.execute(
                    """
                    UPDATE inbounds SET protocol=?,settings=?,stream_settings=?,sniffing=?,disable_flow=?
                    WHERE id=? AND tag=? AND port=?
                    """,
                    (
                        template["protocol"],
                        settings,
                        stream,
                        sniffing,
                        1 if template["disableFlow"] else 0,
                        row_id,
                        source["row"]["tag"],
                        source["row"]["port"],
                    ),
                )
                if cursor.rowcount != 1:
                    raise TransactionError("database_concurrent_change")
                for client_id, auth in template.get("_clientAuthUpdates", {}).items():
                    updated = database.execute(
                        "UPDATE clients SET auth=? WHERE id=? AND (auth='' OR auth IS NULL)",
                        (auth, int(client_id)),
                    )
                    if updated.rowcount != 1:
                        raise TransactionError("database_concurrent_change")
                database.commit()
            except TransactionError:
                database.rollback()
                raise
            except sqlite3.Error as error:
                database.rollback()
                raise TransactionError("database_write_failed") from error

    def _restore_database(self, snapshot: dict[str, Any]) -> None:
        row = snapshot["row"]
        clients = snapshot["clients"]
        with closing(self._connect()) as database:
            try:
                database.execute("BEGIN IMMEDIATE")
                cursor = database.execute(
                    """
                    UPDATE inbounds SET protocol=?,settings=?,stream_settings=?,sniffing=?,disable_flow=?
                    WHERE id=? AND tag=? AND port=?
                    """,
                    (
                        row["protocol"],
                        row["settings"],
                        row["stream_settings"],
                        row["sniffing"],
                        row["disable_flow"],
                        row["id"],
                        row["tag"],
                        row["port"],
                    ),
                )
                if cursor.rowcount != 1:
                    raise TransactionError("database_concurrent_change")
                for client in clients:
                    updated = database.execute("UPDATE clients SET auth=? WHERE id=?", (client.get("auth", ""), client["id"]))
                    if updated.rowcount != 1:
                        raise TransactionError("database_concurrent_change")
                database.commit()
            except TransactionError:
                database.rollback()
                raise
            except sqlite3.Error as error:
                database.rollback()
                raise TransactionError("database_restore_failed") from error

    def _database_matches_snapshot(self, snapshot: dict[str, Any]) -> bool:
        row = snapshot["row"]
        clients = snapshot["clients"]
        current = self._load_inbound_row(row["id"])
        if any(current[column] != row[column] for column in MUTATED_INBOUND_COLUMNS):
            return False
        with closing(self._connect()) as database:
            try:
                for client in clients:
                    stored = database.execute(
                        "SELECT auth FROM clients WHERE id=?", (client["id"],)
                    ).fetchone()
                    if stored is None or stored[0] != client.get("auth", ""):
                        return False
            except sqlite3.Error as error:
                raise TransactionError("database_read_failed") from error
        return True

    def _verify_applied(self, source: dict[str, Any], template: dict[str, Any]) -> None:
        tags = self.runner.list_inbound_tags()
        if template["tag"] not in tags:
            raise TransactionError("runtime_verification_failed")
        row = self._load_inbound_row(source["row"]["id"])
        persisted = {
            "protocol": row["protocol"],
            "settings": _json_object(row["settings"]),
            "streamSettings": _json_object(row["stream_settings"]),
            "sniffing": _json_object(row["sniffing"]),
            "disableFlow": bool(row["disable_flow"]),
        }
        expected = {
            "protocol": template["protocol"],
            "settings": template["settings"],
            "streamSettings": template["streamSettings"],
            "sniffing": template["sniffing"],
            "disableFlow": bool(template["disableFlow"]),
        }
        if _canonical_fingerprint(persisted) != _canonical_fingerprint(expected):
            raise TransactionError("database_verification_failed")
        for client_id, auth in template.get("_clientAuthUpdates", {}).items():
            with closing(self._connect()) as database:
                row = database.execute("SELECT auth FROM clients WHERE id=?", (int(client_id),)).fetchone()
            if row is None or row[0] != auth:
                raise TransactionError("database_verification_failed")

    def apply(self, request: dict[str, Any]) -> dict[str, str]:
        request = self.validate_request(request)
        snapshot_path = self._snapshot_path(request["operationId"])
        if snapshot_path.exists():
            existing = _read_private_json(snapshot_path)
            if existing.get("request") == request and existing.get("phase") == "applied":
                return self._result(request["operationId"], "applied")
            raise TransactionError("operation_conflict")
        operation_dir = snapshot_path.parent
        try:
            operation_dir.mkdir(mode=0o700)
            if os.name != "nt":
                os.chmod(operation_dir, 0o700)
        except OSError as error:
            raise TransactionError("snapshot_write_failed") from error
        source = self.load_target(request, already_validated=True)
        template = self.build_template(source, request["newMode"])
        snapshot = self._new_snapshot(source, template)
        self._save_snapshot(snapshot_path, snapshot, "snapshot")
        self._fault("snapshot")
        try:
            test_path = self._write_temporary_json(operation_dir, "offline-config.json", self._full_test_config(source, template))
            try:
                self._fault("offline_test")
                self.runner.offline_test(test_path)
            finally:
                test_path.unlink(missing_ok=True)
            inbound_path = self._write_temporary_json(operation_dir, "desired-inbound.json", self._runtime_inbound(template))
            try:
                self._save_snapshot(snapshot_path, snapshot, "runtime_remove_pending")
                self._fault("runtime_remove")
                self.runner.remove_inbound(template["tag"])
                self._save_snapshot(snapshot_path, snapshot, "runtime_add_pending")
                self._fault("runtime_add")
                self.runner.add_inbound(inbound_path)
            finally:
                inbound_path.unlink(missing_ok=True)
            self._save_snapshot(snapshot_path, snapshot, "database_pending")
            self._fault("database")
            self.persist_template(source, template)
            self._save_snapshot(snapshot_path, snapshot, "database_committed")
            self._fault("database_committed")
            self._fault("verify")
            self._verify_applied(source, template)
            self._save_snapshot(snapshot_path, snapshot, "applied")
            return self._result(request["operationId"], "applied")
        except SimulatedCrash:
            raise
        except BaseException as error:
            original = error if isinstance(error, TransactionError) else TransactionError("transaction_failed")
            try:
                self._rollback_snapshot(snapshot_path, remove_on_success=True)
            except BaseException as rollback_error:
                try:
                    persisted = _read_private_json(snapshot_path)
                    self._save_snapshot(snapshot_path, persisted, "repair_required")
                except BaseException:
                    pass
                raise TransactionError("rollback_failed") from rollback_error
            raise original

    def finalize(self, operation_id: str) -> dict[str, str]:
        snapshot_path = self._snapshot_path(operation_id)
        snapshot = _read_private_json(snapshot_path)
        if snapshot.get("phase") != "applied":
            raise TransactionError("operation_not_applied")
        self._remove_operation_dir(snapshot_path.parent)
        return self._result(operation_id, "finalized")

    def rollback(self, operation_id: str) -> dict[str, str]:
        snapshot_path = self._snapshot_path(operation_id)
        self._rollback_snapshot(snapshot_path, remove_on_success=True)
        return self._result(operation_id, "rolled_back")

    def _rollback_snapshot(self, snapshot_path: pathlib.Path, *, remove_on_success: bool) -> None:
        snapshot = _read_private_json(snapshot_path)
        phase = str(snapshot.get("phase", ""))
        request = snapshot.get("request")
        old_runtime = snapshot.get("oldRuntimeInbound")
        if not isinstance(request, dict) or not isinstance(old_runtime, dict):
            raise TransactionError("snapshot_invalid")
        runtime_may_have_changed = phase not in {"snapshot"}
        if runtime_may_have_changed:
            operation_dir = snapshot_path.parent
            old_path = self._write_temporary_json(operation_dir, "rollback-inbound.json", old_runtime)
            try:
                self.runner.remove_inbound(request["inboundTag"])
                self.runner.add_inbound(old_path)
            finally:
                old_path.unlink(missing_ok=True)
        if not self._database_matches_snapshot(snapshot):
            self._restore_database(snapshot)
        tags = self.runner.list_inbound_tags()
        if request["inboundTag"] not in tags:
            raise TransactionError("rollback_verification_failed")
        row = self._load_inbound_row(snapshot["row"]["id"])
        for column in MUTATED_INBOUND_COLUMNS:
            if row[column] != snapshot["row"][column]:
                raise TransactionError("rollback_verification_failed")
        if remove_on_success:
            self._remove_operation_dir(snapshot_path.parent)

    def recover_pending(self, min_age_seconds: int = 0) -> list[dict[str, str]]:
        self._validate_config_paths()
        if not isinstance(min_age_seconds, int) or isinstance(min_age_seconds, bool) or min_age_seconds < 0 or min_age_seconds > 3600:
            raise TransactionError("invalid_request")
        results: list[dict[str, str]] = []
        for operation_dir in sorted(self.config.snapshot_dir.iterdir(), key=lambda item: item.name):
            if operation_dir.is_symlink() or not operation_dir.is_dir() or not SAFE_OPERATION_ID.fullmatch(operation_dir.name):
                continue
            snapshot_path = operation_dir / "snapshot.json"
            if not snapshot_path.is_file() or snapshot_path.is_symlink():
                continue
            snapshot = _read_private_json(snapshot_path)
            if snapshot.get("phase") == "repair_required":
                continue
            created_at = snapshot.get("createdAt")
            if min_age_seconds and isinstance(created_at, int) and not isinstance(created_at, bool) and int(time.time()) - created_at < min_age_seconds:
                continue
            try:
                self._rollback_snapshot(snapshot_path, remove_on_success=True)
                results.append(self._result(operation_dir.name, "rolled_back"))
            except BaseException:
                self._save_snapshot(snapshot_path, snapshot, "repair_required")
                results.append(self._result(operation_dir.name, "repair_required", "rollback_failed"))
        return results

    def _remove_operation_dir(self, operation_dir: pathlib.Path) -> None:
        expected_parent = pathlib.Path(os.path.abspath(self.config.snapshot_dir))
        actual_parent = pathlib.Path(os.path.abspath(operation_dir.parent))
        if actual_parent != expected_parent or operation_dir.is_symlink() or not SAFE_OPERATION_ID.fullmatch(operation_dir.name):
            raise TransactionError("unsafe_path")
        try:
            shutil.rmtree(operation_dir)
        except OSError as error:
            raise TransactionError("snapshot_cleanup_failed") from error

    @staticmethod
    def _result(operation_id: str, status: str, error_code: str = "") -> dict[str, str]:
        return {"operationId": operation_id, "status": status, "errorCode": error_code}


def _read_config(path: pathlib.Path) -> ProtocolTransactionConfig:
    _require_regular_file(path)
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise TransactionError("invalid_config") from error
    return ProtocolTransactionConfig.from_json(document)


def _read_request(path: pathlib.Path) -> dict[str, Any]:
    _require_regular_file(path)
    try:
        if path.stat().st_size <= 0 or path.stat().st_size > 16 * 1024:
            raise TransactionError("invalid_request")
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise TransactionError("invalid_request") from error
    if not isinstance(document, dict):
        raise TransactionError("invalid_request")
    return document


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(add_help=True)
    parser.add_argument("--config", required=True)
    subparsers = parser.add_subparsers(dest="action", required=True)
    apply_parser = subparsers.add_parser("apply")
    apply_parser.add_argument("request")
    for action in ("finalize", "rollback"):
        action_parser = subparsers.add_parser(action)
        action_parser.add_argument("operation_id")
    subparsers.add_parser("recover")
    subparsers.add_parser("spool")
    args = parser.parse_args(argv)
    try:
        config = _read_config(pathlib.Path(args.config))
        manager = ProtocolTransactionManager(config, SubprocessRunner(config))
        if args.action == "apply":
            result = manager.apply(_read_request(pathlib.Path(args.request)))
        elif args.action == "finalize":
            result = manager.finalize(args.operation_id)
        elif args.action == "rollback":
            result = manager.rollback(args.operation_id)
        elif args.action == "recover":
            result = {"operations": manager.recover_pending()}
        else:
            if config.spool_request_dir is None or config.spool_result_dir is None:
                raise TransactionError("invalid_config")
            recovered = manager.recover_pending(min_age_seconds=180)
            result = {"recovered": recovered, "operations": process_spool(manager, config.spool_request_dir, config.spool_result_dir)}
        print(json.dumps(result, sort_keys=True, separators=(",", ":")))
        return 0
    except TransactionError as error:
        print(json.dumps({"status": "failed", "errorCode": error.code}, sort_keys=True, separators=(",", ":")))
        return 1


if __name__ == "__main__":
    sys.exit(main())
