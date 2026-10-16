# Sub-Agent — 子智能体委托执行

## 概述

Sub-agent 是在主 agent 控制下独立执行子任务的轻量 agent。主 agent 通过 `sub_agent` 或 `parallel_sub_agent` 工具将任务分派出去，子 agent 在自己的上下文窗口中独立完成，只返回结果。

## 何时使用

- **并行任务**: 多个独立任务可以同时执行（研究 + 编码 + 分析并行跑）
- **大任务拆解**: 一个复杂任务拆成多个子任务，分别由专注的子 agent 处理
- **隔离执行**: 某些任务需要隔离上下文，避免污染主 agent 的对话历史
- **受限场景**: 需要限制工具集和迭代次数的任务

## 工作原理

```
主 Agent                         子 Agent A (researcher)
    │                                │
    ├─ sub_agent("分析API文档") ─→    ├─ 工具: web_fetch, read_file
    │                                ├─ maxIter: 10
    │                                └─ 返回结果
    │
    ├─ sub_agent("生成代码")───→    子 Agent B (coder)
    │                                ├─ 工具: read_file, write_file
    │                                └─ 返回结果
    │
    └─ 汇总结果，继续
```

## 约束

- **禁止递归**: 子 agent 不能调用 `sub_agent` 和 `parallel_sub_agent`
- **无记忆**: 子 agent 不持久化记忆，每次调用独立
- **最大并发 5**: 同一时刻最多 5 个子 agent 并行
- **超时 10 分钟**: 单个子 agent 最长执行时间
- **迭代上限**: 默认 30，最大 60

## 使用示例

### 单个子 agent

```
工具: sub_agent
参数: {
  "task": "分析 E:\\project\\src 目录下的认证模块，找出所有可能的安全漏洞。重点关注：硬编码密码、缺失的权限检查、SQL 注入点",
  "agent_name": "security-scanner",
  "max_iterations": 20
}
```

### 并行子 agent

```
工具: parallel_sub_agent
参数: {
  "tasks": [
    {"agent_name": "researcher", "task": "搜索 Go 语言最新的 SQL 注入防护方案"},
    {"agent_name": "scanner", "task": "扫描当前项目所有 SQL 查询语句，标记未参数化的"},
    {"agent_name": "reporter", "task": "总结上述两个 agent 的发现，生成修复建议"}
  ],
  "timeout_minutes": 8
}
```

## 最佳实践

1. **任务要具体**: 给子 agent 明确、可验证的目标，不要开放式探索
2. **控制迭代**: 简单任务用 5-10 次迭代，复杂分析用 20-30
3. **并行独立任务**: 任务之间不能有数据依赖才并行
4. **命名有意义**: agent_name 帮助主 agent 理解返回结果来自谁
5. **不要过度拆分**: 5 行代码就能完成的就别 spawn 子 agent
