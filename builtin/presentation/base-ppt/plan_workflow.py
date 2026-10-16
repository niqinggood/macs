#!/usr/bin/env python3
import argparse
import json
import re
from pathlib import Path

DEFAULT_LAYOUTS = {
    "quickLookup": {
        "strategy": ["exhibit", "mckinsey"],
        "board": ["exhibit", "mckinsey"],
        "general_business": ["charcoal", "smart_red", "mckinsey"],
        "data_analysis": ["google_style", "mckinsey", "charcoal"],
        "technology": ["google_style", "anthropic", "charcoal", "ai_ops"],
        "government": ["government_blue", "government_red", "ai_ops"],
        "academic": ["academic_defense"],
        "medical": ["medical_university"],
        "finance": ["exhibit", "mckinsey"],
        "creative": ["smart_red", "pixel_retro"],
    },
    "layouts": {
        "exhibit": {
            "label": "Exhibit Style Template",
            "summary": "Conclusion-first layout with premium dark header and evidence-driven content.",
            "tone": "Premium, authoritative, data-driven, conclusion-first",
        },
        "mckinsey": {
            "label": "McKinsey Style Template",
            "summary": "Structured consulting layout with whitespace and crisp logic.",
            "tone": "Structured, professional, minimalist premium",
        },
        "google_style": {
            "label": "Google Style Template",
            "summary": "Airy white report layout with blue, red, yellow and green semantic accents.",
            "tone": "Modern, clear, data-led, optimistic and spacious",
        },
        "government_blue": {
            "label": "Government Blue Template",
            "summary": "Formal hierarchy with blue government-enterprise styling.",
            "tone": "Professional, formal, digital transformation",
        },
        "academic_defense": {
            "label": "Academic Defense Template",
            "summary": "Clear hierarchy for research and defense use cases.",
            "tone": "Rigorous, research-oriented, clear hierarchy",
        },
        "medical_university": {
            "label": "Medical University Template",
            "summary": "Clinical and research report layout with trustworthy tone.",
            "tone": "Medical, rigorous, trustworthy",
        },
        "smart_red": {
            "label": "Smart Red Business Template",
            "summary": "Action-oriented business layout with stronger visual energy.",
            "tone": "Modern, vibrant, commercial",
        },
        "charcoal": {
            "label": "Warm Neutral Template",
            "summary": "Warm paper, charcoal typography, and restrained terracotta/sage accents.",
            "tone": "Editorial, calm, evidence-led",
        },
        "anthropic": {
            "label": "Anthropic Style Template",
            "summary": "Warm editorial layout reserved for explicitly Anthropic or Claude-oriented work.",
            "tone": "Warm, thoughtful, editorial and AI-specific",
        },
        "ai_ops": {
            "label": "AI Ops Template",
            "summary": "High-density architecture and system overview template.",
            "tone": "Dense, modular, architecture-driven",
        },
    },
}

SUPPORTED_RENDER_THEMES = {
    "midnight", "forest", "coral", "charcoal", "teal", "berry", "ocean",
    "mckinsey", "google_style", "anthropic", "exhibit", "government_blue",
    "government_red", "academic_defense", "medical_university", "pixel_retro",
    "smart_red", "ai_ops",
}


FAMILY_KEYWORDS = {
    "finance": [
        "stock", "equity", "fund", "valuation", "invest", "investor", "market",
        "trading", "security", "portfolio", "revenue", "margin", "ebitda", "cash flow",
    ],
    "technology": [
        "ai", "agent", "llm", "model", "platform", "system", "architecture", "product",
        "roadmap", "engineering", "developer", "saas", "api", "cloud", "infra",
        "robot", "robotics", "hardware", "manufacturing", "component",
        "人工智能", "智能体", "大模型", "平台", "系统", "架构", "产品", "研发", "云计算",
        "机器人", "硬件", "制造", "零部件",
    ],
    "data_analysis": [
        "data analysis", "dataset", "eda", "analytics", "statistic", "distribution",
        "correlation", "regression", "sample", "variable", "outlier", "visualization",
        "数据分析", "数据集", "探索性分析", "统计", "分布", "相关性", "回归", "样本",
        "变量", "离群", "异常值", "可视化", "热力图", "散点", "中位数", "均值",
    ],
    "government": [
        "government", "policy", "governance", "regulation", "public", "city", "official",
        "compliance", "state-owned", "soe",
    ],
    "academic": [
        "thesis", "research", "paper", "experiment", "study", "defense", "academic",
        "论文", "研究", "实验", "课题", "答辩", "学术", "文献",
    ],
    "medical": [
        "medical", "hospital", "clinical", "patient", "therapy", "diagnosis", "case study",
        "医疗", "医院", "临床", "患者", "治疗", "诊断", "病例",
    ],
    "creative": [
        "brand", "campaign", "creative", "story", "editorial", "launch", "visual",
        "品牌", "营销活动", "创意", "故事", "发布会", "视觉",
    ],
    "general_business": [
        "sales", "lead", "growth", "customer", "pipeline", "marketing", "proposal",
        "business", "strategy", "plan", "roadshow", "board",
        "销售", "增长", "客户", "营销", "方案", "商业", "战略", "计划", "路演", "董事会",
    ],
}


