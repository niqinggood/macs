"""Evaluate newline-delimited elder-monitor events while isolating bad lines."""

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter
from pathlib import Path

from elder_monitor_core import evaluate_event, load_policy


def main() -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("events", help="JSONL path, or - for stdin")
    parser.add_argument("--policy", help="Optional risk-policy JSON path")
    parser.add_argument("--pretty", action="store_true", help="Indent JSON output")
    args = parser.parse_args()

    policy = load_policy(args.policy)
    if args.events == "-":
        handle = sys.stdin
        should_close = False
    else:
        handle = Path(args.events).open("r", encoding="utf-8")
        should_close = True

    total = 0
    assessments = []
    errors = []
    counts: Counter[str] = Counter()
    try:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            total += 1
            try:
                event = json.loads(line)
                assessment = evaluate_event(event, policy)
                assessments.append(assessment)
                counts[assessment["severity"]] += 1
            except (json.JSONDecodeError, KeyError, ValueError) as exc:
                errors.append({"line": line_number, "error": str(exc)})
    finally:
        if should_close:
            handle.close()

    result = {
        "total": total,
        "processed": len(assessments),
        "failures": len(errors),
        "severity_counts": dict(sorted(counts.items())),
        "assessments": assessments,
        "errors": errors,
    }
    print(json.dumps(result, ensure_ascii=False, indent=2 if args.pretty else None))
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())


