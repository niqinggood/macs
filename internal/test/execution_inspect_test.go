package app

import (
	"path/filepath"
	"testing"
	"time"

	executioninspectcore "mac/internal/executioninspect"
	sessioncore "mac/internal/session"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type ExecutionReceiptCoverage = executioninspectcore.ExecutionReceiptCoverage
type ExecutionInspection = executioninspectcore.ExecutionInspection

var InspectExecution = executioninspectcore.InspectExecution

const (
	toolRecoveryNotStarted           = "not_started"
	toolRecoveryReceiptSucceeded     = "receipt_succeeded"
	toolRecoveryRetryableReadOnly    = "retryable_read_only"
	toolRecoveryRecordedResult       = "recorded_result"
	toolRecoveryOutcomeUnknown       = "outcome_unknown"
	toolRecoveryInconsistentEvidence = "inconsistent_evidence"

	executionDispositionSafeToResume           = "safe_to_resume"
	executionDispositionRequiresReconciliation = "requires_reconciliation"
	executionDispositionBlocked                = "blocked"
)

func TestInspectExecutionRecoveryClassifications(t *testing.T) {
	call := TurnCheckpointToolCall{
		ID: "call-1", Name: "lookup", Arguments: map[string]interface{}{"key": "value"},
	}
	checkpoint := &TurnCheckpoint{
		Version: turnCheckpointVersion, Revision: 4, TaskID: "task-1", ReceiptTaskID: "task-1",
		Phase: turnCheckpointPhaseToolsPending, Iteration: 2, MaxIterations: 20,
		PendingToolCalls: []TurnCheckpointToolCall{call},
	}
	callEntry := TranscriptEntry{Type: "tool_call", ToolCallID: call.ID, ToolName: call.Name, ToolParams: call.Arguments}

	t.Run("authoritative receipt absence proves tool body did not start", func(t *testing.T) {
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, nil, ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "task-1"})
		assertSingleToolRecovery(t, report, toolRecoveryNotStarted, true)
		if report.Disposition != executionDispositionSafeToResume || !report.AutoResumeSafe {
			t.Fatalf("disposition = %q safe=%v", report.Disposition, report.AutoResumeSafe)
		}
	})

	t.Run("non-authoritative receipt absence remains unknown", func(t *testing.T) {
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, nil, ExecutionReceiptCoverage{TaskID: "task-1"})
		assertSingleToolRecovery(t, report, toolRecoveryOutcomeUnknown, false)
		if report.Disposition != executionDispositionBlocked || report.AutoResumeSafe {
			t.Fatalf("disposition = %q safe=%v", report.Disposition, report.AutoResumeSafe)
		}
	})

	t.Run("checkpoint intent without authoritative receipts remains unknown", func(t *testing.T) {
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Checkpoint: checkpoint,
		}, nil, ExecutionReceiptCoverage{})
		assertSingleToolRecovery(t, report, toolRecoveryOutcomeUnknown, false)
	})

	t.Run("interrupted read-only receipt is retryable", func(t *testing.T) {
		receipt := executionInspectorReceipt(t, checkpoint, call, "interrupted", true)
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, []ToolExecutionReceipt{receipt}, ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "task-1", ReceiptCount: 1})
		assertSingleToolRecovery(t, report, toolRecoveryRetryableReadOnly, true)
		if report.PendingTools[0].ReadOnly == nil || !*report.PendingTools[0].ReadOnly {
			t.Fatal("read-only evidence was not preserved")
		}
	})

	t.Run("interrupted mutating receipt is blocked", func(t *testing.T) {
		receipt := executionInspectorReceipt(t, checkpoint, call, "interrupted", false)
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, []ToolExecutionReceipt{receipt}, ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "task-1", ReceiptCount: 1})
		assertSingleToolRecovery(t, report, toolRecoveryOutcomeUnknown, false)
		if report.Disposition != executionDispositionBlocked || report.AutoResumeSafe {
			t.Fatalf("mutating call disposition = %q safe=%v", report.Disposition, report.AutoResumeSafe)
		}
	})

	t.Run("successful receipt is reusable", func(t *testing.T) {
		receipt := executionInspectorReceipt(t, checkpoint, call, "succeeded", false)
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, []ToolExecutionReceipt{receipt}, ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "task-1", ReceiptCount: 1})
		assertSingleToolRecovery(t, report, toolRecoveryReceiptSucceeded, true)
	})

	t.Run("recorded result with stale pending checkpoint requires reconciliation", func(t *testing.T) {
		succeeded := true
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Checkpoint: checkpoint,
			Entries: []TranscriptEntry{callEntry, {
				Type: "tool_result", ToolCallID: call.ID, ToolName: call.Name, ToolSuccess: &succeeded,
			}},
		}, nil, ExecutionReceiptCoverage{TaskID: "task-1"})
		assertSingleToolRecovery(t, report, toolRecoveryRecordedResult, false)
		if report.Disposition != executionDispositionRequiresReconciliation {
			t.Fatalf("disposition = %q", report.Disposition)
		}
		if !executionInspectionHasIssue(report, "checkpoint_lags_recorded_result") {
			t.Fatalf("issues = %#v", report.Issues)
		}
	})

	t.Run("argument hash mismatch fails closed", func(t *testing.T) {
		receipt := executionInspectorReceipt(t, checkpoint, call, "succeeded", true)
		receipt.ArgumentsHash = "different"
		report := InspectExecution(&SessionTranscript{
			SessionID: "task-1", Status: "running", Entries: []TranscriptEntry{callEntry}, Checkpoint: checkpoint,
		}, []ToolExecutionReceipt{receipt}, ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "task-1", ReceiptCount: 1})
		assertSingleToolRecovery(t, report, toolRecoveryInconsistentEvidence, false)
	})
}

