# OpenKitty 改造记录 — 2026-06-06

## 一、问题定位

### SKILL.md 内容无法到达用户

用户让 agent 输出完整的 SKILL.md 内容，agent 调了 `read_file` 拿到 3611 字符，但回复给用户时只说"喏，这就是完整原文"然后总结。

**根因**：LLM（deepseek-v4-pro）把 tool result 当作"给自己看的材料"而非"需要转发给用户的内容"。这是 function-calling 模型的通病——理解→总结→回复，而不是理解→原样转发。

### 数据流分析

```
read_file 执行 → result.Content (3611字符) 
  → mac.go:1554 加入 messages 数组（内部，喂给 LLM）
  → mac.go:1534 callback 只发 "read_file completed (683 chars)"（不发内容）
  → LLM 下一轮回复总结 → mac.go:1630 finalOutput = LLM 的总结
  → 用户看到的是总结，不是原文
```

## 二、Hermes Agent 方法论移植

### 编程 Skill（5 个）

| Skill | 来源 | 核心方法论 |
|-------|------|-----------|
| `plan-mode` | Hermes `plan` | 先写计划不写代码，拆分成 2-5 分钟小任务 |
| `systematic-debugging` | Hermes 同名 | 4 阶段根因调试，禁止无调查修复 |
| `test-driven-development` | Hermes 同名 | RED-GREEN-REFACTOR 铁律 |
| `requesting-code-review` | Hermes 同名 | 提交前 8 步安全检查 + 独立 reviewer |
| `spike` | Hermes 同名 | 抛弃式原型验证想法 |

### 底层机制（3 个）

| 机制 | 实现 | 文件 |
|------|------|------|
| **tool_guardrails** | 精确签名检测（SHA256）+ 无进展循环检测 + block | `mac.go` |
| **error_classifier** | 7 级 API 错误分类 + 接入 callLLM | `guardrails.go` + `mac.go` |
| **工具结果智能裁剪** | 压缩前提取结构化摘要替代硬截断 | `guardrails.go` + `mac.go` |

### Grace Call

迭代预算耗尽时不硬截断，额外做一次无工具 LLM 调用，让 agent 总结当前进度后输出。`mac.go:1685`

### Skill 自动创建提示

复杂任务（≥5 步 + ≥3 工具 + ≥60 秒）完成后，写入 DB 的 `memory_events` 表，后续对话可被检索注入。

## 三、Skill 索引系统

### 改动前

```
## Skills
34 skills available. Use list_skills <keyword> to search...

## Matched Skills (auto-injected)
- better-ppt: Generate professional PPTX...
```

LLM 只知道自动匹配到的 3 个 skill，不知道还有哪些可用。

### 改动后

```
## Built-in Skills
  software-development — plan, debug, TDD, code review
    plan-mode, systematic-debugging, tdd, requesting-code-review, spike, ...
  data-analysis — reports, charts, PPT, dashboards
    better-ppt, data-analysis, stock-daily-report, pl-report-generator
  ...

## User Skills
  Created skills are discoverable via list_skills.

Use read_file skills/<name>/SKILL.md to load full instructions.

## Matched User Skills   ← 仅用户自建 skill
```

### 实现

- **`skills/OPENKITTY_INDEX.json`** — 37 个 skill 分 10 类的索引文件
- **`skill.go:BuildIndexPrompt()`** — 运行时生成分类索引文本
- **`skill.go:GetTopSkills()`** — 仅匹配不在索引中的用户自建 skill
- **`skill.go:IsInIndex()`** — 判断 skill 是否在静态索引中

## 四、Session/Task ID 重构

### 改动前

```
session_1780662112142740100   ← UnixNano，无意义
task_1780662112142740100      ← UnixNano，无意义
logs/cold_messages/task_xxx.json
logs/cold_messages/callllm_task_xxx_retry0_xxx.json
```

### 改动后

```
s260605222918_ni51             ← s + YYMMDDHHMM + agent名
s260605222918_ni51-t3          ← session-t{DB计数递增}
logs/s260605222918_ni51/t3.json
```

### 实现

- **`gateway.go:3685`** — session ID 格式
- **`gateway.go:3697/4259`** — task ID 基于 DB `chat_messages` 表 distinct task_id 计数
- **`mac.go:896`** — saveColdMessages 路径：`logs/{sessionID}/t{n}.json`
- **`mac.go`** — 删除 `savecallLLMMessages` 函数及其调用
- **`mac.go:buildSystemPrompt`** — 新增 `## Session` 段，包含 task 号和 cold data 路径

## 五、小改进

- **memory 最近对话截断**：100 → 200 字符，保留更多关键信息

## 六、新增/修改文件

| 文件 | 状态 | 说明 |
|------|------|------|
| `skills/OPENKITTY_INDEX.json` | 新增 | skill 分类索引 |
| `skills/plan-mode/SKILL.md` | 新增 | 计划驱动 |
| `skills/systematic-debugging/SKILL.md` | 新增 | 系统调试 |
| `skills/test-driven-development/SKILL.md` | 新增 | TDD |
| `skills/requesting-code-review/SKILL.md` | 新增 | 代码审查 |
| `skills/spike/SKILL.md` | 新增 | 原型验证 |
| `guardrails.go` | 新增 | 防死循环 + 错误分类 + 工具预览 |
| `gateway.go` | 修改 | session/task ID 格式 |
| `mac.go` | 修改 | 索引 + grace call + guardrails + 预览 + cold data |
| `skill.go` | 修改 | BuildIndexPrompt + IsInIndex |
| `memory.go` | 修改 | 最近对话截断 100→200 |

## 七、讨论但未实现

| 项目 | 原因 |
|------|------|
| Prompt 缓存分层 | DeepSeek 不支持 cache_control |
| 辅助模型压缩 | 需要架构级改动 |
| 后台 daemon 自改进 | Go fork 成本高，不适合 |
| 工具结果裁剪 LLM 版 | 当前硬截断够用 |
