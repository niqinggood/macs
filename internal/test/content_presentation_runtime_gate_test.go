package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func presentationTestContext(input string) context.Context {
	return withPresentationProductionState(context.Background(), input)
}

func completePresentationAestheticQAForTest(state *presentationProductionState, slides int) {
	if state == nil {
		return
	}
	if slides <= 0 {
		slides = max(state.RenderedSlideCount, state.DeckSlideCount)
	}
	if slides <= 0 {
		slides = 1
	}
	if state.DeckSlideCount <= 0 {
		state.DeckSlideCount = slides
	}
	if state.RenderedSlideCount <= 0 {
		state.RenderedSlideCount = slides
	}
	if state.RenderTarget == "" {
		state.RenderTarget = "preview"
	}
	if state.VisualInspectedImages == nil {
		state.VisualInspectedImages = make(map[string]bool)
	}
	for page := 1; page <= slides; page++ {
		path := filepath.Join(state.RenderTarget, fmt.Sprintf("slide_%02d.png", page))
		state.VisualInspectedImages[normalizedPresentationPath(path)] = true
	}
	state.FinalVisualInspected = true
	state.VisualCoverageComplete = true
	state.AestheticQAPassed = true
	state.AestheticQAEvidence = "test:vision-authored-aesthetic-ledger"
}

func TestPresentationFirstVersionStillUsesSubstantiveQualityFloor(t *testing.T) {
	ctx := presentationTestContext("请先制作一版正式产品介绍 PPT")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || !state.Substantive || !state.StrictQA || !state.PerSlideCustomizationRequired || state.BasePPTHandoffAllowed {
		t.Fatalf("first audience-facing version was incorrectly downgraded: %#v", state)
	}
	if state.RequiredBodyFontSize != 16 || state.RequiredTitleFontSize != 35 {
		t.Fatalf("default audience readability floor was not frozen: %#v", state)
	}
}

func TestPresentationExplicitRoughInternalDraftAllowsBaseRoute(t *testing.T) {
	ctx := presentationTestContext("请快速做一份内部粗糙草稿 PPT，设计不重要")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || state.Substantive || state.PerSlideCustomizationRequired || state.StrictQA || !state.BasePPTHandoffAllowed {
		t.Fatalf("explicit rough internal draft did not retain the bounded base route: %#v", state)
	}
}

