package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePPTPolishPreviewPNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			picture.Set(x, y, color.RGBA{R: 20, G: 40, B: 60, A: 255})
		}
	}
	if err := png.Encode(file, picture); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writePPTPolishRecoveryFixture(t *testing.T, path string, slides int, fractional bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	write := func(name, content string) {
		t.Helper()
		entry, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	write("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/></Types>`)
	write("_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`)
	var slideIDs, relationships strings.Builder
	for number := 1; number <= slides; number++ {
		slideIDs.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+number, number))
		relationships.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, number, number))
		x := "914400"
		if fractional && number == slides {
			x = "914400.5"
		}
		write(fmt.Sprintf("ppt/slides/slide%d.xml", number), fmt.Sprintf(`<?xml version="1.0"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:spPr><a:xfrm><a:off x="%s" y="914400"/><a:ext cx="3657600" cy="914400"/></a:xfrm></p:spPr><p:txBody><a:p><a:r><a:t>Generic slide %d</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`, x, number))
	}
	write("ppt/presentation.xml", `<?xml version="1.0"?><p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst>`+slideIDs.String()+`</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/></p:presentation>`)
	write("ppt/_rels/presentation.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+relationships.String()+`</Relationships>`)
	paddingHeader := &zip.FileHeader{Name: "ppt/media/unreferenced-padding.bin", Method: zip.Store}
	padding, createErr := archive.CreateHeader(paddingHeader)
	if createErr != nil {
		t.Fatal(createErr)
	}
	if _, err := padding.Write(bytes.Repeat([]byte{0x5a}, 16*1024)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPPTPolishPipelineAdaptsToFourInputModes(t *testing.T) {
	pipeline := (&GatewayService{}).defaultPipelineSet()["ppt-polish"]
	if len(pipeline.Steps) != 4 {
		t.Fatalf("steps = %d, want 4 quality stages", len(pipeline.Steps))
	}
	if pipeline.Steps[0].Name != "素材读取" || pipeline.Steps[1].Name != "内容重构" || pipeline.Steps[2].Name != "视觉润色" || pipeline.Steps[3].Name != "交付自查" {
		t.Fatalf("unexpected PPT quality stages: %#v", pipeline.Steps)
	}
	allPrompts := ""
	for _, step := range pipeline.Steps {
		allPrompts += "\n" + step.Prompt
	}
	for _, want := range []string{
		"PPT_INPUT_MODE", "existing_deck", "source_material", "detailed_brief", "concept_only",
		"不得要求补原始 PPT", "从零制作", "普通缺项采用合理默认值",
		"existing_deck 是真正的", "不得声称继承不存在的旧稿",
		"适配当前操作系统", "ppt_work_${execution_id}", "不是用户输入缺失",
	} {
		if !strings.Contains(allPrompts, want) {
			t.Fatalf("adaptive PPT prompts missing %q", want)
		}
	}
	visualStep := pipeline.Steps[2]
	if visualStep.MaxIter != 150 {
		t.Fatalf("visual production iteration budget=%d, want 150", visualStep.MaxIter)
	}
	if visualStep.TimeoutSeconds != 1800 {
		t.Fatalf("visual production timeout=%d, want 1800 seconds for the 150-iteration budget", visualStep.TimeoutSeconds)
	}
	if visualStep.OutputTokenLimit != 8000 {
		t.Fatalf("visual production output budget=%d, want 8000", visualStep.OutputTokenLimit)
	}
	if !containsString(visualStep.Tools, "edit_file") {
		t.Fatalf("visual production cannot edit its scaffold: %#v", visualStep.Tools)
	}
	if !containsString(visualStep.Tools, "vision") {
		t.Fatalf("visual production cannot inspect rendered slides: %#v", visualStep.Tools)
	}
	for _, want := range []string{
		"vision(path=", "watch(file=...)", "slide_NN.png", "逐页缺陷清单",
		"不存在负责预览的下游步骤", "--min-body-font-size", "--min-title-font-size",
		"逐页定制是所有正式", "TASK PAGE COMPOSITIONS", "不要读取或改写 pptx_design.py",
		"不要用 write_file 覆盖整份 builder", "8 页稿至少使用 4 种正文构图家族",
		"租户根目录", "最终渲染之后不得再编辑或重建",
	} {
		if !strings.Contains(visualStep.Prompt, want) {
			t.Fatalf("visual production prompt missing strict QA marker %q", want)
		}
	}
	bidPipeline := (&GatewayService{}).defaultPipelineSet()["bid-proposal"]
	if len(bidPipeline.Steps) < 3 || bidPipeline.Steps[2].MaxIter != 150 || bidPipeline.Steps[2].TimeoutSeconds != 1800 || bidPipeline.Steps[2].OutputTokenLimit != 8000 {
		t.Fatalf("bid-proposal PPT-capable step must share the 150-iteration production budget: %#v", bidPipeline.Steps)
	}
	deliveryStep := pipeline.Steps[3]
	if deliveryStep.Rework == nil || deliveryStep.Rework.Target != "视觉润色" || deliveryStep.Rework.MaxReworks != 2 || deliveryStep.Rework.OnExhausted != "fail" || !deliveryStep.Rework.AllowSideEffects {
		t.Fatalf("PPT delivery audit must allow exactly two bounded visual revisions: %#v", deliveryStep.Rework)
	}
}

func TestPPTPolishAuditFailureRequestsOneFreshVisualRevision(t *testing.T) {
	pipeline := (&GatewayService{}).defaultPipelineSet()["ppt-polish"]
	exec := &PipelineExecution{
		PipelineName:    "ppt-polish",
		RuntimePipeline: &pipeline,
		Steps: []StepResult{
			{Name: "素材读取", Status: "completed", Output: "source"},
			{Name: "内容重构", Status: "completed", Output: "brief"},
			{Name: "视觉润色", Status: "completed", Output: "PPTX真实路径：.openkitty/storage/report.pptx"},
			{Name: "交付自查", Status: "running"},
		},
	}
	if !pptPolishDeliveryAuditCanRework(exec, 3) {
		t.Fatal("configured PPT delivery step did not enable audit-driven visual rework")
	}
	state := exec.presentationStateForStep(context.Background(), 2, "制作 8 页 PPT，并逐页检查")
	state.mu.Lock()
	state.ArtDirectorActivated = true
	state.ActivatedSkill = "ppt-art-director"
	state.Route = "level2-task-builder"
	state.Scaffolded = true
	state.BuilderRead = true
	state.BuilderCustomized = true
	state.BuilderPath = ".openkitty/storage/ppt_work_run/build_deck.py"
	state.DeckCreated = true
	state.DeckPath = ".openkitty/storage/report.pptx"
	state.DeckSlideCount = 8
	state.AuditPassed = true
	state.Rendered = true
	state.RenderedSlideCount = 8
	state.FinalVisualInspected = true
	state.VisualCoverageComplete = true
	state.VisualInspectedImages["preview/slide_08.png"] = true
	state.mu.Unlock()

	auditJSON := `{"fatal_errors":["slide 8 has 1 non-chrome text boxes below the requested 14pt body floor"]}`
	reason := pptPolishAuditReworkReason(auditJSON, errors.New("audit failed"))
	output := buildPPTPolishAuditReworkOutput(reason)
	if !strings.Contains(output, pipelineReworkControlPrefix) || !strings.Contains(output, "slide 8") {
		t.Fatalf("audit rework output omitted the concrete blocker: %s", output)
	}

	outcome, err := (&PipelineExecutor{}).processPipelineReworkDecision(exec, pipeline.Steps, 3, output)
	if err != nil || !outcome.Requested || outcome.TargetIndex != 2 || outcome.Attempt != 1 || outcome.Max != 2 {
		t.Fatalf("bounded visual rework not requested: outcome=%#v err=%v", outcome, err)
	}
	context := pendingPipelineReworkContext(exec, 2)
	for _, want := range []string{
		"Do not call use_skill or scaffold_task_pptx.py again",
		"Correct only the concrete audit defect",
		"rerun the bundled audit_pptx.py with the user's font floors",
		"vision on contact_sheet.jpg and every slide_NN.png again",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("PPT audit rework context missing %q: %s", want, context)
		}
	}

	state.mu.Lock()
	if !state.ArtDirectorActivated || !state.Scaffolded || state.BuilderPath == "" {
		t.Fatalf("rework discarded the selected route or scaffold: %#v", state)
	}
	if !state.BuilderRead || !state.BuilderCustomized {
		t.Fatalf("rework discarded the completed per-slide builder contract: %#v", state)
	}
	if state.DeckCreated || state.AuditPassed || state.Rendered || state.FinalVisualInspected || state.VisualCoverageComplete || len(state.VisualInspectedImages) != 0 {
		t.Fatalf("rework inherited evidence tied to superseded PPTX bytes: %#v", state)
	}
	state.mu.Unlock()

	second, err := (&PipelineExecutor{}).processPipelineReworkDecision(exec, pipeline.Steps, 3, output)
	if err != nil || !second.Requested || second.Attempt != 2 || second.Max != 2 {
		t.Fatalf("second bounded visual rework was not allowed: outcome=%#v err=%v", second, err)
	}
	if _, err := (&PipelineExecutor{}).processPipelineReworkDecision(exec, pipeline.Steps, 3, output); err == nil || !strings.Contains(err.Error(), "rework exhausted") {
		t.Fatalf("a third visual rework was not blocked: %v", err)
	}
}

func TestPPTPolishFreshnessRevisionPreservesDeckAndForbidsRebuild(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-freshness",
		PipelineName: "ppt-polish",
		Input:        "制作 PPT，文件名 frozen.pptx",
		Steps:        []StepResult{{Name: "视觉润色"}, {Name: "交付自查"}},
	}
	state := exec.presentationStateForStep(context.Background(), 0, exec.Input)
	state.DeckCreated = true
	state.DeckPath = ".openkitty/storage/frozen.pptx"
	state.DeckSlideCount = 8
	state.Rendered = true
	state.FinalVisualInspected = true
	exec.resetPresentationProductionEvidenceForStep(0, "visual evidence is stale; rerender the exact current PPTX")
	if !state.BoundedRevision || state.RevisionMode != "freshness" || state.RevisionBuildLimit != 0 || state.RevisionBuildCount != 0 {
		t.Fatalf("freshness-only revision was not configured as a zero-build checkpoint: %#v", state)
	}
	if !state.DeckCreated || state.DeckPath != ".openkitty/storage/frozen.pptx" || state.DeckSlideCount != 8 {
		t.Fatalf("freshness-only reset discarded frozen deck identity: %#v", state)
	}
	if state.Rendered || state.FinalVisualInspected || state.VisualCoverageComplete {
		t.Fatalf("freshness-only reset inherited stale render/vision evidence: %#v", state)
	}
}

