---
name: "Web Design and Restyle"
description: >
  Analyze a reference website's visual design language, deeply understand its style philosophy,
  then map and apply that style to rebuild another website's content.
  REQUIRES explicit design reasoning before writing any code.
read_when:
  - Redesigning or restyling a webpage
  - Applying one website's visual style to another
  - make X look like Y or restyle a webpage
  - Extracting and replicating design systems
tags:
  - web-design
  - restyle
  - html
  - css
  - design-system
---
# 🚨 CRITICAL RULE: NEVER SKIP ANALYSIS

**Do NOT jump to HTML generation.** Shallow style copies are worthless. You MUST reason through each design decision. Follow the 5-step workflow below EXACTLY. Write intermediate analysis files — they force you to think.



# Web Design & Restyle

## CRITICAL: Think Before You Build

Do NOT jump straight from "fetch style" to "generate HTML". You must explicitly reason through each step and document your thinking. The key insight is: **design is not just CSS tokens — it's a philosophy applied to content**.

## Four-Step Workflow (MUST follow this order)

### Step 1: UNDERSTAND THE STYLE (web_fetch mode=snapshot)

Fetch the reference site with `web_fetch(url, mode="snapshot")`. Then write a **structured design analysis** to `storage/design_analysis.md`:

```markdown
# Design Analysis of [REFERENCE SITE]

## Visual Personality
[2-3 sentences: modern/corporate/playful/minimal? What emotion does it convey?]

## Layout Pattern (top to bottom)
1. [Component]: [What it contains, how it looks]
2. [Component]: [What it contains, how it looks]
...

## Component Inventory
| Component | Visual Style | Key CSS Techniques |
|-----------|-------------|-------------------|
| Navigation | e.g. fixed, glassmorphism, dark bg | backdrop-filter, rgba |
| Hero | e.g. gradient bg, large text, CTA | radial-gradient, flexbox |
| Cards | e.g. rounded, hover lift, icon+text | border-radius, transition, shadow |
...

## Color System
- Primary: #xxx — used for [buttons, links]
- Background: #xxx — [dark/light theme]
- Text: #xxx
- Accent: #xxx

## Typography
- Display font: [name] — used for [headings]
- Body font: [name] — used for [content]
- Scale: h1=XXpx, h2=XXpx, body=XXpx

## Spacing & Layout
- Section padding: XXpx
- Card gap: XXpx
- Border radius pattern: [consistent value or varied]

## Signature Effects
- [e.g. backdrop-filter blur on nav]
- [e.g. gradient text on headings]
- [e.g. hover lift + shadow on cards]
```

### Step 2: CATEGORIZE THE CONTENT (web_fetch)

Fetch the target site with `web_fetch(url)`. Then write a **content categorization** to `storage/content_analysis.md`:

```markdown
# Content Analysis of [TARGET SITE]

## Content Inventory
| Content Piece | Type | Quantity | Current Presentation |
|--------------|------|----------|---------------------|
| Team members | people | 19 | text list |
| Benefits | features | 6 | bullet points |
| Events | timeline | 3 | links |
...

## Information Hierarchy
1. Most important: [what should be the hero message?]
2. Secondary: [what comes next?]
3. Detail: [what goes in cards/sections?]
```

### Step 3: MAP DESIGN TO CONTENT (THE CRITICAL STEP)

Write a **component mapping table** to `storage/design_mapping.md`. This is where you explicitly decide HOW each piece of target content will look in the reference style:

```markdown
# Design Mapping: [Reference Style] → [Target Content]

| Target Content | Reference Component | Visual Adaptation |
|---------------|-------------------|-------------------|
| TDP 19 team members | CodeBuddy avatar cards | Each person = card with gradient avatar circle, name, role badge, description |
| TDP 6 benefits | CodeBuddy feature cards | Each benefit = card with colored icon box, title, description |
| TDP 3 events | CodeBuddy event cards | Each event = card with gradient banner, date tag, title, description |
| TDP hero message | CodeBuddy hero | Dark bg, gradient orb, "与腾讯云产研零距离对话", CTA buttons |
| TDP community section | CodeBuddy testimonials | Quote cards with avatar, name, title, testimonial text |
| TDP about/join | CodeBuddy info panels | Two-column layout with list styling |
...

## Design Decisions
- Theme: [dark/light] — because [reason]
- Primary color: #xxx — because CodeBuddy uses [color] for [purpose]
- Card style: [description] — because [reason]
- Typography: [font choice] — because [reason]
```

