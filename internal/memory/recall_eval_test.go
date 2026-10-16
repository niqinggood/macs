package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recallEvalFixture struct {
	Description string `json:"description"`
	Entries     []struct {
		Key      string      `json:"key"`
		Value    string      `json:"value"`
		Scope    MemoryScope `json:"scope"`
		Kind     string      `json:"kind"`
		State    string      `json:"state"`
		Priority int         `json:"priority"`
		Forget   bool        `json:"forget"`
	} `json:"entries"`
	Distractors struct {
		Count    int         `json:"count"`
		Scope    MemoryScope `json:"scope"`
		Priority int         `json:"priority"`
	} `json:"distractors"`
	Summary string `json:"summary"`
	Cases   []struct {
		Name        string   `json:"name"`
		Difficulty  string   `json:"difficulty"`
		Question    string   `json:"question"`
		SessionID   string   `json:"session_id"`
		Expect      string   `json:"expect"`
		Forbid      []string `json:"forbid"`
		ExpectEmpty bool     `json:"expect_empty"`
	} `json:"cases"`
}

// TestRecallEvaluation is a small deterministic quality baseline over the
// actual SQLite-backed recall path. The fixture contains synthetic dialogue
// scenarios so no private transcripts enter the repository.
func TestRecallEvaluation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "recall-eval.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture recallEvalFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Entries) == 0 || len(fixture.Cases) == 0 {
		t.Fatal("recall evaluation fixture is empty")
	}
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "recall-eval.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "eval", PartitionID: "eval-owner", SessionID: "eval-main"}
	for _, entry := range fixture.Entries {
		if err := service.Record(ctx, owner, entry.Key, entry.Value, entry.Scope, entry.Kind, entry.State, entry.Priority, nil, 0); err != nil {
			t.Fatalf("seed %s: %v", entry.Key, err)
		}
		if entry.Forget {
			if forgotten, err := service.Forget(ctx, owner, entry.Key, entry.Scope); err != nil || !forgotten {
				t.Fatalf("forget %s: %v, %v", entry.Key, forgotten, err)
			}
		}
	}
	if fixture.Distractors.Count > 500 {
		t.Fatal("evaluation distractor count is too high")
	}
	for i := 0; i < fixture.Distractors.Count; i++ {
		key := fmt.Sprintf("unrelated_%03d", i)
		if err := service.Record(ctx, owner, key, fmt.Sprintf("unrelated filler item %d", i), fixture.Distractors.Scope, "fact", "active", fixture.Distractors.Priority, nil, 0); err != nil {
			t.Fatalf("seed distractor %d: %v", i, err)
		}
	}
	if fixture.Summary != "" {
		if err := service.SaveSummary(ctx, owner, fixture.Summary); err != nil {
			t.Fatal(err)
		}
	}
	positive, hit1, hit5, leaks := 0, 0, 0, 0
	paraphrase, paraphraseHit5 := 0, 0
	literal, literalHit5 := 0, 0
	maxDistractors := 0
	mrr := 0.0
	for _, testCase := range fixture.Cases {
		caseOwner := owner
		if testCase.SessionID != "" {
			caseOwner.SessionID = testCase.SessionID
		}
		result, err := service.Recall(ctx, caseOwner, testCase.Question, 550, nil)
		if err != nil {
			t.Errorf("%s: recall failed: %v", testCase.Name, err)
			continue
		}
		if testCase.ExpectEmpty && (result.Prompt != "" || len(result.Items) != 0) {
			t.Errorf("%s: unrelated memory injected", testCase.Name)
		}
		distractors := 0
		for _, item := range result.Items {
			if strings.HasPrefix(item.Content, "unrelated_") {
				distractors++
			}
		}
		maxDistractors = max(maxDistractors, distractors)
		for _, forbidden := range testCase.Forbid {
			if strings.Contains(result.Prompt, forbidden) {
				leaks++
				t.Errorf("%s: forbidden content recalled: %q", testCase.Name, forbidden)
			}
		}
		if testCase.Expect == "" {
			continue
		}
		positive++
		if testCase.Difficulty == "paraphrase" {
			paraphrase++
		} else {
			literal++
		}
		rank := 0
		for i, item := range result.Items {
			if strings.Contains(item.Content, testCase.Expect) {
				rank = i + 1
				break
			}
		}
		if rank == 0 {
			t.Logf("MISS %s: expected %q; got %v", testCase.Name, testCase.Expect, recallEvalContents(result.Items))
			continue
		}
		mrr += 1 / float64(rank)
		if rank == 1 {
			hit1++
		}
		if rank <= 5 {
			hit5++
			if testCase.Difficulty == "paraphrase" {
				paraphraseHit5++
			} else {
				literalHit5++
			}
		}
	}
	if positive == 0 {
		t.Fatal("evaluation has no positive recall cases")
	}
	t.Logf("recall baseline: cases=%d positive=%d Hit@1=%d/%d Hit@5=%d/%d MRR=%.3f literal_Hit@5=%d/%d paraphrase_Hit@5=%d/%d forbidden_leaks=%d max_unrelated_project=%d", len(fixture.Cases), positive, hit1, positive, hit5, positive, mrr/float64(positive), literalHit5, literal, paraphraseHit5, paraphrase, leaks, maxDistractors)
	if hit5*4 < positive*3 {
		t.Errorf("Hit@5 below the initial 75%% regression floor: %d/%d", hit5, positive)
	}
	if literalHit5 != literal {
		t.Errorf("literal recall regressed: %d/%d", literalHit5, literal)
	}
	if maxDistractors > 0 {
		t.Errorf("unrelated project recall exceeded cap: %d", maxDistractors)
	}
}

func recallEvalContents(items []RecallItem) []string {
	contents := make([]string, 0, min(6, len(items)))
	for _, item := range items {
		contents = append(contents, item.Content)
		if len(contents) == 6 {
			break
		}
	}
	return contents
}
