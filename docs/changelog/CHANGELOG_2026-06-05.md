# Changelog 2026-06-05

## Bug 修复

### Critical（并发安全+功能阻断）

1. **g.tasks map 并发加锁** — `gateway.go`
   - 新增 `tasksMu sync.RWMutex` 字段
   - 14 处 `g.tasks` 读写全部加锁保护
   - 消除并发 map 访问导致的 Go runtime panic

2. **InterruptCh goroutine 泄漏** — `gateway.go`
   - 协作任务 defer 中增加 `close(task.InterruptCh)`
   - `for range` 循环前 channel 先关闭，goroutine 正常退出

3. **streamResponse CPU 忙等** — `gateway.go`
   - ProgressCh 关闭后设为 nil，消除 select 空转

4. **记忆系统投毒** — `memory.go`
   - 文件存在性校验：OutputPath 不存在则跳过记忆注入
   - 相似度阈值 0.1 → 0.4
   - 注入文本增加"请检查是否需要重新处理"提示

5. **Config 保存不热加载** — `gateway.go`
   - `PUT /api/config/sections` 保存后调用 `g.ReloadConfig()`
   - 修复"保存了但 engine 还是用旧配置"的问题

6. **Task Stop 不 cancel goroutine** — `gateway.go`
   - `GatewayTask` 新增 `Cancel context.CancelFunc`
   - `handleStopTask` / `handleStopTaskByID` 调用 `task.Cancel()`
   - `processTaskWithSession` / `processTask` 用可取消 context 替代 `context.Background()`
   - 修复任务"停止后又复活为 completed"的问题

7. **TodoManager.Stop() 双重关闭 panic** — `todo.go`
   - 新增 `stopped bool` 字段 + mutex 保护
   - 重复调用 Stop() 时直接 return，不再次 close(stopCh)

8. **Sub-agent 同名并发竞态** — `sub_agent_tool.go`
   - `activeAgents` 增加同名检查
   - 同名 sub-agent 已存在时返回错误，不再覆盖

### Medium

9. **中文文件下载编码** — `gateway.go` + `chat.js`
   - 前端 `encodePath`：逐段 URL 编码，保留 `/`
   - 后端 Content-Disposition 支持 RFC 5987 编码
   - `url.QueryEscape` 空格 `+` → `%20`

10. **converter 不支持 .csv/.xlsx** — `converter.go`
    - 新增 `toCSV` 方法：从 markdown 提取表格数据导出为 CSV
    - 错误提示更新

11. **HTTP chat 空消息校验** — `gateway.go`
    - 消息为空时返回 400 "Message cannot be empty"

12. **飞书 API 调用无超时** — `feishu.go`
    - 图片/文件/语音下载添加 60s context 超时

13. **handleTask 路径 nil channel** — `gateway.go`
    - `handleTask` 创建 task 时初始化 ProgressCh
    - `processTask` 结束时关闭 ProgressCh

### Low

14. **app.js 重复函数定义**
    - 删除重复的 `escapeHtml`（regex 版无 null 检查）
    - 删除重复的 `formatFileSize`（不支持 GB）

## Skill 系统改进

15. **`extractIntentKeywords` 合并 `tokenizeKeywords`** — `skill.go`
    - 意图匹配失败时不再只取前 3 个 >3 字符的原始词
    - 改为走完整 CJK/ASCII 分词 + 停用词过滤
    - 删除废弃的 `isStopWord` 函数

16. **Skill 注入兜底** — `mac.go`
    - 意图匹配不到 skill 时列出所有 skill（每个 40 token）
    - 匹配到的 skill 描述从 80 → 100 token

17. **新增 `convert-documents` skill**
    - 指导 AI 使用 `convert` 工具做 MD→PDF/DOCX/PPTX/CSV 转换

18. **`remote_deploy` skill 补充描述**
    - 添加 YAML frontmatter（name + description）

## 多模态改进

19. **ModelConfig 新增 `supports_vision` 字段** — `mac.go`
    - `findVisionModel`：自动扫描配置发现视觉模型
    - 优先级：显式标记 → 名称匹配(seed/vision/gpt-4o/gemini) → doubao_vision 兜底
    - 不再硬编码 `doubao_vision`

20. **API Key 错误提示优化** — `mac.go`
    - `isAPIKeySuspicious`：预检空值/占位符/"YOUR_xxx"/长度<10
    - 正确 key 被拒时不误报"配置错误"

## Web UI 改进

21. **欢迎页重构** — `index.html`
    - 从 350px 高大卡片压缩为一行紧凑提示条
    - welcomeTips 移出 messagesArea，不再被 innerHTML 销毁

22. **显示逻辑修复** — `chat.js`
    - 有历史消息时隐藏 welcomeTips，空会话时显示
    - 发送消息时隐藏 welcomeTips

23. **UI 文案统一** — `index.html`
    - Config → Settings
    - Help 按钮加大

24. **更新提醒系统**
    - `build_windows.bat` 注入日期版本号 `1.YYYY.MMDD`
    - `GatewayConfig` 新增 `update_url`
    - 后台 6h 定时检查
    - 前端 `/api/update/check` 轮询 + 通知条

25. **SSE 断线恢复** — `sse.js`
    - `restorePendingTasks` 拉取历史进度日志 + 订阅后续 progress

## 配置更新

26. **GatewayConfig 新增字段**
    - `UpdateURL string` — 更新检查 JSON URL
27. **ModelConfig 新增字段**
    - `SupportsVision bool` — 标记视觉/多模态能力

## 文件变更清单

| 文件 | 改动 |
|------|------|
| `gateway.go` | tasksMu 加锁、task cancel、config 热加载、Content-Disposition、空消息校验、handleTask channel 初始化 |
| `mac.go` | findVisionModel、isAPIKeySuspicious、skill 注入兜底、visualModelName 自动发现、buildSystemPrompt skill 段 |
| `skill.go` | extractIntentKeywords 合并 tokenizeKeywords |
| `memory.go` | 文件校验、相似度阈值、注入提示语 |
| `todo.go` | Stop() 双重关闭保护 |
| `sub_agent_tool.go` | 同名 sub-agent 去重 |
| `converter.go` | CSV/XLSX 支持 |
| `feishu.go` | API 调用超时 |
| `tools.go` | 无变更（回退 CoreTools 修改） |
| `web/index.html` | 欢迎页重构、配置模型 CTA、更新通知条 |
| `web/chat.js` | encodePath、welcomeTips 显示逻辑 |
| `web/sse.js` | restorePendingTasks 进度恢复+订阅、checkForUpdate |
| `web/app.js` | 去重 escapeHtml/formatFileSize、checkForUpdate 调用 |
| `build_windows.bat` | 版本号注入 |
| `skills/convert-documents/SKILL.md` | 新增 |
| `skills/remote_deploy/SKILL.md` | 补充 frontmatter |
| `skills/better-ppt/SKILL.md` | 重写（数据密集型示例+规则） |

## 已知待处理

- **BUG5**: 系统提示词无 token 预算管控，56 工具摘要可能超 context window（需重构提示词构建）
- **g.tasks 清理机制弱**：handleTask 路径的 task 只靠每小时清理 24h 以上的
- **processTask 路径无流式进度**：`/api/task` 端点不支持实时进度展示