func TestInspectExecutionReportsTranscriptInvariants(t *testing.T) {
	succeeded := true
	transcript := &SessionTranscript{
		SessionID: "bad-transcript", Status: "running",
		Entries: []TranscriptEntry{
			{Type: "tool_result", ToolCallID: "orphan", ToolName: "read", ToolSuccess: &succeeded},
			{Type: "tool_call", ToolCallID: "dupe", ToolName: "read"},
			{Type: "tool_call", ToolCallID: "dupe", ToolName: "write"},
			{Type: "tool_result", ToolCallID: "dupe", ToolName: "other", ToolSuccess: &succeeded},
		},
		Checkpoint: &TurnCheckpoint{
			Version: turnCheckpointVersion, TaskID: "bad-transcript",
			Phase:            turnCheckpointPhaseModelPending,
			PendingToolCalls: []TurnCheckpointToolCall{{ID: "dupe", Name: "read"}},
		},
	}
	report := InspectExecution(transcript, nil, ExecutionReceiptCoverage{})
	for _, code := range []string{
		"duplicate_tool_call",
		"pending_calls_outside_tools_phase",
		"tool_result_without_call",
		"transcript_tool_name_mismatch",
	} {
		if !executionInspectionHasIssue(report, code) {
			t.Errorf("missing issue %q in %#v", code, report.Issues)
		}
	}
	if report.Summary.Errors < 4 {
		t.Fatalf("error count = %d, issues=%#v", report.Summary.Errors, report.Issues)
	}
}

func TestLoadExecutionInspectionReceiptsReadOnly(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "openkitty.db")
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&GatewayTaskRecord{}, &ToolExecutionReceipt{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0)
	if err := database.Create(&GatewayTaskRecord{TaskID: "task-read-only", Status: "failed", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&ToolExecutionReceipt{
		TaskID: "task-read-only", ToolCallID: "call-1", ToolName: "lookup",
		ArgumentsHash: "hash", ReadOnly: true, Status: "interrupted", StartedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := database.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			t.Fatal(err)
		}
	}

	receipts, coverage := loadExecutionInspectionReceipts(databasePath, "task-read-only")
	if !coverage.Available || !coverage.Authoritative || coverage.ReceiptCount != 1 {
		t.Fatalf("coverage = %#v", coverage)
	}
	if len(receipts) != 1 || receipts[0].ToolCallID != "call-1" {
		t.Fatalf("receipts = %#v", receipts)
	}

	missingReceipts, missingCoverage := loadExecutionInspectionReceipts(databasePath, "not-a-task")
	if !missingCoverage.Available || missingCoverage.Authoritative || len(missingReceipts) != 0 {
		t.Fatalf("missing coverage = %#v receipts=%#v", missingCoverage, missingReceipts)
	}
}

