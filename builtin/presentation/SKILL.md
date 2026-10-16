---
name: presentation
version: 1.0.0
author: OpenKitty
status: stable
description: Presentation routing catalog with `ppt-art-director` as the single generic entry. Higher-end commercial decks use PPTMaster when premium visual storytelling is the main value; other substantive audience-facing decks default to task-specific editable per-deck renderers; `base-ppt` is the fast neutral or disclosed failure fallback. `better-ppt` is a legacy metadata alias.
triggers: ['ppt-master', 'pptmaster', 'generator:pptmaster', 'generator=pptmaster', 'base-ppt', 'better-ppt', 'paper-operators-visual', 'paper-operators', '制片人', '纸片人', '海报', '生图', '视觉物料', '知识海报', 'deck-guizang-editorial', 'deck-open-slide-canvas', 'deck-swiss-international', 'frontend-slides', 'html-ppt-retro-quarterly-review', 'latex-posters', 'nanobanana-ppt', 'ppt-keynote', 'pptx', 'pptx-generator', 'pptx-html-fidelity-audit', 'slides']
---

# Presentation Skill

## IRON RULES (non-negotiable)

1. **Color palette discipline**: Never mix colors across palettes, never modify hex values. Pick exactly one palette and commit to every color in it. Palette mixing is the #1 presentation quality killer.
2. **One message per slide**: Each slide has exactly ONE key takeaway. Two messages on one slide means the audience remembers zero. Split aggressively.
3. **Audience-first design**: Identify who will see this presentation and what they care about before designing anything. Executive decks differ from investor decks differ from team decks.
4. **Template fidelity**: When using a named template (Swiss, Guizang, Open Slide Canvas, Keynote), follow every constraint exactly. Do not "improve" or "customize" the designer's system — trust it.
5. **Technical constraints are absolute**: Canvas sizes (1920x1080, 1280x720), overflow prohibition, type scales, and padding ranges are not suggestions. Violating them causes rendering failures.
6. **No decoration without purpose**: Every visual element must serve communication. Ornament without communicative function is visual noise that dilutes the message.

---

## Sub-Skills

## Three production levels

1. **Level 3 — Premium custom (`ppt-master`)**: the first route for explicitly beautiful, premium, design-led, executive, board, investor, client, launch, sales, brand, and showcase decks when strict native-template fidelity is not required. It means the complete upstream Strategist/Executor workflow, not a label applied to another renderer.
2. **Level 2 — Regular custom (task-specific builder)**: the default for other substantive PPT work. Use the scaffolded `deck_spec.json` plus executable `visual_plan.json`, compose every page for the actual narrative, keep native editability, and run structural, per-slide visual, and aesthetic-ledger QA.
3. **Level 1 — Fallback structured (`base-ppt`)**: use only when the user explicitly asks for simple/fast/internal/recurring/fixed-schema output, a compatible placeholder-fill, or when one Level-2 build and one targeted correction still cannot produce a valid deck. Disclose the downgrade.

An empty generator is never permission to choose `base-ppt`. Direct Markdown-to-PPT conversion must name `base-ppt` explicitly; regular work goes through the Level-2 builder.

### ppt-art-director
# PPT Art Director

Route and plan high-quality PPT production before generation. Decide deck type, audience, narrative, visual assets, generator skill, and QA plan before invoking concrete PPT tools.

### ppt-master
# PPT Master Router

Level 3 premium custom. Run the complete bundled upstream workflow for higher-end commercial, executive, client, investor, launch, or showcase work after an explicit `generator: pptmaster` / `pptmaster` request or an autonomous `ppt-art-director` premium handoff. Content must be sufficiently frozen and strict native-template preservation must not be required.

### base-ppt
# Base PPT

Level 1 fallback structured route. Use for explicitly simple/internal output, repeated KPI/table/chart reports, speed, fixed schemas, inspected native-template placeholder filling, or a disclosed Level-2 failure fallback. Do not claim strict company-template fidelity from an adaptive overlay. Substantive audience-ready narrative decks default to a task-specific renderer even when native editability is required.

### paper-operators-visual
# Paper Operators Visual

Plan paper-operator / 制片人 style visual assets and professional handwritten knowledge posters for PPTs, articles, reports, explainer decks, social long images, generated images, and conceptual hero images. Use before `base-ppt`, a task-specific renderer, or HTML slide generation only when the asset improves comprehension.

### better-ppt
# Better PPT (Legacy Compatibility)

Legacy metadata alias that resolves directly to `base-ppt`. New tasks must route to `base-ppt` rather than advertising `better-ppt` as a second production route.

## Professional Slide Design Principles

Follow these rules to create ChatGPT-quality presentations:

