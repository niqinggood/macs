# openkitty 优化记录 — 2026-05-23

## 问题背景

两个线上问题驱动本次优化：

1. **技能发现失败**: 用户让 kitty 到 `www.novaexel.com/chatroom` 发帖，agent 首先找不到 skill.md，折腾了 21 轮后才找到 `nova-forum/SKILL.md`，中间还错误地启动了 sub_agent
2. **新闻回复截断**: 用户问今日财经新闻，kitty 只能说前几秒，后面内容无法完整说出

## 修改文件清单

| 文件 | 改动类型 |
|------|---------|
| `mac.go` | 核心改动：压缩体系重写、关键词分词增强、MaxTokens 提升 |
| `skill.go` | SkillMetadata 扩展、匹配算法增强 |
| `sub_agent_tool.go` | 启动条件校验 |
| `document_tools.go` | speak 工具长文本支持 |
| `web_fetch.go` | 内容提取上限提升 |
| `skills/nova-forum/SKILL.md` | 添加中文标签 |

---

## 一、压缩体系：从 1 层粗暴截断 → 3 层递进压缩

### 旧设计

```
上下文超限 → pressLongMessages(一刀切截断)
```

单层 `truncate(content, N)`，纯头部截断，所有消息同等对待，数据型工具结果（web_fetch 3万字）和操作型工具结果（echo hello）完全一样。

### 新设计

```
┌──────────────────────────────────────────────┐
│ Layer 1: 工具结果自动落盘 (零 LLM 成本)        │
│   超大结果 → 写磁盘，上下文只保留预览+路径引用   │
├──────────────────────────────────────────────┤
│ Layer 2: 智能规则压缩 (零 LLM 成本)            │
│   5档距离 + 数据工具3倍阈值 + 头尾保留         │
├──────────────────────────────────────────────┤
│ Layer 3: LLM 语义摘要 (每5次压缩执行1次)       │
│   调用模型生成结构化摘要，保留关键信息          │
│   失败自动降级到 Layer 2                      │
└──────────────────────────────────────────────┘
```

### Layer 1 — 工具结果落盘 (`mac.go:offloadToolResult`)

- 单条工具结果超过 8000 字符自动写入 `storage/tool_results/`
- 上下文保留：头部 1200 字符 + 尾部 600 字符 + 文件路径
- 无损可恢复：agent 可用 `read_file` 取回完整内容
- 零 LLM 调用成本

### Layer 2 — 智能规则压缩 (`mac.go:pressLongMessages`)

**距离分档：**

| 距离 | 阈值 (tokens) | 数据工具×3 |
|------|-------------|-----------|
| ≤3 (最近) | 8000 | 24000 |
| ≤5 | 4000 | 12000 |
| ≤10 | 1500 | 4500 |
| ≤20 | 600 | 1800 |
| >20 | 200 | 600 |

**工具分级：**
- 数据工具（3倍阈值）：`web_fetch`, `read_file`, `search`, `grep`, `glob`, `list_dir`, `cli`, `search_history`, `list_skills`, `tool_detail`
- 操作工具（正常阈值）：其余工具

**smartTruncate：**
- 保留头部 70% + 尾部 30%
- 中间用 `...[中间内容已省略，保留头尾]...` 标记
- 对新闻列表、日志等按时间倒序排列的内容尤其有效

**旧 assistant 消息：**
- 距离 >15 的消息压缩为单行 `[summarized]`，不再保留完整推理过程

### Layer 3 — LLM 语义压缩 (`mac.go:compressWithLLM`)

- 调用同一 LLM 模型对旧对话生成结构化摘要
- 提示词要求保留：所有用户消息原文、关键数据（URL/路径/API响应/错误）、待办事项、当前工作状态
- Assistant 推理压缩为 1 行摘要
- 超时 60 秒，max_tokens=2048（控制成本）
- 冷却机制：每 5 次压缩仅触发 1 次 LLM 压缩
- 至少 15 条消息才触发（太少不值得）
- 失败后静默降级到 Layer 2

### 对比参考

| 维度 | Claude Code | OpenClaw | openkitty (新) |
|------|------------|----------|---------------|
| 分层数 | 5层 | 6层 | 3层 |
| 零成本层 | Tool Result Budget + Snip + Microcompact | Strip thinking + File-swap + Strip results | Tool offloading + pressLongMessages |
| LLM摘要 | AutoCompact (9段式prompt) | Group-summarize | compressWithLLM |
| 截断策略 | 缓存感知+分片 | 头尾保留+DAG | 头尾保留+工具分级 |
| 可恢复性 | ✅ 摘要后可恢复文件/skill/hooks | ✅ drill-in工具可展开 | ✅ 工具结果落盘可 read_file |

