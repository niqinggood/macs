package session

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"mac/internal/agentloop"
	"mac/internal/conversation"
	"mac/internal/fsutil"
	"mac/internal/toolcontract"
)

const (
	CheckpointVersion           = 1
	CheckpointPhaseModelPending = "model_pending"
	CheckpointPhaseToolsPending = "tools_pending"
	CheckpointPhaseAfterTools   = "after_tools"

	checkpointMaxBytes       = 16 << 20
	checkpointMessageLimit   = 256
	checkpointResultLimit    = 128
	checkpointCallChainLimit = 256
	checkpointPlanStepLimit  = 64
)

type TurnCheckpoint struct {
	Version                 int                      `json:"version"`
	Revision                uint64                   `json:"revision"`
	TaskID                  string                   `json:"task_id"`
	ReceiptTaskID           string                   `json:"receipt_task_id,omitempty"`
	Input                   string                   `json:"input"`
	Phase                   string                   `json:"phase"`
	Iteration               int                      `json:"iteration"`
	MaxIterations           int                      `json:"max_iterations"`
	Messages                []map[string]interface{} `json:"messages"`
	PendingToolCalls        []TurnCheckpointToolCall `json:"pending_tool_calls,omitempty"`
	LastCompletedToolCallID string                   `json:"last_completed_tool_call_id,omitempty"`
	ToolCallSummary         []string                 `json:"tool_call_summary,omitempty"`
	AllToolResults          []string                 `json:"all_tool_results,omitempty"`
	CallChain               []map[string]interface{} `json:"call_chain,omitempty"`
	TotalDurationMs         int64                    `json:"total_duration_ms,omitempty"`
	ToolExecCount           int                      `json:"tool_exec_count,omitempty"`
	FailTracker             map[string]int           `json:"fail_tracker,omitempty"`
	BrokenTools             map[string]bool          `json:"broken_tools,omitempty"`
	FailDiagnosticInjected  map[string]bool          `json:"fail_diagnostic_injected,omitempty"`
	ToolSignatures          map[string]int           `json:"tool_signatures,omitempty"`
	ToolConsecutiveFailures map[string]int           `json:"tool_consecutive_failures,omitempty"`
	ActivePlan              *TurnCheckpointPlan      `json:"active_plan,omitempty"`
	UpdatedAt               time.Time                `json:"updated_at"`
}

type TurnCheckpointPlan struct {
	PlanID string               `json:"plan_id"`
	Steps  []agentloop.PlanStep `json:"steps"`
}

type TurnCheckpointToolCall struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Arguments   map[string]interface{} `json:"arguments"`
	HostManaged bool                   `json:"host_managed,omitempty"`
}

type TurnCheckpointJournal struct {
	Revision     uint64    `json:"revision"`
	Phase        string    `json:"phase"`
	Iteration    int       `json:"iteration"`
	PendingCalls int       `json:"pending_calls,omitempty"`
	NextToolCall string    `json:"next_tool_call,omitempty"`
	CheckpointAt time.Time `json:"checkpoint_at"`
}

func (checkpoint *TurnCheckpoint) PendingCalls() []toolcontract.Call {
	if checkpoint == nil || len(checkpoint.PendingToolCalls) == 0 {
		return nil
	}
	result := make([]toolcontract.Call, 0, len(checkpoint.PendingToolCalls))
	for _, call := range checkpoint.PendingToolCalls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
			continue
		}
		result = append(result, toolcontract.Call{
			ID: strings.TrimSpace(call.ID), Name: strings.TrimSpace(call.Name),
			Arguments: cloneMap(call.Arguments), HostManaged: call.HostManaged,
		})
	}
	return result
}

func CloneTurnCheckpoint(checkpoint *TurnCheckpoint) *TurnCheckpoint {
	if checkpoint == nil {
		return nil
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return nil
	}
	var clone TurnCheckpoint
	if json.Unmarshal(data, &clone) != nil {
		return nil
	}
	return &clone
}

