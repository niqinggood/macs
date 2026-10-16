package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellJobHTTPLogListAndStop(t *testing.T) {
	oldJobs := globalShellJobs
	globalShellJobs = NewShellJobManager()
	t.Cleanup(func() { globalShellJobs = oldJobs })

	workspace := t.TempDir()
	logDir := filepath.Join(workspace, ".openkitty", "runtime", "shell")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(".openkitty", "runtime", "shell", "job.log")
	if err := os.WriteFile(filepath.Join(workspace, logPath), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	job := globalShellJobs.Register("task-1", "go test ./...", logPath, workspace, nil)
	g := &GatewayService{config: &Config{Workspace: workspace}}

	listResp := httptest.NewRecorder()
	g.handleShellJobs(listResp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listResp.Code, listResp.Body.String())
	}
	var listBody struct {
		Jobs []ShellJob `json:"jobs"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Jobs) != 1 || listBody.Jobs[0].ID != job.ID {
		t.Fatalf("unexpected jobs: %+v", listBody.Jobs)
	}

	logResp := httptest.NewRecorder()
	g.handleShellJobLog(logResp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs/log?id="+job.ID+"&lines=1", nil))
	if logResp.Code != http.StatusOK {
		t.Fatalf("log status = %d, body = %s", logResp.Code, logResp.Body.String())
	}
	var logBody struct {
		Content string `json:"content"`
		Lines   int    `json:"lines"`
	}
	if err := json.Unmarshal(logResp.Body.Bytes(), &logBody); err != nil {
		t.Fatal(err)
	}
	if logBody.Content != "three" || logBody.Lines != 1 {
		t.Fatalf("unexpected log response: %+v", logBody)
	}

	stopResp := httptest.NewRecorder()
	g.handleShellJobStop(stopResp, httptest.NewRequest(http.MethodPost, "/api/shell/jobs/stop?id="+job.ID, strings.NewReader(`{}`)))
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body = %s", stopResp.Code, stopResp.Body.String())
	}
	stopped, ok := globalShellJobs.Get(job.ID)
	if !ok || stopped.Status != ShellJobStopped {
		t.Fatalf("job not stopped: ok=%v job=%+v", ok, stopped)
	}
}

func TestShellJobHTTPDownload(t *testing.T) {
	oldJobs := globalShellJobs
	globalShellJobs = NewShellJobManager()
	t.Cleanup(func() { globalShellJobs = oldJobs })

	workspace := t.TempDir()
	logDir := filepath.Join(workspace, ".openkitty", "runtime", "shell")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(".openkitty", "runtime", "shell", "download.log")
	if err := os.WriteFile(filepath.Join(workspace, logPath), []byte("download me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	job := globalShellJobs.Register("task-2", "npm run dev", logPath, workspace, nil)
	g := &GatewayService{config: &Config{Workspace: workspace}}

	resp := httptest.NewRecorder()
	g.handleShellJobDownload(resp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs/download?job_id="+job.ID, nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if got := strings.TrimSpace(resp.Body.String()); got != "download me" {
		t.Fatalf("download body = %q", got)
	}
	if cd := resp.Header().Get("Content-Disposition"); !strings.Contains(cd, "download.log") {
		t.Fatalf("missing download filename header: %q", cd)
	}
}

func TestShellJobHTTPRejectsLogOutsideRuntime(t *testing.T) {
	oldJobs := globalShellJobs
	globalShellJobs = NewShellJobManager()
	t.Cleanup(func() { globalShellJobs = oldJobs })

	workspace := t.TempDir()
	outside := filepath.Join(workspace, "outside.log")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	job := globalShellJobs.Register("task-3", "type outside.log", outside, workspace, nil)
	g := &GatewayService{config: &Config{Workspace: workspace}}

	resp := httptest.NewRecorder()
	g.handleShellJobLog(resp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs/log?id="+job.ID, nil))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestLegacyShellJobHTTPHidesAgentOSOwnedJobs(t *testing.T) {
	oldJobs := globalShellJobs
	globalShellJobs = NewShellJobManager()
	t.Cleanup(func() { globalShellJobs = oldJobs })

	workspace := t.TempDir()
	logDir := filepath.Join(workspace, ".openkitty", "runtime", "shell")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyLog := filepath.Join(".openkitty", "runtime", "shell", "legacy.log")
	privateLog := filepath.Join(".openkitty", "runtime", "shell", "private.log")
	if err := os.WriteFile(filepath.Join(workspace, legacyLog), []byte("legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, privateLog), []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacy := globalShellJobs.Register("legacy", "echo legacy", legacyLog, workspace, nil)
	agentOSCtx := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "agent-service", TenantScoped: true, Service: true,
	})
	private, err := globalShellJobs.RegisterForContext(agentOSCtx, "agentos", "echo private", privateLog, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{config: &Config{Workspace: workspace}}

	listResp := httptest.NewRecorder()
	g.handleShellJobs(listResp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listResp.Code, listResp.Body.String())
	}
	var listBody struct {
		Jobs []ShellJob `json:"jobs"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Jobs) != 1 || listBody.Jobs[0].ID != legacy.ID {
		t.Fatalf("legacy API leaked AgentOS job: %+v", listBody.Jobs)
	}

	logResp := httptest.NewRecorder()
	g.handleShellJobLog(logResp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs/log?id="+private.ID, nil))
	if logResp.Code != http.StatusNotFound {
		t.Fatalf("private log status = %d, body = %s", logResp.Code, logResp.Body.String())
	}
	downloadResp := httptest.NewRecorder()
	g.handleShellJobDownload(downloadResp, httptest.NewRequest(http.MethodGet, "/api/shell/jobs/download?id="+private.ID, nil))
	if downloadResp.Code != http.StatusNotFound {
		t.Fatalf("private download status = %d, body = %s", downloadResp.Code, downloadResp.Body.String())
	}
	stopResp := httptest.NewRecorder()
	g.handleShellJobStop(stopResp, httptest.NewRequest(http.MethodPost, "/api/shell/jobs/stop?id="+private.ID, strings.NewReader(`{}`)))
	if stopResp.Code != http.StatusNotFound {
		t.Fatalf("private stop status = %d, body = %s", stopResp.Code, stopResp.Body.String())
	}
	if got, ok := globalShellJobs.GetForContext(agentOSCtx, private.ID); !ok || got.Status != ShellJobRunning {
		t.Fatalf("legacy API changed hidden job: ok=%v job=%+v", ok, got)
	}
}
