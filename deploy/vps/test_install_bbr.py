"""直接验证一键引导脚本内的 BBR 步骤，不访问网络或修改宿主设置。"""

from contextlib import redirect_stdout
import io
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import types
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("install.sh").read_text(encoding="utf-8")


class InstallBBRTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.etc = Path(self.directory.name)
        self.module = types.ModuleType("aimili_bbr_test")
        self.assertTrue("<<'AIMILI_BBR'\n" in SCRIPT, "一键入口尚未包含 BBR 步骤")
        code = SCRIPT.split("<<'AIMILI_BBR'\n", 1)[1].split("\nAIMILI_BBR\n", 1)[0]
        exec(compile(code, "install.sh:BBR", "exec"), self.module.__dict__)
        self.module.ETC = self.etc
        self.algorithm = "cubic"
        self.available = "reno cubic"
        self.calls = []
        self.fail = ""
        self.output = io.StringIO()
        self.run_patch = patch.object(self.module.subprocess, "run", side_effect=self.run_command)
        self.run_patch.start()
        self.addCleanup(self.run_patch.stop)

    def run_command(self, args, **kwargs):
        self.calls.append(args)
        self.assertLessEqual(kwargs["timeout"], 15)
        if self.fail and self.fail in " ".join(args):
            return subprocess.CompletedProcess(args, 1, "", "fixture: operation denied")
        if args == ["modprobe", "tcp_bbr"]:
            self.available += " bbr"
        if args == ["sysctl", "-n", "net.ipv4.tcp_available_congestion_control"]:
            return subprocess.CompletedProcess(args, 0, self.available, "")
        if args == ["sysctl", "-n", "net.ipv4.tcp_congestion_control"]:
            return subprocess.CompletedProcess(args, 0, self.algorithm, "")
        if args[:2] == ["sysctl", "-w"]:
            self.algorithm = args[2].split("=", 1)[1]
        return subprocess.CompletedProcess(args, 0, "", "")

    def configure(self):
        with redirect_stdout(self.output):
            return self.module.enable_bbr()

    def test_loads_enables_and_persists_without_restarting_services(self):
        self.assertTrue(self.configure())
        self.assertEqual(self.algorithm, "bbr")
        self.assertIn(["modprobe", "tcp_bbr"], self.calls)
        self.assertEqual((self.etc / "modules-load.d/aimili-bbr.conf").read_text(), "tcp_bbr\n")
        self.assertIn("net.ipv4.tcp_congestion_control = bbr",
                      (self.etc / "sysctl.d/zz-aimili-bbr.conf").read_text())
        self.assertFalse(any(c[0] in ("systemctl", "tc", "ip", "apt-get") for c in self.calls))
        self.assertIn("已开启并持久化 BBR", self.output.getvalue())

    def test_available_bbr_does_not_require_modprobe(self):
        self.available += " bbr"
        self.algorithm = "bbr"
        self.assertTrue(self.configure())
        self.assertNotIn(["modprobe", "tcp_bbr"], self.calls)

    def test_repeated_run_is_idempotent_and_preserves_other_settings(self):
        foreign = self.etc / "sysctl.d/10-other-project.conf"
        foreign.parent.mkdir()
        foreign.write_text("net.ipv4.ip_forward = 1\n")
        self.assertTrue(self.configure())
        before = {p.relative_to(self.etc): p.read_bytes() for p in self.etc.rglob("*.conf")}
        self.assertTrue(self.configure())
        self.assertEqual(before, {p.relative_to(self.etc): p.read_bytes() for p in self.etc.rglob("*.conf")})
        self.assertEqual(foreign.read_text(), "net.ipv4.ip_forward = 1\n")

    def test_missing_module_continues_with_reason_and_upgrade_path(self):
        self.fail = "modprobe"
        self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertFalse(list(self.etc.rglob("*.conf")))
        self.assertIn("fixture: operation denied", self.output.getvalue())
        self.assertIn("linux-modules-extra-$(uname -r)", self.output.getvalue())
        self.assertIn("继续安装", self.output.getvalue())

    def test_denied_sysctl_restores_existing_managed_files(self):
        module_file = self.etc / "modules-load.d/aimili-bbr.conf"
        module_file.parent.mkdir()
        module_file.write_text("# existing managed content\n")
        self.fail = "net.ipv4.tcp_congestion_control=bbr"
        self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertEqual(module_file.read_text(), "# existing managed content\n")
        self.assertFalse((self.etc / "sysctl.d/zz-aimili-bbr.conf").exists())
        self.assertNotIn("已开启并持久化 BBR", self.output.getvalue())

    def test_persistence_failure_restores_previous_runtime(self):
        with patch.object(self.module, "atomic_write", side_effect=OSError("fixture: disk full")):
            self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertIn("fixture: disk full", self.output.getvalue())

    def test_second_file_failure_removes_partial_persistence(self):
        original_write = self.module.atomic_write

        def fail_sysctl_file(path, body):
            if path.parent.name == "sysctl.d":
                raise OSError("fixture: sysctl directory read-only")
            original_write(path, body)

        with patch.object(self.module, "atomic_write", side_effect=fail_sysctl_file):
            self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertFalse(list(self.etc.rglob("*.conf")))

    def test_runtime_readback_must_confirm_bbr(self):
        def ignore_setting(args, **kwargs):
            result = self.run_command(args, **kwargs)
            if args == ["sysctl", "-w", "net.ipv4.tcp_congestion_control=bbr"]:
                self.algorithm = "cubic"
            return result

        with patch.object(self.module.subprocess, "run", side_effect=ignore_setting):
            self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertFalse(list(self.etc.rglob("*.conf")))
        self.assertIn("内核回读不是 bbr", self.output.getvalue())

    def test_timeout_reports_bounded_failure_and_continues(self):
        with patch.object(self.module.subprocess, "run", side_effect=subprocess.TimeoutExpired("sysctl", 15)):
            self.assertFalse(self.configure())
        self.assertEqual(self.algorithm, "cubic")
        self.assertIn("15 seconds", self.output.getvalue())
        self.assertIn("继续安装", self.output.getvalue())


class BootstrapBBRGateTests(unittest.TestCase):
    def test_status_and_help_skip_bbr_but_install_arguments_enable_it(self):
        bash = shutil.which("bash")
        if os.name == "nt":
            git = shutil.which("git")
            if git:
                bash = str(Path(git).parent.parent / "bin/bash.exe")
        self.assertTrue(bash, "需要明确的 Bash 路径以验证引导流程")
        self.assertTrue("# BBR_BOOTSTRAP_BEGIN\n" in SCRIPT, "一键入口缺少只读参数门禁")
        gate = SCRIPT.split("# BBR_BOOTSTRAP_BEGIN\n", 1)[1].split("# BBR_BOOTSTRAP_END", 1)[0]
        for args, expected in [((), "BBR"), (("--no-domain",), "BBR"),
                               (("--status",), ""), (("--help",), ""), (("-h",), "")]:
            with self.subTest(args=args):
                result = subprocess.run([bash, "-c", "set -euo pipefail\nconfigure_bbr() { printf BBR; }\n" + gate,
                                         "bbr-test", *args], capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, expected)


if __name__ == "__main__":
    unittest.main()
