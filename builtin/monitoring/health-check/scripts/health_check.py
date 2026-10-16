#!/usr/bin/env python3
"""Read-only OpenKitty host health check over SSH."""

from __future__ import annotations

import argparse
import json
import re
import shlex
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


REDACTIONS = [
    re.compile(r"(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+"),
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True, help="SSH host or config alias")
    parser.add_argument("--user")
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--identity-file", type=Path)
    parser.add_argument("--service", default="openkitty")
    parser.add_argument("--ready-url", default="http://127.0.0.1:8080/readyz")
    parser.add_argument("--accept-new-host-key", action="store_true")
    parser.add_argument("--json", action="store_true")
    return parser.parse_args()


def redact(value: str) -> str:
    result = value
    for pattern in REDACTIONS:
        result = pattern.sub("<redacted>", result)
    return result.strip()


def ssh_base(args: argparse.Namespace) -> tuple[list[str], str]:
    target = f"{args.user}@{args.host}" if args.user else args.host
    strict = "accept-new" if args.accept_new_host_key else "yes"
    command = [
        "ssh",
        "-p",
        str(args.port),
        "-o",
        f"StrictHostKeyChecking={strict}",
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectTimeout=10",
    ]
    if args.identity_file:
        command.extend(["-i", str(args.identity_file.expanduser())])
    return command, target


def run_check(base: list[str], target: str, command: str) -> dict[str, object]:
    result = subprocess.run([*base, target, command], text=True, capture_output=True, timeout=20)
    return {
        "ok": result.returncode == 0,
        "output": redact(result.stdout or result.stderr)[:2000],
    }


def main() -> int:
    args = parse_args()
    base, target = ssh_base(args)
    checks = {
        "hostname": "hostname",
        "uptime": "uptime",
        "cpu": "getconf _NPROCESSORS_ONLN; cat /proc/loadavg",
        "memory": "free -m",
        "disk": "df -hP /",
        "service": f"systemctl is-active {shlex.quote(args.service)} && systemctl --no-pager --lines=12 status {shlex.quote(args.service)}",
        "readiness": "curl --fail --silent --show-error --max-time 10 " + shlex.quote(args.ready_url),
        "ports": "ss -tln 2>/dev/null || netstat -tln 2>/dev/null",
        "recent_logs": f"journalctl -u {shlex.quote(args.service)} --no-pager -n 30 2>/dev/null",
    }
    report: dict[str, object] = {
        "target": args.host,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "checks": {},
    }
    try:
        for name, command in checks.items():
            report["checks"][name] = run_check(base, target, command)  # type: ignore[index]
    except (OSError, subprocess.TimeoutExpired) as exc:
        print(f"health check failed: {exc}", file=sys.stderr)
        return 1

    if args.json:
        print(json.dumps(report, indent=2, ensure_ascii=False))
    else:
        print(f"# OpenKitty health: {args.host}")
        print(f"Observed: {report['observed_at']}")
        for name, result in report["checks"].items():  # type: ignore[union-attr]
            marker = "OK" if result["ok"] else "FAIL"
            print(f"\n## {name} [{marker}]\n{result['output'] or '(empty)'}")

    required = ("hostname", "service", "readiness")
    return 0 if all(report["checks"][name]["ok"] for name in required) else 2  # type: ignore[index]


if __name__ == "__main__":
    raise SystemExit(main())