### Step 4: BUILD (write_file)

ONLY after completing Steps 1-3, generate the HTML. Write it to `storage/<name>.html`.

**Rules for the HTML:**
- Start with `<!DOCTYPE html>` — the file will be written directly
- All CSS inline in a single `<style>` block
- Use CSS custom properties for the design system (:root { --color-primary: ... })
- Include responsive @media queries for mobile/tablet
- Add subtle hover animations (transform, box-shadow transitions)
- ALL content must come from the target site — no placeholder text
- Reference the mapping table from Step 3 to ensure every content piece has the right component

	**After writing, RUN VALIDATION (REQUIRED):**
	
	```bash
	python3 skills/web-design/validate.py storage/<name>.html
	```
	
	This checks: data integrity (render calls match data keys), broken images (relative paths), HTML structure.
	
	**If validation fails:** fix the reported issues and re-run. Do NOT declare success until validate.py passes.
	
	**Manual checklist (also verify visually):**
	- [ ] File starts with `<!DOCTYPE html>`, single `<html>/<head>/<body>`
	- [ ] All images: HTTPS URLs, inline SVG, CSS gradients, or emoji — zero relative paths
	- [ ] @media queries present, hover effects work
	- [ ] All content from target site present, no placeholders
	- [ ] Design matches mapping from Step 3

## Image Handling Rules

**THE RULE:** This is a standalone HTML file. It has NO server, NO relative paths, NO auth cookies.

### ✅ Allowed image sources:
```html
<!-- Public absolute HTTPS URLs (no auth required) -->
<img src="https://example.com/photo.jpg">

<!-- Inline SVG (most reliable) -->
<svg viewBox="0 0 100 100">...</svg>

<!-- CSS gradient placeholder (best for decorative images) -->
<div style="background: linear-gradient(135deg, #366ef4, #8b5cf6); width: 100px; height: 100px; border-radius: 12px;"></div>

<!-- Unicode/emoji for icons -->
<span style="font-size: 48px;">🚀</span>
```

### ❌ Broken image sources:
```html
<img src="/static/logo.png">        <!-- relative path — WILL NOT LOAD -->
<img src="./images/hero.jpg">       <!-- relative path — WILL NOT LOAD -->
<img src="images/hero.jpg">         <!-- relative path — WILL NOT LOAD -->
<img src="D:\web\img.jpg">         <!-- local file — WILL NOT LOAD -->
```

### How to handle images from the target site:
1. If the `<img>` has an absolute `https://` src that loads when you open it directly → keep it
2. If the src is relative or domain-relative → DO NOT include it. Replace with CSS gradient or SVG
3. For avatars/team photos: use gradient circles with initials (e.g., `<div class="avatar">王</div>`)
4. For hero backgrounds: use CSS radial-gradient
5. For icons: use emoji or inline SVG
6. For logos: recreate as SVG or use styled text

## CRITICAL: Content Completeness
When generating HTML from a design analysis, the analysis file may only contain **examples** of content (e.g., 3 team members out of 19). You MUST fill in ALL content, not just the examples. Options:
1. If the original data source is available → fetch it again and include everything
2. If the analysis file is your only source → explicitly list what's missing and note it
3. NEVER silently drop content — a page with 19 members should NOT become a page with 8

## Anti-Patterns (DO NOT DO)
- ❌ Skip the analysis steps and jump to code
- ❌ Write HTML line-by-line in `<p>` tags
- ❌ Generate HTML from analysis examples only — always use complete data
- ❌ Use external CSS/font files (except Google Fonts CDN)
- ❌ Leave placeholder/dummy content
- ❌ Copy the reference site's exact content — use the TARGET site's content
- ❌ Generate the same design for every project — adapt based on what Step 1 found
- ❌ Include `<img>` with relative/local paths — they WILL be broken
- ❌ Call renderMembers(M.foo) without defining M.foo in the data object — page will throw JS error
