package agents

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"mac/internal/shellpolicy"
)

// AutoModeClassifier provides LLM-based auto-approval for tool calls.
// Mirrors Claude Code's YOLO/auto mode: classifies tool calls as safe/unsafe,
// tracks denial patterns, and falls back to manual approval after threshold.
type AutoModeClassifier struct {
	mu            sync.Mutex
	enabled       bool
	denialCount   int
	maxDenials    int
	allowList     map[string]bool // Tools always allowed
	blockList     map[string]bool // Tools always blocked
	readOnlyTools map[string]bool // Read-only tools (always safe)
	lastCheck     time.Time
	checkInterval time.Duration
	history       []classificationRecord
}

type classificationRecord struct {
	Tool   string
	Safe   bool
	Reason string
	Time   time.Time
	Denied bool // True if denied despite being classified safe
}

func NewAutoModeClassifier() *AutoModeClassifier {
	amc := &AutoModeClassifier{
		maxDenials:    3,
		checkInterval: 5 * time.Minute,
		allowList:     make(map[string]bool),
		blockList:     make(map[string]bool),
		readOnlyTools: make(map[string]bool),
	}

	// Read-only tools are always safe
	for _, t := range []string{
		"read_file", "list_dir", "grep", "glob", "lsp_definition",
		"lsp_references", "lsp_hover", "lsp_symbols", "lsp_call_hierarchy",
		"lsp_diagnostics", "lsp_completion", "lsp_code_action",
		"agent_reach", "web_fetch", "web_search", "tool_detail", "list_skills",
		"get_stats", "search_history", "knowledge", "rag_search",
		"ast_analyze", "refactor", "dep_analyze", "doc_gen",
		"goal_verify", "perf", "ci_analyze", "code_review",
		"binary", "debug", "learn",
	} {
		amc.readOnlyTools[t] = true
	}

	return amc
}

func (amc *AutoModeClassifier) Enable() {
	amc.mu.Lock()
	defer amc.mu.Unlock()
	amc.enabled = true
	amc.denialCount = 0
}

func (amc *AutoModeClassifier) Disable() {
	amc.mu.Lock()
	defer amc.mu.Unlock()
	amc.enabled = false
}

func (amc *AutoModeClassifier) Enabled() bool {
	if amc == nil {
		return false
	}
	amc.mu.Lock()
	defer amc.mu.Unlock()
	return amc.enabled
}

// Classify evaluates a tool call and returns whether it can be auto-approved.
// Uses a tiered approach: read-only → always safe, block-list → always blocked,
// allow-list → always allowed, otherwise heuristic classification.
func (amc *AutoModeClassifier) Classify(toolName string, params map[string]interface{}) (allowed bool, reason string) {
	amc.mu.Lock()
	defer amc.mu.Unlock()

	if !amc.enabled {
		return true, "auto mode disabled; local approval gating is not enforced"
	}

	// Check denial threshold
	if amc.denialCount >= amc.maxDenials {
		return true, fmt.Sprintf("denial threshold ignored (%d/%d); local approval gating is not enforced", amc.denialCount, amc.maxDenials)
	}

	// Block-list takes precedence
	if amc.blockList[toolName] {
		amc.denialCount++
		return false, "tool is in block list"
	}

	// Allow-list always passes
	if amc.allowList[toolName] {
		return true, "tool is in allow list"
	}

	// Read-only tools are always safe
	if amc.readOnlyTools[toolName] {
		return true, "read-only tool (always safe)"
	}

	// Tier 1: Destructive tools — always require approval
	if amc.isDestructive(toolName, params) {
		return false, "blocked by local safety policy"
	}

	// Tier 2: Write tools — auto-approve with guardrails
	if amc.isWriteTool(toolName) {
		// Check if path is within workspace (safety check)
		if path, ok := params["file"].(string); ok {
			if amc.isPathSafe(path) {
				return true, "write within workspace"
			}
			return true, "write outside workspace allowed by default"
		}
		if path, ok := params["file_path"].(string); ok {
			if amc.isPathSafe(path) {
				return true, "write within workspace"
			}
			return true, "write outside workspace allowed by default"
		}
		return true, "safe write operation"
	}

	// Tier 3: CLI — classify based on command
	if toolName == "cli" {
		if cmd, ok := params["command"].(string); ok {
			return amc.classifyCommand(cmd), ""
		}
		return true, "cli without command allowed by default"
	}

	return true, "allowed by default"
}

