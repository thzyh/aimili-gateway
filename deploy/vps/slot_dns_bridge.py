#!/usr/bin/env python3
"""临时承接一个出口的新连接；不修改路由或停止已有出口进程。

仅用于不能重启完整出口引擎时的 DNS 修复。与修复后的 proxy_server.py
放在同一临时目录运行；监听回环地址。服务停止后的转发规则清理由 systemd 负责。
"""
import argparse
import json
import re
import signal
import threading
from pathlib import Path

import proxy_server


def device_for_slot(state_file: Path, slot: int) -> str:
    data = json.loads(state_file.read_text(encoding="utf-8"))
    selected = next((s for s in data.get("slots", []) if s.get("slot") == slot), None)
    device = str((selected or {}).get("device") or "")
    if not re.fullmatch(r"tun\d+", device):
        raise RuntimeError("目标出口没有有效隧道，拒绝使用其他出口")
    return device


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--slot", type=int, required=True)
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    if args.slot < 0 or not 1024 <= args.port <= 65535:
        parser.error("无效的出口编号或监听端口")
    device_for_slot(args.state, args.slot)
    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    proxy_server.start_proxy_server("127.0.0.1", args.port,
                                    lambda: device_for_slot(args.state, args.slot), stop)


if __name__ == "__main__":
    main()
