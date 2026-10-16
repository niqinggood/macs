package llmapp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	llmAppEventRunCreated         = "run.created"
	llmAppEventRunStarted         = "run.started"
	llmAppEventRunRecovered       = "run.recovered"
	llmAppEventPhaseChanged       = "phase.changed"
	llmAppEventItemStarted        = "item.started"
	llmAppEventItemCompleted      = "item.completed"
	llmAppEventItemFailed         = "item.failed"
	llmAppEventGraphNodeStarted   = "graph.node_started"
	llmAppEventGraphNodeCompleted = "graph.node_completed"
	llmAppEventGraphNodeFailed    = "graph.node_failed"
	llmAppEventDecisionRequired   = "decision.required"
	llmAppEventHealthChanged      = "health.changed"
	llmAppEventCommandApplied     = "command.applied"
	llmAppEventPlanRevised        = "plan.revised"
	llmAppEventItemsEnqueued      = "items.enqueued"
	llmAppEventPauseRequested     = "run.pause_requested"
	llmAppEventResumed            = "run.resumed"
	llmAppEventStopRequested      = "run.stop_requested"
	llmAppEventRunCompleted       = "run.completed"
	llmAppEventRunDegraded        = "run.degraded"
	llmAppEventRunFailed          = "run.failed"
	llmAppEventRunStopped         = "run.stopped"

	llmAppCommandReplan       = "replan"
	llmAppCommandEnqueueItems = "enqueue_items"
	llmAppCommandPause        = "pause"
	llmAppCommandResume       = "resume"
	llmAppCommandStop         = "stop"

	llmAppDefaultEventLimit = 50
	llmAppMaximumEventLimit = 100
	llmAppMaximumEventWait  = 30
)

var (
	llmAppCommandIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	llmAppPromptVariablePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
)

// LLMAppRunPlan is the mutable, versioned portion of a frozen application
// definition. A worker snapshots this plan when it claims an item; replans
// therefore affect only work that has not crossed that execution boundary.
type LLMAppRunPlan struct {
	Revision        int                    `json:"revision"`
	Digest          string                 `json:"digest"`
	SystemPrompt    string                 `json:"system_prompt,omitempty"`
	PromptTemplate  string                 `json:"prompt_template"`
	PromptVariables map[string]string      `json:"prompt_variables,omitempty"`
	Model           string                 `json:"model,omitempty"`
	MaxTokens       int                    `json:"max_tokens"`
	Temperature     float64                `json:"temperature"`
	Concurrency     int                    `json:"concurrency"`
	OutputSchema    map[string]interface{} `json:"output_schema,omitempty"`
	UpdatedBy       string                 `json:"updated_by"`
	Reason          string                 `json:"reason,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type LLMAppEvent struct {
	Sequence  int64                  `json:"sequence"`
	RunID     string                 `json:"run_id"`
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Message   string                 `json:"message,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type LLMAppPlanChanges struct {
	SystemPrompt    *string                 `json:"system_prompt,omitempty"`
	PromptTemplate  *string                 `json:"prompt_template,omitempty"`
	PromptVariables *map[string]string      `json:"prompt_variables,omitempty"`
	Model           *string                 `json:"model,omitempty"`
	MaxTokens       *int                    `json:"max_tokens,omitempty"`
	Temperature     *float64                `json:"temperature,omitempty"`
	Concurrency     *int                    `json:"concurrency,omitempty"`
	OutputSchema    *map[string]interface{} `json:"output_schema,omitempty"`
}

type LLMAppCommand struct {
	CommandID            string             `json:"command_id,omitempty"`
	Type                 string             `json:"type"`
	ExpectedPlanRevision int                `json:"expected_plan_revision,omitempty"`
	Reason               string             `json:"reason,omitempty"`
	Changes              *LLMAppPlanChanges `json:"changes,omitempty"`
	Items                []interface{}      `json:"items,omitempty"`
}

type LLMAppCommandReceipt struct {
	CommandID        string    `json:"command_id"`
	PayloadDigest    string    `json:"payload_digest,omitempty"`
	RunID            string    `json:"run_id"`
	Type             string    `json:"type"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason,omitempty"`
	PreviousRevision int       `json:"previous_plan_revision"`
	PlanRevision     int       `json:"plan_revision"`
	EffectiveAfter   string    `json:"effective_after_checkpoint,omitempty"`
	SubmittedAt      time.Time `json:"submitted_at"`
	AppliedAt        time.Time `json:"applied_at"`
}

