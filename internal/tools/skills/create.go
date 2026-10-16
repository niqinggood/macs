package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CreateSkillTool creates a reusable SKILL.md from task experience.
type CreateSkillTool struct {
	skillsDir string
}

func NewCreateSkillTool(skillsDir string) *CreateSkillTool {
	return &CreateSkillTool{skillsDir: skillsDir}
}

func (tool *CreateSkillTool) Name() string { return "create_skill" }

func (tool *CreateSkillTool) Description() string {
	return "Create a new reusable skill from task experience. Use this after completing a complex, repeatable task to save the pattern for future use. Skills are SKILL.md files with YAML frontmatter."
}

func (tool *CreateSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type": "string", "description": "Skill directory name (kebab-case, e.g. 'stock-analyzer')",
			},
			"description": map[string]interface{}{
				"type": "string", "description": "One-line description of what this skill does and when to use it",
			},
			"read_when": map[string]interface{}{
				"type": "string", "description": "Comma-separated trigger conditions in Chinese and English",
			},
			"content": map[string]interface{}{
				"type": "string", "description": "Full SKILL.md body content without YAML frontmatter",
			},
			"tags": map[string]interface{}{
				"type": "string", "description": "Comma-separated tags for discovery (optional)",
			},
		},
		"required": []string{"name", "description", "read_when", "content"},
	}
}

func (tool *CreateSkillTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	description, _ := params["description"].(string)
	readWhen, _ := params["read_when"].(string)
	content, _ := params["content"].(string)
	tags, _ := params["tags"].(string)
	if name == "" || description == "" || readWhen == "" || content == "" {
		return "", fmt.Errorf("name, description, read_when, and content are all required")
	}
	name = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
	if name == "." || name == ".." || filepath.Base(name) != name {
		return "", fmt.Errorf("name must be a single skill directory name")
	}
	var readWhenYAML strings.Builder
	for _, item := range strings.Split(readWhen, ",") {
		if item = strings.TrimSpace(item); item != "" {
			readWhenYAML.WriteString(fmt.Sprintf("  - %s\n", item))
		}
	}
	var tagsYAML strings.Builder
	for _, tag := range strings.Split(tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tagsYAML.WriteString(fmt.Sprintf("  - %s\n", tag))
		}
	}
	frontmatter := fmt.Sprintf("---\nname: %s\ndescription: %s\nread_when:\n%s", name, description, readWhenYAML.String())
	if tagsYAML.Len() > 0 {
		frontmatter += fmt.Sprintf("tags:\n%s", tagsYAML.String())
	}
	skillContent := frontmatter + "---\n\n" + content
	skillDir := filepath.Join(tool.skillsDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return "", fmt.Errorf("写入技能文件失败: %w", err)
	}
	metadata := fmt.Sprintf(`{"created_at":"%s","version":1,"total_uses":0,"last_used":"","last_improved":""}`, time.Now().Format("2006-01-02 15:04:05"))
	if err := os.WriteFile(filepath.Join(skillDir, "_meta.json"), []byte(metadata), 0644); err != nil {
		return "", fmt.Errorf("写入技能元数据失败: %w", err)
	}
	return fmt.Sprintf("✅ Skill '%s' created at %s\n\nSkill will be available on next restart or skill reload.", name, skillPath), nil
}
