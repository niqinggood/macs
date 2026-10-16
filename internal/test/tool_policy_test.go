package app

import (
	"context"
	"testing"
)

type toolPolicyMockTool struct {
	name string
}

func (t toolPolicyMockTool) Name() string { return t.name }
func (t toolPolicyMockTool) Description() string {
	return "mock tool"
}
func (t toolPolicyMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t toolPolicyMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestApplyToolPolicy(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(toolPolicyMockTool{name: "read_file"})
	reg.Register(toolPolicyMockTool{name: "write_file"})
	reg.Register(toolPolicyMockTool{name: "cli"})
	reg.Register(toolPolicyMockTool{name: "sub_agent"})

	result := ApplyToolPolicy(reg, []string{"read_file", "cli", "write_file"}, []string{"write_file"})

	if _, ok := reg.Get("read_file"); !ok {
		t.Fatal("read_file should be kept")
	}
	if _, ok := reg.Get("cli"); !ok {
		t.Fatal("cli should be kept")
	}
	if _, ok := reg.Get("write_file"); ok {
		t.Fatal("write_file should be removed by deny-list even when allowed")
	}
	if _, ok := reg.Get("sub_agent"); ok {
		t.Fatal("sub_agent should be removed because it is outside the allow-list")
	}
	if len(result.Removed) != 2 {
		t.Fatalf("expected 2 removed tools, got %#v", result.Removed)
	}
}
