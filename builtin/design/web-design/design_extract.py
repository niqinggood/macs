#!/usr/bin/env python3
"""Extract structured design tokens and DOM layout from a rendered HTML page.
Usage: python design_extract.py <html_file_or_url> [--tokens|--layout|--full]
Like lobster's browser snapshot + accessibility tree, but using parsed HTML.
"""
import sys, re, json
from html.parser import HTMLParser
from collections import Counter

class DesignExtractor(HTMLParser):
    def __init__(self):
        super().__init__()
        self.colors = Counter()
        self.fonts = Counter()
        self.font_sizes = Counter()
        self.border_radii = Counter()
        self.shadows = []
        self.spacings = Counter()
        self.tag_counts = Counter()
        self.classes = Counter()
        self.css_vars = {}
        self.layout_info = []
        self.depth = 0
        self.text_content = []
        self.structure = []

    def handle_starttag(self, tag):
        self.tag_counts[tag] += 1

    def handle_data(self, data):
        text = data.strip()
        if text and len(text) > 10:
            self.text_content.append(text[:200])

def extract_from_html(html):
    # Extract CSS variables
    var_pattern = re.compile(r'--[\w-]+\s*:\s*[^;]+;')
    css_vars = {}
    for match in var_pattern.finditer(html):
        decl = match.group()
        if ':' in decl:
            key, val = decl.split(':', 1)
            css_vars[key.strip()] = val.strip().rstrip(';')

    # Extract color values
    color_pattern = re.compile(r'(?:color|background|border-color|fill|stroke)\s*:\s*([^;]+)', re.IGNORECASE)
    colors = Counter()
    for match in color_pattern.finditer(html):
        colors[match.group(1).strip()] += 1

    # Extract font families
    font_pattern = re.compile(r'font-family\s*:\s*([^;]+)', re.IGNORECASE)
    fonts = Counter()
    for match in font_pattern.finditer(html):
        fonts[match.group(1).strip()] += 1

    # Extract border-radius values
    radius_pattern = re.compile(r'border-radius\s*:\s*([^;]+)', re.IGNORECASE)
    radii = Counter()
    for match in radius_pattern.finditer(html):
        radii[match.group(1).strip()] += 1

    # Extract box-shadows
    shadow_pattern = re.compile(r'box-shadow\s*:\s*([^;]+)', re.IGNORECASE)
    shadows = [m.group(1).strip() for m in shadow_pattern.finditer(html)]

    # Extract spacing values (padding, margin, gap)
    spacing_pattern = re.compile(r'(?:padding|margin|gap)\s*:\s*([^;]+)', re.IGNORECASE)
    spacings = Counter()
    for match in spacing_pattern.finditer(html):
        spacings[match.group(1).strip()] += 1

    # Extract semantic structure
    headings = re.findall(r'<h([1-6])[^>]*>(.*?)</h\1>', html, re.DOTALL)
    sections = re.findall(r'<(?:section|article|nav|header|footer|main|aside)[^>]*>', html, re.IGNORECASE)
    buttons = re.findall(r'<button[^>]*>(.*?)</button>', html, re.DOTALL)
    links = re.findall(r'<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>', html, re.DOTALL)

    # Build structured output
    output = []
    output.append("# Design System Extraction\n")

    output.append("## Color Palette")
    for color, count in colors.most_common(15):
        output.append(f"- `{color}` (used {count}x)")
    output.append("")

    output.append("## Typography")
    output.append("### Font Families")
    for font, count in fonts.most_common(5):
        output.append(f"- {font} (used {count}x)")
    output.append("### Headings Found")
    for level, text in headings:
        output.append(f"- H{level}: {text.strip()[:100]}")
    output.append("")

    output.append("## Spacing & Layout")
    output.append("### Common Spacing Values")
    for space, count in spacings.most_common(10):
        output.append(f"- `{space}` (used {count}x)")
    output.append(f"\n### Border Radius Values: {', '.join(f'`{r}`' for r,_ in radii.most_common(5))}")
    output.append("")

    output.append("## Effects")
    output.append(f"### Box Shadows ({len(shadows)} found)")
    for s in shadows[:5]:
        output.append(f"- `{s}`")
    output.append("")

    output.append("## CSS Custom Properties")
    for var, val in sorted(css_vars.items())[:20]:
        output.append(f"- `{var}`: {val}")
    output.append("")

    output.append("## Page Structure")
    output.append(f"- Sections/containers: {len(sections)}")
    output.append(f"- Buttons: {len(buttons)}")
    output.append(f"- Links: {len(links)}")
    output.append(f"- Headings: {len(headings)}")
    output.append("### Key Buttons")
    for btn in buttons[:10]:
        output.append(f"- {btn.strip()[:100]}")
    output.append("")

    # Extract what appears to be the primary color
    if css_vars:
        primary_keys = [k for k in css_vars if 'primary' in k.lower() or 'brand' in k.lower() or 'blue' in k.lower()]
        if primary_keys:
            output.append(f"## Likely Primary Color: `{css_vars[primary_keys[0]]}`")

    return '\n'.join(output)

if __name__ == '__main__':
    import sys
    if len(sys.argv) > 1:
        with open(sys.argv[1], 'r', encoding='utf-8', errors='replace') as f:
            html = f.read()
    else:
        html = sys.stdin.read()

    print(extract_from_html(html))
