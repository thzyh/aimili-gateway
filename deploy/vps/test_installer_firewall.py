import os
import sys
import types
import unittest
from unittest.mock import patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class InstallerFirewallTests(unittest.TestCase):
    def test_mixed_ports_are_publicly_reachable_and_old_source_rules_removed(self):
        calls = []

        def run(args, **_kwargs):
            calls.append(args)
            if args == ["ufw", "status"]:
                return "30000/tcp ALLOW IN 198.51.100.23\n31000/tcp ALLOW IN 198.51.100.23"
            return ""

        with patch.object(installer, "run", side_effect=run), \
             patch.object(installer, "checkpoint"):
            installer.firewall(4, "198.51.100.23")
        self.assertIn(["ufw", "allow", "30000/tcp"], calls)
        self.assertIn(["ufw", "allow", "8443/tcp"], calls)
        self.assertIn(["ufw", "allow", "31000/tcp"], calls)
        self.assertIn(["ufw", "--force", "delete", "allow", "from", "198.51.100.23", "to", "any", "port", "30000", "proto", "tcp"], calls)
        self.assertFalse(any("from" in call and call[:2] == ["ufw", "allow"] for call in calls))


if __name__ == "__main__":
    unittest.main()
