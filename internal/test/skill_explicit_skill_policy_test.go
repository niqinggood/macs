package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const explicitSkillArtifactTestInstructions = `# Quarterly Risk Memo

1. 输出文件名必须是 ` + "`quarterly_risk_memo.md`" + `；
2. 第一行必须精确为 ` + "`RISK-MEMO-V1`" + `；
3. 必须包含 ` + "`## Assumptions`" + `、` + "`## Conflicts`" + `、` + "`## Actions`" + ` 三个标题；
4. 每个风险使用 ` + "`severity: high|medium|low`" + ` 标记。`

func explicitSkillTestLoader(t *testing.T) *SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "quarterly-risk-memo")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: quarterly-risk-memo\ndescription: fixed risk memo\ntype: builtin\n---\n\n" + explicitSkillArtifactTestInstructions
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(document), 0644); err != nil {
		t.Fatal(err)
	}
	loader := NewSkillsLoader(workspace, "Assistant", "")
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatalf("load test Skill: %v", err)
	}
	return loader
}

func TestExplicitSkillRequirementForcesUseSkillBeforeOtherTools(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	ctx := withExplicitSkillRequirement(
		context.Background(),
		"使用用户自定义的 `quarterly-risk-memo` Skill 整理材料。",
		loader,
	)
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil || requirement.Name != "quarterly-risk-memo" {
		t.Fatalf("explicit skill was not detected: %#v", requirement)
	}
	if blocked, _ := explicitSkillPolicyBlocksTool(ctx, ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "fixtures/input.md"}}); !blocked {
		t.Fatal("source inspection was allowed before explicit skill activation")
	}
	if blocked, _ := explicitSkillPolicyBlocksTool(ctx, ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "other"}}); !blocked {
		t.Fatal("wrong skill was allowed before the requested skill")
	}
	call := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	if blocked, reason := explicitSkillPolicyBlocksTool(ctx, call); blocked {
		t.Fatalf("requested skill was blocked: %s", reason)
	}
	recordExplicitSkillToolResult(ctx, call, ToolResult{ExecutionSucceeded: toolExecutionOutcome(true)}, nil)
	if blocked, reason := explicitSkillPolicyBlocksTool(ctx, ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "fixtures/input.md"}}); blocked {
		t.Fatalf("source read remained blocked after activation: %s", reason)
	}
	if blocked, _ := explicitSkillPolicyBlocksTool(ctx, ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "skills/quarterly-risk-memo/SKILL.md"}}); !blocked {
		t.Fatal("direct SKILL.md read was allowed after activation")
	}
}

func TestExplicitSkillRequirementPromptEmphasizesLiteralContracts(t *testing.T) {
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo skill.", explicitSkillTestLoader(t))
	prompt := explicitSkillRequirementPrompt(ctx)
	for _, want := range []string{"quarterly-risk-memo", "use_skill", "exact contracts", "required literal"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("explicit skill prompt missing %q: %s", want, prompt)
		}
	}
}

func TestExplicitSkillRequirementIgnoresNegatedSkillRequest(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	for _, input := range []string{
		"不要使用 quarterly-risk-memo Skill，直接分析。",
		"不用 quarterly-risk-memo Skill。",
		"Do not activate the quarterly-risk-memo skill; analyze it directly.",
	} {
		if name := explicitlyRequestedSkillName(input, loader); name != "" {
			t.Fatalf("negated skill request %q activated %q", input, name)
		}
	}
}

func TestExplicitSkillRequirementDoesNotConfuseSkillQualifierWithNegation(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	for _, input := range []string{
		"使用用户自定义的 `quarterly-risk-memo` Skill，这是用户目录 Skill，不要把它当内置 Skill。",
		"使用 quarterly-risk-memo Skill，不得忽略 Skill 中的固定格式。",
		"Use the quarterly-risk-memo Skill; do not treat this user Skill as builtin.",
	} {
		if name := explicitlyRequestedSkillName(input, loader); name != "quarterly-risk-memo" {
			t.Fatalf("qualified skill request %q resolved to %q", input, name)
		}
	}
}

