---
name: documents
description: Documents skill — convert-documents, document-extraction, markdown-converter, nano-pdf, word-docx, pdf-tools, docx-tools.
triggers: ['convert-documents', 'document-extraction', 'markdown-converter', 'nano-pdf', 'word-docx', 'pdf-tools', 'docx-tools']
version: 1.1.0
author: OpenKitty
status: stable
---

# Documents Skill

## IRON RULES (non-negotiable)

1. **Never delete source files until output is verified**: Document conversion and extraction tools can fail silently — producing empty files, garbled text, or missing pages. Always keep the original file and verify the output exists, has non-zero size, and is readable before considering deletion.
2. **Format fidelity is best-effort, not guaranteed**: Conversion between formats (especially involving PDF as source or target) will lose formatting. Tables may break, images may disappear, layout will shift. Always warn the user and recommend manual review for critical documents.
3. **OCR quality depends on input quality**: Image-based extraction (OCR via tesseract) produces poor results on low-resolution images, handwriting, complex layouts, or non-English text. Set user expectations accordingly — do not promise accurate OCR on poor inputs.
4. **Security: sanitize extracted paths**: When extracting ZIP archives or processing documents from untrusted sources, check for path traversal attacks (e.g., `../../etc/passwd`) before writing any file to disk.
5. **Check file accessibility before processing**: Password-protected PDFs, corrupted files, or permission-denied files will fail silently with unclear errors. Verify the file is readable and not encrypted before attempting extraction.

## Sub-Skills

| Sub-Skill | Use For |
|-----------|---------|
| **convert-documents** | Convert between markdown/PDF/DOCX/PPTX/HTML |
| **document-extraction** | Auto-detect and extract text from PDF/DOCX/PPTX/XLSX/images/audio |
| **markdown-converter** | `uvx markitdown` — universal file to Markdown |
| **nano-pdf** | Natural-language PDF page editing |
| **word-docx** | Word document (.docx) creation and editing |
| **pdf-tools** | PDF read/merge/split/OCR/watermark/form-fill — full PDF toolkit |
| **docx-tools** | Word reading, creation, template filling and review using macs tools |

### convert-documents
# Document Converter

Convert markdown content between formats using the built-in `convert` tool.

## Usage

```
convert(input="storage/report.md", output="storage/report.pdf")
convert(input="storage/report.md", output="storage/report.docx")
convert(input="storage/report.md", output="storage/report.pptx")
convert(input="storage/report.md", output="storage/report.html")
```

Parameters:
- `input` — path to the markdown file to convert
- `output` — desired output path (extension determines format:
... (full details in sub-directory)

### document-extraction
# Document Extraction

## Step 1: Get tool parameters

```
tool_detail("extract")
```

## Step 2: Use the tool

- `extract(file_path)` — automatically detects file type and extracts text content. Supports: PDF, DOCX, PPTX, XLSX, images (OCR via tesseract), audio (transcription via whisper), HTML, CSV, JSON, XML, ZIP.

### markdown-converter
# Markdown Converter

Convert files to Markdown using `uvx markitdown` — no installation required.

##installation
```bash
pip install markitdown[all]
```

## Basic Usage

```bash
# Convert to stdout
uvx markitdown input.pdf

# Save to file
uvx markitdown input.pdf -o output.md
uvx markitdown input.docx > output.md

# From stdin
cat input.pdf | uvx markitdown
```

## Supported Formats

- **Documents**: PDF, Word (.docx), PowerPoint (.pptx), Excel (.xlsx, .xls)
- **Web/Data**: HTML, CSV, JSON, XM
... (full details in sub-directory)

### nano-pdf
# nano-pdf

Use `nano-pdf` to apply edits to a specific page in a PDF using a natural-language instruction.

## Quick start

```bash
nano-pdf edit deck.pdf 1 "Change the title to 'Q3 Results' and fix the typo in the subtitle"
```

Notes:
- Page numbers are 0-based or 1-based depending on the tool’s version/config; if the result looks off by one, retry with the other.
- Always sanity-check the output PDF before sending it out.

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Converting a file and deleting the original before verifying output | Irreversible data loss if conversion silently produced an empty or corrupt file | Always verify the output file exists, has non-zero size, and content looks correct before removing the original |
| 2 | Converting very large files (>50MB) without warning | Memory exhaustion, timeout, or silently truncated output | Check file size first; warn the user and suggest splitting or using a more appropriate tool |
| 3 | Assuming PDF-to-Markdown conversion is perfect | Tables become garbled text, images are lost, multi-column layouts merge into nonsense | Always review critical conversions manually; use the result as a draft, not the final product |
| 4 | Running extraction on password-protected or encrypted files without checking first | Silent failure returns empty or error text with no clear indication of the cause | Check file accessibility before extraction; if password-protected, ask the user for the password |
| 5 | Using `nano-pdf` without sanity-checking the output PDF | Edits may apply to the wrong page (0-based vs 1-based confusion) or produce unexpected layout shifts | Open and visually inspect the output PDF before sending or publishing |

## Output
All output: `.openkitty/storage/`

## Pre-Action Checklist

- [ ] Understand the user's document task — conversion, extraction, or editing?
- [ ] Check the source file exists, is readable, and is not password-protected
- [ ] Verify the target format is compatible with the source format
- [ ] For large files (>50MB): warn the user about potential performance issues
- [ ] After conversion/extraction: verify the output file exists and has meaningful content
- [ ] For nano-pdf: confirm page numbers (0-based vs 1-based) and visually inspect the output
- [ ] Keep the original file until the user confirms the output is satisfactory
