package skills

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	rootassets "mac"

	"gopkg.in/yaml.v3"
)

func TestEmbeddedLuckRichSkillIsIndexedAndRoutable(t *testing.T) {
	const root = "builtin/luck-rich"
	for _, relative := range []string{
		"SKILL.md",
		"agents/openai.yaml",
		"references/calendar-and-charting.md",
		"references/bazi-foundations.md",
		"references/bazi-analysis.md",
		"references/advanced-bazi-and-audit.md",
		"references/cycles-and-compatibility.md",
		"references/zodiac-and-astrology.md",
		"references/western-astrology-foundations.md",
		"references/case-library.md",
		"references/accuracy-and-calibration.md",
	} {
		if _, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded Luck Rich skill missing %s: %v", relative, err)
		}
	}

	indexData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, "builtin/OPENKITTY_INDEX.json")
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
	category, ok := index["culture-entertainment"]
	if !ok {
		t.Fatal("missing culture-entertainment category")
	}
	if !containsString(category.Skills, "luck-rich") {
		t.Fatalf("culture-entertainment index missing luck-rich: %v", category.Skills)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("luck-rich")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || skill.RequiresRuntime {
		t.Fatalf("Luck Rich should be an embedded documentation-only skill: %#v", skill)
	}
	if skill.Metadata.Version != "2.2.0" || skill.Metadata.Author != "OpenKitty" {
		t.Fatalf("nested standard metadata was not loaded: %#v", skill.Metadata)
	}
	if skill.Interface.DisplayName != "命理校准与解读" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$luck-rich") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("Luck Rich interface metadata was not loaded: %#v", skill.Interface)
	}
	for _, tag := range []string{"星座", "属相", "八字"} {
		if !containsString(skill.Metadata.Tags, tag) {
			t.Fatalf("Luck Rich metadata missing routing tag %q: %v", tag, skill.Metadata.Tags)
		}
	}
	aliasSkill, err := loader.GetSkill("算命")
	if err != nil || aliasSkill != skill {
		t.Fatalf("Chinese alias should resolve to the canonical skill: skill=%p alias=%p err=%v", skill, aliasSkill, err)
	}
	for _, alias := range []string{"luck", "fortune-telling", "ai-fortune-telling"} {
		legacySkill, err := loader.GetSkill(alias)
		if err != nil || legacySkill != skill {
			t.Fatalf("alias %q should resolve to the canonical skill: skill=%p alias=%p err=%v", alias, skill, legacySkill, err)
		}
	}
	activation, err := loader.ExecuteSkill(context.Background(), "luck-rich", nil)
	if err != nil || strings.TrimSpace(activation) == "" {
		t.Fatalf("documentation-only builtin skill should activate: err=%v output=%q", err, activation)
	}

	queries := []string{
		"我是天蝎座，帮我看看星座性格和恋爱倾向",
		"我属龙，想看一下属相性格和生肖配对",
		"请按我的四柱八字分析五行、十神和大运",
		"帮我做一份星座、属相、八字三合一的AI算命",
		"上次八字算得不准，请核对出生时间和排盘口径后重新校准",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			top := loader.GetTopSkillsForQuery(query, 8)
			if len(top) == 0 || top[0] != "luck-rich" {
				t.Fatalf("query %q should rank luck-rich first, got %v", query, top)
			}
		})
	}

	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "luck-rich") {
		t.Fatalf("builtin index prompt omitted luck-rich: %s", prompt)
	}

	uiData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/agents/openai.yaml")
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
	for _, required := range []string{
		"准确度与校准协议",
		"固定输出格式",
		"盘面事实 → 传统含义 → 现实可能 → 反向条件",
		"命中、未命中",
		"不使用“你外表坚强内心敏感",
	} {
		if !strings.Contains(skill.Raw, required) {
			t.Fatalf("Luck Rich calibration instructions missing %q", required)
		}
	}

	if ui.Interface.DisplayName != "命理校准与解读" ||
		!strings.Contains(ui.Interface.DefaultPrompt, "$luck-rich") ||
		strings.TrimSpace(ui.Interface.ShortDescription) == "" {
		t.Fatalf("invalid Luck Rich UI metadata: %#v", ui.Interface)
	}
}
