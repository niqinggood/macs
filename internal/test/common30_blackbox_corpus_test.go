package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCommon30BlackboxCorpusIsComplexAndMachineCheckable(t *testing.T) {
	data, err := os.ReadFile("testdata/blackbox/common_30_complex_tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SchemaVersion int `json:"schema_version"`
		Defaults      struct {
			Destructive bool `json:"destructive"`
		} `json:"defaults"`
		Tasks []struct {
			ID             string   `json:"id"`
			Prompt         string   `json:"prompt"`
			Runner         string   `json:"runner"`
			RepoFixtures   []string `json:"repo_fixtures"`
			ComplexityAxes []string `json:"complexity_axes"`
			Acceptance     struct {
				Checks []map[string]interface{} `json:"checks"`
			} `json:"acceptance"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("invalid common-30 corpus JSON: %v", err)
	}
	if corpus.SchemaVersion != 1 {
		t.Fatalf("schema_version=%d, want 1", corpus.SchemaVersion)
	}
	if corpus.Defaults.Destructive {
		t.Fatal("common-30 must be non-destructive by default")
	}
	if len(corpus.Tasks) != 30 {
		t.Fatalf("task count=%d, want exactly 30", len(corpus.Tasks))
	}
	seen := make(map[string]bool, len(corpus.Tasks))
	special := map[string]string{"C06": "process_config_recovery", "A26": "process_restart_resume", "W29": "browser_attachments"}
	for _, task := range corpus.Tasks {
		if task.ID == "" || seen[task.ID] {
			t.Fatalf("missing or duplicate task ID %q", task.ID)
		}
		seen[task.ID] = true
		if utf8.RuneCountInString(task.Prompt) < 80 {
			t.Errorf("%s prompt is too shallow (%d runes)", task.ID, utf8.RuneCountInString(task.Prompt))
		}
		if len(task.ComplexityAxes) < 3 {
			t.Errorf("%s covers only %d complexity axes", task.ID, len(task.ComplexityAxes))
		}
		if len(task.Acceptance.Checks) < 3 {
			t.Errorf("%s has only %d machine checks", task.ID, len(task.Acceptance.Checks))
		}
		for _, path := range task.RepoFixtures {
			if info, err := os.Stat(path); err != nil || info.IsDir() {
				t.Errorf("%s repo fixture %q is unavailable", task.ID, path)
			}
		}
		if want, ok := special[task.ID]; ok && task.Runner != want {
			t.Errorf("%s runner=%q, want dedicated %q", task.ID, task.Runner, want)
		}
	}
	for id := range special {
		if !seen[id] {
			t.Errorf("missing special black-box scenario %s", id)
		}
	}
}

func TestCommon30HarnessNeverCountsUnsupportedSpecialRunnerAsPass(t *testing.T) {
	data, err := os.ReadFile("testdata/blackbox/run_common30.py")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, marker := range []string{
		`runner not in {"chat", "multi_turn"}`,
		`"outcome": "skipped_special"`,
		`"requires dedicated process/browser driver"`,
		`--allow-skipped-special`,
		`return outcomes_exit_code(outcomes, args.allow_skipped_special)`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("common-30 runner is missing special-runner safeguard %q", marker)
		}
	}
}

func TestCommon30HarnessRequiresExactGatewayWorkspace(t *testing.T) {
	data, err := os.ReadFile("testdata/blackbox/run_common30.py")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, marker := range []string{
		`def gateway_health(base_url: str, expected_workspace: pathlib.Path)`,
		`project.get("workspace")`,
		`gateway workspace mismatch`,
		`gateway_health(args.base_url, args.workspace)`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("common-30 runner is missing workspace safeguard %q", marker)
		}
	}
}
