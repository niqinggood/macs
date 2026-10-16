package app

import (
	"os"
	"path/filepath"
	"testing"

	"mac/internal/permissionpolicy"
)

func TestDenialTrackerLoadSave(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)

	// Should start empty
	rules := dt.GetRules()
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}

	// Record a denial
	dt.RecordDenial("cli", map[string]interface{}{"command": "rm -rf /"}, "dangerous command")
	dt.RecordDenial("cli", map[string]interface{}{"command": "rm -rf /"}, "still dangerous")

	// Should now auto-deny
	deny, reason := dt.ShouldAutoDeny("cli", map[string]interface{}{"command": "rm -rf /tmp"})
	if !deny {
		t.Error("expected auto-deny for rm command")
	}
	if reason == "" {
		t.Error("expected reason for denial")
	}

	// Different command should not be denied
	deny, _ = dt.ShouldAutoDeny("cli", map[string]interface{}{"command": "ls -la"})
	if deny {
		t.Error("ls should not be auto-denied")
	}

	// Save and reload
	if err := dt.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	dt2 := permissionpolicy.NewDenialTracker(dir)
	if err := dt2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	rules = dt2.GetRules()
	if len(rules) != 1 {
		t.Errorf("after reload: expected 1 rule, got %d", len(rules))
	}
}

func TestDenialTrackerRemove(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)

	dt.RecordDenial("web_fetch", map[string]interface{}{"url": "https://evil.com"}, "bad")
	dt.RecordDenial("web_fetch", map[string]interface{}{"url": "https://evil.com/page"}, "bad again")

	// Should be auto-denied
	deny, _ := dt.ShouldAutoDeny("web_fetch", map[string]interface{}{"url": "https://evil.com/api"})
	if !deny {
		t.Error("expected auto-deny")
	}

	// Remove the rule
	pattern := permissionpolicy.ExtractDenialPattern("web_fetch", map[string]interface{}{"url": "https://evil.com/api"})
	dt.RemoveRule("web_fetch", pattern)

	// Should no longer deny
	deny, _ = dt.ShouldAutoDeny("web_fetch", map[string]interface{}{"url": "https://evil.com/api"})
	if deny {
		t.Error("should not deny after removal")
	}
}

func TestDenialTrackerReset(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)

	dt.RecordDenial("cli", map[string]interface{}{"command": "shutdown"}, "no")
	dt.RecordDenial("cli", map[string]interface{}{"command": "shutdown"}, "no again")

	if err := dt.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	rules := dt.GetRules()
	if len(rules) != 0 {
		t.Errorf("after reset: expected 0 rules, got %d", len(rules))
	}
}

func TestExtractDenialPattern(t *testing.T) {
	tests := []struct {
		tool string
		args map[string]interface{}
		want string
	}{
		{"cli", map[string]interface{}{"command": "rm -rf /tmp"}, "cmd:rm"},
		{"web_fetch", map[string]interface{}{"url": "https://evil.com/path"}, "domain:evil.com"},
		{"write_file", map[string]interface{}{"path": "/etc/hosts"}, "dir:/etc"},
		{"git", map[string]interface{}{"action": "push"}, "action:push"},
		{"read_file", nil, "*"},
	}
	for _, tt := range tests {
		got := permissionpolicy.ExtractDenialPattern(tt.tool, tt.args)
		if got != tt.want {
			t.Errorf("extractDenialPattern(%s, %v)=%q, want %q", tt.tool, tt.args, got, tt.want)
		}
	}
}

func TestDenialTrackerPersistence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".openkitty", "denial_rules.json")

	dt := permissionpolicy.NewDenialTracker(dir)
	dt.RecordDenial("write_file", map[string]interface{}{"path": "/etc/passwd"}, "system file")
	dt.RecordDenial("write_file", map[string]interface{}{"path": "/etc/passwd"}, "still system")

	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Error("denial_rules.json should exist after save")
	}

	// Fresh tracker should load rules
	dt2 := permissionpolicy.NewDenialTracker(dir)
	dt2.Load()
	rules := dt2.GetRules()
	if len(rules) != 1 {
		t.Errorf("loaded rules: expected 1, got %d", len(rules))
	}
}
