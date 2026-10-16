---
name: ocr-document
description: Extract text and structured data from images, PDFs, and scanned documents. Auto-detects installed OCR engine. Use when user uploads invoices, contracts, receipts, ID cards, or any printed/scanned material.
read_when:
  - User uploads an image or scanned PDF
  - Invoice, receipt, or contract processing
  - Extracting text from screenshots
  - ID document or form data extraction
  - Bill/receipt reimbursement
---

# OCR Document Extraction

## Usage

```bash
python skills/ocr-document/ocr.py <file_path> [--type invoice|contract|receipt|id|general] [--lang ch|en]
```

The script auto-detects the best available OCR engine (PaddleOCR > EasyOCR > Tesseract).

## Document Types

| Type | Extracts |
|------|----------|
| `invoice` | Invoice number, date, seller, buyer, items, amounts, tax, total |
| `contract` | Parties, date, key clauses, amounts, signatures |
| `receipt` | Merchant, date, items, total, payment method |
| `id` | Name, ID number, address, expiry |
| `general` | All visible text |

## Installation (on server)

```bash
# Best quality (Chinese)
pip install paddlepaddle paddleocr

# Good quality, smaller
pip install easyocr

# Lightweight, no GPU needed
apt install tesseract-ocr tesseract-ocr-chi-sim
pip install pytesseract
```

## Integration

This skill is used by these Pipelines:
- `invoice-reimbursement` — step "OCR识别"
- `contract-review` — step "结构扫描"
- `vendor-qualification` — step "资质收集"

When the user uploads a document image, use this skill to extract text before analysis.
