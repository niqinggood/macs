package middleware

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"mac/internal/toolcontract"
)

// LoopDetectionConfig controls the loop detection behavior.
type LoopDetectionConfig struct {
	Enabled       bool `json:"enabled" yaml:"enabled"`
	WarnThreshold int  `json:"warn_threshold" yaml:"warn_threshold"` // inject warning after N identical calls (default 3)
	HardLimit     int  `json:"hard_limit" yaml:"hard_limit"`         // force-stop after N identical calls (default 5)
	WindowSize    int  `json:"window_size" yaml:"window_size"`       // sliding window for tracking (default 20)
	ToolFreqWarn  int  `json:"tool_freq_warn" yaml:"tool_freq_warn"` // warn after N calls to same tool type (default 30)
	ToolFreqHard  int  `json:"tool_freq_hard" yaml:"tool_freq_hard"` // force-stop after N calls to same tool type (default 50)
	MaxThreads    int  `json:"max_threads" yaml:"max_threads"`       // LRU eviction limit (default 100)
}

func DefaultLoopDetectionConfig() LoopDetectionConfig {
	return LoopDetectionConfig{
		Enabled:       true,
		WarnThreshold: 2,
		HardLimit:     3,
		WindowSize:    20,
		ToolFreqWarn:  15,
		ToolFreqHard:  24,
		MaxThreads:    100,
	}
}

// Per-tool frequency overrides: tool name → (warn, hard_limit).
// High-frequency tools like bash get higher thresholds.
var loopDetectionOverrides = map[string][2]int{
	"bash":         {16, 24},
	"ls":           {20, 32},
	"grep":         {18, 28},
	"glob":         {18, 28},
	"cli":          {12, 20},
	"web_page":     {40, 64},
	"browser_task": {20, 32},
	"computer":     {40, 64},
}

// LoopDetectionMiddleware detects and breaks repetitive tool-call loops.
//
// Two detection layers:
//  1. Hash-based — identical tool call sets (name + stable args) within a window.
//     Uses read_file line-range bucketing to avoid flagging pagination.
//  2. Frequency-based — same tool type called many times with varying arguments.
//
// When a loop is detected:
//   - warn_threshold: a warning message is queued for the next LLM call
//   - hard_limit: tool_calls are stripped, forcing a text answer
type LoopDetectionMiddleware struct {
	Base
	config    LoopDetectionConfig
	overrides map[string][2]int

	mu       sync.Mutex
	history  map[string][]string        // threadID → recent call hashes
	warned   map[string]map[string]bool // threadID → set of warned hashes
	freq     map[string]map[string]int  // threadID → (toolName → count)
	freqWarn map[string]map[string]bool // threadID → set of warned tool names
	pending  map[string][]string        // threadID → pending warnings for next model call
	epoch    map[string]uint64          // threadID → successful mutable-state transitions
}

func NewLoopDetectionMiddleware(cfg LoopDetectionConfig) *LoopDetectionMiddleware {
	if !cfg.Enabled {
		return nil
	}
	defaults := DefaultLoopDetectionConfig()
	if cfg.WarnThreshold <= 0 {
		cfg.WarnThreshold = defaults.WarnThreshold
	}
	if cfg.HardLimit <= 0 {
		cfg.HardLimit = defaults.HardLimit
	}
	if cfg.HardLimit <= cfg.WarnThreshold {
		cfg.HardLimit = cfg.WarnThreshold + 1
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = defaults.WindowSize
	}
	if cfg.ToolFreqWarn <= 0 {
		cfg.ToolFreqWarn = defaults.ToolFreqWarn
	}
	if cfg.ToolFreqHard <= cfg.ToolFreqWarn {
		cfg.ToolFreqHard = max(defaults.ToolFreqHard, cfg.ToolFreqWarn+1)
	}
	if cfg.MaxThreads <= 0 {
		cfg.MaxThreads = defaults.MaxThreads
	}
	return &LoopDetectionMiddleware{
		Base:      NewBase("loop_detection"),
		config:    cfg,
		overrides: loopDetectionOverrides,
		history:   make(map[string][]string),
		warned:    make(map[string]map[string]bool),
		freq:      make(map[string]map[string]int),
		freqWarn:  make(map[string]map[string]bool),
		pending:   make(map[string][]string),
		epoch:     make(map[string]uint64),
	}
}

