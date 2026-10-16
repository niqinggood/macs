package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineExecutionViewResolvesArtifactsInExecutionWorkspace(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, ".openkitty", "storage", "report.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("complete report"), 0644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{
		ID: "exec_workspace_artifact", PipelineName: "demo", Status: "completed", ResolvedWorkspace: workspace,
		Steps: []StepResult{{Name: "deliver", Status: "completed", Output: "Saved to .openkitty/storage/report.md", ToolCalls: []ToolCallRecord{
			{ToolName: "persistent_shell", Result: "execution step failed"},
			{ToolName: "persistent_shell", Result: "created .openkitty/storage/report.md"},
		}}},
	}
	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil {
		t.Fatal("delivery summary is nil")
	}
	if strings.Contains(strings.Join(view.Delivery.Risks, "\n"), "claimed artifact is missing") {
		t.Fatalf("tenant workspace artifact was checked against process cwd: %#v", view.Delivery.Risks)
	}
	if strings.Contains(strings.Join(view.Delivery.Risks, "\n"), "tool persistent_shell failed") {
		t.Fatalf("a later successful call did not recover the intermediate tool failure: %#v", view.Delivery.Risks)
	}
}

func TestPipelineExecutionViewIncludesArtifacts(t *testing.T) {
	workspace := t.TempDir()
	for _, rel := range []string{
		".openkitty/storage/analysis_data.json",
		".openkitty/storage/report.md",
	} {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("validated artifact"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &PipelineExecution{
		ID:                "exec_artifacts",
		PipelineName:      "demo",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{Name: "analyze", Status: "completed", Output: "json: E:/openkitty/.openkitty/storage/analysis_data.json"},
			{Name: "deliver", Status: "completed", Output: "report: C:\\work\\openkitty\\.openkitty\\storage\\report.md\nagain storage/report.md."},
		},
	}

	view := NewPipelineExecutionView(exec)
	if len(view.Artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want 2", view.Artifacts)
	}
	if view.Artifacts[0].Path != ".openkitty/storage/analysis_data.json" {
		t.Fatalf("first artifact path = %q", view.Artifacts[0].Path)
	}
	if view.Artifacts[0].SourceStep != "analyze" {
		t.Fatalf("first source step = %q", view.Artifacts[0].SourceStep)
	}
	if !strings.Contains(view.Artifacts[0].DownloadURL, ".openkitty%2Fstorage%2Fanalysis_data.json") {
		t.Fatalf("download url = %q", view.Artifacts[0].DownloadURL)
	}
	if view.Artifacts[1].Path != ".openkitty/storage/report.md" {
		t.Fatalf("second artifact path = %q", view.Artifacts[1].Path)
	}
}

func TestPipelineExecutionViewNormalizesBrokenOpenKittyStorageArtifact(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, ".openkitty", "storage", "data_analysis_report.pptx")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("validated pptx"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &PipelineExecution{
		ID:                "exec_broken_artifact_path",
		PipelineName:      "data-analysis-ppt-pipeline",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{
				Name:   "PPT report",
				Status: "completed",
				Output: "PPT saved: E:\\adebug2\\0705\\.openkitty\\\nstorage/data_analysis_report.pptx\n",
			},
		},
	}

	view := NewPipelineExecutionView(exec)
	if len(view.Artifacts) != 1 {
		t.Fatalf("artifacts = %#v, want 1", view.Artifacts)
	}
	if view.Artifacts[0].Path != ".openkitty/storage/data_analysis_report.pptx" {
		t.Fatalf("artifact path = %q", view.Artifacts[0].Path)
	}
	if !strings.Contains(view.Artifacts[0].DownloadURL, ".openkitty%2Fstorage%2Fdata_analysis_report.pptx") {
		t.Fatalf("download url = %q", view.Artifacts[0].DownloadURL)
	}
}

