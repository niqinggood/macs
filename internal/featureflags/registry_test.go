package featureflags

import "testing"

func TestRegistryLoad(t *testing.T) {
	registry := NewRegistry(true)
	registry.Load(false, []string{"hooks"})
	if registry.IsEnabled(Hooks) || registry.IsEnabled(Commercial) {
		t.Fatal("disabled/community flags remained enabled")
	}
	if !registry.IsEnabled(MCP) {
		t.Fatal("unconfigured opt-out flag became disabled")
	}
	registry.Load(true, nil)
	if !registry.IsEnabled(CommercialDE) {
		t.Fatal("commercial tier flag was not enabled")
	}
}