---

## 二、技能发现增强

### 问题根因

用户输入 `到www.novaexel.com/chatroom发帖` 的分词结果：
- **旧**: 一整坨 `www.novaexel.com/chatroom发帖` → 无法匹配任何技能关键词
- **新**: `novaexel` / `chatroom` / `发帖` → 三个独立关键词

### 修改内容

**1. tokenizeKeywords 升级 (`mac.go`)**
- 新增 URL 分隔符: `/`, `.`, `?`, `&`, `=`, `:`, `#`, `@`, `|`, `~`, `()`, `[]`
- 新增 CJK-Latin 脚本边界分割: `chatroom发帖` → `chatroom` + `发帖`
- 过滤常见 URL 噪声词: `http`, `https`, `www`, `com`, `cn`, `org`, `net`, `io`, `html`, `php`

**2. SkillMetadata 扩展 (`skill.go`)**
- 新增 `ReadWhen []string` 字段，解析 SKILL.md YAML 的 `read_when`
- 新增 `Tags` 匹配支持

**3. calculateSkillMatchScore 增强 (`skill.go`)**

| 匹配层 | 权重 | 说明 |
|--------|------|------|
| read_when | 12 | 用户意图最直接信号 |
| Tag | 10 | 中文关键词等 |
| 名称 | 10 (+5 exact) | 技能名称 |
| 描述(域名) | 10 | URL/域名匹配高权重 |
| 描述(普通) | 5 | 一般关键词匹配 |
| 内容 | 最高10 | 出现频次加权 |

**4. nova-forum 技能标签 (`skills/nova-forum/SKILL.md`)**
```yaml
tags:
  - 发帖
  - 论坛
  - 聊天室
  - 社区
  - forum
  - post
  - chatroom
```

---

## 三、Sub_agent 启动条件

### 问题

Agent 在第 21 轮（curl 已连续失败 2 次后）启动了 sub_agent——典型的"急了乱投医"。Sub_agent 继承了同样的困惑 + 更少工具 + 无记忆 → 必然失败。

### 修改

**系统提示词 (`mac.go`)**：
```
### ✅ When to use sub_agent:
- User explicitly asks for parallel work
- Multiple truly independent sub-tasks
- Each sub-task is well-defined

### ❌ When NOT to use sub_agent:
- You are stuck or failing repeatedly
- Task is simple and sequential
- You haven't yet understood the problem
- Task requires memory/context the sub-agent won't have
- Single curl/http call, single file read

Rule of thumb: Try it yourself first.
```

**工具层校验 (`sub_agent_tool.go`)**：
- 拒绝 < 5 个词的任务描述（太简单不值得）
- 检测反模式：单个 `curl` / `echo` / `cat` / `ls` / `pwd` 命令伪装成子任务 → 直接报错
- 报错消息明确告知原因和建议

---

## 四、新闻/长内容截断修复

### 瓶颈链路

```
web_fetch 抓取 3万字新闻
  ↓
上下文字符数 > 20000 → 触发压缩
  ↓
pressLongMessages: 工具结果被截断到几千字符
  ↓
LLM 只看到新闻开头 → 生成的回复很短
  ↓
edge-tts --text "..." 命令行参数超长 → 失败或截断
  ↓
用户只听到几秒
```

### 修改

| 瓶颈 | 旧值 | 新值 |
|------|------|------|
| maxContextChars (压缩触发阈值) | 20000 | **80000** |
| MaxTokens (所有默认模型) | 4096 | **8192** |
| web_fetch 内容提取 | 15000 | **30000** |
| web_fetch 纯文本提取 | 20000 | **40000** |
| edge-tts 文本传递 | `--text "..."` CLI参数 | 长文本→ `--file` 临时文件 |
| macOS `say` | 命令行参数 | 长文本→ `-f` 文件 |
| Windows SAPI PowerShell | 单引号内嵌文本 | 长文本→ `Get-Content` 文件读取 |

---

## 五、改动统计

| 类别 | 改动项数 |
|------|---------|
| 压缩体系 | 3 层新增 (offloadToolResult / pressLongMessages升级 / compressWithLLM) |
| 技能发现 | 4 处 (tokenizeKeywords / SkillMetadata / matchScore / tags) |
| Sub_agent | 2 处 (系统提示词 / 工具校验) |
| 截断修复 | 6 处 (maxContextChars / MaxTokens×2 / web_fetch×2 / speak 工具×3) |
| **总计** | **15 处改动，6 个文件** |