// BeforeTool tracks the tool call for loop detection.
// It must be called BEFORE tool execution so it can check the call against history.
// Returns a warning to inject, or "" if no warning needed.
func (m *LoopDetectionMiddleware) BeforeTool(ctx *Context) error {
	if m == nil {
		return nil
	}
	// Host-managed calls are created by bounded controller state, not sampled
	// model output. Counting them as another model retry can block the exact
	// convergence action the controller scheduled after a long workflow.
	// HostManaged is host-only metadata (json:"-"), so providers cannot forge it.
	if ctx != nil && ctx.ToolCall.HostManaged {
		return nil
	}

	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = ctx.TaskID
	}
	if sessionID == "" {
		return nil
	}

	toolName := ctx.ToolCall.Name
	baseCallHash := m.hashToolCall(toolName, ctx.ToolCall.Arguments)

	m.mu.Lock()
	defer m.mu.Unlock()
	// Duplicate signatures are meaningful only while the observable session
	// state is unchanged. A successful mutating call advances this epoch in
	// AfterTool, so a later execution or verification call cannot be rejected
	// merely because the same arguments were used against an older state.
	// Per-tool frequency remains session-wide and is intentionally not scoped
	// to the epoch.
	callHash := epochScopedToolCallHash(m.epoch[sessionID], baseCallHash)

	// Ensure tracking maps exist
	if m.history[sessionID] == nil {
		m.history[sessionID] = make([]string, 0)
		m.warned[sessionID] = make(map[string]bool)
		m.freq[sessionID] = make(map[string]int)
		m.freqWarn[sessionID] = make(map[string]bool)
	}

	// Evict oldest session if over limit
	if len(m.history) > m.config.MaxThreads {
		m.evictOldest()
	}

	// --- Layer 1: Hash-based detection ---
	history := m.history[sessionID]
	history = append(history, callHash)
	if len(history) > m.config.WindowSize {
		history = history[len(history)-m.config.WindowSize:]
	}
	m.history[sessionID] = history

	count := 0
	for _, h := range history {
		if h == callHash {
			count++
		}
	}

	// Hard stop: strip tool_calls from parent response
	if count >= m.config.HardLimit {
		ctx.SetMeta("loop.hard_stop", true)
		ctx.SetMeta("loop.hard_stop_reason", fmt.Sprintf(
			"[FORCED STOP] Repeated tool calls to '%s' exceeded safety limit (%d). Use available results to produce final answer.",
			toolName, m.config.HardLimit,
		))
		return NewGuardrailBlockError(toolName, fmt.Sprintf("loop detected: %s called %d times with same signature", toolName, count), "loop_hard_stop")
	}

	// Warning threshold
	if count >= m.config.WarnThreshold && !m.warned[sessionID][callHash] {
		m.warned[sessionID][callHash] = true
		m.queueWarning(sessionID, fmt.Sprintf(
			"[LOOP DETECTED] Tool '%s' called %d times with same arguments. Stop calling this tool and try a different approach.",
			toolName, count,
		))
	}

	// --- Layer 2: Tool-type frequency ---
	m.freq[sessionID][toolName]++
	toolCount := m.freq[sessionID][toolName]

	effWarn := m.config.ToolFreqWarn
	effHard := m.config.ToolFreqHard
	if override, ok := m.overrides[toolName]; ok {
		effWarn = override[0]
		effHard = override[1]
	}

	if toolCount >= effHard && !m.freqWarn[sessionID][toolName+"_hard"] {
		m.freqWarn[sessionID][toolName+"_hard"] = true
		ctx.SetMeta("loop.hard_stop", true)
		ctx.SetMeta("loop.hard_stop_reason", fmt.Sprintf(
			"[FORCED STOP] Tool '%s' called %d times — exceeded per-tool limit (%d). Use available results to produce final answer.",
			toolName, toolCount, effHard,
		))
		return NewGuardrailBlockError(toolName, fmt.Sprintf("tool frequency hard limit: %s called %d times", toolName, toolCount), "loop_freq_hard_stop")
	}

	if toolCount >= effWarn && !m.freqWarn[sessionID][toolName] {
		m.freqWarn[sessionID][toolName] = true
		m.queueWarning(sessionID, fmt.Sprintf(
			"[LOOP DETECTED] Tool '%s' has been called %d times. Stop calling tools and produce your final answer.",
			toolName, toolCount,
		))
	}

	return nil
}

