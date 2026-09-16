import concurrent.futures
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

import vpngate_manager as manager


class ProbeBatchTests(unittest.TestCase):
    def test_openvpn_probe_processes_never_exceed_two(self):
        active = 0
        peak = 0
        counter_lock = threading.Lock()

        def fake_openvpn(*_args, **_kwargs):
            nonlocal active, peak
            with counter_lock:
                active += 1
                peak = max(peak, active)
            time.sleep(0.05)
            with counter_lock:
                active -= 1
            return False, "probe failed", None

        items = [
            {
                "id": f"node-{index}",
                "config_text": "remote 127.0.0.1 443 tcp",
                "remote_host": "127.0.0.1",
                "remote_port": 443,
                "ping": 1,
            }
            for index in range(4)
        ]
        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(manager, "CONFIG_DIR", Path(temporary)),
                mock.patch.object(manager, "openvpn_probe_capacity", threading.BoundedSemaphore(2)),
                mock.patch.object(manager.vpn_utils, "ping_latency_ms", return_value=1),
                mock.patch.object(manager, "run_openvpn_until_ready", side_effect=fake_openvpn),
            ):
                with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                    results = list(pool.map(manager._probe_one_node, items))

        self.assertEqual(len(results), 4)
        self.assertEqual(peak, 2)

    def test_probe_nodes_returns_one_result_per_input_without_persisting(self):
        items = [
            {
                "id": "a",
                "config_text": "remote 127.0.0.1 443 tcp",
                "remote_host": "127.0.0.1",
                "remote_port": 443,
                "ping": 1,
            },
            {
                "id": "b",
                "config_text": "remote 127.0.0.2 443 tcp",
                "remote_host": "127.0.0.2",
                "remote_port": 443,
                "ping": 2,
            },
        ]

        def result(item):
            status = "available" if item["id"] == "a" else "unavailable"
            return dict(item, probe_status=status)

        with (
            mock.patch.object(manager, "tcp_prescreen_dead", return_value={}),
            mock.patch.object(manager.vpn_utils, "enrich_ip_info"),
            mock.patch.object(manager, "_probe_one_node", side_effect=result) as probe,
            mock.patch.object(manager, "write_json") as write,
        ):
            actual = manager.probe_nodes(items)

        self.assertEqual([item["id"] for item in actual], ["a", "b"])
        self.assertEqual(probe.call_count, 2)
        write.assert_not_called()


if __name__ == "__main__":
    unittest.main()
