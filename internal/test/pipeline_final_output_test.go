package app

import (
	"strings"
	"testing"
)

func TestPipelineFinalOutputUsesLastCompletedStep(t *testing.T) {
	exec := &PipelineExecution{
		Steps: []StepResult{
			{Name: "搜集信息", Status: "completed", Output: "step one details"},
			{Name: "输出报告", Status: "completed", Output: "<think>hidden reasoning</think>\n\n# Final Report"},
		},
	}

	out := exec.FinalOutput()
	if !strings.Contains(out, "# Final Report") {
		t.Fatalf("final output missing last step content: %q", out)
	}
	if strings.Contains(out, "step one details") || strings.Contains(out, "<think>") {
		t.Fatalf("final output should exclude earlier steps and thinking: %q", out)
	}
}

func TestPipelineFinalOutputSkipsToolReviewLeak(t *testing.T) {
	exec := &PipelineExecution{
		Steps: []StepResult{
			{Name: "answer", Status: "completed", Output: "Use the Tencent Hunyuan filing screenshot and Tencent Cloud contract as the mini-program category materials."},
			{Name: "diagnostic", Status: "completed", Output: "\u5de5\u5177/\u6280\u80fd\u5ba1\u67e5 (\u5df2\u6267\u884c 3 \u4e2a\u5de5\u5177)\n\u5931\u8d25\u7edf\u8ba1\nlist_dir: 1 \u6b21\u5931\u8d25\nGuardrail\n\u5931\u8d25\u6a21\u5f0f\n\u63a5\u8fd1\u9884\u7b97\uff0c\u505c\u6b62\u8865\u5145\u4fe1\u606f\u6536\u96c6\uff0c\u751f\u6210\u6700\u7ec8\u7b54\u590d"},
		},
	}

	out, stepName := exec.FinalOutputWithStep()
	if stepName != "answer" {
		t.Fatalf("expected final output to skip diagnostic step, got step %q output %q", stepName, out)
	}
	if strings.Contains(out, "Guardrail") || strings.Contains(out, "list_dir") {
		t.Fatalf("final output should not contain tool review diagnostics: %q", out)
	}
}

func TestPipelineFinalOutputSkipsReflectionCheckpointLeak(t *testing.T) {
	exec := &PipelineExecution{
		Steps: []StepResult{
			{Name: "deliver", Status: "completed", Output: "Generated PPT: .openkitty/storage/cancer_analysis_polished.pptx"},
			{Name: "checkpoint", Status: "completed", Output: "[Reflection checkpoint - iteration 14]\nReview your progress so far:\nWhat remains to be done?"},
		},
	}

	out, stepName := exec.FinalOutputWithStep()
	if stepName != "deliver" {
		t.Fatalf("expected final output to skip checkpoint step, got step %q output %q", stepName, out)
	}
	if strings.Contains(out, "Reflection checkpoint") || !strings.Contains(out, "cancer_analysis_polished.pptx") {
		t.Fatalf("unexpected final output: %q", out)
	}
}

func TestPipelineExecutionViewIncludesFinalOutput(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec_1",
		PipelineName: "demo",
		Steps: []StepResult{
			{Name: "collect", Status: "completed", Output: "internal notes"},
			{Name: "deliver", Status: "completed", Output: "<think>hidden</think>\n\nFinal answer"},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.FinalOutput != "Final answer" {
		t.Fatalf("expected cleaned final output, got %q", view.FinalOutput)
	}
	if view.FinalStepName != "deliver" {
		t.Fatalf("expected final step name, got %q", view.FinalStepName)
	}
	if view.PipelineExecution.ID != "exec_1" {
		t.Fatalf("view should preserve embedded execution")
	}
}

func TestCleanPipelineUserOutputStripsInlineProcessNarration(t *testing.T) {
	out := cleanPipelineUserOutput("没有找到用户上传的会议材料。我需要向用户确认材料来源。\n\n---\n\n请提供会议记录或录音文件。")

	if strings.Contains(out, "我需要向用户确认") {
		t.Fatalf("process narration should be stripped: %q", out)
	}
	for _, want := range []string{"没有找到用户上传的会议材料", "请提供会议记录或录音文件"} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleaned output lost %q: %q", want, out)
		}
	}
}