func TestHostPPTDeliveryAuditReturnsStructuredVisualRework(t *testing.T) {
	python, err := osexec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := osexec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx is unavailable: %v: %s", err, output)
	}

	workspace := t.TempDir()
	target := filepath.Join(workspace, ".openkitty", "storage", "report.pptx")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	create := osexec.Command(python, "-c", `
import os
from pptx import Presentation
from pptx.util import Inches, Pt

p = Presentation()
for index in range(4):
    slide = p.slides.add_slide(p.slide_layouts[6])
    title = slide.shapes.add_textbox(Inches(0.8), Inches(0.6), Inches(8), Inches(0.6))
    title_run = title.text_frame.paragraphs[0].add_run()
    title_run.text = f"Management headline {index + 1}"
    title_run.font.size = Pt(28)
    body = slide.shapes.add_textbox(Inches(0.8), Inches(2.0), Inches(8), Inches(1.0))
    body_run = body.text_frame.paragraphs[0].add_run()
    body_run.text = "Required audience body copy"
    body_run.font.size = Pt(11)
p.save(os.environ["PPTX_TARGET"])
`)
	create.Env = append(os.Environ(), "PYTHONUTF8=1", "PPTX_TARGET="+target)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create tiny-font PPTX: %v\n%s", err, output)
	}

	pipeline := (&GatewayService{}).defaultPipelineSet()["ppt-polish"]
	exec := &PipelineExecution{
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Input:             "制作固定 4 页 PPT，文件名 report.pptx，所有正文至少 14pt。",
		RuntimePipeline:   &pipeline,
		Steps: []StepResult{
			{Name: "素材读取", Status: "completed", Output: "concept only"},
			{Name: "内容重构", Status: "completed", Output: "brief"},
			{Name: "视觉润色", Status: "completed", Output: "PPTX真实路径：" + target},
			{Name: "交付自查", Status: "running"},
		},
	}
	output, ready, err := (&PipelineExecutor{}).hostValidatePPTPolish(context.Background(), exec, 3, exec.Input)
	if err != nil || !ready {
		t.Fatalf("audit defect did not become a structured visual rework: ready=%v err=%v output=%s", ready, err, output)
	}
	control, _, found, parseErr := parsePipelineReworkControl(output)
	if parseErr != nil || !found || control.Status != "retry_required" || !strings.Contains(control.Reason, "14pt body floor") {
		t.Fatalf("unexpected audit rework control: control=%#v found=%v err=%v output=%s", control, found, parseErr, output)
	}
}

