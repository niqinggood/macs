---
name: document-extraction
description: "Extract text content from PDF, Word, PowerPoint, Excel, images (OCR), audio (speech-to-text), HTML, CSV, JSON, XML, ZIP files. Use when user needs to read non-markdown files or extract data from documents. Tool: extract. Always call tool_detail first."
read_when:
  - Extract text or data from PDF, Word, PowerPoint, Excel files / 提取PDF文字、读取Word、解析Excel
  - Extract text from images with OCR / 图片识别文字、OCR
  - Transcribe audio to text / 语音转文字
  - Parse file content or metadata / 解析文件内容
---

# Document Extraction

## Step 1: Get tool parameters

```
tool_detail("extract")
```

## Step 2: Use the tool

- `extract(file_path)` — automatically detects file type and extracts text content. Supports: PDF, DOCX, PPTX, XLSX, images (OCR via tesseract), audio (transcription via whisper), HTML, CSV, JSON, XML, ZIP.
