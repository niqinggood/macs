---
name: understand-contracts
slug: understand-contracts
version: 1.1.0
description: "合同条款知识图谱 — 7 Agent 流水线分析法律合同，提取条款关联、风险点、义务权利、合规清单。"
read_when:
  - reviewing a contract or legal document
  - assessing contract risks
  - extracting obligations and rights from a contract
  - building a compliance checklist
  - comparing multiple contracts
metadata:
  type: builtin
  tags: [legal, contract-analysis, risk-assessment, compliance]
  os: [linux, darwin, win32]
---

# Understand Contracts

## 适用场景

- 审查新合同，快速识别风险条款
- 对比多份合同中的同类条款差异
- 构建企业合同知识库
- 为合同谈判准备条款对照表

## 7 Agent 流水线

```
Phase 1: contract-scanner     → 合同结构扫描（1 agent）
Phase 2: clause-analyzer      → 并行条款分析（5并发，每批15-20条）
Phase 3: risk-analyzer        → 风险矩阵构建（1 agent）
Phase 4: obligation-extractor → 权利义务提取（1 agent）
Phase 5: compliance-builder   → 合规检查清单生成（1 agent）
Phase 6: consistency-reviewer → 条款一致性审核（1 agent）
Phase 7: cross-reference      → 关联合同/法规交叉引用（1 agent）
```

## 风险等级

| 等级 | 标识 | 行动 |
|------|------|------|
| Critical | 🔴 | 必须修改后才能签署 |
| High | 🟠 | 重点谈判，争取修改 |
| Medium | 🟡 | 尝试优化，保留记录 |
| Low | 🟢 | 标准条款，可接受 |

## 输出

```
storage/contract-graph/{contract_name}/
├── 01-contract-overview.md
├── 02-clause-analyses/
├── 03-risk-matrix.md
├── 04-obligations-rights.md
├── 05-compliance-checklist.md
├── 06-consistency-review.md
├── 07-cross-references.md
└── contract-graph.json
```

## Phase Prompts

| Phase | 文件 |
|-------|------|
| Phase 1 | [references/prompts/phase-1-scanner.md](references/prompts/phase-1-scanner.md) |
| Phase 2 | [references/prompts/phase-2-analyzer.md](references/prompts/phase-2-analyzer.md) |
| Phase 3 | [references/prompts/phase-3-risk.md](references/prompts/phase-3-risk.md) |
| Phase 4 | [references/prompts/phase-4-obligations.md](references/prompts/phase-4-obligations.md) |
| Phase 5 | [references/prompts/phase-5-compliance.md](references/prompts/phase-5-compliance.md) |
| Phase 6 | [references/prompts/phase-6-review.md](references/prompts/phase-6-review.md) |
| Phase 7 | [references/prompts/phase-7-crossref.md](references/prompts/phase-7-crossref.md) |
