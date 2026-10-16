package app

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"
	contenttools "mac/internal/tools/content"
)

func TestBasePPTIsCanonicalRuntimeAndBetterPPTIsAliasOnly(t *testing.T) {
	base := filepath.Join("builtin", "presentation", "base-ppt")
	for _, rel := range []string{
		"SKILL.md",
		"generate.py",
		"pptx_helpers.py",
		"template_support.py",
		"html_render.py",
		filepath.Join("scripts", "inspect_pptx_template.py"),
		filepath.Join("scripts", "render_pptx.ps1"),
		filepath.Join("examples", "custom_deck.py"),
		filepath.Join("references", "review-gated-production.md"),
	} {
		if _, err := os.Stat(filepath.Join(base, rel)); err != nil {
			t.Fatalf("canonical base-ppt runtime asset missing %s: %v", rel, err)
		}
	}

	if _, err := os.Stat(filepath.Join("builtin", "presentation", "better-ppt")); !os.IsNotExist(err) {
		t.Fatalf("better-ppt must be a metadata alias, not a duplicate directory: %v", err)
	}

	baseSkill, err := os.ReadFile(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Native JSON schema",
		"chart.categories",
		"KPI uses `metrics`",
		"task-specific `python-pptx`/PptxGenJS",
		"Do not enter a preview/watch/retry loop",
		"Ordinary-deck composition rubric",
		"action title",
		"abnormal wrapping",
		"polished structured route",
		"consecutive table slides",
		"actual vision-based inspection",
		"Bespoke Python fast path",
		"no dependency on an OpenKitty installation path",
	} {
		if !strings.Contains(string(baseSkill), want) {
			t.Fatalf("base-ppt quick instructions missing %q", want)
		}
	}

	loader := skilldomain.NewSkillsLoader(t.TempDir(), "test", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	legacy, resolvedName, ok := loader.ResolveSkill("better-ppt")
	if !ok || legacy == nil || resolvedName != "base-ppt" || legacy.Metadata.Name != "base-ppt" {
		t.Fatalf("better-ppt alias did not resolve directly to base-ppt: ok=%v resolved=%q skill=%#v", ok, resolvedName, legacy)
	}
}

func TestBasePPTRendererUsesDeterministicPerSlideExports(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("builtin", "presentation", "base-ppt", "scripts", "render_pptx.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"slide_{0:D2}.png",
		"$presentation.Slides.Item($index)",
		"$slide.Export($imagePath",
		"10 PNG files for 9 slides",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("base-ppt deterministic renderer missing %q", want)
		}
	}
	if strings.Contains(text, "$presentation.Export($target") {
		t.Fatal("base-ppt renderer must not use locale-sensitive whole-presentation export")
	}
}

func TestPPTArtDirectorDefaultsSubstantiveDecksToAudienceReadyQuality(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("builtin", "presentation", "ppt-art-director", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"communication job",
		"audience-ready quality floor",
		"Do not confuse content scope with production quality",
		"简单把做的事做成 PPT",
		"normally at least 16–18 pt",
		"references\\premium-visual-direction.md",
		"default to a task-specific per-deck renderer",
		"Level 3 / premium custom",
		"Level 2 / regular custom",
		"Level 1 / fallback structured",
		"Do not call `convert` with an empty generator",
		"Core production principle: per-slide customization",
		"write short page-specific functions for every slide",
		"Treat PPTMaster as the higher-end commercial route",
		"Call `use_skill base-ppt` when the user explicitly prioritizes simple or fast output",
		"Fallback in quality order: PPTMaster → task-specific native renderer → `base-ppt`",
		"Avoid consecutive tables unless an exact required table needs two pages",
		"Reading a filename, dimensions, file metadata",
		"approved regular consulting baseline",
		"Avoid rounded tile dashboards",
		"dominant-color share",
		"visual quality remains unverified",
		"Do not announce `base-ppt` and then hand-write a separate renderer",
		"template source and renderer as orthogonal decisions",
		"`strict-existing`, `style-reference`, `registered-family`, or `none`",
		"use standalone `build_task_pptx.py` when the compact vocabulary fits",
		"For every substantive audience-facing deck",
		"concise 6–7 slide count alone is not permission",
		"task-local `deck_spec.json` is the truth and narrative contract",
		"register one named task-local renderer for every slide",
		"Stable renderers and primitives may be reused",
		"thin dispatcher wrappers",
		"fact_model.json",
		"execute exactly the task-local builder as a no-argument script",
		"ordinary 8-slide audience-facing deck, use at least four body composition families",
		"vision(path=\"<relative preview path>\"",
		"inspect every `slide_NN.png` individually",
		"per-slide defect ledger",
		"external send is a separate commit boundary",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ppt-art-director quality routing missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"execute the generated `build_deck.py` directly",
		"Do not inspect `pptx_design.py` or read/rewrite `build_deck.py`",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("ppt-art-director retained contradictory builder-first guidance %q", forbidden)
		}
	}

	planner, err := os.ReadFile(filepath.Join("builtin", "presentation", "base-ppt", "plan_workflow.py"))
	if err != nil {
		t.Fatal(err)
	}
	plannerText := string(planner)
	for _, unwanted := range []string{"Selected template:", `"heading": "Template route"`, `"Generated by OpenKitty"`} {
		if strings.Contains(plannerText, unwanted) {
			t.Fatalf("base-ppt auto-planner still leaks internal production metadata %q", unwanted)
		}
	}

	for _, path := range []string{
		filepath.Join("builtin", "presentation", "SKILL.md"),
		filepath.Join("builtin", "presentation", "ppt-art-director", "SKILL.md"),
		filepath.Join("builtin", "presentation", "base-ppt", "SKILL.md"),
		filepath.Join("builtin", "presentation", "base-ppt", "plan_workflow.py"),
		filepath.Join("builtin", "presentation", "base-ppt", "examples", "custom_deck.py"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, unwanted := range []string{"adebug2", "china_brain_science", "build_brain_deck", "industry_forum", "consulting_navy", "具身智能"} {
			if strings.Contains(string(data), unwanted) {
				t.Fatalf("presentation default route leaked reference-specific token %q in %s", unwanted, path)
			}
		}
	}
}

