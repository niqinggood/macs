package app

import (
	"strings"
	"testing"
)

func TestShouldRequestAutoVerificationForMutatingTools(t *testing.T) {
	cases := []struct {
		name string
		call ToolCall
		want bool
	}{
		{name: "write_file", call: ToolCall{Name: "write_file", Arguments: map[string]interface{}{"path": "a.go"}}, want: false},
		{name: "edit_file", call: ToolCall{Name: "edit_file", Arguments: map[string]interface{}{"path": "a.go"}}, want: false},
		{name: "multiedit", call: ToolCall{Name: "multiedit", Arguments: map[string]interface{}{"path": "a.go"}}, want: false},
		{name: "cli_write", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "go mod tidy"}}, want: false},
		{name: "cli_git_push", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "git push origin main"}}, want: true},
		{name: "cli_destructive", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "rm -rf build"}}, want: true},
		{name: "cli_read", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "git status --short"}}, want: false},
		{name: "cli_test", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "go test ./..."}}, want: false},
		{name: "cli_python_report", call: ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": `python3 "./.openkitty/skill-runs/task/med-guid/scripts/normalize_joys360.py" --url-stdin --format report`}}, want: false},
		{name: "git_push", call: ToolCall{Name: "git", Arguments: map[string]interface{}{"action": "push"}}, want: true},
		{name: "git_commit", call: ToolCall{Name: "git", Arguments: map[string]interface{}{"action": "commit"}}, want: false},
		{name: "verify_work", call: ToolCall{Name: "verify_work", Arguments: map[string]interface{}{}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRequestAutoVerification([]ToolCall{tc.call})
			if got != tc.want {
				t.Fatalf("shouldRequestAutoVerification = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildAutoVerificationPrompt(t *testing.T) {
	prompt := buildAutoVerificationPrompt(
		"fix image bubble preview",
		"edit_file(path=web/style.css)",
		[]string{"edit_file(path=web/style.css)"},
		[]string{"node --check web/chat.js: ok"},
	)
	for _, want := range []string{"Auto verification required", "verify_work", "fix image bubble preview", "web/style.css", "node --check"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestAutoVerificationWaitsForFinalCandidate(t *testing.T) {
	incomplete := &ActivePlan{Steps: []PlanStep{{Status: PlanStepInProgress}}}
	if shouldInjectAutoVerificationBeforeFinal(true, false, incomplete, "最终结果") {
		t.Fatal("verification must not interrupt an incomplete plan")
	}
	if shouldInjectAutoVerificationBeforeFinal(true, false, nil, "[Reflection checkpoint - iteration 10]\nContinue the task.") {
		t.Fatal("verification must not interrupt a progress-only response")
	}
	if !shouldInjectAutoVerificationBeforeFinal(true, false, nil, "已生成 3 首歌曲，以下是音频地址。") {
		t.Fatal("verification should run when the model presents a usable final candidate")
	}
	if shouldInjectAutoVerificationBeforeFinal(true, true, nil, "已完成") {
		t.Fatal("verification must run at most once")
	}
}

func TestAutoVerificationCompletionRequiresLatestPass(t *testing.T) {
	makeResult := func(verdict string, succeeded bool) string {
		return toolResultForFinalization(
			ToolCall{Name: "verify_work"},
			"EVIDENCE:\n- targeted check\nVERDICT: "+verdict,
			succeeded,
		)
	}
	if issue := autoVerificationCompletionIssue(true, true, []string{makeResult("PASS", true)}); issue != "" {
		t.Fatalf("supported pass should complete: %s", issue)
	}
	if issue := autoVerificationCompletionIssue(true, true, []string{makeResult("PARTIAL", true)}); !strings.Contains(issue, "PARTIAL") {
		t.Fatalf("partial verification should reject completion: %q", issue)
	}
	if issue := autoVerificationCompletionIssue(true, true, nil); !strings.Contains(issue, "valid verdict") {
		t.Fatalf("missing verification result should reject completion: %q", issue)
	}
	if issue := autoVerificationCompletionIssue(true, false, nil); !strings.Contains(issue, "was not called") {
		t.Fatalf("skipped required verification should reject completion: %q", issue)
	}
}

func TestBuildAutoVerificationToolCallUsesBoundedCurrentTurnEvidence(t *testing.T) {
	call := buildAutoVerificationToolCall(
		"copy files and verify hashes",
		"cli(command=del staging.tmp)",
		[]string{"old-1", "old-2", "old-3", "old-4", "recent-call"},
		[]string{"old-result-1", "old-result-2", "old-result-3", "old-result-4", "recent-result"},
		7,
	)
	if call.Name != "verify_work" || call.ID != "host_auto_verify_7" {
		t.Fatalf("unexpected deterministic verification call: %#v", call)
	}
	if got := call.Arguments["acceptance_criteria"]; got != "copy files and verify hashes" {
		t.Fatalf("acceptance criteria = %#v", got)
	}
	evidence, ok := call.Arguments["evidence"].([]interface{})
	if !ok || len(evidence) != 8 {
		t.Fatalf("bounded evidence = %#v", call.Arguments["evidence"])
	}
	for _, item := range evidence {
		if strings.Contains(item.(string), "old-1") || strings.Contains(item.(string), "old-result-1") {
			t.Fatalf("stale evidence escaped tail bound: %#v", evidence)
		}
	}
}

func TestNormalizeVerifyWorkOutputRejectsConflictingVerdicts(t *testing.T) {
	output := normalizeVerifyWorkOutput("EVIDENCE:\n- check passed\nVERDICT: PASS\nVERDICT: FAIL")
	if !strings.HasSuffix(output, "VERDICT: PARTIAL") {
		t.Fatalf("conflicting verdicts should fail closed: %s", output)
	}
}