func TestExplicitSkillRequirementReloadsSkillAddedAfterStartup(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "Assistant", "")
	if snapshots := loader.SnapshotSkills(); len(snapshots) != 0 {
		t.Fatalf("new loader unexpectedly contains Skills: %#v", snapshots)
	}

	skillDir := filepath.Join(workspace, "skills", "late-installed-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: late-installed-skill\ndescription: installed after startup\ntype: builtin\n---\n\nFollow the user's request."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(document), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := withExplicitSkillRequirement(context.Background(), "Use the late-installed-skill Skill now.", loader)
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil || requirement.Name != "late-installed-skill" {
		t.Fatalf("late-installed Skill was not discovered: %#v", requirement)
	}
}

func TestExplicitSkillRequirementKeepsPreviousSnapshotWhenReloadFails(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	skillFile := filepath.Join(loader.Workspace(), "skills", "quarterly-risk-memo", "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: [\n---\nbroken"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", loader)
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil || requirement.Name != "quarterly-risk-memo" {
		t.Fatalf("failed reload discarded the previous atomic snapshot: %#v", requirement)
	}
}

func TestExplicitSkillArtifactContractRequiresFullExactReread(t *testing.T) {
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", explicitSkillTestLoader(t))
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)

	requirement := explicitSkillRequirementFromContext(ctx)
	requirement.mu.RLock()
	contract := cloneExplicitSkillArtifactContract(requirement.Artifact)
	requirement.mu.RUnlock()
	if contract == nil || contract.Filename != "quarterly_risk_memo.md" || contract.FirstLine != "RISK-MEMO-V1" {
		t.Fatalf("artifact contract not extracted: %#v", contract)
	}
	if got := strings.Join(contract.Headings, " | "); got != "## Assumptions | ## Conflicts | ## Actions" {
		t.Fatalf("headings = %q", got)
	}
	if len(contract.Fields) != 1 || contract.Fields[0].Syntax != "severity: high|medium|low" {
		t.Fatalf("fields = %#v", contract.Fields)
	}
	if issues := explicitSkillCompletionIssues(ctx); len(issues) != 1 || !strings.Contains(issues[0], "re-read") {
		t.Fatalf("missing-read issues = %#v", issues)
	}

	partial := ToolCall{Name: "read_file", Arguments: map[string]interface{}{
		"path": "runs/S28/quarterly_risk_memo.md", "lines_range": "head:10",
	}}
	recordExplicitSkillToolResult(ctx, partial, ToolResult{
		Name: "read_file", Content: "RISK-MEMO-V1\n## Assumptions\n## Conflicts\n## Actions\nseverity: high", ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	if issues := explicitSkillCompletionIssues(ctx); len(issues) != 1 || !strings.Contains(issues[0], "re-read") {
		t.Fatalf("partial read incorrectly satisfied contract: %#v", issues)
	}

	invalid := ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": `runs\S28\quarterly_risk_memo.md`}}
	recordExplicitSkillToolResult(ctx, invalid, ToolResult{
		Name: "read_file", Content: "Risk memo\n## Assumptions\n## Actions\nseverity: urgent", ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	issues := explicitSkillCompletionIssues(ctx)
	for _, want := range []string{"RISK-MEMO-V1", "## Conflicts", "severity: high|medium|low"} {
		if !strings.Contains(strings.Join(issues, " | "), want) {
			t.Fatalf("invalid artifact issues missing %q: %#v", want, issues)
		}
	}

	valid := ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "runs/S28/quarterly_risk_memo.md"}}
	recordExplicitSkillToolResult(ctx, valid, ToolResult{
		Name:               "read_file",
		Content:            "RISK-MEMO-V1\n## Assumptions\nUnknown owner.\n## Conflicts\nOwner: 待确认\n## Actions\n- severity: high",
		ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	if issues := explicitSkillCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("valid artifact still has issues: %#v", issues)
	}
}

func TestExplicitSkillArtifactContractIgnoresCommandsAndExamples(t *testing.T) {
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", explicitSkillTestLoader(t))
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name:               "use_skill",
		Content:            "Run `python scripts/build.py --output report.md`.\nExample output file: `example.md`.\nThe command must pass `--format markdown`.",
		ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	if issues := explicitSkillCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("ordinary commands/examples created an artifact gate: %#v", issues)
	}
}

func TestExplicitSkillMarkdownEnumNormalizerRepairsEveryUnambiguousTable(t *testing.T) {
	contract := extractExplicitSkillArtifactContract(explicitSkillArtifactTestInstructions)
	if contract == nil {
		t.Fatal("test contract was not parsed")
	}
	content := `RISK-MEMO-V1
## Assumptions
Known facts.
## Conflicts
| risk | severity |
|---|---|
| owner conflict | high |
| date conflict | medium |
## Actions
| action | severity |
|---|---|
| confirm owner | low |`
	repaired, changed, ok := normalizeExplicitSkillMarkdownEnumFields(content, contract.Fields)
	if !ok || !changed {
		t.Fatalf("safe Markdown tables were not normalized: ok=%v changed=%v", ok, changed)
	}
	for _, want := range []string{"| owner conflict | severity: high |", "| date conflict | severity: medium |", "| confirm owner | severity: low |"} {
		if !strings.Contains(repaired, want) {
			t.Fatalf("normalized artifact missing %q:\n%s", want, repaired)
		}
	}
	if !explicitSkillArtifactContractValid(repaired, contract) {
		t.Fatalf("normalized artifact does not satisfy its exact contract:\n%s", repaired)
	}
}

func TestExplicitSkillMarkdownEnumNormalizerRejectsAmbiguityAndUnknownValues(t *testing.T) {
	contract := extractExplicitSkillArtifactContract(explicitSkillArtifactTestInstructions)
	for name, content := range map[string]string{
		"duplicate columns": `RISK-MEMO-V1
## Assumptions
## Conflicts
## Actions
| risk | severity | severity |
|---|---|---|
| owner | high | medium |`,
		"unknown enum": `RISK-MEMO-V1
## Assumptions
## Conflicts
## Actions
| risk | severity |
|---|---|
| owner | urgent |`,
		"escaped pipe": `RISK-MEMO-V1
## Assumptions
## Conflicts
## Actions
| risk | severity |
|---|---|
| owner \| date | high |`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, changed, ok := normalizeExplicitSkillMarkdownEnumFields(content, contract.Fields); ok || changed {
				t.Fatalf("ambiguous artifact was considered repairable: ok=%v changed=%v", ok, changed)
			}
		})
	}
}

