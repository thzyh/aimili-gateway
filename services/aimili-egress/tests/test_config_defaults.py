import json
import os
import subprocess
import sys
import unittest
from pathlib import Path


class ConfigDefaultsTests(unittest.TestCase):
    def test_unconfigured_service_uses_sixty_four_target_and_eighty_temporary_limit(self):
        service_dir = Path(__file__).resolve().parents[1]
        environment = os.environ.copy()
        environment.pop("TARGET_VALID_NODES", None)
        environment.pop("TARGET_VALID_POOL_SIZE", None)
        environment.pop("MAX_VALID_POOL_SIZE", None)
        environment.pop("COLLECTOR_BUSY_RETRY_SECONDS", None)
        environment.pop("FETCH_INTERVAL_SECONDS", None)
        environment.pop("CHECK_INTERVAL_SECONDS", None)
        command = """
import json
import vpngate_manager as manager

manager.read_json = lambda _path, default: dict(default)
manager.load_ui_config = lambda: {}
print(json.dumps({
    "state": manager.get_state(),
    "maximum": getattr(manager, "MAX_VALID_POOL_SIZE", 0),
    "busyRetry": getattr(manager, "COLLECTOR_BUSY_RETRY_SECONDS", 0),
    "fetchInterval": getattr(manager, "FETCH_INTERVAL_SECONDS", 0),
    "checkInterval": getattr(manager, "CHECK_INTERVAL_SECONDS", 0),
}))
"""

        completed = subprocess.run(
            [sys.executable, "-c", command],
            cwd=service_dir,
            env=environment,
            check=True,
            capture_output=True,
            text=True,
        )

        result = json.loads(completed.stdout)
        self.assertEqual(result["state"]["target_valid_nodes"], 64)
        self.assertEqual(result["maximum"], 80)
        self.assertEqual(result["busyRetry"], 600)
        self.assertEqual(result["fetchInterval"], 21600)
        self.assertEqual(result["checkInterval"], 21600)


if __name__ == "__main__":
    unittest.main()
