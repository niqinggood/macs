---
name: convert-documents
description: Convert Markdown to PDF, DOCX, PPTX, or HTML. Use when user asks to convert a file to PDF/Word/PPT/HTML format.
read_when:
  - Convert markdown or MD file to PDF
  - Convert markdown to DOCX/Word
  - Convert markdown to PPTX/PowerPoint
  - Convert document format
  - Export or save as PDF
---

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
- `output` — desired output path (extension determines format: .pdf .docx .pptx .html)

Tool handles the conversion automatically. File is saved to the specified output path.
