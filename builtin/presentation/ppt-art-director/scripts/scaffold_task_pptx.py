#!/usr/bin/env python3
"""Materialize a task-specific editable PPTX builder from bundled assets."""

from __future__ import annotations

import argparse
import json
import shutil
from pathlib import Path


OUTPUT_TOKEN = "__TASK_OUTPUT_PPTX__"
COMPOSITION_MODE_TOKEN = "__TASK_COMPOSITION_MODE__"


def is_shared_storage_root(path: Path) -> bool:
    """Return True only for the collision-prone .openkitty/storage root itself."""

    parts = [part.lower() for part in path.resolve().parts]
    return len(parts) >= 2 and parts[-2:] == [".openkitty", "storage"]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Copy the task PPTX design system and builder skeleton into a task directory."
    )
    parser.add_argument(
        "--builder",
        required=True,
        type=Path,
        help="destination path for the generated task builder, for example work/build_deck.py",
    )
    parser.add_argument(
        "--output",
        required=True,
        type=Path,
        help="absolute or working-directory-relative .pptx output path embedded in the builder",
    )
    return parser.parse_args()


def scaffold(builder: Path, output: Path) -> dict[str, str]:
    script_dir = Path(__file__).resolve().parent
    asset_dir = script_dir.parent / "assets" / "task-pptx"
    source_builder = asset_dir / "build_deck.py"
    source_design = asset_dir / "pptx_design.py"
    source_spec = asset_dir / "deck_spec.json"
    source_visual_plan = asset_dir / "visual_plan.json"

    builder = builder.expanduser().resolve()
    output = output.expanduser().resolve()
    if builder.suffix.lower() != ".py":
        raise ValueError(f"--builder must end in .py: {builder}")
    if output.suffix.lower() != ".pptx":
        raise ValueError(f"--output must end in .pptx: {output}")
    if builder == source_builder:
        raise ValueError("--builder cannot overwrite the bundled template")
    if is_shared_storage_root(builder.parent):
        suggested = builder.parent / "ppt_runs" / "<unique-run-id>" / builder.name
        raise ValueError(
            "TASK_PRIVATE_RUN_DIRECTORY_REQUIRED: do not scaffold build_deck.py, "
            "deck_spec.json, or pptx_design.py directly in the shared .openkitty/storage "
            f"root. Use a new run directory, for example: {suggested}"
        )

    design = builder.parent / "pptx_design.py"
    spec = builder.parent / "deck_spec.json"
    visual_plan = builder.parent / "visual_plan.json"
    collisions = [path for path in (builder, design, spec, visual_plan) if path.exists()]
    if collisions:
        joined = ", ".join(str(path) for path in collisions)
        raise FileExistsError(
            "TASK_RUN_ALREADY_EXISTS: refusing to overwrite task files: "
            f"{joined}. Continue from this run's existing builder, or choose a new unique run directory; "
            "a scaffold collision is not a renderer failure and never justifies a quality downgrade."
        )

    template = source_builder.read_text(encoding="utf-8")
    if template.count(OUTPUT_TOKEN) != 1 or template.count(COMPOSITION_MODE_TOKEN) != 1:
        raise RuntimeError("bundled builder has an unexpected task-token count")
    rendered = (
        template.replace(OUTPUT_TOKEN, output.as_posix())
        .replace(COMPOSITION_MODE_TOKEN, "per-slide")
    )

    builder.parent.mkdir(parents=True, exist_ok=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source_design, design)
    shutil.copy2(source_spec, spec)
    shutil.copy2(source_visual_plan, visual_plan)
    builder.write_text(rendered, encoding="utf-8", newline="\n")

    return {
        "run_dir": str(builder.parent),
        "builder": str(builder),
        "design": str(design),
        "spec": str(spec),
        "visual_plan": str(visual_plan),
        "output": str(output),
        "next_action": (
            "Read this task-local build_deck.py exactly once. Freeze the slide architecture, then write "
            "deck_spec.json once with the task facts and requested audience-facing slides (normally 6-14) "
            "and set needs_customization=false. Keep evidence_policy=source-bound; add a top-level "
            "evidence_ledger with exact trusted excerpts, and make every factual slide cite evidence_refs. "
            "Complete visual_plan.json before the first build: compare 2-3 materially different directions, "
            "select one executable palette/type/image/chart system, map one purposeful primary asset to every "
            "slide, set needs_customization=false, and run the Runtime Directory's "
            "scripts/validate_visual_plan.py with --expected-slides. Acquire or generate every planned media "
            "file before building. Complete deck_spec art_direction.visual_thesis, composition_strategy, "
            "asset_strategy, palette_strategy, typography_strategy, and background_rhythm so they agree with the "
            "selected visual direction. Use conclusion-led titles, a 50/35/24/16 type floor, and give one proof "
            "object most of each content page. Task renderers must consume SLIDE_VISUALS; planned media must be "
            "placed with planned_image() or image_cover/image_contain. "
            "For body compositions, optional layout_family=editorial|spotlight|comparison|metric "
            "and primary_share/mirror in visual_plan.json feed planned_layout(page), whose named regions "
            "expose .box=(x,y,w,h). These regions draw no decorative containers. "
            "Use bounded anchored edits inside TASK PAGE COMPOSITIONS "
            "to register one named task-local renderer for every slide; only then execute build_deck.py. "
            "Supported semantic slide kinds and reusable mechanics: cover, "
            "statement, cards, split, process, metrics, chart, table, matrix, timeline, closing. "
            "Common schemas: cards items accept title/label, body/note, optional value, and an optional "
            "conclusion {title,body} or takeaway rendered as a visible conclusion rail; split "
            "accepts left/right objects or left_title/left_items plus right_title/right_items; "
            "process accepts items with title and body/desc plus an optional lateral string list; metrics accepts items or metrics; "
            "matrix accepts x/y points or columns/rows/values; timeline accepts {label,title,detail} or "
            "{label,milestone,points}; closing accepts up to three items plus three commitments. "
            "Use chart/table/matrix only for supplied or explicitly labeled assumption data. "
            "For 8 slides use at least four body composition families; two consecutive families are "
            "allowed but three are not. Preserve all required facts and rows. deck_spec.json is the truth "
            "contract, not the final layout engine. After structural audit and native rendering, inspect the "
            "contact sheet and every slide separately. Run validate_aesthetic_qa.py --prepare for a new "
            "hash-bound unreviewed ledger, fill it from actual vision observations, and "
            "run scripts/validate_aesthetic_qa.py before delivery. Do not inspect or edit pptx_design.py."
        ),
        "api_contract": (
            "rect/round_rect/circle use line= and line_width= (never stroke/stroke_w); "
            "line uses color=, weight=, dash=, arrow=; create edges before nodes; "
            "native_chart and editable_table create editable objects; timeline consumes caller order; "
            "matrix plots require explicit label/x/y with x/y in 0..1; comparison matrices use "
            "columns/rows/values; decision_frame owns only the "
            "claim/evidence rail; proportional_bar plus text_box supports direct-label comparisons; "
            "rich_text_box supports controlled mixed emphasis without serialized style payloads."
        ),
    }


def main() -> int:
    args = parse_args()
    result = scaffold(args.builder, args.output)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
