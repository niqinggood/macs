package development

import (
	"context"
	"strings"
	"testing"
)

func TestDebugToolAnalyzesGoPanic(t *testing.T) {
	input := "panic: runtime error: invalid memory address or nil pointer dereference\n\ngoroutine 1 [running]:\nmain.run()\n\t/app/main.go:42 +0x1\n"
	result, err := NewDebugTool().Execute(context.Background(), map[string]interface{}{"error_text": input})
	if err != nil {
		t.Fatalf("analyze panic: %v", err)
	}
	for _, expected := range []string{"Nil/Null Pointer Dereference", "/app/main.go:42", "Stack Trace", "Add nil check"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("analysis missing %q:\n%s", expected, result)
		}
	}
}

func TestExtractFileLocationsDeduplicates(t *testing.T) {
	locations := ExtractFileLocations("at src/main.go:10\nat src/main.go:10\nat src/util.ts:5:2")
	if len(locations) != 2 {
		t.Fatalf("got %d locations, want 2: %#v", len(locations), locations)
	}
}

func TestDetectErrorType(t *testing.T) {
	if got := DetectErrorType("context deadline exceeded"); got != "Timeout" {
		t.Fatalf("DetectErrorType() = %q", got)
	}
}
