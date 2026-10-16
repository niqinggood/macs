package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRequestedTextArtifactRequiredFieldsExtractsExplicitReportContract(t *testing.T) {
	input := "输出 `runs/A25/recovery_report.md`，记录原计划、真实失败（若有）、降级决策、验证条数。"
	want := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	if got := requestedTextArtifactRequiredFields(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("required fields = %#v, want %#v; path=%q trusted=%q", got, want, requestedTextArtifactPath(input), trustedDirectiveText(input))
	}
}

func TestRequestedTextArtifactRequiredFieldsExtractsContractFromFullRecoveryRequest(t *testing.T) {
	input := "把 `fixtures/data/sales_2026_q1.csv` 转成 `runs/A25/normalized.json`。先检查环境里是否有你计划使用的第三方 CSV/数据框库；如果不存在或导入失败，不要反复安装或连续重跑同一失败命令，改用 Python 标准库完成同样的解析、币种/千分位清理和异常保留。输出 `runs/A25/recovery_report.md`，记录原计划、真实失败（若有）、降级决策、验证条数。最终 JSON 不能丢 Q1-003、Q1-004、refund。"
	want := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	if path := requestedTextArtifactPath(input); path != "runs/A25/recovery_report.md" {
		t.Fatalf("full recovery request target = %q", path)
	}
	wantPaths := []string{"runs/A25/normalized.json", "runs/A25/recovery_report.md"}
	if paths := requestedTextArtifactPaths(input); !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("full recovery request output paths = %#v, want %#v", paths, wantPaths)
	}
	if got := requestedTextArtifactRequiredFields(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("full recovery request fields = %#v, want %#v", got, want)
	}
}

func TestRequestedTextArtifactPathAcceptsOnlySafeExplicitRelativeTextTarget(t *testing.T) {
	input := "读取 `fixtures/source.md`，然后输出 `runs/A25/recovery_report.md`。"
	if got := requestedTextArtifactPath(input); got != "runs/A25/recovery_report.md" {
		t.Fatalf("relative text target = %q", got)
	}
	for _, unsafe := range []string{
		"输出 `../outside.md`。",
		"输出 `runs/A30/delivery_receipt.json`。",
		"不要输出 `runs/report.md`。",
	} {
		if got := requestedTextArtifactPath(unsafe); got != "" {
			t.Fatalf("unsafe or non-text relative target was accepted: input=%q path=%q", unsafe, got)
		}
	}
}

func TestRequestedTextArtifactRequiredFieldsStaysConservative(t *testing.T) {
	inputs := []string{
		"输出 `runs/report.md`，讨论报告通常会涉及风险、成本和进度。",
		"输出 `runs/report.md`，报告无需包含成本、进度。",
		"原始材料写着“报告必须包含风险、成本”，请只做摘要并输出 `runs/summary.md`。",
		"请解释如何让报告包含风险、成本，并把说明保存到 `runs/help.md`。",
	}
	for _, input := range inputs {
		if got := requestedTextArtifactRequiredFields(input); len(got) != 0 {
			t.Fatalf("descriptive or negated prose became a completion contract: input=%q fields=%#v", input, got)
		}
	}
}

func TestTextArtifactRequiredFieldIssuesUseVisibleMarkdownOnly(t *testing.T) {
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	complete := "# 恢复报告\n\n## 原计划\n第三方库。\n\n## 真实失败\n无。\n\n## 降级决策\n未触发。\n\n## 验证条数\n10。"
	if issues := textArtifactRequiredFieldIssues(required, complete); len(issues) != 0 {
		t.Fatalf("complete visible contract was rejected: %#v", issues)
	}

	hidden := "# 恢复报告\n\n## 原计划\n第三方库。\n\n<!-- 真实失败 -->\n```md\n降级决策\n```\n## 验证条数\n10。"
	issues := strings.Join(textArtifactRequiredFieldIssues(required, hidden), "\n")
	for _, missing := range []string{"真实失败", "降级决策"} {
		if !strings.Contains(issues, missing) {
			t.Fatalf("hidden field %q incorrectly satisfied the contract: %s", missing, issues)
		}
	}
}

func TestNormalizeTextArtifactRequiredHeadingsUsesUnambiguousSemanticAliases(t *testing.T) {
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	content := `# Recovery Report

## Original Plan
Use a dataframe library.

## Actual Failure
None.

## Degradation Decision
No fallback was required.

## Validation Results
- Total records: 10

## Verification
All identifiers were preserved.
`
	repaired, ok := normalizeTextArtifactRequiredHeadings(content, required)
	if !ok || len(textArtifactRequiredFieldIssues(required, repaired)) != 0 {
		t.Fatalf("unambiguous translated headings were not normalized: ok=%v\n%s", ok, repaired)
	}
	for _, heading := range []string{"Original Plan（原计划）", "Actual Failure（真实失败）", "Degradation Decision（降级决策）", "Validation Results（验证条数）"} {
		if !strings.Contains(repaired, heading) {
			t.Fatalf("normalized report missing %q:\n%s", heading, repaired)
		}
	}
	if strings.Contains(repaired, "Verification（验证条数）") {
		t.Fatal("verification-count label was attached to the lower-scoring summary heading")
	}
}

