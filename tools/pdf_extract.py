#!/usr/bin/env python3
"""
pdf_extract.py — PDF text + table extraction using pdfplumber.

Usage:
    python pdf_extract.py <pdf_path>                    # text only, layout-preserving
    python pdf_extract.py <pdf_path> --tables           # text + markdown tables
    python pdf_extract.py <pdf_path> --tables-only      # only tables as markdown

Output: structured text to stdout.

Install: pip install pdfplumber
"""

import sys
import json
if sys.platform == 'win32':
    sys.stdout.reconfigure(encoding='utf-8', errors='replace')
    sys.stderr.reconfigure(encoding='utf-8', errors='replace')


def extract_text(pdf_path):
    """Extract text with correct reading order, handling multi-column layouts."""
    try:
        import pdfplumber
    except ImportError:
        print("PDFPLUMBER_NOT_INSTALLED", file=sys.stderr)
        sys.exit(2)

    with pdfplumber.open(pdf_path) as pdf:
        pages = []
        for i, page in enumerate(pdf.pages):
            # extract_text() uses the default layout algorithm which handles
            # multi-column PDFs much better than naive x/y sorting
            text = page.extract_text()
            if text:
                pages.append(text)
        return "\n\n".join(pages)


def extract_tables(pdf_path):
    """Extract all tables from the PDF, returning a list of {page, index, rows}."""
    try:
        import pdfplumber
    except ImportError:
        print("PDFPLUMBER_NOT_INSTALLED", file=sys.stderr)
        sys.exit(2)

    all_tables = []
    with pdfplumber.open(pdf_path) as pdf:
        for i, page in enumerate(pdf.pages):
            tables = page.extract_tables()
            for j, table in enumerate(tables):
                if not table:
                    continue
                # Clean up: replace None with empty string, strip whitespace
                cleaned = [[(cell or "").strip() for cell in row] for row in table]
                # Skip empty rows
                cleaned = [row for row in cleaned if any(cell for cell in row)]
                if cleaned:
                    all_tables.append({
                        "page": i + 1,
                        "index": j + 1,
                        "rows": cleaned,
                    })
    return all_tables


def tables_to_markdown(tables):
    """Convert extracted tables to markdown format."""
    if not tables:
        return ""

    parts = []
    for t in tables:
        parts.append(f"\n### 表格 (第 {t['page']} 页, 表 {t['index']})\n")
        rows = t["rows"]
        if not rows:
            continue

        # Header row
        parts.append("| " + " | ".join(rows[0]) + " |")
        parts.append("| " + " | ".join(["---"] * len(rows[0])) + " |")

        # Data rows
        for row in rows[1:]:
            # Pad row to match header length
            padded = row + [""] * (len(rows[0]) - len(row))
            parts.append("| " + " | ".join(padded[:len(rows[0])]) + " |")

        parts.append("")

    return "\n".join(parts)


def main():
    if len(sys.argv) < 2:
        print("Usage: python pdf_extract.py <pdf_path> [--tables] [--tables-only]", file=sys.stderr)
        sys.exit(1)

    pdf_path = sys.argv[1]
    mode = "text"
    if "--tables-only" in sys.argv:
        mode = "tables-only"
    elif "--tables" in sys.argv:
        mode = "text+tables"

    try:
        if mode == "tables-only":
            tables = extract_tables(pdf_path)
            print(tables_to_markdown(tables))
        else:
            text = extract_text(pdf_path)
            print(text)
            if mode == "text+tables":
                tables = extract_tables(pdf_path)
                md = tables_to_markdown(tables)
                if md:
                    print("\n---\n")
                    print("## 表格\n")
                    print(md)

    except Exception as e:
        print(f"PDFPLUMBER_ERROR: {e}", file=sys.stderr)
        sys.exit(3)


if __name__ == "__main__":
    main()
