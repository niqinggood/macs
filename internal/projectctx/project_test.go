package projectctx

import (
	"context"
	"runtime"
	"testing"
)

func TestProjectContextStableForWorkspace(t *testing.T) {
	first := New(".")
	second := New(".")
	if first.ProjectID == "" || first.ProjectID != second.ProjectID || first.Workspace == "" {
		t.Fatalf("unstable project contexts: %#v %#v", first, second)
	}
}

func TestResolvedWorkspaceRoundTrip(t *testing.T) {
	want := NormalizeWorkspace(".")
	if got := ResolvedWorkspace(WithResolvedWorkspace(context.Background(), ".")); got != want {
		t.Fatalf("ResolvedWorkspace() = %q, want %q", got, want)
	}
}

func TestWindowsIdentityIgnoresCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only normalization")
	}
	if New(`E:\OpenKitty`).ProjectID != New(`e:\openkitty`).ProjectID {
		t.Fatal("Windows path case changed the project identity")
	}
}
