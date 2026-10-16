#!/usr/bin/env python3
"""
Structured editable PPT generator
Design principles:
  - Quiet editorial headers; reserve full-bleed dark color for narrative moments
  - Strong numeric hierarchy (big KPI numbers, small labels)
  - Quiet footer metadata
  - One primary accent; semantic colors only when the data requires them
  - Cards: subtle surfaces and hairline borders, without dashboard chrome
  - Generous whitespace, consistent 3-zone layout: header / body / footer
"""
import sys, os, json, io, tempfile
from pptx import Presentation
from pptx.util import Inches, Pt, Emu
from pptx.dml.color import RGBColor
from pptx.enum.text import MSO_ANCHOR, PP_ALIGN
from pptx.enum.shapes import MSO_SHAPE
from pptx.chart.data import CategoryChartData
from pptx.enum.chart import XL_CHART_TYPE

from template_support import clear_slides, placeholder_type_name, select_layout

# ── Matplotlib setup (with Chinese font support) ────────────────────────────────
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import matplotlib.font_manager as fm
import numpy as np
from PIL import Image

_MPL_FONT = None
_font_candidates = [
    'Microsoft YaHei', 'SimHei', 'PingFang SC', 'Noto Sans CJK SC',
    'WenQuanYi Micro Hei', 'Source Han Sans SC', 'STHeiti', 'Arial Unicode MS',
    'sans-serif'
]
for _f in _font_candidates:
    for _ff in fm.findSystemFonts():
        if _f.lower() in os.path.basename(_ff).lower():
            _MPL_FONT = fm.FontProperties(fname=_ff)
            break
    if _MPL_FONT:
        break
if _MPL_FONT is None:
    _MPL_FONT = fm.FontProperties()
plt.rcParams['font.family'] = _MPL_FONT.get_name() if _MPL_FONT else 'sans-serif'
plt.rcParams['axes.unicode_minus'] = False

_CHART_COLORS = ['#315C5B', '#B5654D', '#7B8790', '#C39B52', '#819A82', '#7C5C6E']
_GOOGLE_COLORS = ['#4285F4', '#EA4335', '#FBBC04', '#34A853']

# ── Themes ────────────────────────────────────────────────────────────────────
# Each theme: header bg, header text, accent1, accent2, body bg, card fill,
#             body text, muted text, border color, footer bg
THEMES = {
    "midnight": {
        "hbg":  "1E2761",   # header background
        "htx":  "FFFFFF",   # header text
        "ac1":  "4A9FD4",   # accent 1 (primary)
        "ac2":  "F5A623",   # accent 2 (highlight)
        "pbg":  "FFFFFF",   # page background
        "cbg":  "F4F7FB",   # card fill
        "tx":   "1A1A2E",   # body text
        "mt":   "7B8794",   # muted text
        "bd":   "DDE3EE",   # border
        "fbg":  "F4F7FB",   # footer bg
        "hfont":"Calibri",
        "bfont":"Calibri",
    },
    "forest": {
        "hbg":  "1B3A2D",
        "htx":  "FFFFFF",
        "ac1":  "4CAF7D",
        "ac2":  "F5C842",
        "pbg":  "FFFFFF",
        "cbg":  "F3F8F5",
        "tx":   "1B3A2D",
        "mt":   "6B8070",
        "bd":   "D5E8DC",
        "fbg":  "F3F8F5",
        "hfont":"Georgia",
        "bfont":"Calibri",
    },
    "coral": {
        "hbg":  "2F3C7E",
        "htx":  "FFFFFF",
        "ac1":  "F96167",
        "ac2":  "F9E795",
        "pbg":  "FFFFFF",
        "cbg":  "FFF8F8",
        "tx":   "2F3C7E",
        "mt":   "7A7FA8",
        "bd":   "F5D5D7",
        "fbg":  "FFF4F4",
        "hfont":"Trebuchet MS",
        "bfont":"Calibri",
    },
    "charcoal": {
        "hbg":  "2B2A27",
        "htx":  "FFFFFF",
        "ac1":  "C45D3C",
        "ac2":  "6E8B74",
        "pbg":  "FBFAF7",
        "cbg":  "F3EFE7",
        "tx":   "1C1C1A",
        "mt":   "6B6962",
        "bd":   "DED8CD",
        "fbg":  "F3EFE7",
        "hfont":"Arial",
        "bfont":"Arial",
    },
    "teal": {
        "hbg":  "065A82",
        "htx":  "FFFFFF",
        "ac1":  "02C39A",
        "ac2":  "F5A623",
        "pbg":  "FFFFFF",
        "cbg":  "F0F9FB",
        "tx":   "054A6E",
        "mt":   "5F8EA0",
        "bd":   "C8E8F0",
        "fbg":  "F0F9FB",
        "hfont":"Calibri",
        "bfont":"Calibri",
    },
    "berry": {
        "hbg":  "4A1040",
        "htx":  "FFFFFF",
        "ac1":  "C2679D",
        "ac2":  "F5C842",
        "pbg":  "FFFFFF",
        "cbg":  "FCF5FA",
        "tx":   "3A0A30",
        "mt":   "9B6488",
        "bd":   "EDD5E8",
        "fbg":  "FCF5FA",
        "hfont":"Georgia",
        "bfont":"Calibri",
    },
    "ocean": {
        "hbg":  "0A2342",
        "htx":  "FFFFFF",
        "ac1":  "1B9AAA",
        "ac2":  "FFC857",
        "pbg":  "FFFFFF",
        "cbg":  "EEF6FA",
        "tx":   "0A2342",
        "mt":   "5880A0",
        "bd":   "C5DFF0",
        "fbg":  "EEF6FA",
        "hfont":"Cambria",
        "bfont":"Calibri",
    },
    "mckinsey": {
        "hbg":  "0B1F3A",
        "htx":  "FFFFFF",
        "ac1":  "2F6FB3",
        "ac2":  "D6B46A",
        "pbg":  "FFFFFF",
        "cbg":  "F3F1EC",
        "tx":   "142033",
        "mt":   "667085",
        "bd":   "D7DCE3",
        "fbg":  "0B1F3A",
        "hfont":"Microsoft YaHei",
        "bfont":"Microsoft YaHei",
    },
    "google_style": {
        "hbg":  "FFFFFF",
        "htx":  "202124",
        "ac1":  "4285F4",
        "ac2":  "FBBC04",
        "pbg":  "FFFFFF",
        "cbg":  "F8FAFF",
        "tx":   "202124",
        "mt":   "5F6368",
        "bd":   "DADCE0",
        "fbg":  "F8F9FA",
        "hfont":"Arial",
        "bfont":"Arial",
        "series": ["4285F4", "EA4335", "FBBC04", "34A853"],
    },
    "anthropic": {
        "hbg":  "2B2118",
        "htx":  "F7F0E8",
        "ac1":  "D97757",
        "ac2":  "C9A227",
        "pbg":  "F7F0E8",
        "cbg":  "FFF8F0",
        "tx":   "2B2118",
        "mt":   "7C6F64",
        "bd":   "E4D7C8",
        "fbg":  "EFE5D8",
        "hfont":"Georgia",
        "bfont":"Calibri",
    },
    "exhibit": {
        "hbg":  "101827",
        "htx":  "FFFFFF",
        "ac1":  "D6B46A",
        "ac2":  "5DADEC",
        "pbg":  "0F172A",
        "cbg":  "172033",
        "tx":   "F8FAFC",
        "mt":   "CBD5E1",
        "bd":   "334155",
        "fbg":  "111827",
        "hfont":"Georgia",
        "bfont":"Calibri",
    },
    "government_blue": {
        "hbg":  "0B4AA2",
        "htx":  "FFFFFF",
        "ac1":  "1D7AF3",
        "ac2":  "D6B46A",
        "pbg":  "FFFFFF",
        "cbg":  "EEF5FF",
        "tx":   "0B1F3A",
        "mt":   "64748B",
        "bd":   "C7DDF8",
        "fbg":  "F2F7FF",
        "hfont":"Microsoft YaHei",
        "bfont":"Microsoft YaHei",
    },
    "government_red": {
        "hbg":  "9A1B1E",
        "htx":  "FFFFFF",
        "ac1":  "C93636",
        "ac2":  "E7B84B",
        "pbg":  "FFFFFF",
        "cbg":  "FFF4F2",
        "tx":   "2F1B1B",
        "mt":   "7A5C5C",
        "bd":   "F1D0CC",
        "fbg":  "FFF7F5",
        "hfont":"Microsoft YaHei",
        "bfont":"Microsoft YaHei",
    },
    "academic_defense": {
        "hbg":  "12355B",
        "htx":  "FFFFFF",
        "ac1":  "2E86AB",
        "ac2":  "A23B72",
        "pbg":  "FFFFFF",
        "cbg":  "F4F8FB",
        "tx":   "1F2937",
        "mt":   "6B7280",
        "bd":   "D7E3EC",
        "fbg":  "F4F8FB",
        "hfont":"Cambria",
        "bfont":"Calibri",
    },
    "medical_university": {
        "hbg":  "0F5F8C",
        "htx":  "FFFFFF",
        "ac1":  "17A2B8",
        "ac2":  "58B368",
        "pbg":  "FFFFFF",
        "cbg":  "F0FAFA",
        "tx":   "12343B",
        "mt":   "5C7A80",
        "bd":   "CBE7EA",
        "fbg":  "F0FAFA",
        "hfont":"Microsoft YaHei",
        "bfont":"Microsoft YaHei",
    },
    "pixel_retro": {
        "hbg":  "111827",
        "htx":  "F9FAFB",
        "ac1":  "22D3EE",
        "ac2":  "F472B6",
        "pbg":  "0B1020",
        "cbg":  "111827",
        "tx":   "F9FAFB",
        "mt":   "A7F3D0",
        "bd":   "374151",
        "fbg":  "0B1020",
        "hfont":"Consolas",
        "bfont":"Consolas",
    },
    "smart_red": {
        "hbg":  "7C1D1D",
        "htx":  "FFFFFF",
        "ac1":  "EF4444",
        "ac2":  "F97316",
        "pbg":  "FFFFFF",
        "cbg":  "FFF7ED",
        "tx":   "321414",
        "mt":   "7C5C5C",
        "bd":   "FED7AA",
        "fbg":  "FFF7ED",
        "hfont":"Trebuchet MS",
        "bfont":"Calibri",
    },
    "ai_ops": {
        "hbg":  "102A43",
        "htx":  "FFFFFF",
        "ac1":  "00A3FF",
        "ac2":  "FFB020",
        "pbg":  "FFFFFF",
        "cbg":  "EEF6FF",
        "tx":   "102A43",
        "mt":   "627D98",
        "bd":   "C7DDEE",
        "fbg":  "F0F6FF",
        "hfont":"Microsoft YaHei",
        "bfont":"Microsoft YaHei",
    },
}