func TestBasePPTPlannerDefaultsToNeutralAndParsesMarkdownEvidence(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}

	dir := t.TempDir()
	figuresDir := filepath.Join(dir, "figures")
	if err := os.MkdirAll(figuresDir, 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(figuresDir, "01_price.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 66, G: 133, B: 244, A: 255})
		}
	}
	imageFile, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(imageFile, img); err != nil {
		imageFile.Close()
		t.Fatal(err)
	}
	if err := imageFile.Close(); err != nil {
		t.Fatal(err)
	}

	markdownPath := filepath.Join(dir, "analysis.md")
	markdown := strings.Join([]string{
		"# Diamond dataset analysis",
		"",
		"## Overview",
		"- Dataset rows: 53,940 records",
		"- Variables: 10 measured fields",
		"- Missing values: 0 observations",
		"- Median price: USD 2,401",
		"- Fair cut is retained as a category in the distribution analysis.",
		"",
		"![Price distribution](figures/01_price.png)",
		"",
		"| Metric | Value |",
		"| --- | --- |",
		"| Rows | 53,940 |",
		"| Median price | USD 2,401 |",
		"",
		"## Findings",
		"Regression and correlation explain the strongest price relationships.",
	}, "\n")
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}

	planner := filepath.Join("builtin", "presentation", "base-ppt", "plan_workflow.py")
	runPlanner := func(inputPath, outputPath string) map[string]interface{} {
		t.Helper()
		cmd := exec.Command(python, planner, "--input", inputPath, "--output", outputPath)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("base-ppt planner failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		var workflow map[string]interface{}
		if err := json.Unmarshal(data, &workflow); err != nil {
			t.Fatalf("decode planned workflow: %v", err)
		}
		return workflow
	}

	workflow := runPlanner(markdownPath, filepath.Join(dir, "workflow.json"))
	meta, ok := workflow["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("planner metadata missing: %#v", workflow["meta"])
	}
	if got := meta["deck_family"]; got != "data_analysis" {
		t.Fatalf("data fixture family=%v, want data_analysis", got)
	}
	if got := meta["theme"]; got != "charcoal" {
		t.Fatalf("data fixture theme=%v, want neutral charcoal", got)
	}

	var metricValues []string
	var resolvedImages []string
	hasTable := false
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			if blockType, _ := typed["type"].(string); blockType == "image" {
				if path, _ := typed["path"].(string); path != "" {
					resolvedImages = append(resolvedImages, path)
				}
			}
			if blockType, _ := typed["type"].(string); blockType == "table" {
				hasTable = true
			}
			if metric, _ := typed["value"].(string); metric != "" {
				metricValues = append(metricValues, metric)
			}
			for _, child := range typed {
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(workflow["slides"])
	if !hasTable {
		t.Fatal("Markdown table was not preserved as a table block")
	}
	foundImage := false
	for _, resolved := range resolvedImages {
		if filepath.Clean(resolved) == filepath.Clean(imagePath) {
			foundImage = true
			break
		}
	}
	if !foundImage {
		t.Fatalf("Markdown image was not resolved to %s: %v", imagePath, resolvedImages)
	}
	if !containsString(metricValues, "53,940") {
		t.Fatalf("comma-separated KPI was not preserved: %v", metricValues)
	}
	if containsString(metricValues, "01") {
		t.Fatalf("image filename leaked into a fake KPI: %v", metricValues)
	}

	fairPath := filepath.Join(dir, "fair.md")
	if err := os.WriteFile(fairPath, []byte("# Fair value review\n\nA concise neutral briefing."), 0o600); err != nil {
		t.Fatal(err)
	}
	fairWorkflow := runPlanner(fairPath, filepath.Join(dir, "fair-workflow.json"))
	fairMeta := fairWorkflow["meta"].(map[string]interface{})
	if got := fairMeta["deck_family"]; got != "general_business" {
		t.Fatalf("Fair substring was misclassified as technology: family=%v", got)
	}

	explicitGooglePath := filepath.Join(dir, "explicit-google.md")
	if err := os.WriteFile(explicitGooglePath, []byte("# Google style data review\n\nUse a Google-style palette for this analysis."), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitGoogle := runPlanner(explicitGooglePath, filepath.Join(dir, "explicit-google-workflow.json"))
	explicitGoogleMeta := explicitGoogle["meta"].(map[string]interface{})
	if got := explicitGoogleMeta["theme"]; got != "google_style" {
		t.Fatalf("explicit Google style theme=%v, want google_style", got)
	}

	fallbackStylePath := filepath.Join(dir, "fallback-style.md")
	if err := os.WriteFile(fallbackStylePath, []byte("<!-- OPENKITTY_THEME:mckinsey -->\n\n# AI产品商业计划\n\n## 核心结论\n\n- 五个产品形成协同闭环。"), 0o600); err != nil {
		t.Fatal(err)
	}
	fallbackStyle := runPlanner(fallbackStylePath, filepath.Join(dir, "fallback-style-workflow.json"))
	fallbackMeta := fallbackStyle["meta"].(map[string]interface{})
	if got := fallbackMeta["theme"]; got != "mckinsey" {
		t.Fatalf("fallback style theme=%v, want mckinsey", got)
	}
	fallbackJSON, _ := json.Marshal(fallbackStyle["slides"])
	if strings.Contains(string(fallbackJSON), "OPENKITTY_THEME") {
		t.Fatalf("internal fallback style marker leaked into slide content: %s", fallbackJSON)
	}
}

func TestPresentationDefaultsAreNeutralAndVisualAuditIsNotSelfConfirming(t *testing.T) {
	baseSkill, err := os.ReadFile(filepath.Join("builtin", "presentation", "base-ppt", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	baseText := string(baseSkill)
	for _, want := range []string{
		"there is no universal business theme",
		"`charcoal` is the restrained neutral fallback",
		"`base-ppt` is a polished structured route",
		"dominant-color percentages",
		"visual quality remains unverified",
	} {
		if !strings.Contains(baseText, want) {
			t.Fatalf("base-ppt neutral/visual guidance missing %q", want)
		}
	}
	if strings.Contains(baseText, "midnight` is a strong general business default") {
		t.Fatal("base-ppt still promotes a blue general-business default")
	}

	generator, err := os.ReadFile(filepath.Join("builtin", "presentation", "base-ppt", "generate.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`meta.get("theme", "charcoal")`,
		`THEMES.get(theme_name, THEMES["charcoal"])`,
		`"hbg":  "2B2A27"`,
		`"ac1":  "C45D3C"`,
		`def quiet_header_background(slide, t):`,
		`set_bg(slide, t["pbg"])`,
		`shadow=False, radius=0.04`,
		`'#315C5B', '#B5654D', '#7B8790'`,
	} {
		if !strings.Contains(string(generator), want) {
			t.Fatalf("base-ppt neutral fallback missing %q", want)
		}
	}
}

func TestEmbeddedBasePPTIncludesCanonicalRuntimeAssets(t *testing.T) {
	root := "builtin/presentation/base-ppt"
	for _, rel := range []string{"SKILL.md", "generate.py", "pptx_helpers.py", "template_support.py", "html_render.py", "examples/custom_deck.py", "scripts/inspect_pptx_template.py", "scripts/render_pptx.ps1"} {
		if _, err := fs.ReadFile(builtinSkillsFS, root+"/"+rel); err != nil {
			t.Fatalf("embedded base-ppt missing %s: %v", rel, err)
		}
	}
	if !skilldomain.EmbeddedSkillHasRuntimeAssets(builtinSkillsFS, root, skilldomain.SkillMetadata{Name: "base-ppt"}) {
		t.Fatal("canonical base-ppt runtime assets were not detected")
	}
	if skilldomain.EmbeddedSkillHasRuntimeAssets(builtinSkillsFS, "builtin/presentation/better-ppt", skilldomain.SkillMetadata{Name: "better-ppt"}) {
		t.Fatal("legacy better-ppt alias must not materialize a duplicate runtime")
	}
}

func TestPPTArtDirectorBundlesFastSafeQARuntime(t *testing.T) {
	root := filepath.Join("builtin", "presentation", "ppt-art-director")
	scriptPath := filepath.Join(root, "scripts", "render_pptx.ps1")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("ppt-art-director QA runtime missing: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"Get-Process -Name POWERPNT",
		"protect the user-owned Office session",
		"Presentations.Open",
		"slide.Export",
		"slide_{0:D2}.png",
		"contact_sheet.jpg",
		"presentation_visual_inspected = $false",
		"visual_inspection_required = $true",
		"all_slide_images_decodable = $true",
		"sampled_colors",
		"fully local and uploads nothing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ppt-art-director QA runtime missing safety marker %q", want)
		}
	}

	for _, rel := range []string{
		"SKILL.md",
		"references/decision-deck-patterns.md",
		"references/premium-visual-direction.md",
		"references/visual-director-contract.md",
		"references/aesthetic-critic-contract.md",
		"references/template-rendering-contract.md",
		"scripts/build_task_pptx.py",
		"scripts/scaffold_task_pptx.py",
		"scripts/validate_visual_plan.py",
		"scripts/audit_pptx.py",
		"scripts/render_pptx.ps1",
		"scripts/render_pptx.py",
		"scripts/validate_aesthetic_qa.py",
		"assets/task-pptx/pptx_design.py",
		"assets/task-pptx/build_deck.py",
		"assets/task-pptx/deck_spec.json",
		"assets/task-pptx/visual_plan.json",
	} {
		if _, err := fs.ReadFile(builtinSkillsFS, "builtin/presentation/ppt-art-director/"+rel); err != nil {
			t.Fatalf("embedded ppt-art-director missing %s: %v", rel, err)
		}
	}
	if !skilldomain.EmbeddedSkillHasRuntimeAssets(builtinSkillsFS, "builtin/presentation/ppt-art-director", skilldomain.SkillMetadata{Name: "ppt-art-director"}) {
		t.Fatal("ppt-art-director QA script must materialize with the Skill runtime")
	}
	designData, err := os.ReadFile(filepath.Join(root, "assets", "task-pptx", "pptx_design.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"def platform_font_pair()",
		"Microsoft YaHei",
		"PingFang SC",
		"Noto Sans CJK SC",
		"dark_mode: bool = False",
		"def decision_frame(",
		"def image_cover(",
		"def image_contain(",
		"def proportional_bar(",
		"def semantic_legend(",
		"def native_chart(",
		"def editable_table(",
		"def timeline(",
		"def matrix(",
		"coordinates must be within 0..1",
	} {
		if !strings.Contains(string(designData), want) {
			t.Fatalf("task-specific design runtime missing %q", want)
		}
	}
	if !strings.Contains(string(designData), "muted: RGBColor = RGBColor(0x58, 0x5E, 0x63)") {
		t.Fatal("task-specific design runtime regressed to low-contrast body text")
	}
	builderData, err := os.ReadFile(filepath.Join(root, "assets", "task-pptx", "build_deck.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"chart_width = 8.35 if note else 12.0",
		`note = _text(page_spec.get("note")) or _text(page_spec.get("intro"))`,
		"if not note:",
		"meta, size=16",
		"left, right, axis_y = 1.75, 11.58, 3.45",
		"def _timeline_milestone(",
		"def _cards_conclusion(",
		"def _closing_commitments(",
		"def _headline_size(",
		"arrow=True",
		`size=24 if len(right_body) <= 48 else 21`,
		`COMPOSITION_MODE = "__TASK_COMPOSITION_MODE__"`,
		"decision_frame,",
		"proportional_bar,",
		"rich_text_box,",
		"TASK_PAGE_RENDERERS = {}",
		"per-slide customization is incomplete",
		"VISUAL_PLAN_PATH",
		"def planned_image(",
		"def _validate_visual_realization(",
	} {
		if !strings.Contains(string(builderData), want) {
			t.Fatalf("task-specific builder missing quality marker %q", want)
		}
	}
	if strings.Contains(string(builderData), `or _text(page_spec.get("source_note"))`) {
		t.Fatal("source provenance regressed into an audience-facing chart insight card")
	}

	skillData, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skillText := string(skillData)
	for _, want := range []string{
		"do not call `plan_task`",
		"scripts\\build_task_pptx.py",
		"scripts\\scaffold_task_pptx.py",
		"validate_visual_plan.py",
		"validate_aesthetic_qa.py",
		"scripts\\audit_pptx.py",
		"scripts\\render_pptx.ps1",
		"scripts/render_pptx.py",
		"LibreOffice + Poppler",
		"do not kill or take over that user-owned process",
		"Do not run `where`, `which`",
		"integer EMU",
		"references\\decision-deck-patterns.md",
		"references\\premium-visual-direction.md",
		"claim–proof–implication hierarchy",
		"decision_frame(slide",
		"`native_chart`, `editable_table`, `timeline`, and `matrix`",
	} {
		if !strings.Contains(skillText, want) {
			t.Fatalf("ppt-art-director fast path missing %q", want)
		}
	}
}

func TestPPTArtDirectorOneCommandBuilderCreatesEditableDeck(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "deck.json")
	outputPath := filepath.Join(dir, "deck.pptx")
	spec := `{
  "needs_customization": false,
  "brand": "Acme Agent",
  "slides": [
    {"kind":"cover","title":"Acme Agent 融资计划书","subtitle":"企业任务的可审计执行层"},
    {"kind":"statement","title":"机会判断","claim":"企业 AI 正从问答转向可交付工作流","evidence":["项目材料","公开研究","规划假设"]},
    {"kind":"cards","title":"客户痛点","items":[{"title":"工具割裂","body":"跨系统任务难连续执行"},{"title":"交付不稳","body":"结果缺少验收状态"}]},
    {"kind":"process","title":"产品闭环","items":[{"title":"受理","body":"理解目标"},{"title":"执行","body":"调用工具"},{"title":"验收","body":"交付结果"}]},
    {"kind":"split","title":"商业路径","left":{"title":"目标客户","body":"复杂流程企业"},"right":{"title":"收入结构","body":"订阅与私有化交付"}},
    {"kind":"timeline","title":"规划里程碑","items":[{"label":"0–3月","title":"产品验证","detail":"完成场景闭环"},{"label":"3–6月","title":"商业验证","detail":"形成销售方法"},{"label":"6–12月","title":"规模验证","detail":"验证复购与交付"}]},
    {"kind":"closing","title":"融资诉求","takeaway":"以产品、商业与组织验证为资金主线","items":[{"title":"产品","body":"提升可靠交付"},{"title":"市场","body":"完成客户验证"}]}
  ]
}`
	if err := os.WriteFile(specPath, []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	builder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "build_task_pptx.py")
	cmd := exec.Command(python, builder, "--spec", specPath, "--output", outputPath)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("one-command PPTX build failed: %v\n%s", err, output)
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() < 10*1024 {
		t.Fatalf("one-command PPTX missing or too small: info=%v err=%v", info, err)
	}
	structure, err := contenttools.InspectPPTXStructure(outputPath)
	if err != nil || structure.SlideCount != 7 || structure.TextShapeCount == 0 {
		t.Fatalf("unexpected one-command deck structure: %#v err=%v", structure, err)
	}
}

func TestPPTArtDirectorAudienceFacingBuilderProducesDiverseReadableDeck(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "audience-facing.json")
	outputPath := filepath.Join(dir, "audience-facing.pptx")
	spec := `{
  "needs_customization": false,
  "quality_profile": "audience-facing",
  "theme": "consulting",
  "brand": "OpenKitty",
  "slides": [
    {"kind":"cover","title":"OpenKitty 融资计划书","subtitle":"企业复杂任务的可审计执行层","kicker":"Pre-A 路演稿 v0 · 建议区间 RMB 20–30M · 规划案 RMB 25M"},
    {"kind":"statement","title":"机会判断","takeaway":"采购标准正在变化","claim":"企业 AI 的价值从回答迁移到稳定交付","evidence":["跨系统执行","结果可验收","过程可追踪"]},
    {"kind":"cards","title":"客户痛点","conclusion":{"title":"关键结论","body":"流程断点比模型误差更昂贵"},"takeaway":"这行是次要摘要，不应覆盖明确结论","items":[{"title":"今天","body":"工具割裂，交付依赖个人经验"},{"title":"目标","body":"任务连续执行，失败可恢复"}]},
    {"kind":"process","title":"产品闭环","takeaway":"统一受理到验收","items":[{"title":"受理","body":"理解目标"},{"title":"执行","body":"调用工具"},{"title":"验收","body":"检查交付"}],"lateral":["审计","恢复","权限"]},
    {"kind":"chart","title":"增长模型","takeaway":"规划估算（非项目事实）","categories":["试点","复用","规模"],"series":[{"name":"合同额指数","values":[1,2,4]}],"chart_type":"line","note_title":"判断","note":"复用场景推动客户价值提升"},
    {"kind":"matrix","title":"竞争定位","takeaway":"兼顾灵活性与可控性","x_label":"灵活性","y_label":"可控性","points":[{"label":"传统自动化","x":0.25,"y":0.7},{"label":"通用助手","x":0.75,"y":0.3},{"label":"OpenKitty","x":0.82,"y":0.84}]},
    {"kind":"metrics","title":"商业模式","takeaway":"订阅与部署组合","items":[{"value":"¥14.4 万","label":"AI 席位订阅(200 人 × 20% × ¥300/月 × 12)","body":"基础订阅，按席位计费"},{"value":"¥50 万","label":"行业 Skill 套餐","body":"按场景提升客单价"},{"value":"¥120 万","label":"私有部署服务","body":"本地化部署与实施"},{"value":"¥45–300 万","label":"单客户首年 ARR 区间","body":"规划估算，非项目事实"}]},
    {"kind":"table","title":"进入市场","takeaway":"以可量化场景复制","headers":["阶段","客户","场景","指标"],"rows":[["试点","成长企业","报告生产","周期 -50%"],["复制","企业部门","跨系统流程","3 个场景"],["规模","集团客户","治理与部署","续费扩容"]]},
    {"kind":"timeline","title":"里程碑","takeaway":"用客户结果衡量进展","items":[{"label":"M0–6","title":"产品验证","detail":"成功率达标"},{"label":"M7–12","title":"商业验证","detail":"形成标杆"},{"label":"M13–24","title":"规模验证","detail":"验证续费"}]},
    {"kind":"closing","title":"期待与产业资本、AI Infra / AI Employee 方向成长期投资人交流","takeaway":"建议融资 ¥2,000 万，规划估算（非项目事实）","items":[{"title":"产品","body":"提升可靠交付"},{"title":"市场","body":"验证复制路径"}]}
  ]
}`
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "build_task_pptx.py")
	cmd := exec.Command(python, builder, "--spec", specPath, "--output", outputPath)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("audience-facing PPTX build failed: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	auditOutput, err := exec.Command(python, audit, outputPath).CombinedOutput()
	if err != nil {
		t.Fatalf("audience-facing PPTX audit failed: %v\n%s", err, auditOutput)
	}
	var report struct {
		OK              bool     `json:"ok"`
		SlideCount      int      `json:"slide_count"`
		QualityWarnings []string `json:"quality_warnings"`
		QualityScore    int      `json:"quality_score"`
		Layout          struct {
			Repeated []map[string]interface{} `json:"internal_repeated_pairs"`
		} `json:"layout_originality"`
	}
	if err := json.Unmarshal(auditOutput, &report); err != nil {
		t.Fatalf("parse audience-facing audit: %v\n%s", err, auditOutput)
	}
	if !report.OK || report.SlideCount != 10 || len(report.QualityWarnings) != 0 || report.QualityScore != 100 || len(report.Layout.Repeated) != 0 {
		t.Fatalf("audience-facing deck regressed to repetitive or unreadable output: %#v", report)
	}
	audienceText, err := contenttools.ExtractPPTX(outputPath)
	if err != nil {
		t.Fatalf("extract audience-facing deck text: %v", err)
	}
	for _, want := range []string{"关键结论", "流程断点比模型误差更昂贵", "横向能力", "审计", "恢复", "权限"} {
		if !strings.Contains(audienceText, want) {
			t.Fatalf("stable schema field was not rendered as audience text (%q):\n%s", want, audienceText)
		}
	}
	if strings.Contains(audienceText, "这行是次要摘要") {
		t.Fatalf("cards takeaway overrode the explicit conclusion:\n%s", audienceText)
	}
}

func TestPPTArtDirectorBuilderKeepsStructuredMetadataOutOfAudienceText(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "structured-metadata.json")
	outputPath := filepath.Join(dir, "structured-metadata.pptx")
	spec := map[string]interface{}{
		"needs_customization": false,
		"quality_profile":     "audience-facing",
		"brand": map[string]interface{}{
			"name": "AgentOS", "section": "90 DAY PLAN", "tagline": "Generic execution platform",
		},
		"slides": []interface{}{
			map[string]interface{}{"kind": "cover", "title": "AgentOS 能力增强计划", "subtitle": "从任务受理到可靠交付"},
			map[string]interface{}{"kind": "statement", "title": "目标", "claim": "用可核验结果衡量通用能力", "evidence": []string{"执行成功率", "交付一次通过率", "失败可恢复"}},
			map[string]interface{}{"kind": "process", "title": "执行闭环", "items": []interface{}{map[string]string{"title": "理解", "body": "明确目标"}, map[string]string{"title": "执行", "body": "调用工具"}, map[string]string{"title": "验收", "body": "核对产物"}}},
			map[string]interface{}{"kind": "metrics", "title": "硬指标", "items": []interface{}{map[string]string{"value": "90%", "label": "任务成功率", "body": "按真实交付统计"}, map[string]string{"value": "85%", "label": "一次验收率", "body": "按质量门禁统计"}}},
			map[string]interface{}{"kind": "table", "title": "质量指标（一）", "headers": []string{"指标", "目标"}, "rows": []interface{}{[]string{"任务成功率", "90%"}, []string{"一次验收率", "85%"}, []string{"PPT致命版式问题", "0"}}},
			map[string]interface{}{"kind": "table", "title": "质量指标（二）", "headers": []string{"指标", "目标"}, "rows": []interface{}{[]string{"报价复算差异", "0"}, []string{"事实引用可追溯", "100%"}, []string{"交付路径有效", "100%"}}},
			map[string]interface{}{"kind": "timeline", "title": "推进节奏", "items": []interface{}{map[string]interface{}{"label": "D1–30 · 稳态", "milestone": "上线基础门禁", "points": []string{"修复致命问题", "接入运行观测"}}, map[string]interface{}{"label": "D31–60 · 增强", "milestone": "完成专业质检", "points": []string{"提升复杂任务", "固化复算口径"}}, map[string]interface{}{"label": "D61–90 · 规模", "milestone": "建立持续评测", "points": []string{"复盘试点结果", "稳定交付节奏"}}}},
			map[string]interface{}{"kind": "closing", "title": "下一步", "takeaway": "按负责人推进并用验收结果闭环", "decisions_label": "待确认决策", "commitments_label": "阶段结果承诺", "next_actions": []interface{}{map[string]string{"owner": "平台组", "action": "完成逐页视觉验收"}, map[string]string{"owner": "评测组", "action": "固化能力回归集"}}, "commitments": []interface{}{map[string]string{"title": "30 天", "body": "上线基础门禁"}, map[string]string{"title": "60 天", "body": "完成专业质检"}, map[string]string{"title": "90 天", "body": "形成持续评测"}}},
		},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	builder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "build_task_pptx.py")
	cmd := exec.Command(python, builder, "--spec", specPath, "--output", outputPath)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("structured-metadata deck build failed: %v\n%s", err, output)
	}

	audienceText, err := contenttools.ExtractPPTX(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AgentOS", "平台组", "完成逐页视觉验收", "评测组", "PPT致命版式问题", "报价复算差异", "上线基础门禁", "接入运行观测", "阶段结果承诺", "形成持续评测"} {
		if !strings.Contains(audienceText, want) {
			t.Fatalf("required audience content was dropped (%q):\n%s", want, audienceText)
		}
	}
	for _, forbidden := range []string{"{'name':", `"section":`, "{'owner':", `"action":`} {
		if strings.Contains(audienceText, forbidden) {
			t.Fatalf("structured metadata leaked into audience text (%q):\n%s", forbidden, audienceText)
		}
	}
	if violations := pptPolishAudienceContentViolations(audienceText); len(violations) != 0 {
		t.Fatalf("clean structured-metadata deck was rejected: %v", violations)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	auditCmd := exec.Command(python, audit, outputPath, "--min-body-font-size", "14", "--min-title-font-size", "26")
	if output, err := auditCmd.CombinedOutput(); err != nil {
		t.Fatalf("structured-metadata deck failed requested font/audience audit: %v\n%s", err, output)
	}
}

func TestPPTArtDirectorChartNoteAllowsCompactChineseFundingBreakdown(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	builder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "build_task_pptx.py")
	build := func(name, note string) ([]byte, error) {
		t.Helper()
		specPath := filepath.Join(dir, name+".json")
		outputPath := filepath.Join(dir, name+".pptx")
		spec := map[string]interface{}{
			"needs_customization": false,
			"quality_profile":     "audience-facing",
			"brand":               "OpenKitty",
			"slides": []interface{}{
				map[string]interface{}{"kind": "cover", "title": "融资计划", "subtitle": "可编辑演示稿"},
				map[string]interface{}{"kind": "statement", "title": "机会", "claim": "企业需要可交付的 AI 员工", "evidence": []string{"执行", "审计"}},
				map[string]interface{}{"kind": "chart", "title": "资金用途", "categories": []string{"研发", "销售", "运营", "储备"}, "series": []interface{}{map[string]interface{}{"name": "占比", "values": []int{45, 30, 15, 10}}}, "chart_type": "doughnut", "note_title": "规划估算", "note": note},
				map[string]interface{}{"kind": "split", "title": "商业路径", "left": map[string]string{"title": "订阅", "body": "AI 工位"}, "right": map[string]string{"title": "服务", "body": "私有部署"}},
				map[string]interface{}{"kind": "timeline", "title": "里程碑", "items": []interface{}{map[string]string{"label": "M0–6", "title": "验证", "detail": "完成试点"}, map[string]string{"label": "M7–12", "title": "复制", "detail": "形成方法"}}},
				map[string]interface{}{"kind": "closing", "title": "下一步", "takeaway": "验证产品与商业闭环"},
			},
		}
		data, marshalErr := json.Marshal(spec)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(specPath, data, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
		cmd := exec.Command(python, builder, "--spec", specPath, "--output", outputPath)
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
		return cmd.CombinedOutput()
	}

	if output, err := build("accepted-note", strings.Repeat("规", 152)); err != nil {
		t.Fatalf("a compact 152-character chart note should build and defer actual fit to render audit: %v\n%s", err, output)
	}
	if output, err := build("over-limit-note", strings.Repeat("规", 181)); err == nil || !strings.Contains(string(output), "exceeds 180 characters") {
		t.Fatalf("the relaxed limit must remain bounded: err=%v\n%s", err, output)
	}
}

func TestPPTArtDirectorScaffoldBuildsEditableDeck(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx scaffold dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	builder := filepath.Join(dir, "task", "build_deck.py")
	outputPath := filepath.Join(dir, "deliverables", "scaffold-smoke.pptx")
	scaffolder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "scaffold_task_pptx.py")
	cmd := exec.Command(python, scaffolder, "--builder", builder, "--output", outputPath)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	scaffoldOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("task PPTX scaffold failed: %v\n%s", err, scaffoldOutput)
	}
	for _, want := range []string{
		"normally 6-14", "at least four body composition families", "three are not",
		"TASK PAGE COMPOSITIONS", "palette_strategy", "conclusion-led titles", "visual_plan.json", "50/35/24/16",
		"Do not inspect or edit pptx_design.py",
	} {
		if !strings.Contains(string(scaffoldOutput), want) {
			t.Fatalf("scaffold receipt missing %q: %s", want, scaffoldOutput)
		}
	}
	for _, path := range []string{builder, filepath.Join(filepath.Dir(builder), "pptx_design.py"), filepath.Join(filepath.Dir(builder), "deck_spec.json"), filepath.Join(filepath.Dir(builder), "visual_plan.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("scaffolded task file missing %s: %v", path, err)
		}
	}
	builderData, err := os.ReadFile(builder)
	if err != nil {
		t.Fatalf("read scaffolded builder: %v", err)
	}
	if !strings.Contains(string(builderData), "SPEC_PATH") || !strings.Contains(string(builderData), "needs_customization") {
		t.Fatal("scaffolded builder must use the compact task deck specification")
	}
	if !strings.Contains(string(builderData), `COMPOSITION_MODE = "per-slide"`) {
		t.Fatal("scaffolded builder must require per-slide composition")
	}
	customPageRenderers := `def task_slide_01(deck, spec, page_spec, page, total):
    render_cover(deck, spec, page_spec)

def task_slide_02(deck, spec, page_spec, page, total):
    render_statement(deck, spec, page_spec, page, total)

def task_slide_03(deck, spec, page_spec, page, total):
    render_cards(deck, spec, page_spec, page, total)

def task_slide_04(deck, spec, page_spec, page, total):
    render_process(deck, spec, page_spec, page, total)

def task_slide_05(deck, spec, page_spec, page, total):
    render_split(deck, spec, page_spec, page, total)

def task_slide_06(deck, spec, page_spec, page, total):
    render_closing(deck, spec, page_spec, page, total)

TASK_PAGE_RENDERERS = {
    1: task_slide_01, 2: task_slide_02, 3: task_slide_03,
    4: task_slide_04, 5: task_slide_05, 6: task_slide_06,
}`
	customizedBuilder := strings.Replace(string(builderData), "TASK_PAGE_RENDERERS = {}", customPageRenderers, 1)
	if customizedBuilder == string(builderData) {
		t.Fatal("task page renderer anchor missing from scaffolded builder")
	}
	specPath := filepath.Join(filepath.Dir(builder), "deck_spec.json")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read scaffolded deck spec: %v", err)
	}
	specData = []byte(strings.Replace(string(specData), `"needs_customization": true`, `"needs_customization": false`, 1))
	specData = []byte(strings.Replace(string(specData), `"claim": "Replace with one defensible conclusion"`, `"statement": "Rendered statement alias"`, 1))
	specData = []byte(strings.Replace(string(specData), `"takeaway": "Replace with the one conclusion this page must land"`, `"takeaway": "Rendered statement alias"`, 1))
	specData = []byte(strings.Replace(string(specData), `"evidence": ["Evidence A", "Evidence B", "Evidence C"]`, `"points": ["Rendered point A", "Rendered point B", "Rendered point C"]`, 1))
	if err := os.WriteFile(specPath, specData, 0644); err != nil {
		t.Fatalf("customize scaffolded deck spec: %v", err)
	}
	visualPlanPath := filepath.Join(filepath.Dir(builder), "visual_plan.json")
	visualSlides := make([]interface{}, 0, 6)
	for page, composition := range []string{"cinematic-cover", "asymmetric-claim", "editorial-comparison", "causal-flow", "split-synthesis", "closing-manifesto"} {
		assetType := "typographic"
		if page >= 1 && page <= 3 {
			assetType = "diagram"
		}
		visualSlides = append(visualSlides, map[string]interface{}{
			"slide": page + 1, "narrative_job": "Communicate the page conclusion", "composition": composition,
			"density": "medium", "focal_anchor": "One dominant proof object",
			"asset": map[string]interface{}{"type": assetType, "purpose": "Explain the claim", "source": "source-bound deck evidence", "path": "", "prompt": "", "crop": "contain"},
		})
	}
	visualPlan := map[string]interface{}{
		"version": 1, "needs_customization": false, "communication_job": "Make the audience understand and decide", "reference_owned": false,
		"selected_direction": map[string]interface{}{
			"id": "editorial", "name": "Warm editorial evidence", "visual_thesis": "Warm paper and precise diagrams make evidence feel clear and authoritative", "rationale": "The restrained system keeps attention on claims",
			"palette":     map[string]string{"navy": "#111820", "ink": "#20252B", "muted": "#626A73", "line": "#D9DDE1", "paper": "#F4F1EA", "white": "#FFFFFF", "accent": "#C26D3A", "accent_soft": "#F2DED0", "positive": "#47735A", "positive_soft": "#DDEAE1", "warning": "#B17A25", "warning_soft": "#F4E8CB", "negative": "#A44945", "negative_soft": "#F0DAD8"},
			"typography":  map[string]interface{}{"font_cn": "Microsoft YaHei", "font_en": "Arial", "deck_title_pt": 50, "slide_title_pt": 35, "subhead_pt": 24, "body_pt": 16},
			"image_style": "Documentary subject-led imagery with quiet light", "chart_style": "Direct labels and low ink", "background_rhythm": []string{"hero-dark", "light", "light", "anchor", "light", "closing-dark"},
		},
		"candidates": []interface{}{
			map[string]string{"id": "editorial", "name": "Warm editorial evidence", "visual_thesis": "Warm paper and precise diagrams", "differentiator": "Material warmth"},
			map[string]string{"id": "spatial", "name": "Spatial monochrome", "visual_thesis": "Monochrome space and bold type", "differentiator": "More cinematic, less document-like"},
		},
		"slides": visualSlides,
	}
	visualPlanData, err := json.Marshal(visualPlan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visualPlanPath, visualPlanData, 0o644); err != nil {
		t.Fatalf("customize visual plan: %v", err)
	}
	visualValidator := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "validate_visual_plan.py")
	validate := exec.Command(python, visualValidator, visualPlanPath, "--expected-slides", "6")
	validate.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := validate.CombinedOutput(); err != nil || !strings.Contains(string(output), `"visual_plan_valid": true`) {
		t.Fatalf("customized visual plan failed validation: %v\n%s", err, output)
	}

	cmd = exec.Command(python, builder)
	cmd.Dir = filepath.Dir(builder)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "per-slide customization is incomplete") {
		t.Fatalf("unmodified scaffold unexpectedly built without per-slide renderers: err=%v\n%s", err, output)
	}
	thinDispatcherRenderers := `def _render_page(deck, spec, page_spec, page, total):
    renderer = RENDERERS[page_spec["kind"]]
    if page_spec["kind"] == "cover":
        renderer(deck, spec, page_spec)
    else:
        renderer(deck, spec, page_spec, page, total)

def task_slide_01(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

def task_slide_02(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

def task_slide_03(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

def task_slide_04(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

def task_slide_05(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

def task_slide_06(deck, spec, page_spec, page, total):
    _render_page(deck, spec, page_spec, page, total)

TASK_PAGE_RENDERERS = {
    1: task_slide_01, 2: task_slide_02, 3: task_slide_03,
    4: task_slide_04, 5: task_slide_05, 6: task_slide_06,
}`
	thinDispatcherBuilder := strings.Replace(string(builderData), "TASK_PAGE_RENDERERS = {}", thinDispatcherRenderers, 1)
	if err := os.WriteFile(builder, []byte(thinDispatcherBuilder), 0o644); err != nil {
		t.Fatalf("write thin-dispatcher builder: %v", err)
	}
	cmd = exec.Command(python, builder)
	cmd.Dir = filepath.Dir(builder)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "thin dispatcher wrappers") {
		t.Fatalf("thin whole-deck dispatcher unexpectedly passed per-slide validation: err=%v\n%s", err, output)
	}
	if err := os.WriteFile(builder, []byte(customizedBuilder), 0o644); err != nil {
		t.Fatalf("customize scaffolded builder: %v", err)
	}

	cmd = exec.Command(python, builder)
	cmd.Dir = filepath.Dir(builder)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scaffolded builder failed: %v\n%s", err, output)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("scaffolded PPTX missing: %v", err)
	}
	if info.Size() < 20*1024 {
		t.Fatalf("scaffolded PPTX unexpectedly small: %d", info.Size())
	}
	audienceText, err := contenttools.ExtractPPTX(outputPath)
	if err != nil {
		t.Fatalf("extract scaffolded PPTX text: %v", err)
	}
	for _, want := range []string{"Rendered statement alias", "Rendered point A", "Rendered point C"} {
		if !strings.Contains(audienceText, want) {
			t.Fatalf("statement/points alias content was dropped (%q):\n%s", want, audienceText)
		}
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	if output, err := exec.Command(python, audit, outputPath).CombinedOutput(); err != nil {
		t.Fatalf("scaffolded PPTX audit failed: %v\n%s", err, output)
	}
}

