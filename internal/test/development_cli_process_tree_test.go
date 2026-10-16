//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLITimeoutTerminatesDescendantTree(t *testing.T) {
	if !GetTerminalManager().IsGitBashAvailable() {
		t.Skip("Git Bash is not installed")
	}

	workspace := t.TempDir()
	marker := filepath.Join(workspace, "orphan-was-alive.txt")
	markerForBash := strings.ReplaceAll(filepath.ToSlash(marker), `"`, `\"`)
	command := fmt.Sprintf(
		`sh -c '(sleep 2; printf leaked > "%s") & wait'`,
		markerForBash,
	)

	cli := NewCLIToolWithSafety(workspace, false)
	startedAt := time.Now()
	output, err := cli.Execute(context.Background(), map[string]interface{}{
		"command": command,
		"timeout": float64(1),
	})
	elapsed := time.Since(startedAt)
	if err == nil {
		t.Fatalf("expected deadline error, got success:\n%s", output)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timeout did not bound process-tree wait: %v\n%s", elapsed, output)
	}
	for _, want := range []string{
		"status: timeout",
		"work_dir: " + workspace,
		"duration:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("timeout observation missing %q:\n%s", want, output)
		}
	}

	// The descendant would create this file after two seconds if only the
	// outer Git Bash process were killed. Give it enough time to prove that the
	// complete tree was terminated.
	time.Sleep(2500 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("descendant survived the timeout and wrote %s", marker)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("cannot inspect descendant marker: %v", statErr)
	}
}
