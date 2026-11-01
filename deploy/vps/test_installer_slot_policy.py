import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class InstallerSlotPolicyTests(unittest.TestCase):
    def test_missing_policy_is_initialized_without_changing_existing_fields(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "ui_auth.json"
            path.write_text('{"exit_slot_count":4,"username":"fixture"}')
            installer.ensure_slot_selection_policy(path)
            data = installer.load_json(path)
        self.assertFalse(data["exit_slot_residential_only"])
        self.assertEqual(data["exit_slot_count"], 4)
        self.assertEqual(data["username"], "fixture")

    def test_explicit_policy_is_never_overwritten(self):
        for value in [True, False]:
            with self.subTest(value=value), patch.object(installer, "load_json", return_value={"exit_slot_residential_only": value}), \
                 patch.object(installer, "write_json") as write:
                installer.ensure_slot_selection_policy(Path("unused.json"))
                write.assert_not_called()


if __name__ == "__main__":
    unittest.main()
