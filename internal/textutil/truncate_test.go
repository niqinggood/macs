package textutil

import (
	"strings"
	"testing"
)

func TestTruncatePreservesHistoricalWordCounting(t *testing.T) {
	if got := Truncate("hello world", 5); got != "hello world" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := Truncate("abcdefghij", 3); got != "abcdefghij" {
		t.Fatalf("one long English word should count as one unit: %q", got)
	}
}

func TestTruncateLimitsChineseCharacters(t *testing.T) {
	got := Truncate("你好世界", 2)
	if !strings.HasPrefix(got, "你好...") || !strings.Contains(got, "4→2") {
		t.Fatalf("unexpected truncation result: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("你好世界", 2); got != "你好..." {
		t.Fatalf("TruncateRunes() = %q", got)
	}
}