func TestValidateSourceBoundPresentationSpecRequiresTruthAndCompositionContract(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	bad := `{"slides":[{"kind":"cover"},{"kind":"cards","title":"增长 80%"}]}`
	if err := os.WriteFile(badPath, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSourceBoundPresentationSpec(badPath, ""); err == nil ||
		!strings.Contains(err.Error(), "evidence_policy") ||
		!strings.Contains(err.Error(), "art_direction") ||
		!strings.Contains(err.Error(), "palette_strategy") ||
		!strings.Contains(err.Error(), "narrative_job") ||
		!strings.Contains(err.Error(), "source_note") {
		t.Fatalf("incomplete substantive spec escaped the contract: %v", err)
	}

	goodPath := filepath.Join(dir, "good.json")
	good := `{
  "evidence_policy":"source-bound",
  "art_direction":{"visual_thesis":"one strong explanatory anchor per page","composition_strategy":"vary scale and evidence form","asset_strategy":"use only evidence-bearing visuals","palette_strategy":"neutral canvas with one evidence accent","typography_strategy":"30pt claims and 18pt body copy","background_rhythm":"contrast bookends with quiet evidence pages"},
  "evidence_ledger":[{"id":"E1","source":"source: user","locator":"user request","excerpt":"用户确认了三条验证路径"}],
  "slides":[
    {"kind":"cover","title":"产品方向"},
    {"kind":"chart","title":"三条验证路径","narrative_job":"show the decision logic","focal_anchor":"central editable chart","evidence_form":"decision path","composition_family":"asymmetric explanatory chart","source_note":"source: user","evidence_refs":["E1"]}
  ]
}`
	if err := os.WriteFile(goodPath, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSourceBoundPresentationSpec(goodPath, "用户确认了三条验证路径"); err != nil {
		t.Fatalf("complete source-bound spec was rejected: %v", err)
	}
}

func TestValidateSourceBoundPresentationSpecLabelsUnsupportedNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.json")
	write := func(note string) {
		t.Helper()
		data := fmt.Sprintf(`{
  "evidence_policy":"source-bound",
  "art_direction":{"visual_thesis":"evidence-led","composition_strategy":"one dominant visual","asset_strategy":"use charts only for sourced numbers","palette_strategy":"neutral canvas with one evidence accent","typography_strategy":"30pt claims and 18pt body copy","background_rhythm":"contrast bookends with quiet evidence pages"},
  "evidence_ledger":[{"id":"E1","source":"source: public - https://example.test/report","locator":"results","excerpt":"用户没有提供效果数据"}],
  "slides":[
    {"kind":"cover","title":"方案"},
    {"kind":"metrics","title":"预计提升 80%%","narrative_job":"separate plan from fact","focal_anchor":"80%% estimate","evidence_form":"labeled planning estimate","composition_family":"single-metric statement","source_note":%q,"evidence_refs":["E1"]}
  ]
}`, note)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("待确认")
	if err := validateSourceBoundPresentationSpec(path, "用户没有提供效果数据"); err == nil || !strings.Contains(err.Error(), "numeric claims") {
		t.Fatalf("unsupported number with an unknown note escaped: %v", err)
	}

	write("规划估算（非项目事实）")
	if err := validateSourceBoundPresentationSpec(path, "用户没有提供效果数据"); err != nil {
		t.Fatalf("clearly labeled planning estimate was rejected: %v", err)
	}

	write("source: public - https://example.test/report")
	if err := validateSourceBoundPresentationSpec(path, "用户没有提供效果数据"); err == nil || !strings.Contains(err.Error(), "referenced evidence excerpts") {
		t.Fatalf("a public-source label bypassed missing numeric evidence: %v", err)
	}
}

func TestValidateSourceBoundPresentationSpecRejectsUnsupportedTechnicalAcronyms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.json")
	data := `{
  "evidence_policy":"source-bound",
  "art_direction":{"visual_thesis":"explain the mechanism","composition_strategy":"mechanism then result","asset_strategy":"source-grounded diagrams","palette_strategy":"neutral canvas with one evidence accent","typography_strategy":"30pt claims and 18pt body copy","background_rhythm":"contrast bookends with quiet evidence pages"},
  "evidence_ledger":[{"id":"P1","source":"source: local - paper.pdf","locator":"Methods","excerpt":"论文描述了前向神经网络与学习得到的逆向神经网络"}],
  "slides":[
    {"kind":"cover","title":"方法解读"},
    {"kind":"process","title":"方法使用 INR 与 GAN","narrative_job":"explain the method","focal_anchor":"method flow","evidence_form":"architecture diagram","composition_family":"directional mechanism","source_note":"source: local - paper.pdf","evidence_refs":["P1"]}
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	err := validateSourceBoundPresentationSpec(path, "论文描述了前向神经网络与学习得到的逆向神经网络")
	if err == nil || !strings.Contains(err.Error(), "INR") || !strings.Contains(err.Error(), "GAN") {
		t.Fatalf("unsupported technical architecture escaped slide-level evidence binding: %v", err)
	}
}

func TestTurnIntentPoliciesUseFrozenUserRequestNotGeneratedPipelinePrompt(t *testing.T) {
	generatedPrompt := `Write the requested BP plan in chat.
[PRESENTATION CURRENT-TURN EXECUTION CONTRACT]
Create a real PPTX in this turn.`

	ctx := withUserToolPolicyInput(context.Background(), "请写一版 BP 计划书 v0")
	ctx = withTurnIntentDerivedPolicies(ctx, generatedPrompt)
	if state := presentationProductionStateFromContext(ctx); state == nil || state.Expected {
		t.Fatalf("generated pipeline text activated presentation production: %#v", state)
	}

	ctx = withUserToolPolicyInput(context.Background(), "请制作一份可编辑的融资路演 PPTX")
	ctx = withTurnIntentDerivedPolicies(ctx, "只需输出聊天文字，不要制作文件。")
	if state := presentationProductionStateFromContext(ctx); state == nil || !state.Expected {
		t.Fatalf("generated pipeline text suppressed explicit user presentation intent: %#v", state)
	}
}

func TestTurnIntentPoliciesPreserveExistingPresentationCheckpoint(t *testing.T) {
	ctx := presentationTestContext("请制作一份融资路演 PPTX")
	state := presentationProductionStateFromContext(ctx)
	state.Scaffolded = true

	derived := withTurnIntentDerivedPolicies(ctx, "generated step prompt")
	if got := presentationProductionStateFromContext(derived); got != state || !got.Scaffolded {
		t.Fatalf("intent policy derivation replaced the verified production checkpoint: %#v", got)
	}
}

func TestHarnessNewDeckRequestWithRepairClauseStaysOnProductionRoute(t *testing.T) {
	input := "读取 `fixtures/presentation/brief.md` 和 `fixtures/data`，制作 `runs/D12/q1_review.pptx`。严格按 12–15 页、18 分钟、董事会+区域负责人受众设计；图表口径要保留数据异常，备注中给讲稿。必须渲染全部幻灯片，输出逐页 PNG、包含全部页面的联系表、自动审计结果和 `runs/D12/ppt_qa.md`。验收要基于当前这份最终 deck，不能拿参考 deck 或只渲染 9/15 页就宣称通过；发现裁切/重叠要修复后重新全量渲染。\n\n黑盒运行约束：只在当前隔离工作区内读写；不得注册、购买、发消息、上传外部数据或执行其他外部写操作；没有可核验证据时不得宣称产物已完成。"

	ctx := withTurnIntentDerivedPolicies(context.Background(), input)
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		t.Fatal("presentation production state was not installed")
	}
	if !state.Expected || state.Route != "level3-ppt-master" || !state.Substantive {
		t.Fatalf("new-deck request was misclassified as existing-deck QA: %#v", state)
	}
	if !state.StrictQA || state.RequestedMinimumSlides != 12 || state.RequestedMaximumSlides != 15 {
		t.Fatalf("strict QA/page bounds were not preserved: %#v", state)
	}
	if !presentationProductionGateAvailable(ctx) {
		t.Fatal("presentation completion gate was disabled for an explicit PPTX request")
	}
	if prompt := presentationCurrentTurnExecutionPrompt(ctx); !strings.Contains(prompt, "PRESENTATION CURRENT-TURN EXECUTION CONTRACT") {
		t.Fatalf("new-deck execution contract missing: %q", prompt)
	}
}

func TestPresentationRequestCannotFinishFromQAReportWriteAlone(t *testing.T) {
	const qaPath = `runs/D12/ppt_qa.md`
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body: verifiedFileToolCallJSON(t, "write-qa", "write_file",
			`{"path":"`+qaPath+`","content":"全部页面已检查；裁切、重叠均已修复，最终验收通过。"}`),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 3)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(NewWriteFileTool(agent.workspace))
	agent.toolRegistry.Register(NewReadFileTool(agent.workspace))

	output, err := agent.Generate(context.Background(), "制作 runs/D12/q1_review.pptx，并输出 runs/D12/ppt_qa.md；发现裁切或重叠要修复后再验收。")
	if err == nil {
		t.Fatalf("QA-report-only turn did not return a presentation contract failure:\n%s", output)
	}
	if !strings.Contains(output, "没有成功生成可交付的 PPT") {
		t.Fatalf("QA-report-only write escaped the presentation gate: %s", output)
	}
	recovered, err := RecoverSessionTranscriptFromJournal(agent.transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "failed" {
		t.Fatalf("QA-report-only turn persisted status %q, want failed", recovered.Status)
	}
}

func TestPresentationGateBlocksQAReportBeforeDeckExists(t *testing.T) {
	ctx := presentationTestContext("制作 runs/D12/q1_review.pptx，并输出 runs/D12/ppt_qa.md；严格验收。")
	call := ToolCall{ID: "premature-qa", Name: "write_file", Arguments: map[string]interface{}{
		"path":    "runs/D12/ppt_qa.md",
		"content": "全部页面验收通过",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "不能在真实 PPTX 生成前先写验收报告") {
		t.Fatalf("premature QA report was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.DeckCreated = true
	state.DeckPath = "runs/D12/q1_review.pptx"
	state.mu.Unlock()
	if result, err, blocked := checkPresentationToolGate(ctx, call); !blocked || err == nil || !strings.Contains(result.Content, "built-in structural audit") {
		t.Fatalf("pre-render QA report was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	state.mu.Lock()
	state.AuditPassed = true
	state.Rendered = true
	completePresentationAestheticQAForTest(state, 1)
	state.mu.Unlock()
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("post-deck QA report was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationQAReportRemainsSecondaryToDeckProduction(t *testing.T) {
	input := "制作 runs/D12/q1_review.pptx，并输出 runs/D12/ppt_qa.md；严格验收。"
	ctx := withTurnIntentDerivedPolicies(context.Background(), input)
	contract := requestedTextArtifactContractForTurn(ctx, input)
	if contract.PrimaryPath != "" || len(contract.RequiredFields) != 0 {
		t.Fatalf("presentation QA companion incorrectly selected as primary text synthesis target: %#v", contract)
	}
	if len(contract.Paths) != 1 || normalizeToolArtifactPath(contract.Paths[0]) != normalizeToolArtifactPath("runs/D12/ppt_qa.md") {
		t.Fatalf("presentation QA companion was dropped from the completion contract: %#v", contract.Paths)
	}
}

func TestPresentationEvidenceNoteAcceptsGateVocabulary(t *testing.T) {
	sourceBacked, planning, unknown, neutral := presentationEvidenceNoteKind(
		"local evidence · 来源 fixtures/data/sales_2026_q1.csv（清洗后口径）",
	)
	if !sourceBacked || planning || unknown || neutral {
		t.Fatalf("explicit local-evidence note was not classified as source-backed: source=%v planning=%v unknown=%v neutral=%v", sourceBacked, planning, unknown, neutral)
	}
}

func TestPresentationEvidenceNoteAcceptsExplicitBilingualLocalMarker(t *testing.T) {
	sourceBacked, planning, unknown, neutral := presentationEvidenceNoteKind(
		"数据来源 local | fixtures/data/sales_2026_q1.csv（清洗后）",
	)
	if !sourceBacked || planning || unknown || neutral {
		t.Fatalf("explicit bilingual local-source marker was not classified as source-backed: source=%v planning=%v unknown=%v neutral=%v", sourceBacked, planning, unknown, neutral)
	}
}

func TestPresentationLocalReadsExtendSourceBoundEvidence(t *testing.T) {
	ctx := presentationTestContext("读取 fixtures/presentation/brief.md、fixtures/data 和 fixtures/paper.pdf，制作 runs/D12/q1_review.pptx")
	read := ToolCall{ID: "read-source", Name: "read_file", Arguments: map[string]interface{}{
		"path": "fixtures/data/sales_2026_q1.csv",
	}}
	recordPresentationToolResult(ctx, read, ToolResult{
		ToolCallID: read.ID,
		Name:       read.Name,
		Content:    "region,revenue\n华东,83000\n.openkitty/storage/build_deck.py",
	}, nil)
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	sourceContext := state.SourceContext
	state.mu.Unlock()
	if !strings.Contains(sourceContext, "华东,83000") || !strings.Contains(sourceContext, "LOCAL PRESENTATION EVIDENCE") {
		t.Fatalf("authorized local file content was not added to presentation evidence: %q", sourceContext)
	}

	extract := ToolCall{ID: "extract-source", Name: "extract", Arguments: map[string]interface{}{
		"path": "fixtures/paper.pdf",
	}}
	recordPresentationToolResult(ctx, extract, ToolResult{
		ToolCallID: extract.ID,
		Name:       extract.Name,
		Content:    "实验检测率从 74.2% 提升至 88.7%。",
	}, nil)
	fetch := ToolCall{ID: "fetch-source", Name: "web_fetch", Arguments: map[string]interface{}{
		"url": "https://example.test/paper",
	}}
	recordPresentationToolResult(ctx, fetch, ToolResult{
		ToolCallID: fetch.ID,
		Name:       fetch.Name,
		Content:    "The paper reports detection improving from 74.2% to 88.7%.",
	}, nil)
	search := ToolCall{ID: "search-lead", Name: "web_search", Arguments: map[string]interface{}{
		"query": "paper metrics",
	}}
	recordPresentationToolResult(ctx, search, ToolResult{
		ToolCallID: search.ID,
		Name:       search.Name,
		Content:    "Search snippet claims an unverified 99% result.",
	}, nil)
	state.mu.Lock()
	sourceContext = state.SourceContext
	state.mu.Unlock()
	if !strings.Contains(sourceContext, "实验检测率从 74.2% 提升至 88.7%") ||
		!strings.Contains(sourceContext, "DIRECT PUBLIC PRESENTATION EVIDENCE") {
		t.Fatalf("authorized extract/direct fetch did not extend trusted evidence: %q", sourceContext)
	}
	if strings.Contains(sourceContext, "unverified 99%") {
		t.Fatalf("discovery-only search snippet was promoted to precise presentation evidence: %q", sourceContext)
	}

	internal := ToolCall{ID: "read-builder", Name: "read_file", Arguments: map[string]interface{}{
		"path": ".openkitty/storage/build_deck.py",
	}}
	recordPresentationToolResult(ctx, internal, ToolResult{
		ToolCallID: internal.ID,
		Name:       internal.Name,
		Content:    "UNTRUSTED_INTERNAL_NUMBER=999999",
	}, nil)
	state.mu.Lock()
	sourceContext = state.SourceContext
	state.mu.Unlock()
	if strings.Contains(sourceContext, "999999") {
		t.Fatalf("source-file text expanded authorization and promoted an internal generated file: %q", sourceContext)
	}
}

func TestSamePresentationPathPreservesHiddenWorkspaceDirectory(t *testing.T) {
	absolute := `E:\openkitty\.codex-tmp\run\workspace\.openkitty\storage\build_deck.py`
	relative := `.openkitty/storage/build_deck.py`
	if !samePresentationPath(absolute, relative) {
		t.Fatalf("absolute and relative task-builder paths were not equivalent: %q vs %q", absolute, relative)
	}
}

func TestPipelinePresentationCheckpointIsStepLocalAndReusable(t *testing.T) {
	exec := &PipelineExecution{}
	first := exec.presentationStateForStep(context.Background(), 3, "请制作 PPTX")
	first.Scaffolded = true
	second := exec.presentationStateForStep(context.Background(), 3, "请制作 PPTX")
	other := exec.presentationStateForStep(context.Background(), 4, "请制作 PPTX")
	if second != first || !second.Scaffolded {
		t.Fatalf("same pipeline step lost its presentation checkpoint: first=%p second=%p", first, second)
	}
	if other == first {
		t.Fatal("presentation checkpoint leaked into another pipeline step")
	}
}

func TestPPTPolishVisualStepAlwaysUsesStrictPresentationGate(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-contract",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 report.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, "请做一份清晰的 PPT")
	if !state.Expected || !state.StrictQA || state.IntakeRequired || !state.PPTPolishContract {
		t.Fatalf("ppt-polish visual stage did not enforce its production contract: %#v", state)
	}
	if state.ContractDeckPath != ".openkitty/storage/report.pptx" || state.ContractWorkDir != ".openkitty/storage/ppt_work_pipe-contract" || state.ContractPreviewDir != ".openkitty/storage/ppt_work_pipe-contract/preview" {
		t.Fatalf("ppt-polish run-scoped paths were not pinned: %#v", state)
	}
	other := exec.presentationStateForStep(context.Background(), 1, "请做一份清晰的 PPT")
	if other.StrictQA {
		t.Fatalf("strict visual-stage contract leaked to delivery step: %#v", other)
	}
}

func TestPPTPolishToolGatePinsBuilderRenderAndVisionToCurrentRun(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-contract",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 report.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.ActivatedSkill = "ppt-art-director"
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BoundedRevision = true
	state.RevisionMode = "content"
	state.RevisionBuildLimit = 1
	state.RevisionBuildCount = 1
	state.AuditPassed = true
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)

	wrongBuilder := ToolCall{ID: "wrong-builder", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd .openkitty/runtime/builtin/presentation/ppt-art-director/assets/task-pptx && python3 build_deck.py",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, wrongBuilder); !blocked || err == nil || !strings.Contains(result.Content, "任务目录") {
		t.Fatalf("runtime-asset builder escaped current-run contract: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	wrongRender := ToolCall{ID: "wrong-render", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.py --input .openkitty/storage/report.pptx --output-dir .openkitty/storage/r21_render",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, wrongRender); !blocked || err == nil || !strings.Contains(result.Content, "精确 preview") {
		t.Fatalf("off-contract render escaped current-run preview: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	wrongVision := ToolCall{ID: "wrong-vision", Name: "vision", Arguments: map[string]interface{}{
		"path": ".openkitty/storage/r21_render/slide_01.png",
	}}
	state.Rendered = true
	state.RenderTarget = state.ContractPreviewDir
	if result, err, blocked := checkPresentationToolGate(ctx, wrongVision); !blocked || err == nil || !strings.Contains(result.Content, "本次运行 preview") {
		t.Fatalf("off-contract vision escaped current-run preview: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	correctRender := ToolCall{ID: "render", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.py --input .openkitty/storage/report.pptx --output-dir .openkitty/storage/ppt_work_pipe-contract/preview",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, correctRender); blocked || err != nil {
		t.Fatalf("exact current-run render was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationSuccess(ctx, correctRender, `{"presentation_rendered":true,"rendered_slides":8}`)
	correctVision := ToolCall{ID: "vision", Name: "vision", Arguments: map[string]interface{}{
		"path": ".openkitty/storage/ppt_work_pipe-contract/preview/slide_01.png",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, correctVision); blocked || err != nil {
		t.Fatalf("exact current-run vision was blocked: err=%v result=%q", err, result.Content)
	}
}

func TestPPTPolishBoundedRevisionFreezesBytesAfterOneBuild(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-freeze",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 frozen.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BoundedRevision = true
	state.RevisionMode = "content"
	state.RevisionBuildLimit = 1
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)

	edit := ToolCall{ID: "edit", Name: "edit_file", Arguments: map[string]interface{}{
		"file_path": state.ContractWorkDir + "/deck_spec.json", "old_string": "old", "new_string": "new",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, edit); blocked || err != nil {
		t.Fatalf("content edit before the bounded build was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationSuccess(ctx, edit, "updated deck_spec.json")
	prebuildVision := ToolCall{ID: "prebuild-vision", Name: "vision", Arguments: map[string]interface{}{
		"path": state.ContractPreviewDir + "/slide_01.png",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, prebuildVision); !blocked || err == nil || !strings.Contains(result.Content, "尚未完成成功重建") {
		t.Fatalf("old preview was accepted before the content rebuild: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	build := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd " + state.ContractWorkDir + " && python3 build_deck.py",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); blocked || err != nil {
		t.Fatalf("first bounded build was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationSuccess(ctx, build, "saved .openkitty/storage/frozen.pptx\nEXIT=0")
	if state.RevisionBuildCount != 1 {
		t.Fatalf("bounded build count=%d, want 1", state.RevisionBuildCount)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, edit); !blocked || err == nil || !strings.Contains(result.Content, "最终字节已冻结") {
		t.Fatalf("post-build edit was not frozen: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil || !strings.Contains(result.Content, "额度已用完") {
		t.Fatalf("second bounded build was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	state.RevisionMode = "freshness"
	state.RevisionBuildLimit = 0
	state.RevisionBuildCount = 0
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil || !strings.Contains(result.Content, "freshness-only") {
		t.Fatalf("freshness-only revision allowed a rebuild: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPPTPolishBoundedRevisionCountsOnlyConfirmedSuccessfulBuild(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-real-exit",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 masked.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BoundedRevision = true
	state.RevisionMode = "content"
	state.RevisionBuildLimit = 1
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)

	edit := ToolCall{ID: "edit", Name: "write_file", Arguments: map[string]interface{}{
		"path": state.ContractWorkDir + "/deck_spec.json", "content": `{"needs_customization":false}`,
	}}
	recordPresentationSuccess(ctx, edit, "wrote deck_spec.json")
	build := ToolCall{ID: "masked-build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd " + state.ContractWorkDir + " && python build_deck.py; echo BUILD_EXIT=$?; ls -l ../masked.pptx",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil || !strings.Contains(result.Content, "单个直接命令") {
		t.Fatalf("masked build wrapper was not blocked before execution: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	if presentationBuilderResultConfirmsCreation("Command Execution\nstatus: succeeded\noutput result: BUILD_EXIT=127\n-rw-r--r-- 1 root root 50000 ../masked.pptx", state.ContractDeckPath) {
		t.Fatal("masked failed shell output was accepted as a successful deck build")
	}
	if state.RevisionBuildCount != 0 || state.DeckCreated {
		t.Fatalf("blocked masked build consumed the success budget: %#v", state)
	}
	vision := ToolCall{ID: "old-vision", Name: "vision", Arguments: map[string]interface{}{
		"path": state.ContractPreviewDir + "/slide_01.png",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, vision); !blocked || err == nil || !strings.Contains(result.Content, "尚未完成成功重建") {
		t.Fatalf("old preview escaped after masked failure: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPPTPolishRevisionRequiresControlledEditAndCanonicalQAOrder(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-ordered-qa",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 ordered.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BoundedRevision = true
	state.RevisionMode = "content"
	state.RevisionBuildLimit = 1
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)
	build := ToolCall{ID: "build-before-edit", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd " + state.ContractWorkDir + " && python3 build_deck.py",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil || !strings.Contains(result.Content, "尚未发生受控编辑") {
		t.Fatalf("builder ran before the controlled edit: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	inlineEdit := ToolCall{ID: "inline-edit", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 <<'PY'\nopen('" + state.ContractWorkDir + "/deck_spec.json','w').write('{}')\nPY",
	}}
	if payload := presentationToolPayload(inlineEdit); !pptPolishShellMutatesContractFiles(inlineEdit, payload, state.ContractWorkDir, state.ContractPreviewDir) {
		t.Fatalf("inline mutation was not classified: payload=%q work=%q preview=%q", payload, state.ContractWorkDir, state.ContractPreviewDir)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, inlineEdit); !blocked || err == nil || !strings.Contains(result.Content, "内联 Python") {
		t.Fatalf("shell mutation escaped controlled editing: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	state.RevisionMutationCount = 1
	state.RevisionBuildCount = 1
	rawRender := ToolCall{ID: "raw-render", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "libreoffice --headless --convert-to pdf --outdir " + state.ContractPreviewDir + " " + state.ContractDeckPath,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, rawRender); !blocked || err == nil || !strings.Contains(result.Content, "标准脚本") {
		t.Fatalf("raw LibreOffice bypass escaped: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	canonicalRender := ToolCall{ID: "canonical-render", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.py --input " + state.ContractDeckPath + " --output-dir " + state.ContractPreviewDir,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, canonicalRender); !blocked || err == nil || !strings.Contains(result.Content, "尚未通过标准结构审计") {
		t.Fatalf("render ran before audit: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	state.AuditPassed = true
	if result, err, blocked := checkPresentationToolGate(ctx, canonicalRender); blocked || err != nil {
		t.Fatalf("canonical render after audit was blocked: err=%v result=%q", err, result.Content)
	}
	state.Rendered = true
	state.RenderTarget = state.ContractPreviewDir
	badContact := ToolCall{ID: "bad-contact", Name: "vision", Arguments: map[string]interface{}{
		"path": state.ContractPreviewDir + "/contact_sheet.png",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, badContact); !blocked || err == nil || !strings.Contains(result.Content, "contact_sheet.jpg") {
		t.Fatalf("noncanonical contact sheet escaped: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPPTPolishInitialFlowRejectsExitMaskingWrappers(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-direct-command",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 direct.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.ActivatedSkill = "ppt-art-director"
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.DeckCreated = true
	state.DeckPath = state.ContractDeckPath
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)

	for _, command := range []string{
		"cd " + state.ContractWorkDir + " && python3 build_deck.py 2>&1 | tail -40",
		"cd " + state.ContractWorkDir + " && python3 build_deck.py; echo done",
		"python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/audit_pptx.py " + state.ContractDeckPath + " --min-body-font-size 16 --min-title-font-size 35; echo audited",
	} {
		call := ToolCall{ID: "wrapped", Name: "persistent_shell", Arguments: map[string]interface{}{"command": command}}
		if result, err, blocked := checkPresentationToolGate(ctx, call); !blocked || err == nil || !strings.Contains(result.Content, "单个直接命令") {
			t.Fatalf("exit-masking wrapper escaped: command=%q blocked=%v err=%v result=%q", command, blocked, err, result.Content)
		}
	}
}

func TestPPTPolishStrictAuditCannotBeDowngradedOrReusedAfterMutation(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-strict-fonts",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 strict.pptx；所有正文至少 14pt，标题至少 26pt。",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.ArtDirectorActivated = true
	state.ActivatedSkill = "ppt-art-director"
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.DeckCreated = true
	state.DeckPath = state.ContractDeckPath
	if state.RequiredBodyFontSize != 14 || state.RequiredTitleFontSize != 26 {
		t.Fatalf("requested audit floors were not frozen into the run state: %#v", state)
	}
	ctx := context.WithValue(context.Background(), presentationProductionStateContextKey{}, state)
	auditPrefix := "python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/audit_pptx.py " + state.ContractDeckPath
	defaultAudit := ToolCall{ID: "default-audit", Name: "persistent_shell", Arguments: map[string]interface{}{"command": auditPrefix}}
	if result, err, blocked := checkPresentationToolGate(ctx, defaultAudit); !blocked || err == nil || !strings.Contains(result.Content, "--min-body-font-size") {
		t.Fatalf("default audit downgraded the requested floor: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	lowerAudit := ToolCall{ID: "lower-audit", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": auditPrefix + " --min-body-font-size 12 --min-title-font-size 24",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, lowerAudit); !blocked || err == nil || !strings.Contains(result.Content, "至少 14pt") {
		t.Fatalf("lower audit thresholds escaped: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	strictAudit := ToolCall{ID: "strict-audit", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": auditPrefix + " --min-body-font-size 14 --min-title-font-size 26",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, strictAudit); blocked || err != nil {
		t.Fatalf("requested strict audit was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationToolResult(ctx, strictAudit, ToolResult{ToolCallID: strictAudit.ID, Name: strictAudit.Name, Content: "执行失败: exit status 2"}, errors.New("exit status 2"))
	if !state.AuditFailed || state.AuditPassed {
		t.Fatalf("failed strict audit was not frozen: %#v", state)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, strictAudit); !blocked || err == nil || !strings.Contains(result.Content, "受控编辑") {
		t.Fatalf("strict audit was retried without correction: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	build := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd " + state.ContractWorkDir + " && python3 build_deck.py",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil || !strings.Contains(result.Content, "先用受控编辑") {
		t.Fatalf("builder ran after audit failure without an edit: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	edit := ToolCall{ID: "edit", Name: "edit_file", Arguments: map[string]interface{}{
		"file_path": state.ContractWorkDir + "/deck_spec.json", "old_string": "old", "new_string": "new",
	}}
	recordPresentationSuccess(ctx, edit, "updated deck_spec.json")
	if !state.SourceDirty {
		t.Fatalf("controlled source edit did not invalidate QA evidence: %#v", state)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, strictAudit); !blocked || err == nil || !presentationContainsAny(result.Content, "重新构建", "生成新字节") {
		t.Fatalf("audit ran against dirty source: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, build); blocked || err != nil {
		t.Fatalf("corrected direct builder was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationSuccess(ctx, build, "saved .openkitty/storage/strict.pptx\nEXIT=0")
	if state.SourceDirty || state.AuditFailed {
		t.Fatalf("successful corrected build did not clear dirty/failed state: %#v", state)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, strictAudit); blocked || err != nil {
		t.Fatalf("strict audit after corrected build was blocked: err=%v result=%q", err, result.Content)
	}
	recordPresentationSuccess(ctx, strictAudit, `{"ok":true,"path":".openkitty/storage/strict.pptx","slide_count":8,"fatal_errors":[]}`)
	if !state.AuditPassed {
		t.Fatalf("successful strict audit was not recorded: %#v", state)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, defaultAudit); !blocked || err == nil || !strings.Contains(result.Content, "--min-body-font-size") {
		t.Fatalf("default audit became legal after strict success: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	render := ToolCall{ID: "render", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.py --input " + state.ContractDeckPath + " --output-dir " + state.ContractPreviewDir,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, render); blocked || err != nil {
		t.Fatalf("render after strict audit was blocked: err=%v result=%q", err, result.Content)
	}
}

func TestPPTPolishBuilderRunsOnlyAfterDeckSpecGrounding(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "pipe-prebuild-grounding",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Input:             "目标能力架构：需求入口→结果/视觉/数字 QA→交付与反馈。90 天目标：任务成功率 90%。责任人未指定。文件名 grounded.pptx。",
		Steps: []StepResult{
			{Name: "素材读取", Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n未读取外部网页"},
			{Name: "视觉润色"},
		},
	}
	base := withResolvedWorkspace(context.Background(), workspace)
	state := exec.presentationStateForStep(base, 1, exec.Input)
	state.ArtDirectorActivated = true
	state.ActivatedSkill = "ppt-art-director"
	state.Scaffolded = true
	state.BuilderPath = state.ContractWorkDir + "/build_deck.py"
	state.BuilderRead = true
	state.BuilderCustomized = true
	ctx := context.WithValue(base, presentationProductionStateContextKey{}, state)
	workDir := filepath.Join(workspace, filepath.FromSlash(state.ContractWorkDir))
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(workDir, "deck_spec.json")
	bad := `{"evidence_policy":"source-bound","art_direction":{"visual_thesis":"evidence-led operating model","composition_strategy":"flow then exact acceptance table","asset_strategy":"use only user-provided process and metrics","palette_strategy":"neutral canvas with one evidence accent","typography_strategy":"30pt claims and 18pt body copy","background_rhythm":"contrast bookends with quiet evidence pages"},"evidence_ledger":[{"id":"U1","source":"source: user","locator":"user request","excerpt":"90 天目标：任务成功率 90%"}],"slides":[{"kind":"process","items":[{"title":"需求入口"},{"title":"质检"},{"title":"交付与反馈"}]},{"kind":"table","narrative_job":"state the measurable acceptance contract","focal_anchor":"90% target with explicit ownership","evidence_form":"acceptance table","composition_family":"metric ledger","source_note":"来源：用户陈述","evidence_refs":["U1"],"headers":["指标","90天","责任人"],"rows":[["任务成功率","90%","产品负责人"]]}]}`
	if err := os.WriteFile(specPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	build := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "cd " + state.ContractWorkDir + " && python3 build_deck.py",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); !blocked || err == nil ||
		!strings.Contains(result.Content, "构建前内容保真校验失败") || !strings.Contains(result.Content, "结果/视觉/数字 QA") || !strings.Contains(result.Content, "产品负责人") {
		t.Fatalf("ungrounded deck spec reached the builder: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	good := `{"evidence_policy":"source-bound","art_direction":{"visual_thesis":"evidence-led operating model","composition_strategy":"flow then exact acceptance table","asset_strategy":"use only user-provided process and metrics","palette_strategy":"neutral canvas with one evidence accent","typography_strategy":"30pt claims and 18pt body copy","background_rhythm":"contrast bookends with quiet evidence pages"},"evidence_ledger":[{"id":"U1","source":"source: user","locator":"user request","excerpt":"90 天目标：任务成功率 90%"}],"slides":[{"kind":"process","items":[{"title":"需求入口"},{"title":"结果/视觉/数字 QA"},{"title":"交付与反馈"}]},{"kind":"table","narrative_job":"state the measurable acceptance contract","focal_anchor":"90% target with explicit ownership","evidence_form":"acceptance table","composition_family":"metric ledger","source_note":"来源：用户陈述","evidence_refs":["U1"],"headers":["指标","90天","责任人"],"rows":[["任务成功率","90%","待确认"]]}]}`
	if err := os.WriteFile(specPath, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, build); blocked || err != nil {
		t.Fatalf("grounded deck spec was blocked: err=%v result=%q", err, result.Content)
	}
}

func TestPresentationBuilderObservationAndAuditCLIClassification(t *testing.T) {
	observation := ToolCall{ID: "observe", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "wc -l .openkitty/storage/run/build_deck.py 2>&1",
	}}
	if isPresentationBuilderAction(observation, extractAction(observation.Arguments)) {
		t.Fatal("read-only builder observation was classified as a build execution")
	}
	execution := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/storage/run/build_deck.py",
	}}
	if !isPresentationBuilderAction(execution, extractAction(execution.Arguments)) {
		t.Fatal("direct builder execution was not classified as a build")
	}
	if !presentationAuditUsesUnsupportedInputFlag("python3 scripts/audit_pptx.py --input final.pptx --min-body-font-size 14") {
		t.Fatal("unsupported audit --input form was not detected")
	}
	if presentationAuditUsesUnsupportedInputFlag("python3 scripts/audit_pptx.py final.pptx --min-body-font-size 14") {
		t.Fatal("canonical positional audit form was rejected")
	}
}

func recordPresentationSuccess(ctx context.Context, call ToolCall, content string) {
	recordPresentationToolResult(ctx, call, ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil)
}

func activatePresentationArtDirector(ctx context.Context) {
	recordPresentationSuccess(ctx, ToolCall{
		ID:   "skill-art",
		Name: "use_skill",
		Arguments: map[string]interface{}{
			"skill_name": "ppt-art-director",
		},
	}, "技能: ppt-art-director\n运行时目录: C:\\runtime\\ppt-art-director")
}

func TestPresentationGateBlocksRepeatedScaffoldAndBuilderRead(t *testing.T) {
	ctx := presentationTestContext("请制作一份融资路演 PPTX")
	activatePresentationArtDirector(ctx)
	scaffold := ToolCall{ID: "scaffold", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python scaffold_task_pptx.py --builder work/build_deck.py --output result.pptx",
	}}
	recordPresentationSuccess(ctx, scaffold, `{"builder":"work/build_deck.py","output":"result.pptx"}`)
	if result, err, blocked := checkPresentationToolGate(ctx, scaffold); !blocked || err == nil || !strings.Contains(result.Content, "已经成功创建") {
		t.Fatalf("repeated scaffold was not stopped at the verified checkpoint: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	helperRead := ToolCall{ID: "helper-read", Name: "read_file", Arguments: map[string]interface{}{"path": "work/pptx_design.py"}}
	if result, err, blocked := checkPresentationToolGate(ctx, helperRead); !blocked || err == nil || !strings.Contains(result.Content, "不要读取或改写") {
		t.Fatalf("scaffold helper inspection was not stopped: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	read := ToolCall{ID: "read", Name: "read_file", Arguments: map[string]interface{}{"path": "work/build_deck.py"}}
	recordPresentationSuccess(ctx, read, "builder source")
	if result, err, blocked := checkPresentationToolGate(ctx, read); !blocked || err == nil || !strings.Contains(result.Content, "已在本步骤读取过") {
		t.Fatalf("repeated builder read was not stopped: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	boundedRecovery := ToolCall{ID: "bounded-builder-reread", Name: "read_file", Arguments: map[string]interface{}{
		"path": "work/build_deck.py", "lines_range": "680-740",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, boundedRecovery); blocked || err != nil {
		t.Fatalf("bounded builder recovery read was blocked after compaction: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationScaffoldSuccessRequiresMaterializedFiles(t *testing.T) {
	workspace := t.TempDir()
	ctx := withPresentationProductionState(withResolvedWorkspace(context.Background(), workspace), "请制作一份12页部门管理汇报PPTX")
	activatePresentationArtDirector(ctx)
	scaffold := ToolCall{ID: "scaffold", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python scaffold_task_pptx.py --builder runs/D12/build_deck.py --output runs/D12/review.pptx",
	}}
	recordPresentationSuccess(ctx, scaffold, "exit=0")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Scaffolded {
		t.Fatalf("zero-exit scaffold without files was trusted: %#v", state)
	}

	dir := filepath.Join(workspace, "runs", "D12")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"build_deck.py", "deck_spec.json", "visual_plan.json", "pptx_design.py"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("materialized"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	recordPresentationSuccess(ctx, scaffold, "exit=0")
	if !state.Scaffolded || !samePresentationPath(state.BuilderPath, "runs/D12/build_deck.py") {
		t.Fatalf("materialized scaffold was not recorded: %#v", state)
	}
}

func TestPresentationScaffoldSuccessAcceptsVerifiedAbsoluteReceiptPath(t *testing.T) {
	workspace := t.TempDir()
	ctx := withPresentationProductionState(withResolvedWorkspace(context.Background(), workspace), "请制作一份 12 页管理层 PPTX")
	activatePresentationArtDirector(ctx)
	scaffold := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python "C:/runtime/scaffold_task_pptx.py" --builder ".openkitty/storage/build_deck.py" --output "runs/D12/review.pptx"`,
	}}

	// The CLI can execute from a nested task workspace while the production
	// state carries a broader containing workspace. In that case the command's
	// relative builder path alone is ambiguous, but the trusted scaffold receipt
	// reports the exact materialized path.
	taskWorkspace := filepath.Join(workspace, "jobs", "D12", "workspace")
	dir := filepath.Join(taskWorkspace, ".openkitty", "storage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"build_deck.py", "deck_spec.json", "visual_plan.json", "pptx_design.py"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("materialized"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := json.Marshal(map[string]string{"builder": filepath.Join(dir, "build_deck.py")})
	if err != nil {
		t.Fatal(err)
	}
	result := ToolResult{Content: "Command Execution\nstatus: succeeded\noutput result: " + string(receipt)}
	recordPresentationToolResult(ctx, scaffold, result, nil)

	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Scaffolded {
		t.Fatalf("verified absolute scaffold receipt was not recorded: %#v", state)
	}
	if !samePresentationPath(state.BuilderPath, ".openkitty/storage/build_deck.py") {
		t.Fatalf("command-local builder path was not preserved: %#v", state)
	}
}

