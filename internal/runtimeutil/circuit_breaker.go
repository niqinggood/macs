package runtimeutil

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // normal operation
	CircuitOpen     CircuitState = "open"      // failing, reject calls
	CircuitHalfOpen CircuitState = "half_open" // testing recovery
)

// CircuitBreaker protects LLM API calls from cascading failures.
// Claude Code borrowed: API error handling with retry + backoff
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitState
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	lastFailure      time.Time
	lastSuccess      time.Time
	totalFailures    int64
	totalSuccess     int64
	probeInFlight    bool // gate half-open to single probe
	halfOpenStart    time.Time
	permanentErrors  map[int]bool // HTTP status codes that should NOT trip the breaker
}

// LLMCircuitOutcome owns the breaker accounting for one logical LLM call.
// Adaptive retries and protocol-recovery recursion share the same instance via
// context, so a call is settled exactly once instead of once per HTTP attempt.
// A provider request must actually reach http.Client.Do before the outcome is
// eligible to affect breaker counters.
type LLMCircuitOutcome struct {
	mu               sync.Mutex
	once             sync.Once
	breaker          *CircuitBreaker
	parent           context.Context
	providerRequests int
	lastFailureCode  int
}

type llmCircuitOutcomeContextKey struct{}

// BeginLLMCircuitOutcome starts or reuses the breaker accounting for one logical LLM call.
func BeginLLMCircuitOutcome(ctx context.Context, breaker *CircuitBreaker) (context.Context, *LLMCircuitOutcome, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, ok := ctx.Value(llmCircuitOutcomeContextKey{}).(*LLMCircuitOutcome); ok &&
		existing != nil && existing.breaker == breaker {
		return ctx, existing, false, nil
	}
	if breaker == nil {
		return ctx, nil, false, nil
	}
	if err := breaker.Allow(); err != nil {
		return ctx, nil, false, err
	}
	outcome := &LLMCircuitOutcome{breaker: breaker, parent: ctx}
	return context.WithValue(ctx, llmCircuitOutcomeContextKey{}, outcome), outcome, true, nil
}

// MarkLLMProviderRequest records that the logical call reached the provider.
func MarkLLMProviderRequest(ctx context.Context) {
	if ctx == nil {
		return
	}
	if outcome, ok := ctx.Value(llmCircuitOutcomeContextKey{}).(*LLMCircuitOutcome); ok && outcome != nil {
		outcome.mu.Lock()
		outcome.providerRequests++
		outcome.mu.Unlock()
	}
}

// NoteLLMCircuitFailure records the last provider failure for the logical call.
func NoteLLMCircuitFailure(ctx context.Context, statusCode int) {
	if ctx == nil {
		return
	}
	if outcome, ok := ctx.Value(llmCircuitOutcomeContextKey{}).(*LLMCircuitOutcome); ok && outcome != nil {
		outcome.mu.Lock()
		outcome.lastFailureCode = statusCode
		outcome.mu.Unlock()
	}
}

func (o *LLMCircuitOutcome) Finish(success bool) {
	if o == nil || o.breaker == nil {
		return
	}
	o.once.Do(func() {
		o.mu.Lock()
		providerRequests := o.providerRequests
		failureCode := o.lastFailureCode
		o.mu.Unlock()

		// Local preflight failures and caller-driven cancellation say nothing
		// about provider health. They must still release a half-open lease.
		if providerRequests == 0 || (o.parent != nil && o.parent.Err() != nil) {
			o.breaker.releaseProbe()
			return
		}
		if success {
			o.breaker.RecordSuccess()
			return
		}
		o.breaker.RecordFailure(failureCode)
	})
}

func NewCircuitBreaker(failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            CircuitClosed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
		permanentErrors:  map[int]bool{401: true, 403: true}, // auth errors are permanent, don't count
	}
}

// Allow checks if a call should be permitted.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.state = CircuitHalfOpen
			cb.probeInFlight = true
			cb.halfOpenStart = time.Now()
			return nil
		}
		return fmt.Errorf("circuit breaker open: %d failures, retry after %v",
			cb.failureCount, cb.timeout-time.Since(cb.lastFailure))
	case CircuitHalfOpen:
		if cb.probeInFlight {
			return fmt.Errorf("circuit breaker half-open: probe already in flight")
		}
		cb.probeInFlight = true
		return nil
	}
	return nil
}

