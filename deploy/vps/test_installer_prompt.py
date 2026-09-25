import io
import sys
import unittest
from unittest.mock import patch

if sys.platform != "win32":
    import installer


@unittest.skipIf(sys.platform == "win32", "VPS installer requires Linux fcntl")
class InstallerPromptTests(unittest.TestCase):
    def test_pipe_installer_uses_separate_tty_read_and_write_streams(self):
        terminal_input = io.StringIO("\n")
        class CapturedOutput(io.StringIO):
            def close(self):
                pass

        terminal_output = CapturedOutput()
        modes = []

        def open_tty(path, mode, encoding):
            self.assertEqual(path, "/dev/tty")
            self.assertEqual(encoding, "utf-8")
            modes.append(mode)
            return terminal_input if mode == "r" else terminal_output

        with patch("builtins.open", side_effect=open_tty):
            self.assertEqual(installer.prompt("选择部署模式", "1"), "1")
        self.assertEqual(modes, ["r", "w"])
        self.assertEqual(terminal_output.getvalue(), "选择部署模式 [1]：")

    def test_missing_or_closed_tty_has_actionable_error(self):
        with patch("builtins.open", side_effect=OSError("No such device")):
            with self.assertRaisesRegex(installer.InstallError, "--no-domain/--domain"):
                installer.prompt("选择部署模式", "1")
        with patch("builtins.open", side_effect=lambda *_args, **_kwargs: io.StringIO("")):
            with self.assertRaisesRegex(installer.InstallError, "交互输入已结束"):
                installer.prompt("选择部署模式", "1")


if __name__ == "__main__":
    unittest.main()
