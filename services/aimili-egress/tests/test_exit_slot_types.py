import json
import threading
import time
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as manager
from egress_repair import RepairStore


class ExitSlotTypeTests(unittest.TestCase):
    def setUp(self):
        validator = mock.patch.object(
            manager,
            "validated_repair_candidate",
            side_effect=lambda candidates: candidates[0] if candidates else None,
        )
        replenisher = mock.patch.object(
            manager,
            "replenish_repair_country",
            return_value={"state": "completed", "resultCode": "no_usable_nodes"},
        )
        validator.start()
        replenisher.start()
        self.addCleanup(validator.stop)
        self.addCleanup(replenisher.stop)
        self.validator_patch = validator

    def test_main_failure_waits_for_hot_standby_without_cold_dial(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_main", return_value=False
        ), mock.patch.object(manager, "connect_node") as connect, mock.patch.object(manager, "set_state"), mock.patch.object(manager, "mark_main_bad_node"):
            result = manager.repair_main_once({"candidate_id": "jp-old", "country": "JP"})
        self.assertEqual(result["error_code"], "recovery_pending")
        connect.assert_not_called()
        repair_store.require_manual.assert_not_called()
        repair_store.wait_for_standby.assert_called_once_with("main", "jp-old", "JP")

    def test_main_fault_does_not_block_on_country_collection(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_main", return_value=False
        ), mock.patch.object(manager, "replenish_repair_country") as replenish, mock.patch.object(manager, "set_state"), mock.patch.object(manager, "mark_main_bad_node"):
            result = manager.repair_main_once({"candidate_id": "jp-old", "country": "JP"})
        self.assertEqual(result["error_code"], "recovery_pending")
        replenish.assert_not_called()

    def test_main_promotes_validated_standby_without_cold_dial(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_main", return_value=True
        ), mock.patch.object(manager, "connect_node") as connect, mock.patch.object(manager, "mark_main_bad_node"):
            result = manager.repair_main_once({"candidate_id": "mv-old", "country": "MV"})
        self.assertTrue(result["ok"])
        self.assertTrue(result["standby_promoted"])
        connect.assert_not_called()

    def test_main_legacy_region_mode_uses_shared_recovery_queue(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_main", return_value=False
        ), mock.patch.object(manager, "load_ui_config", return_value={"routing_mode": "fixed_region"}), mock.patch.object(manager, "set_state"), mock.patch.object(manager, "mark_main_bad_node"):
            result = manager.repair_main_once({"candidate_id": "mv-old", "country": "MV"})
        self.assertEqual(result["error_code"], "recovery_pending")
        repair_store.wait_for_standby.assert_called_once_with("main", "mv-old", "MV")

    def test_repair_candidate_validation_rejects_stale_candidate_before_switch(self):
        self.validator_patch.stop()
        first = {"id": "jp-stale", "probe_status": "available"}
        second = {"id": "jp-live", "probe_status": "available"}
        results = [
            {**first, "probe_status": "unavailable"},
            {**second, "probe_status": "available", "exit_ip": "198.51.100.50"},
        ]
        with (
            mock.patch.object(manager, "probe_nodes", return_value=results),
            mock.patch.object(manager, "mark_candidate_unavailable") as mark,
        ):
            selected = manager.validated_repair_candidate([first, second])

        self.assertEqual(selected["id"], "jp-live")
        mark.assert_called_once_with("jp-stale", "candidate_egress_failed")

    def test_main_automatic_repair_does_not_repeat_after_restart_record(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {"status": "manual_required"}
        with (
            mock.patch.object(manager, "egress_repair_store", repair_store),
            mock.patch.object(manager, "connect_node") as connect,
        ):
            result = manager.repair_main_once(
                {"candidate_id": "jp-old", "country": "JP"},
            )

        self.assertEqual(result["error_code"], "manual_repair_required")
        connect.assert_not_called()

    def test_main_restart_uses_persisted_manual_state_and_never_dials_a_second_candidate(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            before_restart = RepairStore(path)
            self.assertTrue(before_restart.claim("main", "jp-old", "JP"))
            before_restart.require_manual("main", "replacement_failed", "jp-first")
            after_restart = RepairStore(path)

            with (
                mock.patch.object(manager, "egress_repair_store", after_restart),
                mock.patch.object(manager, "automatic_main_candidates") as candidates,
                mock.patch.object(manager, "connect_node") as connect,
            ):
                result = manager.repair_main_once(
                    {"candidate_id": "jp-old", "country": "JP"},
                )

            self.assertEqual(result["error_code"], "manual_repair_required")
            candidates.assert_not_called()
            connect.assert_not_called()

    def test_main_automatic_candidates_keep_country_and_prefer_residential(self):
        nodes = [
            {"id": "jp-dc", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 1},
            {"id": "us-home", "country_short": "US", "ip_type": "residential", "probe_status": "available", "latency_ms": 1},
            {"id": "jp-home", "country_short": "JP", "ip_type": "residential", "probe_status": "available", "latency_ms": 50},
        ]
        with (
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "main_bad_node_ids", return_value=set()),
        ):
            result = manager.automatic_main_candidates("JP")

        self.assertEqual([node["id"] for node in result], ["jp-home", "jp-dc"])

    def test_main_automatic_candidates_accept_all_countries_for_auto_fallback(self):
        nodes = [
            {"id": "mv-dead", "country_short": "MV", "ip_type": "residential", "probe_status": "unavailable"},
            {"id": "jp-home", "country_short": "JP", "ip_type": "residential", "probe_status": "available"},
            {"id": "kr-dc", "country_short": "KR", "ip_type": "hosting", "probe_status": "available"},
        ]
        with (
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "main_bad_node_ids", return_value=set()),
        ):
            result = manager.automatic_main_candidates("")

        self.assertEqual([node["id"] for node in result], ["jp-home", "kr-dc"])

    def test_no_route_to_host_is_a_persistable_candidate_dial_failure(self):
        code, message = manager.vpn_utils.diagnose_openvpn_failure([
            "TCP: connect to remote failed: No route to host",
            "Exiting due to fatal error",
        ])

        self.assertEqual(code, 2004)
        self.assertIn("ERR_OVPN_NO_ROUTE_TO_HOST", message)
        self.assertEqual(
            manager._candidate_dial_failure_code(message),
            "candidate_dial_failed",
        )

    def test_diagnosed_remote_timeout_is_a_persistable_candidate_dial_failure(self):
        code, message = manager.vpn_utils.diagnose_openvpn_failure([
            "TCP: connect to remote failed: Connection timed out",
            "Exiting due to fatal error",
        ])

        self.assertEqual(code, 2004)
        self.assertIn("ERR_OVPN_NODE_UNREACHABLE", message)
        self.assertEqual(
            manager._candidate_dial_failure_code(message),
            "candidate_dial_failed",
        )

    def test_connection_reset_is_a_persistable_candidate_dial_failure(self):
        for reset_marker in (
            "Connection reset, restarting [0]",
            "SIGUSR1[soft,connection-reset] received",
        ):
            with self.subTest(reset_marker=reset_marker):
                code, message = manager.vpn_utils.diagnose_openvpn_failure([
                    reset_marker,
                    "Exiting due to fatal error",
                ])

                self.assertEqual(code, 2004)
                self.assertIn("ERR_OVPN_NODE_UNREACHABLE", message)
                self.assertEqual(
                    manager._candidate_dial_failure_code(message),
                    "candidate_dial_failed",
                )

    def test_local_permission_or_option_errors_override_connection_reset(self):
        cases = (
            (
                "permission",
                "permission denied while opening TUN device",
                "ERR_OVPN_PERMISSION_DENIED",
            ),
            (
                "options",
                "Options error: --route-nopull is incompatible",
                "ERR_OVPN_ROUTE_NOPULL",
            ),
        )
        for name, local_error, expected_marker in cases:
            with self.subTest(name=name):
                _code, message = manager.vpn_utils.diagnose_openvpn_failure([
                    local_error,
                    "SIGUSR1[soft,connection-reset] received",
                    "Exiting due to fatal error",
                ])

                self.assertIn(expected_marker, message)
                self.assertEqual(manager._candidate_dial_failure_code(message), "")

    def test_mark_candidate_unavailable_persists_blacklist_and_pool_state(self):
        candidate = {
            "id": "jp-stale",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.30",
            "exit_ip": "203.0.113.30",
            "exit_ip_checked_at": 90.0,
            "ip_type": "hosting",
            "probe_status": "available",
            "config_text": "client",
        }
        with tempfile.TemporaryDirectory() as directory:
            nodes_file = Path(directory) / "nodes.json"
            blacklist_file = Path(directory) / "blacklist.json"
            nodes_file.write_text(json.dumps([candidate]), encoding="utf-8")
            blacklist_file.write_text("{}", encoding="utf-8")
            with (
                mock.patch.object(manager, "NODES_FILE", nodes_file),
                mock.patch.object(manager, "BLACKLIST_FILE", blacklist_file),
                mock.patch.object(manager, "active_openvpn_node_id", ""),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            ):
                checked_at = time.time()
                changed = manager.mark_candidate_unavailable(
                    "jp-stale", "candidate_dial_failed", now=checked_at
                )
                reloaded = json.loads(nodes_file.read_text(encoding="utf-8"))
                candidates_after_restart = manager.safe_candidate_snapshot()

            blacklist = json.loads(blacklist_file.read_text(encoding="utf-8"))

        self.assertTrue(changed)
        self.assertEqual(reloaded[0]["probe_status"], "unavailable")
        self.assertEqual(reloaded[0]["probe_message"], "candidate_dial_failed")
        self.assertNotIn("exit_ip", reloaded[0])
        self.assertEqual(blacklist["jp-stale"]["reason_code"], "candidate_dial_failed")
        self.assertEqual(candidates_after_restart, [])

    def test_mark_candidate_unavailable_rejects_non_candidate_failures(self):
        with (
            mock.patch.object(manager, "read_nodes") as read,
            mock.patch.object(manager, "write_json") as write,
        ):
            changed = manager.mark_candidate_unavailable(
                "jp-one", "gateway_validation_failed", now=100.0
            )

        self.assertFalse(changed)
        read.assert_not_called()
        write.assert_not_called()

    def test_blacklist_remains_authoritative_when_nodes_write_crashes(self):
        candidate = {
            "id": "jp-crash",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.31",
            "ip_type": "hosting",
            "probe_status": "available",
            "config_file": "jp-crash.ovpn",
            "config_text": "client",
        }
        original_write_json = manager.write_json
        with tempfile.TemporaryDirectory() as directory:
            nodes_file = Path(directory) / "nodes.json"
            blacklist_file = Path(directory) / "blacklist.json"
            nodes_file.write_text(json.dumps([candidate]), encoding="utf-8")
            blacklist_file.write_text("{}", encoding="utf-8")

            def crash_after_blacklist(path, payload):
                if path == nodes_file:
                    raise OSError("simulated nodes write crash")
                return original_write_json(path, payload)

            with (
                mock.patch.object(manager, "NODES_FILE", nodes_file),
                mock.patch.object(manager, "BLACKLIST_FILE", blacklist_file),
                mock.patch.object(manager, "active_openvpn_node_id", ""),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                mock.patch.object(manager, "write_json", side_effect=crash_after_blacklist),
            ):
                with self.assertRaises(OSError):
                    manager.mark_candidate_unavailable(
                        "jp-crash", "candidate_dial_failed", now=time.time()
                    )

            self.assertEqual(
                json.loads(nodes_file.read_text(encoding="utf-8"))[0]["probe_status"],
                "available",
            )
            with (
                mock.patch.object(manager, "NODES_FILE", nodes_file),
                mock.patch.object(manager, "BLACKLIST_FILE", blacklist_file),
                mock.patch.object(manager, "active_openvpn_node_id", ""),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                mock.patch.object(manager, "main_reserved_candidate_ids", return_value=set()),
                mock.patch.object(manager, "get_slot_pin_map", return_value={}),
                mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [2], "country": "JP", "residential_only": False}),
                mock.patch.object(manager, "get_active_slots", return_value=[2]),
                mock.patch.object(manager, "per_slot_country", return_value="JP"),
                mock.patch.object(manager, "per_slot_isp", return_value=""),
                mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            ):
                self.assertEqual(manager.safe_candidate_snapshot(), [])
                self.assertEqual(
                    manager.select_slot_nodes(set(), 1, "JP", False, proxy_type="datacenter"),
                    [],
                )
                self.assertIsNone(manager.pick_slot_node(2, set()))
                self.assertEqual(
                    manager.assign_node_to_slot(2, "jp-crash")["error"],
                    "未找到该节点",
                )
                self.assertEqual(
                    manager.add_slot_with_node("jp-crash")["error"],
                    "未找到该节点",
                )
                self.assertEqual(
                    manager.assign_managed_slot(2, "jp-crash", "JP", "datacenter")["error_code"],
                    "candidate_not_found",
                )

    def test_connect_node_does_not_reject_candidate_for_local_openvpn_failure(self):
        candidate = {
            "id": "jp-local-failure",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.32",
            "ip_type": "hosting",
            "probe_status": "available",
            "config_text": "client",
        }
        with tempfile.TemporaryDirectory() as directory:
            data_dir = Path(directory) / "data"
            config_dir = Path(directory) / "configs"
            config_file = config_dir / "candidate.ovpn"
            candidate["config_file"] = str(config_file)
            for failure in (
                "OpenVPN log reader thread failed",
                "permission denied while opening TUN device",
                "cannot ioctl TUNSETIFF",
                "OpenVPN timeout after 12s",
            ):
                with self.subTest(failure=failure):
                    with (
                        mock.patch.object(manager, "DATA_DIR", data_dir),
                        mock.patch.object(manager, "CONFIG_DIR", config_dir),
                        mock.patch.object(manager, "is_connecting", False),
                        mock.patch.object(manager, "active_openvpn_node_id", ""),
                        mock.patch.object(manager, "read_nodes", return_value=[candidate]),
                        mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                        mock.patch.object(manager, "load_ui_config", return_value={}),
                        mock.patch.object(manager, "set_state"),
                        mock.patch.object(manager, "log_to_json"),
                        mock.patch.object(manager, "stop_active_openvpn"),
                        mock.patch.object(
                            manager,
                            "run_openvpn_until_ready",
                            return_value=(False, failure, None),
                        ),
                        mock.patch.object(manager, "mark_candidate_unavailable") as mark,
                    ):
                        with self.assertRaises(RuntimeError):
                            manager.connect_node("jp-local-failure")

                    mark.assert_not_called()

    def test_connect_node_rejects_candidate_for_explicit_remote_refusal(self):
        candidate = {
            "id": "jp-refused",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.33",
            "ip_type": "hosting",
            "probe_status": "available",
            "config_text": "client",
        }
        with tempfile.TemporaryDirectory() as directory:
            data_dir = Path(directory) / "data"
            config_dir = Path(directory) / "configs"
            candidate["config_file"] = str(config_dir / "candidate.ovpn")
            with (
                mock.patch.object(manager, "DATA_DIR", data_dir),
                mock.patch.object(manager, "CONFIG_DIR", config_dir),
                mock.patch.object(manager, "is_connecting", False),
                mock.patch.object(manager, "active_openvpn_node_id", ""),
                mock.patch.object(manager, "read_nodes", return_value=[candidate]),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                mock.patch.object(manager, "load_ui_config", return_value={}),
                mock.patch.object(manager, "set_state"),
                mock.patch.object(manager, "log_to_json"),
                mock.patch.object(manager, "stop_active_openvpn"),
                mock.patch.object(
                    manager,
                    "run_openvpn_until_ready",
                    return_value=(False, "TCP connection refused", None),
                ),
                mock.patch.object(manager, "mark_candidate_unavailable", return_value=True) as mark,
            ):
                with self.assertRaises(manager.CandidateUnavailableError):
                    manager.connect_node("jp-refused")

        mark.assert_called_once_with("jp-refused", "candidate_dial_failed")

    def test_connection_reset_rejects_candidate_before_auto_switch_selects_standby(self):
        reset_candidate = {
            "id": "jp-reset",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.34",
            "ip_type": "hosting",
            "latency_ms": 1,
            "score": 2,
            "probe_status": "available",
            "config_text": "client",
        }
        standby_candidate = {
            "id": "jp-standby",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.35",
            "ip_type": "hosting",
            "latency_ms": 2,
            "score": 1,
            "probe_status": "available",
            "config_text": "client",
        }
        _code, reset_message = manager.vpn_utils.diagnose_openvpn_failure([
            "Connection reset, restarting [0]",
            "Exiting due to fatal error",
        ])
        with tempfile.TemporaryDirectory() as directory:
            data_dir = Path(directory) / "data"
            config_dir = Path(directory) / "configs"
            nodes_file = data_dir / "nodes.json"
            blacklist_file = data_dir / "blacklist.json"
            data_dir.mkdir()
            reset_candidate["config_file"] = str(config_dir / "reset.ovpn")
            nodes_file.write_text(
                json.dumps([reset_candidate, standby_candidate]), encoding="utf-8"
            )
            blacklist_file.write_text("{}", encoding="utf-8")
            settings = {"connection_enabled": True, "routing_mode": "auto", "routing_ip_type": "all"}
            with (
                mock.patch.object(manager, "DATA_DIR", data_dir),
                mock.patch.object(manager, "CONFIG_DIR", config_dir),
                mock.patch.object(manager, "NODES_FILE", nodes_file),
                mock.patch.object(manager, "BLACKLIST_FILE", blacklist_file),
                mock.patch.object(manager, "is_connecting", False),
                mock.patch.object(manager, "active_openvpn_node_id", ""),
                mock.patch.object(manager, "main_mutation_allowed", return_value=True),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                mock.patch.object(manager, "load_ui_config", return_value=settings),
                mock.patch.object(manager, "set_state"),
                mock.patch.object(manager, "log_to_json"),
                mock.patch.object(manager, "stop_active_openvpn"),
                mock.patch.object(
                    manager,
                    "mark_candidate_unavailable",
                    wraps=manager.mark_candidate_unavailable,
                ) as mark,
                mock.patch.object(
                    manager,
                    "run_openvpn_until_ready",
                    return_value=(False, reset_message, None),
                ),
            ):
                with self.assertRaises(manager.CandidateUnavailableError):
                    manager.connect_node("jp-reset")
                persisted = json.loads(nodes_file.read_text(encoding="utf-8"))
                blacklist = json.loads(blacklist_file.read_text(encoding="utf-8"))
                with mock.patch.object(manager, "connect_node") as connect:
                    manager.auto_switch_node()

        mark.assert_called_once_with("jp-reset", "candidate_dial_failed")
        self.assertEqual(blacklist["jp-reset"]["reason_code"], "candidate_dial_failed")
        self.assertEqual([node["id"] for node in persisted], ["jp-standby"])
        connect.assert_called_once_with("jp-standby")
    def test_openvpn_command_tolerates_short_host_link_flaps(self):
        with (
            mock.patch.object(manager, "OPENVPN_CONNECT_RETRY_MAX", 3),
            mock.patch.object(manager, "get_openvpn_version", return_value=2.6),
            mock.patch.object(manager.os.path, "exists", return_value=False),
        ):
            command = manager.openvpn_command("candidate.ovpn", route_nopull=True)

        retry_index = command.index("--connect-retry-max")
        self.assertEqual(command[retry_index + 1], "3")
        self.assertIn("--route-nopull", command)

    def test_ensure_policy_routing_repairs_only_when_missing(self):
        with (
            mock.patch.object(manager, "policy_routing_ready", side_effect=[True, False, True]) as ready,
            mock.patch.object(manager, "setup_policy_routing") as setup,
        ):
            self.assertTrue(manager.ensure_policy_routing("tun122", 202))
            setup.assert_not_called()
            self.assertTrue(manager.ensure_policy_routing("tun122", 202))

        self.assertEqual(ready.call_count, 3)
        setup.assert_called_once_with("tun122", 202)

    def test_check_managed_slot_repairs_route_before_egress_probe(self):
        snapshots = [
            {"ok": True, "port": 17930, "status": "up"},
            {"ok": True, "port": 17930, "status": "up"},
        ]
        runtime = {2: {"slot": 2}}
        with (
            mock.patch.object(manager, "managed_slot_snapshot", side_effect=snapshots),
            mock.patch.object(manager, "slot_process_alive", return_value=True),
            mock.patch.object(manager, "ensure_policy_routing", return_value=True) as ensure,
            mock.patch.object(manager, "check_slot_egress", return_value=(True, "198.51.100.20")) as probe,
            mock.patch.object(manager.egress_repair_store, "mark_healthy"),
            mock.patch.object(manager, "exit_slots", runtime),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.check_managed_slot(2)

        ensure.assert_called_once_with("tun122", 202)
        probe.assert_called_once_with(17930)
        self.assertTrue(result["egress_ok"])

    def test_normalize_proxy_type_maps_only_supported_categories(self):
        cases = {
            "residential": "residential",
            "mobile": "residential",
            "hosting": "datacenter",
            "proxy": "datacenter",
            "": "",
            "unknown": "",
        }
        for raw, expected in cases.items():
            with self.subTest(raw=raw):
                self.assertEqual(manager.normalize_proxy_type(raw), expected)

    def test_safe_candidate_snapshot_excludes_unavailable_and_secret_fields(self):
        available = {
            "id": "node-ok",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.10",
            "ip_type": "mobile",
            "owner": "Example ISP",
            "asn": "AS64500",
            "as_name": "Example",
            "latency_ms": 42,
            "score": 123,
            "probe_status": "available",
            "last_probe_at": 1_700_000_000,
            "exit_ip": "203.0.113.10",
            "exit_ip_checked_at": 1_700_000_005,
            "config_text": "secret openvpn profile",
            "config_file": "secret.ovpn",
        }
        unavailable = dict(available, id="node-bad", probe_status="unavailable")

        with mock.patch.object(manager, "read_nodes", return_value=[available, unavailable]):
            actual = manager.safe_candidate_snapshot()

        self.assertEqual(
            actual,
            [
                {
                    "id": "node-ok",
                    "country_short": "JP",
                    "country": "Japan",
                    "ip": "198.51.100.10",
                    "proxy_type": "residential",
                    "owner": "Example ISP",
                    "asn": "AS64500",
                    "as_name": "Example",
                    "latency_ms": 42,
                    "score": 123,
                    "probe_status": "available",
                    "last_probe_at": 1_700_000_000,
                    "exit_ip": "203.0.113.10",
                    "exit_ip_checked_at": 1_700_000_005,
                }
            ],
        )

    def test_select_slot_nodes_enforces_country_and_proxy_type(self):
        nodes = [
            {"id": "jp-home", "country_short": "JP", "ip_type": "mobile", "probe_status": "available", "latency_ms": 20, "score": 2},
            {"id": "jp-dc", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 10, "score": 3},
            {"id": "us-dc", "country_short": "US", "ip_type": "hosting", "probe_status": "available", "latency_ms": 5, "score": 4},
            {"id": "jp-unknown", "country_short": "JP", "ip_type": "unknown", "probe_status": "available", "latency_ms": 1, "score": 5},
        ]

        with mock.patch.object(manager, "read_nodes", return_value=nodes):
            residential = manager.select_slot_nodes(set(), 4, "JP", False, proxy_type="residential")
            datacenter = manager.select_slot_nodes(set(), 4, "JP", False, proxy_type="datacenter")

        self.assertEqual([item["id"] for item in residential], ["jp-home"])
        self.assertEqual([item["id"] for item in datacenter], ["jp-dc"])

    def test_rotate_slot_keeps_its_proxy_type_constraint(self):
        nodes = [
            {"id": "jp-home", "country_short": "JP", "country": "Japan", "ip": "198.51.100.20", "ip_type": "mobile", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "jp-dc", "country_short": "JP", "country": "Japan", "ip": "198.51.100.30", "ip_type": "hosting", "probe_status": "available", "latency_ms": 20, "score": 2},
        ]
        with (
            mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [0], "paused": [], "residential_only": True}),
            mock.patch.object(manager, "set_slot_pin"),
            mock.patch.object(manager, "current_slot_node_ids", return_value={"old-node"}),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
            mock.patch.object(manager, "per_slot_country", return_value="JP"),
            mock.patch.object(manager, "per_slot_isp", return_value=""),
            mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "bring_up_slot", return_value=True),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.switch_slot_node(0)

        self.assertTrue(result["ok"])
        self.assertEqual(result["ip"], "198.51.100.30")

    def test_supervisor_prefers_explicit_slot_type_over_global_residential_filter(self):
        nodes = [
            {"id": "jp-home", "country_short": "JP", "ip_type": "mobile", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "jp-dc", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 20, "score": 2},
        ]
        with (
            mock.patch.object(manager, "get_slot_pin_map", return_value={}),
            mock.patch.object(manager, "get_exit_slot_config", return_value={"residential_only": True}),
            mock.patch.object(manager, "per_slot_country", return_value="JP"),
            mock.patch.object(manager, "per_slot_isp", return_value=""),
            mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
        ):
            selected = manager.pick_slot_node(0, set())

        self.assertEqual(selected["id"], "jp-dc")

    def test_pick_slot_node_prefers_saved_reconnect_hint(self):
        nodes = [
            {"id": "fast-new", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "saved-node", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 20, "score": 2},
        ]
        with (
            mock.patch.object(manager, "slot_reconnect_hints", {0: "saved-node"}),
            mock.patch.object(manager, "get_slot_pin_map", return_value={}),
            mock.patch.object(manager, "get_exit_slot_config", return_value={"residential_only": False}),
            mock.patch.object(manager, "per_slot_country", return_value="JP"),
            mock.patch.object(manager, "per_slot_isp", return_value=""),
            mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
        ):
            selected = manager.pick_slot_node(0, set())

        self.assertEqual(selected["id"], "saved-node")

    def test_pick_slot_node_ignores_unavailable_reconnect_hint(self):
        nodes = [
            {"id": "fast-new", "country_short": "JP", "ip_type": "hosting", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "saved-node", "country_short": "JP", "ip_type": "hosting", "probe_status": "unavailable", "latency_ms": 20, "score": 2},
        ]
        with (
            mock.patch.object(manager, "slot_reconnect_hints", {0: "saved-node"}),
            mock.patch.object(manager, "get_slot_pin_map", return_value={}),
            mock.patch.object(manager, "get_exit_slot_config", return_value={"residential_only": False}),
            mock.patch.object(manager, "per_slot_country", return_value="JP"),
            mock.patch.object(manager, "per_slot_isp", return_value=""),
            mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            mock.patch.object(manager, "read_nodes", return_value=nodes),
        ):
            selected = manager.pick_slot_node(0, set())

        self.assertEqual(selected["id"], "fast-new")

    def test_failed_rotate_candidate_enters_cooldown(self):
        candidate = {
            "id": "jp-stale",
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.30",
            "ip_type": "hosting",
            "probe_status": "available",
            "latency_ms": 20,
            "score": 2,
        }
        bad_nodes = {}
        with (
            mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [0], "paused": [], "residential_only": False}),
            mock.patch.object(manager, "set_slot_pin"),
            mock.patch.object(manager, "current_slot_node_ids", return_value={"old-node"}),
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "per_slot_country", return_value="JP"),
            mock.patch.object(manager, "per_slot_isp", return_value=""),
            mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "bring_up_slot", return_value=False),
            mock.patch.object(manager, "mark_slot_pending"),
            mock.patch.object(manager, "write_slots_state"),
            mock.patch.object(manager, "slot_bad_nodes", bad_nodes),
        ):
            result = manager.switch_slot_node(0)

        self.assertFalse(result["ok"])
        self.assertIn("jp-stale", bad_nodes)

    def test_supervisor_failed_dial_candidate_enters_cooldown(self):
        candidate = {"id": "stale-fastest", "probe_status": "available"}
        bad_nodes = {}
        with (
            mock.patch.object(manager, "exit_slots_supervise_lock", threading.Lock()),
            mock.patch.object(manager, "get_active_slots", return_value=[0]),
            mock.patch.object(manager, "get_paused_slots", return_value=set()),
            mock.patch.object(manager.egress_repair_store, "get", return_value={}),
            mock.patch.object(manager, "exit_slots", {}),
            mock.patch.object(manager, "exit_slot_proxy_stops", {}),
            mock.patch.object(manager, "slot_process_alive", return_value=False),
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "pick_slot_node", return_value=candidate),
            mock.patch.object(manager, "bring_up_slot", return_value=False),
            mock.patch.object(manager, "mark_slot_pending"),
            mock.patch.object(manager, "write_slots_state"),
            mock.patch.object(manager, "slot_bad_nodes", bad_nodes),
        ):
            manager.supervise_exit_slots_once()

        self.assertIn("stale-fastest", bad_nodes)
        self.assertGreater(bad_nodes["stale-fastest"], time.time())

    def test_slot_reconnect_closes_existing_downstream_connections(self):
        registry = mock.Mock()
        registry.close_all.return_value = 2
        path = mock.Mock()
        path.exists.return_value = False
        registries = {2: registry}
        with (
            mock.patch.object(manager, "exit_slots", {2: {"process": object()}}),
            mock.patch.object(manager, "exit_slot_proxy_stops", {2: threading.Event()}),
            mock.patch.object(manager, "exit_slot_proxy_registries", registries),
            mock.patch.object(manager, "stop_process"),
            mock.patch.object(manager, "cleanup_policy_routing"),
            mock.patch.object(manager, "slot_config_path", return_value=path),
        ):
            manager.tear_down_slot(2, stop_proxy=False)

        registry.close_all.assert_called_once_with()
        self.assertIs(registries[2], registry)