### Content Strategy
1. **Audience-first**: Identify who will see this (executives/clients/team/investors) and what they care about.
2. **One message per slide**: Each slide has exactly ONE key takeaway. If there are two messages, split into two slides.
3. **Narrative arc**: Problem → Analysis → Solution → Evidence → Action. The audience should feel a journey.
4. **Dat
... (full details in sub-directory)

### deck-guizang-editorial
【模板: 归藏编辑墨水 Deck (Editorial × E-Ink)】
【意图】叙事、观点、分享、个人风格表达。墨纸印刷感, 不要科技感。Inspired by op7418/guizang-ppt-skill Style A。

【调色板 — 5 选 1, 严禁改 hex、严禁混用】
- 🖋 **墨水经典 Monocle** — ink `#0a0a0b`, paper `#f1efea`, paper-tint `#e8e5de`, ink-tint `#18181a`. 默认 / 通用商业 / 科技。
- 🌊 **靛蓝瓷 Indigo Porcelain** — ink `#0a1f3d`, paper `#f1f3f5`, paper-tint `#e4e8ec`, ink-tint `#152a4a`. 科技 / 研究 / 数据。
- 🌿 **森林墨 Forest Ink** — ink `#1a2e1f`, paper `#f5f1e8`, paper-tint `#ece7da`, ink-tint `#253d2c`. 自然 / 可持续 / 文化。
- 🍂 **牛皮
... (full details in sub-directory)

### deck-open-slide-canvas
【模板: 1920 画布自由 Deck】
【意图】不想被模板束缚的场景 (个人作品集、奇特演讲、艺术 / 设计课 deck)。给一个固定 1920×1080 画布 + 极强的类型 / 调色约束, 让 agent 像写 React 组件一样按内容自由排布每一页。Inspired by 1weiho/open-slide。

【硬性技术规格】
- 画布: 每页严格 `width: 1920px; height: 1080px;` 用 `transform: scale(...)` 适配视窗 (默认 `scale(0.7)` 居中)。
- **绝对禁止 overflow**: 每页内容必须 fit in 1920×1080, 不许滚动条出现。
- 字号 type scale (px): `2xs:18 · xs:22 · sm:28 · md:36 · lg:48 · xl:64 · 2xl:88 · 3xl:120 · 4xl:160 · 5xl:220`。
- 边距 padding: 96 / 128 / 160 三档之一。
- 每页有 `<section class="slide" d
... (full details in sub-directory)

### deck-swiss-international
【模板: 瑞士国际主义 Deck (Swiss International)】
【意图】事实、产品、分析、方法论表达。极度冷静、理性、学院派, 没有任何手绘 / 噪点 / 装饰。Inspired by op7418/guizang-ppt-skill Style B。

【主题】**只能从下面 4 套二选一, 不许混用、不许改 hex**:
- 🔵 **Klein Blue (IKB)** — accent `#002FA7`, paper `#fafaf8`, ink `#0a0a0a`. 商业 / AI / 设计场景。
- 🟡 **Lemon Yellow** — accent `#FFD500`, paper `#f7f5ee` (淡奶油), ink `#0a0a0a`. 年轻 / 零售 / 体育。文字必须用黑色 (不能白色)。
- 🟢 **Lemon Green / Neon** — accent `#C5E803`, paper `#f7f5ee`, ink `#0a0a0a`. 可持续 / 科技初创 / Gen-Z 品牌。文字必须用黑色。
- 🟠 **Safety Oran
... (full details in sub-directory)

### frontend-slides
# frontend-slides

> Curated from @zarazhangrui.

## What it does

Generate animation-rich HTML presentations with visual style previews. Useful for online keynotes, embedded talks, and interactive briefs.

## Source

- Upstream: https://github.com/zarazhangrui/frontend-slides
- Category: `slides`

## How to use

This catalogue entry advertises the skill in Open Design so the agent
discovers it during planning. To run the full upstream workflow with
its original assets, scripts, and references, 
... (full details in sub-directory)

### html-ppt-retro-quarterly-review
# Retro Quarterly Review Template

A high-contrast, print-inspired quarterly review template with three cinematic
slides:

1. Cover (hero title lockup)
2. Three priorities (triptych grid)
3. Roadmap timeline + KPI strip

## Resource map

```text
html-ppt-retro-quarterly-review/
├── SKILL.md
├── assets/
│   └── template.html
├── references/
│   └── checklist.md
└── example.html
```

## Workflow

1. Read active `DESIGN.md` first and map any requested token changes into CSS
   variables while prese
... (full details in sub-directory)

### nanobanana-ppt
# nanobanana-ppt

> Curated from @op7418.

## What it does

AI-powered PPT generation with document analysis and styled images via the NanoBanana stack. Combines image generation with structured deck output.

## Source

