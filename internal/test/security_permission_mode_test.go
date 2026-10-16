package app

import "testing"

func TestPermissionModeValid(t *testing.T) {
	tests := []struct {
		mode string
		ok   bool
	}{
		{"default", true},
		{"acceptEdits", true},
		{"bypassPermissions", true},
		{"plan", true},
		{"invalid", false},
		{"", false},
		{"BYPASSPERMISSIONS", false},
	}
	for _, tt := range tests {
		if got := IsValidPermissionMode(tt.mode); got != tt.ok {
			t.Errorf("IsValidPermissionMode(%q)=%v, want %v", tt.mode, got, tt.ok)
		}
	}
}

func TestResolvePermissionDefault(t *testing.T) {
	tests := []struct {
		tool    string
		args    map[string]interface{}
		allowed bool
		confirm bool
	}{
		{"read_file", nil, true, false},
		{"list_dir", nil, true, false},
		{"write_file", nil, true, false},
		{"web_fetch", nil, true, false},
		{"cli", nil, true, false},
		{"external_agent", nil, true, false},
		{"nonexistent", nil, true, false}, // unknown defaults to Moderate
	}
	for _, tt := range tests {
		allowed, confirm, _ := ResolvePermission(PermissionDefault, tt.tool, tt.args)
		if allowed != tt.allowed {
			t.Errorf("default %s: allowed=%v want=%v", tt.tool, allowed, tt.allowed)
		}
		if confirm != tt.confirm {
			t.Errorf("default %s: confirm=%v want=%v", tt.tool, confirm, tt.confirm)
		}
	}
}

func TestResolvePermissionCLICommandSemantics(t *testing.T) {
	allowed, confirm, reason := ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": "go test ./..."})
	if !allowed || confirm || reason != "" {
		t.Fatalf("go test should be allowed without confirmation, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}

	readOnlyChain := `cd .openkitty/storage && dir alibaba_bytedance_agent_analysis.md && findstr /N "MiniMax\|DeepSeek\|## " alibaba_bytedance_agent_analysis.md | findstr /N "."`
	allowed, confirm, reason = ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": readOnlyChain})
	if !allowed || confirm || reason != "" {
		t.Fatalf("read-only cli chain should be allowed without confirmation, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}

	allowed, confirm, reason = ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": "where python || where python3"})
	if !allowed || confirm || reason != "" {
		t.Fatalf("python discovery command should be allowed without confirmation, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}

	allowed, confirm, reason = ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": "powershell -EncodedCommand SQBFAFgA"})
	if !allowed || confirm || reason != "" {
		t.Fatalf("non-system-delete cli command should not be blocked by OpenKitty permission layer, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}

	allowed, confirm, reason = ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": "rm -rf /"})
	if !allowed || confirm || reason != "" {
		t.Fatalf("permission layer should not require approval; cli safety enforces system protection, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}

func TestResolvePermissionAllowsNovaChatroomCLI(t *testing.T) {
	commands := []string{
		`cd backups/openkitty_code_20260709_013920/openkitty-skills/nova-community/scripts && python chatroom_agent.py read --limit 20`,
		`cd backups/openkitty_code_20260709_013920/openkitty-skills/nova-community/scripts && python chatroom_agent.py topics 2>&1`,
	}
	for _, command := range commands {
		allowed, confirm, reason := ResolvePermission(PermissionDefault, "cli", map[string]interface{}{"command": command})
		if !allowed || confirm || reason != "" {
			t.Fatalf("nova chatroom command should run without OpenKitty confirmation, allowed=%v confirm=%v reason=%q command=%s", allowed, confirm, reason, command)
		}
	}
}

func TestResolvePermissionBypass(t *testing.T) {
	allowed, confirm, _ := ResolvePermission(PermissionBypass, "cli", nil)
	if !allowed || confirm {
		t.Errorf("bypass cli: allowed=%v confirm=%v, want allowed=true confirm=false", allowed, confirm)
	}
	allowed, confirm, reason := ResolvePermission(PermissionBypass, "cli", map[string]interface{}{"command": "rm -rf /"})
	if !allowed || confirm || reason != "" {
		t.Errorf("bypass permission layer should not require approval; cli safety enforces system protection, allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}

func TestResolvePermissionPlan(t *testing.T) {
	// Plan mode: read-only allowed, write blocked
	allowed, _, reason := ResolvePermission(PermissionPlan, "read_file", nil)
	if !allowed {
		t.Errorf("plan read_file should be allowed")
	}

	allowed, _, reason = ResolvePermission(PermissionPlan, "write_file", nil)
	if allowed {
		t.Errorf("plan write_file should be blocked, got: %s", reason)
	}

	// plan_task should be allowed in plan mode
	allowed, _, _ = ResolvePermission(PermissionPlan, "plan_task", nil)
	if !allowed {
		t.Errorf("plan plan_task should be allowed")
	}
}

func TestResolvePermissionAcceptEdits(t *testing.T) {
	// AcceptEdits: file edits auto-accepted
	allowed, confirm, _ := ResolvePermission(PermissionAcceptEdits, "write_file", nil)
	if !allowed || confirm {
		t.Errorf("acceptEdits write_file: allowed=%v confirm=%v, want auto-accept", allowed, confirm)
	}

	allowed, confirm, _ = ResolvePermission(PermissionAcceptEdits, "edit_file", nil)
	if !allowed || confirm {
		t.Errorf("acceptEdits edit_file: allowed=%v confirm=%v, want auto-accept", allowed, confirm)
	}

	allowed, confirm, _ = ResolvePermission(PermissionAcceptEdits, "cli", nil)
	if !allowed || confirm {
		t.Errorf("acceptEdits cli: allowed=%v confirm=%v, want allow without confirmation", allowed, confirm)
	}
}
