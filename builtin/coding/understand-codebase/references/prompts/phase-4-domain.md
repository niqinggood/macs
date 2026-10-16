# Phase 4: 业务域概念提取 (domain-analyzer)

## 目标

从代码中提取业务域的核心概念、实体、流程。

## 输入

Phase 2 的模块分析 + Phase 3 的架构分析。

## Prompt

```
From the code analyses below, extract the business domain model:

[Phase 2 outputs]
[Phase 3 output]

Extract:
1. Core domain entities — what are the key business concepts? Their attributes, states, lifecycles
2. Business processes — trace end-to-end workflows through the code
3. Business rules — constraints, validations, state transitions that encode business logic
4. Terminology glossary — map code identifiers to business meanings
5. Domain boundaries — suggest bounded contexts if this were to be split into microservices

This is the most valuable output — it bridges the gap between code and business understanding.
```

## 输出格式

```
【业务域知识图谱】
核心实体：
  - Entity（含义）— 属性、状态、生命周期
业务流程：
  - 流程名：Step1 → Step2 → Step3
业务规则：
  - 规则描述
术语表：
  | 代码中 | 业务含义 | 所属域 |
  |--------|---------|--------|
```
