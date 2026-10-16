package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRelationshipMentorIsSkillOnly(t *testing.T) {
	for _, employee := range defaultAIEmployees() {
		if employee.ID == "relationship-mentor" {
			t.Fatalf("relationship-mentor must remain a Skill, not a default AI employee: %#v", employee)
		}
	}
}

func TestEmbeddedRelationshipMentorIsIndexedRoutableAndComprehensive(t *testing.T) {
	const root = "builtin/relationship-mentor"
	for _, relative := range []string{"SKILL.md", "agents/openai.yaml"} {
		if _, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded relationship-mentor missing %s: %v", relative, err)
		}
	}

	indexData, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Desc   string   `json:"desc"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatal(err)
	}
	category, ok := index["relationships-wellbeing"]
	if !ok || !containsString(category.Skills, "relationship-mentor") {
		t.Fatalf("relationships-wellbeing index missing relationship-mentor: %#v", category)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("relationship-mentor")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || skill.RequiresRuntime {
		t.Fatalf("relationship-mentor should be embedded and documentation-only: %#v", skill)
	}
	if skill.Metadata.Version != "1.0.2" || skill.Metadata.Author != "OpenKitty" {
		t.Fatalf("unexpected relationship skill metadata: %#v", skill.Metadata)
	}
	if skill.Interface.DisplayName != "情感导师" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$relationship-mentor") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("relationship mentor interface metadata was not loaded: %#v", skill.Interface)
	}
	aliasSkill, err := loader.GetSkill("情感导师")
	if err != nil || aliasSkill != skill {
		t.Fatalf("Chinese alias should resolve to canonical skill: skill=%p alias=%p err=%v", skill, aliasSkill, err)
	}
	activation, err := loader.ExecuteSkill(context.Background(), "relationship-mentor", nil)
	if err != nil || strings.TrimSpace(activation) == "" {
		t.Fatalf("relationship skill should activate: err=%v output=%q", err, activation)
	}

	for _, required := range []string{
		"倾听支持", "聊天记录分析", "我该怎么回", "争吵、冷战", "边界、控制", "信任受损",
		"异地", "婚姻、同居", "性边界", "分手", "复合", "是否继续", "关系质量评估",
		"自伤、自杀", "暴力、胁迫", "不自动脱敏", "不默认联网", "不发问卷",
	} {
		if !strings.Contains(skill.Raw, required) {
			t.Fatalf("relationship mentor instructions missing %q", required)
		}
	}
	for _, forbidden := range []string{"自动脱敏用户材料", "断联是唯一", "必须原谅", "保证复合"} {
		if strings.Contains(skill.Raw, forbidden) {
			t.Fatalf("relationship mentor contains rigid or misleading rule %q", forbidden)
		}
	}

	queries := []string{
		"我和对象总是为同一件事吵架，怎么沟通才能不互相指责",
		"帮我分析这段暧昧期聊天，对方到底释放了哪些信号",
		"他说想冷静一段时间，我应该怎么回复才真诚又有边界",
		"失恋后一直想复合，请帮我判断这段关系还值不值得继续",
		"结婚前因为钱和双方父母的问题反复冲突怎么办",
	}
	for _, query := range queries {
		top := loader.GetTopSkillsForQuery(query, 8)
		if !containsString(top, "relationship-mentor") {
			t.Fatalf("relationship query %q did not discover relationship-mentor: %v", query, top)
		}
	}
	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "relationship-mentor") {
		t.Fatalf("builtin index prompt omitted relationship-mentor: %s", prompt)
	}

	uiData, err := fs.ReadFile(builtinSkillsFS, root+"/agents/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ui struct {
		Interface struct {
			DisplayName      string `yaml:"display_name"`
			ShortDescription string `yaml:"short_description"`
			DefaultPrompt    string `yaml:"default_prompt"`
		} `yaml:"interface"`
	}
	if err := yaml.Unmarshal(uiData, &ui); err != nil {
		t.Fatal(err)
	}
	if ui.Interface.DisplayName != "情感导师" ||
		!strings.Contains(ui.Interface.DefaultPrompt, "$relationship-mentor") ||
		strings.TrimSpace(ui.Interface.ShortDescription) == "" {
		t.Fatalf("invalid relationship mentor UI metadata: %#v", ui.Interface)
	}
}
