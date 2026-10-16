# Changelog — 2026-06-28 ~ 2026-06-29

## AI 员工系统

### 路由修复
- **禁用 Session 延续路由** — 同一 session 之前聊过某个 DE，后续消息不再自动转给它
- **注释掉 Supervisor 智能路由** — 不再根据消息内容自动判断分给哪个 DE
- **规则：必须显式 @mention 才能触发 AI 员工，不 @ 不触发**

### 提示词工程
- **investment-advisor** 改为 Direct 模式 — 用户没给标的时候追问，不瞎编
- **meeting-secretary** 改为 Direct 模式 — 直接处理用户文字，不找音频文件
- **market-analyst** 改为 Direct 模式 — 直接分析主题，不跑竞品 Pipeline
- **social-media** 增加 SystemPrompt — 问热点先搜再答，不误创作内容
- **AE 加载时合并硬编码 Direct 配置** — DB 中没有 Direct 的从默认值合并

### @mention 保留
- 历史对话中 `@投资顾问` 等前缀不再被剥离消失

## 系统提示词恢复

- **完整恢复原始 SystemPrompt**：Core Identity / Storage / Platform / Core Principles / Reflection-Before-Action(PREV/MEMORY/NEXT) / Workflow / Tool Priority / Rules / Parallel Orchestration
- 拆到单独文件 `system_prompt.go`，使用 Go 原始字符串，彻底解决转义问题
- **增加 local-first 规则**：优先读本地文件，本地没有再搜索网络
- **强化中文搜索**：中文内容(知乎/小红书/微博)必须用 `engine="sogou"`
- **禁用 ProactiveMemory 和 KG 上下文注入** — 不再把旧话题注入新对话

## Web UI 改进

### 执行详情
- **独立执行详情页** `/web/execution.html?id=xxx` — 新标签页打开，三段式展示（接收任务 → 执行过程 → 交付结果）
- 工作流步骤带编号、状态色标、工具调用记录、审批留痕

### 文件预览
- **文件预览页** `/web/file-view.html?path=xxx` — 点击气泡中的文件路径，新标签页渲染 Markdown（GitHub 风格 + 代码高亮），附带下载/复制按钮

### 执行日志流
- 等宽字体 + 最新行深色高亮（`#1e293b`），旧行渐隐（`#94a3b8`）
- **Debug 模式接线** — 复选框真正控制 token/chars 信息的显示/隐藏
- **web_fetch 日志带参数** — 显示 URL 或 q=xxx 搜索词

### AI 员工面板
- **DE → AE 全局重命名**（34 个文件，`digital_employee.go` → `ai_employee.go` 等）
- **编辑改为模态框** — 弹出式编辑，支持新增 AI 员工
- **执行记录按 pipeline_name 过滤**（修复了之前的无效 DE ID 过滤）
- **执行记录表格增加"查看"列** — 点击打开独立详情页

### 聊天界面
- **@ 下拉提示** — 输入 `@` 自动弹出 AI 员工列表，支持关键词过滤、↑↓ 选择、Enter 确认、Esc 关闭
- **气泡间距** `2px → 8px`，透气感
- **AI 气泡底色** 改为 `#f6f6f4` 浅灰（白天模式）、暗色保持 `#1e293b`（黑夜模式）
- **顶栏折叠按钮** `◀` — 可折叠/展开功能按钮行
- **文本框可拖拽调整高度** — `resize: vertical` + `max-height: 300px`

### 黑夜模式
- 补全 log 组件暗色样式（`.log-header`、`.log-title`、`.log-stream` 等）
- 滚动条暗色适配
- 类名从 `.progress-timeline` 迁移到 `.log-stream`

## 后端修复

### Pipeline
- **investment-advisory Pipeline 四步 Prompt 优化** — Step 1 增加前置检查，Step 4 增加交付规范
- **meeting-minutes Pipeline 修复** — 从"找音频文件"改为"直接处理文字"
- **market-analyst 改为 Direct 模式** — 不再跑对不上号的"竞品分析" Pipeline

### 稳定性
- **autonomous_worker** — `Start()` 时初始化 `LastRun` 为当前时间，防止首次启动误触发过期循环
- **no progress log found** — 后端返回友好提示替代 404，前端清除 stale localStorage 引用

### tool_end 日志
- 显示 URL 或 query 参数，方便追踪爬取目标
- 支持 `web_search` 工具显示 `q=xxx`

## 数据

- 28 个内置 AI 员工，含投资顾问、会议秘书、开发工程师、法务顾问、智能客服等
- SystemPrompt 约 800 tokens（静态缓存命中，不计费）
- 工具：web_fetch（支持 sogou/duckduckgo/bing 等 11 个搜索引擎）、read_file、write_file 等 40+ 工具
