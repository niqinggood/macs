package app

import (
	"context"
	"strings"
	"testing"
	"time"

	developmenttools "mac/internal/tools/development"
)

func TestCodexApprovalManagerGetReturnsCopy(t *testing.T) {
	m := NewCodexApprovalManager()
	req := m.Create(CodexApprovalRequest{SessionID: "s1", TaskID: "task1", Summary: "Command: go test"})
	got, ok := m.Get(req.ID)
	if !ok {
		t.Fatalf("expected approval request")
	}
	if got.ID != req.ID || got.Summary != req.Summary {
		t.Fatalf("unexpected approval copy: %#v", got)
	}
	if got.Response != nil {
		t.Fatalf("response channel should not be exposed")
	}
	got.Summary = "mutated"
	again, ok := m.Get(req.ID)
	if !ok || again.Summary != req.Summary {
		t.Fatalf("stored approval was mutated: %#v", again)
	}
}

func TestSummarizeCodexCommandApprovalIncludesShellAnalysis(t *testing.T) {
	got := summarizeCodexCommandApproval(map[string]interface{}{
		"command": "go test ./...",
		"cwd":     "E:/openkitty",
		"reason":  "verify tests",
	})
	for _, want := range []string{"Command: go test ./...", "Shell analysis: class=build_test, blocked=false", "Suggested permission rule: cli(go test)", "CWD: E:/openkitty", "Reason: verify tests"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestCreateCodexApprovalRequestAddsShellAnalysis(t *testing.T) {
	manager := NewCodexApprovalManager()
	req := createCodexApprovalRequest(codexAppServerRunOptions{
		SessionID:       "s1",
		ApprovalManager: manager,
	}, "exec", "Command: go test ./...", map[string]interface{}{
		"command": "go test ./...",
		"cwd":     "E:/openkitty",
	})
	if req == nil || req.ShellAnalysis == nil {
		t.Fatalf("expected shell analysis on approval request: %#v", req)
	}
	if req.ShellAnalysis.Class != ShellClassBuildTest || req.ShellAnalysis.SuggestedRule != "cli(go test)" {
		t.Fatalf("unexpected shell analysis: %#v", req.ShellAnalysis)
	}

	got, ok := manager.Get(req.ID)
	if !ok || got.ShellAnalysis == nil {
		t.Fatalf("expected copied approval with shell analysis")
	}
	got.ShellAnalysis.SuggestedRule = "mutated"
	again, _ := manager.Get(req.ID)
	if again.ShellAnalysis.SuggestedRule != "cli(go test)" {
		t.Fatalf("approval shell analysis should be deep-copied: %#v", again.ShellAnalysis)
	}
}

func TestCodexAutomaticCommandDecisionRunsWithoutApproval(t *testing.T) {
	for _, command := range []string{
		"go test ./...",
		"git reset --hard HEAD",
		"npm install",
		"shutdown /s /t 0",
	} {
		decision, reason := codexAutomaticCommandDecision(map[string]interface{}{"command": command})
		if decision != "acceptForSession" || reason != "" {
			t.Fatalf("command %q decision=%q reason=%q, want automatic allow", command, decision, reason)
		}
	}
}

func TestCodexAutomaticCommandDecisionBlocksSystemDestruction(t *testing.T) {
	for _, command := range []string{
		"rm -rf /",
		`Remove-Item -Recurse -Force C:\Windows\System32`,
		"mkfs.ext4 /dev/sda1",
		"dd if=image.bin of=/dev/sda",
		"echo broken > /etc/passwd",
	} {
		decision, reason := codexAutomaticCommandDecision(map[string]interface{}{"command": command})
		if decision != "decline" || reason == "" {
			t.Fatalf("command %q decision=%q reason=%q, want system-destruction block", command, decision, reason)
		}
	}
}

func TestCodexFileChangeProtectionOnlyTargetsSystemPaths(t *testing.T) {
	params := map[string]interface{}{"itemId": "item-1", "grantRoot": `E:\openkitty`}
	if codexFileChangeTargetsProtectedSystemPath(params, map[string]string{"item-1": `1 update: E:\openkitty\main.go`}) {
		t.Fatal("workspace file change should be automatically allowed")
	}
	params["grantRoot"] = `C:\Windows`
	if !codexFileChangeTargetsProtectedSystemPath(params, map[string]string{"item-1": `1 update: C:\Windows\System32\drivers\etc\hosts`}) {
		t.Fatal("protected system file change should be blocked")
	}
}

func TestProjectCodexNotificationCommandAndFinalText(t *testing.T) {
	progress, finalText := projectCodexNotification(map[string]interface{}{
		"method": "item/started",
		"params": map[string]interface{}{
			"item": map[string]interface{}{
				"type":    "commandExecution",
				"command": "go test ./...",
				"cwd":     "E:/openkitty",
			},
		},
	}, map[string]string{})
	if progress.Type != "tool_start" || progress.ToolName != "exec_command" {
		t.Fatalf("unexpected command progress: %#v", progress)
	}
	if finalText != "" {
		t.Fatalf("final text = %q, want empty", finalText)
	}

	progress, finalText = projectCodexNotification(map[string]interface{}{
		"method": "item/completed",
		"params": map[string]interface{}{
			"item": map[string]interface{}{
				"type": "agentMessage",
				"text": "done",
			},
		},
	}, map[string]string{})
	if progress.Type != "text_delta" || finalText != "done" {
		t.Fatalf("unexpected agent message projection: progress=%#v final=%q", progress, finalText)
	}
}

func TestCodexTurnFailureCanUseDeliveredFinalForEncodingArtifact(t *testing.T) {
	finalText := "Done: .openkitty/storage/doubao_pdf_pages/doubao_pages_side_by_side.png"
	if !codexTurnFailureCanUseDeliveredFinal(finalText, "failed", "ptc_execute UnicodeDecodeError: codec can't decode byte") {
		t.Fatalf("artifact plus encoding failure should be non-fatal")
	}
}

func TestCodexTurnFailureCanUseDeliveredFinalRejectsNoArtifact(t *testing.T) {
	if codexTurnFailureCanUseDeliveredFinal("I could not finish the task.", "failed", "model failed") {
		t.Fatalf("plain failed turn without artifact should remain fatal")
	}
}

func TestFormatCodexAppServerOutputOmitsProgressTranscript(t *testing.T) {
	got := formatCodexAppServerOutput(
		"success",
		time.Second,
		"E:/openkitty/.openkitty/storage/external_agents/report.md",
		nil,
		[]string{"M api.go"},
		"final answer",
		[]string{"Run command: go test ./...", "Command completed"},
	)
	if strings.Contains(got, "[Codex progress]") || strings.Contains(got, "Run command: go test") {
		t.Fatalf("transport output leaked progress transcript:\n%s", got)
	}
	for _, want := range []string{"[External Agent: codex-app-server]", "[Report:", "[Changed files]", "final answer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
}

func TestShouldNotifyCodexChannelProgressSuppressesBubbleLogs(t *testing.T) {
	for _, eventType := range []string{"tool_start", "tool_result", "complete", "error", "cancelled", "permission_denied"} {
		if shouldNotifyCodexChannelProgress(codexAppServerProgress{Type: eventType, Message: "Run command: go test ./..."}) {
			t.Fatalf("progress event %q should stay in SSE bubbles, not external channel transport", eventType)
		}
	}
}

func TestDiffExternalAgentChangedFilesIgnoresBaseline(t *testing.T) {
	got := developmenttools.DiffExternalAgentChangedFiles(
		[]string{"M existing.go", "?? old.txt"},
		[]string{"M existing.go", "?? old.txt", "A new.go", " M changed.go", "A new.go"},
	)
	want := []string{"A new.go", "M changed.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diff = %#v, want %#v", got, want)
	}
}

func TestWaitCodexApprovalDecisionDeclinesOnContextCancel(t *testing.T) {
	m := NewCodexApprovalManager()
	req := m.Create(CodexApprovalRequest{SessionID: "s1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := waitCodexApprovalDecision(ctx, req)
	if got != "decline" {
		t.Fatalf("decision = %q, want decline", got)
	}
}
