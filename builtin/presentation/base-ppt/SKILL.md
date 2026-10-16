---
name: base-ppt
aliases:
  - better-ppt
  - better-ppt-generator
  - base-ppt-generator
description: Fast, neutral, native, element-level editable PPTX generation through a compact JSON renderer for simple, internal, recurring, fixed-schema, and standard structured decks, plus portable python-pptx helpers and a copyable example for rapid editable exceptions. It is also the disclosed fallback when a task-specific per-deck builder cannot produce a valid deck after a bounded attempt.
read_when:
  - Creating a standard editable PPTX
  - Data report with editable charts or tables
  - Filling a supplied company PPTX template
  - Rapidly writing a bespoke editable python-pptx deck, timeline, or matrix from stable helpers
---

# Base PPT

This is **Level 1 — fallback structured**, not the normal PPT generator. An empty or omitted generator must not route here. Direct conversion may use it only when the caller explicitly supplies `generator=base-ppt`; otherwise regular substantive work returns to the Level-2 task-specific builder.

`base-ppt` owns two complementary methods: the fast polished structured renderer driven by JSON and a lightweight helper kit for quickly writing an editable exception. `better-ppt` remains only as a legacy compatibility alias. It is the deliberate fast neutral route, not the default route for a substantive audience-facing deck. Its reusable mechanics are a foundation, not a reason to flatten a richer story or encode one reference deck into the planner.

`base-ppt` is a polished structured route when speed, neutrality, and a stable schema are the priority; the helper/example is its escape hatch for a small bespoke editable composition.

Use JSON when the standard vocabulary fits. Copy the helper/example when the deck needs its own composition. Return to `ppt-art-director` when cinematic pacing, launch/showcase art direction, bespoke infographics, or complex relationship design still requires visual judgment.

## Route boundary

- Use the structured JSON route for title/content/section/end decks made from `bullets`, `kpi`, `table`, `chart`, `text`, `quote`, and `image` blocks, or for a supplied native template whose placeholders can express those pages.
- Use the copyable helper/example for page-specific editable layouts, including explicit timelines and matrices. Writing a short task-specific `build_ppt.py` is a valid high-quality method. Do not add content-keyword guesses to decide coordinates or composition.
- Route bespoke architecture, flow, network, relationship, annotated-infographic, or cinematic work to a task-specific `python-pptx`/PptxGenJS renderer; use `ppt-art-director` when the visual direction itself still needs judgment.
- PPTMaster belongs to `ppt-master`, whether the user explicitly requests it or `ppt-art-director` autonomously selects it as the premium SVG-first route.

## Communication and design brief

Before writing workflow JSON, determine the audience, communication job, central takeaway, and an arc appropriate to the material. Examples include context → stakes → evidence → implications → action; question → analysis → answer; and current state → change → future state. An agenda is not a narrative.

- Give each slide one narrative job and a takeaway title a presenter could plausibly say aloud.
- Turn evidence into meaning: show what the audience sees, what it means, why it matters, and how it changes the conclusion or next step.
- Use tables only for exact lookup. Use charts for comparison, KPI cards for a few hero metrics, quotes for genuine sourced language, and text for a short interpretation—not as interchangeable decoration.
- Avoid consecutive table slides, repeated card grids, generic category titles, empty decorative shapes, and endings that merely say “Thank you”. If the JSON vocabulary would cause those patterns, switch to the helper/example or return to `ppt-art-director`.
- Keep one palette, one grid, strong title/body contrast, purposeful whitespace, and topic-appropriate typography. State an internal palette rationale tied to the subject, source visuals, audience, or brand. Use either a neutral paper/ink base with one restrained accent or a compact semantic palette whose colors have stable roles; never rotate colors across equivalent objects as decoration.

## Fast JSON renderer

After `use_skill base-ppt`, do not list or probe Skill/runtime source files. Let `<Runtime Directory>` be the directory returned by `use_skill`. If the user explicitly requests a named design system such as Apple, Stripe, McKinsey, Linear, Notion, Vercel, Airbnb, or Tesla, read only the matching `<Runtime Directory>\design-systems\<name>.md` once before writing the workflow.

```powershell
python "<Runtime Directory>\generate.py" "<workflow.json>" "<output.pptx>"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<Runtime Directory>\scripts\render_pptx.ps1" -InputPptx "<output.pptx>" -OutputDir "<task-private-preview-dir>"
```

## Bespoke Python fast path

When page-specific composition will be faster and better than forcing workflow JSON, copy the portable helper and example into task storage, then edit the task copy. The copied script has no dependency on an OpenKitty installation path:

```powershell
Copy-Item -LiteralPath "<Runtime Directory>\pptx_helpers.py" -Destination "<task-storage>\pptx_helpers.py"
Copy-Item -LiteralPath "<Runtime Directory>\examples\custom_deck.py" -Destination "<task-storage>\build_ppt.py"
python "<task-storage>\build_ppt.py" --output "<task-storage>\output.pptx" --theme navy_gold
```

`pptx_helpers.py` deliberately owns mechanics rather than content: 16:9 canvas setup; validated six-digit colors; fitted text; aspect-preserving images; stable header/footer shells; KPI strips; editable native charts and tables; parameterized timelines and matrices; bounds validation; and safe save-path creation. The task script owns the storyline, page geometry, real data, and design decisions. Use its `Deck.rect()` and `Deck.text()` primitives freely instead of expanding the helper with a topic-specific page type.

