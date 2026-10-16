# Stripe Design System

## Philosophy
Clean, modern fintech. Precision without coldness. Gradients add warmth. Data is beautiful. Uses subtle blur effects and glass-morphism for depth. Strong grid alignment, generous whitespace.

## Color Palette
| Role | Hex | Usage |
|------|-----|-------|
| Background | #f6f9fc | Default slide background |
| Card BG | #ffffff | Cards, content panels |
| Text Primary | #0a2540 | Headings, body |
| Text Secondary | #425466 | Captions, metadata |
| Accent | #635bff | Primary CTA, links |
| Accent Hover | #7c73ff | Hover state |
| Gradient 1 | #635bff | Stripe-style gradient |
| Gradient 2 | #00d4ff | Stripe-style gradient |
| Success | #00b86b | Positive metrics |
| Warning | #ffc233 | Alerts |
| Border | #e6ebf1 | Card/table borders |

## Typography
- **Display**: Soehne, system-ui — 48-56px, 700 weight
- **Heading**: Soehne, system-ui — 28-36px, 600 weight
- **Body**: Soehne, system-ui — 16-18px, 400 weight, line-height 1.6
- **Code/Metrics**: 'SF Mono', 'Fira Code', monospace — 14-16px

## Layout
- **Hero**: Gradient background (635bff → 00d4ff), centered value prop + CTA
- **Content**: Two-column grid, text left + visual right
- **Data slides**: Full-width cards in 2×2 or 3×2 grid, each with a KPI
- **Spacing**: 48px between cards, 24px within cards
- **Grid**: 12-column, 24px gutter

## Components
- **Cards**: White bg, 8px border-radius, subtle shadow (0 2px 12px rgba(0,0,0,0.06)), 24px padding
- **KPI cards**: Large number (36-48px, monospace, accent color), small label below (14px, secondary text)
- **Tables**: Clean, striped rows (#f6f9fc alternate), header with bottom border only, 12px cell padding
- **Buttons**: Pill shape, accent gradient, 16px horizontal padding
- **Badges**: Rounded pill, semi-transparent bg, 12px font

## Data Visualization
- Line charts: Smooth curves, gradient fill below
- Bar charts: Rounded top corners (4px), accent color
- Donut charts: 8px stroke, gradient segments
- Always include value labels on data points
