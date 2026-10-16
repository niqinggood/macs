package meetingminutes

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agent describes the small, presentation-focused subset of an agent needed
// in generated meeting minutes.
type Agent struct {
	Name               string
	OriginalName       string
	Type               string
	Model              string
	SystemMessage      string
	EnabledTools       []string
	SupportsMultimodal bool
}

// Task describes one collaboration task in the generated report.
type Task struct {
	TaskID string
	Agent  string
	Type   string
}

type Thought struct {
	Time      string `json:"time"`
	AgentName string `json:"agent_name"`
	TaskID    string `json:"task_id,omitempty"`
	Type      string `json:"type"`
	Content   string `json:"content"`
}

type LLMCall struct {
	Time          string `json:"time"`
	AgentName     string `json:"agent_name"`
	TaskID        string `json:"task_id,omitempty"`
	Prompt        string `json:"prompt"`
	Response      string `json:"response"`
	TokenEstimate int    `json:"token_estimate"`
}

type ConversationLog struct {
	Time    string `json:"time"`
	Speaker string `json:"speaker"`
	Message string `json:"message"`
}

type CollaborationEvent struct {
	Time        string                 `json:"time"`
	Type        string                 `json:"type"`
	Description string                 `json:"description"`
	Agents      []string               `json:"agents,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

type ErrorRecord struct {
	Time      string `json:"time"`
	TaskID    string `json:"task_id"`
	AgentName string `json:"agent_name"`
	Severity  string `json:"severity"`
	ErrorMsg  string `json:"error_msg"`
	Context   string `json:"context,omitempty"`
}

type EnhancedMeetingMinutes struct {
	SessionStart        time.Time                `json:"session_start"`
	Records             []string                 `json:"-"`
	TaskRecords         []map[string]interface{} `json:"task_records"`
	ThoughtRecords      map[string][]Thought     `json:"thought_records"`
	LLMCallRecords      []LLMCall                `json:"llm_call_records"`
	CollaborationEvents []CollaborationEvent     `json:"collaboration_events"`
	ErrorRecords        []ErrorRecord            `json:"error_records"`
	ConversationLogs    []ConversationLog        `json:"conversation_logs"`
}

func NewEnhancedMeetingMinutes() *EnhancedMeetingMinutes {
	return &EnhancedMeetingMinutes{
		SessionStart:        time.Now(),
		Records:             make([]string, 0),
		TaskRecords:         make([]map[string]interface{}, 0),
		ThoughtRecords:      make(map[string][]Thought),
		LLMCallRecords:      make([]LLMCall, 0),
		CollaborationEvents: make([]CollaborationEvent, 0),
		ErrorRecords:        make([]ErrorRecord, 0),
		ConversationLogs:    make([]ConversationLog, 0),
	}
}

// AddHeader 添加头部信息
func (m *EnhancedMeetingMinutes) AddHeader(teamName string, taskDescription string, agents []Agent) {
	header := fmt.Sprintf(`
## 多Agent团队会议记录

### 团队基本信息
- 团队名称: %s
- 任务描述: %s
- 会话开始时间: %s
- 参与Agent数量: %d

### Agent角色配置
`, teamName, taskDescription, m.SessionStart.Format("2006-01-02 15:04:05"), len(agents))

	for idx, agent := range agents {
		displayName := agent.OriginalName
		if displayName == "" {
			displayName = agent.Name
		}
		systemMsg := agent.SystemMessage
		if len(systemMsg) > 120 {
			systemMsg = systemMsg[:120] + "..."
		}
		tools := strings.Join(agent.EnabledTools, ", ")
		if tools == "" {
			tools = "无"
		}

		header += fmt.Sprintf(`
**Agent %d: %s**
- Agent ID: %s
- Agent类型: %s
- 模型: %s
- 系统提示摘要: %s
- 启用工具: %s
- 多模态支持: %v

`, idx+1, displayName, agent.Name, agent.Type, agent.Model, systemMsg, tools, agent.SupportsMultimodal)
	}

	m.Records = append(m.Records, header)
}

// AddCollaborationPlan 添加协作计划
func (m *EnhancedMeetingMinutes) AddCollaborationPlan(collabType string, tasks []Task, failureStrategy string, maxRound int) {
	plan := fmt.Sprintf(`
### 协作执行计划

#### 协作模式配置
- 协作类型: %s
- 失败策略: %s
- 最大回合数: %d

#### 任务执行序列
序号 | 任务ID | 执行Agent | 任务类型
------|--------|-----------|----------
`, collabType, failureStrategy, maxRound)

	for idx, task := range tasks {
		taskType := task.Type
		if taskType == "" {
			taskType = "default"
		}
		plan += fmt.Sprintf("%d | %s | %s | %s\n", idx+1, task.TaskID, task.Agent, taskType)
	}

	plan += "\n*注：任务按上述顺序或并行执行*\n"
	m.Records = append(m.Records, plan)
}

// AddTaskStart 添加任务开始
func (m *EnhancedMeetingMinutes) AddTaskStart(taskID string, agentName string, inputData string) {
	timestamp := time.Now().Format("15:04:05.000")

	inputPreview := inputData
	if len(inputPreview) > 250 {
		inputPreview = inputPreview[:250] + "..."
	}

	taskRecord := fmt.Sprintf(`
## 任务执行过程: %s

#### 任务启动信息
- 任务ID: %s
- 执行Agent: %s
- 开始时间: %s
- 输入数据预览:
%s
- 输入长度: %d 字符

**📢 系统消息**: Agent `+"`"+`%s`+"`"+` 开始处理任务 `+"`"+`%s`+"`"+`
`, taskID, taskID, agentName, timestamp, inputPreview, len(inputData), agentName, taskID)

	m.Records = append(m.Records, taskRecord)

	m.TaskRecords = append(m.TaskRecords, map[string]interface{}{
		"task_id":    taskID,
		"agent_name": agentName,
		"start_time": timestamp,
		"status":     "running",
	})
}

// AddTaskProgress 添加任务进度
func (m *EnhancedMeetingMinutes) AddTaskProgress(taskID string, agentName string, progress string, details string) {
	timestamp := time.Now().Format("15:04:05.000")

	progressRecord := fmt.Sprintf(`
#### 任务进度更新
- 时间: %s
- Agent: %s
- 进度状态: %s
`, timestamp, agentName, progress)

	if details != "" {
		progressRecord += fmt.Sprintf("- 进度详情: %s\n", details)
	}

	progressRecord += fmt.Sprintf("\n**📋 进度更新**: Agent `%s` 报告进度: `%s`\n", agentName, progress)
	m.Records = append(m.Records, progressRecord)
}

// AddConversationLog 添加对话记录
func (m *EnhancedMeetingMinutes) AddConversationLog(speaker string, message string, timestamp string) {
	if timestamp == "" {
		timestamp = time.Now().Format("15:04:05.000")
	}

	messagePreview := message

	conversationRecord := fmt.Sprintf(`
#### 🗣️ 对话记录
- 时间: %s
- 发言人: %s
- 发言内容:
%s
`, timestamp, speaker, messagePreview)

	m.Records = append(m.Records, conversationRecord)

	m.ConversationLogs = append(m.ConversationLogs, ConversationLog{
		Time:    timestamp,
		Speaker: speaker,
		Message: message,
	})
}

// AddAgentThinking 添加Agent思考
func (m *EnhancedMeetingMinutes) AddAgentThinking(agentName string, thinking string, taskID string, thoughtType string) {
	timestamp := time.Now().Format("15:04:05.000")

	thoughtTypeMap := map[string]string{
		"internal":   "内部思考",
		"reasoning":  "逻辑推理",
		"planning":   "任务规划",
		"evaluation": "结果评估",
		"decision":   "决策过程",
	}

	thoughtDisplay := thoughtTypeMap[thoughtType]
	if thoughtDisplay == "" {
		thoughtDisplay = thoughtType
	}

	thinkingPreview := thinking
	if len(thinkingPreview) > 300 {
		thinkingPreview = thinkingPreview[:300] + "..."
	}

	thinkingRecord := fmt.Sprintf(`
#### 🤔 Agent思考过程
- Agent: %s
- 时间: %s
- 思考类型: %s
`, agentName, timestamp, thoughtDisplay)

	if taskID != "" {
		thinkingRecord += fmt.Sprintf("- 关联任务: %s\n", taskID)
	}

	thinkingRecord += fmt.Sprintf(`
- 思考内容:
%s
- 思考长度: %d 字符

**💭 思考记录**: Agent `+"`"+`%s`+"`"+` 正在进行 `+"`"+`%s`+"`"+`，内容已记录
`, thinkingPreview, len(thinking), agentName, thoughtDisplay)

	m.Records = append(m.Records, thinkingRecord)

	thought := Thought{
		Time:      timestamp,
		AgentName: agentName,
		TaskID:    taskID,
		Type:      thoughtType,
		Content:   thinking,
	}
	m.ThoughtRecords[agentName] = append(m.ThoughtRecords[agentName], thought)
}

// AddLLMCall 添加LLM调用记录
func (m *EnhancedMeetingMinutes) AddLLMCall(agentName string, prompt string, response string, taskID string, tokenEstimate int) {
	timestamp := time.Now().Format("15:04:05.000")

	if tokenEstimate == 0 {
		totalChars := len(prompt) + len(response)
		tokenEstimate = int(float64(totalChars) * 0.7)
	}

	promptPreview := prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200] + "..."
	}

	responsePreview := response
	if len(responsePreview) > 300 {
		responsePreview = responsePreview[:300] + "..."
	}

	llmRecord := fmt.Sprintf(`
#### ⚙️ LLM API调用记录
- 调用Agent: %s
- 调用时间: %s
`, agentName, timestamp)

	if taskID != "" {
		llmRecord += fmt.Sprintf("- 关联任务: %s\n", taskID)
	}

	llmRecord += fmt.Sprintf(`
- 请求预览:
%s
- 响应预览:
%s
- 统计信息:
  - 请求长度: %d 字符
  - 响应长度: %d 字符
  - 估算Token: %d

**🔧 API调用**: Agent `+"`"+`%s`+"`"+` 调用了LLM API，已收到响应
`, promptPreview, responsePreview, len(prompt), len(response), tokenEstimate, agentName)

	m.Records = append(m.Records, llmRecord)

	m.LLMCallRecords = append(m.LLMCallRecords, LLMCall{
		Time:          timestamp,
		AgentName:     agentName,
		TaskID:        taskID,
		Prompt:        prompt,
		Response:      response,
		TokenEstimate: tokenEstimate,
	})
}

// AddTaskCompletion 添加任务完成
func (m *EnhancedMeetingMinutes) AddTaskCompletion(taskID string, agentName string, outputData string, thinkingProcess string, metrics map[string]interface{}) {
	timestamp := time.Now().Format("15:04:05.000")

	outputPreview := outputData
	if len(outputPreview) > 400 {
		outputPreview = outputPreview[:400] + "..."
	}

	completionRecord := fmt.Sprintf(`
#### ✅ 任务完成结果
- 完成时间: %s
- 执行Agent: %s
- 执行状态: 成功完成
- 输出摘要:
%s
- 输出统计:
  - 总长度: %d 字符
  - 行数: %d
`, timestamp, agentName, outputPreview, len(outputData), strings.Count(outputData, "\n")+1)

	if metrics != nil {
		completionRecord += "- 性能指标:\n"
		for key, value := range metrics {
			if key == "duration" {
				if duration, ok := value.(float64); ok {
					completionRecord += fmt.Sprintf("  - 执行耗时: %.2f 秒\n", duration)
				}
			} else if key == "retry_count" {
				completionRecord += fmt.Sprintf("  - 重试次数: %v\n", value)
			} else if key == "memory_used" {
				completionRecord += fmt.Sprintf("  - 内存使用: %v MB\n", value)
			}
		}
	}

	if thinkingProcess != "" && len(thinkingProcess) > 10 {
		thinkingPreview := thinkingProcess
		if len(thinkingPreview) > 150 {
			thinkingPreview = thinkingPreview[:150] + "..."
		}
		completionRecord += fmt.Sprintf(`
- 关联思考过程:
> %s
`, thinkingPreview)
	}

	completionRecord += fmt.Sprintf("\n**🎯 任务完成**: Agent `%s` 成功完成任务 `%s`\n", agentName, taskID)

	m.Records = append(m.Records, completionRecord)

	// 更新任务记录
	for i, record := range m.TaskRecords {
		if record["task_id"] == taskID && record["agent_name"] == agentName {
			record["status"] = "completed"
			record["end_time"] = timestamp
			record["output_length"] = len(outputData)
			if metrics != nil {
				if duration, ok := metrics["duration"]; ok {
					record["duration"] = duration
				}
			}
			m.TaskRecords[i] = record
			break
		}
	}
}

// AddCollaborationEvent 添加协作事件
func (m *EnhancedMeetingMinutes) AddCollaborationEvent(eventType string, description string, agents []string, details map[string]interface{}) {
	timestamp := time.Now().Format("15:04:05.000")

	eventMap := map[string]string{
		"engine_start":           "引擎启动",
		"sequential_start":       "顺序协作开始",
		"parallel_start":         "并行协作开始",
		"group_chat_start":       "群聊开始",
		"vote_start":             "投票开始",
		"nested_start":           "嵌套任务开始",
		"group_chat_formation":   "群聊组建",
		"chat_start":             "对话开始",
		"chat_complete":          "对话完成",
		"task_handoff":           "任务交接",
		"parallel_task_start":    "并行任务启动",
		"parallel_all_start":     "全部并行任务启动",
		"parallel_all_complete":  "全部并行任务完成",
		"vote_complete":          "投票完成",
		"host_group_start":       "有主持分组开始",
		"host_group_complete":    "有主持分组完成",
		"engine_complete":        "引擎完成",
		"execution_stopped":      "执行停止",
		"retry_start":            "重试开始",
		"agent_chat":             "Agent对话",
		"sequential_next":        "顺序执行下一步",
		"group_discussion_start": "分组讨论开始",
	}

	eventDisplay := eventMap[eventType]
	if eventDisplay == "" {
		eventDisplay = eventType
	}

	eventRecord := fmt.Sprintf(`
#### 🔄 协作事件: %s
- 发生时间: %s
- 事件描述: %s
`, eventDisplay, timestamp, description)

	if agents != nil {
		eventRecord += fmt.Sprintf("- 涉及Agent: %s\n", strings.Join(agents, ", "))
	}

	if details != nil {
		eventRecord += "- 事件详情:\n"
		for key, value := range details {
			if str, ok := value.(string); ok {
				if len(str) > 100 {
					str = str[:100] + "..."
				}
				eventRecord += fmt.Sprintf("  - %s: %s\n", key, str)
			} else {
				eventRecord += fmt.Sprintf("  - %s: %v\n", key, value)
			}
		}
	}

	eventRecord += fmt.Sprintf("\n**🔄 协作事件**: %s\n", description)

	m.Records = append(m.Records, eventRecord)

	m.CollaborationEvents = append(m.CollaborationEvents, CollaborationEvent{
		Time:        timestamp,
		Type:        eventType,
		Description: description,
		Agents:      agents,
		Details:     details,
	})
}

// AddError 添加错误记录
func (m *EnhancedMeetingMinutes) AddError(taskID string, agentName string, errorMsg string, context string, severity string) {
	timestamp := time.Now().Format("15:04:05.000")

	errorRecord := fmt.Sprintf(`
## ❌ 错误报告

#### 错误详情
- 任务ID: %s
- 执行Agent: %s
- 错误时间: %s
- 错误级别: %s
- 错误信息: %s
`, taskID, agentName, timestamp, severity, errorMsg)

	if context != "" {
		contextPreview := context
		if len(contextPreview) > 300 {
			contextPreview = contextPreview[:300] + "..."
		}
		errorRecord += fmt.Sprintf(`
- 错误上下文:
%s
`, contextPreview)
	}

	errorRecord += fmt.Sprintf(`
- 建议处理:
  - 检查Agent配置
  - 验证输入数据格式
  - 查看网络连接状态
  - 确认API密钥有效性

**⚠️ 错误警告**: Agent `+"`"+`%s`+"`"+` 在执行任务 `+"`"+`%s`+"`"+` 时遇到错误
`, agentName, taskID)

	m.Records = append(m.Records, errorRecord)

	// 更新任务记录
	for i, record := range m.TaskRecords {
		if record["task_id"] == taskID && record["agent_name"] == agentName {
			record["status"] = "failed"
			record["end_time"] = timestamp
			record["error"] = errorMsg
			m.TaskRecords[i] = record
			break
		}
	}

	m.ErrorRecords = append(m.ErrorRecords, ErrorRecord{
		Time:      timestamp,
		TaskID:    taskID,
		AgentName: agentName,
		Severity:  severity,
		ErrorMsg:  errorMsg,
		Context:   context,
	})
}

// AddSummary 添加总结
func (m *EnhancedMeetingMinutes) AddSummary(status string, deliveryFile string, taskResults []map[string]interface{}) {
	sessionEnd := time.Now()
	duration := int(sessionEnd.Sub(m.SessionStart).Seconds())

	totalTasks := len(m.TaskRecords)
	completedTasks := 0
	failedTasks := 0

	for _, t := range taskResults {
		if t["status"] == "completed" {
			completedTasks++
		} else if t["status"] == "failed" {
			failedTasks++
		}
	}

	summary := fmt.Sprintf(`
## 📊 会话总结报告

### 执行统计概览
- 总体状态: %s
- 开始时间: %s
- 结束时间: %s
- 总耗时: %d 秒 (%d分%d秒)
- 任务统计:
  - 总任务数: %d
  - 成功任务: %d
  - 失败任务: %d
`, status, m.SessionStart.Format("2006-01-02 15:04:05"), sessionEnd.Format("2006-01-02 15:04:05"),
		duration, duration/60, duration%60, totalTasks, completedTasks, failedTasks)

	if deliveryFile != "" {
		summary += fmt.Sprintf("\n- 交付文件: %s\n", deliveryFile)
	}

	// 对话记录统计
	if len(m.ConversationLogs) > 0 {
		summary += fmt.Sprintf(`
### 对话记录统计
- 总对话记录数: %d
`, len(m.ConversationLogs))

		speakerCounts := make(map[string]int)
		for _, log := range m.ConversationLogs {
			speakerCounts[log.Speaker]++
		}

		for speaker, count := range speakerCounts {
			summary += fmt.Sprintf("  - %s: %d 条发言\n", speaker, count)
		}
	}

	// 思考过程统计
	if len(m.ThoughtRecords) > 0 {
		totalThoughts := 0
		for _, thoughts := range m.ThoughtRecords {
			totalThoughts += len(thoughts)
		}

		summary += fmt.Sprintf(`
### 思考过程统计
- 总思考记录数: %d
`, totalThoughts)

		for agent, thoughts := range m.ThoughtRecords {
			summary += fmt.Sprintf("  - %s: %d 次思考\n", agent, len(thoughts))
		}
	}

	// LLM调用统计
	if len(m.LLMCallRecords) > 0 {
		totalCalls := len(m.LLMCallRecords)
		totalTokens := 0
		for _, call := range m.LLMCallRecords {
			totalTokens += call.TokenEstimate
		}

		avgTokens := 0
		if totalCalls > 0 {
			avgTokens = totalTokens / totalCalls
		}

		summary += fmt.Sprintf(`
### LLM调用统计
- 总调用次数: %d
- 估算总Token: %d
- 平均每次Token: %d
`, totalCalls, totalTokens, avgTokens)
	}

	// 错误统计
	if len(m.ErrorRecords) > 0 {
		errorCount := 0
		warningCount := 0
		for _, e := range m.ErrorRecords {
			if e.Severity == "ERROR" {
				errorCount++
			} else {
				warningCount++
			}
		}

		summary += fmt.Sprintf(`
### 错误统计
- 总错误数: %d
- 严重错误: %d
- 警告信息: %d
`, len(m.ErrorRecords), errorCount, warningCount)
	}

	// 协作事件统计
	if len(m.CollaborationEvents) > 0 {
		summary += fmt.Sprintf(`
### 协作事件统计
- 总事件数: %d
`, len(m.CollaborationEvents))

		eventCounts := make(map[string]int)
		for _, event := range m.CollaborationEvents {
			eventCounts[event.Type]++
		}

		for eventType, count := range eventCounts {
			eventDisplay := strings.ReplaceAll(eventType, "_", " ")
			eventDisplay = strings.Title(eventDisplay)
			summary += fmt.Sprintf("  - %s: %d 次\n", eventDisplay, count)
		}
	}

	// 性能分析
	if len(m.TaskRecords) > 0 {
		var totalDuration float64
		var completedWithDuration int
		var minDuration float64 = 1e9
		var maxDuration float64

		for _, t := range m.TaskRecords {
			if t["status"] == "completed" {
				if dur, ok := t["duration"]; ok {
					if d, ok := dur.(float64); ok {
						totalDuration += d
						completedWithDuration++
						if d < minDuration {
							minDuration = d
						}
						if d > maxDuration {
							maxDuration = d
						}
					}
				}
			}
		}

		avgDuration := 0.0
		if completedWithDuration > 0 {
			avgDuration = totalDuration / float64(completedWithDuration)
		}

		summary += fmt.Sprintf(`
### 性能分析
- 总执行时间: %.2f 秒
- 平均任务耗时: %.2f 秒
`, totalDuration, avgDuration)

		if completedWithDuration > 0 {
			summary += fmt.Sprintf("- 最快任务: %.2f 秒\n", minDuration)
			summary += fmt.Sprintf("- 最慢任务: %.2f 秒\n", maxDuration)
		}
	}

	summary += fmt.Sprintf(`
---
报告生成时间: %s
Multi-Agent Engine v1.0 - 增强会议记录系统
`, time.Now().Format("2006-01-02 15:04:05"))

	m.Records = append(m.Records, summary)
}

// Generate 生成会议记录文件
func (m *EnhancedMeetingMinutes) Generate(outputPath string) string {
	os.MkdirAll(filepath.Dir(outputPath), 0755)

	title := fmt.Sprintf(`# 多Agent团队执行详细记录

生成时间: %s
记录格式: 详细过程记录（包含Agent思考、对话、动作等）

%s

`, time.Now().Format("2006-01-02 15:04:05"), strings.Repeat("=", 80))

	toc := m.generateTableOfContents()

	fullContent := title + toc + strings.Join(m.Records, "\n")

	err := os.WriteFile(outputPath, []byte(fullContent), 0644)
	if err != nil {
		log.Printf("写入会议记录失败: %v", err)
		return ""
	}

	totalThoughts := 0
	for _, thoughts := range m.ThoughtRecords {
		totalThoughts += len(thoughts)
	}

	log.Printf("增强会议记录已生成: %s", outputPath)
	log.Printf("记录包含: %d 个任务记录, %d 个思考记录, %d 个LLM调用记录, %d 条对话记录",
		len(m.TaskRecords), totalThoughts, len(m.LLMCallRecords), len(m.ConversationLogs))

	return outputPath
}

// generateTableOfContents 生成目录
func (m *EnhancedMeetingMinutes) generateTableOfContents() string {
	toc := "## 记录目录\n\n"

	sections := []struct {
		name  string
		level int
	}{
		{"团队基本信息", 3},
		{"Agent角色配置", 3},
		{"协作执行计划", 3},
		{"任务执行过程", 2},
		{"会话总结报告", 2},
	}

	for _, s := range sections {
		indent := strings.Repeat("  ", s.level-2)
		toc += fmt.Sprintf("%s- %s\n", indent, s.name)
	}

	if len(m.TaskRecords) > 0 {
		toc += "\n### 详细任务列表\n"
		for i, task := range m.TaskRecords {
			status := "🔄进行中"
			if task["status"] == "completed" {
				status = "✅完成"
			} else if task["status"] == "failed" {
				status = "❌失败"
			}

			duration := ""
			if d, ok := task["duration"]; ok {
				duration = fmt.Sprintf(" (%.1fs)", d)
			}

			toc += fmt.Sprintf("  %d. %s - %s %s%s\n",
				i+1, task["task_id"], task["agent_name"], status, duration)
		}
	}

	if len(m.ConversationLogs) > 0 {
		toc += fmt.Sprintf("\n### 对话统计\n")
		toc += fmt.Sprintf("  - 总对话数: %d 条\n", len(m.ConversationLogs))
	}

	toc += fmt.Sprintf("\n%s\n\n", strings.Repeat("-", 80))

	return toc
}
