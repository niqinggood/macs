#!/usr/bin/env python3
"""Render a PPTX to deterministic per-slide PNGs on Linux/macOS.

The renderer uses LibreOffice for PPTX-to-PDF conversion and Poppler's
pdftoppm for PDF-to-PNG rasterization. It never executes input from the deck
and invokes both programs without a shell.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Render every PPTX slide with LibreOffice and Poppler."
    )
    parser.add_argument("--input", required=True, type=Path, help="input .pptx file")
    parser.add_argument("--output-dir", required=True, type=Path, help="PNG output directory")
    parser.add_argument("--dpi", type=int, default=150, help="PNG resolution, default 150 DPI")
    return parser.parse_args()


def find_program(env_name: str, candidates: list[str]) -> str:
    configured = os.environ.get(env_name, "").strip()
    if configured:
        path = Path(configured).expanduser()
        if path.is_file():
            return str(path.resolve())
        raise RuntimeError(f"{env_name} points to a missing executable: {path}")
    for candidate in candidates:
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
    raise RuntimeError(f"required program not found: {' or '.join(candidates)}")


def image_number(path: Path) -> int:
    match = re.search(r"-(\d+)\.png$", path.name, re.IGNORECASE)
    return int(match.group(1)) if match else 0


def optional_contact_sheet(images: list[Path], output_dir: Path) -> Path | None:
    try:
        from PIL import Image, ImageOps
    except ImportError:
        return None
    if not images:
        return None

    opened = [Image.open(path).convert("RGB") for path in images]
    try:
        thumb_width = 400
        ratio = opened[0].height / max(opened[0].width, 1)
        thumb_height = max(1, round(thumb_width * ratio))
        columns = min(4, len(opened))
        rows = (len(opened) + columns - 1) // columns
        sheet = Image.new("RGB", (columns * thumb_width, rows * thumb_height), (24, 28, 38))
        for index, image in enumerate(opened):
            thumb = ImageOps.fit(image, (thumb_width, thumb_height))
            sheet.paste(thumb, ((index % columns) * thumb_width, (index // columns) * thumb_height))
        target = output_dir / "contact_sheet.jpg"
        sheet.save(target, "JPEG", quality=90)
        return target
    finally:
        for image in opened:
            image.close()


def pixel_integrity_checks(images: list[Path]) -> list[dict[str, object]]:
    """Verify that every rendered page is decodable and visually non-empty."""

    try:
        from PIL import Image
    except ImportError as exc:
        raise RuntimeError("Pillow is required for cross-platform pixel QA") from exc

    checks: list[dict[str, object]] = []
    for index, path in enumerate(images, start=1):
        with Image.open(path) as image:
            rgb = image.convert("RGB")
            width, height = rgb.size
            if width < 640 or height < 360:
                raise RuntimeError(
                    f"rendered slide {index} is unexpectedly small: {width}x{height}"
                )
            step_x = max(1, width // 40)
            step_y = max(1, height // 24)
            sampled_colors = {
                rgb.getpixel((x, y))
                for y in range(0, height, step_y)
                for x in range(0, width, step_x)
            }
            if len(sampled_colors) < 2:
                raise RuntimeError(f"rendered slide {index} appears blank or corrupt")
            checks.append(
                {
                    "slide": index,
                    "width": width,
                    "height": height,
                    "bytes": path.stat().st_size,
                    "sampled_colors": len(sampled_colors),
                    "decodable": True,
                }
            )
    return checks


def run() -> dict[str, object]:
    args = parse_args()
    source = args.input.expanduser().resolve()
    output_dir = args.output_dir.expanduser().resolve()
    if source.suffix.lower() != ".pptx" or not source.is_file():
        raise RuntimeError(f"input PPTX not found: {source}")
    if args.dpi < 72 or args.dpi > 300:
        raise RuntimeError("--dpi must be between 72 and 300")

    office = find_program(
        "OPENKITTY_OFFICE_BIN",
        [
            "libreoffice",
            "soffice",
            "/Applications/LibreOffice.app/Contents/MacOS/soffice",
        ],
    )
    pdftoppm = find_program("OPENKITTY_PDFTOPPM_BIN", ["pdftoppm"])
    output_dir.mkdir(parents=True, exist_ok=True)
    for old in output_dir.glob("slide_*.png"):
        old.unlink()

    with tempfile.TemporaryDirectory(prefix="openkitty-ppt-render-") as raw_temp:
        temp_dir = Path(raw_temp).resolve()
        profile_dir = temp_dir / "lo-profile"
        profile_dir.mkdir()
        conversion = subprocess.run(
            [
                office,
                "--headless",
                "--nologo",
                "--nolockcheck",
                "--nodefault",
                "--nofirststartwizard",
                f"-env:UserInstallation={profile_dir.as_uri()}",
                "--convert-to",
                "pdf",
                "--outdir",
                str(temp_dir),
                str(source),
            ],
            check=False,
            capture_output=True,
            text=True,
            timeout=180,
        )
        pdf = temp_dir / f"{source.stem}.pdf"
        if conversion.returncode != 0 or not pdf.is_file():
            detail = (conversion.stderr or conversion.stdout).strip()
            raise RuntimeError(
                f"failed to render presentation with LibreOffice (exit {conversion.returncode}): {detail}"
            )

        prefix = temp_dir / "slide"
        raster = subprocess.run(
            [pdftoppm, "-png", "-r", str(args.dpi), str(pdf), str(prefix)],
            check=False,
            capture_output=True,
            text=True,
            timeout=180,
        )
        if raster.returncode != 0:
            detail = (raster.stderr or raster.stdout).strip()
            raise RuntimeError(f"failed to rasterize presentation PDF (exit {raster.returncode}): {detail}")

        raw_images = sorted(temp_dir.glob("slide-*.png"), key=image_number)
        if not raw_images:
            raise RuntimeError("LibreOffice conversion produced no rendered slide images")
        width = max(2, len(str(len(raw_images))))
        images: list[Path] = []
        for index, raw_image in enumerate(raw_images, start=1):
            target = output_dir / f"slide_{index:0{width}d}.png"
            shutil.move(str(raw_image), target)
            images.append(target)

    pixel_checks = pixel_integrity_checks(images)
    contact_sheet = optional_contact_sheet(images, output_dir)
    if contact_sheet is None:
        raise RuntimeError("Pillow could not create contact_sheet.jpg")
    return {
        "ok": True,
        "presentation_rendered": True,
        "render_fidelity": "high_fidelity_libreoffice",
        "input": str(source),
        "output_dir": str(output_dir),
        "slide_count": len(images),
        "dpi": args.dpi,
        "images": [str(path) for path in images],
        "contact_sheet": str(contact_sheet),
        "pixel_checks": pixel_checks,
        "slides_rendered": len(pixel_checks),
        "pixel_integrity_checked": True,
        "all_slide_images_decodable": True,
        "presentation_visual_inspected": False,
        "visual_inspection_required": True,
        "pixel_integrity_scope": (
            "LibreOffice render and image decodability only; use audit_pptx.py for structure "
            "and an actual image/vision tool for composition, clipping, overlap, and aesthetics"
        ),
    }


def main() -> int:
    try:
        print(json.dumps(run(), ensure_ascii=False, indent=2))
        return 0
    except (OSError, RuntimeError, subprocess.SubprocessError) as exc:
        print(
            json.dumps(
                {
                    "ok": False,
                    "presentation_rendered": False,
                    "error": str(exc),
                },
                ensure_ascii=False,
                indent=2,
            )
        )
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
