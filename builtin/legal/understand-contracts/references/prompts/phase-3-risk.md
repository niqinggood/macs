# Phase 3: 风险矩阵构建 (risk-analyzer)

## 目标

综合所有条款分析，构建整体风险视图。

## 输入

Phase 2 所有条款分析。

## Prompt

```
Based on the clause-by-clause analysis below, build a risk matrix:

[Phase 2 outputs]

Build:
1. Risk heat map — which sections of the contract concentrate the most risk?
2. Top 5 risk items with severity, trigger conditions, and an evidence-based likelihood assessment
3. Risk interconnections — e.g., "termination clause + IP clause together create risk of losing IP on termination"
4. Risk mitigation recommendations:
   - Must-fix before signing
   - Should-negotiate
   - Acceptable as-is
5. Industry benchmark — are these risk levels typical for this contract type? Cite a source or label this as an unverified experience-based judgment.

Output as a structured risk matrix with clear action items.

Do not invent numerical probabilities, market ranges, or industry practice. If the contract and available evidence do not support likelihood, state which event would activate the risk instead.
```
