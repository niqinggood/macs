package operations

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	todocore "mac/internal/todo"
)

type TodoCreator interface {
	CreateTodo(*todocore.Todo) (*todocore.Todo, error)
}

type TodoTools struct {
	db *gorm.DB
}

func NewTodoTools(db *gorm.DB) *TodoTools {
	return &TodoTools{db: db}
}

// ==================== 1. 创建 TODO 工具 ====================

type CreateTodoTool struct {
	tm TodoCreator
}

func NewCreateTodoTool(tm TodoCreator) *CreateTodoTool {
	return &CreateTodoTool{tm: tm}
}

func (t *CreateTodoTool) Name() string {
	return "create_todo"
}

func (t *CreateTodoTool) Description() string {
	return "为当前用户创建一个待办任务，可以设置执行时间、cron表达式或间隔执行。任务会由系统定期触发启动agent开始执行。"
}

func (t *CreateTodoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "string",
				"description": "任务标题",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "任务描述，即要让Agent执行的任务内容",
			},
			"priority": map[string]interface{}{
				"type":        "string",
				"description": "优先级: low, normal, high, urgent",
				"enum":        []string{"low", "normal", "high", "urgent"},
			},
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "指定执行的Agent名称，不指定则自动选择",
			},
			"schedule_type": map[string]interface{}{
				"type":        "string",
				"description": "调度类型: once(一次), cron(周期), interval(间隔)",
				"enum":        []string{"once", "cron", "interval"},
			},
			"run_at": map[string]interface{}{
				"type":        "string",
				"description": "执行时间（schedule_type=once时使用），格式: 2006-01-02 15:04:05",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "cron表达式（schedule_type=cron时使用），支持秒级: '0 0 9 * * *' 表示每天9点",
			},
			"interval_sec": map[string]interface{}{
				"type":        "integer",
				"description": "间隔秒数（schedule_type=interval时使用），默认60秒",
			},
			"input_data": map[string]interface{}{
				"type":        "string",
				"description": "任务输入数据，会作为Agent的输入",
			},
			"max_retries": map[string]interface{}{
				"type":        "integer",
				"description": "失败重试次数，默认3次",
			},
		},
		"required": []string{"title", "description"},
	}
}

func (t *CreateTodoTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	title, _ := params["title"].(string)
	if title == "" {
		return "请提供任务标题", nil
	}

	description, _ := params["description"].(string)
	if description == "" {
		return "请提供任务描述", nil
	}

	priority := todocore.PriorityNormal
	if p, ok := params["priority"].(string); ok {
		priority = todocore.TodoPriority(p)
	}

	scheduleType := "once"
	if st, ok := params["schedule_type"].(string); ok {
		scheduleType = st
	}

	agentName, _ := params["agent_name"].(string)
	if agentName == "" {
		if name, ok := ctx.Value("agent_name").(string); ok {
			agentName = name
		}
	}
	inputData, _ := params["input_data"].(string)

	maxRetries := 3
	if mr, ok := params["max_retries"].(float64); ok {
		maxRetries = int(mr)
	}

	userName := ""
	if name, ok := params["user_name"].(string); ok {
		userName = name
	}
	if userName == "" {
		userName = "anonymous"
	}

	todo := &todocore.Todo{
		Title:        title,
		Description:  description,
		Priority:     priority,
		AgentName:    agentName,
		ScheduleType: scheduleType,
		InputData:    inputData,
		MaxRetries:   maxRetries,
		CreatedBy:    userName,
	}

	if runAtStr, ok := params["run_at"].(string); ok && runAtStr != "" {
		runAt, err := parseTodoTime(runAtStr)
		if err == nil {
			todo.RunAt = &runAt
		}
	}

	if cronExpr, ok := params["cron_expr"].(string); ok {
		todo.CronExpr = cronExpr
	}

	if intervalSec, ok := params["interval_sec"].(float64); ok {
		todo.IntervalSec = int(intervalSec)
	}

	created, err := t.tm.CreateTodo(todo)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("✅ TODO已创建\n\n"))
	out.WriteString(fmt.Sprintf("ID: %s\n", created.TodoID))
	out.WriteString(fmt.Sprintf("标题: %s\n", created.Title))
	out.WriteString(fmt.Sprintf("优先级: %s\n", created.Priority))
	out.WriteString(fmt.Sprintf("调度类型: %s\n", created.ScheduleType))
	if created.NextRunAt != nil {
		out.WriteString(fmt.Sprintf("下次执行: %s\n", created.NextRunAt.Format("2006-01-02 15:04:05")))
	}
	out.WriteString(fmt.Sprintf("创建者: %s\n", created.CreatedBy))

	return out.String(), nil
}

