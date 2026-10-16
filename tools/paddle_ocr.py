#!/usr/bin/env python3
"""
paddle_ocr.py — Chinese/English OCR using Baidu PaddleOCR.
Usage:
    python paddle_ocr.py <image_path>              # text output
    python paddle_ocr.py <image_path> --json       # JSON with boxes
    python paddle_ocr.py <image_path> --lang en    # English only

Output: recognized text to stdout.
Exit codes: 0=success, 2=not installed, 3=error
Install: pip install paddleocr paddlepaddle
"""

import sys
import json
import os


def ocr_image(image_path, lang="ch"):
    """Run OCR on an image and return (full_text, box_results)."""
    try:
        from paddleocr import PaddleOCR
    except ImportError:
        print("PADDLEOCR_NOT_INSTALLED", file=sys.stderr)
        sys.exit(2)

    try:
        ocr = PaddleOCR(lang=lang)
    except Exception as e:
        print(f"PADDLEOCR_INIT_ERROR: {e}", file=sys.stderr)
        sys.exit(3)

    result = ocr.ocr(image_path)
    if not result or not result[0]:
        print("NO_TEXT_DETECTED", file=sys.stderr)
        return ("", [])

    lines = result[0]
    full_text_parts = []
    boxes = []

    for line_data in lines:
        if not line_data:
            continue
        box, (text, confidence) = line_data
        full_text_parts.append(text)
        boxes.append({
            "text": text,
            "confidence": round(float(confidence), 4),
            "box": [[int(p[0]), int(p[1])] for p in box],
        })

    return ("\n".join(full_text_parts), boxes)


def main():
    if len(sys.argv) < 2:
        print("Usage: python paddle_ocr.py <image> [--json] [--lang ch]", file=sys.stderr)
        sys.exit(1)

    image_path = sys.argv[1]
    if not os.path.isfile(image_path):
        print(f"FILE_NOT_FOUND: {image_path}", file=sys.stderr)
        sys.exit(3)

    lang = "ch"
    if "--lang" in sys.argv:
        idx = sys.argv.index("--lang")
        if idx + 1 < len(sys.argv):
            lang = sys.argv[idx + 1]

    use_json = "--json" in sys.argv

    try:
        full_text, boxes = ocr_image(image_path, lang)
        if use_json:
            print(json.dumps({"text": full_text, "boxes": boxes}, ensure_ascii=False, indent=2))
        else:
            print(full_text)
    except Exception as e:
        print(f"PADDLEOCR_ERROR: {e}", file=sys.stderr)
        sys.exit(3)


if __name__ == "__main__":
    main()
