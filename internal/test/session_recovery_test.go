package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessioncore "mac/internal/session"
)

func newRunningRecoveryTranscript(t *testing.T, workspace, taskID string) *SessionTranscript {
	t.Helper()
	dir := sessioncore.TranscriptDirectory(workspace)
	transcript := NewSessionTranscript(taskID, "Researcher", dir)
	transcript.RecordUserMessage("生成报告并保存")
	transcript.RecordToolCall("write_file", map[string]interface{}{"path": "report.md"}, "call-write")
	transcript.RecordToolResult("write_file", "saved report.md", true, 25, "call-write")
	transcript.RecordToolCall("cli", map[string]interface{}{"command": "verify-report"}, "call-verify")
	if err := transcript.RecordTurnCheckpoint(&TurnCheckpoint{
		Input:         "生成报告并保存",
		Phase:         turnCheckpointPhaseToolsPending,
		Iteration:     3,
		MaxIterations: 20,
		Messages: []map[string]interface{}{
			{"role": "system", "content": "system"},
			{"role": "user", "content": "生成报告并保存"},
			BuildAssistantAPIMessage("", []ToolCall{{
				ID: "call-verify", Name: "cli", Arguments: map[string]interface{}{"command": "verify-report"},
			}}),
		},
		PendingToolCalls: checkpointToolCalls([]ToolCall{{
			ID: "call-verify", Name: "cli", Arguments: map[string]interface{}{"command": "verify-report"},
		}}),
		LastCompletedToolCallID: "call-write",
	}); err != nil {
		t.Fatal(err)
	}
	return transcript
}

func seedInterruptedRecoveryTask(t *testing.T, gateway *GatewayService, taskID, sessionID string) {
	t.Helper()
	now := time.Now().Add(-time.Minute)
	record := GatewayTaskRecord{
		TaskID:    taskID,
		SessionID: sessionID,
		UserID:    "user-1",
		TaskType:  "chat",
		Status:    "running",
		Message:   "生成报告并保存",
		AgentName: "Researcher",
		Channel:   "web",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := gateway.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.saveChatMessage(sessionID, "user", "Researcher", record.Message, "running", taskID, "web", "user-1"); err != nil {
		t.Fatal(err)
	}
	if recovered, err := recoverOrphanGatewayTasks(gateway.db, time.Now()); err != nil || recovered != 1 {
		t.Fatalf("recover orphan = %d, %v", recovered, err)
	}
}

func TestIndexInterruptedSessionJournalCreatesExplicitSafeResumePoint(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	taskID := "recovery-session-t1"
	seedInterruptedRecoveryTask(t, gateway, taskID, "recovery-session")
	transcript := newRunningRecoveryTranscript(t, workspace, taskID)

	indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger)
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("indexed = %d, want 1", indexed)
	}
	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.RecoveryState != sessioncore.RecoveryStateAvailable {
		t.Fatalf("indexed task = %#v", record)
	}
	if record.RecoveryJournal != transcript.JournalPath() || record.RecoveryEntryCount != 4 {
		t.Fatalf("journal metadata missing: %#v", record)
	}

	recoveries, err := gateway.listInterruptedTaskRecoveries("recovery-session", 10)
	if err != nil || len(recoveries) != 1 || !recoveries[0].CanResume {
		t.Fatalf("recoveries = %#v, err=%v", recoveries, err)
	}
	encoded, _ := json.Marshal(recoveries[0])
	if strings.Contains(string(encoded), workspace) || strings.Contains(string(encoded), "recovery_journal") {
		t.Fatalf("recovery API metadata leaked server path: %s", encoded)
	}

	prompt := sessioncore.BuildRecoveryPrompt(record.Message, transcript, "请优先检查现有报告")
	for _, required := range []string{
		"不要盲目重放", "先检查当前文件", "saved report.md", "call-verify", "执行状态未知", "请优先检查现有报告",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("recovery prompt missing %q:\n%s", required, prompt)
		}
	}
}

