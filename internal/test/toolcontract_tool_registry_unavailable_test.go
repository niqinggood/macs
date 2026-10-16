package app

import (
	"context"
	"strings"
	"testing"
)

func TestToolRegistryUnavailableToolErrorListsActualTaskTools(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(registryNameMockTool{name: "read_file"})
	registry.Register(registryNameMockTool{name: "web_fetch"})

	_, err := registry.ExecuteRaw(context.Background(), ToolCall{
		ID:   "missing-call",
		Name: "list_dir",
	})
	if err == nil {
		t.Fatal("missing tool unexpectedly executed")
	}
	message := err.Error()
	for _, want := range []string{
		`tool "list_dir" is not available in this task`,
		"read_file",
		"web_fetch",
		"do not simulate the missing tool",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing-tool recovery error lacks %q: %s", want, message)
		}
	}
}
