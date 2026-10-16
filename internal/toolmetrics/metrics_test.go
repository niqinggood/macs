package toolmetrics

import (
	"strings"
	"testing"
)

func TestRecordReportAndReset(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Record("read_file", true, 100)
	Record("read_file", false, 200)
	report := Report()
	for _, expected := range []string{"read_file: 2 calls", "50% fail", "avg 150ms"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("Report() = %q, missing %q", report, expected)
		}
	}
	Reset()
	if report := Report(); report != "No tools used yet." {
		t.Fatalf("Report() after reset = %q", report)
	}
}
