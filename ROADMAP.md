# Development Roadmap

## 已完成 (2026-06-16 ~ 2026-06-18)

### 核心架构
- [x] **Model Provider 供应商体系** — DB 加密存储 API Key，支持多供应商(DeepSeek/豆包/OpenAI/Gemini/Ollama)，能力标签(text-to-text/multi-modal/text-to-image/text-to-video)
- [x] **DE 执行链路接入 Provider** — Dispatch 注入 model_binding → Pipeline 步骤按 capability 自动选模型
- [x] **DE 结果存会话** — savePipelineResultToChat，Web UI 能看到 DE 回复
- [x] **DE 输出过滤** — stripToolCalls 正则过滤 `<tool_call>` 痕迹
- [x] **Pipeline 审批超时告警** — EscalationAfter 24h/48h 半时预警 + 超时 PushSystemMessage
- [x] **Pipeline 持久化恢复** — RecoverOrphanExecutions，重启恢复 waiting_approval 状态

### 数字员工
- [x] 21 个 DE（15 免费 + 6 商业版），Tier 分级(community/commercial)
- [x] 商业版 DE 锁定：前端 🔒 标识 + 后端 dispatch 拦截
- [x] DE 面板统计：共 N 人 · 免费 N · 🔒 N
- [x] DE 表单加模型选择下拉框（文字/视觉/出图）
- [x] DB 空启动自动播种 16 DE + 16 Pipeline

### 自动更新
- [x] 版本号 ldflags 注入 + `scripts/build/build_windows.bat` 交互式输入
- [x] 一键更新：发现 → 下载 → 进度条轮询 → 替换重启
- [x] bat 脚本重试循环防文件占用 + 保留下载原文件名
- [x] Windows/macOS 替换重启、Mac Intel/Apple 架构区分
- [x] 人话错误提示（502/503/500/404）
- [x] 下载进度条：前端 500ms 轮询 `/api/update/progress`

### Web UI
- [x] DE 面板统计栏 + 商业版标识
- [x] 供应商管理页面（增删改查 + 模型卡片）
- [x] 技能管理页面（列表/新建/编辑/删除/热重载/GitHub 安装）
- [x] 工具市场页面（运行时/AI CLI/知识工具/医疗 AI/技能 Hub）
- [x] 健康检查加强（DB 状态/Agent 数/DE 数/运行时长/版本）
- [x] 页面版号动态读取、Session 分页(默认 20 条)
- [x] 运行按钮去色块 + 删除/运行位置调换

### 安全与合规
- [x] **PII 脱敏工具** — redact_pii，9 种识别模式(身份证/手机/邮箱/银行卡/车牌等)
- [x] **HIPAA 合规模板** — 脱敏→本地初筛→云端增强→回填→审批
- [x] **本地模型支持** — Ollama Provider (qwen3 + llama3.2-vision)
- [x] API Key AES-256-GCM 加密存储

### 集成
- [x] **Obsidian Vault 集成** — vault_explore 工具(6 操作) + obsidian-sync 技能
- [x] **飞书/钉钉/企微/QQ DE 通知** — SendMessage 通用接口
- [x] **钉钉 SendMessage 实现** — webhook 缓存按 conversationID 发送
- [x] 医疗/法律/金融行业技能包

### 运维
- [x] DB 每日 VACUUM + 备份(保留 7 份)
- [x] Storage 临时文件定期清理(7 天)
- [x] Session 消息分页(20 条默认)

### 项目管理
- [x] Electron 桌面应用壳（托盘 + 子进程管理 + NSIS 安装包）
- [x] README.md 技术文档重写（7 步 Go 参考实现 + 数据流图）
- [x] [Agent System 产品介绍/BP 风格文档](docs/architecture/AGENT_SYSTEM.md)

---

## 待完成

### 紧急
- [ ] **工具市场弹窗不工作** — 正在排查 embed.FS 编译问题
- [ ] 前端所有 JS/CSS 加版本号防缓存

### 近期
- [ ] 商业版 License 在线验证（RSA 签名）
- [ ] DE 通知飞书/钉钉/企微 真实环境实测
- [ ] `config.yaml` 老 model_config 标记 deprecated，引导迁移到 Provider
- [ ] 加点测试（至少健康检查 + DE 播种）

### 中期
- [ ] 前端移动端适配
- [ ] 多语言 Web UI（英文版）
- [ ] API 文档自动生成（Swagger/OpenAPI）
- [ ] Pipeline 审批记录页面
- [ ] 飞书卡片消息 + 审批组件集成
- [ ] Flux/Midjourney 等生图 API 对接

### 远期
- [ ] 插件市场 / Skill Hub 在线浏览
- [ ] 多租户 SaaS 模式
- [ ] 远程 Agent 协作（跨机器 Pipeline）
- [ ] 语音唤醒 + 实时对话
