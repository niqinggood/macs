package toolcontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
)

// ====================== Guardrail Config ======================
// 借鉴 Hermes ToolGuardrailConfig + Claude Code circuit-breaker

type GuardrailConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"` // 总开关，默认 true

	// Exact failure: same tool + same args failing repeatedly
	ExactFailureWarnAfter  int `json:"exact_failure_warn_after" yaml:"exact_failure_warn_after"`   // 默认 2
	ExactFailureBlockAfter int `json:"exact_failure_block_after" yaml:"exact_failure_block_after"` // 默认 5

	// Same-tool failure: different args, same tool failing
	SameToolFailureWarnAfter int `json:"same_tool_failure_warn_after" yaml:"same_tool_failure_warn_after"` // 默认 3
	SameToolFailureHaltAfter int `json:"same_tool_failure_halt_after" yaml:"same_tool_failure_halt_after"` // 默认 8

	// No-progress: read-only tools returning same result
	NoProgressWarnAfter  int `json:"no_progress_warn_after" yaml:"no_progress_warn_after"`   // 默认 2
	NoProgressBlockAfter int `json:"no_progress_block_after" yaml:"no_progress_block_after"` // 默认 5

	// Circuit breaker: max consecutive failures across all tools before halting the turn
	MaxConsecutiveFailures int `json:"max_consecutive_failures" yaml:"max_consecutive_failures"` // 默认 5

	// Result deduplication: skip re-displaying result if same content hash seen
	ResultDedupEnabled bool `json:"result_dedup_enabled" yaml:"result_dedup_enabled"` // 默认 true

	// Pre-action gate: warn before re-attempting known failure patterns
	PreActionGateEnabled bool `json:"pre_action_gate_enabled" yaml:"pre_action_gate_enabled"` // 默认 true

	// Diagnostic injection: after N same-signature failures, inject troubleshooting guidance
	DiagnosticAfterFailures int `json:"diagnostic_after_failures" yaml:"diagnostic_after_failures"` // 默认 2
}

// DefaultGuardrailConfig returns sensible defaults.
func DefaultGuardrailConfig() GuardrailConfig {
	return GuardrailConfig{
		Enabled:                  true,
		ExactFailureWarnAfter:    2,
		ExactFailureBlockAfter:   5,
		SameToolFailureWarnAfter: 3,
		SameToolFailureHaltAfter: 8,
		NoProgressWarnAfter:      2,
		NoProgressBlockAfter:     5,
		MaxConsecutiveFailures:   5,
		ResultDedupEnabled:       true,
		PreActionGateEnabled:     true,
		DiagnosticAfterFailures:  2,
	}
}

// MergeGuardrailConfig merges user config with defaults (zero values → use default).
func MergeGuardrailConfig(user GuardrailConfig) GuardrailConfig {
	d := DefaultGuardrailConfig()
	if !user.Enabled && user.ExactFailureWarnAfter == 0 {
		// If user explicitly set Enabled=false, keep that; otherwise merge field-by-field
	}
	mergeInt := func(target *int, userVal, defaultVal int) {
		if userVal > 0 {
			*target = userVal
		} else {
			*target = defaultVal
		}
	}
	mergeInt(&d.ExactFailureWarnAfter, user.ExactFailureWarnAfter, DefaultGuardrailConfig().ExactFailureWarnAfter)
	mergeInt(&d.ExactFailureBlockAfter, user.ExactFailureBlockAfter, DefaultGuardrailConfig().ExactFailureBlockAfter)
	mergeInt(&d.SameToolFailureWarnAfter, user.SameToolFailureWarnAfter, DefaultGuardrailConfig().SameToolFailureWarnAfter)
	mergeInt(&d.SameToolFailureHaltAfter, user.SameToolFailureHaltAfter, DefaultGuardrailConfig().SameToolFailureHaltAfter)
	mergeInt(&d.NoProgressWarnAfter, user.NoProgressWarnAfter, DefaultGuardrailConfig().NoProgressWarnAfter)
	mergeInt(&d.NoProgressBlockAfter, user.NoProgressBlockAfter, DefaultGuardrailConfig().NoProgressBlockAfter)
	mergeInt(&d.MaxConsecutiveFailures, user.MaxConsecutiveFailures, DefaultGuardrailConfig().MaxConsecutiveFailures)
	mergeInt(&d.DiagnosticAfterFailures, user.DiagnosticAfterFailures, DefaultGuardrailConfig().DiagnosticAfterFailures)
	if user.ExactFailureWarnAfter > 0 || user.ExactFailureBlockAfter > 0 {
		d.Enabled = user.Enabled // respect explicit enable/disable when thresholds are set
	}
	d.ResultDedupEnabled = user.ResultDedupEnabled || (!user.Enabled && d.ResultDedupEnabled)
	d.PreActionGateEnabled = user.PreActionGateEnabled || (!user.Enabled && d.PreActionGateEnabled)
	return d
}

