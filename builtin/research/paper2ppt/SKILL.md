---
name: paper2ppt
description: >
  Convert academic papers to presentation slides. Keynote/conference style.
  Trigger: 论文转PPT/paper to slides/make presentation from paper.
version: 1.0.0
---

# Paper to PPT

Convert research papers to well-designed presentation slides.

## Slide Structure

1. **Title Slide** — paper title, authors, affiliation
2. **Background** — 1-2 slides, why this matters
3. **Research Question** — 1 slide, clear gap/hypothesis
4. **Methods** — 1-2 slides, key points only
5. **Results** — 2-4 slides, one per key finding
6. **Discussion** — 1-2 slides, implications
7. **Conclusion** — 1 slide, take-home message
8. **References** — 1 slide, key citations only

## Design Rules

- Max 6 bullet points per slide
- Max 6 words per bullet
- One key figure per slide
- Consistent color scheme
- Large fonts (≥24pt body, ≥36pt titles)

## Usage

Start with `ppt-art-director`; default the conference deck to a task-specific per-deck renderer, use PPTMaster only when a higher-end commercial presentation treatment genuinely fits, and use `base-ppt` only for explicit fast/fixed-schema output or disclosed failure fallback.
Prompt template:
```
Create a conference presentation from [PAPER_PATH].
Style: derive one restrained visual thesis from the paper, audience, and venue.
Audience: [AUDIENCE].
Time: [DURATION] minutes.
```
