# OpenKitty 上下文管理架构

## 整体流程

```
用户发消息
    │
    ▼
gateway.go processTaskWithSession
    │
    ├─ 1. 从 chat_messages 表加载历史
    ├─ 2. 构建 historyMessages 数组
    ├─ 3. 注入 context
    │
    ▼
agent.Generate(ctx, message)
    │
    ├─ 4. 构建 systemPrompt (含"最近对话")
    ├─ 5. 从 context 提取 historyMessages
    ├─ 6. 拼装完整 messages 数组
    │
    ▼
发送给 DeepSeek LLM
```

---

## 第一步：加载历史消息

**文件**: `gateway.go` → `processTaskWithSession`

```go
// 从 DB 加载最近的聊天记录
query := g.db.Where(
    "session_id = ? AND role IN ('user', 'assistant') AND task_id != ?",
    sessionID, task.ID,
)
query.Order("created_at desc").Limit(50).Find(&messages)

// 反转顺序（DB 是倒序的，改成时间正序）
for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
    messages[i], messages[j] = messages[j], messages[i]
}
```

## 第二步：构建 historyMessages 数组

```go
for _, msg := range messages {
    role := "user"
    content := msg.Content
    if msg.Role == "assistant" {
        role = "assistant"
        content = truncateStr(content, 1200)  // ≈ 400 tokens
    }
    historyMessages = append(historyMessages, map[string]interface{}{
        "role":    role,
        "content": content,
    })
}
```

| 角色 | 处理方式 | 原因 |
|------|---------|------|
| 用户消息 | 完整保留 | 用户输入短，且是最重要的上下文 |
| assistant 回复 | 截断到 1200 字符 (~400 tokens) | 防止长回复（歌曲表格等）撑爆上下文 |

## 第三步：通过 context 传递

```go
ctx = context.WithValue(ctx, "history_messages", historyMessages)
```

## 第四步：构建 systemPrompt

**文件**: `mac.go` → `buildSystemPrompt` → `memory.go` → `GetMemoryContext`

systemPrompt 包含以下部分：

```
now is 2026-06-01 09:15:04
your name is openkitty1, You are agent...

## Runtime                           ← 工作目录、输出路径
## Skills                            ← 可用技能列表
## Platform Policy (Windows)          ← 平台策略
## How to Work                       ← 工作流程指引
## Guidelines                        ← 软件工程规范
## Available Tools                    ← 工具列表及说明

## 💬 最近对话                        ← memory.go GetMemoryContext
用户: 你开始了没
助手: 啊，抱歉抱歉！我一直在读取API文档...   ← truncate(answer, 100)

## 🔧 工具使用提示                    ← 工具成功率统计
## 📌 参考                            ← 相似历史问题匹配
## 📚 相关知识                        ← 知识库匹配

## Tool Examples                     ← 工具调用示例
## Response Format                   ← 响应格式
```

### "最近对话" 的数据来源

```go
// memory.go GetMemoryContext
m.db.Where("session_id = ?", sessionID).
    Order("created_at DESC").
    Limit(3).                    // 只取最近 3 轮
    Find(&recentEvents)

for i := len(recentEvents) - 1; i >= 0; i-- {
    event := recentEvents[i]
    // 用户问题：完整保留
    context.WriteString(fmt.Sprintf("用户: %s\n", event.UserQuestion))
    // 助手回答：截断到 100 字符
    answer := truncate(event.FinalAnswer, 100)
    context.WriteString(fmt.Sprintf("助手: %s\n", answer))
}
```

**注意**："最近对话" 来自 `memory_events` 表，记录的是每轮的最终答案。与 `chat_messages` 表不同：

| 表 | 内容 | 用途 |
|----|------|------|
| `chat_messages` | 所有角色消息（含中间过程） | 构建多轮消息历史 |
| `memory_events` | 每轮的 Q&A 摘要 + 执行记录 | system prompt 中的快速回顾 |

## 第五步：拼装最终 messages

**文件**: `mac.go` → `Generate`

```go
// 初始：只有 system prompt
messages = [system]

// 注入多轮历史
if historyMsgs, ok := ctx.Value("history_messages").([]map[string]interface{}); ok {
    for _, hm := range historyMsgs {
        messages = append(messages, hm)
    }
}

// 添加当前用户消息
messages = append(messages, map[string]interface{}{
    "role":    "user",
    "content": input,
})
```

最终发往 LLM 的消息结构：

```
messages = [
    {role: "system",   content: "<完整 systemPrompt>"},     // ~13K 字符
    {role: "user",     content: "http://192.168..."},       // 第1轮用户
    {role: "assistant",content: "好的，我已经获取到..."},    // 第1轮助手 (≤1200 字符)
    {role: "user",     content: "user1/user1"},             // 第2轮用户
    {role: "assistant",content: "登录成功！项目列表..."},    // 第2轮助手 (≤1200 字符)
    ...
    {role: "user",     content: "你开始了没"},              // 当前用户
]
```

## 第六步：上下文压缩（安全网）

**文件**: `mac.go` → agent loop

```go
const (
    compressMsgThreshold = 80   // 超过 80 条消息触发压缩
    maxContextChars      = 80000 // 超过 80000 字符触发压缩
)

if len(messages) > 80 || totalChars > 80000 {
    messages = pressLongMessages(messages)
}
```

`pressLongMessages` 按距离末尾的位置分级压缩：

| 距离末尾 | 截断阈值 | 说明 |
|---------|---------|------|
| ≤2 条 | 3000 字符 | 最新消息几乎不压缩 |
| 3-10 条 | 200 字符 | 中间消息大幅压缩 |
| >10 条 | 50 字符 | 旧消息极度压缩 |

**注意**：user 和 system 角色的消息不受 `pressLongMessages` 影响，始终保持完整。

## 信息到达 LLM 的两条路径总结

```
                    ┌──────────────────────┐
                    │      DeepSeek LLM     │
                    └──────────────────────┘
                           ▲          ▲
                           │          │
              路径 A       │          │   路径 B
          (多轮消息)       │          │  (最近对话)
                           │          │
        ┌──────────────────┘          └──────────────┐
        │  chat_messages 表              memory_events 表 │
        │  ≤50 条                        最近 3 轮          │
        │  assistant ≤1200 字符          assistant ≤100 字符│
        └───────────────────────────────────────────────┘
```

两条路径互补：
- **路径 A** 提供完整的对话脉络，是主要上下文
- **路径 B** 提供快速回顾，在 A 被压缩后仍能看到最近的摘要

## 相关文件

| 文件 | 关键函数 | 作用 |
|------|---------|------|
| `gateway.go` | `processTaskWithSession` | 加载历史，注入 context |
| `gateway.go` | `processTask` | 同上（非 streaming 路径） |
| `mac.go` | `Generate` | 拼装 messages，agent 主循环 |
| `mac.go` | `buildSystemPrompt` | 构建 system prompt |
| `memory.go` | `GetMemoryContext` | 构建"最近对话"等记忆上下文 |
| `mac.go` | `pressLongMessages` | 消息压缩（安全网） |
