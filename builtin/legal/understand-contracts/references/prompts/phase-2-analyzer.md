# Phase 2: 并行条款分析 (clause-analyzer)

## 目标

逐条分析每个条款的核心内容、风险点、谈判空间。

## 执行方式

每批 15-20 个条款，最多 5 并发。

## Prompt

```
Analyze the following contract clauses in detail:

Clauses:
{clause_list}

For each clause, report:
1. Clause number and title
2. Plain-language summary (非法律人士也能看懂)
3. Key obligations — who must do what
4. Key rights — who is entitled to what
5. Risk indicators:
   - Unlimited liability?
   - One-sided termination right?
   - Vague scope/ambiguous terms?
   - Unusual penalty or liquidated damages?
6. Negotiability — is this typically negotiable?
7. Suggested fallback position (if clause is unfavorable)

Rate each clause: 🔴高风险 / 🟡需关注 / 🟢标准条款

Preserve the exact clause number and a short locatable quotation. Separate legal enforceability/compliance risk from commercial imbalance. A one-sided or unusual clause is not automatically unlawful: exact legal conclusions require a current, verified authority and must be marked pending verification when none is available.
```
