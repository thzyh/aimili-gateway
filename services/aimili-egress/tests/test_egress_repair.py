import json
import tempfile
import unittest
from pathlib import Path

from egress_repair import RepairStore


class RepairStoreTests(unittest.TestCase):
    def test_same_failure_can_claim_only_one_automatic_repair(self):
        with tempfile.TemporaryDirectory() as directory:
            store = RepairStore(Path(directory) / "repair.json", now=lambda: 10.0)

            self.assertTrue(store.claim("slot:1", "jp-broken", "JP"))
            self.assertFalse(store.claim("slot:1", "jp-broken", "JP"))

    def test_manual_required_survives_process_restart(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            first_process = RepairStore(path, now=lambda: 10.0)
            first_process.claim("slot:1", "jp-broken", "JP")
            first_process.require_manual("slot:1", "replacement_failed", "jp-new")

            restarted_process = RepairStore(path, now=lambda: 20.0)

            self.assertFalse(restarted_process.claim("slot:1", "jp-broken", "JP"))
            self.assertEqual(restarted_process.get("slot:1")["status"], "manual_required")
            self.assertEqual(restarted_process.get("slot:1")["attempt_count"], 1)

    def test_unresolved_failure_does_not_reclaim_when_candidate_identity_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            store = RepairStore(path, now=lambda: 10.0)
            self.assertTrue(store.claim("slot:1", "jp-broken", "JP"))
            store.require_manual("slot:1", "replacement_failed", "jp-new")

            restarted_process = RepairStore(path, now=lambda: 20.0)

            self.assertFalse(restarted_process.claim("slot:1", "", "JP"))
            self.assertFalse(restarted_process.claim("slot:1", "jp-different", "JP"))
            self.assertEqual(restarted_process.get("slot:1")["attempt_count"], 1)

    def test_healthy_recovery_allows_a_later_new_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "repair.json"
            store = RepairStore(path, now=lambda: 10.0)
            store.claim("main", "jp-old", "JP")
            store.require_manual("main", "replacement_failed")
            store.mark_healthy("main", "jp-manual")

            self.assertTrue(store.claim("main", "jp-manual", "JP"))
            self.assertEqual(json.loads(path.read_text(encoding="utf-8"))["version"], 1)


if __name__ == "__main__":
    unittest.main()
