---
name: paper-operators-visual
description: Plan and generate prompts or image assets for PPTs, posters, article illustrations, reports, explainers, cover images, social cards, and visual material packs using Paper Operators / 纸片人制片人 tactile paper-stage scenes or professional handwritten knowledge-poster style. Use when the user asks for prettier PPT素材, 制片人系列, 纸片人, 立体纸模, 生图, 配图, 海报, 长图, 小红书/朋友圈传播图, conceptual diagrams, hero images, or material assets that should turn abstract content into visual deliverables. Do not use for generic mascots, stock icon grids, pure charts, logos, stickers, or decoration that does not improve comprehension.
---

# Paper Operators Visual

Use this skill to turn source content into publish-grade visual assets for slides and articles. It has two main routes:

- **Paper Operators / 纸片人制片人**: 16:9 tactile paper diorama scenes where a folded-paper operator physically performs the core idea.
- **Professional handwritten knowledge poster**: 9:16 A3-paper/whiteboard-style knowledge posters for social sharing, course recap, investment brief, meeting summary, or industry notes.

The visual must clarify the message. If removing the operator, handwriting, or poster format does not weaken comprehension, use a chart, table, photo, or normal slide layout instead.

## Source Map

Read only the reference that matches the job:

- `reference/ppt/paper-operators-main/paper-operators/SKILL.md`: full Paper Operators workflow. Use for precise paper-person visual metaphors, article illustrations, or multi-image series.
- `reference/ppt/paper-operators-main/paper-operators/references/`: deep references. Pull only the needed file:
  - `intent-reading.md`: audience, author stance, emotional goal, content-specific particulars.
  - `relationship-grammar.md`: connection, sequence, dependency, causality, feedback, contrast, tradeoff, hierarchy, transformation, boundary, divergence, tension.
  - `operator-library.md`: choose the operator family and physical action.
  - `composition-modes.md`: decide single dense image vs series.
  - `series-and-chaining.md`: keep multi-image assets connected.
  - `prompt-template.md` and `qa-checklist.md`: final prompt and acceptance checks.
- `references/handwritten-knowledge-poster.md`: internal OpenKitty reference for 9:16 professional handwritten knowledge posters. Read it for social long images, course recaps, investment briefs, market notes, meeting summaries, or method posters.
- `builtin/presentation/ppt-master/`: premium SVG-first deck runtime route when the user explicitly requests `pptmaster` or beauty-first deck production.

## Route Selection

Choose one route before writing prompts:

- **PPT needs stronger visual storytelling**: plan 1-4 Paper Operators assets, then reference them from `base-ppt`, a task-specific renderer, or an HTML deck.
- **User asks for 海报/长图/手写知识图/小红书图**: use the handwritten knowledge-poster route; default aspect ratio 9:16.
- **User asks for 制片人/纸片人/立体纸模/concept image**: use Paper Operators route; default aspect ratio 16:9.
- **User asks for a complete deck and route is ambiguous**: call `ppt-art-director` first, then this skill only for selected cover/chapter/concept/process pages.
- **User requests `generator: pptmaster`**: route to `ppt-master`; use this skill only for supporting visual assets if needed.

## Paper Operators Workflow

For each asset:

1. Pick one **source anchor**: a sentence, claim, tension, transformation, tradeoff, process, or emotion.
2. Write the **viewer takeaway** in one sentence.
3. Choose the **relationship type** before choosing the operator.
4. Choose an operator family and physical action:
   - Thread Runner: connection, sequence, handoff, route, dependency.
   - Lens Keeper: evidence, inspection, hidden mechanism, comparison.
   - Gate Builder: filtering, threshold, approval, rejection, transformation.
   - Frame Setter: art, taste, curation, interpretation, reframing.
   - Light Catcher: attention, atmosphere, revelation, photography/film.
   - Care Folder: emotion, care, recovery, personal essays.
   - Boundary Keeper: burnout, refusal, self-protection, scope boundary.
   - Stack Mason: hierarchy, system layers, product architecture.
   - Tradeoff Weigher: decision, economics, strategy, ethics.
   - Cartographer: route choice, system terrain, market map.