func TestExplicitSkillReadHookDeterministicallyRepairsWrittenMarkdownArtifact(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", loader)
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)

	relativePath := filepath.Join("runs", "S28", "quarterly_risk_memo.md")
	absolutePath := filepath.Join(loader.Workspace(), relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0755); err != nil {
		t.Fatal(err)
	}
	content := `RISK-MEMO-V1
## Assumptions
Known facts.
## Conflicts
| risk | severity |
|---|---|
| owner conflict | high |
## Actions
| action | severity |
|---|---|
| confirm owner | medium |`
	if err := os.WriteFile(absolutePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": relativePath}},
		ToolResult{Name: "write_file", ExecutionSucceeded: toolExecutionOutcome(true)}, nil)

	readContent, err := NewReadFileTool(loader.Workspace()).Execute(ctx, map[string]interface{}{"path": relativePath})
	if err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": relativePath}},
		ToolResult{Name: "read_file", Content: readContent, ExecutionSucceeded: toolExecutionOutcome(true)}, nil)

	onDisk, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| owner conflict | severity: high |", "| confirm owner | severity: medium |"} {
		if !strings.Contains(string(onDisk), want) {
			t.Fatalf("deterministic repair missing %q:\n%s", want, onDisk)
		}
	}
	if issues := explicitSkillCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("deterministically repaired artifact still failed: %#v", issues)
	}
}

