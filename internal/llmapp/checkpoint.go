package llmapp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	llmAppItemPending       = "pending"
	llmAppItemEffectPending = "effect_pending"
	llmAppItemSettled       = "settled"
	llmAppReplaySafe        = "safe"
	llmAppReplayNever       = "never"
)

// llmAppItemState is the authoritative item checkpoint. results.jsonl remains
// an append-only observation stream; recovery and terminal aggregation use
// these atomic checkpoints so a crash cannot create duplicate completion.
type llmAppItemState struct {
	Item                 llmAppWorkItem           `json:"item"`
	Status               string                   `json:"status"`
	PlanRevision         int                      `json:"plan_revision,omitempty"`
	PlanDigest           string                   `json:"plan_digest,omitempty"`
	ValidationAttempt    int                      `json:"validation_attempt,omitempty"`
	Messages             []map[string]interface{} `json:"messages,omitempty"`
	EffectID             string                   `json:"effect_id,omitempty"`
	ReplayPolicy         string                   `json:"replay_policy,omitempty"`
	RecoveredEffectCount int                      `json:"recovered_effect_count,omitempty"`
	Result               *LLMAppItemResult        `json:"result,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	UpdatedAt            time.Time                `json:"updated_at"`
}

func (m *LLMAppManager) itemStatePath(runID string, item llmAppWorkItem) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", item.Index, item.ID)))
	return filepath.Join(
		m.runDir(runID), "items",
		fmt.Sprintf("%06d_%s.json", item.Index, hex.EncodeToString(sum[:8])),
	)
}

func (m *LLMAppManager) writeItemState(run *llmAppManagedRun, state llmAppItemState) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	now := time.Now()
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	state.UpdatedAt = now
	return m.writeRunJSON(run, m.itemStatePath(run.snapshot().RunID, state.Item), state)
}

func (m *LLMAppManager) readItemState(runID string, item llmAppWorkItem) (llmAppItemState, error) {
	var state llmAppItemState
	err := readLLMAppJSON(m.itemStatePath(runID, item), &state)
	return state, err
}

func (m *LLMAppManager) loadItemStates(runID string) ([]llmAppItemState, error) {
	dir := filepath.Join(m.runDir(runID), "items")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	states := make([]llmAppItemState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		var state llmAppItemState
		if err := readLLMAppJSON(filepath.Join(dir, entry.Name()), &state); err != nil {
			return nil, fmt.Errorf("read item checkpoint %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(state.Item.ID) == "" {
			return nil, fmt.Errorf("item checkpoint %s has no item id", entry.Name())
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Item.Index < states[j].Item.Index })
	return states, nil
}

func (m *LLMAppManager) prepareItemQueue(
	run *llmAppManagedRun,
	items []llmAppWorkItem,
) ([]LLMAppItemResult, error) {
	runID := run.snapshot().RunID
	states, err := m.loadItemStates(runID)
	if err != nil {
		return nil, err
	}
	stateByID := make(map[string]llmAppItemState, len(states))
	itemByID := make(map[string]llmAppWorkItem, len(items)+len(states))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	for _, state := range states {
		stateByID[state.Item.ID] = state
		if _, exists := itemByID[state.Item.ID]; !exists {
			itemByID[state.Item.ID] = state.Item
			items = append(items, state.Item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })

	legacy, err := readLegacyLLMAppResults(filepath.Join(m.runDir(runID), "results.jsonl"))
	if err != nil {
		return nil, err
	}
	pending := make([]llmAppWorkItem, 0, len(items))
	settled := make([]LLMAppItemResult, 0, len(items))
	planRevisions := make(map[string]int, len(items))
	recoveredEffects := 0
	itemErrors := 0
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.ID] {
			return nil, fmt.Errorf("duplicate llm_app item id %q", item.ID)
		}
		seen[item.ID] = true
		state, exists := stateByID[item.ID]
		if !exists {
			if result, ok := legacy[item.ID]; ok {
				copyResult := result
				state = llmAppItemState{Item: item, Status: llmAppItemSettled, Result: &copyResult}
			} else {
				state = llmAppItemState{Item: item, Status: llmAppItemPending}
			}
			if err := m.writeItemState(run, state); err != nil {
				return nil, fmt.Errorf("initialize item checkpoint: %w", err)
			}
		}
		if state.Status == llmAppItemEffectPending {
			if state.ReplayPolicy == llmAppReplayNever {
				return nil, fmt.Errorf("item %s has an unsettled non-replayable effect %s; manual reconciliation is required", item.ID, state.EffectID)
			}
			state.Status = llmAppItemPending
			state.RecoveredEffectCount++
			recoveredEffects++
			if err := m.writeItemState(run, state); err != nil {
				return nil, fmt.Errorf("recover item checkpoint: %w", err)
			}
		}
		switch state.Status {
		case llmAppItemSettled:
			if state.Result == nil {
				return nil, fmt.Errorf("settled item %s has no result", item.ID)
			}
			settled = append(settled, *state.Result)
			if state.Result.Error != "" {
				itemErrors++
			}
		case llmAppItemPending, "":
			pending = append(pending, item)
			if state.PlanRevision > 0 {
				planRevisions[item.ID] = state.PlanRevision
			}
		default:
			return nil, fmt.Errorf("item %s has unsupported checkpoint status %q", item.ID, state.Status)
		}
	}

	run.mu.Lock()
	run.pendingItems = pending
	run.itemPlanRevisions = planRevisions
	run.inFlightItems = 0
	run.queueOpen = true
	run.record.Progress = LLMAppProgress{Completed: len(settled), Total: len(items)}
	if len(items) > 0 {
		run.record.Progress.Percent = float64(len(settled)) * 100 / float64(len(items))
	}
	run.record.ItemErrors = itemErrors
	if recoveredEffects > 0 {
		run.record.Usage.UnknownModelCalls += recoveredEffects
	}
	run.record.CurrentItem = ""
	run.record.Checkpoint = fmt.Sprintf("%d/%d", len(settled), len(items))
	run.record.LastProgressAt = time.Now()
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return nil, err
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].Index < settled[j].Index })
	return settled, nil
}

func (m *LLMAppManager) checkpointItemClaim(
	run *llmAppManagedRun,
	item llmAppWorkItem,
	plan LLMAppRunPlan,
) error {
	state, err := m.readItemState(run.snapshot().RunID, item)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		state = llmAppItemState{Item: item, Status: llmAppItemPending}
	}
	if state.Status == llmAppItemSettled {
		return errors.New("cannot claim a settled llm_app item")
	}
	if state.PlanRevision == 0 {
		state.PlanRevision = plan.Revision
		state.PlanDigest = plan.Digest
	}
	return m.writeItemState(run, state)
}

func (m *LLMAppManager) checkpointItemEffect(
	run *llmAppManagedRun,
	item llmAppWorkItem,
	plan LLMAppRunPlan,
	validationAttempt int,
	messages []map[string]interface{},
	effectID string,
	replayPolicy string,
) error {
	state, err := m.readItemState(run.snapshot().RunID, item)
	if err != nil {
		return err
	}
	state.Status = llmAppItemEffectPending
	state.PlanRevision = plan.Revision
	state.PlanDigest = plan.Digest
	state.ValidationAttempt = validationAttempt
	state.Messages = cloneLLMAppMessages(messages)
	state.EffectID = effectID
	state.ReplayPolicy = replayPolicy
	state.Result = nil
	return m.writeItemState(run, state)
}

func (m *LLMAppManager) checkpointItemRetry(
	run *llmAppManagedRun,
	item llmAppWorkItem,
	validationAttempt int,
	messages []map[string]interface{},
) error {
	state, err := m.readItemState(run.snapshot().RunID, item)
	if err != nil {
		return err
	}
	state.Status = llmAppItemPending
	state.ValidationAttempt = validationAttempt
	state.Messages = cloneLLMAppMessages(messages)
	state.EffectID = ""
	state.ReplayPolicy = ""
	return m.writeItemState(run, state)
}

func (m *LLMAppManager) settleItemState(run *llmAppManagedRun, item llmAppWorkItem, result LLMAppItemResult) error {
	state, err := m.readItemState(run.snapshot().RunID, item)
	if err != nil {
		return err
	}
	copyResult := result
	state.Status = llmAppItemSettled
	state.Result = &copyResult
	state.Messages = nil
	state.EffectID = ""
	state.ReplayPolicy = ""
	return m.writeItemState(run, state)
}

func (m *LLMAppManager) collectSettledItemResults(runID string) ([]LLMAppItemResult, error) {
	states, err := m.loadItemStates(runID)
	if err != nil {
		return nil, err
	}
	results := make([]LLMAppItemResult, 0, len(states))
	for _, state := range states {
		if state.Status == llmAppItemSettled && state.Result != nil {
			results = append(results, *state.Result)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Index < results[j].Index })
	return results, nil
}

func readLegacyLLMAppResults(path string) (map[string]LLMAppItemResult, error) {
	results := make(map[string]LLMAppItemResult)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var result LLMAppItemResult
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			return nil, fmt.Errorf("decode legacy item result: %w", err)
		}
		if result.ID != "" {
			results[result.ID] = result
		}
	}
	return results, scanner.Err()
}

func readLLMAppWorkItems(path string) ([]llmAppWorkItem, error) {
	items := []llmAppWorkItem{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return items, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var item llmAppWorkItem
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode persisted llm_app input: %w", err)
		}
		items = append(items, item)
	}
	return items, scanner.Err()
}

func cloneLLMAppMessages(messages []map[string]interface{}) []map[string]interface{} {
	if len(messages) == 0 {
		return nil
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	var clone []map[string]interface{}
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil
	}
	return clone
}