STRONG_FAMILY_KEYWORDS = {
    "data_analysis": ["data analysis", "dataset", "eda", "regression", "correlation", "数据分析", "数据集", "回归", "相关性"],
    "technology": ["ai", "agent", "llm", "architecture", "robotics", "人工智能", "智能体", "大模型", "架构", "机器人"],
    "academic": ["thesis", "defense", "academic", "论文", "答辩", "学术"],
    "medical": ["clinical", "patient", "diagnosis", "临床", "患者", "诊断"],
    "finance": ["valuation", "portfolio", "ebitda", "估值", "投资组合"],
}


EXPLICIT_THEME_KEYWORDS = {
    "google_style": ["google_style", "google style", "google-style", "谷歌风格", "谷歌配色"],
    "anthropic": ["anthropic style", "anthropic-style", "claude style", "claude code style", "克劳德风格"],
    "mckinsey": ["mckinsey style", "麦肯锡风格", "openkitty_theme:mckinsey"],
    "exhibit": ["exhibit style", "深色展陈"],
    "academic_defense": ["academic defense", "学术答辩风格"],
}


PROFILE_TEXT = {
    "finance": {
        "eyebrow": "Investment readout",
        "subtitle": "Conclusion-first analysis with signals, evidence and actions.",
        "summary": "Executive Summary",
        "agenda": "Readout Structure",
        "actions": "Decision And Follow-up",
        "end_title": "Key Takeaways",
        "end_subtitle": "Validate assumptions, monitor triggers and update the call.",
        "tags": ["Conclusion", "Evidence", "Risk"],
        "footer": "Investment summary",
        "audience": "Investors, executives, decision makers",
        "narrative": "Conclusion -> supporting evidence -> implications -> risks -> action",
    },
    "technology": {
        "eyebrow": "Product and technology review",
        "subtitle": "Clean, high-whitespace deck with architecture and roadmap emphasis.",
        "summary": "What Matters Most",
        "agenda": "Storyline",
        "actions": "Build And Launch",
        "end_title": "Next Milestones",
        "end_subtitle": "Convert findings into roadmap, owners and checkpoints.",
        "tags": ["Product", "Architecture", "Roadmap"],
        "footer": "Technology review",
        "audience": "Product leaders, engineering, technical stakeholders",
        "narrative": "Problem -> architecture -> evidence -> roadmap -> next steps",
    },
    "data_analysis": {
        "eyebrow": "Data evidence review",
        "subtitle": "A visual evidence story from data quality to implications and action.",
        "summary": "Evidence At A Glance",
        "agenda": "Analysis Story",
        "actions": "Implications And Next Steps",
        "end_title": "What The Evidence Supports",
        "end_subtitle": "Use the findings, limits and next tests to guide the next decision.",
        "tags": ["Data", "Evidence", "Implication"],
        "footer": "Data evidence review",
        "audience": "Analysts, researchers and decision makers",
        "narrative": "Question -> data -> pattern -> model -> implication -> next step",
    },
    "government": {
        "eyebrow": "Project briefing",
        "subtitle": "Structured report with formal hierarchy and execution framing.",
        "summary": "Core Summary",
        "agenda": "Report Outline",
        "actions": "Execution Priorities",
        "end_title": "Report Complete",
        "end_subtitle": "Proceed with review, alignment and implementation planning.",
        "tags": ["Summary", "Execution", "Governance"],
        "footer": "Government-enterprise report",
        "audience": "Government or enterprise leadership",
        "narrative": "Situation -> policy context -> plan -> execution -> governance",
    },
    "academic": {
        "eyebrow": "Research presentation",
        "subtitle": "Clear research logic with methods, evidence and conclusion.",
        "summary": "Research Summary",
        "agenda": "Presentation Outline",
        "actions": "Questions And Next Work",
        "end_title": "Thank You",
        "end_subtitle": "Review the conclusions and discuss future work.",
        "tags": ["Question", "Method", "Conclusion"],
        "footer": "Research presentation",
        "audience": "Review committee, academic audience",
        "narrative": "Question -> method -> evidence -> conclusion -> future work",
    },
    "medical": {
        "eyebrow": "Clinical review",
        "subtitle": "Structured medical narrative with case signals and treatment logic.",
        "summary": "Clinical Summary",
        "agenda": "Case Structure",
        "actions": "Follow-up Actions",
        "end_title": "Thank You",
        "end_subtitle": "Review the case implications and clinical next steps.",
        "tags": ["Case", "Evidence", "Action"],
        "footer": "Medical review",
        "audience": "Clinical and research audience",
        "narrative": "Case -> findings -> interpretation -> action",
    },
    "creative": {
        "eyebrow": "Story-driven presentation",
        "subtitle": "Visual-first deck with stronger rhythm and sharper takeaways.",
        "summary": "Key Storyline",
        "agenda": "Narrative Arc",
        "actions": "Launch Actions",
        "end_title": "Next Move",
        "end_subtitle": "Turn the narrative into launch and communication actions.",
        "tags": ["Story", "Visual", "Action"],
        "footer": "Creative presentation",
        "audience": "Brand, marketing, creative stakeholders",
        "narrative": "Hook -> context -> idea -> proof -> action",
    },
    "general_business": {
        "eyebrow": "Structured business review",
        "subtitle": "Consulting-style deck with explicit structure, evidence and next steps.",
        "summary": "Executive Summary",
        "agenda": "Agenda",
        "actions": "Recommended Actions",
        "end_title": "Thank You",
        "end_subtitle": "Review the findings and move into the next decision cycle.",
        "tags": ["Insight", "Analysis", "Action"],
        "footer": "Business review",
        "audience": "Business stakeholders",
        "narrative": "Situation -> analysis -> evidence -> recommendation -> action",
    },
}


