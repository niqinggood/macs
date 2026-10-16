---
name: design
description: Visual design system for all outputs. Read taste-design BEFORE any visual work. 设计, 美化, 配色, 界面, 样式.
version: 1.0.0
author: OpenKitty
status: stable
---

# Design Skills

Visual design system. Covers anti-slop aesthetics, design system scaffolding, and web restyling.

---

## ⚠️ IRON RULES

1. **Read `taste-design` FIRST.** Never write visual code without loading it.
2. **Prose-first.** Describe design intent with a specific reference, not adjectives.
3. **One accent color.** Scarcity creates meaning.
4. **No AI slop.** Purple gradients, Inter font, heavy shadows, emoji spam — all banned.
5. **Tokens are context.** Exact values support the prose, not replace it.

---

## Sub-skills

### taste-design
**Use when**: Writing ANY visual code — HTML, CSS, UI components, web pages, mini-programs, dashboards, presentation themes.

The anti-slop design system. Google DESIGN.md philosophy + concrete rules tested across hundreds of generated UIs. Defines: default palette (warm monochrome), typography stack, component specs, motion guidelines, anti-slop hall of shame, scaffolding workflow (`npx claude-plugin-design-md`).

**IMPORTANT**: Load `skills/design/taste-design/SKILL.md` BEFORE generating any frontend or visual code. This is NOT optional.

### web-design
**Use when**: Restyling an existing webpage, applying one site's visual style to another, or extracting and replicating a design system from a reference website.

5-step workflow: Extract content → Analyze reference style → Map style to content → Build components → Polish. Has bundled Python tools for design extraction and validation.

---

## Decision Guide

| User says | Use |
|-----------|-----|
| "设计一个网页/登录页/仪表盘" | `taste-design` first, then `web-design` |
| "改样式/配色/美化" | `taste-design` |
| "让 X 看起来像 Y" | `web-design` (extract Y's style → apply to X) |
| "提取这个网站的设计系统" | `web-design` |
| "帮我做小程序设计" | `taste-design` (universal anti-slop rules apply) |
| "PPT 配色方案" | `taste-design` |
| "Scaffold a DESIGN.md" | `taste-design` → `npx claude-plugin-design-md` |

---

## Anti-Patterns

| # | Anti-Pattern | Correct |
|---|-------------|---------|
| 1 | Jumping to HTML without reading taste-design | Load taste-design first, ALWAYS |
| 2 | "Modern, clean, trustworthy" as design brief | Use a specific reference ("Swiss railway board, 1972") |
| 3 | Using purple gradients + Inter + heavy shadows | Banned by taste-design |
| 4 | Skipping content extraction before restyling | Extract content → understand hierarchy → then design |
| 5 | Copying CSS tokens without understanding the philosophy | Design is philosophy applied to content, not just tokens |

Output: `.openkitty/storage/`