// ====================== Tool Call Signature ======================
// 借鉴 Hermes ToolCallSignature — stable identity for (tool, canonicalArgs)

type ToolCallSignature struct {
	ToolName string `json:"tool_name"`
	ArgsHash string `json:"args_hash"` // sha256 of compact sorted JSON
}

// NewToolCallSignature builds a signature from tool name and arguments.
func NewToolCallSignature(toolName string, args map[string]interface{}) ToolCallSignature {
	canonical := canonicalArgs(args)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	return ToolCallSignature{ToolName: toolName, ArgsHash: hash}
}

// String returns a compact representation for logging.
func (s ToolCallSignature) String() string {
	return fmt.Sprintf("%s:%s", s.ToolName, s.ArgsHash[:12])
}

// canonicalArgs produces sorted compact JSON for stable hashing.
func canonicalArgs(args map[string]interface{}) string {
	if args == nil {
		return "{}"
	}
	// sort keys for canonical form
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(",")
		}
		valJSON, _ := json.Marshal(args[k])
		sb.WriteString(fmt.Sprintf("%q:%s", k, string(valJSON)))
	}
	sb.WriteString("}")
	return sb.String()
}

// ====================== Guardrail Decision ======================
// 借鉴 Hermes ToolGuardrailDecision

type GuardrailAction string

const (
	ActionAllow GuardrailAction = "allow" // proceed normally
	ActionWarn  GuardrailAction = "warn"  // proceed but emit warning
	ActionBlock GuardrailAction = "block" // block this specific call
	ActionHalt  GuardrailAction = "halt"  // halt the entire turn
)

type GuardrailDecision struct {
	Action    GuardrailAction    `json:"action"`
	Code      string             `json:"code"`    // machine-readable reason
	Message   string             `json:"message"` // human-readable explanation
	ToolName  string             `json:"tool_name"`
	Count     int                `json:"count"` // how many times this pattern has occurred
	Signature *ToolCallSignature `json:"signature,omitempty"`
}

// AllowsExecution returns true if the tool should still execute.
func (d GuardrailDecision) AllowsExecution() bool {
	return d.Action == ActionAllow || d.Action == ActionWarn
}

// ShouldHaltTurn returns true if the entire turn should stop.
func (d GuardrailDecision) ShouldHaltTurn() bool {
	return d.Action == ActionHalt
}

// ====================== Tool Guardrail Controller ======================

type ToolGuardrailController struct {
	config                    GuardrailConfig
	readOnlyByName            ReadOnlyNameClassifier
	extractErrorSignature     func(string) string
	generateFailureDiagnostic func(string, string, int) string

	// Per-turn state
	exactFailures       map[ToolCallSignature]int // sig → consecutive failure count
	sameToolFailures    map[string]int            // toolName → consecutive failure count
	resultHashes        map[string]int            // result hash → occurrence count (for no-progress)
	resultContentHashes map[string]int            // contentHash → occurrence count (for dedup)
	failTracker         map[string]int            // "tool:errorSig" → count
	consecutiveFailures int                       // circuit breaker counter
	callOrder           []ToolCallSignature       // ordered calls for review

	// Diagnostic injection dedup
	diagnosticInjected map[string]bool // failKey → already injected

	// Last tool result tracking for sequence analysis
	lastToolOK bool

	mu sync.Mutex

	// Config callback for logging
	logf func(format string, args ...interface{})
}

type GuardrailDependencies struct {
	ReadOnlyByName            ReadOnlyNameClassifier
	ExtractErrorSignature     func(string) string
	GenerateFailureDiagnostic func(string, string, int) string
}

