package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type toolDetailSearchMockTool struct {
	name string
	desc string
}

func (m toolDetailSearchMockTool) Name() string        { return m.name }
func (m toolDetailSearchMockTool) Description() string { return m.desc }
func (m toolDetailSearchMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (m toolDetailSearchMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestToolDetailSearchRanksNormalizedNameMatches(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(toolDetailSearchMockTool{name: "web_fetch", desc: "Fetch URL content"})
	reg.Register(toolDetailSearchMockTool{name: "read_file", desc: "Read a local file"})
	reg.Register(toolDetailSearchMockTool{name: "knowledge_query", desc: "Search documents and notes"})

	result := (&ToolDetailTool{registry: reg}).searchTools("read file")

	firstRead := strings.Index(result, "**read_file**")
	firstWeb := strings.Index(result, "**web_fetch**")
	if firstRead < 0 {
		t.Fatalf("expected read_file in search result:\n%s", result)
	}
	if firstWeb >= 0 && firstWeb < firstRead {
		t.Fatalf("expected read_file to rank before web_fetch:\n%s", result)
	}
}

func TestToolDetailSearchReportsTotalWhenResultsAreTruncated(t *testing.T) {
	reg := NewToolRegistry()
	for i := 0; i < 12; i++ {
		reg.Register(toolDetailSearchMockTool{
			name: fmt.Sprintf("bulk_%02d", i),
			desc: "common capability used for bulk search tests",
		})
	}

	result := (&ToolDetailTool{registry: reg}).searchTools("common")

	for _, want := range []string{
		"Found 12 tools matching 'common'",
		"Showing top 10 of 12 matches.",
		"**bulk_00**",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in search result:\n%s", want, result)
		}
	}
	if strings.Contains(result, "**bulk_11**") {
		t.Fatalf("expected truncated result to omit bulk_11:\n%s", result)
	}
}

func TestToolDetailSearchRejectsBlankQuery(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(toolDetailSearchMockTool{name: "read_file", desc: "Read a local file"})

	result := (&ToolDetailTool{registry: reg}).searchTools("  ")

	if !strings.Contains(result, "empty query") {
		t.Fatalf("expected empty query hint, got %q", result)
	}
}