PROFILE_TEXT_ZH = {
    "finance": {
        "eyebrow": "投资分析",
        "subtitle": "以结论为先，用证据、风险与行动支撑判断。",
        "summary": "核心结论",
        "agenda": "分析主线",
        "actions": "决策与后续行动",
        "end_title": "结论与下一步",
        "end_subtitle": "验证关键假设，跟踪触发条件，持续更新判断。",
        "tags": ["结论", "证据", "风险"],
        "footer": "投资分析",
        "audience": "投资者、管理层与决策者",
        "narrative": "结论 -> 证据 -> 影响 -> 风险 -> 行动",
    },
    "technology": {
        "eyebrow": "产品与技术复盘",
        "subtitle": "以问题、证据和演进路径串联技术判断。",
        "summary": "关键判断",
        "agenda": "叙事主线",
        "actions": "建设与落地",
        "end_title": "下一阶段里程碑",
        "end_subtitle": "把发现转化为路径、责任人与检查点。",
        "tags": ["产品", "架构", "路径"],
        "footer": "产品与技术复盘",
        "audience": "产品、工程与技术负责人",
        "narrative": "问题 -> 机制 -> 证据 -> 路径 -> 下一步",
    },
    "data_analysis": {
        "eyebrow": "数据洞察",
        "subtitle": "从数据质量、模式与模型证据，推导可执行结论。",
        "summary": "核心发现",
        "agenda": "分析主线",
        "actions": "结论与下一步",
        "end_title": "证据支持的结论",
        "end_subtitle": "结合发现、边界与后续验证，推动下一步决策。",
        "tags": ["数据", "证据", "洞察"],
        "footer": "数据分析",
        "audience": "分析师、研究者与决策者",
        "narrative": "问题 -> 数据 -> 模式 -> 模型 -> 含义 -> 行动",
    },
    "government": {
        "eyebrow": "专题汇报",
        "subtitle": "以正式层级呈现现状、方案、执行与治理。",
        "summary": "核心摘要",
        "agenda": "汇报结构",
        "actions": "执行重点",
        "end_title": "结论与落实",
        "end_subtitle": "进入评审、协同与实施规划。",
        "tags": ["摘要", "执行", "治理"],
        "footer": "专题汇报",
        "audience": "政府或企业管理层",
        "narrative": "现状 -> 政策环境 -> 方案 -> 执行 -> 治理",
    },
    "academic": {
        "eyebrow": "研究汇报",
        "subtitle": "以问题、方法、证据和结论构成清晰研究逻辑。",
        "summary": "研究摘要",
        "agenda": "汇报结构",
        "actions": "讨论与后续研究",
        "end_title": "结论与讨论",
        "end_subtitle": "审视结论、局限与未来工作。",
        "tags": ["问题", "方法", "结论"],
        "footer": "研究汇报",
        "audience": "评审专家与学术听众",
        "narrative": "问题 -> 方法 -> 证据 -> 结论 -> 未来工作",
    },
    "medical": {
        "eyebrow": "临床研究复盘",
        "subtitle": "以病例、发现、解释和行动组织医学证据。",
        "summary": "临床摘要",
        "agenda": "病例结构",
        "actions": "随访行动",
        "end_title": "结论与随访",
        "end_subtitle": "审视临床含义与下一步安排。",
        "tags": ["病例", "证据", "行动"],
        "footer": "临床复盘",
        "audience": "临床与研究听众",
        "narrative": "病例 -> 发现 -> 解释 -> 行动",
    },
    "creative": {
        "eyebrow": "叙事提案",
        "subtitle": "以视觉节奏、关键主张和行动构建故事。",
        "summary": "核心故事",
        "agenda": "叙事弧线",
        "actions": "发布行动",
        "end_title": "下一步动作",
        "end_subtitle": "把叙事转化为发布与传播行动。",
        "tags": ["故事", "视觉", "行动"],
        "footer": "叙事提案",
        "audience": "品牌、市场与创意团队",
        "narrative": "引子 -> 背景 -> 创意 -> 证据 -> 行动",
    },
    "general_business": {
        "eyebrow": "经营复盘",
        "subtitle": "以明确结构、证据和行动支撑业务判断。",
        "summary": "执行摘要",
        "agenda": "议程",
        "actions": "建议行动",
        "end_title": "结论与行动",
        "end_subtitle": "确认判断并进入下一轮决策。",
        "tags": ["洞察", "分析", "行动"],
        "footer": "经营复盘",
        "audience": "业务相关方",
        "narrative": "现状 -> 分析 -> 证据 -> 建议 -> 行动",
    },
}


