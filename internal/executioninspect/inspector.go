package executioninspect

import (
	"fmt"
	"sort"
	"strings"

	"mac/internal/agentos"
	"mac/internal/session"
	"mac/internal/toolcontract"
)

const executionInspectionVersion = 1

const (
	toolRecoveryNotStarted           = "not_started"
	toolRecoveryReceiptSucceeded     = "receipt_succeeded"
	toolRecoveryRetryableReadOnly    = "retryable_read_only"
	toolRecoveryRecordedResult       = "recorded_result"
	toolRecoveryOutcomeUnknown       = "outcome_unknown"
	toolRecoveryInconsistentEvidence = "inconsistent_evidence"
)

const (
	executionDispositionCompleted              = "completed"
	executionDispositionSafeToContinue         = "safe_to_continue"
	executionDispositionSafeToResume           = "safe_to_resume"
	executionDispositionRequiresReconciliation = "requires_reconciliation"
	executionDispositionBlocked                = "blocked"
	executionDispositionInsufficientEvidence   = "insufficient_evidence"
)

// ExecutionReceiptCoverage describes whether an absent receipt is meaningful.
// A successful database query alone is not authoritative: the task must also
// exist in the Gateway task table, proving that its tool executor was wired to
// the durable ledger.
type ExecutionReceiptCoverage struct {
	Available     bool   `json:"available"`
	Authoritative bool   `json:"authoritative"`
	TaskID        string `json:"task_id,omitempty"`
	Source        string `json:"source,omitempty"`
	ReceiptCount  int    `json:"receipt_count"`
	Detail        string `json:"detail,omitempty"`
}

// ToolRecoveryAssessment is a conservative, evidence-backed classification
// for one tool call still present in the durable checkpoint.
type ToolRecoveryAssessment struct {
	ToolCallID     string `json:"tool_call_id"`
	ToolName       string `json:"tool_name"`
	State          string `json:"state"`
	Action         string `json:"action"`
	AutoResumeSafe bool   `json:"auto_resume_safe"`
	ReadOnly       *bool  `json:"read_only,omitempty"`
	ReceiptStatus  string `json:"receipt_status,omitempty"`
	RecordedResult *bool  `json:"recorded_result_succeeded,omitempty"`
	Detail         string `json:"detail"`
}

