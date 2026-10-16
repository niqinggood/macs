package app

import "testing"

func TestProjectMapIsCoreTool(t *testing.T) {
	if !CoreTools["project_map"] {
		t.Fatal("project_map should be a core tool")
	}
}
