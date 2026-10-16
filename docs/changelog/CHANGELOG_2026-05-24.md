# openkitty 优化记录 — 2026-05-24

## 一、系统提示词重构：从瀑布规划到迭代思考

### 问题
系统提示词强制 agent 在行动前做全盘计划：
> *"For any task requiring 2+ tool calls, create a plan with plan_task BEFORE taking action."*

这导致 agent 不敢边做边调整，执行僵化。遇到分析型任务（如"仿照X风格重建Y页面"），agent 跳过中间推理直接生成输出，质量很差。

### 改动
`mac.go` — 替换 Planning 段落为 How to Work 段落：

- **简单任务 (1-3步)**：直接做，不规划
- **复杂任务 (4+步)**：只规划接下来 3-4 步，完成后停下来反思——学到了什么？原计划还有效吗？下一步是什么？
- **分析型任务**：必须先写中间产物（`design_analysis.md`、`content_analysis.md`、`design_mapping.md`），让 agent 显式记录推理过程，用户也能检查

---

## 二、write_file HTML 输出修复

### 问题
`write.go:writeHTMLFile()` 对所有 `.html` 文件强制套用基础模板 + `markdownToHTML()` 转换，导致 LLM 生成的完整 HTML 被逐行包裹 `<p>` 标签。1746 行中有 1528 行被 `<p>` 包裹，JavaScript 代码因 `<` 字符触发语法错误。

### 根因
```go
// write.go:394 — 把 Markdown 转 HTML，每行非标题文本包 <p>
htmlContent := t.markdownToHTML(content)
```

### 改动
`write.go:writeHTMLFile()` — 检测到内容已是完整 HTML 时（以 `<!DOCTYPE` 或 `<html` 开头），直接写入跳过模板和 Markdown 转换。

---

## 三、浏览器 DOM Snapshot（对齐龙虾能力）

### 问题
龙虾有 `browser.open` + `snapshot` 能拿到渲染后的 accessibility tree，openkitty 的 web_fetch 只能拿 raw HTML。对 SPA（如 codebuddy.cn）只能拿到空壳。

### 改动
`web_fetch.go` — 新增 `mode: "snapshot"` 参数：

- 用 chromedp 渲染页面（30s 超时，等 SPA hydrate）
- 执行 JS 提取 DOM 结构（accessibility-tree 格式：角色、文本、CSS class、引用ID）
- 同时提取 CSS 自定义属性和关键颜色
- 输出类似龙虾的 snapshot 格式：
```
rootwebarea "腾讯云代码助手 CodeBuddy"
  - navigation .header-nav
    - link "/home/" .nav-logo
    - button "CodeBuddy" .letter-up
```

---

## 四、ClawHub 适配器（openclaw → openkitty skill 转换）

### 能力
`clawhub_tool.go` (661行) — 注册为 `clawhub` 工具：

| 动作 | 功能 |
|------|------|
| `search "keyword"` | 搜索 ClawHub (npx + GitHub API 兜底) |
| `install <slug>` | 下载 + 自动转换为 openkitty 格式 |
| `install --source github --github_url <url>` | 从 GitHub 克隆安装 |
| `convert --local_path <path>` | 本地 openclaw SKILL.md 转换 |
| `list` | 列出已安装技能 |

### 自动转换规则
| openclaw | → | openkitty |
|----------|---|-----------|
| `~/.claw/`、`~/.nanobot/` 路径 | → | `skills/` |
| `metadata.openclaw.requires.env` | → | 环境变量提示 |
| `metadata.openclaw.requires.bins` | → | `allowed-tools: Bash(...)`  |
| `description` | → | 自动生成 `read_when` 触发词 |
| `scripts/script.py` | → | `skills/<skill>/scripts/script.py` |

---

## 五、web_fetch 参数提升

| 参数 | 旧值 | 新值 | 原因 |
|------|------|------|------|
| 内容提取上限 | 30000 | **50000** | 对齐龙虾 maxChars |
| JS 渲染超时 | 15s | **30s** | SPA 需要更多时间 hydrate |
| 纯文本提取上限 | 40000 | **50000** | 同上 |

---

## 六、max_tokens 提升

| 配置 | 旧值 | 新值 |
|------|------|------|
| Agent max_tokens (config.yaml) | 8048 | **16384** |
| Model max_tokens (deepseek) | 2048 | **16384** |

---

## 七、技能新增

| 技能 | 路径 | 用途 |
|------|------|------|
| web-design | `skills/web-design/SKILL.md` | 四步设计工作流：理解风格→分类内容→映射组件→生成HTML |
| design_extract.py | `skills/web-design/design_extract.py` | 从HTML提取结构化设计token |
| nova-chatroom | `skills/nova-chatroom/SKILL.md` | chatroom API 发帖（答题获 Key→发帖→回帖） |

---

## 八、安全系统 CLI 细粒度分级

### 问题
`safety.go` 对 `cli` 工具一刀切返回 `RiskHigh`，任何命令都要确认。nova-forum 技能的 curl 发帖被反复拦截。

### 改动
`safety.go:classifyCLICommand()` — 按命令类型分级：

| 命令 | 风险等级 |
|------|---------|
| `curl` GET 请求 | RiskModerate |
| `curl` POST/PUT | RiskModerate |
| `echo`、`cat`、`ls`、`grep`、`python` | RiskModerate |
| `rm`、`del`、`shutdown`、`format` | RiskHigh |

---

## 九、sub_agent 启动条件收紧

### 改动
- **系统提示词**：增加 ✅When/❌When Not 规则表，强调"先自己试"
- **工具层校验**：拒绝 <5 词任务、检测单个 curl/echo 反模式
- **CLI 循环检测**：相同命令失败 3 次自动注入"STOP using CLI"诊断

---

## 十、前端日志用户友好翻译

`mac.go:translateProgressMessage()` — 将工程师日志翻译为用户友好英文：

| 原始 | 翻译 |
|------|------|
| `begin task[xxx], build systemprompt:3308` | `Preparing...` |
| `Begin logical analysis,thinking round[1]` | `Thinking...` |
| `will use web_fetch: {...}` | `Searching the web...` |
| `will use code_check: {...}` | `Checking code...` |
| `Context compressed: ...` | `Compacting context...` |
| `故障诊断: X已失败N次` | `Troubleshooting...` |

---

## 十一、对话上下文持续

### 问题
同一 session 的新消息被当作独立对话，无法接续上次未完成的对话。

### 改动
`gateway.go:processTaskWithSession()` — 
- 检测上次任务是否未完成（用户消息 status ≠ completed）
- 未完成时用 `[Conversation continued]` 模式注入完整对话转录
- 任务完成后更新用户消息 status（pending → completed/failed）

---

## 改动文件清单

| 文件 | 改动 |
|------|------|
| `mac.go` | 系统提示词重构 + CLI 循环检测 + 日志翻译 + max_tokens + sub_agent 轻量化 |
| `gateway.go` | 对话上下文持续 |
| `web_fetch.go` | snapshot 模式 + 参数提升 |
| `write.go` | HTML 直接写入修复 |
| `safety.go` | CLI 细粒度分级 |
| `clawhub_tool.go` | **新增** — ClawHub 适配器 |
| `skills/web-design/SKILL.md` | **新增** — 四步设计工作流 |
| `skills/web-design/design_extract.py` | **新增** — 设计 token 提取 |
| `skills/nova-chatroom/SKILL.md` | **新增** — chatroom API 技能 |
| `config.yaml` | max_tokens 提升 |
