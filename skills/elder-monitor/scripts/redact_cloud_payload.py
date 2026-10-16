"""Create a minimized, pseudonymous payload for optional cloud model review."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from datetime import datetime
from pathlib import Path
from typing import Any

from elder_monitor_core import validate_event


def _duration_seconds(window: dict[str, str]) -> int:
    start = datetime.fromisoformat(window["start"].replace("Z", "+00:00"))
    end = datetime.fromisoformat(window["end"].replace("Z", "+00:00"))
    return max(0, int((end - start).total_seconds()))


def _normalize_help_value(value: Any) -> bool:
    if value is True:
        return True
    if isinstance(value, str):
        return value.strip().lower() in {"true", "yes", "detected", "help", "need_help"}
    return False


def sanitize_event(event: dict[str, Any]) -> dict[str, Any]:
    errors = validate_event(event)
    if errors:
        raise ValueError("; ".join(errors))

    safe_observations = []
    for item in event["observations"]:
        item_type = item["type"]
        value = item.get("value")
        if item_type == "help_request":
            value = _normalize_help_value(value)
        safe_item = {
            "type": item_type,
            "value": value,
            "confidence": item["confidence"],
            "source": item["source"],
        }
        for optional in ("count", "duration_sec"):
            if optional in item:
                safe_item[optional] = item[optional]
        safe_observations.append(safe_item)

    event_ref = hashlib.sha256(event["event_id"].encode("utf-8")).hexdigest()[:16]
    profile = event.get("profile", {})
    context = event.get("context", {})
    return {
        "event_ref": event_ref,
        "window_duration_sec": _duration_seconds(event["window"]),
        "observations": safe_observations,
        "context": {"time_period": context.get("time_period", "unknown")},
        "baseline_features": {
            "has_sitting_baseline": "normal_sitting_minutes" in profile,
            "has_inactive_baseline": "normal_inactive_minutes" in profile,
        },
        "instruction": "Return normalized observations only; do not diagnose disease.",
    }


def main() -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("event", help="Event JSON path, or - for stdin")
    parser.add_argument("--pretty", action="store_true", help="Indent JSON output")
    args = parser.parse_args()

    try:
        if args.event == "-":
            event = json.load(sys.stdin)
        else:
            with Path(args.event).open("r", encoding="utf-8") as handle:
                event = json.load(handle)
        payload = sanitize_event(event)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False, indent=2), file=sys.stderr)
        return 2

    print(json.dumps(payload, ensure_ascii=False, indent=2 if args.pretty else None))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

