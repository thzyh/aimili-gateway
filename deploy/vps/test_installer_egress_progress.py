import os
import sys
import types
import unittest
from unittest.mock import patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class Clock:
    now = 0

    def monotonic(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class EgressProgressTests(unittest.TestCase):
    def test_timeout_distinguishes_reserved_pool_from_missing_candidates(self):
        clock = Clock()

        def control(path):
            return {
                "main": {"active": True, "egress_ok": True},
                "slots": [{"slot": 0, "egress_ok": True}],
                "candidates": [],
                "capacity": {"currentValidNodeCount": 10},
                "standbys": [{"status": "ready"}] * 5,
            }[path]

        with patch.object(installer, "control", side_effect=control), \
             patch.object(installer, "load_json", return_value={"exit_slot_residential_only": True}), \
             patch.object(installer, "say"), \
             patch.object(installer.time, "monotonic", side_effect=clock.monotonic), \
             patch.object(installer.time, "sleep", side_effect=clock.sleep):
            with self.assertRaises(installer.InstallError) as raised:
                installer.wait_egress(4)
        message = str(raised.exception)
        for expected in ["未占用候选 0", "有效缓存 10", "热备用就绪 5", "类型策略仅住宅", "未就绪出口位 2,3,4"]:
            self.assertIn(expected, message)

    def test_wait_reports_elapsed_time_and_ready_slot_count(self):
        clock = Clock()
        messages = []

        def control(path):
            if path == "main":
                return {"active": True, "egress_ok": True}
            if path == "slots":
                ready = 4 if clock.now >= 40 else 2
                return [{"slot": number, "egress_ok": number < ready} for number in range(4)]
            if path == "candidates":
                return [{}] * 8
            raise AssertionError(path)

        with patch.object(installer, "control", side_effect=control), \
             patch.object(installer, "say", side_effect=messages.append), \
             patch.object(installer, "checkpoint"), \
             patch.object(installer.time, "monotonic", side_effect=clock.monotonic), \
             patch.object(installer.time, "sleep", side_effect=clock.sleep):
            installer.wait_egress(4)
        self.assertTrue(any("已等待 0 秒" in item and "2/4" in item for item in messages))
        self.assertTrue(any("已等待 30 秒" in item for item in messages))


if __name__ == "__main__":
    unittest.main()