func NormalizeTurnCheckpoint(checkpoint *TurnCheckpoint) error {
	if checkpoint == nil {
		return fmt.Errorf("turn checkpoint is nil")
	}
	checkpoint.Version = CheckpointVersion
	checkpoint.TaskID = strings.TrimSpace(checkpoint.TaskID)
	checkpoint.ReceiptTaskID = strings.TrimSpace(checkpoint.ReceiptTaskID)
	if checkpoint.ReceiptTaskID == "" {
		checkpoint.ReceiptTaskID = checkpoint.TaskID
	}
	if checkpoint.TaskID == "" {
		return fmt.Errorf("turn checkpoint task_id is empty")
	}
	switch checkpoint.Phase {
	case CheckpointPhaseModelPending, CheckpointPhaseToolsPending, CheckpointPhaseAfterTools:
	default:
		return fmt.Errorf("unsupported turn checkpoint phase %q", checkpoint.Phase)
	}
	if checkpoint.Iteration < 0 {
		checkpoint.Iteration = 0
	}
	if checkpoint.MaxIterations < checkpoint.Iteration {
		checkpoint.MaxIterations = checkpoint.Iteration
	}
	checkpoint.Input = clipText(checkpoint.Input, 128*1024)
	checkpoint.Messages = boundedCheckpointMessages(checkpoint.Messages)
	checkpoint.PendingToolCalls = CheckpointToolCalls(checkpoint.PendingCalls())
	checkpoint.ToolCallSummary = boundedStrings(checkpoint.ToolCallSummary, 256, 4000)
	checkpoint.AllToolResults = boundedStrings(checkpoint.AllToolResults, checkpointResultLimit, 32*1024)
	checkpoint.CallChain = boundedMaps(checkpoint.CallChain, checkpointCallChainLimit, 8000)
	checkpoint.FailTracker = cloneStringIntMap(checkpoint.FailTracker)
	checkpoint.BrokenTools = cloneStringBoolMap(checkpoint.BrokenTools)
	checkpoint.FailDiagnosticInjected = cloneStringBoolMap(checkpoint.FailDiagnosticInjected)
	checkpoint.ToolSignatures = cloneStringIntMap(checkpoint.ToolSignatures)
	checkpoint.ToolConsecutiveFailures = cloneStringIntMap(checkpoint.ToolConsecutiveFailures)
	checkpoint.ActivePlan = normalizePlan(checkpoint.ActivePlan)
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = time.Now()
	}
	return nil
}

