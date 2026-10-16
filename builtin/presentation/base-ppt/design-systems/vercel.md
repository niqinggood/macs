# Vercel Design System

## Philosophy
Modern developer platform aesthetic. Dark backgrounds with vibrant accent gradients. Geometric precision, sharp typography, high contrast. Feels fast, technical, cutting-edge. Neon-like accent colors on dark surfaces.

## Color Palette
| Role | Hex | Usage |
|------|-----|-------|
| Background | #000000 | Hero, dark slides |
| Surface | #0a0a0a | Cards, panels |
| Surface Raised | #141414 | Elevated cards |
| Text Primary | #fafafa | Headings, body |
| Text Secondary | #888888 | Captions, metadata |
| Accent | #0070f3 | Primary brand blue |
| Accent Gradient 1 | #0070f3 | Gradient start |
| Accent Gradient 2 | #ff0080 | Gradient end (pink) |
| Purple | #7928ca | Secondary accent |
| Cyan | #00dfd8 | Tertiary accent |
| Success | #00cc66 | Positive metrics |
| Border | #222222 | Subtle borders |

## Typography
- **Display**: Inter, system-ui — 56-72px, 800 weight, -1px tracking
- **Heading**: Inter — 32-40px, 700 weight
- **Body**: Inter — 16-18px, 400 weight, line-height 1.7
- **Code**: 'SF Mono', 'Fira Code', monospace — 14px
- **Label**: Inter — 12-14px, 600 weight, uppercase, letter-spacing 0.5px

## Layout
- **Hero**: Full-bleed black, centered or left-aligned. Gradient text on main heading (0070f3 → ff0080). Subtitle in secondary text. CTA row below
- **Content**: Can be dark (black bg) or light (white bg) — switch per section
- **2-column split**: Text left (40%) + visual (code block, diagram, screenshot) right (60%)
- **Grid**: 12-column, 24px gutter, 80px page margin

## Components
- **Cards**: Surface bg (#0a0a0a), 1px border (#222), 12px radius, 24px padding, hover glow border
- **Code blocks**: Terminal-style — #0a0a0a bg, green prompt, syntax highlighted code, 1px #222 border
- **Gradient accents**: Used sparingly for emphasis — gradient underline on headings, gradient dot indicators, gradient stat bars
- **Tables**: Dark, minimal — no bg headers, just weight difference, thin #222 borders
- **Buttons**: Geometric — 8px radius, solid accent fill, white text, arrow icon trailing
- **Logos/Partners grid**: Grayscale logos on dark bg, 3-4 per row

## Chart Style
- Dark theme charts: transparent bg, #888 gridlines at 5% opacity
- Accent gradient for primary series
- Monospace labels
- Minimal — no chart borders, no background fill
