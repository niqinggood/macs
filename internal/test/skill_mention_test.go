package app

import (
	"io"
	"log"
	"strings"
	"testing"

	skilldomain "mac/internal/skills"
)

func skillMentionTestLoader() *SkillsLoader {
	loader := NewSkillsLoader(".", "Assistant", "")
	loader.RegisterSkill("guided-report", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{
		Name: "guided-report", Aliases: []string{"report", "报告助手"},
	}})
	loader.RegisterSkill("data-review", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{
		Name: "data-review", Aliases: []string{"review"},
	}})
	loader.RegisterSkill("med-guid", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{Name: "med-guid"}})
	return loader
}

func TestResolveExplicitSkillMentionUsesCanonicalNameAndCleansOnlyMatchedMention(t *testing.T) {
	run, task, matched, err := skilldomain.ResolveExplicitMention(
		"请用 @REPORT：分析 Q2 数据，并通知 @Alice", skillMentionTestLoader(),
	)
	if err != nil || !matched {
		t.Fatalf("resolve mention: matched=%v err=%v", matched, err)
	}
	if run.SkillName != "guided-report" || run.ClientReference != "at_mention" {
		t.Fatalf("run = %#v", run)
	}
	if strings.Contains(strings.ToLower(task), "@report") || !strings.Contains(task, "@Alice") || !strings.Contains(task, "分析 Q2 数据") {
		t.Fatalf("cleaned task = %q", task)
	}
}

func TestResolveExplicitSkillMentionSupportsChineseAliasAndDefaultTask(t *testing.T) {
	run, task, matched, err := skilldomain.ResolveExplicitMention("@报告助手", skillMentionTestLoader())
	if err != nil || !matched || run.SkillName != "guided-report" {
		t.Fatalf("resolve Chinese alias: run=%#v matched=%v err=%v", run, matched, err)
	}
	if strings.TrimSpace(task) == "" {
		t.Fatalf("default task = %q", task)
	}
}

func TestResolveExplicitMedGuidMentionAddsNaturalDialogueDeliveryToOrdinaryChat(t *testing.T) {
	run, task, matched, err := skilldomain.ResolveExplicitMention(
		"@med-guid 最近睡不好，白天没精神，中医怎么看？", skillMentionTestLoader(),
	)
	if err != nil || !matched || run.SkillName != "med-guid" {
		t.Fatalf("resolve med-guid: run=%#v matched=%v err=%v", run, matched, err)
	}
	for _, required := range []string{
		"最近睡不好", "普通健康诊聊", "不要自我介绍", "复述用户原话或数据",
		"220–340 个中文字符", "不要输出“核心判断：”“结论：”", "1 个最相关的中医待辨方向",
		"不超过 3 项", "最多一个问句且只问一件事", "未知设备分数只称设备提示",
	} {
		if !strings.Contains(task, required) {
			t.Fatalf("med-guid natural task missing %q: %s", required, task)
		}
	}
}

func TestResolveExplicitMedGuidMentionPreservesRequestedFormalDelivery(t *testing.T) {
	for _, message := range []string{
		"@med-guid 请给我标准版解读",
		"@med-guid 请整理成报告",
		"@med-guid write a structured report",
	} {
		_, task, matched, err := skilldomain.ResolveExplicitMention(message, skillMentionTestLoader())
		if err != nil || !matched {
			t.Fatalf("resolve formal med-guid %q: matched=%v err=%v", message, matched, err)
		}
		if strings.Contains(task, "本轮交付方式") {
			t.Fatalf("formal med-guid request received natural-chat override: %s", task)
		}
	}

	_, task, matched, err := skilldomain.ResolveExplicitMention(
		"@med-guid 不要报告，简单聊聊睡眠", skillMentionTestLoader(),
	)
	if err != nil || !matched || !strings.Contains(task, "本轮交付方式") {
		t.Fatalf("negated report request must keep natural chat: matched=%v err=%v task=%s", matched, err, task)
	}
}