func TestPPTPolishVisualEvidenceRequiresContactSheetAndEverySlide(t *testing.T) {
	execID := "pipe-visual-evidence-current"
	preview := ".openkitty/storage/ppt_work_" + sanitizeFilename(execID) + "/preview/"
	visual := StepResult{Name: "视觉润色"}
	visual.ToolCalls = append(visual.ToolCalls, ToolCallRecord{
		ToolName: "vision",
		Status:   "success",
		Params:   `{"path":"` + preview + `contact_sheet.jpg"}`,
	})
	for number := 1; number <= 8; number++ {
		visual.ToolCalls = append(visual.ToolCalls, ToolCallRecord{
			ToolName: "vision",
			Status:   "success",
			Params:   `{"path":"` + preview + `slide_0` + string(rune('0'+number)) + `.png"}`,
		})
	}
	exec := &PipelineExecution{ID: execID, PipelineName: "ppt-polish", Steps: []StepResult{visual}}
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err != nil {
		t.Fatalf("complete visual evidence rejected: %v", err)
	}
	exec.Steps[0].ToolCalls = exec.Steps[0].ToolCalls[:len(exec.Steps[0].ToolCalls)-1]
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err == nil || !strings.Contains(err.Error(), "slide_08") {
		t.Fatalf("missing per-slide vision evidence was not diagnosed: %v", err)
	}
}

func TestPPTPolishVisualEvidenceRejectsSharedOrStalePreview(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe-current-preview",
		PipelineName: "ppt-polish",
		Steps: []StepResult{{Name: "视觉润色", ToolCalls: []ToolCallRecord{
			{ToolName: "vision", Status: "success", Params: `{"path":".openkitty/storage/rendered_pages/contact_sheet.jpg"}`},
			{ToolName: "vision", Status: "success", Params: `{"path":".openkitty/storage/ppt_work_pipe-old-preview/preview/slide_01.png"}`},
		}}},
	}
	err := validatePPTPolishVisualEvidence(exec, 0, 8)
	if err == nil || !strings.Contains(err.Error(), "current-run preview") || !strings.Contains(err.Error(), "inspected_slides=0/8") {
		t.Fatalf("stale visual evidence was accepted or poorly diagnosed: %v", err)
	}
}

func TestPPTPolishVisualEvidenceMustFollowFinalBuildAndRender(t *testing.T) {
	execID := "pipe-final-build-freshness"
	preview := ".openkitty/storage/ppt_work_" + sanitizeFilename(execID) + "/preview"
	deck := ".openkitty/storage/final-freshness.pptx"
	started := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	visual := StepResult{Name: "视觉润色"}
	visual.ToolCalls = append(visual.ToolCalls, ToolCallRecord{
		ToolName:  "vision",
		Status:    "success",
		Timestamp: started.Add(time.Second),
		Params:    `{"path":"` + preview + `/contact_sheet.jpg"}`,
	})
	for number := 1; number <= 8; number++ {
		visual.ToolCalls = append(visual.ToolCalls, ToolCallRecord{
			ToolName:  "vision",
			Status:    "success",
			Timestamp: started.Add(time.Duration(number+1) * time.Second),
			Params:    fmt.Sprintf(`{"path":"%s/slide_%02d.png"}`, preview, number),
		})
	}
	visual.ToolCalls = append(visual.ToolCalls, ToolCallRecord{
		ToolName:  "persistent_shell",
		Status:    "success",
		Timestamp: started.Add(20 * time.Second),
		Params: fmt.Sprintf(
			`{"command":"python3 build_deck.py deck_spec.json %s"}`,
			deck,
		),
		Result: "Command Execution\nstatus: succeeded\noutput result: " + deck,
	})
	exec := &PipelineExecution{ID: execID, PipelineName: "ppt-polish", Steps: []StepResult{visual}}
	err := validatePPTPolishVisualEvidence(exec, 0, 8)
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "rerender") {
		t.Fatalf("post-vision rebuild was not rejected as stale evidence: %v", err)
	}

	exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
		ToolName:  "persistent_shell",
		Status:    "success",
		Timestamp: started.Add(30 * time.Second),
		Params: fmt.Sprintf(
			`{"command":"python3 render_pptx.py --input %s --output-dir %s"}`,
			deck,
			preview,
		),
		Result: "Command Execution\nstatus: succeeded\noutput result: rendered 8 slides to " + preview,
	})
	exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
		ToolName:  "vision",
		Status:    "success",
		Timestamp: started.Add(31 * time.Second),
		Params:    `{"path":"` + preview + `/contact_sheet.jpg"}`,
	})
	for number := 1; number <= 8; number++ {
		exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
			ToolName:  "vision",
			Status:    "success",
			Timestamp: started.Add(time.Duration(number+31) * time.Second),
			Params:    fmt.Sprintf(`{"path":"%s/slide_%02d.png"}`, preview, number),
		})
	}
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err != nil {
		t.Fatalf("fresh post-build render and per-slide vision were rejected: %v", err)
	}
}