// ==================== 2. 列出 TODO 工具 ====================

type ListTodosTool struct {
	db *gorm.DB
}

func NewListTodosTool(db *gorm.DB) *ListTodosTool {
	return &ListTodosTool{db: db}
}

func (t *ListTodosTool) Name() string {
	return "list_todos"
}

func (t *ListTodosTool) Description() string {
	return "列出待办任务，可以按用户、Agent、状态过滤"
}

func (t *ListTodosTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"created_by": map[string]interface{}{
				"type":        "string",
				"description": "按创建者用户名过滤",
			},
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "按Agent名称过滤",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "按状态过滤: pending, running, completed, failed, cancelled",
				"enum":        []string{"pending", "running", "completed", "failed", "cancelled"},
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "返回数量，默认20",
			},
		},
	}
}

func (t *ListTodosTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	createdBy, _ := params["created_by"].(string)
	agentName, _ := params["agent_name"].(string)
	statusStr, _ := params["status"].(string)

	limit := 20
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	query := t.db.Model(&todocore.Todo{})
	if createdBy != "" {
		query = query.Where("created_by = ?", createdBy)
	}
	if agentName != "" {
		query = query.Where("agent_name = ?", agentName)
	}
	if statusStr != "" {
		query = query.Where("status = ?", todocore.TodoStatus(statusStr))
	}

	var todos []todocore.Todo
	err := query.Order("priority DESC, created_at DESC").Limit(limit).Find(&todos).Error
	if err != nil {
		return "", fmt.Errorf("查询TODO失败: %w", err)
	}

	if len(todos) == 0 {
		if createdBy != "" {
			return fmt.Sprintf("用户「%s」暂无待办任务", createdBy), nil
		}
		return "暂无待办任务", nil
	}

	var out strings.Builder
	if createdBy != "" {
		out.WriteString(fmt.Sprintf("## 📋 用户「%s」的待办任务\n\n", createdBy))
	} else {
		out.WriteString("## 📋 待办任务列表\n\n")
	}

	out.WriteString("| ID | 标题 | 优先级 | 状态 | 调度类型 | 创建者 | 下次执行 |\n")
	out.WriteString("|----|------|--------|------|----------|--------|----------|\n")

	for _, todo := range todos {
		nextRun := ""
		if todo.NextRunAt != nil {
			nextRun = todo.NextRunAt.Format("01-02 15:04")
		}
		priorityIcon := map[todocore.TodoPriority]string{
			todocore.PriorityLow:    "🟢",
			todocore.PriorityNormal: "🟡",
			todocore.PriorityHigh:   "🟠",
			todocore.PriorityUrgent: "🔴",
		}[todo.Priority]

		statusIcon := map[todocore.TodoStatus]string{
			todocore.TodoStatusPending:   "⏳",
			todocore.TodoStatusRunning:   "🔄",
			todocore.TodoStatusCompleted: "✅",
			todocore.TodoStatusFailed:    "❌",
			todocore.TodoStatusCancelled: "⛔",
		}[todo.Status]

		titleDisplay := todo.Title
		if len(titleDisplay) > 30 {
			titleDisplay = titleDisplay[:27] + "..."
		}

		out.WriteString(fmt.Sprintf("| %s %s | %s | %s | %s | %s | %s | %s |\n",
			priorityIcon, todo.TodoID[:8],
			titleDisplay,
			priorityIcon,
			statusIcon,
			todo.ScheduleType,
			todo.CreatedBy,
			nextRun))
	}

	out.WriteString(fmt.Sprintf("\n共 %d 条任务", len(todos)))
	return out.String(), nil
}

