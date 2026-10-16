package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type repairingCodeSessionAgent struct {
	workspace string
	inputs    []string
}

func (agent *repairingCodeSessionAgent) Name() string       { return "repairing" }
func (agent *repairingCodeSessionAgent) GetType() AgentType { return AgentTypeCustom }
func (agent *repairingCodeSessionAgent) Generate(_ context.Context, input string) (string, error) {
	agent.inputs = append(agent.inputs, input)
	path := filepath.Join(agent.workspace, "value.go")
	if len(agent.inputs) == 1 {
		if err := os.WriteFile(path, []byte("package sample\n\nfunc Value() int {\n"), 0644); err != nil {
			return "", err
		}
		return "implemented Value", nil
	}
	if strings.Contains(input, "[CODING VERIFICATION FAILED]") {
		if err := os.WriteFile(path, []byte("package sample\n\nfunc Value() int {\n\treturn 42\n}\n"), 0644); err != nil {
			return "", err
		}
		return "repaired Value and verified it", nil
	}
	return "unexpected invocation", nil
}

func TestDetectCodeChangeSetOnlyAttributesCurrentTurn(t *testing.T) {
	before := codeWorkspaceState{
		"already-dirty.go": {Status: "M", Fingerprint: "before"},
		"untouched.go":     {Status: "M", Fingerprint: "same"},
	}
	after := codeWorkspaceState{
		"already-dirty.go": {Status: "M", Fingerprint: "after"},
		"untouched.go":     {Status: "M", Fingerprint: "same"},
		"new.go":           {Status: "??", Fingerprint: "new"},
	}
	changeSet := detectCodeChangeSet(t.TempDir(), 3, -1, before, after, nil)
	if len(changeSet.Files) != 2 {
		t.Fatalf("change set = %#v", changeSet.Files)
	}
	if changeSet.Files[0].Path != "already-dirty.go" || changeSet.Files[1].Path != "new.go" {
		t.Fatalf("unexpected attributed files: %#v", changeSet.Files)
	}
}