# ── Helpers ───────────────────────────────────────────────────────────────────
def rgb(h):
    if isinstance(h, RGBColor): return h
    h = h.lstrip("#")
    return RGBColor(int(h[0:2],16), int(h[2:4],16), int(h[4:6],16))

def blend_hex(foreground, background, opacity):
    """Pre-blend two six-digit colors for APIs that cannot express alpha."""
    fg = str(foreground).lstrip("#")[:6]
    bg = str(background).lstrip("#")[:6]
    opacity = max(0.0, min(1.0, float(opacity)))
    channels = []
    for offset in (0, 2, 4):
        value = round(
            int(bg[offset:offset + 2], 16) * (1.0 - opacity)
            + int(fg[offset:offset + 2], 16) * opacity
        )
        channels.append(f"{value:02X}")
    return "".join(channels)


def accent_palette(t):
    colors = [str(color).lstrip("#")[:6] for color in t.get("series", []) if color]
    if colors:
        return colors
    return list(dict.fromkeys([t["ac1"], t["ac2"]]))


def semantic_accent(t, item=None, index=0):
    palette = accent_palette(t)
    role = str((item or {}).get("color_role", (item or {}).get("tone", ""))).strip().lower()
    if theme_name(t) == "google_style":
        role_colors = {
            "primary": "4285F4", "info": "4285F4", "evidence": "4285F4",
            "risk": "EA4335", "negative": "EA4335", "danger": "EA4335",
            "highlight": "FBBC04", "warning": "FBBC04", "attention": "FBBC04",
            "positive": "34A853", "success": "34A853", "action": "34A853",
        }
        if role in role_colors:
            return role_colors[role]
    return palette[index % len(palette)]


def accent_text_color(t, color):
    """Keep semantic identity while preserving contrast on light canvases."""
    normalized = str(color).lstrip("#").upper()
    if is_google(t) and normalized == "FBBC04":
        return "F9AB00"
    return normalized


def visual_text_units(text):
    units = 0.0
    for char in str(text or ""):
        code = ord(char)
        if char.isspace():
            units += 0.30
        elif 0x2E80 <= code <= 0x9FFF or 0xF900 <= code <= 0xFAFF:
            units += 1.0
        elif char.isupper():
            units += 0.68
        else:
            units += 0.54
    return units


def fitted_font_size(text, width, max_size, min_size, max_lines=1):
    """Estimate a safe title size from visual width without shrinking body copy."""
    units = max(1.0, visual_text_units(text))
    capacity = max(1.0, width * 72.0 * max_lines / (max_size * 0.72))
    if units <= capacity:
        return max_size
    return round(max(min_size, max_size * capacity / units), 1)

def rect(slide, x, y, w, h, fill, line=None):
    sh = slide.shapes.add_shape(
        MSO_SHAPE.RECTANGLE,
        Inches(x), Inches(y), Inches(w), Inches(h)
    )
    sh.fill.solid(); sh.fill.fore_color.rgb = rgb(fill)
    if line:
        sh.line.color.rgb = rgb(line)
        sh.line.width = Pt(0.5)
    else:
        sh.line.fill.background()
    return sh

def card(slide, x, y, w, h, fill, shadow=True, radius=0.06):
    """Premium card with shadow depth (质感: 阴影+圆角)"""
    if shadow:
        # Offset dark rect behind for shadow effect
        rect(slide, x + 0.03, y + 0.04, w, h, "0000001A")
    sh = slide.shapes.add_shape(
        MSO_SHAPE.ROUNDED_RECTANGLE if radius > 0 else MSO_SHAPE.RECTANGLE,
        Inches(x), Inches(y), Inches(w), Inches(h)
    )
    sh.fill.solid(); sh.fill.fore_color.rgb = rgb(fill)
    sh.line.fill.background()
    return sh

def textbox(slide, x, y, w, h, text, size=12, bold=False, italic=False,
            color="333333", align=PP_ALIGN.LEFT, font="Calibri", wrap=True,
            vertical_anchor=None):
    tb = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
    tf = tb.text_frame; tf.word_wrap = wrap
    if vertical_anchor is not None:
        tf.vertical_anchor = vertical_anchor
    tf.margin_left = Pt(4); tf.margin_right = Pt(4)
    tf.margin_top = Pt(2); tf.margin_bottom = Pt(2)
    p = tf.paragraphs[0]; p.alignment = align
    r = p.add_run(); r.text = text
    r.font.size = Pt(size); r.font.bold = bold; r.font.italic = italic
    r.font.color.rgb = rgb(color); r.font.name = font
    return tb


def add_picture_contain(slide, path, x, y, w, h, background="FFFFFF", border="DADCE0", caption="", font="Calibri", text_color="5F6368"):
    caption_h = 0.24 if caption else 0.0
    image_h = max(0.2, h - caption_h)
    rect(slide, x, y, w, image_h, background, line=border)
    with Image.open(path) as image:
        pixel_w, pixel_h = image.size
    if not pixel_w or not pixel_h:
        raise ValueError(f"image has invalid dimensions: {path}")
    image_ratio = pixel_w / pixel_h
    box_ratio = w / image_h
    if image_ratio >= box_ratio:
        draw_w = w
        draw_h = w / image_ratio
    else:
        draw_h = image_h
        draw_w = image_h * image_ratio
    draw_x = x + (w - draw_w) / 2
    draw_y = y + (image_h - draw_h) / 2
    picture = slide.shapes.add_picture(path, Inches(draw_x), Inches(draw_y), Inches(draw_w), Inches(draw_h))
    if caption:
        textbox(slide, x, y + image_h + 0.02, w, max(0.16, caption_h - 0.02), caption,
                7.5, color=text_color, align=PP_ALIGN.CENTER, font=font, wrap=False)
    return picture

def set_bg(slide, fill_color):
    bg = slide.background; bg.fill.solid()
    bg.fill.fore_color.rgb = rgb(fill_color)

def quiet_header_background(slide, t):
    """Light editorial header for ordinary content pages."""
    set_bg(slide, t["pbg"])
    rect(slide, 0, 0, W, 0.035, t["hbg"])
    rect(slide, LM, HH - 0.02, CW, 0.01, t["bd"])

def theme_name(t):
    return t.get("name", "")

def is_exhibit(t):
    return theme_name(t) == "exhibit"

def is_mckinsey(t):
    return theme_name(t) == "mckinsey"

def is_google(t):
    return theme_name(t) == "google_style"

def draw_soft_grid(slide, color, background, x=6.9, y=0.6, w=2.4, h=3.8, cols=6, rows=8, alpha_hex="18"):
    opacity = int(alpha_hex, 16) / 255.0
    line_color = blend_hex(color, background, opacity)
    for i in range(cols + 1):
        xx = x + w * i / cols
        rect(slide, xx, y, 0.01, h, line_color)
    for j in range(rows + 1):
        yy = y + h * j / rows
        rect(slide, x, yy, w, 0.01, line_color)


# ── Layout constants (inches) ─────────────────────────────────────────────────
W  = 10.0        # slide width
H  = 5.63        # slide height
LM = 0.45        # left margin
RM = 0.45        # right margin
CW = W - LM - RM # content width
HH = 1.15        # header height
FH = 0.28        # footer height
FY = H - FH      # footer Y
BY = HH + 0.18   # body start Y
BH = FY - BY - 0.12  # body height
GAP = 0.12       # gap between cards
STRIPE = 0.018   # hairline accent on structured cards


# ── Header ────────────────────────────────────────────────────────────────────
def draw_header(slide, t, eyebrow, title, insight=""):
    if is_exhibit(t):
        rect(slide, 0, 0, W, 0.05, t["ac1"])
        rect(slide, 0, 0.05, W, 0.015, t["ac2"])
        rect(slide, LM, 0.22, CW * 0.70, 0.58, t["hbg"])
        rect(slide, LM, 0.22, 0.05, 0.58, t["ac2"])
        if eyebrow:
            textbox(slide, LM + 0.16, 0.28, CW * 0.58, 0.14,
                    eyebrow.upper(), 7.2, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])
        textbox(slide, LM + 0.16, 0.42, CW * 0.58, 0.20,
                title, 18, bold=True, color=t["htx"], font=t["hfont"])
        if insight:
            textbox(slide, LM + CW * 0.73, 0.28, CW * 0.22, 0.40,
                    insight, 8.2, color=t["mt"], font=t["bfont"], wrap=True)
        return

    if is_mckinsey(t):
        rect(slide, 0, 0, W, 0.96, t["hbg"])
        rect(slide, 0, 0, W, 0.035, t["ac2"])
        if eyebrow:
            textbox(slide, LM, 0.16, CW, 0.14,
                    eyebrow.upper(), 7.0, bold=True, color=t["ac2"], font=t["bfont"])
        textbox(slide, LM, 0.36, CW * 0.70, 0.34,
                title, 19.5, bold=True, color=t["htx"], font=t["hfont"], wrap=True)
        if insight:
            textbox(slide, LM + CW * 0.74, 0.28, CW * 0.24, 0.46,
                    insight, 8.0, color="D7DEEA", font=t["bfont"], wrap=True)
        return

    if is_google(t):
        rect(slide, 0, 0, W * 0.25, 0.04, "4285F4")
        rect(slide, W * 0.25, 0, W * 0.20, 0.04, "EA4335")
        rect(slide, W * 0.45, 0, W * 0.15, 0.04, "FBBC04")
        rect(slide, W * 0.60, 0, W * 0.40, 0.04, "34A853")
        if eyebrow:
            textbox(slide, LM, 0.22, CW, 0.14,
                    eyebrow.upper(), 7.0, color=t["ac1"], font=t["bfont"])
        title_w = CW * 0.70 if insight else CW
        title_size = fitted_font_size(title, title_w, 19, 15.5, max_lines=2)
        textbox(slide, LM, 0.40, title_w, 0.40,
                title, title_size, bold=True, color=t["tx"], font=t["hfont"], wrap=True)
        rect(slide, LM, 0.86, 1.1, 0.025, "4285F4")
        rect(slide, LM + 1.1, 0.86, 0.55, 0.025, "EA4335")
        rect(slide, LM + 1.65, 0.86, 0.55, 0.025, "FBBC04")
        rect(slide, LM + 2.20, 0.86, 1.15, 0.025, "34A853")
        if insight:
            textbox(slide, LM, 0.96, CW * 0.90, 0.20,
                    insight, 8.4, color=t["mt"], font=t["bfont"], wrap=True)
        return

    quiet_header_background(slide, t)
    ey = 0.16
    if eyebrow:
        textbox(slide, LM, ey, CW, 0.15,
                eyebrow.upper(), 7.2, bold=True, color=t["ac1"],
                font=t["bfont"])
        ey = 0.36

    title_w = CW * 0.70 if insight else CW
    title_size = fitted_font_size(title, title_w, 20, 15.5, max_lines=2)
    textbox(slide, LM, ey, title_w, 0.46,
            title, title_size, bold=True, color=t["tx"], font=t["hfont"], wrap=True)

    if insight:
        textbox(slide, LM + CW * 0.74, 0.28, CW * 0.26, 0.50,
                insight, 8.2, color=t["mt"], font=t["bfont"], wrap=True)