func newLLMAppRunPlan(definition LLMAppDefinition, now time.Time) (LLMAppRunPlan, error) {
	plan := LLMAppRunPlan{
		Revision: 1, SystemPrompt: definition.SystemPrompt,
		PromptTemplate:  definition.PromptTemplate,
		PromptVariables: cloneLLMAppStringMap(definition.PromptVariables),
		Model:           definition.Model, MaxTokens: definition.MaxTokens,
		Temperature: definition.Temperature, Concurrency: definition.Concurrency,
		OutputSchema: cloneLLMAppJSONMap(definition.OutputSchema),
		UpdatedBy:    "application_definition", Reason: "initial run plan", UpdatedAt: now,
	}
	if err := normalizeLLMAppRunPlan(&plan); err != nil {
		return LLMAppRunPlan{}, err
	}
	if err := updateLLMAppRunPlanDigest(&plan); err != nil {
		return LLMAppRunPlan{}, err
	}
	return plan, nil
}

func normalizeLLMAppRunPlan(plan *LLMAppRunPlan) error {
	if plan == nil {
		return errors.New("runtime plan is required")
	}
	plan.SystemPrompt = strings.TrimSpace(plan.SystemPrompt)
	plan.PromptTemplate = strings.TrimSpace(plan.PromptTemplate)
	if plan.PromptTemplate == "" {
		return errors.New("runtime plan prompt_template is required")
	}
	variables, err := normalizeLLMAppPromptVariables(plan.PromptVariables)
	if err != nil {
		return err
	}
	plan.PromptVariables = variables
	promptChars := len(plan.SystemPrompt) + len(plan.PromptTemplate)
	for key, value := range variables {
		promptChars += len(key) + len(value)
	}
	if promptChars > llmAppMaximumPromptChars {
		return fmt.Errorf("runtime plan prompt content exceeds %d characters", llmAppMaximumPromptChars)
	}
	plan.Model = strings.TrimSpace(plan.Model)
	if plan.MaxTokens <= 0 || plan.MaxTokens > 32768 {
		return errors.New("runtime plan max_tokens must be between 1 and 32768")
	}
	if plan.Temperature < 0 || plan.Temperature > 2 {
		return errors.New("runtime plan temperature must be between 0 and 2")
	}
	if plan.Concurrency <= 0 || plan.Concurrency > llmAppMaximumConcurrency {
		return fmt.Errorf("runtime plan concurrency must be between 1 and %d", llmAppMaximumConcurrency)
	}
	if plan.Revision <= 0 {
		plan.Revision = 1
	}
	if err := validateLLMAppJSONSchema(plan.OutputSchema); err != nil {
		return err
	}
	plan.UpdatedBy = strings.TrimSpace(plan.UpdatedBy)
	if plan.UpdatedBy == "" {
		plan.UpdatedBy = "openkitty"
	}
	plan.Reason = strings.TrimSpace(plan.Reason)
	return nil
}

func normalizeLLMAppPromptVariables(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(values))
	total := 0
	for rawKey, rawValue := range values {
		key := strings.TrimSpace(rawKey)
		if !llmAppPromptVariablePattern.MatchString(key) {
			return nil, fmt.Errorf("prompt variable %q must match %s", rawKey, llmAppPromptVariablePattern.String())
		}
		if len(rawValue) > llmAppMaximumPromptChars {
			return nil, fmt.Errorf("prompt variable %q is too large", key)
		}
		total += len(key) + len(rawValue)
		if total > llmAppMaximumPromptChars {
			return nil, errors.New("prompt variables exceed the runtime prompt limit")
		}
		normalized[key] = rawValue
	}
	return normalized, nil
}

func updateLLMAppRunPlanDigest(plan *LLMAppRunPlan) error {
	copyPlan := cloneLLMAppRunPlan(*plan)
	copyPlan.Digest = ""
	data, err := json.Marshal(copyPlan)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	plan.Digest = hex.EncodeToString(digest[:])
	return nil
}

func cloneLLMAppRunPlan(plan LLMAppRunPlan) LLMAppRunPlan {
	plan.PromptVariables = cloneLLMAppStringMap(plan.PromptVariables)
	plan.OutputSchema = cloneLLMAppJSONMap(plan.OutputSchema)
	return plan
}

func cloneLLMAppStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func cloneLLMAppJSONMap(source map[string]interface{}) map[string]interface{} {
	if len(source) == 0 {
		return nil
	}
	data, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var target map[string]interface{}
	if err := json.Unmarshal(data, &target); err != nil {
		return nil
	}
	return target
}

func (plan LLMAppRunPlan) applyToDefinition(base LLMAppDefinition) LLMAppDefinition {
	base.SystemPrompt = plan.SystemPrompt
	base.PromptTemplate = plan.PromptTemplate
	base.PromptVariables = cloneLLMAppStringMap(plan.PromptVariables)
	base.Model = plan.Model
	base.MaxTokens = plan.MaxTokens
	base.Temperature = plan.Temperature
	base.Concurrency = plan.Concurrency
	base.OutputSchema = cloneLLMAppJSONMap(plan.OutputSchema)
	return base
}

func decodeLLMAppCommand(raw interface{}) (LLMAppCommand, error) {
	if raw == nil {
		return LLMAppCommand{}, errors.New("command is required")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return LLMAppCommand{}, fmt.Errorf("encode llm_app command: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var command LLMAppCommand
	if err := decoder.Decode(&command); err != nil {
		return LLMAppCommand{}, fmt.Errorf("decode llm_app command: %w", err)
	}
	command.Type = strings.ToLower(strings.TrimSpace(command.Type))
	command.CommandID = strings.TrimSpace(command.CommandID)
	if command.CommandID == "" {
		command.CommandID, err = newLLMAppCommandID()
		if err != nil {
			return LLMAppCommand{}, err
		}
	}
	if !llmAppCommandIDPattern.MatchString(command.CommandID) {
		return LLMAppCommand{}, errors.New("command_id contains unsupported characters or is too long")
	}
	if command.ExpectedPlanRevision < 0 {
		return LLMAppCommand{}, errors.New("expected_plan_revision cannot be negative")
	}
	command.Reason = strings.TrimSpace(command.Reason)
	if len(command.Reason) > 2000 {
		return LLMAppCommand{}, errors.New("command reason exceeds 2000 characters")
	}
	if command.Reason == "" {
		command.Reason = "MACS runtime command"
	}
	switch command.Type {
	case llmAppCommandReplan:
		if command.Changes == nil || !command.Changes.hasChanges() {
			return LLMAppCommand{}, errors.New("replan command requires non-empty changes")
		}
		if len(command.Items) > 0 {
			return LLMAppCommand{}, errors.New("replan command does not accept items")
		}
	case llmAppCommandEnqueueItems:
		if len(command.Items) == 0 {
			return LLMAppCommand{}, errors.New("enqueue_items command requires items")
		}
		if command.Changes != nil {
			return LLMAppCommand{}, errors.New("enqueue_items command does not accept changes")
		}
	case llmAppCommandPause, llmAppCommandResume, llmAppCommandStop:
		if command.Changes != nil || len(command.Items) > 0 {
			return LLMAppCommand{}, fmt.Errorf("%s command does not accept changes or items", command.Type)
		}
	default:
		return LLMAppCommand{}, fmt.Errorf("unsupported llm_app command type %q", command.Type)
	}
	return command, nil
}

func (changes *LLMAppPlanChanges) hasChanges() bool {
	return changes != nil && (changes.SystemPrompt != nil || changes.PromptTemplate != nil ||
		changes.PromptVariables != nil || changes.Model != nil || changes.MaxTokens != nil ||
		changes.Temperature != nil || changes.Concurrency != nil || changes.OutputSchema != nil)
}

func newLLMAppCommandID() (string, error) {
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate llm_app command id: %w", err)
	}
	return "cmd_" + hex.EncodeToString(random), nil
}

func (m *LLMAppManager) applyCommand(run *llmAppManagedRun, command LLMAppCommand) (LLMAppCommandReceipt, error) {
	if run == nil {
		return LLMAppCommandReceipt{}, errors.New("run not found")
	}
	if err := m.assertLocalRunControl(run); err != nil {
		return LLMAppCommandReceipt{}, err
	}
	run.commandMu.Lock()
	defer run.commandMu.Unlock()
	payloadDigest, err := digestLLMAppCommand(command)
	if err != nil {
		return LLMAppCommandReceipt{}, err
	}
	run.mu.Lock()
	cachedReceipt, receiptExists := run.commandReceipts[command.CommandID]
	run.mu.Unlock()
	if receiptExists {
		if cachedReceipt.PayloadDigest != "" && cachedReceipt.PayloadDigest != payloadDigest {
			return LLMAppCommandReceipt{}, errors.New("command_id was already used with a different command payload")
		}
		return cachedReceipt, nil
	}
	before := run.snapshot()
	if state, readErr := m.readCommandState(before.RunID, command.CommandID); readErr == nil {
		if state.PayloadDigest != payloadDigest {
			return LLMAppCommandReceipt{}, errors.New("command_id was already used with a different command payload")
		}
		if state.Status == "applied" && state.Receipt != nil {
			return *state.Receipt, nil
		}
		return LLMAppCommandReceipt{}, fmt.Errorf(
			"%w for command %s; inspect run state before deciding whether to issue a new command",
			ErrLLMAppCommandOutcomeUnknown, command.CommandID,
		)
	} else if !os.IsNotExist(readErr) {
		return LLMAppCommandReceipt{}, readErr
	}
	if isTerminalLLMAppStatus(before.Status) {
		return LLMAppCommandReceipt{}, fmt.Errorf("cannot apply command to run in %s status", before.Status)
	}
	if command.ExpectedPlanRevision > 0 && command.ExpectedPlanRevision != before.PlanRevision {
		return LLMAppCommandReceipt{}, fmt.Errorf(
			"plan revision conflict: expected %d, current %d",
			command.ExpectedPlanRevision, before.PlanRevision,
		)
	}
	run.mu.Lock()
	workflow := run.workflow
	run.mu.Unlock()
	if command.Type == llmAppCommandReplan && workflow == llmAppWorkflowGraph {
		return LLMAppCommandReceipt{}, errors.New("replan is not supported for graph workflows; create a new immutable graph definition")
	}
	if command.Type == llmAppCommandEnqueueItems && workflow != llmAppWorkflowBatch {
		return LLMAppCommandReceipt{}, errors.New("enqueue_items is supported only for batch workflows")
	}
	submittedAt := time.Now()
	commandState := llmAppCommandState{
		Command: command, PayloadDigest: payloadDigest, Status: "accepted", CreatedAt: submittedAt,
	}
	if err := m.writeCommandState(run, commandState); err != nil {
		return LLMAppCommandReceipt{}, fmt.Errorf("persist command intent: %w", err)
	}
	var applyErr error
	var stopCancel context.CancelFunc
	var stopDone <-chan struct{}
	switch command.Type {
	case llmAppCommandReplan:
		applyErr = m.replanRun(run, *command.Changes, command.Reason)
	case llmAppCommandEnqueueItems:
		applyErr = m.enqueueRunItems(run, command.CommandID, command.Items)
	case llmAppCommandPause:
		applyErr = m.pauseRun(run)
	case llmAppCommandResume:
		applyErr = m.resumeRun(run)
	case llmAppCommandStop:
		stopCancel, stopDone, applyErr = m.requestRunStop(run, command.Reason)
		if stopCancel != nil {
			defer func() {
				stopCancel()
				if stopDone != nil {
					select {
					case <-stopDone:
					case <-time.After(2 * time.Second):
					}
				}
			}()
		}
	}
	if applyErr != nil {
		commandState.Status = "outcome_unknown"
		commandState.Error = applyErr.Error()
		if persistErr := m.writeCommandState(run, commandState); persistErr != nil {
			return LLMAppCommandReceipt{}, errors.Join(applyErr, persistErr)
		}
		return LLMAppCommandReceipt{}, applyErr
	}
	after := run.snapshot()
	receipt := LLMAppCommandReceipt{
		CommandID: command.CommandID, RunID: after.RunID, Type: command.Type,
		PayloadDigest: payloadDigest,
		Status:        "applied", Reason: command.Reason,
		PreviousRevision: before.PlanRevision, PlanRevision: after.PlanRevision,
		EffectiveAfter: before.Checkpoint, SubmittedAt: submittedAt, AppliedAt: time.Now(),
	}
	commandState.Status = "applied"
	commandState.Receipt = &receipt
	if err := m.writeCommandState(run, commandState); err != nil {
		return LLMAppCommandReceipt{}, fmt.Errorf("persist applied command receipt: %w", err)
	}
	run.mu.Lock()
	if run.commandReceipts == nil {
		run.commandReceipts = make(map[string]LLMAppCommandReceipt)
	}
	run.commandReceipts[command.CommandID] = receipt
	run.mu.Unlock()
	if err := m.appendRunJSONLine(run, after.CommandPath, receipt); err != nil && m.logger != nil {
		m.logger.Printf("persist llm_app command %s: %v", command.CommandID, err)
	}
	m.emitRunEvent(run, llmAppEventCommandApplied, "openkitty", "runtime command applied", map[string]interface{}{
		"command_id": command.CommandID, "command_type": command.Type,
		"reason": command.Reason, "previous_plan_revision": before.PlanRevision,
		"plan_revision": after.PlanRevision,
	})
	return receipt, nil
}

func (m *LLMAppManager) replanRun(run *llmAppManagedRun, changes LLMAppPlanChanges, reason string) error {
	run.mu.Lock()
	if run.workflow == llmAppWorkflowGraph {
		run.mu.Unlock()
		return errors.New("replan is not supported for graph workflows; create a new immutable graph definition")
	}
	if isTerminalLLMAppStatus(run.record.Status) {
		status := run.record.Status
		run.mu.Unlock()
		return fmt.Errorf("cannot replan run in %s status", status)
	}
	previous := run.plan.Revision
	plan := cloneLLMAppRunPlan(run.plan)
	runID := run.record.RunID
	leaseToken := run.leaseToken
	if changes.SystemPrompt != nil {
		plan.SystemPrompt = *changes.SystemPrompt
	}
	if changes.PromptTemplate != nil {
		plan.PromptTemplate = *changes.PromptTemplate
	}
	if changes.PromptVariables != nil {
		plan.PromptVariables = cloneLLMAppStringMap(*changes.PromptVariables)
	}
	if changes.Model != nil {
		plan.Model = *changes.Model
	}
	if changes.MaxTokens != nil {
		plan.MaxTokens = *changes.MaxTokens
	}
	if changes.Temperature != nil {
		plan.Temperature = *changes.Temperature
	}
	if changes.Concurrency != nil {
		plan.Concurrency = *changes.Concurrency
	}
	if changes.OutputSchema != nil {
		plan.OutputSchema = cloneLLMAppJSONMap(*changes.OutputSchema)
	}
	plan.Revision = previous + 1
	plan.UpdatedBy = "openkitty"
	plan.Reason = reason
	plan.UpdatedAt = time.Now()
	if err := normalizeLLMAppRunPlan(&plan); err != nil {
		run.mu.Unlock()
		return err
	}
	if err := updateLLMAppRunPlanDigest(&plan); err != nil {
		run.mu.Unlock()
		return err
	}
	planPath := filepath.Join(m.runDir(runID), "plan.json")
	planRevisionPath := llmAppPlanRevisionPath(m.runDir(runID), plan.Revision)
	if err := m.withRunLeaseToken(runID, leaseToken, func() error {
		if err := writeLLMAppJSON(planRevisionPath, plan); err != nil {
			return err
		}
		return writeLLMAppJSON(planPath, plan)
	}); err != nil {
		run.mu.Unlock()
		return err
	}
	run.plan = plan
	run.record.PlanRevision = plan.Revision
	run.record.PlanDigest = plan.Digest
	run.record.LastHeartbeatAt = time.Now()
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return err
	}
	m.emitRunEvent(run, llmAppEventPlanRevised, "openkitty", "runtime plan revised", map[string]interface{}{
		"previous_plan_revision": previous, "plan_revision": plan.Revision,
		"plan_digest": plan.Digest, "reason": reason,
	})
	return nil
}