func TestPPTPolishVisualEvidenceAcceptsCompleteFreshPreviewFromWrappedRenderer(t *testing.T) {
	workspace := t.TempDir()
	execID := "pipe-wrapped-render-freshness"
	exec := &PipelineExecution{
		ID:                execID,
		PipelineName:      "ppt-polish",
		Input:             "制作固定 8 页 PPT，文件名 wrapped_render.pptx。",
		ResolvedWorkspace: workspace,
		Steps:             []StepResult{{Name: "视觉润色"}},
	}
	deck := pptPolishDefaultOutputPath(exec, workspace)
	previewRelative := pptPolishCurrentRunPreviewRelativePath(exec)
	preview := filepath.Join(workspace, filepath.FromSlash(previewRelative))
	if err := os.MkdirAll(filepath.Dir(deck), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deck, bytes.Repeat([]byte("pptx"), 3*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	if err := os.Chtimes(deck, base, base); err != nil {
		t.Fatal(err)
	}
	imagePaths := []string{filepath.Join(preview, "contact_sheet.jpg")}
	for number := 1; number <= 8; number++ {
		imagePaths = append(imagePaths, filepath.Join(preview, fmt.Sprintf("slide_%02d.png", number)))
	}
	for _, path := range imagePaths {
		writePPTPolishPreviewPNG(t, path)
		if err := os.Chtimes(path, base.Add(5*time.Second), base.Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	toolDeck := filepath.ToSlash(deck)
	exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
		ToolName:  "persistent_shell",
		Status:    "success",
		Timestamp: base.Add(-time.Second),
		Params:    fmt.Sprintf(`{"command":"python3 build_deck.py deck_spec.json %s"}`, toolDeck),
		Result:    "Command Execution\nstatus: succeeded\noutput result: " + toolDeck,
	})
	exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
		ToolName:  "vision",
		Status:    "success",
		Timestamp: base.Add(10 * time.Second),
		Params:    fmt.Sprintf(`{"path":"%s/contact_sheet.jpg"}`, previewRelative),
	})
	for number := 1; number <= 8; number++ {
		exec.Steps[0].ToolCalls = append(exec.Steps[0].ToolCalls, ToolCallRecord{
			ToolName:  "vision",
			Status:    "success",
			Timestamp: base.Add(time.Duration(number+10) * time.Second),
			Params:    fmt.Sprintf(`{"path":"%s/slide_%02d.png"}`, previewRelative, number),
		})
	}
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err != nil {
		t.Fatalf("fresh wrapped render evidence was rejected: %v", err)
	}

	staleSlide := filepath.Join(preview, "slide_08.png")
	if err := os.Chtimes(staleSlide, base.Add(-time.Second), base.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err == nil || !strings.Contains(err.Error(), "slide_08 predates") {
		t.Fatalf("preview image older than final deck was accepted: %v", err)
	}
	if err := os.Chtimes(staleSlide, base.Add(5*time.Second), base.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staleSlide); err != nil {
		t.Fatal(err)
	}
	if err := validatePPTPolishVisualEvidence(exec, 0, 8); err == nil || !strings.Contains(err.Error(), "slide_08 is missing") {
		t.Fatalf("incomplete wrapped preview was accepted: %v", err)
	}
}

func TestHostPPTDeliveryTurnsStaleVisualEvidenceIntoBoundedRework(t *testing.T) {
	python, err := osexec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := osexec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx is unavailable: %v: %s", err, output)
	}
	workspace := t.TempDir()
	pipeline := (&GatewayService{}).defaultPipelineSet()["ppt-polish"]
	execID := "pipe-host-stale-visual"
	input := "制作固定 8 页 PPT，文件名 stale_visual_rework.pptx。"
	target := filepath.Join(workspace, ".openkitty", "storage", "stale_visual_rework.pptx")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	create := osexec.Command(python, "-c", `
import os
from pptx import Presentation
from pptx.util import Inches, Pt

p = Presentation()
p.slide_width = Inches(13.333)
p.slide_height = Inches(7.5)
for index in range(8):
    slide = p.slides.add_slide(p.slide_layouts[6])
    title = slide.shapes.add_textbox(Inches(0.8), Inches(0.5), Inches(10.5), Inches(0.7))
    title_run = title.text_frame.paragraphs[0].add_run()
    title_run.text = f"Management result {index + 1}"
    title_run.font.size = Pt(28)
    body = slide.shapes.add_textbox(Inches(0.8), Inches(1.7), Inches(10.5), Inches(1.2))
    body_run = body.text_frame.paragraphs[0].add_run()
    body_run.text = f"Validated audience content for slide {index + 1}"
    body_run.font.size = Pt(18)
p.save(os.environ["PPTX_TARGET"])
`)
	create.Env = append(os.Environ(), "PYTHONUTF8=1", "PPTX_TARGET="+target)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create valid stale-evidence fixture: %v\n%s", err, output)
	}
	preview := ".openkitty/storage/ppt_work_" + sanitizeFilename(execID) + "/preview"
	toolTarget := filepath.ToSlash(target)
	started := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	visual := StepResult{
		Name:   "视觉润色",
		Status: "completed",
		Output: "PPTX真实路径：" + target,
		ToolCalls: []ToolCallRecord{
			{ToolName: "vision", Status: "success", Timestamp: started, Params: `{"path":"` + preview + `/contact_sheet.jpg"}`},
			{
				ToolName:  "persistent_shell",
				Status:    "success",
				Timestamp: started.Add(time.Second),
				Params:    fmt.Sprintf(`{"command":"python3 build_deck.py deck_spec.json %s"}`, toolTarget),
				Result:    "Command Execution\nstatus: succeeded\noutput result: " + toolTarget,
			},
		},
	}
	execution := &PipelineExecution{
		ID:                execID,
		PipelineName:      "ppt-polish",
		Input:             input,
		ResolvedWorkspace: workspace,
		RuntimePipeline:   &pipeline,
		Steps: []StepResult{
			{Name: "素材读取", Status: "completed", Output: "用户当前请求"},
			{Name: "内容重构", Status: "completed", Output: "brief"},
			visual,
			{Name: "交付自查", Status: "running"},
		},
	}
	executor := &PipelineExecutor{gateway: &GatewayService{config: &Config{Workspace: workspace}}, fileLogger: log.New(io.Discard, "", 0)}
	output, ready, err := executor.hostValidatePPTPolish(context.Background(), execution, 3, input)
	if err != nil || !ready {
		t.Fatalf("stale evidence did not become a structured rework: ready=%v err=%v output=%s", ready, err, output)
	}
	control, _, found, parseErr := parsePipelineReworkControl(output)
	if parseErr != nil || !found || control.Status != "retry_required" || !strings.Contains(control.Reason, "stale") || !strings.Contains(control.Reason, "rerender") {
		t.Fatalf("unexpected stale-evidence rework control: control=%#v found=%v err=%v output=%s", control, found, parseErr, output)
	}
	outcome, err := executor.processPipelineReworkDecision(execution, pipeline.Steps, 3, output)
	if err != nil || !outcome.Requested || outcome.TargetIndex != 2 || outcome.Attempt != 1 || outcome.Max != 2 {
		t.Fatalf("host stale-evidence rework was not routed to visual production: outcome=%#v err=%v", outcome, err)
	}
	context := pendingPipelineReworkContext(execution, 2)
	for _, want := range []string{"PPT freshness-only revision rules", "Freeze the existing deck_spec.json", "do not edit or rebuild", "exact contracted run-scoped preview"} {
		if !strings.Contains(context, want) {
			t.Fatalf("freshness-only recovery context missing %q: %s", want, context)
		}
	}
}

