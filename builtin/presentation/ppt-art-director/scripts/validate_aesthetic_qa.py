#!/usr/bin/env python3
"""Validate a vision-authored per-slide aesthetic QA ledger."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import zipfile
from pathlib import Path


SLIDE_KEYS = {
    "hierarchy", "composition", "typography", "asset_quality",
    "visual_relevance", "density_balance",
}
DECK_KEYS = {"coherence", "rhythm", "visual_storytelling", "originality"}
SLIDE_IMAGE = re.compile(r"^slide[_-]?0*([1-9]\d*)\.(?:png|jpe?g|webp)$", re.IGNORECASE)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--qa", required=True, type=Path)
    parser.add_argument("--deck", required=True, type=Path)
    parser.add_argument("--render-dir", required=True, type=Path)
    parser.add_argument("--expected-slides", type=int, default=0)
    parser.add_argument("--prepare", action="store_true", help="Create an unreviewed hash-bound ledger; never marks visual QA as passed")
    return parser.parse_args()


def pptx_slide_count(path: Path) -> int:
    with zipfile.ZipFile(path) as archive:
        return len([
            name for name in archive.namelist()
            if re.fullmatch(r"ppt/slides/slide\d+\.xml", name)
        ])


def numeric_score(value: object, label: str, errors: list[str]) -> float:
    if not isinstance(value, (int, float)) or isinstance(value, bool) or not 0 <= value <= 100:
        errors.append(f"{label} must be a number within 0..100")
        return 0
    return float(value)


def resolve_image(render_dir: Path, value: object) -> Path | None:
    if not isinstance(value, str) or not value.strip():
        return None
    path = Path(value.strip()).expanduser()
    if not path.is_absolute():
        path = render_dir / path
    return path.resolve()


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            value.update(chunk)
    return value.hexdigest()


def prepare(qa_path: Path, deck_path: Path, render_dir: Path, expected: int = 0) -> dict[str, object]:
    """Prepare evidence identities, not aesthetic judgments. Do not overwrite a review."""
    deck_path, render_dir = deck_path.resolve(), render_dir.resolve()
    count = pptx_slide_count(deck_path)
    if count < 1 or (expected and count != expected):
        raise ValueError("deck slide count does not match the review request")
    images: dict[int, Path] = {}
    for path in render_dir.iterdir():
        match = SLIDE_IMAGE.fullmatch(path.name)
        if not match or not path.is_file():
            continue
        number = int(match.group(1))
        if number in images or path.resolve().parent != render_dir:
            raise ValueError("ambiguous or external rendered slide image")
        if path.stat().st_mtime + 1 < deck_path.stat().st_mtime:
            raise ValueError("rendered slides predate the deck; render again before review")
        images[number] = path
    if set(images) != set(range(1, count + 1)):
        raise ValueError("render directory must contain exactly one final image per slide")
    ledger = {
        "schema_version": 2,
        "deck_sha256": digest(deck_path),
        "verdict": "unreviewed",
        "slides": [{
            "slide": i, "image": images[i].name, "image_sha256": digest(images[i]),
            "scores": {key: None for key in sorted(SLIDE_KEYS)},
            "observations": [], "blocking_defects": [],
        } for i in range(1, count + 1)],
        "deck_scores": {key: None for key in sorted(DECK_KEYS)},
        "repairs_applied": [],
    }
    with qa_path.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(ledger, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    return {"aesthetic_qa_prepared": True, "aesthetic_qa_passed": False, "qa": str(qa_path.resolve()), "slide_count": count}


def validate(qa_path: Path, deck_path: Path, render_dir: Path, expected: int = 0) -> dict[str, object]:
    qa_path = qa_path.expanduser().resolve()
    deck_path = deck_path.expanduser().resolve()
    render_dir = render_dir.expanduser().resolve()
    if not deck_path.is_file():
        raise ValueError(f"deck does not exist: {deck_path}")
    if not render_dir.is_dir():
        raise ValueError(f"render directory does not exist: {render_dir}")
    actual = pptx_slide_count(deck_path)
    if actual < 1:
        raise ValueError("deck must contain at least one slide")
    if expected and actual != expected:
        raise ValueError(f"deck has {actual} slides; expected {expected}")
    expected = expected or actual
    qa = json.loads(qa_path.read_text(encoding="utf-8"))
    if not isinstance(qa, dict):
        raise ValueError("aesthetic ledger must be an object")
    errors: list[str] = []
    if qa.get("schema_version") != 2 or qa.get("deck_sha256") != digest(deck_path):
        errors.append("ledger must be prepared for the exact final deck (schema_version=2 and matching deck_sha256)")

    slides = qa.get("slides")
    if not isinstance(slides, list) or len(slides) != expected:
        errors.append(f"slides must contain exactly {expected} entries")
        slides = slides if isinstance(slides, list) else []
    covered: set[int] = set()
    slide_averages: list[float] = []
    for position, entry in enumerate(slides, 1):
        if not isinstance(entry, dict):
            errors.append(f"slides[{position}] must be an object")
            continue
        number = entry.get("slide")
        if type(number) is not int or number != position:
            errors.append(f"slides[{position}].slide must equal {position}")
        else:
            covered.add(position)
        image = resolve_image(render_dir, entry.get("image"))
        if image is None or not image.is_file():
            errors.append(f"slides[{position}].image must exist in the final render directory")
        elif image.parent != render_dir:
            errors.append(f"slides[{position}].image must stay inside the final render directory")
        elif not SLIDE_IMAGE.fullmatch(image.name) or int(SLIDE_IMAGE.fullmatch(image.name).group(1)) != position:
            errors.append(f"slides[{position}].image must be the matching slide_NN image")
        elif entry.get("image_sha256") != digest(image):
            errors.append(f"slides[{position}].image_sha256 does not match the reviewed image; reinspect it")
        elif image.stat().st_mtime + 1 < deck_path.stat().st_mtime:
            errors.append(f"slides[{position}].image predates the deck; render it again")
        scores = entry.get("scores")
        if not isinstance(scores, dict):
            errors.append(f"slides[{position}].scores must be an object")
            scores = {}
        values = [numeric_score(scores.get(key), f"slides[{position}].scores.{key}", errors) for key in sorted(SLIDE_KEYS)]
        if values:
            average = sum(values) / len(values)
            slide_averages.append(average)
            if min(values) < 65:
                errors.append(f"slide {position} has an aesthetic dimension below 65")
            if average < 75:
                errors.append(f"slide {position} average aesthetic score is below 75")
        observations = entry.get("observations")
        if not isinstance(observations, list) or not observations or not all(isinstance(item, str) and item.strip() for item in observations):
            errors.append(f"slides[{position}].observations must contain vision-grounded notes")
        defects = entry.get("blocking_defects")
        if not isinstance(defects, list):
            errors.append(f"slides[{position}].blocking_defects must be an array")
        elif defects:
            errors.append(f"slide {position} has unresolved blocking defects")

    deck_scores = qa.get("deck_scores")
    if not isinstance(deck_scores, dict):
        errors.append("deck_scores must be an object")
        deck_scores = {}
    for key in sorted(DECK_KEYS):
        if numeric_score(deck_scores.get(key), f"deck_scores.{key}", errors) < 75:
            errors.append(f"deck_scores.{key} is below 75")
    if qa.get("verdict") != "pass":
        errors.append('verdict must be "pass"')
    if not isinstance(qa.get("repairs_applied"), list):
        errors.append("repairs_applied must be an array")
    if covered != set(range(1, expected + 1)):
        errors.append("per-slide aesthetic coverage is incomplete")

    if errors:
        raise ValueError("; ".join(errors))
    return {
        "aesthetic_qa_passed": True,
        "hash_bound": True,
        "qa": str(qa_path),
        "deck": str(deck_path),
        "render_dir": str(render_dir),
        "slide_count": expected,
        "minimum_slide_average": round(min(slide_averages), 2),
        "deck_scores": {key: deck_scores[key] for key in sorted(DECK_KEYS)},
    }


def main() -> int:
    args = parse_args()
    try:
        operation = prepare if args.prepare else validate
        receipt = operation(args.qa, args.deck, args.render_dir, args.expected_slides)
    except (OSError, ValueError, json.JSONDecodeError, zipfile.BadZipFile) as exc:
        print(json.dumps({"aesthetic_qa_passed": False, "error": str(exc)}, ensure_ascii=False, indent=2))
        return 2
    print(json.dumps(receipt, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
