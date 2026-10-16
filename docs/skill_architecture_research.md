# Skill 发现与上下文管理 — 业界方案研究

## Claude Code：Progressive Disclosure

### 三级加载模型

| 层级 | 何时加载 | Token 消耗 | 内容 |
|------|---------|-----------|------|
| Level 1: Metadata | 始终（启动时） | ~100/skill | `name` + `description`（YAML frontmatter） |
| Level 2: Instructions | Skill 触发时 | ~5K | 完整 `SKILL.md` body |
| Level 3: Resources | 按需 | 不进上下文 | Scripts/files 通过 Bash 执行 |

### 触发机制

Skill 的 `description` 是**主要触发信号**，必须编码"何时用"而非只描述"做什么"。

好例子：
```yaml
description: Reviews code for security vulnerabilities. Use proactively after code changes.
```

坏例子：
```yaml
description: For testing
```

Claude 自主判断何时激活，用户也可手动 `/skill-name` 调用。

### 系统提示词架构

```
┌──────────────────────────────┐
│  FIXED CACHE LAYER           │  ← 缓存，不变
│  Agent identity, tools, rules│
├──────────────────────────────┤
│  DYNAMIC LAYER               │  ← 每轮变化
│  Session state, time, env    │
├──────────────────────────────┤
│  SKILL INJECTION             │  ← 按需加载
│  Triggered skills, memory    │
└──────────────────────────────┘
```

固定层 10K-100K token，服务端缓存。任何修改导致整个缓存失效。

### Subagent 模式

子 Agent 独立上下文窗口（200K），不污染主对话。
- Tool 隔离：可限制只读或开放写入
- Model 选择：Haiku/Sonnet/Opus 按需分配
- 层级委派：Parent → Guide Subagent → Skill Scanner → Answer

### Skill vs Subagent vs MCP

| | Skills | Subagents | MCP |
|------|--------|-----------|-----|
| 用途 | 教"怎么做" | 隔离+委派任务 | 连接外部工具/数据 |
| 上下文 | 共享主上下文 | 独立窗口 | Host 定义 |
| 适用 | 可重复方法论、工作流 | 并行执行、权限隔离、大任务 | 外部系统（DB、API） |

---

## OpenAI Codex CLI：Agent Loop + 自动压缩

### Agent Loop

```
User Input → Build Prompt → Model → Tool Call? → Execute → Append → Re-query
                                    ↓ No
                                 Final Response
```

一轮对话可包含数百次 model↔tool 迭代。三款产品共享同一核心：Codex CLI / Codex Cloud / Codex VS Code Extension。

### 工具来源

| 来源 | 说明 |
|------|------|
| Codex 内置 | shell, apply_patch, read_file, browser 等 |
| API 提供 | Responses API 暴露的工具 |
| 用户 MCP | Model Context Protocol 服务器自定义工具 |

MCP 工具动态变化会触发缓存失效，是已知问题。

### 提示词构建（分层叠加）

优先级：developer > system > user > assistant

```
1. Developer 消息（沙箱环境描述）
2. ~/.codex/config.toml 指令
3. AGENTS.md 遍历（项目根→工作目录逐级）
4. 配置的 skills metadata
5. 环境上下文（cwd, shell）
6. 用户实际消息（放最后）
```

### 上下文压缩（Auto Compact）

超过 `auto_compact_limit` 时自动触发：
- 调用 `/responses/compact` 端点
- 返回压缩表示替换原文
- 包含 `encrypted_content` 保留模型潜在理解
- 原需手动 `/compact`，现已完全自动化

### 缓存策略

核心设计：**旧请求始终是新请求的前缀**，保证缓存命中。

```
请求1: [A]
请求2: [A][B]      ← A 命中缓存
请求3: [A][B][C]   ← A+B 命中缓存
```

O(n²) → O(n)。缓存失效场景：工具列表变化、模型切换、配置变更。

---

## 对照我们的架构

| 维度 | 我们当前 | Claude Code | Codex CLI |
|------|---------|-------------|-----------|
| Skill 发现 | 意图识别 + 自动注入 top 5 | description 触发 + 手动 | AGENTS.md 遍历 |
| 注入方式 | 追加到 system message | 三级渐进加载 | 分层叠加 prompt |
| 注入时机 | Generate 启动时自动 | 启动时 metadata，触发时 body | 每轮构建 prompt |
| 上下文管理 | 无压缩 | Subagent 隔离 | 自动 compact |
| 缓存 | 依赖 API 自动前缀缓存 | 固定层显式缓存 | 严格前缀匹配 |

---

## 可借鉴的改进方向

### 1. Skill 描述规范化（对标 Claude Code）
每个 SKILL.md 的 `description` 应编码"何时用"：
```yaml
description: >
  Generate PPTX presentations from data. Use when user asks to create PPT, 
  slides, or data reports. NOT for simple text documents.
```

### 2. 上下文压缩（对标 Codex）
对话超过 N 轮（如 15 轮）自动摘要旧消息，保持上下文在可控范围。

### 3. Subagent 隔离（对标 Claude Code）
复杂多阶段任务（如"分析 CSV + 生成 PPT"）拆成子任务，每个子 Agent 独立上下文。

### 4. 缓存优化（对标两者）
固定层（identity + tools + rules）保持不变，变量层（时间、skills、history）追加在末尾。

### 5. read_when 触发机制（对标 Claude Code description）
让 `read_when` 真正起作用：匹配到意图后自动 `read_file` SKILL.md body 注入上下文，实现 Level 2 加载。

---

## 实现优先级

| 优先级 | 改进项 | 收益 | 复杂度 |
|--------|--------|------|--------|
| P0 | Skill 自动注入（已完成） | 高 | 低 |
| P1 | read_when 触发 → 自动加载 SKILL.md | 高 | 低 |
| P2 | 上下文压缩（对话摘要） | 高 | 中 |
| P3 | 缓存优化（固定/动态分层） | 中 | 中 |
| P4 | Subagent 隔离 | 中 | 高 |