func TestPPTPolishRequestFontFloorsBecomeAuditArguments(t *testing.T) {
	got := strings.Join(pptxAuditArgumentsForRequest("所有正文至少 14pt，标题至少 26pt。"), " ")
	want := "--min-body-font-size 14 --min-title-font-size 26"
	if got != want {
		t.Fatalf("audit arguments=%q, want %q", got, want)
	}
	if got := pptxAuditArgumentsForRequest("清晰专业即可"); len(got) != 0 {
		t.Fatalf("unrequested font floor was invented: %#v", got)
	}
}

func TestPPTPolishRequiredAudienceTextPreservesLabeledQuotedPhrases(t *testing.T) {
	request := `封面副标题“从可用走向可信交付”；右侧结论“能力提升的关键是闭环质量，不是单纯增加工具”。三个阶段分别是“稳定与可观测”“质量与专业化”“规模化与运营”。目标能力架构：需求入口→编排与角色→工具执行→结果/视觉/数字 QA→交付与反馈，画成五层流程。视觉采用“暖白咨询风”，不要出现“机密”。`
	want := []string{
		"从可用走向可信交付",
		"能力提升的关键是闭环质量，不是单纯增加工具",
		"稳定与可观测",
		"质量与专业化",
		"规模化与运营",
		"需求入口",
		"编排与角色",
		"工具执行",
		"结果/视觉/数字 QA",
		"交付与反馈",
	}
	if got := pptPolishRequiredAudiencePhrases(request); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("required audience phrases=%q, want %q", got, want)
	}
	visible := "从可用走向可信交付\n能力提升的关键是闭环质量,不是单纯增加工具\n稳定与可观测  质量与专业化  规模化与运营\n需求入口 编排与角色 工具执行 结果 / 视觉 / 数字 QA 交付与反馈"
	if err := validatePPTPolishRequiredAudienceText(request, visible); err != nil {
		t.Fatalf("verbatim audience requirements were rejected: %v", err)
	}
	if err := validatePPTPolishRequiredAudienceText(request, strings.Replace(visible, "能力提升的关键是闭环质量,不是单纯增加工具", "闭环质量才是能力提升的关键", 1)); err == nil || !strings.Contains(err.Error(), "不是单纯增加工具") {
		t.Fatalf("paraphrased required conclusion was accepted: %v", err)
	}
	if err := validatePPTPolishRequiredAudienceText(request, strings.Replace(visible, "结果 / 视觉 / 数字 QA", "视觉 QA", 1)); err == nil || !strings.Contains(err.Error(), "结果/视觉/数字 QA") {
		t.Fatalf("weakened arrow-chain label was accepted: %v", err)
	}
}

func TestPPTPolishClosedDetailedBriefRejectsUnsupportedNumericClaims(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{
		Name:   "素材读取",
		Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n可信素材与来源：用户当前请求；未读取外部网页或其他任务文件。",
	}}}
	request := "基线为 72%、58%、P95 18 分钟、重试率 12%；90 天目标为 90%、85%、≤8 分钟、≤4%。列为 30天/60天/90天。"
	visible := "基线 72% 58% 18分钟 12%；目标 90% 85% ≤8分钟 ≤4%；30 / 60 / 90 天；其余为未提供、待确认、未统计。"
	if err := validatePPTPolishDetailedBriefNumericClaims(exec, request, visible); err != nil {
		t.Fatalf("user-provided numeric claims were rejected: %v", err)
	}
	bad := visible + " 30 天 78%，60 天 84%，P95 ≤14 分钟，致命版式问题下降 50%。"
	err := validatePPTPolishDetailedBriefNumericClaims(exec, request, bad)
	if err == nil || !strings.Contains(err.Error(), "78%") || !strings.Contains(err.Error(), "≤14分钟") || !strings.Contains(err.Error(), "50%") {
		t.Fatalf("unsupported detailed-brief numbers were accepted: %v", err)
	}
}

func TestPPTPolishClosedDetailedBriefRejectsMissingSuppliedNumericClaims(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{
		Name:   "素材读取",
		Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n未读取外部网页",
	}}}
	request := "基线为任务成功率 72%、一次验收率 58%、P95 耗时 18 分钟、重试率 12%；90 天目标为 90%、85%、≤8 分钟、≤4%。"
	visible := "任务成功率 72%，90 天目标 90%；其余待确认。"
	err := validatePPTPolishRequiredNumericClaims(exec, request, visible)
	if err == nil || !strings.Contains(err.Error(), "58%") || !strings.Contains(err.Error(), "18分钟") || !strings.Contains(err.Error(), "≤4%") {
		t.Fatalf("missing user-supplied numbers were accepted: %v", err)
	}
	complete := "90天计划：基线 72%、58%、18 分钟、12%；目标 90%、85%、≤8 分钟、≤4%。"
	if err := validatePPTPolishRequiredNumericClaims(exec, request, complete); err != nil {
		t.Fatalf("complete user-supplied numeric set was rejected: %v", err)
	}
}

func TestPPTPolishRequiredAudienceTextIncludesExplicitListsAndColumns(t *testing.T) {
	request := "KPI 表包含任务成功率、一次验收率、P95 耗时、重试率、PPT 致命版式问题、报价复算差异；列为指标/基线/30天/60天/90天/责任人，文字必须清晰可读。"
	visible := "指标 基线 30天 60天 90天 责任人\n任务成功率 一次验收率 P95 耗时 重试率 PPT 致命版式问题 报价复算差异"
	if err := validatePPTPolishRequiredAudienceText(request, visible); err != nil {
		t.Fatalf("explicit metric and column lists were rejected: %v", err)
	}
	err := validatePPTPolishRequiredAudienceText(request, strings.Replace(visible, "报价复算差异", "", 1))
	if err == nil || !strings.Contains(err.Error(), "报价复算差异") {
		t.Fatalf("missing explicit list item was accepted: %v", err)
	}
}