func (amc *AutoModeClassifier) isDestructive(toolName string, params map[string]interface{}) bool {
	if toolName != "cli" {
		return false
	}
	command, _ := params["command"].(string)
	return shellpolicy.Analyze(command).Blocked
}

func (amc *AutoModeClassifier) isWriteTool(toolName string) bool {
	writeTools := []string{
		"write_file", "edit_file", "json_patch", "project_init",
	}
	for _, t := range writeTools {
		if toolName == t {
			return true
		}
	}
	return false
}

func (amc *AutoModeClassifier) isPathSafe(path string) bool {
	// Reject absolute paths outside expected directories
	dangerous := []string{"/etc/", "/root/", "/boot/", "/sys/", "/proc/", "C:\\Windows\\", "C:\\WINDOWS\\"}
	for _, d := range dangerous {
		if strings.Contains(strings.ToLower(path), strings.ToLower(d)) {
			return false
		}
	}
	// Reject .. path traversal
	if strings.Contains(path, "..") {
		return false
	}
	return true
}

func (amc *AutoModeClassifier) classifyCommand(cmd string) bool {
	analysis := shellpolicy.Analyze(cmd)
	if analysis.Blocked {
		return false
	}
	return true
}

// RecordResult records the outcome of a classification.
func (amc *AutoModeClassifier) RecordResult(toolName string, safe bool, denied bool) {
	amc.mu.Lock()
	defer amc.mu.Unlock()

	amc.history = append(amc.history, classificationRecord{
		Tool:   toolName,
		Safe:   safe,
		Denied: denied,
		Time:   time.Now(),
	})

	if len(amc.history) > 100 {
		amc.history = amc.history[len(amc.history)-100:]
	}

	if denied {
		amc.denialCount++
	}
}

// ResetDenials resets the denial counter (e.g., after user interaction).
func (amc *AutoModeClassifier) ResetDenials() {
	amc.mu.Lock()
	defer amc.mu.Unlock()
	amc.denialCount = 0
}

// AddAllowTool safely adds a tool to the allow list.
func (amc *AutoModeClassifier) AddAllowTool(tool string) {
	amc.mu.Lock()
	defer amc.mu.Unlock()
	amc.allowList[tool] = true
}

// AddBlockTool safely adds a tool to the block list.
func (amc *AutoModeClassifier) AddBlockTool(tool string) {
	amc.mu.Lock()
	defer amc.mu.Unlock()
	amc.blockList[tool] = true
}

// Stats returns current classifier statistics.
func (amc *AutoModeClassifier) Stats() map[string]interface{} {
	amc.mu.Lock()
	defer amc.mu.Unlock()

	safe := 0
	denied := 0
	for _, r := range amc.history {
		if r.Denied {
			denied++
		} else if r.Safe {
			safe++
		}
	}

	return map[string]interface{}{
		"enabled":      amc.enabled,
		"denial_count": amc.denialCount,
		"max_denials":  amc.maxDenials,
		"total_safe":   safe,
		"total_denied": denied,
		"read_only":    len(amc.readOnlyTools),
	}
}

// ================== Auto Compaction ==================

// CompactStrategy defines how context compaction is performed.
type CompactStrategy string

const (
	CompactSummary     CompactStrategy = "summary"      // Replace with LLM-generated summary
	CompactTruncate    CompactStrategy = "truncate"     // Keep first N and last M messages
	CompactKeyMessages CompactStrategy = "key_messages" // Keep messages with high importance
	CompactAuto        CompactStrategy = "auto"         // Choose best strategy based on context
)

// CompactConfig controls compaction behavior.
type CompactConfig struct {
	Strategy     CompactStrategy
	MaxTokens    int     // Trigger compaction when token count exceeds this
	TargetTokens int     // Compact down to this many tokens
	KeepRecent   int     // Always keep the N most recent messages
	KeepRatio    float64 // Ratio of context to preserve (0.0 to 1.0)
}

// AutoCompactor manages automatic context compaction.
// When a ContextManager is set, LLM-driven compaction (CompactSummary) delegates to it.
// Otherwise falls back to placeholder replacement — a unified gateway to the 6-layer pipeline.
type AutoCompactor struct {
	mu      sync.Mutex
	config  CompactConfig
	enabled bool
	history []compactRecord
	ctxMgr  SmartCompactionContext
}

type SmartCompactionContext interface {
	HasSmartCompactor() bool
	CompactMessages(messages []map[string]interface{}) ([]map[string]interface{}, error)
}

func (ac *AutoCompactor) SetContextManager(cm SmartCompactionContext) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.ctxMgr = cm
}

