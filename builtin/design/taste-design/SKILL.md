---
name: taste-design
description: >-
  Anti-slop frontend design system for AI agents. Combines Google DESIGN.md philosophy (prose-first, specific references, token+rationale format) with concrete anti-slop rules (no purple gradients, no Inter, no heavy shadows). Use when: building any frontend, HTML page, UI, CSS, scaffold DESIGN.md, design system, 设计, 美化, 配色, 界面. Read BEFORE writing frontend code.
version: 2.0.0
author: OpenKitty
status: stable
---

# Taste Design — Anti-Slop UI System

Google DESIGN.md philosophy: **prose defines intent, tokens provide precision, constraints create character.**
Plus concrete anti-slop rules tested across hundreds of generated UIs.

---

## ⚠️ IRON RULES

1. **Read this BEFORE any frontend code.** Never skip.
2. **Prose-first.** Describe intent ("a Swiss railway departure board, 1972") — not adjectives ("modern, clean").
3. **One accent color.** Scarcity outside its purpose makes its presence meaningful.
4. **Negative constraints define character.** What you DON'T use matters more than what you DO.
5. **Scaffold, don't rewrite.** Use `npx claude-plugin-design-md` for new DESIGN.md files — don't write tokens from memory.

---

## Quick Start: Scaffolding a DESIGN.md

```bash
# Starter template (blank — every section, neutral palette)
npx -y claude-plugin-design-md

# Or pick an example:
npx -y claude-plugin-design-md atmospheric-glass
```

**Validate:**
```bash
npx @google/design.md lint DESIGN.md
```

**Export to Tailwind:**
```bash
npx @google/design.md export --format tailwind DESIGN.md       # v3 JSON
npx @google/design.md export --format css-tailwind DESIGN.md   # v4 CSS @theme
```

---

## DESIGN.md Format

A DESIGN.md has two layers (see `references/design-md-spec.md` for full spec):

1. **YAML front matter** — Machine-readable tokens.
2. **Markdown body** — Human-readable rationale organized into `##` sections.

**Canonical section order:** Overview → Colors → Typography → Layout → Elevation & Depth → Shapes → Components → Do's and Don'ts.

### Quick Reference: Token Types

| Type | Format | Example |
|------|--------|---------|
| Color | Any CSS color | `"#1A1C1E"`, `"oklch(62% 0.18 250)"` |
| Dimension | number + unit | `48px`, `-0.02em` |
| Token Ref | `{path.to.token}` | `{colors.primary}` |
| Typography | object with fontFamily, fontSize, etc. | See references |

Component tokens reference design tokens:
```yaml
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    rounded: "{rounded.md}"
```

---

## Design Philosophy

### 1. Specific Reference > Generic Adjectives
```
❌ "A modern, clean dashboard with blue accents"
✅ "A 1960s NASA mission control console — amber CRTs, toggle switches, concrete."
```
A specific reference carries a complete world. Adjectives describe a region; a reference describes a point.

### 2. Negative Constraints Come Free
Name the reference and the "don'ts" arrive automatically. A Swiss railway board has no gradients. A lecture handout has no hero sections.

### 3. One Accent, Used Sparingly
The scarcity of an accent outside its purpose makes its presence inside meaningful.

### 4. Modest Typography Scale
Section title ~1.9× body, not 5× body. Trust modest differences. Don't use Bold everywhere.

---

## Concrete Rules: Default Palette

When no DESIGN.md exists, use this warm monochrome palette:

| Role | Value | Purpose |
|------|-------|---------|
| Page bg | `#E8E4DE` | Warm stone — never pure white |
| Sidebar/cards | `#DFDBD4` | Warm sand |
| Surface | `#FFFFFF` | Card interiors |
| Borders | `#D5D0C8` | Subtle separation |
| Primary text | `#111111` | Off-black — never `#000` |
| Secondary text | `#787774` | Warm gray |
| Primary btn | `#111111` bg, `#FFFFFF` text | Dark, no shadow |
| Accent (links) | `#1F6C9F` or `#346538` | Desaturated, calm |
| Error | `#B3261E` | Standard semantic |

---

## Typography

- **Body**: System font stack (`-apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`)
- **Headings**: Tighter tracking (`letter-spacing: -0.02em`), `line-height: 1.2`
- **Body text**: `line-height: 1.6`, `max-width: 65ch`
- **Never**: pure `#000000` text, Inter/Roboto as defaults
- **Weights**: Medium(500) + SemiBold(600) for hierarchy, not just Regular + Bold

---

## Components

- **Cards**: `border: 1px solid #EAEAEA`, `border-radius: 8px`, NO shadow (or `0 1px 3px rgba(0,0,0,0.04)` max)
- **Buttons**: `border-radius: 6px`, `background: #111`, `color: #fff`. Hover: `background: #333`. No shadow.
- **Active tabs**: `border-bottom: 2px solid #111`, NOT colored background blocks
- **Inputs**: `border: 1px solid #EAEAEA`, `border-radius: 6px`, focus: `border-color: #111`
- **Dividers**: `border-bottom: 1px solid #EAEAEA`, never colored lines
- **Badges**: Pill-shaped, muted pastel backgrounds

---

## Shadows (only when unavoidable)

- Cards: `0 1px 3px rgba(0,0,0,0.04)` or none
- Elevated: `0 1px 4px rgba(0,0,0,0.06)` max
- Elevation comes from borders, not shadows

---

## Motion

- Transitions: `200-300ms`, `transform` + `opacity` only
- Hover: `scale(0.98)` or background shift
- No scroll-jacking, parallax, or gratuitous animation
- Staggered lists: `animation-delay: calc(var(--index) * 80ms)`

---

## Layout

- Container: `1200-1440px` max-width, auto margins
- Section padding: `py-24` minimum — generous
- Cards: asymmetrical bento over 3 equal columns
- `min-height: 100dvh` not `100vh`

---

## Anti-Slop Hall of Shame

| # | NEVER | Reason |
|---|-------|--------|
| 1 | Purple-to-blue gradients (`#667eea → #764ba2`) | AI signature slop |
| 2 | Inter / Roboto / Open Sans as defaults | No typography thinking |
| 3 | Heavy drop shadows (`shadow-lg`, `shadow-xl`) | Visual noise |
| 4 | Glassmorphism without intent | Unless frosted glass IS the reference |
| 5 | Giant gradient CTA hero sections | SaaS template, not design |
| 6 | Emoji as section icons | Lazy, not intentional |
| 7 | "Welcome to [Product]" hero | Zero information |
| 8 | `rounded-full` on containers | Default, not decided |
| 9 | AI copy: "Elevate", "Seamless", "Unleash", "Next-Gen" | Meaningless |
| 10 | Fade-in-on-scroll / animated counters | Motion without meaning |

---

## Pre-Delivery Checklist

- [ ] Design intent is a specific reference, not adjectives
- [ ] One accent color, used sparingly
- [ ] Typography has a reason (why this font?)
- [ ] Spacing is intentional, not Tailwind defaults
- [ ] No shadows unless necessary
- [ ] Color contrast ≥ WCAG AA (4.5:1)
- [ ] No slop hall-of-shame items
- [ ] Components reference DESIGN.md tokens where possible

---

## References

- `references/design-md-spec.md` — Full Google DESIGN.md format specification
- `references/philosophy.md` — Design philosophy deep-dive
- `templates/DESIGN.md` — Blank starter template (scaffold via `npx claude-plugin-design-md`)

Output: `.openkitty/storage/`
