package app

import (
	"strings"
	"testing"

	"mac/internal/toolcontract"
)

func TestUseSkillToolPublishesExactTopLevelArgumentContract(t *testing.T) {
	tool := &UseSkillTool{}
	description := tool.Description()
	for _, want := range []string{"skill_name", "顶层", "parameters", "不得"} {
		if !strings.Contains(description, want) {
			t.Fatalf("use_skill description missing %q: %s", want, description)
		}
	}

	schema := tool.Parameters()
	required := toolcontract.SchemaStringList(schema["required"])
	if len(required) != 1 || required[0] != "skill_name" {
		t.Fatalf("use_skill required fields = %v, want [skill_name]", required)
	}
	properties, _ := schema["properties"].(map[string]interface{})
	if _, ok := properties["skill_name"]; !ok {
		t.Fatal("use_skill schema does not advertise skill_name")
	}
	if _, ok := properties["parameters"]; !ok {
		t.Fatal("use_skill schema does not advertise parameters")
	}
}
