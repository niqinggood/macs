package toolcontract

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"mac/internal/runtimeutil"
)

// ====================== Tool-level Circuit Breaker ======================

// ToolCircuitBreaker protects a single tool from cascading failures.
// CogniFlow borrowed: circuit breaker per tool, 5 consecutive failures → 30s meltdown.
type ToolCircuitBreaker struct {
	toolName         string
	failureThreshold int           // consecutive failures to trip (default 5)
	successThreshold int           // consecutive successes to close (default 3)
	timeout          time.Duration // how long to stay open (default 30s)
	state            runtimeutil.CircuitState
	failureCount     int
	successCount     int
	lastFailure      time.Time
	halfOpenStart    time.Time
	mu               sync.Mutex
	logger           *log.Logger
}

func NewToolCircuitBreaker(toolName string, logger *log.Logger) *ToolCircuitBreaker {
	return &ToolCircuitBreaker{
		toolName:         toolName,
		failureThreshold: 5,
		successThreshold: 3,
		timeout:          30 * time.Second,
		state:            runtimeutil.CircuitClosed,
		logger:           logger,
	}
}

func (tcb *ToolCircuitBreaker) Allow() error {
	tcb.mu.Lock()
	defer tcb.mu.Unlock()

	switch tcb.state {
	case runtimeutil.CircuitClosed:
		return nil
	case runtimeutil.CircuitOpen:
		if time.Since(tcb.lastFailure) > tcb.timeout {
			tcb.state = runtimeutil.CircuitHalfOpen
			tcb.halfOpenStart = time.Now()
			tcb.logger.Printf("[ToolCB] %s: half-open (testing recovery)", tcb.toolName)
			return nil
		}
		return fmt.Errorf("[ToolCB] %s: circuit open (%d failures, retry in %v)",
			tcb.toolName, tcb.failureCount, tcb.timeout-time.Since(tcb.lastFailure))
	case runtimeutil.CircuitHalfOpen:
		return nil
	}
	return nil
}

func (tcb *ToolCircuitBreaker) RecordSuccess() {
	tcb.mu.Lock()
	defer tcb.mu.Unlock()

	if tcb.state == runtimeutil.CircuitHalfOpen {
		tcb.successCount++
		if tcb.successCount >= tcb.successThreshold {
			tcb.state = runtimeutil.CircuitClosed
			tcb.failureCount = 0
			tcb.successCount = 0
			tcb.logger.Printf("[ToolCB] %s: closed (recovered)", tcb.toolName)
		}
	} else {
		tcb.failureCount = 0
	}
}

func (tcb *ToolCircuitBreaker) RecordFailure() {
	tcb.mu.Lock()
	defer tcb.mu.Unlock()

	tcb.failureCount++
	tcb.lastFailure = time.Now()

	if tcb.state == runtimeutil.CircuitHalfOpen {
		tcb.state = runtimeutil.CircuitOpen
		tcb.successCount = 0
		tcb.logger.Printf("[ToolCB] %s: open (probe failed, %d consecutive failures)", tcb.toolName, tcb.failureCount)
		return
	}

	if tcb.failureCount >= tcb.failureThreshold {
		tcb.state = runtimeutil.CircuitOpen
		tcb.logger.Printf("[ToolCB] %s: open (%d consecutive failures, meltdown %v)", tcb.toolName, tcb.failureCount, tcb.timeout)
	}
}

func (tcb *ToolCircuitBreaker) State() string {
	tcb.mu.Lock()
	defer tcb.mu.Unlock()
	return string(tcb.state)
}

// ====================== Retry with Backoff ======================

// RetryConfig defines exponential backoff retry behavior.
type RetryConfig struct {
	MaxRetries int           // max retries (default 3)
	BaseDelay  time.Duration // initial delay (default 1s)
	MaxDelay   time.Duration // cap (default 15s)
	Multiplier float64       // backoff factor (default 2.0)
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Second,
		MaxDelay:   15 * time.Second,
		Multiplier: 2.0,
	}
}

