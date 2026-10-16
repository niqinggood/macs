#!/usr/bin/env python3
"""Scan a customer-facing quotation for common internal/solution leakage.

The scanner is deliberately conservative. It supports plain text, Markdown,
HTML, DOCX, XLSX, PPTX and PDF (when pypdf is installed) without modifying the
source file. It is a sending gate, not a substitute for human review.
"""

from __future__ import annotations

import argparse
import html
import json
import re
import sys
import zipfile
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Iterable, Sequence
from xml.etree import ElementTree as ET


TEXT_EXTENSIONS = {".md", ".txt", ".html", ".htm", ".csv", ".tsv", ".json", ".yaml", ".yml"}
OFFICE_EXTENSIONS = {".docx", ".xlsx", ".pptx"}


@dataclass(frozen=True)
class Finding:
    rule_id: str
    severity: str
    message: str
    evidence: str
    count: int = 1


@dataclass(frozen=True)
class ExtractedDocument:
    text: str
    detail_units: int
    source_type: str


@dataclass(frozen=True)
class PatternRule:
    rule_id: str
    severity: str
    message: str
    pattern: re.Pattern[str]


PATTERN_RULES = (
    PatternRule(
        "INTERNAL-LABEL",
        "BLOCK",
        "文件含内部/禁止外发标识，不应作为客户附件。",
        re.compile(r"内部资料|仅供内部|内部[-—]?禁止外发|禁止外发|内部核算|内部审批版", re.I),
    ),
    PatternRule(
        "INTERNAL-ECONOMICS",
        "BLOCK",
        "暴露了内部成本、利润或谈判底线。",
        re.compile(r"内部成本|成本价卡|角色日成本|毛利(?:率)?|实际毛利|红线价(?:格)?|谈判底线|折扣授权|供应商底价|交付总成本|P80|P95|O\s*/\s*M\s*/\s*P", re.I),
    ),
    PatternRule(
        "ROLE-RATE",
        "BLOCK",
        "暴露了逐角色人日或日单价。",
        re.compile(r"\d+(?:\.\d+)?\s*人(?:日|天)|元\s*/\s*人(?:日|天)|(?:FE|BE|QA|ALG|OPS).{0,30}(?:人天|单价)", re.I),
    ),
    PatternRule(
        "API-PATH",
        "BLOCK",
        "包含可直接实施的 API 方法或路径。",
        re.compile(r"(?:GET|POST|PUT|PATCH|DELETE)\s+/[A-Za-z0-9_{}./:-]+|/api/v\d+(?:/|\b)", re.I),
    ),
    PatternRule(
        "DATA-SCHEMA",
        "BLOCK",
        "包含表结构、字段或接口 Schema 级细节。",
        re.compile(r"CREATE\s+TABLE|ALTER\s+TABLE|JSON\s+Schema|tenant_id|price_plan|rate_card|Idempotency-Key|字段字典|核心数据模型|ER\s*图", re.I),
    ),
    PatternRule(
        "IMPLEMENTATION-IP",
        "BLOCK",
        "包含算法、Prompt、编排或基础设施实现细节。",
        re.compile(r"系统提示词|\bPrompt\b|超参|模型选型|路由权重|DAG|节点调度|Redis\s+Sorted\s+Set|缓存\s*Key|消息\s*Topic|Dockerfile|K8s|Kubernetes|Helm\s*配置", re.I),
    ),
    PatternRule(
        "TASK-BLUEPRINT",
        "BLOCK",
        "包含内部任务编号或可直接照做的逐任务蓝图。",
        re.compile(r"(?:^|[\s|])M\d+-[\w/-]+|【[^】]{0,80}(?:页面|后端|接口定义|算法)[^】]{0,20}】|产出[:：].{0,100}(?:接口|页面|模块)", re.I | re.M),
    ),
    PatternRule(
        "SOURCE-STRUCTURE",
        "WARN",
        "提到源码/目录/SDK 等实现资产，请确认是合同交付边界而非免费方案。",
        re.compile(r"源码结构|代码目录|类图|SDK\s*结构|模块\s*\d+\s*个接口|\d+\s*个接口", re.I),
    ),
)