func (transcript *SessionTranscript) RecordTurnCheckpoint(checkpoint *TurnCheckpoint) error {
	if transcript == nil || checkpoint == nil {
		return nil
	}
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	candidate := CloneTurnCheckpoint(checkpoint)
	if candidate == nil {
		return fmt.Errorf("clone turn checkpoint")
	}
	candidate.TaskID = transcript.SessionID
	if candidate.ReceiptTaskID == "" {
		candidate.ReceiptTaskID = transcript.SessionID
	}
	if transcript.Checkpoint != nil && candidate.Revision <= transcript.Checkpoint.Revision {
		candidate.Revision = transcript.Checkpoint.Revision + 1
	} else if candidate.Revision == 0 {
		candidate.Revision = 1
	}
	candidate.UpdatedAt = time.Now()
	if err := NormalizeTurnCheckpoint(candidate); err != nil {
		return err
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > checkpointMaxBytes {
		return fmt.Errorf("turn checkpoint is too large: %d bytes", len(data))
	}
	if strings.TrimSpace(transcript.checkpointPath) == "" {
		return fmt.Errorf("turn checkpoint path is empty")
	}
	if err := fsutil.WriteAtomic(transcript.checkpointPath, data, 0o600); err != nil {
		return fmt.Errorf("write turn checkpoint: %w", err)
	}
	transcript.Checkpoint = CloneTurnCheckpoint(candidate)
	journal := &TurnCheckpointJournal{
		Revision: candidate.Revision, Phase: candidate.Phase, Iteration: candidate.Iteration,
		PendingCalls: len(candidate.PendingToolCalls), CheckpointAt: candidate.UpdatedAt,
	}
	if len(candidate.PendingToolCalls) > 0 {
		journal.NextToolCall = candidate.PendingToolCalls[0].ID
	}
	transcript.appendJournalEventLocked(SessionJournalEvent{Event: "turn_checkpoint", Checkpoint: journal})
	return nil
}

func (transcript *SessionTranscript) CheckpointPath() string {
	if transcript == nil {
		return ""
	}
	transcript.mu.RLock()
	defer transcript.mu.RUnlock()
	return transcript.checkpointPath
}

func LoadTurnCheckpoint(path string) (*TurnCheckpoint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > checkpointMaxBytes {
		return nil, fmt.Errorf("turn checkpoint is too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var checkpoint TurnCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode turn checkpoint: %w", err)
	}
	if checkpoint.Version != CheckpointVersion {
		return nil, fmt.Errorf("unsupported turn checkpoint version %d", checkpoint.Version)
	}
	if err := NormalizeTurnCheckpoint(&checkpoint); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func CheckpointToolCalls(calls []toolcontract.Call) []TurnCheckpointToolCall {
	result := make([]TurnCheckpointToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, TurnCheckpointToolCall{
			ID:          strings.TrimSpace(call.ID),
			Name:        strings.TrimSpace(call.Name),
			Arguments:   CloneTranscriptParams(call.Arguments),
			HostManaged: call.HostManaged,
		})
	}
	return result
}

func CheckpointActivePlan(plan *agentloop.ActivePlan) *TurnCheckpointPlan {
	if plan == nil {
		return nil
	}
	planID, steps := plan.Snapshot()
	return normalizePlan(&TurnCheckpointPlan{PlanID: planID, Steps: steps})
}

func ActivePlanFromTurnCheckpoint(snapshot *TurnCheckpointPlan) *agentloop.ActivePlan {
	normalized := normalizePlan(snapshot)
	if normalized == nil {
		return nil
	}
	for _, step := range normalized.Steps {
		if step.Status != agentloop.PlanStepCompleted && step.Status != agentloop.PlanStepSkipped {
			return &agentloop.ActivePlan{
				PlanID: normalized.PlanID,
				Steps:  append([]agentloop.PlanStep(nil), normalized.Steps...),
			}
		}
	}
	return nil
}

func RestoreTurnCheckpointMessages(currentSystem, checkpointMessages []map[string]interface{}) []map[string]interface{} {
	restored := cloneMessages(currentSystem)
	start := 0
	for start < len(checkpointMessages) {
		role, _ := checkpointMessages[start]["role"].(string)
		if role != "system" {
			break
		}
		start++
	}
	return append(restored, cloneMessages(checkpointMessages[start:])...)
}

func LastCompletedToolCallID(messages []map[string]interface{}) string {
	for index := len(messages) - 1; index >= 0; index-- {
		role, _ := messages[index]["role"].(string)
		if role != "tool" {
			continue
		}
		if callID, _ := messages[index]["tool_call_id"].(string); strings.TrimSpace(callID) != "" {
			return strings.TrimSpace(callID)
		}
	}
	return ""
}

func TurnCheckpointResumeIteration(checkpoint *TurnCheckpoint) int {
	if checkpoint == nil || checkpoint.Iteration <= 0 {
		return 0
	}
	if checkpoint.Phase == CheckpointPhaseModelPending {
		return checkpoint.Iteration - 1
	}
	return checkpoint.Iteration
}

func BuildTurnRecoveryControlMessage(checkpoint *TurnCheckpoint) map[string]interface{} {
	if checkpoint == nil {
		return nil
	}
	return agentloop.NewInternalControlMessage("TURN RECOVERY", fmt.Sprintf(`[TURN RECOVERY]
Continue the interrupted turn from durable checkpoint revision %d, phase %s, iteration %d.
Completed tool messages are historical receipts. The host reconciles pending call IDs before the next model request; do not invent or duplicate their results.`,
		checkpoint.Revision,
		checkpoint.Phase,
		checkpoint.Iteration,
	))
}

func boundedStrings(values []string, limit, maxRunes int) []string {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, clipText(value, maxRunes))
	}
	return result
}

