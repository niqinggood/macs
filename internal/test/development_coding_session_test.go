package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mac/internal/projectctx"
)

func mustWriteCodingProjectInstruction(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type codeSessionFakeAgent struct {
	contexts []context.Context
	inputs   []string
}

func (agent *codeSessionFakeAgent) Name() string       { return "fake" }
func (agent *codeSessionFakeAgent) GetType() AgentType { return AgentTypeCustom }
func (agent *codeSessionFakeAgent) Generate(ctx context.Context, input string) (string, error) {
	agent.contexts = append(agent.contexts, ctx)
	agent.inputs = append(agent.inputs, input)
	return "response: " + input, nil
}

func TestParseCodeCommandArgs(t *testing.T) {
	options, err := parseCodeCommandArgs([]string{
		"--path", "repo",
		"--config", "config.yaml",
		"--agent", "coder",
		"--resume", "latest",
		"--worktree",
		"fix", "the", "tests",
	})
	if err != nil {
		t.Fatalf("parse code args: %v", err)
	}
	if options.Workspace != "repo" || options.ConfigPath != "config.yaml" ||
		options.Agent != "coder" || options.Resume != "latest" || !options.Worktree {
		t.Fatalf("parsed options = %+v", options)
	}
	if options.Prompt != "fix the tests" || options.OneShot {
		t.Fatalf("positional task = %q, one-shot=%t", options.Prompt, options.OneShot)
	}

	oneShot, err := parseCodeCommandArgs([]string{"--prompt", "review this"})
	if err != nil {
		t.Fatalf("parse one-shot args: %v", err)
	}
	if oneShot.Prompt != "review this" || !oneShot.OneShot {
		t.Fatalf("one-shot options = %+v", oneShot)
	}
}

func TestParseCodeCommandArgsRejectsUnknownOrMissingValues(t *testing.T) {
	if _, err := parseCodeCommandArgs([]string{"--unknown"}); err == nil {
		t.Fatal("unknown code option should fail")
	}
	if _, err := parseCodeCommandArgs([]string{"--agent"}); err == nil {
		t.Fatal("missing option value should fail")
	}
}

func TestCodeModeRequestedAndWindowsPauseRouting(t *testing.T) {
	if !codeModeRequested([]string{"code"}) || codeModeRequested([]string{"serve"}) {
		t.Fatal("code subcommand detection failed")
	}
	if shouldPauseOnWindows([]string{"openkitty", "code"}) {
		t.Fatal("code mode must never wait for the Windows double-click pause")
	}
}

func TestCodeSessionSaveLoadAndLatest(t *testing.T) {
	root := t.TempDir()
	first := &CodeSessionState{
		Version:       codeSessionStateVersion,
		ID:            "code-first",
		RootWorkspace: root,
		Workspace:     root,
		Agent:         "coder",
		CreatedAt:     time.Now(),
		Messages: []CodeSessionMessage{
			{Role: "user", Content: "hello", Time: time.Now()},
		},
	}
	if err := saveCodeSession(first); err != nil {
		t.Fatalf("save first session: %v", err)
	}
	firstPath := codeSessionPath(root, first.ID)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(firstPath, oldTime, oldTime); err != nil {
		t.Fatalf("age first session: %v", err)
	}

	second := &CodeSessionState{
		Version:       codeSessionStateVersion,
		ID:            "code-second",
		RootWorkspace: root,
		Workspace:     root,
		Agent:         "coder",
		CreatedAt:     time.Now(),
	}
	if err := saveCodeSession(second); err != nil {
		t.Fatalf("save second session: %v", err)
	}

	loaded, err := loadCodeSession(root, "code-first")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ID != first.ID || len(loaded.Messages) != 1 || loaded.Messages[0].Content != "hello" {
		t.Fatalf("loaded state = %+v", loaded)
	}
	latest, err := loadCodeSession(root, "latest")
	if err != nil {
		t.Fatalf("load latest session: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("latest session = %q, want %q", latest.ID, second.ID)
	}
}

func TestFindCodeConfigPrefersSelectedWorkspace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "openkitty-config.yaml")
	if err := os.WriteFile(path, []byte("models: {}"), 0644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	found, err := findCodeConfig(root, "")
	if err != nil {
		t.Fatalf("find workspace config: %v", err)
	}
	if found != path {
		t.Fatalf("config path = %q, want %q", found, path)
	}
}

func TestCodingSessionRunTurnPersistsHistoryAndContext(t *testing.T) {
	root := t.TempDir()
	state := &CodeSessionState{
		Version:       codeSessionStateVersion,
		ID:            "code-test",
		RootWorkspace: root,
		Workspace:     root,
		Agent:         "fake",
		CreatedAt:     time.Now(),
	}
	fake := &codeSessionFakeAgent{}
	var output bytes.Buffer
	session := &CodingSession{
		state:  state,
		agent:  fake,
		engine: &MultiAgentEngine{},
		output: &output,
		errors: &output,
	}

	if err := session.runTurn("inspect repository"); err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if state.Turn != 1 || len(state.Messages) != 2 {
		t.Fatalf("session state after turn = %+v", state)
	}
	if len(fake.contexts) != 1 ||
		fake.contexts[0].Value("session_id") != state.ID ||
		resolvedWorkspaceFromContext(fake.contexts[0]) != normalizeProjectWorkspace(root) {
		t.Fatalf("agent context was not bound to coding session")
	}
	if !strings.Contains(output.String(), "response: inspect repository") {
		t.Fatalf("turn output missing response:\n%s", output.String())
	}
	loaded, err := loadCodeSession(root, state.ID)
	if err != nil {
		t.Fatalf("reload persisted turn: %v", err)
	}
	if loaded.Turn != 1 || len(loaded.Messages) != 2 {
		t.Fatalf("persisted state = %+v", loaded)
	}
}

func TestCodingSessionInstructionCommandShowsAuditAndContent(t *testing.T) {
	root := t.TempDir()
	mustWriteCodingProjectInstruction(t, filepath.Join(root, "AGENTS.md"), "instruction command content")
	var output bytes.Buffer
	session := &CodingSession{
		state: &CodeSessionState{
			ID:            "code-test",
			RootWorkspace: root,
			Workspace:     root,
			Agent:         "fake",
		},
		llm: &LLMAgent{
			workspace:           root,
			projectInstructions: projectctx.NewProjectInstructionLoader(""),
		},
		output: &output,
		errors: &output,
	}
	handled, exit := session.handleCommand("/instructions full")
	if !handled || exit {
		t.Fatalf("instruction command handled=%t exit=%t", handled, exit)
	}
	for _, want := range []string{"AGENTS.md", "instruction command content", "Fingerprint"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("instruction command missing %q:\n%s", want, output.String())
		}
	}
}