func TestExplicitSkillReadHookDoesNotRepairArtifactWithoutCurrentTurnWrite(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", loader)
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)

	relativePath := filepath.Join("runs", "S28", "quarterly_risk_memo.md")
	absolutePath := filepath.Join(loader.Workspace(), relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0755); err != nil {
		t.Fatal(err)
	}
	content := "RISK-MEMO-V1\n## Assumptions\n## Conflicts\n## Actions\n| risk | severity |\n|---|---|\n| owner | high |"
	if err := os.WriteFile(absolutePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	readContent, err := NewReadFileTool(loader.Workspace()).Execute(ctx, map[string]interface{}{"path": relativePath})
	if err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": relativePath}},
		ToolResult{Name: "read_file", Content: readContent, ExecutionSucceeded: toolExecutionOutcome(true)}, nil)

	onDisk, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != content {
		t.Fatalf("artifact without a current-turn write was mutated:\n%s", onDisk)
	}
	if issues := explicitSkillCompletionIssues(ctx); len(issues) == 0 {
		t.Fatal("unrepaired artifact did not fail closed")
	}
}

func TestExplicitSkillReadHookRejectsMultipleWrittenArtifactsWithRequiredFilename(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", loader)
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	content := "RISK-MEMO-V1\n## Assumptions\n## Conflicts\n## Actions\n| risk | severity |\n|---|---|\n| owner | high |"
	for _, relativePath := range []string{
		filepath.Join("runs", "first", "quarterly_risk_memo.md"),
		filepath.Join("runs", "second", "quarterly_risk_memo.md"),
	} {
		absolutePath := filepath.Join(loader.Workspace(), relativePath)
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolutePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		recordExplicitSkillToolResult(ctx,
			ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": relativePath}},
			ToolResult{Name: "write_file", ExecutionSucceeded: toolExecutionOutcome(true)}, nil)
	}
	target := filepath.Join("runs", "second", "quarterly_risk_memo.md")
	readContent, err := NewReadFileTool(loader.Workspace()).Execute(ctx, map[string]interface{}{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": target}},
		ToolResult{Name: "read_file", Content: readContent, ExecutionSucceeded: toolExecutionOutcome(true)}, nil)
	onDisk, err := os.ReadFile(filepath.Join(loader.Workspace(), target))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != content {
		t.Fatalf("ambiguous artifact target was mutated:\n%s", onDisk)
	}
	if issues := explicitSkillCompletionIssues(ctx); len(issues) == 0 {
		t.Fatal("ambiguous artifact target did not fail closed")
	}
}

func TestExplicitSkillReadHookNeverRepairsOutsideSkillWorkspace(t *testing.T) {
	loader := explicitSkillTestLoader(t)
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", loader)
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)

	outsidePath := filepath.Join(t.TempDir(), "quarterly_risk_memo.md")
	content := "RISK-MEMO-V1\n## Assumptions\n## Conflicts\n## Actions\n| risk | severity |\n|---|---|\n| owner | high |"
	if err := os.WriteFile(outsidePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": outsidePath}},
		ToolResult{Name: "write_file", ExecutionSucceeded: toolExecutionOutcome(true)}, nil)
	readContent, err := NewReadFileTool(loader.Workspace()).Execute(ctx, map[string]interface{}{"path": outsidePath})
	if err != nil {
		t.Fatal(err)
	}
	recordExplicitSkillToolResult(ctx,
		ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": outsidePath}},
		ToolResult{Name: "read_file", Content: readContent, ExecutionSucceeded: toolExecutionOutcome(true)}, nil)
	onDisk, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != content {
		t.Fatalf("outside-workspace artifact was mutated:\n%s", onDisk)
	}
	if issues := explicitSkillCompletionIssues(ctx); len(issues) == 0 {
		t.Fatal("outside-workspace artifact did not fail closed")
	}
}

func TestBuildExplicitSkillRepairPromptCarriesLiteralContract(t *testing.T) {
	ctx := withExplicitSkillRequirement(context.Background(), "Use the quarterly-risk-memo Skill.", explicitSkillTestLoader(t))
	activate := ToolCall{Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "quarterly-risk-memo"}}
	recordExplicitSkillToolResult(ctx, activate, ToolResult{
		Name: "use_skill", Content: explicitSkillArtifactTestInstructions, ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil)
	message := buildExplicitSkillRepairPrompt(ctx, explicitSkillCompletionIssues(ctx))
	content, _ := message["content"].(string)
	for _, want := range []string{
		"quarterly_risk_memo.md", "RISK-MEMO-V1", "## Conflicts", "severity: high|medium|low",
		"Do not call use_skill again", "read_file", "without lines_range",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("repair prompt missing %q: %s", want, content)
		}
	}
}
