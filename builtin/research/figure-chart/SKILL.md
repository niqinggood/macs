---
name: figure-chart
description: >
  Create publication-quality figures and charts from research data.
  Python matplotlib/seaborn, schema diagrams, flowcharts.
  Trigger: 作图/画图/figure/chart/图表/diagram/示意图.
version: 1.0.0
---

# Figure & Chart Generation

Publication-quality scientific figures.

## Figure Types

| Type | Tool | Use Case |
|------|------|----------|
| Statistical charts | Python matplotlib/seaborn | bar, scatter, box, heatmap |
| Scientific diagrams | Python matplotlib | molecular, anatomical |
| Flowcharts | Mermaid/D2 | methods workflow |
| Schematic diagrams | LLM description → code | conceptual models |
| Network graphs | Python networkx | citation networks, pathways |

## Journal Requirements

Common specifications:
- Resolution: 300 DPI minimum
- Format: TIFF/EPS (vector preferred)
- Font: Arial/Helvetica, ≥8pt
- Color: CMYK for print, RGB for online
- Figure legends: separate from figure file

## Color Palettes

For accessibility (colorblind-safe):
- Okabe-Ito palette
- viridis/plasma/inferno
- tableau-colorblind10

## Workflow

1. User provides data or describes figure
2. Generate Python script using matplotlib/seaborn
3. Execute: `python3 script.py → output.png`
4. Review and iterate

## Integration

Uses openkitty's Python execution environment.
For complex multi-panel figures, uses `data-analysis` for prep.
