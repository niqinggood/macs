#!/usr/bin/env python3
"""Collect a bounded, read-only health snapshot from an SSH inventory."""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import re
import shlex
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


SECRET_PATTERN = re.compile(r"(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("inventory", type=Path)
    parser.add_argument("--output", type=Path, default=Path("storage/fleet_report.md"))
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--show-hosts", action="store_true")
    parser.add_argument("--accept-new-host-key", action="store_true")
    return parser.parse_args()


def load_inventory(path: Path) -> list[dict[str, Any]]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    servers = payload.get("servers")
    if not isinstance(servers, list) or not servers:
        raise ValueError("inventory must contain a non-empty servers array")
    seen: set[tuple[str, int, str]] = set()
    result: list[dict[str, Any]] = []
    for index, server in enumerate(servers):
        if not isinstance(server, dict) or not server.get("host"):
            raise ValueError(f"servers[{index}] must contain host")
        if any(key in server for key in ("password", "passwd", "private_key", "token")):
            raise ValueError(f"servers[{index}] contains credential material; use SSH config or identity_env")
        port = int(server.get("port", 22))
        key = (str(server["host"]), port, str(server.get("user", "")))
        if key in seen:
            continue
        seen.add(key)
        result.append(server)
    return result


def collect(server: dict[str, Any], accept_new: bool) -> dict[str, Any]:
    host = str(server["host"])
    user = str(server.get("user", ""))
    target = f"{user}@{host}" if user else host
    port = int(server.get("port", 22))
    strict = "accept-new" if accept_new else "yes"
    argv = [
        "ssh",
        "-p",
        str(port),
        "-o",
        f"StrictHostKeyChecking={strict}",
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectTimeout=8",
    ]
    identity_env = server.get("identity_env")
    if identity_env:
        import os

        identity = os.environ.get(str(identity_env), "")
        if not identity:
            raise ValueError(f"identity environment variable is not set for {server.get('name', host)}")
        argv.extend(["-i", identity])
    service = shlex.quote(str(server.get("service", "openkitty")))
    ready_url = shlex.quote(str(server.get("ready_url", "http://127.0.0.1:8080/readyz")))
    command = " ; ".join(
        [
            "printf 'hostname='; hostname",
            "printf 'load='; cut -d' ' -f1-3 /proc/loadavg",
            "printf 'memory='; free -m | awk 'NR==2{printf \"%s/%sMB\\n\",$3,$2}'",
            "printf 'disk='; df -hP / | awk 'NR==2{print $5}'",
            f"printf 'service='; systemctl is-active {service} 2>/dev/null || true",
            f"printf 'ready='; curl --fail --silent --max-time 8 {ready_url} >/dev/null && echo yes || echo no",
        ]
    )
    observed = datetime.now(timezone.utc).isoformat()
    try:
        completed = subprocess.run([*argv, target, command], text=True, capture_output=True, timeout=20)
        values: dict[str, str] = {}
        for line in completed.stdout.splitlines():
            if "=" in line:
                key, value = line.split("=", 1)
                values[key.strip()] = SECRET_PATTERN.sub("<redacted>", value.strip())[:300]
        return {
            "name": str(server.get("name", host)),
            "host": host,
            "observed_at": observed,
            "online": completed.returncode == 0,
            **values,
            "error": SECRET_PATTERN.sub("<redacted>", completed.stderr.strip())[:300],
        }
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {
            "name": str(server.get("name", host)),
            "host": host,
            "observed_at": observed,
            "online": False,
            "error": str(exc)[:300],
        }


def render(results: list[dict[str, Any]], show_hosts: bool) -> str:
    lines = [
        "# Fleet Health Report",
        "",
        f"Generated: {datetime.now(timezone.utc).isoformat()}",
        "",
        "| Server | Online | Load | Memory | Disk | Service | Ready |",
        "|---|---|---|---|---|---|---|",
    ]
    for result in results:
        label = result["name"]
        if show_hosts:
            label = f"{label} ({result['host']})"
        lines.append(
            "| {name} | {online} | {load} | {memory} | {disk} | {service} | {ready} |".format(
                name=label,
                online="yes" if result.get("online") else "no",
                load=result.get("load", "-"),
                memory=result.get("memory", "-"),
                disk=result.get("disk", "-"),
                service=result.get("service", "-"),
                ready=result.get("ready", "-"),
            )
        )
    return "\n".join(lines) + "\n"


def main() -> int:
    args = parse_args()
    try:
        servers = load_inventory(args.inventory.expanduser().resolve())
        concurrency = max(1, min(args.concurrency, 32, len(servers)))
        with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
            results = list(executor.map(lambda item: collect(item, args.accept_new_host_key), servers))
        output = args.output.expanduser().resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(render(results, args.show_hosts), encoding="utf-8")
        print(f"report: {output}")
        return 0 if all(item.get("online") for item in results) else 2
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        print(f"monitor failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
