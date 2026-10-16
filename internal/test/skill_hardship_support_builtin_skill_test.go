package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"

	"gopkg.in/yaml.v3"
)

func TestHardshipSupportIsSkillOnly(t *testing.T) {
	for _, employee := range defaultAIEmployees() {
		if employee.ID == "hardship-support" {
			t.Fatalf("hardship-support must remain a Skill, not a default AI employee: %#v", employee)
		}
	}
}

func TestEmbeddedHardshipSupportIsIndexedRoutableAndComprehensive(t *testing.T) {
	const root = "builtin/hardship-support"
	for _, relative := range []string{
		"SKILL.md",
		"agents/openai.yaml",
		"references/debt.md",
		"references/finance-tax-loan.md",
		"references/assistance-mediation.md",
		"references/final-review.md",
		"scripts/cashflow_check.py",
		"scripts/review_answer.py",
	} {
		if _, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded hardship-support missing %s: %v", relative, err)
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
	category, ok := index["hardship-assistance"]
	if !ok || !containsString(category.Skills, "hardship-support") {
		t.Fatalf("hardship-assistance index missing hardship-support: %#v", category)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("hardship-support")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || !skill.RequiresRuntime {
		t.Fatalf("hardship-support should embed and materialize its references and arithmetic helper: %#v", skill)
	}
	if skill.Metadata.Version != "1.3.3" || skill.Metadata.Author != "OpenKitty" {
		t.Fatalf("unexpected hardship skill metadata: %#v", skill.Metadata)
	}
	if skill.Interface.DisplayName != "困难帮扶专员" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$hardship-support") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("hardship specialist interface metadata was not loaded: %#v", skill.Interface)
	}
	aliasSkill, err := loader.GetSkill("困难帮扶")
	if err != nil || aliasSkill != skill {
		t.Fatalf("Chinese alias should resolve to canonical skill: skill=%p alias=%p err=%v", skill, aliasSkill, err)
	}
	activation, err := loader.ExecuteSkill(context.Background(), "hardship-support", nil)
	if err != nil || strings.TrimSpace(activation) == "" || !strings.Contains(activation, "运行时目录") {
		t.Fatalf("hardship skill should activate: err=%v output=%q", err, activation)
	}
	runtimeDir := skilldomain.RuntimeDirectoryFromActivation(activation)
	if runtimeDir == "" {
		t.Fatalf("hardship activation omitted its runtime bundle: %q", activation)
	}
	for _, relative := range []string{
		"references/debt.md",
		"references/finance-tax-loan.md",
		"references/assistance-mediation.md",
		"references/final-review.md",
		"scripts/cashflow_check.py",
		"scripts/review_answer.py",
	} {
		if _, err := os.ReadFile(filepath.Join(runtimeDir, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("hardship runtime missing %s: %v", relative, err)
		}
	}

	fullInstructions := skill.Raw
	for _, relative := range []string{
		"references/debt.md",
		"references/finance-tax-loan.md",
		"references/assistance-mediation.md",
		"references/final-review.md",
	} {
		data, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative)
		if err != nil {
			t.Fatal(err)
		}
		fullInstructions += "\n" + string(data)
	}
	for _, required := range []string{
		"债务、现金流与协商", "现金流与金额复核", "催收、付款与诈骗风险", "家庭财务、税务与正规贷款", "正规贷款申请",
		"生活困难与救助导航", "邻里、物业和社区矛盾", "一般人际关系紧张", "高风险情况",
		"legal-assistant", "relationship-mentor", "med-guid", "不自动脱敏", "不发问卷",
		"每笔收入只计算一次", "扣除已确定固定还款后的余额", "热线", "还债前可用额",
		"纸质文件", "电子送达", "次级债务", "每个用户点名对象",
		"实际年化成本", "生活救助", "中立协调",
		"review_answer.py", "review_status", "Skill 内复核工作流", "不是宿主工具、AI 员工 Pipeline",
	} {
		if !strings.Contains(fullInstructions, required) {
			t.Fatalf("hardship support instructions missing %q", required)
		}
	}
	for _, forbidden := range []string{"保证债务一定清零", "保证贷款一定通过", "可以伪造困难证明", "自动扫描整个工作区"} {
		if strings.Contains(fullInstructions, forbidden) {
			t.Fatalf("hardship support contains rigid or unsafe rule %q", forbidden)
		}
	}

	queries := []string{
		"我欠了信用卡网贷和亲友的钱，现在收入下降，怎么排还款和协商顺序",
		"第一次申请正规经营贷款，需要什么流程、材料和费用",
		"这个季度税款逾期了，帮我梳理补救步骤和要准备的资料",
		"家里突然没有收入，想查本地临时救助应该去哪里办",
		"楼上长期漏水又不沟通，我应该先找物业还是申请调解",
		"和同事关系很紧张，事情总说不清楚，帮我写一段沟通话术",
	}
	for _, query := range queries {
		top := loader.GetTopSkillsForQuery(query, 8)
		if !containsString(top, "hardship-support") {
			t.Fatalf("hardship query %q did not discover hardship-support: %v", query, top)
		}
	}
	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "hardship-support") {
		t.Fatalf("builtin index prompt omitted hardship-support: %s", prompt)
	}

	uiData, err := fs.ReadFile(builtinSkillsFS, root+"/agents/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ui struct {
		Interface struct {
			DisplayName      string `yaml:"display_name"`
			ShortDescription string `yaml:"short_description"`
			DefaultPrompt    string `yaml:"default_prompt"`
		} `yaml:"interface"`
	}
	if err := yaml.Unmarshal(uiData, &ui); err != nil {
		t.Fatal(err)
	}
	if ui.Interface.DisplayName != "困难帮扶专员" ||
		!strings.Contains(ui.Interface.DefaultPrompt, "$hardship-support") ||
		strings.TrimSpace(ui.Interface.ShortDescription) == "" {
		t.Fatalf("invalid hardship specialist UI metadata: %#v", ui.Interface)
	}
}