- Upstream: https://github.com/op7418/NanoBanana-PPT-Skills
- Category: `image-generation`

## How to use

This catalogue entry advertises the skill in Open Design so the agent
discovers it during planning. To run the full upstream workflow with
its original assets, scripts, and 
... (full details in sub-directory)

### ppt-keynote
【模板: Keynote 风格 PPT】
- 每张幻灯片是一个 `<section class="slide">`, 整体宽 1280 高 720, 居中显示, 背景渐变。
- 单页内容极简: 大标题 + 1-3 行支持文字; 或一张数据图; 或一个金句。
- 字号: 标题 `text-7xl font-semibold tracking-tight`, 副标题 `text-2xl text-neutral-500`。
- 第一页是封面 (主题 + 演讲者 / 日期), 最后一页是 "Thanks." 或行动号召。
- 顶部右上角小指示器: 当前页 / 总页数。
- 加一段 JavaScript 监听 ArrowLeft / ArrowRight / 空格键切换 slide; 同时维护 hash (#/3)。
- 每页之间用 fade-in 动画。
- 保持留白, 数据卡片用 grid 布局对齐, 颜色克制。

### pptx
# pptx

> Curated from Anthropic's official skills repository.

## What it does

Read, generate, and adjust PowerPoint slides, layouts, and templates. Useful for executive decks, training material, and product reviews.

## Source

- Upstream: https://github.com/anthropics/skills/tree/main/skills/pptx
- Category: `slides`

## How to use

This catalogue entry advertises the skill in Open Design so the agent
discovers it during planning. To run the full upstream workflow with
its original assets, s
... (full details in sub-directory)

### pptx-generator
# pptx-generator

> Curated from the MiniMax AI team.

## What it does

Create and edit PowerPoint presentations from scratch with PptxGenJS — MiniMax's production-tested deck pipeline.

## Source

- Upstream: https://github.com/MiniMax-AI/skills
- Category: `slides`

## How to use

This catalogue entry advertises the skill in Open Design so the agent
discovers it during planning. To run the full upstream workflow with
its original assets, scripts, and references, install the upstream
bundle int
... (full details in sub-directory)

### pptx-html-fidelity-audit
# PPTX ↔ HTML Fidelity Audit

A repeatable workflow for catching the ways a `python-pptx` export silently drifts from its HTML source — and fixing them with a layout discipline that prevents the same regressions on the next pass.

## When this skill applies

The user has:

- A source HTML slide deck (typically a single-file deck with `<section class="slide">` blocks):

  ```html
  <section class="slide light">
    <div class="chrome">2026 · Q2 review</div>
    <span class="kicker">Pillar 03</spa
... (full details in sub-directory)

### slides
# slides

> Curated from OpenAI's skills repository.

## What it does

Create and edit .pptx presentation decks with PptxGenJS. Useful for sales decks, kickoff briefs, and design-system showcases.

## Source

- Upstream: https://github.com/openai/skills
- Category: `slides`

## How to use

This catalogue entry advertises the skill in Open Design so the agent
discovers it during planning. To run the full upstream workflow with
its original assets, scripts, and references, install the upstream
bun
... (full details in sub-directory)

---

## Anti-Patterns (DO NOT DO)

| # | Anti-Pattern | Why It Fails | Correct Behavior |
|---|-------------|--------------|-----------------|
| 1 | Mixing colors from different palettes in a single deck | Destroys visual coherence instantly; the deck looks amateur and inconsistent | Pick exactly one palette; use only those hex values on every single slide |
| 2 | Cramming multiple messages onto one slide | Audience cognitive load maxes out at one idea per slide; they remember nothing | Split into multiple slides, each anchored to a single key takeaway |
| 3 | Ignoring technical constraints (canvas size, overflow, type scale) | Broken rendering, scrollbars, content cut off during the actual presentation | Test every slide for overflow and fit against the template's exact specs before finalizing |
| 4 | Adding decorative flair or personal "improvements" to a strict template | Swiss/Guizang/Keynote templates rely on restraint; decoration breaks the entire aesthetic system | Follow every template rule exactly; the constraint system is the design, not an obstacle |
| 5 | Designing for yourself instead of the target audience | What is clear and compelling to you may be opaque or irrelevant to executives, clients, or investors | Identify the audience persona before opening any design tool; tailor every content and style decision |

---

## Pre-Output Checklist
- [ ] Exactly one color palette used throughout — no mixing, no modified hex values
- [ ] Each slide carries exactly one key message (split if needed)
- [ ] Audience identified and content tailored accordingly
- [ ] Template constraints verified (canvas size, type scale, overflow, padding)
- [ ] All visual elements serve communication purpose — no empty decoration
- [ ] Output saved to `.openkitty/storage/`

## Output
All output: `.openkitty/storage/`
