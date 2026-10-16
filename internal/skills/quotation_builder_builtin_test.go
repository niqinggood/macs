package skills

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rootassets "mac"
)

func TestEmbeddedQuotationBuilderIsIndexedRoutableAndComplete(t *testing.T) {
	const root = "builtin/business-finance/quotation-builder"
	files := []string{
		"SKILL.md",
		"agents/openai.yaml",
		"references/technical-scope.md",
		"references/estimation-pricing.md",
		"references/deliverable-template.md",
		"references/disclosure-policy.md",
		"references/sources.md",
		"scripts/calculate_quote.py",
		"scripts/review_client_quote.py",
		"tests/test_calculate_quote.py",
		"tests/test_review_client_quote.py",
	}
	for _, relative := range files {
		if _, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded quotation-builder missing %s: %v", relative, err)
		}
	}

	indexData, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Desc   string   `json:"desc"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatal(err)
	}
	category, ok := index["business"]
	if !ok || !containsString(category.Skills, "quotation-builder") {
		t.Fatalf("business index missing quotation-builder: %#v", category)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("quotation-builder")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || !skill.RequiresRuntime {
		t.Fatalf("quotation-builder should embed and materialize references and calculator: %#v", skill)
	}
	if skill.Metadata.Version != "2.1.0" || skill.Metadata.Author != "OpenKitty" {
		t.Fatalf("unexpected quotation-builder metadata: %#v", skill.Metadata)
	}
	if skill.Interface.DisplayName != "技术项目报价助手" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$quotation-builder") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("quotation-builder interface metadata was not loaded: %#v", skill.Interface)
	}
	aliasSkill, err := loader.GetSkill("技术报价")
	if err != nil || aliasSkill != skill {
		t.Fatalf("Chinese alias should resolve to quotation-builder: skill=%p alias=%p err=%v", skill, aliasSkill, err)
	}

	activation, err := loader.ExecuteSkill(context.Background(), "quotation-builder", nil)
	if err != nil || strings.TrimSpace(activation) == "" || !strings.Contains(activation, "运行时目录") {
		t.Fatalf("quotation-builder should activate with runtime: err=%v output=%q", err, activation)
	}
	runtimeDir := RuntimeDirectoryFromActivation(activation)
	if runtimeDir == "" {
		t.Fatalf("quotation-builder activation omitted runtime directory: %q", activation)
	}
	for _, relative := range files[2:] {
		if _, err := os.ReadFile(filepath.Join(runtimeDir, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("quotation-builder runtime missing %s: %v", relative, err)
		}
	}

	fullInstructions := skill.Raw
	for _, relative := range files[2:7] {
		data, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+relative)
		if err != nil {
			t.Fatal(err)
		}
		fullInstructions += "\n" + string(data)
	}
	for _, required := range []string{
		"小程序", "H5", "管理后台", "并发用户", "QPS", "高可用", "算法框架", "MLOps",
		"WBS", "PERT", "COSMIC", "COCOMO II", "P80", "P95", "目标毛利率", "加成率",
		"一次性", "周期性", "按量", "变更控制", "验收", "calculate_quote.py",
		"不编市场单价", "不重复加风险", "客户价格", "客户责任",
		"最小必要披露", "付费方案", "禁止外发", "review_client_quote.py",
	} {
		if !strings.Contains(fullInstructions, required) {
			t.Fatalf("quotation-builder instructions missing %q", required)
		}
	}

	queries := []string{
		"做一个微信小程序、H5 和运营管理后台，帮我拆功能算人天并出开发报价",
		"这个 API 系统峰值要到 5000 QPS 和高可用，怎么估工期和预算",
		"训练自有算法模型、部署推理服务并建设 MLOps，需要多少人日和费用",
		"请把这份 PRD 做成可发客户的软件项目报价单，含税、付款节点和排除项",
	}
	for _, query := range queries {
		top := loader.GetTopSkillsForQuery(query, 10)
		if !containsString(top, "quotation-builder") {
			t.Fatalf("quotation query %q did not discover quotation-builder: %v", query, top)
		}
	}
	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "quotation-builder") {
		t.Fatalf("builtin index prompt omitted quotation-builder: %s", prompt)
	}
}
