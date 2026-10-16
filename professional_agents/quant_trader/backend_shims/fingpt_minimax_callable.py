"""Reviewed FinGPT sentiment callable backed by the upstream FinoGrid provider.

This module is intentionally small: the sentiment prompt, response mapping,
and provider implementation come from the pinned FinGPT repository.  The
wrapper only adapts OpenKitty's safe research envelope to that callable and
turns upstream transport errors into an honest backend failure.
"""

from __future__ import annotations

import os
import re
from typing import Any, Dict, List, Mapping


DATE_PATTERN = re.compile(
    r"(?<!\d)(?:20\d{2}[年./-](?:0?[1-9]|1[0-2])[月./-](?:0?[1-9]|[12]\d|3[01])日?|"
    r"20\d{2}年(?:0?[1-9]|1[0-2])月)(?!\d)"
)
CODE_PATTERN = re.compile(r"(?<!\d)\d{6}(?:\.(?:SS|SZ))?(?!\d)", re.IGNORECASE)
ORG_PATTERN = re.compile(
    r"[\u4e00-\u9fffA-Za-z0-9]{2,30}(?:股份有限公司|有限责任公司|有限公司|集团|银行|证券|公司)"
)
SENTENCE_SPLIT = re.compile(r"(?<=[。！？!?；;])\s*|[\r\n]+")
EVENT_TERMS = (
    "公告",
    "业绩",
    "营收",
    "利润",
    "增长",
    "下降",
    "亏损",
    "中标",
    "签署",
    "合同",
    "收购",
    "重组",
    "增持",
    "减持",
    "回购",
    "分红",
    "诉讼",
    "处罚",
    "立案",
    "风险",
)
SENTIMENT_ZH = {
    "positive": "正面",
    "negative": "负面",
    "neutral": "中性",
}


def _unique(values: List[str], limit: int) -> List[str]:
    seen = set()
    output: List[str] = []
    for value in values:
        value = " ".join(str(value).split()).strip()
        if not value or value in seen:
            continue
        seen.add(value)
        output.append(value)
        if len(output) >= limit:
            break
    return output


def _extract_financial_evidence(text: str, symbols: Any) -> Dict[str, Any]:
    sentences = _unique(
        [item.strip()[:280] for item in SENTENCE_SPLIT.split(text) if item.strip()],
        80,
    )
    events = [item for item in sentences if any(term in item for term in EVENT_TERMS)]
    if not events:
        events = sentences[:3]
    symbol_values = symbols if isinstance(symbols, list) else [symbols]
    entities = [str(item).strip() for item in symbol_values if str(item).strip()]
    entities.extend(match.group(0) for match in ORG_PATTERN.finditer(text))
    entities.extend(match.group(0).upper() for match in CODE_PATTERN.finditer(text))
    return {
        "event_evidence": _unique(events, 6),
        "entities": _unique(entities, 12),
        "dates": _unique([match.group(0) for match in DATE_PATTERN.finditer(text)], 12),
    }


def _render_financial_intelligence(
    text: str, label: str, score: Any, evidence: Mapping[str, Any]
) -> str:
    sentiment = SENTIMENT_ZH.get(label.lower(), label or "未知")
    lines = [
        "# AI金信分析师",
        "",
        "- 材料长度：%d 字符" % len(text),
        "- FinGPT 情绪：**%s**（score=%s）" % (sentiment, score),
        "- 分析边界：只分析调用者提供的文本，不抓行情、不预测价格、不下单。",
        "",
        "## 事件/材料陈述及原文证据",
        "",
    ]
    events = evidence.get("event_evidence", [])
    if events:
        lines.extend("%d. %s" % (index + 1, item) for index, item in enumerate(events))
    else:
        lines.append("- 未提取到可展示的事件陈述。")
    lines.extend(["", "## 识别到的实体/证券标识", ""])
    entities = evidence.get("entities", [])
    lines.append("、".join(entities) if entities else "未识别")
    lines.extend(["", "## 识别到的日期", ""])
    dates = evidence.get("dates", [])
    lines.append("、".join(dates) if dates else "材料中未识别到明确日期")
    lines.extend(
        [
            "",
            "## 使用提醒",
            "",
            "以上“事件/材料陈述”是原文摘录，不代表已由外部来源核实；情绪标签也不等于利好/利空已兑现，更不能单独作为买卖依据。",
        ]
    )
    return "\n".join(lines)


async def analyze(payload: Mapping[str, Any]) -> Dict[str, Any]:
    input_data = payload.get("input", {})
    if not isinstance(input_data, Mapping):
        raise ValueError("FinGPT callable input must be an object")

    text = input_data.get("text") or input_data.get("news") or input_data.get("question")
    if not isinstance(text, str) or not text.strip():
        raise ValueError("FinGPT sentiment requires input.text, input.news, or input.question")
    text = text.strip()

    # The reviewed upstream provider defaults to the international MiniMax
    # endpoint.  FinGPT also runs with China-region accounts; select the
    # endpoint through an explicitly allowlisted variable without changing the
    # upstream repository or inheriting host-wide environment state.
    from finogrid.fingpt_integration.sentiment import minimax_provider

    configured_base_url = os.environ.get("MINIMAX_BASE_URL", "").strip()
    if configured_base_url:
        minimax_provider.MINIMAX_BASE_URL = configured_base_url
    model = os.environ.get("MINIMAX_MODEL", "MiniMax-M3").strip() or "MiniMax-M3"
    provider = minimax_provider.MiniMaxSentimentProvider(model=model)
    result = await provider.score(text)
    if not isinstance(result, Mapping):
        raise RuntimeError("FinGPT sentiment provider returned a non-object result")
    if result.get("error"):
        return {
            "status": "error",
            "output": "",
            "data": {
                "provider": "fingpt",
                "upstream_api": (
                    "finogrid.fingpt_integration.sentiment."
                    "minimax_provider.MiniMaxSentimentProvider.score"
                ),
                "model": model,
            },
            "warnings": ["FinGPT sentiment provider failed: %s" % result["error"]],
            "artifacts": [],
        }

    label = str(result.get("label") or "neutral")
    score = result.get("score", 0)
    symbols = input_data.get("symbols", input_data.get("tickers", []))
    evidence = _extract_financial_evidence(text, symbols)
    return {
        "status": "success",
        "output": _render_financial_intelligence(text, label, score, evidence),
        "data": {
            "provider": "fingpt",
            "upstream_api": (
                "finogrid.fingpt_integration.sentiment."
                "minimax_provider.MiniMaxSentimentProvider.score"
            ),
            "model": model,
            "symbols": symbols,
            "sentiment": {"label": label, "score": score},
            "evidence": evidence,
            "input_characters": len(text),
        },
        "warnings": [
            "FinGPT supplies the financial sentiment label. Event, entity, and date fields "
            "are deterministic excerpts from caller-supplied text and are not externally verified; "
            "this integration does not forecast prices or submit orders."
        ],
        "artifacts": [],
    }
