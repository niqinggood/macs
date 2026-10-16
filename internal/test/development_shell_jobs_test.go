package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operationstools "mac/internal/tools/operations"
)

func TestShellJobManagerTracksJobLifecycle(t *testing.T) {
	mgr := NewShellJobManager()
	job := mgr.Register("task1", "npm run dev", "runtime.log", "/tmp/project", nil)

	jobs := mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %d", len(jobs))
	}
	if jobs[0].ID != job.ID || jobs[0].Status != ShellJobRunning || jobs[0].PID != 0 {
		t.Fatalf("unexpected job snapshot: %#v", jobs[0])
	}

	mgr.Finish(job.ID, nil)
	got, ok := mgr.Get(job.ID)
	if !ok {
		t.Fatal("expected job to remain available after finish")
	}
	if got.Status != ShellJobExited || got.EndedAt == nil {
		t.Fatalf("expected exited job with end time, got %#v", got)
	}
}

func TestShellSessionsToolReadsJobLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, ".openkitty", "runtime", "shell", "job.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewShellJobManager()
	job := mgr.Register("task1", "npm run dev", logPath, dir, nil)
	tool := operationstools.NewShellSessionsTool(dir, mgr, nil)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":    "log",
		"job_id":    job.ID,
		"max_lines": float64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "two\nthree") || strings.Contains(out, "one\n") {
		t.Fatalf("expected tailed log output, got:\n%s", out)
	}
}

func TestShellSessionsToolStopsMissingJob(t *testing.T) {
	tool := operationstools.NewShellSessionsTool(t.TempDir(), NewShellJobManager(), nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "stop",
		"job_id": "missing",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing job error, got %v", err)
	}
}

func TestShellSessionsIsCoreTool(t *testing.T) {
	if !CoreTools["shell_sessions"] {
		t.Fatal("shell_sessions should be a core tool")
	}
}

func TestShellJobManagerIsolatesTenantServiceOwners(t *testing.T) {
	mgr := NewShellJobManager()
	tenantAOwner := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "service-one", TenantScoped: true, Service: true,
	})
	tenantAPeer := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "service-two", TenantScoped: true, Service: true,
	})
	tenantBOwner := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-b", ServiceID: "service-one", TenantScoped: true, Service: true,
	})
	controlPlane := withExecutionIdentity(context.Background(), ExecutionIdentity{ControlPlane: true})

	legacy := mgr.Register("legacy", "echo legacy", "legacy.log", ".", nil)
	owned, err := mgr.RegisterForContext(tenantAOwner, "agentos-a", "echo private", "private.log", ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, err := mgr.RegisterForContext(tenantBOwner, "agentos-b", "echo other", "other.log", ".", nil)
	if err != nil {
		t.Fatal(err)
	}

	if jobs := mgr.List(); len(jobs) != 1 || jobs[0].ID != legacy.ID {
		t.Fatalf("legacy caller should only see legacy jobs: %+v", jobs)
	}
	if jobs := mgr.ListForContext(tenantAOwner); len(jobs) != 1 || jobs[0].ID != owned.ID {
		t.Fatalf("owner should only see its own tenant/service jobs: %+v", jobs)
	}
	if _, ok := mgr.GetForContext(tenantAPeer, owned.ID); ok {
		t.Fatal("same-tenant peer service read another service's job")
	}
	if _, ok := mgr.GetForContext(tenantBOwner, owned.ID); ok {
		t.Fatal("cross-tenant service read another tenant's job")
	}
	if err := mgr.StopForContext(tenantAPeer, owned.ID); err == nil {
		t.Fatal("same-tenant peer service stopped another service's job")
	}
	if jobs := mgr.ListForContext(controlPlane); len(jobs) != 3 {
		t.Fatalf("control plane should see all jobs, got %+v", jobs)
	}
	if _, ok := mgr.GetForContext(controlPlane, otherTenant.ID); !ok {
		t.Fatal("control plane could not read tenant-owned job")
	}
	if err := mgr.StopForContext(tenantAOwner, owned.ID); err != nil {
		t.Fatalf("owner could not stop its job: %v", err)
	}
	if stopped, ok := mgr.GetForContext(tenantAOwner, owned.ID); !ok || stopped.Status != ShellJobStopped {
		t.Fatalf("owner did not observe stopped job: ok=%v job=%+v", ok, stopped)
	}
}

func TestShellJobManagerRejectsTenantScopedResourceWithoutOwner(t *testing.T) {
	mgr := NewShellJobManager()
	ctx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", TenantScoped: true, Service: true,
	})
	if _, err := mgr.RegisterForContext(ctx, "agentos", "echo private", "private.log", ".", nil); err == nil {
		t.Fatal("tenant-scoped job without a service/user owner should fail closed")
	}
	if jobs := mgr.List(); len(jobs) != 0 {
		t.Fatalf("failed registration created a legacy-visible job: %+v", jobs)
	}
}

func TestShellSessionsToolEnforcesJobOwnerForListLogAndStop(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, ".openkitty", "runtime", "shell", "owned.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("tenant secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ownerCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "owner", TenantScoped: true, Service: true,
	})
	peerCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "peer", TenantScoped: true, Service: true,
	})
	mgr := NewShellJobManager()
	job, err := mgr.RegisterForContext(ownerCtx, "agentos-task", "echo secret", logPath, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := operationstools.NewShellSessionsTool(dir, mgr, nil)

	listed, err := tool.Execute(peerCtx, map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed, job.ID) || strings.Contains(listed, "tenant secret") {
		t.Fatalf("peer list leaked owner job: %s", listed)
	}
	if _, err := tool.Execute(peerCtx, map[string]interface{}{
		"action": "log", "job_id": job.ID,
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("peer log should fail closed as not found, got %v", err)
	}
	if _, err := tool.Execute(peerCtx, map[string]interface{}{
		"action": "stop", "job_id": job.ID,
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("peer stop should fail closed as not found, got %v", err)
	}
	ownerLog, err := tool.Execute(ownerCtx, map[string]interface{}{
		"action": "log", "job_id": job.ID,
	})
	if err != nil || !strings.Contains(ownerLog, "tenant secret") {
		t.Fatalf("owner could not read log: err=%v output=%s", err, ownerLog)
	}
}
