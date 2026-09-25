import io
import os
import sys
import types
import unittest
from unittest.mock import MagicMock, patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class InitialCredentialsTests(unittest.TestCase):
    def test_credentials_are_written_only_to_interactive_terminal(self):
        terminal = io.StringIO()
        opened = MagicMock()
        opened.__enter__.return_value = terminal
        with patch.object(installer, "open", return_value=opened, create=True) as access, \
             patch.object(installer, "say") as log:
            installer.show_initial_credentials("https://example.test", {
                "username": "example-user", "password": "example-password"})
        access.assert_called_once_with("/dev/tty", "w", encoding="utf-8")
        self.assertIn("example-user", terminal.getvalue())
        self.assertIn("example-password", terminal.getvalue())
        log.assert_not_called()


if __name__ == "__main__":
    unittest.main()
