#!/usr/bin/env python3
"""Inspect a user-supplied PPTX without modifying it."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from pptx import Presentation


SKILL_DIR = Path(__file__).resolve().parents[1]
if str(SKILL_DIR) not in sys.path:
    sys.path.insert(0, str(SKILL_DIR))

from template_support import template_manifest  # noqa: E402


def main():
    parser = argparse.ArgumentParser(
        description="Inspect PPTX masters, layouts, placeholders, and canvas size"
    )
    parser.add_argument("template", help="Path to the source PPTX")
    parser.add_argument("--output", "-o", help="Optional JSON output path")
    args = parser.parse_args()

    source = Path(args.template).expanduser().resolve()
    if not source.is_file():
        parser.error(f"template not found: {source}")
    manifest = template_manifest(Presentation(str(source)), source)
    payload = json.dumps(manifest, ensure_ascii=False, indent=2)
    if args.output:
        target = Path(args.output).expanduser().resolve()
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(payload + "\n", encoding="utf-8")
        print(target)
    else:
        print(payload)


if __name__ == "__main__":
    main()