func TestPipelineExecutionViewIncludesDeliverySummary(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_delivery",
		PipelineName: "demo",
		Status:       "completed",
		Steps: []StepResult{
			{
				Name:       "collect",
				Status:     "completed",
				Output:     "raw notes",
				DurationMs: 100,
				ToolCalls:  []ToolCallRecord{{ToolName: "read_file", DurationMs: 10, Cached: true}},
			},
			{
				Name:       "deliver",
				Status:     "completed",
				Output:     "Final report saved to .openkitty/storage/report.md",
				DurationMs: 250,
				ToolCalls:  []ToolCallRecord{{ToolName: "write_file", DurationMs: 20}},
			},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil {
		t.Fatal("delivery summary is nil")
	}
	if view.Delivery.CompletedSteps != 2 || view.Delivery.TotalSteps != 2 {
		t.Fatalf("step counts = %d/%d", view.Delivery.CompletedSteps, view.Delivery.TotalSteps)
	}
	if view.Delivery.ToolCalls != 2 || view.Delivery.CachedToolCalls != 1 {
		t.Fatalf("tool call counts = %d cached=%d", view.Delivery.ToolCalls, view.Delivery.CachedToolCalls)
	}
	if view.Delivery.EstimatedCredits != 3 {
		t.Fatalf("estimated credits = %d, want 3 without a validated artifact", view.Delivery.EstimatedCredits)
	}
	if len(view.Delivery.CreditBreakdown) == 0 || !strings.Contains(strings.Join(view.Delivery.CreditBreakdown, "\n"), "cached tool call") {
		t.Fatalf("credit breakdown = %#v", view.Delivery.CreditBreakdown)
	}
	if view.Delivery.DurationMs != 350 {
		t.Fatalf("duration = %d", view.Delivery.DurationMs)
	}
	if view.Delivery.RiskLevel != "medium" {
		t.Fatalf("risk = %q", view.Delivery.RiskLevel)
	}
	if !strings.Contains(strings.Join(view.Delivery.Risks, "\n"), "claimed artifact is missing") {
		t.Fatalf("risks should mention missing artifact: %#v", view.Delivery.Risks)
	}
	if len(view.Delivery.Artifacts) != 0 {
		t.Fatalf("missing artifact claim became a delivery link: %#v", view.Delivery.Artifacts)
	}
}

func TestPipelineExecutionViewDeliverySummaryMarksFailuresHighRisk(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_delivery_failed",
		PipelineName: "demo",
		Status:       "failed",
		Steps: []StepResult{
			{Name: "collect", Status: "completed", Output: "ok"},
			{Name: "deliver", Status: "failed", Error: "missing input file"},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil {
		t.Fatal("delivery summary is nil")
	}
	if view.Delivery.RiskLevel != "high" {
		t.Fatalf("risk = %q", view.Delivery.RiskLevel)
	}
	if view.Delivery.FailedSteps != 1 {
		t.Fatalf("failed steps = %d", view.Delivery.FailedSteps)
	}
	if len(view.Delivery.Risks) == 0 || !strings.Contains(view.Delivery.Risks[0], "missing input file") {
		t.Fatalf("risks = %#v", view.Delivery.Risks)
	}
}

func TestPipelineParallelVirtualStepToolCallsRollUpToParent(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_parallel_audit",
		PipelineName: "wide-research",
		Steps: []StepResult{
			{Name: "Scope", Status: "completed"},
			{Name: "Parallel evidence collection", Status: "running"},
		},
	}

	auditIdx := parallelAuditStepIndex(1, 1)
	exec.recordToolCall(auditIdx, ToolCallRecord{ToolName: "web_fetch", DurationMs: 42})

	if len(exec.Steps[1].ToolCalls) != 1 {
		t.Fatalf("parent tool calls = %#v", exec.Steps[1].ToolCalls)
	}
	if exec.Steps[1].ToolCalls[0].ToolName != "web_fetch" {
		t.Fatalf("tool call = %#v", exec.Steps[1].ToolCalls[0])
	}
	if len(exec.Steps[0].ToolCalls) != 0 {
		t.Fatalf("wrong parent got tool calls: %#v", exec.Steps[0].ToolCalls)
	}
	view := exec.parallelContractExecutionView(auditIdx)
	if len(view.Steps[1].ToolCalls) != 1 || view.Steps[1].ToolCalls[0].ToolName != "web_fetch" {
		t.Fatalf("branch-local audit view = %#v", view.Steps[1].ToolCalls)
	}
}

func TestPipelineDeliveryCreditsWeightExternalAgents(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_credit_external",
		PipelineName: "coding",
		Status:       "completed",
		Steps: []StepResult{
			{
				Name:   "delegate",
				Status: "completed",
				ToolCalls: []ToolCallRecord{
					{ToolName: "external_agent"},
					{ToolName: "read_file"},
					{ToolName: "web_fetch", Cached: true},
				},
			},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil {
		t.Fatal("delivery summary is nil")
	}
	if view.Delivery.EstimatedCredits != 14 {
		t.Fatalf("estimated credits = %d, want 14", view.Delivery.EstimatedCredits)
	}
	breakdown := strings.Join(view.Delivery.CreditBreakdown, "\n")
	if !strings.Contains(breakdown, "external_agent x1") {
		t.Fatalf("breakdown missing external_agent: %#v", view.Delivery.CreditBreakdown)
	}
	if !strings.Contains(breakdown, "1 cached tool call") {
		t.Fatalf("breakdown missing cached calls: %#v", view.Delivery.CreditBreakdown)
	}
}

func TestPipelineDeliveryEvidenceSummaryExtractsSources(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_evidence",
		PipelineName: "wide-research",
		Status:       "completed",
		Steps: []StepResult{
			{
				Name:   "collect",
				Status: "completed",
				ToolCalls: []ToolCallRecord{
					{
						ToolName: "web_search",
						Result: "1. Official filing\nURL: https://www.sec.gov/filing\n" +
							"2. Market note\nURL: https://example.com/market-note",
					},
					{
						ToolName: "web_fetch",
						Params:   `{"url":"https://company.example/news"}`,
						Result:   "Company announcement",
					},
				},
			},
			{
				Name:   "report",
				Status: "completed",
				Output: "Evidence table: verified claim from https://www.sec.gov/filing",
			},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Evidence == nil {
		t.Fatal("evidence summary is nil")
	}
	evidence := view.Delivery.Evidence
	if evidence.SourceCount != 3 {
		t.Fatalf("source count = %d, want 3; sources=%#v", evidence.SourceCount, evidence.Sources)
	}
	if evidence.PrimarySourceCount != 1 {
		t.Fatalf("primary source count = %d, want 1", evidence.PrimarySourceCount)
	}
	if evidence.Level != "moderate" {
		t.Fatalf("evidence level = %q, want moderate", evidence.Level)
	}
	if evidence.VerifiedMentions == 0 {
		t.Fatalf("verified mentions = 0")
	}
	if len(evidence.Warnings) != 0 {
		t.Fatalf("warnings = %#v", evidence.Warnings)
	}
}

func TestPipelineDeliveryEvidenceSummaryWarnsWhenResearchHasNoSources(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_evidence_missing",
		PipelineName: "wide-research",
		Status:       "completed",
		Steps: []StepResult{
			{Name: "report", Status: "completed", Output: "Likely market demand, but missing evidence."},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Evidence == nil {
		t.Fatal("evidence summary is nil")
	}
	if view.Delivery.Evidence.Level != "none" {
		t.Fatalf("evidence level = %q, want none", view.Delivery.Evidence.Level)
	}
	if len(view.Delivery.Evidence.Warnings) == 0 {
		t.Fatalf("expected evidence warnings")
	}
	if view.Delivery.RiskLevel != "medium" {
		t.Fatalf("risk = %q, want medium", view.Delivery.RiskLevel)
	}
}

func TestPipelineArtifactContractSurfacesFinalDeliverableOnly(t *testing.T) {
	workspace := t.TempDir()
	storage := filepath.Join(workspace, ".openkitty", "storage")
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"deck_spec_exec.json": `{"slides":[]}`,
		"probe_exec.json":     `{"probe":true}`,
		"deck_exec.pptx":      strings.Repeat("pptx", 3000),
	} {
		if err := os.WriteFile(filepath.Join(storage, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exec := &PipelineExecution{
		ID: "exec", Status: "completed", ResolvedWorkspace: workspace,
		RuntimePipeline: &Pipeline{Steps: []PipelineStep{{ArtifactContract: &PipelineArtifactContract{
			Paths: []string{".openkitty/storage/deck_${execution_id}.pptx"}, MinBytes: 100,
		}}}},
		Steps: []StepResult{{Name: "deliver", Status: "completed", ToolCalls: []ToolCallRecord{{
			ToolName: "write_file", Status: "success", Artifacts: []ToolCallArtifactRecord{
				{Path: ".openkitty/storage/deck_spec_exec.json", Size: 13, Validated: true},
				{Path: ".openkitty/storage/probe_exec.json", Size: 14, Validated: true},
			},
		}}, Output: "spec .openkitty/storage/deck_spec_exec.json final .openkitty/storage/deck_exec.pptx"}},
	}
	artifacts := extractPipelineArtifacts(exec)
	if len(artifacts) != 1 || artifacts[0].Path != ".openkitty/storage/deck_exec.pptx" {
		t.Fatalf("contract-owned step exposed internal build files: %#v", artifacts)
	}
}
