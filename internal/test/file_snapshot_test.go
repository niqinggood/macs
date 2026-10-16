package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacetools "mac/internal/tools/workspace"
)

type FileSnapshot = workspacetools.FileSnapshot
type SnapshotManager = workspacetools.SnapshotManager

var NewSnapshotManager = workspacetools.NewSnapshotManager
var NewUndoSnapshotTool = workspacetools.NewUndoSnapshotTool

func TestSnapshotManagerSaveAndRestore(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	// Create a test file
	testFile := filepath.Join(dir, "test.txt")
	original := []byte("original content")
	if err := os.WriteFile(testFile, original, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Take snapshot before "edit"
	snap, err := sm.SaveSnapshot(testFile, "edit_file")
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.ToolName != "edit_file" {
		t.Errorf("expected edit_file, got %s", snap.ToolName)
	}

	// Modify the file
	modified := []byte("modified content")
	if err := os.WriteFile(testFile, modified, 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	// Restore from snapshot
	if err := sm.RestoreSnapshot(testFile); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}

	// Verify restored
	restored, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(restored) != string(original) {
		t.Errorf("restored content mismatch: got %q, want %q", restored, original)
	}
}

func TestSnapshotManagerNewFile(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	path := filepath.Join(dir, "nonexistent.txt")
	snap, err := sm.SaveSnapshot(path, "write_file")
	if err != nil {
		t.Fatalf("save snapshot for new file: %v", err)
	}
	if snap == nil || snap.Existed {
		t.Fatalf("expected a creation tombstone, got %+v", snap)
	}
	if err := os.WriteFile(path, []byte("created"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	sm.FinalizeSnapshots([]*FileSnapshot{snap})
	if err := sm.RestoreSnapshot(path); err != nil {
		t.Fatalf("restore new-file snapshot: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("restoring a creation tombstone should remove the file, stat err=%v", err)
	}
}

func TestSnapshotManagerListAndCleanup(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	// Create and snapshot multiple files
	for i, name := range []string{"a.txt", "b.txt", "c.txt"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("content"), 0644)
		snap, err := sm.SaveSnapshot(path, "write_file")
		if err != nil || snap == nil {
			t.Fatalf("snapshot %d: err=%v snap=%v", i, err, snap)
		}
	}

	list := sm.ListSnapshots()
	if len(list) != 3 {
		t.Errorf("expected 3 snapshots, got %d", len(list))
	}

	last := sm.LastSnapshot()
	if last == nil || last.OriginalPath != filepath.Join(dir, "c.txt") {
		t.Error("LastSnapshot should be c.txt")
	}

	// Cleanup
	sm.Cleanup()
	list = sm.ListSnapshots()
	if len(list) != 0 {
		t.Errorf("after cleanup: expected 0 snapshots, got %d", len(list))
	}
}

func TestSnapshotManagerSummary(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	summary := sm.SnapshotSummary()
	if summary != "No snapshots taken this session." {
		t.Error("empty summary should indicate no snapshots")
	}

	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("data"), 0644)
	sm.SaveSnapshot(path, "write_file")

	summary = sm.SnapshotSummary()
	if summary == "No snapshots taken this session." {
		t.Error("summary should show snapshot after save")
	}
}

func TestSnapshotManagerDiff(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3"), 0644)
	sm.SaveSnapshot(path, "edit_file")
	os.WriteFile(path, []byte("line1\nline2 modified\nline3\nline4"), 0644)

	diff, err := sm.DiffSnapshot(path)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff == "" || !strings.Contains(diff, "changed") {
		t.Errorf("diff should show changes, got: %s", diff)
	}
}

func TestSnapshotManagerBatchRestore(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")
	for i, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(fmt.Sprintf("v1-%d", i)), 0644)
		sm.SaveSnapshot(path, "write_file")
		os.WriteFile(path, []byte(fmt.Sprintf("v2-%d", i)), 0644)
	}
	count, err := sm.BatchRestore()
	if err != nil {
		t.Fatalf("batch restore: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 restores, got %d", count)
	}
}

func TestSnapshotManagerFinalizeDropsNoOpAndKeepsPartialMutation(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)

	noOp, err := sm.SaveSnapshot(path, "edit_file")
	if err != nil {
		t.Fatalf("save no-op snapshot: %v", err)
	}
	sm.FinalizeSnapshots([]*FileSnapshot{noOp})
	if got := len(sm.ListSnapshots()); got != 0 {
		t.Fatalf("no-op snapshot should be discarded, got %d", got)
	}

	changed, err := sm.SaveSnapshot(path, "edit_file")
	if err != nil {
		t.Fatalf("save changed snapshot: %v", err)
	}
	os.WriteFile(path, []byte("partially changed"), 0644)
	sm.FinalizeSnapshots([]*FileSnapshot{changed})
	if got := len(sm.ListSnapshots()); got != 1 {
		t.Fatalf("changed snapshot should be retained, got %d", got)
	}
}