func llmAppPlanRevisionPath(runDir string, revision int) string {
	return filepath.Join(runDir, "plans", fmt.Sprintf("revision_%06d.json", revision))
}

func (m *LLMAppManager) enqueueRunItems(run *llmAppManagedRun, commandID string, values []interface{}) error {
	items, err := llmAppItemsFromValues(values)
	if err != nil {
		return err
	}
	run.mu.Lock()
	if run.workflow != llmAppWorkflowBatch {
		run.mu.Unlock()
		return errors.New("enqueue_items is supported only for batch workflows")
	}
	if !run.queueOpen {
		phase := run.record.Phase
		run.mu.Unlock()
		return fmt.Errorf("run is not accepting items in phase %s", phase)
	}
	items = splitLLMAppItems(items, run.maxItemChars)
	base := run.record.Progress.Total
	if base+len(items) > run.maxItems {
		limit := run.maxItems
		run.mu.Unlock()
		return fmt.Errorf("enqueued items would exceed application limit %d", limit)
	}
	for index := range items {
		items[index].Index = base + index
		items[index].ID = fmt.Sprintf("item_%06d", base+index+1)
	}
	inputPath := filepath.Join(m.runDir(run.record.RunID), "inputs.jsonl")
	runID := run.record.RunID
	leaseToken := run.leaseToken
	if err := m.withRunLeaseToken(runID, leaseToken, func() error {
		for _, item := range items {
			if err := appendLLMAppJSONLine(inputPath, item); err != nil {
				return fmt.Errorf("persist enqueued item: %w", err)
			}
			now := time.Now()
			state := llmAppItemState{
				Item: item, Status: llmAppItemPending, CreatedAt: now, UpdatedAt: now,
			}
			if err := writeLLMAppJSON(m.itemStatePath(runID, item), state); err != nil {
				return fmt.Errorf("persist enqueued item checkpoint: %w", err)
			}
		}
		return nil
	}); err != nil {
		run.mu.Unlock()
		return err
	}
	run.pendingItems = append(run.pendingItems, items...)
	run.record.Progress.Total += len(items)
	if run.record.Progress.Total > 0 {
		run.record.Progress.Percent = float64(run.record.Progress.Completed) * 100 / float64(run.record.Progress.Total)
	}
	run.record.LastHeartbeatAt = time.Now()
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return err
	}
	m.emitRunEvent(run, llmAppEventItemsEnqueued, "openkitty", "items appended to running batch", map[string]interface{}{
		"command_id": commandID, "count": len(items), "total": run.snapshot().Progress.Total,
	})
	return nil
}