func TestPPTPolishDeckSpecRejectsUnitlessClaimsAndPrematureDecisions(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:           "pipe-spec-grounding",
		PipelineName: "ppt-polish",
		Steps: []StepResult{{
			Name:   "素材读取",
			Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n未读取外部网页",
		}},
	}
	request := "基线为任务成功率72%；90天目标为90%，列为30天/60天/90天。KPI表还要包含PPT致命版式问题、报价复算差异。管理层需确认三项决策（优先级、验收口径、试点团队）。"
	workDir := filepath.Join(workspace, filepath.FromSlash(pptPolishCurrentRunWorkRelativePath(exec)))
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(workDir, "deck_spec.json")
	bad := `{"slides":[
		{"kind":"table","columns":["指标","基线","30天","60天","90天","责任人"],"rows":[
			["任务成功率","72%","待确认","待确认","90%","产品负责人"],
			["PPT 致命版式问题","未提供","待确认","待确认","0","设计负责人"],
			["报价复算差异","未提供","待确认","待确认","≤1%","财务接口人"]]},
		{"kind":"closing","items":[
			{"title":"优先级","body":"确认能力增强为Q3最高优先级"},
			{"title":"验收口径","body":"六项KPI同时达标"},
			{"title":"试点团队","body":"首批两个团队承接试点"}]}
	]}`
	if err := os.WriteFile(specPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validatePPTPolishDeckSpecGrounding(exec, request, workspace)
	if err == nil || !strings.Contains(err.Error(), "PPT 致命版式问题/90天=0") ||
		!strings.Contains(err.Error(), "产品负责人") || !strings.Contains(err.Error(), "优先级") || !strings.Contains(err.Error(), "验收口径") || !strings.Contains(err.Error(), "试点团队") {
		t.Fatalf("unsupported unitless value or premature decisions escaped: %v", err)
	}
	good := strings.NewReplacer(
		`"0"`, `"待确认"`,
		`"≤1%"`, `"待确认"`,
		`"产品负责人"`, `"待确认"`,
		`"设计负责人"`, `"待确认"`,
		`"财务接口人"`, `"待确认"`,
		`"确认能力增强为Q3最高优先级"`, `"优先级待确认"`,
		`"六项KPI同时达标"`, `"验收口径由管理层确认"`,
		`"首批两个团队承接试点"`, `"试点团队待确认"`,
	).Replace(bad)
	if err := os.WriteFile(specPath, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePPTPolishDeckSpecGrounding(exec, request, workspace); err != nil {
		t.Fatalf("truthful unknowns were rejected: %v", err)
	}
}

func TestPPTPolishDeckSpecRejectsMissingFixedPagesBeforeBuild(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:           "pipe-fixed-pages",
		PipelineName: "ppt-polish",
		Steps: []StepResult{{
			Name:   "素材读取",
			Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n未读取外部网页",
		}},
	}
	request := "制作固定 8 页 PPT。"
	workDir := filepath.Join(workspace, filepath.FromSlash(pptPolishCurrentRunWorkRelativePath(exec)))
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := `{"slides":[{"kind":"cover"},{"kind":"statement"},{"kind":"cards"},{"kind":"process"},{"kind":"timeline"},{"kind":"table"},{"kind":"closing"}]}`
	if err := os.WriteFile(filepath.Join(workDir, "deck_spec.json"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	err := validatePPTPolishDeckSpecGrounding(exec, request, workspace)
	if err == nil || !strings.Contains(err.Error(), "contains 7 slides") || !strings.Contains(err.Error(), "minimum 8") {
		t.Fatalf("seven-page spec reached the fixed-eight builder: %v", err)
	}
}

func TestPPTPolishAudienceGroundingAggregatesCopyAndNumericBlockers(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{
		Name:   "素材读取",
		Output: "PPT_INPUT_MODE: detailed_brief\n本次可信素材仅来自当前用户请求\n未读取外部网页",
	}}}
	request := "目标能力架构：需求入口→结果/视觉/数字 QA→交付与反馈，基线 72%，目标 90%。"
	audience := "需求入口 视觉 QA 交付与反馈 72% 78% 90%"
	err := validatePPTPolishAudienceGrounding(exec, request, audience)
	if err == nil || !strings.Contains(err.Error(), "结果/视觉/数字 QA") || !strings.Contains(err.Error(), "78%") {
		t.Fatalf("copy and numeric blockers were not aggregated: %v", err)
	}
}

func TestPPTPolishNumericProvenanceDoesNotApplyToExternallySourcedMode(t *testing.T) {
	exec := &PipelineExecution{Steps: []StepResult{{
		Name:   "素材读取",
		Output: "PPT_INPUT_MODE: source_material\n可信素材与来源：https://example.test/source",
	}}}
	if err := validatePPTPolishDetailedBriefNumericClaims(exec, "制作研究型 PPT", "经核验增长 78%，周期 14 分钟"); err != nil {
		t.Fatalf("external-source mode was incorrectly treated as a closed detailed brief: %v", err)
	}
}

func TestPPTPolishRequestedFilenameAndExactPageCountAreHostRequirements(t *testing.T) {
	input := "制作固定 8 页 PPT，文件名 agentos_90d_capability_plan_r10.pptx。"
	if got := requestedPPTXFilename(input); got != "agentos_90d_capability_plan_r10.pptx" {
		t.Fatalf("requested filename=%q", got)
	}
	if err := validatePPTPolishRequestedFilename(input, filepath.Join("work", "ppt_polished_default.pptx")); err == nil {
		t.Fatal("wrong output filename was accepted")
	}
	if err := validatePPTPolishRequestedFilename(input, filepath.Join("work", "agentos_90d_capability_plan_r10.pptx")); err != nil {
		t.Fatalf("requested output filename was rejected: %v", err)
	}
	if err := validatePPTPolishRequestedSlideCount(input, 13); err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Fatalf("oversized fixed-page deck was accepted: %v", err)
	}
	if err := validatePPTPolishRequestedSlideCount(input, 8); err != nil {
		t.Fatalf("exact requested page count was rejected: %v", err)
	}
}

