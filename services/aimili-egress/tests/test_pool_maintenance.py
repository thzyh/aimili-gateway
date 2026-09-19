import base64
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as manager


def node(index, status="not_checked", active=False):
    return {
        "id": f"n{index}",
        "probe_status": status,
        "active": active,
        "config_file": f"n{index}.ovpn",
        "config_text": "",
    }


def country_node(node_id, country, status="not_checked"):
    return {
        "id": node_id,
        "country_short": country,
        "country": country,
        "probe_status": status,
        "config_file": f"{node_id}.ovpn",
        "config_text": f"remote {node_id}.invalid 443 tcp\n",
    }


class PoolMaintenanceTests(unittest.TestCase):
    def test_country_catalog_reports_regular_target_and_temporary_limit(self):
        with (
            mock.patch.object(
                manager,
                "read_json",
                return_value=[{"code": "JP", "name": "日本", "candidateCount": 3, "observedAt": 1}],
            ),
            mock.patch.object(
                manager,
                "read_nodes",
                return_value=[country_node("jp-one", "JP", "available")],
            ),
        ):
            catalog = manager.country_catalog_snapshot()

        self.assertEqual(catalog[0]["targetValidNodeCount"], 64)
        self.assertEqual(catalog[0]["maxValidNodeCount"], 150)

    def test_completed_country_refresh_keeps_its_final_counts_after_pool_changes(self):
        original_state = dict(manager.country_refresh_state)
        try:
            manager._replace_country_refresh(
                state="completed",
                country="ALL",
                cacheTotal=40,
                countryValidCount=40,
                usableCount=40,
                finishedAt=1_700_000_000,
            )
            current_nodes = [
                country_node(f"jp-{index}", "JP", "available")
                for index in range(38)
            ]

            with mock.patch.object(manager, "read_nodes", return_value=current_nodes):
                snapshot = manager.country_refresh_snapshot()

            self.assertEqual(snapshot["cacheTotal"], 40)
            self.assertEqual(snapshot["countryValidCount"], 40)
        finally:
            manager._replace_country_refresh(**original_state)

    def test_pool_exhaustion_recovery_waits_for_current_mutation_to_finish(self):
        original_thread = threading.Thread
        worker_entered = threading.Event()
        maintenance_started = threading.Event()
        workers = []
        maintenance_calls = []

        @manager._mutation_guard("operation_busy")
        def guarded_maintenance(force=False):
            maintenance_calls.append(force)
            maintenance_started.set()
            return "recovered"

        class CoordinatedThread:
            def __init__(self, *, target, args=(), kwargs=None, daemon=None):
                def run():
                    worker_entered.set()
                    target(*args, **(kwargs or {}))

                self.worker = original_thread(target=run, daemon=daemon)
                workers.append(self.worker)

            def start(self):
                self.worker.start()
                if not worker_entered.wait(1):
                    raise AssertionError("recovery worker did not start")

        original_active_id = manager.active_openvpn_node_id
        manager.active_openvpn_node_id = ""
        try:
            with (
                mock.patch.object(
                    manager,
                    "load_ui_config",
                    return_value={"connection_enabled": True, "routing_mode": "auto"},
                ),
                mock.patch.object(manager, "read_nodes", return_value=[]),
                mock.patch.object(manager, "stop_active_openvpn"),
                mock.patch.object(manager, "write_json"),
                mock.patch.object(manager, "set_state"),
                mock.patch.object(manager, "log_to_json"),
                mock.patch.object(manager, "maintain_valid_nodes", guarded_maintenance),
                mock.patch.object(manager.threading, "Thread", CoordinatedThread),
            ):
                manager.auto_switch_node()
                self.assertTrue(
                    maintenance_started.wait(1),
                    "pool recovery was lost while auto_switch_node still held the mutation lock",
                )
                self.assertEqual(maintenance_calls, [False])
        finally:
            manager.active_openvpn_node_id = original_active_id
            for worker in workers:
                worker.join(timeout=1)

    def test_corrupt_pool_metadata_returns_safe_defaults_without_touching_nodes(self):
        with tempfile.TemporaryDirectory() as directory:
            metadata = Path(directory) / "pool_metadata.json"
            metadata.write_text("{broken", encoding="utf-8")
            with mock.patch.object(manager, "POOL_METADATA_FILE", metadata):
                result = manager.load_pool_metadata()

        self.assertEqual(result["schemaVersion"], 1)
        self.assertEqual(result["maintenanceRound"], 0)
        self.assertEqual(result["manualProtectedIds"], [])
    def test_web_snapshot_exposes_only_available_nodes_and_summary_counts(self):
        nodes = [
            {**country_node("jp-good", "JP", "available"), "config_text": "secret"},
            country_node("jp-bad", "JP", "unavailable"),
            country_node("us-pending", "US", "not_checked"),
            country_node("us-good", "US", "available"),
        ]
        catalog = [
            {"code": "JP", "name": "日本", "candidateCount": 12, "observedAt": 100.0},
            {"code": "US", "name": "美国", "candidateCount": 8, "observedAt": 100.0},
        ]

        payload = manager.build_web_node_snapshot(
            nodes,
            {"last_check_message": "ok"},
            catalog,
            {"state": "idle"},
        )

        self.assertEqual([item["id"] for item in payload["nodes"]], ["jp-good", "us-good"])
        self.assertNotIn("config_text", payload["nodes"][0])
        self.assertEqual(
            payload["node_stats"],
            {
                "official_node_count": 20,
                "official_country_count": 2,
                "current_node_count": 2,
                "current_country_count": 2,
            },
        )

    def test_prune_stored_nodes_discards_every_non_available_node(self):
        existing = [
            country_node("jp-good", "JP", "available"),
            country_node("jp-bad", "JP", "unavailable"),
            country_node("jp-pending", "JP", "not_checked"),
        ]
        with (
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "write_json") as write,
        ):
            removed = manager.prune_stored_unavailable_nodes()

        self.assertEqual(removed, 2)
        write.assert_called_once()
        self.assertEqual([item["id"] for item in write.call_args.args[1]], ["jp-good"])

    def test_replenishment_scheduler_skips_when_maintenance_is_busy(self):
        with (
            mock.patch.object(
                manager,
                "maintenance_lock",
                mock.Mock(locked=mock.Mock(return_value=True)),
            ),
            mock.patch.object(manager, "maintain_valid_nodes") as run,
            mock.patch.object(manager, "log_to_json"),
        ):
            self.assertFalse(manager.schedule_valid_pool_replenishment("busy"))

        run.assert_not_called()

    def test_replenishment_scheduler_starts_background_maintenance(self):
        completed = manager.threading.Event()

        def maintain(force=False, revalidate_existing=True):
            self.assertFalse(force)
            self.assertFalse(revalidate_existing)
            completed.set()

        with (
            mock.patch.object(
                manager,
                "maintenance_lock",
                mock.Mock(locked=mock.Mock(return_value=False)),
            ),
            mock.patch.object(manager, "maintain_valid_nodes", side_effect=maintain),
            mock.patch.object(manager, "log_to_json"),
        ):
            self.assertTrue(manager.schedule_valid_pool_replenishment("test"))
            self.assertTrue(completed.wait(1))

    def test_full_refresh_scheduler_revalidates_existing_nodes(self):
        completed = manager.threading.Event()

        def maintain(force=False, revalidate_existing=False):
            self.assertFalse(force)
            self.assertTrue(revalidate_existing)
            completed.set()

        with (
            mock.patch.object(
                manager,
                "maintenance_lock",
                mock.Mock(locked=mock.Mock(return_value=False)),
            ),
            mock.patch.object(manager, "maintain_valid_nodes", side_effect=maintain),
            mock.patch.object(manager, "log_to_json"),
        ):
            self.assertTrue(
                manager.schedule_valid_pool_replenishment(
                    "manual refresh",
                    revalidate_existing=True,
                )
            )
            self.assertTrue(completed.wait(1))

    def test_country_refresh_runs_in_background_and_exposes_safe_status(self):
        candidates = [country_node("jp-one", "JP")]
        with (
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(
                manager,
                "country_catalog_snapshot",
                return_value=[
                    {"code": "JP", "candidateCount": 1},
                    {"code": "US", "candidateCount": 2},
                ],
            ),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(
                manager,
                "probe_nodes",
                side_effect=lambda batch: [dict(item, probe_status="available") for item in batch],
            ),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "write_json"),
            mock.patch.object(manager, "set_state"),
        ):
            accepted = manager.start_country_refresh("jp")
            deadline = time.time() + 2
            snapshot = manager.country_refresh_snapshot()
            while snapshot["state"] == "running" and time.time() < deadline:
                time.sleep(0.01)
                snapshot = manager.country_refresh_snapshot()

        self.assertEqual(accepted["state"], "running")
        self.assertEqual(snapshot["state"], "completed")
        self.assertEqual(snapshot["country"], "JP")
        self.assertEqual(snapshot["catalogCount"], 3)
        self.assertEqual(snapshot["resultCode"], "success")
        self.assertEqual(snapshot["officialCount"], 1)
        self.assertEqual(snapshot["usableCount"], 1)
        self.assertEqual(snapshot["retainedCount"], 0)
        self.assertEqual(snapshot["countryCandidateCount"], 1)
        self.assertEqual(snapshot["testedCount"], 1)
        self.assertNotIn("exception", snapshot)

    def test_country_refresh_worker_scans_full_country_contract(self):
        original_state = manager.country_refresh_snapshot()
        gate = manager.threading.Event()
        gate.set()
        manager._set_country_refresh(
            state="running",
            country="JP",
            phase="fetching",
            startedAt=1_700_000_000,
        )
        try:
            with mock.patch.object(
                manager,
                "refresh_country_nodes",
                return_value={"state": "completed", "country": "JP", "resultCode": "success"},
            ) as refresh:
                manager._country_refresh_worker("JP", gate)

            refresh.assert_called_once_with("JP", _lock_held=True)
        finally:
            manager._replace_country_refresh(**original_state)
            if manager.maintenance_lock.locked():
                manager.maintenance_lock.release()

    def test_all_country_refresh_runs_global_maintenance_and_exposes_aggregate_status(self):
        existing = [country_node("jp-old", "JP", "available")]
        refreshed = [
            country_node(f"jp-{index}", "JP", "available")
            for index in range(40)
        ]
        stored_metadata = {}

        def store_metadata(document):
            stored_metadata.update(document)

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 40),
            mock.patch.object(manager, "ensure_dirs"),
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "fetch_candidates", return_value=refreshed),
            mock.patch.object(
                manager,
                "country_catalog_snapshot",
                return_value=[{"code": "JP", "candidateCount": 40}],
            ),
            mock.patch.object(
                manager,
                "replenish_valid_pool",
                return_value=(refreshed, {}, {"tested": 12, "batches": 12, "stop_reason": "target_reached"}),
            ),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata", side_effect=store_metadata),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(manager, "write_json"),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "log_to_json"),
        ):
            accepted = manager.start_country_refresh("ALL")
            deadline = time.time() + 2
            snapshot = dict(manager.country_refresh_state)
            while snapshot.get("state") == "running" and time.time() < deadline:
                time.sleep(0.01)
                snapshot = dict(manager.country_refresh_state)

        self.assertEqual(accepted["state"], "running")
        self.assertEqual(snapshot["state"], "completed")
        self.assertEqual(snapshot["country"], "ALL")
        self.assertEqual(snapshot["officialCount"], 40)
        self.assertEqual(snapshot["testedCount"], 12)
        self.assertEqual(snapshot["usableCount"], 40)
        self.assertEqual(snapshot["cacheTotal"], 40)
        self.assertEqual(stored_metadata["lastRefresh"]["country"], "ALL")

    def test_country_refresh_preserves_other_countries_and_managed_slots(self):
        existing = [
            country_node("jp-old", "JP", "available"),
            country_node("jp-slot", "JP", "available"),
            country_node("us-existing", "US", "available"),
        ]
        candidates = [country_node(f"jp-new-{index}", "JP") for index in range(8)]
        stored_nodes = []

        def probe(batch):
            return [
                dict(
                    item,
                    probe_status="available",
                    exit_ip=f"203.0.113.{index + 10}",
                    exit_ip_checked_at=1_700_000_000 + index,
                )
                for index, item in enumerate(batch)
            ]

        def store(path, payload):
            if path == manager.NODES_FILE:
                stored_nodes[:] = payload

        with (
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(manager, "country_catalog_snapshot", return_value=[{"code": "JP", "candidateCount": 8}]),
            mock.patch.object(manager, "current_slot_node_ids", return_value={"jp-slot"}),
            mock.patch.object(manager, "probe_nodes", side_effect=probe),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "write_json", side_effect=store),
            mock.patch.object(manager, "set_state"),
        ):
            result = manager.refresh_country_nodes("JP", target_size=5, max_probes=20)

        self.assertEqual(result["state"], "completed")
        self.assertEqual(result["resultCode"], "success")
        self.assertEqual(result["testedCount"], 4)
        self.assertEqual(result["validCount"], 6)
        self.assertEqual(
            [item["id"] for item in stored_nodes],
            ["jp-slot", "jp-new-0", "jp-new-1", "jp-new-2", "jp-new-3", "us-existing", "jp-old"],
        )
        refreshed = [item for item in stored_nodes if item["id"].startswith("jp-new-")]
        self.assertTrue(all(item.get("exit_ip") for item in refreshed))

    def test_country_refresh_temporarily_grows_full_pool_and_reports_new_and_retained_counts(self):
        existing = [country_node("jp-old", "JP", "available")]
        existing.extend(
            country_node(f"us-old-{index:02d}", "US", "available")
            for index in range(63)
        )
        candidates = [country_node(f"jp-new-{index}", "JP") for index in range(2)]
        stored_nodes = []
        stored_metadata = {}

        def store(path, payload):
            if path == manager.NODES_FILE:
                stored_nodes[:] = payload

        def store_metadata(payload):
            stored_metadata.update(payload)

        with (
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(
                manager,
                "country_catalog_snapshot",
                return_value=[{"code": "JP", "candidateCount": 2}],
            ),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(
                manager,
                "probe_nodes",
                side_effect=lambda batch: [dict(item, probe_status="available") for item in batch],
            ),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata", side_effect=store_metadata),
            mock.patch.object(manager, "write_json", side_effect=store),
            mock.patch.object(manager, "set_state"),
        ):
            result = manager.refresh_country_nodes("JP", target_size=2, max_probes=2)

        self.assertEqual(len(stored_nodes), 66)
        self.assertEqual(result["cacheTotal"], 66)
        self.assertEqual(result["newUsableCount"], 2)
        self.assertEqual(result["retainedCount"], 1)
        self.assertEqual(result["countryValidCount"], 3)
        self.assertEqual(result["targetValidNodeCount"], 64)
        self.assertEqual(result["maxValidNodeCount"], 150)
        self.assertEqual(stored_metadata["manualProtectedIds"], ["jp-new-0", "jp-new-1"])

    def test_default_country_refresh_probes_every_unique_candidate_and_keeps_all_usable(self):
        existing = [
            country_node(f"us-old-{index:02d}", "US", "available")
            for index in range(64)
        ]
        candidates = [country_node(f"jp-{index:02d}", "JP") for index in range(27)]
        stored_nodes = []

        def store(path, payload):
            if path == manager.NODES_FILE:
                stored_nodes[:] = payload

        with (
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(
                manager,
                "country_catalog_snapshot",
                return_value=[{"code": "JP", "candidateCount": 42}],
            ),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(
                manager,
                "probe_nodes",
                side_effect=lambda batch: [dict(item, probe_status="available") for item in batch],
            ),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata"),
            mock.patch.object(manager, "write_json", side_effect=store),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "cleanup_unreferenced_node_configs", return_value={}),
        ):
            result = manager.refresh_country_nodes("JP")

        self.assertEqual(result["officialCount"], 42)
        self.assertEqual(result["countryCandidateCount"], 27)
        self.assertEqual(result["testedCount"], 27)
        self.assertEqual(result["passedCount"], 27)
        self.assertEqual(result["failedCount"], 0)
        self.assertEqual(result["newUsableCount"], 27)
        self.assertEqual(result["countryValidCount"], 27)
        self.assertEqual(result["cacheTotal"], 91)
        self.assertEqual(result["stopReason"], "candidates_exhausted")
        self.assertEqual(len(stored_nodes), 91)

    def test_explicit_country_refresh_rechecks_candidates_in_failure_cooldown(self):
        candidates = [
            country_node("jp-cooled", "JP"),
            country_node("jp-fresh", "JP"),
        ]
        probed = []

        def probe(batch):
            probed.extend(item["id"] for item in batch)
            return [dict(item, probe_status="available") for item in batch]

        with (
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(manager, "country_catalog_snapshot", return_value=[{"code": "JP", "candidateCount": 2}]),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(manager, "probe_nodes", side_effect=probe),
            mock.patch.object(
                manager,
                "load_blacklist",
                return_value={"jp-cooled": {"until": time.time() + 3600}},
            ),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata"),
            mock.patch.object(manager, "write_json"),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "cleanup_unreferenced_node_configs", return_value={}),
        ):
            result = manager.refresh_country_nodes("JP")

        self.assertEqual(probed, ["jp-cooled", "jp-fresh"])
        self.assertEqual(result["testedCount"], 2)
        self.assertEqual(result["passedCount"], 2)

    def test_country_refresh_drops_manual_required_candidate_from_available_pool(self):
        broken = country_node("jp-broken", "JP", "available")
        replacement = country_node("jp-new", "JP")
        stored_nodes = []

        def repair_state(key):
            if key == "slot:1":
                return {"status": "manual_required", "failed_candidate_id": "jp-broken"}
            return {}

        def store(path, payload):
            if path == manager.NODES_FILE:
                stored_nodes[:] = payload

        with (
            mock.patch.object(manager, "read_nodes", return_value=[broken]),
            mock.patch.object(manager.egress_repair_store, "get", side_effect=repair_state),
            mock.patch.object(manager, "fetch_candidates", return_value=[replacement]),
            mock.patch.object(manager, "country_catalog_snapshot", return_value=[{"code": "JP", "candidateCount": 1}]),
            mock.patch.object(manager, "current_slot_node_ids", return_value={"jp-broken"}),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value={"jp-broken"}),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(manager, "probe_nodes", return_value=[dict(replacement, probe_status="available")]),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata"),
            mock.patch.object(manager, "write_json", side_effect=store),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "cleanup_unreferenced_node_configs", return_value={}),
        ):
            result = manager.refresh_country_nodes("JP")

        self.assertNotIn("jp-broken", {item["id"] for item in stored_nodes})
        self.assertEqual(result["preservedCount"], 0)
        self.assertEqual(result["countryValidCount"], 1)

    def test_config_cleanup_removes_only_unreferenced_regular_node_files(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            keep = config_dir / "keep.ovpn"
            stale = config_dir / "stale.ovpn"
            slot = config_dir / ".slot_0.ovpn"
            probe = config_dir / ".test_active.ovpn"
            for path in (keep, stale, slot, probe):
                path.write_text("config", encoding="utf-8")

            with mock.patch.object(manager, "CONFIG_DIR", config_dir):
                result = manager.cleanup_unreferenced_node_configs([
                    {"id": "keep", "config_file": str(keep)},
                ])

            self.assertTrue(keep.exists())
            self.assertFalse(stale.exists())
            self.assertTrue(slot.exists())
            self.assertTrue(probe.exists())
            self.assertEqual(result["removedFiles"], 1)

    def test_config_cleanup_preserves_running_main_and_slot_candidates(self):
        with tempfile.TemporaryDirectory() as directory:
            config_dir = Path(directory)
            main = config_dir / "main-live.ovpn"
            slot = config_dir / "slot-live.ovpn"
            stale = config_dir / "stale.ovpn"
            for path in (main, slot, stale):
                path.write_text("config", encoding="utf-8")

            original_active_id = manager.active_openvpn_node_id
            manager.active_openvpn_node_id = "main-live"
            try:
                with (
                    mock.patch.object(manager, "CONFIG_DIR", config_dir),
                    mock.patch.object(
                        manager,
                        "reserved_slot_candidate_ids",
                        return_value={"slot-live"},
                    ),
                ):
                    result = manager.cleanup_unreferenced_node_configs([])
            finally:
                manager.active_openvpn_node_id = original_active_id

            self.assertTrue(main.exists())
            self.assertTrue(slot.exists())
            self.assertFalse(stale.exists())
            self.assertEqual(result["removedFiles"], 1)

    def test_country_refresh_stops_after_twenty_failed_real_probes(self):
        candidates = [country_node(f"jp-{index}", "JP") for index in range(30)]

        def fail(batch):
            return [
                dict(item, probe_status="unavailable", probe_message="dial_failed")
                for item in batch
            ]

        with (
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_candidates", return_value=candidates),
            mock.patch.object(manager, "country_catalog_snapshot", return_value=[{"code": "JP", "candidateCount": 30}]),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "probe_nodes", side_effect=fail),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "write_json"),
            mock.patch.object(manager, "set_state"),
        ):
            result = manager.refresh_country_nodes("JP", target_size=5, max_probes=20)

        self.assertEqual(result["state"], "completed")
        self.assertEqual(result["resultCode"], "no_usable_nodes")
        self.assertEqual(result["testedCount"], 20)
        self.assertEqual(result["validCount"], 0)
        self.assertEqual(result["stopReason"], "probe_limit_reached")

    def test_country_refresh_distinguishes_empty_official_catalog(self):
        with (
            mock.patch.object(manager, "read_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_candidates", return_value=[]),
            mock.patch.object(
                manager,
                "country_catalog_snapshot",
                return_value=[{"code": "US", "candidateCount": 4}],
            ),
            mock.patch.object(manager, "current_slot_node_ids", return_value=set()),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "write_json"),
            mock.patch.object(manager, "set_state"),
        ):
            result = manager.refresh_country_nodes("JP")

        self.assertEqual(result["state"], "completed")
        self.assertEqual(result["resultCode"], "no_official_candidates")
        self.assertEqual(result["officialCount"], 0)
        self.assertEqual(result["testedCount"], 0)
        self.assertEqual(result["usableCount"], 0)

    def test_country_refresh_maps_upstream_exception_without_exposing_it(self):
        with (
            mock.patch.object(manager, "read_nodes", return_value=[country_node("us-one", "US", "available")]),
            mock.patch.object(
                manager,
                "fetch_candidates",
                side_effect=RuntimeError("upstream detail must stay private"),
            ),
            mock.patch.object(manager, "write_json") as write,
            mock.patch.object(manager, "set_state"),
        ):
            result = manager.refresh_country_nodes("JP")

        self.assertEqual(result["state"], "failed")
        self.assertEqual(result["resultCode"], "upstream_unavailable")
        self.assertEqual(result["errorCode"], "upstream_unavailable")
        self.assertNotIn("detail", str(result).lower())
        write.assert_not_called()

    def test_country_refresh_busy_results_use_closed_result_codes(self):
        with mock.patch.object(
            manager.main_assignment_coordinator,
            "background_mutation_allowed",
            return_value=False,
        ):
            operation_busy = manager.refresh_country_nodes("JP")
        self.assertEqual(operation_busy["resultCode"], "operation_busy")

        manager.maintenance_lock.acquire()
        try:
            maintenance_busy = manager.refresh_country_nodes("JP")
        finally:
            manager.maintenance_lock.release()
        self.assertEqual(maintenance_busy["resultCode"], "maintenance_busy")

    def test_country_refresh_worker_releases_preacquired_maintenance_lock_when_mutation_is_busy(self):
        captured = {}
        original_state = manager.country_refresh_snapshot()

        class DeferredThread:
            def __init__(self, *, target, args, **_kwargs):
                captured["target"] = target
                captured["args"] = args

            def start(self):
                return None

        manager._set_country_refresh(state="idle", country="", phase="")
        try:
            with mock.patch.object(manager.threading, "Thread", DeferredThread):
                accepted = manager.start_country_refresh("JP")
            self.assertEqual(accepted["state"], "running")
            self.assertTrue(manager.maintenance_lock.locked())

            lease = manager.acquire_mutation_lease("worker-conflict")
            self.assertTrue(lease["ok"])
            try:
                captured["target"](*captured["args"])
            finally:
                manager.release_mutation_lease(lease["lease_id"])

            self.assertFalse(manager.maintenance_lock.locked())
            self.assertEqual(
                manager.country_refresh_snapshot()["resultCode"],
                "operation_busy",
            )
        finally:
            manager._set_country_refresh(**original_state)
            if manager.maintenance_lock.locked():
                manager.maintenance_lock.release()

    def test_country_refresh_worker_preserves_upstream_failure_code(self):
        original_state = manager.country_refresh_snapshot()
        try:
            manager._set_country_refresh(state="running", country="JP", phase="fetching")
            gate = manager.threading.Event()
            gate.set()
            with mock.patch.object(
                manager,
                "refresh_country_nodes",
                return_value={
                    "state": "failed",
                    "country": "JP",
                    "resultCode": "upstream_unavailable",
                    "errorCode": "upstream_unavailable",
                },
            ):
                manager._country_refresh_worker("JP", gate)

            snapshot = manager.country_refresh_snapshot()
            self.assertEqual(snapshot["state"], "failed")
            self.assertEqual(snapshot["resultCode"], "upstream_unavailable")
            self.assertEqual(snapshot["errorCode"], "upstream_unavailable")
        finally:
            manager._set_country_refresh(**original_state)

    def test_all_country_refresh_worker_preserves_operation_busy_code(self):
        original_state = manager.country_refresh_snapshot()
        try:
            manager._set_country_refresh(
                state="running",
                country="ALL",
                phase="fetching",
                startedAt=1_700_000_000,
            )
            gate = manager.threading.Event()
            gate.set()
            with mock.patch.object(manager, "maintain_valid_nodes", return_value="operation_busy"):
                manager._all_country_refresh_worker(gate)

            snapshot = manager.country_refresh_snapshot()
            self.assertEqual(snapshot["state"], "failed")
            self.assertEqual(snapshot["resultCode"], "operation_busy")
            self.assertEqual(snapshot["errorCode"], "operation_busy")
        finally:
            manager._set_country_refresh(**original_state)

    def test_country_refresh_new_round_clears_previous_result_code(self):
        captured = {}
        original_state = manager.country_refresh_snapshot()

        class DeferredThread:
            def __init__(self, *, target, args, **_kwargs):
                captured["target"] = target
                captured["args"] = args

            def start(self):
                return None

        manager._set_country_refresh(
            state="completed",
            country="US",
            phase="",
            resultCode="no_usable_nodes",
            errorCode="",
        )
        try:
            with mock.patch.object(manager.threading, "Thread", DeferredThread):
                accepted = manager.start_country_refresh("JP")

            self.assertEqual(accepted["state"], "running")
            self.assertEqual(accepted["country"], "JP")
            self.assertNotIn("resultCode", accepted)
        finally:
            manager._set_country_refresh(**original_state)
            if manager.maintenance_lock.locked():
                manager.maintenance_lock.release()

    def test_collector_delays_boot_scan_and_backs_off_after_failure(self):
        sleeps = []

        class StopLoop(Exception):
            pass

        def record_sleep(seconds):
            sleeps.append(seconds)
            if len(sleeps) == 2:
                raise StopLoop()

        with (
            mock.patch.object(manager, "COLLECTOR_INITIAL_DELAY_SECONDS", 120),
            mock.patch.object(manager, "COLLECTOR_FAILURE_BACKOFF_SECONDS", 600),
            mock.patch.object(manager, "maintain_valid_nodes", return_value="获取节点失败"),
            mock.patch.object(manager, "active_openvpn_running", return_value=False),
            mock.patch.object(manager, "log_to_json"),
            mock.patch.object(manager.time, "sleep", side_effect=record_sleep),
        ):
            with self.assertRaises(StopLoop):
                manager.collector_loop()

        self.assertEqual(sleeps, [120, 600])

    def test_collector_retries_operation_busy_after_ten_minutes(self):
        sleeps = []

        class StopLoop(Exception):
            pass

        def record_sleep(seconds):
            sleeps.append(seconds)
            raise StopLoop()

        with (
            mock.patch.object(manager, "COLLECTOR_INITIAL_DELAY_SECONDS", 0),
            mock.patch.object(manager, "COLLECTOR_BUSY_RETRY_SECONDS", 600),
            mock.patch.object(manager, "CHECK_INTERVAL_SECONDS", 21600),
            mock.patch.object(manager, "maintain_valid_nodes", return_value="operation_busy"),
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "log_to_json"),
            mock.patch.object(manager.time, "sleep", side_effect=record_sleep),
        ):
            with self.assertRaises(StopLoop):
                manager.collector_loop()

        self.assertEqual(sleeps, [600])

    def test_storage_sort_excludes_unavailable_and_untested_nodes(self):
        manager.active_openvpn_node_id = "live"
        try:
            stored = manager.sort_all_nodes(
                [
                    node(1, "available"),
                    node(2, "unavailable"),
                    node(3, "not_checked"),
                    {**node(4, "unavailable", True), "id": "live"},
                ]
            )
        finally:
            manager.active_openvpn_node_id = ""
        self.assertEqual([item["id"] for item in stored], ["n1", "live"])

    def test_replenish_preserves_running_and_pinned_managed_slot_nodes(self):
        existing = [
            {**node(0, "available"), "id": "main-live"},
            {**node(1, "available"), "id": "slot-live"},
            {**node(2, "available"), "id": "slot-pinned"},
        ]
        manager.active_openvpn_node_id = "main-live"
        try:
            with (
                mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 3),
                mock.patch.object(manager, "current_slot_node_ids", return_value={"slot-live"}),
                mock.patch.object(manager, "get_slot_pin_map", return_value={"2": "slot-pinned"}),
            ):
                pool, _, stats = manager.replenish_valid_pool(
                    existing,
                    [],
                    {},
                    lambda _batch: self.fail("protected nodes must not be reprobed"),
                    now=100.0,
                )
        finally:
            manager.active_openvpn_node_id = ""

        self.assertEqual(
            [item["id"] for item in pool],
            ["main-live", "slot-live", "slot-pinned"],
        )
        self.assertEqual(stats["tested"], 0)
        self.assertEqual(stats["stop_reason"], "target_reached")

    def test_replenishes_from_later_candidates_until_target(self):
        existing = [
            node(0, "available"),
            node(1, "available"),
            node(2, "unavailable"),
            node(3, "unavailable"),
        ]
        candidates = [node(index) for index in range(10)]
        calls = []

        def probe(batch):
            calls.append([item["id"] for item in batch])
            return [
                dict(
                    item,
                    probe_status=(
                        "unavailable" if item["id"] in {"n8", "n9"} else "available"
                    ),
                )
                for item in batch
            ]

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 5),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 2),
        ):
            pool, blacklist, stats = manager.replenish_valid_pool(
                existing, candidates, {}, probe, now=100.0
            )

        self.assertEqual(
            [item["id"] for item in pool],
            ["n0", "n1", "n4", "n5", "n6"],
        )
        self.assertEqual(
            calls,
            [["n0", "n1"], ["n4", "n5"], ["n6", "n7"]],
        )
        self.assertEqual(set(blacklist), {"n2", "n3"})
        self.assertEqual(stats["stop_reason"], "target_reached")

    def test_failure_replenishment_keeps_valid_pool_and_only_probes_gap(self):
        existing = [
            node(0, "available"),
            node(1, "available"),
            node(2, "available"),
            node(3, "available"),
            node(4, "unavailable"),
        ]
        candidates = [*existing, node(5), node(6)]
        calls = []

        def probe(batch):
            calls.append([item["id"] for item in batch])
            return [dict(item, probe_status="available") for item in batch]

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 5),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 2),
        ):
            pool, _, stats = manager.replenish_valid_pool(
                existing,
                candidates,
                {},
                probe,
                now=100.0,
                revalidate_existing=False,
            )

        self.assertEqual([item["id"] for item in pool], ["n0", "n1", "n2", "n3", "n5"])
        self.assertEqual(calls, [["n5"]])
        self.assertEqual(stats["tested"], 1)
        self.assertEqual(stats["stop_reason"], "target_reached")

    def test_replenishment_preserves_runtime_slot_nodes_without_redialing_them(self):
        existing = [
            {**node(0, "available"), "id": "main-live"},
            {**node(1, "available"), "id": "slot-live"},
            {**node(2, "available"), "id": "ordinary"},
        ]
        calls = []

        def probe(batch):
            calls.extend(item["id"] for item in batch)
            return [dict(item, probe_status="available") for item in batch]

        with (
            mock.patch.object(manager, "active_openvpn_node_id", "main-live"),
            mock.patch.object(manager, "current_slot_node_ids", return_value={"slot-live"}),
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 3),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 3),
        ):
            pool, _, stats = manager.replenish_valid_pool(
                existing, existing, {}, probe, now=100.0
            )

        self.assertEqual([item["id"] for item in pool[:2]], ["main-live", "slot-live"])
        self.assertEqual(calls, ["ordinary"])
        self.assertEqual(stats["stop_reason"], "target_reached")

    def test_stops_when_candidates_are_exhausted(self):
        def fail(batch):
            return [
                dict(item, probe_status="unavailable", probe_message="failed")
                for item in batch
            ]

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 3),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 2),
        ):
            pool, _, stats = manager.replenish_valid_pool(
                [], [node(1), node(2)], {}, fail, now=100.0
            )
        self.assertEqual(pool, [])
        self.assertEqual(stats["tested"], 2)
        self.assertEqual(stats["stop_reason"], "candidates_exhausted")

    def test_missing_probe_result_is_cooled_and_not_retried(self):
        calls = []

        def omit(batch):
            calls.append([item["id"] for item in batch])
            return []

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 1),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 1),
        ):
            pool, blacklist, stats = manager.replenish_valid_pool(
                [], [node(1)], {}, omit, now=100.0
            )
        self.assertEqual(pool, [])
        self.assertEqual(calls, [["n1"]])
        self.assertIn("n1", blacklist)
        self.assertEqual(stats["tested"], 1)

    def test_maintenance_does_not_restore_an_existing_node_that_just_failed(self):
        existing = [node(1, "available")]
        stored_nodes = []
        stored_blacklist = {}

        def store(path, payload):
            if path == manager.NODES_FILE:
                stored_nodes[:] = payload
            elif path == manager.BLACKLIST_FILE:
                stored_blacklist.update(payload)

        with (
            mock.patch.object(manager, "TARGET_VALID_POOL_SIZE", 1),
            mock.patch.object(manager, "NODE_TEST_BATCH_SIZE", 1),
            mock.patch.object(manager, "ensure_dirs"),
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(manager, "fetch_candidates", return_value=[node(1)]),
            mock.patch.object(manager, "probe_nodes", return_value=[node(1, "unavailable")]),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "load_pool_metadata", return_value=manager.default_pool_metadata()),
            mock.patch.object(manager, "store_pool_metadata"),
            mock.patch.object(manager, "reserved_slot_candidate_ids", return_value=set()),
            mock.patch.object(manager.main_assignment_coordinator, "reserved_candidate_ids", return_value=set()),
            mock.patch.object(manager, "write_json", side_effect=store),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "log_to_json"),
        ):
            manager.maintain_valid_nodes()

        self.assertEqual(stored_nodes, [])
        self.assertIn("n1", stored_blacklist)

    def test_api_failure_keeps_the_existing_pool(self):
        existing = [node(1, "available")]
        with (
            mock.patch.object(manager, "active_openvpn_running", return_value=True),
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(
                manager, "fetch_candidates", side_effect=RuntimeError("API down")
            ),
            mock.patch.object(manager.vpn_utils, "check_and_fix_dns"),
            mock.patch.object(
                manager.vpn_utils,
                "diagnose_api_failure",
                return_value=(1000, "API down"),
            ),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "write_json") as write,
        ):
            message = manager.maintain_valid_nodes()
        self.assertIn("保留现有有效节点", message)
        write.assert_not_called()

    def test_api_failure_recovers_main_connection_from_cached_pool(self):
        existing = [node(1, "available")]
        runtime = {"connected": False}

        def connect_cached_node():
            runtime["connected"] = True

        with (
            mock.patch.object(
                manager,
                "active_openvpn_running",
                side_effect=lambda: runtime["connected"],
            ),
            mock.patch.object(manager, "read_nodes", return_value=existing),
            mock.patch.object(
                manager, "fetch_candidates", side_effect=RuntimeError("API down")
            ),
            mock.patch.object(manager.vpn_utils, "check_and_fix_dns"),
            mock.patch.object(
                manager.vpn_utils,
                "diagnose_api_failure",
                return_value=(1000, "API down"),
            ),
            mock.patch.object(manager, "load_ui_config", return_value={"connection_enabled": True, "routing_mode": "auto"}),
            mock.patch.object(
                manager,
                "auto_switch_node",
                side_effect=connect_cached_node,
            ) as auto_switch,
            mock.patch.object(manager, "set_state"),
        ):
            message = manager.maintain_valid_nodes()

        self.assertIn("保留现有有效节点", message)
        auto_switch.assert_called_once_with()