func TestAgentSnapshotsEveryPathDeclaredByMultiEdit(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	created := filepath.Join(dir, "created.txt")
	os.WriteFile(existing, []byte("before"), 0644)

	registry := NewToolRegistry()
	registry.Register(NewMultiEditTool(dir))
	agent := &LLMAgent{
		workspace:       dir,
		toolRegistry:    registry,
		snapshotManager: NewSnapshotManager(dir, "test-session"),
	}
	snapshots := agent.savePreMutationSnapshot(ToolCall{
		Name: "multiedit",
		Arguments: map[string]interface{}{
			"edits": []interface{}{
				map[string]interface{}{"file_path": "existing.txt", "old_string": "before", "new_string": "after"},
				map[string]interface{}{"file_path": "created.txt", "old_string": "", "new_string": "new"},
			},
		},
	})
	if len(snapshots) != 2 {
		t.Fatalf("multi-edit snapshots = %d, want 2", len(snapshots))
	}
	byPath := make(map[string]*FileSnapshot)
	for _, snapshot := range snapshots {
		byPath[snapshot.OriginalPath] = snapshot
	}
	if byPath[existing] == nil || !byPath[existing].Existed {
		t.Fatalf("existing preimage missing: %+v", byPath)
	}
	if byPath[created] == nil || byPath[created].Existed {
		t.Fatalf("creation tombstone missing: %+v", byPath)
	}
}

func TestSnapshotManagerBatchRestoreUsesEarliestPreimage(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v0"), 0644)
	sm.SaveSnapshot(path, "edit_file")
	os.WriteFile(path, []byte("v1"), 0644)
	sm.SaveSnapshot(path, "edit_file")
	os.WriteFile(path, []byte("v2"), 0644)

	count, err := sm.BatchRestore()
	if err != nil {
		t.Fatalf("batch restore: %v", err)
	}
	if count != 1 {
		t.Fatalf("batch restore count = %d", count)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v0" {
		t.Fatalf("batch restore should use earliest preimage, got %q", data)
	}
}

func TestSnapshotManagerPersistsCheckpointsAcrossSessionResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("before"), 0644)

	first := NewSnapshotManager(dir, "resume-session")
	snapshot, err := first.SaveSnapshot(path, "edit_file")
	if err != nil {
		t.Fatalf("save persistent snapshot: %v", err)
	}
	os.WriteFile(path, []byte("after"), 0644)
	first.FinalizeSnapshots([]*FileSnapshot{snapshot})
	first.EndTurn()

	resumed := NewSnapshotManager(dir, "resume-session")
	if got := len(resumed.ListSnapshots()); got != 1 {
		t.Fatalf("resumed snapshots = %d, want 1", got)
	}
	if turns := resumed.ListTurns(); len(turns) != 1 || turns[0].FileCount != 1 {
		t.Fatalf("resumed turns = %+v", turns)
	}
	if err := resumed.RestoreSnapshot(path); err != nil {
		t.Fatalf("restore resumed snapshot: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "before" {
		t.Fatalf("restored content = %q", data)
	}
}

func TestSnapshotManagerExport(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("data"), 0644)
	sm.SaveSnapshot(path, "write_file")

	exportDir := filepath.Join(dir, "export")
	if err := sm.ExportSnapshots(exportDir); err != nil {
		t.Fatalf("export: %v", err)
	}
	entries, _ := os.ReadDir(exportDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 exported file, got %d", len(entries))
	}
}

func TestSnapshotManagerDoubleRestore(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("v1"), 0644)
	sm.SaveSnapshot(path, "write_file")
	os.WriteFile(path, []byte("v2"), 0644)

	// First restore
	if err := sm.RestoreSnapshot(path); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "v1" {
		t.Errorf("after first restore: got %q, want %q", data, "v1")
	}

	// Second restore should fail (already restored)
	if err := sm.RestoreSnapshot(path); err == nil {
		t.Error("second restore should fail - snapshot already used")
	}
}

func TestSnapshotEndTurnAndRewind(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "test-session")

	testPath := filepath.Join(dir, "test.go")
	otherPath := filepath.Join(dir, "other.txt")
	os.WriteFile(testPath, []byte("v0"), 0644)
	sm.EndTurn() // no changes, should be empty

	// Turn 1: edit an existing file.
	sm.SaveSnapshot(testPath, "edit_file")
	os.WriteFile(testPath, []byte("v1"), 0644)
	sm.EndTurn()

	// Turn 2: create another file.
	sm.SaveSnapshot(otherPath, "write_file")
	os.WriteFile(otherPath, []byte("hello"), 0644)
	sm.EndTurn()

	// List turns
	turns := sm.ListTurns()
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Turn != 1 || turns[0].FileCount != 1 {
		t.Errorf("turn 0: got turn=%d files=%d", turns[0].Turn, turns[0].FileCount)
	}

	// Rewind to the start of turn 1: restore the edit and delete the file
	// created in turn 2.
	count, err := sm.RewindToTurn(1)
	if err != nil {
		t.Fatalf("rewind failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 files restored, got %d", count)
	}

	data, _ := os.ReadFile(testPath)
	if string(data) != "v0" {
		t.Errorf("expected v0 after rewind, got %s", data)
	}
	if _, err := os.Stat(otherPath); !os.IsNotExist(err) {
		t.Fatalf("rewind should remove a later-created file, stat err=%v", err)
	}
	if got := sm.CurrentTurn(); got != 1 {
		t.Fatalf("rewind should branch history at turn 1, current turn=%d", got)
	}
}
