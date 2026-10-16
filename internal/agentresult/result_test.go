package agentresult

import (
	"fmt"
	"testing"
)

func TestExecutionErrorCarriesReasonAndOutput(t *testing.T) {
	err := NewError("task failed", "partial result")
	if err.Error() != "task failed" {
		t.Fatalf("Error() = %q", err.Error())
	}
	wrapper := fmt.Errorf("agent: %w", err)
	executionError, ok := AsExecutionError(wrapper)
	if !ok || executionError.UserMessage() != "partial result" {
		t.Fatalf("AsExecutionError() = %#v, %v", executionError, ok)
	}
	if got := FailureUserMessage("", wrapper); got != "partial result" {
		t.Fatalf("FailureUserMessage() = %q", got)
	}
}

func TestTerminal(t *testing.T) {
	if output, err := Terminal("diagnostic", "failed", "task failed"); output != "diagnostic" || err == nil {
		t.Fatalf("failed Terminal() = %q, %v", output, err)
	}
	if output, err := Terminal("done", "success", ""); output != "done" || err != nil {
		t.Fatalf("successful Terminal() = %q, %v", output, err)
	}
}