def load_layouts(skill_dir: Path) -> dict:
    candidates = [
        skill_dir.parent / "ppt-master" / "skills" / "ppt-master" / "templates" / "layouts" / "layouts_index.json",
        skill_dir.parents[2] / "runtime" / "builtin" / "presentation" / "ppt-master" / "skills" / "ppt-master" / "templates" / "layouts" / "layouts_index.json",
    ]
    for candidate in candidates:
        if candidate.exists():
            try:
                return json.loads(candidate.read_text(encoding="utf-8-sig"))
            except Exception:
                return DEFAULT_LAYOUTS
    return DEFAULT_LAYOUTS


def clean_line(text: str) -> str:
    text = text.strip().strip("`")
    text = text.replace("**", "").replace("__", "").replace("\t", " ")
    return " ".join(text.split())


IMAGE_PATTERN = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
TABLE_SEPARATOR_PATTERN = re.compile(r"^:?-{3,}:?$")
CHINESE_SECTION_PATTERN = re.compile(r"^([一二三四五六七八九十百]+)[、.．]\s*(.+)$")
PAGE_MARKER_PATTERN = re.compile(r"\s*[（(]\s*Page(?:\s*对应[^）)]*)?\s*\d*\s*[）)]\s*$", re.I)
LIST_PREFIX_PATTERN = re.compile(r"^\s*(?:[-*+•◦▪]\s*|\d+[.)、]\s*)")


def _empty_section(title="Key Takeaways"):
    return {"title": title, "items": [], "images": [], "tables": []}


def clean_section_heading(text: str) -> str:
    return PAGE_MARKER_PATTERN.sub("", clean_line(text)).strip()


def _resolve_local_media(path_text: str, source_dir: Path | None):
    path_text = path_text.strip().strip("<>").split(" ", 1)[0].strip()
    if not path_text or re.match(r"^[a-z]+://", path_text, re.I):
        return ""
    path = Path(path_text)
    if not path.is_absolute() and source_dir is not None:
        path = source_dir / path
    try:
        if path.exists():
            return str(path.resolve())
        if source_dir is not None:
            matches = [candidate for candidate in source_dir.rglob(path.name) if candidate.is_file()]
            if matches:
                closest = min(matches, key=lambda candidate: (len(candidate.relative_to(source_dir).parts), len(str(candidate))))
                return str(closest.resolve())
        return ""
    except OSError:
        return ""


def _parse_markdown_table(raw_lines):
    rows = []
    for raw in raw_lines:
        cells = [clean_line(cell) for cell in raw.strip().strip("|").split("|")]
        rows.append(cells)
    if len(rows) < 3 or not rows[0]:
        return None
    if not all(TABLE_SEPARATOR_PATTERN.match(cell.replace(" ", "")) for cell in rows[1]):
        return None
    width = len(rows[0])
    body = [row[:width] + [""] * max(0, width - len(row)) for row in rows[2:] if any(row)]
    if not body:
        return None
    return {"headers": rows[0], "rows": body[:8]}


