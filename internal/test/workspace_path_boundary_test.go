package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func personalWorkspaceTestContext(workspace, subjectID string) context.Context {
	ctx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID:     "tenant-personal-files",
		UserID:       "service:test",
		SubjectID:    subjectID,
		ServiceID:    "test",
		Service:      true,
		TenantScoped: true,
	})
	return withResolvedWorkspace(ctx, workspace)
}

func requirePersonalWorkspaceRejection(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected personal workspace boundary rejection")
	}
	if !strings.Contains(err.Error(), "personal workspace boundary") {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestPersonalAgentOSFileBoundaryReadWriteEdit(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}

	alicePath := filepath.Join(aliceWorkspace, "profile.txt")
	bobPath := filepath.Join(bobWorkspace, "profile.txt")
	if err := os.WriteFile(alicePath, []byte("owner=alice"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bobPath, []byte("owner=bob"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")
	reader := NewReadFileTool(aliceWorkspace)
	writer := NewWriteFileTool(aliceWorkspace)
	editor := NewEditFileTool(aliceWorkspace)

	got, err := reader.Execute(ctx, map[string]interface{}{"path": "profile.txt"})
	if err != nil {
		t.Fatalf("read Alice relative file: %v", err)
	}
	if !strings.Contains(got, "owner=alice") {
		t.Fatalf("Alice read returned %q", got)
	}
	got, err = reader.Execute(ctx, map[string]interface{}{"path": alicePath})
	if err != nil || !strings.Contains(got, "owner=alice") {
		t.Fatalf("read Alice absolute file: result=%q err=%v", got, err)
	}

	_, err = reader.Execute(ctx, map[string]interface{}{"path": bobPath})
	requirePersonalWorkspaceRejection(t, err)

	_, err = writer.Execute(ctx, map[string]interface{}{
		"path":    "notes.txt",
		"content": "alice-only",
	})
	if err != nil {
		t.Fatalf("write Alice relative file: %v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(aliceWorkspace, "notes.txt")); readErr != nil || string(data) != "alice-only" {
		t.Fatalf("Alice write mismatch: data=%q err=%v", data, readErr)
	}

	_, err = writer.Execute(ctx, map[string]interface{}{
		"path":    bobPath,
		"content": "overwritten-by-alice",
	})
	requirePersonalWorkspaceRejection(t, err)
	if data, readErr := os.ReadFile(bobPath); readErr != nil || string(data) != "owner=bob" {
		t.Fatalf("Bob file changed after rejected write: data=%q err=%v", data, readErr)
	}

	_, err = editor.Execute(ctx, map[string]interface{}{
		"file_path":  "profile.txt",
		"old_string": "owner=alice",
		"new_string": "owner=alice-updated",
	})
	if err != nil {
		t.Fatalf("edit Alice relative file: %v", err)
	}
	if data, readErr := os.ReadFile(alicePath); readErr != nil || string(data) != "owner=alice-updated" {
		t.Fatalf("Alice edit mismatch: data=%q err=%v", data, readErr)
	}

	_, err = editor.Execute(ctx, map[string]interface{}{
		"file_path":  bobPath,
		"old_string": "owner=bob",
		"new_string": "owner=alice",
	})
	requirePersonalWorkspaceRejection(t, err)
	if data, readErr := os.ReadFile(bobPath); readErr != nil || string(data) != "owner=bob" {
		t.Fatalf("Bob file changed after rejected edit: data=%q err=%v", data, readErr)
	}
}

func TestPersonalAgentOSFileBoundaryCoversDiscoveryAndBatchTools(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	alicePath := filepath.Join(aliceWorkspace, "record.txt")
	bobPath := filepath.Join(bobWorkspace, "record.txt")
	bobJSONPath := filepath.Join(bobWorkspace, "record.json")
	if err := os.WriteFile(alicePath, []byte("ALICE_CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bobPath, []byte("BOB_CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bobJSONPath, []byte("{\"owner\":\"bob\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")

	listResult, err := NewListDirTool(aliceWorkspace).Execute(ctx, map[string]interface{}{"path": "."})
	if err != nil || !strings.Contains(listResult, "record.txt") {
		t.Fatalf("list Alice workspace: result=%q err=%v", listResult, err)
	}
	_, err = NewListDirTool(aliceWorkspace).Execute(ctx, map[string]interface{}{"path": bobWorkspace})
	requirePersonalWorkspaceRejection(t, err)

	globResult, err := NewGlobTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"pattern": "*.txt",
		"path":    ".",
	})
	if err != nil || !strings.Contains(globResult, "record.txt") {
		t.Fatalf("glob Alice workspace: result=%q err=%v", globResult, err)
	}
	_, err = NewGlobTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"pattern": "*.txt",
		"path":    bobWorkspace,
	})
	requirePersonalWorkspaceRejection(t, err)

	grepResult, err := NewGrepTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"pattern": "ALICE_CANARY",
		"path":    ".",
	})
	if err != nil || !strings.Contains(grepResult, "ALICE_CANARY") {
		t.Fatalf("grep Alice workspace: result=%q err=%v", grepResult, err)
	}
	_, err = NewGrepTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"pattern": "BOB_CANARY",
		"path":    bobWorkspace,
	})
	requirePersonalWorkspaceRejection(t, err)

	_, err = NewJSONPatchTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"path": bobJSONPath,
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/owner", "value": "alice"},
		},
	})
	requirePersonalWorkspaceRejection(t, err)

	_, err = NewMultiEditTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{
				"file_path":  bobPath,
				"old_string": "BOB_CANARY",
				"new_string": "ALICE_CANARY",
			},
		},
	})
	requirePersonalWorkspaceRejection(t, err)
}

