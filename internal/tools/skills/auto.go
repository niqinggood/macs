package skills

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/skillpath"
)

// ====================== Auto Skill Creation ======================

// SkillCandidate represents a conversation segment that might become a skill.
type SkillCandidate struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Keywords    []string  `json:"keywords"`
	Content     string    `json:"content"`
	SessionID   string    `json:"session_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// ConversationMessage contains only the fields used to discover reusable
// skill candidates.
type ConversationMessage struct {
	Role      string
	Content   string
	SessionID string
	CreatedAt time.Time
}

// AutoSkillTool lets the agent save reusable knowledge as skills.
type AutoSkillTool struct {
	skillsDir  string
	fileLogger *log.Logger
}

func NewAutoSkillTool(skillsDir string, fileLogger *log.Logger) *AutoSkillTool {
	os.MkdirAll(skillsDir, 0755)
	return &AutoSkillTool{skillsDir: skillsDir, fileLogger: fileLogger}
}

func (t *AutoSkillTool) Name() string { return "save_skill" }
func (t *AutoSkillTool) Description() string {
	return "Save reusable knowledge or workflow as a skill for future use. Use this when you discover a pattern, solution, or workflow worth remembering across sessions."
}

func (t *AutoSkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title":       map[string]interface{}{"type": "string", "description": "Skill title, e.g. 'Go Error Handling Patterns'"},
			"description": map[string]interface{}{"type": "string", "description": "One-line description of what this skill does"},
			"category":    map[string]interface{}{"type": "string", "description": "Category: coding, analysis, writing, devops, research"},
			"content":     map[string]interface{}{"type": "string", "description": "The skill content in markdown. Include instructions, examples, and best practices."},
		},
		"required": []string{"title", "content"},
	}
}

func (t *AutoSkillTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	title, _ := params["title"].(string)
	desc, _ := params["description"].(string)
	category, _ := params["category"].(string)
	content, _ := params["content"].(string)

	if title == "" || content == "" {
		return "", fmt.Errorf("title and content are required")
	}

	dirName := skillpath.SanitizeName(title)
	skillDir := filepath.Join(t.skillsDir, dirName)
	os.MkdirAll(skillDir, 0755)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	if desc != "" {
		sb.WriteString(fmt.Sprintf("**Description**: %s\n\n", desc))
	}
	if category != "" {
		sb.WriteString(fmt.Sprintf("**Category**: %s\n\n", category))
	}
	sb.WriteString("## Instructions\n\n")
	sb.WriteString(content)
	sb.WriteString(fmt.Sprintf("\n\n---\n*Auto-generated skill · %s*\n", time.Now().Format("2006-01-02 15:04")))

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}

	t.fileLogger.Printf("[AutoSkill] Created: %s (%s)", title, skillPath)
	return fmt.Sprintf("Skill saved: %s\nPath: %s\nCategory: %s", title, skillPath, category), nil
}

// ExtractSkillCandidates analyzes a conversation for potential skills.
func ExtractSkillCandidates(messages []ConversationMessage) []SkillCandidate {
	var candidates []SkillCandidate
	// Group messages by session and look for patterns:
	// 1. Repeated tool call sequences → workflow skill
	// 2. Long successful completions → knowledge skill
	// 3. User corrections followed by success → troubleshooting skill
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.Content) > 500 {
			// Check if this looks like a reusable pattern
			lower := strings.ToLower(msg.Content)
			if strings.Contains(lower, "step") || strings.Contains(lower, "pattern") ||
				strings.Contains(lower, "best practice") || strings.Contains(lower, "workflow") {
				candidates = append(candidates, SkillCandidate{
					Title:     fmt.Sprintf("Session %s Pattern", msg.SessionID[:min(8, len(msg.SessionID))]),
					Content:   msg.Content,
					SessionID: msg.SessionID,
					CreatedAt: msg.CreatedAt,
				})
			}
		}
	}
	return candidates
}
