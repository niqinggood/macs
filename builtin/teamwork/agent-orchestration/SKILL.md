---
name: agent-orchestration
slug: agent-orchestration
version: 2.1.0
description: "多 Agent 编排框架 — 7 Agent 流水线模板、并行调度、状态传递、质量审核。"
read_when:
  - orchestrating multiple agents for a complex task
  - running a multi-phase pipeline
  - coordinating parallel agent execution
  - setting up a multi-agent workflow
  - managing task dependencies across agents
metadata:
  type: builtin
  tags: [orchestration, multi-agent, pipeline, concurrency]
  os: [linux, darwin, win32]
---

# Agent Orchestration

## 适用场景

- 将复杂任务拆分为多个子任务，由独立 agent 并行或串行执行
- 任务有明确的阶段依赖（Phase 1 输出 → Phase 2 输入）
- 需要质量审核环节的多阶段产出
- 大批量处理需要并发控制

## 7 Agent 流水线模板

```
Phase 1: Scanner (1 agent)   → 扫描目标，分类排序
Phase 2: Analyzer (5并发)    → 深度分析每个单元
Phase 3: Synthesizer (1)     → 综合识别模式
Phase 4: Extractor (1)       → 提取领域知识
Phase 5: Builder (1)         → 构建最终交付物
Phase 6: Reviewer (1)        → 质量审核
Phase 7: Correlator (1)      → 关联补充知识
```

## 执行协议

### 并发控制
- Phase 内最大并发：5 agent
- 单 agent 最大处理单元：30
- 超时：300s，失败重试 2 次（指数退避）

### 状态传递
每个 Phase 输出通过文件传递（不在 agent 间通过上下文传递）：
```
storage/pipeline/{run_id}/
├── state.json              # 流水线状态
├── phase-1-output.md
├── phase-2-outputs/        # 并发 agent 输出
├── phase-3-output.md
└── ...
```

### 质量门禁
- Phase 2 → 覆盖率 ≥ 90%
- Phase 3 → 至少识别 2 层结构
- Phase 4 → 至少提取 3 个核心实体
- Phase 6 → 关键问题 = 0

### 错误恢复
失败 → 等待 2^n 秒 → 重试（最多 2 次）→ 记录失败 → 继续其他 → 完成后报告失败清单

## 可用流水线

| 流水线 | 业务 Skill | 说明 |
|--------|-----------|------|
| `codebase` | understand-codebase | 代码库知识图谱 |
| `contracts` | understand-contracts | 合同条款分析 |
| `competitors` | understand-competitors | 竞对情报分析 |
| `decision-chain` | understand-decision-chain | 决策链分析 |
| `industry-trends` | understand-industry-trends | 行业趋势分析 |

## 自定义流水线

详见 [references/custom-pipeline.md](references/custom-pipeline.md)

## 详细协议

执行协议细节、并发控制、错误恢复的详细说明见：
- [references/execution-protocol.md](references/execution-protocol.md)
- [references/custom-pipeline.md](references/custom-pipeline.md)
