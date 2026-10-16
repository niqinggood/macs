#!/usr/bin/env python3
"""Build an editable task deck from the bundled compact JSON specification."""

from __future__ import annotations

import argparse
from collections import Counter
import json
import platform
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path


OUTPUT_TOKEN = "__TASK_OUTPUT_PPTX__"
COMPOSITION_MODE_TOKEN = "__TASK_COMPOSITION_MODE__"
ALLOWED_KINDS = {
    "cover", "statement", "cards", "split", "process", "metrics",
    "chart", "table", "matrix", "timeline", "closing",
}
EXPLANATORY_KINDS = {"process", "metrics", "chart", "table", "matrix", "timeline"}


def _text(value) -> str:
    if value is None or isinstance(value, bool):
        return ""
    if isinstance(value, (str, int, float)):
        return str(value).strip()
    return ""


def _items(value) -> list:
    return list(value) if isinstance(value, list) else []


def _bullets(values) -> str:
    return "\n".join(f"• {_text(item)}" for item in _items(values) if _text(item))


def _closing_action(value, index: int) -> dict[str, str]:
    if isinstance(value, dict):
        title = (
            _text(value.get("title"))
            or _text(value.get("owner"))
            or _text(value.get("label"))
            or f"Action {index}"
        )
        body = (
            _text(value.get("body"))
            or _text(value.get("action"))
            or _text(value.get("desc"))
            or _text(value.get("detail"))
        )
        return {"title": title, "body": body}
    return {"title": f"Action {index}", "body": _text(value)}


def _statement_claim(slide: dict) -> str:
    return (
        _text(slide.get("claim"))
        or _text(slide.get("takeaway"))
        or _text(slide.get("body"))
        or _text(slide.get("statement"))
    )


def _statement_evidence(slide: dict) -> list:
    return _items(slide.get("evidence")) or _items(slide.get("points"))


