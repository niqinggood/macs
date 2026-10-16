#!/usr/bin/env python3
"""Deterministic startup fundraising readiness scoring.

Input is a JSON file (or "-" for stdin) containing all eight dimensions.
Each dimension may be a number from 0 to 5, or an object with a numeric
"score" and optional "evidence".
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any


VERSION = "1.0"
DIMENSIONS = (
    "traction",
    "market",
    "team",
    "product",
    "story",
    "unit_economics",
    "use_of_funds",
    "timing",
)


def _load(path: str) -> dict[str, Any]:
    if path == "-":
        raw = sys.stdin.read()
    else:
        raw = Path(path).read_text(encoding="utf-8")
    data = json.loads(raw)
    if not isinstance(data, dict):
        raise ValueError("input must be a JSON object")
    return data


def _score(value: Any, dimension: str) -> tuple[float, str]:
    evidence = ""
    if isinstance(value, dict):
        evidence = str(value.get("evidence", "")).strip()
        value = value.get("score")
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{dimension}.score must be a number from 0 to 5")
    score = float(value)
    if score < 0 or score > 5:
        raise ValueError(f"{dimension}.score must be between 0 and 5")
    return score, evidence


def diagnose(data: dict[str, Any]) -> dict[str, Any]:
    missing = [name for name in DIMENSIONS if name not in data]
    if missing:
        return {
            "version": VERSION,
            "status": "insufficient_data",
            "score": None,
            "grade": None,
            "missing_dimensions": missing,
            "message": "Provide all eight dimensions; missing values are not inferred.",
        }

    dimensions: dict[str, dict[str, Any]] = {}
    for name in DIMENSIONS:
        score, evidence = _score(data[name], name)
        dimensions[name] = {"score": score, "evidence": evidence}

    total = round(
        sum(item["score"] for item in dimensions.values())
        / (len(DIMENSIONS) * 5)
        * 100,
        1,
    )
    if total >= 80:
        grade = "ready"
    elif total >= 65:
        grade = "conditional"
    else:
        grade = "not_ready"

    weaknesses = [
        name
        for name, _ in sorted(
            dimensions.items(),
            key=lambda item: (item[1]["score"], DIMENSIONS.index(item[0])),
        )[:3]
    ]
    return {
        "version": VERSION,
        "status": "ok",
        "score": total,
        "grade": grade,
        "dimensions": dimensions,
        "weaknesses": weaknesses,
        "scoring_note": "Equal-weight diagnostic based only on supplied 0-5 scores.",
    }


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: funding_diagnosis.py <input.json|->", file=sys.stderr)
        return 2
    try:
        result = diagnose(_load(argv[1]))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, ensure_ascii=False))
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