// AfterTool drains pending warnings into the context for injection into
// the next LLM call. This ensures warnings don't break tool_calls pairing.
func (m *LoopDetectionMiddleware) AfterTool(ctx *Context) error {
	if m == nil {
		return nil
	}

	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = ctx.TaskID
	}

	// If hard stop was triggered, replace the tool result with a stop message
	if stopped, _ := ctx.GetMeta("loop.hard_stop"); stopped == true {
		if reason, ok := ctx.GetMeta("loop.hard_stop_reason"); ok {
			ctx.ToolResult = &toolcontract.Result{
				ToolCallID: ctx.ToolCall.ID,
				Name:       ctx.ToolCall.Name,
				Content:    reason.(string),
			}
			ctx.ToolErr = nil
		}
	}

	// Only a successful non-read-only call proves that the session's mutable
	// state may have advanced. Failures and observations stay in the current
	// epoch so genuine retries remain subject to duplicate-call detection.
	if sessionID != "" && ctx.ToolResult != nil && !loopCallIsReadOnly(ctx) &&
		toolcontract.ToolResultExecutionSucceeded(*ctx.ToolResult, ctx.ToolErr) {
		m.mu.Lock()
		m.epoch[sessionID]++
		m.mu.Unlock()
	}

	return nil
}

func loopCallIsReadOnly(ctx *Context) bool {
	if ctx == nil {
		return false
	}
	if value, ok := ctx.Metadata["tool_read_only"].(bool); ok {
		return value
	}
	switch strings.ToLower(strings.TrimSpace(ctx.ToolCall.Name)) {
	case "read_file", "list_dir", "grep", "glob", "snapshot", "search_history",
		"knowledge_query", "list_skills", "get_stats", "tool_detail", "rag_search",
		"rag_extract", "agent_reach", "web_search", "web_targets", "web_extract",
		"plan_task", "update_plan_step", "vector_search", "lsp_hover", "lsp_definition",
		"lsp_references":
		return true
	default:
		return false
	}
}

// BeforeModel injects queued warnings into the system prompt.
func (m *LoopDetectionMiddleware) BeforeModel(ctx *Context) error {
	if m == nil {
		return nil
	}

	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = ctx.TaskID
	}

	m.mu.Lock()
	warnings := m.pending[sessionID]
	delete(m.pending, sessionID)
	m.mu.Unlock()

	if len(warnings) == 0 {
		return nil
	}

	// Deduplicate and append as system message
	seen := make(map[string]bool)
	var merged string
	for _, w := range warnings {
		if !seen[w] {
			seen[w] = true
			if merged != "" {
				merged += "\n"
			}
			merged += w
		}
	}

	ctx.Messages = append(ctx.Messages, map[string]interface{}{
		"role":    "system",
		"content": merged,
	})

	return nil
}

// Reset clears tracking for a session.
func (m *LoopDetectionMiddleware) Reset(sessionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.history, sessionID)
	delete(m.warned, sessionID)
	delete(m.freq, sessionID)
	delete(m.freqWarn, sessionID)
	delete(m.pending, sessionID)
	delete(m.epoch, sessionID)
}

// --- internal helpers ---

func (m *LoopDetectionMiddleware) queueWarning(sessionID, warning string) {
	if m.pending[sessionID] == nil {
		m.pending[sessionID] = make([]string, 0)
	}
	m.pending[sessionID] = append(m.pending[sessionID], warning)
}

func (m *LoopDetectionMiddleware) evictOldest() {
	// Find the session with fewest entries (LRU approximation)
	var oldest string
	var fewest int
	for id, h := range m.history {
		if oldest == "" || len(h) < fewest {
			oldest = id
			fewest = len(h)
		}
	}
	if oldest != "" {
		delete(m.history, oldest)
		delete(m.warned, oldest)
		delete(m.freq, oldest)
		delete(m.freqWarn, oldest)
		delete(m.pending, oldest)
		delete(m.epoch, oldest)
	}
}

