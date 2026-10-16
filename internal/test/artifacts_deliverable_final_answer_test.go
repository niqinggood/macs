package app

import (
	"strings"
	"testing"
)

func TestBuildDeliverableFinalAnswerFromToolResultsActionResult(t *testing.T) {
	out := buildDeliverableFinalAnswerFromToolResults([]string{
		"code_check: confirmation required",
		`ptc_execute: [key] fresh: sk-Research-...
[read] 10 records; top IDs: [2360, 2359, 2357]
[target] #2360 by openclaw-agent
[post] http 201: {"message":"操作成功","post":{"id":2361,"topicId":3,"author":"Researcher-CN","content":"# Researcher-CN action\n\n- openkitty-Researcher-CN","replyTo":2360},"status":"success"}
[audit] -> .openkitty/storage/external_service_action_result_v2.json`,
	})

	for _, want := range []string{
		"已完成工具动作",
		"已回复目标内容",
		"位置：topic #3",
		"Researcher-CN action",
		"#2361",
		"#2360",
		"Researcher-CN",
		"openkitty-Researcher-CN",
		".openkitty/storage/external_service_action_result_v2.json",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("action deliverable summary missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "confirmation required") || strings.Contains(out, "Task failed") {
		t.Fatalf("action deliverable summary should not leak failure noise: %s", out)
	}
}

func TestBuildActionDeliverableFinalAnswerFromToolResultsIgnoresModelRecap(t *testing.T) {
	modelRecap := `The user asked me to run an external action. I successfully completed it.

Let me provide a clean summary to the user.`
	out := buildActionDeliverableFinalAnswerFromToolResults([]string{
		modelRecap,
		`ptc_execute: [target] #2359 by external-service
[post] http 201: {"message":"操作成功","post":{"id":2368,"topicId":2,"author":"Researcher-CN","content":"# Researcher-CN action\n\n... openkitty-Researcher-CN","replyTo":2359},"status":"success"}
[audit] -> .openkitty/storage/action_2359_result.json`,
	})

	for _, want := range []string{
		"已完成工具动作",
		"已回复目标内容",
		"位置：topic #2",
		"Researcher-CN action",
		"#2368",
		"#2359",
		"Researcher-CN",
		".openkitty/storage/action_2359_result.json",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("action deliverable summary missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "The user asked me") || strings.Contains(out, "Let me provide") {
		t.Fatalf("action deliverable summary should not include model recap: %s", out)
	}
}

func TestBuildActionDeliverableFinalAnswerRejectsReadOnlyPTCSuccess(t *testing.T) {
	t1Result := `ptc_execute: ## PTC Execution
{"apiKey":"sk-redacted","status":"success","type":"result"}
{"status":"success","topics":[{"id":11,"title":"🦞 碳基对博"},{"id":14,"title":"🎵 音乐"}]}`

	if out := buildActionDeliverableFinalAnswer(t1Result); out != "" {
		t.Fatalf("read-only API-key/topics result must not become an action completion: %s", out)
	}
}

func TestBuildActionDeliverableFinalAnswerUsesActionScopedID(t *testing.T) {
	result := `ptc_execute: challenge={"id":8,"status":"success"}
POST STATUS: 201
{"message":"发布成功","post":{"id":2527,"topicId":14,"author":"openkitty-researcher","content":"Loud Silence","replyTo":null},"status":"success"}`

	out := buildActionDeliverableFinalAnswer(result)
	for _, want := range []string{"已发布内容", "结果 ID：#2527", "位置：topic #14"} {
		if !strings.Contains(out, want) {
			t.Fatalf("action summary missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "已回复目标内容") || strings.Contains(out, "结果 ID：#8") {
		t.Fatalf("action summary used replyTo:null or unrelated challenge id: %s", out)
	}
}

func TestIsProgressOnlyFinalOutputRejectsMalformedMiniMaxToolCallResidue(t *testing.T) {
	malformed := `]<]minimax[>[<tool_call>
]<]minimax[>[<invoke name="reflect>]<]minimax[>[</invoke>
]<]minimax[>[</tool_call>`

	if !isProgressOnlyFinalOutput(malformed) {
		t.Fatalf("malformed provider tool-call residue must not be accepted as a final answer: %q", cleanUserFacingFinalAnswer(malformed))
	}
}

func TestInternalRecapSeparatedFromUserFinal(t *testing.T) {
	recap := internalRecapBeforeFinalOverride(
		"",
		"The user asked me to post a reply. I successfully posted it.\n\nLet me provide a clean summary.",
		"已完成工具动作。\n- 结果 ID：#2368",
	)
	if !strings.Contains(recap, "The user asked me") {
		t.Fatalf("expected internal recap to preserve model recap, got: %q", recap)
	}

	log := buildTaskCompletionLog("task finish from action tool result", 1.25, 3, []string{"ptc_execute"}, "已完成工具动作。", recap)
	if !strings.Contains(log, "User Final Output:\n已完成工具动作。") {
		t.Fatalf("task log should include user final output: %s", log)
	}
	if !strings.Contains(log, "Internal Recap (not user-visible):") || !strings.Contains(log, "The user asked me") {
		t.Fatalf("task log should keep internal recap separately: %s", log)
	}
}

func TestBuildDeliverableFinalAnswerFromToolResultsIgnoresOrdinaryToolLogs(t *testing.T) {
	out := buildDeliverableFinalAnswerFromToolResults([]string{
		"read_file completed",
		"persistent_shell: generated some logs but no PPTX",
	})
	if out != "" {
		t.Fatalf("ordinary tool logs should not produce a deliverable summary: %s", out)
	}
}

func TestBuildVerifiedFileDeliverableRequiresWriteThenReadback(t *testing.T) {
	path := `E:\work\runs\S27\builtin_skill_evidence.md`
	write := toolResultForFinalization(ToolCall{
		Name: "write_file", Arguments: map[string]interface{}{"path": path},
	}, "成功写入文件: "+path, true)
	read := toolResultForFinalization(ToolCall{
		Name: "read_file", Arguments: map[string]interface{}{"path": path},
	}, "# Builtin Skill Evidence\nsource=builtin", true)
	edit := toolResultForFinalization(ToolCall{
		Name: "edit_file", Arguments: map[string]interface{}{"file_path": path},
	}, "文件编辑成功", true)

	out := buildVerifiedFileDeliverableFinalAnswerFromToolResults([]string{write, read, edit})
	for _, want := range []string{"已完成并回读核验交付文件", path, "恢复了交付状态"} {
		if !strings.Contains(out, want) {
			t.Fatalf("verified file recovery missing %q: %s", want, out)
		}
	}
	if got := buildVerifiedFileDeliverableFinalAnswerFromToolResults([]string{write}); got != "" {
		t.Fatalf("write without readback must not recover terminal success: %s", got)
	}
	if got := buildVerifiedFileDeliverableFinalAnswerFromToolResults([]string{read, write}); got != "" {
		t.Fatalf("read before write must not count as delivery verification: %s", got)
	}
}

func TestBuildVerifiedFileDeliverableRejectsLaterFailedMutation(t *testing.T) {
	path := `runs\report.md`
	results := []string{
		toolResultForFinalization(ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": path}}, "成功写入文件", true),
		toolResultForFinalization(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": `runs/report.md`}}, "verified", true),
		toolResultForFinalization(ToolCall{Name: "edit_file", Arguments: map[string]interface{}{"file_path": path}}, "old_string not found", false),
	}
	if got := buildVerifiedFileDeliverableFinalAnswerFromToolResults(results); got != "" {
		t.Fatalf("a later failed mutation must invalidate terminal recovery: %s", got)
	}
}

func TestCollectMentionedPathsSkipsSkillProbesAndMissingPaths(t *testing.T) {
	paths := collectMentionedPaths([]string{
		"NOT FOUND: .openkitty/skills/example-skill/SKILL.md\nNOT FOUND: skills/example-skill/SKILL.md",
		"URL: https://html.duckduckgo.com/html/?q=openkitty",
		"saved .openkitty/storage/report.md\nsaved .openkitty/logs/pipe_lead.json",
		`runtime path: C:\Users\example\.openkitty\runtime\builtin\example-skill\SKILL.md`,
		`artifact: E:\adebug2\0708\.openkitty\storage\deck.pptx`,
	})
	got := strings.Join(paths, "\n")
	want := strings.Join([]string{
		".openkitty/storage/report.md",
		".openkitty/logs/pipe_lead.json",
		`E:\adebug2\0708\.openkitty\storage\deck.pptx`,
	}, "\n")
	if got != want {
		t.Fatalf("unexpected recoverable paths\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestPromptSafeSkillDescriptionStripsReadSkillInstructions(t *testing.T) {
	desc := "Interact with community. Read the full SKILL.md before acting."
	got := promptSafeSkillDescription(desc)
	if strings.Contains(strings.ToLower(got), "skill.md") || strings.Contains(strings.ToLower(got), "read") {
		t.Fatalf("skill description still contains read instruction: %q", got)
	}
	if !strings.Contains(got, "Interact with community") {
		t.Fatalf("skill description lost useful content: %q", got)
	}
}
