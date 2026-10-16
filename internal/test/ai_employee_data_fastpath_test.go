package app

import (
	"strings"
	"testing"
)

func TestDataAnalystCanUseLocalFastPath(t *testing.T) {
	for _, task := range []string{
		`分析 E:\data\sales.csv 并生成图表`,
		`读取 .openkitty/storage/orders.xlsx 做渠道分析`,
		`检查 ./fixtures/events.json`,
	} {
		if !dataAnalystCanUseLocalFastPath(task) {
			t.Fatalf("expected local fast path for %q", task)
		}
	}
	for _, task := range []string{
		"分析本季度经营情况",
		`读取 E:\data\brief.md`,
	} {
		if dataAnalystCanUseLocalFastPath(task) {
			t.Fatalf("unexpected local fast path for %q", task)
		}
	}
}

func TestDataAnalystLocalFastPipelineIsSingleStepDelivery(t *testing.T) {
	got := dataAnalystLocalFastPipeline()
	if got.Name != dataAnalysisLocalFastPipelineName || len(got.Steps) != 1 {
		t.Fatalf("unexpected fast pipeline: %#v", got)
	}
	step := got.Steps[0]
	if len(step.Tools) != 1 || step.Tools[0] != "data_science" {
		t.Fatalf("fast pipeline tools = %v", step.Tools)
	}
	for _, want := range []string{"只调用一次 data_science", `task="report"`, "确切 Markdown/PNG 路径", "不要调用 shell", "不要自己编写 Python", "文件验证"} {
		if !strings.Contains(step.Prompt, want) {
			t.Fatalf("fast pipeline prompt missing %q", want)
		}
	}
}
