# Phase 6: 条款一致性审核 (consistency-reviewer)

## 目标

检查合同内部的逻辑一致性，发现矛盾或漏洞。

## 输入

Phase 1-5 全部输出。

## Prompt

```
Review the full contract analysis for consistency:

[All Phase 1-5 outputs]

Check:
1. Internal contradictions — does clause X say one thing but clause Y implies another?
2. Missing definitions — terms used but not defined
3. Cross-reference errors — "as set forth in Section 5.2" but 5.2 doesn't exist
4. Gap analysis — what's NOT covered that normally would be in this contract type?
   - No data protection clause in a SaaS contract?
   - No IP assignment in a development contract?
5. Ambiguity traps — clauses that could be interpreted multiple ways
6. Time inconsistency — conflicting deadlines or timelines

Flag each finding as: 🔴 Critical (must fix) / 🟡 Important (should address) / 🔵 Note (FYI)
```