func TestIndexInterruptedSessionJournalRestoresDurableFinalAnswer(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	taskID := "completed-window-t1"
	seedInterruptedRecoveryTask(t, gateway, taskID, "completed-window")
	transcript := NewSessionTranscript(taskID, "Researcher", sessioncore.TranscriptDirectory(workspace))
	transcript.RecordUserMessage("完成报告")
	transcript.RecordAssistantMessage("报告已经完成")
	if err := transcript.Finalize("最终报告内容", "success"); err != nil {
		t.Fatal(err)
	}

	indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger)
	if err != nil || indexed != 1 {
		t.Fatalf("index terminal journal = %d, %v", indexed, err)
	}
	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "completed" || record.ErrorMsg != "" || record.RecoveryState != sessioncore.RecoveryStateRecoveredComplete {
		t.Fatalf("terminal journal was not reconciled: %#v", record)
	}
	if record.Result != `"最终报告内容"` {
		t.Fatalf("recovered result = %q", record.Result)
	}
	var assistant ChatMessage
	if err := gateway.db.Where("task_id = ? AND role = ?", taskID, "assistant").First(&assistant).Error; err != nil {
		t.Fatal(err)
	}
	if assistant.Content != "最终报告内容" || assistant.Status != "completed" {
		t.Fatalf("restored assistant message = %#v", assistant)
	}
}

func TestIndexInterruptedSessionJournalReconstructsMissingTaskRecord(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	taskID := "missing-record-t1"
	if _, err := gateway.saveChatMessage("missing-record", "user", "Researcher", "继续长任务", "pending", taskID, "web", "user-1"); err != nil {
		t.Fatal(err)
	}
	newRunningRecoveryTranscript(t, workspace, taskID)

	indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger)
	if err != nil || indexed != 1 {
		t.Fatalf("index missing-record crash window = %d, %v", indexed, err)
	}
	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.RecoveryState != sessioncore.RecoveryStateAvailable || record.SessionID != "missing-record" || record.Message != "继续长任务" {
		t.Fatalf("reconstructed record = %#v", record)
	}
}

type recoveryCaptureAgent struct {
	input  chan string
	result string
}

type recoveryContextCaptureAgent struct {
	state  chan turnResumeState
	result string
}

func (a *recoveryContextCaptureAgent) Name() string       { return "Researcher" }
func (a *recoveryContextCaptureAgent) GetType() AgentType { return AgentTypeCustom }
func (a *recoveryContextCaptureAgent) Generate(ctx context.Context, _ string) (string, error) {
	state, _ := turnResumeStateFromContext(ctx)
	a.state <- state
	return a.result, nil
}

func (a *recoveryCaptureAgent) Name() string       { return "Researcher" }
func (a *recoveryCaptureAgent) GetType() AgentType { return AgentTypeCustom }
func (a *recoveryCaptureAgent) Generate(_ context.Context, input string) (string, error) {
	a.input <- input
	return a.result, nil
}