// ==================== 3. 获取 TODO 详情工具 ====================

type GetTodoTool struct {
	db *gorm.DB
}

func NewGetTodoTool(db *gorm.DB) *GetTodoTool {
	return &GetTodoTool{db: db}
}

func (t *GetTodoTool) Name() string {
	return "get_todo"
}

func (t *GetTodoTool) Description() string {
	return "获取TODO详情，包括执行历史和结果"
}

func (t *GetTodoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"todo_id": map[string]interface{}{
				"type":        "string",
				"description": "TODO ID",
			},
			"include_logs": map[string]interface{}{
				"type":        "boolean",
				"description": "是否包含执行日志，默认true",
			},
		},
		"required": []string{"todo_id"},
	}
}

func (t *GetTodoTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	todoID, _ := params["todo_id"].(string)
	if todoID == "" {
		return "请提供TODO ID", nil
	}

	includeLogs := true
	if il, ok := params["include_logs"].(bool); ok {
		includeLogs = il
	}

	var todo todocore.Todo
	err := t.db.Where("todo_id = ?", todoID).First(&todo).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Sprintf("未找到TODO: %s", todoID), nil
		}
		return "", fmt.Errorf("获取TODO失败: %w", err)
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("## 📋 TODO详情: %s\n\n", todo.Title))
	out.WriteString(fmt.Sprintf("ID: %s\n", todo.TodoID))
	out.WriteString(fmt.Sprintf("描述: %s\n", todo.Description))
	out.WriteString(fmt.Sprintf("优先级: %s\n", todo.Priority))
	out.WriteString(fmt.Sprintf("状态: %s\n", todo.Status))
	out.WriteString(fmt.Sprintf("调度类型: %s\n", todo.ScheduleType))
	out.WriteString(fmt.Sprintf("创建者: %s\n", todo.CreatedBy))

	if todo.AgentName != "" {
		out.WriteString(fmt.Sprintf("指定Agent: %s\n", todo.AgentName))
	}
	if todo.CronExpr != "" {
		out.WriteString(fmt.Sprintf("Cron表达式: %s\n", todo.CronExpr))
	}
	if todo.IntervalSec > 0 {
		out.WriteString(fmt.Sprintf("间隔: %d秒\n", todo.IntervalSec))
	}

	out.WriteString(fmt.Sprintf("\n执行次数: %d\n", todo.RunCount))
	if todo.LastRunAt != nil {
		out.WriteString(fmt.Sprintf("最后执行: %s\n", todo.LastRunAt.Format("2006-01-02 15:04:05")))
	}
	if todo.NextRunAt != nil {
		out.WriteString(fmt.Sprintf("下次执行: %s\n", todo.NextRunAt.Format("2006-01-02 15:04:05")))
	}

	if todo.LastResult != "" {
		out.WriteString(fmt.Sprintf("\n### 最后结果\n%s\n", truncate(todo.LastResult, 500)))
	}
	if todo.LastError != "" {
		out.WriteString(fmt.Sprintf("\n### 最后错误\n%s\n", todo.LastError))
	}

	// 获取执行日志
	if includeLogs {
		var logs []todocore.TodoExecutionLog
		err = t.db.Where("todo_id = ?", todoID).Order("run_at DESC").Limit(5).Find(&logs).Error
		if err == nil && len(logs) > 0 {
			out.WriteString("\n### 执行历史\n\n")
			out.WriteString("| 时间 | 状态 | 耗时 |\n")
			out.WriteString("|------|------|------|\n")
			for _, log := range logs {
				statusIcon := "✅"
				if log.Status == "failed" {
					statusIcon = "❌"
				}
				out.WriteString(fmt.Sprintf("| %s | %s | %dms |\n",
					log.RunAt.Format("01-02 15:04:05"),
					statusIcon,
					log.DurationMs))
			}
		}
	}

	return out.String(), nil
}