func TestCodingSessionAgentsCommandShowsRetainedStatus(t *testing.T) {
	root := t.TempDir()
	manager := NewSubAgentTool(nil, root, nil, nil)
	manager.activeAgents["reviewer"] = &subAgentSession{
		id:        "reviewer",
		task:      "review the current implementation carefully",
		startTime: time.Now().Add(-time.Second),
		endTime:   time.Now(),
		status:    "completed",
		result:    "review complete",
	}
	registry := NewToolRegistry()
	registry.Register(NewAgentStatusTool(manager))

	var output bytes.Buffer
	session := &CodingSession{
		state:  &CodeSessionState{ID: "code-agents", RootWorkspace: root, Workspace: root},
		llm:    &LLMAgent{toolRegistry: registry},
		output: &output,
		errors: &output,
	}
	handled, exit := session.handleCommand("/agents full")
	if !handled || exit {
		t.Fatalf("agents command handled=%t exit=%t", handled, exit)
	}
	for _, want := range []string{`"status": "completed"`, "review complete"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("agents command missing %q:\n%s", want, output.String())
		}
	}
}

func TestCodingSessionUndoRewindsFilesAndConversationBranch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.txt")
	os.WriteFile(path, []byte("before"), 0644)
	manager := NewSnapshotManager(root, "code-undo")
	snapshot, err := manager.SaveSnapshot(path, "edit_file")
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	os.WriteFile(path, []byte("after"), 0644)
	manager.FinalizeSnapshots([]*FileSnapshot{snapshot})
	manager.EndTurn()

	state := &CodeSessionState{
		Version:       codeSessionStateVersion,
		ID:            "code-undo",
		RootWorkspace: root,
		Workspace:     root,
		Agent:         "fake",
		Turn:          1,
		Messages: []CodeSessionMessage{
			{Role: "user", Content: "change it"},
			{Role: "assistant", Content: "done"},
		},
		ChangeSets: []CodeChangeSet{{Turn: 1}},
		Verifications: []CodeVerificationReceipt{{
			Turn:   1,
			Status: codeCheckPassed,
		}},
	}
	var output bytes.Buffer
	session := &CodingSession{
		state: state,
		llm: &LLMAgent{
			snapshotManager: manager,
		},
		output: &output,
		errors: &output,
	}
	session.undo("")

	data, _ := os.ReadFile(path)
	if string(data) != "before" {
		t.Fatalf("undo content = %q", data)
	}
	if state.Turn != 0 || len(state.Messages) != 0 {
		t.Fatalf("undo should branch conversation at turn 0: %+v", state)
	}
	if len(state.ChangeSets) != 0 || len(state.Verifications) != 0 {
		t.Fatalf("undo should trim change/verification receipts: %+v", state)
	}
}
