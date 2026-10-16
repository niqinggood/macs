package content

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
)

func getString(values map[string]interface{}, key, fallback string) string {
	return configvalue.String(values, key, fallback)
}

func getInt(values map[string]interface{}, key string, fallback int) int {
	return configvalue.Int(values, key, fallback)
}

// PromptTemplate is a reusable prompt for common enterprise tasks.
type PromptTemplate struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"` // "report", "analysis", "coding", "email", "meeting", "custom"
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Variables   []string `json:"variables,omitempty"` // placeholders like {{name}}, {{date}}
	Tags        []string `json:"tags,omitempty"`
	UsageCount  int      `json:"usage_count"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// TemplateManager manages prompt templates.
type TemplateManager struct {
	mu         sync.RWMutex
	templates  map[string]*PromptTemplate
	storageDir string
}

func NewTemplateManager(storageDir string) *TemplateManager {
	os.MkdirAll(storageDir, 0755)
	tm := &TemplateManager{
		templates:  make(map[string]*PromptTemplate),
		storageDir: storageDir,
	}
	tm.loadBuiltinTemplates()
	tm.loadFromDisk()
	return tm
}

func (tm *TemplateManager) loadBuiltinTemplates() {
	builtins := []PromptTemplate{
		{
			Name: "weekly-report", Category: "report",
			Description: "Generate a weekly work report",
			Prompt:      "请根据以下要点生成本周工作报告：\n1. 本周完成的工作\n2. 遇到的问题和解决方案\n3. 下周计划\n4. 需要协调的事项\n\n背景信息：{{context}}",
			Variables:   []string{"context"},
			Tags:        []string{"周报", "汇报"},
		},
		{
			Name: "code-review", Category: "coding",
			Description: "Review code for common issues",
			Prompt:      "Review the following code for:\n1. Security vulnerabilities\n2. Performance issues\n3. Error handling gaps\n4. Code style violations\n5. Test coverage suggestions\n\nCode:\n```\n{{code}}\n```",
			Variables:   []string{"code"},
			Tags:        []string{"代码审查", "安全"},
		},
		{
			Name: "meeting-minutes", Category: "meeting",
			Description: "Generate meeting minutes from notes",
			Prompt:      "根据以下会议记录生成会议纪要：\n\n## 会议主题\n{{title}}\n\n## 参会人员\n{{attendees}}\n\n## 讨论内容\n{{notes}}\n\n请输出：\n1. 会议概要\n2. 决议事项\n3. 待办事项（含负责人和截止日期）\n4. 下次会议时间",
			Variables:   []string{"title", "attendees", "notes"},
			Tags:        []string{"会议", "纪要"},
		},
		{
			Name: "email-reply", Category: "email",
			Description: "Draft a professional email reply",
			Prompt:      "请根据以下邮件内容起草回复：\n\n原始邮件：\n{{original_email}}\n\n回复要点：\n{{key_points}}\n\n要求：\n- 语气：{{tone}}\n- 语言：{{language}}",
			Variables:   []string{"original_email", "key_points", "tone", "language"},
			Tags:        []string{"邮件", "回复"},
		},
		{
			Name: "data-analysis", Category: "analysis",
			Description: "Analyze data and generate insights",
			Prompt:      "请分析以下数据：\n\n数据描述：{{data_description}}\n数据样本：\n```\n{{data_sample}}\n```\n\n请提供：\n1. 数据摘要统计\n2. 关键趋势和模式\n3. 异常值检测\n4. 建议的下一步行动",
			Variables:   []string{"data_description", "data_sample"},
			Tags:        []string{"数据分析", "统计"},
		},
		{
			Name: "bug-report", Category: "coding",
			Description: "Analyze a bug and suggest fixes",
			Prompt:      "Analyze the following bug:\n\n## Bug Description\n{{description}}\n\n## Steps to Reproduce\n{{steps}}\n\n## Error Message\n```\n{{error}}\n```\n\n## Environment\n{{environment}}\n\nProvide:\n1. Root cause analysis\n2. Proposed fix\n3. Prevention measures",
			Variables:   []string{"description", "steps", "error", "environment"},
			Tags:        []string{"调试", "bug"},
		},
	}

	for i := range builtins {
		builtins[i].CreatedAt = time.Now().Format("2006-01-02")
		builtins[i].UpdatedAt = builtins[i].CreatedAt
		tm.templates[builtins[i].Name] = &builtins[i]
	}
}

func (tm *TemplateManager) loadFromDisk() {
	entries, err := os.ReadDir(tm.storageDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(tm.storageDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var tmpl PromptTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			continue
		}
		if tmpl.Name != "" {
			tm.templates[tmpl.Name] = &tmpl
		}
	}
}

func (tm *TemplateManager) Save(tmpl *PromptTemplate) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tmpl.UpdatedAt = time.Now().Format("2006-01-02")
	tm.templates[tmpl.Name] = tmpl

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(tm.storageDir, tmpl.Name+".json")
	return os.WriteFile(path, data, 0644)
}

func (tm *TemplateManager) Get(name string) *PromptTemplate {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.templates[name]
}

func (tm *TemplateManager) Delete(name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.templates, name)
	path := filepath.Join(tm.storageDir, name+".json")
	return os.Remove(path)
}

func (tm *TemplateManager) List(category string) []*PromptTemplate {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	var list []*PromptTemplate
	for _, t := range tm.templates {
		if category == "" || t.Category == category {
			list = append(list, t)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UsageCount > list[j].UsageCount
	})
	return list
}

func (tm *TemplateManager) Use(name string) *PromptTemplate {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if t, ok := tm.templates[name]; ok {
		t.UsageCount++
		return t
	}
	return nil
}

func (tm *TemplateManager) FillTemplate(name string, vars map[string]string) (string, error) {
	tmpl := tm.Get(name)
	if tmpl == nil {
		return "", fmt.Errorf("template '%s' not found", name)
	}
	result := tmpl.Prompt
	for key, val := range vars {
		placeholder := "{{" + key + "}}"
		result = strings.ReplaceAll(result, placeholder, val)
	}
	tm.Use(name)
	return result, nil
}

// ==================== prompt_template tool ====================

type PromptTemplateTool struct {
	Manager *TemplateManager
}

func (t *PromptTemplateTool) Name() string { return "prompt_template" }

func (t *PromptTemplateTool) Description() string {
	return "Manage and use prompt templates for common enterprise tasks. " +
		"Categories: report, analysis, coding, email, meeting, custom. " +
		"Use templates to quickly generate consistent prompts for recurring tasks."
}

func (t *PromptTemplateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'list' (all templates), 'use' (fill and return template), 'save' (create/update), 'delete', 'categories'",
				"enum":        []string{"list", "use", "save", "delete", "categories"},
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Template name",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Filter by category (for list) or set category (for save)",
			},
			"variables": map[string]interface{}{
				"type":        "string",
				"description": "JSON object of variable values for filling template (for 'use' action). Example: {\"context\":\"project X\",\"code\":\"...\"}",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Template prompt text with {{variable}} placeholders (for 'save' action)",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Template description (for 'save' action)",
			},
			"tags": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated tags (for 'save' action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *PromptTemplateTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := getString(params, "action", "list")

	if t.Manager == nil {
		return "Template manager not available", nil
	}

	switch action {
	case "list":
		category := getString(params, "category", "")
		templates := t.Manager.List(category)
		if len(templates) == 0 {
			return "No templates found. Use 'save' to create one.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Templates (%d):\n", len(templates)))
		for _, tmpl := range templates {
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s (used %dx)\n", tmpl.Name, tmpl.Category, tmpl.Description, tmpl.UsageCount))
		}
		return sb.String(), nil

	case "categories":
		cats := map[string]int{}
		for _, tmpl := range t.Manager.List("") {
			cats[tmpl.Category]++
		}
		var sb strings.Builder
		sb.WriteString("Categories:\n")
		for cat, count := range cats {
			sb.WriteString(fmt.Sprintf("- %s: %d templates\n", cat, count))
		}
		return sb.String(), nil

	case "use":
		name := getString(params, "name", "")
		if name == "" {
			return "Error: name is required", nil
		}
		varsJSON := getString(params, "variables", "{}")
		var vars map[string]string
		json.Unmarshal([]byte(varsJSON), &vars)
		result, err := t.Manager.FillTemplate(name, vars)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Filled template '%s':\n\n%s", name, result), nil

	case "save":
		name := getString(params, "name", "")
		prompt := getString(params, "prompt", "")
		if name == "" || prompt == "" {
			return "Error: name and prompt are required", nil
		}
		tmpl := &PromptTemplate{
			Name:        name,
			Category:    getString(params, "category", "custom"),
			Description: getString(params, "description", ""),
			Prompt:      prompt,
			Tags:        strings.Split(getString(params, "tags", ""), ","),
		}
		if err := t.Manager.Save(tmpl); err != nil {
			return fmt.Sprintf("Error saving: %v", err), nil
		}
		return fmt.Sprintf("Template '%s' saved", name), nil

	case "delete":
		name := getString(params, "name", "")
		if name == "" {
			return "Error: name is required", nil
		}
		if err := t.Manager.Delete(name); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Template '%s' deleted", name), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}
