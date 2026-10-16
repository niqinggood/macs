package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestDelegateToCannotActivateEmployeeFromMainAgent(t *testing.T) {
	tool := NewAECollaborationTool(NewAIEmployeeManager(nil, log.New(io.Discard, "", 0)), log.New(io.Discard, "", 0))
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"ae_id": "quant-trader",
		"task":  "analyze NVDA",
	})
	if err == nil || !strings.Contains(err.Error(), "@mention") {
		t.Fatalf("delegate_to error = %v, want explicit @ activation denial", err)
	}
}
