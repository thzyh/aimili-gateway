import argparse
import re
import sys
from pathlib import Path


KNOWN = {
    "github.com/mhsanaei/3x-ui/v3": {
        "TestBotContextNamesRealCIJobs",
        "TestReviewNamesRealCIJobsAndGates",
    },
    "github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken": {
        "TestFileKeySourceRejectsLoosePerms",
    },
    "github.com/mhsanaei/3x-ui/v3/internal/web/service/panel": {
        "TestUpdateProxyEnvVars",
    },
}


def verify(output, exit_code, platform):
    if exit_code == 0:
        return
    if platform.lower() != "windows":
        raise ValueError("unexpected 3x-ui full-test failure outside Windows")
    if exit_code == 124 or "BOUNDED_COMMAND_TIMEOUT" in output:
        raise ValueError("unexpected 3x-ui full-test timeout")
    if re.search(r"(^|\n)(panic:|fatal error:|.*\[build failed\])", output, re.I):
        raise ValueError("unexpected 3x-ui panic or build failure")

    pending_tests = []
    classified_packages = set()
    for line in output.splitlines():
        test_match = re.match(r"^--- FAIL: ([^\s/(]+)", line)
        if test_match:
            pending_tests.append(test_match.group(1))
            continue
        package_match = re.match(r"^FAIL\s+(github\.com/mhsanaei/3x-ui/v3\S*)\s", line)
        if not package_match:
            continue
        package = package_match.group(1)
        allowed = KNOWN.get(package)
        if allowed is None:
            raise ValueError(f"unexpected 3x-ui failure package: {package}")
        if not pending_tests or not set(pending_tests).issubset(allowed):
            raise ValueError(f"unexpected 3x-ui failures in {package}: {pending_tests or ['unclassified failure']}")
        classified_packages.add(package)
        pending_tests.clear()

    if pending_tests or not classified_packages:
        raise ValueError(f"unexpected 3x-ui failures: {pending_tests or ['unclassified failure']}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("--exit-code", required=True, type=int)
    parser.add_argument("--platform", required=True)
    args = parser.parse_args()
    try:
        verify(Path(args.output).read_text(encoding="utf-8", errors="replace"), args.exit_code, args.platform)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print("3x-ui full test result contains only exact known Windows platform baselines.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