// NewToolGuardrailController creates a new controller.
func NewToolGuardrailController(cfg GuardrailConfig, dependencies ...GuardrailDependencies) *ToolGuardrailController {
	if cfg.Enabled {
		defaults := DefaultGuardrailConfig()
		if cfg.ExactFailureWarnAfter <= 0 {
			cfg.ExactFailureWarnAfter = defaults.ExactFailureWarnAfter
		}
		if cfg.ExactFailureBlockAfter <= 0 {
			cfg.ExactFailureBlockAfter = defaults.ExactFailureBlockAfter
		}
		if cfg.ExactFailureBlockAfter < cfg.ExactFailureWarnAfter {
			cfg.ExactFailureBlockAfter = cfg.ExactFailureWarnAfter
		}
		if cfg.SameToolFailureWarnAfter <= 0 {
			cfg.SameToolFailureWarnAfter = defaults.SameToolFailureWarnAfter
		}
		if cfg.SameToolFailureHaltAfter <= 0 {
			cfg.SameToolFailureHaltAfter = defaults.SameToolFailureHaltAfter
		}
		if cfg.SameToolFailureHaltAfter < cfg.SameToolFailureWarnAfter {
			cfg.SameToolFailureHaltAfter = cfg.SameToolFailureWarnAfter
		}
		if cfg.NoProgressWarnAfter <= 0 {
			cfg.NoProgressWarnAfter = defaults.NoProgressWarnAfter
		}
		if cfg.NoProgressBlockAfter <= 0 {
			cfg.NoProgressBlockAfter = defaults.NoProgressBlockAfter
		}
		if cfg.NoProgressBlockAfter < cfg.NoProgressWarnAfter {
			cfg.NoProgressBlockAfter = cfg.NoProgressWarnAfter
		}
		if cfg.MaxConsecutiveFailures <= 0 {
			cfg.MaxConsecutiveFailures = defaults.MaxConsecutiveFailures
		}
		if cfg.DiagnosticAfterFailures <= 0 {
			cfg.DiagnosticAfterFailures = defaults.DiagnosticAfterFailures
		}
	}
	controller := &ToolGuardrailController{
		config:              cfg,
		exactFailures:       make(map[ToolCallSignature]int),
		sameToolFailures:    make(map[string]int),
		resultHashes:        make(map[string]int),
		resultContentHashes: make(map[string]int),
		failTracker:         make(map[string]int),
		diagnosticInjected:  make(map[string]bool),
		lastToolOK:          true,
		logf:                func(format string, args ...interface{}) { log.Printf(format, args...) },
	}
	if len(dependencies) > 0 {
		controller.readOnlyByName = dependencies[0].ReadOnlyByName
		controller.extractErrorSignature = dependencies[0].ExtractErrorSignature
		controller.generateFailureDiagnostic = dependencies[0].GenerateFailureDiagnostic
	}
	return controller
}

// SetLogger sets the logging function.
func (c *ToolGuardrailController) SetLogger(logf func(format string, args ...interface{})) {
	if logf != nil {
		c.logf = logf
	}
}

// Reset clears per-turn state (call at the start of each agent turn).
func (c *ToolGuardrailController) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exactFailures = make(map[ToolCallSignature]int)
	c.sameToolFailures = make(map[string]int)
	c.resultHashes = make(map[string]int)
	c.resultContentHashes = make(map[string]int)
	c.failTracker = make(map[string]int)
	c.consecutiveFailures = 0
	c.callOrder = nil
	c.diagnosticInjected = make(map[string]bool)
	c.lastToolOK = true
}

func (c *ToolGuardrailController) errorSignature(output string) string {
	if c.extractErrorSignature != nil {
		return c.extractErrorSignature(output)
	}
	output = strings.ToLower(strings.TrimSpace(output))
	if len(output) > 80 {
		return output[:80]
	}
	return output
}

func (c *ToolGuardrailController) failureDiagnostic(toolName, output string, count int) string {
	if c.generateFailureDiagnostic != nil {
		return c.generateFailureDiagnostic(toolName, output, count)
	}
	return fmt.Sprintf("[TROUBLESHOOTING] The tool %q failed %d times. Correct the inputs or use a different approach before retrying.\n\nOriginal error: %s", toolName, count, CompactToolFailure(output))
}

