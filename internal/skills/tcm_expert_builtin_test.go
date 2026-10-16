package skills

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	rootassets "mac"

	"gopkg.in/yaml.v3"
)

func TestEmbeddedTCMExpertIsIndexedRoutableAndComprehensive(t *testing.T) {
	const root = "builtin/tcm-expert"
	for _, relative := range []string{
		"SKILL.md",
		"agents/openai.yaml",
		"references/acupressure-and-footcare.md",
		"references/case-library.md",
		"references/clinical-reasoning.md",
		"references/clinical-response-contract.md",
		"references/common-conditions.md",
		"references/evidence-and-sources.md",
		"references/expert-quality-standard.md",
		"references/herb-risk-register.md",
		"references/food-therapy.md",
		"references/formula-differentiation.md",
		"references/graphene-and-thermal-products.md",
		"references/modern-indicators-to-tcm.md",
		"references/lab-clusters-and-red-flags.md",
		"references/professional-competency-core.md",
		"references/tcm-foundations.md",
		"references/treatment-safety.md",
		"references/wellness-care-plans.md",
	} {
		if _, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded tcm-expert missing %s: %v", relative, err)
		}
	}

	wellnessData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/references/wellness-care-plans.md")
	if err != nil {
		t.Fatal(err)
	}
	wellness := string(wellnessData)
	for _, required := range []string{
		"九种体质的保健知识",
		"四季与地域调养",
		"五维保健处方（非药物）",
		"七天起步与十四天复盘",
		"湿气重",
		"容易上火",
		"肾虚",
		"气血不足",
		"八段锦",
		"合成保健案例的讲法",
		"GB/T 46939—2025",
	} {
		if !strings.Contains(wellness, required) {
			t.Fatalf("tcm wellness knowledge missing %q", required)
		}
	}

	indexData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, "builtin/OPENKITTY_INDEX.json")
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
	category, ok := index["health-wellness"]
	if !ok || !containsString(category.Skills, "tcm-expert") {
		t.Fatalf("health-wellness index missing tcm-expert: %#v", category)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("tcm-expert")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || skill.RequiresRuntime {
		t.Fatalf("tcm-expert should be an embedded documentation-only skill: %#v", skill)
	}
	if skill.Metadata.Version != "2.1.0" || skill.Metadata.Author != "OpenKitty" {
		t.Fatalf("unexpected tcm-expert metadata: %#v", skill.Metadata)
	}
	if skill.Interface.DisplayName != "中医保健专家" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$tcm-expert") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("tcm-expert interface metadata was not loaded: %#v", skill.Interface)
	}

	aliasSkill, err := loader.GetSkill("中医专家")
	if err != nil || aliasSkill != skill {
		t.Fatalf("Chinese alias should resolve to tcm-expert: skill=%p alias=%p err=%v", skill, aliasSkill, err)
	}
	activation, err := loader.ExecuteSkill(context.Background(), "tcm-expert", nil)
	if err != nil || strings.TrimSpace(activation) == "" {
		t.Fatalf("tcm-expert should activate: err=%v output=%q", err, activation)
	}

	for _, required := range []string{
		"现代指标到中医候选病机",
		"常见专病与证候鉴别",
		"食疗基础知识",
		"穴位按揉与足部调护",
		"常用方剂鉴别",
		"教学案例库",
		"先分流，后辨证",
		"不生成可直接执行的个体化处方",
		"临床响应契约",
		"患者回答的最高优先级规则",
		"中医保健与治未病方案",
		"未提及不等于阴性",
		"权威性与质量控制标准",
		"固定交付格式",
		"三个高频业务场景",
		"时间先后不能证明因果",
		"GB/T 46939—2025",
		"报告型不超过约 1400 个汉字",
		"不能写成“过一周再看”",
		"最终成稿核对",
		"高频场景发布闸门",
		"不得猜体重变化来自水、脂肪或肌肉",
	} {
		if !strings.Contains(skill.Raw, required) {
			t.Fatalf("tcm-expert instructions missing %q", required)
		}
	}

	for _, required := range []string{
		"专家能力内核",
		"发布硬门槛",
		"证据权重",
		"一天中的发作时段不能单独对应",
		"压力大、反复思考或叹气不能单独定肝郁",
		"专病最小鉴别集",
		"六类高频回答模板",
		"危险足/单舌开方",
		"石墨烯与热疗产品审查",
	} {
		if !strings.Contains(skill.Raw, required) {
			t.Fatalf("tcm-expert professional instructions missing %q", required)
		}
	}

	professionalData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/references/professional-competency-core.md")
	if err != nil {
		t.Fatal(err)
	}
	professional := string(professionalData)
	for _, required := range []string{
		"最小病例数据集",
		"内部证据分级",
		"病机链审查",
		"常见误判校正",
		"专病问题表示",
		"付费级发布标准",
	} {
		if !strings.Contains(professional, required) {
			t.Fatalf("tcm professional competency core missing %q", required)
		}
	}

	qualityData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/references/expert-quality-standard.md")
	if err != nil {
		t.Fatal(err)
	}
	quality := string(qualityData)
	for _, required := range []string{
		"权威回答的五个支柱",
		"问诊资料完整度",
		"置信级而不是确定语气",
		"证据标识",
		"建议强度分层",
		"付费级用户答案结构",
		"发布前质量门",
		"GB/T 46939—2025",
		"中医养生保健服务规范（试行）",
		"不得用“权威报告”",
	} {
		if !strings.Contains(quality, required) {
			t.Fatalf("tcm expert quality standard missing %q", required)
		}
	}

	for _, query := range []string{
		"请做中医体质辨识，并给我一份七天食养起居和八段锦保健计划",
		"最近总说湿气重又怕冷，中医治未病该怎么调养",
		"秋天口干睡不好，想按体质做四季养生和穴位保健",
		"请从中医角度归纳我的血脂血糖和体检指标，再给食疗建议",
		"最近失眠乏力胃口差，中医辨证可能是什么情况",
		"糖尿病足能不能泡脚，哪些穴位可以安全按揉",
		"帮我审查这个中药方的配伍和安全风险",
		"石墨烯发热护膝能不能调理关节和体寒",
	} {
		top := loader.GetTopSkillsForQuery(query, 8)
		if !containsString(top, "tcm-expert") {
			t.Fatalf("TCM query %q did not discover tcm-expert: %v", query, top)
		}
	}
	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "tcm-expert") {
		t.Fatalf("builtin index prompt omitted tcm-expert: %s", prompt)
	}

	uiData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/agents/openai.yaml")
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
	if ui.Interface.DisplayName != "中医保健专家" ||
		!strings.Contains(ui.Interface.DefaultPrompt, "$tcm-expert") ||
		strings.TrimSpace(ui.Interface.ShortDescription) == "" {
		t.Fatalf("invalid tcm-expert UI metadata: %#v", ui.Interface)
	}
}