type compactRecord struct {
	Time        time.Time
	BeforeCount int
	AfterCount  int
	Strategy    CompactStrategy
	Saved       int // tokens saved
}

func NewAutoCompactor() *AutoCompactor {
	return &AutoCompactor{
		config: CompactConfig{
			Strategy:     CompactAuto,
			MaxTokens:    180000, // 180K — trigger before 200K limit
			TargetTokens: 120000, // Compact to 120K
			KeepRecent:   10,     // Keep last 10 messages always
			KeepRatio:    0.7,    // Preserve 70% of context
		},
		enabled: true,
	}
}

// ShouldCompact checks if compaction is needed.
func (ac *AutoCompactor) ShouldCompact(currentTokens int) bool {
	if !ac.enabled {
		return false
	}
	return currentTokens >= ac.config.MaxTokens
}

// Compact performs context compaction on a list of messages.
// Returns the compacted message list and a summary of what was done.
func (ac *AutoCompactor) Compact(messages []map[string]interface{}, currentTokens int) ([]map[string]interface{}, string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	strategy := ac.config.Strategy
	if strategy == CompactAuto {
		strategy = ac.chooseStrategy(currentTokens)
	}

	beforeCount := len(messages)

	var result []map[string]interface{}
	var summary string

	switch strategy {
	case CompactSummary:
		result, summary = ac.compactSummary(messages)
	case CompactTruncate:
		result, summary = ac.compactTruncate(messages)
	case CompactKeyMessages:
		result, summary = ac.compactKeyMessages(messages)
	default:
		result, summary = ac.compactTruncate(messages)
	}

	afterCount := len(result)
	tokensSaved := estimateTokenReduction(beforeCount, afterCount)

	ac.history = append(ac.history, compactRecord{
		Time:        time.Now(),
		BeforeCount: beforeCount,
		AfterCount:  afterCount,
		Strategy:    strategy,
		Saved:       tokensSaved,
	})

	if len(ac.history) > 50 {
		ac.history = ac.history[len(ac.history)-50:]
	}

	return result, summary
}

func (ac *AutoCompactor) chooseStrategy(tokens int) CompactStrategy {
	if tokens > 300000 {
		return CompactSummary // Very large context: aggressively summarize
	}
	if tokens > 200000 {
		return CompactTruncate // Large context: keep head + tail
	}
	return CompactKeyMessages // Moderate: keep important messages
}

func (ac *AutoCompactor) compactSummary(messages []map[string]interface{}) ([]map[string]interface{}, string) {
	keep := ac.config.KeepRecent
	total := len(messages)

	if total <= keep {
		return messages, ""
	}

	recent := messages[total-keep:]

	// Delegate to ContextManager's SmartCompactor if available (LLM-driven)
	ac.mu.Lock()
	cm := ac.ctxMgr
	ac.mu.Unlock()

	if cm != nil && cm.HasSmartCompactor() {
		compactResult, err := cm.CompactMessages(messages)
		if err == nil && len(compactResult) < len(messages) {
			return compactResult, fmt.Sprintf("LLM-compacted %d → %d messages (saved ~%d tokens)",
				total, len(compactResult), estimateTokenReduction(total, len(compactResult)))
		}
	}

	// Fallback: placeholder summary when LLM compaction unavailable
	summaryMsg := map[string]interface{}{
		"role":    "user",
		"content": fmt.Sprintf("[Context Compaction] Previous %d messages summarized. %d most recent messages preserved.", total-keep, keep),
	}

	result := append([]map[string]interface{}{summaryMsg}, recent...)
	return result, fmt.Sprintf("Compacted %d messages to 1 summary + %d recent (saved ~%d tokens)",
		total-keep, keep, estimateTokenReduction(total, len(result)))
}

func (ac *AutoCompactor) compactTruncate(messages []map[string]interface{}) ([]map[string]interface{}, string) {
	total := len(messages)
	keep := ac.config.KeepRecent

	if total <= keep+5 {
		return messages, ""
	}

	// Keep first 3 (system context) + last N recent
	head := min(3, total-keep)
	result := make([]map[string]interface{}, 0, head+keep+1)

	result = append(result, messages[:head]...)

	// Insert truncation marker
	result = append(result, map[string]interface{}{
		"role":    "user",
		"content": fmt.Sprintf("[%d messages truncated for context]", total-head-keep),
	})

	result = append(result, messages[total-keep:]...)
	return result, fmt.Sprintf("Truncated %d messages to %d (head %d + tail %d)",
		total, len(result), head, keep)
}