func TestResolveExplicitSkillMentionLeavesUnknownPartialAndEmailMentionsUntouched(t *testing.T) {
	loader := skillMentionTestLoader()
	for _, message := range []string{
		"@unknown do work",
		"@reporter do work",
		"mail user@report.com about it",
		"identifier foo@report",
	} {
		run, task, matched, err := skilldomain.ResolveExplicitMention(message, loader)
		if err != nil || matched || run != nil || task != message {
			t.Errorf("message %q unexpectedly routed: run=%#v task=%q matched=%v err=%v", message, run, task, matched, err)
		}
	}
}

func TestResolveExplicitSkillMentionRejectsAmbiguousAlias(t *testing.T) {
	loader := skillMentionTestLoader()
	loader.RegisterSkill("second-report", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{
		Name: "second-report", Aliases: []string{"report"},
	}})
	_, _, matched, err := skilldomain.ResolveExplicitMention("@report build it", loader)
	if err == nil || matched || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous alias: matched=%v err=%v", matched, err)
	}
}

func TestSkillMentionAliasConflictsAreDeterministic(t *testing.T) {
	snapshots := skillMentionTestLoader().SnapshotSkills()
	snapshots = append(snapshots, SkillSnapshot{Name: "second-report", Aliases: []string{"report"}})
	conflicts := skilldomain.SkillMentionAliasConflicts(snapshots)
	if got := strings.Join(conflicts["report"], ","); got != "guided-report,second-report" {
		t.Fatalf("conflicts = %q", got)
	}
}

func TestResolveExplicitSkillMentionCanonicalNameWinsAliasCollision(t *testing.T) {
	loader := skillMentionTestLoader()
	loader.RegisterSkill("other", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{
		Name: "other", Aliases: []string{"guided-report"},
	}})
	run, _, matched, err := skilldomain.ResolveExplicitMention("@guided-report build it", loader)
	if err != nil || !matched || run.SkillName != "guided-report" {
		t.Fatalf("canonical collision resolution: run=%#v matched=%v err=%v", run, matched, err)
	}
}

func TestBindSkillMentionRequestCreatesOneTurnSkillRun(t *testing.T) {
	loader := skillMentionTestLoader()
	agent := &LLMAgent{name: "Assistant", skillsLoader: loader}
	factory := &AgentFactory{agents: map[string]Agent{"Assistant": agent}}
	gateway := &GatewayService{engine: &MultiAgentEngine{agentFactory: factory}}
	req := &MessageRequest{TaskType: "chat", Message: "@review inspect this"}

	matched, err := gateway.bindSkillMentionRequest(req)
	if err != nil || !matched {
		t.Fatalf("bind request: matched=%v err=%v", matched, err)
	}
	if req.TaskType != "skill" || req.SkillRun == nil || req.SkillRun.SkillName != "data-review" || req.Message != "inspect this" {
		t.Fatalf("bound request = %#v", req)
	}
}

func TestSkillMentionDoesNotTakeKnownAIEmployeeAlias(t *testing.T) {
	loader := skillMentionTestLoader()
	loader.RegisterSkill("employee-collision", &skilldomain.Skill{Metadata: skilldomain.SkillMetadata{
		Name: "employee-collision", Aliases: []string{"报告员"},
	}})
	gateway := &GatewayService{logger: log.New(io.Discard, "", 0)}
	gateway.aeManager = NewAIEmployeeManager(gateway, log.New(io.Discard, "", 0))
	gateway.aeManager.Register([]AIEmployeeConfig{{
		ID: "report-employee", Name: "报告员", AtName: "报告员", Direct: &DirectConfig{Prompt: "report"},
	}})

	message := "@报告员 做日报"
	employee, employeeTask := gateway.aeManager.MatchAtName(message)
	if employee == nil || employeeTask != "做日报" {
		t.Fatalf("employee precedence setup failed: employee=%#v task=%q", employee, employeeTask)
	}
	// Gateway routing checks the employee before calling the Skill resolver;
	// this assertion documents that the collision itself remains exact and can
	// therefore be reserved deterministically.
	run, _, matched, err := skilldomain.ResolveExplicitMention(message, loader)
	if err != nil || !matched || run.SkillName != "employee-collision" {
		t.Fatalf("Skill collision setup failed: run=%#v matched=%v err=%v", run, matched, err)
	}
}