func (m *LLMAppManager) initializeItemQueue(run *llmAppManagedRun, items []llmAppWorkItem) {
	run.mu.Lock()
	run.pendingItems = append([]llmAppWorkItem(nil), items...)
	run.inFlightItems = 0
	run.queueOpen = true
	run.record.Progress = LLMAppProgress{Total: len(items)}
	run.record.CurrentItem = ""
	run.record.LastProgressAt = time.Now()
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil && m.logger != nil {
		m.logger.Printf("persist initialized llm_app queue %s: %v", run.snapshot().RunID, err)
	}
}

func (m *LLMAppManager) claimRunItem(
	ctx context.Context,
	run *llmAppManagedRun,
	workerID int,
) (llmAppWorkItem, LLMAppRunPlan, bool, error) {
	for {
		if err := m.waitUntilRunnable(ctx, run); err != nil {
			return llmAppWorkItem{}, LLMAppRunPlan{}, false, err
		}
		run.mu.Lock()
		if !run.queueOpen {
			run.mu.Unlock()
			return llmAppWorkItem{}, LLMAppRunPlan{}, false, nil
		}
		if len(run.pendingItems) == 0 {
			if run.inFlightItems == 0 {
				run.queueOpen = false
				m.signalWorkChangedLocked(run)
				run.mu.Unlock()
				return llmAppWorkItem{}, LLMAppRunPlan{}, false, nil
			}
			changed := run.workChangedCh
			run.mu.Unlock()
			select {
			case <-ctx.Done():
				return llmAppWorkItem{}, LLMAppRunPlan{}, false, ctx.Err()
			case <-changed:
				continue
			}
		}
		if workerID >= run.plan.Concurrency {
			changed := run.workChangedCh
			run.mu.Unlock()
			select {
			case <-ctx.Done():
				return llmAppWorkItem{}, LLMAppRunPlan{}, false, ctx.Err()
			case <-changed:
				continue
			}
		}
		if run.pauseRequested {
			run.mu.Unlock()
			continue
		}
		item := run.pendingItems[0]
		run.pendingItems = run.pendingItems[1:]
		run.inFlightItems++
		plan := cloneLLMAppRunPlan(run.plan)
		if revision := run.itemPlanRevisions[item.ID]; revision > 0 && revision != plan.Revision {
			var recoveredPlan LLMAppRunPlan
			path := llmAppPlanRevisionPath(m.runDir(run.record.RunID), revision)
			if err := readLLMAppJSON(path, &recoveredPlan); err != nil {
				run.inFlightItems--
				m.signalWorkChangedLocked(run)
				run.mu.Unlock()
				return llmAppWorkItem{}, LLMAppRunPlan{}, false, fmt.Errorf("load item plan revision %d: %w", revision, err)
			}
			plan = recoveredPlan
		}
		run.mu.Unlock()
		if err := m.checkpointItemClaim(run, item, plan); err != nil {
			m.finishClaimedRunItem(run)
			return llmAppWorkItem{}, LLMAppRunPlan{}, false, err
		}
		return item, plan, true, nil
	}
}