def parse_sections(markdown: str, fallback_title: str, source_dir: Path | None = None):
    title = fallback_title or "Presentation"
    sections = []
    current = _empty_section()
    raw_lines = markdown.splitlines()
    has_markdown_title = any(clean_line(raw).startswith("# ") for raw in raw_lines)
    plain_title_index = None
    if not has_markdown_title and (not fallback_title or fallback_title.strip().lower() == "presentation"):
        for candidate_index, raw in enumerate(raw_lines):
            candidate = clean_line(raw)
            if not candidate or candidate.startswith(("```", "|", "- ", "* ", ">")):
                continue
            if CHINESE_SECTION_PATTERN.match(candidate):
                break
            if len(candidate) <= 88:
                title = clean_section_heading(candidate)
                title = re.sub(r"(?:完整)?PPT内容(?:整理)?$", "", title, flags=re.I).rstrip(" ·-—") or title
                plain_title_index = candidate_index
            break
    in_code_fence = False
    index = 0
    while index < len(raw_lines):
        raw = raw_lines[index]
        stripped = raw.strip()
        if stripped.startswith("```"):
            in_code_fence = not in_code_fence
            index += 1
            continue
        if in_code_fence:
            index += 1
            continue
        if stripped.startswith("|"):
            table_lines = []
            while index < len(raw_lines) and raw_lines[index].strip().startswith("|"):
                table_lines.append(raw_lines[index])
                index += 1
            table = _parse_markdown_table(table_lines)
            if table:
                current["tables"].append(table)
            continue
        line = clean_line(raw)
        index += 1
        if not line or line in {"---", "***", "___"}:
            continue
        if line.startswith("<!--") and line.endswith("-->"):
            continue
        if plain_title_index is not None and index - 1 == plain_title_index:
            continue
        if line.startswith("# "):
            title = line[2:].strip() or title
        elif line.startswith("## "):
            if current["title"] or current["items"] or current["images"] or current["tables"]:
                current["items"] = dedupe(current["items"], 8)
                if current["items"] or current["images"] or current["tables"] or current["title"] != "Key Takeaways":
                    sections.append(current)
            current = _empty_section(trim_text(clean_section_heading(line[3:].strip()), 52))
        elif CHINESE_SECTION_PATTERN.match(line):
            heading_match = CHINESE_SECTION_PATTERN.match(line)
            if current["title"] or current["items"] or current["images"] or current["tables"]:
                current["items"] = dedupe(current["items"], 8)
                if current["items"] or current["images"] or current["tables"] or current["title"] != "Key Takeaways":
                    sections.append(current)
            current = _empty_section(trim_text(clean_section_heading(heading_match.group(2)), 52))
        elif line.startswith("### "):
            current["items"].append(trim_text(line[4:].strip(), 96))
        else:
            image_match = IMAGE_PATTERN.search(line)
            if image_match:
                media_path = _resolve_local_media(image_match.group(2), source_dir)
                if media_path:
                    current["images"].append({
                        "path": media_path,
                        "alt": trim_text(image_match.group(1).strip() or current["title"], 48),
                    })
                continue
            item = LIST_PREFIX_PATTERN.sub("", line).strip()
            if item.startswith(">"):
                item = item[1:].strip()
            for part in split_sentences(item):
                if part:
                    current["items"].append(trim_text(part, 96))
    current["items"] = dedupe(current["items"], 8)
    if current["items"] or current["images"] or current["tables"] or current["title"] != "Key Takeaways":
        sections.append(current)
    if not sections:
        sections = [_empty_section()]
        sections[0]["items"] = [trim_text(markdown.strip(), 96)]
    return trim_text(title, 56), sections


def _keyword_count(text: str, keyword: str) -> int:
    keyword = keyword.lower().strip()
    if not keyword:
        return 0
    if keyword.isascii():
        pattern = r"(?<![a-z0-9_])" + re.escape(keyword) + r"(?![a-z0-9_])"
        return len(re.findall(pattern, text))
    return text.count(keyword)


def detect_family(text: str) -> str:
    lower = text.lower()
    scores = {}
    for family, keywords in FAMILY_KEYWORDS.items():
        score = sum(min(2, _keyword_count(lower, kw)) for kw in keywords)
        score += sum(2 * min(2, _keyword_count(lower, kw)) for kw in STRONG_FAMILY_KEYWORDS.get(family, []))
        scores[family] = score
    priority = {name: index for index, name in enumerate(FAMILY_KEYWORDS)}
    ranked = sorted(scores.items(), key=lambda item: (-item[1], priority[item[0]]))
    if ranked and ranked[0][1] > 0:
        return ranked[0][0]
    return "general_business"


