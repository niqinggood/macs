package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedMemoryBaseDirUsesWorkspace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "edge-workspace")

	tests := []struct {
		name       string
		runtimeDir string
		configured string
		want       string
	}{
		{
			name:       "default",
			configured: "",
			want:       filepath.Join(workspace, ".openkitty", "memories"),
		},
		{
			name:       "legacy default",
			configured: "./memory",
			want:       filepath.Join(workspace, ".openkitty", "memories"),
		},
		{
			name:       "configured relative path",
			configured: filepath.Join("state", "memories"),
			want:       filepath.Join(workspace, "state", "memories"),
		},
		{
			name:       "runtime directory",
			runtimeDir: ".runtime",
			want:       filepath.Join(workspace, ".runtime", ".openkitty", "memories"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolvedMemoryBaseDir(workspace, test.runtimeDir, test.configured); got != test.want {
				t.Fatalf("resolvedMemoryBaseDir() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInitWorkspaceUsesConfiguredWorkspaceOutsideWorkingDirectory(t *testing.T) {
	previousEventLog := globalEventLog
	globalEventLog = nil
	t.Cleanup(func() {
		globalEventLog = previousEventLog
	})

	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	workspace := filepath.Join(t.TempDir(), "edge-workspace")
	config := &Config{Workspace: workspace}
	config.MemoryConfig.BaseDir = filepath.Join(".openkitty", "memories")

	if err := initWorkspace(config); err != nil {
		t.Fatal(err)
	}

	for _, relativePath := range []string{
		filepath.Join(".openkitty", "storage"),
		filepath.Join(".openkitty", "storage", ".system"),
		filepath.Join(".openkitty", "memories"),
		filepath.Join(".openkitty", "skills"),
		filepath.Join(".openkitty", "logs"),
	} {
		if info, err := os.Stat(filepath.Join(workspace, relativePath)); err != nil || !info.IsDir() {
			t.Fatalf("workspace directory %q was not created: %v", relativePath, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, ".openkitty")); !os.IsNotExist(err) {
		t.Fatalf("initWorkspace wrote to process working directory: %v", err)
	}
}
