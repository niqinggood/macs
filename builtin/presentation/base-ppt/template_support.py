#!/usr/bin/env python3
"""Shared, deterministic support for inspecting and selecting PPTX layouts."""

from __future__ import annotations

import re
from pathlib import Path


_NAME_HINTS = {
    "title": ("title", "cover", "opening", "封面", "标题"),
    "section": ("section", "chapter", "divider", "transition", "章节", "分节", "过渡"),
    "content": ("content", "body", "text", "正文", "内容"),
    "end": ("end", "ending", "closing", "thank", "q&a", "结束", "结尾", "致谢"),
}


def placeholder_type_name(value):
    name = getattr(value, "name", None)
    if name:
        return str(name)
    text = str(value)
    match = re.match(r"([A-Z_]+)", text)
    return match.group(1) if match else text


def iter_layouts(prs):
    for master_index, master in enumerate(prs.slide_masters):
        for layout_index, layout in enumerate(master.slide_layouts):
            yield master_index, layout_index, layout


def layout_record(master_index, layout_index, layout):
    placeholders = []
    for placeholder in layout.placeholders:
        fmt = placeholder.placeholder_format
        placeholders.append(
            {
                "idx": int(fmt.idx),
                "name": placeholder.name,
                "type": placeholder_type_name(fmt.type),
                "left": int(placeholder.left),
                "top": int(placeholder.top),
                "width": int(placeholder.width),
                "height": int(placeholder.height),
            }
        )
    return {
        "master_index": master_index,
        "layout_index": layout_index,
        "name": layout.name or f"layout-{master_index}-{layout_index}",
        "placeholders": placeholders,
    }


def _normalized_name(value):
    return re.sub(r"[\s_\-]+", "", str(value or "").casefold())


def _layout_score(record, slide_type):
    name = _normalized_name(record["name"])
    types = {item["type"] for item in record["placeholders"]}
    score = 0
    for hint in _NAME_HINTS[slide_type]:
        if _normalized_name(hint) in name:
            score += 12

    has_title = bool(types & {"TITLE", "CENTER_TITLE"})
    has_subtitle = "SUBTITLE" in types
    has_body = bool(types & {"BODY", "OBJECT", "TEXT"})
    if slide_type == "title":
        score += 7 if "CENTER_TITLE" in types else 0
        score += 5 if has_title else 0
        score += 3 if has_subtitle else 0
        score -= 3 if has_body else 0
    elif slide_type == "section":
        score += 6 if has_title else 0
        score += 2 if has_subtitle else 0
        score -= 2 if has_body else 0
    elif slide_type == "content":
        score += 6 if has_title else 0
        score += 8 if has_body else 0
    elif slide_type == "end":
        score += 5 if has_title else 0
        score += 2 if has_subtitle else 0
        score -= 3 if has_body else 0
    return score


def _resolve_explicit_layout(records, requested):
    if requested is None:
        return None
    if isinstance(requested, dict):
        master = requested.get("master_index")
        layout = requested.get("layout_index")
        for record, layout_obj in records:
            if record["master_index"] == master and record["layout_index"] == layout:
                return record, layout_obj
        raise ValueError(f"template layout not found: master={master} layout={layout}")

    text = str(requested).strip()
    match = re.fullmatch(r"(\d+)\s*:\s*(\d+)", text)
    if match:
        master, layout = int(match.group(1)), int(match.group(2))
        return _resolve_explicit_layout(
            records, {"master_index": master, "layout_index": layout}
        )

    wanted = text.casefold()
    matches = [(record, obj) for record, obj in records if record["name"].casefold() == wanted]
    if not matches:
        raise ValueError(f"template layout not found by name: {text}")
    if len(matches) > 1:
        refs = ", ".join(
            f"{record['master_index']}:{record['layout_index']}" for record, _ in matches
        )
        raise ValueError(f"template layout name is ambiguous: {text}; use one of {refs}")
    return matches[0]


def select_layout(prs, slide_type, requested=None):
    records = [
        (layout_record(master, index, layout), layout)
        for master, index, layout in iter_layouts(prs)
    ]
    if not records:
        raise ValueError("template has no slide layouts")
    explicit = _resolve_explicit_layout(records, requested)
    if explicit is not None:
        return explicit
    canonical = slide_type if slide_type in _NAME_HINTS else "content"
    return max(
        records,
        key=lambda item: (
            _layout_score(item[0], canonical),
            -item[0]["master_index"],
            -item[0]["layout_index"],
        ),
    )


def recommended_layouts(prs):
    result = {}
    for slide_type in ("title", "section", "content", "end"):
        record, _ = select_layout(prs, slide_type)
        result[slide_type] = {
            "master_index": record["master_index"],
            "layout_index": record["layout_index"],
            "name": record["name"],
        }
    return result


def clear_slides(prs):
    """Remove exemplar slides while preserving masters, layouts, and theme parts."""
    slide_ids = list(prs.slides._sldIdLst)
    for slide_id in slide_ids:
        relationship_id = slide_id.rId
        prs.part.drop_rel(relationship_id)
        prs.slides._sldIdLst.remove(slide_id)


def template_manifest(prs, template_path=None):
    width = int(prs.slide_width)
    height = int(prs.slide_height)
    layouts = [
        layout_record(master, index, layout)
        for master, index, layout in iter_layouts(prs)
    ]
    return {
        "schema_version": 1,
        "template_path": str(Path(template_path).resolve()) if template_path else None,
        "source_slide_count": len(prs.slides),
        "slide_size": {
            "width_emu": width,
            "height_emu": height,
            "width_inches": round(width / 914400, 4),
            "height_inches": round(height / 914400, 4),
            "aspect_ratio": round(width / height, 6) if height else None,
        },
        "master_count": len(prs.slide_masters),
        "layouts": layouts,
        "recommended_layouts": recommended_layouts(prs),
        "fidelity_note": (
            "This manifest describes native masters, layouts, and placeholders. "
            "Strict generation must fill inherited placeholders only; style-reference "
            "or adaptive rendering is a separate intent."
        ),
    }
