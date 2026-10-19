# OpenKitty 技术架构文档

> Go + React 多智能体 AI 协作框架，支持工具调用、技能系统、记忆管理、多渠道接入

---

## 一、整体架构

```
┌─────────────────────────────────────────────────────┐
│                    Gateway (gateway.go)              │
│  HTTP API / WebSocket / SSE / 多渠道接入            │
│  飞书 / 钉钉 / QQ / 企微 / 邮件                     │
├─────────────────────────────────────────────────────┤
│              MultiAgentEngine (mac.go)               │
│  6种协作模式: 顺序/并行/群聊/投票/嵌套/有主持        │
├─────────────────────────────────────────────────────┤
│                LLMAgent (mac.go)                     │
│  LLM 迭代循环 / 工具注册表 / 技能系统 / 记忆系统     │
├──────────┬──────────┬──────────┬────────────────────┤
│  Tools   │  Skills  │  Memory  │  Safety            │
│ (42个)   │ (42个)   │ (SQLite) │  (4级风险)         │
└──────────┴──────────┴──────────┴────────────────────┘
```

### 核心数据流

```
用户请求 → Gateway(路由/鉴权) → Engine(选择协作模式)
  → Agent(LLM迭代循环) → 工具执行 → 结果输出 → 记忆保存 → 响应返回
```

---

## 二、Agent 核心循环

### LLMAgent.Generate() 主循环

```go
// mac.go:753
func (a *LLMAgent) Generate(ctx context.Context, input string) (string, error) {
    // 1. 构建系统提示词
    systemPrompt := a.buildSystemPrompt(...)
    
    // 2. 初始化消息列表
    messages := [systemPrompt, userMessage]
    
    // 3. 迭代循环 (最多120轮)
    for iteration < a.maxIterations {
        // 检查取消
        if IsTaskCancelled(db, taskID) { break }
        
        // 检查用户中断(插话)
        if msg := checkInterrupt(sessionID) { messages = append(messages, userMsg) }
        
        // 调用 LLM
        response := a.callLLM(ctx, messages)
        
        // 处理工具调用
        if response.ToolCalls != nil {
            for _, tc := range response.ToolCalls {
                result := a.toolRegistry.Execute(tc)
                messages = append(messages, toolResult)
            }
        } else {
            // 无工具调用 = 最终回复
            return response.Content
        }
        
        // 上下文压缩 (3层递进)
        if shouldCompress(messages) {
            messages = compress(messages)
        }
    }
}
```

### 关键设计决策

**1. 迭代循环 vs 单次调用**: 采用迭代循环允许 agent 在单次任务中多次调用 LLM + 工具，实现复杂的多步推理。每次迭代 LLM 可以看到之前工具调用的结果。

**2. 工具调用机制**: 采用 OpenAI Function Calling 格式。LLM 返回 `tool_calls` JSON，系统解析并执行对应工具，将结果作为 `tool` 角色消息追加到对话中。

**3. 渐进式工具加载**: Core tools (15个) 在系统提示词中带有完整参数 schema。Extended tools (27个) 只有名称和一行描述，agent 需要时通过 `tool_detail(name)` 按需获取参数。

---

## 三、三层上下文压缩体系

### 设计理念

先做便宜的事，最后才用 LLM 压缩。参考 Claude Code 的 5 层和 OpenClaw 的 6 层压缩流水线。

### Layer 1: 工具结果落盘 (零 LLM 成本)

```go
// mac.go:offloadToolResult
// 超大工具结果 (>8000 chars) → 写入磁盘，上下文只保留预览+路径
func (a *LLMAgent) offloadToolResult(toolName, content string) string {
    if len(content) <= 8000 { return content }
    // 写入 storage/tool_results/
    os.WriteFile(filePath, content)
    // 上下文只保留: 头部1200字 + "完整内容已保存至xxx" + 尾部600字
    return head + "...[完整内容已保存]..." + tail
}
```

### Layer 2: 智能规则压缩 (零 LLM 成本)