5. Design the paper world: tabletop stage, route table, archive desk, gallery workbench, boundary room, miniature town, inspection bench, terrain tray, or a content-specific world.
6. Add state coding: blue for the main route; amber for evidence; coral for risk/blockage; mint for growth/recovery; gray/off-white for neutral material.
7. Add readable labels: usually 3-6 short Chinese labels on physical tags, plaques, route signs, stitched labels, or sentence strips.
8. QA with the Swap Test: if the image could move to another article/deck on the same topic unnoticed, it is too generic.

## Handwritten Poster Workflow

Use when the deliverable should look like a real expert's A3 handwritten knowledge map, not a digital PPT card.
Read `references/handwritten-knowledge-poster.md` before writing the final prompt for this route.

1. Extract 6-8 information modules from the source.
2. Create a top title area: title, one-line summary, and a small label such as `重点一览`, `核心复盘`, or `手写版`.
3. Create a dense middle area: numbered modules, 2-5 short points each, restrained hand-drawn icons, varied module sizes, clear grouping.
4. Create a bottom conclusion area: core judgment, action recommendation, formula, or risk note.
5. Style rules: off-white paper, black marker title, clear marker body text, dark blue / ink green / muted red / warm yellow accents, real paper texture, light shadows, professional handwriting, no childish scrapbook feel.
6. Negative prompt: no blue-purple tech gradient, no cyberpunk, no glassmorphism, no 3D icons, no cute cartoon, no low-end marketing long image, no fake dense text, no typos.

## Output Contract

For each visual asset, output:

```text
asset_id:
route: paper-operators / handwritten-poster
slide or placement:
source anchor:
viewer takeaway:
relationship type:
operator family or poster module plan:
paper world / poster layout:
core action:
labels:
composition:
state coding / color system:
final image prompt:
QA risks:
```

For a series, add:

```text
series throughline:
shared world / operator / palette / label style:
per-image role: opening / build / turn / resolution
handoff: image N end state -> image N+1 start state
```

## Final Prompt Pattern

Paper Operators:

```text
Create a 16:9 high-angle tactile paper diorama illustration.
Scene: {paper world}.
A folded off-white paper operator ({operator family}) is {core action} to show {relationship type}.
Main route: a flowing blue carrier ribbon moves from {start} through {action station} to {result}.
Objects: {paper cards, gates, lenses, trays, frames, shelves, terrain, stamps, tags}.
Readable Chinese labels on physical paper tags: {labels}.
Palette: warm off-white paper, charcoal outlines, quiet gray shadows, passage blue, plus {semantic accents}.
Mood: calm, editorial, premium, publishable.
Avoid: PPT flowchart, stock icons, mascot, robot, black blob figure, fake dense text, flat icon grid.
```

Handwritten poster:

```text
Create a vertical 9:16 professional handwritten knowledge poster on realistic off-white A3 paper.
The page looks like an expert researcher or course instructor organized the source after reading it carefully.
Top: {title}, {one-line summary}, small tag {tag}.
Middle: 6-8 structured modules with numbered headings, 2-5 concise points each, restrained hand-drawn icons, clear section borders.
Bottom: emphasized conclusion / action recommendation / risk note: {bottom conclusion}.
Style: black marker title, clean marker body text, dark blue emphasis, ink green borders, muted red risk marks, warm yellow highlights, real paper texture, natural shadows, professional readable handwriting.
Avoid: tech gradient, cyberpunk, glassmorphism, 3D icons, cartoon scrapbook, childish hand-copied poster, low-end marketing long image, fake text, typos.
```

## PPT Usage

- Use assets for cover, chapter divider, key concept, process story, contrast/tradeoff page, or emotional anchor.
- Do not decorate every slide. For 10-15 slides, 2-4 strong assets are usually enough.
- If image generation is unavailable, deliver the storyboard and final prompts; do not claim images exist.
- If images are generated, save them under `.openkitty/storage/` and reference them from `base-ppt` or the selected task-specific renderer.
