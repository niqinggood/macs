package app

import (
	"io/fs"
	"strings"
	"testing"
)

func TestLegalAssistantDoesNotReplaceExistingLegalEmployee(t *testing.T) {
	for _, employee := range defaultAIEmployees() {
		if employee.ID != "legal-review" {
			continue
		}
		if employee.Role != "合同审查与法律风险合规" || employee.Direct != nil || strings.TrimSpace(employee.SystemMessage) != "" {
			t.Fatalf("legal-assistant Skill must not rewrite the existing legal employee: %#v", employee)
		}
		return
	}
	t.Fatal("missing pre-existing legal-review employee")
}

func TestEmbeddedLegalAssistantHasProfessionalSourceContract(t *testing.T) {
	const root = "builtin/legal/legal-assistant"
	for _, relative := range []string{
		"SKILL.md",
		"agents/openai.yaml",
		"references/authoritative-sources.md",
		"references/professional-workflows.md",
	} {
		if _, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded legal-assistant missing %s: %v", relative, err)
		}
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("legal-assistant")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || skill.RequiresRuntime {
		t.Fatalf("legal-assistant should be an embedded documentation-only skill: %#v", skill)
	}
	if skill.Interface.DisplayName != "专业法律顾问" ||
		!strings.Contains(skill.Interface.DefaultPrompt, "$legal-assistant") ||
		strings.TrimSpace(skill.Interface.ShortDescription) == "" {
		t.Fatalf("legal-assistant interface metadata was not loaded: %#v", skill.Interface)
	}
	for _, required := range []string{
		"references/authoritative-sources.md",
		"references/professional-workflows.md",
		"不自动删改、掩码或脱敏",
		"交付前证据门禁",
		"数据库首页、栏目页",
		"不得猜测、拼接或递增",
		"失败、空白或错误页面不得再进入",
		"法律语义与成稿校验",
		"当前效力状态未单独核验",
		"未实际打开",
		"不把“不予支持”改写成“条款无效”",
		"恶意/非恶意",
		"唯一入口",
		"改换措辞",
		"英文思考",
		"web_search",
		"web_fetch",
	} {
		if !strings.Contains(skill.Raw, required) {
			t.Fatalf("legal-assistant instructions missing %q", required)
		}
	}
	for _, query := range []string{
		"请检索现行中国公司法并说明效力状态和官方来源",
		"请分析劳动争议的法律依据、证据缺口和程序期限",
		"请把个人信息保护要求映射成企业合规整改清单",
	} {
		top := loader.GetTopSkillsForQuery(query, 8)
		if !containsString(top, "legal-assistant") {
			t.Fatalf("legal query %q did not discover legal-assistant: %v", query, top)
		}
	}

	sources, err := fs.ReadFile(builtinSkillsFS, root+"/references/authoritative-sources.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{
		"flk.npc.gov.cn",
		"xzfg.moj.gov.cn",
		"court.gov.cn",
		"rmfyalk.court.gov.cn",
		"samr.gov.cn",
		"cac.gov.cn",
		"cnipa.gov.cn",
		"chinatax.gov.cn",
		"wb.flk.npc.gov.cn/flfg/PDF/bd53dd912c1048f2aecbaa229238334b.pdf",
		"court.gov.cn/zixun/xiangqing/419382.html",
		"gongbao.court.gov.cn/Details/f4722cf61c92a585f04b2ecd334f5b.html",
	} {
		if !strings.Contains(string(sources), domain) {
			t.Fatalf("authoritative source map missing %q", domain)
		}
	}
}
