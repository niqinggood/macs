package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type parallelHardeningTestAgent struct {
	name     string
	generate func(context.Context, string) (string, error)
}

func (a *parallelHardeningTestAgent) Name() string { return a.name }
func (a *parallelHardeningTestAgent) Generate(ctx context.Context, prompt string) (string, error) {
	if a.generate == nil {
		return "ok", nil
	}
	return a.generate(ctx, prompt)
}
func (a *parallelHardeningTestAgent) GetType() AgentType { return AgentTypeCustom }

func newParallelHardeningExecutor(t *testing.T, workspace, pipelineName string, creator func(AgentConfig, string) (Agent, error)) *PipelineExecutor {
	t.Helper()
	factory := &AgentFactory{
		subAgents:           make(map[string]Agent),
		testSubAgentCreator: creator,
	}
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	return NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
}

func TestParallelAuditIndexNeverAliasesRealSteps(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{Name: "parent-zero"}, {Name: "real-next"}, {Name: "parent-two"}}}

	firstBranchIdx := parallelAuditStepIndex(0, 0)
	if firstBranchIdx >= 0 {
		t.Fatalf("parallel audit index = %d, want negative namespace", firstBranchIdx)
	}
	parent, branch, ok := decodeParallelAuditStepIndex(firstBranchIdx)
	if !ok || parent != 0 || branch != 0 {
		t.Fatalf("decoded (%d, %d, %v), want (0, 0, true)", parent, branch, ok)
	}

	exec.recordToolCall(firstBranchIdx, ToolCallRecord{ToolName: "branch-zero"})
	seventhBranchIdx := parallelAuditStepIndex(0, 7)
	exec.recordToolCall(seventhBranchIdx, ToolCallRecord{ToolName: "branch-seven"})
	exec.recordToolCall(1, ToolCallRecord{ToolName: "real-step-one"})
	exec.recordToolCall(parallelAuditStepIndex(2, 3), ToolCallRecord{ToolName: "parent-two-branch"})

	if got := len(exec.Steps[0].ToolCalls); got != 2 {
		t.Fatalf("parent step 0 audit calls = %d, want 2", got)
	}
	if got := len(exec.Steps[1].ToolCalls); got != 1 || exec.Steps[1].ToolCalls[0].ToolName != "real-step-one" {
		t.Fatalf("real step 1 audit was polluted: %+v", exec.Steps[1].ToolCalls)
	}
	if got := len(exec.Steps[2].ToolCalls); got != 1 || exec.Steps[2].ToolCalls[0].ToolName != "parent-two-branch" {
		t.Fatalf("parent step 2 audit calls = %+v", exec.Steps[2].ToolCalls)
	}
	firstBranchView := exec.parallelContractExecutionView(firstBranchIdx)
	if got := firstBranchView.Steps[0].ToolCalls; len(got) != 1 || got[0].ToolName != "branch-zero" {
		t.Fatalf("branch-local contract audit leaked calls: %+v", got)
	}
	seventhBranchView := exec.parallelContractExecutionView(seventhBranchIdx)
	if got := seventhBranchView.Steps[0].ToolCalls; len(got) != 1 || got[0].ToolName != "branch-seven" {
		t.Fatalf("branch-local contract audit leaked calls: %+v", got)
	}
}

func TestParallelSubStepAppliesAndValidatesArtifactContract(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:           "pipe_parallel_contract",
		PipelineName: "parallel-contract",
		Status:       "running",
		CreatedAt:    time.Now().Add(-time.Second),
		Steps:        []StepResult{{Name: "fanout", Status: "pending"}},
	}
	artifactTemplate := ".openkitty/storage/parallel_${execution_id}.md"
	artifactPath := renderPipelineRuntimePath(artifactTemplate, exec)
	fullArtifactPath := filepath.Join(workspace, filepath.FromSlash(artifactPath))

	var promptMu sync.Mutex
	var prompts []string
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		return &parallelHardeningTestAgent{name: config.Name, generate: func(_ context.Context, prompt string) (string, error) {
			promptMu.Lock()
			prompts = append(prompts, prompt)
			promptMu.Unlock()
			if err := os.MkdirAll(filepath.Dir(fullArtifactPath), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(fullArtifactPath, []byte("# current execution artifact\nverified body"), 0o644); err != nil {
				return "", err
			}
			return "artifact created", nil
		}}, nil
	})
	step := &PipelineStep{Name: "fanout", Parallel: []PipelineStep{{
		Name: "writer", Prompt: "write the artifact", NoTools: true, MaxIter: 1,
		ArtifactContract: &PipelineArtifactContract{Paths: []string{artifactTemplate}, MinBytes: 8},
	}}}

	executor.executeParallelSteps(context.Background(), exec, step, 0, "make report", "session")

	if exec.Status != "running" || exec.Steps[0].Status != "completed" {
		t.Fatalf("parallel contract execution status = %q/%q, error=%q", exec.Status, exec.Steps[0].Status, exec.Steps[0].Error)
	}
	promptMu.Lock()
	defer promptMu.Unlock()
	if len(prompts) != 1 {
		t.Fatalf("Generate calls = %d, want 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "Current-run artifact contract (mandatory)") || !strings.Contains(prompts[0], filepath.ToSlash(artifactPath)) {
		t.Fatalf("parallel sub-step prompt did not receive runtime artifact contract:\n%s", prompts[0])
	}
}