```go
// mac.go:pressLongMessages + smartTruncate
// 5档距离分级 + 数据工具3倍阈值 + 头尾保留
//
// 距离分档: ≤3(8000) / ≤5(4000) / ≤10(1500) / ≤20(600) / >20(200)
// 数据工具(web_fetch, read_file, grep等): 阈值×3
//
// SmartTruncate: 保留头部70% + 尾部30%，中间标记省略
// 对新闻列表、日志等按时间倒序排列的内容尤其有效
```

### Layer 3: LLM 语义压缩

```go
// mac.go:compressWithLLM
// 冷却机制: 每5次压缩触发1次 LLM 压缩(避免API浪费)
// 失败自动降级到 Layer 2
// 提示词要求保留: 用户消息原文 + 关键数据(URL/路径/错误) + 待办事项
```

### 对比参考

| 维度 | Claude Code | OpenClaw | OpenKitty |
|------|------------|----------|-----------|
| 分层数 | 5层 | 6层 | 3层 |
| 零成本层 | Tool Result Budget + Snip + Microcompact | Strip thinking + File-swap | Tool offloading + pressLongMessages |
| LLM摘要 | AutoCompact (9段式prompt) | Group-summarize | compressWithLLM |
| 可恢复性 | 摘要后可恢复文件/skill | drill-in工具可展开 | 工具结果落盘可read_file |

---

## 四、工具系统

### Tool 接口

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, params map[string]interface{}) (string, error)
}
```

### 核心工具 (15个，完整schema)

| 工具 | 功能 | 关键技术 |
|------|------|---------|
| web_fetch | 网页抓取+搜索(17引擎) | chromedp渲染 + Mozilla Readability + CSS token提取 |
| read_file/write_file/edit_file | 文件操作 | HTML直接写入检测 + append模式 |
| cli | 命令执行 | 持久Shell复用 + 8s超时降级 |
| grep/glob | 代码搜索 | ripgrep |
| code_check | 代码检查 | syntax/lint/test/run |
| plan_task/update_plan_step | 任务规划 | 迭代式思考 |
| speak/listen | 语音合成/识别 | edge-tts + SAPI + whisper |

### web_fetch 管线

```
1. chromedp JS渲染 (30s超时)
   ↓ 失败 → HTTP直接获取
2. Mozilla Readability 提取正文
   ↓ 失败 → EnhancedHTMLParser
3. CSS Design Token 提取
4. mode="snapshot" → 浏览器DOM结构(accessibility tree)
```

### write_file HTML 处理

```go
// 检测完整HTML，直接写入，跳过Markdown转换包裹
if strings.HasPrefix(content, "<!DOCTYPE") || strings.HasPrefix(content, "<html") {
    return writeTextFile(path, content) // 直接写入
}
// 否则: Markdown → HTML 转换
```

---

## 五、技能系统

### 技能格式

```yaml
---
name: Nova Chatroom Agent
description: Post and reply in sicurrent.com chatroom
read_when:
  - Posting to Nova chatroom
  - Replying in chatroom
tags:
  - chatroom
  - 聊天室
---
# Markdown 指引内容...
```

### 技能发现

```go
// skill.go:calculateSkillMatchScore
// 多维度匹配打分:
//   read_when 匹配: 权重12 (用户意图最直接信号)
//   Tag 匹配:     权重10
//   名称匹配:     权重10 (+5 exact)
//   域名匹配:     权重10
//   描述匹配:     权重5
//   内容匹配:     最高10
```

### 系统提示词策略

- 固定展示 3 个 hero skill (better-ppt, nova-chatroom, web-design)
- 关键词匹配 2-3 个任务相关 skill
- 描述截断到 80 字符
- 其余 37+ 个通过 `list_skills` 按需查询

---

## 六、安全系统

### 四级风险分级

```go
// safety.go:ClassifyToolAction + classifyCLICommand
RiskSafe     // 只读操作: read_file, list_dir, grep, glob
RiskModerate // 低风险: curl GET, echo, cat, python, git status
RiskHigh     // 高风险: curl POST, rm, shutdown, chmod 777
RiskBlocked  // 禁止: fork bomb, format C:, dd if=
```

### CLI 细粒度分类

```go
func classifyCLICommand(cmd string) RiskLevel {
    if IsDangerousFileCommand(cmd) { return RiskHigh }
    if strings.HasPrefix(cmd, "curl ") {
        if containsPOST(cmd) { return RiskModerate }
        return RiskModerate  // GET请求
    }
    if isReadOnly(cmd) { return RiskModerate } // echo, cat, ls, grep
    return RiskHigh  // 未知命令默认高风险
}
```

---

## 七、前端实时通信

### SSE (Server-Sent Events)

```
Gateway ──SSE──→ Browser
  │                │
  │ progress event │ update progress-timeline bubble
  │ tool_start     │ show tool + key param
  │ complete       │ fetch result + render in chat