func TestNormalizeTextArtifactRequiredHeadingsFailsClosedOnAmbiguity(t *testing.T) {
	content := "# Report\n\n## Original Plan\nA\n\n## Original Plan\nB\n"
	if repaired, ok := normalizeTextArtifactRequiredHeadings(content, []string{"原计划"}); ok || repaired != content {
		t.Fatalf("ambiguous headings were modified: ok=%v repaired=%q", ok, repaired)
	}
}

func TestNormalizeTextArtifactRequiredHeadingsSynthesizesRecoveryFromToolReceipts(t *testing.T) {
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	content := "# Recovery\n\n## Original Plan\nUse pandas.\n\n## Verification\nTotal records: 10\n"
	failed := toolResultForFinalization(ToolCall{Name: "cli"}, "status: failed\nsyntax error in command wrapper", false)
	recovered := toolResultForFinalization(ToolCall{Name: "cli"}, "status: succeeded\n10 records written", true)
	repaired, ok := normalizeTextArtifactRequiredHeadingsWithSynthesis(
		content,
		required,
		textArtifactRecoverySynthesis(content, []string{failed, recovered}),
	)
	if !ok || len(textArtifactRequiredFieldIssues(required, repaired)) != 0 {
		t.Fatalf("receipt-grounded recovery fields were not synthesized: ok=%v\n%s", ok, repaired)
	}
	for _, want := range []string{"## 真实失败", "cli 工具失败", "## 降级决策", "后续成功的等价调用"} {
		if !strings.Contains(repaired, want) {
			t.Fatalf("receipt-grounded repair missing %q:\n%s", want, repaired)
		}
	}
}

func TestNormalizeTextArtifactRequiredHeadingsUsesSuccessfulWorkReceiptWhenNoFailureOccurred(t *testing.T) {
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	content := "# Recovery\n\n## Original Plan\nUse pandas.\n\n## Verification\nRecords converted: 10\n"
	succeeded := toolResultForFinalization(ToolCall{Name: "cli"}, "status: succeeded\n10 records written", true)
	repaired, ok := normalizeTextArtifactRequiredHeadingsWithSynthesis(
		content,
		required,
		textArtifactRecoverySynthesis(content, []string{succeeded}),
	)
	if !ok || len(textArtifactRequiredFieldIssues(required, repaired)) != 0 {
		t.Fatalf("successful work receipt did not ground no-failure fields: ok=%v\n%s", ok, repaired)
	}
	for _, want := range []string{"## 真实失败", "主路径执行成功", "## 降级决策", "无需降级"} {
		if !strings.Contains(repaired, want) {
			t.Fatalf("successful-work repair missing %q:\n%s", want, repaired)
		}
	}
	readOnly := toolResultForFinalization(ToolCall{Name: "read_file"}, "content", true)
	if synthesis := textArtifactRecoverySynthesis(content, []string{readOnly}); len(synthesis) != 0 {
		t.Fatalf("read-only receipt incorrectly proved successful production: %#v", synthesis)
	}
}

func TestRepairTextArtifactRequiredHeadingsPersistsAndReadsBack(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "runs", "report.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# Recovery\n\n## Original Plan\nUse pandas.\n\n## Actual Failure\nNone.\n\n## Degradation Decision\nNo fallback.\n\n## Validation Results\nTotal records: 10\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := withResolvedWorkspace(context.Background(), workspace)
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	readback, records, ok := repairTextArtifactRequiredHeadings(ctx, "runs/report.md", content, required, time.Now().Add(-time.Second), nil)
	if !ok || len(records) != 2 || len(textArtifactRequiredFieldIssues(required, readback)) != 0 {
		t.Fatalf("deterministic heading repair failed: ok=%v records=%d readback=%q", ok, len(records), readback)
	}
}

