package app

import (
	"strings"
	"testing"
)

func TestSystemPromptsKeepResearchSearchModelLed(t *testing.T) {
	prompts := map[string]string{
		"static":        systemPromptStatic(true),
		"agent_static":  (&LLMAgent{}).buildSystemPromptStatic(),
		"legacy_prompt": (&LLMAgent{}).buildSystemPrompt(),
	}

	for name, prompt := range prompts {
		if strings.Contains(prompt, "max 2 web_fetch") {
			t.Fatalf("%s still hard-caps research fetching at two calls", name)
		}
		if strings.Contains(prompt, "max 2 per turn") {
			t.Fatalf("%s still hard-caps slow web tools at two calls", name)
		}
		if !strings.Contains(prompt, "there is no target web-call count") {
			t.Fatalf("%s does not return search stopping judgment to the model", name)
		}
		if !strings.Contains(prompt, "until the evidence adequately supports") {
			t.Fatalf("%s does not define evidence sufficiency as the stopping condition", name)
		}
		if !strings.Contains(prompt, "low-yield result is a reason to change retrieval strategy") {
			t.Fatalf("%s still treats low-yield retrieval as a reason to finalize", name)
		}
	}
}
