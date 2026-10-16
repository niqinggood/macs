package agents

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"mac/internal/configvalue"
)

type EmployeeSummary struct {
	ID           string
	Name         string
	Role         string
	Kind         string
	InternalOnly bool
}

// CollaborationRuntime isolates employee discovery and pipeline execution
// from the reusable collaboration tools.
type CollaborationRuntime interface {
	Available() bool
	HasExecutionPrincipal(context.Context) bool
	GetEmployee(string) (EmployeeSummary, bool)
	ListEmployees() []EmployeeSummary
	Dispatch(context.Context, string, string, string) (string, error)
	WaitForCompletion(context.Context, string, time.Duration) string
}

type AECollaborationTool struct {
	runtime CollaborationRuntime
	logger  *log.Logger
}

func NewAECollaborationTool(runtime CollaborationRuntime, logger *log.Logger) *AECollaborationTool {
	return &AECollaborationTool{runtime: runtime, logger: logger}
}

func (t *AECollaborationTool) Name() string { return "delegate_to" }

func (t *AECollaborationTool) Description() string {
	return "将子任务委托给另一个 AI 员工处理。当当前问题超出你的职责范围时，委托给专业对口的同事，并返回同事的处理结果。"
}

func (t *AECollaborationTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ae_id":   map[string]interface{}{"type": "string", "description": "目标 AI 员工 ID（例如 cs-after-sales、cs-pre-sales）"},
			"task":    map[string]interface{}{"type": "string", "description": "委托的子任务描述，需要包含足够上下文"},
			"timeout": map[string]interface{}{"type": "integer", "description": "超时秒数，默认 60"},
		},
		"required": []string{"ae_id", "task"},
	}
}

func (t *AECollaborationTool) IsReadOnly() bool   { return false }
func (t *AECollaborationTool) MaxResultSize() int { return 3000 }

func (t *AECollaborationTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	employeeID, _ := params["ae_id"].(string)
	task, _ := params["task"].(string)
	timeoutSeconds := configvalue.Int(params, "timeout", 60)
	if t == nil || t.runtime == nil || !t.runtime.HasExecutionPrincipal(ctx) {
		return "", fmt.Errorf("delegate_to is only available inside an AI employee explicitly activated by @mention")
	}
	if !t.runtime.Available() {
		return "DE 协作系统未初始化", nil
	}

	employee, ok := t.runtime.GetEmployee(employeeID)
	if !ok || employee.InternalOnly {
		available := make([]string, 0)
		for _, candidate := range t.runtime.ListEmployees() {
			if candidate.InternalOnly {
				continue
			}
			available = append(available, fmt.Sprintf("%s(%s)", candidate.ID, candidate.Name))
		}
		return fmt.Sprintf("AI 员工 '%s' 不存在。可用的员工: %s", employeeID, strings.Join(available, ", ")), nil
	}

	if t.logger != nil {
		t.logger.Printf("[Collab] Delegating to %s: %s", employee.Name, truncateRunes(task, 80))
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sessionID := fmt.Sprintf("collab_%d", time.Now().UnixNano())
	executionID, err := t.runtime.Dispatch(subCtx, employeeID, task, sessionID)
	if err != nil {
		return fmt.Sprintf("委托给 %s 失败: %v", employee.Name, err), nil
	}
	result := t.runtime.WaitForCompletion(subCtx, executionID, timeout)
	if result == "" {
		return fmt.Sprintf("委托给 %s 超时（%d 秒），请稍后重试或转人工处理", employee.Name, timeoutSeconds), nil
	}
	if t.logger != nil {
		t.logger.Printf("[Collab] %s completed, result: %d chars", employee.Name, len(result))
	}
	return fmt.Sprintf("## %s 的处理结果\n\n%s", employee.Name, result), nil
}

type DEInfoTool struct {
	runtime CollaborationRuntime
}

func NewDEInfoTool(runtime CollaborationRuntime) *DEInfoTool {
	return &DEInfoTool{runtime: runtime}
}

func (t *DEInfoTool) Name() string { return "list_colleagues" }

func (t *DEInfoTool) Description() string {
	return "查看所有可用的 AI 员工，返回每个员工的 ID、名称、职责和类型。"
}

func (t *DEInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (t *DEInfoTool) IsReadOnly() bool { return true }

func (t *DEInfoTool) Execute(context.Context, map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "暂无可用 AI 员工", nil
	}
	employees := t.runtime.ListEmployees()
	visible := make([]EmployeeSummary, 0, len(employees))
	for _, employee := range employees {
		if !employee.InternalOnly {
			visible = append(visible, employee)
		}
	}
	if len(visible) == 0 {
		return "暂无可用 AI 员工", nil
	}

	var output strings.Builder
	output.WriteString("## 可委托的 AI 员工\n\n")
	output.WriteString("| ID | 名称 | 职责 | 类型 |\n")
	output.WriteString("|----|------|------|------|\n")
	for _, employee := range visible {
		icon := "📋"
		if employee.Kind == "router" {
			icon = "🔀"
		}
		output.WriteString(fmt.Sprintf("| %s | %s | %s | %s %s |\n", employee.ID, employee.Name, employee.Role, icon, employee.Kind))
	}
	output.WriteString(fmt.Sprintf("\n共 %d 位 AI 员工。使用 delegate_to 工具将任务委托给指定员工。", len(visible)))
	return output.String(), nil
}
