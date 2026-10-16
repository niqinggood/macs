---
name: understand-codebase
slug: understand-codebase
version: 1.1.0
description: "代码库知识图谱生成 — 7 Agent 流水线分析项目结构、架构分层、业务域、依赖关系。"
read_when:
  - analyzing a codebase or project structure
  - understanding how a codebase is organized
  - generating an onboarding guide for developers
  - extracting business domain knowledge from code
  - mapping code dependencies or architecture
  - preparing for a major refactoring
metadata:
  type: builtin
  tags: [code-analysis, knowledge-graph, architecture, onboarding]
  os: [linux, darwin, win32]
---

# Understand Codebase

## 适用场景

- 接手新项目，快速理解代码结构
- 修改核心模块前，分析影响范围
- 生成新人入职指南
- 提取业务域概念和术语
- 为项目自动生成 CLAUDE.md

## 7 Agent 流水线

```
Phase 1: project-scanner     → 项目结构扫描（1 agent）
Phase 2: file-analyzer       → 并行文件分析（5并发，每批≤30文件）
Phase 3: architecture-analyzer → 架构分层识别（1 agent）
Phase 4: domain-analyzer     → 业务域概念提取（1 agent）
Phase 5: tour-builder        → 学习路径生成（1 agent）
Phase 6: graph-reviewer      → 质量审核（1 agent）
Phase 7: article-analyzer    → 文档/知识库图谱化（1 agent）
```

Phase 2 并发控制：最多 5 agent 并行，单 agent 处理 ≤30 文件，超时 300s，失败重试 2 次。

## 执行模式

| 模式 | 命令 | 执行 Phase |
|------|------|-----------|
| 全量 | `/understand-codebase {path}` | 1→2→3→4→5→6→7 |
| 快速 | `--quick` | 1→3 |
| 增量 | `--diff` | 仅分析变更文件 |

## 输出

```
storage/codebase-graph/
├── 01-project-overview.md
├── 02-module-analyses/
├── 03-architecture.md
├── 04-domain-model.md
├── 05-onboarding-guide.md
├── 06-review-report.md
├── 07-doc-graph.md
└── knowledge-graph.json
```

## Phase Prompts

每个 Phase 的详细 prompt 模板在 `references/prompts/` 目录下，执行时按需加载：

| Phase | 文件 |
|-------|------|
| Phase 1 | [references/prompts/phase-1-scanner.md](references/prompts/phase-1-scanner.md) |
| Phase 2 | [references/prompts/phase-2-analyzer.md](references/prompts/phase-2-analyzer.md) |
| Phase 3 | [references/prompts/phase-3-architecture.md](references/prompts/phase-3-architecture.md) |
| Phase 4 | [references/prompts/phase-4-domain.md](references/prompts/phase-4-domain.md) |
| Phase 5 | [references/prompts/phase-5-tour.md](references/prompts/phase-5-tour.md) |
| Phase 6 | [references/prompts/phase-6-review.md](references/prompts/phase-6-review.md) |
| Phase 7 | [references/prompts/phase-7-articles.md](references/prompts/phase-7-articles.md) |

执行流程由 agent-orchestration 的 `codebase` 流水线模板驱动。
