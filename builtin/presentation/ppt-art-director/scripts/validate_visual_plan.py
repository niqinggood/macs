#!/usr/bin/env python3
"""Validate an executable per-deck visual direction and asset plan."""

from __future__ import annotations

import argparse
import json
import math
import re
from pathlib import Path


HEX_COLOR = re.compile(r"^#[0-9A-Fa-f]{6}$")
MEDIA_TYPES = {"photo", "illustration", "screenshot"}
VISUAL_TYPES = MEDIA_TYPES | {"chart", "table", "diagram", "map", "icon-system"}
ALLOWED_TYPES = VISUAL_TYPES | {"typographic"}
ALLOWED_DENSITIES = {"light", "medium", "dense"}
ALLOWED_CROPS = {"cover", "contain", "left", "right", "background"}
PALETTE_KEYS = {
    "navy", "ink", "muted", "line", "paper", "white", "accent", "accent_soft",
    "positive", "positive_soft", "warning", "warning_soft", "negative", "negative_soft",
}
TYPE_FLOORS = {
    "deck_title_pt": 50,
    "slide_title_pt": 35,
    "subhead_pt": 24,
    "body_pt": 16,
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("plan", type=Path)
    parser.add_argument("--expected-slides", type=int, default=0)
    return parser.parse_args()


def text(value: object) -> str:
    return value.strip() if isinstance(value, str) else ""


def resolve_asset(plan_path: Path, raw: str) -> Path:
    path = Path(raw).expanduser()
    if not path.is_absolute():
        path = plan_path.parent / path
    return path.resolve()


def validate(plan_path: Path, expected_slides: int = 0) -> dict[str, object]:
    plan_path = plan_path.expanduser().resolve()
    plan = json.loads(plan_path.read_text(encoding="utf-8"))
    errors: list[str] = []
    if plan.get("needs_customization", True):
        errors.append("visual_plan.json still needs customization")
    serialized = json.dumps(plan, ensure_ascii=False).lower()
    if "replace with" in serialized or "待替换" in serialized:
        errors.append("visual_plan.json still contains template placeholder text")
    if not text(plan.get("communication_job")):
        errors.append("communication_job is required")

    selected = plan.get("selected_direction")
    if not isinstance(selected, dict):
        errors.append("selected_direction must be an object")
        selected = {}
    for key in ("id", "name", "visual_thesis", "rationale", "image_style", "chart_style"):
        if not text(selected.get(key)):
            errors.append(f"selected_direction.{key} is required")
    rhythm = selected.get("background_rhythm")
    if not isinstance(rhythm, list) or not rhythm or not all(text(item) for item in rhythm):
        errors.append("selected_direction.background_rhythm must be a non-empty string list")
        rhythm = []

    palette = selected.get("palette")
    if not isinstance(palette, dict):
        errors.append("selected_direction.palette must be an object")
        palette = {}
    for key in sorted(PALETTE_KEYS):
        if not HEX_COLOR.fullmatch(text(palette.get(key))):
            errors.append(f"selected_direction.palette.{key} must be #RRGGBB")

    typography = selected.get("typography")
    if not isinstance(typography, dict):
        errors.append("selected_direction.typography must be an object")
        typography = {}
    for key in ("font_cn", "font_en"):
        if not text(typography.get(key)):
            errors.append(f"selected_direction.typography.{key} is required")
    for key, floor in TYPE_FLOORS.items():
        value = typography.get(key)
        if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value) or value < floor:
            errors.append(f"selected_direction.typography.{key} must be at least {floor}")

    selected_id = text(selected.get("id"))
    candidates = plan.get("candidates")
    if not isinstance(candidates, list):
        candidates = []
        errors.append("candidates must be an array")
    minimum_candidates = 1 if plan.get("reference_owned") is True else 2
    if not minimum_candidates <= len(candidates) <= 3:
        errors.append(f"candidates must contain {minimum_candidates} to 3 directions")
    candidate_ids: list[str] = []
    for index, candidate in enumerate(candidates, 1):
        if not isinstance(candidate, dict):
            errors.append(f"candidates[{index}] must be an object")
            continue
        for key in ("id", "name", "visual_thesis", "differentiator"):
            if not text(candidate.get(key)):
                errors.append(f"candidates[{index}].{key} is required")
        candidate_ids.append(text(candidate.get("id")))
    if selected_id and selected_id not in candidate_ids:
        errors.append("selected_direction.id must match one candidate id")
    if len(set(candidate_ids)) != len(candidate_ids):
        errors.append("candidate ids must be unique")

    slides = plan.get("slides")
    if not isinstance(slides, list):
        slides = []
        errors.append("slides must be an array")
    if expected_slides and len(slides) != expected_slides:
        errors.append(f"visual plan has {len(slides)} slides; expected {expected_slides}")
    if len(rhythm) not in {0, len(slides)}:
        errors.append("background_rhythm must have one entry per slide")

    compositions: list[str] = []
    visual_count = 0
    media_count = 0
    for index, slide in enumerate(slides, 1):
        if not isinstance(slide, dict):
            errors.append(f"slides[{index}] must be an object")
            continue
        if type(slide.get("slide")) is not int or slide.get("slide") != index:
            errors.append(f"slides[{index}].slide must equal {index}")
        if "layout_family" in slide:
            if not isinstance(slide["layout_family"], str) or slide["layout_family"] not in {"editorial", "spotlight", "comparison", "metric"}:
                errors.append(f"slides[{index}].layout_family is unsupported")
            share = slide.get("primary_share", 0.68)
            if not isinstance(share, (int, float)) or isinstance(share, bool) or not 0.55 <= share <= 0.78:
                errors.append(f"slides[{index}].primary_share must be within 0.55..0.78")
            if type(slide.get("mirror", False)) is not bool:
                errors.append(f"slides[{index}].mirror must be boolean")
        for key in ("narrative_job", "composition", "focal_anchor"):
            if not text(slide.get(key)):
                errors.append(f"slides[{index}].{key} is required")
        composition = text(slide.get("composition")).lower()
        if composition:
            compositions.append(composition)
        density = text(slide.get("density")).lower()
        if density not in ALLOWED_DENSITIES:
            errors.append(f"slides[{index}].density must be light, medium, or dense")
        asset = slide.get("asset")
        if not isinstance(asset, dict):
            errors.append(f"slides[{index}].asset must be an object")
            continue
        asset_type = text(asset.get("type")).lower()
        if asset_type not in ALLOWED_TYPES:
            errors.append(f"slides[{index}].asset.type is unsupported: {asset_type!r}")
        if asset_type in VISUAL_TYPES:
            visual_count += 1
        if asset_type in MEDIA_TYPES:
            media_count += 1
        for key in ("purpose", "source"):
            if not text(asset.get(key)):
                errors.append(f"slides[{index}].asset.{key} is required")
        crop = text(asset.get("crop")).lower()
        if crop not in ALLOWED_CROPS:
            errors.append(f"slides[{index}].asset.crop is unsupported: {crop!r}")
        if asset_type in MEDIA_TYPES:
            raw_path = text(asset.get("path"))
            if not raw_path:
                errors.append(f"slides[{index}] media asset requires path")
            elif not resolve_asset(plan_path, raw_path).is_file():
                errors.append(f"slides[{index}] media asset does not exist: {raw_path}")
            for focal_key in ("focal_x", "focal_y"):
                focal = asset.get(focal_key, 0.5)
                if not isinstance(focal, (int, float)) or isinstance(focal, bool) or not 0 <= focal <= 1:
                    errors.append(f"slides[{index}].asset.{focal_key} must be within 0..1")

    minimum_families = 4 if len(slides) >= 8 else 3 if len(slides) >= 6 else 2
    if slides and len(set(compositions)) < minimum_families:
        errors.append(f"visual plan needs at least {minimum_families} composition families")
    if any(a == b == c for a, b, c in zip(compositions, compositions[1:], compositions[2:])):
        errors.append("three consecutive slides must not repeat one composition family")
    minimum_visuals = max(2, math.ceil(len(slides) / 2)) if slides else 0
    if visual_count < minimum_visuals:
        errors.append(f"visual plan needs at least {minimum_visuals} non-typographic proof objects")

    if errors:
        raise ValueError("; ".join(errors))
    return {
        "visual_plan_valid": True,
        "plan": str(plan_path),
        "slide_count": len(slides),
        "composition_families": len(set(compositions)),
        "visual_asset_count": visual_count,
        "media_asset_count": media_count,
        "selected_direction": selected_id,
    }


def main() -> int:
    args = parse_args()
    try:
        receipt = validate(args.plan, args.expected_slides)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(json.dumps({"visual_plan_valid": False, "error": str(exc)}, ensure_ascii=False, indent=2))
        return 2
    print(json.dumps(receipt, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
