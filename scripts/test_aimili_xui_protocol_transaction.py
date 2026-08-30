import hashlib
import importlib.util
import json
import os
import pathlib
import sqlite3
import stat
import sys
import tempfile
import time
import unittest
from contextlib import closing


SCRIPT = pathlib.Path(__file__).with_name("aimili_xui_protocol_transaction.py")
MODULE = None
if SCRIPT.exists():
    SPEC = importlib.util.spec_from_file_location("aimili_xui_protocol_transaction", SCRIPT)
    MODULE = importlib.util.module_from_spec(SPEC)
    assert SPEC.loader is not None
    sys.modules[SPEC.name] = MODULE
    SPEC.loader.exec_module(MODULE)


TCP = "vless_tcp_reality_vision"
XHTTP = "vless_xhttp_reality"
HYSTERIA2 = "hysteria2_quic_tls"
OPERATION_ID = "protocol-switch-0001"
CLIENT_EMAIL = "aimili-gateway-slot-one"
CLIENT_UUID = "11111111-1111-4111-8111-111111111111"
EXISTING_AUTH = "existing-hysteria-auth"


class FakeRunner:
    def __init__(self, fail_at=None, runtime_tags=None, fail_always=False):
        self.fail_at = fail_at
        self.fail_always = fail_always
        self.failed = False
        self.calls = []
        self.runtime_tags = set(runtime_tags or {"agw-slot-one-vless", "unmanaged-inbound"})

    def _record(self, name, value=None):
        self.calls.append((name, value))
        if self.fail_at == name and (self.fail_always or not self.failed):
            self.failed = True
            raise MODULE.TransactionError(name + "_failed")

    def offline_test(self, config_path):
        document = json.loads(pathlib.Path(config_path).read_text(encoding="utf-8"))
        self._record("offline_test", document)

    def remove_inbound(self, tag):
        self._record("rmi", tag)
        self.runtime_tags.discard(tag)

    def add_inbound(self, inbound_path):
        inbound = json.loads(pathlib.Path(inbound_path).read_text(encoding="utf-8"))
        self._record("adi", inbound)
        self.runtime_tags.add(inbound["tag"])

    def list_inbound_tags(self):
        self._record("lsi", None)
        return set(self.runtime_tags)


class FakeSpoolManager:
    def __init__(self):
        self.calls = []

    def apply(self, request):
        self.calls.append(("apply", request["operationId"]))
        return {"operationId": request["operationId"], "status": "applied", "errorCode": ""}

    def finalize(self, operation_id):
        self.calls.append(("finalize", operation_id))
        return {"operationId": operation_id, "status": "finalized", "errorCode": ""}

    def rollback(self, operation_id):
        self.calls.append(("rollback", operation_id))
        return {"operationId": operation_id, "status": "rolled_back", "errorCode": ""}


class CrashingSpoolManager(FakeSpoolManager):
    def apply(self, request):
        raise MODULE.SimulatedCrash("database_committed")


class SpoolWorkerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.requests = self.root / "requests"
        self.results = self.root / "results"
        self.requests.mkdir(mode=0o700)
        self.results.mkdir(mode=0o700)
        self.manager = FakeSpoolManager()

    def tearDown(self):
        self.temp.cleanup()

    def test_processes_closed_actions_and_writes_private_results(self):
        operation = "operation-safe-1"
        request = {
            "operationId": operation,
            "egressId": "agw-main",
            "inboundId": 7,
            "inboundTag": "aimili-reality",
            "port": 8443,
            "oldMode": TCP,
            "newMode": XHTTP,
            "expectedFingerprint": "0" * 64,
        }
        envelopes = {
            f"{operation}.apply.json": {"action": "apply", "operationId": operation, "request": request},
            "operation-safe-2.finalize.json": {"action": "finalize", "operationId": "operation-safe-2"},
            "operation-safe-3.rollback.json": {"action": "rollback", "operationId": "operation-safe-3"},
        }
        for name, envelope in envelopes.items():
            (self.requests / name).write_text(json.dumps(envelope), encoding="utf-8")

        MODULE.process_spool(self.manager, self.requests, self.results)

        self.assertEqual(
            self.manager.calls,
            [("apply", operation), ("finalize", "operation-safe-2"), ("rollback", "operation-safe-3")],
        )
        self.assertEqual(list(self.requests.iterdir()), [])
        for name in envelopes:
            result_path = self.results / name
            result = json.loads(result_path.read_text(encoding="utf-8"))
            self.assertEqual(set(result), {"operationId", "status", "errorCode"})
            if os.name != "nt":
                self.assertEqual(stat.S_IMODE(result_path.stat().st_mode), 0o640)

    def test_rejects_filename_envelope_mismatch_without_invoking_manager(self):
        name = "operation-safe-1.apply.json"
        (self.requests / name).write_text(
            json.dumps({"action": "rollback", "operationId": "operation-safe-1"}),
            encoding="utf-8",
        )

        MODULE.process_spool(self.manager, self.requests, self.results)

        self.assertEqual(self.manager.calls, [])
        result = json.loads((self.results / name).read_text(encoding="utf-8"))
        self.assertEqual(result, {"operationId": "operation-safe-1", "status": "failed", "errorCode": "invalid_request"})

    def test_crash_keeps_request_for_recovery_and_retrigger(self):
        operation = "operation-safe-1"
        request = {
            "operationId": operation,
            "egressId": "agw-main",
            "inboundId": 7,
            "inboundTag": "aimili-reality",
            "port": 8443,
            "oldMode": TCP,
            "newMode": XHTTP,
            "expectedFingerprint": "0" * 64,
        }
        name = f"{operation}.apply.json"
        request_path = self.requests / name
        request_path.write_text(
            json.dumps({"action": "apply", "operationId": operation, "request": request}),
            encoding="utf-8",
        )

        with self.assertRaises(MODULE.SimulatedCrash):
            MODULE.process_spool(CrashingSpoolManager(), self.requests, self.results)

        self.assertTrue(request_path.is_file())
        self.assertFalse((self.results / name).exists())

    @unittest.skipIf(os.name == "nt", "ordinary Windows test accounts may not create symlinks")
    def test_rejects_symlinked_request(self):
        target = self.root / "outside.json"
        target.write_text("{}", encoding="utf-8")
        name = "operation-safe-1.apply.json"
        (self.requests / name).symlink_to(target)

        MODULE.process_spool(self.manager, self.requests, self.results)

        self.assertEqual(self.manager.calls, [])
        result = json.loads((self.results / name).read_text(encoding="utf-8"))
        self.assertEqual(result["errorCode"], "unsafe_path")


class ProtocolTransactionTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(MODULE, "协议事务助手尚未实现")
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.database_path = self.root / "x-ui.db"
        self.snapshot_dir = self.root / "transactions"
        self.runtime_config_path = self.root / "runtime.json"
        self.certificate_path = self.root / "certificate.pem"
        self.private_key_path = self.root / "private-key.pem"
        self.xray_binary = self.root / "xray"
        self.certificate_path.write_text("test-certificate", encoding="utf-8")
        self.private_key_path.write_text("test-private-key", encoding="utf-8")
        self.xray_binary.write_text("test-xray", encoding="utf-8")
        self._create_database()
        self._write_runtime_config()
        self.config = MODULE.ProtocolTransactionConfig(
            database_path=self.database_path,
            snapshot_dir=self.snapshot_dir,
            runtime_config_path=self.runtime_config_path,
            certificate_path=self.certificate_path,
            private_key_path=self.private_key_path,
            xray_binary=self.xray_binary,
            api_server="127.0.0.1:10085",
            allowed_ports=(8443, 20000, 20001, 20002),
        )

    def tearDown(self):
        self.temp.cleanup()

    def _create_database(self):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.executescript(
                """
                CREATE TABLE inbounds(
                    id INTEGER PRIMARY KEY,
                    user_id INTEGER NOT NULL DEFAULT 1,
                    up INTEGER NOT NULL DEFAULT 0,
                    down INTEGER NOT NULL DEFAULT 0,
                    total INTEGER NOT NULL DEFAULT 0,
                    remark TEXT NOT NULL,
                    sub_sort_index INTEGER NOT NULL DEFAULT 1,
                    enable INTEGER NOT NULL DEFAULT 1,
                    expiry_time INTEGER NOT NULL DEFAULT 0,
                    traffic_reset TEXT NOT NULL DEFAULT 'never',
                    traffic_reset_day INTEGER NOT NULL DEFAULT 1,
                    last_traffic_reset_time INTEGER NOT NULL DEFAULT 0,
                    listen TEXT NOT NULL DEFAULT '',
                    port INTEGER NOT NULL,
                    protocol TEXT NOT NULL,
                    settings TEXT NOT NULL,
                    stream_settings TEXT NOT NULL,
                    tag TEXT NOT NULL UNIQUE,
                    sniffing TEXT NOT NULL,
                    node_id INTEGER,
                    share_addr_strategy TEXT NOT NULL DEFAULT 'node',
                    share_addr TEXT NOT NULL DEFAULT '',
                    origin_node_guid TEXT NOT NULL DEFAULT '',
                    disable_flow INTEGER NOT NULL DEFAULT 0
                );
                CREATE TABLE clients(
                    id INTEGER PRIMARY KEY,
                    email TEXT NOT NULL UNIQUE,
                    sub_id TEXT NOT NULL,
                    uuid TEXT NOT NULL,
                    password TEXT NOT NULL DEFAULT '',
                    auth TEXT NOT NULL DEFAULT '',
                    flow TEXT NOT NULL DEFAULT '',
                    security TEXT NOT NULL DEFAULT '',
                    enable INTEGER NOT NULL DEFAULT 1,
                    comment TEXT NOT NULL DEFAULT ''
                );
                CREATE TABLE client_inbounds(
                    client_id INTEGER NOT NULL,
                    inbound_id INTEGER NOT NULL,
                    flow_override TEXT NOT NULL DEFAULT '',
                    created_at INTEGER NOT NULL DEFAULT 0,
                    PRIMARY KEY(client_id, inbound_id)
                );
                """
            )
            tcp_settings = {
                "clients": [{"id": CLIENT_UUID, "email": CLIENT_EMAIL, "flow": "xtls-rprx-vision"}],
                "decryption": "none",
            }
            reality = {
                "show": False,
                "xver": 0,
                "target": "127.0.0.1:443",
                "serverNames": ["example.invalid"],
                "privateKey": "test-reality-private-key",
                "shortIds": ["0102030405060708"],
                "settings": {
                    "publicKey": "test-reality-public-key",
                    "fingerprint": "chrome",
                    "serverName": "example.invalid",
                    "spiderX": "/",
                },
            }
            stream = {"network": "tcp", "security": "reality", "tcpSettings": {}, "realitySettings": reality}
            sniffing = {"enabled": True, "destOverride": ["http", "tls", "quic"], "routeOnly": False}
            rows = [
                (41, "Aimili Gateway agw-slot-one VLESS", 20000, "vless", "agw-slot-one-vless"),
                (42, "Aimili Gateway agw-slot-one mixed", 30000, "mixed", "agw-slot-one-mixed"),
                (43, "Unmanaged", 24443, "vless", "unmanaged-inbound"),
                (44, "Aimili Reality", 8443, "vless", "aimili-reality"),
            ]
            for inbound_id, remark, port, protocol, tag in rows:
                settings = tcp_settings
                selected_stream = stream
                if protocol == "mixed":
                    settings = {"auth": "password", "accounts": [{"user": "mixed-user", "pass": "mixed-pass"}], "udp": True}
                    selected_stream = {}
                database.execute(
                    "INSERT INTO inbounds(id, remark, port, protocol, settings, stream_settings, tag, sniffing) VALUES(?,?,?,?,?,?,?,?)",
                    (
                        inbound_id,
                        remark,
                        port,
                        protocol,
                        json.dumps(settings, sort_keys=True),
                        json.dumps(selected_stream, sort_keys=True),
                        tag,
                        json.dumps(sniffing, sort_keys=True),
                    ),
                )
            database.execute(
                "INSERT INTO clients(id,email,sub_id,uuid,password,auth,flow,security,enable,comment) VALUES(1,?,?,?,?,?,?,?,?,?)",
                (CLIENT_EMAIL, "stable-sub-id", CLIENT_UUID, "unrelated-password", EXISTING_AUTH, "xtls-rprx-vision", "auto", 1, "unchanged"),
            )
            database.execute("INSERT INTO client_inbounds(client_id,inbound_id) VALUES(1,41)")
            database.execute("INSERT INTO client_inbounds(client_id,inbound_id) VALUES(1,44)")
            database.execute(
                "INSERT INTO clients(id,email,sub_id,uuid,password,auth,flow,security,enable,comment) VALUES(2,?,?,?,?,?,?,?,?,?)",
                ("unmanaged-client", "other-sub-id", "22222222-2222-4222-8222-222222222222", "keep-password", "keep-auth", "", "auto", 1, "must stay byte-identical"),
            )
            database.execute("INSERT INTO client_inbounds(client_id,inbound_id) VALUES(2,43)")

    def _write_runtime_config(self):
        document = {
            "log": {"loglevel": "warning"},
            "inbounds": [
                {"tag": "api", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door", "settings": {"address": "127.0.0.1"}},
                {"tag": "agw-slot-one-vless", "port": 20000, "protocol": "vless"},
                {"tag": "aimili-reality", "port": 8443, "protocol": "vless"},
                {"tag": "unmanaged-inbound", "port": 24443, "protocol": "vless"},
            ],
            "outbounds": [
                {"tag": "agw-slot-one-socks", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 17928}]}},
                {"tag": "aimili-socks", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 7928}]}},
                {"tag": "direct", "protocol": "freedom"},
            ],
            "routing": {
                "rules": [
                    {"type": "field", "inboundTag": ["agw-slot-one-vless"], "outboundTag": "agw-slot-one-socks"},
                    {"type": "field", "inboundTag": ["aimili-reality"], "outboundTag": "aimili-socks"},
                    {"type": "field", "inboundTag": ["unmanaged-inbound"], "outboundTag": "direct"},
                ]
            },
        }
        self.runtime_config_path.write_text(json.dumps(document, sort_keys=True), encoding="utf-8")

    def _request(self, **overrides):
        request = {
            "operationId": OPERATION_ID,
            "egressId": "agw-slot-one",
            "inboundId": 41,
            "inboundTag": "agw-slot-one-vless",
            "port": 20000,
            "oldMode": TCP,
            "newMode": XHTTP,
        }
        request["expectedFingerprint"] = MODULE.request_fingerprint(request)
        request.update(overrides)
        if not overrides.get("expectedFingerprint") and any(key != "expectedFingerprint" for key in overrides):
            material = {key: value for key, value in request.items() if key != "expectedFingerprint"}
            request["expectedFingerprint"] = MODULE.request_fingerprint(material)
        return request

    def _manager(self, runner=None, fault_injector=None, token_factory=None):
        return MODULE.ProtocolTransactionManager(
            self.config,
            runner or FakeRunner(),
            fault_injector=fault_injector,
            token_factory=token_factory,
        )

    def _inbound_row(self, inbound_id=41):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            return database.execute(
                "SELECT id,remark,port,protocol,settings,stream_settings,tag,sniffing,disable_flow FROM inbounds WHERE id=?",
                (inbound_id,),
            ).fetchone()

    def _client_row(self, client_id):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            return database.execute("SELECT * FROM clients WHERE id=?", (client_id,)).fetchone()

    def test_request_contract_rejects_unknown_fields_paths_credentials_and_bad_ids(self):
        manager = self._manager()
        mutations = [
            {"xrayJson": {}},
            {"databasePath": "/tmp/other.db"},
            {"auth": "caller-secret"},
            {"firewallRule": "allow 443/udp"},
            {"operationId": "../escape"},
            {"expectedFingerprint": "not-a-sha256"},
        ]
        for mutation in mutations:
            with self.subTest(mutation=next(iter(mutation))):
                with self.assertRaisesRegex(MODULE.TransactionError, "invalid_request"):
                    manager.validate_request(self._request(**mutation))

    def test_rejects_missing_xray_binary(self):
        config = MODULE.ProtocolTransactionConfig(**{**self.config.as_dict(), "xray_binary": self.root / "missing-xray"})
        with self.assertRaisesRegex(MODULE.TransactionError, "unsafe_path"):
            MODULE.ProtocolTransactionManager(config, FakeRunner()).validate_request(self._request())

    def test_rejects_mixed_unknown_non_gateway_and_non_whitelisted_targets(self):
        manager = self._manager()
        requests = [
            self._request(inboundId=42, inboundTag="agw-slot-one-mixed", port=30000),
            self._request(inboundTag="unknown-tag"),
            self._request(inboundId=43, inboundTag="unmanaged-inbound", port=24443, egressId="agw-unmanaged"),
            self._request(port=443),
        ]
        for request in requests:
            with self.subTest(tag=request["inboundTag"], port=request["port"]):
                with self.assertRaisesRegex(MODULE.TransactionError, "ownership_conflict|invalid_request"):
                    manager.validate_request(request)

    def test_rejects_symlinked_database_runtime_config_and_snapshot_root(self):
        if not hasattr(os, "symlink"):
            self.skipTest("platform does not support symlinks")
        for field_name, source in (
            ("database_path", self.database_path),
            ("runtime_config_path", self.runtime_config_path),
        ):
            link = self.root / (field_name + ".link")
            try:
                os.symlink(source, link)
            except OSError:
                self.skipTest("symlink creation is unavailable")
            config = MODULE.ProtocolTransactionConfig(**{**self.config.as_dict(), field_name: link})
            with self.subTest(field=field_name):
                with self.assertRaisesRegex(MODULE.TransactionError, "unsafe_path"):
                    MODULE.ProtocolTransactionManager(config, FakeRunner()).validate_request(self._request())

    def test_owned_main_requires_exact_legacy_binding_and_main_socks_chain(self):
        main = self._request(
            egressId="agw-main",
            inboundId=44,
            inboundTag="aimili-reality",
            port=8443,
        )
        self._manager().validate_request(main)
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.execute("UPDATE inbounds SET remark='not legacy main' WHERE id=44")
        with self.assertRaisesRegex(MODULE.TransactionError, "ownership_conflict"):
            self._manager().validate_request(main)

    def test_tcp_mode_rejects_disable_flow_drift(self):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.execute("UPDATE inbounds SET disable_flow=1 WHERE id=41")
        with self.assertRaisesRegex(MODULE.TransactionError, "managed_resource_drift|expected_state_mismatch"):
            self._manager().validate_request(self._request())

    def test_tcp_mode_rejects_missing_global_vision_flow(self):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.execute("UPDATE clients SET flow='' WHERE id=1")
        with self.assertRaisesRegex(MODULE.TransactionError, "managed_resource_drift"):
            self._manager().validate_request(self._request())

    def test_template_tcp_and_xhttp_preserve_reality_identity_and_select_flow(self):
        manager = self._manager()
        source = manager.load_target(self._request())
        xhttp = manager.build_template(source, XHTTP)
        self.assertEqual("vless", xhttp["protocol"])
        self.assertEqual("xhttp", xhttp["streamSettings"]["network"])
        self.assertEqual("reality", xhttp["streamSettings"]["security"])
        self.assertEqual("auto", xhttp["streamSettings"]["xhttpSettings"]["mode"])
        self.assertTrue(xhttp["streamSettings"]["xhttpSettings"]["path"].startswith("/"))
        self.assertEqual(
            ["127.0.0.1", "::1"],
            xhttp["streamSettings"]["sockopt"]["trustedXForwardedFor"],
        )
        self.assertEqual("none", xhttp["settings"]["decryption"])
        self.assertEqual("", xhttp["settings"]["clients"][0].get("flow", ""))
        self.assertTrue(xhttp["disableFlow"])
        original_reality = json.loads(source["row"]["stream_settings"])["realitySettings"]
        self.assertEqual(original_reality, xhttp["streamSettings"]["realitySettings"])

        tcp = manager.build_template(source, TCP)
        self.assertEqual("tcp", tcp["streamSettings"]["network"])
        self.assertEqual("xtls-rprx-vision", tcp["settings"]["clients"][0]["flow"])
        self.assertFalse(tcp["disableFlow"])

    def test_hysteria_template_uses_tls_files_and_independent_existing_auth(self):
        manager = self._manager(token_factory=lambda: "new-auth-must-not-replace-existing")
        source = manager.load_target(self._request())
        template = manager.build_template(source, HYSTERIA2)
        self.assertEqual("hysteria", template["protocol"])
        self.assertEqual({"version": 2, "clients": [{"auth": EXISTING_AUTH, "email": CLIENT_EMAIL}]}, template["settings"])
        self.assertEqual("hysteria", template["streamSettings"]["network"])
        self.assertEqual(2, template["streamSettings"]["hysteriaSettings"]["version"])
        self.assertEqual("tls", template["streamSettings"]["security"])
        certificate = template["streamSettings"]["tlsSettings"]["certificates"][0]
        self.assertEqual(str(self.certificate_path), certificate["certificateFile"])
        self.assertEqual(str(self.private_key_path), certificate["keyFile"])
        self.assertFalse(certificate["oneTimeLoading"])
        self.assertNotEqual(CLIENT_UUID, template["settings"]["clients"][0]["auth"])

    def test_hysteria_generates_auth_only_when_missing_and_preserves_other_client_columns(self):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.execute("UPDATE clients SET auth='' WHERE id=1")
        before_unmanaged = self._client_row(2)
        manager = self._manager(token_factory=lambda: "generated-independent-auth")
        source = manager.load_target(self._request())
        template = manager.build_template(source, HYSTERIA2)
        self.assertEqual("generated-independent-auth", template["settings"]["clients"][0]["auth"])
        manager.persist_template(source, template)
        with closing(sqlite3.connect(self.database_path)) as database, database:
            managed = database.execute("SELECT uuid,password,auth,flow,security,enable,comment FROM clients WHERE id=1").fetchone()
        self.assertEqual((CLIENT_UUID, "unrelated-password", "generated-independent-auth", "xtls-rprx-vision", "auto", 1, "unchanged"), managed)
        self.assertEqual(before_unmanaged, self._client_row(2))

    def test_apply_keeps_snapshot_with_root_only_permissions_until_finalize(self):
        manager = self._manager()
        result = manager.apply(self._request())
        self.assertEqual({"operationId": OPERATION_ID, "status": "applied", "errorCode": ""}, result)
        operation_dir = self.snapshot_dir / OPERATION_ID
        snapshot = operation_dir / "snapshot.json"
        self.assertTrue(snapshot.is_file())
        if os.name != "nt":
            self.assertEqual(0o700, stat.S_IMODE(operation_dir.stat().st_mode))
            self.assertEqual(0o600, stat.S_IMODE(snapshot.stat().st_mode))
        manager.finalize(OPERATION_ID)
        self.assertFalse(operation_dir.exists())

    def test_apply_order_is_snapshot_offline_rmi_adi_database_and_verify(self):
        events = []
        runner = FakeRunner()

        class RecordingRunner(FakeRunner):
            def _record(self, name, value=None):
                events.append(name)
                super()._record(name, value)

        runner = RecordingRunner()
        manager = self._manager(runner=runner, fault_injector=lambda phase: events.append(phase))
        manager.apply(self._request())
        expected = ["snapshot", "offline_test", "offline_test", "runtime_remove", "rmi", "runtime_add", "adi", "database", "database_committed", "verify", "lsi"]
        self.assertEqual(expected, events)
        self.assertEqual("agw-slot-one-vless", runner.calls[1][1])
        self.assertEqual("agw-slot-one-vless", runner.calls[2][1]["tag"])
        forbidden = json.dumps(runner.calls, default=str)
        self.assertNotIn("panel/api/inbounds", forbidden)
        self.assertNotIn("restart", forbidden.lower())

    def test_non_target_database_rows_and_runtime_config_remain_byte_identical(self):
        unmanaged_before = self._inbound_row(43)
        mixed_before = self._inbound_row(42)
        client_before = self._client_row(2)
        runtime_before = self.runtime_config_path.read_bytes()
        self._manager().apply(self._request())
        self.assertEqual(unmanaged_before, self._inbound_row(43))
        self.assertEqual(mixed_before, self._inbound_row(42))
        self.assertEqual(client_before, self._client_row(2))
        self.assertEqual(runtime_before, self.runtime_config_path.read_bytes())

    def test_hot_add_database_and_fingerprint_failures_restore_old_runtime_and_database(self):
        original = self._inbound_row(41)
        for phase in ("adi", "database", "verify"):
            with self.subTest(phase=phase):
                self._restore_original_fixture(original)
                runner = FakeRunner(fail_at=phase if phase == "adi" else None)

                def fault(current):
                    if current == phase:
                        raise MODULE.TransactionError(phase + "_failed")

                manager = self._manager(runner=runner, fault_injector=fault)
                with self.assertRaisesRegex(MODULE.TransactionError, phase + "_failed"):
                    manager.apply(self._request())
                self.assertEqual(original, self._inbound_row(41))
                self.assertIn("agw-slot-one-vless", runner.runtime_tags)
                operation_dir = self.snapshot_dir / OPERATION_ID
                self.assertFalse(operation_dir.exists())

    def test_database_lock_before_commit_restores_runtime_without_false_repair_state(self):
        original = self._inbound_row(41)
        runner = FakeRunner()
        locked = sqlite3.connect(self.database_path, timeout=0.1)
        try:
            locked.execute("BEGIN IMMEDIATE")
            with self.assertRaisesRegex(MODULE.TransactionError, "database_write_failed"):
                self._manager(runner=runner).apply(self._request())
        finally:
            locked.rollback()
            locked.close()
        self.assertEqual(original, self._inbound_row(41))
        self.assertIn("agw-slot-one-vless", runner.runtime_tags)
        self.assertFalse((self.snapshot_dir / OPERATION_ID).exists())

    def _restore_original_fixture(self, row):
        with closing(sqlite3.connect(self.database_path)) as database, database:
            database.execute(
                "UPDATE inbounds SET remark=?,port=?,protocol=?,settings=?,stream_settings=?,tag=?,sniffing=?,disable_flow=? WHERE id=?",
                (row[1], row[2], row[3], row[4], row[5], row[6], row[7], row[8], row[0]),
            )
        operation_dir = self.snapshot_dir / OPERATION_ID
        if operation_dir.exists():
            for child in operation_dir.iterdir():
                child.unlink()
            operation_dir.rmdir()

    def test_rollback_failure_returns_safe_error_and_keeps_snapshot(self):
        runner = FakeRunner(fail_at="adi", fail_always=True)
        manager = self._manager(runner=runner)
        with self.assertRaisesRegex(MODULE.TransactionError, "rollback_failed") as raised:
            manager.apply(self._request())
        self.assertNotIn(CLIENT_UUID, str(raised.exception))
        self.assertTrue((self.snapshot_dir / OPERATION_ID / "snapshot.json").is_file())

    def test_explicit_rollback_restores_applied_transaction(self):
        original = self._inbound_row(41)
        manager = self._manager()
        manager.apply(self._request())
        self.assertNotEqual(original, self._inbound_row(41))
        result = manager.rollback(OPERATION_ID)
        self.assertEqual({"operationId": OPERATION_ID, "status": "rolled_back", "errorCode": ""}, result)
        self.assertEqual(original, self._inbound_row(41))
        self.assertFalse((self.snapshot_dir / OPERATION_ID).exists())

    def test_explicit_rollback_without_snapshot_reports_not_applied(self):
        manager = self._manager()

        with self.assertRaises(MODULE.TransactionError) as raised:
            manager.rollback(OPERATION_ID)

        self.assertEqual("operation_not_applied", raised.exception.code)

    def test_recover_pending_restores_crashed_transactions(self):
        manager = self._manager()

        def crash(phase):
            if phase == "database_committed":
                raise MODULE.SimulatedCrash()

        manager = self._manager(fault_injector=crash)
        with self.assertRaises(MODULE.SimulatedCrash):
            manager.apply(self._request())
        recovered = self._manager().recover_pending()
        self.assertEqual([{"operationId": OPERATION_ID, "status": "rolled_back", "errorCode": ""}], recovered)
        self.assertEqual("vless", self._inbound_row(41)[3])

    def test_recover_pending_rolls_back_applied_but_unfinalized_transaction(self):
        original = self._inbound_row(41)
        manager = self._manager()
        manager.apply(self._request())
        self.assertNotEqual(original, self._inbound_row(41))
        recovered = manager.recover_pending()
        self.assertEqual([{"operationId": OPERATION_ID, "status": "rolled_back", "errorCode": ""}], recovered)
        self.assertEqual(original, self._inbound_row(41))

    def test_recover_pending_respects_gateway_commit_lease(self):
        manager = self._manager()
        manager.apply(self._request())
        snapshot_path = self.snapshot_dir / OPERATION_ID / "snapshot.json"

        self.assertEqual([], manager.recover_pending(min_age_seconds=180))
        self.assertTrue(snapshot_path.exists())

        snapshot = json.loads(snapshot_path.read_text(encoding="utf-8"))
        snapshot["createdAt"] = int(time.time()) - 181
        snapshot_path.write_text(json.dumps(snapshot), encoding="utf-8")
        recovered = manager.recover_pending(min_age_seconds=180)
        self.assertEqual([{"operationId": OPERATION_ID, "status": "rolled_back", "errorCode": ""}], recovered)
        self.assertFalse(snapshot_path.exists())

    def test_request_fingerprint_is_canonical_and_excludes_no_secret_material(self):
        left = {"operationId": OPERATION_ID, "egressId": "agw-slot-one", "inboundId": 41, "inboundTag": "agw-slot-one-vless", "port": 20000, "oldMode": TCP, "newMode": XHTTP}
        right = dict(reversed(list(left.items())))
        expected = hashlib.sha256(json.dumps(left, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        self.assertEqual(expected, MODULE.request_fingerprint(left))
        self.assertEqual(expected, MODULE.request_fingerprint(right))


if __name__ == "__main__":
    unittest.main()