func (m *LLMAppManager) finishClaimedRunItem(run *llmAppManagedRun) {
	run.mu.Lock()
	if run.inFlightItems > 0 {
		run.inFlightItems--
	}
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
}

func (m *LLMAppManager) closeItemQueue(run *llmAppManagedRun) {
	run.mu.Lock()
	run.queueOpen = false
	run.pendingItems = nil
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
}

func (m *LLMAppManager) signalWorkChangedLocked(run *llmAppManagedRun) {
	if run.workChangedCh != nil {
		close(run.workChangedCh)
	}
	run.workChangedCh = make(chan struct{})
}

func (m *LLMAppManager) emitRunEvent(
	run *llmAppManagedRun,
	eventType string,
	source string,
	message string,
	data map[string]interface{},
) {
	if run == nil {
		return
	}
	run.persistMu.Lock()
	var event LLMAppEvent
	persistErr := m.withRunLease(run, func() error {
		run.mu.Lock()
		defer run.mu.Unlock()
		if run.record.EventPath == "" {
			run.record.EventPath = filepath.Join(m.runDir(run.record.RunID), "events.jsonl")
		}
		event = LLMAppEvent{
			Sequence: run.record.EventSequence + 1, RunID: run.record.RunID,
			Type: strings.TrimSpace(eventType), Source: strings.TrimSpace(source),
			Message: strings.TrimSpace(message), Data: data, CreatedAt: time.Now(),
		}
		if err := appendLLMAppJSONLine(run.record.EventPath, event); err != nil {
			return err
		}
		run.record.EventSequence = event.Sequence
		if run.eventChangedCh != nil {
			close(run.eventChangedCh)
		}
		run.eventChangedCh = make(chan struct{})
		return writeLLMAppJSON(filepath.Join(m.runDir(run.record.RunID), "run.json"), run.record)
	})
	run.persistMu.Unlock()
	if persistErr != nil && m.logger != nil {
		if event.RunID == "" {
			m.logger.Printf("skip llm_app event after lease loss: %v", persistErr)
		} else {
			m.logger.Printf("persist llm_app event %s/%d: %v", event.RunID, event.Sequence, persistErr)
		}
	}
}