// ====================== Protected Tool Wrapper ======================

// ToolProtector wraps tool execution with circuit breaker + retry + backoff.
type ToolProtector struct {
	breakers map[string]*ToolCircuitBreaker // per-tool circuit breakers
	retryCfg RetryConfig
	mu       sync.RWMutex
	logger   *log.Logger
	stats    *ProtectionStats
}

type ProtectionStats struct {
	mu            sync.Mutex
	TotalCalls    int64                `json:"total_calls"`
	TotalFailures int64                `json:"total_failures"`
	TotalRetries  int64                `json:"total_retries"`
	CircuitTrips  int64                `json:"circuit_trips"`
	PerTool       map[string]*ToolStat `json:"per_tool"`
}

type ToolStat struct {
	Calls    int64  `json:"calls"`
	Failures int64  `json:"failures"`
	Retries  int64  `json:"retries"`
	Trips    int64  `json:"trips"`
	State    string `json:"state"`
}

func NewToolProtector(retryCfg RetryConfig, logger *log.Logger) *ToolProtector {
	return &ToolProtector{
		breakers: make(map[string]*ToolCircuitBreaker),
		retryCfg: retryCfg,
		logger:   logger,
		stats: &ProtectionStats{
			PerTool: make(map[string]*ToolStat),
		},
	}
}

// getOrCreateBreaker returns the circuit breaker for a tool, creating one if needed.
func (tp *ToolProtector) getOrCreateBreaker(toolName string) *ToolCircuitBreaker {
	tp.mu.RLock()
	cb, ok := tp.breakers[toolName]
	tp.mu.RUnlock()
	if ok {
		return cb
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if cb, ok = tp.breakers[toolName]; ok {
		return cb
	}
	cb = NewToolCircuitBreaker(toolName, tp.logger)
	tp.breakers[toolName] = cb
	tp.stats.PerTool[toolName] = &ToolStat{}
	return cb
}

// Execute runs the tool with circuit breaker + retry protection.
// Returns (result, error) — the caller gets the last result or the last error.
func (tp *ToolProtector) Execute(ctx context.Context, tool Tool, params map[string]interface{}) (string, error) {
	cb := tp.getOrCreateBreaker(tool.Name())

	// Update stats
	tp.stats.mu.Lock()
	tp.stats.TotalCalls++
	if s, ok := tp.stats.PerTool[tool.Name()]; ok {
		s.Calls++
		s.State = cb.State()
	}
	tp.stats.mu.Unlock()

	// Check circuit breaker
	if err := cb.Allow(); err != nil {
		tp.logger.Printf("[Protect] %s: rejected by circuit breaker: %v", tool.Name(), err)
		return "", err
	}

	// Execute with retry
	var lastErr error
	maxAttempts := 1 + tp.retryCfg.MaxRetries

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Calculate backoff delay
			delay := time.Duration(float64(tp.retryCfg.BaseDelay) * math.Pow(tp.retryCfg.Multiplier, float64(attempt-1)))
			if delay > tp.retryCfg.MaxDelay {
				delay = tp.retryCfg.MaxDelay
			}
			tp.logger.Printf("[Protect] %s: retry %d/%d after %v", tool.Name(), attempt, tp.retryCfg.MaxRetries, delay)

			tp.stats.mu.Lock()
			tp.stats.TotalRetries++
			if s := tp.stats.PerTool[tool.Name()]; s != nil {
				s.Retries++
			}
			tp.stats.mu.Unlock()

			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		// Execute
		result, err := tool.Execute(ctx, params)
		if err == nil {
			cb.RecordSuccess()
			return result, nil
		}

		lastErr = err
		tp.logger.Printf("[Protect] %s: attempt %d failed: %v", tool.Name(), attempt+1, err)
	}

	// All attempts failed
	cb.RecordFailure()

	tp.stats.mu.Lock()
	tp.stats.TotalFailures++
	if s := tp.stats.PerTool[tool.Name()]; s != nil {
		s.Failures++
		if tp.retryCfg.MaxRetries > 0 {
			s.Trips++
			tp.stats.CircuitTrips++
		}
	}
	tp.stats.mu.Unlock()

	return "", fmt.Errorf("%s: all %d attempts failed, last error: %w", tool.Name(), maxAttempts, lastErr)
}