func TestPlanCodeVerificationSelectsSiblingGoTests(t *testing.T) {
	root := writeVerificationGoFixture(t)
	changeSet := CodeChangeSet{
		Turn:  1,
		Files: []CodeChangedFile{{Path: "value.go", Status: "M"}},
	}
	specs := planCodeVerification(root, changeSet, codeVerificationAuto)
	var commands []string
	for _, spec := range specs {
		commands = append(commands, formatCodeVerificationCommand(spec))
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{"gofmt -d", "go test -count=1 -run", "TestValue"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("verification plan missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "./...") {
		t.Fatalf("automatic verification should remain targeted:\n%s", joined)
	}
}

func TestRunCodeVerificationPassesAndReportsCompileFailure(t *testing.T) {
	root := writeVerificationGoFixture(t)
	changeSet := CodeChangeSet{
		Turn:  1,
		Files: []CodeChangedFile{{Path: "value.go", Status: "M"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	passed := runCodeVerification(ctx, root, changeSet, "test", codeVerificationAuto)
	cancel()
	if passed.Status != codeCheckPassed {
		t.Fatalf("valid fixture verification = %+v\n%s", passed, passed.Summary(true))
	}

	if err := os.WriteFile(filepath.Join(root, "value.go"), []byte("package sample\n\nfunc Value() int {\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	failed := runCodeVerification(ctx, root, changeSet, "test", codeVerificationAuto)
	cancel()
	if failed.Status != codeCheckFailed {
		t.Fatalf("broken fixture verification = %+v\n%s", failed, failed.Summary(true))
	}
	if !strings.Contains(failed.Summary(true), "FAILED") {
		t.Fatalf("failure summary missing evidence:\n%s", failed.Summary(true))
	}
}

func TestCodingSessionAutoVerificationRepairsOnce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := writeVerificationGoFixture(t)
	if err := os.WriteFile(
		filepath.Join(root, "value.go"),
		[]byte("package sample\n\nfunc Value() int {\n\treturn 0\n}\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	runVerificationGit(t, root, "init")
	runVerificationGit(t, root, "add", "go.mod", "value.go", "value_test.go")

	state := &CodeSessionState{
		Version:       codeSessionStateVersion,
		ID:            "code-auto-repair",
		RootWorkspace: root,
		Workspace:     root,
		Agent:         "repairing",
		CreatedAt:     time.Now(),
	}
	agent := &repairingCodeSessionAgent{workspace: root}
	var output bytes.Buffer
	session := &CodingSession{
		state:  state,
		agent:  agent,
		engine: &MultiAgentEngine{},
		output: &output,
		errors: &output,
	}

	if err := session.runTurn("implement Value so it returns forty two"); err != nil {
		t.Fatalf("run repaired coding turn: %v\n%s", err, output.String())
	}
	if len(agent.inputs) != 2 || !strings.Contains(agent.inputs[1], "[CODING VERIFICATION FAILED]") {
		t.Fatalf("repair evidence was not fed back exactly once: %#v", agent.inputs)
	}
	if state.Turn != 1 || len(state.Messages) != 2 {
		t.Fatalf("repair must remain one user turn: %+v", state)
	}
	if len(state.ChangeSets) != 1 || len(state.ChangeSets[0].Files) != 1 ||
		state.ChangeSets[0].Files[0].Path != "value.go" {
		t.Fatalf("unexpected persisted change set: %#v", state.ChangeSets)
	}
	if len(state.Verifications) != 2 ||
		!state.Verifications[0].RepairAttempted ||
		!state.Verifications[0].RepairSucceeded ||
		state.Verifications[1].Status != codeCheckPassed {
		t.Fatalf("unexpected verification receipts: %#v", state.Verifications)
	}
	if !strings.Contains(output.String(), "Verification PASSED") {
		t.Fatalf("final verification receipt not shown:\n%s", output.String())
	}
}

func TestCodingSessionVerifyCommandPersistsReceipt(t *testing.T) {
	root := writeVerificationGoFixture(t)
	var output bytes.Buffer
	session := &CodingSession{
		state: &CodeSessionState{
			Version:       codeSessionStateVersion,
			ID:            "code-manual-verify",
			RootWorkspace: root,
			Workspace:     root,
			Turn:          1,
		},
		output: &output,
		errors: &output,
	}
	handled, exit := session.handleCommand("/verify value.go")
	if !handled || exit {
		t.Fatalf("verify command handled=%t exit=%t", handled, exit)
	}
	if len(session.state.Verifications) != 1 ||
		session.state.Verifications[0].Status != codeCheckPassed {
		t.Fatalf("manual verification receipt = %#v", session.state.Verifications)
	}
	if !strings.Contains(output.String(), "Verification PASSED") {
		t.Fatalf("manual verification output:\n%s", output.String())
	}
}

func TestCodingSessionReviewCommandIncludesUntrackedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	runVerificationGit(t, root, "init")
	if err := os.WriteFile(
		filepath.Join(root, "new.go"),
		[]byte("package sample\n\nvar password = \"secret123\"\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	session := &CodingSession{
		state: &CodeSessionState{
			Version:       codeSessionStateVersion,
			ID:            "code-review",
			RootWorkspace: root,
			Workspace:     root,
		},
		output: &output,
		errors: &output,
	}
	handled, exit := session.handleCommand("/review")
	if !handled || exit {
		t.Fatalf("review command handled=%t exit=%t", handled, exit)
	}
	if len(session.state.Verifications) != 1 ||
		session.state.Verifications[0].Trigger != "manual_review" {
		t.Fatalf("review receipt = %#v", session.state.Verifications)
	}
	for _, want := range []string{"new.go", "Possible hardcoded secret"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("review output missing %q:\n%s", want, output.String())
		}
	}
}

func TestBuildCodingRepairPromptContainsExactFailureEvidence(t *testing.T) {
	receipt := CodeVerificationReceipt{
		Status: codeCheckFailed,
		Checks: []CodeVerificationCheck{{
			Name:    "Related Go tests",
			Command: "go test ./pkg",
			Status:  codeCheckFailed,
			Output:  "expected 42, got 0",
		}},
	}
	prompt := buildCodingRepairPrompt(
		"make Value return 42",
		"implemented Value",
		CodeChangeSet{Files: []CodeChangedFile{{Path: "value.go", Status: "M"}}},
		receipt,
	)
	for _, want := range []string{
		"[CODING VERIFICATION FAILED]",
		"make Value return 42",
		"value.go",
		"go test ./pkg",
		"expected 42, got 0",
		"one repair attempt",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing %q:\n%s", want, prompt)
		}
	}
}

func writeVerificationGoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/verification\n\ngo 1.22\n",
		"value.go":      "package sample\n\nfunc Value() int {\n\treturn 42\n}\n",
		"value_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 42 {\n\t\tt.Fatalf(\"Value() = %d\", Value())\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runVerificationGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