// AllowHTTPStatus classifies an HTTP status as transient or permanent, and records accordingly.
// 401/403 auth errors never count as failures — they won't self-heal.
func (cb *CircuitBreaker) AllowHTTPStatus(statusCode int) error {
	if cb.permanentErrors[statusCode] {
		return nil // don't check breaker for auth errors
	}
	return cb.Allow()
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalSuccess++
	cb.lastSuccess = time.Now()

	switch cb.state {
	case CircuitHalfOpen:
		cb.probeInFlight = false
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = CircuitClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	case CircuitClosed:
		cb.failureCount = 0
	}
}

// RecordFailure records a failed call. Set statusCode to 0 if unknown.
func (cb *CircuitBreaker) RecordFailure(statusCode int) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Permanent errors don't count toward breaker trip
	if statusCode > 0 && cb.permanentErrors[statusCode] {
		// A permanent response is neutral for provider-health accounting, but
		// it still completes an admitted half-open probe.
		if cb.state == CircuitHalfOpen {
			cb.probeInFlight = false
		}
		return
	}

	cb.totalFailures++
	cb.lastFailure = time.Now()

	switch cb.state {
	case CircuitClosed:
		cb.failureCount++
		if cb.failureCount >= cb.failureThreshold {
			cb.state = CircuitOpen
			cb.probeInFlight = false
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.failureCount++
		cb.successCount = 0
		cb.probeInFlight = false
	case CircuitOpen:
		cb.probeInFlight = false
		cb.failureCount++
		// Extend timeout with jitter on repeated failures
		jitter := time.Duration(randInt(0, int(cb.timeout.Seconds()/2))) * time.Second
		cb.timeout += jitter
	}
}

// releaseProbe completes an admitted call without changing health counters.
// It is used when no provider request was sent or the parent context cancelled
// the call, neither of which is evidence that the provider is unhealthy.
func (cb *CircuitBreaker) releaseProbe() {
	if cb == nil {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == CircuitHalfOpen {
		cb.probeInFlight = false
	}
}

func randInt(min, max int) int {
	if max <= min {
		return min
	}
	return min + int(time.Now().UnixNano())%(max-min)
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// SetTimeout updates how long an open circuit waits before admitting a probe.
// It is useful for runtime reconfiguration and deterministic tests.
func (cb *CircuitBreaker) SetTimeout(timeout time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.timeout = timeout
}

// Stats returns circuit breaker statistics.
func (cb *CircuitBreaker) Stats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return map[string]interface{}{
		"state":           string(cb.state),
		"failures":        cb.failureCount,
		"successes":       cb.successCount,
		"total_failures":  cb.totalFailures,
		"total_successes": cb.totalSuccess,
		"last_failure":    cb.lastFailure.Format("15:04:05"),
		"last_success":    cb.lastSuccess.Format("15:04:05"),
	}
}

// Reset manually resets the circuit breaker.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.probeInFlight = false
}

// ==================== circuit_breaker tool ====================

type CircuitBreakerTool struct {
	Breaker *CircuitBreaker
}

func (t *CircuitBreakerTool) Name() string { return "circuit_breaker" }

func (t *CircuitBreakerTool) Description() string {
	return "View or reset the API circuit breaker state. " +
		"The circuit breaker protects API quota by stopping calls after consecutive failures. " +
		"States: closed (normal), open (blocking due to failures), half_open (testing recovery)."
}

func (t *CircuitBreakerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'status' (view state), 'reset' (force close)",
				"enum":        []string{"status", "reset"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *CircuitBreakerTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := "status"
	if value, ok := params["action"].(string); ok && value != "" {
		action = value
	}

	if t.Breaker == nil {
		return "Circuit breaker not available", nil
	}

	switch action {
	case "status":
		stats := t.Breaker.Stats()
		return fmt.Sprintf("Circuit Breaker: **%s**\n- Current failures: %v\n- Total failures: %v\n- Total successes: %v\n- Last failure: %v",
			stats["state"], stats["failures"], stats["total_failures"], stats["total_successes"], stats["last_failure"]), nil
	case "reset":
		t.Breaker.Reset()
		return "Circuit breaker reset to closed state", nil
	}
	return fmt.Sprintf("Unknown action: %s", action), nil
}
