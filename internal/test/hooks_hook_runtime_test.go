package app

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadHookSettingsSupportsDirectJSONAndNestedYAML(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		event   LifecycleEvent
	}{
		{
			name:  "direct json",
			file:  "settings.json",
			event: LCEventPostToolUse,
			content: `{"enabled":{"PostToolUse":[` +
				`{"type":"callback","command":"audit","match":"edit_*"}]}}`,
		},
		{
			name:  "nested yaml",
			file:  "settings.yaml",
			event: LCEventPreToolUse,
			content: "other_setting: true\nhooks:\n  enabled:\n    PreToolUse:\n" +
				"      - type: http\n        url: https://example.com/hook\n        timeout: 3\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.file)
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			settings, err := LoadHookSettingsFromFile(path)
			if err != nil {
				t.Fatalf("load hook settings: %v", err)
			}
			if got := len(settings.Enabled[test.event]); got != 1 {
				t.Fatalf("expected one %s hook, got %d: %#v", test.event, got, settings)
			}
		})
	}
}

func TestLoadHookSettingsRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	content := `{"hooks":{"enabled":{"NotARealEvent":[{"type":"shell","command":"x.ps1"}]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadHookSettingsFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "NotARealEvent") {
		t.Fatalf("expected unknown lifecycle event error, got %v", err)
	}
}

func TestHookManagerDeduplicatesOnlyExactConfigs(t *testing.T) {
	manager := NewHookManagerV2(nil)
	manager.LoadConfig(V2HookSettings{Enabled: map[LifecycleEvent][]V2HookConfig{
		LCEventPreToolUse: {
			{Type: HookExecCallback, Command: "audit", Match: "read_*"},
			{Type: HookExecCallback, Command: "audit", Match: "read_*"},
			{Type: HookExecCallback, Command: "audit", Match: "edit_*"},
		},
	}})
	if got := manager.ConfigCount(); got != 2 {
		t.Fatalf("expected two distinct configs after exact dedupe, got %d", got)
	}
}

func TestReloadGlobalHookSettingsKeepsLiveSnapshotOnFailure(t *testing.T) {
	oldGlobal := GetGlobalHookManagerV2()
	defer SetGlobalHookManagerV2(oldGlobal)

	manager := NewHookManagerV2(nil)
	manager.LoadConfig(V2HookSettings{Enabled: map[LifecycleEvent][]V2HookConfig{
		LCEventPostToolUse: {{Type: HookExecCallback, Command: "existing"}},
	}})
	SetGlobalHookManagerV2(manager)
	want := manager.SnapshotConfig()

	workspace := t.TempDir()
	settingsDir := filepath.Join(workspace, ".openkitty")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{
  "hooks": {"enabled": {"PreToolUse": [{"type": "http", "url": "relative-url"}]}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReloadGlobalHookSettings(&Config{Workspace: workspace}, log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("expected invalid settings reload to fail")
	}
	if got := manager.SnapshotConfig(); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed reload changed live hooks\nwant: %#v\n got: %#v", want, got)
	}
}

func TestHookManagerConcurrentFireAndReloadDoesNotDeadlock(t *testing.T) {
	manager := NewHookManagerV2(nil)
	manager.RegisterCallback("allow", func(*HookContextV2) *HookResultV2 {
		return &HookResultV2{Decision: HookDecisionAllow}
	})
	settings := V2HookSettings{Enabled: map[LifecycleEvent][]V2HookConfig{
		LCEventPreToolUse: {{Type: HookExecCallback, Command: "allow", Match: "*"}},
	}}
	manager.LoadConfig(settings)

	finished := make(chan struct{}, 2)
	go func() {
		defer func() { finished <- struct{}{} }()
		for i := 0; i < 1000; i++ {
			manager.Fire(context.Background(), LCEventPreToolUse, "edit_file", &HookContextV2{})
		}
	}()
	go func() {
		defer func() { finished <- struct{}{} }()
		for i := 0; i < 1000; i++ {
			manager.LoadConfig(settings)
		}
	}()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for completed := 0; completed < 2; completed++ {
		select {
		case <-finished:
		case <-timer.C:
			t.Fatal("concurrent Fire/LoadConfig deadlocked")
		}
	}
}

func TestHookExecutionErrorDoesNotPanic(t *testing.T) {
	manager := NewHookManagerV2(nil)
	manager.LoadConfig(V2HookSettings{Enabled: map[LifecycleEvent][]V2HookConfig{
		LCEventPreToolUse: {{Type: HookExecCallback, Command: "missing"}},
	}})
	result := manager.Fire(context.Background(), LCEventPreToolUse, "read_file", nil)
	if len(result.Errors) != 1 {
		t.Fatalf("expected missing callback error, got %#v", result)
	}
}

func TestHookScriptSafetyOnlyBlocksSystemDestruction(t *testing.T) {
	dir := t.TempDir()
	ordinary := filepath.Join(dir, "ordinary.ps1")
	if err := os.WriteFile(ordinary, []byte("curl http://localhost/install\nsudo apt-get install jq\niptables -L\nRemove-Item ./build -Recurse\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isHookScriptSafe(ordinary) {
		t.Fatal("ordinary networking, install, administration, and project cleanup should be allowed")
	}

	destructive := filepath.Join(dir, "destructive.sh")
	if err := os.WriteFile(destructive, []byte("rm -rf /\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isHookScriptSafe(destructive) {
		t.Fatal("root filesystem deletion must remain blocked")
	}
}

func TestHTTPHookHonorsParentCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	executor := NewHookHTTPExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := executor.ExecuteContext(ctx, V2HookConfig{Type: HookExecHTTP, URL: server.URL}, &HookContextV2{})
	if err == nil {
		t.Fatal("expected parent cancellation to stop HTTP hook")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("HTTP hook ignored parent cancellation: %v", elapsed)
	}
}

func TestConfigWatcherHotReloadsHookSettingsAndRollsBackInvalidEdit(t *testing.T) {
	oldGlobal := GetGlobalHookManagerV2()
	defer SetGlobalHookManagerV2(oldGlobal)
	manager := NewHookManagerV2(nil)
	manager.LoadConfig(V2HookSettings{Enabled: map[LifecycleEvent][]V2HookConfig{
		LCEventPostToolUse: {{Type: HookExecCallback, Command: "keep"}},
	}})
	SetGlobalHookManagerV2(manager)
	wantAfterInvalid := manager.SnapshotConfig()

	workspace := t.TempDir()
	settingsDir := filepath.Join(workspace, ".openkitty")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := generateTemplateConfig()
	config.Workspace = workspace
	config.Gateway.Enabled = false
	config.Gateway.EnableWebUI = false
	config.Hooks = *DefaultHookSettings()
	configPath := filepath.Join(workspace, "openkitty-config.yaml")
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}

	bot := &ClawBot{
		config:     config,
		configPath: configPath,
		logger:     log.New(io.Discard, "", 0),
		stopCh:     make(chan os.Signal, 1),
	}
	bot.StartConfigWatcher()
	if bot.watcher == nil {
		t.Fatal("config watcher did not start")
	}
	defer bot.watcher.Close()

	settingsPath := filepath.Join(settingsDir, "settings.json")
	invalid := `{"hooks":{"enabled":{"PreToolUse":[{"type":"http","url":"not-absolute"}]}}}`
	if err := os.WriteFile(settingsPath, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(900 * time.Millisecond)
	if got := manager.SnapshotConfig(); !reflect.DeepEqual(got, wantAfterInvalid) {
		t.Fatalf("invalid watched edit replaced live hooks\nwant: %#v\n got: %#v", wantAfterInvalid, got)
	}

	valid := `{"hooks":{"enabled":{"PreToolUse":[` +
		`{"type":"callback","command":"one"},` +
		`{"type":"callback","command":"two"}]}}}`
	if err := os.WriteFile(settingsPath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(manager.SnapshotConfig().Enabled[LCEventPreToolUse]); got == 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("watcher did not hot reload valid Hook V2 settings: %#v", manager.SnapshotConfig())
}