// finalizeRunRecord commits the terminal event and terminal status under one
// persistence barrier. Status readers cannot observe a terminal run while its
// worker is still appending the final event or rewriting run.json.
func (m *LLMAppManager) finalizeRunRecord(
	run *llmAppManagedRun,
	eventType string,
	message string,
	data map[string]interface{},
	update func(*LLMAppRunRecord),
) {
	if run == nil {
		return
	}
	run.persistMu.Lock()
	var record LLMAppRunRecord
	var nextSequence int64
	var eventErr error
	var runErr error
	leaseErr := m.withRunLease(run, func() error {
		run.mu.Lock()
		defer run.mu.Unlock()
		if run.record.EventPath == "" {
			run.record.EventPath = filepath.Join(m.runDir(run.record.RunID), "events.jsonl")
		}
		nextSequence = run.record.EventSequence + 1
		event := LLMAppEvent{
			Sequence: nextSequence, RunID: run.record.RunID, Type: eventType,
			Source: "worker", Message: message, Data: data, CreatedAt: time.Now(),
		}
		eventErr = appendLLMAppJSONLine(run.record.EventPath, event)
		if eventErr == nil {
			run.record.EventSequence = nextSequence
			if run.eventChangedCh != nil {
				close(run.eventChangedCh)
			}
			run.eventChangedCh = make(chan struct{})
		}
		if update != nil {
			update(&run.record)
		}
		record = run.record
		runErr = writeLLMAppJSON(filepath.Join(m.runDir(record.RunID), "run.json"), record)
		return errors.Join(eventErr, runErr)
	})
	run.persistMu.Unlock()
	if leaseErr != nil && eventErr == nil && runErr == nil {
		if m.logger != nil {
			m.logger.Printf("skip terminal llm_app commit after lease loss: %v", leaseErr)
		}
		return
	}
	if eventErr != nil && m.logger != nil {
		m.logger.Printf("persist terminal llm_app event %s/%d: %v", record.RunID, nextSequence, eventErr)
	}
	if runErr != nil && m.logger != nil {
		m.logger.Printf("persist terminal llm_app run %s: %v", record.RunID, runErr)
	}
}

