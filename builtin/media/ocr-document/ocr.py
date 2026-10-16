#!/usr/bin/env python3
"""
OCR Document Extractor — auto-detects engine, extracts structured data.
Usage: python ocr.py <file> [--type invoice|contract|receipt|id|general] [--lang ch|en]
"""
import sys, os, json, re, argparse

def detect_engine():
    """Auto-detect available OCR engine."""
    engines = []
    try:
        import paddleocr
        engines.append("paddleocr")
    except ImportError:
        pass
    try:
        import easyocr
        engines.append("easyocr")
    except ImportError:
        pass
    try:
        import pytesseract
        engines.append("tesseract")
    except ImportError:
        pass
    return engines[0] if engines else None

def ocr_paddleocr(file_path, lang="ch"):
    from paddleocr import PaddleOCR
    ocr = PaddleOCR(use_angle_cls=True, lang=lang)
    result = ocr.ocr(file_path, cls=True)
    lines = []
    if result and result[0]:
        for line in result[0]:
            text = line[1][0]
            conf = line[1][1]
            box = line[0]
            lines.append({"text": text, "confidence": round(conf, 2), "box": box})
    return lines

def ocr_easyocr(file_path, lang="ch"):
    import easyocr
    langs = ["ch_sim", "en"] if lang == "ch" else ["en"]
    reader = easyocr.Reader(langs)
    result = reader.readtext(file_path)
    lines = []
    for box, text, conf in result:
        lines.append({"text": text, "confidence": round(conf, 2), "box": box})
    return lines

def ocr_tesseract(file_path, lang="chi_sim+eng"):
    import pytesseract
    from PIL import Image
    lang = "chi_sim+eng" if lang == "ch" else "eng"
    img = Image.open(file_path)
    text = pytesseract.image_to_string(img, lang=lang)
    # Tesseract returns plain text, split into lines
    lines = []
    for t in text.strip().split("\n"):
        t = t.strip()
        if t:
            lines.append({"text": t, "confidence": 1.0})
    return lines

def extract_invoice_fields(lines):
    """Extract structured fields from invoice OCR results."""
    full_text = " ".join(l["text"] for l in lines)
    fields = {"type": "invoice", "raw_lines": [l["text"] for l in lines]}

    patterns = {
        "invoice_no": r"发票号码[：:]?\s*(\w+)",
        "invoice_code": r"发票代码[：:]?\s*(\d+)",
        "date": r"开票日期[：:]?\s*(\d{4}[-年]\d{1,2}[-月]\d{1,2}[日]?)",
        "seller": r"销售方名称[：:]?\s*(.+?)(?:\s|$)",
        "buyer": r"购买方名称[：:]?\s*(.+?)(?:\s|$)",
        "total_amount": r"价税合计[：:]?\s*[¥￥]?\s*([\d,.]+)",
        "tax_amount": r"税额[：:]?\s*[¥￥]?\s*([\d,.]+)",
        "amount": r"金额[：:]?\s*[¥￥]?\s*([\d,.]+)",
        "seller_tax_id": r"纳税人识别号[：:]?\s*(\w{15,20})",
    }
    for key, pattern in patterns.items():
        m = re.search(pattern, full_text)
        if m:
            fields[key] = m.group(1).strip()

    # Extract line items
    items = []
    item_pattern = re.compile(r"[\*]?(\S+)\s+(\d+)\s+([\d,.]+)\s+([\d,.]+)")
    for line_data in lines:
        m = item_pattern.search(line_data["text"])
        if m:
            items.append({"name": m.group(1), "qty": m.group(2), "price": m.group(3), "amount": m.group(4)})
    if items:
        fields["items"] = items

    return fields

def extract_contract_fields(lines):
    """Extract structured fields from contract OCR results."""
    full_text = " ".join(l["text"] for l in lines)
    fields = {"type": "contract", "raw_lines": [l["text"] for l in lines]}

    patterns = {
        "party_a": r"甲方[：:]?\s*(.+?)(?:\s|$|乙方)",
        "party_b": r"乙方[：:]?\s*(.+?)(?:\s|$|甲方)",
        "contract_date": r"签订日期[：:]?\s*(\d{4}[-年]\d{1,2}[-月]\d{1,2}[日]?)",
        "amount": r"合同金额[：:]?\s*[¥￥]?\s*([\d,.]+)",
        "governing_law": r"管辖[：:]?\s*(.+?)(?:\s|$)",
        "effective_date": r"生效日期[：:]?\s*(\d{4}[-年]\d{1,2}[-月]\d{1,2}[日]?)",
    }
    for key, pattern in patterns.items():
        m = re.search(pattern, full_text)
        if m:
            fields[key] = m.group(1).strip()

    # Detect risk keywords
    risk_keywords = ["无限责任", "单方解约", "自动续约", "违约金", "保密", "竞业禁止", "知识产权"]
    risks = [kw for kw in risk_keywords if kw in full_text]
    if risks:
        fields["risk_keywords_found"] = risks

    return fields

def extract_general(lines):
    """Return all text with position info."""
    return {"type": "general", "lines": [{"text": l["text"], "confidence": l["confidence"]} for l in lines]}

EXTRACTORS = {
    "invoice": extract_invoice_fields,
    "contract": extract_contract_fields,
    "receipt": extract_invoice_fields,  # similar structure
    "id": extract_general,
    "general": extract_general,
}

if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("file", help="Image or PDF path")
    p.add_argument("--type", default="general", choices=["invoice","contract","receipt","id","general"])
    p.add_argument("--lang", default="ch")
    args = p.parse_args()

    if not os.path.exists(args.file):
        print(json.dumps({"error": f"File not found: {args.file}"}, ensure_ascii=False))
        sys.exit(1)

    engine = detect_engine()
    if not engine:
        print(json.dumps({"error": "No OCR engine installed. Run: pip install paddleocr easyocr or apt install tesseract-ocr"}, ensure_ascii=False))
        sys.exit(1)

    print(f"# OCR Engine: {engine}", file=sys.stderr)

    # Perform OCR
    ocr_funcs = {"paddleocr": ocr_paddleocr, "easyocr": ocr_easyocr, "tesseract": ocr_tesseract}
    lines = ocr_funcs[engine](args.file, args.lang)

    if not lines:
        print(json.dumps({"error": "No text detected in image"}, ensure_ascii=False))
        sys.exit(0)

    # Extract structured fields
    extractor = EXTRACTORS.get(args.type, extract_general)
    result = extractor(lines)
    result["engine"] = engine
    result["file"] = args.file

    print(json.dumps(result, ensure_ascii=False, indent=2))
