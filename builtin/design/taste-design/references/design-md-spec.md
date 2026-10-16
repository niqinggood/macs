# DESIGN.md Format Specification (Condensed)

Source: Google Labs DESIGN.md spec. Full spec at `npx @google/design.md spec`.

## File Structure

1. **YAML front matter** — Machine-readable design tokens, delimited by `---` fences.
2. **Markdown body** — Human-readable design rationale in `##` sections.

## Token Schema

```yaml
version: <string>           # optional
name: <string>              # required
description: <string>       # optional
colors:
  <token-name>: <Color>
typography:
  <token-name>:
    fontFamily: <string>
    fontSize: <Dimension>
    fontWeight: <string|number>
    lineHeight: <Dimension>          # optional
    letterSpacing: <Dimension>       # optional
    fontFeature: <string>            # optional
    fontVariation: <string>          # optional
rounded:
  <scale-level>: <Dimension>
spacing:
  <scale-level>: <Dimension | number>
components:
  <component-name>:
    <property>: <string | TokenRef>
```

## Token Types

| Type | Format | Example |
|------|--------|---------|
| Color | Any CSS color | `"#1A1C1E"`, `"rgb(26,28,30)"`, `"oklch(62% 0.18 250)"` |
| Dimension | number + unit (px, em, rem) | `48px`, `-0.02em`, `1.5` |
| TokenRef | `{path.to.token}` | `{colors.primary}`, `{typography.body}` |

## Section Order

Sections use `##` headings. Omitted sections are fine; present sections must follow this order:

| # | Section | Aliases |
|---|---------|---------|
| 1 | Overview | Brand & Style |
| 2 | Colors | |
| 3 | Typography | |
| 4 | Layout | Layout & Spacing |
| 5 | Elevation & Depth | Elevation |
| 6 | Shapes | |
| 7 | Components | |
| 8 | Do's and Don'ts | |

## Component Properties

Valid properties: `backgroundColor`, `textColor`, `typography`, `rounded`, `padding`, `size`, `height`, `width`.

Variants (hover, active, pressed) = separate component entries with related key names:
```yaml
components:
  button-primary:         { backgroundColor: "{colors.primary}" }
  button-primary-hover:   { backgroundColor: "{colors.primary-container}" }
  button-primary-active:  { backgroundColor: "{colors.primary}" }
  button-primary-disabled:{ backgroundColor: "{colors.surface-container}" }
```

## Linting Rules

| Rule | Severity | Checks |
|------|----------|--------|
| `broken-ref` | error | Token refs that don't resolve |
| `missing-primary` | warning | Colors defined but no `primary` |
| `contrast-ratio` | warning | Component text/background below WCAG AA (4.5:1) |
| `orphaned-tokens` | warning | Color defined but never referenced |
| `missing-sections` | info | Optional sections absent when related tokens exist |
| `missing-typography` | warning | Colors but no typography |
| `section-order` | warning | Sections out of canonical order |
| `unknown-key` | warning | Looks like a typo of a known key |

## Consumer Behavior

| Scenario | Behavior |
|----------|----------|
| Unknown section heading | Preserve; do not error |
| Unknown color token name | Accept if valid value |
| Unknown component property | Accept with warning |
| Duplicate section heading | Error; reject |

## Export Formats

```bash
npx @google/design.md export --format tailwind DESIGN.md     # JSON → tailwind.config.js
npx @google/design.md export --format css-tailwind DESIGN.md # CSS @theme block
npx @google/design.md export --format dtcg DESIGN.md         # W3C Design Tokens JSON
```