def choose_theme(layouts: dict, family: str, source_text: str = "") -> str:
    lower = source_text.lower()
    for theme, keywords in EXPLICIT_THEME_KEYWORDS.items():
        if theme in SUPPORTED_RENDER_THEMES and any(keyword in lower for keyword in keywords):
            return theme
    return "charcoal"


def collect_agenda(sections, limit=5):
    out = []
    for idx, sec in enumerate(sections[:limit], 1):
        if sec["title"]:
            title = re.sub(r"^\s*\d+[.、)]\s*", "", sec["title"]).strip()
            out.append(f"{idx}. {trim_text(title, 42)}")
    return out


def collect_summary(sections, limit=5):
    out = []
    for sec in sections:
        if sec["items"]:
            out.append(sec["items"][0])
        for item in sec["items"][1:]:
            if len(out) >= limit:
                break
            if len(item) > 18:
                out.append(item)
        if len(out) >= limit:
            break
    out = dedupe(out, limit)
    return out or ["Summarize the available material into a concise decision-ready view."]


def extract_metrics(items):
    number = r"[0-9][0-9,]*(?:\.[0-9]+)?"
    re_metric = re.compile(
        rf"(?<![A-Za-z0-9_/])(?:USD\s*|[$¥￥])?{number}"
        rf"(?:\s*[–—-]\s*(?:USD\s*|[$¥￥])?{number})?"
        r"(?:\s*(?:%|x|X|bp|bps|k|K|m|M|b|B|ct|倍|万|亿))?"
    )
    out = []
    seen = set()
    for item in items:
        if not item or "figures/" in item.lower() or item.lstrip().startswith("|"):
            continue
        candidates = list(re_metric.finditer(item))
        if not candidates:
            continue
        def candidate_score(match):
            value = match.group(0).strip()
            normalized = value.lower().replace(" ", "")
            score = 1
            if "," in value:
                score += 4
            if "%" in value or "bp" in normalized or "倍" in value:
                score += 5
            if value.startswith(("$", "¥", "￥")) or normalized.startswith("usd"):
                score += 4
            if re.search(r"(?:万|亿|[xX])$", value):
                score += 3
            if re.fullmatch(r"~?20\d{2}(?:[-–—]20\d{2}[A-Za-z]?)?(?:年|E)?", normalized, re.I):
                score -= 7
            context = item[max(0, match.start() - 18): min(len(item), match.end() + 18)]
            if any(marker in context for marker in ("身高", "尺寸", "米高", "长宽高")):
                score -= 7
            return score

        match = max(candidates, key=candidate_score)
        if candidate_score(match) <= 0:
            continue
        value = match.group(0).strip()
        if value in seen:
            continue
        seen.add(value)
        prefix = item[:match.start()].replace("`", "").strip(" :-,.;，。；：、（）()")
        suffix = item[match.end():].replace("`", "").strip(" :-,.;，。；：、（）()")
        label = prefix
        for separator in ("：", ":"):
            if separator in label:
                label = label.split(separator, 1)[0].strip()
                break
        if not label:
            label = suffix or "Key metric"
        label = trim_text(label or "Key metric", 30)
        sub = trim_text(suffix, 28) if suffix and suffix != label else ""
        out.append({"value": trim_text(value, 18), "label": label, "sub": sub})
        if len(out) >= 4:
            break
    return out


def split_items(items):
    if len(items) <= 1:
        return items, ["Add one more supporting point or evidence source."]
    mid = (len(items) + 1) // 2
    left = items[:mid]
    right = items[mid:] or ["Add one more supporting point or evidence source."]
    return left, right


def build_section_blocks(items):
    metrics = extract_metrics(items)
    if len(metrics) >= 2 and len(items) >= 3:
        blocks = [{"type": "kpi", "span": "full", "metrics": metrics}]
        metric_values = [metric.get("value", "") for metric in metrics]
        residual = [
            item for item in items
            if not any(value and value in str(item) for value in metric_values)
        ]
        if residual:
            blocks.append({"type": "bullets", "span": "full", "items": residual[:3]})
        return blocks
    if len(items) >= 4:
        left, right = split_items(items)
        return [
            {"type": "bullets", "span": "left", "items": left},
            {"type": "bullets", "span": "right", "items": right},
        ]
    return [{"type": "text", "span": "full", "heading": "Key points", "body": "\n".join(items)}]


def contains_cjk(text: str) -> bool:
    return bool(re.search(r"[\u3400-\u9fff]", text))