// ==================== 4. 取消 TODO 工具 ====================

type CancelTodoTool struct {
	db *gorm.DB
}

func NewCancelTodoTool(db *gorm.DB) *CancelTodoTool {
	return &CancelTodoTool{db: db}
}

func (t *CancelTodoTool) Name() string {
	return "cancel_todo"
}

func (t *CancelTodoTool) Description() string {
	return "取消一个待办任务"
}

func (t *CancelTodoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"todo_id": map[string]interface{}{
				"type":        "string",
				"description": "要取消的TODO ID",
			},
		},
		"required": []string{"todo_id"},
	}
}

func (t *CancelTodoTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	todoID, _ := params["todo_id"].(string)
	if todoID == "" {
		return "请提供TODO ID", nil
	}

	result := t.db.Model(&todocore.Todo{}).
		Where("todo_id = ?", todoID).
		Updates(map[string]interface{}{
			"status":     todocore.TodoStatusCancelled,
			"updated_at": time.Now(),
		})

	if result.Error != nil {
		return "", fmt.Errorf("取消TODO失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Sprintf("未找到TODO: %s", todoID), nil
	}

	return fmt.Sprintf("✅ TODO %s 已取消", todoID), nil
}

// ==================== 5. 更新 TODO 工具 ====================

type UpdateTodoTool struct {
	db *gorm.DB
}

func NewUpdateTodoTool(db *gorm.DB) *UpdateTodoTool {
	return &UpdateTodoTool{db: db}
}

func (t *UpdateTodoTool) Name() string {
	return "update_todo"
}

func (t *UpdateTodoTool) Description() string {
	return "更新一个待办任务的信息，如标题、描述、优先级、调度配置等"
}

func (t *UpdateTodoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"todo_id": map[string]interface{}{
				"type":        "string",
				"description": "要更新的TODO ID",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "新标题",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "新描述",
			},
			"priority": map[string]interface{}{
				"type":        "string",
				"description": "新优先级: low, normal, high, urgent",
				"enum":        []string{"low", "normal", "high", "urgent"},
			},
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "指定执行的Agent名称",
			},
			"schedule_type": map[string]interface{}{
				"type":        "string",
				"description": "调度类型: once, cron, interval",
				"enum":        []string{"once", "cron", "interval"},
			},
			"run_at": map[string]interface{}{
				"type":        "string",
				"description": "执行时间（schedule_type=once时使用）",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "cron表达式（schedule_type=cron时使用）",
			},
			"interval_sec": map[string]interface{}{
				"type":        "integer",
				"description": "间隔秒数（schedule_type=interval时使用）",
			},
			"input_data": map[string]interface{}{
				"type":        "string",
				"description": "任务输入数据",
			},
			"max_retries": map[string]interface{}{
				"type":        "integer",
				"description": "失败重试次数",
			},
		},
		"required": []string{"todo_id"},
	}
}

