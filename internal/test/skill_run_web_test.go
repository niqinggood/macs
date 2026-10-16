package app

import (
	"strings"
	"testing"
)

func TestWebSkillRunComposerBindsOnlyTheNextAcceptedRequest(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	skillJS := mustReadWebTestFile(t, "web/skill_run.js")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, id := range []string{
		`id="skillRunSelection"`, `id="skillRunPopover"`, `id="skillRunSearch"`,
		`id="skillRunList"`, `id="skillRunParameterFields"`, `id="skillRunUseBtn"`,
		`id="skillRunBtn"`,
	} {
		if !strings.Contains(indexHTML, id) {
			t.Fatalf("Skill Run composer markup missing %s", id)
		}
	}
	if skillIndex, chatIndex := strings.Index(indexHTML, "/web/skill_run.js"), strings.Index(indexHTML, "/web/chat.js"); skillIndex < 0 || chatIndex <= skillIndex {
		t.Fatal("web/skill_run.js must load before web/chat.js")
	}
	for _, marker := range []string{
		"fetch('/api/skills'", "window.getSelectedSkillRunRequest", "window.clearSelectedSkillRun",
		"skill_name: configuredSkill.name", "parameters: collectParameters()",
		"skill.display_name", "skill.short_description", "skill.default_prompt",
		"applyDefaultPrompt(configuredSkill)", "input.value === autoFilledPrompt",
	} {
		if !strings.Contains(skillJS, marker) {
			t.Fatalf("Skill Run picker behavior missing %q", marker)
		}
	}
	for _, marker := range []string{
		"task_type: selectedSkillRun ? 'skill' : 'chat'",
		"requestData.skill_run = selectedSkillRun",
		"if (currentMode === 'collab')",
		"if (selectedSkillRun)",
		"Skill 运行只支持单 Agent",
		"(result.task_id || result.result) && selectedSkillRun",
		"window.clearSelectedSkillRun()",
		"loadSkillMentionList",
		"skill.display_name || skill.name",
		"skill.short_description || skill.description",
		"await ensureSkillMentionListReady()",
		"window.messageHasInstalledSkillMention",
		"activeTaskId && (selectedSkillRun || inlineSkillMention)",
		"data-kind=",
		"at-kind-skill",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("Skill Run one-shot send contract missing %q", marker)
		}
	}
	for _, selector := range []string{
		".skill-run-selection", ".skill-run-popover", ".skill-run-item", ".skill-run-use-btn", ".at-kind-skill",
	} {
		if !strings.Contains(styleCSS, selector) {
			t.Fatalf("Skill Run styling missing %s", selector)
		}
	}
	useButton := webCSSRule(t, styleCSS, ".skill-run-use-btn")
	for _, disallowed := range []string{"#6366f1", "#7c3aed", "linear-gradient"} {
		if strings.Contains(useButton, disallowed) {
			t.Fatalf("Skill Run primary action must stay neutral; found %q in %s", disallowed, useButton)
		}
	}
	for _, neutralSkillSelector := range []string{
		`.at-item[data-kind="skill"].active`,
		`.at-item[data-kind="skill"] .at-atname`,
	} {
		if !strings.Contains(styleCSS, neutralSkillSelector) {
			t.Fatalf("@Skill autocomplete must override legacy blue selection styling: missing %s", neutralSkillSelector)
		}
	}
}
