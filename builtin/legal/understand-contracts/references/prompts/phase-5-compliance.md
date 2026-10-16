# Phase 5: 合规检查清单生成 (compliance-builder)

## 目标

基于合同条款，生成可执行的合规检查清单。

## 输入

Phase 4 权利义务提取结果。

## Prompt

```
Based on the contract obligations, generate a compliance checklist:

[Phase 4 output]

Generate:
1. **Pre-signing checklist** — what to verify before signing
2. **Ongoing compliance calendar** — recurring obligations with dates
   - Example: "Q1 royalty report due within 30 days of quarter end"
3. **Event-triggered obligations** — things that must happen when X occurs
   - Example: "Notify counterparty within 5 days of change of control"
4. **Record-keeping requirements** — what documents/records must be maintained
5. **Audit trail recommendations** — how to prove compliance if challenged

Format as executable checklists that a non-legal team member can follow.
```