func TestCleanPipelineUserOutputStripsEnglishProcessNarration(t *testing.T) {
	out := cleanPipelineUserOutput("Let me generate the final report.\n\n# Final Report\nDone.")

	if strings.Contains(strings.ToLower(out), "let me") {
		t.Fatalf("english process narration should be stripped: %q", out)
	}
	if !strings.Contains(out, "# Final Report") || !strings.Contains(out, "Done.") {
		t.Fatalf("cleaned output lost final content: %q", out)
	}
}

func TestCleanPipelineUserOutputPreservesCircledChoiceLines(t *testing.T) {
	input := `请选择信息起点：

① 联网检索公开资料——我会标注来源。
② 读取你上传的材料——我会按材料形成底稿。
③ 两者结合——我会交叉核验。`
	out := cleanPipelineUserOutput(input)
	for _, option := range []string{"① 联网检索", "② 读取", "③ 两者结合"} {
		if !strings.Contains(out, option) {
			t.Fatalf("choice line %q was stripped as process narration: %q", option, out)
		}
	}
}

func TestCleanPipelineUserOutputStripsSearchAndDeliveryNarration(t *testing.T) {
	out := cleanPipelineUserOutput("检索已足够判断：没有可靠轮次基准。直接给出最终交付。\n\n# 融资准备度\n现有指标方向积极，但证据不完整。")
	for _, forbidden := range []string{"检索已足够判断", "直接给出最终交付"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("process narration %q should be stripped: %q", forbidden, out)
		}
	}
	if !strings.Contains(out, "# 融资准备度") {
		t.Fatalf("cleaned output lost final answer: %q", out)
	}
}

func TestCleanPipelineUserOutputStripsInternalScopeAndEvidenceRuleNarration(t *testing.T) {
	input := `按用户限定，本轮只做准备度评估，不扩展为完整报告。

# 融资准备度
现有指标只能支持方向性判断。

> 说明：用户未给出的信息一律记为 unknown；不得把未知升级为负面事实。

- 未生成落盘文件；本轮不涉及对外联络。`
	out := cleanPipelineUserOutput(input)
	for _, forbidden := range []string{"按用户限定", "用户未给出的信息一律记为", "未生成落盘文件"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("internal narration %q should be stripped: %q", forbidden, out)
		}
	}
	for _, want := range []string{"# 融资准备度", "只能支持方向性判断"} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleaned output lost substantive content %q: %q", want, out)
		}
	}
}

func TestCleanPipelineUserOutputPreservesSubstantiveOngoingTrendAfterDecimal(t *testing.T) {
	input := "- 基本面解读：营收同比 12.69%，盈利弹性正在释放；但仍需经营现金流验证。"
	if got := cleanPipelineUserOutput(input); got != input {
		t.Fatalf("substantive financial sentence was damaged: %q", got)
	}
}

func TestCleanPipelineUserOutputPreservesRelationshipDraftAndTradeoffLanguage(t *testing.T) {
	input := strings.Join([]string{
		"你需要承担的风险：气氛会紧一点；如果对方继续否定你的感受，你得准备决定这段关系够不够让你安心。",
		`> 「我没想逼你，但“你想太多”这种话让我越来越不敢跟你说真话。我能接受你忙，不能接受每次我开口都被挡回来。接下来两周我会照常过，但如果你也想继续，我们得找个时间把联系安排说清楚。你定时间。」`,
	}, "\n")

	got := cleanPipelineUserOutput(input)
	for _, want := range []string{
		"你需要承担的风险",
		"你得准备决定",
		"这种话让我越来越不敢跟你说真话",
		"接下来两周我会照常过",
		"如果你也想继续",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("substantive relationship wording %q was stripped: %q", want, got)
		}
	}
}
