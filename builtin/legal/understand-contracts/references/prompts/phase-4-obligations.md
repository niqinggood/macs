# Phase 4: 权利义务提取 (obligation-extractor)

## 目标

提取各方的具体义务和权利，形成可执行的义务清单。

## 输入

Phase 2 所有条款分析。

## Prompt

```
From the contract analysis, extract all obligations and rights:

[Phase 2 outputs]

For each party, create:

### Party A Obligations
| # | Obligation | Clause Ref | Deadline/Trigger | Consequence of Breach |
|---|-----------|------------|------------------|----------------------|
| 1 | Pay license fee | 4.2 | Within 30 days | Late fee + interest |

### Party A Rights
| # | Right | Clause Ref | Condition | Remedy if Violated |
|---|-------|------------|-----------|-------------------|

### Party B Obligations + Rights
(same structure)

Also extract:
- Mutual obligations
- Conditional obligations (triggered by specific events)
- Survival clauses — obligations that continue after termination
```