func TestResolveExecutionInspectionJournalByTask(t *testing.T) {
	workspace := t.TempDir()
	transcript := NewSessionTranscript("inspect-task", "Researcher", sessioncore.TranscriptDirectory(workspace))
	transcript.RecordUserMessage("inspect me")
	options := executionInspectCommandOptions{TaskID: "inspect-task", Workspace: workspace, NoReceipts: true}
	journal, resolvedWorkspace, err := resolveExecutionInspectionJournal(options)
	if err != nil {
		t.Fatal(err)
	}
	if journal != transcript.JournalPath() || resolvedWorkspace != filepath.Clean(workspace) {
		t.Fatalf("journal=%q workspace=%q", journal, resolvedWorkspace)
	}
	if inferred, ok := inferExecutionWorkspaceFromJournal(journal); !ok || inferred != filepath.Clean(workspace) {
		t.Fatalf("inferred workspace=%q ok=%v", inferred, ok)
	}
}

func TestParseExecutionInspectCommandArgs(t *testing.T) {
	options, err := parseExecutionInspectCommandArgs([]string{"--task", "task-1", "-C", "work", "--compact"})
	if err != nil {
		t.Fatal(err)
	}
	if options.TaskID != "task-1" || options.Workspace != "work" || !options.Compact {
		t.Fatalf("options = %#v", options)
	}
	if _, err := parseExecutionInspectCommandArgs(nil); err == nil {
		t.Fatal("missing journal/task should fail")
	}
	if _, err := parseExecutionInspectCommandArgs([]string{"--journal", "x", "--db", "db", "--no-receipts"}); err == nil {
		t.Fatal("conflicting DB options should fail")
	}
}

func executionInspectorReceipt(t *testing.T, checkpoint *TurnCheckpoint, call TurnCheckpointToolCall, status string, readOnly bool) ToolExecutionReceipt {
	t.Helper()
	hash, err := toolCallArgumentsHash(ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	if err != nil {
		t.Fatal(err)
	}
	return ToolExecutionReceipt{
		TaskID: checkpoint.ReceiptTaskID, ToolCallID: call.ID, ToolName: call.Name,
		ArgumentsHash: hash, ReadOnly: readOnly, Status: status,
	}
}

func assertSingleToolRecovery(t *testing.T, report ExecutionInspection, state string, safe bool) {
	t.Helper()
	if len(report.PendingTools) != 1 {
		t.Fatalf("pending tools = %#v", report.PendingTools)
	}
	if report.PendingTools[0].State != state || report.PendingTools[0].AutoResumeSafe != safe {
		t.Fatalf("assessment = %#v, want state=%q safe=%v", report.PendingTools[0], state, safe)
	}
}

func executionInspectionHasIssue(report ExecutionInspection, code string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func BenchmarkInspectExecutionHundredToolCalls(b *testing.B) {
	entries := make([]TranscriptEntry, 0, 200)
	pending := make([]TurnCheckpointToolCall, 0, 100)
	receipts := make([]ToolExecutionReceipt, 0, 100)
	succeeded := true
	for index := 0; index < 100; index++ {
		call := TurnCheckpointToolCall{
			ID:   "call-" + string(rune('a'+index%26)) + string(rune('a'+index/26)),
			Name: "lookup", Arguments: map[string]interface{}{"index": index},
		}
		entries = append(entries,
			TranscriptEntry{Type: "tool_call", ToolCallID: call.ID, ToolName: call.Name},
			TranscriptEntry{Type: "tool_result", ToolCallID: call.ID, ToolName: call.Name, ToolSuccess: &succeeded},
		)
		pending = append(pending, call)
		hash, _ := toolCallArgumentsHash(ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
		receipts = append(receipts, ToolExecutionReceipt{
			TaskID: "benchmark", ToolCallID: call.ID, ToolName: call.Name,
			ArgumentsHash: hash, ReadOnly: true, Status: "succeeded",
		})
	}
	transcript := &SessionTranscript{
		SessionID: "benchmark", Status: "running", Entries: entries,
		Checkpoint: &TurnCheckpoint{
			Version: turnCheckpointVersion, TaskID: "benchmark", ReceiptTaskID: "benchmark",
			Phase: turnCheckpointPhaseToolsPending, PendingToolCalls: pending,
		},
	}
	coverage := ExecutionReceiptCoverage{Available: true, Authoritative: true, TaskID: "benchmark", ReceiptCount: len(receipts)}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = InspectExecution(transcript, receipts, coverage)
	}
}
