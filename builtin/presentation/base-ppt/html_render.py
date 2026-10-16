#!/usr/bin/env python3
"""
HTML Slide Renderer — ChatGPT-quality presentation generation.
Reads a JSON workflow, generates HTML slides, optionally screenshots via Chrome CDP.
Usage: python html_render.py workflow.json output_dir [--no-screenshot]
"""
import json, os, sys, subprocess, base64, tempfile, shutil

SLIDE_W, SLIDE_H = 1280, 720
CSS_FILE = os.path.join(os.path.dirname(__file__), "slide_templates", "base.css")

def load_css():
    with open(CSS_FILE, "r", encoding="utf-8") as f:
        return f.read()

def build_slide_html(slide, theme_css=""):
    """Build a complete HTML page for one slide."""
    css = load_css() + "\n" + theme_css
    content = slide.get("content", "")
    bg = slide.get("background", "#ffffff")
    extra_class = slide.get("class", "")

    return f"""<!DOCTYPE html><html><head><meta charset="UTF-8">
<style>{css}</style></head>
<body style="background:{bg}" class="{extra_class}">
{content}
<div class="slide-number">{slide.get("number", "")}</div>
<div class="page-footer">{slide.get("footer", "")}</div>
</body></html>"""

# Pre-built slide builders for common types
def cover_slide(data):
    title = data.get("title", "Presentation Title")
    subtitle = data.get("subtitle", "")
    author = data.get("author", "")
    date = data.get("date", "")
    return f"""<div style="text-align:center">
<h1 style="font-size:56px;margin-bottom:24px">{title}</h1>
<p style="font-size:22px;color:#64748b;margin-bottom:48px">{subtitle}</p>
<div style="margin-top:48px;font-size:16px;color:#94a3b8">
<p>{author}</p><p>{date}</p></div></div>"""

def section_slide(data):
    title = data.get("title", "Section")
    desc = data.get("description", "")
    num = data.get("section_num", "")
    return f"""<div style="text-align:left">
<div style="font-size:14px;color:#3b82f6;text-transform:uppercase;letter-spacing:2px;margin-bottom:16px">Part {num}</div>
<h1 style="font-size:52px">{title}</h1>
<p style="font-size:20px;color:#64748b;margin-top:12px">{desc}</p></div>"""

def content_slide(data):
    title = data.get("title", "")
    body = data.get("body", "")
    bullets = data.get("bullets", [])
    image = data.get("image", "")
    layout = data.get("layout", "text-left")

    html = f'<h2 style="margin-bottom:24px">{title}</h2>'
    if layout == "two-column":
        html += '<div class="grid-2">'
        html += f'<div><p style="font-size:17px;line-height:1.7">{body}</p>'
        if bullets:
            html += '<ul style="margin-top:8px;padding-left:20px">'
            for b in bullets:
                html += f'<li>{b}</li>'
            html += '</ul>'
        html += '</div>'
        if image:
            html += f'<div style="display:flex;align-items:center;justify-content:center"><img src="{image}" style="max-width:100%;max-height:500px;border-radius:12px"></div>'
        html += '</div>'
    else:
        html += f'<p style="font-size:17px;line-height:1.7;max-width:900px">{body}</p>'
        if bullets:
            html += '<ul style="margin-top:12px;padding-left:20px;max-width:900px">'
            for b in bullets:
                html += f'<li style="font-size:17px;line-height:1.7;margin-bottom:6px">{b}</li>'
            html += '</ul>'
    return html

def kpi_slide(data):
    title = data.get("title", "Key Metrics")
    metrics = data.get("metrics", [{"value": "0", "label": "Metric"}])
    html = f'<h2 style="margin-bottom:32px">{title}</h2><div class="grid-3">'
    for m in metrics:
        change = m.get("change", "")
        color = "#22c55e" if change.startswith("+") else "#ef4444" if change.startswith("-") else "#64748b"
        html += f'''<div class="kpi-card">
<div class="metric">{m["value"]}</div>
<div class="metric-label">{m["label"]}</div>
<div style="font-size:14px;color:{color};margin-top:4px">{change}</div></div>'''
    html += '</div>'
    return html