func (t *UpdateTodoTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	todoID, _ := params["todo_id"].(string)
	if todoID == "" {
		return "请提供TODO ID", nil
	}

	// 先检查任务是否存在
	var todo todocore.Todo
	if err := t.db.Where("todo_id = ?", todoID).First(&todo).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Sprintf("未找到TODO: %s", todoID), nil
		}
		return "", fmt.Errorf("查询TODO失败: %w", err)
	}

	updates := make(map[string]interface{})
	updates["updated_at"] = time.Now()

	if title, ok := params["title"].(string); ok && title != "" {
		updates["title"] = title
	}
	if description, ok := params["description"].(string); ok && description != "" {
		updates["description"] = description
	}
	if priority, ok := params["priority"].(string); ok && priority != "" {
		updates["priority"] = todocore.TodoPriority(priority)
	}
	if agentName, ok := params["agent_name"].(string); ok {
		updates["agent_name"] = agentName
	}
	if scheduleType, ok := params["schedule_type"].(string); ok && scheduleType != "" {
		updates["schedule_type"] = scheduleType
	}
	if cronExpr, ok := params["cron_expr"].(string); ok {
		updates["cron_expr"] = cronExpr
	}
	if intervalSec, ok := params["interval_sec"].(float64); ok {
		updates["interval_sec"] = int(intervalSec)
	}
	if inputData, ok := params["input_data"].(string); ok {
		updates["input_data"] = inputData
	}
	if maxRetries, ok := params["max_retries"].(float64); ok {
		updates["max_retries"] = int(maxRetries)
	}

	// 处理时间参数
	if runAtStr, ok := params["run_at"].(string); ok && runAtStr != "" {
		runAt, err := parseTodoTime(runAtStr)
		if err == nil {
			updates["run_at"] = runAt
		}
	}

	// 如果调度类型改变，重新计算下次执行时间
	if _, hasScheduleType := updates["schedule_type"]; hasScheduleType {
		scheduleType := updates["schedule_type"].(string)
		now := time.Now()
		switch scheduleType {
		case "once":
			if runAt, ok := updates["run_at"].(*time.Time); ok && runAt != nil {
				updates["next_run_at"] = runAt
			} else {
				updates["next_run_at"] = &now
			}
		case "interval":
			intervalSec := 60
			if val, ok := updates["interval_sec"].(int); ok && val > 0 {
				intervalSec = val
			} else if todo.IntervalSec > 0 {
				intervalSec = todo.IntervalSec
			}
			nextRun := now.Add(time.Duration(intervalSec) * time.Second)
			updates["next_run_at"] = &nextRun
		case "cron":
			cronExpr := ""
			if val, ok := updates["cron_expr"].(string); ok && val != "" {
				cronExpr = val
			} else {
				cronExpr = todo.CronExpr
			}
			if cronExpr != "" {
				nextRun := calculateNextCronTimeStatic(cronExpr, now)
				updates["next_run_at"] = &nextRun
			}
		}
	}

	if len(updates) <= 1 { // 只有 updated_at
		return "没有需要更新的字段", nil
	}

	result := t.db.Model(&todocore.Todo{}).Where("todo_id = ?", todoID).Updates(updates)
	if result.Error != nil {
		return "", fmt.Errorf("更新TODO失败: %w", result.Error)
	}

	// 获取更新后的信息
	var updatedTodo todocore.Todo
	t.db.Where("todo_id = ?", todoID).First(&updatedTodo)

	var out strings.Builder
	out.WriteString(fmt.Sprintf("✅ TODO已更新\n\n"))
	out.WriteString(fmt.Sprintf("ID: %s\n", updatedTodo.TodoID))
	out.WriteString(fmt.Sprintf("标题: %s\n", updatedTodo.Title))
	out.WriteString(fmt.Sprintf("优先级: %s\n", updatedTodo.Priority))
	out.WriteString(fmt.Sprintf("状态: %s\n", updatedTodo.Status))
	out.WriteString(fmt.Sprintf("调度类型: %s\n", updatedTodo.ScheduleType))
	if updatedTodo.NextRunAt != nil {
		out.WriteString(fmt.Sprintf("下次执行: %s\n", updatedTodo.NextRunAt.Format("2006-01-02 15:04:05")))
	}

	return out.String(), nil
}

func parseTodoTime(value string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, format := range formats {
		if parsed, err := time.ParseInLocation(format, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", value)
}

func calculateNextCronTimeStatic(expression string, after time.Time) time.Time {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.DowOptional)
	schedule, err := parser.Parse(expression)
	if err != nil {
		log.Printf("解析cron表达式失败 %s: %v，回退到明天此时", expression, err)
		return after.Add(24 * time.Hour)
	}
	next := schedule.Next(after)
	if !next.After(after) {
		log.Printf("cron Next() 返回非未来时间 %v (after=%v)，强制 +24h", next, after)
		return after.Add(24 * time.Hour)
	}
	return next
}
