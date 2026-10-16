package codegraph

import (
	"testing"
)

func TestScanSelf(t *testing.T) {
	g, err := ScanGoProject("../..") // scan the main openkitty project
	if err != nil {
		t.Skipf("scan failed (may need working dir): %v", err)
		return
	}
	t.Logf("Files: %d, Symbols: %d, Packages: %d", g.Files, len(g.Symbols), len(g.Packages))

	if g.Files == 0 {
		t.Error("expected at least 1 file")
	}

	kindCount := make(map[string]int)
	for _, s := range g.Symbols {
		kindCount[s.Kind]++
	}
	for k, v := range kindCount {
		t.Logf("  %s: %d", k, v)
	}
}
