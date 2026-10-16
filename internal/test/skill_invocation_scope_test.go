package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"
)

func invokedSkillTaskContext(taskID string) context.Context {
	return context.WithValue(context.Background(), "task_id", taskID)
}

func scopedSkillTestLoader() *SkillsLoader {
	loader := NewSkillsLoader(".", "", "")
	loader.RegisterSkill("task-a-skill", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "task-a-skill", Type: skilldomain.SkillTypeBuiltin},
		Source:   "builtin",
		Path:     "builtin://task-a-skill",
		Code:     "TASK-A-INSTRUCTIONS",
	})
	loader.RegisterSkill("task-b-skill", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "task-b-skill", Type: skilldomain.SkillTypeBuiltin},
		Source:   "builtin",
		Path:     "builtin://task-b-skill",
		Code:     "TASK-B-INSTRUCTIONS",
	})
	return loader
}

func promptMessageText(messages []map[string]interface{}) string {
	var text strings.Builder
	for _, message := range messages {
		content, _ := message["content"].(string)
		text.WriteString(content)
		text.WriteByte('\n')
	}
	return text.String()
}

func TestUseSkillInvocationContinuationIsTaskScoped(t *testing.T) {
	loader := scopedSkillTestLoader()
	tool := NewUseSkillTool(loader, nil)
	taskA := invokedSkillTaskContext("task-a")
	taskB := invokedSkillTaskContext("task-b")

	if _, err := tool.Execute(taskA, map[string]interface{}{"skill_name": "task-a-skill"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(taskB, map[string]interface{}{"skill_name": "task-b-skill"}); err != nil {
		t.Fatal(err)
	}

	promptA := loader.BuildInvokedSkillsPromptForContext(taskA, 5, 4000)
	promptB := loader.BuildInvokedSkillsPromptForContext(taskB, 5, 4000)
	if !strings.Contains(promptA, "TASK-A-INSTRUCTIONS") || strings.Contains(promptA, "TASK-B-INSTRUCTIONS") {
		t.Fatalf("task A continuation crossed scopes:\n%s", promptA)
	}
	if !strings.Contains(promptB, "TASK-B-INSTRUCTIONS") || strings.Contains(promptB, "TASK-A-INSTRUCTIONS") {
		t.Fatalf("task B continuation crossed scopes:\n%s", promptB)
	}
	if legacy := loader.BuildInvokedSkillsPrompt(5, 4000); legacy != "" {
		t.Fatalf("task-scoped use_skill invocation leaked into legacy scope:\n%s", legacy)
	}
}

func TestInvokedSkillLegacyScopeRemainsExplicitAndIsolated(t *testing.T) {
	loader := scopedSkillTestLoader()
	loader.RecordInvokedSkill("task-a-skill", "LEGACY-INSTRUCTIONS")

	legacy := loader.BuildInvokedSkillsPrompt(5, 4000)
	if !strings.Contains(legacy, "LEGACY-INSTRUCTIONS") {
		t.Fatalf("legacy wrapper did not use the legacy scope:\n%s", legacy)
	}
	if scoped := loader.BuildInvokedSkillsPromptForContext(invokedSkillTaskContext("task-a"), 5, 4000); scoped != "" {
		t.Fatalf("legacy invocation leaked into task scope:\n%s", scoped)
	}
	if explicitLegacy := loader.BuildInvokedSkillsPromptForContext(context.Background(), 5, 4000); explicitLegacy != legacy {
		t.Fatalf("context without task_id did not select the legacy scope:\n%s", explicitLegacy)
	}
}

func TestCompactionRestoresOnlyCurrentTaskInvokedSkills(t *testing.T) {
	loader := scopedSkillTestLoader()
	taskA := invokedSkillTaskContext("task-a")
	taskB := invokedSkillTaskContext("task-b")
	loader.RecordInvokedSkillForContext(taskA, "task-a-skill", "TASK-A-RUNTIME-PATH")
	loader.RecordInvokedSkillForContext(taskB, "task-b-skill", "TASK-B-RUNTIME-PATH")
	agent := &LLMAgent{skillsLoader: loader}

	messages := []map[string]interface{}{{"role": "system", "content": "system"}}
	for i := 0; i < 18; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		messages = append(messages, map[string]interface{}{
			"role": role, "content": fmt.Sprintf("message-%02d", i),
		})
	}

	compressedA, err := agent.deterministicCompress(taskA, messages)
	if err != nil {
		t.Fatal(err)
	}
	textA := promptMessageText(compressedA)
	if !strings.Contains(textA, "TASK-A-RUNTIME-PATH") || strings.Contains(textA, "TASK-B-RUNTIME-PATH") {
		t.Fatalf("deterministic compaction restored the wrong task scope:\n%s", textA)
	}

	compactContextB := BuildCompactSessionContextForContext(taskB, agent)
	if !strings.Contains(compactContextB.InvokedSkillsPrompt, "TASK-B-RUNTIME-PATH") ||
		strings.Contains(compactContextB.InvokedSkillsPrompt, "TASK-A-RUNTIME-PATH") {
		t.Fatalf("smart compaction context restored the wrong task scope:\n%s", compactContextB.InvokedSkillsPrompt)
	}
}

func TestUserSkillReloadPreservesTaskInvocationsAndClearsLegacyScope(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "", "")
	loader.RegisterSkill("user-skill", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "user-skill", Type: skilldomain.SkillTypeBuiltin},
		Source:   "user",
		Path:     "user-skill/SKILL.md",
		Code:     "USER-INSTRUCTIONS",
	})
	loader.RegisterSkill("builtin-skill", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "builtin-skill", Type: skilldomain.SkillTypeBuiltin},
		Source:   "builtin",
		Path:     "builtin://builtin-skill",
		Code:     "BUILTIN-INSTRUCTIONS",
	})
	taskA := invokedSkillTaskContext("task-a")
	taskB := invokedSkillTaskContext("task-b")
	for _, ctx := range []context.Context{taskA, taskB} {
		loader.RecordInvokedSkillForContext(ctx, "user-skill", "USER-SCOPED")
		loader.RecordInvokedSkillForContext(ctx, "builtin-skill", "BUILTIN-SCOPED")
	}
	loader.RecordInvokedSkill("user-skill", "USER-LEGACY")

	if _, err := loader.ReloadUserSkills([]string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for name, ctx := range map[string]context.Context{
		"task-a": taskA,
		"task-b": taskB,
	} {
		prompt := loader.BuildInvokedSkillsPromptForContext(ctx, 5, 4000)
		if !strings.Contains(prompt, "USER-SCOPED") || !strings.Contains(prompt, "BUILTIN-SCOPED") {
			t.Fatalf("%s active task lost an already observed skill during reload:\n%s", name, prompt)
		}
	}
	if legacy := loader.BuildInvokedSkillsPrompt(5, 4000); legacy != "" {
		t.Fatalf("legacy user-skill continuation survived reload:\n%s", legacy)
	}
	if fresh := loader.BuildInvokedSkillsPromptForContext(invokedSkillTaskContext("task-c"), 5, 4000); fresh != "" {
		t.Fatalf("a new task inherited another task's invocation:\n%s", fresh)
	}
}
