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


class InstallerPackagesTests(unittest.TestCase):
    def test_swap_is_ready_before_apt(self):
        calls = []
        with patch.object(installer, "ensure_swap", side_effect=lambda: calls.append("swap")), \
             patch.object(installer, "run", side_effect=lambda args, **_kw: calls.append(args[0]) or ""), \
             patch.object(installer, "say"), \
             patch.object(installer, "checkpoint"), \
             patch.object(installer.shutil, "which", return_value="/usr/sbin/openvpn"):
            installer.system_packages()
        self.assertEqual(calls, ["swap", "apt-get", "apt-get"])

    def test_swap_fstab_entry_is_added_once(self):
        with tempfile.TemporaryDirectory() as folder:
            fstab = Path(folder) / "fstab"
            swap = Path("/swapfile")
            fstab.write_text("# existing file\nUUID=abc / ext4 defaults 0 1\n", encoding="utf-8")
            def local_write(path, body, *_args):
                path.write_text(body, encoding="utf-8")
            with patch.object(installer, "write", side_effect=local_write):
                installer.add_swap_fstab_entry(fstab, swap)
                installer.add_swap_fstab_entry(fstab, swap)
            self.assertEqual(fstab.read_text(encoding="utf-8").count(f"{swap} none swap sw 0 0"), 1)


if __name__ == "__main__":
    unittest.main()