STACK_TERMS = (
    "MySQL",
    "PostgreSQL",
    "Redis",
    "MinIO",
    "Kafka",
    "RabbitMQ",
    "Docker",
    "Kubernetes",
    "K8s",
    "Helm",
    "Nginx",
    "Elasticsearch",
    "Prometheus",
    "Grafana",
)


def _local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def _decode_text(path: Path) -> str:
    raw = path.read_bytes()
    for encoding in ("utf-8-sig", "utf-8", "gb18030"):
        try:
            return raw.decode(encoding)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")


def _xml_text(raw: bytes) -> tuple[list[str], int]:
    root = ET.fromstring(raw)
    parts: list[str] = []
    count = 0
    for node in root.iter():
        name = _local_name(node.tag)
        if name in {"t", "v", "f"} and node.text:
            value = html.unescape(node.text).strip()
            if value:
                parts.append(value)
                count += 1
        elif name in {"p", "tr", "row", "c"} and parts and parts[-1] != "\n":
            parts.append("\n")
    return parts, count


def _extract_office(path: Path) -> ExtractedDocument:
    extension = path.suffix.lower()
    prefixes = {
        ".docx": ("word/document.xml", "word/header", "word/footer", "word/comments"),
        ".xlsx": ("xl/sharedStrings.xml", "xl/worksheets/", "xl/workbook.xml"),
        ".pptx": ("ppt/slides/", "ppt/notesSlides/"),
    }[extension]
    parts: list[str] = []
    detail_units = 0
    with zipfile.ZipFile(path) as archive:
        names = sorted(
            name
            for name in archive.namelist()
            if name.endswith(".xml") and any(name == prefix or name.startswith(prefix) for prefix in prefixes)
        )
        for name in names:
            try:
                extracted, count = _xml_text(archive.read(name))
            except ET.ParseError:
                continue
            parts.extend(extracted)
            parts.append("\n")
            detail_units += count
    return ExtractedDocument(" ".join(parts), detail_units, extension.lstrip("."))


def _extract_pdf(path: Path) -> ExtractedDocument:
    try:
        from pypdf import PdfReader
    except ImportError as error:  # pragma: no cover - dependency differs by host
        raise RuntimeError("PDF 扫描需要 pypdf；当前环境未安装，禁止把未扫描 PDF 直接外发。") from error
    reader = PdfReader(path)
    pages = [(page.extract_text() or "") for page in reader.pages]
    return ExtractedDocument("\n".join(pages), len(pages), "pdf")


def extract_document(path: Path) -> ExtractedDocument:
    extension = path.suffix.lower()
    if extension in TEXT_EXTENSIONS:
        text = _decode_text(path)
        if extension in {".html", ".htm"}:
            text = re.sub(r"<[^>]+>", " ", text)
        detail_units = sum(1 for line in text.splitlines() if line.strip())
        return ExtractedDocument(text, detail_units, extension.lstrip("."))
    if extension in OFFICE_EXTENSIONS:
        return _extract_office(path)
    if extension == ".pdf":
        return _extract_pdf(path)
    raise ValueError(f"不支持的文件类型：{extension or '[无扩展名]'}")


def _snippet(text: str, match: re.Match[str], radius: int = 50) -> str:
    start = max(0, match.start() - radius)
    end = min(len(text), match.end() + radius)
    return re.sub(r"\s+", " ", text[start:end]).strip()


def _normalise_money(number: str, unit: str) -> int:
    value = float(number.replace(",", ""))
    if unit == "万":
        value *= 10000
    return round(value)


