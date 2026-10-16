package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeProfessionalAgentTestManifest(t *testing.T, root string, allowed []string) {
	t.Helper()
	dir := filepath.Join(root, "quant-trader")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := ProfessionalAgentManifest{
		Protocol:          professionalAgentProtocol,
		ID:                "quant-trader",
		Version:           "test",
		Command:           "unused-test-command",
		AllowedEmployees:  allowed,
		Capabilities:      []string{"analyze", "health", "trading", "research-management", "financial-intelligence"},
		MaxOutputBytes:    1024 * 1024,
		MaxTimeoutSeconds: 30,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newProfessionalAgentTestRegistry(t *testing.T, allowed []string) *ProfessionalAgentRegistry {
	t.Helper()
	root := filepath.Join(t.TempDir(), "professional_agents")
	writeProfessionalAgentTestManifest(t, root, allowed)
	registry := &ProfessionalAgentRegistry{root: root, logger: log.New(io.Discard, "", 0)}
	registry.invoke = func(_ context.Context, _ ProfessionalAgentManifest, request ProfessionalAgentRequest, _, _ string) (ProfessionalAgentResponse, error) {
		return ProfessionalAgentResponse{
			Protocol:  professionalAgentProtocol,
			RequestID: request.RequestID,
			AgentID:   request.AgentID,
			Status:    "completed",
			Output:    "isolated quant result",
			Data:      json.RawMessage(`{"mode":"paper"}`),
		}, nil
	}
	return registry
}

func professionalAgentTestRequest() ProfessionalAgentRequest {
	return ProfessionalAgentRequest{
		Protocol:    professionalAgentProtocol,
		RequestID:   "exec-test:s0:a1",
		AgentID:     "quant-trader",
		Capability:  "analyze",
		EmployeeID:  "quant-trader",
		TenantID:    "default",
		ExecutionID: "exec-test",
		StepIndex:   0,
		Input:       "analyze NVDA",
	}
}

func TestProfessionalAgentManifestEnforcesEmployeeAudience(t *testing.T) {
	registry := newProfessionalAgentTestRegistry(t, []string{"another-employee"})
	_, err := registry.Invoke(context.Background(), ExecutionPrincipal{
		Kind: "employee", EmployeeID: "quant-trader", TenantID: "default",
	}, professionalAgentTestRequest(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("Invoke error = %v, want audience denial", err)
	}
}

func TestProfessionalAgentStepRequiresSealedEmployeeGrant(t *testing.T) {
	registry := newProfessionalAgentTestRegistry(t, []string{"quant-trader"})
	executor := NewPipelineExecutor(nil, nil, log.New(io.Discard, "", 0))
	executor.professionalAgents = registry
	exec := &PipelineExecution{
		ID: "exec-test", PipelineName: "quant-trader", Input: "analyze NVDA",
		Principal: ExecutionPrincipal{Kind: "employee", EmployeeID: "quant-trader", TenantID: "default"},
		Steps:     []StepResult{{Name: "quant", Revision: 1}},
	}
	step := &PipelineStep{Name: "quant", ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "analyze"}}

	if _, err := executor.executeStep(context.Background(), exec, step, 0, "ignored", "session"); err == nil || !strings.Contains(err.Error(), "explicitly addressed") {
		t.Fatalf("ungranted executeStep error = %v, want explicit employee denial", err)
	}

	ctx := withEmployeeExecutionGrant(context.Background(), "quant-trader", "default")
	output, err := executor.executeStep(ctx, exec, step, 0, "ignored", "session")
	if err != nil {
		t.Fatalf("granted executeStep: %v", err)
	}
	if !strings.Contains(output, "isolated quant result") || len(exec.Steps[0].ToolCalls) != 1 {
		t.Fatalf("output/tool audit = %q / %#v", output, exec.Steps[0].ToolCalls)
	}
}

func TestProfessionalAgentRejectsArtifactPathTraversal(t *testing.T) {
	runDir := t.TempDir()
	outside := filepath.Join(filepath.Dir(runDir), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := professionalAgentTestRequest()
	response := ProfessionalAgentResponse{
		Protocol: professionalAgentProtocol, RequestID: request.RequestID, AgentID: request.AgentID,
		Status: "completed", Artifacts: []ProfessionalAgentArtifact{{Path: "../outside.txt"}},
	}
	if err := validateProfessionalAgentResponse(request, &response, runDir); err == nil {
		t.Fatal("path traversal artifact was accepted")
	}
}

func TestProfessionalAgentRunDirectoryCapsLongExecutionIDs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "professional_agents")
	registry := &ProfessionalAgentRegistry{root: root, logger: log.New(io.Discard, "", 0)}
	longID := "pipe_quant-trader_20260714_041206_189132000_d79aa5"
	runDir, err := registry.createRunDir("quant-trader", longID, 0)
	if err != nil {
		t.Fatal(err)
	}
	directoryKey := filepath.Base(filepath.Dir(runDir))
	if len(directoryKey) > 24 {
		t.Fatalf("physical execution directory key is too long: %q", directoryKey)
	}
	if strings.Contains(runDir, longID) {
		t.Fatalf("long execution ID was repeated in physical path: %q", runDir)
	}
}

func TestProfessionalAgentDegradedMarkerIsInternal(t *testing.T) {
	output := formatProfessionalAgentResponse(ProfessionalAgentResponse{
		Status: "unavailable",
		Output: "backend missing",
		Data: json.RawMessage(`{
			"mode":"research",
			"backend_results":[{
				"backend":"ai-hedge-fund",
				"status":"degraded",
				"data":{"quality_gate":{"passed":false}},
				"output":"RAW_VERBOSE_BACKEND_DETAIL"
			}]
		}`),
	})
	if got := professionalAgentOutputStatus(output); got != "unavailable" {
		t.Fatalf("status = %q", got)
	}
	clean := stripProfessionalAgentStatusMarker(output)
	if strings.Contains(clean, professionalAgentStatusMarker) || !strings.Contains(clean, "backend missing") {
		t.Fatalf("clean output = %q", clean)
	}
	if !strings.Contains(clean, "运行摘要") || !strings.Contains(clean, "quality_gate") || strings.Contains(clean, "RAW_VERBOSE_BACKEND_DETAIL") {
		t.Fatalf("non-failed output should contain a compact diagnostic summary: %q", clean)
	}
}

func TestQuantTraderWorkerHealthOverRealNDJSONProcess(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python executable is not available")
	}
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	registry := NewProfessionalAgentRegistry(workspace, log.New(io.Discard, "", 0))
	manifest, agentDir, err := registry.loadManifest("quant-trader")
	if err != nil {
		t.Fatal(err)
	}
	request := professionalAgentTestRequest()
	request.RequestID = "health-real-process"
	request.Capability = "health"
	request.Input = "{}"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := registry.invokeProcess(ctx, manifest, request, agentDir, t.TempDir())
	if err != nil {
		t.Fatalf("invokeProcess health: %v", err)
	}
	if response.Status != "completed" || response.Protocol != professionalAgentProtocol || response.RequestID != request.RequestID {
		t.Fatalf("health response = %#v", response)
	}
}