# ── Footer ────────────────────────────────────────────────────────────────────
def draw_footer(slide, t, label, pg, total):
    if is_exhibit(t):
        rect(slide, 0, FY, W, FH, t["fbg"])
        rect(slide, 0, FY, W, 0.008, t["bd"])
        if label:
            textbox(slide, LM, FY + 0.05, CW * 0.7, 0.2,
                    label.upper(), 7.2, color=t["mt"], font=t["bfont"])
        textbox(slide, 0, FY + 0.05, W - RM, 0.2,
                f"{pg:02d}", 7.8, color=t["tx"],
                align=PP_ALIGN.RIGHT, font=t["bfont"])
        return
    if is_mckinsey(t):
        rect(slide, 0, FY, W, FH, t["fbg"])
        rect(slide, 0, FY, W, 0.008, t["ac2"])
        if label:
            textbox(slide, LM, FY + 0.05, CW * 0.7, 0.2,
                    label, 7.0, color="D7DEEA", font=t["bfont"])
        textbox(slide, 0, FY + 0.05, W - RM, 0.2,
                f"{pg} / {total}", 7.2, color=t["htx"],
                align=PP_ALIGN.RIGHT, font=t["bfont"])
        return
    rect(slide, 0, FY, W, FH, t["fbg"])
    # thin top border
    rect(slide, 0, FY, W, 0.008, t["bd"])

    if label:
        textbox(slide, LM, FY + 0.05, CW * 0.7, 0.2,
                label, 7.5, color=t["mt"], font=t["bfont"])
    textbox(slide, 0, FY + 0.05, W - RM, 0.2,
            f"{pg} / {total}", 7.5, color=t["mt"],
            align=PP_ALIGN.RIGHT, font=t["bfont"])


# ── Card primitives ───────────────────────────────────────────────────────────
def draw_card(slide, t, x, y, w, h, accent_color=None):
    """Theme-aware card treatment."""
    if is_exhibit(t):
        card(slide, x, y, w, h, t["cbg"], shadow=False, radius=0.0)
        frame = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
        frame.fill.background()
        frame.line.color.rgb = rgb(t["bd"])
        frame.line.width = Pt(0.7)
        rect(slide, x, y, w, 0.015, accent_color or t["ac2"])
        return
    if is_mckinsey(t):
        card(slide, x, y, w, h, "FFFFFF", shadow=False, radius=0.0)
        frame = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
        frame.fill.background()
        frame.line.color.rgb = rgb(t["bd"])
        frame.line.width = Pt(0.7)
        rect(slide, x, y, w, 0.012, accent_color or t["ac1"])
        return
    if is_google(t):
        card(slide, x, y, w, h, "FFFFFF", shadow=False, radius=0.10)
        frame = slide.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
        frame.fill.background()
        frame.line.color.rgb = rgb("E8EAED")
        frame.line.width = Pt(0.9)
        if accent_color:
            rect(slide, x, y, w, 0.018, accent_color)
        return
    card(slide, x, y, w, h, t["cbg"], shadow=False, radius=0.04)
    frame = slide.shapes.add_shape(
        MSO_SHAPE.ROUNDED_RECTANGLE,
        Inches(x), Inches(y), Inches(w), Inches(h)
    )
    frame.fill.background()
    frame.line.color.rgb = rgb(t["bd"])
    frame.line.width = Pt(0.55)
    if accent_color:
        rect(slide, x, y, w, STRIPE, accent_color)


# ── Block renderers ───────────────────────────────────────────────────────────

def blk_bullets(slide, t, b, x, y, w, h):
    items = b.get("items", [])
    if not items:
        raise ValueError("bullets block requires non-empty items")
    n = len(items)
    accents = accent_palette(t)

    if is_google(t) and b.get("variant", "list") != "cards":
        row_gap = 0.05
        row_h = (h - (n - 1) * row_gap) / n
        font_size = 11.6 if n <= 5 and w >= 6 else (10.6 if n <= 6 else 9.6)
        for i, item in enumerate(items):
            cy = y + i * (row_h + row_gap)
            color = semantic_accent(t, index=i)
            marker_h = min(0.40, max(0.16, row_h * 0.58))
            rect(slide, x, cy + max(0.03, (row_h - marker_h) / 2), 0.045, marker_h, color)
            textbox(slide, x + 0.14, cy + 0.01, w - 0.18, row_h - 0.02,
                    str(item), font_size, color=t["tx"], font=t["bfont"], wrap=True,
                    vertical_anchor=MSO_ANCHOR.MIDDLE if n == 1 else None)
            if i < n - 1:
                rect(slide, x + 0.14, cy + row_h, w - 0.14, 0.008, t["bd"])
        return

    if is_exhibit(t):
        ih = (h - (n - 1) * 0.08) / n
        for i, item in enumerate(items):
            cy = y + i * (ih + 0.08)
            rect(slide, x, cy + 0.02, 0.03, ih - 0.04, semantic_accent(t, index=i))
            textbox(slide, x + 0.10, cy, w - 0.14, ih,
                    item, 10.2, color=t["tx"], font=t["bfont"], wrap=True)
        return

    if is_mckinsey(t):
        ih = (h - (n - 1) * 0.06) / n
        for i, item in enumerate(items):
            cy = y + i * (ih + 0.06)
            rect(slide, x, cy + 0.05, 0.02, 0.02, semantic_accent(t, index=i))
            textbox(slide, x + 0.08, cy, w - 0.10, ih,
                    item, 9.8, color=t["tx"], font=t["bfont"], wrap=True)
        return

    # Compact grid: short statements should not be stretched into giant empty
    # cards. Keep the occupied area proportional to the amount of content.
    if n <= 3:
        cols = n
    elif n <= 6:
        cols = 2 if n == 4 else 3
    else:
        cols = 2
    rows = (n + cols - 1) // cols
    cw = (w - (cols - 1) * GAP) / cols
    available_h = (h - (rows - 1) * GAP) / rows
    max_card_h = 1.75 if rows == 1 else 1.25
    ih = min(available_h, max_card_h)
    grid_h = rows * ih + (rows - 1) * GAP
    start_y = y + max(0, (h - grid_h) / 2)
    font_size = 14 if n <= 3 else (12.5 if n <= 6 else 11.5)

    for i, item in enumerate(items):
        row = i // cols
        col = i % cols
        cx = x + col * (cw + GAP)
        cy = start_y + row * (ih + GAP)
        draw_card(slide, t, cx, cy, cw, ih, semantic_accent(t, index=i))
        textbox(slide, cx + 0.15, cy + STRIPE + 0.10,
                cw - 0.30, ih - STRIPE - 0.18,
                str(item), font_size, color=t["tx"], font=t["bfont"], wrap=True)


