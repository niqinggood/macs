package skills

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"mac/internal/toolcontract"
)

const DefaultSkillMaxInlineChars = 48000

// SkillsRuntime is the application boundary needed by skill catalog tools.
type SkillsRuntime interface {
	BuildSkillsSummary() string
	SearchSkills(keywords []string, limit int, includeContent bool) string
	ExecuteSkill(ctx context.Context, skillName string, params map[string]interface{}) (string, error)
	RecordInvokedSkillForContext(ctx context.Context, name, content string)
	SkillExecutionPolicy(name string) (bool, toolcontract.SandboxCapabilities, bool)
}

// ListSkillsTool 列出技能工具
type ListSkillsTool struct {
	skillsLoader SkillsRuntime
}

func NewListSkillsTool(loader SkillsRuntime) *ListSkillsTool {
	return &ListSkillsTool{skillsLoader: loader}
}

func (t *ListSkillsTool) Name() string { return "list_skills" }

// Search results are catalog content. Skill descriptions can legitimately
// discuss failures, exceptions, or blocked states; those words must not turn a
// successful catalog lookup into a failed tool execution.
func (t *ListSkillsTool) ResultIsContentPayload() bool { return true }

func (t *ListSkillsTool) Description() string {
	return "列出所有可用技能及其状态。或根据关键词检索可用技能。支持从技能名称、描述、内容中匹配，返回匹配到的技能列表及其摘要信息。"
}

func (t *ListSkillsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"keywords": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "检索关键词列表，多个关键词之间是OR关系。不传则返回所有技能列表",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "返回结果数量限制，默认10，最大50（仅在传入keywords时生效）",
				"default":     2,
			},
			"include_content": map[string]interface{}{
				"type":        "boolean",
				"description": "是否包含技能的内容片段，默认false（仅在传入keywords时生效）",
				"default":     false,
			},
		},
		// 不设置 required，让 keywords 变为可选
	}
}

func (t *ListSkillsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// 兼容旧版本：无参数或空参数时，返回完整摘要
	if params == nil || len(params) == 0 {
		return t.skillsLoader.BuildSkillsSummary(), nil
	}

	// 检查是否有 keywords 参数
	keywordsRaw, hasKeywords := params["keywords"]
	if !hasKeywords {
		// 没有 keywords 参数，返回完整摘要（兼容旧调用）
		return t.skillsLoader.BuildSkillsSummary(), nil
	}

	// 解析 keywords
	var keywords []string
	switch v := keywordsRaw.(type) {
	case []interface{}:
		for _, kw := range v {
			if str, ok := kw.(string); ok && str != "" {
				keywords = append(keywords, strings.ToLower(str))
			}
		}
	case []string:
		for _, kw := range v {
			if kw != "" {
				keywords = append(keywords, strings.ToLower(kw))
			}
		}
	case string:
		// 支持单个字符串
		if v != "" {
			keywords = []string{strings.ToLower(v)}
		}
	}

	// 没有有效关键词，返回完整摘要
	if len(keywords) == 0 {
		return t.skillsLoader.BuildSkillsSummary(), nil
	}

	// 获取 limit（可选）
	limit := 10
	if limitRaw, ok := params["limit"]; ok {
		switch v := limitRaw.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// 是否包含内容片段
	includeContent := false
	if v, ok := params["include_content"]; ok {
		if b, ok := v.(bool); ok {
			includeContent = b
		}
	}
	// 执行检索
	return t.skillsLoader.SearchSkills(keywords, limit, includeContent), nil
}

// func (t *ListSkillsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
// 	return t.skillsLoader.BuildSkillsSummary(), nil
// }

// UseSkillTool 使用技能工具
type UseSkillTool struct {
	skillsLoader SkillsRuntime
}

func NewUseSkillTool(loader SkillsRuntime) *UseSkillTool {
	return &UseSkillTool{skillsLoader: loader}
}

func (t *UseSkillTool) Name() string { return "use_skill" }

func (t *UseSkillTool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	return toolcontract.SandboxCapabilities{Process: true, Network: true}
}

func (t *UseSkillTool) IsReadOnlyForCall(args map[string]interface{}) bool {
	if t == nil || t.skillsLoader == nil {
		return false
	}
	skillName, _ := args["skill_name"].(string)
	readOnly, _, found := t.skillsLoader.SkillExecutionPolicy(strings.TrimSpace(skillName))
	return found && readOnly
}

func (t *UseSkillTool) SandboxCapabilitiesForCall(args map[string]interface{}) toolcontract.SandboxCapabilities {
	if t == nil || t.skillsLoader == nil {
		return toolcontract.SandboxCapabilities{}
	}
	skillName, _ := args["skill_name"].(string)
	_, capabilities, found := t.skillsLoader.SkillExecutionPolicy(strings.TrimSpace(skillName))
	if !found {
		return toolcontract.SandboxCapabilities{}
	}
	return capabilities
}

// MaxResultSize keeps the bounded instruction budget chosen by the tool-result
// normalizer intact through the agent's legacy byte-based offload pass. Without
// this override, use_skill fell back to the generic 8K limit and could clip the
// exact scaffold and QA commands from otherwise valid skill activations.
func (t *UseSkillTool) MaxResultSize() int {
	return utf8.UTFMax * DefaultSkillMaxInlineChars
}

func (t *UseSkillTool) Description() string {
	return "激活一个已命名的技能。顶层 skill_name 始终必填，值必须是 list_skills 返回或任务明确给出的精确技能标识；技能专属的上下文和选项只能放入顶层 parameters 对象，不得用 context、mode 或其他自造字段替代或省略 skill_name。"
}

func (t *UseSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{
				"type":        "string",
				"description": "必填的顶层字段：要激活的精确技能标识（例如 example-skill），不得替换为显示名称、模式或意图",
			},
			"parameters": map[string]interface{}{
				"type":        "object",
				"description": "可选的顶层对象：只存放该技能需要的上下文和选项；不能在其中省略顶层 skill_name",
			},
		},
		"required": []string{"skill_name"},
	}
}

func (t *UseSkillTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	skillName, _ := params["skill_name"].(string)

	// 提取参数
	skillParams := make(map[string]interface{})
	if p, ok := params["parameters"].(map[string]interface{}); ok {
		skillParams = p
	}

	// 执行技能
	result, err := t.skillsLoader.ExecuteSkill(ctx, skillName, skillParams)
	if err != nil {
		t.skillsLoader.RecordInvokedSkillForContext(ctx, skillName, "")
		return fmt.Sprintf("技能执行失败: %v", err), nil
	}
	t.skillsLoader.RecordInvokedSkillForContext(ctx, skillName, result)

	return result, nil
}
