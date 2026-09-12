import json
import os
import subprocess
import sys
import unittest
from pathlib import Path


class ConfigDefaultsTests(unittest.TestCase):
    def test_unconfigured_service_reports_fifty_node_pool_target(self):
        service_dir = Path(__file__).resolve().parents[1]
        environment = os.environ.copy()
        environment.pop("TARGET_VALID_NODES", None)
        environment.pop("TARGET_VALID_POOL_SIZE", None)
        command = """
import json
import vpngate_manager as manager

manager.read_json = lambda _path, default: dict(default)
manager.load_ui_config = lambda: {}
print(json.dumps(manager.get_state()))
"""

        completed = subprocess.run(
            [sys.executable, "-c", command],
            cwd=service_dir,
            env=environment,
            check=True,
            capture_output=True,
            text=True,
        )

        state = json.loads(completed.stdout)
        self.assertEqual(state["target_valid_nodes"], 50)


if __name__ == "__main__":
    unittest.main()