def blk_kpi(slide, t, b, x, y, w, h):
    metrics = b.get("metrics", [])
    if not metrics:
        raise ValueError("kpi block requires non-empty metrics")
    n = len(metrics)
    cw = (w - (n - 1) * GAP) / n
    accents = accent_palette(t)

    if is_google(t):
        for i, metric in enumerate(metrics):
            cx = x + i * (cw + GAP)
            color = semantic_accent(t, metric, i)
            if i > 0:
                rect(slide, cx - GAP / 2, y + 0.10, 0.008, h - 0.20, t["bd"])
            rect(slide, cx + 0.08, y + 0.08, min(0.52, cw * 0.38), 0.035, color)
            value = str(metric.get("value", ""))
            label = str(metric.get("label", ""))
            sub = str(metric.get("sub", ""))
            value_units = max(1.0, visual_text_units(value))
            value_size = round(min(30.0, max(14.0, (cw - 0.22) * 72.0 / (value_units * 1.15))), 1)
            textbox(slide, cx + 0.04, y + 0.20, cw - 0.08, h * 0.36,
                    value, value_size, bold=True, color=accent_text_color(t, color),
                    align=PP_ALIGN.CENTER, font=t["hfont"], wrap=False)
            textbox(slide, cx + 0.08, y + h * 0.52, cw - 0.16, h * 0.20,
                    label, 8.8, bold=True, color=t["tx"], align=PP_ALIGN.CENTER, font=t["bfont"], wrap=True)
            if sub:
                textbox(slide, cx + 0.08, y + h * 0.77, cw - 0.16, h * 0.14,
                        sub, 7.4, color=t["mt"], align=PP_ALIGN.CENTER, font=t["bfont"], wrap=True)
        return

    if is_exhibit(t):
        for i, m in enumerate(metrics):
            cx = x + i * (cw + GAP)
            if i > 0:
                rect(slide, cx - GAP / 2, y + 0.08, 0.01, h - 0.16, t["bd"])
            val = str(m.get("value", ""))
            label = str(m.get("label", ""))
            sub = str(m.get("sub", ""))
            textbox(slide, cx, y + 0.10, cw, h * 0.38,
                    val, 26 if len(val) > 6 else 30, bold=True, color=semantic_accent(t, m, i),
                    align=PP_ALIGN.CENTER, font=t["hfont"])
            textbox(slide, cx + 0.02, y + h * 0.48, cw - 0.04, h * 0.16,
                    label, 8.4, bold=True, color=t["tx"], align=PP_ALIGN.CENTER, font=t["bfont"])
            if sub:
                textbox(slide, cx + 0.02, y + h * 0.70, cw - 0.04, h * 0.14,
                        sub, 7.4, color=t["mt"], align=PP_ALIGN.CENTER, font=t["bfont"])
        return

    if is_mckinsey(t):
        for i, m in enumerate(metrics):
            cx = x + i * (cw + GAP)
            rect(slide, cx, y, cw, h, "FFFFFF", line=t["bd"])
            rect(slide, cx, y, cw, 0.012, semantic_accent(t, m, i))
            val = str(m.get("value", ""))
            label = str(m.get("label", ""))
            sub = str(m.get("sub", ""))
            textbox(slide, cx + 0.05, y + 0.10, cw - 0.10, h * 0.34,
                    val, 24 if len(val) > 6 else 28, bold=True, color=t["ac1"],
                    align=PP_ALIGN.CENTER, font=t["hfont"])
            textbox(slide, cx + 0.05, y + h * 0.46, cw - 0.10, h * 0.16,
                    label, 8.2, bold=True, color=t["tx"], align=PP_ALIGN.CENTER, font=t["bfont"])
            if sub:
                textbox(slide, cx + 0.05, y + h * 0.68, cw - 0.10, h * 0.12,
                        sub, 7.2, color=t["mt"], align=PP_ALIGN.CENTER, font=t["bfont"])
        return

    for i, m in enumerate(metrics):
        cx = x + i * (cw + GAP)
        draw_card(slide, t, cx, y, cw, h, semantic_accent(t, m, i))

        val = str(m.get("value", ""))
        label = str(m.get("label", ""))
        sub = str(m.get("sub", ""))

        # Big number
        vsz = 32 if len(val) <= 5 else 26
        textbox(slide, cx + 0.1, y + STRIPE + 0.12,
                cw - 0.2, h * 0.48,
                val, vsz, bold=True, color=semantic_accent(t, m, i),
                align=PP_ALIGN.CENTER, font=t["hfont"])
        # Label
        textbox(slide, cx + 0.08, y + STRIPE + h * 0.54,
                cw - 0.16, h * 0.24,
                label, 8.5, bold=True, color=t["tx"],
                align=PP_ALIGN.CENTER, font=t["bfont"])
        # Sub description
        if sub:
            textbox(slide, cx + 0.08, y + STRIPE + h * 0.76,
                    cw - 0.16, h * 0.2,
                    sub, 7.5, color=t["mt"],
                    align=PP_ALIGN.CENTER, font=t["bfont"])


def blk_table(slide, t, b, x, y, w, h):
    headers = b.get("headers", [])
    rows = b.get("rows", [])
    if not headers or not rows:
        raise ValueError("table block requires headers and rows")
    ncols = len(headers)
    nrows = len(rows)
    total_rows = nrows + 1
    longest = max((len(str(value)) for row in rows for value in row), default=0)
    desired_rh = 0.36 + min(longest, 120) / 360.0
    rh = min(h / total_rows, max(0.34, desired_rh))
    body_font_size = 6.6 if ncols >= 5 else (7.2 if ncols >= 4 else 8.0)

    tbl = slide.shapes.add_table(
        total_rows, ncols,
        Inches(x), Inches(y), Inches(w), Inches(total_rows * rh)
    ).table

    for ci, hdr in enumerate(headers):
        cell = tbl.cell(0, ci)
        cell.text = str(hdr)
        cell.margin_left = Pt(3); cell.margin_right = Pt(3)
        cell.margin_top = Pt(2); cell.margin_bottom = Pt(2)
        for p in cell.text_frame.paragraphs:
            p.font.size = Pt(8.5); p.font.bold = True
            p.font.color.rgb = rgb("FFFFFF" if is_google(t) else t["htx"])
            p.font.name = t["hfont"]
            p.alignment = PP_ALIGN.CENTER
        cell.fill.solid(); cell.fill.fore_color.rgb = rgb("4285F4" if is_google(t) else t["hbg"])

    for ri, row in enumerate(rows):
        for ci, val in enumerate(row):
            cell = tbl.cell(ri + 1, ci)
            cell.text = str(val)
            cell.margin_left = Pt(3); cell.margin_right = Pt(3)
            cell.margin_top = Pt(2); cell.margin_bottom = Pt(2)
            for p in cell.text_frame.paragraphs:
                p.font.size = Pt(body_font_size); p.font.color.rgb = rgb(t["tx"])
                p.font.name = t["bfont"]
                p.alignment = PP_ALIGN.LEFT if ci > 0 else PP_ALIGN.CENTER
            cell.fill.solid()
            if is_exhibit(t):
                cell.fill.fore_color.rgb = rgb(t["cbg"]) if ri % 2 == 0 else rgb(t["hbg"])
            elif is_google(t):
                cell.fill.fore_color.rgb = rgb("F8F9FA") if ri % 2 == 0 else rgb("FFFFFF")
            else:
                cell.fill.fore_color.rgb = rgb(t["cbg"]) if ri % 2 == 0 else rgb("FFFFFF")


def _render_chart_mpl(categories, series_list, chart_type, title, w_inch, h_inch, t, dpi=150):
    """Render chart with matplotlib, return PNG bytes. Handles Chinese reliably."""
    fig, ax = plt.subplots(figsize=(w_inch, h_inch), dpi=dpi)
    fig.patch.set_facecolor('#ffffff')
    ax.set_facecolor('#ffffff' if is_google(t) else '#fafbfc')

    n = len(categories)
    x = np.arange(n) if chart_type != "pie" else None
    theme_colors = [f"#{color}" for color in accent_palette(t)]
    colors = [theme_colors[index % len(theme_colors)] for index in range(max(n, len(series_list), 1))]

    if chart_type == "pie":
        vals = [s.get("values", [0])[0] if isinstance(s.get("values", []), list) else s.get("values", 0) for s in series_list]
        labels = [s.get("name", "") for s in series_list]
        wedges, texts, autotexts = ax.pie(vals, labels=None, colors=colors[:len(vals)],
            autopct='%1.1f%%', startangle=90, pctdistance=0.75)
        for t in autotexts:
            t.set_fontsize(9); t.set_fontweight('bold')
        ax.legend(wedges, labels, loc='lower center', ncol=2, fontsize=8, prop=_MPL_FONT)
    elif chart_type == "line":
        for i, sr in enumerate(series_list):
            vals = sr.get("values", [])
            name = sr.get("name", f"Series {i+1}")
            ax.plot(x, vals, 'o-', color=colors[i], lw=2.5, markersize=7,
                    markerfacecolor='white', markeredgewidth=2, label=name, zorder=3)
        ax.set_xticks(x); ax.set_xticklabels(categories, fontproperties=_MPL_FONT, fontsize=9)
        ax.legend(fontsize=9, prop=_MPL_FONT); ax.grid(axis='y', alpha=0.3, lw=0.5)
    else:  # bar (default)
        total_bars = len(series_list)
        bar_w = 0.8 / total_bars if total_bars > 1 else 0.55
        for i, sr in enumerate(series_list):
            vals = sr.get("values", [])
            name = sr.get("name", f"Series {i+1}")
            offset = (i - (total_bars - 1) / 2) * bar_w if total_bars > 1 else 0
            bar_colors = [colors[index % len(colors)] for index in range(len(vals))] if total_bars == 1 and is_google(t) else colors[i]
            bars = ax.bar(x + offset, vals, bar_w, color=bar_colors, alpha=0.92, label=name, zorder=3)
            for bar in bars:
                h = bar.get_height()
                ax.text(bar.get_x() + bar.get_width() / 2, h + max(vals) * 0.01,
                        str(h) if isinstance(h, (int, float)) and h == int(h) else f'{h:.1f}',
                        ha='center', va='bottom', fontsize=8, fontweight='bold', color=f"#{t['tx']}")
        ax.set_xticks(x); ax.set_xticklabels(categories, fontproperties=_MPL_FONT, fontsize=9)
        if total_bars > 1:
            ax.legend(fontsize=9, prop=_MPL_FONT)
        ax.grid(axis='y', alpha=0.3, lw=0.5)

    ax.spines[['top', 'right']].set_visible(False)
    ax.tick_params(labelsize=8)
    if title:
        ax.set_title(title, fontsize=13, fontweight='bold', color=f"#{t['tx']}",
                     fontproperties=_MPL_FONT, pad=12)

    buf = io.BytesIO()
    fig.savefig(buf, format='png', dpi=dpi, bbox_inches='tight', facecolor='#ffffff', edgecolor='none')
    plt.close(fig)
    buf.seek(0)
    return buf


