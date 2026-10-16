# 意图识别系统

## 当前状态

`skill.go:extractIntentKeywords()` — 30+ 意图模式，匹配用户输入中的关键词，返回语义标签。

已应用场景：
- **Skills 匹配**：系统提示词中 Topic-relevant 技能列表，用意图标签匹配 skill 描述，替代简单分词
- **Skill 搜索**：`BuildSkillsSummaryWithIntent()` 按意图搜索相关技能

## 意图模式（30+ 类）

| 意图 | 关键词示例 |
|------|-----------|
| code | 写代码, 编写, 实现, 开发, 脚本, 编程 |
| analysis | 分析, 统计, 计算, 汇总, 对比 |
| data | 数据, csv, json, 提取, 清洗, 转换 |
| report | 报表, 日报, 图表, excel, ppt, 幻灯片 |
| chart | 图表, 折线图, 柱状图, 饼图, 绘图 |
| debug | 调试, 错误, 修复, 排查, 异常, 报错 |
| api | 接口, api, 请求, post, get, http |
| db | 数据库, sql, mysql, 查询, 表 |
| git | git, 提交, 推送, 分支, 合并 |
| test | 测试, 验证, 单元测试, 自测 |
| deploy | 部署, 发布, 上线 |
| monitor | 监控, 状态, 健康检查, 性能 |
| search | 搜索, 查询, 查找, 检索 |
| notify | 通知, 提醒, 消息, 推送 |
| translate | 翻译, 中英文 |
| summarize | 总结, 摘要, 概括 |
| security | 安全, 权限, 加密, 认证 |
| backup | 备份, 恢复, 还原 |
| schedule | 定时, 计划任务, 调度 |
| sendemail | 发邮件, 163, 邮箱 |
| image | 图片, 截图, 海报, 生成图片 |
| document | 文档, 说明文档, markdown, 注释 |
| optimize | 优化, 提速, 改进, 重构 |
| ... | ... |

## 可扩展场景

### 1. Agent 自动路由
根据意图自动选择 Agent，无需手动切换。
```
code/debug/api → 开发 Agent
report/data/chart → 分析 Agent  
email/notify → 通讯 Agent
search/summarize → 通用 Agent
deploy/monitor → 运维 Agent
```

### 2. 对话历史智能裁剪
压缩上下文时按当前意图过滤：
- code 意图 → 保留代码相关消息，丢弃闲聊
- debug 意图 → 保留报错信息和修复记录
- search 意图 → 保留搜索结果，丢弃无关工具调用

### 3. Skill 预加载
匹配到意图后，把 top 1-2 个 skill 的 SKILL.md 内容直接注入上下文，省掉 `tool_detail` + `read_file` 两步调用。
```
PPT 意图 → 自动加载 better-ppt skill 用法
邮件意图 → 自动加载 sendmail skill 用法
部署意图 → 自动加载 remote_deploy skill 用法
```

### 4. 任务参数自适应
根据意图调整任务执行策略：
- code/debug → 更多迭代次数、更长超时
- report → Markdown 结构化输出
- data → 表格优先展示
- search → 更快响应，减少迭代

### 5. 记忆检索过滤
按意图过滤记忆库，只加载相关历史：
- debug 意图 → 只检索报错相关记忆
- code 意图 → 只检索编程经验
- report 意图 → 跳过开发类记忆

### 6. 输出格式自适应
根据意图选择输出格式：
- analysis → 表格 + 要点
- code → 代码块
- report → Markdown 结构
- summarize → 要点列表
- image → HTML/CSS 卡片

## 实现优先级建议

| 优先级 | 场景 | 收益 | 复杂度 |
|--------|------|------|--------|
| P0 | Skills 匹配（已完成） | 高 | 低 |
| P1 | Skill 预加载 | 高 | 低 |
| P2 | Agent 路由 | 高 | 中 |
| P3 | 输出格式自适应 | 中 | 低 |
| P4 | 对话历史裁剪 | 中 | 中 |
| P5 | 任务参数自适应 | 中 | 低 |
| P6 | 记忆过滤 | 中 | 中 |
