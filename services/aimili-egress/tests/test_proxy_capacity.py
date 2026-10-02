import threading
import time
import types
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest import mock

import proxy_server


class FakeSocket:
    def __init__(self):
        self.closed = False

    def close(self):
        self.closed = True


class FailingThread:
    def start(self):
        raise RuntimeError("can't start new thread")


class ProxyResourceBudgetTests(unittest.TestCase):
    def headroom(self, *, mem_available_mib=1024, fd_soft=4096, fd_count=20, files=None):
        contents = {
            "/proc/meminfo": (
                f"MemAvailable: {mem_available_mib * 1024} kB\n"
                "SwapFree: 10485760 kB\n"
            ),
            "/proc/self/cgroup": "0::/aimili.slice/unit\n",
        }
        contents.update(files or {})

        def read_text(path, *args, **kwargs):
            try:
                return contents[path.as_posix()]
            except KeyError:
                raise FileNotFoundError(path.as_posix()) from None

        resource = types.SimpleNamespace(
            RLIMIT_NOFILE=7,
            RLIM_INFINITY=-1,
            getrlimit=mock.Mock(return_value=(fd_soft, fd_soft)),
        )
        with mock.patch.dict("sys.modules", {"resource": resource}), \
                mock.patch.object(proxy_server.Path, "read_text", new=read_text), \
                mock.patch.object(proxy_server.Path, "iterdir", return_value=iter(range(fd_count))):
            result = proxy_server.proxy_resource_headroom()
        resource.getrlimit.assert_called_once_with(resource.RLIMIT_NOFILE)
        return result

    def test_ram_budget_reserves_64_mib_and_ignores_swap(self):
        self.assertEqual(self.headroom(mem_available_mib=96), 32)
        self.assertEqual(self.headroom(files={
            "/proc/meminfo": "MemAvailable: 99840 kB\nSwapFree: 10485760 kB\n",
        }), 33)

    def test_fd_budget_reserves_32_fds_and_two_fds_per_connection(self):
        self.assertEqual(self.headroom(fd_soft=100, fd_count=20), 24)

    def test_parent_cgroup_pid_limit_is_enforced(self):
        self.assertEqual(self.headroom(files={
            "/sys/fs/cgroup/aimili.slice/unit/pids.max": "500",
            "/sys/fs/cgroup/aimili.slice/unit/pids.current": "100",
            "/sys/fs/cgroup/aimili.slice/pids.max": "200",
            "/sys/fs/cgroup/aimili.slice/pids.current": "157",
        }), 11)

    def test_root_cgroup_pid_limit_is_also_enforced(self):
        self.assertEqual(self.headroom(files={
            "/sys/fs/cgroup/aimili.slice/unit/pids.max": "500",
            "/sys/fs/cgroup/aimili.slice/unit/pids.current": "100",
            "/sys/fs/cgroup/pids.max": "41",
            "/sys/fs/cgroup/pids.current": "2",
        }), 7)

    def test_parent_cgroup_memory_leaves_32_mib_and_one_mib_per_connection(self):
        mib = 1024**2
        self.assertEqual(self.headroom(files={
            "/sys/fs/cgroup/aimili.slice/unit/memory.max": str(512 * mib),
            "/sys/fs/cgroup/aimili.slice/unit/memory.current": str(128 * mib),
            "/sys/fs/cgroup/aimili.slice/memory.max": str(128 * mib),
            "/sys/fs/cgroup/aimili.slice/memory.current": str(87 * mib),
        }), 9)

    def test_any_resource_below_its_reserve_returns_zero(self):
        mib = 1024**2
        cases = (
            {"mem_available_mib": 63},
            {"fd_soft": 51, "fd_count": 20},
            {"files": {
                "/sys/fs/cgroup/aimili.slice/unit/pids.max": "40",
                "/sys/fs/cgroup/aimili.slice/unit/pids.current": "10",
            }},
            {"files": {
                "/sys/fs/cgroup/aimili.slice/unit/memory.max": str(64 * mib),
                "/sys/fs/cgroup/aimili.slice/unit/memory.current": str(33 * mib),
            }},
        )
        for inputs in cases:
            with self.subTest(inputs=inputs):
                self.assertEqual(self.headroom(**inputs), 0)

    def test_unlimited_fd_and_cgroup_limits_do_not_reduce_ram_budget(self):
        self.assertEqual(self.headroom(mem_available_mib=96, fd_soft=-1, files={
            "/sys/fs/cgroup/aimili.slice/unit/pids.max": "max",
            "/sys/fs/cgroup/aimili.slice/unit/memory.max": "max",
        }), 32)


