---
name: data-analysis
description: >
  Academic and business data analysis with complete report delivery. Statistical
  testing, chart generation, data cleaning, insight synthesis, and Markdown
  report writing. Trigger: 数据分析/data analysis/统计分析/画图/统计检验/分析报告.
version: 1.0.0
---

# Data Analysis

Statistical analysis, visualization, and complete report delivery for research
and business data. Do not stop at raw statistics or chart paths when the user asks
for data analysis; synthesize the results into a readable report unless the user
explicitly asks for only a calculation, chart, or code snippet.

## Analysis Types

| Analysis | When | Tools |
|----------|------|-------|
| Descriptive | Summarize data | mean, median, SD, IQR |
| Comparative | Group differences | t-test, ANOVA, Mann-Whitney |
| Correlational | Relationships | Pearson, Spearman, Kendall |
| Regression | Prediction | Linear, Logistic, Mixed-effects |
| Survival | Time-to-event | Kaplan-Meier, Cox PH |
| Dimensionality | Feature reduction | PCA, Factor Analysis |

## Statistical Reporting

Follow APA 7 standards:
```
t(df) = X.XX, p = .XXX, Cohen's d = X.XX
F(df1, df2) = X.XX, p = .XXX, η² = X.XX
χ²(df, N = XXX) = X.XX, p = .XXX
r(df) = X.XX, p = .XXX
```

## Visualization

| Chart Type | When |
|------------|------|
| Bar chart | Categorical comparisons |
| Box plot | Distribution + outliers |
| Scatter plot | Correlation/regression |
| Line chart | Time series |
| Heatmap | Correlation matrix |
| Forest plot | Meta-analysis |

## Report Delivery

For data-analysis tasks, produce a complete Markdown report saved under
`.openkitty/storage/` when file writing is available.

If the user explicitly asks for PPT, PPTX, PowerPoint, slides, 幻灯片, 演示文稿,
deck, or 汇报材料, create a real `.pptx` deck after the Markdown report and
charts. Save it under `.openkitty/storage/`, reuse real generated chart PNGs, and
verify the deck by reopening it to check slide count and file size. The final
answer must include the PPTX path; do not stop at a Markdown summary.

Required report structure:
1. Executive summary: 3-6 conclusion-first bullets.
2. Data source and analysis scope: source file/path, filters, assumptions.
3. Data overview: row/column counts, fields, sample or time range.
4. Data quality: missing values, duplicates, outliers, limitations.
5. Core findings: each finding must cite a metric, table, or observed pattern.
6. Chart interpretation: reference real generated PNG paths and explain what each
   chart proves or does not prove.
7. Recommendations: operational or research next steps tied to the findings.
8. Output inventory: report path, structured data path, chart paths, skipped items.

When the data does not contain sales fields, do not force sales-funnel language.
Use generic statistical insights, anomalies, distributions, segments, trends, and
actionable recommendations. If data is insufficient, write a degraded report and
state the limitation instead of pretending the missing analysis was completed.

## Integration

Uses openkitty's Python execution for pandas/matplotlib/seaborn analysis.
For a standard editable presentation: use `builtin/presentation/base-ppt` only when
the user asks for PPT/slides; otherwise the default deliverable is a Markdown
analysis report plus charts.