func TestPPTArtDirectorAestheticQAValidatorRequiresScoredSlideCoverage(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "report.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"p=Presentation()",
		"p.slides.add_slide(p.slide_layouts[6])",
		"p.slides.add_slide(p.slide_layouts[6])",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create aesthetic QA deck: %v\n%s", err, output)
	}
	renderDir := filepath.Join(dir, "rendered_pages")
	if err := os.MkdirAll(renderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 2; page++ {
		file, err := os.Create(filepath.Join(renderDir, fmt.Sprintf("slide_%02d.png", page)))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	scores := map[string]interface{}{"hierarchy": 82, "composition": 82, "typography": 82, "asset_quality": 82, "visual_relevance": 82, "density_balance": 82}
	qa := map[string]interface{}{
		"version": 1,
		"slides": []interface{}{
			map[string]interface{}{"slide": 1, "image": "slide_01.png", "scores": scores, "observations": []string{"Clear focal point and intentional spacing"}, "blocking_defects": []string{}},
			map[string]interface{}{"slide": 2, "image": "slide_02.png", "scores": scores, "observations": []string{"Closing resolves the opening"}, "blocking_defects": []string{}},
		},
		"deck_scores":     map[string]interface{}{"coherence": 84, "rhythm": 84, "visual_storytelling": 84, "originality": 84},
		"repairs_applied": []string{}, "verdict": "pass",
	}
	qaPath := filepath.Join(dir, "aesthetic_qa.json")
	writeQA := func(value map[string]interface{}) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(qaPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeQA(qa)
	validator := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "validate_aesthetic_qa.py")
	run := func() ([]byte, error) {
		cmd := exec.Command(python, validator, "--qa", qaPath, "--deck", deck, "--render-dir", renderDir, "--expected-slides", "2")
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
		return cmd.CombinedOutput()
	}
	if output, err := run(); err != nil || !strings.Contains(string(output), `"aesthetic_qa_passed": true`) {
		t.Fatalf("valid aesthetic QA ledger failed: %v\n%s", err, output)
	}
	badScores := map[string]interface{}{"hierarchy": 82, "composition": 60, "typography": 82, "asset_quality": 82, "visual_relevance": 82, "density_balance": 82}
	qa["slides"].([]interface{})[0].(map[string]interface{})["scores"] = badScores
	writeQA(qa)
	if output, err := run(); err == nil || !strings.Contains(string(output), "below 65") {
		t.Fatalf("low aesthetic score unexpectedly passed: err=%v\n%s", err, output)
	}
}

func TestPPTArtDirectorScaffoldRejectsSharedStorageRoot(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}

	dir := t.TempDir()
	storage := filepath.Join(dir, ".openkitty", "storage")
	builder := filepath.Join(storage, "build_deck.py")
	outputPath := filepath.Join(storage, "report.pptx")
	scaffolder := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "scaffold_task_pptx.py")
	cmd := exec.Command(python, scaffolder, "--builder", builder, "--output", outputPath)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shared storage root accepted a collision-prone builder:\n%s", output)
	}
	if !strings.Contains(string(output), "TASK_PRIVATE_RUN_DIRECTORY_REQUIRED") ||
		!strings.Contains(string(output), "ppt_runs") {
		t.Fatalf("shared-root rejection lacks recovery guidance: %s", output)
	}
	if _, statErr := os.Stat(builder); !os.IsNotExist(statErr) {
		t.Fatalf("rejected scaffold still created the shared builder: %v", statErr)
	}
}

