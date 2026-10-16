package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	skilltools "mac/internal/tools/skills"
)

func TestExecuteUserSkillRecordsActualUse(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	writeReloadSkill(t, root, "measured-skill", "Records real use", "# Measured\nFollow these instructions.")

	loader := NewSkillsLoader(workspace, "metrics-agent", "")
	if err := loader.LoadAllSkills(root, "user"); err != nil {
		t.Fatalf("load user skill: %v", err)
	}
	if _, err := loader.ExecuteSkill(context.Background(), "measured-skill", nil); err != nil {
		t.Fatalf("execute user skill: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".skill_metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var db skilltools.SkillMetricsDB
	if err := json.Unmarshal(data, &db); err != nil {
		t.Fatal(err)
	}
	metric := db.Skills["measured-skill"]
	if metric == nil || metric.UseCount != 1 || metric.FailCount != 0 || metric.LastUsed.IsZero() {
		t.Fatalf("actual skill use was not recorded: %#v", metric)
	}
}