func epochScopedToolCallHash(epoch uint64, callHash string) string {
	return fmt.Sprintf("epoch=%d:%s", epoch, callHash)
}

// hashToolCall produces a stable hash for a tool call.
// For read_file: line numbers are bucketed into 200-line ranges
//
//	so pagination (different line ranges) doesn't trigger false positives.
//
// For write_file/str_replace: full args hashed (content-sensitive).
// For other tools: only salient fields (path, url, query, command, pattern) are hashed.
func (m *LoopDetectionMiddleware) hashToolCall(name string, args map[string]interface{}) string {
	switch name {
	case "read_file":
		return hashReadFileCall(args)
	case "write_file", "edit_file", "json_patch", "str_replace", "plan_task", "use_skill":
		return hashFullArgs(name, args)
	case "update_plan_step":
		return hashPlanStepCall(args)
	case "web_page", "browser_task":
		// Browser interactions commonly reuse the same tool and URL while
		// changing action, target, element index, input text, or objective.
		// Hashing only URL/path fields collapses a normal login sequence
		// (extract -> username -> password -> click) into one fake loop.
		return hashFullArgs(name, args)
	default:
		return hashSalientFields(name, args)
	}
}

func hashPlanStepCall(args map[string]interface{}) string {
	stepID, _ := args["step_id"].(string)
	status, _ := args["status"].(string)
	stepID = strings.TrimSpace(stepID)
	status = strings.ToLower(strings.TrimSpace(status))
	if stepID == "" || status == "" {
		return hashFullArgs("update_plan_step", args)
	}
	stable := "step_id=" + stepID + ";status=" + status
	return fmt.Sprintf("update_plan_step:%x", md5.Sum([]byte(stable)))
}

// hashReadFileCall buckets line ranges into 200-line windows.
// e.g., reading lines 0-200 and 100-300 produce the same hash.
func hashReadFileCall(args map[string]interface{}) string {
	path, _ := args["path"].(string)
	start, end := readFileLineRange(args)

	bucketSize := 200
	bucketStart := (start - 1) / bucketSize
	bucketEnd := (end - 1) / bucketSize

	return fmt.Sprintf("read_file:%s:%d-%d", path, bucketStart, bucketEnd)
}

func readFileLineRange(args map[string]interface{}) (int, int) {
	start := getIntArg(args, "start_line", 1)
	end := getIntArg(args, "end_line", start)

	// read_file exposes its public range as lines_range="M-N". Older callers
	// may still send start_line/end_line, so accept both forms. Ignoring
	// lines_range made distinct pagination reads look identical and caused a
	// false hard stop on the third read.
	for _, key := range []string{"lines_range", "line_range"} {
		raw, _ := args[key].(string)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var parsedStart, parsedEnd int
		if matched, _ := fmt.Sscanf(raw, "%d-%d", &parsedStart, &parsedEnd); matched == 2 {
			start, end = parsedStart, parsedEnd
			break
		}
		if matched, _ := fmt.Sscanf(strings.ToLower(raw), "head:%d", &parsedEnd); matched == 1 {
			start = 1
			end = parsedEnd
			break
		}
	}

	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	return start, end
}

// hashFullArgs hashes the complete args for content-sensitive tools.
func hashFullArgs(name string, args map[string]interface{}) string {
	data, _ := json.Marshal(args)
	return fmt.Sprintf("%s:%x", name, md5.Sum(data))
}

// hashSalientFields hashes only the meaningful fields for most tools.
func hashSalientFields(name string, args map[string]interface{}) string {
	salient := []string{"path", "url", "query", "command", "pattern", "glob", "cmd", "tool_name"}
	vals := make(map[string]interface{})
	for _, field := range salient {
		if v, ok := args[field]; ok && v != nil {
			vals[field] = v
		}
	}
	if len(vals) == 0 {
		return name + ":no_salient_args"
	}
	// Sort keys for stable serialization
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var stable string
	for _, k := range keys {
		stable += fmt.Sprintf("%s=%v;", k, vals[k])
	}
	return fmt.Sprintf("%s:%x", name, md5.Sum([]byte(stable)))
}

func getIntArg(args map[string]interface{}, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return defaultVal
}
