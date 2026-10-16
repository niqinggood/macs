# DESIGN.md Philosophy

Source: Google DESIGN.md PHILOSOPHY.md. Distilled for AI agent consumption.

## Core Principle

**The quality of a generated design is determined less by the precision of its values than by how clearly the intent is described.**

Tokens are context that serves as a reference in the prose. They are not rendering instructions.

## Prose > Tokens

```
DESIGN.md contains two layers: tokens (YAML) and prose (Markdown).
The prose is where the design lives. Everything else exists to support it.
```

A design that references "A 1970s graduate lecture handout in the tradition of an old and established university" evokes:
- One color of ink
- Generous margins
- Serif set at reading size
- Absence of decoration

That single sentence carries more useful information than a dozen metric values.

## Specific Reference > Adjectives

"Modern, clean, trustworthy, premium" evokes nothing specific. A model creates something in the center of what those words describe — generic output.

A specific reference describes a point. Adjectives describe a region.

## Negative Constraints

Naming the reference names the restrictions automatically. A lecture handout does not glow or use gradients. You don't have to list these.

An intentional list of do's and don'ts paired with a strong reference is the sweet spot.

```
✅ "Don't add a hero moment to the title page. A real handout
    title page is the first page of content, not a magazine cover."
❌ "Don't use bold, anywhere. Don't use sans-serif, anywhere.
    Don't add corner ornaments. Don't use a display serif..."
```

When the "don'ts" list grows long, the reference was too vague.

## Format Grows Through Users, Not Spec

The spec defines the structural minimum (colors, typography, spacing, rounded, components). Everything beyond that is yours to define. Add motion tokens, iconography guidelines, casing rules — the format accepts any key, any section.

The linter accepts custom values. Agents read the prose. No spec change needed.

## Print-First Mentality

Design for the printed page. The screen is the substrate; the design is the page. This mindset eliminates:
- Glows
- Glass surfaces
- Drop shadows
- Gratuitous animation
- Dark mode as default

Unless the specific reference demands them.
