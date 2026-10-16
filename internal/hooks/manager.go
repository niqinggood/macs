package hooks

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// HookManagerV2 is the settings-driven lifecycle hook runtime. Configuration
// replacement is atomic; hook execution takes a snapshot and never holds the
// manager lock while user code is running.
type HookManagerV2 struct {
	mu sync.RWMutex

	configs map[LifecycleEvent][]V2HookConfig

	shell    *HookShellExecutor
	http     *HookHTTPExecutor
	callback *HookCallbackExecutor
	prompt   *HookPromptExecutor

	asyncMu   sync.Mutex
	asyncJobs map[string]*asyncHookJob

	enabled bool
	logger  *log.Logger
}

type asyncHookJob struct {
	id        string
	event     LifecycleEvent
	ctx       *HookContextV2
	startedAt time.Time
	resultCh  chan *HookResultV2
}

func NewHookManagerV2(logger *log.Logger, safetyCheck ShellSafetyCheck) *HookManagerV2 {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &HookManagerV2{
		configs:   make(map[LifecycleEvent][]V2HookConfig),
		shell:     NewHookShellExecutor(true, safetyCheck),
		http:      NewHookHTTPExecutor(),
		callback:  NewHookCallbackExecutor(),
		asyncJobs: make(map[string]*asyncHookJob),
		enabled:   true,
		logger:    logger,
	}
}

// SetPromptCaller sets the LLM caller for prompt and agent hook types.
func (m *HookManagerV2) SetPromptCaller(caller PromptCaller) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompt = NewHookPromptExecutor(caller)
}

func (m *HookManagerV2) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
}

func (m *HookManagerV2) RegisterCallback(id string, fn HookCallbackFunc) func() {
	return m.callback.Register(id, fn)
}

// LoadConfig atomically replaces the live hook configuration. Callers should
// validate the complete settings snapshot first with ValidateHookSettings.
func (m *HookManagerV2) LoadConfig(hooks V2HookSettings) {
	next := make(map[LifecycleEvent][]V2HookConfig)
	for event, configs := range hooks.Enabled {
		if !IsValidLifecycleEvent(string(event)) {
			m.logger.Printf("[HookManagerV2] unknown event: %s", event)
			continue
		}
		next[event] = m.deduplicateConfigs(configs)
	}

	m.mu.Lock()
	m.configs = next
	m.mu.Unlock()

	for event, configs := range next {
		m.logger.Printf("[HookManagerV2] loaded %d hooks for event %s", len(configs), event)
	}
}

func (m *HookManagerV2) ConfigCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, configs := range m.configs {
		total += len(configs)
	}
	return total
}

func (m *HookManagerV2) SnapshotConfig() V2HookSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := *DefaultHookSettings()
	for event, configs := range m.configs {
		snapshot.Enabled[event] = append([]V2HookConfig(nil), configs...)
	}
	return snapshot
}

func (m *HookManagerV2) deduplicateConfigs(configs []V2HookConfig) []V2HookConfig {
	seen := make(map[string]bool, len(configs))
	result := make([]V2HookConfig, 0, len(configs))
	for _, config := range configs {
		key := m.configKey(config)
		if seen[key] {
			m.logger.Printf("[HookManagerV2] dedup: skipping duplicate hook %s", key)
			continue
		}
		seen[key] = true
		result = append(result, config)
	}
	return result
}

func (m *HookManagerV2) configKey(config V2HookConfig) string {
	return fmt.Sprintf("%s:%s:%s:%s:%d", config.Type, config.Match, config.Command, config.URL, config.Timeout)
}

// getConfigs returns matching configs as a new slice.
func (m *HookManagerV2) getConfigs(event LifecycleEvent, matchQuery string) []V2HookConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getConfigsLocked(event, matchQuery)
}

func (m *HookManagerV2) getConfigsLocked(event LifecycleEvent, matchQuery string) []V2HookConfig {
	configs := m.configs[event]
	matched := make([]V2HookConfig, 0, len(configs))
	for _, config := range configs {
		if config.Match == "" || config.Match == "*" || matchGlob(config.Match, matchQuery) {
			matched = append(matched, config)
		}
	}
	return matched
}

func (m *HookManagerV2) snapshotForFire(event LifecycleEvent, matchQuery string) (bool, []V2HookConfig) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled, m.getConfigsLocked(event, matchQuery)
}

type FireResult struct {
	Results           []*HookResultV2
	Blocked           bool
	BlockReason       string
	AdditionalContext string
	Errors            []error
}

func (m *HookManagerV2) Fire(ctx context.Context, event LifecycleEvent, matchQuery string, hookContext *HookContextV2) *FireResult {
	enabled, configs := m.snapshotForFire(event, matchQuery)
	if !enabled || len(configs) == 0 {
		return &FireResult{}
	}

	if hookContext == nil {
		hookContext = &HookContextV2{}
	} else {
		copied := *hookContext
		hookContext = &copied
	}
	hookContext.Event = event
	if hookContext.Timestamp == 0 {
		hookContext.Timestamp = time.Now().Unix()
	}

	result := &FireResult{}
	if len(configs) == 1 {
		hookResult, err := m.executeHook(ctx, configs[0], hookContext)
		m.collectOutcome(result, event, hookResult, err)
		return result
	}

	// Execute independent hooks concurrently, but aggregate their effects in
	// configuration order so context and error output remain deterministic.
	type hookOutcome struct {
		index  int
		result *HookResultV2
		err    error
	}
	outcomeCh := make(chan hookOutcome, len(configs))
	for index, config := range configs {
		go func(i int, cfg V2HookConfig) {
			hookResult, err := m.executeHook(ctx, cfg, hookContext)
			outcomeCh <- hookOutcome{index: i, result: hookResult, err: err}
		}(index, config)
	}
	ordered := make([]hookOutcome, len(configs))
	for range configs {
		outcome := <-outcomeCh
		ordered[outcome.index] = outcome
	}
	for _, outcome := range ordered {
		m.collectOutcome(result, event, outcome.result, outcome.err)
	}
	return result
}

