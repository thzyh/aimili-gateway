import argparse
import os
import signal
import subprocess
import sys
from pathlib import Path


def stop_tree(process):
    if os.name == "nt":
        subprocess.run(
            ["taskkill", "/PID", str(process.pid), "/T", "/F"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
            timeout=5,
        )
    else:
        os.killpg(process.pid, signal.SIGKILL)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--timeout-seconds", type=int, required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--working-directory")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("command is required after --")
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("w", encoding="utf-8", errors="replace") as stream:
        process = subprocess.Popen(
            command,
            cwd=args.working_directory,
            stdout=stream,
            stderr=subprocess.STDOUT,
            start_new_session=os.name != "nt",
        )
        try:
            return process.wait(timeout=args.timeout_seconds)
        except subprocess.TimeoutExpired:
            stream.write(f"\nBOUNDED_COMMAND_TIMEOUT seconds={args.timeout_seconds}\n")
            stream.flush()
            stop_tree(process)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            return 124


if __name__ == "__main__":
    raise SystemExit(main())
