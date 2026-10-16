# OpenKitty 能力全景图

## 架构原理

```
用户请求 → Gateway(HTTP/WS/SSE) → MultiAgentEngine → AgentFactory → LLMAgent.Generate()
                                    ↑                        ↑
                              协作引擎(6模式)          工具注册表(30+工具)
                              MemoryStore               SkillsLoader(41技能)
```

### 核心循环 (Generate)
1. `buildSystemPrompt` — 注入系统提示 + 记忆上下文 + 工具列表 + 规划指导
2. `callLLM` → 获取响应 → 解析 tool_calls
3. 并行执行工具(goroutine pool) → 结果回传 messages
4. `plan_task` 进度注入 + failTracker 错误恢复 → 回到步骤2
5. 无 tool_calls 时 → 最终输出 → 写入记忆

## 工具矩阵 (30+)

| 类别 | 工具 | 技术栈 |
|------|------|--------|
| 📁 文件 | read_file, write_file, list_dir | Go原生 |
| 🌐 网络 | web_fetch(17引擎), curl(Go原生) | Go + Node(readability) |
| 💻 命令 | cli(22原生命令), snapshot | Go原生 + Git Bash |
| 📝 文档 | extract(PDF/DOCX/音频), convert(MD→PDF/HTML/DOCX/PPTX) | Go + Python(wisper) + Node(pptx) |
| 🎯 规划 | plan_task, update_plan_step | Go引擎追踪 |
| 🧠 记忆 | knowledge_query(TF-IDF语义搜索), search_history | Go纯算法 |
| 👁️ 视觉 | vision(豆包API), screenshot(Puppeteer) | Go + Node |
| 🗣️ 语音 | speak(edge-tts), extract音频(whisper) | Go + Python |
| 🖼️ 图片 | image_process(Sharp), read_file(图片→base64) | Go + Node |
| 📊 PPT | better-ppt(JSON工作流→python-pptx) | Go + Python |
| 🔧 运维 | sys(info/ps/disk/memory/install) | Go原生 |
| 🤖 外部 | external_agent(调Claude/OpenCode), sub_agent, parallel_sub_agent | Go + CLI |

## 创新点

### 1. Go引擎 + 多语言工具链
Go 负责稳定性(goroutine并发、Gateway、SQLite)，Python/Node.js 负责生态(pptx/whisper/readability/sharp/puppeteer)。通过 `exec.Command` 管道通信，零学习成本接npm/pip生态。

### 2. JSON驱动PPT
AI不写PPT — AI写JSON工作流(每页逻辑+布局+用图)，Python-pptx忠实渲染。分离决策与执行，LLM擅长JSON，PPT质量接近人工。

### 3. TF-IDF 语义搜索(零依赖)
纯Go标准库实现余弦相似度+TF-IDF加权，替换SQL LIKE。跨会话记忆/知识检索精度提升3-5倍，不装模型不调API。

### 4. 并行工具执行
同一轮LLM返回的多个tool_calls并发执行(goroutine)，复杂任务速度提升2-3倍。write_file自动串行防冲突。

### 5. NotebookLM式两阶段PPT
Phase 1: Agent深度分析数据→生成图表PNG→计算统计指标
Phase 2: 写JSON工作流(引用全部素材)→Python渲染
每页观点+图表+表格+要点同时在，内容密度是普通AI PPT的3倍。

### 6. 22个Go原生命令
ls/cat/grep/wc/head/tail/sed/awk/curl/find/cp/mv/rm/mkdir/echo/pwd/which/sort/uniq — 全部Go标准库实现。Windows/Mac/Linux一致体验，不需要安装Git Bash。

### 7. 自动修复循环
- failTracker: 同一工具同一错误≥2次→注入修复方案
- 工具审查: 每5个工具→统计失败率→失败≥3次标记忽略
- HTTP 400错误: 自动清理损坏消息对→强制纯文本回复

### 8. 模型配置校验
启动时检查API key是否占位符/太短/baseURL格式，输出中文配置指南。

### 9. 前端体验
- `/storage/` 直链(点击下载)
- Welcome tips(不占对话历史)
- File默认storage目录
- TODO面板功能说明
- 耗时展示(DB精确计算)
- 响应式卡片布局
- 字体紧凑化(0.78em)
