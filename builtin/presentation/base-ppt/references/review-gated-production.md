# Review-Gated PPT Production

Use this reference for premium, high-stakes, or visually sensitive PPT work where a quick one-pass render is likely to produce weak slides. It adapts the useful production discipline from the review-gated SVG/PPT workflow into OpenKitty's local PPT skills.

## Principle

Treat every generated file as a candidate until it passes content, layout, visual, and export checks. Do not let the model's confidence replace inspection.

## Stage Model

Use these stages for premium decks:

1. Content
   - Parse the source into page-level content.
   - Preserve facts, numbers, named entities, case details, risk notes, and source-specific claims.
   - Create one action title and one core message per slide.
   - Track what was compressed, moved to notes, or omitted.

2. Plan
   - Choose slide count, narrative arc, layout family, and per-slide wireframe.
   - Decide what appears on-slide versus speaker notes.
   - Run a capacity check before rendering: estimate whether the planned text fits the intended regions.
   - If a key slide is overfull, split it, move secondary material to notes, or change layout before rendering.

3. Draft
   - Render only from the approved plan.
   - Do not silently rewrite slide content during rendering just because it does not fit.
   - Use theme, typography, spacing, charts, and visual assets consistently.

4. Preview
   - Inspect the rendered slides, screenshots, or converted previews.
   - Check overflow, overlap, unsafe margins, unreadable chart labels, weak hierarchy, excessive density, and empty regions.
   - Fix blocking issues before presenting the file as a deliverable.

5. Revise
   - Record what changed and why.
   - If the visual implementation proves the plan cannot work, return to the plan stage instead of squeezing text smaller.

6. Export
   - Export only after content, layout, and preview checks pass.
   - Final output must be in the project or `.openkitty/storage/`, with a concrete path reported to the user.

## Blocking Quality Gates

Do not call the deck done when any of these are true:

- Text is clipped, overlapping, or outside safe margins.
- Tables or charts are unreadable.
- A slide contains multiple unrelated messages.
- Facts, numbers, or claims changed from the source without explanation.
- The deck ignores an explicit template, brand, generator, or editability requirement.
- Generated images are claimed but no local image file or returned image result exists.
- The output path is missing.

## Lightweight Mode

For ordinary quick decks, collapse the workflow into:

1. Content plan.
2. Render.
3. Preview/check.
4. Fix obvious issues.
5. Deliver file path.

Even in lightweight mode, never skip the preview/check step when a local renderer or screenshot path is available.

## Human Review

When the user asks for approval loops, high-end polish, or "先让我看一版", expose a review point:

- Provide the draft path and preview path if available.
- Ask for visual/content feedback after a concrete draft exists.
- Do not fabricate approval state. Continue only from actual user feedback.

## How This Fits `base-ppt`

- `plan_workflow.py` maps to Content and Plan.
- `generate.py` maps to Draft and Export.
- Local preview, screenshot conversion, or PPT inspection maps to Preview.
- Manual fixes to workflow JSON, theme, layout, or generated files map to Revise.

This reference defines process discipline. It is not a requirement to copy external scripts or preserve another project's internal state files.