func TestAtMentionStartsQuantTraderAndRunsIsolatedWorker(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python executable is not available")
	}
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(t.TempDir(), "fake_tradingagents_backend.py")
	backendCode := `import json, sys
request = json.load(sys.stdin)
assert request["safety"]["live_trading"] is False
assert request["safety"]["order_submission"] is False
json.dump({
  "status": "success",
  "output": "FAKE_TRADINGAGENTS_WORKED",
  "data": {"mode": request["mode"], "isolated": True},
  "warnings": [],
  "artifacts": []
}, sys.stdout)
`
	if err := os.WriteFile(backend, []byte(backendCode), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"QUANT_TRADER_TRADINGAGENTS_REPO", "QUANT_TRADER_TRADINGAGENTS_PYTHON", "QUANT_TRADER_TRADINGAGENTS_ENTRYPOINT", "QUANT_TRADER_TRADINGAGENTS_MODULE", "QUANT_TRADER_TRADINGAGENTS_HTTP_ENDPOINT",
		"QUANT_TRADER_AI_HEDGE_FUND_REPO", "QUANT_TRADER_AI_HEDGE_FUND_PYTHON", "QUANT_TRADER_AI_HEDGE_FUND_ENTRYPOINT", "QUANT_TRADER_AI_HEDGE_FUND_MODULE", "QUANT_TRADER_AI_HEDGE_FUND_HTTP_ENDPOINT",
		"QUANT_TRADER_FINGPT_REPO", "QUANT_TRADER_FINGPT_MODULE", "QUANT_TRADER_FINGPT_HTTP_ENDPOINT",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("QUANT_TRADER_TRADINGAGENTS_PYTHON", python)
	t.Setenv("QUANT_TRADER_TRADINGAGENTS_ENTRYPOINT", backend)
	t.Setenv("QUANT_TRADER_TRADINGAGENTS_TIMEOUT_SECONDS", "10")
	t.Setenv("QUANT_TRADER_TRADINGAGENTS_ENV_ALLOWLIST", "")
	// Keep this test hermetic now that production adapters can discover the
	// reviewed runtimes from the workspace without explicit REPO/PYTHON vars.
	t.Setenv("QUANT_TRADER_AUTO_DISCOVERY", "false")

	logger := log.New(io.Discard, "", 0)
	gateway := &GatewayService{config: &Config{Workspace: workspace}, logger: logger}
	gateway.config.Pipeline.Pipelines = gateway.defaultPipelineSet()
	gateway.initSSE()
	gateway.pipelineExec = NewPipelineExecutor(nil, gateway, logger)
	manager := NewAIEmployeeManager(gateway, logger)
	gateway.aeManager = manager
	var quant AIEmployeeConfig
	for _, employee := range defaultAIEmployees() {
		if employee.ID == "quant-trader" {
			quant = employee
			break
		}
	}
	manager.Register([]AIEmployeeConfig{quant})

	if cfg, _ := manager.MatchAtName("请量化交易员分析 NVDA"); cfg != nil {
		t.Fatalf("message without @ unexpectedly activated employee: %#v", cfg)
	}
	if cfg, _ := manager.MatchAtName("@量化交易员 分析 NVDA，只做研究"); cfg != nil {
		t.Fatalf("internal compatibility facade unexpectedly retained a user @ route: %#v", cfg)
	}
	record, err := manager.DispatchExplicitMention(context.Background(), quant.ID, "分析 NVDA，只做研究", "quant-diagnostic-session")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var execution *PipelineExecution
	for time.Now().Before(deadline) {
		candidate, ok := gateway.pipelineExec.GetExecution(record.ExecID)
		if ok && (candidate.Status == "completed" || candidate.Status == "degraded" || candidate.Status == "failed" || candidate.Status == "stopped") {
			execution = candidate
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution == nil {
		t.Fatal("quant trader execution did not reach a terminal state")
	}
	if execution.Status != "completed" {
		t.Fatalf("execution status = %q, want completed from the isolated TradingAgents backend; steps=%#v", execution.Status, execution.Steps)
	}
	if len(execution.Steps) != 1 || !strings.Contains(execution.Steps[0].Output, "FAKE_TRADINGAGENTS_WORKED") {
		t.Fatalf("isolated worker output = %#v", execution.Steps)
	}
	if len(execution.Steps[0].ToolCalls) != 1 || !strings.HasPrefix(execution.Steps[0].ToolCalls[0].ToolName, "professional_agent:quant-trader") {
		t.Fatalf("professional agent audit = %#v", execution.Steps[0].ToolCalls)
	}
}

func TestProfessionalOnlyDispatchForwardsOriginalUserPayload(t *testing.T) {
	workspace := t.TempDir()
	logger := log.New(io.Discard, "", 0)
	gateway := &GatewayService{config: &Config{Workspace: workspace}, logger: logger}
	gateway.config.Pipeline.Pipelines = gateway.defaultPipelineSet()
	gateway.initSSE()
	gateway.pipelineExec = NewPipelineExecutor(nil, gateway, logger)

	registry := newProfessionalAgentTestRegistry(t, []string{"quant-trader"})
	received := make(chan string, 1)
	registry.invoke = func(_ context.Context, _ ProfessionalAgentManifest, request ProfessionalAgentRequest, _, _ string) (ProfessionalAgentResponse, error) {
		received <- request.Input
		return ProfessionalAgentResponse{
			Protocol:  professionalAgentProtocol,
			RequestID: request.RequestID,
			AgentID:   request.AgentID,
			Status:    "completed",
			Output:    "original payload received",
		}, nil
	}
	gateway.pipelineExec.professionalAgents = registry

	manager := NewAIEmployeeManager(gateway, logger)
	gateway.aeManager = manager
	var quant AIEmployeeConfig
	for _, employee := range defaultAIEmployees() {
		if employee.ID == "quant-trader" {
			quant = employee
			break
		}
	}
	quant.SystemMessage = "HOST SYSTEM TEXT MUST NOT ENTER THE PROFESSIONAL PROTOCOL"
	manager.Register([]AIEmployeeConfig{quant})

	original := `{"mode":"research","symbols":["AAPL"],"frameworks":["tradingagents"]}`
	// This test verifies protocol forwarding, not chat persistence. Suppress the
	// asynchronous chat writer and wait for the pipeline goroutine itself to
	// finish so Windows can remove t.TempDir without racing a late sidecar write.
	dispatchCtx := withAESuppressResultChatPersistence(context.Background())
	record, err := manager.DispatchExplicitMention(dispatchCtx, quant.ID, original, "original-input-session")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	select {
	case got := <-received:
		if got != original {
			t.Fatalf("professional input = %q, want exact original payload %q", got, original)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("professional agent did not receive a request")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		execution, ok := gateway.pipelineExec.GetExecution(record.ExecID)
		completed := false
		if ok {
			execution.mu.Lock()
			completed = execution.Status == "completed" && !execution.running
			execution.mu.Unlock()
		}
		if completed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("professional-only pipeline did not complete")
}

func TestQuantTraderIsBuiltInExplicitEmployee(t *testing.T) {
	var found *AIEmployeeConfig
	for _, employee := range defaultAIEmployees() {
		if employee.ID == "quant-trader" {
			copy := employee
			found = &copy
			break
		}
	}
	if found == nil || found.AtName != "交易员" || found.Pipeline != "ai-trader" {
		t.Fatalf("quant trader employee = %#v", found)
	}
	gateway := &GatewayService{config: &Config{}}
	pipeline, ok := gateway.defaultPipelineSet()["ai-trader"]
	if !ok || len(pipeline.Steps) != 1 || pipeline.Steps[0].ProfessionalAgent == nil {
		t.Fatalf("quant trader pipeline = %#v", pipeline)
	}
}