def profile_for(family: str, source_text: str):
    profiles = PROFILE_TEXT_ZH if contains_cjk(source_text) else PROFILE_TEXT
    return profiles.get(family, profiles["general_business"])


def find_action_section(sections):
    markers = ("结论", "建议", "洞察", "下一步", "recommend", "conclusion", "takeaway", "action", "next step")
    for section in reversed(sections):
        if any(marker in section.get("title", "").lower() for marker in markers) and section.get("items"):
            return section
    return None


def derive_actions(sections, chinese=False):
    action_section = find_action_section(sections)
    if action_section:
        actions = dedupe(action_section.get("items", []), 4)
        if actions:
            return actions
    if sections and sections[-1].get("items"):
        source_items = sections[-1]["items"]
        start = 1 if len(source_items) >= 4 and len(source_items[0]) <= 24 else 0
        inferred = dedupe(source_items[start:], 3)
        if len(inferred) >= 2:
            return inferred
    if chinese:
        return ["基于现有证据明确下一项决策，并保留关键边界与待验证假设。"]
    return ["Use the available evidence to define the next decision while preserving its limits."]


def build_image_blocks(section, items):
    images = section.get("images", [])[:2]
    if not images:
        return build_section_blocks(items)
    image_blocks = [
        {"type": "image", "path": image["path"], "caption": image.get("alt", "")}
        for image in images
    ]
    if len(image_blocks) == 1:
        image_blocks[0]["span"] = "left"
        if items:
            return [
                image_blocks[0],
                {"type": "bullets", "span": "right", "items": items[:5]},
            ]
        image_blocks[0]["span"] = "full"
        return image_blocks
    rows = [[image_blocks[0], image_blocks[1]]]
    if items:
        rows.append([{"type": "bullets", "span": "full", "items": items[:5]}])
    return rows


def section_slide_title(section):
    return trim_text(section.get("title", "") or "Key finding", 52)


def build_workflow(markdown: str, fallback_title: str, layouts: dict, source_dir: Path | None = None):
    title, sections = parse_sections(markdown, fallback_title, source_dir)
    source_text = f"{title}\n{markdown}"
    family = detect_family(source_text)
    profile = profile_for(family, source_text)
    theme = choose_theme(layouts, family, source_text)
    chinese = contains_cjk(source_text)
    layout_meta = layouts.get("layouts", {}).get(theme, {})
    summary_items = collect_summary(sections, 5)
    content_sections = [section for section in sections if section.get("title") != "Key Takeaways"] or sections
    content_sections = content_sections[:10]
    agenda_items = collect_agenda(content_sections, 6)
    metrics = extract_metrics(summary_items)
    action_section = find_action_section(content_sections)
    display_sections = [section for section in content_sections if section is not action_section]
    if not display_sections:
        display_sections = content_sections
        action_section = None
    slides = [{
        "type": "title",
        "eyebrow": profile["eyebrow"],
        "title": title,
        "subtitle": profile["subtitle"],
        "tags": profile["tags"],
    }]

    if len(agenda_items) >= 4:
        slides.append({
            "type": "content",
            "eyebrow": "00",
            "title": profile["agenda"],
            "insight": profile["narrative"],
            "footer": profile["footer"],
            "blocks": [
                {"type": "bullets", "span": "left", "items": agenda_items},
                {"type": "text", "span": "right", "heading": "阅读方式" if chinese else "How to read", "body": profile["narrative"]},
            ],
        })

    if len(metrics) >= 3:
        summary_blocks = [{"type": "kpi", "span": "full", "metrics": metrics}]
    else:
        summary_blocks = [{"type": "bullets", "span": "full", "items": summary_items}]
    slides.append({
        "type": "content",
        "eyebrow": "01",
        "title": profile["summary"],
        "insight": join_insight(summary_items),
        "footer": profile["footer"],
        "blocks": summary_blocks,
    })

    if len(display_sections) >= 7:
        divider_indexes = {0, 4, 8}
    elif len(display_sections) >= 5:
        divider_indexes = {0, 3}
    else:
        divider_indexes = set()

    for idx, sec in enumerate(display_sections, 1):
        items = dedupe(sec["items"], 6) or ["Summarize the available material into a concise decision-ready view."]
        if idx - 1 in divider_indexes:
            slides.append({
                "type": "section",
                "number": f"{idx:02d}",
                "title": section_slide_title(sec),
                "subtitle": trim_text(items[0], 72),
            })

        if sec.get("images") or sec.get("items"):
            section_blocks = build_image_blocks(sec, items)
            slides.append({
                "type": "content",
                "eyebrow": f"{idx:02d}",
                "title": section_slide_title(sec),
                "insight": join_insight(items),
                "footer": profile["footer"],
                "blocks": section_blocks,
            })

        for table in sec.get("tables", [])[:1]:
            table_suffix = "关键数据" if chinese else "Key data"
            slides.append({
                "type": "content",
                "eyebrow": f"{idx:02d}",
                "title": trim_text(f"{section_slide_title(sec)} · {table_suffix}", 52),
                "insight": join_insight(items),
                "footer": profile["footer"],
                "blocks": [{
                    "type": "table",
                    "span": "full",
                    "headers": table["headers"],
                    "rows": table["rows"],
                }],
            })

    actions = dedupe((action_section or {}).get("items", []), 4) or derive_actions(content_sections, chinese)
    action_block = {"type": "bullets", "span": "full", "items": actions}
    slides.append({
        "type": "content",
        "eyebrow": "99",
        "title": profile["actions"],
        "insight": join_insight(actions),
        "footer": profile["footer"],
        "blocks": [action_block],
    })
    slides.append({
        "type": "end",
        "title": profile["end_title"],
        "subtitle": trim_text(actions[0], 76) if actions else profile["end_subtitle"],
        "contacts": [],
    })

    return {
        "meta": {
            "title": title,
            "theme": theme,
            "deck_family": family,
            "template_label": layout_meta.get("label", theme),
            "template_tone": layout_meta.get("tone", ""),
            "template_summary": layout_meta.get("summary", ""),
            "production_plan": {
                "audience": profile["audience"],
                "deck_type": family,
                "slide_count": len(slides),
                "narrative_arc": profile["narrative"],
                "generator": "base-ppt",
                "style_theme": theme,
                "editable_pptx": True,
                "qa_plan": "Check one-message-per-slide, whitespace, overflow, alignment, and metric readability before delivery.",
            },
        },
        "slides": slides,
    }


