package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryArtifactContractDoesNotInheritFormatsFromHistory(t *testing.T) {
	history := []map[string]interface{}{
		{"role": "user", "content": "最终要 Word 和 Excel；现在先不要创建任何文件，只问澄清问题。"},
		{"role": "assistant", "content": "请补充市场和预算。"},
	}
	ctx := context.WithValue(context.Background(), "history_messages", history)
	prompt := currentTurnBinaryArtifactPrompt(ctx, "信息已补充，现在可以开始，所有输出放到 runs/A22。")
	if prompt != "" {
		t.Fatalf("history unexpectedly created a binary artifact contract: %s", prompt)
	}
}

func TestBinaryArtifactContractUsesExplicitCurrentTurnFormats(t *testing.T) {
	prompt := currentTurnBinaryArtifactPrompt(context.Background(), "现在生成 Word 和 Excel 文件。")
	for _, want := range []string{"DOCX", "XLSX", "convert", "real OOXML workbook"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("binary artifact prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBinaryArtifactContractHonorsCurrentTurnNoWriteBoundary(t *testing.T) {
	request := "最终要 Word 和 Excel；现在不要创建任何文件，只问澄清问题。"
	if prompt := currentTurnBinaryArtifactPrompt(context.Background(), request); prompt != "" {
		t.Fatalf("no-write intake unexpectedly activated artifact production: %s", prompt)
	}
}

func TestBinaryArtifactContractDoesNotTreatInputSpreadsheetAsOutput(t *testing.T) {
	request := "读取 input.xlsx 并在聊天里分析，不要创建文件。"
	if formats := binaryArtifactFormatsForTurn(context.Background(), request); len(formats) != 0 {
		t.Fatalf("input spreadsheet became an output contract: %v", formats)
	}
}

func TestBinaryArtifactContractIgnoresGeneratedPipelineFormatExamples(t *testing.T) {
	ctx := withUserToolPolicyInput(context.Background(), "分析一下当前 A 股行情和大盘涨跌原因。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	generatedStagePrompt := `输出媒介由用户当前请求明确决定。报告、方案等领域名词本身不等于 PPTX、PDF、Word、Excel 或落盘文件授权；用户未明确指定文件时必须在聊天正文交付。`
	if formats := binaryArtifactFormatsForTurn(ctx, generatedStagePrompt); len(formats) != 0 {
		t.Fatalf("generated pipeline format examples became an artifact contract: %v", formats)
	}
	if issues := binaryArtifactDeliveryIssues(ctx, generatedStagePrompt, nil); len(issues) != 0 {
		t.Fatalf("answer-only pipeline stage incorrectly required a binary receipt: %v", issues)
	}
}

func TestBinaryArtifactContractDoesNotCarryUnrelatedHistoryIntoPipeline(t *testing.T) {
	history := []map[string]interface{}{{"role": "user", "content": "上一项任务请生成 Word 报告。"}}
	ctx := context.WithValue(context.Background(), "history_messages", history)
	ctx = withUserToolPolicyInput(ctx, "分析一下当前 A 股行情和大盘涨跌原因。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	if formats := binaryArtifactFormatsForTurn(ctx, "当前阶段输出自然语言回答。"); len(formats) != 0 {
		t.Fatalf("unrelated chat history leaked an artifact contract into pipeline: %v", formats)
	}
}

func TestBinaryArtifactContractUsesFrozenUserRequestInsidePipeline(t *testing.T) {
	ctx := withUserToolPolicyInput(context.Background(), "请生成 Word 版行情报告。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	generatedStagePrompt := "只完成当前研究步骤，并在聊天中返回阶段结果。"
	formats := binaryArtifactFormatsForTurn(ctx, generatedStagePrompt)
	if len(formats) != 1 || formats[0] != "docx" {
		t.Fatalf("explicit user artifact request was lost inside pipeline: %v", formats)
	}
}

func TestPipelineFinalAnswerIsNotRejectedByFormatWordsInControlPrompt(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", strings.Repeat("这是基于已核验数据形成的市场分析结论。", 24)),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 1)
	ctx := withUserToolPolicyInput(context.Background(), "分析一下当前 A 股行情和大盘涨跌原因。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	input := `只完成当前分析步骤。输出媒介由用户请求决定；不得把报告一词自行解释成 Word、Excel 或落盘文件。`

	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("answer-only pipeline stage failed: %v\noutput=%s", err, output)
	}
	if strings.Contains(output, "Binary artifact delivery") || strings.Contains(output, ".docx") {
		t.Fatalf("answer-only pipeline stage was polluted by a binary artifact failure: %s", output)
	}
}

func TestTextArtifactContractIgnoresGeneratedPipelineDeliveryPath(t *testing.T) {
	ctx := withUserToolPolicyInput(context.Background(), "分析一下当前 A 股行情和大盘涨跌原因。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	generatedStagePrompt := `交付阶段使用 write_file 保存到 .openkitty/storage/a_share_market_brief.md。`
	contract := requestedTextArtifactContractForTurn(ctx, generatedStagePrompt)
	if contract.PrimaryPath != "" || len(contract.Paths) != 0 || len(contract.RequiredFields) != 0 {
		t.Fatalf("generated pipeline path became a user artifact contract: %#v", contract)
	}
}

func TestTextArtifactContractUsesFrozenUserRequestInsidePipeline(t *testing.T) {
	ctx := withUserToolPolicyInput(context.Background(), "请把行情报告保存到 reports/a_share.md。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	contract := requestedTextArtifactContractForTurn(ctx, "当前阶段只整理证据。")
	if contract.PrimaryPath != "reports/a_share.md" || len(contract.Paths) != 1 || contract.Paths[0] != "reports/a_share.md" {
		t.Fatalf("explicit user text artifact request was lost: %#v", contract)
	}
}

func TestPipelineFinalAnswerIsNotRejectedByGeneratedTextArtifactPath(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", strings.Repeat("这是基于已核验数据形成的市场分析结论。", 24)),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 1)
	ctx := withUserToolPolicyInput(context.Background(), "分析一下当前 A 股行情和大盘涨跌原因。")
	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	input := `当前步骤生成聊天回答；后续交付说明示例：使用 write_file 保存到 .openkitty/storage/a_share_market_brief.md。`

	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("answer-only pipeline stage failed: %v\noutput=%s", err, output)
	}
	if strings.Contains(output, "requested artifact") || strings.Contains(output, "a_share_market_brief.md") {
		t.Fatalf("generated text path polluted the user-facing completion: %s", output)
	}
}

func TestBinaryArtifactContractAppendsMissingConversionsFromBoundedSource(t *testing.T) {
	request := "请生成 Word 和 Excel 报告。"
	results := []string{
		toolResultForFinalization(ToolCall{Name: "write_file", Arguments: map[string]interface{}{
			"path": "runs/report.md",
		}}, "成功写入文件", true),
	}
	proposed := []ToolCall{{Name: "convert", Arguments: map[string]interface{}{
		"input": "runs/report.md", "output": "runs/report.docx",
	}}}

	calls, added := ensureBinaryArtifactConversionCalls(context.Background(), request, results, proposed, 7)
	if added != 1 || len(calls) != 2 {
		t.Fatalf("conversion calls added=%d calls=%#v", added, calls)
	}
	if got := toolArtifactPath(calls[1]); got != "runs/report.xlsx" {
		t.Fatalf("missing XLSX conversion output=%q calls=%#v", got, calls)
	}
}

func TestBinaryArtifactDeliveryIssuesRequireEveryRequestedReceipt(t *testing.T) {
	request := "最终交付 Word 和 Excel。"
	results := []string{
		toolResultForFinalization(ToolCall{Name: "convert", Arguments: map[string]interface{}{
			"output": "runs/report.docx",
		}}, "converted", true),
	}
	issues := binaryArtifactDeliveryIssues(context.Background(), request, results)
	if len(issues) != 1 || !strings.Contains(issues[0], ".xlsx") {
		t.Fatalf("delivery issues=%v, want only XLSX", issues)
	}
	results = append(results, toolResultForFinalization(ToolCall{Name: "convert", Arguments: map[string]interface{}{
		"output": "runs/report.xlsx",
	}}, "converted", true))
	if issues := binaryArtifactDeliveryIssues(context.Background(), request, results); len(issues) != 0 {
		t.Fatalf("complete binary receipts still rejected: %v", issues)
	}
}

func TestBinaryArtifactDeliveryIssuesRejectBrokenOPCReceipt(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyPackage := func(referenceID, name string) {
		source := writeOPCTestPackage(t, minimalOPCTestParts(referenceID))
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "runs", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyPackage("rId1", "valid.xlsx")
	copyPackage("rId2", "broken.xlsx")
	ctx := withResolvedWorkspace(context.Background(), workspace)
	request := "请生成 Excel 工作簿。"

	validResults := []string{toolResultForFinalization(ToolCall{Name: "convert", Arguments: map[string]interface{}{
		"output": "runs/valid.xlsx",
	}}, "converted", true)}
	if issues := binaryArtifactDeliveryIssues(ctx, request, validResults); len(issues) != 0 {
		t.Fatalf("valid OPC artifact receipt was rejected: %v", issues)
	}

	brokenResults := []string{toolResultForFinalization(ToolCall{Name: "convert", Arguments: map[string]interface{}{
		"output": "runs/broken.xlsx",
	}}, "converted", true)}
	issues := binaryArtifactDeliveryIssues(ctx, request, brokenResults)
	if len(issues) != 1 || !strings.Contains(issues[0], "undeclared relationship") {
		t.Fatalf("broken OPC artifact receipt was not rejected: %v", issues)
	}
}
