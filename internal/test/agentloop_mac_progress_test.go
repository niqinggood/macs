package app

import (
	"strings"
	"testing"
)

func TestIsProgressOnlyFinalOutputRejectsInternalReflection(t *testing.T) {
	output := `我已理解任务：基于已有材料整理报告。

<reflection>
**PREV**: 用户要求基于已有结果整理报告。
MEMORY: 之前应该有阶段性结果文件。
NEXT: 读取目录结构。
</reflection>`

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("internal reflection/progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsLongToolSeekingDraft(t *testing.T) {
	output := "Let me try a different approach -- I'll use subprocess via a Python script that I write and execute differently. Actually, let me check if there's a way to run Python before I provide the final report."

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("long tool-seeking draft should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsGenerateNextDraft(t *testing.T) {
	output := "All 6 charts confirmed! Now let me generate the comprehensive Markdown report."

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("generate-next progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsCreateNextDraft(t *testing.T) {
	output := "Now let me create the full analysis output -- data quality report, funnel conversion, revenue trend, charts, and markdown report -- all saved to storage."

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("create-next progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsLetMeNowGenerate(t *testing.T) {
	output := "Excellent! I have all the data. Let me now generate the comprehensive daily follow-up list with quote suggestions and visit reminders."

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("let-me-now-generate progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsToolReviewLeak(t *testing.T) {
	output := "\u5de5\u5177/\u6280\u80fd\u5ba1\u67e5 (\u5df2\u6267\u884c 3 \u4e2a\u5de5\u5177)\n" +
		"\u26a0\ufe0f \u5931\u8d25\u7edf\u8ba1\n" +
		"\u26a0\ufe0f list_dir: 1 \u6b21\u5931\u8d25\n\n" +
		"\u5efa\u8bae\n" +
		"\u5f53\u524d\u65e0\u4e0d\u53ef\u7528\u5de5\u5177\uff0c\u7ee7\u7eed\u6309\u8ba1\u5212\u6267\u884c\u3002\n" +
		"Guardrail\n" +
		"\u5df2\u6267\u884c 20 \u6b21\u5de5\u5177\u8c03\u7528\u3002\n\n" +
		"\u5931\u8d25\u6a21\u5f0f\n" +
		"\u63a5\u8fd1\u9884\u7b97\uff0c\u505c\u6b62\u8865\u5145\u4fe1\u606f\u6536\u96c6\uff0c\u751f\u6210\u6700\u7ec8\u7b54\u590d"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("tool review / guardrail diagnostics should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsReflectionCheckpointLeak(t *testing.T) {
	output := `[Reflection checkpoint - iteration 14]
Review your progress so far:

What have you accomplished? Summarize key findings in 1-2 sentences.
Is your current approach working? If not, what alternative could you try?
What remains to be done? Estimate remaining steps.
Should you adjust your strategy? Consider if a simpler or more direct approach exists.
Continue with your task after this brief reflection.`

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("reflection checkpoint should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsCheckpointSummaryLeak(t *testing.T) {
	output := `[Checkpoint - iteration 25]
88. [Assistant]:

89. [User]: 分析cancer_data2.csv 做一个精美ppt
90. [Tool] cli: 工具执行错误: confirmation required for cli
91. [Tool] glob: Found 1 files matching '**/cancer_data2.csv': cancer_data2.csv`

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("checkpoint summary should not be accepted as final answer")
	}
}

func TestDiagnosticProgressOutputDropsInternalCheckpoint(t *testing.T) {
	output := "[Checkpoint - iteration 25]\n88. [Assistant]:\n89. [User]: task"
	if got := diagnosticProgressOutput(output); got != "" {
		t.Fatalf("diagnostic progress output = %q, want empty for internal checkpoint", got)
	}
}

func TestDiagnosticProgressOutputStripsThinking(t *testing.T) {
	output := "<think>hidden chain of thought</think>\n\nLet me try the reply directly first."
	got := diagnosticProgressOutput(output)
	if strings.Contains(got, "<think>") || strings.Contains(got, "hidden chain") {
		t.Fatalf("diagnostic progress output leaked thinking: %q", got)
	}
	if !strings.Contains(got, "Let me try") {
		t.Fatalf("diagnostic progress output lost visible text: %q", got)
	}
}

func TestIsProgressOnlyFinalOutputRejectsChineseNowBasedOn(t *testing.T) {
	output := "已读取PRD内容。现在基于PRD中的需求优先级、资源约束和依赖关系，制定完整的Q1-Q4季度产品路线图。"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("chinese now-based-on progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsChineseSwitchEngine(t *testing.T) {
	output := "搜狗连续6次无结果。让我换用其他引擎并尝试URL直接抓取。"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("chinese switch-engine progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsChineseSwitchSearchStrategy(t *testing.T) {
	output := "好的，让我换个更有效的搜索策略——直接搜索具体行业头部公司关于AI平台/视觉质检的新闻和需求。"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("chinese switch-search-strategy progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsChineseCompletelyDifferentStrategy(t *testing.T) {
	output := "让我换一个完全不同的策略——直接搜索具体的知名制造业企业、电子制造企业、新能源企业，看它们是否有AI平台建设/工业视觉检测方面的需求信号。"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("chinese completely-different-strategy progress output should not be accepted as final answer")
	}
}

func TestIsProgressOnlyFinalOutputRejectsFailureDiagnostic(t *testing.T) {
	output := "未能生成可用最终回答。\n\n## 执行诊断\n- 工具执行：15 次\n\n## 工具失败统计\n- read_file 失败 1 次：encoding"

	if !isProgressOnlyFinalOutput(output) {
		t.Fatal("failure diagnostic should not be accepted as a successful final answer")
	}
}

func TestBuildIterationLimitDiagnosticNoToolDataDoesNotBlameNetwork(t *testing.T) {
	out := buildIterationLimitDiagnostic("", 2, 2, 0, nil, nil, nil, nil, nil)

	for _, want := range []string{"执行诊断", "迭代轮次：2/2", "工具执行：0 次", "没有收到任何工具数据"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnostic missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "网络请求失败") {
		t.Fatalf("diagnostic should not blame network without evidence: %s", out)
	}
}

func TestBuildIterationLimitDiagnosticBrokenToolWordingIsObservabilityOnly(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"",
		18,
		120,
		15,
		nil,
		[]string{"read_file"},
		[]string{"read_file: Tool Result\nstatus: failed_compacted\nsummary: output exceeded 8000 chars"},
		map[string]int{"read_file:output_too_large": 1},
		map[string]bool{"read_file": true},
	)

	if !strings.Contains(out, "read_file 高失败率") || !strings.Contains(out, "未禁用") {
		t.Fatalf("diagnostic should describe observability-only stats, got: %s", out)
	}
	if strings.Contains(out, "read_file 已禁用") {
		t.Fatalf("diagnostic should not say the tool is disabled: %s", out)
	}
	if strings.Contains(out, "本轮建议暂停") {
		t.Fatalf("diagnostic should not tell the system to pause the tool: %s", out)
	}
}

func TestBuildToolReviewDoesNotPromoteFailuresToUnavailableTools(t *testing.T) {
	agent := &LLMAgent{}
	brokenTools := map[string]bool{}
	out := agent.buildToolReview(
		map[string]int{
			"read_file:output_too_large": 2,
			"read_file:not_found":        1,
		},
		brokenTools,
		3,
		"task-1",
	)

	if len(brokenTools) != 0 {
		t.Fatalf("tool review should not mutate failures into unavailable tools: %#v", brokenTools)
	}
	if strings.Contains(out, "不可用") || strings.Contains(out, "暂停") || strings.Contains(out, "已禁用") {
		t.Fatalf("tool review should be observability-only, got: %s", out)
	}
	if !strings.Contains(out, "read_file") || !strings.Contains(out, "3 次失败") {
		t.Fatalf("tool review should still show failure stats, got: %s", out)
	}
}

func TestBuildIterationLimitDiagnosticClassifiesNetworkToolFailure(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"",
		3,
		3,
		2,
		nil,
		[]string{"web_fetch"},
		[]string{"web_fetch: context deadline exceeded"},
		map[string]int{"web_fetch:context deadline exceeded": 2},
		nil,
	)

	for _, want := range []string{"工具或网络访问异常", "web_fetch 失败 2 次", "context deadline exceeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnostic missing %q: %s", want, out)
		}
	}
}

func TestBuildIterationLimitDiagnosticReportsDataButNoSummary(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"Let me generate the final report.",
		4,
		4,
		1,
		nil,
		[]string{"web_fetch"},
		[]string{"web_fetch: 1. source\nURL: https://example.com/news"},
		nil,
		nil,
	)

	for _, want := range []string{"已收到部分工具数据", "已获取到网页搜索结果", "最后模型输出"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnostic missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "请重试") {
		t.Fatalf("diagnostic should not tell user to retry: %s", out)
	}
}

func TestBuildIterationLimitDiagnosticBeforeMaxDoesNotClaimIterationLimit(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"Let me try the reply directly first since read worked without explicit key.",
		8,
		120,
		7,
		nil,
		[]string{"ptc_execute"},
		[]string{"ptc_execute: No API key. Run 'key' first for external service /api-key endpoint."},
		nil,
		nil,
	)

	if !strings.Contains(out, "未能生成可用最终回答") {
		t.Fatalf("diagnostic should report no usable final answer before max iterations: %s", out)
	}
	if strings.Contains(out, "任务达到迭代上限") {
		t.Fatalf("diagnostic should not claim iteration limit before max iterations: %s", out)
	}
	if !strings.Contains(out, "外部服务或工具鉴权失败") {
		t.Fatalf("diagnostic should classify external service key as tool auth: %s", out)
	}
	if strings.Contains(out, "模型 API Key") {
		t.Fatalf("diagnostic should not blame model API key for external service key evidence: %s", out)
	}
}

func TestBuildIterationLimitDiagnosticPermissionBeatsAPIKeyMention(t *testing.T) {
	out := buildIterationLimitDiagnostic(
		"",
		15,
		120,
		16,
		nil,
		[]string{"code_check", "ptc_execute"},
		[]string{"ERROR: No API key. Run 'key' first."},
		map[string]int{"code_check:工具执行错误: confirmation required for code_check": 2},
		nil,
	)

	if !strings.Contains(out, "工具权限、审批或访问限制") {
		t.Fatalf("diagnostic should prioritize confirmation-required failures: %s", out)
	}
	if strings.Contains(out, "模型 API Key") {
		t.Fatalf("diagnostic should not blame model API key when confirmation is the concrete blocker: %s", out)
	}
}

func TestBuildIterationLimitDiagnosticIgnoresFailureWordsInSuccessfulCatalogContent(t *testing.T) {
	result := toolResultForFinalization(ToolCall{
		Name: "list_skills", Arguments: map[string]interface{}{"keywords": []interface{}{"recovery"}},
	}, "A skill for blocked permission exception recovery and fatal failure analysis.", true)
	out := buildIterationLimitDiagnostic(
		"]<]minimax[>[",
		4,
		4,
		1,
		nil,
		[]string{"list_skills"},
		[]string{result},
		nil,
		nil,
	)
	if strings.Contains(out, "工具权限、审批或访问限制") {
		t.Fatalf("successful catalog prose polluted failure diagnosis: %s", out)
	}
}
