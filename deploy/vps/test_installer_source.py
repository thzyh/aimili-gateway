import argparse
import os
import sys
import types
import unittest
from unittest.mock import patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class InstallerSourceTests(unittest.TestCase):
    def args(self, **changes):
        values = dict(domain=None, no_domain=True, slots=4, allowed_source=None,
                      disable_source_limit=False, interactive=True)
        values.update(changes)
        return argparse.Namespace(**values)

    def test_enter_disables_source_limit_on_first_install(self):
        with patch.object(installer, "public_ip", return_value="203.0.113.8"), \
             patch.object(installer, "load_json", return_value={}), \
             patch.object(installer, "prompt", return_value="") as prompt:
            self.assertEqual(installer.desired(self.args()),
                             ("https://203.0.113.8", "", 4, ""))
            self.assertEqual(len(prompt.call_args.args), 1)

    def test_interactive_address_enables_source_limit(self):
        with patch.object(installer, "public_ip", return_value="203.0.113.8"), \
             patch.object(installer, "load_json", return_value={}), \
             patch.object(installer, "prompt", side_effect=["bad", "198.51.100.23"]) as prompt:
            self.assertEqual(installer.desired(self.args())[3], "198.51.100.23")
            self.assertEqual(prompt.call_count, 2)

    def test_noninteractive_rerun_preserves_existing_policy_unless_explicitly_disabled(self):
        old = {"allowedSource": "198.51.100.23", "slots": 4}
        with patch.object(installer, "public_ip", return_value="203.0.113.8"), \
             patch.object(installer, "load_json", return_value=old):
            self.assertEqual(installer.desired(self.args(interactive=False))[3], "198.51.100.23")
            self.assertEqual(installer.desired(self.args(interactive=False, disable_source_limit=True))[3], "")


if __name__ == "__main__":
    unittest.main()