def blk_chart(slide, t, b, x, y, w, h):
    """Render chart block. Priority: 1) pre-rendered image, 2) inline data via matplotlib, 3) auto-chart from table data."""
    content = b.get("content", b) if isinstance(b, dict) else {}
    img_path = content.get("chart", "") if isinstance(content, dict) else ""

    # ── Path 1: pre-rendered image ──
    if img_path and os.path.exists(img_path):
        add_picture_contain(slide, img_path, x, y, w, h, background="FFFFFF", border=t["bd"], font=t["bfont"], text_color=t["mt"])
        return
    if img_path:
        resolved = img_path
        if not os.path.exists(resolved):
            for prefix in ["storage/", "output/", "charts/", "images/"]:
                if resolved.startswith(prefix):
                    alt = resolved[len(prefix):]
                    if os.path.exists(alt):
                        resolved = alt
                        break
        if not os.path.exists(resolved):
            for base in ["storage", "output", ".", "skills/base-ppt"]:
                alt = os.path.join(base, img_path)
                if os.path.exists(alt):
                    resolved = alt
                    break
        if os.path.exists(resolved):
            add_picture_contain(slide, resolved, x, y, w, h, background="FFFFFF", border=t["bd"], font=t["bfont"], text_color=t["mt"])
            return

    # ── Path 2: inline data → matplotlib chart ──
    categories = content.get("categories", [])
    series = content.get("series", [])
    chart_type = content.get("chart_type", "bar")
    chart_title = content.get("title", "")

    # ── Path 3: auto-chart from parent slide's table data ──
    if (not categories or not series) and "rows" in b:
        rows = b.get("rows", [])
        headers = b.get("headers", [])
        if rows and headers and len(headers) >= 2 and len(rows) >= 1:
            categories = [r[0] for r in rows]
            for ci in range(1, len(headers)):
                series.append({
                    "name": headers[ci],
                    "values": [float(str(r[ci]).replace(',', '').replace('%', '').replace('$', '').replace('亿', '').replace('万', '')) if ci < len(r) else 0 for r in rows]
                })

    if not categories or not series:
        raise ValueError("chart block requires direct categories and series fields")

    # Native editable chart route. The default remains the higher-fidelity
    # raster path, but management decks can explicitly request OOXML chart data
    # that users may edit in PowerPoint.
    if content.get("native_editable", False):
        try:
            cd = CategoryChartData()
            cd.categories = categories
            for sr in series:
                cd.add_series(sr.get("name", ""), sr.get("values", []))
            chart_types = {
                "bar": XL_CHART_TYPE.COLUMN_CLUSTERED,
                "line": XL_CHART_TYPE.LINE_MARKERS,
                "pie": XL_CHART_TYPE.PIE,
            }
            chart_frame = slide.shapes.add_chart(
                chart_types.get(chart_type, XL_CHART_TYPE.COLUMN_CLUSTERED),
                Inches(x), Inches(y), Inches(w), Inches(h), cd
            )
            chart = chart_frame.chart
            chart.has_title = bool(chart_title)
            if chart_title:
                chart.chart_title.text_frame.text = chart_title
                for p in chart.chart_title.text_frame.paragraphs:
                    p.font.color.rgb = rgb(t["tx"])
                    p.font.name = t["hfont"]
            chart.has_legend = len(series) > 1
            try:
                chart.category_axis.tick_labels.font.color.rgb = rgb(t["tx"])
                chart.category_axis.tick_labels.font.name = t["bfont"]
                chart.category_axis.format.line.color.rgb = rgb(t["bd"])
                chart.value_axis.tick_labels.font.color.rgb = rgb(t["tx"])
                chart.value_axis.tick_labels.font.name = t["bfont"]
                chart.value_axis.format.line.color.rgb = rgb(t["bd"])
                chart.value_axis.major_gridlines.format.line.color.rgb = rgb(t["bd"])
            except Exception:
                pass
            for i, sr in enumerate(chart.series):
                try:
                    color = semantic_accent(t, index=i)
                    sr.format.fill.solid()
                    sr.format.fill.fore_color.rgb = rgb(color)
                    sr.format.line.color.rgb = rgb(color)
                    sr.format.line.width = Pt(2.5)
                    sr.marker.format.fill.solid()
                    sr.marker.format.fill.fore_color.rgb = rgb(color)
                    sr.marker.format.line.color.rgb = rgb(color)
                except Exception:
                    pass
            try:
                chart.plots[0].has_data_labels = True
                chart.plots[0].data_labels.font.size = Pt(8)
                chart.plots[0].data_labels.font.color.rgb = rgb(t["tx"])
            except Exception:
                pass
            return
        except Exception as e:
            print(f"[warn] native editable chart failed; falling back to raster: {e}", file=sys.stderr)

    # Render chart with matplotlib and embed as image
    try:
        png_buf = _render_chart_mpl(categories, series, chart_type, chart_title,
                                    w_inch=w, h_inch=h, t=t, dpi=150)
        slide.shapes.add_picture(png_buf, Inches(x), Inches(y), Inches(w), Inches(h))
    except Exception as e:
        # Last resort: native pptx chart (may have issues with Chinese)
        try:
            cd = CategoryChartData()
            cd.categories = categories
            for sr in series:
                cd.add_series(sr.get("name", ""), sr.get("values", []))
            ct_map = {"bar": 5, "line": 8, "pie": 17}
            cf = slide.shapes.add_chart(
                ct_map.get(chart_type, 5),
                Inches(x), Inches(y), Inches(w), Inches(h), cd
            )
            chart = cf.chart
            for i, sr in enumerate(chart.series):
                sr.format.fill.solid()
                sr.format.fill.fore_color.rgb = rgb(semantic_accent(t, index=i))
            try:
                chart.plots[0].has_data_labels = True
                chart.plots[0].data_labels.font.size = Pt(7)
            except Exception:
                pass
        except Exception:
            textbox(slide, x + 0.3, y + h / 2 - 0.2, w - 0.6, 0.4,
                    f"图表生成失败: {str(e)[:60]}", 10,
                    color="#ef4444", align=PP_ALIGN.CENTER, font=t["bfont"])


def blk_text(slide, t, b, x, y, w, h):
    body = b.get("body", "")
    heading = b.get("heading", "")
    if not body and not heading:
        raise ValueError("text block requires heading or body")
    box_h = min(h, 2.2)
    box_y = y + max(0, (h - box_h) / 2)
    if is_google(t) and b.get("variant", "editorial") != "card":
        color = semantic_accent(t, b, 0)
        rect(slide, x, box_y + 0.05, 0.055, box_h - 0.10, color)
        cy = box_y + 0.06
        if heading:
            textbox(slide, x + 0.18, cy, w - 0.22, 0.34,
                    heading, 16.5, bold=True, color=t["tx"], font=t["hfont"], wrap=True)
            cy += 0.46
        if body:
            textbox(slide, x + 0.18, cy, w - 0.22, box_h - (cy - box_y) - 0.08,
                    body, 13.2, color=t["tx"], font=t["bfont"], wrap=True)
        return
    draw_card(slide, t, x, box_y, w, box_h, t["ac1"])
    cy = box_y + STRIPE + 0.12
    if heading:
        textbox(slide, x + 0.15, cy, w - 0.3, 0.3,
                heading, 16, bold=True, color=t["tx"], font=t["hfont"])
        cy += 0.40
    if body:
        textbox(slide, x + 0.15, cy, w - 0.3, box_h - (cy - box_y) - 0.12,
                body, 14, color=t["tx"], font=t["bfont"], wrap=True)


def blk_quote(slide, t, b, x, y, w, h):
    text = b.get("text", "")
    author = b.get("author", "")
    rect(slide, x, y, w, h, t["hbg"])
    rect(slide, x, y, 0.06, h, t["ac1"])  # left accent bar
    qy = y + h * 0.15
    textbox(slide, x + 0.2, qy, w - 0.35, h * 0.6,
            f"\u201c{text}\u201d", 12, italic=True,
            color=t["htx"], font=t["hfont"], wrap=True)
    if author:
        textbox(slide, x + 0.2, y + h * 0.78, w - 0.35, h * 0.18,
                f"\u2014 {author}", 8.5, color=blend_hex(t["htx"], t["hbg"], 0.60),
                font=t["bfont"])


def blk_image(slide, t, b, x, y, w, h):
    path = b.get("path", "")
    if not path or not os.path.exists(path):
        raise ValueError(f"image block path not found: {path or '<empty>'}")
    iw = min(b.get("width", w) or w, w)
    ih = min(b.get("height", h) or h, h)
    add_picture_contain(
        slide, path, x, y, iw, ih,
        background="FFFFFF" if is_google(t) else t["pbg"],
        border=t["bd"], caption=str(b.get("caption", "")),
        font=t["bfont"], text_color=t["mt"],
    )


BLK = {
    "bullets": blk_bullets,
    "kpi":     blk_kpi,
    "table":   blk_table,
    "chart":   blk_chart,
    "text":    blk_text,
    "quote":   blk_quote,
    "image":   blk_image,
}

SUPPORTED_SLIDE_TYPES = {"title", "section", "content", "end"}
SUPPORTED_BLOCK_TYPES = set(BLK)
UNSUPPORTED_DIAGRAM_TYPES = {
    "diagram", "flow", "flowchart", "architecture", "network",
    "relationship", "mindmap", "timeline-diagram",
}
PLACEHOLDER_MARKERS = (
    "[name]", "[website]", "[contact]", "[company]", "[title]",
    "tbd", "todo", "待补充", "placeholder",
)


def _workflow_error(slide_no, message):
    return ValueError(f"slide {slide_no}: {message}")


def _normalize_chart_block(block, slide_no):
    original_type = str(block.get("type", "chart")).strip().lower()
    if original_type != "chart":
        block.setdefault("chart_type", original_type)
        block["type"] = "chart"

    content = block.pop("content", None)
    if isinstance(content, dict):
        for key, value in content.items():
            block.setdefault(key, value)

    data = block.pop("data", None)
    if isinstance(data, dict):
        for key, value in data.items():
            block.setdefault(key, value)

    chart_type = str(block.get("chart_type", "bar")).strip().lower()
    diagram_keys = {"nodes", "edges", "steps", "links"}
    if chart_type in UNSUPPORTED_DIAGRAM_TYPES or any(key in block for key in diagram_keys):
        raise _workflow_error(
            slide_no,
            f"native base-ppt does not support {chart_type or 'diagram'}; "
            "switch to a task-specific python-pptx/PptxGenJS renderer",
        )
    if not block.get("categories") or not block.get("series"):
        raise _workflow_error(
            slide_no,
            "chart requires direct categories and series fields; do not ship a placeholder",
        )
    block["type"] = "chart"
    return block


