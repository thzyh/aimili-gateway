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
    def test_sudo_pipe_uses_matching_stderr_tty_in_who_records(self):
        with patch.dict(os.environ, {"SSH_CLIENT": "", "SSH_CONNECTION": ""}), \
             patch.object(installer.os, "ttyname", return_value="/dev/pts/2", create=True), \
             patch.object(installer.subprocess, "run", return_value=types.SimpleNamespace(
                 returncode=0, stdout="root pts/2 2026-09-25 06:00 (198.51.100.23)\n"
                                          "root pts/3 2026-09-25 06:01 (203.0.113.44)\n")) as who:
            self.assertEqual(installer.suggested_source_ipv4(), "198.51.100.23")
            self.assertEqual(who.call_args.args[0], ["who"])

    def test_missing_ssh_source_defaults_to_loopback_not_public(self):
        with patch.dict(os.environ, {"SSH_CLIENT": "", "SSH_CONNECTION": ""}), \
             patch.object(installer.os, "ttyname", side_effect=OSError("no tty"), create=True):
            self.assertEqual(installer.suggested_source_ipv4(), "127.0.0.1")

    def test_enter_accepts_detected_source_during_first_install(self):
        args = argparse.Namespace(domain=None, no_domain=True, slots=4, allowed_source=None)
        with patch.object(installer, "public_ip", return_value="203.0.113.8"), \
             patch.object(installer, "load_json", return_value={}), \
             patch.object(installer, "suggested_source_ipv4", return_value="198.51.100.23"), \
             patch.object(installer, "prompt", side_effect=lambda _label, default: default) as prompt:
            self.assertEqual(installer.desired(args),
                             ("https://203.0.113.8", "", 4, "198.51.100.23"))
            self.assertEqual(prompt.call_args.args[1], "198.51.100.23")

    def test_invalid_interactive_source_is_reprompted(self):
        args = argparse.Namespace(domain=None, no_domain=True, slots=4, allowed_source=None)
        with patch.object(installer, "public_ip", return_value="203.0.113.8"), \
             patch.object(installer, "load_json", return_value={}), \
             patch.object(installer, "suggested_source_ipv4", return_value="198.51.100.23"), \
             patch.object(installer, "prompt", side_effect=["bad", "198.51.100.23"]) as prompt:
            self.assertEqual(installer.desired(args)[3], "198.51.100.23")
            self.assertEqual(prompt.call_count, 2)


if __name__ == "__main__":
    unittest.main()
