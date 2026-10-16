package app

import (
	"strings"
	"testing"
)

func TestDegradedAEPipelineSummaryHidesRawProviderDiagnostics(t *testing.T) {
	execution := &PipelineExecution{
		Status: "degraded",
		Steps: []StepResult{
			{Name: "分析", Status: "degraded", Error: `API返回错误状态码: 422 new_sensitive request_id=secret`},
			{Name: "报告交付", Status: "completed", Output: "# 可读报告\n\n## 综合结论\n已基于结构化证据完成兜底交付。"},
		},
	}
	got := summarizeAEPipelineResult(execution)
	for _, forbidden := range []string{"422", "new_sensitive", "request_id", "secret"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("raw provider diagnostic leaked to user-facing summary: %s", got)
		}
	}
	for _, want := range []string{"# 可读报告", "## 综合结论"} {
		if !strings.Contains(got, want) {
			t.Fatalf("degraded delivery summary missing %q: %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "# 可读报告") {
		t.Fatalf("chat content must begin with the deliverable, got: %s", got)
	}
}

func TestAEUserFacingTaskStatusTreatsDeliveredDegradedRunAsCompleted(t *testing.T) {
	if got := aeUserFacingTaskStatus("degraded", "# 已完成的报告"); got != "completed" {
		t.Fatalf("user-facing status = %q, want completed", got)
	}
	if got := aeUserFacingTaskStatus("degraded", "  "); got != "degraded" {
		t.Fatalf("empty degraded result must remain auditable, got %q", got)
	}
	if got := aeUserFacingTaskStatus("failed", "阶段性内容"); got != "failed" {
		t.Fatalf("failed status must not be hidden, got %q", got)
	}
}

func TestFailedAEPipelineSummaryDoesNotExposeInternalIntakeAsPartialResult(t *testing.T) {
	execution := &PipelineExecution{
		Status: "failed",
		RuntimePipeline: &Pipeline{
			AIEmployeeOriginalTask: "请基于公开材料制作一版方案",
		},
		Steps: []StepResult{
			{
				Name:   aiEmployeeIntakeStepName,
				Status: "completed",
				Output: "## 意图理解\n目标结果：完成方案。\n\n## 约束账本\n内部约束。\n\n## 执行边界\n只按工作流执行。",
			},
			{Name: "业务交付", Status: "failed", Error: "blocked or incomplete final answer"},
		},
	}
	got := summarizeAEPipelineResult(execution)
	for _, forbidden := range []string{"## 意图理解", "## 约束账本", "## 执行边界", "已保留的阶段性结果"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("failed summary leaked internal intake %q: %s", forbidden, got)
		}
	}
}

func TestFailedAEPipelineSummaryPreservesCompletedBusinessPartialResult(t *testing.T) {
	execution := &PipelineExecution{
		Status: "failed",
		Steps: []StepResult{
			{Name: aiEmployeeIntakeStepName, Status: "completed", Output: "## 意图理解\n内部受理"},
			{Name: "证据分析", Status: "completed", Output: "# 已完成的证据分析\n\n这是可供用户使用的阶段性结果。"},
			{Name: "最终交付", Status: "failed", Error: "provider timeout"},
		},
	}
	got := summarizeAEPipelineResult(execution)
	if !strings.Contains(got, "# 已完成的证据分析") || strings.Contains(got, "## 意图理解") {
		t.Fatalf("failed summary did not preserve only the business partial result: %s", got)
	}
}

func TestFailedPPTPipelineSummaryHidesInternalDiagnostics(t *testing.T) {
	execution := &PipelineExecution{
		PipelineName: "ppt-polish",
		Status:       "failed",
		Steps: []StepResult{
			{
				Name:   "视觉润色",
				Status: "completed",
				Output: "Route actually executed: Level 2\nSyntaxError at line 230\nbuild_deck.py",
			},
			{
				Name:   "交付自查",
				Status: "failed",
				Error:  "PPTX delivery missing: /private/workspace/ppt_polished.pptx",
			},
		},
	}
	got := summarizeAEPipelineResult(execution)
	for _, forbidden := range []string{"Route actually executed", "SyntaxError", "build_deck.py", "/private/workspace", "delivery missing"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("internal PPT diagnostic leaked into user summary: %s", got)
		}
	}
	for _, want := range []string{"没有成功生成", "没有把构建脚本"} {
		if !strings.Contains(got, want) {
			t.Fatalf("PPT failure summary missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "重新发送同一条") {
		t.Fatalf("internal PPT failure must not shift recovery work back to the user: %s", got)
	}
}
