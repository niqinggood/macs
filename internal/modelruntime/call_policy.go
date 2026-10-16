package modelruntime

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPStatusError struct {
	StatusCode int
	Header     http.Header
	Message    string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "LLM HTTP error"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return "LLM HTTP status " + strconv.Itoa(e.StatusCode)
}

func HTTPErrorMetadata(err error) (int, http.Header, bool) {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil {
		return 0, nil, false
	}
	return statusErr.StatusCode, statusErr.Header, true
}

const (
	defaultLLMCallTimeout = 300 * time.Second
	minLLMCallTimeout     = 15 * time.Second
	maxLLMCallTimeout     = 300 * time.Second
	defaultLLMMaxAttempts = 2
	maxLLMMaxAttempts     = 3
	MaxRetryDelay         = 30 * time.Second
)

// CallPolicy is the bounded retry policy for one logical model call.
// MaxAttempts includes the initial request, despite the legacy config key being
// named llm_max_retries. This keeps the default at two network attempts rather
// than silently expanding it to one initial request plus two retries.
type CallPolicy struct {
	AttemptTimeout time.Duration
	MaxAttempts    int
}

type llmCallTimeoutOverrideContextKey struct{}
type llmCallMaxAttemptsOverrideContextKey struct{}

func WithCallTimeoutOverride(ctx context.Context, timeout time.Duration) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx
	}
	if timeout > maxLLMCallTimeout {
		timeout = maxLLMCallTimeout
	}
	return context.WithValue(ctx, llmCallTimeoutOverrideContextKey{}, timeout)
}

// WithCallMaxAttemptsOverride applies a turn-local attempt budget without
// changing the global runtime configuration. This is intentionally an exact
// override (after clamping), so a long-running construction call can avoid a
// second full timeout even when the global policy normally permits retries.
func WithCallMaxAttemptsOverride(ctx context.Context, attempts int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if attempts <= 0 {
		return ctx
	}
	if attempts > maxLLMMaxAttempts {
		attempts = maxLLMMaxAttempts
	}
	return context.WithValue(ctx, llmCallMaxAttemptsOverrideContextKey{}, attempts)
}

func CallPolicyForContext(ctx context.Context, policy CallPolicy) CallPolicy {
	if ctx == nil {
		return policy
	}
	override, _ := ctx.Value(llmCallTimeoutOverrideContextKey{}).(time.Duration)
	if override > policy.AttemptTimeout {
		if override > maxLLMCallTimeout {
			override = maxLLMCallTimeout
		}
		policy.AttemptTimeout = override
	}
	if attempts, ok := ctx.Value(llmCallMaxAttemptsOverrideContextKey{}).(int); ok && attempts > 0 {
		if attempts > maxLLMMaxAttempts {
			attempts = maxLLMMaxAttempts
		}
		policy.MaxAttempts = attempts
	}
	return policy
}

func ResolveCallPolicy(timeoutSeconds, maxAttempts int) CallPolicy {
	timeout := time.Duration(timeoutSeconds) * time.Second
	switch {
	case timeoutSeconds <= 0:
		timeout = defaultLLMCallTimeout
	case timeout < minLLMCallTimeout:
		timeout = minLLMCallTimeout
	case timeout > maxLLMCallTimeout:
		timeout = maxLLMCallTimeout
	}

	attempts := maxAttempts
	switch {
	case attempts <= 0:
		attempts = defaultLLMMaxAttempts
	case attempts > maxLLMMaxAttempts:
		attempts = maxLLMMaxAttempts
	}

	return CallPolicy{AttemptTimeout: timeout, MaxAttempts: attempts}
}

// retryBackoff returns the delay before attempt retryIndex, where zero is the
// initial attempt. The maximum is kept small because model calls already have a
// per-attempt deadline.
func (p CallPolicy) RetryBackoff(retryIndex int) time.Duration {
	if retryIndex <= 0 {
		return 0
	}
	backoff := time.Duration(1<<uint(retryIndex)) * 2 * time.Second
	if backoff > 30*time.Second {
		return 30 * time.Second
	}
	return backoff
}

// retryDelay keeps retries bounded while honoring a provider's Retry-After
// directive. OpenKitty deliberately has no provider/model fallback here: the
// exact same request is retried against the user-selected model.
func (p CallPolicy) RetryDelay(retryIndex int, retryAfter string, now time.Time) time.Duration {
	if delay, ok := ParseRetryAfter(retryAfter, now); ok {
		if delay > MaxRetryDelay {
			return MaxRetryDelay
		}
		return delay
	}
	return p.RetryBackoff(retryIndex)
}

func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func IsRetryableHTTPStatus(status int, headers http.Header) bool {
	if headers != nil {
		switch strings.ToLower(strings.TrimSpace(headers.Get("X-Should-Retry"))) {
		case "false":
			return false
		case "true":
			return true
		}
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return status >= 500
	}
}

// totalWaitUpperBound is the maximum wall-clock wait introduced by the model
// request attempts and their backoffs. Generate must not add another outer
// retry for an exhausted call, otherwise this bound would be multiplied.
func (p CallPolicy) TotalWaitUpperBound() time.Duration {
	total := time.Duration(p.MaxAttempts) * p.AttemptTimeout
	for retryIndex := 1; retryIndex < p.MaxAttempts; retryIndex++ {
		// Retry-After is honored but capped at MaxRetryDelay, which can be
		// longer than the ordinary exponential backoff.
		delay := p.RetryBackoff(retryIndex)
		if delay < MaxRetryDelay {
			delay = MaxRetryDelay
		}
		total += delay
	}
	return total
}

// AttemptContext avoids stacking a later deadline on a parent context
// that is already stricter. This is shared by streaming and non-streaming calls.
func AttemptContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultLLMCallTimeout
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func WaitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