// IsReadOnlyTool returns true for tools that are safe to call without protection.
func (tp *ToolProtector) IsReadOnlyTool(tool Tool) bool {
	if ro, ok := tool.(ReadOnlyTool); ok {
		return ro.IsReadOnly()
	}
	return false
}

// ShouldProtect returns true if the tool should be protected.
// Read-only tools don't need circuit breaker (they can't cause damage).
func (tp *ToolProtector) ShouldProtect(tool Tool) bool {
	return !tp.IsReadOnlyTool(tool)
}

// Stats returns current protection statistics.
func (tp *ToolProtector) Stats() *ProtectionStats {
	tp.stats.mu.Lock()
	defer tp.stats.mu.Unlock()
	// Update state for each tool
	for name, cb := range tp.breakers {
		if s := tp.stats.PerTool[name]; s != nil {
			s.State = cb.State()
		}
	}
	return tp.stats
}

// ResetBreaker manually resets a tool's circuit breaker.
func (tp *ToolProtector) ResetBreaker(toolName string) {
	tp.mu.RLock()
	cb, ok := tp.breakers[toolName]
	tp.mu.RUnlock()
	if ok {
		cb.mu.Lock()
		cb.state = runtimeutil.CircuitClosed
		cb.failureCount = 0
		cb.successCount = 0
		cb.mu.Unlock()
		tp.logger.Printf("[Protect] %s: breaker manually reset", toolName)
	}
}

// ====================== Protection Stats Tool ======================

type ProtectionStatsTool struct {
	protector *ToolProtector
}

func NewProtectionStatsTool(protector *ToolProtector) *ProtectionStatsTool {
	return &ProtectionStatsTool{protector: protector}
}

func (t *ProtectionStatsTool) Name() string { return "tool_protection_stats" }
func (t *ProtectionStatsTool) Description() string {
	return "查看工具执行保护状态。返回各工具的熔断器状态、调用次数、失败次数、重试次数。"
}
func (t *ProtectionStatsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"reset": map[string]interface{}{"type": "string", "description": "重置某个工具的熔断器(输入工具名)"},
		},
		"required": []string{},
	}
}
func (t *ProtectionStatsTool) IsReadOnly() bool { return true }
func (t *ProtectionStatsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if reset, ok := params["reset"].(string); ok && reset != "" {
		t.protector.ResetBreaker(reset)
		return fmt.Sprintf("已重置 %s 的熔断器", reset), nil
	}

	stats := t.protector.Stats()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 工具执行保护状态\n\n"))
	sb.WriteString(fmt.Sprintf("总调用: %d | 总失败: %d | 总重试: %d | 熔断触发: %d\n\n",
		stats.TotalCalls, stats.TotalFailures, stats.TotalRetries, stats.CircuitTrips))
	sb.WriteString("| 工具 | 调用 | 失败 | 重试 | 状态 |\n")
	sb.WriteString("|------|------|------|------|------|\n")
	for name, s := range stats.PerTool {
		stateIcon := "✅"
		if s.State == "open" {
			stateIcon = "🔴"
		} else if s.State == "half_open" {
			stateIcon = "🟡"
		}
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %s %s |\n",
			name, s.Calls, s.Failures, s.Retries, stateIcon, s.State))
	}
	return sb.String(), nil
}

// ====================== Global Instance ======================

var globalToolProtector *ToolProtector

func InitToolProtector(logger *log.Logger) *ToolProtector {
	globalToolProtector = NewToolProtector(DefaultRetryConfig(), logger)
	return globalToolProtector
}

func GetToolProtector() *ToolProtector {
	return globalToolProtector
}
