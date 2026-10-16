package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requirePTCPython(t *testing.T) {
	t.Helper()
	python, _ := resolvePythonCommand()
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("PTC Python runtime unavailable: %v", err)
	}
}

func newPTCExecutionTestTool(t *testing.T, workspace string, runtime ptcRuntimeMode) *PTCExecuteTool {
	t.Helper()
	reg := NewToolRegistry()
	reg.Register(ptcRequiredParamTool{})
	reg.Register(registryNameMockTool{name: "cli"})
	server := NewPTCServer(reg)
	t.Cleanup(server.Stop)
	return &PTCExecuteTool{
		server: server, workspace: workspace,
		fileLogger: log.New(io.Discard, "", 0), runtimeOverride: runtime,
	}
}

func TestPTCGuardedRuntimeScrubsCredentialsWithoutChangingCompatDefault(t *testing.T) {
	requirePTCPython(t)
	workspace := t.TempDir()
	t.Setenv("OPENKITTY_PTC_TEST_SECRET", "compat-secret-8472")

	compat := newPTCExecutionTestTool(t, workspace, ptcRuntimeCompat)
	compatOutput, err := compat.Execute(context.Background(), map[string]interface{}{
		"script": `import os
print("secret=" + str(os.getenv("OPENKITTY_PTC_TEST_SECRET", "<missing>")))
print("cwd=" + os.getcwd())`,
	})
	if err != nil {
		t.Fatalf("compat execution: %v", err)
	}
	if !strings.Contains(compatOutput, "secret=compat-secret-8472") {
		t.Fatalf("compat runtime no longer preserves explicitly configured environment:\n%s", compatOutput)
	}
	if !strings.Contains(compatOutput, workspace) {
		t.Fatalf("compat runtime cwd changed:\n%s", compatOutput)
	}

	guarded := newPTCExecutionTestTool(t, workspace, ptcRuntimeGuarded)
	guardedOutput, err := guarded.Execute(context.Background(), map[string]interface{}{
		"script": `import os
print("secret=" + str(os.getenv("OPENKITTY_PTC_TEST_SECRET", "<missing>")))
print("cwd=" + os.getcwd())`,
	})
	if err != nil {
		t.Fatalf("guarded execution: %v", err)
	}
	if !strings.Contains(guardedOutput, "secret=<missing>") || strings.Contains(guardedOutput, "compat-secret-8472") {
		t.Fatalf("guarded runtime exposed inherited credential:\n%s", guardedOutput)
	}
	if !strings.Contains(guardedOutput, "PTC runtime: guarded") {
		t.Fatalf("guarded runtime was not observable in its receipt:\n%s", guardedOutput)
	}
	if strings.Contains(guardedOutput, "cwd="+workspace) {
		t.Fatalf("guarded runtime reused workspace cwd:\n%s", guardedOutput)
	}
	if _, err := os.Stat(filepath.Join(workspace, "ptc_client.py")); !os.IsNotExist(err) {
		t.Fatalf("generated helper leaked into workspace: %v", err)
	}
}

func TestPTCRuntimeRejectsUnknownHostConfiguration(t *testing.T) {
	tool := newPTCExecutionTestTool(t, t.TempDir(), ptcRuntimeMode("guardded"))
	_, err := tool.Execute(context.Background(), map[string]interface{}{"script": `print("should not run")`})
	if err == nil || !strings.Contains(err.Error(), "invalid OPENKITTY_PTC_RUNTIME") {
		t.Fatalf("unknown runtime configuration was not rejected: %v", err)
	}
	if tool.server.Port() != 0 {
		t.Fatal("PTC server started before invalid runtime configuration was rejected")
	}
}

func TestPTCGuardedRuntimeBlocksDirectPrivilegesButKeepsRegistryRPC(t *testing.T) {
	requirePTCPython(t)
	workspace := t.TempDir()
	secretPath := filepath.Join(workspace, "workspace-secret.txt")
	if err := os.WriteFile(secretPath, []byte("must-not-be-read-directly"), 0600); err != nil {
		t.Fatal(err)
	}

	tool := newPTCExecutionTestTool(t, workspace, ptcRuntimeGuarded)
	script := fmt.Sprintf(`
import socket, subprocess, sys

checks = [
    ("file", lambda: open(%q, "r").read()),
    ("process", lambda: subprocess.run([sys.executable, "-c", "print('child')"], check=True)),
    ("network", lambda: socket.create_connection(("127.0.0.1", 1), timeout=0.2)),
]
for name, operation in checks:
    try:
        operation()
        print(name + "=UNEXPECTED_SUCCESS")
    except Exception as exc:
        print(name + "=" + type(exc).__name__ + ":" + str(exc))

print("cli_wrapper=" + str(hasattr(ptc_client, "cli")))
print("rpc=" + ptc_client.read_file(path="allowed-through-registry.txt"))
`, secretPath)
	output, err := tool.Execute(context.Background(), map[string]interface{}{"script": script})
	if err != nil {
		t.Fatalf("guarded execution: %v", err)
	}
	for _, want := range []string{
		"file=PermissionError:guarded PTC blocked open",
		"process=PermissionError:guarded PTC blocked subprocess.Popen",
		"network=PermissionError:guarded PTC blocked socket.",
		"cli_wrapper=False",
		"rpc=",
		"read:allowed-through-registry.txt",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("guarded output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "UNEXPECTED_SUCCESS") || strings.Contains(output, "must-not-be-read-directly") {
		t.Fatalf("guarded runtime allowed a direct privilege:\n%s", output)
	}
}

func TestPTCGuardedStateIsTaskScopedAndCompatStateStillWorks(t *testing.T) {
	requirePTCPython(t)
	script := `counter = globals().get("counter", 0) + 1
print("counter=" + str(counter))`

	compat := newPTCExecutionTestTool(t, t.TempDir(), ptcRuntimeCompat)
	for index, want := range []string{"counter=1", "counter=2"} {
		output, err := compat.Execute(context.Background(), map[string]interface{}{"script": script, "mode": "stateful"})
		if err != nil {
			t.Fatalf("compat state call %d: %v", index+1, err)
		}
		if !strings.Contains(output, want) || strings.Contains(output, "state save failed") {
			t.Fatalf("compat state call %d missing %q:\n%s", index+1, want, output)
		}
	}

	guarded := newPTCExecutionTestTool(t, t.TempDir(), ptcRuntimeGuarded)
	ctxA := context.WithValue(context.Background(), "task_id", "guarded-task-a")
	ctxB := context.WithValue(context.Background(), "task_id", "guarded-task-b")
	for index, tc := range []struct {
		ctx  context.Context
		want string
	}{{ctxA, "counter=1"}, {ctxA, "counter=2"}, {ctxB, "counter=1"}} {
		output, err := guarded.Execute(tc.ctx, map[string]interface{}{"script": script, "mode": "stateful"})
		if err != nil {
			t.Fatalf("guarded state call %d: %v", index+1, err)
		}
		if !strings.Contains(output, tc.want) || strings.Contains(output, "state save failed") {
			t.Fatalf("guarded state call %d missing %q:\n%s", index+1, tc.want, output)
		}
	}

	stateA, scopedA := guarded.guardedStateFile(ctxA, "unused")
	stateB, scopedB := guarded.guardedStateFile(ctxB, "unused")
	if !scopedA || !scopedB || stateA == stateB {
		t.Fatalf("guarded task state was not isolated: %q / %q", stateA, stateB)
	}
	for _, path := range []string{stateA, stateB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("guarded state file was not persisted for the task: %v", err)
		}
	}
}