Timeline and matrix are bespoke-helper capabilities, not native JSON block types. Choose them explicitly from the communication job and evidence, never from keyword guessing. Timeline milestones must come from ordered source evidence; matrix x/y values must come from supplied data or an explicit analyst judgment. Do not add content-domain keyword routing, machine-specific paths, or guessed coordinates to the helper or planner. The example is reference code to copy and reshape, not a template whose demo content should leak into a real deck.

## Existing native PPTX templates

Template source and renderer are separate decisions. Before claiming strict fidelity, inspect the user's PPTX once:

```powershell
python "<Runtime Directory>\scripts\inspect_pptx_template.py" "<brand.pptx>" --output "<task-private-template-manifest.json>"
```

Use `template_layout` on a slide to select an exact layout name or `master:layout` reference from the manifest. Use `template_fields` only when an explicit placeholder map is needed; keys may be placeholder name, type, or idx.

```powershell
python "<Runtime Directory>\generate.py" "<workflow.json>" "<output.pptx>" --template "<brand.pptx>" --template-mode strict
```

- `strict` is the default. It removes exemplar slides, preserves the native canvas/masters/layouts, and fills inherited placeholders only. It rejects rich blocks that have no explicit placeholder mapping instead of silently painting generic chrome over the template.
- `adaptive` is a separate, lower-fidelity contract for compatible 16:9 templates. It uses the template as theme/layout assistance and overlays the base-ppt renderer. Never describe adaptive output as strict template preservation.
- If the requested page cannot fit an inspected layout, switch to an object-mapped task-specific template edit or report the fidelity limitation. Do not route a strict existing PPTX through PPTMaster.

Render once, inspect the exported PNGs with an actual image/vision tool, make one targeted correction when needed, and re-render only the changed deck. File existence, dimensions, metadata, a `read_file` message, dominant-color percentages, color counts, brightness, and text-pixel coverage are not visual inspection. If actual image/vision inspection is unavailable, disclose that visual quality remains unverified. Do not enter a preview/watch/retry loop.

If the generator reports a schema error in an existing workflow JSON, read that JSON and use `json_patch` with JSON Pointer paths. Do not rewrite the entire workflow through another large `write_file` call, and never append partial fragments to a JSON document.

## Native JSON schema

Use only this schema. `chart.categories` and `chart.series` are direct block fields; KPI uses `metrics`, not `items`.

```json
{
  "meta": {"theme": "charcoal", "title": "Deck title"},
  "slides": [
    {"type": "title", "title": "Deck title", "subtitle": "Audience-facing subtitle"},
    {"type": "content", "title": "One clear takeaway", "blocks": [
      {"type": "bullets", "items": ["Point one", "Point two", "Point three"]},
      {"type": "text", "heading": "Interpretation", "body": "Why this evidence matters"}
    ]},
    {"type": "content", "title": "Evidence", "blocks": [
      {"type": "kpi", "metrics": [{"value": "42", "label": "Grounded metric", "sub": "Source context"}]},
      {"type": "chart", "chart_type": "bar", "categories": ["A", "B"], "series": [{"name": "Actual", "values": [1, 2]}], "native_editable": true}
    ]},
    {"type": "end", "title": "Decision or next action"}
  ]
}
```

Supported JSON block types are only `bullets`, `kpi`, `table`, `chart`, `text`, `quote`, and `image`. Side-by-side blocks use `span: "left"` and `span: "right"`. Timeline, matrix, diagram, flowchart, architecture, network, and free-form relationship requests are not native JSON blocks; use the bespoke helper/example or a richer task-specific route instead. Never replace a requested relationship with invented numeric data.

For a `text` block, use `heading` and/or `body`. Do not use a `content` field in new workflows; the generator accepts a string `content` only as a compatibility alias.

Theme guidance: there is no universal business theme. `charcoal` is the restrained neutral fallback; use `mckinsey`, `exhibit`, `google_style`, `anthropic`, or a domain theme only when the requested direction and evidence support it. Do not infer a branded look from broad content keywords.

## Ordinary-deck composition rubric

- Use an action title that states the page takeaway, not a generic topic label. Give every number a comparison, denominator, period, or short interpretation when the source provides one.
- Keep one grid, consistent margins and spacing, a restrained palette, and no more than two font families. Use clear title/body/note size contrast and left-align body copy.
- Vary composition with the narrative; do not repeat one card grid across the deck. Cards require genuinely parallel entities with comparable fields. For an explicit timeline, matrix, or other page-specific layout, copy the helper/example instead of inventing a JSON block. Preserve image aspect ratios and prefer compact aligned content over giant empty cards, narrow text boxes, long paragraphs, or low-resolution decoration.
- The cover needs a real visual hierarchy and context, not floating placeholder shapes. The closing must resolve the deck's communication job with a conclusion, decision, action, or synthesis.
- If several consecutive pages are best expressed as tables, reconsider the narrative and route. Exact values can live in one appendix-style table while the main story uses comparisons and implications.
- After rendering, check shape/text overlap, clipping, abnormal wrapping, local crowding or emptiness, weak margins, misaligned columns, contrast, and placeholders. After a correction, re-check the affected slide because a fix can introduce a second defect.

## Evidence and completion gate

- When the evidence set is frozen, every visible claim and number must come from it. Never invent ROI, time savings, availability, deployment modes, customer proof, contact details, performance scores, or roadmap metrics.
- Remove placeholders such as `[name]`, `[website]`, `[contact]`, `TBD`, and `待补充` before generation.
- A `.pptx` file, slide count, or successful process exit alone is insufficient. Delivery requires no error/placeholder slides, expected body content on every non-divider slide, and one actual vision-based inspection of rendered pixels.