def quote_slide(data):
    quote = data.get("quote", "")
    author = data.get("author", "")
    return f"""<div style="text-align:center;max-width:800px;margin:0 auto">
<div style="font-size:36px;font-weight:300;line-height:1.4;font-style:italic;color:#334155;margin-bottom:24px">"{quote}"</div>
<p style="font-size:18px;color:#64748b">— {author}</p></div>"""

def generate_slides(workflow, output_dir):
    """Generate HTML files for all slides in the workflow."""
    os.makedirs(output_dir, exist_ok=True)
    slides = workflow.get("slides", [])
    meta = workflow.get("meta", {})
    theme_css = meta.get("theme_css", "")

    builders = {
        "cover": cover_slide,
        "section": section_slide,
        "content": content_slide,
        "kpi": kpi_slide,
        "quote": quote_slide,
    }

    html_files = []
    for i, slide in enumerate(slides):
        slide.setdefault("number", str(i + 1))
        slide.setdefault("footer", meta.get("footer", ""))

        stype = slide.get("type", "content")
        builder = builders.get(stype)

        if builder:
            slide["content"] = builder(slide)
        elif not slide.get("content"):
            slide["content"] = content_slide(slide)

        html = build_slide_html(slide, theme_css)
        path = os.path.join(output_dir, f"slide_{i+1:02d}.html")
        with open(path, "w", encoding="utf-8") as f:
            f.write(html)
        html_files.append(path)

    print(f"Generated {len(html_files)} HTML slides in {output_dir}")
    return html_files

def screenshot_slides(html_files, output_dir, chrome_path=None):
    """Use headless Chrome to screenshot HTML slides as PNG."""
    png_files = []
    for hf in html_files:
        png = hf.replace(".html", ".png")
        abs_html = "file://" + os.path.abspath(hf).replace("\\", "/")
        cmd = [
            chrome_path or "chromium",
            f"--headless=new",
            f"--window-size={SLIDE_W},{SLIDE_H}",
            f"--screenshot={os.path.abspath(png)}",
            f"--hide-scrollbars",
            abs_html
        ]
        subprocess.run(cmd, capture_output=True, timeout=15)
        if os.path.exists(png):
            png_files.append(png)
            print(f"  Screenshot: {png}")
    return png_files

def pack_pptx(png_files, output_path):
    """Pack PNG screenshots into a PPTX."""
    try:
        from pptx import Presentation
        from pptx.util import Inches
    except ImportError:
        print("python-pptx not installed. Run: pip install python-pptx")
        return None

    prs = Presentation()
    prs.slide_width = Inches(13.333)
    prs.slide_height = Inches(7.5)
    blank_layout = prs.slide_layouts[6]  # blank

    for png in png_files:
        slide = prs.slides.add_slide(blank_layout)
        slide.shapes.add_picture(png, Inches(0), Inches(0),
                                 Inches(13.333), Inches(7.5))

    prs.save(output_path)
    print(f"PPTX saved: {output_path}")
    return output_path

if __name__ == "__main__":
    import argparse
    p = argparse.ArgumentParser()
    p.add_argument("workflow", help="JSON workflow file")
    p.add_argument("output", help="Output PPTX path")
    p.add_argument("--no-screenshot", action="store_true")
    p.add_argument("--chrome", help="Chrome executable path")
    args = p.parse_args()

    with open(args.workflow, "r", encoding="utf-8") as f:
        wf = json.load(f)

    tmpdir = tempfile.mkdtemp(prefix="slides_")
    try:
        html_files = generate_slides(wf, tmpdir)
        png_files = screenshot_slides(html_files, tmpdir, args.chrome) if not args.no_screenshot else []
        if png_files:
            pack_pptx(png_files, args.output)
        else:
            print(f"HTML slides ready in {tmpdir}")
            print("To pack into PPTX, run with Chrome available or use --no-screenshot")
    finally:
        if args.no_screenshot:
            print(f"HTML files preserved in {tmpdir}")
        else:
            shutil.rmtree(tmpdir, ignore_errors=True)
