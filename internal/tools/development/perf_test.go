package development

import (
	"context"
	"strings"
	"testing"
)

func TestParseGoBenchOutput(t *testing.T) {
	output := "BenchmarkEncode-8  2500  1200.5 ns/op  2048 B/op  12 allocs/op\n"
	results := ParseGoBenchOutput(output)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	got := results[0]
	if got.Name != "BenchmarkEncode-8" || got.Ops != 2500 || got.NsPerOp != 1200.5 || got.BytesPerOp != 2048 || got.AllocsPerOp != 12 {
		t.Fatalf("unexpected benchmark result: %#v", got)
	}
}

func TestPerfAnalyzeWithoutProfilesIsActionable(t *testing.T) {
	result, err := NewPerfTool(t.TempDir()).Execute(context.Background(), map[string]interface{}{"action": "analyze"})
	if err != nil {
		t.Fatalf("analyze empty workspace: %v", err)
	}
	if !strings.Contains(result, "Run 'profile' action first") {
		t.Fatalf("unexpected empty analysis result: %q", result)
	}
}