def dedupe(items, limit):
    out = []
    seen = set()
    for item in items:
        item = trim_text(item.strip(), 96)
        if not item or item in seen:
            continue
        seen.add(item)
        out.append(item)
        if len(out) >= limit:
            break
    return out


def split_sentences(text: str):
    text = text.strip()
    if not text:
        return []
    if len(text) <= 110:
        return [text]
    for sep in [". ", "; ", ": "]:
        if sep in text:
            return [part.strip() for part in text.split(sep) if part.strip()]
    return [text]


def trim_text(text: str, limit: int):
    text = text.strip()
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 3)].rstrip() + "..."


def join_insight(items):
    if not items:
        return "Summarize the available material into a concise decision-ready view."
    if len(items) == 1:
        return trim_text(items[0], 88)
    return trim_text(f"{items[0]}; {items[1]}", 88)


def write_plan_markdown(workflow: dict, path: Path):
    plan = workflow["meta"].get("production_plan", {})
    lines = [
        "# PPT production route",
        f"- audience: {plan.get('audience', '')}",
        f"- deck type: {plan.get('deck_type', '')}",
        f"- slide count: {plan.get('slide_count', '')}",
        f"- narrative arc: {plan.get('narrative_arc', '')}",
        f"- generator skill: {plan.get('generator', '')}",
        "- visual asset skill: paper-operators-visual (optional)",
        f"- style/theme: {plan.get('style_theme', '')}",
        f"- editable PPTX required: {'yes' if plan.get('editable_pptx') else 'no'}",
        "- external account needed: no; fallback: local shapes, charts, and template-aware workflow",
        f"- QA plan: {plan.get('qa_plan', '')}",
    ]
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description="Plan a template-aware PPT workflow JSON from markdown.")
    parser.add_argument("--input", help="Markdown file path")
    parser.add_argument("--markdown", help="Raw markdown text")
    parser.add_argument("--output", required=True, help="Workflow JSON output path")
    parser.add_argument("--plan-out", help="Optional markdown plan output path")
    parser.add_argument("--title", default="Presentation", help="Fallback title")
    args = parser.parse_args()

    if not args.input and not args.markdown:
        raise SystemExit("Provide --input or --markdown")

    if args.input:
        input_path = Path(args.input).resolve()
        markdown = input_path.read_text(encoding="utf-8-sig")
        source_dir = input_path.parent
    else:
        markdown = args.markdown or ""
        source_dir = None

    skill_dir = Path(__file__).resolve().parent
    layouts = load_layouts(skill_dir)
    workflow = build_workflow(markdown, args.title, layouts, source_dir)

    out_path = Path(args.output)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(workflow, ensure_ascii=False, indent=2), encoding="utf-8")

    if args.plan_out:
        write_plan_markdown(workflow, Path(args.plan_out))

    print(f"workflow saved: {out_path}")


if __name__ == "__main__":
    main()
