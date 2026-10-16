package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHookManagerNew(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	if hm == nil {
		t.Fatal("NewHookManager returned nil")
	}
	if hm.maxTimeout != 30*time.Second {
		t.Error("default timeout should be 30s")
	}
	if hm.maxRetries != 2 {
		t.Error("default retries should be 2")
	}
	if !hm.sandboxMode {
		t.Error("default sandbox should be enabled")
	}
}

func TestHookManagerSetConfig(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	hm.SetConfig(5*time.Second, 0, false)
	if hm.maxTimeout != 5*time.Second || hm.maxRetries != 0 || hm.sandboxMode {
		t.Error("SetConfig failed")
	}
}

func TestHookManagerListRegistered(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	// Empty dir should have no hooks
	if len(hm.ListRegistered()) != 0 {
		t.Error("empty dir should have no hooks")
	}

	// Create a hook script
	os.WriteFile(filepath.Join(dir, "pre_tool_use.sh"), []byte("#!/bin/bash\necho ok"), 0755)
	registered := hm.ListRegistered()
	if len(registered) != 1 || registered[0] != HookPreToolUse {
		t.Errorf("should find pre_tool_use hook, got %v", registered)
	}
}

func TestHookManagerFindScript(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)

	// No scripts
	if hm.findHookScript(HookPreToolUse) != "" {
		t.Error("should not find nonexistent script")
	}

	// Create .sh
	os.WriteFile(filepath.Join(dir, "pre_tool_use.sh"), []byte("echo ok"), 0755)
	if hm.findHookScript(HookPreToolUse) == "" {
		t.Error("should find .sh script")
	}

	// Create .bat
	dir2 := t.TempDir()
	hm2 := NewHookManager(dir2)
	os.WriteFile(filepath.Join(dir2, "session_start.bat"), []byte("@echo ok"), 0755)
	if hm2.findHookScript(HookSessionStart) == "" {
		t.Error("should find .bat script")
	}
}

func TestHookManagerIsSafe(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)

	// Safe script
	safePath := filepath.Join(dir, "safe.sh")
	os.WriteFile(safePath, []byte("echo hello"), 0755)
	if !hm.isHookSafe(safePath) {
		t.Error("safe script should pass")
	}

	// Dangerous script
	dangerPath := filepath.Join(dir, "danger.sh")
	os.WriteFile(dangerPath, []byte("sudo rm -rf /"), 0755)
	if hm.isHookSafe(dangerPath) {
		t.Error("dangerous script should be blocked")
	}

	// Sandbox disabled
	hm.sandboxMode = false
	if !hm.isHookSafe(dangerPath) {
		t.Error("with sandbox off, dangerous script should pass")
	}
}

func TestHookManagerAuditLog(t *testing.T) {
	dir := t.TempDir()
	hm := NewHookManager(dir)
	// Should not panic
	hm.auditLog(HookPreToolUse, "agent1", "read_file", true, 100*time.Millisecond, nil)
	hm.auditLog(HookPostToolUse, "agent1", "write_file", false, 50*time.Millisecond, nil)
}