class ProxyCapacityTests(unittest.TestCase):
    def test_device_provider_is_resolved_for_each_new_connection(self):
        current = {"device": "tun0"}
        provider = lambda: current["device"]

        self.assertEqual(proxy_server.resolve_device(provider), "tun0")
        current["device"] = "tun140"
        self.assertEqual(proxy_server.resolve_device(provider), "tun140")

    def test_explicit_legacy_limits_remain_supported(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=128,
            per_listener_limit=64,
            wait_ms=0,
        )

        for _ in range(64):
            self.assertTrue(capacity.try_acquire("main"))
        self.assertFalse(capacity.try_acquire("main"))

        for _ in range(64):
            self.assertTrue(capacity.try_acquire("slot-1"))
        self.assertFalse(capacity.try_acquire("slot-2"))

        for listener in ("main", "slot-1"):
            for _ in range(64):
                capacity.release(listener)

    def test_one_listener_exhaustion_does_not_block_another_listener(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=4, per_listener_limit=2, wait_ms=0
        )

        self.assertTrue(capacity.try_acquire("30000"))
        self.assertTrue(capacity.try_acquire("30000"))
        self.assertFalse(capacity.try_acquire("30000"))
        self.assertTrue(capacity.try_acquire("30001"))

        capacity.release("30000")
        capacity.release("30000")
        capacity.release("30001")

    def test_a_listener_can_use_more_than_the_old_64_connection_limit(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=160, per_listener_limit=120, wait_ms=0
        )

        for _ in range(120):
            self.assertTrue(capacity.try_acquire("30000"))
        self.assertFalse(capacity.try_acquire("30000"))
        self.assertEqual(capacity.rejection_reason(), "listener_limit")
        self.assertEqual(capacity.snapshot()["active"], 120)

        for _ in range(120):
            capacity.release("30000")
        self.assertEqual(capacity.snapshot()["active"], 0)

    def test_registered_idle_listeners_keep_a_small_fair_reserve(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=8, per_listener_limit=8, wait_ms=0,
            reserve_per_listener=2,
        )
        capacity.register_listener("main")
        capacity.register_listener("slot-1")

        for _ in range(6):
            self.assertTrue(capacity.try_acquire("main"))
        self.assertFalse(capacity.try_acquire("main"))
        self.assertEqual(capacity.rejection_reason(), "global_limit")

        self.assertTrue(capacity.try_acquire("slot-1"))
        self.assertTrue(capacity.try_acquire("slot-1"))
        self.assertFalse(capacity.try_acquire("slot-1"))
        snapshot = capacity.snapshot()
        self.assertEqual(snapshot["active"], 8)
        self.assertEqual(snapshot["listeners"]["main"], 6)
        self.assertEqual(snapshot["listeners"]["slot-1"], 2)

        for listener, count in (("main", 6), ("slot-1", 2)):
            for _ in range(count):
                capacity.release(listener)

    def test_unregistering_an_idle_listener_returns_its_reserve(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=4, per_listener_limit=4, wait_ms=0,
            reserve_per_listener=2,
        )
        capacity.register_listener("main")
        capacity.register_listener("stopped-listener")
        self.assertTrue(capacity.try_acquire("main"))
        self.assertTrue(capacity.try_acquire("main"))
        self.assertFalse(capacity.try_acquire("main"))

        capacity.unregister_listener("stopped-listener")
        self.assertTrue(capacity.try_acquire("main"))
        self.assertTrue(capacity.try_acquire("main"))
        for _ in range(4):
            capacity.release("main")

    def test_resource_pressure_blocks_new_clients_without_removing_active_clients(self):
        headroom = {"value": 6}
        clock = {"now": 1000.0}
        with mock.patch.object(proxy_server.time, "monotonic", side_effect=lambda: clock["now"]):
            capacity = proxy_server.ProxyCapacity(
                global_limit=160, per_listener_limit=120, wait_ms=0,
                resource_sampler=lambda: headroom["value"],
            )
            for _ in range(3):
                self.assertTrue(capacity.try_acquire("30000"))

            headroom["value"] = 0
            clock["now"] += 6
            self.assertFalse(capacity.try_acquire("30000"))
            self.assertEqual(capacity.rejection_reason(), "resource_pressure")
            self.assertEqual(capacity.snapshot()["active"], 3)
            self.assertEqual(capacity.snapshot()["listeners"]["30000"], 3)

            capacity.release("30000")
            headroom["value"] = 1
            clock["now"] += 6
            self.assertTrue(capacity.try_acquire("30001"))
            for listener, count in (("30000", 2), ("30001", 1)):
                for _ in range(count):
                    capacity.release(listener)
            self.assertEqual(capacity.snapshot()["active"], 0)

    def test_resource_recovery_does_not_override_the_configured_global_limit(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=2, per_listener_limit=2, wait_ms=0,
            resource_sampler=lambda: 100,
        )
        self.assertTrue(capacity.try_acquire("30000"))
        self.assertTrue(capacity.try_acquire("30001"))
        self.assertFalse(capacity.try_acquire("30002"))
        self.assertEqual(capacity.rejection_reason(), "global_limit")
        capacity.release("30000")
        capacity.release("30001")

    def test_short_capacity_wait_is_woken_by_release(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=1, per_listener_limit=1, wait_ms=400
        )
        self.assertTrue(capacity.try_acquire("30000"))
        started = threading.Event()
        finished = threading.Event()
        result = []

        def acquire_after_full_listener():
            started.set()
            result.append(capacity.try_acquire("30000"))
            finished.set()

        worker = threading.Thread(target=acquire_after_full_listener)
        worker.start()
        try:
            self.assertTrue(started.wait(1))
            self.assertFalse(finished.wait(0.05), "capacity was rejected immediately")
            capacity.release("30000")
            self.assertTrue(finished.wait(1), "release did not wake the waiting client")
            self.assertEqual(result, [True])
            capacity.release("30000")
        finally:
            worker.join(1)
        self.assertEqual(capacity.snapshot()["active"], 0)

    def test_wait_has_one_bounded_budget_and_reports_the_failed_limit(self):
        for second_listener, expected_reason in (
            ("30000", "listener_limit"), ("30001", "global_limit"),
        ):
            with self.subTest(reason=expected_reason):
                capacity = proxy_server.ProxyCapacity(
                    global_limit=1, per_listener_limit=1, wait_ms=50
                )
                self.assertTrue(capacity.try_acquire("30000"))
                begin = time.monotonic()
                self.assertFalse(capacity.try_acquire(second_listener))
                elapsed = time.monotonic() - begin
                self.assertGreaterEqual(elapsed, 0.035)
                self.assertLess(elapsed, 0.30)
                self.assertEqual(capacity.rejection_reason(), expected_reason)
                capacity.release("30000")

    def test_concurrent_clients_never_exceed_limits_and_release_every_permit(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=8, per_listener_limit=4, wait_ms=100,
            reserve_per_listener=0, resource_sampler=lambda: 100,
        )
        barrier = threading.Barrier(17)
        lock = threading.Lock()
        counts = {"30000": 0, "30001": 0}
        peaks = {"active": 0, "listener": 0}

        def use_connection(index):
            listener = "30000" if index % 2 == 0 else "30001"
            barrier.wait(timeout=3)
            successful = 0
            for _ in range(4):
                if not capacity.try_acquire(listener):
                    continue
                try:
                    with lock:
                        counts[listener] += 1
                        peaks["active"] = max(peaks["active"], sum(counts.values()))
                        peaks["listener"] = max(peaks["listener"], counts[listener])
                    time.sleep(0.002)
                    successful += 1
                finally:
                    with lock:
                        counts[listener] -= 1
                    capacity.release(listener)
            return successful

        with ThreadPoolExecutor(max_workers=16) as workers:
            futures = [workers.submit(use_connection, index) for index in range(16)]
            barrier.wait(timeout=3)
            successes = sum(future.result(timeout=3) for future in futures)

        self.assertGreaterEqual(successes, 8)
        self.assertLessEqual(peaks["active"], 8)
        self.assertLessEqual(peaks["listener"], 4)
        self.assertEqual(capacity.snapshot()["active"], 0)
        self.assertTrue(all(value == 0 for value in capacity.snapshot()["listeners"].values()))

    def test_thread_start_failure_closes_socket_and_releases_both_limits(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=1, per_listener_limit=1, wait_ms=0
        )
        client = FakeSocket()

        started = proxy_server.start_proxy_client_thread(
            client,
            ("127.0.0.1", 12345),
            "tun101",
            "30000",
            capacity,
            thread_factory=lambda **_kwargs: FailingThread(),
        )

        self.assertFalse(started)
        self.assertTrue(client.closed)
        self.assertTrue(capacity.try_acquire("30000"))
        capacity.release("30000")

    def test_client_worker_exception_releases_capacity(self):
        capacity = proxy_server.ProxyCapacity(
            global_limit=1, per_listener_limit=1, wait_ms=0
        )
        threads = []

        def thread_factory(**kwargs):
            thread = threading.Thread(**kwargs)
            threads.append(thread)
            return thread

        with mock.patch.object(proxy_server, "proxy_client", side_effect=RuntimeError("broken client")), \
                mock.patch.object(threading, "excepthook"):
            self.assertTrue(proxy_server.start_proxy_client_thread(
                FakeSocket(), ("127.0.0.1", 12345), "tun101", "30000",
                capacity, thread_factory=thread_factory,
            ))
            threads[0].join(1)

        self.assertFalse(threads[0].is_alive())
        self.assertEqual(capacity.snapshot()["active"], 0)
        self.assertTrue(capacity.try_acquire("30000"))
        capacity.release("30000")


if __name__ == "__main__":
    unittest.main()