func (m *HookManagerV2) collectOutcome(fireResult *FireResult, event LifecycleEvent, result *HookResultV2, err error) {
	if err != nil {
		fireResult.Errors = append(fireResult.Errors, err)
		m.logger.Printf("[HookManagerV2] %s hook error: %v", event, err)
	}
	if result != nil {
		fireResult.Results = append(fireResult.Results, result)
		m.applyResult(fireResult, result)
	}
}

func (m *HookManagerV2) applyResult(fireResult *FireResult, result *HookResultV2) {
	switch result.Decision {
	case HookDecisionBlock:
		fireResult.Blocked = true
		if fireResult.BlockReason == "" {
			fireResult.BlockReason = result.Message
		} else {
			fireResult.BlockReason += "; " + result.Message
		}
	case HookDecisionAllow, HookDecisionPassthrough:
		if result.AdditionalContext != "" {
			if fireResult.AdditionalContext != "" {
				fireResult.AdditionalContext += "\n"
			}
			fireResult.AdditionalContext += result.AdditionalContext
		}
	}
}

func (m *HookManagerV2) executeHook(ctx context.Context, config V2HookConfig, hookContext *HookContextV2) (*HookResultV2, error) {
	startedAt := time.Now()
	var result *HookResultV2
	var err error

	switch config.Type {
	case HookExecShell:
		result, err = m.shell.ExecuteContext(ctx, config, hookContext)
	case HookExecHTTP:
		result, err = m.http.ExecuteContext(ctx, config, hookContext)
	case HookExecCallback:
		result, err = m.callback.Execute(config, hookContext)
	case HookExecPrompt, HookExecAgent:
		if prompt := m.promptExecutor(); prompt != nil {
			result, err = prompt.ExecuteContext(ctx, config, hookContext)
		} else {
			result = &HookResultV2{
				Decision:     HookDecisionPassthrough,
				Message:      "prompt executor not configured (call SetPromptCaller)",
				ExecutorType: config.Type,
			}
		}
	default:
		return nil, fmt.Errorf("unknown hook type: %s", config.Type)
	}

	if result != nil {
		result.ConfigID = m.configKey(config)
		result.Duration = time.Since(startedAt)
		m.logger.Printf("[HookManagerV2] %s:%s -> %s (%v)",
			hookContext.Event, result.ConfigID, result.Decision, result.Duration)
	} else if err != nil {
		m.logger.Printf("[HookManagerV2] %s:%s -> error: %v",
			hookContext.Event, m.configKey(config), err)
	}
	return result, err
}

func (m *HookManagerV2) promptExecutor() *HookPromptExecutor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.prompt
}

var (
	hookManagerV2GlobalMu sync.RWMutex
	hookManagerV2Global   *HookManagerV2
)

func SetGlobalHookManagerV2(manager *HookManagerV2) {
	hookManagerV2GlobalMu.Lock()
	defer hookManagerV2GlobalMu.Unlock()
	hookManagerV2Global = manager
}

func GetGlobalHookManagerV2() *HookManagerV2 {
	hookManagerV2GlobalMu.RLock()
	defer hookManagerV2GlobalMu.RUnlock()
	return hookManagerV2Global
}

func GetOrCreateGlobalHookManagerV2(logger *log.Logger, safetyCheck ShellSafetyCheck) *HookManagerV2 {
	hookManagerV2GlobalMu.Lock()
	defer hookManagerV2GlobalMu.Unlock()
	if hookManagerV2Global == nil {
		hookManagerV2Global = NewHookManagerV2(logger, safetyCheck)
	}
	return hookManagerV2Global
}

func (m *HookManagerV2) FireToolHook(ctx context.Context, event LifecycleEvent, agentName, taskID, sessionID, toolName string, params map[string]interface{}) *FireResult {
	return m.Fire(ctx, event, toolName, &HookContextV2{
		AgentName: agentName,
		TaskID:    taskID,
		SessionID: sessionID,
		ToolName:  toolName,
		Params:    params,
	})
}

func (m *HookManagerV2) FireSessionHook(ctx context.Context, event LifecycleEvent, agentName, taskID, sessionID, channel string) *FireResult {
	return m.Fire(ctx, event, channel, &HookContextV2{
		AgentName: agentName,
		TaskID:    taskID,
		SessionID: sessionID,
		Channel:   channel,
	})
}

func (m *HookManagerV2) FireModelHook(ctx context.Context, event LifecycleEvent, agentName, sessionID string) *FireResult {
	return m.Fire(ctx, event, "*", &HookContextV2{
		AgentName: agentName,
		SessionID: sessionID,
	})
}
