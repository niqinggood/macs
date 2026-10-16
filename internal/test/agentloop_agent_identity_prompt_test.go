package app

import (
	"strings"
	"testing"
)

func TestBuildRegularAgentIdentityIncludesConfiguredRoleAndProtectsProductIdentity(t *testing.T) {
	prompt := buildRegularAgentIdentity("Assistant", "只回答当前问题，不输出能力菜单。")
	for _, want := range []string{
		"OpenKitty's Assistant agent",
		"user-facing product identity is OpenKitty",
		"Never introduce yourself as MiniMax",
		"## Configured Role Instructions",
		"只回答当前问题，不输出能力菜单。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("regular agent identity missing %q:\n%s", want, prompt)
		}
	}
}