class FetchCandidatesTests(unittest.TestCase):
    @staticmethod
    def api_text(count, countries=None):
        header = (
            "#HostName,IP,CountryShort,CountryLong,Score,Ping,Speed,"
            "NumVpnSessions,OpenVPN_ConfigData_Base64"
        )
        rows = []
        for index in range(count):
            ip = f"198.51.100.{index + 1}"
            country = (countries or ["US"] * count)[index]
            config = base64.b64encode(
                f"remote {ip} 443 tcp\nproto tcp\n".encode("utf-8")
            ).decode("ascii")
            country_long = "Japan" if country == "JP" else "United States"
            rows.append(f"host-{index},{ip},{country},{country_long},100,10,1000,1,{config}")
        return "\n".join([header, *rows])

    def test_country_fetch_filters_before_the_candidate_limit(self):
        text = self.api_text(5, ["US", "US", "JP", "JP", "JP"])
        with (
            mock.patch.object(manager, "MAX_FETCH_ROWS", 2),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "cached_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_api_text", return_value=text),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "log_to_json"),
            mock.patch.object(manager, "store_country_catalog"),
        ):
            candidates = manager.fetch_candidates("JP")

        self.assertEqual([item["country_short"] for item in candidates], ["JP", "JP"])

    def test_country_catalog_counts_raw_rows_without_node_secrets(self):
        rows = manager.parse_vpngate_rows(
            self.api_text(3, ["JP", "US", "JP"])
        )

        catalog = manager.build_country_catalog(rows, observed_at=100.0)

        self.assertEqual(
            catalog,
            [
                {"code": "JP", "name": "日本", "candidateCount": 2, "observedAt": 100.0},
                {"code": "US", "name": "美国", "candidateCount": 1, "observedAt": 100.0},
            ],
        )
        self.assertNotIn("OpenVPN_ConfigData_Base64", str(catalog))

    def test_fetch_reads_beyond_the_first_thirty_rows(self):
        with (
            mock.patch.object(manager, "MAX_FETCH_ROWS", 40),
            mock.patch.object(manager, "load_blacklist", return_value={}),
            mock.patch.object(manager, "cached_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_api_text", return_value=self.api_text(40)),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "log_to_json"),
        ):
            candidates = manager.fetch_candidates()
        self.assertEqual(len(candidates), 40)

    def test_successful_empty_snapshot_does_not_report_api_failure(self):
        cooling = {
            "US_198.51.100.1_443_tcp": {"until": 9999999999.0},
            "US_198.51.100.2_443_tcp": {"until": 9999999999.0},
        }
        with (
            mock.patch.object(manager, "MAX_FETCH_ROWS", 2),
            mock.patch.object(manager, "load_blacklist", return_value=cooling),
            mock.patch.object(manager, "cached_nodes", return_value=[]),
            mock.patch.object(manager, "fetch_api_text", return_value=self.api_text(2)),
            mock.patch.object(manager, "set_state"),
            mock.patch.object(manager, "log_to_json"),
            mock.patch.object(manager.vpn_utils, "diagnose_api_failure") as diagnose,
        ):
            candidates = manager.fetch_candidates()
        self.assertEqual(candidates, [])
        diagnose.assert_not_called()


if __name__ == "__main__":
    unittest.main()
