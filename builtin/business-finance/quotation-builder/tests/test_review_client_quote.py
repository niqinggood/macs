from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "review_client_quote.py"
SPEC = importlib.util.spec_from_file_location("client_quote_reviewer", SCRIPT)
if SPEC is None or SPEC.loader is None:  # pragma: no cover - import guard
    raise RuntimeError(f"cannot load {SCRIPT}")
REVIEWER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = REVIEWER
SPEC.loader.exec_module(REVIEWER)


class ClientQuoteReviewerTests(unittest.TestCase):
    def scan(self, text: str):
        return REVIEWER.scan_document(REVIEWER.ExtractedDocument(text, 20, "md"))

    def test_safe_business_quote_passes(self) -> None:
        text = """
        OPC 开发平台商务报价书（客户版）
        项目预算：550,000 元，税费口径签约前确认。
        工期：60 个自然日，自双方启动条件满足次日起算。
        交付成果：能力市场、服务交易、智能路由和双场景 MVP。
        验收：主业务闭环演示通过，测试、部署和操作文档完成移交。
        详细技术方案属于签约后的方案定义阶段交付物。
        """
        self.assertEqual(self.scan(text), [])

    def test_internal_cost_and_api_path_are_blocked(self) -> None:
        text = "内部核算：后端 158.25 人天，毛利率 20%。POST /api/v1/orders"
        rules = {finding.rule_id for finding in self.scan(text)}
        self.assertIn("INTERNAL-LABEL", rules)
        self.assertIn("INTERNAL-ECONOMICS", rules)
        self.assertIn("ROLE-RATE", rules)
        self.assertIn("API-PATH", rules)

    def test_conflicting_price_and_schedule_are_blocked(self) -> None:
        text = "项目总价 55 万元，另一页含税总价 575,319.24 元。工期 60天，也可按12周执行。"
        rules = {finding.rule_id for finding in self.scan(text)}
        self.assertIn("PRICE-CONFLICT", rules)
        self.assertIn("SCHEDULE-CONFLICT", rules)

    def test_docx_xml_is_scanned_without_office_dependency(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "unsafe.docx"
            xml = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
            <w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
              <w:body><w:p><w:r><w:t>仅供内部：GET /api/v1/secret</w:t></w:r></w:p></w:body>
            </w:document>"""
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("word/document.xml", xml)
            document = REVIEWER.extract_document(path)
            rules = {finding.rule_id for finding in REVIEWER.scan_document(document)}
        self.assertIn("INTERNAL-LABEL", rules)
        self.assertIn("API-PATH", rules)

    def test_strict_mode_fails_on_warning(self) -> None:
        finding = REVIEWER.Finding("TEST", "WARN", "warning", "evidence")
        self.assertEqual(REVIEWER.exit_code([finding], strict=False), 1)
        self.assertEqual(REVIEWER.exit_code([finding], strict=True), 2)


if __name__ == "__main__":
    unittest.main()
