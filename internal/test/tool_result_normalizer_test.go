package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	skilldomain "mac/internal/skills"
	"mac/internal/toolcontract"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type ToolResultNormalizerConfig = toolcontract.ToolResultNormalizerConfig

var NewToolResultNormalizerWithConfig = toolcontract.NewToolResultNormalizerWithConfig
var DefaultToolResultNormalizerConfig = toolcontract.DefaultToolResultNormalizerConfig

type noisyMockTool struct {
	name           string
	output         string
	err            error
	contentPayload bool
}

func (m *noisyMockTool) Name() string { return m.name }
func (m *noisyMockTool) Description() string {
	return "mock noisy tool"
}
func (m *noisyMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *noisyMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if m.err != nil {
		return m.output, m.err
	}
	return m.output, nil
}
func (m *noisyMockTool) ResultIsContentPayload() bool { return m.contentPayload }

func TestToolResultNormalizerKeepsSmallOutput(t *testing.T) {
	n := NewToolResultNormalizerWithConfig(t.TempDir(), ToolResultNormalizerConfig{MaxInlineChars: 20})
	got := n.Normalize("mock", "short output")
	if got != "short output" {
		t.Fatalf("small output should be unchanged, got %q", got)
	}
}

func TestToolResultNormalizerNeverInlinesBinaryOutput(t *testing.T) {
	workspace := t.TempDir()
	n := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 8000})
	binary := string(append([]byte("%PDF-1.7\n"), []byte{0x00, 0xff, 0xfe, 0x00}...)) + "SECRET_BINARY_MARKER"

	got := n.Normalize("web_fetch", binary)
	for _, want := range []string{"status: unsupported_binary", "binary content was not exposed", "details_file_scope: operator_debug_only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("safe binary result missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "%PDF-") || strings.Contains(got, "SECRET_BINARY_MARKER") {
		t.Fatalf("binary bytes leaked into normalized model output:\n%s", got)
	}
	detailsPath := extractDetailsFileForTest(t, got)
	full, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(detailsPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != binary {
		t.Fatal("operator-only details file did not preserve the original binary for debugging")
	}
}

func TestToolResultNormalizerRepairsMixedUTF8AndGBKDiagnostics(t *testing.T) {
	workspace := t.TempDir()
	n := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 8000})
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("文件名、目录名或卷标语法不正确。"))
	if err != nil {
		t.Fatal(err)
	}
	mixed := string(append([]byte("执行失败: exit status 1\noutput result:\n"), gbk...))
	got := n.NormalizeWithOutcome("cli", mixed, false)
	if strings.Contains(got, "status: unsupported_binary") {
		t.Fatalf("mixed Windows diagnostic was misclassified as binary:\n%s", got)
	}
	for _, want := range []string{"执行失败", "文件名、目录名或卷标语法不正确"} {
		if !strings.Contains(got, want) {
			t.Fatalf("decoded diagnostic missing %q:\n%s", want, got)
		}
	}
}

func TestToolRegistryNormalizesLargeToolOutput(t *testing.T) {
	workspace := t.TempDir()
	output := strings.Repeat("line without signal\n", 20)
	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars: 80,
		HeadLines:      2,
		TailLines:      2,
	}))
	reg.Register(&noisyMockTool{name: "noisy", output: output})

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "noisy",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tool Result", "status: compacted", "details_file:", "original_chars:"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected normalized result to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Count(result.Content, "line without signal") >= 20 {
		t.Fatalf("normalized result still contains full output:\n%s", result.Content)
	}

	detailsPath := extractDetailsFileForTest(t, result.Content)
	full, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(detailsPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != output {
		t.Fatal("details file should contain the full original output")
	}
}

func TestToolRegistryNormalizedOutputKeepsFailureLines(t *testing.T) {
	workspace := t.TempDir()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("noise line\n")
	}
	b.WriteString("FAIL: TestPaymentFlow (0.01s)\n")
	b.WriteString("payment_test.go:42: expected paid=true got false\n")
	for i := 0; i < 20; i++ {
		b.WriteString("more noise\n")
	}

	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 80}))
	reg.Register(&noisyMockTool{name: "cli", output: b.String()})

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "cli",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status: failed_compacted", "FAIL: TestPaymentFlow", "payment_test.go:42"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected key failure line %q, got:\n%s", want, result.Content)
		}
	}
}

func TestToolRegistryReadFileContentWithFailureMarkersStaysCompacted(t *testing.T) {
	workspace := t.TempDir()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("try:\n")
		b.WriteString("    run()\n")
		b.WriteString("except Exception:\n")
		b.WriteString("    print(\"[FAIL] no id returned\")\n")
	}

	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 80}))
	reg.Register(&noisyMockTool{name: "read_file", output: b.String()})

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "read_file",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "status: compacted") {
		t.Fatalf("read_file source text should compact without failed status, got:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "status: failed_compacted") {
		t.Fatalf("read_file source text should not be failed_compacted, got:\n%s", result.Content)
	}
}