func TestPresentationScaffoldReceiptCannotEscapeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	ctx := withPresentationProductionState(withResolvedWorkspace(context.Background(), workspace), "请制作一份 12 页管理层 PPTX")
	activatePresentationArtDirector(ctx)
	scaffold := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python "C:/runtime/scaffold_task_pptx.py" --builder ".openkitty/storage/build_deck.py" --output "runs/D12/review.pptx"`,
	}}
	for _, name := range []string{"build_deck.py", "deck_spec.json", "visual_plan.json", "pptx_design.py"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("materialized"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := json.Marshal(map[string]string{"builder": filepath.Join(outside, "build_deck.py")})
	if err != nil {
		t.Fatal(err)
	}
	recordPresentationToolResult(ctx, scaffold, ToolResult{Content: "status: succeeded\noutput result: " + string(receipt)}, nil)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Scaffolded {
		t.Fatalf("out-of-workspace scaffold receipt was trusted: %#v", state)
	}
}

func TestPresentationGateRequiresStandaloneScaffoldCommand(t *testing.T) {
	ctx := presentationTestContext("请制作一份12页部门管理汇报PPTX")
	activatePresentationArtDirector(ctx)
	wrapped := ToolCall{ID: "wrapped-scaffold", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "mkdir -p runs/D12 && python scaffold_task_pptx.py --builder runs/D12/build_deck.py --output runs/D12/review.pptx",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, wrapped); !blocked || err == nil || !strings.Contains(result.Content, "作为单独命令") {
		t.Fatalf("wrapped scaffold command was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	standalone := ToolCall{ID: "standalone-scaffold", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python scaffold_task_pptx.py --builder runs/D12/build_deck.py --output runs/D12/review.pptx",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, standalone); blocked || err != nil {
		t.Fatalf("standalone scaffold command was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateRejectsHandAuthoredFactModel(t *testing.T) {
	ctx := presentationTestContext("请根据销售CSV制作一份12页部门管理汇报PPTX")
	activatePresentationArtDirector(ctx)
	handAuthored := ToolCall{ID: "fact-model-write", Name: "write_file", Arguments: map[string]interface{}{
		"path": "runs/D12/fact_model.json", "content": `{"total": 98000}`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, handAuthored); !blocked || err == nil || !strings.Contains(result.Content, "确定性数据分析") {
		t.Fatalf("hand-authored fact model was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	script := ToolCall{ID: "fact-model-script", Name: "write_file", Arguments: map[string]interface{}{
		"path": "runs/D12/build_fact_model.py", "content": "print('deterministic')",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, script); blocked || err != nil {
		t.Fatalf("deterministic fact-model script was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestValidatePresentationFactModelRejectsBrokenRowReconciliation(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "fact_model.json")
	if err := os.WriteFile(broken, []byte(`{
  "raw_rows": 10,
  "included_rows": 7,
  "excluded": [{"id":"Q1-009"}],
  "duplicate_discarded": [{"id":"Q1-004"}],
  "reconciliation": {"raw_rows": 10, "sum_check": true},
  "totals": {"revenue_net": 87500},
  "region": {"east":{"revenue":31300},"north":{"revenue":33800},"south":{"revenue":20400}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(broken); err == nil || !strings.Contains(err.Error(), "row reconciliation failed") {
		t.Fatalf("broken fact model was accepted: %v", err)
	}

	valid := filepath.Join(dir, "valid_fact_model.json")
	if err := os.WriteFile(valid, []byte(`{
  "raw_rows": 10,
  "included_rows": [{"id":"Q1-001"},{"id":"Q1-002"},{"id":"Q1-003"},{"id":"Q1-004"},{"id":"Q1-005"},{"id":"Q1-006"},{"id":"Q1-007"},{"id":"Q1-008"}],
  "excluded_rows": [{"id":"Q1-009","reason":"cancelled by source policy"}],
  "duplicate_pairs": [{"id":"Q1-004","kept_raw":{"cost":"7900"},"discarded_raw":{"cost":""}}],
  "reconciliation": {"raw_rows": 10, "sum_check": true},
  "totals": {"revenue":98000,"cost":63300,"gross_profit":34700,"gross_margin":0.3540816327},
  "by_region": {"east":{"revenue":43800,"cost":28000},"north":{"revenue":33800,"cost":21900},"south":{"revenue":20400,"cost":13400}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(valid); err != nil {
		t.Fatalf("valid fact model was rejected: %v", err)
	}
}

func TestValidatePresentationFactModelRejectsRetainedDuplicateMissingCostContradiction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fact_model.json")
	if err := os.WriteFile(path, []byte(`{
  "raw_row_count": 10,
  "included_rows": [{"order_id":"Q1-001"},{"order_id":"Q1-002"},{"order_id":"Q1-003"},{"order_id":"Q1-004"},{"order_id":"Q1-005"},{"order_id":"Q1-006"},{"order_id":"Q1-007"},{"order_id":"Q1-008"}],
  "excluded_rows": [{"order_id":"Q1-009","reason":"cancelled"}],
  "duplicate_pairs": [{
    "order_id":"Q1-004",
    "kept_raw":{"order_id":"Q1-004","cost":"7900"},
    "discarded_raw":{"order_id":"Q1-004","cost":""}
  }],
  "row_reconciliation": {"raw":10,"included":8,"sum_check":true},
  "missing_cost_rows": [{"order_id":"Q1-004","revenue":12500}],
  "totals_after_cleaning": {"revenue":98000,"cost":63300,"gross_profit":34700,"gross_margin":0.3540816327},
  "region_aggregates": {
    "east":{"revenue":43800,"cost":28000,"missing_cost_rows":0},
    "north":{"revenue":33800,"cost":21900,"missing_cost_rows":0},
    "south":{"revenue":20400,"cost":13400,"missing_cost_rows":0}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(path); err == nil || !strings.Contains(err.Error(), "contradicts retained duplicate row") {
		t.Fatalf("contradictory retained duplicate fact model was accepted: %v", err)
	}
}

func writePresentationFactSourceFixture(t *testing.T, workspace string) string {
	t.Helper()
	dataDir := filepath.Join(workspace, "fixtures", "data")
	storage := filepath.Join(workspace, ".openkitty", "storage")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "policy.md"), []byte("cancelled rows do not count; refund negative values remain; duplicate order_id keeps the most complete row; invalid dates enter the anomaly list."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "sales.csv"), []byte(`order_id,date,region,product,revenue,cost,customer_id,status
Q1-001,2026-01-03,east,Pro,"12,500",8000,C001,paid
Q1-002,2026/01/04,south,Lite,9200,6100,C002,paid
Q1-003,2026-02-30,north,Pro,15000,9800,C003,paid
Q1-004,2026-02-11,east,Pro,12500,,C001,paid
Q1-004,2026-02-11,east,Pro,12500,7900,C001,paid
Q1-005,2026-02-18,south,Lite,-2000,-1300,C004,refund
Q1-006,2026-03-01,north,Max,"18,800",12100,C005,paid
Q1-007,2026-03-07,east,Max,18800,12100,,paid
Q1-008,07-03-2026,south,Pro,13200,8600,C006,paid
Q1-009,2026-03-19,north,Lite,8700,5900,C007,cancelled
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "targets.csv"), []byte(`region,q1_revenue_target,q1_gross_margin_target
east,36000,0.36
south,22000,0.34
north,32000,0.35
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return storage
}

func TestValidatePresentationFactModelRejectsSemanticallyWrongSourceCleaning(t *testing.T) {
	workspace := t.TempDir()
	storage := writePresentationFactSourceFixture(t, workspace)
	path := filepath.Join(storage, "fact_model.json")
	if err := os.WriteFile(path, []byte(`{
  "source_files":{"sales":"fixtures/data/sales.csv","targets":"fixtures/data/targets.csv","policy":"fixtures/data/policy.md"},
  "raw_row_count":10,
  "included_rows":[{"order_id":"Q1-001"},{"order_id":"Q1-002"},{"order_id":"Q1-004"},{"order_id":"Q1-005"},{"order_id":"Q1-006"},{"order_id":"Q1-007"},{"order_id":"Q1-008"}],
  "excluded_rows":[{"order_id":"Q1-003","reason":"invalid date"},{"order_id":"Q1-009","reason":"cancelled"}],
  "duplicate_pairs":[{"order_id":"Q1-004","kept_raw":{"cost":"7900"},"discarded_raw":{"cost":""}}],
  "reconciliation":{"raw_row_count":10,"sum_check":true},
  "totals":{"revenue":83000,"cost":53500,"gross_profit":29500,"gross_margin":0.3554216867},
  "by_region":{"east":{"revenue":43800,"cost":28000},"south":{"revenue":20400,"cost":13400},"north":{"revenue":18800,"cost":12100}},
  "targets":{"east":{"revenue_target":0,"gross_margin_target":0},"south":{"revenue_target":0,"gross_margin_target":0},"north":{"revenue_target":0,"gross_margin_target":0}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(path); err == nil || !strings.Contains(err.Error(), "included_rows IDs disagree") {
		t.Fatalf("source-semantic cleaning error was accepted: %v", err)
	}
}

func TestValidatePresentationFactModelChecksSourceTotalsAndTargets(t *testing.T) {
	workspace := t.TempDir()
	storage := writePresentationFactSourceFixture(t, workspace)
	path := filepath.Join(storage, "fact_model.json")
	model := `{
  "source_files":{"sales":"fixtures/data/sales.csv","targets":"fixtures/data/targets.csv","policy":"fixtures/data/policy.md"},
  "raw_row_count":10,
  "included_rows":[{"order_id":"Q1-001"},{"order_id":"Q1-002"},{"order_id":"Q1-003"},{"order_id":"Q1-004"},{"order_id":"Q1-005"},{"order_id":"Q1-006"},{"order_id":"Q1-007"},{"order_id":"Q1-008"}],
  "excluded_rows":[{"order_id":"Q1-009","reason":"cancelled"}],
  "duplicate_pairs":[{"order_id":"Q1-004","kept_raw":{"cost":"7900"},"discarded_raw":{"cost":""}}],
  "reconciliation":{"raw_row_count":10,"sum_check":true},
  "totals":{"revenue":98000,"cost":63300,"gross_profit":34700,"gross_margin":0.3540816327},
  "by_region":{"east":{"revenue":43800,"cost":28000},"south":{"revenue":20400,"cost":13400},"north":{"revenue":33800,"cost":21900}},
  "targets":{"east":{"revenue_target":36000,"gross_margin_target":0.36},"south":{"revenue_target":22000,"gross_margin_target":0.34},"north":{"revenue_target":32000,"gross_margin_target":0.35}}
}`
	if err := os.WriteFile(path, []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(path); err != nil {
		t.Fatalf("source-grounded fact model was rejected: %v", err)
	}
	badTargets := strings.Replace(model, `"revenue_target":36000`, `"revenue_target":0`, 1)
	if err := os.WriteFile(path, []byte(badTargets), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePresentationFactModel(path); err == nil || !strings.Contains(err.Error(), "target east disagrees with source") {
		t.Fatalf("zeroed source target was accepted: %v", err)
	}
}

func TestPresentationFactModelIssueFindsWorkspaceStorageModel(t *testing.T) {
	workspace := t.TempDir()
	storage := filepath.Join(workspace, ".openkitty", "storage")
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "fact_model.json"), []byte(`{
  "raw_rows":10,
  "included_rows":7,
  "excluded":[{"id":"Q1-009"}],
  "duplicate_discarded":[{"id":"Q1-004"}],
  "reconciliation":{"raw_rows":10}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := withResolvedWorkspace(context.Background(), workspace)
	issue := presentationFactModelIssue(ctx, "runs/D12/build_deck.py")
	if !strings.Contains(issue, "row reconciliation failed") {
		t.Fatalf("workspace storage fact model was not validated: %q", issue)
	}
}

func TestPresentationCurrentTurnExecutionPromptAllowsAutonomousWorkflow(t *testing.T) {
	ctx := presentationTestContext("请生成一份管理层汇报 PPT")
	prompt := presentationCurrentTurnExecutionPrompt(ctx)
	for _, want := range []string{
		"CURRENT-TURN",
		`Load "ppt-art-director" exactly once`,
		"evidence_policy",
		"asset_strategy",
		"palette_strategy",
		"content-driven page design",
		"Create a real audience-facing PPTX",
		"Prior-turn",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("current-turn execution prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPresentationCurrentTurnExecutionPromptSkipsExistingDeckQA(t *testing.T) {
	ctx := presentationTestContext("检查 existing.pptx 是否有重叠")
	if prompt := presentationCurrentTurnExecutionPrompt(ctx); prompt != "" {
		t.Fatalf("existing-deck QA must not be forced through a new scaffold: %s", prompt)
	}
}

func TestPresentationExplicitIntakeFirstAsksInsteadOfBuilding(t *testing.T) {
	ctx := presentationTestContext("请制作精美可编辑 PPT，先确认受众、用途和核心观点。")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.IntakeRequired || state.Expected {
		t.Fatalf("explicit intake-first request was routed into production: %#v", state)
	}
	if issues := presentationBlockingIssues(ctx); len(issues) != 0 {
		t.Fatalf("intake turn must not require a PPTX: %v", issues)
	}
	prompt := presentationCurrentTurnExecutionPrompt(ctx)
	for _, want := range []string{"INTAKE REQUIRED", "audience", "use/setting", "core viewpoint", "Do not infer defaults"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("intake prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPresentationIntakeAnswerResumesProduction(t *testing.T) {
	original := "请制作精美可编辑 PPT，先确认受众、用途和核心观点。"
	base := context.WithValue(context.Background(), "history_messages", []map[string]interface{}{
		{"role": "user", "content": original},
		{"role": "assistant", "content": "请确认：受众是谁？用途和会议场景是什么？核心观点是什么？"},
	})
	ctx := withPresentationProductionState(base, "受众是基金经理，用于次日晨会，核心观点是震荡分化与防御板块占优。")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.IntakeRequired || !state.Expected || state.Route != "level3-ppt-master" {
		t.Fatalf("answered presentation intake did not resume production: %#v", state)
	}
}

func TestPresentationQARepairPromptRecoversMissingCurrentTurnSkill(t *testing.T) {
	prompt := buildPresentationQARepairPrompt([]string{"缺少 PPTX"})
	content, _ := prompt["content"].(string)
	for _, want := range []string{"Use ppt-art-director", "Preserve the exact Runtime Directory", "Do not repeat a failed call", "real PPTX"} {
		if !strings.Contains(content, want) {
			t.Fatalf("repair prompt missing %q: %s", want, content)
		}
	}
}

func TestPresentationQARepairPromptDoesNotReloadSuccessfulSkill(t *testing.T) {
	ctx := presentationTestContext("请生成一份管理层汇报 PPT")
	activatePresentationArtDirector(ctx)
	prompt := buildPresentationQARepairPromptForContext(ctx, []string{"未形成可交付的 PPTX"})
	content, _ := prompt["content"].(string)
	for _, want := range []string{"already recorded", "ppt-art-director", "Do not call it again"} {
		if !strings.Contains(content, want) {
			t.Fatalf("activated repair prompt missing %q: %s", want, content)
		}
	}
	if strings.Contains(content, "No successful current-turn") {
		t.Fatalf("activated repair prompt contradicted the host receipt: %s", content)
	}
}

func TestPresentationStrictQARepairSeparatesNativeRenderAndVision(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.DeckPath = `runs\A23\report.pptx`
	state.DeckCreated = true

	first := buildPresentationQARepairPromptForContext(ctx, []string{"未获得真实 PowerPoint 页面渲染证据", "未获得渲染页面的像素级视觉检查证据"})
	firstContent, _ := first["content"].(string)
	wantedRenderer := "render_pptx.py"
	if runtime.GOOS == "windows" {
		wantedRenderer = "render_pptx.ps1"
	}
	for _, want := range []string{"two sequential steps", wantedRenderer, "contact_sheet.jpg", "Rendering alone is not visual inspection"} {
		if !strings.Contains(firstContent, want) {
			t.Fatalf("render-stage repair missing %q: %s", want, firstContent)
		}
	}

	state.Rendered = true
	state.RenderTarget = `runs\A23\rendered_pages`
	second := buildPresentationQARepairPromptForContext(ctx, []string{"未获得渲染页面的像素级视觉检查证据"})
	secondContent, _ := second["content"].(string)
	for _, want := range []string{"Do not render again", "Call the vision tool now", "contact_sheet.jpg", "clipping", "overlap"} {
		if !strings.Contains(secondContent, want) {
			t.Fatalf("vision-stage repair missing %q: %s", want, secondContent)
		}
	}
}

func TestPresentationQARendererSelectionIsCrossPlatform(t *testing.T) {
	for _, test := range []struct {
		goos, script, runner string
	}{
		{"windows", "render_pptx.ps1", "powershell"},
		{"darwin", "render_pptx.py", "python3"},
		{"linux", "render_pptx.py", "python3"},
	} {
		script, runner, ok := presentationQARendererForOS(test.goos)
		if !ok || script != test.script || runner != test.runner {
			t.Fatalf("renderer mismatch for %s: ok=%v script=%q runner=%q", test.goos, ok, script, runner)
		}
	}
	if _, _, ok := presentationQARendererForOS("plan9"); ok {
		t.Fatal("unsupported platform received a presentation renderer")
	}
}

func TestPresentationUnixRendererIsTrustedOnlyFromMaterializedRuntime(t *testing.T) {
	trusted := ToolCall{Name: "cli", Arguments: map[string]interface{}{
		"command": `python3 '/workspace/.openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.py' --input report.pptx --output-dir preview`,
	}}
	if !isTrustedLocalPresentationVisualQA(trusted) {
		t.Fatal("bundled Unix renderer was not recognized as trusted")
	}
	untrusted := ToolCall{Name: "cli", Arguments: map[string]interface{}{
		"command": `python3 './scripts/render_pptx.py' --input report.pptx --output-dir preview`,
	}}
	if isTrustedLocalPresentationVisualQA(untrusted) {
		t.Fatal("workspace-local Unix renderer was allowed to forge a visual-QA receipt")
	}
}

func TestPresentationVisualReceiptRequiresActualVision(t *testing.T) {
	receipt := `{"presentation_visual_inspected":true,"all_slides_inspected":true,"visual_qa_passed":true}`
	trusted := ToolCall{Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}
	if hasPresentationVisualEvidence(trusted, receipt) {
		t.Fatal("bundled renderer pixel integrity was mistaken for actual visual inspection")
	}
	untrusted := ToolCall{Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File runs/render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}
	if hasPresentationVisualEvidence(untrusted, receipt) {
		t.Fatal("workspace script was allowed to forge a visual-QA receipt")
	}
	vision := ToolCall{Name: "vision", Arguments: map[string]interface{}{"path": "preview/contact_sheet.jpg"}}
	if !hasPresentationVisualEvidence(vision, "所有页面检查完成，未发现裁切或重叠。") {
		t.Fatal("real vision inspection was rejected")
	}
}

func TestBundledRendererRecordsRenderButStillRequiresVisualInspection(t *testing.T) {
	ctx := presentationTestContext("请生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.DeckCreated = true
	state.DeckPath = "runs/A23/report.pptx"
	state.DeckSlideCount = 10
	state.AuditPassed = true

	call := ToolCall{Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/render_pptx.ps1 -InputPptx runs/A23/report.pptx -OutputDir runs/A23/preview`,
	}}
	receipt := `{"ok":true,"input":"runs/A23/report.pptx","output_dir":"runs/A23/preview","slide_count":10,"contact_sheet":"runs/A23/preview/contact_sheet.jpg","presentation_visual_inspected":true,"all_slides_inspected":true,"visual_qa_passed":true}`
	recordPresentationSuccess(ctx, call, receipt)
	if !state.Rendered || state.FinalVisualInspected || state.VisualCoverageComplete {
		t.Fatalf("bundled renderer did not stay separate from visual inspection: %#v", state)
	}
	if issues := presentationBlockingIssues(ctx); len(issues) == 0 {
		t.Fatalf("missing actual vision was not kept as a blocking issue: %v", issues)
	}
}

func TestPresentationContactSheetAloneNeverCompletesSubstantiveSixSlideQA(t *testing.T) {
	ctx := presentationTestContext("生成 6 页正式对外 PPT")
	state := presentationProductionStateFromContext(ctx)
	state.DeckCreated = true
	state.DeckPath = "runs/A24/report.pptx"
	state.DeckSlideCount = 6
	state.AuditPassed = true
	state.Rendered = true
	state.RenderTarget = "runs/A24/rendered_pages"
	state.RenderedSlideCount = 6
	state.FinalVisualInspected = true
	state.VisualCoverageComplete = true
	state.VisualInspectedImages[normalizedPresentationPath("runs/A24/rendered_pages/contact_sheet.jpg")] = true

	issues := strings.Join(presentationBlockingIssues(ctx), " | ")
	if !strings.Contains(issues, "0/6") || !strings.Contains(issues, "contact sheet") {
		t.Fatalf("contact sheet incorrectly replaced individual six-slide vision: %s", issues)
	}
	if !strings.Contains(issues, "审美评分账本") {
		t.Fatalf("missing aesthetic ledger was not blocking: %s", issues)
	}
}

func TestPresentationAestheticQARequiresIndividualVisionThenUnlocks(t *testing.T) {
	ctx := presentationTestContext("生成 6 页正式对外 PPT")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.DeckCreated = true
	state.DeckPath = "runs/A25/report.pptx"
	state.DeckSlideCount = 6
	state.AuditPassed = true
	state.Rendered = true
	state.RenderTarget = "runs/A25/rendered_pages"
	state.RenderedSlideCount = 6
	state.FinalVisualInspected = true
	state.VisualCoverageComplete = true

	writeLedger := ToolCall{ID: "write-aesthetic", Name: "write_file", Arguments: map[string]interface{}{
		"path": "runs/A25/aesthetic_qa.json", "content": `{}`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, writeLedger); !blocked || err == nil || !strings.Contains(result.Content, "0/6") {
		t.Fatalf("aesthetic ledger was allowed before individual vision: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	for page := 1; page <= 6; page++ {
		path := fmt.Sprintf("runs/A25/rendered_pages/slide_%02d.png", page)
		state.VisualInspectedImages[normalizedPresentationPath(path)] = true
	}
	if result, err, blocked := checkPresentationToolGate(ctx, writeLedger); blocked || err != nil {
		t.Fatalf("vision-grounded aesthetic ledger write was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	validator := ToolCall{ID: "validate-aesthetic", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\workspace\.openkitty\runtime\builtin\presentation\ppt-art-director\scripts\validate_aesthetic_qa.py --qa runs/A25/aesthetic_qa.json --deck runs/A25/report.pptx --render-dir runs/A25/rendered_pages --expected-slides 6`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, validator); blocked || err != nil {
		t.Fatalf("canonical aesthetic validator was blocked after full vision: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, validator, `{"aesthetic_qa_passed":true,"deck":"runs/A25/report.pptx","slide_count":6}`)
	if !state.AestheticQAPassed {
		t.Fatalf("canonical aesthetic receipt was not recorded: %#v", state)
	}
	if issues := presentationBlockingIssues(ctx); len(issues) != 0 {
		t.Fatalf("complete aesthetic workflow remained blocked: %v", issues)
	}
}

func TestPresentationRebuildInvalidatesAestheticQA(t *testing.T) {
	state := &presentationProductionState{
		AuditPassed: true, Rendered: true, FinalVisualInspected: true,
		VisualCoverageComplete: true, AestheticQAPassed: true,
		AestheticQAEvidence: "receipt",
	}
	invalidatePresentationDownstreamEvidence(state)
	if state.AestheticQAPassed || state.AestheticQAEvidence != "" {
		t.Fatalf("rebuild left stale aesthetic evidence: %#v", state)
	}
}

func TestPresentationPremiumRouteRequiresPPTMasterBeforeScaffold(t *testing.T) {
	ctx := presentationTestContext("制作一份精美的董事会 PPT")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder runs/A26/build_deck.py --output runs/A26/report.pptx`,
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "use_skill ppt-master") {
		t.Fatalf("premium request started a conservative scaffold: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationStrictQAInjectsTrustedNativeRendererOnce(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the deterministic native PowerPoint renderer is Windows-only")
	}
	workspace := t.TempDir()
	ctx := withResolvedWorkspace(presentationTestContext("Create a 10 slide PPT and strictly render every page"), workspace)
	runtimeDir := filepath.Join(workspace, ".openkitty", "runtime", "builtin", "presentation", "ppt-art-director")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "scripts", "render_pptx.ps1"), []byte("param()"), 0o644); err != nil {
		t.Fatal(err)
	}
	deckPath := filepath.Join(workspace, "runs", "report.pptx")
	writePresentationPPTXWithSlides(t, deckPath, 10)

	recordPresentationSuccess(ctx, ToolCall{ID: "skill", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "ppt-art-director",
	}}, "Runtime Directory: .openkitty/runtime/builtin/presentation/ppt-art-director")
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.CandidatePaths["runs/report.pptx"] = struct{}{}
	state.AuditPassed = true
	state.AuditEvidence = "test:canonical-audit"
	// A prior renderer receipt without trusted full-page visual coverage must
	// not suppress the bundled render-and-pixel-QA action.
	state.Rendered = true
	state.RenderTarget = "runs/preview"
	state.mu.Unlock()

	call, ok := buildPresentationNativeQAToolCall(ctx, 7)
	if !ok {
		t.Fatal("strict QA did not inject the trusted native renderer")
	}
	command, _ := call.Arguments["command"].(string)
	for _, want := range []string{"render_pptx.ps1", deckPath, filepath.Join(workspace, "runs", "rendered_pages")} {
		if !strings.Contains(strings.ToLower(command), strings.ToLower(want)) {
			t.Fatalf("native renderer command missing %q: %s", want, command)
		}
	}
	if !isTrustedLocalPresentationVisualQA(call) {
		t.Fatalf("host-injected renderer is not recognized as trusted: %#v", call)
	}
	if _, repeated := buildPresentationNativeQAToolCall(ctx, 8); repeated {
		t.Fatal("native QA was injected more than once for the same deck revision")
	}
}

func TestPresentationStrictQAPrioritizesReadyHostRendererOverModelCalls(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the deterministic native PowerPoint renderer is Windows-only")
	}
	workspace := t.TempDir()
	ctx := withResolvedWorkspace(presentationTestContext("Create a 3 slide PPT and strictly render every page"), workspace)
	runtimeDir := filepath.Join(workspace, ".openkitty", "runtime", "builtin", "presentation", "ppt-art-director")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "scripts", "render_pptx.ps1"), []byte("param()"), 0o644); err != nil {
		t.Fatal(err)
	}
	deckPath := filepath.Join(workspace, "runs", "report.pptx")
	writePresentationPPTXWithSlides(t, deckPath, 3)
	recordPresentationSuccess(ctx, ToolCall{ID: "skill", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "ppt-art-director",
	}}, "Runtime Directory: .openkitty/runtime/builtin/presentation/ppt-art-director")
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.CandidatePaths["runs/report.pptx"] = struct{}{}
	state.AuditPassed = true
	state.AuditEvidence = "test:canonical-audit"
	state.mu.Unlock()

	modelCalls := []ToolCall{{ID: "model-work", Name: "write_file", Arguments: map[string]interface{}{
		"path": "runs/notes.md", "content": "optional follow-up",
	}}}
	calls, deferred, ok := prioritizePresentationNativeQA(ctx, 9, modelCalls)
	if !ok || deferred != len(modelCalls) || len(calls) != 1 {
		t.Fatalf("ready native QA did not receive the next execution slot: ok=%v deferred=%d calls=%#v", ok, deferred, calls)
	}
	if calls[0].Name != "cli" || !calls[0].HostManaged || calls[0].ID == modelCalls[0].ID {
		t.Fatalf("prioritized call is not the trusted host-managed renderer: %#v", calls[0])
	}
	if next, nextDeferred, repeated := prioritizePresentationNativeQA(ctx, 10, modelCalls); repeated || nextDeferred != 0 || len(next) != 1 || next[0].ID != modelCalls[0].ID {
		t.Fatalf("one-shot host scheduling did not return control to model calls: repeated=%v deferred=%d calls=%#v", repeated, nextDeferred, next)
	}
}

func TestPresentationSkillActivationReceiptPreventsSameSkillReload(t *testing.T) {
	ctx := presentationTestContext("请生成一份管理层汇报 PPT")
	call := ToolCall{ID: "skill-art", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "ppt-art-director",
	}}
	result := ToolResult{Content: "技能: ppt-art-director\n运行时目录: ./.openkitty/runtime/builtin/presentation/ppt-art-director"}
	recordPresentationToolResult(ctx, call, result, nil)
	receipt := presentationSkillActivationReceipt(ctx, call, result, nil)
	for _, want := range []string{
		"succeeded", "ppt-art-director", "Do not call the same Skill again",
		"standalone compact JSON", "build_task_pptx.py", "run the scaffold first",
		"deck_spec.json", "visual_plan.json", "SLIDE_VISUALS", "35pt title",
	} {
		if !strings.Contains(receipt, want) {
			t.Fatalf("activation receipt missing %q: %s", want, receipt)
		}
	}
}

func TestPresentationRepeatedSkillCallReturnsIdempotentRecoveryCheckpoint(t *testing.T) {
	ctx := presentationTestContext("请生成一份管理层汇报 PPT，并严格验收")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.RuntimeDir = `C:\runtime\ppt-art-director`
	state.Scaffolded = true
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BuilderPath = `.openkitty/storage/build_deck.py`
	state.mu.Unlock()

	call := ToolCall{ID: "skill-recovery", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "ppt-art-director",
	}}
	result, err, handled := checkPresentationToolGate(ctx, call)
	if !handled || err != nil || !ToolResultExecutionSucceeded(result, err) {
		t.Fatalf("repeated activation did not become a successful checkpoint lookup: handled=%v err=%v result=%#v", handled, err, result)
	}
	for _, want := range []string{
		"idempotent checkpoint lookup",
		`Runtime Directory: C:\runtime\ppt-art-director`,
		"scaffolded=true, builder_read=true, builder_customized=true",
		"Existing task builder: .openkitty/storage/build_deck.py",
		"correct the deterministic fact_model generator",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("recovery checkpoint missing %q: %s", want, result.Content)
		}
	}

	// Recording the host-handled receipt must preserve all existing production
	// checkpoints rather than restarting the workflow.
	recordPresentationToolResult(ctx, call, result, nil)
	if !state.Scaffolded || !state.BuilderRead || !state.BuilderCustomized || state.BuilderPath != `.openkitty/storage/build_deck.py` {
		t.Fatalf("idempotent recovery reset production state: %#v", state)
	}
}

func TestPresentationScaffoldReceiptRecordsBuilderPathForCompactRecovery(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/scaffold_task_pptx.py --builder ".openkitty/storage/ppt_work_run/build_deck.py" --output report.pptx`,
	}}
	recordPresentationToolResult(ctx, call, ToolResult{Content: `{"builder":"ok","spec":"ok"}`}, nil)
	state := presentationProductionStateFromContext(ctx)
	if !state.Scaffolded || state.BuilderPath != ".openkitty/storage/ppt_work_run/build_deck.py" {
		t.Fatalf("scaffold checkpoint did not preserve builder path: %#v", state)
	}
}

func TestPresentationStrictQALocksOrdinaryDeckToLevel2(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	for _, skillName := range []string{"ppt-master", "base-ppt"} {
		call := ToolCall{ID: "route-" + skillName, Name: "use_skill", Arguments: map[string]interface{}{"skill_name": skillName}}
		result, err, blocked := checkPresentationToolGate(ctx, call)
		if !blocked || err == nil || !strings.Contains(result.Content, "Level 2") {
			t.Fatalf("strict ordinary deck allowed route hop to %s: blocked=%v err=%v result=%q", skillName, blocked, err, result.Content)
		}
	}
}

func TestPresentationRequestedSlideBoundsHandleExactRangeAndAppendix(t *testing.T) {
	for _, test := range []struct {
		input        string
		minimum, max int
	}{
		{"生成 10 页 PPT", 10, 10},
		{"生成至少 10 页 PPT", 10, 0},
		{"生成 8–12 页 PPT", 8, 12},
		{"主 deck 10 页允许 2 页附录", 10, 12},
		{"PPT 最多 15 页", 0, 15},
	} {
		if got := requestedPresentationMinimumSlides(test.input); got != test.minimum {
			t.Errorf("minimum for %q = %d, want %d", test.input, got, test.minimum)
		}
		if got := requestedPresentationMaximumSlides(test.input); got != test.max {
			t.Errorf("maximum for %q = %d, want %d", test.input, got, test.max)
		}
	}
}

func TestPresentationAcceptanceRejectsDeckAboveExplicitMaximum(t *testing.T) {
	ctx := presentationTestContext("请生成主 deck 10 页、允许 2 页附录的 PPT，并全页渲染检查")
	state := presentationProductionStateFromContext(ctx)
	state.DeckCreated = true
	state.DeckPath = "report.pptx"
	state.DeckSlideCount = 13
	state.AuditPassed = true
	state.Rendered = true
	state.FinalVisualInspected = true
	state.VisualCoverageComplete = true
	issues := strings.Join(presentationBlockingIssues(ctx), " | ")
	if !strings.Contains(issues, "13 页") || !strings.Contains(issues, "最多 12 页") {
		t.Fatalf("explicit maximum was not enforced: %s", issues)
	}
}

func TestPresentationGateRejectsSpecAboveExplicitMaximumBeforeBuild(t *testing.T) {
	ctx := presentationTestContext("请生成主 deck 10 页、允许 2 页附录的 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	slides := make([]string, 13)
	for i := range slides {
		slides[i] = `{}`
	}
	call := ToolCall{ID: "oversized-spec", Name: "write_file", Arguments: map[string]interface{}{
		"path": "runs/A23/deck_spec.json", "content": `{"slides":[` + strings.Join(slides, ",") + `]}`,
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "13 页") || !strings.Contains(result.Content, "最多 12 页") {
		t.Fatalf("oversized spec was not rejected before write: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateValidatesPersistedSpecBeforeStableBuilder(t *testing.T) {
	workspace := t.TempDir()
	ctx := withResolvedWorkspace(presentationTestContext("请生成主 deck 10 页、允许 2 页附录的 PPT，并全页渲染检查"), workspace)
	activatePresentationArtDirector(ctx)
	specPath := filepath.Join(workspace, "runs", "A23", "deck_spec.json")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, []byte(`{"slides":[{},{},{},{},{},{},{},{},{},{},{},{},{}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	call := ToolCall{ID: "stable-builder", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec "runs/A23/deck_spec.json" --output "runs/A23/report.pptx"`,
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "封面也计入总页数") {
		t.Fatalf("persisted oversized spec reached builder: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationStrictQABlocksStableSpecBuilderWithoutPerSlideScaffold(t *testing.T) {
	ctx := presentationTestContext("生成 10 页 PPT，并全页渲染检查")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "stable-builder", Name: "cli", Arguments: map[string]interface{}{
		"command": `python .openkitty/runtime/builtin/presentation/ppt-art-director/scripts/build_task_pptx.py --spec runs/A23/deck_spec.json --output runs/A23/report.pptx`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); !blocked || err == nil || !strings.Contains(result.Content, "逐页定制") {
		t.Fatalf("stable renderer bypassed the per-slide contract: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationShortAudienceFacingDeckStillRequiresPerSlideCustomization(t *testing.T) {
	ctx := presentationTestContext("请制作 6 页正式行业分析 PPT")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Substantive || !state.PerSlideCustomizationRequired {
		t.Fatalf("short formal deck lost the per-slide quality floor: %#v", state)
	}
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "compact-six", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); !blocked || err == nil || !strings.Contains(result.Content, "逐页定制") {
		t.Fatalf("six-page formal deck escaped through compact rendering: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationExplicitInternalFixedSchemaMayUseCompactRenderer(t *testing.T) {
	ctx := presentationTestContext("请快速生成 6 页内部固定结构周报 PPT，设计不重要")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.PerSlideCustomizationRequired || !state.BasePPTHandoffAllowed {
		t.Fatalf("explicit utilitarian deck was forced into per-slide production: %#v", state)
	}
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "compact-internal", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("explicit compact route was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationStrictQAAllowsPremiumAndBoundedFallbackRoutes(t *testing.T) {
	premium := presentationTestContext("生成董事会使用的 10 页高级 PPT，并逐页渲染检查")
	activatePresentationArtDirector(premium)
	pptMaster := ToolCall{ID: "premium", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "ppt-master"}}
	if result, err, blocked := checkPresentationToolGate(premium, pptMaster); blocked || err != nil {
		t.Fatalf("premium route was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	fallback := presentationTestContext("生成 10 页 PPT，并逐页渲染检查")
	activatePresentationArtDirector(fallback)
	failedBuild := ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	for range 2 {
		recordPresentationToolResult(fallback, failedBuild, ToolResult{Name: "cli", Content: "status: failed"}, fmt.Errorf("builder failed"))
	}
	basePPT := ToolCall{ID: "fallback", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "base-ppt"}}
	if result, err, blocked := checkPresentationToolGate(fallback, basePPT); blocked || err != nil {
		t.Fatalf("bounded Level-2 fallback was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationExplicitTaskSpecificBuilderNeverFallsBackAfterFailures(t *testing.T) {
	ctx := presentationTestContext("制作固定 8 页 PPT；必须走任务专属构建器，不得降级。")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.TaskSpecificBuilderRequired {
		t.Fatalf("explicit task-specific route was not frozen: %#v", state)
	}
	activatePresentationArtDirector(ctx)
	failedBuild := ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	for range 2 {
		recordPresentationToolResult(ctx, failedBuild, ToolResult{Name: "cli", Content: "status: failed"}, fmt.Errorf("builder failed"))
	}
	basePPT := ToolCall{ID: "fallback", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "base-ppt"}}
	if result, err, blocked := checkPresentationToolGate(ctx, basePPT); !blocked || err == nil || !strings.Contains(result.Content, "不得切换或降级") {
		t.Fatalf("explicit task-specific contract escaped through base-ppt: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationSuccessfulLevel2BuildClearsOldFallbackFailures(t *testing.T) {
	ctx := presentationTestContext("生成 8 页 PPT")
	activatePresentationArtDirector(ctx)
	build := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": `python runtime/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	recordPresentationToolResult(ctx, build, ToolResult{Name: "persistent_shell", Content: "status: failed"}, fmt.Errorf("builder failed"))
	recordPresentationSuccess(ctx, build, "generated report.pptx\nstatus: success")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Level2BuildFailures != 0 {
		t.Fatalf("successful Level-2 build retained stale fallback failures: %#v", state)
	}
}

func TestPresentationMissingDeckFailureSuppressesHistoricalPseudoDelivery(t *testing.T) {
	output := `最终交付报告（本会话定稿）
校验证据：6691 字节
.openkitty/storage/old.html`
	got := appendPresentationDeliveryFailure(output, []string{"未形成可交付的 PPTX"})
	for _, forbidden := range []string{"最终交付报告", "校验证据", "6691", "old.html"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("missing-deck failure leaked historical pseudo-delivery %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "未检测到可打开的新 PPTX") {
		t.Fatalf("missing-deck failure lacks a trusted host explanation: %s", got)
	}
}

func TestPresentationStrictQAFailureSuppressesContradictoryCompletion(t *testing.T) {
	output := `✅ 交付完成说明
实际文件位置：.openkitty/storage/deck/build_and_qa.py
用户一键复现命令：python3 build_and_qa.py`
	got := appendPresentationDeliveryFailure(output, []string{
		"未获得 PPTX 结构完整性验证证据",
		"未获得渲染页面的像素级视觉检查证据",
	})
	for _, forbidden := range []string{"✅ 交付完成", "实际文件位置", "一键复现命令", "build_and_qa.py"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("strict-QA failure retained contradictory completion marker %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "仅属于中间产物") {
		t.Fatalf("strict-QA failure lacks intermediate-artifact explanation: %s", got)
	}
}

func TestPresentationGateBlocksDirectBuildBeforeArtDirectorAndScaffold(t *testing.T) {
	ctx := presentationTestContext("请生成一份管理层汇报 PPT")
	call := ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python .openkitty\storage\build_deck.py --output .openkitty\storage\report.pptx`,
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "use_skill ppt-art-director") {
		t.Fatalf("direct builder bypassed art direction: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateBlocksInlineDeckFallback(t *testing.T) {
	ctx := presentationTestContext("生成 PPT")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "inline", Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": "from pptx import Presentation\nprs = Presentation()\nprs.save('ugly.pptx')",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "一次性内联脚本") {
		t.Fatalf("inline PPT bypassed per-slide production: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationReferenceVisionGapDoesNotBlockScaffold(t *testing.T) {
	ctx := presentationTestContext(`参考 E:\\good\\reference.pptx 重做这份 PPT`)
	state := presentationProductionStateFromContext(ctx)
	activatePresentationArtDirector(ctx)
	scaffold := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\\runtime\\ppt-art-director\\scripts\\scaffold_task_pptx.py --builder .openkitty\\storage\\build_deck.py --output .openkitty\\storage\\result.pptx`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, scaffold); blocked || err != nil {
		t.Fatalf("missing optional reference pixels should not deadlock the build: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	if joined := strings.Join(presentationBlockingIssues(ctx), " | "); !strings.Contains(joined, "参考稿") {
		t.Fatalf("reference inspection gap should remain visible as a strict-QA issue: %s", joined)
	}

	recordPresentationSuccess(ctx, ToolCall{ID: "render-ref", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\\runtime\\scripts\\render_pptx.ps1 -InputPptx E:\\good\\reference.pptx -OutputDir .openkitty\\storage\\reference-preview`,
	}}, "status: success\nrendered reference pages")
	if !state.ReferenceRendered || state.DeckCreated {
		t.Fatalf("reference render was misclassified as final-deck output: %#v", state)
	}
	visionCall := ToolCall{ID: "vision-ref", Name: "vision", Arguments: map[string]interface{}{
		"path": `.openkitty\\storage\\reference-preview\\contact-sheet.png`,
	}}
	if paths := presentationVisualImagePaths(visionCall); len(paths) == 0 || !isPresentationVisionAction(visionCall) || !presentationVisualTargetsRender(visionCall, state.ReferenceRenderTarget) {
		t.Fatalf("reference vision call did not target its preview: paths=%q target=%q", paths, state.ReferenceRenderTarget)
	}
	recordPresentationSuccess(ctx, visionCall, "参考稿为白底、深蓝与金色咨询风格，包含时间轴、矩阵和图表。")
	if !state.ReferenceVisualInspected {
		t.Fatalf("reference preview image was not recorded as visual evidence: %#v", state)
	}
	if joined := strings.Join(presentationBlockingIssues(ctx), " | "); strings.Contains(joined, "参考稿") {
		t.Fatalf("successful reference image inspection did not clear its strict-QA issue: %s", joined)
	}
}

func TestPresentationGateRequiresScaffoldReadAndBoundedPerSlideBuilderEdit(t *testing.T) {
	ctx := presentationTestContext("生成一份行业分析 PPT")
	activatePresentationArtDirector(ctx)
	builder := ToolCall{ID: "write-builder", Name: "write_file", Arguments: map[string]interface{}{
		"path":    `.openkitty\\storage\\build_deck.py`,
		"content": "from pptx import Presentation\nprs = Presentation()\nprs.save('report.pptx')",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, builder); !blocked || err == nil || !strings.Contains(result.Content, "scaffold_task_pptx.py") {
		t.Fatalf("builder bypassed the mandatory scaffold: %v %v %q", blocked, err, result.Content)
	}
	scaffold := ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\\runtime\\scripts\\scaffold_task_pptx.py --builder .openkitty\\storage\\build_deck.py --output .openkitty\\storage\\report.pptx`,
	}}
	recordPresentationSuccess(ctx, scaffold, "Command Execution\nstatus: success\nscaffolded")
	anchoredEdit := ToolCall{ID: "edit-builder", Name: "edit_file", Arguments: map[string]interface{}{
		"path":       `.openkitty\storage\build_deck.py`,
		"old_string": "TASK_PAGE_RENDERERS = {}",
		"new_string": "TASK_PAGE_RENDERERS = {1: render_slide_01}",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, anchoredEdit); !blocked || err == nil || !strings.Contains(result.Content, "完整读取一次") {
		t.Fatalf("builder edit before the required read was allowed: %v %v %q", blocked, err, result.Content)
	}
	read := ToolCall{ID: "read-builder", Name: "read_file", Arguments: map[string]interface{}{
		"path": `.openkitty\storage\build_deck.py`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, read); blocked || err != nil {
		t.Fatalf("task-local builder read was blocked: %v %v %q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, read, "scaffolded builder source")
	if result, err, blocked := checkPresentationToolGate(ctx, builder); !blocked || err == nil || !strings.Contains(result.Content, "整体覆盖") {
		t.Fatalf("whole-builder overwrite was allowed: %v %v %q", blocked, err, result.Content)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, anchoredEdit); blocked || err != nil {
		t.Fatalf("bounded per-slide builder edit was blocked: %v %v %q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, anchoredEdit, "updated TASK PAGE COMPOSITIONS")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.BuilderRead || !state.BuilderCustomized {
		t.Fatalf("per-slide builder receipt was not recorded: %#v", state)
	}
	build := ToolCall{ID: "build-customized", Name: "cli", Arguments: map[string]interface{}{
		"command": `python .openkitty\storage\build_deck.py`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, build); blocked || err != nil {
		t.Fatalf("customized task builder was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateBlocksDesignHelperReadBeforeScaffold(t *testing.T) {
	ctx := presentationTestContext("生成一份行业分析 PPT")
	activatePresentationArtDirector(ctx)
	builderRead := ToolCall{ID: "builder-read", Name: "read_file", Arguments: map[string]interface{}{
		"path": ".openkitty/storage/ppt_work/build_deck.py",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, builderRead)
	if !blocked || err == nil || !strings.Contains(result.Content, "首次生成代码动作") {
		t.Fatalf("builder read before scaffold was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	call := ToolCall{ID: "helper-read", Name: "read_file", Arguments: map[string]interface{}{
		"path": ".openkitty/storage/ppt_work/pptx_design.py",
	}}
	result, err, blocked = checkPresentationToolGate(ctx, call)
	if !blocked || err == nil || !strings.Contains(result.Content, "不要读取或改写") {
		t.Fatalf("design helper read before scaffold was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateRejectsRawPythonPPTXGeometry(t *testing.T) {
	ctx := presentationTestContext("生成一份通用管理汇报 PPT")
	activatePresentationArtDirector(ctx)

	for _, call := range []ToolCall{
		{ID: "write-raw", Name: "write_file", Arguments: map[string]interface{}{
			"file_path": ".openkitty/storage/build_deck.py",
			"content":   "shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, 0.5, y, 4.0, 1.25)",
		}},
		{ID: "edit-raw", Name: "edit_file", Arguments: map[string]interface{}{
			"file_path":  ".openkitty/storage/build_deck.py",
			"old_string": "pass",
			"new_string": "box = slide.shapes.add_textbox(x, y, w, h)",
		}},
		{ID: "multi-raw", Name: "multiedit", Arguments: map[string]interface{}{
			"edits": []interface{}{map[string]interface{}{
				"file_path":  ".openkitty/storage/build_deck.py",
				"old_string": "pass",
				"new_string": "line = slide.shapes.add_connector(1, ax, ay, bx, by)",
			}},
		}},
	} {
		result, err, blocked := checkPresentationToolGate(ctx, call)
		if !blocked || err == nil || !strings.Contains(result.Content, "整数 EMU") {
			t.Fatalf("raw geometry was not blocked for %s: blocked=%v err=%v result=%q", call.ID, blocked, err, result.Content)
		}
	}
}

func TestPresentationGateAllowsExplicitPythonPPTXGeometryConversion(t *testing.T) {
	ctx := presentationTestContext("快速生成一份内部固定结构 PPT，设计不重要")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "write-safe", Name: "write_file", Arguments: map[string]interface{}{
		"file_path": ".openkitty/storage/build_deck.py",
		"content": strings.Join([]string{
			"shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, _I(x), _I(y), _I(w), _I(h))",
			"box = slide.shapes.add_textbox(Inches(0.5), Inches(1), Inches(4), Inches(1.25))",
			"line = slide.shapes.add_connector(1, int(start.left), int(start.top), int(end.left), int(end.top))",
			"keyword_box = slide.shapes.add_textbox(height=_I(h), width=_I(w), top=_I(y), left=_I(x))",
		}, "\n"),
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("explicit EMU conversion should be allowed: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGeometryRepairIgnoresEditOldString(t *testing.T) {
	ctx := presentationTestContext("快速生成一份内部固定结构 PPT，设计不重要")
	activatePresentationArtDirector(ctx)
	call := ToolCall{ID: "repair", Name: "edit_file", Arguments: map[string]interface{}{
		"file_path":  ".openkitty/storage/build_deck.py",
		"old_string": "box = slide.shapes.add_textbox(x, y, w, h)",
		"new_string": "box = slide.shapes.add_textbox(_I(x), _I(y), _I(w), _I(h))",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("repair edit should inspect only new_string: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationBuilderSourceEditDoesNotCreateDeck(t *testing.T) {
	ctx := presentationTestContext("生成一份行业分析 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python3 /runtime/scripts/scaffold_task_pptx.py --builder .openkitty/storage/build_deck.py --output .openkitty/storage/report.pptx`,
	}}, "status: success\noutput=.openkitty/storage/report.pptx")

	recordPresentationSuccess(ctx, ToolCall{ID: "edit-builder", Name: "edit_file", Arguments: map[string]interface{}{
		"path":       ".openkitty/storage/build_deck.py",
		"old_string": "SLIDE_ARCHITECTURE = []",
		"new_string": "SLIDE_ARCHITECTURE = ['report.pptx']",
	}}, "status: success\nupdated build_deck.py")

	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Scaffolded {
		t.Fatalf("expected scaffold receipt, got %#v", state)
	}
	if state.DeckCreated || state.DeckPath != "" {
		t.Fatalf("editing builder source must not mark scaffold placeholder as generated: %#v", state)
	}
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "未形成可交付的 PPTX") {
		t.Fatalf("source-only progress must remain blocked: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationCompletionRequiresAuditRenderAndVision(t *testing.T) {
	ctx := presentationTestContext("生成一份财经简报 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\\runtime\\scripts\\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "Command Execution\nstatus: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "Command Execution\nstatus: success\ncreated report.pptx")

	issues := presentationCompletionIssues(ctx)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"结构完整性", "真实 PowerPoint", "像素级视觉检查"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing expected QA issue %q in %q", want, joined)
		}
	}

	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\\runtime\\scripts\\audit_pptx.py report.pptx`,
	}}, "Command Execution\nstatus: success\nAUDIT_OK\nslide_count=1")
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\\runtime\\scripts\\render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, "Command Execution\nstatus: success\nrendered 1 slide")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\\slide_01.png`,
	}}, "页面清晰，无明显遮挡或溢出。")
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 1)
	if issues := presentationCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("complete QA evidence should pass: %v", issues)
	}
}

func TestPresentationBasePPTHandoffDoesNotRequireLevel2Scaffold(t *testing.T) {
	ctx := presentationTestContext("快速生成固定结构的内部周报 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "skill-base", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "base-ppt",
	}}, "技能: base-ppt")
	convert := ToolCall{ID: "convert", Name: "convert", Arguments: map[string]interface{}{
		"format": "pptx", "output_path": "weekly.pptx",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, convert); blocked || err != nil {
		t.Fatalf("base-ppt handoff should allow convert: %v %v %q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, convert, "created weekly.pptx")
	for _, issue := range presentationCompletionIssues(ctx) {
		if strings.Contains(issue, "scaffold") {
			t.Fatalf("base-ppt handoff must not require Level 2 scaffold: %v", presentationCompletionIssues(ctx))
		}
	}
}

func TestPresentationGateBlocksPrematureBasePPTFallbackForBeautifulDeck(t *testing.T) {
	ctx := presentationTestContext("帮我写一份脑科学与未来规划的报告，整理成漂亮的 PPT")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || !state.PPTMasterAllowed {
		t.Fatalf("整理成 PPT must activate the presentation production gate: %#v", state)
	}
	activatePresentationArtDirector(ctx)

	basePPT := ToolCall{ID: "skill-base", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "base-ppt",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, basePPT)
	if !blocked || err == nil || !strings.Contains(result.Content, "最后灾备") {
		t.Fatalf("beautiful deck escaped through premature base-ppt: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	state = presentationProductionStateFromContext(ctx)
	if state == nil || state.Route != "level3-ppt-master" {
		t.Fatalf("blocked base-ppt fallback rewrote the preferred route: %#v", state)
	}
}

func TestPresentationBeautifulDeckRequiresBothQualityRoutesBeforeDisasterFallback(t *testing.T) {
	ctx := presentationTestContext("制作一份精美的 10 页投资人 PPT")
	activatePresentationArtDirector(ctx)

	basePPT := ToolCall{ID: "base", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "base-ppt"}}
	if result, err, blocked := checkPresentationToolGate(ctx, basePPT); !blocked || err == nil {
		t.Fatalf("base-ppt was allowed before premium route: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	pptMaster := ToolCall{ID: "premium", Name: "use_skill", Arguments: map[string]interface{}{"skill_name": "ppt-master"}}
	if result, err, blocked := checkPresentationToolGate(ctx, pptMaster); blocked || err != nil {
		t.Fatalf("premium recovery route was blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, pptMaster, "技能: ppt-master")

	premiumExport := ToolCall{ID: "premium-export", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/skills/ppt-master/scripts/svg_to_pptx.py project -s final -o report.pptx`,
	}}
	recordPresentationToolResult(ctx, premiumExport, ToolResult{Name: "cli", Content: "status: failed"}, fmt.Errorf("export failed"))
	if result, err, blocked := checkPresentationToolGate(ctx, basePPT); !blocked || err == nil {
		t.Fatalf("base-ppt was allowed before premium correction: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	recordPresentationToolResult(ctx, premiumExport, ToolResult{Name: "cli", Content: "status: failed"}, fmt.Errorf("export failed again"))
	state := presentationProductionStateFromContext(ctx)
	if state.Route != "level2-task-builder" {
		t.Fatalf("failed premium route did not hand off to Level 2: %#v", state)
	}
	level2Build := ToolCall{ID: "level2-build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python runtime/scripts/build_task_pptx.py --spec deck.json --output report.pptx`,
	}}
	for range 2 {
		recordPresentationToolResult(ctx, level2Build, ToolResult{Name: "cli", Content: "status: failed"}, fmt.Errorf("builder failed"))
	}
	if result, err, blocked := checkPresentationToolGate(ctx, basePPT); blocked || err != nil {
		t.Fatalf("disaster fallback remained blocked after both quality routes failed: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateAllowsExplicitBasePPTRequest(t *testing.T) {
	ctx := presentationTestContext("请直接使用 base-ppt 生成一份 PPT")
	activatePresentationArtDirector(ctx)
	basePPT := ToolCall{ID: "skill-base", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "base-ppt",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, basePPT); blocked || err != nil {
		t.Fatalf("explicit base-ppt request should be allowed: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, basePPT, "技能: base-ppt")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Route != "level1-base-ppt" {
		t.Fatalf("explicit base-ppt request did not select level 1: %#v", state)
	}
}

func TestPresentationGateAllowsEnglishFastOrInternalBasePPTHandoff(t *testing.T) {
	for _, input := range []string{
		"Create a quick PPT draft about the weekly status",
		"Create an internal-only PowerPoint for the operations team",
		"Generate a recurring report deck with a fixed schema",
	} {
		ctx := presentationTestContext(input)
		if state := presentationProductionStateFromContext(ctx); state == nil || !state.BasePPTHandoffAllowed {
			t.Fatalf("English base-ppt intent was not recognized for %q: %#v", input, state)
		}
	}
}

func TestPresentationGateRoutesEnglishVisualQualityToLevel3(t *testing.T) {
	ctx := presentationTestContext("Create a quick but visually polished customer-facing PowerPoint")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || state.BasePPTHandoffAllowed || state.Route != "level3-ppt-master" {
		t.Fatalf("English visual-quality request must use the premium route: %#v", state)
	}
}

func TestPresentationGateDoesNotAffectUnrelatedTask(t *testing.T) {
	ctx := presentationTestContext("修复 Go 单元测试")
	call := ToolCall{ID: "unrelated", Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": "from pptx import Presentation\nprs = Presentation()\nprs.save('fixture.pptx')",
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("unrelated task should not inherit presentation state: %v %v %q", blocked, err, result.Content)
	}
}

func TestPresentationGateBlocksChatroomPublishUntilVisualQACompletes(t *testing.T) {
	ctx := presentationTestContext("生成一份行业分析 PPT，然后发到 chatroom")
	publish := ToolCall{ID: "publish", Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `publish @"prepared.json" --intent-id weixin_session-t1`,
	}}
	if result, _, blocked := checkPresentationToolGate(ctx, publish); !blocked || !strings.Contains(result.Content, "未形成可交付的 PPTX") {
		t.Fatalf("chatroom publish should be blocked before PPT QA: blocked=%v result=%q", blocked, result.Content)
	}

	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "Command Execution\nstatus: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "Command Execution\nstatus: success\ncreated report.pptx")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\audit_pptx.py report.pptx`,
	}}, "Command Execution\nstatus: success\nAUDIT_OK")
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\runtime\scripts\render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, "Command Execution\nstatus: success\nrendered")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\contact-sheet.png`,
	}}, "所有页面可读，无裁切、重叠或占位符。")
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 1)

	if issues := presentationCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("test setup did not complete PPT QA: %v", issues)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, publish); blocked || err != nil {
		t.Fatalf("chatroom publish should be allowed after PPT QA: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestExistingPresentationQAResumesWithoutLevel2Scaffold(t *testing.T) {
	ctx := presentationTestContext(`请验收并发布 E:\deliverables\existing-deck.pptx`)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || state.Route != "qa-existing-deck" || !state.DeckCreated {
		t.Fatalf("existing deck did not enter QA-only route: %#v", state)
	}

	activatePresentationArtDirector(ctx)
	if state.Route != "qa-existing-deck" {
		t.Fatalf("art-director activation overwrote QA-only route: %q", state.Route)
	}
	for _, issue := range presentationCompletionIssues(ctx) {
		if strings.Contains(issue, "scaffold") {
			t.Fatalf("existing-deck QA must not require a new scaffold: %v", presentationCompletionIssues(ctx))
		}
	}

	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\audit_pptx.py E:\deliverables\existing-deck.pptx`,
	}}, "Command Execution\nstatus: success\nAUDIT_OK")
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\runtime\scripts\render_pptx.ps1 -InputPptx E:\deliverables\existing-deck.pptx -OutputDir preview`,
	}}, "Command Execution\nstatus: success\nrendered")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\contact-sheet.png`,
	}}, "页面清晰，无裁切或重叠。")
	completePresentationAestheticQAForTest(state, 1)

	if issues := presentationCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("existing-deck QA did not close: %v", issues)
	}
}

func TestExistingPresentationMinimalRepairDoesNotRequireNewDeckScaffold(t *testing.T) {
	input := `检查既有 PPT，不要直接重做；只修复确认的问题，输出 E:\deliverables\fixed.pptx。源文件：E:\fixtures\existing.pptx`
	ctx := presentationTestContext(input)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Route != "qa-existing-deck" || !samePresentationPath(state.DeckPath, `E:\fixtures\existing.pptx`) {
		t.Fatalf("minimal repair did not select the supplied existing deck: %#v", state)
	}
	call := ToolCall{ID: "repair", Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": `from pptx import Presentation\nprs=Presentation(r"E:\fixtures\existing.pptx")\nprs.save(r"E:\deliverables\fixed.pptx")`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, call); blocked || err != nil {
		t.Fatalf("existing-deck minimal repair was incorrectly forced through Level 2: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationRebuildInvalidatesStaleQAEvidence(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build-1", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit-1", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\audit_pptx.py report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "render-1", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\runtime\scripts\render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-1", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\contact-sheet.png`,
	}}, "页面清晰，无裁切、重叠或占位符。")
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 1)
	if issues := presentationCompletionIssues(ctx); len(issues) != 0 {
		t.Fatalf("initial QA setup incomplete: %v", issues)
	}

	recordPresentationSuccess(ctx, ToolCall{ID: "build-2", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success\ncorrected")
	joined := strings.Join(presentationCompletionIssues(ctx), " | ")
	for _, want := range []string{"结构完整性", "真实 PowerPoint", "像素级视觉检查"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rebuild did not invalidate %q evidence: %s", want, joined)
		}
	}
}

func TestPresentationSubstantiveAcceptanceBlocksMissingQAByDefault(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success\ncreated report.pptx")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok {
		t.Fatal("presentation acceptance state was not available")
	}
	if acceptance.Level != "blocked" || len(acceptance.BlockingIssues) < 4 {
		t.Fatalf("substantive audience-facing acceptance must block missing QA: %#v", acceptance)
	}
	if len(acceptance.Warnings) != 0 {
		t.Fatalf("substantive QA gaps must not be downgraded to warnings: %#v", acceptance.Warnings)
	}
	joined := strings.Join(acceptance.BlockingIssues, " | ")
	for _, want := range []string{"结构完整性", "真实 PowerPoint", "像素级视觉检查", "审美评分账本"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing blocking production requirement %q: %s", want, joined)
		}
	}
}

func TestPresentationWarningDoesNotReplaceCurrentAnswer(t *testing.T) {
	const current = "这是当前任务的最终回答，不是旧 PPT 状态。"
	got := appendPresentationDeliveryWarning(current, context.Background(), []string{"missing optional render check"})
	if !strings.Contains(got, current) {
		t.Fatalf("presentation warning replaced current answer: %q", got)
	}
}

func TestPresentationWarningIsIdempotentAcrossFinalAndGracePasses(t *testing.T) {
	ctx := presentationTestContext("快速生成一份内部粗糙草稿 PPT，设计不重要，不要严格验收")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success\ncreated report.pptx")
	warnings := presentationQualityWarnings(ctx)
	once := appendPresentationDeliveryWarning("交付说明", ctx, warnings)
	twice := appendPresentationDeliveryWarning(once, ctx, warnings)
	if once != twice {
		t.Fatalf("presentation warning was appended twice:\n%s", twice)
	}
	if strings.Count(twice, "文件可以下载和继续编辑") != 1 {
		t.Fatalf("human QA note count != 1:\n%s", twice)
	}
}

func TestPresentationShellSyntaxErrorNeverCreatesPhantomDeck(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	call := ToolCall{ID: "build", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": `python3 build_deck.py --output report.pptx 2>&1 | tail -20`,
	}}
	recordPresentationSuccess(ctx, call, `Command Execution
status: succeeded
output result:
  File "build_deck.py", line 230
SyntaxError: '(' was never closed`)
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" {
		t.Fatalf("failed shell pipeline created a phantom deck: %#v ok=%v", acceptance, ok)
	}
	if acceptance.DeckPath != "" {
		t.Fatalf("failed shell pipeline recorded a deck path: %q", acceptance.DeckPath)
	}
}

func TestPresentationStrictAcceptancePromotesMissingQAToBlocking(t *testing.T) {
	ctx := presentationTestContext(`请严格验收 E:\deliverables\report.pptx，确保无重叠`)
	activatePresentationArtDirector(ctx)

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" {
		t.Fatalf("strict QA should be blocked without evidence: %#v ok=%v", acceptance, ok)
	}
	joined := strings.Join(acceptance.BlockingIssues, " | ")
	for _, want := range []string{"结构完整性", "真实 PowerPoint", "像素级视觉检查"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("strict QA did not promote %q: %s", want, joined)
		}
	}
}

func TestPresentationAcceptsEquivalentOutcomeEvidence(t *testing.T) {
	ctx := presentationTestContext("生成一份行业分析 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")

	recordPresentationSuccess(ctx, ToolCall{ID: "custom-audit", Name: "company_ppt_validator", Arguments: map[string]interface{}{
		"file": "report.pptx",
	}}, `{"pptx_structure_valid":true}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "custom-render", Name: "company_slide_renderer", Arguments: map[string]interface{}{
		"file": "report.pptx", "output_dir": "preview",
	}}, `{"presentation_rendered":true,"render_fidelity":"native"}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "custom-visual", Name: "image_inspector", Arguments: map[string]interface{}{
		"path": "preview/contact-sheet.png",
	}}, `{"presentation_visual_inspected":true}`)
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 1)

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "verified" || len(acceptance.BlockingIssues) != 0 || len(acceptance.Warnings) != 0 {
		t.Fatalf("equivalent evidence should fully verify the deck: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationPillowWireframeAndReadPlaceholderAreNotVisualEvidence(t *testing.T) {
	payload := `from PIL import Image, ImageDraw\nimg = Image.new("RGB", (1280, 720))\nImageDraw.Draw(img)`
	if isPresentationRenderAction(payload) {
		t.Fatal("Pillow shape wireframe must not count as a real presentation render")
	}
	probePayload := `if Path(r"C:\Program Files\LibreOffice\program\soffice.exe").exists(): print("found")`
	if hasPresentationRenderEvidence(probePayload, "LibreOffice not found. Cannot render slides to images.") {
		t.Fatal("a successful renderer probe with an unavailable result must not count as rendered output")
	}
	placeholder := ToolCall{ID: "read", Name: "read_file", Arguments: map[string]interface{}{
		"path": "preview/slide_01.png",
	}}
	if hasPresentationVisualEvidence(placeholder, `[Image: preview/slide_01.png] Use the vision tool to analyze this image`) {
		t.Fatal("an image path placeholder must not count as pixel inspection")
	}
}

func TestPresentationGenericCLIResultMarkerCannotForgeNativeRender(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "wireframe", Name: "cli", Arguments: map[string]interface{}{
		"command": `python render_pngs.py report.pptx`,
	}}, `{"presentation_rendered":true,"render_fidelity":"native"}`)
	state := presentationProductionStateFromContext(ctx)
	if state.Rendered {
		t.Fatal("an arbitrary CLI marker promoted a wireframe into native render evidence")
	}
}

func TestPresentationDeckCanPublishWithOptionalQAWarnings(t *testing.T) {
	ctx := presentationTestContext("Create a quick internal PPT draft, skip strict QA, then publish it to chatroom")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\runtime\scripts\scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")

	publish := ToolCall{ID: "publish", Name: "code_check", Arguments: map[string]interface{}{
		"action": "run",
		"file":   `C:\runtime\nova-community\scripts\chatroom_agent.py`,
		"args":   `publish @"prepared.json" --intent-id weixin_session-t1`,
	}}
	if result, err, blocked := checkPresentationToolGate(ctx, publish); blocked || err != nil {
		t.Fatalf("optional QA warnings should not block an explicitly requested publish: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationCandidateStreamingIsBufferedUntilAcceptance(t *testing.T) {
	if shouldStreamPresentationCandidate(presentationTestContext("生成一份 PPT")) {
		t.Fatal("presentation model text should be buffered until host acceptance")
	}
	if !shouldStreamPresentationCandidate(presentationTestContext("解释一下 Go interface")) {
		t.Fatal("unrelated model text should continue streaming normally")
	}
}

func TestPresentationDetectionCoversCommandsWithoutTreatingQuestionsAsBuilds(t *testing.T) {
	for _, input := range []string{
		"帮我做PPT", "把这份报告转成 PPT", "请导出一份演示文稿", "继续做今天 A 股复盘 PPT，按照你的来", "Build a product deck",
	} {
		if !isPresentationCreationRequest(input) {
			t.Fatalf("creation request was missed: %q", input)
		}
	}
	for _, input := range []string{
		"PPT 是怎么做的？", "介绍一下 PPT 制作方法", "制作 PPT 时报错算 bug 吗", "是否支持生成 PPT？",
		"我需要知道当前 OpenKitty 能否制作并验收 PPT；只记录能力证据，不要真的生成 PPT。",
	} {
		if isPresentationCreationRequest(input) {
			t.Fatalf("non-creation question was misrouted: %q", input)
		}
	}
	if !isPresentationCreationRequest("不要生成旧版 PPT，请生成一份新版 PPT") {
		t.Fatal("a separate affirmative production command must win over an unrelated negated command")
	}
}

func TestPresentationStyleOnlyFollowupReopensLevel2ModificationRoute(t *testing.T) {
	base := context.WithValue(context.Background(), "history_messages", []map[string]interface{}{
		{"role": "assistant", "content": "已生成 .openkitty/storage/a_share_today_20260727.pptx"},
	})
	ctx := withPresentationProductionState(base, "商务深色（黑底金线）")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || state.Route != "level2-task-builder" {
		t.Fatalf("style-only PPT follow-up did not reopen the Level 2 route: %#v", state)
	}
}

func TestPresentationGateRejectsWorkspaceCounterfeitScaffold(t *testing.T) {
	ctx := presentationTestContext("继续做今天 A 股复盘 PPT，按照你的来")
	activatePresentationArtDirector(ctx)

	fakeWrite := ToolCall{ID: "fake-write", Name: "write_file", Arguments: map[string]interface{}{
		"path":    ".openkitty/storage/scaffold_task_pptx.py",
		"content": "print('pretend scaffold')",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, fakeWrite)
	if !blocked || err == nil || !strings.Contains(result.Content, "用户存储") {
		t.Fatalf("workspace counterfeit scaffold write must be blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
	recordPresentationSuccess(ctx, fakeWrite, "written")

	fakeRun := ToolCall{ID: "fake-run", Name: "cli", Arguments: map[string]interface{}{
		"command": "python3 .openkitty/storage/scaffold_task_pptx.py",
	}}
	result, err, blocked = checkPresentationToolGate(ctx, fakeRun)
	if !blocked || err == nil || !strings.Contains(result.Content, "实际运行时") {
		t.Fatalf("workspace counterfeit scaffold execution must be blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Scaffolded {
		t.Fatalf("counterfeit scaffold created a valid receipt: %#v", state)
	}
}

func TestPresentationGateRejectsWorkspaceCounterfeitQAHelpers(t *testing.T) {
	ctx := presentationTestContext("Create a polished 13-slide board PPTX with strict visual QA")
	activatePresentationArtDirector(ctx)

	fakeRender := ToolCall{ID: "fake-render-write", Name: "write_file", Arguments: map[string]interface{}{
		"path":    ".openkitty/storage/render_runner.py",
		"content": "from PIL import Image, ImageDraw\nfrom pptx import Presentation\nImage.new('RGB', (1600,900))\n# contact_sheet render_receipt",
	}}
	if !isCounterfeitPresentationQAHelperAction(fakeRender, presentationToolPayload(fakeRender)) {
		t.Fatal("counterfeit render helper classifier did not recognize a Pillow/python-pptx wireframe")
	}
	result, err, blocked := checkPresentationToolGate(ctx, fakeRender)
	if !blocked || err == nil || !strings.Contains(result.Content, "workspace-local render/audit/probe helpers") {
		t.Fatalf("counterfeit render helper write was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	fakeAudit := ToolCall{ID: "fake-audit-run", Name: "cli", Arguments: map[string]interface{}{
		"command": "python .openkitty/storage/audit_runner.py runs/D12/q1_review.pptx runs/D12/render runs/D12/audit_receipt.json",
	}}
	result, err, blocked = checkPresentationToolGate(ctx, fakeAudit)
	if !blocked || err == nil || !strings.Contains(result.Content, "scripts/audit_pptx.py") {
		t.Fatalf("counterfeit audit helper execution was not blocked: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationGateAllowsBuilderSourceToRetainScaffoldProvenance(t *testing.T) {
	ctx := presentationTestContext("Create a research presentation")
	activatePresentationArtDirector(ctx)
	state := presentationProductionStateFromContext(ctx)
	state.mu.Lock()
	state.Scaffolded = true
	state.mu.Unlock()

	writeBuilder := ToolCall{ID: "write-builder", Name: "write_file", Arguments: map[string]interface{}{
		"path":    ".openkitty/storage/run/build_deck.py",
		"content": "# Generated by scaffold_task_pptx.py\nfrom pptx import Presentation\n",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, writeBuilder)
	if blocked || err != nil {
		t.Fatalf("builder provenance comment must not be treated as a counterfeit scaffold: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}

	editBuilder := ToolCall{ID: "edit-builder", Name: "edit_file", Arguments: map[string]interface{}{
		"path":     ".openkitty/storage/run/build_deck.py",
		"old_text": "# Generated by scaffold_task_pptx.py",
		"new_text": "# Generated by scaffold_task_pptx.py; customized for this deck",
	}}
	result, err, blocked = checkPresentationToolGate(ctx, editBuilder)
	if blocked || err != nil {
		t.Fatalf("builder edit containing scaffold provenance must be allowed: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationCapabilityEvidenceTaskDoesNotOpenProductionGate(t *testing.T) {
	ctx := presentationTestContext("我需要知道当前 OpenKitty 能否制作并验收 PPT。请记录内置 Skill 证据，不要真的生成 PPT。")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || state.Expected {
		t.Fatalf("capability-only evidence task was misrouted into PPT production: %#v", state)
	}

	// Reading a presentation Skill or recording PPTX capability prose is
	// observation evidence, not new user intent. Neither may open the production
	// gate for an explicitly non-production task.
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{
		ID:   "write-evidence",
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    "builtin_skill_evidence.md",
			"content": "OpenKitty can create and validate PPTX files.",
		},
	}, "Successfully wrote builtin_skill_evidence.md")
	if state.Expected || state.ArtDirectorActivated || state.DeckCreated {
		t.Fatalf("tool evidence mutated capability-only intent into production: %#v", state)
	}
	if issues := presentationBlockingIssues(ctx); len(issues) != 0 {
		t.Fatalf("capability-only task received PPT production blockers: %v", issues)
	}
}

func TestPresentationFullPageRenderLanguageIsStrictAndReferenceNegationWins(t *testing.T) {
	input := "制作 runs/D12/q1_review.pptx；必须渲染全部幻灯片并输出包含全部页面的联系表，发现裁切或重叠要修复；不能拿参考 deck 代替最终稿。"
	ctx := presentationTestContext(input)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || !state.StrictQA {
		t.Fatalf("explicit full-page QA contract was not strict: %#v", state)
	}
	if state.ReferenceRequired {
		t.Fatalf("negated reference mention became a reference requirement: %#v", state)
	}
	if !presentationReferenceRequested(`参考 E:\examples\good.pptx 制作 E:\out\new.pptx`) {
		t.Fatal("positive reference request was lost while handling negation")
	}
}

func TestPresentationFullPageRenderCheckVariantIsStrict(t *testing.T) {
	input := "生成主 deck 10 页，允许 2 页附录，并全页渲染检查。"
	if !isStrictPresentationQARequest(input) {
		t.Fatal("full-page render-check wording did not activate strict QA")
	}
	if got := requestedPresentationMinimumSlides(input); got != 10 {
		t.Fatalf("requested minimum slides=%d, want 10", got)
	}
}

func TestPresentationPerSlideVisionWordingIsStrictAndBlocksBaseConvert(t *testing.T) {
	input := "制作固定 8 页 PPT；contact sheet 和 slide_01 至 slide_08 每一页分别调用 vision(path=...) 检查。"
	if !isStrictPresentationQARequest(input) {
		t.Fatal("explicit per-slide vision wording did not activate strict QA")
	}
	ctx := presentationTestContext(input)
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "persistent_shell", Arguments: map[string]interface{}{
		"command": "python scaffold_task_pptx.py --builder work/build_deck.py --output work/result.pptx",
	}}, `{"builder":"work/build_deck.py","output":"work/result.pptx"}`)
	convert := ToolCall{ID: "base", Name: "convert", Arguments: map[string]interface{}{
		"output_format": "pptx", "fallback": true, "generator": "base-ppt",
	}}
	result, err, blocked := checkPresentationToolGate(ctx, convert)
	if !blocked || err == nil || !strings.Contains(result.Content, "不能用 convert/base-ppt") {
		t.Fatalf("strict per-slide task escaped Level 2 through base-ppt: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestPresentationStrictGateIncludesFailedSelfAudit(t *testing.T) {
	ctx := presentationTestContext("生成 report.pptx，必须渲染全部页面并严格验收")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "qa-read", Name: "read_file", Arguments: map[string]interface{}{
		"path": "ppt_qa.md",
	}}, "## 数据一致性\n判定：❌ 不一致，需对齐\n\n总体：⚠️ 有项目待修。")
	issues := strings.Join(presentationBlockingIssues(ctx), " | ")
	if !strings.Contains(issues, "自检报告") {
		t.Fatalf("failed PPT self-audit did not block strict completion: %s", issues)
	}
}

func TestPresentationFollowupUsesRecentDeckPath(t *testing.T) {
	base := context.WithValue(context.Background(), "history_messages", []map[string]interface{}{
		{"role": "assistant", "content": `已生成 E:\\deliverables\\previous.pptx`},
	})
	ctx := withPresentationProductionState(base, "继续验收并交付")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected || state.Route != "qa-existing-deck" || !samePresentationPath(state.DeckPath, `E:\deliverables\previous.pptx`) {
		t.Fatalf("follow-up did not resume the recent deck: %#v", state)
	}

	modifyCtx := withPresentationProductionState(base, "继续修改上一份")
	modifyState := presentationProductionStateFromContext(modifyCtx)
	if modifyState == nil || !modifyState.Expected || modifyState.Route != "level2-task-builder" {
		t.Fatalf("modification follow-up did not re-enter production: %#v", modifyState)
	}
}

func TestPresentationStrictQANegationWins(t *testing.T) {
	if isStrictPresentationQARequest("不要严格验收，普通检查后交付即可") {
		t.Fatal("negated strict QA must not become a blocking policy")
	}
}

func TestPresentationReferenceRenderCannotSatisfyFinalDeckQA(t *testing.T) {
	ctx := presentationTestContext(`参考 E:\\good\\reference.pptx 重做这份 PPT`)
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "render-ref", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File C:\\runtime\\scripts\\render_pptx.ps1 -InputPptx E:\\good\\reference.pptx -OutputDir reference-preview`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-ref", Name: "vision", Arguments: map[string]interface{}{
		"path": `reference-preview\\contact-sheet.png`,
	}}, "参考稿视觉检查完成。")

	state := presentationProductionStateFromContext(ctx)
	if state.DeckCreated || state.Rendered || state.FinalVisualInspected || !state.ReferenceVisualInspected {
		t.Fatalf("reference evidence leaked into final-deck state: %#v", state)
	}
}

func TestPresentationReferencePathUsesItsSemanticRoleInsteadOfFirstPPTXPath(t *testing.T) {
	ctx := presentationTestContext(`请输出 E:\deliverables\final.pptx，并参考 E:\examples\brand-reference.pptx 的风格制作`)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.ReferenceRequired || !samePresentationPath(state.ReferencePath, `E:\examples\brand-reference.pptx`) {
		t.Fatalf("reference path role was confused with the earlier output path: %#v", state)
	}

	ctx = presentationTestContext(`E:\examples\brand-reference.pptx 作为参考，制作一份新的 PPT`)
	state = presentationProductionStateFromContext(ctx)
	if state == nil || !samePresentationPath(state.ReferencePath, `E:\examples\brand-reference.pptx`) {
		t.Fatalf("reference path following-label form was missed: %#v", state)
	}
}

func TestPresentationOutputFilenameAndLayoutRequestDoNotCreateReferenceRequirement(t *testing.T) {
	input := `制作固定 8 页 PPT，文件名 agentos_90d_capability_plan_r11.pptx；版式与视觉设计要清晰专业。`
	ctx := presentationTestContext(input)
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.Expected {
		t.Fatalf("presentation request was not recognized: %#v", state)
	}
	if state.ReferenceRequired || state.ReferencePath != "" {
		t.Fatalf("the requested output PPTX was misclassified as a reference deck: %#v", state)
	}
}

func TestPresentationQAEvidenceIsScopedToCurrentDeckAndRenderDirectory(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python C:\\runtime\\scripts\\scaffold_task_pptx.py --builder build_deck.py --output report-a.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report-a.pptx`,
	}}, "status: success")

	recordPresentationSuccess(ctx, ToolCall{ID: "audit-other", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report-b.pptx`,
	}}, "status: success\nPPTX_STRUCTURE_OK")
	recordPresentationSuccess(ctx, ToolCall{ID: "render-other", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report-b.pptx -OutputDir preview-b`,
	}}, "status: success\nPPTX_RENDER_OK")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-random", Name: "vision", Arguments: map[string]interface{}{
		"path": `brand-assets\\logo.png`,
	}}, "图片清晰。")

	state := presentationProductionStateFromContext(ctx)
	if state.AuditPassed || state.Rendered || state.FinalVisualInspected {
		t.Fatalf("evidence for another deck/image contaminated acceptance: %#v", state)
	}
	if !samePresentationPath(state.DeckPath, "report-a.pptx") {
		t.Fatalf("current deck path changed unexpectedly: %q", state.DeckPath)
	}
}

func TestPresentationVisualDefectCannotBeReportedAsVerified(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\\contact-sheet.png`,
	}}, "发现一处需要修正的裁切。")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "尚未修复") {
		t.Fatalf("detected visual defect must block substantive delivery: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationVisualDefectDetectionUnderstandsNegation(t *testing.T) {
	for _, clean := range []string{
		"页面清晰，未发现裁切、重叠或占位符。",
		"没有检测到溢出，整体排版正常。",
		"No clipping or overlap detected.",
	} {
		if presentationVisualDefectDetected(clean) {
			t.Fatalf("clean visual finding was misread as a defect: %q", clean)
		}
	}
	if !presentationVisualDefectDetected("未发现重叠，但第 10 页发现裁切，需要修正。") {
		t.Fatal("a real defect after a negative finding was suppressed")
	}
}

func TestPresentationVisualAcceptanceTracksAllRenderedSlides(t *testing.T) {
	ctx := presentationTestContext("生成一份 15 页产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report.pptx`,
	}}, `{"pptx_structure_valid":true,"slide_count":15}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, `{"ok":true,"presentation_rendered":true,"slide_count":15,"output_dir":"preview"}`)

	for _, slide := range []int{1, 2, 3, 4, 6, 7, 8, 9, 15} {
		recordPresentationSuccess(ctx, ToolCall{ID: fmt.Sprintf("vision-%d", slide), Name: "vision", Arguments: map[string]interface{}{
			"path": fmt.Sprintf(`preview\slide_%02d.png`, slide),
		}}, "该页布局清晰，未发现异常。")
	}
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "只覆盖部分") {
		t.Fatalf("nine inspected pages must block a fifteen-page deck: %#v ok=%v", acceptance, ok)
	}

	for _, slide := range []int{5, 10, 11, 12, 13, 14} {
		recordPresentationSuccess(ctx, ToolCall{ID: fmt.Sprintf("vision-%d", slide), Name: "vision", Arguments: map[string]interface{}{
			"path": fmt.Sprintf(`preview\slide_%02d.png`, slide),
		}}, "该页布局清晰，未发现异常。")
	}
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 15)
	acceptance, ok = presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "verified" || len(acceptance.Warnings) != 0 {
		t.Fatalf("all fifteen distinct rendered pages should complete coverage: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationExplicitPerSlideVisionCannotBeSatisfiedByContactSheet(t *testing.T) {
	ctx := presentationTestContext("制作固定 3 页 PPT；先看 contact sheet，再对 slide_01 至 slide_03 每一页分别调用 vision(path=...) 检查。")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report.pptx`,
	}}, `{"pptx_structure_valid":true,"slide_count":3}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, `{"presentation_rendered":true,"slide_count":3,"output_dir":"preview"}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-contact", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\contact_sheet.jpg`,
	}}, "所有页面检查完成，未发现裁切或重叠。")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	issues := strings.Join(acceptance.BlockingIssues, " | ")
	if !ok || acceptance.Level != "blocked" || !strings.Contains(issues, "成功记录仅 0/3") {
		t.Fatalf("contact sheet incorrectly satisfied explicit per-slide QA: %#v ok=%v", acceptance, ok)
	}

	for slide := 1; slide <= 3; slide++ {
		recordPresentationSuccess(ctx, ToolCall{ID: fmt.Sprintf("vision-slide-%d", slide), Name: "vision", Arguments: map[string]interface{}{
			"path": fmt.Sprintf(`preview\slide_%02d.png`, slide),
		}}, "该页布局清晰，未发现异常。")
	}
	completePresentationAestheticQAForTest(presentationProductionStateFromContext(ctx), 3)
	acceptance, ok = presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "verified" || len(acceptance.BlockingIssues) != 0 || len(acceptance.Warnings) != 0 {
		t.Fatalf("distinct successful per-slide vision calls did not complete QA: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationEightPageFormalDeckRequiresIndividualSlideVision(t *testing.T) {
	ctx := presentationTestContext("生成一份 8 页产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report.pptx --min-body-font-size 16 --min-title-font-size 35`,
	}}, `{"pptx_structure_valid":true,"slide_count":8}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, `{"presentation_rendered":true,"slide_count":8,"output_dir":"preview"}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-contact", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\contact_sheet.jpg`,
	}}, "整套节奏清晰，无裁切或重叠。")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	issues := strings.Join(acceptance.BlockingIssues, " | ")
	if !ok || acceptance.Level != "blocked" ||
		!strings.Contains(issues, "正式对外稿要求逐页") ||
		!strings.Contains(issues, "成功记录仅 0/8") {
		t.Fatalf("contact sheet incorrectly completed default eight-page QA: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationVisualDefectRemainsUntilDeckIsRerendered(t *testing.T) {
	ctx := presentationTestContext("生成一份产品介绍 PPT")
	activatePresentationArtDirector(ctx)
	recordPresentationSuccess(ctx, ToolCall{ID: "scaffold", Name: "cli", Arguments: map[string]interface{}{
		"command": `python scaffold_task_pptx.py --builder build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "build", Name: "cli", Arguments: map[string]interface{}{
		"command": `python build_deck.py --output report.pptx`,
	}}, "status: success")
	recordPresentationSuccess(ctx, ToolCall{ID: "audit", Name: "cli", Arguments: map[string]interface{}{
		"command": `python audit_pptx.py report.pptx`,
	}}, `{"pptx_structure_valid":true}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "render", Name: "cli", Arguments: map[string]interface{}{
		"command": `powershell -File render_pptx.ps1 -InputPptx report.pptx -OutputDir preview`,
	}}, `{"presentation_rendered":true,"slide_count":2}`)
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-1", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\slide_01.png`,
	}}, "发现裁切，需要修正。")
	recordPresentationSuccess(ctx, ToolCall{ID: "vision-2", Name: "vision", Arguments: map[string]interface{}{
		"path": `preview\slide_02.png`,
	}}, "第二页布局清晰。")

	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok || acceptance.Level != "blocked" || !strings.Contains(strings.Join(acceptance.BlockingIssues, " | "), "尚未修复") {
		t.Fatalf("a later clean page must not erase an earlier defect: %#v ok=%v", acceptance, ok)
	}
}

func TestPresentationUserSkillCanSelectCustomRoute(t *testing.T) {
	ctx := presentationTestContext("生成一份品牌路演 PPT")
	recordPresentationSuccess(ctx, ToolCall{ID: "custom-skill", Name: "use_skill", Arguments: map[string]interface{}{
		"skill_name": "company-deck-builder",
	}}, "用户技能：公司品牌 Presentation Builder")
	state := presentationProductionStateFromContext(ctx)
	if state == nil || !state.ArtDirectorActivated || state.Route != "custom-presentation-skill" {
		t.Fatalf("user-defined presentation skill was not accepted: %#v", state)
	}
}
