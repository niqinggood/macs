"""Evaluate one elder-monitor event JSON document using deterministic rules."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from elder_monitor_core import evaluate_event, load_policy


def _read_json(path: str) -> dict:
    if path == "-":
        return json.load(sys.stdin)
    with Path(path).open("r", encoding="utf-8") as handle:
        return json.load(handle)


def main() -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("event", help="Event JSON path, or - for stdin")
    parser.add_argument("--policy", help="Optional risk-policy JSON path")
    parser.add_argument("--pretty", action="store_true", help="Indent JSON output")
    args = parser.parse_args()

    try:
        event = _read_json(args.event)
        assessment = evaluate_event(event, load_policy(args.policy))
    except (OSError, json.JSONDecodeError, KeyError, ValueError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False, indent=2), file=sys.stderr)
        return 2

    print(json.dumps(assessment, ensure_ascii=False, indent=2 if args.pretty else None))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())