func TestParallelSubStepReceivesUpstreamFrozenSourceContract(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := ".openkitty/storage/.system/source.json"
	sourceData := []byte(`{"source_url":"https://example.com/source","content":"verified source body"}`)
	fullSourcePath := filepath.Join(workspace, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(fullSourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullSourcePath, sourceData, 0o644); err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256(sourceData)
	sourceSHA := hex.EncodeToString(sourceHash[:])
	exec := &PipelineExecution{
		ID: "pipe_parallel_downstream_source", PipelineName: "parallel-downstream-source", Status: "running",
		Steps: []StepResult{
			{Name: "source", Status: "completed", SourceSnapshot: &PipelineSourceSnapshot{Path: sourcePath, SHA256: sourceSHA, Bytes: len(sourceData), RequireDownstreamRead: true}},
			{Name: "fanout", Status: "running"},
		},
	}
	var capturedPrompt string
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		return &parallelHardeningTestAgent{name: config.Name, generate: func(_ context.Context, prompt string) (string, error) {
			capturedPrompt = prompt
			return "used frozen source", nil
		}}, nil
	})
	upstreamSteps := append([]StepResult(nil), exec.Steps...)

	_, _, err := executor.executeParallelSubStep(context.Background(), exec, PipelineStep{Name: "reader", NoTools: true, MaxIter: 1}, 1, 0, upstreamSteps, "summarize source", "session")
	if err != nil {
		t.Fatalf("execute parallel downstream sub-step: %v", err)
	}
	if !strings.Contains(capturedPrompt, "Frozen source contract (mandatory)") || !strings.Contains(capturedPrompt, sourceSHA) || !strings.Contains(capturedPrompt, sourcePath) {
		t.Fatalf("parallel downstream prompt omitted frozen-source contract:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, string(sourceData)) || !strings.Contains(capturedPrompt, "has validated and pre-read") {
		t.Fatalf("parallel downstream prompt omitted host-pre-read complete source:\n%s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "No verified host pre-read is present") {
		t.Fatalf("parallel downstream prompt still requested a redundant model read:\n%s", capturedPrompt)
	}
	auditIdx := parallelAuditStepIndex(1, 0)
	branchCalls := exec.parallelToolCalls[auditIdx]
	if len(branchCalls) != 1 || branchCalls[0].ToolName != "read_file" || !strings.Contains(branchCalls[0].Params, `"host_preread":true`) {
		t.Fatalf("parallel branch did not retain independent host read evidence: %#v", branchCalls)
	}
}

func TestParallelSubStepContractFailureGetsOneRepairThenFails(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:           "pipe_parallel_contract_failure",
		PipelineName: "parallel-contract-failure",
		Status:       "running",
		CreatedAt:    time.Now().Add(-time.Second),
		Steps:        []StepResult{{Name: "fanout", Status: "pending"}},
	}
	var calls atomic.Int32
	var sawRepair atomic.Bool
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		return &parallelHardeningTestAgent{name: config.Name, generate: func(_ context.Context, prompt string) (string, error) {
			calls.Add(1)
			if strings.Contains(prompt, "Contract repair (one bounded attempt)") {
				sawRepair.Store(true)
			}
			return "claimed success without creating the artifact", nil
		}}, nil
	})
	step := &PipelineStep{Name: "fanout", Parallel: []PipelineStep{{
		Name: "broken-writer", Prompt: "write the artifact", NoTools: true, MaxIter: 1,
		ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/missing_${execution_id}.md"}, MinBytes: 8},
	}}}

	executor.executeParallelSteps(context.Background(), exec, step, 0, "make report", "session")

	if got := calls.Load(); got != 2 {
		t.Fatalf("Generate calls = %d, want initial attempt plus one repair", got)
	}
	if !sawRepair.Load() {
		t.Fatal("repair attempt did not receive the contract repair instruction")
	}
	if exec.Status != "failed" || exec.Steps[0].Status != "failed" {
		t.Fatalf("status = %q/%q, want failed/failed", exec.Status, exec.Steps[0].Status)
	}
	if !strings.Contains(exec.Steps[0].Error, "pipeline contract failed after repair") {
		t.Fatalf("unexpected contract failure: %q", exec.Steps[0].Error)
	}
}

