package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestProfessionalAgentFailedResponseRemainsValidProtocolPayload(t *testing.T) {
	request := professionalAgentTestRequest()
	response := ProfessionalAgentResponse{
		Protocol:  professionalAgentProtocol,
		RequestID: request.RequestID,
		AgentID:   request.AgentID,
		Status:    "failed",
		Output:    "partial diagnostic",
		Warnings:  []string{"upstream rate limited"},
		Data:      json.RawMessage(`{"backend_results":[{"backend":"tradingagents","status":"error"}]}`),
		Error:     "configured backends failed",
	}
	if err := validateProfessionalAgentResponse(request, &response, t.TempDir()); err != nil {
		t.Fatalf("valid failed response was rejected: %v", err)
	}
	if response.Output != "partial diagnostic" || len(response.Warnings) != 1 || !bytes.Contains(response.Data, []byte("backend_results")) {
		t.Fatalf("failed response payload was lost: %#v", response)
	}
}

func TestProfessionalAgentFailedResponseIsObservableBoundedAndTerminal(t *testing.T) {
	workspace := t.TempDir()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&PipelineExecutionRecord{}); err != nil {
		t.Fatalf("migrate pipeline execution: %v", err)
	}

	logger := log.New(io.Discard, "", 0)
	progressEvents := make(chan Event, 32)
	gateway := &GatewayService{
		db:               db,
		logger:           logger,
		sseClients:       map[string]map[string]chan Event{"*": {"test": progressEvents}},
		taskStreams:      make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
		config: &Config{
			Workspace: workspace,
			Pipeline: PipelineConfig{Pipelines: map[string]Pipeline{
				"failed-professional": {
					Name: "failed-professional",
					Steps: []PipelineStep{
						{
							Name:              "professional research",
							ContinueOnError:   true,
							ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "analyze"},
						},
						{
							Name:              "must not run after hard failure",
							ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "analyze"},
						},
					},
				},
			}},
		},
	}

	root := filepath.Join(t.TempDir(), "professional_agents")
	writeProfessionalAgentTestManifest(t, root, []string{"quant-trader"})
	registry := &ProfessionalAgentRegistry{root: root, workspace: workspace, logger: logger}
	unexpectedSecondStep := make(chan struct{}, 1)
	rawSecret := "sk-prod-ABCDEFGHIJKLMNOPQRSTUV1234567890"
	largePadding := strings.Repeat("diagnostic-padding-", 12000)
	registry.invoke = func(_ context.Context, _ ProfessionalAgentManifest, request ProfessionalAgentRequest, _, _ string) (ProfessionalAgentResponse, error) {
		if request.StepIndex != 0 {
			unexpectedSecondStep <- struct{}{}
			return ProfessionalAgentResponse{
				Protocol: professionalAgentProtocol, RequestID: request.RequestID, AgentID: request.AgentID,
				Status: "completed", Output: "unexpected continuation",
			}, nil
		}
		data, marshalErr := json.Marshal(map[string]interface{}{
			"backend_results": []map[string]interface{}{
				{
					"backend": "tradingagents", "status": "error",
					"warnings": []string{"YFRateLimitError: Too Many Requests"},
				},
				{
					"backend": "fingpt", "status": "error",
					"warnings": []string{"provider rejected api_key=" + rawSecret},
				},
			},
			"api_key": rawSecret,
			"padding": largePadding,
		})
		if marshalErr != nil {
			return ProfessionalAgentResponse{}, marshalErr
		}
		return ProfessionalAgentResponse{
			Protocol:  professionalAgentProtocol,
			RequestID: request.RequestID,
			AgentID:   request.AgentID,
			Status:    "failed",
			Output:    "partial backend diagnostics; bearer " + rawSecret,
			Warnings:  []string{"backend warning token=" + rawSecret},
			Data:      data,
			Error:     "The configured quantitative backends failed; api_key=" + rawSecret,
		}, nil
	}

	executor := NewPipelineExecutor(nil, gateway, logger)
	executor.professionalAgents = registry
	gateway.pipelineExec = executor
	ctx := withEmployeeExecutionGrant(context.Background(), "quant-trader", "default")
	exec, err := executor.Run(ctx, "failed-professional", "analyze test symbol", "failed-session", RunModeAPI, "")
	if err != nil {
		t.Fatalf("start pipeline: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var status string
	var steps []StepResult
	for time.Now().Before(deadline) {
		status, steps = exec.progressSnapshot()
		if status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "failed" {
		t.Fatalf("execution status = %q, want failed; steps=%#v", status, steps)
	}
	if len(steps) != 2 || steps[0].Status != "failed" || steps[1].Status != "pending" {
		t.Fatalf("step terminal states = %#v", steps)
	}
	select {
	case <-unexpectedSecondStep:
		t.Fatal("continue_on_error ran a later step after a protocol-level failed response")
	default:
	}

	failedStep := steps[0]
	if !strings.Contains(failedStep.Output, `"backend_results"`) ||
		!strings.Contains(failedStep.Output, "tradingagents") ||
		!strings.Contains(failedStep.Output, "YFRateLimitError") {
		t.Fatalf("failed step output lacks backend diagnostics: %q", failedStep.Output)
	}
	if len(failedStep.Output) > professionalAgentStepOutputMaxBytes {
		t.Fatalf("failed step output is unbounded: %d bytes", len(failedStep.Output))
	}
	if len(failedStep.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one", failedStep.ToolCalls)
	}
	call := failedStep.ToolCalls[0]
	if call.Status != "error" || call.Error == "" || call.Result == "" || call.CallID == "" {
		t.Fatalf("failed tool audit = %#v", call)
	}
	if !strings.Contains(call.Error, "tradingagents=error") || !strings.Contains(call.Result, `"backend_results"`) {
		t.Fatalf("failed tool audit lacks backend index/result: %#v", call)
	}
	if len(call.Error) > professionalAgentErrorMaxBytes || len(call.Result) > professionalAgentToolResultMaxBytes {
		t.Fatalf("failed tool audit is unbounded: error=%d result=%d", len(call.Error), len(call.Result))
	}
	progressStatus, progressMessage := pipelineToolCallProgressOutcome(call)
	if progressStatus != "error" || !strings.Contains(progressMessage, "tradingagents=error") {
		t.Fatalf("tool progress = %q / %q", progressStatus, progressMessage)
	}

	preservedChannels := 0
	for name, value := range map[string]string{
		"step output":   failedStep.Output,
		"step error":    failedStep.Error,
		"tool result":   call.Result,
		"tool error":    call.Error,
		"tool progress": progressMessage,
	} {
		if strings.Contains(value, rawSecret) {
			preservedChannels++
		}
		if strings.Contains(value, "[REDACTED]") || strings.Contains(value, "[SCRUBBED]") {
			t.Fatalf("%s was forcibly redacted: %q", name, value)
		}
	}
	if preservedChannels == 0 {
		t.Fatal("professional-agent workflow lost the original credential in every observable channel")
	}

	var record PipelineExecutionRecord
	for time.Now().Before(deadline) {
		if db.Where("exec_id = ?", exec.ID).First(&record).Error == nil && record.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.Status != "failed" || !strings.Contains(record.StateJSON, "backend_results") {
		t.Fatalf("persisted execution = status %q state %q", record.Status, record.StateJSON)
	}
	if !strings.Contains(record.StateJSON, rawSecret) || len(record.StateJSON) > 2*professionalAgentStepOutputMaxBytes {
		t.Fatalf("persisted execution lost workflow content or is unbounded: %d bytes", len(record.StateJSON))
	}
	if strings.Contains(record.StateJSON, "[REDACTED]") || strings.Contains(record.StateJSON, "[SCRUBBED]") {
		t.Fatalf("persisted execution was forcibly redacted: %s", record.StateJSON)
	}

	stepFile := filepath.Join(workspace, buildStepOutputPath(exec, 0, failedStep.Name))
	fileData, err := os.ReadFile(stepFile)
	if err != nil {
		t.Fatalf("read persisted failed step output: %v", err)
	}
	if !bytes.Contains(fileData, []byte("backend_results")) || !bytes.Contains(fileData, []byte(rawSecret)) || len(fileData) > professionalAgentStepOutputMaxBytes {
		t.Fatalf("failed step file is missing workflow content or is unbounded: %d bytes", len(fileData))
	}
	if bytes.Contains(fileData, []byte("[REDACTED]")) || bytes.Contains(fileData, []byte("[SCRUBBED]")) {
		t.Fatalf("failed step file was forcibly redacted: %s", fileData)
	}

	var failedProgress *ProgressEvent
	progressDeadline := time.After(2 * time.Second)
	for failedProgress == nil {
		select {
		case event := <-progressEvents:
			progress, ok := event.Data.(ProgressEvent)
			if ok && progress.Type == "pipeline_step_failed" {
				copy := progress
				failedProgress = &copy
			}
		case <-progressDeadline:
			t.Fatal("pipeline_step_failed progress event was not broadcast")
		}
	}
	if failedProgress.Status != "error" || failedProgress.ExecutionID != exec.ID ||
		failedProgress.StepName != failedStep.Name || failedProgress.ToolCallID != call.CallID ||
		!strings.Contains(failedProgress.Message, "backend_results") {
		t.Fatalf("failed progress correlation/detail = %#v", failedProgress)
	}
	if strings.Contains(failedProgress.Message, "[REDACTED]") || strings.Contains(failedProgress.Message, "[SCRUBBED]") || len(failedProgress.Message) > professionalAgentProgressMaxBytes {
		t.Fatalf("failed progress was forcibly redacted or is unbounded: %q", failedProgress.Message)
	}
}

func TestProfessionalAgentArtifactsReachPipelineViewAsValidatedMetadata(t *testing.T) {
	workspace := t.TempDir()
	// Agent code may be installed outside the active user workspace. Runtime
	// artifacts must still land under the workspace so normal downloads work.
	root := filepath.Join(t.TempDir(), "professional_agents")
	writeProfessionalAgentTestManifest(t, root, []string{"quant-trader"})
	registry := &ProfessionalAgentRegistry{root: root, workspace: workspace, logger: log.New(io.Discard, "", 0)}
	payload := []byte(`{"signal":"underweight"}`)
	registry.invoke = func(_ context.Context, _ ProfessionalAgentManifest, request ProfessionalAgentRequest, _, runDir string) (ProfessionalAgentResponse, error) {
		relative := filepath.Join("results", "decision.json")
		absolute := filepath.Join(runDir, relative)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return ProfessionalAgentResponse{}, err
		}
		if err := os.WriteFile(absolute, payload, 0o600); err != nil {
			return ProfessionalAgentResponse{}, err
		}
		return ProfessionalAgentResponse{
			Protocol:  professionalAgentProtocol,
			RequestID: request.RequestID,
			AgentID:   request.AgentID,
			Status:    "degraded",
			Output:    "fixture-backed result",
			Artifacts: []ProfessionalAgentArtifact{{Name: "decision", Path: filepath.ToSlash(relative)}},
		}, nil
	}

	executor := NewPipelineExecutor(nil, nil, log.New(io.Discard, "", 0))
	executor.professionalAgents = registry
	exec := &PipelineExecution{
		ID: "exec-artifact", PipelineName: "quant-trader", Input: "analyze AAPL",
		Principal: ExecutionPrincipal{Kind: "employee", EmployeeID: "quant-trader", TenantID: "default"},
		Steps:     []StepResult{{Name: "quant", Revision: 1}},
	}
	step := &PipelineStep{Name: "quant", ProfessionalAgent: &ProfessionalAgentStep{ID: "quant-trader", Capability: "analyze"}}
	ctx := withEmployeeExecutionGrant(context.Background(), "quant-trader", "default")
	output, err := executor.executeStep(ctx, exec, step, 0, "ignored", "session")
	if err != nil {
		t.Fatalf("execute professional step: %v", err)
	}
	exec.Steps[0].Output = output
	exec.Steps[0].Status = "degraded"

	view := NewPipelineExecutionView(exec)
	if len(view.Artifacts) != 1 {
		t.Fatalf("artifacts = %#v, want one validated artifact", view.Artifacts)
	}
	artifact := view.Artifacts[0]
	if artifact.Path != ".openkitty/professional-agents/quant-trader/exec-artifact/step-0/results/decision.json" {
		t.Fatalf("artifact path = %q", artifact.Path)
	}
	if artifact.Size != int64(len(payload)) || !artifact.Validated {
		t.Fatalf("artifact validation metadata = %#v", artifact)
	}
	if artifact.SourceStep != "quant" || !strings.Contains(artifact.DownloadURL, "%2F.openkitty%2Fprofessional-agents%2Fquant-trader%2F") {
		t.Fatalf("artifact delivery metadata = %#v", artifact)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{`"size":24`, `"validated":true`, `"download_url"`} {
		if !bytes.Contains(encoded, []byte(wanted)) {
			t.Fatalf("pipeline JSON missing %s: %s", wanted, encoded)
		}
	}

	gateway := &GatewayService{config: &Config{Workspace: workspace}, logger: log.New(io.Discard, "", 0)}
	recorder := httptest.NewRecorder()
	gateway.handleDownloadFile(recorder, httptest.NewRequest(http.MethodGet, artifact.DownloadURL, nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatalf("validated artifact download = status %d body %q", recorder.Code, recorder.Body.Bytes())
	}
}

func TestPipelineRejectsUntrustedProfessionalArtifactAuditPaths(t *testing.T) {
	tests := []struct {
		name string
		call ToolCallRecord
	}{
		{
			name: "path traversal",
			call: ToolCallRecord{ToolName: "professional_agent:quant-trader:analyze", Artifacts: []ToolCallArtifactRecord{{
				Path: ".openkitty/professional-agents/quant-trader/../../outside.txt", Size: 10, Validated: true,
			}}},
		},
		{
			name: "unvalidated",
			call: ToolCallRecord{ToolName: "professional_agent:quant-trader:analyze", Artifacts: []ToolCallArtifactRecord{{
				Path: ".openkitty/professional-agents/quant-trader/run/result.txt", Size: 10,
			}}},
		},
		{
			name: "wrong tool provenance",
			call: ToolCallRecord{ToolName: "read_file", Artifacts: []ToolCallArtifactRecord{{
				Path: ".openkitty/professional-agents/quant-trader/run/result.txt", Size: 10, Validated: true,
			}}},
		},
		{
			name: "failed invocation",
			call: ToolCallRecord{ToolName: "professional_agent:quant-trader:analyze", Error: "failed", Artifacts: []ToolCallArtifactRecord{{
				Path: ".openkitty/professional-agents/quant-trader/run/result.txt", Size: 10, Validated: true,
			}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exec := &PipelineExecution{Steps: []StepResult{{Name: "quant", Output: "claimed .openkitty/professional-agents/quant-trader/run/result.txt", ToolCalls: []ToolCallRecord{test.call}}}}
			if got := extractPipelineArtifacts(exec); len(got) != 0 {
				t.Fatalf("untrusted artifact was exposed: %#v", got)
			}
		})
	}

	exec := &PipelineExecution{Steps: []StepResult{{Name: "quant", ToolCalls: []ToolCallRecord{{
		ToolName: "professional_agent:quant-trader:analyze",
		Status:   "success",
		Result:   "an optional upstream test failed, overall response is degraded",
		Artifacts: []ToolCallArtifactRecord{{
			Path: ".openkitty/professional-agents/quant-trader/run/result.txt", Size: 10, Validated: true,
		}},
	}}}}}
	if got := extractPipelineArtifacts(exec); len(got) != 1 {
		t.Fatalf("explicit successful audit status was overridden by result prose: %#v", got)
	}
}

func TestProfessionalAgentProcessLogSeparatesProcessAndResponseStatus(t *testing.T) {
	var logs bytes.Buffer
	registry := &ProfessionalAgentRegistry{logger: log.New(&logs, "", 0)}
	t.Setenv("OPENKITTY_PROFESSIONAL_AGENT_TEST_HELPER", "1")
	manifest := ProfessionalAgentManifest{
		ID:             "quant-trader",
		Command:        os.Args[0],
		Args:           []string{"-test.run=^TestProfessionalAgentProcessLogHelper$"},
		Environment:    []string{"OPENKITTY_PROFESSIONAL_AGENT_TEST_HELPER"},
		MaxOutputBytes: 64 * 1024,
	}
	request := professionalAgentTestRequest()
	request.RequestID = "safe-request-id"
	request.Input = "sensitive-input-must-not-be-logged"
	response, err := registry.invokeProcess(context.Background(), manifest, request, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("invoke helper: %v", err)
	}
	if response.Status != "failed" {
		t.Fatalf("response status = %q", response.Status)
	}
	got := logs.String()
	for _, wanted := range []string{"process_status=success", "response_status=failed", "request=safe-request-id"} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("log missing %q: %s", wanted, got)
		}
	}
	if strings.Contains(got, request.Input) || strings.Contains(got, " status=success") {
		t.Fatalf("log leaked input or retained ambiguous status: %s", got)
	}
}

func TestProfessionalAgentProcessLogHelper(t *testing.T) {
	if os.Getenv("OPENKITTY_PROFESSIONAL_AGENT_TEST_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	response := ProfessionalAgentResponse{
		Protocol: professionalAgentProtocol, RequestID: "safe-request-id", AgentID: "quant-trader",
		Status: "failed", Error: "paper-only policy rejection",
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
