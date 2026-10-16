#!/usr/bin/env python3
"""
unstructured_extract.py — High-quality document parsing using the Unstructured library.

Usage:
    python unstructured_extract.py <file_path>               # structured markdown (default)
    python unstructured_extract.py <file_path> --json        # JSON output with element types
    python unstructured_extract.py <file_path> --text-only   # plain text only

Supported formats: PDF, DOCX, PPTX, HTML, TXT, MD, images (with OCR), CSV, XML, EPUB

Output: structured text with # markers for headings, table preservation.
Exit codes: 0=success, 2=not installed, 3=extraction error

Install: pip install unstructured pdf2image pillow
"""

import sys
import json
import os


def extract_elements(file_path):
    """Partition a document and return Unstructured elements."""
    try:
        from unstructured.partition.auto import partition
    except ImportError:
        print("UNSTRUCTURED_NOT_INSTALLED", file=sys.stderr)
        sys.exit(2)

    # partition() auto-detects file type and picks the right parser
    elements = partition(
        filename=file_path,
        strategy="auto",           # fast for native PDF text, hi_res for scanned/OCR
        include_page_breaks=True,  # insert PageBreak elements between pages
    )
    return elements


def elements_to_markdown(elements):
    """Convert Unstructured elements to structured markdown text.

    Heading rules:
    - Title → # heading
    - Section-header / Headline → ## heading
    - Sub-headline → ### heading
    - Table → rendered as markdown table (simplified)
    - ListItem → * bullet
    - PageBreak → --- separator
    - NarrativeText / UncategorizedText → plain paragraph
    """
    output = []
    prev_was_heading = False

    for el in elements:
        etype = str(el.category) if hasattr(el, 'category') else type(el).__name__
        text = str(el.text).strip() if el.text else ""
        if not text and etype != "PageBreak":
            continue

        if etype == "PageBreak":
            output.append("\n---\n")
            prev_was_heading = False
            continue

        if etype == "Title":
            output.append(f"\n# {text}\n")
            prev_was_heading = True
        elif etype in ("Section-header", "Headline", "Header"):
            output.append(f"\n## {text}\n")
            prev_was_heading = True
        elif etype in ("Sub-headline", "Subheadline", "SubHeader"):
            output.append(f"\n### {text}\n")
            prev_was_heading = True
        elif etype == "ListItem":
            output.append(f"* {text}")
            prev_was_heading = False
        elif etype == "Table":
            # Unstructured Table element has html_text which we can use
            html = getattr(el, 'html_text', None)
            if html:
                output.append(f"\n{html_to_markdown_table(html)}\n")
            else:
                output.append(f"\n```\n{text}\n```\n")
            prev_was_heading = False
        elif etype in ("NarrativeText", "UncategorizedText", "Text", "Paragraph"):
            if prev_was_heading:
                output.append("")  # blank line after heading
            output.append(text)
            output.append("")
            prev_was_heading = False
        elif etype in ("Image", "Picture"):
            output.append(f"[Image: {text}]") if text else None
            prev_was_heading = False
        else:
            # Catch-all for any other element type
            if text:
                output.append(text)
                output.append("")
                prev_was_heading = False

    return "\n".join(output)


def html_to_markdown_table(html_text):
    """Convert simple HTML table to markdown table."""
    try:
        from html.parser import HTMLParser

        rows = []
        current_row = []
        current_tag = None

        class TableParser(HTMLParser):
            def handle_starttag(self, tag, attrs):
                nonlocal current_tag
                current_tag = tag
                if tag == 'tr':
                    nonlocal current_row
                    current_row = []

            def handle_endtag(self, tag):
                nonlocal current_tag, current_row, rows
                if tag == 'tr' and current_row:
                    rows.append(current_row)
                    current_row = []
                current_tag = None

            def handle_data(self, data):
                nonlocal current_tag, current_row
                if current_tag in ('td', 'th') and data.strip():
                    current_row.append(data.strip())

        parser = TableParser()
        parser.feed(html_text)

        if not rows:
            return html_text  # fallback

        md = []
        # Header
        md.append("| " + " | ".join(rows[0]) + " |")
        md.append("| " + " | ".join(["---"] * len(rows[0])) + " |")
        # Body
        for row in rows[1:]:
            padded = row + [""] * (len(rows[0]) - len(row))
            md.append("| " + " | ".join(padded) + " |")

        return "\n".join(md)
    except Exception:
        return html_text  # fallback


def elements_to_json(elements):
    """Convert Unstructured elements to JSON-serializable list."""
    result = []
    for el in elements:
        etype = str(el.category) if hasattr(el, 'category') else type(el).__name__
        item = {
            "type": etype,
            "text": str(el.text).strip() if el.text else "",
        }
        # Include metadata if available
        meta = {}
        if hasattr(el, 'metadata') and el.metadata:
            m = el.metadata
            if hasattr(m, 'page_number') and m.page_number:
                meta["page"] = m.page_number
            if hasattr(m, 'filename') and m.filename:
                meta["file"] = m.filename
        if meta:
            item["metadata"] = meta
        result.append(item)
    return result


def main():
    if len(sys.argv) < 2:
        print("Usage: python unstructured_extract.py <file> [--json] [--text-only]", file=sys.stderr)
        sys.exit(1)

    file_path = sys.argv[1]
    if not os.path.isfile(file_path):
        print(f"FILE_NOT_FOUND: {file_path}", file=sys.stderr)
        sys.exit(3)

    mode = "markdown"
    if "--json" in sys.argv:
        mode = "json"
    elif "--text-only" in sys.argv:
        mode = "text"

    try:
        elements = extract_elements(file_path)
        if not elements:
            print("", file=sys.stderr)
            sys.exit(3)

        if mode == "json":
            print(json.dumps(elements_to_json(elements), ensure_ascii=False, indent=2))
        elif mode == "text":
            # Plain text concatenation
            for el in elements:
                if el.text and str(el.category) != "PageBreak":
                    print(str(el.text).strip())
        else:
            print(elements_to_markdown(elements))

    except Exception as e:
        print(f"UNSTRUCTURED_ERROR: {e}", file=sys.stderr)
        sys.exit(3)


if __name__ == "__main__":
    main()