func TestPersonalAgentOSFileBoundaryRejectsMismatchedToolWorkspace(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	bobPath := filepath.Join(bobWorkspace, "record.txt")
	if err := os.WriteFile(bobPath, []byte("BOB_CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")
	_, err := NewReadFileTool(bobWorkspace).Execute(ctx, map[string]interface{}{"path": "record.txt"})
	requirePersonalWorkspaceRejection(t, err)

	_, err = NewGlobTool(bobWorkspace).Execute(ctx, map[string]interface{}{"pattern": "*.txt"})
	requirePersonalWorkspaceRejection(t, err)

	_, err = NewGrepTool(bobWorkspace).Execute(ctx, map[string]interface{}{"pattern": "BOB_CANARY"})
	requirePersonalWorkspaceRejection(t, err)
}

func TestPersonalAgentOSFileBoundaryRejectsLinkEscape(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bobWorkspace, "private.txt"), []byte("BOB_CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(aliceWorkspace, "linked-bob")
	if err := os.Symlink(bobWorkspace, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable on this platform: %v", err)
	}

	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")
	_, err := NewReadFileTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"path": filepath.Join("linked-bob", "private.txt"),
	})
	requirePersonalWorkspaceRejection(t, err)

	_, err = NewWriteFileTool(aliceWorkspace).Execute(ctx, map[string]interface{}{
		"path":    filepath.Join("linked-bob", "new.txt"),
		"content": "must-not-escape",
	})
	requirePersonalWorkspaceRejection(t, err)
	if _, statErr := os.Stat(filepath.Join(bobWorkspace, "new.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("rejected link write created Bob file: %v", statErr)
	}
}

func TestPersonalAgentOSFileBoundaryKeepsLegacyAbsolutePathCompatibility(t *testing.T) {
	root := t.TempDir()
	toolWorkspace := filepath.Join(root, "workspace")
	externalWorkspace := filepath.Join(root, "external")
	if err := os.MkdirAll(toolWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(externalWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	externalPath := filepath.Join(externalWorkspace, "legacy.txt")
	if err := os.WriteFile(externalPath, []byte("legacy-absolute-path"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := NewReadFileTool(toolWorkspace).Execute(context.Background(), map[string]interface{}{
		"path": externalPath,
	})
	if err != nil {
		t.Fatalf("legacy absolute read was rejected: %v", err)
	}
	if !strings.Contains(got, "legacy-absolute-path") {
		t.Fatalf("legacy absolute read returned %q", got)
	}
}

func TestPersonalAgentOSFileBoundaryRejectsCrossUserPreMutationSnapshot(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	bobPath := filepath.Join(bobWorkspace, "record.txt")
	if err := os.WriteFile(bobPath, []byte("BOB_CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewToolRegistry()
	registry.Register(NewWriteFileTool(aliceWorkspace))
	agent := &LLMAgent{
		workspace:       aliceWorkspace,
		toolRegistry:    registry,
		snapshotManager: NewSnapshotManager(aliceWorkspace, "alice-session"),
	}
	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")
	snapshots := agent.savePreMutationSnapshot(ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    bobPath,
			"content": "attempted overwrite",
		},
	}, ctx)
	if len(snapshots) != 0 {
		t.Fatalf("cross-user path created pre-mutation snapshots: %#v", snapshots)
	}
	if got := agent.snapshotManager.ListSnapshots(); len(got) != 0 {
		t.Fatalf("snapshot manager retained Bob's preimage: %#v", got)
	}
}