def scan_document(document: ExtractedDocument) -> list[Finding]:
    text = document.text
    findings: list[Finding] = []
    for rule in PATTERN_RULES:
        matches = list(rule.pattern.finditer(text))
        if matches:
            findings.append(
                Finding(rule.rule_id, rule.severity, rule.message, _snippet(text, matches[0]), len(matches))
            )

    stack_hits = [term for term in STACK_TERMS if re.search(rf"\b{re.escape(term)}\b", text, re.I)]
    if len(stack_hits) >= 4:
        findings.append(
            Finding(
                "STACK-DENSITY",
                "WARN",
                "技术栈名词过密，可能泄露实现选型；客户版通常只需说明兼容或部署边界。",
                ", ".join(stack_hits[:10]),
                len(stack_hits),
            )
        )

    if document.detail_units > 500:
        findings.append(
            Finding(
                "EXCESSIVE-DETAIL",
                "BLOCK",
                "内容颗粒度远超客户商务报价，疑似混入任务清单或技术详设。",
                f"检测到 {document.detail_units} 个文本/表格单元",
                document.detail_units,
            )
        )
    elif document.detail_units > 220:
        findings.append(
            Finding(
                "EXCESSIVE-DETAIL",
                "WARN",
                "客户报价内容较长，请确认每一项都用于采购决策。",
                f"检测到 {document.detail_units} 个文本/表格单元",
                document.detail_units,
            )
        )

    total_pattern = re.compile(
        r"(?:项目总价|含税总价|总预算|基准预算|商务报价|成交价)\D{0,18}(\d[\d,]*(?:\.\d+)?)\s*(万|元)",
        re.I,
    )
    totals = {
        _normalise_money(match.group(1), match.group(2))
        for match in total_pattern.finditer(text)
    }
    if len(totals) > 1:
        findings.append(
            Finding(
                "PRICE-CONFLICT",
                "BLOCK",
                "同一客户文件出现多个不一致的权威总价。",
                "、".join(f"{value:,}元" for value in sorted(totals)),
                len(totals),
            )
        )

    duration_forms = []
    for label, pattern in (
        ("60天", r"60\s*(?:个)?(?:自然日|工作日|天)"),
        ("12周", r"12\s*(?:个)?周"),
        ("3个月", r"3\s*(?:个)?月"),
    ):
        if re.search(pattern, text):
            duration_forms.append(label)
    if "60天" in duration_forms and ("12周" in duration_forms or "3个月" in duration_forms):
        findings.append(
            Finding(
                "SCHEDULE-CONFLICT",
                "BLOCK",
                "同时出现 60 天与 12 周/3 个月口径，需统一并说明自然日或工作日。",
                "、".join(duration_forms),
                len(duration_forms),
            )
        )
    return findings


def _render_markdown(path: Path, document: ExtractedDocument, findings: Iterable[Finding]) -> str:
    rows = list(findings)
    blocked = sum(1 for finding in rows if finding.severity == "BLOCK")
    warned = sum(1 for finding in rows if finding.severity == "WARN")
    lines = [
        "# 客户报价发送前审查",
        "",
        f"- 文件：`{path}`",
        f"- 类型：`{document.source_type}`",
        f"- 结论：{'阻止发送' if blocked else ('需要人工确认' if warned else '自动检查通过')}",
        f"- 阻断项：{blocked}；警告项：{warned}",
    ]
    if not rows:
        lines.extend(["", "未发现预设泄露模式。仍需人工核对金额、税、工期、附件和收件人。"])
        return "\n".join(lines) + "\n"
    lines.extend(["", "| 级别 | 规则 | 问题 | 次数 | 证据片段 |", "|---|---|---|---:|---|"])
    for finding in rows:
        evidence = finding.evidence.replace("|", "\\|").replace("\n", " ")
        lines.append(
            f"| {finding.severity} | {finding.rule_id} | {finding.message} | {finding.count} | {evidence} |"
        )
    return "\n".join(lines) + "\n"


def exit_code(findings: Iterable[Finding], strict: bool) -> int:
    rows = list(findings)
    if any(finding.severity == "BLOCK" for finding in rows):
        return 2
    if any(finding.severity == "WARN" for finding in rows):
        return 2 if strict else 1
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="审查客户报价是否泄露内部经济数据或可执行技术方案。")
    parser.add_argument("path", type=Path, help="待外发的报价文件")
    parser.add_argument("--strict", action="store_true", help="警告也返回非零退出码")
    parser.add_argument("--format", choices=("markdown", "json"), default="markdown")
    args = parser.parse_args(argv)

    try:
        document = extract_document(args.path)
        findings = scan_document(document)
    except (OSError, ValueError, RuntimeError, zipfile.BadZipFile) as error:
        print(f"客户报价审查失败：{error}", file=sys.stderr)
        return 2

    if args.format == "json":
        payload = {
            "path": str(args.path),
            "source_type": document.source_type,
            "detail_units": document.detail_units,
            "findings": [asdict(finding) for finding in findings],
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    else:
        sys.stdout.write(_render_markdown(args.path, document, findings))
    return exit_code(findings, args.strict)


if __name__ == "__main__":
    raise SystemExit(main())
