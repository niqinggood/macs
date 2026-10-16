// Package agentloop contains provider-neutral state machines used by one Agent turn.
package agentloop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"mac/internal/toolcontract"
)

type pendingToolRecovery struct {
	directive   toolcontract.RecoveryDirective
	toolName    string
	path        string
	failures    int
	promptCount int
	refreshed   bool
}

// ToolRecoveryTracker owns bounded recovery obligations for one agent turn.
// A recoverable failure remains pending until the corresponding mutation
// succeeds. If the model tries to finish early, the host injects a corrective
// control message instead of silently accepting a false success report.
type ToolRecoveryTracker struct {
	mu      sync.Mutex
	pending map[string]*pendingToolRecovery
}

func NewToolRecoveryTracker() *ToolRecoveryTracker {
	return &ToolRecoveryTracker{pending: make(map[string]*pendingToolRecovery)}
}

func (t *ToolRecoveryTracker) Observe(call toolcontract.Call, result toolcontract.Result, succeeded bool) {
	if t == nil {
		return
	}
	callPath := normalizeRecoveryPath(firstStringArg(call.Arguments, "file_path", "path", "file"))
	t.mu.Lock()
	defer t.mu.Unlock()

	if succeeded && (strings.EqualFold(call.Name, "edit_file") || strings.EqualFold(call.Name, "multiedit")) {
		for key, state := range t.pending {
			if callPath == "" || state.path == "" || sameRecoveryPath(callPath, state.path) {
				delete(t.pending, key)
			}
		}
		return
	}

	if strings.EqualFold(call.Name, "read_file") && succeeded {
		for _, state := range t.pending {
			if callPath != "" && sameRecoveryPath(callPath, state.path) {
				state.refreshed = true
			}
		}
	}

	if result.Recovery == nil || strings.TrimSpace(result.Recovery.Code) == "" {
		return
	}
	directive := *result.Recovery
	if directive.MaxPrompts <= 0 {
		directive.MaxPrompts = 1
	}
	keyPath := callPath
	if keyPath == "" {
		keyPath = normalizeRecoveryPath(strings.TrimPrefix(directive.ResourceKey, "file:"))
	}
	key := strings.ToLower(strings.TrimSpace(directive.Code)) + ":" + keyPath
	state, exists := t.pending[key]
	if !exists {
		state = &pendingToolRecovery{toolName: call.Name, path: keyPath}
		t.pending[key] = state
	}
	state.directive = directive
	state.failures++
}

func (t *ToolRecoveryTracker) NextControlMessage() (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := make([]string, 0, len(t.pending))
	for key, state := range t.pending {
		if state.promptCount < state.directive.MaxPrompts {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	sort.Strings(keys)
	state := t.pending[keys[0]]
	state.promptCount++
	args, _ := json.Marshal(state.directive.Arguments)
	refreshStatus := "The failed tool result already contains a fresh excerpt read by the host."
	if state.refreshed {
		refreshStatus = "The suggested read_file refresh has completed."
	}
	message := fmt.Sprintf(`[RECOVERABLE TOOL FAILURE: %s]
Do not finish the task or claim the edit succeeded yet. %s
Next, call %s with %s if another read is needed, then retry %s using a different exact old_string copied from the current file.
Recovery rule: %s
This is bounded recovery prompt %d/%d. Never repeat the failed tool arguments unchanged.`,
		state.directive.Code,
		refreshStatus,
		state.directive.SuggestedTool,
		string(args),
		state.toolName,
		state.directive.Instruction,
		state.promptCount,
		state.directive.MaxPrompts,
	)
	return message, true
}

func (t *ToolRecoveryTracker) HasPending() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending) > 0
}

func normalizeRecoveryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
}

func sameRecoveryPath(left, right string) bool {
	left = strings.TrimPrefix(normalizeRecoveryPath(left), "./")
	right = strings.TrimPrefix(normalizeRecoveryPath(right), "./")
	if left == right {
		return true
	}
	return strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left)
}

func firstStringArg(arguments map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
