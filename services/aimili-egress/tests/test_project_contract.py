import pathlib
import unittest


SERVICE_ROOT = pathlib.Path(__file__).resolve().parents[1]
GATEWAY_ROOT = pathlib.Path(__file__).resolve().parents[3]


class ProjectContractTests(unittest.TestCase):
    def test_embedded_engine_contains_every_runtime_module(self):
        for name in (
            "vpngate_manager.py",
            "egress_repair.py",
            "control_api.py",
            "node_pool.py",
            "proxy_server.py",
            "vpn_utils.py",
            "main_assignment.py",
        ):
            self.assertTrue((SERVICE_ROOT / name).is_file(), name)

    def test_readme_declares_one_source_repository_and_two_processes(self):
        text = (SERVICE_ROOT / "README.md").read_text(encoding="utf-8")
        self.assertIn("唯一源码来源", text)
        self.assertIn("两个独立服务", text)
        self.assertIn("ed102e3", text)

    def test_systemd_unit_runs_embedded_source_with_persistent_data(self):
        unit = (GATEWAY_ROOT / "deploy" / "systemd" / "aimilivpn.service").read_text(
            encoding="utf-8"
        )
        self.assertIn("/opt/aimili-gateway/services/aimili-egress/vpngate_manager.py", unit)
        self.assertIn("VPNGATE_DATA_DIR=/opt/aimilivpn/vpngate_data", unit)
        self.assertIn("AIMILI_CONTROL_ADDRESS=127.0.0.1:8790", unit)

    def test_split_repository_installer_is_not_copied(self):
        self.assertFalse((SERVICE_ROOT / "install.sh").exists())


if __name__ == "__main__":
    unittest.main()