func TestPPTArtDirectorDesignAdaptersUseInchFloatGeometry(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, lxml").CombinedOutput(); err != nil {
		t.Skipf("python-pptx scaffold dependencies unavailable: %v: %s", err, output)
	}

	designDir := filepath.Join("builtin", "presentation", "ppt-art-director", "assets", "task-pptx")
	script := strings.Join([]string{
		"from pptx.enum.text import MSO_ANCHOR",
		"from enum import Enum",
		"from pptx.util import Inches",
		"from pptx_design import DEFAULT_THEME, assert_deck_bounds, card, chip, circle, decision_frame, editable_table, line, matrix, native_chart, new_presentation, new_slide, proportional_bar, rect, rich_text_box, round_rect, semantic_legend, text_box, timeline",
		"p=new_presentation()",
		"s=new_slide(p)",
		"inch_box=rect(s, 0.5, 1.0, 2.0, 0.5, fill=DEFAULT_THEME.paper)",
		"assert (inch_box.left,inch_box.top,inch_box.width,inch_box.height)==(457200,914400,1828800,457200)",
		"exec(\"try:\\n rect(s, Inches(0.5), 1.0, 2.0, 0.5)\\n raise AssertionError('Inches must be rejected')\\nexcept TypeError as exc:\\n assert 'inch float' in str(exc)\")",
		"exec(\"try:\\n rect(s, int(Inches(0.5)), 1.0, 2.0, 0.5)\\n raise AssertionError('raw EMU must be rejected')\\nexcept TypeError as exc:\\n assert 'raw EMU integer' in str(exc)\")",
		"box=text_box(s, 0.5, 0.5, 4.0, 0.5, 'Alias-safe text', font_size=12, italic=True)",
		"assert box.text_frame.paragraphs[0].runs[0].font.italic is True",
		"rich_adapter=text_box(s, 0.5, 6.0, 4.0, 0.5, [{'text':'Rich adapter','size':14,'bold':True,'color':DEFAULT_THEME.accent}])",
		"assert rich_adapter.text == 'Rich adapter'",
		"assert rich_adapter.text_frame.paragraphs[0].runs[0].font.bold is True",
		"anchor_box=text_box(s, 4.7, 0.5, 1.0, 0.5, 'M', anchor='middle')",
		"assert anchor_box.text_frame.vertical_anchor is MSO_ANCHOR.MIDDLE",
		"rich_anchor_box=rich_text_box(s, 5.8, 0.5, 1.0, 0.5, [{'text':'B'}], anchor='bottom')",
		"assert rich_anchor_box.text_frame.vertical_anchor is MSO_ANCHOR.BOTTOM",
		"qualified_anchor_box=text_box(s, 6.9, 0.5, 1.0, 0.5, 'Q', anchor='MSO_VERTICAL_ANCHOR.MIDDLE')",
		"assert qualified_anchor_box.text_frame.vertical_anchor is MSO_ANCHOR.MIDDLE",
		"ForeignAnchor=Enum('MSO_VERTICAL_ANCHOR', {'TOP':1,'MIDDLE':3,'BOTTOM':4})",
		"foreign_anchor_box=text_box(s, 8.0, 0.5, 1.0, 0.5, 'F', anchor=ForeignAnchor.MIDDLE)",
		"assert foreign_anchor_box.text_frame.vertical_anchor is MSO_ANCHOR.MIDDLE",
		"legacy_anchor_box=text_box(s, 9.1, 0.5, 1.0, 0.5, 'L', anchor=3)",
		"assert legacy_anchor_box.text_frame.vertical_anchor is MSO_ANCHOR.MIDDLE",
		"exec(\"try:\\n text_box(s, 10.2, 0.5, 1.0, 0.5, 'class', anchor=MSO_ANCHOR)\\n raise AssertionError('enum class must be rejected')\\nexcept ValueError as exc:\\n assert 'pass a member' in str(exc)\")",
		"exec(\"try:\\n text_box(s, 7.0, 0.5, 1.0, 0.5, 'bad', anchor='sideways')\\n raise AssertionError('invalid anchor must be rejected')\\nexcept ValueError as exc:\\n assert 'MSO_ANCHOR.MIDDLE' in str(exc)\")",
		"text_box(s, x=0.5, y=0.9, w=4.0, h=0.5, text='Font alias', font='Aptos', theme=DEFAULT_THEME, fill=DEFAULT_THEME.paper)",
		"round_rect(s, 0.5, 1.5, 2.0, 0.8, 0.1, fill=DEFAULT_THEME.paper, color=DEFAULT_THEME.line, radius=0.08, theme=DEFAULT_THEME)",
		"circle(s, 3.0, 1.5, 0.7, 0.4, fill=DEFAULT_THEME.accent, theme=DEFAULT_THEME)",
		"chip(s, 4.0, 1.5, 'Legacy chip', 0.4, size=9, bold=False)",
		"chip(s, 4.0, 2.0, 1.6, 0.35, fill=DEFAULT_THEME.accent_soft)",
		"card(s, 6.0, 1.5, 3.0, 1.5, title='Card', body='Compatible border', line=DEFAULT_THEME.line, line_width=1.0, radius=0.1)",
		"round_rect(s, 9.4, 3.0, 4.0, 0.7, fill=DEFAULT_THEME.paper)",
		"line(s, (0.5, 4.0), (12.8, 4.0), color=DEFAULT_THEME.line, theme=DEFAULT_THEME)",
		"decision_frame(s, deck_title='Decision', takeaway='Evidence supports the claim', brand='OpenKitty', page=1, total=1, source_note='Frozen input')",
		"semantic_legend(s, 0.5, 6.7)",
		"track, fill, ratio=proportional_bar(s, 0.5, 5.0, 4.0, 0.25, 3, 5)",
		"assert track is not None and fill is not None and ratio == 0.6",
		"s2=new_slide(p)",
		"native_chart(s2, 0.5, 0.5, 5.8, 2.6, ['A','B'], [{'name':'Actual','values':[1,2]}], chart_type='stacked-bar', show_data_labels=True)",
		"editable_table(s2, 6.7, 0.5, 6.1, 2.6, ['Option','Value'], [['A','1'],['B','2']], column_widths=[3.0,3.1])",
		"timeline(s2, 0.5, 3.6, 5.8, 2.8, [{'label':'01','title':'Start','detail':'Explicit'},{'label':'02','title':'Finish','detail':'Explicit'}])",
		"matrix(s2, 6.7, 3.5, 6.1, 3.4, [{'label':'A','x':0.2,'y':0.8},{'label':'B','x':0.7,'y':0.3}], x_label='X', y_label='Y')",
		"assert_deck_bounds(p)",
	}, ";")
	cmd := exec.Command(python, "-c", script)
	cmd.Dir = designDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pptx design compatibility adapters failed: %v\n%s", err, output)
	}

	reject := strings.Join([]string{
		"from pptx_design import matrix, new_presentation, new_slide",
		"p=new_presentation()",
		"s=new_slide(p)",
		"matrix(s, 0.5, 0.5, 2, 2, [{'label':'bad','x':1.2,'y':0.5}])",
	}, ";")
	cmd = exec.Command(python, "-c", reject)
	cmd.Dir = designDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "0..1") {
		t.Fatalf("matrix must reject guessed/out-of-range coordinates: err=%v\n%s", err, output)
	}
}