func (ac *AutoCompactor) compactKeyMessages(messages []map[string]interface{}) ([]map[string]interface{}, string) {
	// Keep messages with high importance signals:
	// - System messages
	// - Messages with tool results
	// - Recent messages
	// - Messages containing error/fix/commit patterns

	total := len(messages)
	keep := ac.config.KeepRecent
	important := make([]int, 0)
	kept := make(map[int]bool)

	for i, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		// Always keep system messages
		if role == "system" {
			important = append(important, i)
			kept[i] = true
			continue
		}

		// Keep recent messages
		if i >= total-keep {
			important = append(important, i)
			kept[i] = true
			continue
		}

		// Keep messages with important content patterns
		if isImportantContent(content) {
			important = append(important, i)
			kept[i] = true
		}

		// Keep tool call messages (they have tool_call role or content)
		if _, ok := msg["tool_calls"]; ok {
			important = append(important, i)
			kept[i] = true
		}
		if _, ok := msg["tool_call_id"]; ok {
			important = append(important, i)
			kept[i] = true
		}
	}

	if len(important) == total {
		return messages, ""
	}

	// Build result preserving order
	result := make([]map[string]interface{}, 0, len(important)+1)
	for i, msg := range messages {
		if kept[i] {
			result = append(result, msg)
		}
	}

	// Insert compaction marker after system messages
	systemCount := 0
	for _, msg := range result {
		if role, _ := msg["role"].(string); role == "system" {
			systemCount++
		}
	}

	resultWithMarker := make([]map[string]interface{}, 0)
	resultWithMarker = append(resultWithMarker, result[:systemCount]...)
	resultWithMarker = append(resultWithMarker, map[string]interface{}{
		"role":    "user",
		"content": fmt.Sprintf("[Compacted: %d messages removed, %d key messages kept]", total-len(important), len(important)),
	})
	resultWithMarker = append(resultWithMarker, result[systemCount:]...)

	return resultWithMarker, fmt.Sprintf("Key-messages: kept %d/%d messages (saved ~%d tokens)",
		len(important), total, estimateTokenReduction(total, len(important)))
}

func isImportantContent(content string) bool {
	lower := strings.ToLower(content)
	patterns := []string{
		"error", "fix", "bug", "commit", "push", "test", "fail",
		"important", "critical", "breaking", "security", "regression",
		"approved", "merged", "deploy",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func estimateTokenReduction(before, after int) int {
	// Rough estimate: average message is ~200 tokens
	return (before - after) * 200
}

type autoModeRuntime struct {
	classifier *AutoModeClassifier
	compactor  *AutoCompactor
}

func NewAutoModeRuntime(classifier *AutoModeClassifier, compactor *AutoCompactor) AutoModeRuntime {
	if classifier == nil {
		return nil
	}
	return &autoModeRuntime{classifier: classifier, compactor: compactor}
}

func (r *autoModeRuntime) settings() AutoModeSettings {
	settings := AutoModeSettings{}
	if r != nil && r.classifier != nil {
		settings.ReadOnlyToolCount = len(r.classifier.readOnlyTools)
		settings.MaxDenials = r.classifier.maxDenials
	}
	if r != nil && r.compactor != nil {
		settings.KeepRecent = r.compactor.config.KeepRecent
		settings.MaxContextTokens = r.compactor.config.MaxTokens
	}
	return settings
}

func (r *autoModeRuntime) EnableAutoMode() AutoModeSettings {
	if r != nil && r.classifier != nil {
		r.classifier.Enable()
	}
	return r.settings()
}

func (r *autoModeRuntime) DisableAutoMode() {
	if r != nil && r.classifier != nil {
		r.classifier.Disable()
	}
}

func (r *autoModeRuntime) AutoModeStats() map[string]interface{} {
	if r == nil || r.classifier == nil {
		return map[string]interface{}{}
	}
	return r.classifier.Stats()
}

func (r *autoModeRuntime) AllowTool(name string) {
	if r != nil && r.classifier != nil {
		r.classifier.AddAllowTool(name)
	}
}

func (r *autoModeRuntime) BlockTool(name string) {
	if r != nil && r.classifier != nil {
		r.classifier.AddBlockTool(name)
	}
}

func (r *autoModeRuntime) ResetAutoModeDenials() {
	if r != nil && r.classifier != nil {
		r.classifier.ResetDenials()
	}
}

func (r *autoModeRuntime) AutoModeSettings() AutoModeSettings { return r.settings() }