class ManagedSlotFacadeTests(unittest.TestCase):
    def setUp(self):
        validator = mock.patch.object(
            manager,
            "validated_repair_candidate",
            side_effect=lambda candidates: candidates[0] if candidates else None,
        )
        replenisher = mock.patch.object(
            manager,
            "replenish_repair_country",
            return_value={"state": "completed", "resultCode": "no_usable_nodes"},
        )
        marker = mock.patch.object(manager, "mark_candidate_unavailable", return_value=True)
        validator.start()
        replenisher.start()
        marker.start()
        self.addCleanup(validator.stop)
        self.addCleanup(replenisher.stop)
        self.addCleanup(marker.stop)

    def test_automatic_slot_repair_promotes_only_its_standby(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_slot", return_value=True
        ) as promote, mock.patch.object(manager, "bring_up_slot") as bring_up, mock.patch.object(manager, "managed_slot_snapshot", return_value={"ok": True}):
            result = manager.repair_slot_once(0, {"node_id": "jp-old", "country": "JP"})
        self.assertTrue(result["ok"])
        self.assertTrue(result["standby_promoted"])
        promote.assert_called_once_with(0)
        bring_up.assert_not_called()

    def test_slot_failure_waits_for_background_replenishment(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_slot", return_value=False
        ), mock.patch.object(manager, "replenish_repair_country") as replenish, mock.patch.object(manager, "bring_up_slot") as bring_up:
            result = manager.repair_slot_once(0, {"node_id": "jp-old", "country": "JP"})
        self.assertEqual(result["error_code"], "recovery_pending")
        replenish.assert_not_called()
        bring_up.assert_not_called()

    def test_automatic_slot_repair_does_not_retry_claimed_failure(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {"status": "manual_required"}
        with (
            mock.patch.object(manager, "egress_repair_store", repair_store),
            mock.patch.object(manager, "slot_operation_locks", {}),
            mock.patch.object(manager, "bring_up_slot") as bring_up,
        ):
            result = manager.repair_slot_once(
                1,
                {"node_id": "jp-old", "country": "JP"},
            )

        self.assertEqual(result["error_code"], "manual_repair_required")
        bring_up.assert_not_called()

    def test_slot_restart_keeps_manual_placeholder_and_supervisor_does_not_dial(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            before_restart = RepairStore(path)
            self.assertTrue(before_restart.claim("slot:0", "jp-old", "JP"))
            before_restart.require_manual("slot:0", "replacement_failed", "jp-first")
            after_restart = RepairStore(path)

            with (
                mock.patch.object(manager, "egress_repair_store", after_restart),
                mock.patch.object(manager, "exit_slots_supervise_lock", threading.Lock()),
                mock.patch.object(manager, "slot_operation_locks", {}),
                mock.patch.object(manager, "get_active_slots", return_value=[0]),
                mock.patch.object(manager, "get_paused_slots", return_value=set()),
                mock.patch.object(manager, "exit_slots", {}),
                mock.patch.object(manager, "exit_slot_proxy_stops", {}),
                mock.patch.object(manager, "mark_slot_disconnected") as disconnected,
                mock.patch.object(manager, "repair_slot_once") as repair,
                mock.patch.object(manager, "pick_slot_node") as pick,
                mock.patch.object(manager, "bring_up_slot") as bring_up,
                mock.patch.object(manager, "write_slots_state"),
            ):
                manager.supervise_exit_slots_once()

            disconnected.assert_called_once_with(
                0,
                "恢复预算已耗尽，等待人工重试或更换出口",
                candidate_id="jp-old",
                country="JP",
            )
            repair.assert_not_called()
            pick.assert_not_called()
            bring_up.assert_not_called()

    def test_one_busy_slot_does_not_block_another_slot_from_recovering(self):
        slot_zero_lock = threading.RLock()
        slot_zero_lock.acquire()
        candidate = {"id": "kr-live", "country_short": "KR", "probe_status": "available"}
        try:
            with (
                mock.patch.object(manager, "exit_slots_supervise_lock", threading.Lock()),
                mock.patch.object(manager, "slot_operation_locks", {0: slot_zero_lock}),
                mock.patch.object(manager, "get_active_slots", return_value=[0, 1]),
                mock.patch.object(manager, "get_paused_slots", return_value=set()),
                mock.patch.object(manager, "exit_slots", {}),
                mock.patch.object(manager, "exit_slot_proxy_stops", {}),
                mock.patch.object(manager, "slot_process_alive", return_value=False),
                mock.patch.object(manager, "tear_down_slot"),
                mock.patch.object(manager, "pick_slot_node", side_effect=lambda slot, _used: candidate if slot == 1 else None),
                mock.patch.object(manager, "bring_up_slot", return_value=True) as bring_up,
                mock.patch.object(manager, "write_slots_state"),
            ):
                worker = threading.Thread(target=manager.supervise_exit_slots_once)
                worker.start()
                worker.join(timeout=2)
        finally:
            slot_zero_lock.release()

        self.assertFalse(worker.is_alive())
        bring_up.assert_called_once_with(1, candidate)

    def test_successful_manual_slot_assignment_clears_restart_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            repair_store = RepairStore(path)
            self.assertTrue(repair_store.claim("slot:2", "jp-old", "JP"))
            repair_store.require_manual("slot:2", "replacement_failed", "jp-first")
            candidate = {"id": "jp-manual", "country_short": "JP", "country": "Japan", "probe_status": "available"}

            with (
                mock.patch.object(manager, "egress_repair_store", repair_store),
                mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [2], "paused": [], "residential_only": False}),
                mock.patch.object(manager, "read_nodes", return_value=[candidate]),
                mock.patch.object(manager, "main_reserved_candidate_ids", return_value=set()),
                mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
                mock.patch.object(manager, "exit_slots", {}),
                mock.patch.object(manager, "slot_operation_locks", {}),
                mock.patch.object(manager, "load_ui_config", return_value={}),
                mock.patch.object(manager, "get_paused_slots", return_value=set()),
                mock.patch.object(manager, "_save_slot_lists"),
                mock.patch.object(manager, "set_slot_pin"),
                mock.patch.object(manager, "tear_down_slot"),
                mock.patch.object(manager, "bring_up_slot", return_value=True),
                mock.patch.object(manager, "write_slots_state"),
            ):
                result = manager.assign_node_to_slot(2, "jp-manual")

            self.assertTrue(result["ok"])
            after_restart = RepairStore(path)
            self.assertEqual(after_restart.get("slot:2")["status"], "healthy")
            self.assertTrue(after_restart.claim("slot:2", "jp-manual", "JP"))

    def test_slot_missing_standby_does_not_exhaust_budget_immediately(self):
        repair_store = mock.Mock()
        repair_store.get.return_value = {}
        with mock.patch.object(manager, "egress_repair_store", repair_store), mock.patch.object(
            manager, "promote_dedicated_standby_to_slot", return_value=False
        ), mock.patch.object(manager, "tear_down_slot") as stop:
            result = manager.repair_slot_once(2, {"node_id": "kr-old", "country": "KR"})
        self.assertEqual(result["error_code"], "recovery_pending")
        repair_store.require_manual.assert_not_called()
        repair_store.wait_for_standby.assert_called_once_with("slot:2", "kr-old", "KR")
        stop.assert_not_called()

    def test_slot_repair_uses_country_code_when_runtime_also_has_localized_name(self):
        repair_store = mock.Mock()
        repair_store.claim.return_value = True
        with (
            mock.patch.object(manager, "egress_repair_store", repair_store),
            mock.patch.object(manager, "slot_operation_locks", {}),
            mock.patch.object(manager, "automatic_slot_candidates", return_value=[]) as candidates,
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "mark_slot_disconnected"),
            mock.patch.object(manager, "write_slots_state"),
        ):
            manager.repair_slot_once(
                2, {"node_id": "jp-old", "country": "日本", "country_short": "JP"},
            )

        repair_store.wait_for_standby.assert_called_once_with("slot:2", "jp-old", "JP")
        self.assertTrue(all(call.args == (2, "JP") for call in candidates.call_args_list))

    def test_slot_check_is_not_blocked_by_main_repair_state(self):
        snapshot = {
            "ok": True,
            "slot": 0,
            "port": 17928,
            "node_id": "jp-slot",
            "status": "up",
        }
        runtime = {0: {"node_id": "jp-slot", "status": "up"}}
        with (
            mock.patch.object(
                manager.main_assignment_coordinator,
                "mutation_allowed",
                return_value=False,
            ),
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "slot_process_alive", return_value=True),
            mock.patch.object(manager, "ensure_policy_routing", return_value=True),
            mock.patch.object(manager, "check_slot_egress", return_value=(True, "198.51.100.20")),
            mock.patch.object(manager.egress_repair_store, "mark_healthy"),
            mock.patch.object(manager, "exit_slots", runtime),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.check_managed_slot(0)

        self.assertTrue(result["ok"])
        self.assertNotEqual(result.get("error_code"), "operation_busy")

    def test_slot_supervisor_is_not_blocked_by_main_repair_state(self):
        candidate = {"id": "jp-slot", "probe_status": "available"}
        with (
            mock.patch.object(
                manager.main_assignment_coordinator,
                "mutation_allowed",
                return_value=False,
            ),
            mock.patch.object(manager, "exit_slots_supervise_lock", threading.Lock()),
            mock.patch.object(manager, "get_active_slots", return_value=[0]),
            mock.patch.object(manager, "get_paused_slots", return_value=set()),
            mock.patch.object(manager.egress_repair_store, "get", return_value={}),
            mock.patch.object(manager, "exit_slots", {}),
            mock.patch.object(manager, "exit_slot_proxy_stops", {}),
            mock.patch.object(manager, "slot_process_alive", return_value=False),
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "pick_slot_node", return_value=candidate),
            mock.patch.object(manager, "bring_up_slot", return_value=True) as bring_up,
            mock.patch.object(manager, "write_slots_state"),
        ):
            manager.supervise_exit_slots_once()

        bring_up.assert_called_once_with(0, candidate)

    def test_check_managed_slot_repairs_a_proven_tunnel_egress_failure(self):
        snapshot = {
            "ok": True,
            "slot": 2,
            "node_id": "jp-stale",
            "country": "JP",
            "port": 17930,
            "status": "up",
        }
        runtime = {2: {"node_id": "jp-stale", "status": "up"}}
        repaired = {
            **snapshot,
            "node_id": "jp-new",
            "egress_ok": True,
            "exit_ip": "203.0.113.55",
        }
        with (
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "check_slot_egress", return_value=(False, "")),
            mock.patch.object(manager, "check_interface_exit_ip", return_value=(False, "")),
            mock.patch.object(manager, "slot_process_alive", return_value=True),
            mock.patch.object(manager, "mark_candidate_unavailable", return_value=True) as mark,
            mock.patch.object(manager, "repair_slot_once", return_value=repaired) as repair,
            mock.patch.object(manager, "exit_slots", runtime),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.check_managed_slot(2)

        mark.assert_called_once_with("jp-stale", "candidate_egress_failed")
        repair.assert_called_once_with(2, snapshot)
        self.assertTrue(result["auto_repair_performed"])
        self.assertEqual(result["node_id"], "jp-new")

    def test_check_managed_slot_keeps_candidate_when_tunnel_egress_is_healthy(self):
        snapshot = {
            "ok": True,
            "slot": 2,
            "node_id": "jp-live",
            "port": 17930,
            "status": "up",
        }
        with (
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "check_slot_egress", return_value=(False, "")),
            mock.patch.object(
                manager,
                "check_interface_exit_ip",
                return_value=(True, "203.0.113.40"),
            ),
            mock.patch.object(manager, "slot_process_alive", return_value=True),
            mock.patch.object(manager, "mark_candidate_unavailable") as mark,
            mock.patch.object(manager, "exit_slots", {2: {"node_id": "jp-live"}}),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.check_managed_slot(2)

        self.assertEqual(result["error_code"], "egress_check_failed")
        self.assertFalse(result["candidate_rejected"])
        mark.assert_not_called()

    def test_check_managed_slot_repairs_once_when_tunnel_is_missing(self):
        snapshot = {
            "ok": True,
            "slot": 0,
            "node_id": "ru-broken",
            "country": "RU",
            "port": 17928,
            "status": "up",
        }
        repaired = {
            **snapshot,
            "node_id": "ru-home",
            "status": "up",
            "egress_ok": True,
            "exit_ip": "203.0.113.44",
        }
        with (
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "slot_process_alive", return_value=False),
            mock.patch.object(manager, "ensure_policy_routing") as ensure,
            mock.patch.object(manager, "check_slot_egress") as proxy_probe,
            mock.patch.object(manager, "repair_slot_once", return_value=repaired) as repair,
        ):
            result = manager.check_managed_slot(0)

        ensure.assert_not_called()
        proxy_probe.assert_not_called()
        repair.assert_called_once_with(0, snapshot)
        self.assertTrue(result["auto_repair_performed"])
        self.assertEqual(result["node_id"], "ru-home")

    def test_check_managed_slot_returns_attempt_result_when_no_candidate_exists(self):
        snapshot = {
            "ok": True,
            "slot": 0,
            "node_id": "ru-broken",
            "country": "RU",
            "port": 17928,
            "status": "up",
        }
        disconnected = {
            **snapshot,
            "status": "disconnected",
            "egress_ok": False,
            "repair_status": "manual_required",
            "auto_repair_attempted": True,
            "last_error_code": "no_same_country_candidate",
        }
        with (
            mock.patch.object(manager, "managed_slot_snapshot", side_effect=[snapshot, disconnected]),
            mock.patch.object(manager, "slot_process_alive", return_value=False),
            mock.patch.object(
                manager,
                "repair_slot_once",
                return_value={
                    "ok": False,
                    "error_code": "no_same_country_candidate",
                    "auto_repair_performed": True,
                },
            ),
        ):
            result = manager.check_managed_slot(0)

        self.assertTrue(result["ok"])
        self.assertFalse(result["egress_ok"])
        self.assertTrue(result["auto_repair_performed"])
        self.assertEqual(result["last_error_code"], "no_same_country_candidate")

    def test_rechecking_an_attempted_failure_logs_that_no_repair_was_repeated(self):
        snapshot = {
            "ok": True,
            "slot": 0,
            "node_id": "ru-broken",
            "country": "RU",
            "port": 17928,
            "status": "disconnected",
            "egress_ok": False,
            "repair_status": "manual_required",
        }
        with (
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "slot_process_alive", return_value=False),
            mock.patch.object(
                manager,
                "repair_slot_once",
                return_value={
                    "ok": False,
                    "error_code": "manual_repair_required",
                    "auto_repair_performed": False,
                },
            ),
            mock.patch.object(manager, "log_to_json") as log,
            mock.patch("builtins.print") as output,
        ):
            result = manager.check_managed_slot(0)

        self.assertFalse(result["auto_repair_performed"])
        self.assertEqual(result["last_error_code"], "manual_repair_required")
        self.assertIn("不重复更换节点", output.call_args.args[0])
        self.assertIn("不重复更换节点", log.call_args.args[2])

    def test_check_managed_slot_marks_a_verified_healthy_failure_closed(self):
        snapshot = {
            "ok": True,
            "slot": 0,
            "node_id": "jp-live",
            "country": "JP",
            "port": 17928,
            "status": "up",
        }
        repair_store = mock.Mock()
        with (
            mock.patch.object(manager, "managed_slot_snapshot", return_value=snapshot),
            mock.patch.object(manager, "slot_process_alive", return_value=True),
            mock.patch.object(manager, "ensure_policy_routing", return_value=True),
            mock.patch.object(manager, "check_slot_egress", return_value=(True, "203.0.113.50")),
            mock.patch.object(manager, "egress_repair_store", repair_store),
            mock.patch.object(manager, "exit_slots", {0: {"node_id": "jp-live"}}),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.check_managed_slot(0)

        self.assertTrue(result["ok"])
        repair_store.mark_healthy.assert_called_once_with("slot:0", "jp-live")

    def test_assign_managed_slot_propagates_proven_candidate_failure(self):
        candidate = {
            "id": "jp-stale",
            "country_short": "JP",
            "country": "Japan",
            "ip_type": "hosting",
            "probe_status": "available",
        }
        previous = {"node_id": "", "status": "pending"}
        with (
            mock.patch.object(manager, "get_active_slots", return_value=[2]),
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "exit_slots", {2: previous}),
            mock.patch.object(manager, "get_slot_pin_map", return_value={}),
            mock.patch.object(manager, "get_slot_country_map", return_value={}),
            mock.patch.object(manager, "get_slot_type_map", return_value={}),
            mock.patch.object(manager, "set_slot_country"),
            mock.patch.object(manager, "set_slot_type"),
            mock.patch.object(
                manager,
                "assign_node_to_slot",
                return_value={
                    "ok": False,
                    "error_code": "candidate_dial_failed",
                    "candidate_rejected": True,
                },
            ),
        ):
            result = manager.assign_managed_slot(2, "jp-stale", "JP", "datacenter")

        self.assertEqual(result["error_code"], "candidate_dial_failed")
        self.assertTrue(result["candidate_rejected"])

    def test_assign_managed_slot_preserves_rejection_when_restore_fails(self):
        candidate = {
            "id": "jp-stale",
            "country_short": "JP",
            "country": "Japan",
            "ip_type": "hosting",
            "probe_status": "available",
        }
        previous = {"node_id": "jp-old", "status": "up"}
        with (
            mock.patch.object(manager, "get_active_slots", return_value=[2]),
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "exit_slots", {2: previous}),
            mock.patch.object(manager, "get_slot_pin_map", return_value={"2": "jp-old"}),
            mock.patch.object(manager, "get_slot_country_map", return_value={"2": "US"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"2": "datacenter"}),
            mock.patch.object(manager, "set_slot_country"),
            mock.patch.object(manager, "set_slot_type"),
            mock.patch.object(
                manager,
                "assign_node_to_slot",
                side_effect=[
                    {
                        "ok": False,
                        "error_code": "candidate_dial_failed",
                        "candidate_rejected": True,
                    },
                    {"ok": False, "error_code": "assign_failed"},
                ],
            ),
        ):
            result = manager.assign_managed_slot(2, "jp-stale", "JP", "datacenter")

        self.assertEqual(
            result,
            {
                "ok": False,
                "state": "repair_required",
                "error_code": "rollback_failed",
                "candidate_rejected": True,
            },
        )

    def test_assign_pending_slot_does_not_restore_a_stale_pin(self):
        candidate = {
            "id": "jp-new",
            "country_short": "JP",
            "country": "Japan",
            "ip_type": "hosting",
            "probe_status": "available",
        }
        assign = mock.Mock(return_value={
            "ok": False,
            "error_code": "candidate_dial_failed",
            "candidate_rejected": True,
        })
        clear_pin = mock.Mock()
        with (
            mock.patch.object(manager, "get_active_slots", return_value=[2]),
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager, "exit_slots", {2: {"node_id": "", "status": "pending"}}),
            mock.patch.object(manager, "get_slot_pin_map", return_value={"2": "jp-stale"}),
            mock.patch.object(manager, "get_slot_country_map", return_value={"2": "US"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"2": "residential"}),
            mock.patch.object(manager, "set_slot_country"),
            mock.patch.object(manager, "set_slot_type"),
            mock.patch.object(manager, "set_slot_pin", clear_pin),
            mock.patch.object(manager, "assign_node_to_slot", assign),
        ):
            result = manager.assign_managed_slot(2, "jp-new", "JP", "datacenter")

        self.assertEqual(assign.call_count, 1)
        clear_pin.assert_called_once_with(2, "")
        self.assertEqual(result["error_code"], "candidate_dial_failed")
        self.assertTrue(result["candidate_rejected"])

    def test_slot_orphan_cleanup_matches_only_the_exact_managed_slot(self):
        with tempfile.TemporaryDirectory() as directory:
            proc_root = Path(directory)
            commands = {
                101: ["/usr/sbin/openvpn", "--setenv", "AIMILI_SLOT", "2"],
                102: ["/usr/sbin/openvpn", "--setenv", "AIMILI_SLOT", "1"],
                103: ["/usr/sbin/openvpn", "--setenv", "NOT_AIMILI_SLOT", "2"],
            }
            for pid, command in commands.items():
                path = proc_root / str(pid)
                path.mkdir()
                (path / "cmdline").write_bytes(b"\0".join(part.encode() for part in command) + b"\0")
            killed = []
            with (
                mock.patch.object(manager.sys, "platform", "linux"),
                mock.patch.object(manager.os, "kill", side_effect=lambda pid, sig: killed.append((pid, sig))),
                mock.patch.object(manager.time, "sleep"),
            ):
                manager.kill_unregistered_slot_openvpn_processes(2, proc_root=proc_root)

        self.assertEqual([pid for pid, _sig in killed], [101, 101])

    def test_openvpn_reader_thread_failure_reaps_started_process(self):
        class Process:
            stdout = []

            def __init__(self):
                self.terminated = False
                self.waited = False

            def poll(self):
                return None

            def terminate(self):
                self.terminated = True

            def wait(self, timeout=None):
                self.waited = True
                return 0

        class Thread:
            def __init__(self, **_kwargs):
                pass

            def start(self):
                raise RuntimeError("can't start new thread")

        process = Process()
        with (
            mock.patch.object(manager.subprocess, "Popen", return_value=process),
            mock.patch.object(manager.threading, "Thread", Thread),
        ):
            ok, message, returned = manager.run_openvpn_until_ready(
                "slot.ovpn", True, True, timeout=1, dev="tun122"
            )

        self.assertFalse(ok)
        self.assertIn("thread", message.lower())
        self.assertIsNone(returned)
        self.assertTrue(process.terminated)
        self.assertTrue(process.waited)

    def test_policy_routing_failure_reaps_process_and_does_not_register_slot(self):
        process = mock.Mock()
        node = {
            "id": "jp-one", "country": "Japan", "country_short": "JP",
            "ip": "198.51.100.10", "ip_type": "hosting", "config_text": "client",
        }
        runtime = {}
        with (
            mock.patch.object(manager, "CONFIG_DIR"),
            mock.patch.object(manager, "slot_config_path", return_value=mock.MagicMock()),
            mock.patch.object(manager, "run_openvpn_until_ready", return_value=(True, "ok", process)),
            mock.patch.object(manager, "setup_policy_routing", return_value=False),
            mock.patch.object(manager, "stop_process") as stop,
            mock.patch.object(manager, "ensure_slot_proxy") as ensure_proxy,
            mock.patch.object(manager, "exit_slots", runtime),
        ):
            result = manager.bring_up_slot(2, node)

        self.assertFalse(result)
        stop.assert_called_once_with(process)
        ensure_proxy.assert_not_called()
        self.assertNotIn(2, runtime)

    def test_create_managed_slot_pins_the_requested_candidate(self):
        candidates = [
            {"id": "jp-fast", "country_short": "JP", "country": "Japan", "ip_type": "hosting", "probe_status": "available", "latency_ms": 1},
            {"id": "jp-requested", "country_short": "JP", "country": "Japan", "ip_type": "hosting", "probe_status": "available", "latency_ms": 20},
        ]
        selected = []
        runtime_slot = {
            "slot": 3, "country_short": "JP", "country": "Japan", "ip_type": "hosting",
            "port": 17931, "status": "up", "node_id": "jp-requested", "process": object(),
        }
        with (
            mock.patch.object(manager, "read_nodes", return_value=candidates),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "add_slot_with_node", side_effect=lambda node: selected.append(node) or {"ok": True, "slot": 3}),
            mock.patch.object(manager, "set_slot_country"),
            mock.patch.object(manager, "set_slot_type"),
            mock.patch.object(manager, "get_slot_country_map", return_value={"3": "JP"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"3": "datacenter"}),
            mock.patch.object(manager, "exit_slots", {3: runtime_slot}),
        ):
            result = manager.create_managed_slot("JP", "datacenter", "jp-requested")

        self.assertEqual(selected, ["jp-requested"])
        self.assertEqual(result["node_id"], "jp-requested")

    def test_create_managed_slot_rejects_requested_candidate_with_wrong_classification(self):
        candidate = {
            "id": "kr-home", "country_short": "KR", "country": "Korea",
            "ip_type": "residential", "probe_status": "available",
        }
        with mock.patch.object(manager, "read_nodes", return_value=[candidate]):
            result = manager.create_managed_slot("JP", "datacenter", "kr-home")

        self.assertEqual(result, {"ok": False, "error_code": "candidate_mismatch"})

    def test_managed_slots_snapshot_exposes_safe_runtime_fields_only(self):
        runtime = {
            1: {
                "slot": 1, "country_short": "JP", "country": "Japan", "ip_type": "hosting",
                "port": 17929, "status": "up", "node_id": "jp-one", "process": object(),
                "config_text": "secret",
            }
        }
        with (
            mock.patch.object(manager, "exit_slots", runtime),
            mock.patch.object(manager, "get_slot_country_map", return_value={"1": "JP"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"1": "datacenter"}),
        ):
            snapshots = manager.managed_slots_snapshot()

        self.assertEqual([item["slot"] for item in snapshots], [1])
        self.assertNotIn("process", snapshots[0])
        self.assertNotIn("config_text", snapshots[0])

    def test_managed_slot_snapshot_replaces_stale_runtime_country_name(self):
        runtime = {
            1: {
                "slot": 1,
                "country_short": "KR",
                "country": "越南",
                "ip_type": "residential",
                "port": 17929,
                "status": "up",
                "node_id": "kr-current",
            }
        }
        current = {
            "id": "kr-current",
            "country_short": "KR",
            "country": "韩国",
            "ip_type": "residential",
        }
        with (
            mock.patch.object(manager, "exit_slots", runtime),
            mock.patch.object(manager, "get_slot_country_map", return_value={"1": "KR"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"1": "residential"}),
            mock.patch.object(manager, "read_nodes", return_value=[current]),
        ):
            snapshot = manager.managed_slot_snapshot(1)

        self.assertEqual(snapshot["country"], "KR")
        self.assertEqual(snapshot["country_name"], "韩国")

    def test_start_control_plane_uses_explicit_loopback_configuration(self):
        sentinel = object()
        with (
            mock.patch.dict(
                "os.environ",
                {
                    "AIMILI_CONTROL_ADDRESS": "127.0.0.1:8899",
                    "AIMILI_CONTROL_TOKEN_FILE": "/run/aimili/control.token",
                },
                clear=False,
            ),
            mock.patch("control_api.start_control_server", return_value=sentinel) as start,
        ):
            actual = manager.start_control_plane()

        self.assertIs(actual, sentinel)
        args = start.call_args.args
        self.assertIs(args[0], manager)
        self.assertEqual(args[1], "127.0.0.1:8899")
        self.assertEqual(str(args[2]).replace("\\", "/"), "/run/aimili/control.token")

    def test_create_managed_slot_selects_matching_node_and_returns_safe_snapshot(self):
        candidates = [
            {"id": "jp-home", "country_short": "JP", "country": "Japan", "ip": "198.51.100.20", "ip_type": "mobile", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "jp-dc", "country_short": "JP", "country": "Japan", "ip": "198.51.100.30", "ip_type": "hosting", "probe_status": "available", "latency_ms": 20, "score": 2},
        ]
        runtime_slot = {
            "slot": 2,
            "country_short": "JP",
            "country": "Japan",
            "ip": "198.51.100.30",
            "ip_type": "hosting",
            "port": 17930,
            "status": "up",
            "node_id": "jp-dc",
            "process": object(),
        }
        selected = []
        countries = {}
        types = {}

        def add(node_id):
            selected.append(node_id)
            return {"ok": True, "slot": 2}

        with (
            mock.patch.object(manager, "read_nodes", return_value=candidates),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "add_slot_with_node", side_effect=add),
            mock.patch.object(manager, "set_slot_country", side_effect=lambda slot, value: countries.update({str(slot): value}) or countries.copy()),
            mock.patch.object(manager, "set_slot_type", side_effect=lambda slot, value: types.update({str(slot): value}) or types.copy()),
            mock.patch.object(manager, "get_slot_country_map", side_effect=lambda: countries.copy()),
            mock.patch.object(manager, "get_slot_type_map", side_effect=lambda: types.copy()),
            mock.patch.object(manager, "exit_slots", {2: runtime_slot}),
        ):
            result = manager.create_managed_slot("JP", "datacenter")

        self.assertEqual(selected, ["jp-dc"])
        self.assertEqual(result["proxy_type"], "datacenter")
        self.assertEqual(result["port"], 17930)
        self.assertNotIn("process", result)

    def test_create_managed_slot_reports_no_candidate_without_allocating(self):
        allocated = []
        with (
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "add_slot_with_node", side_effect=lambda node: allocated.append(node)),
        ):
            result = manager.create_managed_slot("JP", "datacenter")

        self.assertEqual(result, {"ok": False, "error_code": "no_matching_candidate"})
        self.assertEqual(allocated, [])

    def test_create_managed_slot_retries_the_next_matching_candidate(self):
        candidates = [
            {"id": "jp-stale", "country_short": "JP", "country": "Japan", "ip_type": "hosting", "probe_status": "available", "latency_ms": 1, "score": 9},
            {"id": "jp-live", "country_short": "JP", "country": "Japan", "ip_type": "hosting", "probe_status": "available", "latency_ms": 2, "score": 8},
        ]
        runtime_slot = {
            "slot": 0,
            "country_short": "JP",
            "country": "Japan",
            "ip_type": "hosting",
            "port": 17928,
            "status": "up",
            "node_id": "jp-live",
            "process": object(),
        }
        selected = []
        countries = {}
        types = {}

        def add(node_id):
            selected.append(node_id)
            if node_id == "jp-stale":
                return {"ok": False, "error": "authentication failed"}
            return {"ok": True, "slot": 0}

        with (
            mock.patch.object(manager, "read_nodes", return_value=candidates),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "add_slot_with_node", side_effect=add),
            mock.patch.object(manager, "set_slot_country", side_effect=lambda slot, value: countries.update({str(slot): value}) or countries.copy()),
            mock.patch.object(manager, "set_slot_type", side_effect=lambda slot, value: types.update({str(slot): value}) or types.copy()),
            mock.patch.object(manager, "get_slot_country_map", side_effect=lambda: countries.copy()),
            mock.patch.object(manager, "get_slot_type_map", side_effect=lambda: types.copy()),
            mock.patch.object(manager, "exit_slots", {0: runtime_slot}),
            mock.patch.object(manager, "slot_bad_nodes", {}),
        ):
            result = manager.create_managed_slot("JP", "datacenter")

        self.assertEqual(selected, ["jp-stale", "jp-live"])
        self.assertTrue(result["ok"])
        self.assertEqual(result["node_id"], "jp-live")

    def test_add_slot_with_node_rolls_back_allocation_when_dial_fails(self):
        candidate = {"id": "jp-stale", "probe_status": "available"}
        with (
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "get_active_slots", return_value=[]),
            mock.patch.object(manager, "load_ui_config", return_value={}),
            mock.patch.object(manager, "_save_slot_lists"),
            mock.patch.object(manager, "set_slot_pin"),
            mock.patch.object(manager, "assign_node_to_slot", return_value={"ok": False, "error": "authentication failed"}),
            mock.patch.object(manager, "delete_slot", return_value={"ok": True}) as delete,
        ):
            result = manager.add_slot_with_node("jp-stale")

        self.assertFalse(result["ok"])
        delete.assert_called_once_with(0)

    def test_assign_managed_slot_hides_a_candidate_that_fails_to_dial(self):
        candidate = {"id": "jp-stale", "country_short": "JP", "ip_type": "hosting", "probe_status": "available"}
        previous = {"node_id": "kr-old", "country_short": "KR", "proxy_type": "datacenter"}
        with (
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "get_active_slots", return_value=[2]),
            mock.patch.object(manager, "get_slot_country_map", return_value={"2": "KR"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"2": "datacenter"}),
            mock.patch.object(manager, "exit_slots", {2: previous}),
            mock.patch.object(manager, "assign_node_to_slot", side_effect=[{"ok": False, "error_code": "candidate_dial_failed", "error": "dial failed"}, {"ok": True}]),
            mock.patch.object(manager, "mark_blacklisted") as blacklist,
        ):
            result = manager.assign_managed_slot(2, "jp-stale", "JP", "datacenter")

        self.assertFalse(result["ok"])
        self.assertEqual(result["error_code"], "assign_failed")
        blacklist.assert_called_once_with(candidate, "分配到出口位失败：dial failed")

    def test_assign_managed_slot_does_not_reject_a_candidate_when_the_slot_supervisor_is_busy(self):
        candidate = {"id": "jp-wait", "country_short": "JP", "ip_type": "hosting", "probe_status": "available"}
        previous = {"node_id": "kr-old", "country_short": "KR", "proxy_type": "datacenter"}
        with (
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "get_active_slots", return_value=[2]),
            mock.patch.object(manager, "get_slot_country_map", return_value={"2": "KR"}),
            mock.patch.object(manager, "get_slot_type_map", return_value={"2": "datacenter"}),
            mock.patch.object(manager, "exit_slots", {2: previous}),
            mock.patch.object(manager, "assign_node_to_slot", side_effect=[{"ok": False, "error_code": "operation_busy", "error": "供给器正忙"}, {"ok": True}]),
            mock.patch.object(manager, "mark_blacklisted") as blacklist,
        ):
            result = manager.assign_managed_slot(2, "jp-wait", "JP", "datacenter")

        self.assertFalse(result["ok"])
        self.assertEqual(result["error_code"], "assign_failed")
        blacklist.assert_not_called()

    def test_assign_node_to_slot_marks_only_a_real_dial_failure(self):
        candidate = {"id": "jp-dial", "probe_status": "available"}

        def fail_dial(_slot, _candidate):
            manager.main_assignment_thread.slot_candidate_failure_code = "candidate_dial_failed"
            return False

        with (
            mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [2], "paused": [], "residential_only": False}),
            mock.patch.object(manager, "read_nodes", return_value=[candidate]),
            mock.patch.object(manager, "exit_slots", {}),
            mock.patch.object(manager, "load_ui_config", return_value={}),
            mock.patch.object(manager, "get_paused_slots", return_value=set()),
            mock.patch.object(manager, "_save_slot_lists"),
            mock.patch.object(manager, "set_slot_pin"),
            mock.patch.object(manager, "tear_down_slot"),
            mock.patch.object(manager, "bring_up_slot", side_effect=fail_dial),
            mock.patch.object(manager, "mark_slot_pending"),
            mock.patch.object(manager, "write_slots_state"),
        ):
            result = manager.assign_node_to_slot(2, "jp-dial")

        self.assertEqual(result["error_code"], "candidate_dial_failed")

    def test_rotate_managed_slot_retries_after_a_stale_candidate(self):
        expected = {"ok": True, "slot": 0, "node_id": "jp-live", "status": "up"}
        with (
            mock.patch.object(
                manager,
                "switch_slot_node",
                side_effect=[
                    {"ok": False, "error": "authentication failed"},
                    {"ok": True, "ip": "198.51.100.40"},
                ],
            ) as switch,
            mock.patch.object(manager, "managed_slot_snapshot", return_value=expected),
        ):
            result = manager.rotate_managed_slot(0)

        self.assertEqual(result, expected)
        self.assertEqual(switch.call_count, 2)

    def test_managed_rotate_waits_only_for_the_same_busy_slot(self):
        same_slot_lock = threading.Lock()
        same_slot_lock.acquire()
        release = threading.Timer(0.05, same_slot_lock.release)
        release.start()
        try:
            with (
                mock.patch.object(manager, "slot_operation_locks", {0: same_slot_lock}),
                mock.patch.object(manager, "get_exit_slot_config", return_value={"active": [0], "paused": [], "residential_only": False}),
                mock.patch.object(manager, "set_slot_pin"),
                mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
                mock.patch.object(manager, "per_slot_country", return_value="JP"),
                mock.patch.object(manager, "per_slot_isp", return_value=""),
                mock.patch.object(manager, "per_slot_type", return_value="datacenter"),
                mock.patch.object(manager, "select_slot_nodes", return_value=[{"id": "jp-live", "ip": "198.51.100.40", "country": "Japan"}]),
                mock.patch.object(manager, "tear_down_slot"),
                mock.patch.object(manager, "bring_up_slot", return_value=True),
                mock.patch.object(manager, "write_slots_state"),
                mock.patch.object(manager, "managed_slot_snapshot", return_value={"ok": True, "slot": 0, "status": "up"}),
            ):
                started = time.monotonic()
                result = manager.rotate_managed_slot(0)
        finally:
            release.cancel()
            if same_slot_lock.locked():
                same_slot_lock.release()

        self.assertTrue(result["ok"])
        self.assertGreaterEqual(time.monotonic() - started, 0.04)


if __name__ == "__main__":
    unittest.main()
