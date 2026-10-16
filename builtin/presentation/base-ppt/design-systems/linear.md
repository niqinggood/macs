# Linear Design System

## Philosophy
Dark, sleek, developer-first. Precision tool aesthetic. Monospace accents, keyboard-shortcut style labels, minimal chrome. Feels like a premium code editor. Uses subtle borders and minimal color — lets accent colors pop dramatically.

## Color Palette
| Role | Hex | Usage |
|------|-----|-------|
| Background | #0d0d0d | Default slide |
| Surface | #1a1a1a | Cards, panels |
| Surface Hover | #222222 | Interactive elements |
| Text Primary | #e6e6e6 | Headings, body |
| Text Secondary | #888888 | Captions, metadata |
| Accent | #5e6ad2 | Primary CTAs, highlights |
| Accent Hover | #6e7af2 | Hover |
| Success | #0eb573 | Positive indicators |
| Danger | #e5484d | Negative, warnings |
| Warning | #f5a623 | Alerts |
| Border | #2a2a2a | Subtle dividers |

## Typography
- **Display**: Inter, system-ui — 48-56px, 700 weight, -0.5px tracking
- **Heading**: Inter — 24-32px, 600 weight
- **Body**: Inter — 15-17px, 400 weight, line-height 1.6
- **Code/Label**: 'SF Mono', 'JetBrains Mono', monospace — 12-14px, used for labels, tags, data
- **Keyboard/KBD**: Monospace, 11px, #888888, subtle border, 4px border-radius

## Layout
- **Standard**: 2-panel with sidebar (dark surface) + main content
- **Hero**: Centered, large heading, subtitle, CTA row
- **Grid**: 12-column, 20px gutter, asymmetric encouraged
- **Spacing**: Generous vertical rhythm — 32px between sections, 16px within

## Components
- **Cards**: Surface bg (#1a1a1a), 6px border-radius, 1px border (#2a2a2a), 20px padding
- **Code blocks**: Darker bg (#0a0a0a), monospace, syntax highlighting (green strings, blue keywords, purple functions)
- **Tables**: Minimal — thin #2a2a2a borders, no alternating rows, header bold + uppercase + letter-spacing
- **Buttons**: Subtle — surface bg, 1px border, hover lifts border to accent color
- **Badges/Tags**: Monospace font, pill shape, subtle bg + border, 11px
- **Progress bars**: 4px height, accent fill, surface track, 2px radius

## Charts
- Dark theme: #1a1a1a background, #e6e6e6 gridlines at 10% opacity
- Accent color for primary data series
- Minimal axis labels, monospace font
- No unnecessary chart junk (drop shadows, 3D, gradients)
