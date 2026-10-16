// Package agentresult carries typed terminal failures across Agent, Gateway,
// channel, and persistence boundaries.
package agentresult

import (
	"errors"
	"strings"
)

// ExecutionError retains a user-facing partial result while the run remains a
// failed terminal state.
type ExecutionError struct {
	Reason string
	Output string
}

func (executionError *ExecutionError) Error() string {
	if executionError == nil {
		return "agent execution failed"
	}
	if reason := strings.TrimSpace(executionError.Reason); reason != "" {
		return reason
	}
	return "agent execution failed"
}

func (executionError *ExecutionError) UserMessage() string {
	if executionError == nil {
		return ""
	}
	return strings.TrimSpace(executionError.Output)
}

func NewError(reason, output string) error {
	return &ExecutionError{Reason: strings.TrimSpace(reason), Output: strings.TrimSpace(output)}
}

func Terminal(output, status, reason string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(status), "failed") {
		return output, NewError(reason, output)
	}
	return output, nil
}

func AsExecutionError(err error) (*ExecutionError, bool) {
	var executionError *ExecutionError
	if !errors.As(err, &executionError) {
		return nil, false
	}
	return executionError, true
}

func FailureUserMessage(output string, err error) string {
	if executionError, ok := AsExecutionError(err); ok && executionError.UserMessage() != "" {
		return executionError.UserMessage()
	}
	if output = strings.TrimSpace(output); output != "" {
		return output
	}
	if err != nil {
		return strings.TrimSpace(err.Error())
	}
	return ""
}
