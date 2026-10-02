import os
import queue
import subprocess
import tempfile
import threading
import time
import unittest
from contextlib import ExitStack
from pathlib import Path
from unittest import mock

# 导入时也使用临时运行目录，避免模块初始化读取工作区中的历史配置。
with tempfile.TemporaryDirectory() as bootstrap_directory:
    with mock.patch.dict(os.environ, {"VPNGATE_DATA_DIR": bootstrap_directory}):
        import vpngate_manager as manager


class FakeOpenVPN:
    """真实 reader/watcher 线程使用的受控进程，不启动 OpenVPN。"""

    def __init__(self, *lines):
        self.lines = queue.Queue()
        self.finished = threading.Event()
        self.stdout = self
        for line in lines:
            self.lines.put(line + "\n")

    def __iter__(self):
        while True:
            line = self.lines.get()
            if line is None:
                return
            yield line

    def push(self, line):
        self.lines.put(line + "\n")

    def poll(self):
        return 0 if self.finished.is_set() else None

    def wait(self, timeout=None):
        if not self.finished.wait(timeout):
            raise subprocess.TimeoutExpired("fake-openvpn", timeout)
        return 0

    def terminate(self):
        self.finished.set()
        self.lines.put(None)

    kill = terminate


class PushedDNSTests(unittest.TestCase):
    def setUp(self):
        self.patches = ExitStack()
        self.addCleanup(self.patches.close)
        directory = self.patches.enter_context(tempfile.TemporaryDirectory())
        self.directory = Path(directory)
        self.patches.enter_context(mock.patch.dict(os.environ, {"VPNGATE_DATA_DIR": directory}))
        self.patches.enter_context(mock.patch.object(manager, "DATA_DIR", self.directory))
        self.patches.enter_context(mock.patch.object(manager, "CONFIG_DIR", self.directory / "configs"))
        self.patches.enter_context(mock.patch.object(manager, "_openvpn_dns_owners", {}))
        self.patches.enter_context(mock.patch.object(manager, "openvpn_capacity", threading.BoundedSemaphore(2)))
        self.patches.enter_context(mock.patch.object(manager, "openvpn_command", return_value=["fake-openvpn"]))
        self.patches.enter_context(mock.patch.object(manager, "log_to_json"))
        self.patches.enter_context(mock.patch.object(manager, "update_handshake_status"))
        self.processes = []
        self.threads = []
        real_thread = threading.Thread

        def create_thread(*args, **kwargs):
            thread = real_thread(*args, **kwargs)
            self.threads.append(thread)
            return thread

        self.patches.enter_context(mock.patch.object(manager.threading, "Thread", side_effect=create_thread))
        self.addCleanup(self.finish_processes)

    def finish_processes(self):
        for process in self.processes:
            process.terminate()
        for thread in self.threads:
            thread.join(1)
            self.assertFalse(thread.is_alive(), "受控进程 reader/watcher 必须退出")
        manager.proxy_server.purge_dns_cache()
        manager.proxy_server._tun_dns_servers.clear()

    def run_process(self, lines, *, keep_alive):
        process = FakeOpenVPN(*lines)
        self.processes.append(process)
        with mock.patch.object(manager.subprocess, "Popen", return_value=process):
            result = manager.run_openvpn_until_ready(
                str(self.directory / "candidate.ovpn"), keep_alive, True,
                timeout=1, dev="tun121", report_status=False,
            )
        return result, process

    def wait_for(self, predicate):
        deadline = time.monotonic() + 1
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.005)
        self.fail("受控 OpenVPN 线程未在 1 秒内完成 DNS 生命周期动作")

    def test_only_push_reply_registers_ipv4_dns_for_its_device(self):
        with mock.patch.object(manager.proxy_server, "register_tun_dns_servers") as register:
            manager.register_pushed_dns("tun121", "PUSH: 'PUSH_REPLY,dhcp-option DNS 10.211.254.254,dhcp-option DNS 8.8.8.8,dhcp-option DNS6 2001:db8::1'")
            register.assert_called_once_with("tun121", ["10.211.254.254", "8.8.8.8"])
            register.reset_mock()
            manager.register_pushed_dns("tun121", "Options error: option 'dhcp-option DNS 9.9.9.9' cannot be used")
            register.assert_not_called()

    def test_started_tunnel_replaces_stale_dns_and_cache(self):
        proxy = manager.proxy_server
        proxy.register_tun_dns_servers("tun121", ["9.9.9.9"])
        proxy._dns_cache.update({"tun121|ping0.cc": ("203.0.113.7", time.time()), "tun122|ping0.cc": ("203.0.113.8", time.time())})
        result, _ = self.run_process([
            "PUSH: 'PUSH_REPLY,dhcp-option DNS 10.211.254.254'",
            "Initialization Sequence Completed",
        ], keep_alive=True)
        self.assertTrue(result[0])
        self.assertEqual(proxy.get_tun_dns_servers("tun121")[0], "10.211.254.254")
        self.assertNotIn("tun121|ping0.cc", proxy._dns_cache)
        self.assertIn("tun122|ping0.cc", proxy._dns_cache)

    def test_short_probe_and_failed_handshake_clear_dns_and_cache(self):
        for final_line, keep_alive, expected_ok in [("Initialization Sequence Completed", False, True), ("AUTH_FAILED", True, False)]:
            with self.subTest(final_line=final_line):
                proxy = manager.proxy_server
                register = proxy.register_tun_dns_servers

                def seed_cache(device, servers):
                    register(device, servers)
                    proxy._dns_cache[f"{device}|ping0.cc"] = ("203.0.113.7", time.time())

                with mock.patch.object(proxy, "register_tun_dns_servers", side_effect=seed_cache):
                    result, _ = self.run_process([
                        "PUSH: 'PUSH_REPLY,dhcp-option DNS 10.211.254.254'",
                        final_line,
                    ], keep_alive=keep_alive)
                self.assertEqual(result[0], expected_ok)
                self.assertIsNone(result[2])
                self.assertNotIn("tun121", proxy._tun_dns_servers)
                self.assertNotIn("tun121|ping0.cc", proxy._dns_cache)
                self.assertNotIn("tun121", manager._openvpn_dns_owners)

    def test_reader_applies_reconnect_dns_and_watcher_cleans_exit(self):
        result, process = self.run_process([
            "PUSH: 'PUSH_REPLY,dhcp-option DNS 10.211.254.254'",
            "Initialization Sequence Completed",
        ], keep_alive=True)
        self.assertIs(result[2], process)
        proxy = manager.proxy_server
        proxy._dns_cache["tun121|ping0.cc"] = ("203.0.113.7", time.time())
        process.push("PUSH: 'PUSH_REPLY,dhcp-option DNS 10.211.254.253'")
        self.wait_for(lambda: proxy.get_tun_dns_servers("tun121")[0] == "10.211.254.253")
        self.assertNotIn("tun121|ping0.cc", proxy._dns_cache)
        process.terminate()
        self.wait_for(lambda: "tun121" not in proxy._tun_dns_servers)

    def test_old_reader_and_exit_cannot_modify_new_same_device_dns(self):
        old_owner = manager._begin_openvpn_dns_lifecycle("tun121")
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 10.0.0.1", owner=old_owner)
        new_owner = manager._begin_openvpn_dns_lifecycle("tun121")
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 10.0.0.2", owner=new_owner)
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 10.0.0.3", owner=old_owner)
        manager._end_openvpn_dns_lifecycle("tun121", old_owner)
        self.assertEqual(manager.proxy_server.get_tun_dns_servers("tun121")[0], "10.0.0.2")
        manager._end_openvpn_dns_lifecycle("tun121", new_owner)
        self.assertNotIn("tun121", manager.proxy_server._tun_dns_servers)

    def test_reconnect_without_dns_stops_using_old_server(self):
        owner = manager._begin_openvpn_dns_lifecycle("tun121")
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 10.0.0.1", owner=owner)
        manager.register_pushed_dns("tun121", "PUSH_REPLY,ifconfig 10.0.0.3 255.255.255.0", owner=owner)
        self.assertNotIn("tun121", manager.proxy_server._tun_dns_servers)

    def test_invalid_reconnect_dns_cannot_retain_old_resolver(self):
        owner = manager._begin_openvpn_dns_lifecycle("tun121")
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 10.0.0.1", owner=owner)
        manager.register_pushed_dns("tun121", "PUSH_REPLY,dhcp-option DNS 999.1.1.1,dhcp-option DNS6 2001:db8::1", owner=owner)
        self.assertNotIn("tun121", manager.proxy_server._tun_dns_servers)

    def test_launch_failure_clears_lifecycle(self):
        with mock.patch.object(manager.subprocess, "Popen", side_effect=FileNotFoundError):
            result = manager.run_openvpn_until_ready(str(self.directory / "candidate.ovpn"), True, True, dev="tun121")
        self.assertFalse(result[0])
        self.assertNotIn("tun121", manager._openvpn_dns_owners)

    def test_command_error_is_not_swallowed_and_releases_lifecycle(self):
        with mock.patch.object(manager, "openvpn_command", side_effect=RuntimeError("invalid OPENVPN_CMD")):
            with self.assertRaisesRegex(RuntimeError, "invalid OPENVPN_CMD"):
                manager.run_openvpn_until_ready(str(self.directory / "candidate.ovpn"), True, True, dev="tun121")
        self.assertNotIn("tun121", manager._openvpn_dns_owners)
        self.assertTrue(manager.openvpn_capacity.acquire(blocking=False))
        self.assertTrue(manager.openvpn_capacity.acquire(blocking=False))

    def test_capacity_failure_does_not_clear_existing_tunnel_dns(self):
        manager.proxy_server.register_tun_dns_servers("tun121", ["10.0.0.1"])
        with mock.patch.object(manager.openvpn_capacity, "acquire", return_value=False):
            result = manager.run_openvpn_until_ready(str(self.directory / "candidate.ovpn"), True, True, dev="tun121")
        self.assertFalse(result[0])
        self.assertEqual(manager.proxy_server.get_tun_dns_servers("tun121")[0], "10.0.0.1")


if __name__ == "__main__":
    unittest.main()