func TestPPTArtDirectorAuditRejectsSerializedRichTextPayload(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "serialized-rich-text.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"s.shapes.add_textbox(Inches(1), Inches(1), Inches(8), Inches(1)).text=\"[{'text': 'Visible title', 'size': 24, 'color': (20, 41, 71), 'bold': True, 'align': <PP_PARAGRAPH_ALIGNMENT.LEFT: 1>} ]\"",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create serialized rich-text fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err == nil {
		t.Fatalf("serialized rich-text payload unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "serialized rich-text/style payload") {
		t.Fatalf("serialized payload failure lacks diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsSerializedConfigurationPayload(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "serialized-config.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		`s.shapes.add_textbox(Inches(1), Inches(1), Inches(8), Inches(1)).text="{'owner': '平台组', 'action': '完成逐页验收'}"`,
		`s.shapes.add_textbox(Inches(1), Inches(2), Inches(8), Inches(1)).text="鍒嗘瀽缁撴灉锛氫换鍔″凡瀹屾垚"`,
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	create.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create serialized configuration fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err == nil {
		t.Fatalf("serialized configuration payload unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "configuration/action dictionaries are also forbidden") {
		t.Fatalf("serialized configuration failure lacks diagnosis: %s", output)
	}
	if !strings.Contains(string(output), "likely mojibake/garbled audience text") {
		t.Fatalf("garbled-text failure lacks diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditEnforcesRequestedFontFloors(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "font-floor.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches,Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"title=s.shapes.add_textbox(Inches(.6),Inches(.35),Inches(10),Inches(.6))",
		"title.text_frame.paragraphs[0].text='Management title'",
		"title.text_frame.paragraphs[0].font.size=Pt(24)",
		"body=s.shapes.add_textbox(Inches(.6),Inches(1.5),Inches(8),Inches(1))",
		"body.text_frame.paragraphs[0].text='Readable content that must honor the requested floor'",
		"body.text_frame.paragraphs[0].font.size=Pt(12)",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create font-floor fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	if output, err := exec.Command(python, audit, deck).CombinedOutput(); err != nil {
		t.Fatalf("request-specific font floors leaked into the default audit: %v\n%s", err, output)
	}
	output, err := exec.Command(
		python,
		audit,
		deck,
		"--min-body-font-size", "14",
		"--min-title-font-size", "26",
	).CombinedOutput()
	if err == nil {
		t.Fatalf("requested font floors unexpectedly passed: %s", output)
	}
	for _, diagnosis := range []string{"requested 14pt body floor", "requested 26pt title floor"} {
		if !strings.Contains(string(output), diagnosis) {
			t.Fatalf("font-floor audit lacks %q diagnosis: %s", diagnosis, output)
		}
	}
}

func TestPPTArtDirectorAuditRejectsTitleOnlyContentSlide(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	deck := filepath.Join(t.TempDir(), "title-only-content-slide.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.enum.shapes import MSO_SHAPE",
		"from pptx.util import Inches,Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"cover=s.shapes.add_textbox(Inches(.6),Inches(.8),Inches(10),Inches(1))",
		"cover.text_frame.paragraphs[0].text='Executive delivery plan'",
		"cover.text_frame.paragraphs[0].font.size=Pt(30)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"title=s.shapes.add_textbox(Inches(.6),Inches(.35),Inches(10),Inches(.7))",
		"title.text_frame.paragraphs[0].text='Root cause diagnosis'",
		"title.text_frame.paragraphs[0].font.size=Pt(28)",
		"s.shapes.add_shape(MSO_SHAPE.RECTANGLE,Inches(.6),Inches(2),Inches(.1),Inches(2))",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create title-only fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(
		python,
		audit,
		deck,
		"--min-body-font-size", "14",
		"--min-title-font-size", "26",
	).CombinedOutput()
	if err == nil {
		t.Fatalf("title-only content slide unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "no substantive body content below the title region") {
		t.Fatalf("title-only failure lacks the concrete diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsFractionalOOXMLCoordinates(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"s.shapes.add_textbox(Inches(1), Inches(1), Inches(4), Inches(1)).text='Validated content'",
		"p.save(r'" + strings.ReplaceAll(valid, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create valid fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	if output, err := exec.Command(python, audit, valid).CombinedOutput(); err != nil {
		t.Fatalf("valid PPTX audit failed: %v\n%s", err, output)
	}

	broken := filepath.Join(dir, "fractional.pptx")
	createBroken := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"s.shapes.add_textbox(Inches(1), Inches(1), Inches(4), Inches(1)).text='Validated content'",
		"s.shapes.add_connector(1, 914400.5, 1828800.0, 2743200.5, 1828800.0)",
		"p.save(r'" + strings.ReplaceAll(broken, "'", "''") + "')",
	}, ";"))
	if output, err := createBroken.CombinedOutput(); err != nil {
		t.Fatalf("create fractional fixture: %v\n%s", err, output)
	}
	output, err := exec.Command(python, audit, broken).CombinedOutput()
	if err == nil {
		t.Fatalf("fractional OOXML coordinate unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "fractional/invalid EMU") {
		t.Fatalf("fractional coordinate failure lacks diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditDistinguishesPolicyTextFromPlaceholder(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	createDeck := func(path, text string) {
		t.Helper()
		cmd := exec.Command(python, "-c", strings.Join([]string{
			"from pptx import Presentation",
			"from pptx.util import Inches",
			"p=Presentation()",
			"s=p.slides.add_slide(p.slide_layouts[6])",
			"s.shapes.add_textbox(Inches(1), Inches(1), Inches(8), Inches(1)).text=" + fmt.Sprintf("%q", text),
			"p.save(r'" + strings.ReplaceAll(path, "'", "''") + "')",
		}, ";"))
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create placeholder fixture: %v\n%s", err, output)
		}
	}

	policy := filepath.Join(dir, "policy.pptx")
	createDeck(policy, "质量边界：不写占位符，不补造数据")
	if output, err := exec.Command(python, audit, policy).CombinedOutput(); err != nil {
		t.Fatalf("normal policy text was misclassified as a placeholder: %v\n%s", err, output)
	}

	placeholder := filepath.Join(dir, "placeholder.pptx")
	createDeck(placeholder, "方案标题 TBD")
	if output, err := exec.Command(python, audit, placeholder).CombinedOutput(); err != nil || !strings.Contains(string(output), "placeholder on slide") {
		t.Fatalf("explicit placeholder was not reported as a quality warning: err=%v\n%s", err, output)
	}
}

func TestPPTArtDirectorAuditWarnsOnExcessiveTinyTextBoxes(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "tiny-text.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches, Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"boxes=[s.shapes.add_textbox(Inches(.6+(i%4)*3.0), Inches(.8+(i//4)*2.0), Inches(2.6), Inches(.6)) for i in range(8)]",
		"[(setattr(box.text_frame.paragraphs[0], 'text', f'Dense small label {i}'), setattr(box.text_frame.paragraphs[0].font, 'size', Pt(11))) for i,box in enumerate(boxes)]",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create tiny-text fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err != nil {
		t.Fatalf("readable PPTX was blocked by a quality-only tiny-text finding: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "\"status\": \"passed_with_warnings\"") ||
		!strings.Contains(string(output), "non-footer text boxes at 12pt or smaller") {
		t.Fatalf("tiny-text warning lacks severity or actionable diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsLikelyTextOverflow(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "overflow.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches, Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"b=s.shapes.add_textbox(Inches(.8), Inches(3.7), Inches(5.45), Inches(.48))",
		"b.text_frame.paragraphs[0].text='情景、触发信号、缓释措施和责任角色全部挤在一个很矮的文本框中，必然覆盖下一行内容'",
		"b.text_frame.paragraphs[0].font.size=Pt(16)",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create overflow fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err == nil {
		t.Fatalf("overflow escaped the audience-facing quality gate:\n%s", output)
	}
	if !strings.Contains(string(output), "text boxes likely to overflow at authored font size") ||
		!strings.Contains(string(output), `"quality_gate_passed": false`) {
		t.Fatalf("overflow failure lacks diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsNearIdenticalTextRegions(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "overlapping-text.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches, Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"title=s.shapes.add_textbox(Inches(.7), Inches(.35), Inches(9), Inches(.65))",
		"title.text_frame.paragraphs[0].text='Evidence-led headline'",
		"title.text_frame.paragraphs[0].font.size=Pt(30)",
		"a=s.shapes.add_textbox(Inches(1), Inches(2), Inches(5.5), Inches(1.1))",
		"a.text_frame.paragraphs[0].text='First substantive page region'",
		"a.text_frame.paragraphs[0].font.size=Pt(18)",
		"b=s.shapes.add_textbox(Inches(1), Inches(2), Inches(10.8), Inches(1.1))",
		"b.text_frame.paragraphs[0].text='Second region accidentally reused the same coordinates'",
		"b.text_frame.paragraphs[0].font.size=Pt(18)",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create overlap fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err == nil {
		t.Fatalf("same-coordinate text regions escaped the quality gate:\n%s", output)
	}
	if !strings.Contains(string(output), "near-total overlapping text-box pairs") ||
		!strings.Contains(string(output), `"quality_gate_passed": false`) {
		t.Fatalf("overlap failure lacks actionable diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditAllowsNestedTextInsideLabeledPanel(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "nested-panel-text.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.enum.shapes import MSO_SHAPE",
		"from pptx.util import Inches, Pt",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"title=s.shapes.add_textbox(Inches(.7), Inches(.35), Inches(9), Inches(.65))",
		"title.text_frame.paragraphs[0].text='One labelled evidence panel'",
		"title.text_frame.paragraphs[0].font.size=Pt(30)",
		"panel=s.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(.7), Inches(1.5), Inches(6), Inches(4))",
		"panel.text_frame.paragraphs[0].text='Method comparison'",
		"panel.text_frame.paragraphs[0].font.size=Pt(18)",
		"detail=s.shapes.add_textbox(Inches(1), Inches(2.5), Inches(5.3), Inches(.8))",
		"detail.text_frame.paragraphs[0].text='Supporting evidence deliberately sits inside the larger panel'",
		"detail.text_frame.paragraphs[0].font.size=Pt(18)",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create nested-panel fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err != nil {
		t.Fatalf("legitimate nested panel text was treated as a collision: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "near-total overlapping text-box pairs") {
		t.Fatalf("nested panel produced an overlap false positive: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsRepeatedRoundedCardDashboard(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "rounded-dashboard.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.enum.shapes import MSO_SHAPE",
		"from pptx.util import Inches",
		"p=Presentation()",
		"p.slide_width=Inches(13.333)",
		"p.slide_height=Inches(7.5)",
		"slides=[p.slides.add_slide(p.slide_layouts[6]) for _ in range(5)]",
		"[setattr(s.shapes.add_textbox(Inches(.6), Inches(.3), Inches(8), Inches(.6)), 'text', f'Claim {page+1}') for page,s in enumerate(slides)]",
		"[[s.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(.6+(i%2)*6.0), Inches(1.3+(i//2)*2.5), Inches(5.2), Inches(1.8)) for i in range(4)] for s in slides]",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create rounded-dashboard fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, deck).CombinedOutput()
	if err == nil {
		t.Fatalf("rounded-card dashboard escaped the audience-facing quality gate:\n%s", output)
	}
	if !strings.Contains(string(output), "rounded-card UI grammar dominates") ||
		!strings.Contains(string(output), `"quality_gate_passed": false`) {
		t.Fatalf("rounded-card failure lacks actionable diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditRejectsLateOpaqueOccluder(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	broken := filepath.Join(dir, "occluded.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"from pptx.dml.color import RGBColor",
		"from pptx.enum.shapes import MSO_SHAPE",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"[setattr(s.shapes.add_textbox(Inches(1), Inches(1+i), Inches(4), Inches(.5)), 'text', f'Visible row {i}') for i in range(4)]",
		"o=s.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(.5), Inches(.5), Inches(8), Inches(5))",
		"o.fill.solid()",
		"o.fill.fore_color.rgb=RGBColor(255,255,255)",
		"p.save(r'" + strings.ReplaceAll(broken, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create occluded fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	output, err := exec.Command(python, audit, broken).CombinedOutput()
	if err == nil {
		t.Fatalf("late opaque occluder unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "late opaque shape covering") {
		t.Fatalf("occluder failure lacks diagnosis: %s", output)
	}
}

func TestPPTArtDirectorAuditAcceptsGraphicFrameTable(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	deck := filepath.Join(dir, "table.pptx")
	create := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"s=p.slides.add_slide(p.slide_layouts[6])",
		"s.shapes.add_textbox(Inches(.5), Inches(.4), Inches(8), Inches(.6)).text='Flight options'",
		"t=s.shapes.add_table(3, 3, Inches(.5), Inches(1.2), Inches(8), Inches(2)).table",
		"t.cell(0,0).text='Route'",
		"t.cell(0,1).text='Duration'",
		"t.cell(0,2).text='Notes'",
		"t.cell(1,0).text='Direct'",
		"t.cell(1,1).text='2h 30m'",
		"t.cell(1,2).text='Fastest option'",
		"p.save(r'" + strings.ReplaceAll(deck, "'", "''") + "')",
	}, ";"))
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create table fixture: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	if output, err := exec.Command(python, audit, deck).CombinedOutput(); err != nil {
		t.Fatalf("ordinary GraphicFrame table failed structural audit: %v\n%s", err, output)
	}
}

func TestSummarizePPTXAuditResultSeparatesStructureFromQuality(t *testing.T) {
	got := summarizePPTXAuditResult(`{
		"ok": true,
		"status": "passed_with_warnings",
		"fatal_errors": [],
		"quality_warnings": ["small text", "repeated layout"]
	}`)
	if !strings.Contains(got, "结构审计已通过") || !strings.Contains(got, "2项") || !strings.Contains(got, "保留有效交付物") {
		t.Fatalf("quality findings were not summarized as non-blocking advice: %s", got)
	}

	got = summarizePPTXAuditResult(`{
		"ok": false,
		"status": "failed",
		"fatal_errors": ["malformed XML"],
		"quality_warnings": []
	}`)
	if !strings.Contains(got, "结构审计未通过") {
		t.Fatalf("fatal structural errors were not kept blocking: %s", got)
	}
}

func TestBasePPTGeneratorNormalizesSafeAliasesAndRejectsUnsupportedDiagrams(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, matplotlib, PIL").CombinedOutput(); err != nil {
		t.Skipf("base-ppt Python dependencies unavailable: %v: %s", err, output)
	}

	script := filepath.Join("builtin", "presentation", "base-ppt", "generate.py")
	dir := t.TempDir()
	wideImage := filepath.Join(dir, "wide.png")
	wide := image.NewRGBA(image.Rect(0, 0, 400, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 400; x++ {
			wide.Set(x, y, color.RGBA{R: 66, G: 133, B: 244, A: 255})
		}
	}
	wideFile, err := os.Create(wideImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(wideFile, wide); err != nil {
		wideFile.Close()
		t.Fatal(err)
	}
	if err := wideFile.Close(); err != nil {
		t.Fatal(err)
	}
	validInput := filepath.Join(dir, "valid.json")
	validOutput := filepath.Join(dir, "valid.pptx")
	valid := map[string]interface{}{
		"meta": map[string]interface{}{"theme": "google_style", "title": "QA"},
		"slides": []interface{}{
			map[string]interface{}{"type": "cover", "title": "QA Deck", "subtitle": "Compatibility"},
			map[string]interface{}{"type": "content", "title": "Compact content", "bullets": []string{"One", "Two", "Three", "Four"}},
			map[string]interface{}{
				"type": "content", "title": "Editable evidence",
				"blocks": []interface{}{
					map[string]interface{}{"type": "kpi", "items": []interface{}{map[string]interface{}{"value": "42", "label": "Grounded"}}},
					map[string]interface{}{"type": "chart", "data": map[string]interface{}{"categories": []string{"A", "B"}, "series": []interface{}{
						map[string]interface{}{"name": "Primary", "values": []int{1, 2}},
						map[string]interface{}{"name": "Risk", "values": []int{2, 3}},
						map[string]interface{}{"name": "Attention", "values": []int{3, 4}},
						map[string]interface{}{"name": "Positive", "values": []int{4, 5}},
					}}, "native_editable": true},
				},
			},
			map[string]interface{}{
				"type": "content", "title": "Aspect preserved",
				"blocks": []interface{}{map[string]interface{}{"type": "image", "path": wideImage, "span": "full"}},
			},
			map[string]interface{}{
				"type": "content", "title": "Compatible interpretation",
				"blocks": []interface{}{
					map[string]interface{}{"type": "text", "content": "A legacy content field is normalized to body."},
				},
			},
			map[string]interface{}{"type": "ending", "title": "Next action"},
		},
	}
	writeJSONFixture(t, validInput, valid)
	cmd := exec.Command(python, script, validInput, validOutput)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("valid base-ppt workflow failed: %v\n%s", err, output)
	}
	info, err := os.Stat(validOutput)
	if err != nil {
		t.Fatalf("valid PPTX missing: %v", err)
	}
	if info.Size() < 10*1024 {
		t.Fatalf("valid PPTX unexpectedly small: %d", info.Size())
	}
	if err := validatePPTPolishRequestedTheme("Use a Google-style presentation.", validOutput); err != nil {
		t.Fatalf("generated Google-style PPTX failed host theme validation: %v", err)
	}
	verify := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.enum.shapes import MSO_SHAPE_TYPE",
		"p=Presentation(r'" + strings.ReplaceAll(validOutput, "'", "''") + "')",
		"pictures=[sh for sl in p.slides for sh in sl.shapes if sh.shape_type == MSO_SHAPE_TYPE.PICTURE]",
		"assert pictures and abs((pictures[0].width / pictures[0].height) - 4.0) < 0.05, [(sh.width, sh.height) for sh in pictures]",
		"charts=[sh.chart for sl in p.slides for sh in sl.shapes if getattr(sh, 'has_chart', False)]",
		"assert charts, 'native chart missing'",
		"colors=[str(sr.format.fill.fore_color.rgb) for sr in charts[0].series]",
		"assert colors == ['4285F4', 'EA4335', 'FBBC04', '34A853'], colors",
	}, ";"))
	verify.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("google-style rendering contract failed: %v\n%s", err, output)
	}

	invalidInput := filepath.Join(dir, "invalid-diagram.json")
	invalidOutput := filepath.Join(dir, "invalid-diagram.pptx")
	invalid := map[string]interface{}{
		"meta": map[string]interface{}{"theme": "mckinsey"},
		"slides": []interface{}{
			map[string]interface{}{
				"type": "content", "title": "Architecture",
				"blocks": []interface{}{map[string]interface{}{"type": "chart", "chart_type": "flowchart", "data": map[string]interface{}{"steps": []string{"A", "B"}}}},
			},
		},
	}
	writeJSONFixture(t, invalidInput, invalid)
	cmd = exec.Command(python, script, invalidInput, invalidOutput)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported diagram unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "task-specific python-pptx/PptxGenJS renderer") {
		t.Fatalf("unsupported diagram error lacks route guidance: %s", output)
	}
	if _, statErr := os.Stat(invalidOutput); !os.IsNotExist(statErr) {
		t.Fatalf("failed diagram workflow must not leave a PPTX, stat err=%v", statErr)
	}
}

func TestBasePPTCustomHelperExampleRunsFromPortableTaskDirectory(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, PIL").CombinedOutput(); err != nil {
		t.Skipf("base-ppt Python dependencies unavailable: %v: %s", err, output)
	}

	taskDir := t.TempDir()
	assets := map[string]string{
		"pptx_helpers.py": filepath.Join("builtin", "presentation", "base-ppt", "pptx_helpers.py"),
		"build_ppt.py":    filepath.Join("builtin", "presentation", "base-ppt", "examples", "custom_deck.py"),
	}
	for targetName, sourcePath := range assets {
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read portable base-ppt asset %s: %v", sourcePath, err)
		}
		if err := os.WriteFile(filepath.Join(taskDir, targetName), data, 0o600); err != nil {
			t.Fatalf("copy portable base-ppt asset %s: %v", targetName, err)
		}
	}

	outputPath := filepath.Join(taskDir, "portable-custom-deck.pptx")
	cmd := exec.Command(python, filepath.Join(taskDir, "build_ppt.py"), "--output", outputPath)
	cmd.Dir = taskDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("portable custom renderer example failed outside the skill directory: %v\n%s", err, output)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("portable custom deck missing: %v", err)
	}
	if info.Size() < 20*1024 {
		t.Fatalf("portable custom deck unexpectedly small: %d", info.Size())
	}

	verifyScript := strings.Join([]string{
		"from pptx import Presentation",
		"p=Presentation(r'" + strings.ReplaceAll(outputPath, "'", "''") + "')",
		"assert len(p.slides)==6, len(p.slides)",
		"assert any(getattr(sh, 'has_chart', False) for sl in p.slides for sh in sl.shapes), 'editable chart missing'",
		"text=' '.join(sh.text for sl in p.slides for sh in sl.shapes if getattr(sh, 'has_text_frame', False))",
		"assert '四个里程碑' in text and '价值与成熟度' in text, text",
		"sw,sh=p.slide_width,p.slide_height",
		"outside=[(i,s.name) for i,sl in enumerate(p.slides,1) for s in sl.shapes if s.left<0 or s.top<0 or s.left+s.width>sw or s.top+s.height>sh]",
		"assert not outside, outside",
	}, ";")
	verify := exec.Command(python, "-c", verifyScript)
	verify.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("portable custom renderer contract failed: %v\n%s", err, output)
	}
}

func TestBasePPTStrictTemplateModeInspectsAndPreservesNativeContract(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx, matplotlib").CombinedOutput(); err != nil {
		t.Skipf("base-ppt Python dependencies unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	template := filepath.Join(dir, "brand-template.pptx")
	createTemplate := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation()",
		"p.slide_width=Inches(10)",
		"p.slide_height=Inches(7.5)",
		"s=p.slides.add_slide(p.slide_layouts[0])",
		"s.shapes.title.text='EXEMPLAR SHOULD NOT SURVIVE'",
		"p.save(r'" + strings.ReplaceAll(template, "'", "''") + "')",
	}, ";"))
	if output, err := createTemplate.CombinedOutput(); err != nil {
		t.Fatalf("create native template fixture: %v\n%s", err, output)
	}

	manifestPath := filepath.Join(dir, "template-manifest.json")
	inspector := filepath.Join("builtin", "presentation", "base-ppt", "scripts", "inspect_pptx_template.py")
	inspect := exec.Command(python, inspector, template, "--output", manifestPath)
	inspect.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := inspect.CombinedOutput(); err != nil {
		t.Fatalf("inspect native template: %v\n%s", err, output)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SourceSlideCount   int                       `json:"source_slide_count"`
		Layouts            []map[string]any          `json:"layouts"`
		RecommendedLayouts map[string]map[string]any `json:"recommended_layouts"`
		SlideSize          struct {
			WidthEMU  int64 `json:"width_emu"`
			HeightEMU int64 `json:"height_emu"`
		} `json:"slide_size"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode template manifest: %v", err)
	}
	if manifest.SourceSlideCount != 1 || len(manifest.Layouts) == 0 || len(manifest.RecommendedLayouts) != 4 {
		t.Fatalf("unexpected template manifest: slides=%d layouts=%d recommendations=%d", manifest.SourceSlideCount, len(manifest.Layouts), len(manifest.RecommendedLayouts))
	}

	workflowPath := filepath.Join(dir, "strict.json")
	outputPath := filepath.Join(dir, "strict.pptx")
	workflow := map[string]interface{}{
		"meta": map[string]interface{}{"theme": "charcoal"},
		"slides": []interface{}{
			map[string]interface{}{"type": "title", "title": "Native title", "subtitle": "Inherited subtitle"},
			map[string]interface{}{"type": "content", "title": "Native content", "bullets": []string{"First", "Second"}},
		},
	}
	writeJSONFixture(t, workflowPath, workflow)
	generator := filepath.Join("builtin", "presentation", "base-ppt", "generate.py")
	generate := exec.Command(python, generator, workflowPath, outputPath, "--template", template)
	generate.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("strict native template generation failed: %v\n%s", err, output)
	}
	verify := exec.Command(python, "-c", strings.Join([]string{
		"from pptx import Presentation",
		"from pptx.util import Inches",
		"p=Presentation(r'" + strings.ReplaceAll(outputPath, "'", "''") + "')",
		"assert len(p.slides)==2, len(p.slides)",
		"assert int(p.slide_width)==int(Inches(10)), p.slide_width",
		"assert int(p.slide_height)==int(Inches(7.5)), p.slide_height",
		"text=' '.join(sh.text for sl in p.slides for sh in sl.shapes if getattr(sh, 'has_text_frame', False))",
		"assert 'EXEMPLAR SHOULD NOT SURVIVE' not in text, text",
		"assert 'Native title' in text and 'Native content' in text and 'First' in text, text",
	}, ";"))
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("strict native template contract failed: %v\n%s", err, output)
	}

	badWorkflowPath := filepath.Join(dir, "strict-rich.json")
	badOutputPath := filepath.Join(dir, "strict-rich.pptx")
	badWorkflow := map[string]interface{}{
		"meta": map[string]interface{}{"theme": "charcoal"},
		"slides": []interface{}{
			map[string]interface{}{
				"type": "content", "title": "Unmapped KPI",
				"blocks": []interface{}{map[string]interface{}{"type": "kpi", "metrics": []interface{}{map[string]interface{}{"value": "42", "label": "Metric"}}}},
			},
		},
	}
	writeJSONFixture(t, badWorkflowPath, badWorkflow)
	bad := exec.Command(python, generator, badWorkflowPath, badOutputPath, "--template", template)
	bad.Env = append(os.Environ(), "PYTHONUTF8=1")
	badOutput, badErr := bad.CombinedOutput()
	if badErr == nil || !strings.Contains(string(badOutput), "strict template mode cannot place block type") {
		t.Fatalf("unmapped rich block should fail strict template mode: err=%v\n%s", badErr, badOutput)
	}
	if _, statErr := os.Stat(badOutputPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed strict template generation must not leave a PPTX, stat err=%v", statErr)
	}
}

func TestGenericPPTSkillRankingPrefersArtDirectorButExplicitPPTMasterWins(t *testing.T) {
	loader := skilldomain.NewSkillsLoader(".", "", "")
	skill := func(name, description string) *skilldomain.Skill {
		return &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: name, Description: description}, Content: description}
	}

	artGeneric, _ := loader.CalculateSkillMatchScore(skill("ppt-art-director", "PPT presentation routing"), []string{"ppt", "presentation"})
	baseGeneric, _ := loader.CalculateSkillMatchScore(skill("base-ppt", "editable PPT presentation"), []string{"ppt", "presentation"})
	masterGeneric, _ := loader.CalculateSkillMatchScore(skill("ppt-master", "PPTmaster upstream presentation"), []string{"ppt", "presentation"})
	if artGeneric <= baseGeneric || artGeneric <= masterGeneric {
		t.Fatalf("generic PPT ranking should prefer art director: art=%d base=%d master=%d", artGeneric, baseGeneric, masterGeneric)
	}

	masterExplicit, _ := loader.CalculateSkillMatchScore(skill("ppt-master", "PPTmaster upstream presentation"), []string{"pptmaster"})
	baseExplicit, _ := loader.CalculateSkillMatchScore(skill("base-ppt", "editable PPT presentation"), []string{"pptmaster"})
	if masterExplicit <= baseExplicit {
		t.Fatalf("explicit PPTmaster ranking should prefer upstream: master=%d base=%d", masterExplicit, baseExplicit)
	}
}

func writeJSONFixture(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