// CheckBefore evaluates whether a tool call should proceed, be warned about,
// or be blocked. Call before every tool execution.
func (c *ToolGuardrailController) CheckBefore(toolName string, args map[string]interface{}) GuardrailDecision {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.config.Enabled {
		return GuardrailDecision{Action: ActionAllow, Code: "disabled"}
	}

	sig := NewToolCallSignature(toolName, args)

	// 1. Exact duplicate failure check
	if count := c.exactFailures[sig]; count >= c.config.ExactFailureBlockAfter {
		return GuardrailDecision{
			Action:    ActionBlock,
			Code:      "exact_failure_blocked",
			Message:   fmt.Sprintf("[LOOP BLOCKED] Exact same call to '%s' with the same arguments already failed %d times. Do not execute it again.", toolName, count),
			ToolName:  toolName,
			Count:     count,
			Signature: &sig,
		}
	} else if count >= c.config.ExactFailureWarnAfter {
		return GuardrailDecision{
			Action:    ActionWarn,
			Code:      "exact_failure_warn",
			Message:   fmt.Sprintf("[WARNING] '%s' with these args has failed %d times. Consider a different approach.", toolName, count),
			ToolName:  toolName,
			Count:     count,
			Signature: &sig,
		}
	}

	// 2. Same-tool failure check (different args, same tool)
	if count := c.sameToolFailures[toolName]; count >= c.config.SameToolFailureHaltAfter {
		return GuardrailDecision{
			Action:   ActionWarn,
			Code:     "same_tool_failure_observed",
			Message:  fmt.Sprintf("[OBSERVE] Tool '%s' has failed %d times with different arguments.", toolName, count),
			ToolName: toolName,
			Count:    count,
		}
	} else if count >= c.config.SameToolFailureWarnAfter {
		return GuardrailDecision{
			Action:   ActionWarn,
			Code:     "same_tool_failure_warn",
			Message:  fmt.Sprintf("[WARNING] Tool '%s' has failed %d times. The tool itself may be unsuitable for this task.", toolName, count),
			ToolName: toolName,
			Count:    count,
		}
	}

	// Track call order
	c.callOrder = append(c.callOrder, sig)

	return GuardrailDecision{Action: ActionAllow, Code: "ok"}
}