def _normalize_block(block, slide_no):
    if not isinstance(block, dict):
        raise _workflow_error(slide_no, "every block must be an object")
    block = dict(block)
    block_type = str(block.get("type", "")).strip().lower()
    if block_type in UNSUPPORTED_DIAGRAM_TYPES:
        raise _workflow_error(
            slide_no,
            f"native base-ppt does not support {block_type}; switch to a task-specific renderer",
        )
    if block_type == "chart":
        return _normalize_chart_block(block, slide_no)
    if block_type not in SUPPORTED_BLOCK_TYPES:
        raise _workflow_error(slide_no, f"unsupported block type: {block_type or '<missing>'}")

    if block_type == "kpi" and not block.get("metrics") and block.get("items"):
        block["metrics"] = block.pop("items")
    if block_type == "text" and not block.get("body"):
        content = block.pop("content", None)
        if isinstance(content, str) and content.strip():
            block["body"] = content

    required = {
        "bullets": ("items",),
        "kpi": ("metrics",),
        "table": ("headers", "rows"),
        "text": (),
        "quote": ("text",),
        "image": ("path",),
    }
    for field in required.get(block_type, ()):
        if not block.get(field):
            raise _workflow_error(slide_no, f"{block_type} block requires {field}")
    if block_type == "text" and not (block.get("heading") or block.get("body")):
        raise _workflow_error(slide_no, "text block requires heading or body")
    if block_type == "bullets" and len(block.get("items", [])) > 8:
        raise _workflow_error(slide_no, "bullets block has more than 8 items; split the slide")
    return block


def normalize_workflow(workflow):
    if not isinstance(workflow, dict):
        raise ValueError("workflow must be a JSON object")
    workflow = dict(workflow)
    meta = dict(workflow.get("meta") or {})
    theme = str(meta.get("theme", "charcoal")).strip()
    if theme not in THEMES:
        raise ValueError(f"unsupported theme: {theme}")
    meta["theme"] = theme
    workflow["meta"] = meta

    slides = workflow.get("slides")
    if not isinstance(slides, list) or not slides:
        raise ValueError("workflow requires a non-empty slides array")

    normalized_slides = []
    aliases = {"cover": "title", "ending": "end"}
    for index, original in enumerate(slides, 1):
        if not isinstance(original, dict):
            raise _workflow_error(index, "slide must be an object")
        slide = dict(original)
        slide_type = str(slide.get("type", "content")).strip().lower()
        slide_type = aliases.get(slide_type, slide_type)
        if slide_type not in SUPPORTED_SLIDE_TYPES:
            raise _workflow_error(index, f"unsupported slide type: {slide_type}")
        slide["type"] = slide_type
        if not str(slide.get("title", "")).strip():
            raise _workflow_error(index, "title is required")

        if slide_type == "content":
            blocks = slide.get("blocks")
            if blocks is None:
                blocks = []
                if slide.get("bullets"):
                    blocks.append({"type": "bullets", "items": slide.pop("bullets")})
                if slide.get("kpi"):
                    metrics = slide.pop("kpi")
                    if isinstance(metrics, dict):
                        metrics = metrics.get("metrics") or metrics.get("items") or []
                    blocks.append({"type": "kpi", "metrics": metrics})
                if slide.get("table"):
                    table = dict(slide.pop("table"))
                    table["type"] = "table"
                    blocks.append(table)
                if slide.get("chart"):
                    chart = dict(slide.pop("chart"))
                    chart_kind = str(chart.get("type", "chart")).strip().lower()
                    chart["type"] = "chart"
                    if chart_kind != "chart":
                        chart.setdefault("chart_type", chart_kind)
                    blocks.append(chart)
                if isinstance(slide.get("content"), str) and slide["content"].strip():
                    blocks.append({"type": "text", "body": slide.pop("content")})
            if not isinstance(blocks, list):
                raise _workflow_error(index, "content slide blocks must be an array")
            if not blocks and not slide.get("template_fields"):
                raise _workflow_error(index, "content slide requires non-empty blocks")

            normalized_blocks = []
            for block in blocks:
                if isinstance(block, list):
                    normalized_blocks.append([_normalize_block(item, index) for item in block])
                else:
                    normalized_blocks.append(_normalize_block(block, index))
            slide["blocks"] = normalized_blocks
        normalized_slides.append(slide)

    encoded = json.dumps(normalized_slides, ensure_ascii=False).lower()
    for marker in PLACEHOLDER_MARKERS:
        if marker in encoded:
            raise ValueError(f"unresolved placeholder is not allowed: {marker}")

    workflow["slides"] = normalized_slides
    return workflow


# ── Special slide types ───────────────────────────────────────────────────────

def slide_title(slide, t, d):
    if is_exhibit(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac1"])
        rect(slide, 0, 0.05, W, 0.015, t["ac2"])
        draw_soft_grid(slide, t["ac2"], t["hbg"], x=7.1, y=0.7, w=2.2, h=3.6, cols=5, rows=8, alpha_hex="20")
        rect(slide, LM, 1.05, 0.05, 2.05, t["ac2"])
        eyebrow = d.get("eyebrow", "")
        if eyebrow:
            textbox(slide, LM + 0.18, 1.02, 4.2, 0.16,
                    eyebrow.upper(), 8, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])
        textbox(slide, LM + 0.18, 1.34, 5.8, 1.05,
                d.get("title", ""), 30, bold=True, color=t["htx"], font=t["hfont"])
        sub = d.get("subtitle", "")
        if sub:
            textbox(slide, LM + 0.18, 2.58, 5.4, 0.45,
                    sub, 12, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])
        tags = d.get("tags", [])
        tx = LM + 0.18
        for tag in tags[:4]:
            tw = len(tag) * 0.10 + 0.28
            rect(slide, tx, 3.25, tw, 0.03, t["ac2"])
            textbox(slide, tx, 3.34, tw, 0.14,
                    tag, 8, color=blend_hex(t["htx"], t["hbg"], 0.80), font=t["bfont"])
            tx += tw + 0.18
        return

    if is_mckinsey(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac2"])
        rect(slide, LM, 1.00, 0.06, 2.45, t["ac2"])
        eyebrow = d.get("eyebrow", "")
        if eyebrow:
            textbox(slide, LM + 0.18, 1.00, 5.0, 0.16,
                    eyebrow.upper(), 8, color=t["ac2"], font=t["bfont"])
        textbox(slide, LM + 0.18, 1.38, 7.9, 1.10,
                d.get("title", ""), 30, bold=True, color=t["htx"], font=t["hfont"], wrap=True)
        sub = d.get("subtitle", "")
        if sub:
            textbox(slide, LM + 0.18, 2.62, 7.4, 0.48,
                    sub, 12, color="D7DEEA", font=t["bfont"], wrap=True)
        tags = d.get("tags", [])
        tx = LM + 0.18
        for tag in tags[:4]:
            tw = min(1.7, max(0.62, len(str(tag)) * 0.095 + 0.28))
            rect(slide, tx, 3.42, tw, 0.028, t["ac2"])
            textbox(slide, tx, 3.53, tw, 0.20,
                    str(tag), 8, color=t["htx"], font=t["bfont"])
            tx += tw + 0.18
        return

    if is_google(t):
        set_bg(slide, "FFFFFF")
        rect(slide, 0, 0, W * 0.25, 0.05, "4285F4")
        rect(slide, W * 0.25, 0, W * 0.2, 0.05, "EA4335")
        rect(slide, W * 0.45, 0, W * 0.15, 0.05, "FBBC04")
        rect(slide, W * 0.60, 0, W * 0.40, 0.05, "34A853")
        rect(slide, 7.28, 0.92, 1.42, 1.42, "E8F0FE")
        rect(slide, 8.82, 0.92, 0.48, 1.42, "FCE8E6")
        rect(slide, 7.28, 2.48, 0.64, 0.64, "FEF7E0")
        rect(slide, 8.05, 2.48, 1.25, 0.64, "E6F4EA")
        rect(slide, 7.28, 3.27, 2.02, 0.06, "202124")
        eyebrow = d.get("eyebrow", "")
        if eyebrow:
            textbox(slide, 0.82, 1.12, 5.8, 0.18,
                    eyebrow.upper(), 8, color="4285F4", font=t["bfont"])
        title = d.get("title", "")
        title_size = fitted_font_size(title, 5.95, 32, 23, max_lines=2)
        textbox(slide, 0.82, 1.50, 5.95, 1.28,
                title, title_size, bold=True, color=t["tx"], font=t["hfont"], wrap=True)
        sub = d.get("subtitle", "")
        if sub:
            textbox(slide, 0.82, 2.94, 5.85, 0.50,
                    sub, 11.5, color=t["mt"], font=t["bfont"], wrap=True)
        tags = [str(tag) for tag in d.get("tags", [])[:4]]
        tag_x = 0.82
        for index, tag in enumerate(tags):
            width = min(1.55, max(0.64, visual_text_units(tag) * 0.10 + 0.28))
            color = semantic_accent(t, index=index)
            rect(slide, tag_x, 3.72, width, 0.025, color)
            textbox(slide, tag_x, 3.82, width, 0.20, tag, 8.2, color=t["tx"], font=t["bfont"], wrap=False)
            tag_x += width + 0.18
        return

    set_bg(slide, t["hbg"])
    rect(slide, 0, 0, W, 0.035, t["ac1"])
    rect(slide, LM, 0.92, 0.045, 3.30, t["ac1"])

    eyebrow = d.get("eyebrow", "")
    ty = 1.02
    if eyebrow:
        textbox(slide, LM + 0.18, ty, 7.5, 0.22,
                eyebrow.upper(), 8, color=blend_hex(t["htx"], t["hbg"], 0.62),
                font=t["bfont"])
        ty = 1.34

    title = d.get("title", "")
    title_size = fitted_font_size(title, 7.8, 34, 24, max_lines=2)
    textbox(slide, LM + 0.18, ty, 7.8, 1.34,
            title, title_size, bold=True, color=t["htx"], font=t["hfont"], wrap=True)

    sub = d.get("subtitle", "")
    if sub:
        textbox(slide, LM + 0.18, ty + 1.52, 7.4, 0.54,
                sub, 12, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])

    tags = d.get("tags", [])
    if tags:
        textbox(
            slide, LM + 0.18, ty + 2.30, 7.8, 0.26,
            "  /  ".join(str(tag) for tag in tags[:5]), 8.5,
            color=blend_hex(t["htx"], t["hbg"], 0.58), font=t["bfont"]
        )