func TestParallelWorkerPanicHonorsContinueOnError(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID: "pipe_parallel_panic", PipelineName: "parallel-panic", Status: "running",
		Steps: []StepResult{{Name: "fanout", Status: "pending"}},
	}
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		if strings.HasSuffix(config.Name, "_optional-panic") {
			return &parallelHardeningTestAgent{name: config.Name, generate: func(context.Context, string) (string, error) {
				panic("boom")
			}}, nil
		}
		return &parallelHardeningTestAgent{name: config.Name, generate: func(context.Context, string) (string, error) {
			return "healthy branch", nil
		}}, nil
	})
	step := &PipelineStep{Name: "fanout", Parallel: []PipelineStep{
		{Name: "optional-panic", NoTools: true, MaxIter: 1, ContinueOnError: true},
		{Name: "required-healthy", NoTools: true, MaxIter: 1},
	}}

	executor.executeParallelSteps(context.Background(), exec, step, 0, "input", "session")

	if exec.Status != "running" || exec.Steps[0].Status != "completed" {
		t.Fatalf("status = %q/%q, optional panic should degrade", exec.Status, exec.Steps[0].Status)
	}
	for _, want := range []string{"DATA_STATUS: fetch_failed", "panic in parallel sub-step", "healthy branch"} {
		if !strings.Contains(exec.Steps[0].Output, want) {
			t.Fatalf("merged output missing %q: %s", want, exec.Steps[0].Output)
		}
	}
}

func TestParallelCollectorWallDeadlineBoundsContextIgnoringWorker(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID: "pipe_parallel_deadline", PipelineName: "parallel-deadline", Status: "running",
		Steps: []StepResult{{Name: "fanout", Status: "pending"}},
	}
	release := make(chan struct{})
	defer close(release)
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		if strings.HasSuffix(config.Name, "_optional-hang") {
			return &parallelHardeningTestAgent{name: config.Name, generate: func(context.Context, string) (string, error) {
				<-release // deliberately ignores ctx until the test releases it
				return "released", nil
			}}, nil
		}
		return &parallelHardeningTestAgent{name: config.Name, generate: func(context.Context, string) (string, error) {
			return "healthy branch", nil
		}}, nil
	})
	executor.parallelWallTimeout = 250 * time.Millisecond
	step := &PipelineStep{Name: "fanout", Parallel: []PipelineStep{
		{Name: "optional-hang", NoTools: true, MaxIter: 1, ContinueOnError: true},
		{Name: "required-healthy", NoTools: true, MaxIter: 1},
	}}

	started := time.Now()
	executor.executeParallelSteps(context.Background(), exec, step, 0, "input", "session")
	elapsed := time.Since(started)

	if elapsed > time.Second {
		t.Fatalf("parallel collector took %v, want bounded return", elapsed)
	}
	if exec.Status != "running" || exec.Steps[0].Status != "completed" {
		t.Fatalf("status = %q/%q, optional timeout should degrade", exec.Status, exec.Steps[0].Status)
	}
	if !strings.Contains(exec.Steps[0].Output, "parallel step wall deadline exceeded") {
		t.Fatalf("timeout degradation missing from output: %s", exec.Steps[0].Output)
	}
}

func TestParallelCollectorRespectsParentCancellation(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID: "pipe_parallel_cancel", PipelineName: "parallel-cancel", Status: "running",
		Steps: []StepResult{{Name: "fanout", Status: "pending"}},
	}
	release := make(chan struct{})
	defer close(release)
	executor := newParallelHardeningExecutor(t, workspace, exec.PipelineName, func(config AgentConfig, _ string) (Agent, error) {
		return &parallelHardeningTestAgent{name: config.Name, generate: func(context.Context, string) (string, error) {
			<-release
			return "released", errors.New("released after cancellation")
		}}, nil
	})
	executor.parallelWallTimeout = time.Second
	step := &PipelineStep{Name: "fanout", Parallel: []PipelineStep{{Name: "required", NoTools: true, MaxIter: 1}}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	started := time.Now()
	executor.executeParallelSteps(ctx, exec, step, 0, "input", "session")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("parent cancellation took %v, want prompt return", elapsed)
	}
	if exec.Status != "stopped" || !strings.Contains(exec.Steps[0].Error, "context canceled") {
		t.Fatalf("status/error = %q/%q, want stopped/context canceled", exec.Status, exec.Steps[0].Error)
	}
}