func TestPPTPolishDefaultPathHonorsExplicitFilename(t *testing.T) {
	exec := &PipelineExecution{ID: "pipe-ignored", Input: "保存为 executive_review.pptx"}
	path := pptPolishDefaultOutputPath(exec, t.TempDir())
	if filepath.Base(path) != "executive_review.pptx" {
		t.Fatalf("default path ignored explicit filename: %s", path)
	}
}

func TestPPTPolishFallbackPreservesExistingModelArtifact(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "generic-preserve",
		Input:             "制作固定 4 页 PPT，文件名 executive_review.pptx。",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{Name: "内容重构", Status: "completed"},
			{Name: "视觉润色", Status: "running"},
		},
	}
	target := pptPolishDefaultOutputPath(exec, workspace)
	writePPTPolishRecoveryFixture(t, target, 4, false)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	output, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), exec, &PipelineStep{Name: "视觉润色"}, 1, errors.New("model final answer was incomplete"),
	)
	if err != nil || !ready {
		t.Fatalf("existing model artifact was not recovered: ready=%v err=%v output=%q", ready, err, output)
	}
	if !strings.Contains(output, "model-produced-pptx-preserved") || pptPolishOutputUsesHostBaseFallback(output) {
		t.Fatalf("existing artifact was mislabeled as a base fallback: %s", output)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("host recovery changed existing model PPTX bytes")
	}
}

func TestPPTPolishIncompleteVisualFinalDefersToBoundedDeliveryRework(t *testing.T) {
	workspace := t.TempDir()
	pipeline := (&GatewayService{}).defaultPipelineSet()["ppt-polish"]
	exec := &PipelineExecution{
		ID:                "eight-page-pending-visual-rework",
		Input:             "制作固定 8 页 PPT，文件名 executive_review.pptx。",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		RuntimePipeline:   &pipeline,
		Steps: []StepResult{
			{Name: "素材读取", Status: "completed"},
			{Name: "内容重构", Status: "completed"},
			{Name: "视觉润色", Status: "running"},
			{Name: "交付自查", Status: "pending"},
		},
	}
	target := pptPolishDefaultOutputPath(exec, workspace)
	writePPTPolishRecoveryFixture(t, target, 8, false)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	output, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), exec, &pipeline.Steps[2], 2, errors.New("model finalized after the last vision call without a complete step result"),
	)
	if err != nil || !ready {
		t.Fatalf("valid model deck was not deferred to the delivery rework gate: ready=%v err=%v output=%q", ready, err, output)
	}
	if !strings.Contains(output, "model-produced-pptx-preserved-pending-visual-rework") ||
		!strings.Contains(output, "视觉证据状态：未放行") {
		t.Fatalf("pending visual rework was not explicit: %s", output)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("pending visual rework changed existing model PPTX bytes")
	}
	if len(exec.Steps[2].ToolCalls) != 1 || exec.Steps[2].ToolCalls[0].ToolName != "pptx_preserve_pending_visual_rework" {
		t.Fatalf("pending visual preservation receipt missing: %#v", exec.Steps[2].ToolCalls)
	}

	withoutDeliveryRework := &PipelineExecution{
		ID:                exec.ID,
		Input:             exec.Input,
		PipelineName:      exec.PipelineName,
		ResolvedWorkspace: exec.ResolvedWorkspace,
		Steps:             append([]StepResult(nil), exec.Steps...),
	}
	withoutDeliveryRework.Steps[2].ToolCalls = nil
	if _, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), withoutDeliveryRework, &PipelineStep{Name: "视觉润色"}, 2, errors.New("model finalization incomplete"),
	); !ready || err == nil || !strings.Contains(err.Error(), "visual QA is incomplete") {
		t.Fatalf("visual evidence was relaxed without a bounded delivery rework: ready=%v err=%v", ready, err)
	}
}

func TestPPTPolishFallbackReconcilesOnlyCurrentRunGenericFilename(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "generic-name-reconcile",
		Input:             "制作固定 4 页 PPT，文件名 executive_review.pptx。",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{Name: "内容重构", Status: "completed"},
			{Name: "视觉润色", Status: "running"},
		},
	}
	alternate := filepath.Join(workspace, ".openkitty", "storage", "ppt_polished_"+sanitizeFilename(exec.ID)+".pptx")
	target := pptPolishDefaultOutputPath(exec, workspace)
	writePPTPolishRecoveryFixture(t, alternate, 4, false)
	before, err := os.ReadFile(alternate)
	if err != nil {
		t.Fatal(err)
	}

	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	output, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), exec, &PipelineStep{Name: "视觉润色"}, 1, errors.New("model finalized without the requested filename"),
	)
	if err != nil || !ready {
		t.Fatalf("current-run generic filename was not reconciled: ready=%v err=%v output=%q", ready, err, output)
	}
	if !strings.Contains(output, "model-produced-pptx-preserved") || filepath.Base(target) != "executive_review.pptx" {
		t.Fatalf("reconciled output has the wrong delivery identity: %s", output)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("filename reconciliation changed model-produced PPTX bytes")
	}
	if _, err := os.Stat(alternate); err != nil {
		t.Fatalf("diagnostic current-run alternate was removed: %v", err)
	}
}

func TestPPTPolishFallbackRefusesToOverwriteInvalidModelArtifact(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "generic-invalid",
		Input:             "制作固定 4 页 PPT，文件名 executive_review.pptx。",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{Name: "内容重构", Status: "completed"},
			{Name: "视觉润色", Status: "running"},
		},
	}
	target := pptPolishDefaultOutputPath(exec, workspace)
	writePPTPolishRecoveryFixture(t, target, 4, true)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	_, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), exec, &PipelineStep{Name: "视觉润色"}, 1, errors.New("model build failed"),
	)
	if !ready || err == nil || !strings.Contains(err.Error(), "fractional/invalid EMU") || !strings.Contains(err.Error(), "refusing fallback overwrite") {
		t.Fatalf("invalid model artifact did not fail closed: ready=%v err=%v", ready, err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid model artifact was overwritten instead of preserved for diagnosis")
	}
}