func TestToolRegistryReadFileFailureTranscriptUsesExecutionOutcome(t *testing.T) {
	workspace := t.TempDir()
	output := "执行失败: exit status 1\n" + strings.Repeat("FAIL: TestPaymentFlow\ntraceback from a previous command\n", 20)
	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 80}))
	reg.Register(&noisyMockTool{name: "read_file", output: output, contentPayload: true})

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "read-transcript",
		Name:      "read_file",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionSucceeded == nil || !*result.ExecutionSucceeded {
		t.Fatalf("successful read_file execution must carry an explicit success outcome: %+v", result)
	}
	if !strings.Contains(result.Content, "status: compacted") || strings.Contains(result.Content, "status: failed_compacted") {
		t.Fatalf("a successfully read failure transcript was misclassified:\n%s", result.Content)
	}
	if !ToolResultExecutionSucceeded(result, nil) {
		t.Fatal("downstream processing ignored the registry's explicit success outcome")
	}

	processed := NewToolResultProcessor().Process(AgentToolRunResult{
		ToolCall: ToolCall{ID: "read-transcript", Name: "read_file"},
		Result:   result,
	}, map[string]int{}, map[string]bool{})
	if !processed.Succeeded {
		t.Fatalf("agent observation marked a successful read as failed: %+v", processed)
	}
}

func TestToolRegistryListSkillsCatalogFailureWordsStaySuccessful(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "test", "")
	loader.RegisterSkill("recovery-guide", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{
			Name:        "recovery-guide",
			Description: "Handle exception, fatal failure, and blocked workflow recovery.",
		},
		Source: "builtin",
		Path:   "builtin://recovery-guide",
	})

	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{MaxInlineChars: 80}))
	reg.Register(NewListSkillsTool(loader))
	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "catalog",
		Name:      "list_skills",
		Arguments: map[string]interface{}{"keywords": []interface{}{"failure"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionSucceeded == nil || !*result.ExecutionSucceeded || !ToolResultExecutionSucceeded(result, nil) {
		t.Fatalf("catalog prose was misclassified as an execution failure: %+v", result)
	}
	if strings.Contains(result.Content, "status: failed_compacted") {
		t.Fatalf("successful catalog lookup received failed status:\n%s", result.Content)
	}
}

func TestToolResultNormalizerKeepsBoundedUseSkillInstructionsIntact(t *testing.T) {
	workspace := t.TempDir()
	content := "技能: ppt-art-director\n运行时目录: C:\\runtime\\ppt-art-director\n" +
		strings.Repeat("This skill explains exception handling and failure fallback without reporting a tool failure.\n", 180)
	n := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars:      80,
		SkillMaxInlineChars: 24000,
	})

	got := n.Normalize("use_skill", content)
	if got != content {
		t.Fatalf("bounded use_skill instructions must remain intact; got:\n%s", got)
	}
	if !ToolExecutionSucceededForTool("use_skill", got, nil) {
		t.Fatal("instruction prose containing exception/failure was misclassified as a failed skill activation")
	}
}

func TestToolResultNormalizerOversizedSkillPreservesExecutableHandoff(t *testing.T) {
	workspace := t.TempDir()
	content := "技能: ppt-art-director\n" +
		"描述: audience-facing presentation production\n" +
		"运行时目录: C:\\runtime\\ppt-art-director\n" +
		strings.Repeat("Design guidance about failure and fallback.\n", 60) +
		`python "<Runtime Directory>\scripts\scaffold_task_pptx.py" --builder "work\build_deck.py" --output "deck.pptx"` + "\n" +
		`powershell.exe -File "<Runtime Directory>\scripts\render_pptx.ps1" -InputPptx "deck.pptx"` + "\n"
	n := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars:      80,
		SkillMaxInlineChars: 120,
		KeyOutputMaxChars:   1200,
	})

	got := n.Normalize("use_skill", content)
	for _, want := range []string{
		"status: compacted",
		"运行时目录: C:\\runtime\\ppt-art-director",
		"scaffold_task_pptx.py",
		"render_pptx.ps1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("oversized skill handoff lost %q:\n%s", want, got)
		}
	}
}