def validate_slide_content(slide: dict, index: int, kind: str) -> None:
    """Reject only content volumes that cannot stay readable at 14pt+."""

    def limited(value, label: str, maximum: int) -> None:
        if len(_text(value)) > maximum:
            raise ValueError(
                f"slide {index} {label} exceeds {maximum} characters; split or prioritize it"
            )

    if kind == "statement":
        claim = _statement_claim(slide)
        if not claim:
            raise ValueError(
                f"slide {index} statement requires claim, takeaway, body, or statement"
            )
        limited(claim, "claim", 120)
        evidence = _statement_evidence(slide)
        if len(evidence) > 4:
            raise ValueError(f"slide {index} statement supports at most 4 evidence items")
        for offset, value in enumerate(evidence, 1):
            limited(value, f"evidence[{offset}]", 80)
    elif kind == "cards":
        items = _items(slide.get("items"))
        if not 2 <= len(items) <= 6:
            raise ValueError(f"slide {index} cards requires 2 to 6 items")
        limited(slide.get("takeaway"), "takeaway", 90)
        body_limit = 90 if len(items) <= 3 else (50 if len(items) == 4 else 38)
        for offset, item in enumerate(items, 1):
            if isinstance(item, dict):
                limited(item.get("title") or item.get("label"), f"items[{offset}].title", 42)
                limited(item.get("body") or item.get("desc") or item.get("note"), f"items[{offset}].body", body_limit)
    elif kind == "split":
        for side, maximum in (("left", 130), ("right", 90)):
            panel = slide.get(side) or {}
            if isinstance(panel, dict):
                limited(panel.get("title"), f"{side}.title", 48)
                body = "\n".join(
                    part for part in (_text(panel.get("body")), _bullets(panel.get("bullets"))) if part
                )
                limited(body, f"{side}.body", maximum)
    elif kind == "process":
        items = _items(slide.get("items"))
        if not 3 <= len(items) <= 5:
            raise ValueError(f"slide {index} process requires 3 to 5 items")
        for offset, item in enumerate(items, 1):
            if isinstance(item, dict):
                limited(item.get("title"), f"items[{offset}].title", 30)
                limited(item.get("body") or item.get("desc") or item.get("detail"), f"items[{offset}].body", 48)
        lateral = slide.get("lateral")
        if lateral is not None and not isinstance(lateral, list):
            raise ValueError(f"slide {index} process lateral must be a list")
        lateral_items = _items(lateral)
        if len(lateral_items) > 4:
            raise ValueError(f"slide {index} process supports at most 4 lateral capabilities")
        for offset, value in enumerate(lateral_items, 1):
            limited(value, f"lateral[{offset}]", 24)
    elif kind == "metrics":
        items = _items(slide.get("items")) or _items(slide.get("metrics"))
        if not 2 <= len(items) <= 4:
            raise ValueError(f"slide {index} metrics requires 2 to 4 items")
        for offset, item in enumerate(items, 1):
            if isinstance(item, dict):
                limited(item.get("value"), f"items[{offset}].value", 26)
                limited(item.get("label"), f"items[{offset}].label", 36)
                limited(item.get("body") or item.get("note") or item.get("desc"), f"items[{offset}].body", 72)
    elif kind == "chart":
        categories = _items(slide.get("categories"))
        series = _items(slide.get("series"))
        if not 2 <= len(categories) <= 8 or not 1 <= len(series) <= 4:
            raise ValueError(f"slide {index} chart needs 2 to 8 categories and 1 to 4 series")
        for offset, item in enumerate(series, 1):
            if not isinstance(item, dict) or len(_items(item.get("values"))) != len(categories):
                raise ValueError(f"slide {index} series[{offset}] values must match categories")
        # Keep the schema guard generous enough for a compact Chinese chart
        # annotation; the structural/render audit remains the final authority
        # for actual text overflow.
        limited(slide.get("note") or slide.get("intro"), "note", 180)
    elif kind == "table":
        headers = _items(slide.get("headers"))
        rows = _items(slide.get("rows"))
        if not 2 <= len(headers) <= 7 or not 1 <= len(rows) <= 6:
            raise ValueError(f"slide {index} table needs 2 to 7 columns and 1 to 6 rows")
        for row_offset, row in enumerate(rows, 1):
            if not isinstance(row, list) or len(row) != len(headers):
                raise ValueError(f"slide {index} row {row_offset} must match the header count")
            for col_offset, value in enumerate(row, 1):
                limited(value, f"rows[{row_offset}][{col_offset}]", 70)
    elif kind == "matrix":
        points = _items(slide.get("points"))
        if points:
            if len(points) > 8:
                raise ValueError(f"slide {index} matrix supports at most 8 points")
            for offset, point in enumerate(points, 1):
                if isinstance(point, dict):
                    limited(point.get("label"), f"points[{offset}].label", 42)
    elif kind == "timeline":
        items = _items(slide.get("items")) or _items(slide.get("milestones"))
        if not 2 <= len(items) <= 6:
            raise ValueError(f"slide {index} timeline requires 2 to 6 milestones")
        for offset, item in enumerate(items, 1):
            if isinstance(item, dict):
                limited(item.get("title"), f"items[{offset}].title", 34)
                limited(item.get("detail") or item.get("body"), f"items[{offset}].detail", 72)
    elif kind == "closing":
        items = _items(slide.get("items")) or _items(slide.get("next_actions"))
        if len(items) > 3:
            raise ValueError(f"slide {index} closing supports at most 3 actions")
        for offset, item in enumerate(items, 1):
            action = _closing_action(item, offset)
            limited(action.get("title"), f"actions[{offset}].title", 42)
            limited(action.get("body"), f"actions[{offset}].body", 72)


