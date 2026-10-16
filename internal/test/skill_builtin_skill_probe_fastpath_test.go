package app

import (
	"testing"

	skilldomain "mac/internal/skills"
)

func TestBuiltinSkillWorkspaceProbeBecomesUseSkill(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test", "")
	loader.RegisterSkill("base-ppt", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: "base-ppt", Aliases: []string{"better-ppt"}}, Source: "builtin"})
	agent := &LLMAgent{skillsLoader: loader}
	calls, changed := agent.normalizeBuiltinSkillProbeCalls([]ToolCall{{
		ID:        "probe-1",
		Name:      "list_dir",
		Arguments: map[string]interface{}{"path": `E:\work\.openkitty\skills\better-ppt`},
	}})
	if !changed || len(calls) != 1 || calls[0].ID != "probe-1" || calls[0].Name != "use_skill" {
		t.Fatalf("builtin probe was not normalized: changed=%v calls=%#v", changed, calls)
	}
	if calls[0].Arguments["skill_name"] != "base-ppt" {
		t.Fatalf("skill_name = %#v", calls[0].Arguments["skill_name"])
	}
}

func TestBuiltinSkillProbeNormalizerPreservesUserSkillsAndOrdinaryPaths(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test", "")
	loader.RegisterSkill("custom-deck", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: "custom-deck"}, Source: "user"})
	agent := &LLMAgent{skillsLoader: loader}
	for _, call := range []ToolCall{
		{ID: "user-skill", Name: "list_dir", Arguments: map[string]interface{}{"path": `.openkitty/skills/custom-deck`}},
		{ID: "ordinary", Name: "list_dir", Arguments: map[string]interface{}{"path": `.openkitty/storage`}},
	} {
		got, changed := agent.normalizeBuiltinSkillProbeCalls([]ToolCall{call})
		if changed || len(got) != 1 || got[0].Name != call.Name {
			t.Fatalf("unrelated probe changed: changed=%v got=%#v", changed, got)
		}
	}
}

func TestBuiltinRuntimeMainDocumentProbeBecomesUseSkill(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test", "")
	loader.RegisterSkill("ppt-art-director", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "ppt-art-director"},
		Source:   "builtin",
	})
	agent := &LLMAgent{skillsLoader: loader}
	for _, call := range []ToolCall{
		{ID: "read", Name: "read_file", Arguments: map[string]interface{}{
			"path": `C:\Users\tester\.openkitty\runtime\builtin\presentation\ppt-art-director\SKILL.md`,
		}},
		{ID: "script", Name: "ptc_execute", Arguments: map[string]interface{}{
			"script": `open(r"C:\Users\tester\.openkitty\runtime\builtin\presentation\ppt-art-director\SKILL.md", encoding="utf-8").read()`,
		}},
	} {
		got, changed := agent.normalizeBuiltinSkillProbeCalls([]ToolCall{call})
		if !changed || len(got) != 1 || got[0].Name != "use_skill" || got[0].Arguments["skill_name"] != "ppt-art-director" {
			t.Fatalf("runtime main document probe was not routed to memory: changed=%v got=%#v", changed, got)
		}
	}
}

func TestBuiltinRuntimeSupportFilesAndUserSkillFilesRemainReadable(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test", "")
	loader.RegisterSkill("ppt-art-director", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: "ppt-art-director"}, Source: "builtin"})
	loader.RegisterSkill("quarterly-risk-memo", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: "quarterly-risk-memo"}, Source: "user"})
	agent := &LLMAgent{skillsLoader: loader}
	for _, call := range []ToolCall{
		{ID: "reference", Name: "read_file", Arguments: map[string]interface{}{
			"path": `C:\Users\tester\.openkitty\runtime\builtin\presentation\ppt-art-director\references\qa.md`,
		}},
		{ID: "user", Name: "read_file", Arguments: map[string]interface{}{
			"path": `E:\work\skills\quarterly-risk-memo\SKILL.md`,
		}},
	} {
		got, changed := agent.normalizeBuiltinSkillProbeCalls([]ToolCall{call})
		if changed || len(got) != 1 || got[0].Name != "read_file" {
			t.Fatalf("legitimate support/user file was rerouted: changed=%v got=%#v", changed, got)
		}
	}
}