```

### 进度气泡格式

```
14:15:30 r3-s1                         ← 第3轮第1步，带时间戳
14:15:32 web_fetch https://api.xxx.com ← 工具+关键参数
14:15:35 write_file report.md          ← 写文件
14:15:38 plan_task 1.抓取 2.分析 3.生成 ← 计划详情
14:15:40 update_plan_step step_1=completed (抓取风格) ← 步骤标题
14:16:00 Done                          ← 任务完成
```

### 任务完成处理

```javascript
// 任务完成后: 直接fetch结果 → renderMessage → 追加到聊天区
// 无需刷新页面
fetch('/api/task/' + taskId)
  .then(r => r.json())
  .then(data => {
      var msg = { role: 'assistant', content: data.result };
      document.getElementById('messagesArea').innerHTML += renderMessage(msg);
  });
```

---

## 八、关键优化记录

### 系统提示词优化

| 阶段 | 大小 | 措施 |
|------|------|------|
| 初始 | 31K tokens | - |
| 优化后 | ~9.8K tokens | Skill描述80字截断、Remote Deploy 7→1行、Guidelines 11→1行、Hero skill固定3个 |

### 上下文管理

| 参数 | 旧值 | 新值 |
|------|------|------|
| maxContextChars | 20,000 | 80,000 |
| maxTokens(agent) | 8,048 | 16,384 |
| web_fetch上限 | 30,000 | 50,000 |
| JS渲染超时 | 15s | 30s |

### 稳定性修复

| 问题 | 根因 | 修复 |
|------|------|------|
| Regex panic | Go不支持 `(?!...)` 负向先行断言 | 改用正向匹配+字符串判断 |
| CLI死循环 | 同命令反复调用不检测 | `cliCmdTracker` ≥3次注入 STOP |
| 终端创建卡死 | git-bash启动阻塞 | 8s超时→降级cmd执行 |
| 任务取消无效 | gateway ID ≠ agent ID | `sync.Map` 双ID标记 |
| HTML `<p>`包裹 | write_file强制Markdown转换 | 检测完整HTML直接写入 |
| 中文截断乱码 | 字节截断切碎UTF-8 | `[]rune` 字符级截断 |

---

## 九、设计模式识别

| 模式 | 位置 | 说明 |
|------|------|------|
| 工厂模式 | AgentFactory | 创建和缓存 Agent 实例 |
| 策略模式 | Tool接口 | 42个工具统一Execute接口 |
| 观察者模式 | AgentCallback | Agent状态变更通知Gateway |
| 责任链模式 | 3层压缩 | Layer1→2→3递进处理 |
| 享元模式 | PersistentShell | 按taskID复用终端会话 |
| 代理模式 | ClawHub Tool | 代理下载+格式转换 |

---

## 十、部署与构建

```bash
# 构建 (web/ 通过 //go:embed 内嵌到二进制)
go build -ldflags="-s -w" -o openkitty.exe ./cmd/macs

# UPX 压缩: 50.8MB → 13.2MB (25.9%)
upx --best openkitty.exe

# 启动
./openkitty.exe -c config.yaml
# Web UI: http://localhost:8080/web
```
