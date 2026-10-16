# Prompt Cache 优化记录

## 缓存原理

LLM API（DeepSeek/OpenAI 兼容）基于前缀匹配缓存。系统提示词的开头部分如果不变，后续调用可命中缓存，节省 token 和延迟。

## 提示词结构（优化后）

```
[========== 静态前缀（可缓存）==========]
## Core Identity + agent name
## Runtime + workspace
Skills index (BuildIndexPrompt, sorted keys)
## Platform Policy (OS fixed)
## Core Principles
## Workflow / Tool Priority / Rules

[---------- 动态后缀（每次变化）----------]
## Session (taskID, sessID — 每次不同)
## Memory (记忆检索 — 每次不同)
## Tool Examples (静态但放后面)
## Available Tools (已排序，放 Current time 前)
Current time
## Response Format + "Begin."
## Matched User Skills (输入关键词匹配，追加在最后)
```

## 已修复的缓存击穿点

| # | 问题 | 位置 | 修复 | 提交 |
|---|------|------|------|------|
| 1 | 时间戳在提示词最开头 | `mac.go:2824` | 移到 `## Response Format` 之前 | `5b51651` |
| 2 | `## Session` 在静态区中间 | `mac.go:2850-2860` | 移到 `## Memory` 之前 | `5b51651` |
| 3 | `GetAllToolSummaries()` 未排序 | `tools.go:181` | 按字母排序输出 (Go map 迭代随机) | `ee50807` |
| 4 | `isSimple` 条件分支 | `mac.go:2871` | 始终输出 Workflow 段 | `ee50807` |
| 5 | `## Available Tools` 在静态前缀内 | `mac.go:2895` | 移到 `Current time` 前 | `73c80cb` |

## 已确认无问题的流程

- **消息压缩** (`pressLongMessages`): 保留 system prompt (index 0) 不动，只压缩 assistant/user/tool
- **全局摘要** (`compressSummary`): `result = append(result, messages[0])` 保留原样
- **反射检查点/反思消息**: 以独立 system 消息追加，不修改前缀
- **extractIntentKeywords + GetTopSkills**: 匹配结果追加在系统提示词最末尾
- **迭代间消息增长**: 追加 assistant/tool 消息，不修改 system prompt
- **多 Agent**: 每个 agent 有独立 name，各自独立 cache key（预期行为）

## 相关文件

| 文件 | 关键函数/行 |
|------|------------|
| `mac.go:2818` | `buildSystemPrompt()` — 系统提示词构建 |
| `mac.go:3848` | `callLLM()` → `globalCacheMetrics.Record()` — 缓存指标采集 |
| `mac.go:7892` | `CacheMetricsTracker` — 指标采集器 |
| `tools.go:181` | `GetAllToolSummaries()` — 工具列表（已排序） |
| `skill.go:1144` | `BuildIndexPrompt()` — 技能索引（sorted keys） |
| `gateway.go:1190-1192` | `/api/monitor/cache` 端点 |
| `web/app.js:3925` | `loadMonitorData()` — 前端监控面板 |

## 监控面板

- 入口: 顶部 **⚙ 工具 ▾ → 📊 系统状态**
- 展示: 缓存命中率 / 命中Tokens / 未命中Tokens / 平均延迟
- 图表: Chart.js 折线图 (最近 50 次调用)
- 明细: 最近 10 次调用逐条记录

## 验证方法

1. 重启程序，发送几条同类对话（相似长度、相似主题）
2. 打开监控面板观察命中率
3. 同类对话应稳定在 60-90% 命中率
4. 如果命中率跳动，检查是否有新工具注册或 config 变更