// RecordAfter records the result of a tool execution. Call after every tool execution.
// Returns a (possibly modified) result content — e.g. with dedup applied.
func (c *ToolGuardrailController) RecordAfter(toolName string, args map[string]interface{}, resultContent string, err error, executionOutcomes ...*bool) (string, *GuardrailDecision) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sig := NewToolCallSignature(toolName, args)
	success := ToolExecutionSucceededForTool(toolName, resultContent, err)
	if err == nil && len(executionOutcomes) > 0 && executionOutcomes[0] != nil {
		success = *executionOutcomes[0]
	}

	// Update consecutive failure tracker
	if success {
		c.consecutiveFailures = 0
		c.lastToolOK = true
	} else {
		c.consecutiveFailures++
		c.lastToolOK = false
	}

	// --- Failure tracking ---
	if !success {
		errSig := c.errorSignature(resultContent)
		failKey := toolName + "|" + errSig

		// Exact duplicate failure
		c.exactFailures[sig]++

		// Same tool failure (different args)
		foundExact := false
		for existingSig := range c.exactFailures {
			if existingSig.ToolName == toolName && existingSig.ArgsHash == sig.ArgsHash {
				foundExact = true
				break
			}
		}
		if !foundExact || c.exactFailures[sig] == 1 {
			c.sameToolFailures[toolName]++
		}

		// Error signature tracking
		c.failTracker[failKey]++

		// Circuit breaker check
		if c.config.Enabled && c.consecutiveFailures >= c.config.MaxConsecutiveFailures {
			decision := &GuardrailDecision{
				Action:   ActionWarn,
				Code:     "consecutive_failures_observed",
				Message:  fmt.Sprintf("[OBSERVE] %d consecutive tool failures.", c.consecutiveFailures),
				ToolName: toolName,
				Count:    c.consecutiveFailures,
			}
			c.logf("[GUARDRAIL] %s", decision.Message)
			return resultContent, decision
		}

		// Check for diagnostic injection
		if c.failTracker[failKey] >= c.config.DiagnosticAfterFailures && !c.diagnosticInjected[failKey] {
			c.diagnosticInjected[failKey] = true
			decision := &GuardrailDecision{
				Action:   ActionAllow,
				Code:     "diagnostic_needed",
				Message:  c.failureDiagnostic(toolName, resultContent, c.failTracker[failKey]),
				ToolName: toolName,
				Count:    c.failTracker[failKey],
			}
			c.logf("[GUARDRAIL] Diagnostic injected for %s (fail #%d)", failKey, c.failTracker[failKey])
			return resultContent, decision
		}

		return resultContent, nil
	}

	// --- Success tracking ---
	// Exact retry failures are valid only while the underlying mutable state is
	// unchanged. A successful structured mutation (for example, correcting a
	// deck specification) makes an earlier build failure stale and must allow
	// the same deterministic build command to run against the new state.
	if toolGuardrailStateMutation(toolName) {
		c.exactFailures = make(map[ToolCallSignature]int)
		c.sameToolFailures = make(map[string]int)
		c.failTracker = make(map[string]int)
		c.diagnosticInjected = make(map[string]bool)
		c.resultHashes = make(map[string]int)
		c.resultContentHashes = make(map[string]int)
	}
	// Reset failure counters on success for this specific signature
	c.exactFailures[sig] = 0
	// Reset same-tool counter on success (a successful call resets the tool's failure streak)
	c.sameToolFailures[toolName] = 0

	// Result deduplication (借鉴 Claude Code: semantic dedup)
	if c.config.ResultDedupEnabled && len(resultContent) > 200 {
		hash := shortHash(resultContent)
		sigKey := toolName + ":" + hash
		if prevIdx, ok := c.resultContentHashes[sigKey]; ok {
			deduped := fmt.Sprintf("[Duplicate result only - %s executed successfully; output matches call #%d. Do not rerun.]", toolName, prevIdx)
			c.logf("[GUARDRAIL] Dedup: %s result matches call #%d", toolName, prevIdx)
			return deduped, nil
		}
		c.resultContentHashes[sigKey] = len(c.resultContentHashes) + 1
	}

	// No-progress detection (read-only tools returning same hash)
	if c.readOnlyByName != nil && c.readOnlyByName(toolName) && len(resultContent) < 4000 {
		resultHash := shortHash(resultContent)
		c.resultHashes[resultHash]++
		count := c.resultHashes[resultHash]

		if c.config.Enabled && count >= c.config.NoProgressBlockAfter {
			decision := &GuardrailDecision{
				Action:   ActionWarn,
				Code:     "no_progress_observed",
				Message:  fmt.Sprintf("[OBSERVE] %s returned the same result %d times.", toolName, count),
				ToolName: toolName,
				Count:    count,
			}
			c.logf("[GUARDRAIL] %s", decision.Message)
			return resultContent, decision
		}
		if count >= c.config.NoProgressWarnAfter {
			c.logf("[GUARDRAIL] No-progress: %s returned same result %d times", toolName, count)
		}
	}

	return resultContent, nil
}

func toolGuardrailStateMutation(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write_file", "edit_file", "multi_edit", "multiedit", "json_patch", "apply_patch", "convert":
		return true
	default:
		return false
	}
}

// PreActionGate checks ALL pending tool calls before batch execution and returns
// warnings for any that match known failure patterns. (借鉴 Hermes pre-action gate)
func (c *ToolGuardrailController) PreActionGate(toolCalls []ToolCall) []GuardrailDecision {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.config.Enabled || !c.config.PreActionGateEnabled {
		return nil
	}

	var warnings []GuardrailDecision
	for _, tc := range toolCalls {
		for failKey, count := range c.failTracker {
			parts := strings.SplitN(failKey, "|", 2)
			if len(parts) == 2 && parts[0] == tc.Name && count >= 3 {
				warnings = append(warnings, GuardrailDecision{
					Action:   ActionWarn,
					Code:     "pre_action_gate",
					Message:  fmt.Sprintf("[Recovery hint] Tool '%s' has failed with '%s' %d times. Correct the inputs, path, launcher, or environment before retrying; local and read-only setup may continue after a concrete correction.", tc.Name, parts[1], count),
					ToolName: tc.Name,
					Count:    count,
				})
			}
		}
	}
	return warnings
}

