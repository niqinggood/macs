package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workspacetools "mac/internal/tools/workspace"
)

type FileHistoryManager = workspacetools.FileHistoryManager

var NewFileHistoryManager = workspacetools.NewFileHistoryManager
var withFileHistoryManager = workspacetools.WithFileHistoryManager
var SetGlobalFileHistory = workspacetools.SetGlobalFileHistory
var PreEditBackup = workspacetools.PreEditBackup

func TestRunScopedFileHistoryIgnoresMostRecentlyCreatedAgent(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	pathA := filepath.Join(workspaceA, "shared-name.txt")
	pathB := filepath.Join(workspaceB, "shared-name.txt")
	if err := os.WriteFile(pathA, []byte("owner-a-before"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("owner-b-before"), 0644); err != nil {
		t.Fatal(err)
	}

	managerA := NewFileHistoryManager(workspaceA, "run-a")
	managerB := NewFileHistoryManager(workspaceB, "run-b")
	toolA := NewWriteFileTool(workspaceA)
	toolB := NewWriteFileTool(workspaceB)

	aContextReady := make(chan context.Context, 1)
	releaseA := make(chan struct{})
	aResult := make(chan error, 1)

	go func() {
		ctxA := withFileHistoryManager(context.Background(), managerA)
		// This used to make A the process-global owner.
		SetGlobalFileHistory(managerA)
		aContextReady <- ctxA
		<-releaseA
		_, err := toolA.Execute(ctxA, map[string]interface{}{
			"path":    pathA,
			"content": "owner-a-after",
		})
		aResult <- err
	}()

	// A's run context exists before B is created. B then becomes the most
	// recently initialized agent and completes its own edit before A resumes.
	// The old global implementation deterministically backed A's file up into
	// managerB at this point.
	<-aContextReady
	ctxB := withFileHistoryManager(context.Background(), managerB)
	SetGlobalFileHistory(managerB)
	if _, err := toolB.Execute(ctxB, map[string]interface{}{
		"path":    pathB,
		"content": "owner-b-after",
	}); err != nil {
		t.Fatalf("B write: %v", err)
	}
	close(releaseA)
	if err := <-aResult; err != nil {
		t.Fatalf("A write: %v", err)
	}

	assertSingleFileHistoryBackup(t, managerA, pathA, "owner-a-before")
	assertSingleFileHistoryBackup(t, managerB, pathB, "owner-b-before")
	if history := managerB.GetHistory(pathA); len(history) != 0 {
		t.Fatalf("A history leaked into B manager: %#v", history)
	}
	if history := managerA.GetHistory(pathB); len(history) != 0 {
		t.Fatalf("B history leaked into A manager: %#v", history)
	}
}

func TestPreEditBackupWithoutRunManagerIsNoOp(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "legacy.txt")
	if err := os.WriteFile(path, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}

	manager := NewFileHistoryManager(workspace, "run")
	SetGlobalFileHistory(manager)
	PreEditBackup(path)

	if history := manager.GetHistory(path); len(history) != 0 {
		t.Fatalf("context-free backup selected a manager: %#v", history)
	}
}

func assertSingleFileHistoryBackup(t *testing.T, manager *FileHistoryManager, path, want string) {
	t.Helper()
	history := manager.GetHistory(path)
	if len(history) != 1 {
		t.Fatalf("history for %s = %d entries, want 1", path, len(history))
	}
	data, err := os.ReadFile(history[0].BackupPath)
	if err != nil {
		t.Fatalf("read backup for %s: %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("backup for %s = %q, want %q", path, got, want)
	}
}