func TestResumeInterruptedTaskContinuesAsNewIdempotentTask(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	gateway.config = &Config{Workspace: workspace}
	agent := &recoveryCaptureAgent{input: make(chan string, 1), result: "恢复后完成"}
	gateway.engine = &MultiAgentEngine{agentFactory: &AgentFactory{agents: map[string]Agent{"Researcher": agent}}}
	taskID := "resume-session-t1"
	seedInterruptedRecoveryTask(t, gateway, taskID, "resume-session")
	resolvedWorkspace := normalizeProjectWorkspace(workspace)
	if err := gateway.db.Model(&GatewayTaskRecord{}).Where("task_id = ?", taskID).Update("resolved_workspace", resolvedWorkspace).Error; err != nil {
		t.Fatal(err)
	}
	newRunningRecoveryTranscript(t, workspace, taskID)
	if indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger); err != nil || indexed != 1 {
		t.Fatalf("index resume journal = %d, %v", indexed, err)
	}

	resumed, err := gateway.resumeInterruptedTask(taskID, "保持原文件名")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != "resume-session-t2" || resumed.ResumedFromTaskID != taskID {
		t.Fatalf("resumed task = %#v", resumed)
	}
	if resumed.ResolvedWorkspace != resolvedWorkspace || resumed.Request == nil || resumed.Request.ResolvedWorkspace != resolvedWorkspace {
		t.Fatalf("resumed workspace was not preserved: %#v", resumed)
	}
	bootstrap, err := RecoverSessionTranscriptFromJournal(resumed.RecoveryJournal)
	if err != nil {
		t.Fatalf("recover resumed bootstrap checkpoint: %v", err)
	}
	if bootstrap.SessionID != resumed.ID || bootstrap.Checkpoint == nil || bootstrap.Checkpoint.ReceiptTaskID != taskID {
		t.Fatalf("resumed bootstrap checkpoint = %#v", bootstrap)
	}
	select {
	case prompt := <-agent.input:
		for _, required := range []string{"生成报告并保存", "saved report.md", "保持原文件名", "不要盲目重放"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("agent recovery input missing %q: %s", required, prompt)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resumed task did not reach agent")
	}
	select {
	case raw := <-resumed.ResultCh:
		response, ok := raw.(GatewayResponse)
		if !ok || response.Status != "completed" || response.Result != "恢复后完成" {
			t.Fatalf("resume response = %#v", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resumed task did not complete")
	}

	var original GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", taskID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if original.RecoveryState != sessioncore.RecoveryStateResumed || original.ResumedByTaskID != resumed.ID {
		t.Fatalf("original recovery state = %#v", original)
	}
	var continued GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", resumed.ID).First(&continued).Error; err != nil {
		t.Fatal(err)
	}
	if continued.Status != "completed" || continued.ResumedFromTaskID != taskID || continued.ResolvedWorkspace != resolvedWorkspace {
		t.Fatalf("continued task record = %#v", continued)
	}

	if _, err := gateway.resumeInterruptedTask(taskID, "again"); !errors.Is(err, errInterruptedTaskNotResumable) {
		t.Fatalf("second resume should be rejected idempotently, got %v", err)
	}
	var visible ChatMessage
	if err := gateway.db.Where("task_id = ? AND role = ?", resumed.ID, "user").First(&visible).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(visible.Content, "<recovery_log") || !strings.Contains(visible.Content, "保持原文件名") {
		t.Fatalf("visible recovery message should stay concise: %q", visible.Content)
	}
}

func TestResumeInterruptedTaskInjectsStructuredTurnCheckpoint(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	gateway.config = &Config{Workspace: workspace}
	agent := &recoveryContextCaptureAgent{state: make(chan turnResumeState, 1), result: "done"}
	gateway.engine = &MultiAgentEngine{agentFactory: &AgentFactory{agents: map[string]Agent{"Researcher": agent}}}
	taskID := "checkpoint-resume-t1"
	seedInterruptedRecoveryTask(t, gateway, taskID, "checkpoint-resume")
	newRunningRecoveryTranscript(t, workspace, taskID)
	if indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger); err != nil || indexed != 1 {
		t.Fatalf("index checkpoint journal = %d, %v", indexed, err)
	}

	resumed, err := gateway.resumeInterruptedTask(taskID, "保留验证命令")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-agent.state:
		if state.Checkpoint == nil || state.SourceTaskID != taskID || state.Supplement != "保留验证命令" {
			t.Fatalf("resume context = %+v", state)
		}
		pending := state.Checkpoint.PendingCalls()
		if state.Checkpoint.Iteration != 3 || len(pending) != 1 || pending[0].ID != "call-verify" {
			t.Fatalf("structured checkpoint position = %+v", state.Checkpoint)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resumed task did not receive structured checkpoint")
	}
	select {
	case <-resumed.ResultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("resumed checkpoint task did not complete")
	}
}

