# Agent Orchestration — 执行协议详细说明

## 并发控制

```
并发规则：
- Phase 内最大并发数：5
- 单 agent 最大处理单元数：30
- agent 超时：300s（5分钟）
- 失败重试：最多 2 次
- 重试退避：指数退避 2^n 秒
```

## 错误恢复协议

```
if agent 失败:
    if retry_count < 2:
        等待 2^retry_count 秒
        重新启动 agent
    else:
        记录失败单元到 state.json
        继续处理其他单元
        完成后报告失败清单

if Phase 级失败:
    暂停流水线
    保存当前状态
    报告错误详情
    等待用户决定：重试 / 跳过 / 终止
```

## 质量门禁

每个 Phase 完成后检查：

- Phase 2 → 覆盖率 ≥ 目标文件数的 90%
- Phase 3 → 至少识别出 2 层架构分层
- Phase 4 → 至少提取出 3 个核心实体
- Phase 6 → 通过率 ≥ 80%（关键问题 = 0）

不通过则自动重跑对应 Phase（最多 1 次）。

## Agent 角色定义

| 角色 | 职责 | 并发数 | 典型 Phase |
|------|------|--------|-----------|
| Scanner | 扫描、发现、归类 | 1 | Phase 1 |
| Analyzer | 深度分析、符号提取 | 5 max | Phase 2 |
| Synthesizer | 综合分析、抽象提炼 | 1 | Phase 3 |
| Extractor | 领域概念提取 | 1 | Phase 4 |
| Builder | 基于分析结果构建产出 | 1 | Phase 5 |
| Reviewer | 质量审核、一致性检查 | 1 | Phase 6 |
| Correlator | 关联补充知识源 | 1 | Phase 7 |

## 执行模式

### 串行模式
每个 Phase 依赖前一 Phase 的输出：
```
Phase 1 → Phase 2 → Phase 3 → ... → Phase N
```

### 并行模式（扇出-扇入）
同一 Phase 内启动多个 agent 并发处理：
```
Phase 2: [Agent-1] [Agent-2] [Agent-3] [Agent-4] [Agent-5]
```

### 扇出-扇入模式
并发处理，结果汇总后进入下一阶段：
```
          ┌→ Agent-1 ─┐
Phase 1 → ├→ Agent-2 ─┤ → Aggregate → Phase 3
          └→ Agent-3 ─┘
```

## 使用方法

### 启动流水线
```
/orchestrate {pipeline_name} {target_path}
```

### 查看进度
```
/orchestrate status
```

### 恢复中断的流水线
```
/orchestrate resume {run_id}
```