def slide_section(slide, t, d):
    if is_exhibit(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac1"])
        rect(slide, 0, 0.05, W, 0.015, t["ac2"])
        draw_soft_grid(slide, t["ac2"], t["hbg"], x=7.0, y=0.55, w=2.4, h=4.0, cols=6, rows=10, alpha_hex="18")
        num = d.get("number", "")
        if num:
            textbox(slide, LM, 0.75, 2.0, 1.0, str(num), 72, bold=True,
                    color=blend_hex(t["htx"], t["hbg"], 0.10), font=t["hfont"])
        rect(slide, LM, 2.10, 0.05, 1.15, t["ac2"])
        textbox(slide, LM + 0.18, 2.00, 5.8, 0.55,
                d.get("title", ""), 26, bold=True, color=t["htx"], font=t["hfont"])
        if d.get("subtitle"):
            textbox(slide, LM + 0.18, 2.68, 5.0, 0.32,
                    d["subtitle"], 11, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])
        return

    if is_mckinsey(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac2"])
        num = d.get("number", "")
        if num:
            textbox(slide, 7.25, 1.20, 1.7, 1.1, str(num), 64, bold=True, color="243A5A", align=PP_ALIGN.RIGHT, font=t["hfont"])
        rect(slide, LM, 1.55, 0.06, 0.90, t["ac2"])
        textbox(slide, LM + 0.18, 1.40, 5.6, 0.44,
                d.get("title", ""), 25, bold=True, color=t["htx"], font=t["hfont"])
        if d.get("subtitle"):
            textbox(slide, LM + 0.18, 2.00, 5.0, 0.30,
                    d["subtitle"], 10.5, color="D7DEEA", font=t["bfont"])
        return

    if is_google(t):
        set_bg(slide, "FFFFFF")
        rect(slide, 0, 0, W * 0.25, 0.05, "4285F4")
        rect(slide, W * 0.25, 0, W * 0.20, 0.05, "EA4335")
        rect(slide, W * 0.45, 0, W * 0.15, 0.05, "FBBC04")
        rect(slide, W * 0.60, 0, W * 0.40, 0.05, "34A853")
        rect(slide, 0.82, 1.20, 0.055, 2.65, "4285F4")
        rect(slide, 0.98, 1.20, 0.025, 1.40, "EA4335")
        rect(slide, 1.10, 1.20, 0.025, 0.92, "FBBC04")
        rect(slide, 1.22, 1.20, 0.025, 2.10, "34A853")
        number = str(d.get("number", ""))
        if number:
            textbox(slide, 1.55, 0.88, 1.65, 1.15, number, 66, bold=True,
                    color="D2E3FC", font=t["hfont"], wrap=False)
        title = d.get("title", "")
        title_size = fitted_font_size(title, 6.2, 29, 21, max_lines=2)
        textbox(slide, 2.42, 1.62, 6.15, 0.92, title, title_size, bold=True,
                color=t["tx"], font=t["hfont"], wrap=True)
        if d.get("subtitle"):
            textbox(slide, 2.42, 2.72, 5.95, 0.58, d["subtitle"], 11.5,
                    color=t["mt"], font=t["bfont"], wrap=True)
        return

    set_bg(slide, t["hbg"])
    rect(slide, 0, 0, W, 0.035, t["ac1"])
    rect(slide, LM, 1.42, 0.045, 1.55, t["ac1"])
    num = d.get("number", "")
    if num:
        textbox(slide, LM + 0.18, 1.22, 2.0, 0.24,
                str(num), 10, bold=True, color=t["ac1"], font=t["bfont"])
    textbox(slide, LM + 0.18, 1.62, 8.0, 1.0,
            d.get("title", ""), 31, bold=True,
            color=t["htx"], font=t["hfont"])
    if d.get("subtitle"):
        textbox(slide, LM + 0.18, 2.78, 7.4, 0.5,
                d["subtitle"], 12, color=blend_hex(t["htx"], t["hbg"], 0.68),
                font=t["bfont"])


def slide_end(slide, t, d):
    if is_exhibit(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac1"])
        rect(slide, 0, 0.05, W, 0.015, t["ac2"])
        draw_soft_grid(slide, t["ac2"], t["hbg"], x=6.95, y=0.85, w=2.35, h=3.2, cols=5, rows=7, alpha_hex="14")
        textbox(slide, LM, 1.65, 5.8, 0.85,
                d.get("title", "Thank You"), 31, bold=True, color=t["htx"], font=t["hfont"])
        if d.get("subtitle"):
            textbox(slide, LM, 2.52, 5.2, 0.30,
                    d["subtitle"], 10.5, color=blend_hex(t["htx"], t["hbg"], 0.68), font=t["bfont"])
        return

    if is_mckinsey(t):
        set_bg(slide, t["hbg"])
        rect(slide, 0, 0, W, 0.05, t["ac2"])
        rect(slide, LM, 1.70, 0.06, 1.00, t["ac2"])
        textbox(slide, LM + 0.18, 1.55, 5.6, 0.65,
                d.get("title", "Next step"), 28, bold=True, color=t["htx"], font=t["hfont"])
        if d.get("subtitle"):
            textbox(slide, LM + 0.18, 2.30, 5.0, 0.26,
                    d["subtitle"], 10.5, color="D7DEEA", font=t["bfont"])
        return

    if is_google(t):
        set_bg(slide, "FFFFFF")
        rect(slide, 0, 0, W * 0.25, 0.05, "4285F4")
        rect(slide, W * 0.25, 0, W * 0.20, 0.05, "EA4335")
        rect(slide, W * 0.45, 0, W * 0.15, 0.05, "FBBC04")
        rect(slide, W * 0.60, 0, W * 0.40, 0.05, "34A853")
        title = d.get("title", "Next action")
        title_size = fitted_font_size(title, 7.8, 33, 24, max_lines=2)
        textbox(slide, 0.82, 1.52, 7.8, 1.0, title, title_size, bold=True,
                color=t["tx"], font=t["hfont"], wrap=True)
        if d.get("subtitle"):
            textbox(slide, 0.82, 2.72, 7.45, 0.70, d["subtitle"], 12,
                    color=t["mt"], font=t["bfont"], wrap=True)
        rect(slide, 0.82, 4.10, 2.10, 0.10, "4285F4")
        rect(slide, 2.92, 4.10, 1.10, 0.10, "EA4335")
        rect(slide, 4.02, 4.10, 1.10, 0.10, "FBBC04")
        rect(slide, 5.12, 4.10, 2.20, 0.10, "34A853")
        return

    set_bg(slide, t["hbg"])
    rect(slide, 0, 0, W, 0.08, t["ac2"])
    textbox(slide, 0, 1.8, W, 1.1,
            d.get("title", "Thank You"), 40, bold=True,
            color=t["htx"], align=PP_ALIGN.CENTER, font=t["hfont"])
    if d.get("subtitle"):
        textbox(slide, 0, 3.1, W, 0.5,
                d["subtitle"], 12, color=blend_hex(t["htx"], t["hbg"], 0.68),
                align=PP_ALIGN.CENTER, font=t["bfont"])
    # Contact info row
    contacts = d.get("contacts", [])
    if contacts:
        total_w = W - 2 * LM
        cw = total_w / len(contacts)
        for i, c in enumerate(contacts):
            textbox(slide, LM + i * cw, 3.9, cw, 0.35,
                    c, 9, color=blend_hex(t["htx"], t["hbg"], 0.60),
                    align=PP_ALIGN.CENTER, font=t["bfont"])


# ── Layout engine ─────────────────────────────────────────────────────────────

def build_rows(blocks):
    """Group blocks into rows. Handles both nested [[a,b],[c]] and flat [a,b,c] formats."""
    rows = []
    if not blocks:
        return rows

    # Detect nested format: first element is a list → [[left,right], [full], ...]
    if isinstance(blocks[0], list):
        for row_items in blocks:
            if not isinstance(row_items, list) or len(row_items) == 0:
                continue
            if len(row_items) == 1:
                rows.append(("full", row_items[0], None))
            elif len(row_items) >= 2:
                rows.append(("side", row_items[0], row_items[1]))
        return rows

    # Flat format: use span="left"/"right" for side-by-side pairing
    pending = None
    for blk in blocks:
        sp = blk.get("span", "full")
        if sp in ("left", "right"):
            if pending is None:
                pending = {"left": None, "right": None}
            pending[sp] = blk
            if pending["left"] and pending["right"]:
                rows.append(("side", pending["left"], pending["right"]))
                pending = None
        else:
            if pending:
                if pending["left"]:
                    rows.append(("full", pending["left"], None))
                pending = None
            rows.append(("full", blk, None))
    if pending and pending["left"]:
        rows.append(("full", pending["left"], None))
    return rows


def row_weight(row):
    _, first, second = row
    kinds = {block.get("type", "") for block in (first, second) if block}
    if kinds & {"image", "chart"}:
        return 1.65
    if "table" in kinds:
        return 1.50
    if "kpi" in kinds:
        return 0.90
    if "quote" in kinds:
        return 1.10
    return 1.0


def side_widths(first, second):
    first_type = first.get("type", "")
    second_type = second.get("type", "")
    visual_types = {"image", "chart", "table"}
    text_types = {"bullets", "text", "quote"}
    if first_type in visual_types and second_type in text_types:
        first_ratio = 0.59
    elif first_type in text_types and second_type in visual_types:
        first_ratio = 0.41
    else:
        first_ratio = 0.50
    usable = CW - GAP
    first_width = usable * first_ratio
    return first_width, usable - first_width


def render_content(slide, t, d, pg, total):
    set_bg(slide, "FFFFFF" if is_mckinsey(t) else t["pbg"])

    eyebrow = d.get("eyebrow", "")
    title = d.get("title", "")
    insight = d.get("insight", "")
    footer_label = d.get("footer", "")

    draw_header(slide, t, eyebrow, title, insight)
    draw_footer(slide, t, footer_label, pg, total)

    blocks = d.get("blocks", [])
    if not blocks:
        return

    rows = build_rows(blocks)
    n = len(rows)
    weights = [row_weight(row) for row in rows]
    content_h = BH - max(0, n - 1) * GAP
    total_weight = sum(weights) or 1.0
    y = BY

    for row, weight in zip(rows, weights):
        row_h = content_h * weight / total_weight
        layout, ba, bb = row

        if layout == "side":
            fn_a = BLK[ba["type"]]
            fn_b = BLK[bb["type"]]
            left_w, right_w = side_widths(ba, bb)
            fn_a(slide, t, ba, LM, y, left_w, row_h)
            fn_b(slide, t, bb, LM + left_w + GAP, y, right_w, row_h)

        else:
            sp = ba.get("span", "full")
            if sp == "center":
                bw = CW * 0.72
                bx = LM + (CW - bw) / 2
            else:
                bw = CW
                bx = LM
            fn = BLK[ba["type"]]
            fn(slide, t, ba, bx, y, bw, row_h)
        y += row_h + GAP


# ── Main renderer ─────────────────────────────────────────────────────────────

def render(slide, t, d, pg, total):
    tp = d.get("type", "content")
    if tp == "title":
        slide_title(slide, t, d)
    elif tp == "section":
        slide_section(slide, t, d)
    elif tp == "end":
        slide_end(slide, t, d)
    else:
        render_content(slide, t, d, pg, total)


def generate(workflow, output_path):
    workflow = normalize_workflow(workflow)
    prs = Presentation()
    prs.slide_width = Inches(W)
    prs.slide_height = Inches(H)

    meta = workflow.get("meta", {})
    theme_name = meta.get("theme", "charcoal")
    t = dict(THEMES.get(theme_name, THEMES["charcoal"]))
    t["name"] = theme_name

    slides = workflow.get("slides", [])
    total = len(slides)
    for i, d in enumerate(slides):
        sl = prs.slides.add_slide(prs.slide_layouts[6])
        render(sl, t, d, i + 1, total)

    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    prs.save(output_path)


# ── Template mode ──────────────────────────────────────────────────────────────

def extract_template_theme(template_path):
    """Extract dominant colors and fonts from a template's slide master.
    Returns a THEME-style dict for use in rendering."""
    try:
        prs = Presentation(template_path)
        sm = prs.slide_masters[0]
        # Try to read theme colors from slide master background
        bg = sm.background.fill
        bg_color = "FFFFFF"
        try:
            detected_bg = bg.fore_color.rgb
            if detected_bg is not None:
                bg_color = str(detected_bg)
        except Exception:
            pass

        # Extract font scheme — often in theme XML
        hfont = "Calibri"
        bfont = "Calibri"
        try:
            theme = sm.slide_layouts[0]
            for ph in theme.placeholders:
                if ph.placeholder_format.idx == 0:  # title
                    for p in ph.text_frame.paragraphs:
                        if p.runs:
                            hfont = p.runs[0].font.name or hfont
                            break
                if ph.placeholder_format.idx == 1:  # body
                    for p in ph.text_frame.paragraphs:
                        if p.runs:
                            bfont = p.runs[0].font.name or bfont
                            break
        except Exception:
            pass

        # Build a theme from detected colors — use dark variants for header
        t = {
            "hbg": "2B2A27",
            "htx": "FFFFFF",
            "ac1": "C45D3C",
            "ac2": "6E8B74",
            "pbg": bg_color,
            "cbg": "F4F1EA",
            "tx": "242321",
            "mt": "6C6963",
            "bd": "D9D3C8",
            "fbg": "F4F1EA",
            "hfont": hfont,
            "bfont": bfont,
        }
        return t, prs
    except Exception as e:
        print(f"Warning: Could not extract theme from template: {e}")
        return None, None


def detect_slide_layouts(prs):
    """Return semantic layout references instead of assuming fixed indices."""
    mapping = {}
    for slide_type in ("title", "section", "content", "end"):
        record, _ = select_layout(prs, slide_type)
        mapping[slide_type] = record
    return mapping


def _flatten_template_blocks(blocks):
    for block in blocks or []:
        if isinstance(block, list):
            yield from _flatten_template_blocks(block)
        else:
            yield block


def _strict_body_payload(slide):
    values = []
    for block in _flatten_template_blocks(slide.get("blocks", [])):
        block_type = block.get("type")
        if block_type == "bullets":
            values.extend(str(item) for item in block.get("items", []))
        elif block_type == "text":
            if block.get("heading"):
                values.append(str(block["heading"]))
            if block.get("body"):
                values.append(str(block["body"]))
        elif block_type == "quote":
            quote = block.get("quote") or block.get("text")
            if quote:
                values.append(str(quote))
            if block.get("attribution"):
                values.append(str(block["attribution"]))
        else:
            raise ValueError(
                "strict template mode cannot place block type "
                f"{block_type!r} without an explicit template_fields map; "
                "inspect the template and map inherited placeholders, use "
                "--template-mode adaptive, or switch to a task-specific renderer"
            )
    return values


def _set_placeholder_text(placeholder, value):
    values = value if isinstance(value, list) else [value]
    values = [str(item) for item in values if item is not None and str(item) != ""]
    if not values:
        return
    frame = placeholder.text_frame
    frame.clear()
    for index, text in enumerate(values):
        paragraph = frame.paragraphs[0] if index == 0 else frame.add_paragraph()
        paragraph.text = text
        paragraph.level = 0


def _template_field_value(fields, placeholder):
    fmt = placeholder.placeholder_format
    type_name = placeholder_type_name(fmt.type)
    candidates = (
        str(fmt.idx),
        placeholder.name,
        placeholder.name.casefold(),
        type_name,
        type_name.casefold(),
    )
    for candidate in candidates:
        if candidate in fields:
            return True, fields[candidate]
    return False, None


def _fill_strict_template_slide(slide, definition):
    fields = definition.get("template_fields") or {}
    if not isinstance(fields, dict):
        raise ValueError("template_fields must be an object keyed by placeholder name, type, or idx")

    placeholders = list(slide.placeholders)
    explicitly_filled = set()
    for placeholder in placeholders:
        found, value = _template_field_value(fields, placeholder)
        if found:
            _set_placeholder_text(placeholder, value)
            explicitly_filled.add(id(placeholder))

    title = definition.get("title")
    subtitle = definition.get("subtitle") or definition.get("eyebrow")
    body = [] if fields else _strict_body_payload(definition)
    title_filled = False
    subtitle_filled = False
    body_filled = False

    for placeholder in placeholders:
        if id(placeholder) in explicitly_filled:
            continue
        type_name = placeholder_type_name(placeholder.placeholder_format.type)
        if title and not title_filled and type_name in {"TITLE", "CENTER_TITLE"}:
            _set_placeholder_text(placeholder, title)
            title_filled = True
        elif subtitle and not subtitle_filled and type_name == "SUBTITLE":
            _set_placeholder_text(placeholder, subtitle)
            subtitle_filled = True
        elif body and not body_filled and type_name in {"BODY", "OBJECT", "TEXT"}:
            _set_placeholder_text(placeholder, body)
            body_filled = True

    if fields:
        return
    if title and not title_filled:
        raise ValueError(
            f"strict template layout {slide.slide_layout.name!r} has no title placeholder; "
            "choose template_layout explicitly or provide template_fields"
        )
    if body and not body_filled:
        raise ValueError(
            f"strict template layout {slide.slide_layout.name!r} has no body placeholder; "
            "choose template_layout explicitly or provide template_fields"
        )


def _validate_adaptive_canvas(prs):
    expected_width = int(Inches(W))
    expected_height = int(Inches(H))
    if abs(int(prs.slide_width) - expected_width) > 1000 or abs(int(prs.slide_height) - expected_height) > 1000:
        raise ValueError(
            "adaptive template mode requires the base-ppt 16:9 canvas; use strict mode "
            "to preserve this template's native canvas or switch to a task-specific renderer"
        )


def template_generate(workflow, template_path, output_path, template_mode="strict"):
    """Generate PPTX using a template file as the base.

    Strict mode preserves the native canvas and fills inherited placeholders only.
    Adaptive mode uses the template as theme/layout assistance, then overlays the
    base-ppt renderer. These are intentionally different fidelity contracts.
    """
    workflow = normalize_workflow(workflow)
    t, prs = extract_template_theme(template_path)
    if prs is None:
        raise ValueError(f"could not open template: {template_path}")
    if template_mode not in {"strict", "adaptive"}:
        raise ValueError("template_mode must be 'strict' or 'adaptive'")

    meta = workflow.get("meta", {})
    if template_mode == "adaptive" and meta.get("theme"):
        t = dict(THEMES.get(meta["theme"], t))
    else:
        t = dict(t)
    t["name"] = meta.get("theme", "template")

    if template_mode == "adaptive":
        _validate_adaptive_canvas(prs)
    clear_slides(prs)
    slides = workflow.get("slides", [])
    total = len(slides)

    for i, d in enumerate(slides):
        tp = d.get("type", "content")
        _, layout = select_layout(prs, tp, d.get("template_layout"))
        sl = prs.slides.add_slide(layout)
        if template_mode == "strict":
            _fill_strict_template_slide(sl, d)
        else:
            render(sl, t, d, i + 1, total)

    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    prs.save(output_path)


# ── CLI ───────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser(description="Structured editable PPT generator")
    ap.add_argument("input", help="Workflow JSON file or JSON string")
    ap.add_argument("output", nargs="?", default="output.pptx", help="Output PPTX path")
    ap.add_argument("--template", "-t", default=None,
                    help="Path to a native template .pptx file")
    ap.add_argument("--template-mode", choices=("strict", "adaptive"), default="strict",
                    help="strict fills inherited placeholders only; adaptive overlays base-ppt on a compatible 16:9 template")
    args = ap.parse_args()

    inp = args.input
    try:
        wf = json.loads(inp)
    except json.JSONDecodeError:
        if os.path.exists(inp):
            with open(inp, "r", encoding="utf-8-sig") as f:
                wf = json.load(f)
        else:
            print(f"Invalid input: {inp}")
            sys.exit(1)

    try:
        if args.template:
            if not os.path.exists(args.template):
                print(f"Template not found: {args.template}")
                sys.exit(1)
            template_generate(wf, args.template, args.output, args.template_mode)
        else:
            generate(wf, args.output)
        sz = os.path.getsize(args.output)
        print(f"PPTX saved: {args.output} ({sz / 1024:.1f} KB)")
    except Exception as e:
        print(f"Failed: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