func TestUseSkillToolPreservesNormalizerInstructionBudgetThroughOffload(t *testing.T) {
	tool := NewUseSkillTool(nil, nil)
	want := utf8.UTFMax * DefaultToolResultNormalizerConfig().SkillMaxInlineChars
	if got := tool.MaxResultSize(); got != want {
		t.Fatalf("use_skill byte budget=%d, want %d", got, want)
	}

	registry := NewToolRegistry()
	registry.Register(tool)
	if got := getToolMaxResultSize(registry, "use_skill"); got != want {
		t.Fatalf("registered use_skill offload budget=%d, want %d", got, want)
	}
}

func TestUseSkillExplicitFailureStillFails(t *testing.T) {
	content := "技能执行失败: 技能 ppt-art-director 不存在"
	if ToolExecutionSucceededForTool("use_skill", content, nil) {
		t.Fatal("explicit use_skill failure envelope must remain failed")
	}
}

func TestToolResultNormalizerCapsVeryLongKeyLines(t *testing.T) {
	workspace := t.TempDir()
	longFailure := "fatal: " + strings.Repeat("x", 5000) + "\n"
	content := strings.Repeat("noise\n", 10) + longFailure + strings.Repeat("tail\n", 10)
	n := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars:    80,
		KeyOutputMaxChars: 200,
	})

	got := n.Normalize("cli", content)
	if !strings.Contains(got, "status: failed_compacted") {
		t.Fatalf("expected failed_compacted status, got:\n%s", got)
	}
	if !strings.Contains(got, "preview truncated to 200 chars") {
		t.Fatalf("expected capped preview marker, got:\n%s", got)
	}
	if strings.Count(got, "x") > 260 {
		t.Fatalf("normalized result leaked too much of the long line: %d x chars", strings.Count(got, "x"))
	}
}

func TestToolResultNormalizerDoesNotTreatWebPageFailureWordsAsToolFailure(t *testing.T) {
	workspace := t.TempDir()
	normalizer := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars: 80,
		KeyLineLimit:   4,
		HeadLines:      2,
		TailLines:      2,
	})
	page := "# Vendor Trust Center\n\n**Source:** https://vendor.example/trust\n\n" +
		strings.Repeat("Failed login attempts are monitored. Disaster recovery avoids service failure. ", 8)
	got := normalizer.Normalize("web_fetch", page)
	if !strings.Contains(got, "status: compacted") {
		t.Fatalf("successful web page should be compacted as success, got:\n%s", got)
	}
	if strings.Contains(got, "status: failed_compacted") {
		t.Fatalf("page prose incorrectly changed fetch success to failure:\n%s", got)
	}
}

func TestToolResultNormalizerPreservesWebSearchTitlesAndURLs(t *testing.T) {
	workspace := t.TempDir()
	normalizer := NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars: 120,
		KeyLineLimit:   8,
		HeadLines:      2,
		TailLines:      2,
	})
	content := "## Search fallback\nOriginal engine brave failed; used agent_reach_exa.\n\n" +
		"Title: Vendor Pricing\nURL: https://vendor.example/pricing\nHighlights: plans\n\n" +
		"Title: Vendor Security\nURL: https://vendor.example/security\nHighlights: SOC 2\n" + strings.Repeat("detail ", 80)
	got := normalizer.Normalize("web_fetch", content)
	for _, want := range []string{"status: compacted", "Title: Vendor Pricing", "https://vendor.example/pricing", "https://vendor.example/security"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compacted web search lost %q:\n%s", want, got)
		}
	}
}

func TestToolResultNormalizerDoesNotRenormalizePersistedResult(t *testing.T) {
	n := NewToolResultNormalizerWithConfig(t.TempDir(), ToolResultNormalizerConfig{MaxInlineChars: 20})
	content := "Tool Result\n" +
		"tool: noisy\n" +
		"status: compacted\n" +
		"details_file: C:/tmp/openkitty/tool-results/noisy.log\n" +
		"original_chars: 100000\n"

	got := n.Normalize("mock", content)
	if got != content {
		t.Fatalf("already-normalized result should be unchanged, got:\n%s", got)
	}
}

func TestToolRegistryNormalizesLargeToolError(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(workspace, ToolResultNormalizerConfig{
		MaxInlineChars:    80,
		KeyOutputMaxChars: 200,
	}))
	reg.Register(&noisyMockTool{
		name: "cli",
		err:  errors.New("fatal: " + strings.Repeat("x", 1000)),
	})

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "cli",
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tool Result", "status: failed_compacted", "details_file:", "执行失败:"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected normalized error to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Count(result.Content, "x") > 260 {
		t.Fatalf("normalized error leaked too much of the long line: %d x chars", strings.Count(result.Content, "x"))
	}
}

func extractDetailsFileForTest(t *testing.T, content string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "details_file: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "details_file: "))
		}
	}
	t.Fatalf("details_file missing from:\n%s", content)
	return ""
}