func BuildToolRetryGuardControl(
	warnings []GuardrailDecision,
	toolCalls []ToolCall,
	failTracker map[string]int,
) string {
	var items []string
	seen := make(map[string]bool)
	add := func(key, message string) {
		key = strings.TrimSpace(key)
		message = strings.TrimSpace(message)
		if key == "" || message == "" || seen[key] || len(items) >= 6 {
			return
		}
		seen[key] = true
		items = append(items, "- "+message)
	}
	for _, warning := range warnings {
		add(
			warning.ToolName+"|"+warning.Code,
			warning.Message,
		)
	}
	for _, call := range toolCalls {
		prefix := call.Name + ":"
		for failKey, count := range failTracker {
			if count < 3 || !strings.HasPrefix(failKey, prefix) {
				continue
			}
			errorSignature := strings.TrimSpace(strings.TrimPrefix(failKey, prefix))
			add(
				call.Name+"|"+errorSignature,
				fmt.Sprintf(
					"Tool '%s' has already failed with '%s' %d times. Correct the inputs, path, launcher, or environment before retrying. Local/read-only preparation may continue after a concrete correction; at-most-once limits apply only to confirmed external mutations.",
					call.Name,
					errorSignature,
					count,
				),
			)
		}
	}
	if len(items) == 0 {
		return ""
	}
	return "Repeated tool failures were detected before this tool turn:\n" +
		strings.Join(items, "\n") +
		"\nUse the results from this turn, then make one concrete correction or choose a materially different recovery step. Do not repeat an unchanged no-progress call."
}

// BuildToolReview generates a structured tool review message for the agent,
// summarizing failure patterns and suggesting broken tools to avoid.
// (借鉴 Claude Code tool review)
func (c *ToolGuardrailController) BuildToolReview(taskID string, extraBrokenTools map[string]bool) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("## 工具执行审查\n\n")

	// Summary stats
	totalCalls := len(c.callOrder)
	sb.WriteString(fmt.Sprintf("已执行 %d 次工具调用。\n\n", totalCalls))

	// Failure patterns
	if len(c.failTracker) > 0 {
		sb.WriteString("### 失败模式\n")
		// Sort by count desc
		type failEntry struct {
			key   string
			count int
		}
		var entries []failEntry
		for k, v := range c.failTracker {
			entries = append(entries, failEntry{k, v})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })
		for _, e := range entries {
			if e.count >= 2 {
				sb.WriteString(fmt.Sprintf("- %s: %d 次\n", e.key, e.count))
			}
		}
		sb.WriteString("\n")
	}

	// Broken tools
	broken := make(map[string]bool)
	for toolName, count := range c.sameToolFailures {
		if count >= 3 {
			broken[toolName] = true
		}
	}
	for t := range extraBrokenTools {
		broken[t] = true
	}
	if len(broken) > 0 {
		sb.WriteString("### 高失败率工具（仅统计，不影响可用性）\n")
		for t := range broken {
			sb.WriteString(fmt.Sprintf("- **%s**: 多次失败，仅用于定位原因，工具未禁用\n", t))
		}
		sb.WriteString("\n")
	}

	// No-progress tools
	if len(c.resultHashes) > 0 {
		for toolName := range c.sameToolFailures {
			// Check if this tool also had no-progress
			for _, count := range c.resultHashes {
				if count >= 3 && c.sameToolFailures[toolName] >= 2 {
					sb.WriteString(fmt.Sprintf("- **%s**: 重复返回相同信息，无新进展\n", toolName))
					break
				}
			}
		}
	}

	if sb.Len() == len("## 工具执行审查\n\n已执行 0 次工具调用。\n\n") {
		return "" // nothing to report
	}

	return sb.String()
}

// GetFailTracker returns a copy of the fail tracker for external use.
func (c *ToolGuardrailController) GetFailTracker() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.failTracker))
	for k, v := range c.failTracker {
		out[k] = v
	}
	return out
}

// shortHash returns the first 8 chars of sha256 hex.
func shortHash(content string) string {
	limit := len(content)
	if limit > 2000 {
		limit = 2000
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content[:limit])))
	return hash[:16]
}
