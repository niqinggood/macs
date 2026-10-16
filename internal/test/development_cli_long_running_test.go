package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCLILongRunningCommandRequiresBackground(t *testing.T) {
	tool := NewCLITool(t.TempDir())
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "go run ./cmd/server",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"class: long_running", "status: not_started", "background=true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestCLILocalHTTPServerRequiresBackground(t *testing.T) {
	tool := NewCLITool(t.TempDir())
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "python -m http.server 8765 --bind 127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"class: long_running", "status: not_started", "background=true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestCLIParametersExposeBackgroundFlag(t *testing.T) {
	params := NewCLITool(".").Parameters()
	props := params["properties"].(map[string]interface{})
	background, ok := props["background"].(map[string]interface{})
	if !ok {
		t.Fatal("cli schema should expose background")
	}
	if background["type"] != "boolean" {
		t.Fatalf("background should be boolean, got %#v", background["type"])
	}
}

func TestCLIExplicitBackgroundIsHonoredForUnclassifiedWorkspaceCommand(t *testing.T) {
	tool := NewCLITool(t.TempDir())
	out, err := tool.Execute(context.WithValue(context.Background(), "task_id", "background-test"), map[string]interface{}{
		"command":    "echo openkitty-background-test",
		"background": true,
	})
	if err != nil {
		t.Fatalf("background command failed: %v", err)
	}
	for _, want := range []string{"status: background_started", "job_id:", "pid:", "shell_sessions"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs := globalShellJobs.List()
		for _, job := range jobs {
			if job.TaskID == "background-test" && job.Status != ShellJobRunning {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("managed background command did not exit")
}

func TestCLIBackgroundJobCapturesSealedExecutionOwner(t *testing.T) {
	oldJobs := globalShellJobs
	globalShellJobs = NewShellJobManager()
	t.Cleanup(func() { globalShellJobs = oldJobs })

	ownerCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "agent-service", TenantScoped: true, Service: true,
	})
	ownerCtx = context.WithValue(ownerCtx, "task_id", "agentos-background-test")
	tool := NewCLITool(t.TempDir())
	out, err := tool.Execute(ownerCtx, map[string]interface{}{
		"command":    "echo tenant-owned-background-test",
		"background": true,
	})
	if err != nil {
		t.Fatalf("background command failed: %v", err)
	}
	if !strings.Contains(out, "status: background_started") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if jobs := globalShellJobs.List(); len(jobs) != 0 {
		t.Fatalf("legacy view leaked tenant-owned background job: %+v", jobs)
	}
	jobs := globalShellJobs.ListForContext(ownerCtx)
	if len(jobs) != 1 || jobs[0].TaskID != "agentos-background-test" {
		t.Fatalf("owner view did not contain background job: %+v", jobs)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs = globalShellJobs.ListForContext(ownerCtx)
		if len(jobs) == 1 && jobs[0].Status != ShellJobRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("tenant-owned background command did not release its log before cleanup")
}