func TestPPTPolishHostBaseFallbackCannotFakeEightPageVisualQA(t *testing.T) {
	workspace := t.TempDir()
	exec := &PipelineExecution{
		ID:                "generic-eight-pages",
		Input:             "制作固定 8 页 PPT，文件名 executive_review.pptx。",
		PipelineName:      "ppt-polish",
		ResolvedWorkspace: workspace,
		Steps: []StepResult{
			{Name: "内容重构", Status: "completed"},
			{Name: "视觉润色", Status: "running"},
		},
	}
	briefPath := filepath.Join(workspace, ".openkitty", "storage", "ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	var brief strings.Builder
	brief.WriteString("# 通用能力规划\n\n")
	for number := 1; number <= 8; number++ {
		brief.WriteString(fmt.Sprintf("### Page %d — 阶段 %d\n- **核心观点**：明确第 %d 阶段的通用目标。\n- **支持内容**：可验证结果；责任边界；下一步行动。\n\n", number, number, number))
	}
	if err := os.WriteFile(briefPath, []byte(brief.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Steps[0].Output = "Markdown 制作简报：" + briefPath
	executor := &PipelineExecutor{fileLogger: log.New(io.Discard, "", 0)}
	_, ready, err := executor.hostFallbackPPTPolishVisual(
		context.Background(), exec, &PipelineStep{Name: "视觉润色"}, 1, errors.New("model route failed"),
	)
	if !ready || err == nil || !strings.Contains(err.Error(), "every-slide vision QA") {
		t.Fatalf("eight-page fallback quality downgrade was not refused: ready=%v err=%v", ready, err)
	}
	if _, statErr := os.Stat(pptPolishDefaultOutputPath(exec, workspace)); !os.IsNotExist(statErr) {
		t.Fatalf("refused fallback still materialized an output: %v", statErr)
	}
}

func TestPPTPolishHostFallbackDoesNotReplaceFailedQualityEvidence(t *testing.T) {
	for _, message := range []string{"PPTX delivery missing: result.pptx", "PPTX delivery too small: result.pptx"} {
		if !pptPolishHostFallbackEligible(errors.New(message)) {
			t.Fatalf("missing-artifact recovery was disabled for %q", message)
		}
	}
	for _, message := range []string{"PPTX structural audit failed: serialized payload", "PPTX visual QA evidence missing: slide_08"} {
		if pptPolishHostFallbackEligible(errors.New(message)) {
			t.Fatalf("quality failure would be hidden by host fallback: %q", message)
		}
	}
}

func TestPPTPolishVisualInternalMissingResponseUsesHostFallback(t *testing.T) {
	pipeline := Pipeline{Name: "ppt-polish"}
	exec := &PipelineExecution{PipelineName: "ppt-polish"}
	step := &PipelineStep{Name: "视觉润色"}
	internalFailure := "SLOT_STATUS: MISSING\nINPUT_STATUS: MISSING\n<!--PIPELINE_STOP-->\n迭代预算不足"
	if !shouldRecoverPPTPolishVisualMissingOutput(exec, step, internalFailure, pipeline) {
		t.Fatal("visual-production internal missing response must route to the host PPT fallback")
	}
	if shouldRecoverPPTPolishVisualMissingOutput(exec, &PipelineStep{Name: "素材读取"}, internalFailure, pipeline) {
		t.Fatal("non-visual decisive input gaps must remain user-facing")
	}
	if shouldRecoverPPTPolishVisualMissingOutput(exec, step, "SLOT_STATUS: FILLED\n已生成真实PPTX", pipeline) {
		t.Fatal("successful visual output must not be replaced")
	}
}

func TestPPTPolishPreserveBriefHasNoCaseSpecificCustomerServiceRewrite(t *testing.T) {
	material := `# Slide 1
OpenKitty 产品介绍

# Slide 2
Talk Track
### Original editable table
| 能力 | 说明 |
| --- | --- |
| 自动化 | 可配置 |`
	got := buildPPTPolishPreserveBrief(material)
	if strings.Contains(got, "客服AI") || strings.Contains(got, "客服 AI") {
		t.Fatalf("generic PPT preserve path retained case-specific wording: %s", got)
	}
	if !strings.Contains(got, "Talk Track") || !strings.Contains(got, "OpenKitty 产品介绍") {
		t.Fatalf("source titles were not preserved: %s", got)
	}
}

func TestPipelineStepOutputUsesResolvedExecutionWorkspace(t *testing.T) {
	globalWorkspace := t.TempDir()
	tenantWorkspace := t.TempDir()
	gateway := &GatewayService{config: &Config{Workspace: globalWorkspace}}
	executor := NewPipelineExecutor(nil, gateway, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID:                "tenant-output",
		ResolvedWorkspace: tenantWorkspace,
		Steps:             []StepResult{{Name: "material"}},
	}

	executor.saveStepOutputToFile(exec, 0, "tenant scoped output")
	rel := buildStepOutputPath(exec, 0, exec.Steps[0].Name)
	tenantPath := filepath.Join(tenantWorkspace, rel)
	if data, err := os.ReadFile(tenantPath); err != nil || string(data) != "tenant scoped output" {
		t.Fatalf("step output was not saved in the execution workspace: path=%s err=%v data=%q", tenantPath, err, data)
	}
	if _, err := os.Stat(filepath.Join(globalWorkspace, rel)); !os.IsNotExist(err) {
		t.Fatalf("step output leaked into the service workspace: %v", err)
	}
}

func TestPipelineTenantRegistryDoesNotBorrowWorkspaceBoundGlobalTool(t *testing.T) {
	globalWorkspace := t.TempDir()
	globalTools := NewToolRegistry()
	globalTools.Register(&presentationBudgetTestTool{name: "cli"})
	factory := &AgentFactory{
		workspace: globalWorkspace,
		agents: map[string]Agent{
			"global": &LLMAgent{toolRegistry: globalTools},
		},
	}
	executor := &PipelineExecutor{agentFactory: factory}
	exec := &PipelineExecution{ResolvedWorkspace: t.TempDir()}

	registry := executor.buildStepRegistryFrom(NewToolRegistry(), []string{"cli"}, exec, 0)
	if _, ok := registry.Get("cli"); ok {
		t.Fatal("tenant step borrowed a workspace-bound tool from the global resident agent")
	}
}
