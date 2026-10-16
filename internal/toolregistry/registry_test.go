package toolregistry

import (
	"context"
	"strings"
	"testing"

	"mac/internal/toolcontract"
)

type metadataSearchTestTool struct{}

func (metadataSearchTestTool) Name() string { return "document_filler" }
func (metadataSearchTestTool) Description() string {
	return "Fill structured DOCX templates."
}
func (metadataSearchTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (metadataSearchTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}
func (metadataSearchTestTool) ToolSearchMetadata() toolcontract.ToolSearchMetadata {
	return toolcontract.ToolSearchMetadata{
		Aliases: []string{"lims_word_fill"},
		Tags:    []string{"Word", "下划线", "表格", "填写"},
	}
}

func TestToolDetailSearchUsesRegisteredMetadata(t *testing.T) {
	registry := New(Dependencies{})
	registry.RegisterBuiltinTools()
	registry.Register(metadataSearchTestTool{})
	detail, ok := registry.Get("tool_detail")
	if !ok {
		t.Fatal("tool_detail was not registered")
	}
	result, err := detail.Execute(context.Background(), map[string]interface{}{
		"action":    "search",
		"tool_name": "LIMS Word下划线填写",
	})
	if err != nil {
		t.Fatalf("search metadata: %v", err)
	}
	if !strings.Contains(result, "**document_filler**") {
		t.Fatalf("metadata-backed tool not found:\n%s", result)
	}
}

func TestToolDetailAdvertisesHermesStyleSearchAlias(t *testing.T) {
	registry := New(Dependencies{})
	registry.RegisterBuiltinTools()
	detail, _ := registry.Get("tool_detail")
	result, err := detail.Execute(context.Background(), map[string]interface{}{
		"action":    "search",
		"tool_name": "tool_search",
	})
	if err != nil {
		t.Fatalf("search tool alias: %v", err)
	}
	if !strings.Contains(result, "**tool_detail**") {
		t.Fatalf("tool_search alias did not resolve existing tool_detail:\n%s", result)
	}
}
