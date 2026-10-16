package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type ptcRequiredParamTool struct{}

func (ptcRequiredParamTool) Name() string        { return "read_file" }
func (ptcRequiredParamTool) Description() string { return "mock PTC required-param tool" }
func (ptcRequiredParamTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
		"required": []string{"path"},
	}
}
func (ptcRequiredParamTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "read:" + params["path"].(string), nil
}

func TestPTCTruncationPreservesStructuredReceiptAtTail(t *testing.T) {
	receipt := `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":2529}`
	input := strings.Repeat("diagnostic-noise-", 5000) + "\n" + receipt
	got := truncatePTCTextPreservingTail(input, ptcMaxStdoutBytes, "stdout")
	if len(got) > ptcMaxStdoutBytes {
		t.Fatalf("truncated output length = %d, max %d", len(got), ptcMaxStdoutBytes)
	}
	if !strings.Contains(got, receipt) {
		t.Fatalf("tail receipt was lost: %s", got[len(got)-200:])
	}
	if !strings.Contains(got, "tail preserved") {
		t.Fatal("truncation marker did not explain head/tail preservation")
	}
}

func TestPTCHandleListToolsReturnsSortedAllowedTools(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "write_file"})
	reg.Register(registryNameMockTool{name: "not_allowed"})
	reg.Register(registryNameMockTool{name: "cli"})
	reg.Register(registryNameMockTool{name: "read_file"})

	server := NewPTCServer(reg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	req.Header.Set("X-PTC-Token", server.Token())
	rr := httptest.NewRecorder()

	server.handleListTools(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []string{"cli", "read_file", "write_file"}
	if !reflect.DeepEqual(payload.Tools, want) {
		t.Fatalf("tools = %v, want %v", payload.Tools, want)
	}
}

func TestPTCHandleListToolsRejectsUnauthorized(t *testing.T) {
	server := NewPTCServer(NewToolRegistry())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	rr := httptest.NewRecorder()

	server.handleListTools(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestPTCClientCodePersistsLocalCallCount(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "read_file"})
	server := NewPTCServer(reg)

	code := server.ClientCode()

	for _, want := range []string{
		"_ptc_call_count = getattr(_ptc_call, '_count', 0) + 1",
		"_ptc_call._count = _ptc_call_count",
		"Tool call limit exceeded (80)",
		"def read_file(**kwargs):",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("client code missing %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, "{ptcMaxToolCalls}") {
		t.Fatalf("client code leaked Go constant name:\n%s", code)
	}
}

func TestEnsurePythonUTF8EnvUpsertsEncoding(t *testing.T) {
	got := ensurePythonUTF8Env([]string{"PATH=C:\\Windows", "PYTHONIOENCODING=gbk"})
	joined := "\n" + strings.Join(got, "\n") + "\n"
	if !strings.Contains(joined, "\nPYTHONIOENCODING=utf-8\n") {
		t.Fatalf("PYTHONIOENCODING not upserted: %v", got)
	}
	if !strings.Contains(joined, "\nPYTHONUTF8=1\n") {
		t.Fatalf("PYTHONUTF8 not added: %v", got)
	}
}

func TestLooksLikePythonReaderUnicodeDecodeError(t *testing.T) {
	stderr := `Exception in thread Thread-1 (_readerthread):
UnicodeDecodeError: 'utf-8' codec can't decode byte 0xb2 in position 10`
	if !looksLikePythonReaderUnicodeDecodeError(stderr) {
		t.Fatalf("expected reader UnicodeDecodeError to be detected")
	}
	if looksLikePythonReaderUnicodeDecodeError("UnicodeDecodeError: user script failed") {
		t.Fatalf("plain script UnicodeDecodeError should not be treated as reader-only")
	}
}

func TestResolvePythonCommandFallsBackToPythonOnWindowsStyleInstalls(t *testing.T) {
	name, args := resolvePythonCommandWithLookPath(func(candidate string) (string, error) {
		if candidate == "python" {
			return `C:\Python311\python.exe`, nil
		}
		return "", exec.ErrNotFound
	})
	if name != "python" || len(args) != 0 {
		t.Fatalf("resolver = %s %v, want python with no args", name, args)
	}
}

func TestResolvePythonCommandUsesPyLauncherWhenNeeded(t *testing.T) {
	name, args := resolvePythonCommandWithLookPath(func(candidate string) (string, error) {
		if candidate == "py" {
			return `C:\Windows\py.exe`, nil
		}
		return "", exec.ErrNotFound
	})
	if name != "py" || len(args) != 1 || args[0] != "-3" {
		t.Fatalf("resolver = %s %v, want py -3", name, args)
	}
}

func TestPTCClientCodeEnforcesLocalCallLimitInPython(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python command not available")
	}

	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "read_file"})
	server := NewPTCServer(reg)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ptc_client.py"), []byte(server.ClientCode()), 0644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	script := `