// ExecutionInvariantIssue is produced only by explicit inspection. Runtime
// recording remains unchanged, so these checks add no work to the hot path.
type ExecutionInvariantIssue struct {
	Severity   string `json:"severity"`
	Code       string `json:"code"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Message    string `json:"message"`
}

// ExecutionInspection combines the transcript, checkpoint and execution
// receipts without exposing tool arguments or result content.
type ExecutionInspection struct {
	Version             int                        `json:"version"`
	SessionID           string                     `json:"session_id"`
	AgentName           string                     `json:"agent_name,omitempty"`
	TranscriptStatus    string                     `json:"transcript_status"`
	JournalPath         string                     `json:"journal_path,omitempty"`
	CheckpointPresent   bool                       `json:"checkpoint_present"`
	CheckpointPhase     string                     `json:"checkpoint_phase,omitempty"`
	CheckpointRevision  uint64                     `json:"checkpoint_revision,omitempty"`
	CheckpointIteration int                        `json:"checkpoint_iteration,omitempty"`
	ReceiptCoverage     ExecutionReceiptCoverage   `json:"receipt_coverage"`
	PendingTools        []ToolRecoveryAssessment   `json:"pending_tools"`
	Issues              []ExecutionInvariantIssue  `json:"issues"`
	Disposition         string                     `json:"disposition"`
	AutoResumeSafe      bool                       `json:"auto_resume_safe"`
	Summary             ExecutionInspectionSummary `json:"summary"`
}

type ExecutionInspectionSummary struct {
	TranscriptEntries int `json:"transcript_entries"`
	PendingTools      int `json:"pending_tools"`
	SafePendingTools  int `json:"safe_pending_tools"`
	Warnings          int `json:"warnings"`
	Errors            int `json:"errors"`
}

type executionTranscriptSnapshot struct {
	SessionID  string
	AgentName  string
	Status     string
	Journal    string
	Entries    []session.TranscriptEntry
	Checkpoint *session.TurnCheckpoint
}

type executionToolEvidence struct {
	call        *session.TranscriptEntry
	callIndex   int
	result      *session.TranscriptEntry
	resultIndex int
}

// InspectExecution is deliberately side-effect free. Callers decide when to
// load files or query receipts; ordinary agent execution never invokes it.
func InspectExecution(transcript *session.SessionTranscript, receipts []agentos.ToolExecutionReceipt, coverage ExecutionReceiptCoverage) ExecutionInspection {
	snapshot := snapshotExecutionTranscript(transcript)
	report := ExecutionInspection{
		Version:          executionInspectionVersion,
		SessionID:        snapshot.SessionID,
		AgentName:        snapshot.AgentName,
		TranscriptStatus: snapshot.Status,
		JournalPath:      snapshot.Journal,
		ReceiptCoverage:  coverage,
		PendingTools:     make([]ToolRecoveryAssessment, 0),
		Issues:           make([]ExecutionInvariantIssue, 0),
		Summary: ExecutionInspectionSummary{
			TranscriptEntries: len(snapshot.Entries),
		},
	}

	evidence, transcriptIssues := inspectTranscriptToolPairs(snapshot.Entries)
	report.Issues = append(report.Issues, transcriptIssues...)
	receiptByCall, receiptIssues := inspectReceiptSet(receipts, coverage.TaskID)
	report.Issues = append(report.Issues, receiptIssues...)

	checkpoint := snapshot.Checkpoint
	if checkpoint == nil {
		if strings.EqualFold(strings.TrimSpace(snapshot.Status), "running") || strings.TrimSpace(snapshot.Status) == "" {
			report.Issues = append(report.Issues, ExecutionInvariantIssue{
				Severity: "warning",
				Code:     "checkpoint_missing",
				Message:  "no durable turn checkpoint is available",
			})
		}
		report.Disposition, report.AutoResumeSafe = executionRecoveryDisposition(snapshot.Status, nil, nil)
		finalizeExecutionInspection(&report)
		return report
	}

	report.CheckpointPresent = true
	report.CheckpointPhase = checkpoint.Phase
	report.CheckpointRevision = checkpoint.Revision
	report.CheckpointIteration = checkpoint.Iteration
	report.Issues = append(report.Issues, inspectCheckpointInvariants(checkpoint, evidence)...)

	for _, pending := range checkpoint.PendingToolCalls {
		assessment := assessPendingToolRecovery(pending, evidence[pending.ID], receiptByCall[pending.ID], coverage)
		report.PendingTools = append(report.PendingTools, assessment)
		if assessment.AutoResumeSafe {
			report.Summary.SafePendingTools++
		}
	}
	report.Summary.PendingTools = len(report.PendingTools)

	report.Disposition, report.AutoResumeSafe = executionRecoveryDisposition(snapshot.Status, checkpoint, report.PendingTools)
	finalizeExecutionInspection(&report)
	return report
}

func snapshotExecutionTranscript(transcript *session.SessionTranscript) executionTranscriptSnapshot {
	if transcript == nil {
		return executionTranscriptSnapshot{}
	}
	snapshot := transcript.Snapshot()
	return executionTranscriptSnapshot{
		SessionID:  snapshot.SessionID,
		AgentName:  snapshot.AgentName,
		Status:     snapshot.Status,
		Journal:    snapshot.Journal,
		Entries:    snapshot.Entries,
		Checkpoint: snapshot.Checkpoint,
	}
}

func inspectTranscriptToolPairs(entries []session.TranscriptEntry) (map[string]executionToolEvidence, []ExecutionInvariantIssue) {
	evidence := make(map[string]executionToolEvidence)
	issues := make([]ExecutionInvariantIssue, 0)
	for index := range entries {
		entry := entries[index]
		if entry.Type != "tool_call" && entry.Type != "tool_result" {
			continue
		}
		callID := strings.TrimSpace(entry.ToolCallID)
		if callID == "" {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "warning",
				Code:     "legacy_tool_event_without_call_id",
				Message:  fmt.Sprintf("transcript entry %d (%s) has no tool_call_id", index, entry.Type),
			})
			continue
		}
		item := evidence[callID]
		switch entry.Type {
		case "tool_call":
			if item.call != nil {
				issues = append(issues, ExecutionInvariantIssue{
					Severity: "error", Code: "duplicate_tool_call", ToolCallID: callID,
					Message: "the transcript contains more than one tool_call with this id",
				})
			} else {
				copy := entry
				item.call = &copy
				item.callIndex = index
			}
		case "tool_result":
			if item.result != nil {
				issues = append(issues, ExecutionInvariantIssue{
					Severity: "error", Code: "duplicate_tool_result", ToolCallID: callID,
					Message: "the transcript contains more than one tool_result with this id",
				})
			} else {
				copy := entry
				item.result = &copy
				item.resultIndex = index
			}
		}
		evidence[callID] = item
	}

	for callID, item := range evidence {
		if item.result != nil && item.call == nil {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "tool_result_without_call", ToolCallID: callID,
				Message: "a tool_result has no preceding tool_call in the recovered transcript",
			})
			continue
		}
		if item.call == nil || item.result == nil {
			continue
		}
		if item.resultIndex < item.callIndex {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "tool_result_before_call", ToolCallID: callID,
				Message: "the tool_result appears before its tool_call",
			})
		}
		if strings.TrimSpace(item.call.ToolName) != strings.TrimSpace(item.result.ToolName) {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "transcript_tool_name_mismatch", ToolCallID: callID,
				Message: fmt.Sprintf("tool_call names %q but tool_result names %q", item.call.ToolName, item.result.ToolName),
			})
		}
		if item.result.ToolSuccess == nil {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "warning", Code: "tool_result_outcome_missing", ToolCallID: callID,
				Message: "the tool_result has no explicit success outcome",
			})
		}
	}
	return evidence, issues
}

func inspectReceiptSet(receipts []agentos.ToolExecutionReceipt, taskID string) (map[string]*agentos.ToolExecutionReceipt, []ExecutionInvariantIssue) {
	byCall := make(map[string]*agentos.ToolExecutionReceipt, len(receipts))
	issues := make([]ExecutionInvariantIssue, 0)
	for index := range receipts {
		receipt := receipts[index]
		callID := strings.TrimSpace(receipt.ToolCallID)
		if callID == "" {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "receipt_call_id_missing",
				Message: "an execution receipt has no tool_call_id",
			})
			continue
		}
		if previous := byCall[callID]; previous != nil {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "duplicate_tool_receipt", ToolCallID: callID,
				Message: "more than one execution receipt exists for this tool call",
			})
			continue
		}
		if expected := strings.TrimSpace(taskID); expected != "" && strings.TrimSpace(receipt.TaskID) != expected {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "receipt_task_mismatch", ToolCallID: callID,
				Message: fmt.Sprintf("receipt belongs to task %q instead of %q", receipt.TaskID, expected),
			})
		}
		copy := receipt
		byCall[callID] = &copy
	}
	return byCall, issues
}

func inspectCheckpointInvariants(checkpoint *session.TurnCheckpoint, evidence map[string]executionToolEvidence) []ExecutionInvariantIssue {
	issues := make([]ExecutionInvariantIssue, 0)
	if checkpoint == nil {
		return issues
	}
	if checkpoint.Phase == "tools_pending" && len(checkpoint.PendingToolCalls) == 0 {
		issues = append(issues, ExecutionInvariantIssue{
			Severity: "warning", Code: "tools_pending_without_calls",
			Message: "checkpoint phase is tools_pending but no pending calls are stored",
		})
	}
	if checkpoint.Phase != "tools_pending" && len(checkpoint.PendingToolCalls) > 0 {
		issues = append(issues, ExecutionInvariantIssue{
			Severity: "error", Code: "pending_calls_outside_tools_phase",
			Message: "pending tool calls are stored outside the tools_pending phase",
		})
	}
	seen := make(map[string]struct{}, len(checkpoint.PendingToolCalls))
	for _, call := range checkpoint.PendingToolCalls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" || strings.TrimSpace(call.Name) == "" {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "invalid_pending_tool_call", ToolCallID: callID,
				Message: "a pending tool call must have both id and name",
			})
			continue
		}
		if _, exists := seen[callID]; exists {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "error", Code: "duplicate_pending_tool_call", ToolCallID: callID,
				Message: "the checkpoint contains the same pending tool call more than once",
			})
		}
		seen[callID] = struct{}{}
		if item := evidence[callID]; item.result != nil {
			issues = append(issues, ExecutionInvariantIssue{
				Severity: "warning", Code: "checkpoint_lags_recorded_result", ToolCallID: callID,
				Message: "the transcript has a result for a call that remains pending in the checkpoint",
			})
		}
	}
	return issues
}

func assessPendingToolRecovery(call session.TurnCheckpointToolCall, evidence executionToolEvidence, receipt *agentos.ToolExecutionReceipt, coverage ExecutionReceiptCoverage) ToolRecoveryAssessment {
	assessment := ToolRecoveryAssessment{
		ToolCallID: strings.TrimSpace(call.ID),
		ToolName:   strings.TrimSpace(call.Name),
	}
	if assessment.ToolCallID == "" || assessment.ToolName == "" {
		return inconsistentToolRecovery(assessment, "pending call identity is incomplete")
	}

	if receipt != nil {
		readOnly := receipt.ReadOnly
		assessment.ReadOnly = &readOnly
		assessment.ReceiptStatus = strings.TrimSpace(receipt.Status)
		if strings.TrimSpace(receipt.ToolName) != assessment.ToolName {
			return inconsistentToolRecovery(assessment, fmt.Sprintf("receipt tool name %q does not match checkpoint", receipt.ToolName))
		}
		hash, err := agentos.ToolCallArgumentsHash(toolcontract.Call{ID: assessment.ToolCallID, Name: assessment.ToolName, Arguments: call.Arguments})
		if err != nil {
			return inconsistentToolRecovery(assessment, fmt.Sprintf("cannot hash checkpoint arguments: %v", err))
		}
		if strings.TrimSpace(receipt.ArgumentsHash) != hash {
			return inconsistentToolRecovery(assessment, "receipt argument hash does not match checkpoint")
		}

		switch assessment.ReceiptStatus {
		case "succeeded":
			assessment.State = toolRecoveryReceiptSucceeded
			assessment.Action = "reuse_receipt_result"
			assessment.AutoResumeSafe = true
			assessment.Detail = "the durable receipt contains a successful result and the existing ledger will reuse it"
			return assessment
		case "running", "failed", "interrupted":
			if receipt.ReadOnly {
				assessment.State = toolRecoveryRetryableReadOnly
				assessment.Action = "retry_read_only_call"
				assessment.AutoResumeSafe = true
				assessment.Detail = "the previous attempt did not complete durably, but the receipt classifies this call as read-only"
				return assessment
			}
			assessment.State = toolRecoveryOutcomeUnknown
			assessment.Action = "block_and_reconcile_external_state"
			assessment.Detail = "a mutating call has a non-success receipt, so its external side effect cannot be inferred safely"
			return assessment
		default:
			return inconsistentToolRecovery(assessment, fmt.Sprintf("unsupported receipt status %q", assessment.ReceiptStatus))
		}
	}

	if evidence.result != nil {
		assessment.State = toolRecoveryRecordedResult
		assessment.Action = "restore_recorded_result_before_continuing"
		assessment.RecordedResult = evidence.result.ToolSuccess
		assessment.Detail = "the transcript recorded a result but no reusable receipt exists; replaying the stale pending checkpoint would be unsafe"
		return assessment
	}

	if coverage.Authoritative {
		assessment.State = toolRecoveryNotStarted
		assessment.Action = "execute_call"
		assessment.AutoResumeSafe = true
		if evidence.call == nil {
			assessment.Detail = "authoritative ledger lookup found no receipt and the transcript has no tool-call start record, so the tool body did not start"
		} else {
			assessment.Detail = "the call was announced in the transcript, but authoritative ledger lookup found no receipt, so the tool body did not start"
		}
		return assessment
	}

	assessment.State = toolRecoveryOutcomeUnknown
	assessment.Action = "block_and_obtain_receipt_evidence"
	if evidence.call == nil {
		assessment.Detail = "no tool-call start was recovered, but receipt coverage is not authoritative; absence from the journal alone cannot prove that execution never began"
	} else {
		assessment.Detail = "the transcript shows an attempted call, but receipt coverage is not authoritative and no result was recorded"
	}
	return assessment
}

func inconsistentToolRecovery(assessment ToolRecoveryAssessment, detail string) ToolRecoveryAssessment {
	assessment.State = toolRecoveryInconsistentEvidence
	assessment.Action = "block_and_repair_durable_evidence"
	assessment.AutoResumeSafe = false
	assessment.Detail = detail
	return assessment
}

func executionRecoveryDisposition(status string, checkpoint *session.TurnCheckpoint, pending []ToolRecoveryAssessment) (string, bool) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && status != "running" {
		return executionDispositionCompleted, false
	}
	if checkpoint == nil {
		return executionDispositionInsufficientEvidence, false
	}
	if len(pending) == 0 {
		return executionDispositionSafeToContinue, true
	}
	reconciliation := false
	for _, assessment := range pending {
		if assessment.AutoResumeSafe {
			continue
		}
		if assessment.State == toolRecoveryRecordedResult {
			reconciliation = true
			continue
		}
		return executionDispositionBlocked, false
	}
	if reconciliation {
		return executionDispositionRequiresReconciliation, false
	}
	return executionDispositionSafeToResume, true
}

func finalizeExecutionInspection(report *ExecutionInspection) {
	if report == nil {
		return
	}
	for _, issue := range report.Issues {
		switch issue.Severity {
		case "error":
			report.Summary.Errors++
		case "warning":
			report.Summary.Warnings++
		}
	}
	if report.Summary.Errors > 0 && report.AutoResumeSafe {
		report.AutoResumeSafe = false
		report.Disposition = executionDispositionBlocked
	}
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].Severity != report.Issues[j].Severity {
			return report.Issues[i].Severity == "error"
		}
		if report.Issues[i].Code != report.Issues[j].Code {
			return report.Issues[i].Code < report.Issues[j].Code
		}
		return report.Issues[i].ToolCallID < report.Issues[j].ToolCallID
	})
}