func TestIndexInterruptedResumedTaskUsesBootstrapCheckpoint(t *testing.T) {
	gateway := newTaskLifecycleTestGateway(t)
	workspace := t.TempDir()
	newTaskID := "resume-bootstrap-t2"
	checkpoint := &TurnCheckpoint{
		TaskID: "resume-bootstrap-t1", ReceiptTaskID: "resume-bootstrap-t1",
		Input: "continue work", Phase: turnCheckpointPhaseToolsPending,
		Iteration: 5, MaxIterations: 20,
		Messages:         []map[string]interface{}{{"role": "user", "content": "continue work"}},
		PendingToolCalls: checkpointToolCalls([]ToolCall{{ID: "call-next", Name: "read_file", Arguments: map[string]interface{}{"path": "README.md"}}}),
	}
	bootstrap := NewSessionTranscript(newTaskID, "Researcher", sessioncore.TranscriptDirectory(workspace))
	if err := bootstrap.RecordTurnCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	if err := gateway.db.Create(&GatewayTaskRecord{
		TaskID: newTaskID, SessionID: "resume-bootstrap", UserID: "user-1",
		TaskType: "chat", Status: "running", Message: "continue work",
		AgentName: "Researcher", Channel: "web", ResumedFromTaskID: "resume-bootstrap-t1",
		RecoveryJournal: bootstrap.JournalPath(), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if recovered, err := recoverOrphanGatewayTasks(gateway.db, time.Now()); err != nil || recovered != 1 {
		t.Fatalf("recover resumed orphan = %d, %v", recovered, err)
	}
	if indexed, err := indexInterruptedSessionJournals(gateway.db, workspace, gateway.logger); err != nil || indexed != 1 {
		t.Fatalf("index resumed bootstrap = %d, %v", indexed, err)
	}
	var record GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", newTaskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.RecoveryState != sessioncore.RecoveryStateAvailable || !strings.Contains(record.RecoveryPreview, "第 5 轮") {
		t.Fatalf("bootstrap recovery record = %+v", record)
	}
}

func TestRecoveryCandidatePrefersBootstrapCheckpointOverNewerEmptyJournal(t *testing.T) {
	now := time.Now()
	bootstrap := recoveredJournalCandidate{
		path: "bootstrap.jsonl",
		transcript: &SessionTranscript{
			Status: "running",
			Checkpoint: &TurnCheckpoint{
				TaskID: "task", Phase: turnCheckpointPhaseToolsPending, Iteration: 4,
			},
		},
		activity: now,
	}
	newerEmpty := recoveredJournalCandidate{
		path:       "newer.jsonl",
		transcript: &SessionTranscript{Status: "running"},
		activity:   now.Add(time.Second),
	}
	if preferRecoveredJournalCandidate(bootstrap, newerEmpty) {
		t.Fatal("newer session-start-only journal displaced a usable bootstrap checkpoint")
	}
	newerCheckpoint := newerEmpty
	newerCheckpoint.transcript = &SessionTranscript{
		Status: "running",
		Checkpoint: &TurnCheckpoint{
			TaskID: "task", Phase: turnCheckpointPhaseAfterTools, Iteration: 5,
		},
	}
	if !preferRecoveredJournalCandidate(bootstrap, newerCheckpoint) {
		t.Fatal("newer progressed checkpoint was not preferred")
	}
}

func TestRecoverSessionTranscriptRejectsOversizedJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((64 << 20) + 1); err != nil {
		file.Close()
		t.Skipf("filesystem does not support sparse test file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = RecoverSessionTranscriptFromJournal(path)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized journal error = %v", err)
	}
}

func TestResumeRecoveryPromptStaysBounded(t *testing.T) {
	transcript := &SessionTranscript{Entries: make([]TranscriptEntry, 0, 200)}
	for i := 0; i < 200; i++ {
		transcript.Entries = append(transcript.Entries, TranscriptEntry{Type: "tool_result", ToolName: "read", ToolResult: strings.Repeat("x", 2000)})
	}
	prompt := sessioncore.BuildRecoveryPrompt(strings.Repeat("m", 10000), transcript, strings.Repeat("s", 10000))
	if got := len([]rune(prompt)); got > 18001 {
		t.Fatalf("recovery prompt has %d runes, want <= 18001", got)
	}
	if !strings.Contains(prompt, "恢复规则") {
		t.Fatalf("bounded recovery prompt lost safety preface: %s", prompt[:min(len(prompt), 200)])
	}
}

func Example() {
	prompt := sessioncore.BuildRecoveryPrompt(
		"finish the report",
		&SessionTranscript{Entries: []TranscriptEntry{{Type: "tool_call", ToolName: "write_file", ToolCallID: "pending"}}},
		"",
	)
	fmt.Println(strings.Contains(prompt, "先检查当前文件"))
	// Output: true
}