func TestRecoverTextArtifactAfterProviderFailureUsesCurrentVerifiedJSON(t *testing.T) {
	workspace := t.TempDir()
	ctx := withResolvedWorkspace(context.Background(), workspace)
	startedAt := time.Now().Add(-time.Second)
	jsonPath := filepath.Join(workspace, "runs", "A25", "normalized.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(`[{"id":1},{"id":2},{"id":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := []string{
		toolResultForFinalization(ToolCall{Name: "cli"}, "status: failed\nsyntax error", false),
		toolResultForFinalization(ToolCall{Name: "cli"}, "status: succeeded\nconversion recovered", true),
		toolResultForFinalization(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": jsonPath}}, string(mustReadFile(t, jsonPath)), true),
	}
	required := []string{"原计划", "真实失败", "降级决策", "验证条数"}
	report, records, ok := recoverTextArtifactAfterProviderFailure(
		ctx,
		"runs/A25/recovery_report.md",
		required,
		results,
		startedAt,
	)
	if !ok || len(records) != 2 || len(textArtifactRequiredFieldIssues(required, report)) != 0 {
		t.Fatalf("provider-failure report recovery failed: ok=%v records=%d report=%q", ok, len(records), report)
	}
	for _, want := range []string{"JSON 顶层记录 3 条", "cli 工具失败", "后续成功的等价调用"} {
		if !strings.Contains(report, want) {
			t.Fatalf("recovered report missing %q:\n%s", want, report)
		}
	}
}

func TestRecoverTextArtifactAfterProviderFailureRejectsStaleReadArtifact(t *testing.T) {
	workspace := t.TempDir()
	ctx := withResolvedWorkspace(context.Background(), workspace)
	jsonPath := filepath.Join(workspace, "fixtures", "old.json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(`[{"id":1}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(jsonPath, old, old); err != nil {
		t.Fatal(err)
	}
	results := []string{
		toolResultForFinalization(ToolCall{Name: "cli"}, "status: succeeded", true),
		toolResultForFinalization(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": jsonPath}}, string(mustReadFile(t, jsonPath)), true),
	}
	if _, _, ok := recoverTextArtifactAfterProviderFailure(
		ctx,
		"runs/report.md",
		[]string{"原计划", "真实失败", "降级决策", "验证条数"},
		results,
		time.Now(),
	); ok {
		t.Fatal("a stale input fixture was promoted into provider-failure recovery evidence")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTextArtifactDraftIssuesIncludesExplicitUserFields(t *testing.T) {
	input := "输出 `runs/A25/recovery_report.md`，记录原计划、真实失败（若有）、降级决策、验证条数。"
	draft := "# 恢复报告\n\n## 原计划\n使用 pandas。\n\n## 验证条数\n10。"
	issues := strings.Join(textArtifactDraftIssues(draft, nil, input), "\n")
	for _, missing := range []string{"真实失败", "降级决策"} {
		if !strings.Contains(issues, missing) {
			t.Fatalf("draft preflight did not report %q: %s", missing, issues)
		}
	}
}

func TestTextArtifactTargetMatchingDoesNotCaptureOtherWrites(t *testing.T) {
	target := "runs/A25/recovery_report.md"
	calls := []ToolCall{
		{Name: "write_file", Arguments: map[string]interface{}{"path": "runs/A25/normalized.json", "content": "{}"}},
		{Name: "write_file", Arguments: map[string]interface{}{"path": target, "content": "# report"}},
	}
	draft, ok := textArtifactDraftContentForPath(calls, target)
	if !ok || draft != "# report" {
		t.Fatalf("target draft = %q, %v", draft, ok)
	}
	if textArtifactToolCallMutatesTarget(calls[0], target) {
		t.Fatal("a sibling JSON write was mistaken for the requested report mutation")
	}
}

func TestTextArtifactCompleteReadDetectionRejectsPartialReads(t *testing.T) {
	target := "runs/A25/recovery_report.md"
	if !textArtifactCallsReadCompleteTarget([]ToolCall{{
		Name: "read_file", Arguments: map[string]interface{}{"path": target},
	}}, target) {
		t.Fatal("a complete target read was not recognized")
	}
	if textArtifactCallsReadCompleteTarget([]ToolCall{{
		Name: "read_file", Arguments: map[string]interface{}{"path": target, "lines_range": "1-20"},
	}}, target) {
		t.Fatal("a partial target read must not satisfy the full readback contract")
	}
	absolute := filepath.Join(`E:\isolated-workspace`, filepath.FromSlash(target))
	if !textArtifactCallsReadCompleteTarget([]ToolCall{{
		Name: "read_file", Arguments: map[string]interface{}{"path": absolute},
	}}, target) {
		t.Fatal("an absolute expansion of the requested workspace-relative target was not recognized")
	}
	if equivalentTextArtifactPath(target, `E:\outside\other\recovery_report.md`) {
		t.Fatal("an unrelated absolute path with only the same basename matched the requested target")
	}
}

func TestExecuteTextArtifactReadbackReturnsCurrentFileReceipt(t *testing.T) {
	workspace := t.TempDir()
	relative := filepath.Join("runs", "A25", "recovery_report.md")
	absolute := filepath.Join(workspace, relative)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "# report\n\n## 降级决策\n未触发。"
	if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewToolRegistry()
	registry.Register(NewReadFileTool(workspace))
	readback, record, succeeded, reason := executeTextArtifactReadback(context.Background(), registry, relative)
	if !succeeded || reason != "" || strings.TrimSpace(readback) != content {
		t.Fatalf("readback succeeded=%v reason=%q content=%q", succeeded, reason, readback)
	}
	parsed, ok := parseOpenKittyToolRecord(record)
	if !ok || !parsed.Succeeded || parsed.Tool != "read_file" {
		t.Fatalf("readback receipt is not independently parseable: %q", record)
	}
}