func boundedCheckpointMessages(messages []map[string]interface{}) []map[string]interface{} {
	if len(messages) == 0 {
		return nil
	}
	mediaFree := conversation.StripMediaFromMessages(messages)
	if len(mediaFree) > checkpointMessageLimit {
		prefixCount := 0
		for prefixCount < len(mediaFree) {
			role, _ := mediaFree[prefixCount]["role"].(string)
			if role != "system" {
				break
			}
			prefixCount++
		}
		keepTail := checkpointMessageLimit - prefixCount - 1
		if keepTail < 1 {
			keepTail = 1
			prefixCount = min(prefixCount, checkpointMessageLimit-2)
		}
		trimmed := make([]map[string]interface{}, 0, checkpointMessageLimit)
		trimmed = append(trimmed, mediaFree[:prefixCount]...)
		trimmed = append(trimmed, map[string]interface{}{
			"role": "system", "content": fmt.Sprintf("[Turn checkpoint omitted %d oldest hot-context messages]", len(mediaFree)-prefixCount-keepTail),
		})
		trimmed = append(trimmed, mediaFree[len(mediaFree)-keepTail:]...)
		mediaFree = trimmed
	}
	result := cloneMessages(mediaFree)
	for _, message := range result {
		if content, ok := message["content"].(string); ok {
			message["content"] = conversation.ClipText(content, 64*1024)
		}
	}
	return result
}

func boundedMaps(values []map[string]interface{}, limit, maxRunes int) []map[string]interface{} {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	result := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		clone := cloneMap(value)
		for key, item := range clone {
			if text, ok := item.(string); ok {
				clone[key] = clipText(text, maxRunes)
			}
		}
		result = append(result, clone)
	}
	return result
}

func cloneMap(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	data, _ := json.Marshal(source)
	var clone map[string]interface{}
	_ = json.Unmarshal(data, &clone)
	return clone
}

func normalizePlan(plan *TurnCheckpointPlan) *TurnCheckpointPlan {
	if plan == nil || len(plan.Steps) == 0 {
		return nil
	}
	steps := plan.Steps
	if len(steps) > checkpointPlanStepLimit {
		steps = steps[:checkpointPlanStepLimit]
	}
	result := &TurnCheckpointPlan{
		PlanID: clipText(strings.TrimSpace(plan.PlanID), 256),
		Steps:  make([]agentloop.PlanStep, 0, len(steps)),
	}
	if result.PlanID == "" {
		result.PlanID = "restored_plan"
	}
	hasInProgress := false
	for index, step := range steps {
		step.ID = conversation.ClipText(strings.TrimSpace(step.ID), 128)
		if step.ID == "" {
			step.ID = fmt.Sprintf("step_%d", index+1)
		}
		step.Title = conversation.ClipText(strings.TrimSpace(step.Title), 1024)
		step.Description = conversation.ClipText(strings.TrimSpace(step.Description), 4096)
		switch step.Status {
		case agentloop.PlanStepCompleted, agentloop.PlanStepSkipped:
		case agentloop.PlanStepInProgress:
			if hasInProgress {
				step.Status = agentloop.PlanStepPending
			} else {
				hasInProgress = true
			}
		case agentloop.PlanStepPending:
		default:
			step.Status = agentloop.PlanStepPending
		}
		result.Steps = append(result.Steps, step)
	}
	if !hasInProgress {
		for index := range result.Steps {
			if result.Steps[index].Status == agentloop.PlanStepPending {
				result.Steps[index].Status = agentloop.PlanStepInProgress
				break
			}
		}
	}
	return result
}

func cloneStringIntMap(source map[string]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]int, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneStringBoolMap(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]bool, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneMessages(messages []map[string]interface{}) []map[string]interface{} {
	if len(messages) == 0 {
		return nil
	}
	result := make([]map[string]interface{}, len(messages))
	for index, message := range messages {
		result[index] = cloneMap(message)
	}
	return result
}

func clipText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}
