# Phase 6: 质量审核 (graph-reviewer)

## 目标

审核前 5 个阶段的产出，检查准确性、完整性、一致性。

## 输入

所有前序阶段的输出。

## Prompt

```
Review the following codebase analysis outputs for quality:

[All Phase 1-5 outputs]

Check:
1. Accuracy — do the architecture descriptions match the actual file analyses? Spot-check key claims.
2. Completeness — are there obvious gaps? Files or modules not covered?
3. Consistency — do different phases agree? If Phase 3 says X but Phase 2 shows Y, flag it.
4. Actionable suggestions — what would make this analysis more useful?

Be critical but constructive. The goal is to make the knowledge graph trustworthy.
```

## 输出格式

```
【审核报告】
准确性检查：
  ✅ 架构分层描述与代码一致
  ⚠️ Phase 2 中 module X 的分析遗漏了文件 Y
  ❌ 错误描述及修正

完整性检查：
  - 已分析文件：X/Y（缺少 Z 个）
  - 缺少的模块：[list]

一致性检查：
  - 不一致之处及修正建议

建议改进：
  - [具体建议]
```