import sys, urllib.request
sys.path.insert(0, r"` + dir + `")
import ptc_client

class Resp:
    def read(self):
        return b'{"content":"ok"}'

urllib.request.urlopen = lambda req, timeout=30: Resp()

for _ in range(80):
    assert ptc_client._ptc_call("read_file") == "ok"

try:
    ptc_client._ptc_call("read_file")
except RuntimeError as exc:
    assert "Tool call limit exceeded (80)" in str(exc), str(exc)
else:
    raise AssertionError("local PTC call limit did not fire")
`
	cmd := exec.Command(python, "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python helper execution failed: %v\n%s", err, output)
	}
}

func TestPTCHandleToolCallUsesRegistryMiddleware(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "read_file"})
	mw := newTestMiddleware("ptc_test")
	mw.after = func(ctx *AgentContext) error {
		ctx.ToolResult.Content = "middleware:" + ctx.ToolResult.Content
		return nil
	}
	reg.SetChain(NewMiddlewareChain(mw))
	server := NewPTCServer(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/read_file", bytes.NewBufferString(`{}`))
	req.Header.Set("X-PTC-Token", server.Token())
	rr := httptest.NewRecorder()

	server.handleToolCall(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["content"] != "middleware:ok" {
		t.Fatalf("content = %q, want middleware:ok", payload["content"])
	}
}

func TestPTCHandleToolCallInheritsParentExecutionIdentity(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "read_file"})
	capture := newIdentityCaptureMiddleware()
	reg.SetChain(NewMiddlewareChain(capture))
	server := NewPTCServer(reg)
	parent := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-ptc", UserID: "worker", RoleID: "analyst",
		AgentName: "Analyst", Source: "agent_os", ServiceID: "erp", Service: true,
	})
	server.setRunContext(parent)
	defer server.setRunContext(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/read_file", bytes.NewBufferString(`{}`))
	req.Header.Set("X-PTC-Token", server.Token())
	rr := httptest.NewRecorder()
	server.handleToolCall(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := capture.snapshot()
	if got.TenantID != "tenant-ptc" || got.RoleID != "analyst" || got.AgentName != "Analyst" || got.ServiceID != "erp" {
		t.Fatalf("PTC sub-call lost parent identity: %#v", got)
	}
}

func TestPTCHandleToolCallReturnsRegistryValidationFailureAsError(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ptcRequiredParamTool{})
	server := NewPTCServer(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/read_file", bytes.NewBufferString(`{}`))
	req.Header.Set("X-PTC-Token", server.Token())
	rr := httptest.NewRecorder()

	server.handleToolCall(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(payload["error"], "[invalid_args]") {
		t.Fatalf("expected invalid_args error, got %#v", payload)
	}
	if payload["content"] != "" {
		t.Fatalf("validation failure should not return content: %#v", payload)
	}
}

func TestPTCHandleToolCallRejectsMalformedJSONBody(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(ptcRequiredParamTool{})
	server := NewPTCServer(reg)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/read_file", bytes.NewBufferString(`{"path":`))
	req.Header.Set("X-PTC-Token", server.Token())
	rr := httptest.NewRecorder()

	server.handleToolCall(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(payload["error"], "invalid JSON body") {
		t.Fatalf("expected invalid JSON body error, got %#v", payload)
	}
	if strings.Contains(payload["error"], "[invalid_args]") {
		t.Fatalf("malformed JSON should fail before tool validation: %#v", payload)
	}
}
