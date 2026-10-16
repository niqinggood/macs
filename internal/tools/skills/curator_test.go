package skills

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSkillAutoPruneRequiresOldVerifiedUse(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		metric *SkillMetric
		want   bool
	}{
		{name: "newly discovered skill with zero last-used is protected", metric: &SkillMetric{Score: 0, CreatedAt: now}},
		{name: "load without execution is protected", metric: &SkillMetric{Score: 5, CreatedAt: now.Add(-90 * 24 * time.Hour), LastLoaded: now.Add(-70 * 24 * time.Hour)}},
		{name: "recent load protects an old poorly performing skill", metric: &SkillMetric{Score: 5, UseCount: 2, FailCount: 2, CreatedAt: now.Add(-120 * 24 * time.Hour), LastUsed: now.Add(-90 * 24 * time.Hour), LastLoaded: now.Add(-time.Hour)}},
		{name: "old verified low score skill is eligible", metric: &SkillMetric{Score: 5, UseCount: 2, FailCount: 2, CreatedAt: now.Add(-120 * 24 * time.Hour), LastUsed: now.Add(-90 * 24 * time.Hour), LastLoaded: now.Add(-80 * 24 * time.Hour)}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EligibleForSkillAutoPrune(tc.metric, now); got != tc.want {
				t.Fatalf("EligibleForSkillAutoPrune() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCurateSkillsAutoPruneDoesNotArchiveUnobservedSkill(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"new-skill", "old-failing-skill"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewCurateSkillsTool(dir)
	now := time.Now()
	tool.saveMetrics(&SkillMetricsDB{Version: 1, Skills: map[string]*SkillMetric{
		"new-skill":         {Name: "new-skill", CreatedAt: now},
		"old-failing-skill": {Name: "old-failing-skill", UseCount: 1, FailCount: 1, CreatedAt: now.Add(-120 * 24 * time.Hour), LastUsed: now.Add(-90 * 24 * time.Hour)},
	}})

	if _, err := tool.Execute(context.Background(), map[string]interface{}{"action": "auto_prune"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new-skill", "SKILL.md")); err != nil {
		t.Fatalf("unobserved skill was archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".archived", "old-failing-skill", "SKILL.md")); err != nil {
		t.Fatalf("eligible old failing skill was not archived: %v", err)
	}
}

func TestRecordSkillUseCountsFailedAttemptAndIsConcurrencySafe(t *testing.T) {
	metricsPath := filepath.Join(t.TempDir(), ".skill_metrics.json")
	const attempts = 24
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(success bool) { defer wg.Done(); RecordSkillUse(metricsPath, "measured-skill", success) }(i%3 != 0)
	}
	wg.Wait()

	db := (&CurateSkillsTool{metricsPath: metricsPath}).loadMetrics()
	metric := db.Skills["measured-skill"]
	if metric == nil {
		t.Fatal("metric was not persisted")
	}
	if metric.UseCount != attempts {
		t.Fatalf("UseCount = %d, want %d", metric.UseCount, attempts)
	}
	if metric.FailCount != attempts/3 {
		t.Fatalf("FailCount = %d, want %d", metric.FailCount, attempts/3)
	}
}