def validate_evidence_contract(payload: dict) -> None:
    """Validate traceability structure when a compact spec opts into it."""

    policy = _text(payload.get("evidence_policy")).lower()
    if policy not in {"source-bound", "evidence-bound", "事实约束", "证据约束"}:
        return
    entries = payload.get("evidence_ledger")
    if not isinstance(entries, list):
        raise ValueError("source-bound spec requires an evidence_ledger array")
    evidence_ids: set[str] = set()
    for index, entry in enumerate(entries, 1):
        if not isinstance(entry, dict):
            raise ValueError(f"evidence_ledger[{index}] must be an object")
        evidence_id = _text(entry.get("id"))
        if not evidence_id or evidence_id in evidence_ids:
            raise ValueError(f"evidence_ledger[{index}] needs a unique id")
        for field in ("source", "locator", "excerpt"):
            if not _text(entry.get(field)):
                raise ValueError(f"evidence_ledger[{index}] is missing {field}")
        evidence_ids.add(evidence_id)

    source_markers = (
        "source: user", "source: local", "source: project", "source: public",
        "来源：用户", "来源：本地", "来源：项目", "公开来源", "用户陈述", "用户材料", "本地材料",
    )
    non_factual_markers = (
        "规划估算（非项目事实）", "planning estimate (not a project fact)",
        "待确认", "未统计", "未提供", "未知", "不含新增事实", "no new factual claims",
    )
    for index, slide in enumerate(_items(payload.get("slides")), 1):
        if index == 1 or _text(slide.get("kind")).lower() in {"cover", "title"}:
            continue
        note = _text(slide.get("source_note"))
        lower_note = note.lower()
        if not note:
            raise ValueError(f"slide {index} requires a visible source_note")
        source_backed = any(marker in lower_note for marker in source_markers)
        non_factual = any(marker.lower() in lower_note for marker in non_factual_markers)
        if source_backed and not non_factual:
            refs = slide.get("evidence_refs")
            if not isinstance(refs, list) or not refs:
                raise ValueError(f"slide {index} requires evidence_refs")
            missing = [_text(ref) for ref in refs if _text(ref) not in evidence_ids]
            if missing:
                raise ValueError(f"slide {index} has unresolved evidence_refs: {missing}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate a compact deck specification and build an editable PPTX in one command."
    )
    parser.add_argument("--spec", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--work-dir", type=Path)
    return parser.parse_args()


def load_and_validate(spec_path: Path) -> dict:
    payload = json.loads(spec_path.read_text(encoding="utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("spec root must be a JSON object")
    if payload.get("needs_customization", True):
        raise ValueError("needs_customization must be false")
    slides = payload.get("slides")
    if not isinstance(slides, list) or not 6 <= len(slides) <= 14:
        raise ValueError("slides must contain 6 to 14 objects")
    if not isinstance(slides[0], dict) or str(slides[0].get("kind", "")).lower() != "cover":
        raise ValueError("the first slide must use kind=cover")
    quality_profile = str(payload.get("quality_profile", "audience-facing")).strip().lower()
    if quality_profile not in {"audience-facing", "recurring"}:
        raise ValueError("quality_profile must be audience-facing or recurring")
    if quality_profile == "audience-facing" and (
        not isinstance(slides[-1], dict)
        or str(slides[-1].get("kind", "")).strip().lower() != "closing"
    ):
        raise ValueError("an audience-facing deck must end with kind=closing")
    body_kinds: list[str] = []
    for index, slide in enumerate(slides, 1):
        if not isinstance(slide, dict):
            raise ValueError(f"slide {index} must be an object")
        kind = str(slide.get("kind", "")).strip().lower()
        if kind not in ALLOWED_KINDS:
            raise ValueError(f"slide {index} has unsupported kind: {kind!r}")
        if not str(slide.get("title", "")).strip():
            raise ValueError(f"slide {index} is missing a title")
        if index not in {1, len(slides)}:
            body_kinds.append(kind)
        for key in ("body", "takeaway", "claim", "note", "intro"):
            value = slide.get(key)
            if isinstance(value, str) and len(value.strip()) > 220:
                raise ValueError(
                    f"slide {index} field {key!r} is too dense; split or prioritize it instead of shrinking text"
                )
        validate_slide_content(slide, index, kind)
    validate_evidence_contract(payload)
    if quality_profile == "audience-facing" and len(slides) >= 8:
        counts = Counter(body_kinds)
        minimum_families = 5 if len(slides) >= 10 else 4
        if len(counts) < minimum_families:
            raise ValueError(
                f"audience-facing deck needs at least {minimum_families} distinct body composition kinds; got {sorted(counts)}"
            )
        if counts.get("cards", 0) > 1:
            raise ValueError("cards may appear at most once; use chart, timeline, matrix, process, or focused evidence pages")
        if not any(kind in EXPLANATORY_KINDS for kind in body_kinds):
            raise ValueError("audience-facing deck needs at least one explanatory chart/process/timeline/matrix/table/metrics page")
        for offset, (left, middle, right) in enumerate(zip(body_kinds, body_kinds[1:], body_kinds[2:]), start=2):
            if left == middle == right:
                raise ValueError(
                    f"slides {offset} to {offset + 2} repeat kind={left}; three consecutive content pages need another composition"
                )
    visible_text = json.dumps(payload, ensure_ascii=False).lower()
    for marker in ("replace with", "needs task content", "problem a", "evidence a", "[待补]", "placeholder"):
        if marker in visible_text:
            raise ValueError(f"spec still contains scaffold placeholder text: {marker}")
    return payload


def materialize_builder(spec: dict, output: Path, work_dir: Path) -> Path:
    asset_dir = Path(__file__).resolve().parent.parent / "assets" / "task-pptx"
    source_builder = asset_dir / "build_deck.py"
    source_design = asset_dir / "pptx_design.py"
    template = source_builder.read_text(encoding="utf-8")
    if template.count(OUTPUT_TOKEN) != 1 or template.count(COMPOSITION_MODE_TOKEN) != 1:
        raise RuntimeError("bundled builder has an unexpected task-token count")

    work_dir.mkdir(parents=True, exist_ok=True)
    builder = work_dir / "build_deck.py"
    rendered = (
        template.replace(OUTPUT_TOKEN, output.as_posix())
        .replace(COMPOSITION_MODE_TOKEN, "stable")
    )
    builder.write_text(rendered, encoding="utf-8", newline="\n")
    shutil.copy2(source_design, work_dir / "pptx_design.py")
    (work_dir / "deck_spec.json").write_text(
        json.dumps(spec, ensure_ascii=False, indent=2), encoding="utf-8", newline="\n"
    )
    return builder


def validate_output(output: Path) -> dict:
    if not output.is_file() or output.stat().st_size < 10 * 1024:
        raise RuntimeError("builder did not create a valid-sized PPTX")
    with zipfile.ZipFile(output) as package:
        slides = [
            name for name in package.namelist()
            if name.startswith("ppt/slides/slide") and name.endswith(".xml")
        ]
    if len(slides) < 6:
        raise RuntimeError(f"PPTX contains only {len(slides)} slides")
    return {
        "output": str(output),
        "slides": len(slides),
        "bytes": output.stat().st_size,
        "editable": True,
        "generation_platform": platform.system().lower(),
        "renderer": "python-pptx",
    }


def main() -> int:
    args = parse_args()
    spec_path = args.spec.expanduser().resolve()
    output = args.output.expanduser().resolve()
    if spec_path.suffix.lower() != ".json" or not spec_path.is_file():
        raise ValueError("--spec must be an existing .json file")
    if output.suffix.lower() != ".pptx":
        raise ValueError("--output must end in .pptx")
    work_dir = (args.work_dir or output.with_suffix("").with_name(output.stem + "_work")).expanduser().resolve()
    spec = load_and_validate(spec_path)
    output.parent.mkdir(parents=True, exist_ok=True)
    builder = materialize_builder(spec, output, work_dir)
    completed = subprocess.run(
        [sys.executable, str(builder)], cwd=str(work_dir), text=True,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"deck build failed: {completed.stdout[-2000:]}")
    result = validate_output(output)
    result["work_dir"] = str(work_dir)
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
