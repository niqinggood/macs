package desktop

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLinuxXDoToolKeyTranslation(t *testing.T) {
	tests := map[string]string{
		"enter":        "Return",
		"ctrl+s":       "ctrl+s",
		"ctrl+shift+a": "ctrl+shift+a",
		"pageup":       "Prior",
		"win+r":        "super+r",
		"f12":          "f12",
	}
	for input, wanted := range tests {
		got, ok := linuxXDoToolKey(input)
		if !ok || got != wanted {
			t.Errorf("linuxXDoToolKey(%q)=(%q,%t), want (%q,true)", input, got, ok, wanted)
		}
	}
	if _, ok := linuxXDoToolKey("ctrl+not-a-key"); ok {
		t.Fatal("unexpected support for an unknown key")
	}
}

func TestLinuxWtypeChordTranslation(t *testing.T) {
	got, ok := linuxWtypeKeyArgs("ctrl+shift+s")
	want := []string{"-M", "ctrl", "-M", "shift", "-k", "s", "-m", "shift", "-m", "ctrl"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("linuxWtypeKeyArgs=(%v,%t), want (%v,true)", got, ok, want)
	}
}

func TestDesktopScreenshotPathCanBeControlledByComputerSession(t *testing.T) {
	workspace := t.TempDir()
	tool := NewDesktopTaskTool(workspace, NewDesktopTaskManager())
	wanted := filepath.Join(workspace, ".openkitty", "storage", "computer", "session", "screen.png")
	abs, rel := tool.screenshotPaths("ignored", wanted)
	if abs != filepath.Clean(wanted) {
		t.Fatalf("absolute path=%q want=%q", abs, wanted)
	}
	if rel != ".openkitty/storage/computer/session/screen.png" {
		t.Fatalf("relative path=%q", rel)
	}
}

func TestDesktopSchemaExposesCrossPlatformComputerActions(t *testing.T) {
	tool := NewDesktopTaskTool(".", NewDesktopTaskManager())
	properties := tool.Parameters()["properties"].(map[string]interface{})
	action := properties["action"].(map[string]interface{})
	values := action["enum"].([]string)
	for _, wanted := range []string{"capabilities", "scroll", "drag"} {
		found := false
		for _, value := range values {
			if value == wanted {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("desktop action schema is missing %q: %v", wanted, values)
		}
	}
}

func TestDesktopCapabilitiesDoesNotCreateTaskRecord(t *testing.T) {
	manager := NewDesktopTaskManager()
	tool := NewDesktopTaskTool(t.TempDir(), manager)
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"action": "capabilities"}); err != nil {
		t.Fatalf("capabilities failed: %v", err)
	}
	if tasks := manager.List(10); len(tasks) != 0 {
		t.Fatalf("capability inspection created %d task record(s)", len(tasks))
	}
}
