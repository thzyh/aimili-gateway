"""运行时容量预算。

容量上限不是机器型号的静态常量：同一台 VPS 可能同时运行其他服务，
因此这里同时参考总内存、当前可用内存、CPU 数量和当前已启用槽位。
该模块只给出保守的自动上限；实际配置仍必须经过调用方的原子更新。
"""

from __future__ import annotations

import os
import time
from dataclasses import asdict, dataclass
from pathlib import Path


MIN_TARGET_POOL = 16
MAX_TARGET_POOL = 256
MIN_REGULAR_SLOTS = 1
MAX_REGULAR_SLOTS = 16


@dataclass(frozen=True)
class HostFacts:
    memory_total_bytes: int
    memory_available_bytes: int
    cpu_count: int
    load1: float


@dataclass(frozen=True)
class CapacityLimits:
    regular_exit_slots_max: int
    target_valid_nodes_max: int
    emergency_valid_nodes_max: int
    memory_total_bytes: int
    memory_available_bytes: int
    cpu_count: int
    load1: float
    sampled_at: float

    def as_dict(self) -> dict:
        result = asdict(self)
        result["memoryTotalBytes"] = result.pop("memory_total_bytes")
        result["memoryAvailableBytes"] = result.pop("memory_available_bytes")
        result["cpuCount"] = result.pop("cpu_count")
        result["load1"] = round(float(result["load1"]), 2)
        result["sampledAt"] = result.pop("sampled_at")
        result["regularExitSlotsMax"] = result.pop("regular_exit_slots_max")
        result["targetValidNodesMax"] = result.pop("target_valid_nodes_max")
        result["emergencyValidNodesMax"] = result.pop("emergency_valid_nodes_max")
        return result


def read_host_facts() -> HostFacts:
    total = available = 0
    try:
        for line in Path("/proc/meminfo").read_text(encoding="ascii").splitlines():
            name, _, raw = line.partition(":")
            value = int(raw.strip().split()[0]) * 1024
            if name == "MemTotal":
                total = value
            elif name == "MemAvailable":
                available = value
    except (OSError, ValueError, IndexError):
        pass
    if total <= 0:
        total = 512 * 1024 * 1024
    if available < 0:
        available = 0
    try:
        load1 = float(os.getloadavg()[0])
    except (AttributeError, OSError):
        load1 = 0.0
    cpus = max(1, os.cpu_count() or 1)
    if hasattr(os, "sched_getaffinity"):
        try:
            cpus = min(cpus, len(os.sched_getaffinity(0)))
        except OSError:
            pass
    try:
        quota, period = Path("/sys/fs/cgroup/cpu.max").read_text(encoding="ascii").split()[:2]
        if quota != "max" and int(period) > 0:
            cpus = min(cpus, max(1, (int(quota) + int(period) - 1) // int(period)))
    except (OSError, ValueError, IndexError):
        pass
    return HostFacts(total, min(total, available), cpus, max(0.0, load1))


def _memory_slot_limit(facts: HostFacts) -> int:
    # 每个长期 OpenVPN 槽位预留约 64 MiB，并为 Gateway、3x-ui、Caddy
    # 和其它项目保留 256 MiB。这个值是安全预算，不是性能承诺。
    total_mb = facts.memory_total_bytes // (1024 * 1024)
    return max(MIN_REGULAR_SLOTS, min(MAX_REGULAR_SLOTS, (total_mb - 256) // 64))


def limits_for(facts: HostFacts, current_slots: int, process_limit: int | None = None) -> CapacityLimits:
    memory_limit = _memory_slot_limit(facts)
    cpu_limit = max(MIN_REGULAR_SLOTS, min(MAX_REGULAR_SLOTS, facts.cpu_count * 4))
    free_mb = facts.memory_available_bytes // (1024 * 1024)
    # Existing exits remain admissible.  New exits require free memory beyond
    # a 256 MiB reserve shared with the rest of the host.
    expansion_limit = max(current_slots, current_slots + max(0, (free_mb - 256) // 64))
    regular_max = max(current_slots, MIN_REGULAR_SLOTS, min(MAX_REGULAR_SLOTS, memory_limit, cpu_limit, expansion_limit))
    if process_limit is not None:
        regular_max = max(current_slots, MIN_REGULAR_SLOTS, min(regular_max, process_limit - 3))
    if facts.load1 > facts.cpu_count * 1.75:
        regular_max = max(current_slots, MIN_REGULAR_SLOTS)
    pool_budget = max(1, min(regular_max, free_mb // 40))
    if facts.load1 > facts.cpu_count * 1.75:
        pool_budget = max(1, pool_budget - 1)
    target_max = max(MIN_TARGET_POOL, current_slots + 3, min(MAX_TARGET_POOL, pool_budget * 16))
    # 512 MiB / 4 槽位保留既有约 64/150 体验；更大主机按出口位阶梯提升。
    emergency_max = max(target_max, min(MAX_TARGET_POOL * 2, pool_budget * 32 + 22))
    return CapacityLimits(
        regular_max,
        target_max,
        emergency_max,
        facts.memory_total_bytes,
        facts.memory_available_bytes,
        facts.cpu_count,
        facts.load1,
        time.time(),
    )


def clamp_settings(target: int, emergency: int, limits: CapacityLimits) -> tuple[int, int]:
    target = max(MIN_TARGET_POOL, min(limits.target_valid_nodes_max, int(target)))
    emergency = max(target, min(limits.emergency_valid_nodes_max, int(emergency)))
    return target, emergency