func (m *LLMAppManager) readEvents(
	ctx context.Context,
	run *llmAppManagedRun,
	record LLMAppRunRecord,
	params map[string]interface{},
) (string, error) {
	after, err := llmAppNonNegativeIntParam(params, "after_seq", 0)
	if err != nil {
		return "", err
	}
	limit, err := llmAppNonNegativeIntParam(params, "limit", llmAppDefaultEventLimit)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = llmAppDefaultEventLimit
	}
	if limit > llmAppMaximumEventLimit {
		limit = llmAppMaximumEventLimit
	}
	waitSeconds, err := llmAppNonNegativeIntParam(params, "wait_seconds", 0)
	if err != nil {
		return "", err
	}
	if waitSeconds > llmAppMaximumEventWait {
		waitSeconds = llmAppMaximumEventWait
	}
	filters := llmAppStringSetParam(params, "event_types")
	originalAfter := int64(after)
	scanAfter := originalAfter
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	for {
		record = run.snapshot()
		events, hasMore, next, err := readLLMAppEventsFile(record.EventPath, scanAfter, limit, filters)
		if err != nil {
			return "", err
		}
		scanAfter = next
		terminal := isTerminalLLMAppStatus(record.Status)
		if len(events) > 0 || hasMore || terminal || waitSeconds == 0 || !time.Now().Before(deadline) {
			return marshalLLMAppJSON(map[string]interface{}{
				"run_id": record.RunID, "status": record.Status,
				"events": events, "count": len(events), "after_seq": originalAfter,
				"next_after_seq": scanAfter, "current_sequence": record.EventSequence,
				"has_more": hasMore, "terminal": terminal,
			})
		}

		run.mu.Lock()
		record = run.record
		changed := run.eventChangedCh
		hasUnscannedEvents := record.EventSequence > scanAfter
		terminal = isTerminalLLMAppStatus(record.Status)
		run.mu.Unlock()
		if hasUnscannedEvents {
			continue
		}
		if terminal {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			continue
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return "", ctx.Err()
		case <-changed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func readLLMAppEventsFile(
	path string,
	after int64,
	limit int,
	filters map[string]bool,
) ([]LLMAppEvent, bool, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []LLMAppEvent{}, false, after, nil
		}
		return nil, false, after, err
	}
	defer file.Close()
	events := make([]LLMAppEvent, 0, limit)
	hasMore := false
	cursor := after
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event LLMAppEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Sequence <= after {
			continue
		}
		if len(filters) > 0 && !filters[event.Type] {
			cursor = event.Sequence
			continue
		}
		if len(events) >= limit {
			hasMore = true
			break
		}
		events = append(events, event)
		cursor = event.Sequence
	}
	if err := scanner.Err(); err != nil {
		return nil, false, cursor, err
	}
	return events, hasMore, cursor, nil
}

func lastLLMAppEventSequence(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()
	var sequence int64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event LLMAppEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil && event.Sequence > sequence {
			sequence = event.Sequence
		}
	}
	if err := scanner.Err(); err != nil {
		return sequence, err
	}
	return sequence, nil
}

func llmAppNonNegativeIntParam(params map[string]interface{}, key string, fallback int) (int, error) {
	value, ok := params[key]
	if !ok || value == nil {
		return fallback, nil
	}
	var number int
	switch typed := value.(type) {
	case int:
		number = typed
	case int64:
		number = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = int(parsed)
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if number < 0 {
		return 0, fmt.Errorf("%s cannot be negative", key)
	}
	return number, nil
}

func llmAppStringSetParam(params map[string]interface{}, key string) map[string]bool {
	raw, ok := params[key].([]interface{})
	if !ok {
		if stringsRaw, stringsOK := params[key].([]string); stringsOK {
			result := make(map[string]bool, len(stringsRaw))
			for _, value := range stringsRaw {
				if value = strings.TrimSpace(value); value != "" {
					result[value] = true
				}
			}
			return result
		}
		return nil
	}
	result := make(map[string]bool, len(raw))
	for _, value := range raw {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			result[text] = true
		}
	}
	return result
}
