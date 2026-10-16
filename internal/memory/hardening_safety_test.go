package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMemoryHardeningConfirmationGrammar(t *testing.T) {
	for _, tc := range []struct {
		turn, key string
		want      bool
	}{
		{"确认记忆 color", "color", true},
		{"请确认记忆：`color_theme`", "color_theme", true},
		{"Please confirm memory: \"Color\"", "Color", true},
		{"确认记忆 color_theme", "color", false},
		{"确认记忆 color，但不要保存", "color", false},
		{"不要确认记忆 color", "color", false},
		{"confirm memorycolor", "color", false},
		{"confirm memory Color", "color", false},
		{"确认记忆 'color", "color", false},
	} {
		t.Run(tc.turn, func(t *testing.T) {
			if got := explicitCandidateConfirmation(WithUserTurn(context.Background(), tc.turn), tc.key); got != tc.want {
				t.Fatalf("confirmation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryHardeningHistoryScopesAndPositiveControl(t *testing.T) {
	s, _ := hardeningService(t)
	store := NewMemoryStore(t.TempDir(), s.db, "assistant", 10)
	for _, e := range []MemoryEvent{
		{TenantID: "audit", AgentName: "assistant", UserName: "alice", SessionID: "shared", UserQuestion: "quartz", FinalAnswer: "quartz allowed", Status: "success"},
		{TenantID: "audit", AgentName: "private", UserName: "alice", SessionID: "shared", UserQuestion: "quartz", FinalAnswer: "quartz foreign-agent", Status: "success"},
		{TenantID: "other", AgentName: "assistant", UserName: "alice", SessionID: "shared", UserQuestion: "quartz", FinalAnswer: "quartz foreign-tenant", Status: "success"},
		{TenantID: "audit", AgentName: "assistant", UserName: "bob", SessionID: "shared", UserQuestion: "quartz", FinalAnswer: "quartz foreign-user", Status: "success"},
	} {
		if err := s.db.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"alice", ""} {
		hits := store.SearchMemoryFTSForTenant("audit", "quartz", user, 10)
		want := 1
		if user == "" {
			want = 2
		} // The legacy unnamed search is partition-scoped.
		if len(hits) != want {
			t.Fatalf("user %q: got %+v", user, hits)
		}
		for _, hit := range hits {
			if strings.Contains(hit.FinalAnswer, "foreign-agent") || strings.Contains(hit.FinalAnswer, "foreign-tenant") {
				t.Fatalf("leaked %+v", hit)
			}
		}
	}
	recent := store.GetMemoryContextForTenant("audit", "alice", "shared", "")
	if !strings.Contains(recent, "quartz allowed") || strings.Contains(recent, "foreign-") {
		t.Fatalf("recent context: %s", recent)
	}
}

func TestMemoryHardeningLearningEventPreservesIdentity(t *testing.T) {
	source := &MemoryEvent{TenantID: "tenant", AgentName: "assistant", AgentType: "team", UserID: "id", UserName: "alice", UserChannel: "api", SessionID: "session", SystemPrompt: "private prompt", OutputPath: "private artifact"}
	event := NewLearningEvent(source, "lesson", "resolved")
	if event.TenantID != source.TenantID || event.AgentName != source.AgentName || event.AgentType != source.AgentType || event.UserID != source.UserID || event.UserName != source.UserName || event.UserChannel != source.UserChannel || event.SessionID != source.SessionID {
		t.Fatalf("identity changed: %+v", event)
	}
	if event.SystemPrompt != "" || event.OutputPath != "" || event.ID != 0 {
		t.Fatal("source content copied to lesson")
	}
	if NewLearningEvent(nil, "", "") != nil {
		t.Fatal("nil source manufactured identity")
	}
}

func TestMemoryHardeningSpecificMatchBeforeCandidateCap(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Record(ctx, owner, "quartz_target", "quartz deployment configuration", MemoryProject, "fact", "active", 1, nil, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if err := s.Record(ctx, owner, fmt.Sprintf("deployment_%d", i), "deployment configuration", MemoryProject, "fact", "active", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.searchRecallEntries(ctx, owner, "quartz deployment")
	if err != nil || len(entries) != 200 || entries[0].Key != "quartz_target" {
		t.Fatalf("specific match lost before cap: count=%d error=%v", len(entries), err)
	}
	result, err := s.Recall(ctx, owner, "quartz deployment", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "quartz_target") {
		t.Fatalf("specific match not recalled: %v %s", err, result.Prompt)
	}
}

func TestMemoryHardeningLiteralKeywordSearch(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	for _, value := range []string{"build_id", "buildXid"} {
		if err := s.Record(ctx, owner, value, value, MemoryProject, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.searchRecallEntries(ctx, owner, "build_id")
	if err != nil || len(entries) != 1 || entries[0].Key != "build_id" {
		t.Fatalf("wildcard expanded: %+v %v", entries, err)
	}
}

func TestMemoryHardeningRetractionWordBoundaries(t *testing.T) {
	filter := retractionFilter(context.Background(), []memoryRetraction{{Length: 2, Digest: retractionDigest("go")}})
	for _, tc := range []struct {
		text    string
		blocked bool
	}{
		{"Go compiler", true}, {"(GO)", true}, {"使用Go编译器", true}, {"G o compiler", true},
		{"Google cloud", false}, {"cargo ship", false}, {"go_tool", false}, {"go2 service", false},
	} {
		if got := filter(tc.text); got != tc.blocked {
			t.Errorf("%q blocked=%v want %v", tc.text, got, tc.blocked)
		}
	}
}

func TestMemoryHardeningSummaryCommitRequiresCurrentLease(t *testing.T) {
	for _, mode := range []string{"expired", "replaced", "different-session", "different-user"} {
		t.Run(mode, func(t *testing.T) {
			s, owner := hardeningService(t)
			ctx := context.Background()
			if err := s.SaveSummary(ctx, owner, "accepted"); err != nil {
				t.Fatal(err)
			}
			if err := s.Enqueue(ctx, owner, "summary", "late"); err != nil {
				t.Fatal(err)
			}
			called := false
			_ = s.ProcessPending(ctx, owner, func(workCtx context.Context, job ProcessingJob) error {
				called = true
				target := owner
				switch mode {
				case "expired":
					if err := s.db.Model(&ProcessingJob{}).Where("id = ?", job.ID).Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
						t.Fatal(err)
					}
				case "replaced":
					if err := s.db.Model(&ProcessingJob{}).Where("id = ?", job.ID).Update("lease_token", "replacement").Error; err != nil {
						t.Fatal(err)
					}
				case "different-session":
					target.SessionID = "other"
				case "different-user":
					target.SubjectID = "bob"
					target.PartitionID = "bob"
				}
				err := s.SaveSummary(workCtx, target, "rejected")
				if !errors.Is(err, ErrMemoryLeaseLost) {
					t.Fatalf("stale or foreign write: %v", err)
				}
				if got, err := s.LoadSummary(ctx, target); err != nil || (mode == "different-session" || mode == "different-user") && got != "" {
					t.Fatalf("foreign summary: %q %v", got, err)
				}
				return err
			})
			if !called {
				t.Fatal("job not claimed")
			}
			if got, err := s.LoadSummary(ctx, owner); err != nil || got != "accepted" {
				t.Fatalf("accepted summary changed: %q %v", got, err)
			}
		})
	}
}

func TestMemoryHardeningSummaryRevisionSurvivesReopenAndDuplicate(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.Enqueue(ctx, owner, "summary", "first"); err != nil {
			t.Fatal(err)
		}
	}
	var first ProcessingJob
	if err := s.db.First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if first.SourceRevision != 1 {
		t.Fatalf("first revision %d", first.SourceRevision)
	}
	// A new Service instance reuses the database counter, not in-memory state.
	reopened := NewService(s.db, s.workspace, "assistant")
	if err := reopened.SaveSummary(ctx, owner, "direct newer snapshot"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Enqueue(ctx, owner, "summary", "next"); err != nil {
		t.Fatal(err)
	}
	var jobs []ProcessingJob
	if err := s.db.Order("source_revision ASC").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[1].SourceRevision != 3 {
		t.Fatalf("counter/idempotence: %+v", jobs)
	}
	if err := s.ProcessPending(ctx, owner, func(workCtx context.Context, job ProcessingJob) error {
		if job.ID == first.ID {
			return s.SaveSummary(workCtx, owner, "stale")
		}
		return s.SaveSummary(workCtx, owner, "latest")
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.LoadSummary(ctx, owner); err != nil || got != "latest" {
		t.Fatalf("summary=%q %v", got, err)
	}
	if err := s.db.First(&first, "id = ?", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.State != "superseded" || first.Payload != "" {
		t.Fatalf("stale job not retired: %+v", first)
	}
}

func TestMemoryHardeningLegacySummaryNullableMetadata(t *testing.T) {
	for _, older := range []bool{true, false} {
		t.Run(fmt.Sprint(older), func(t *testing.T) {
			s, owner := hardeningService(t)
			ctx := context.Background()
			_, namespace, err := s.scoped(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			// Columns added to a pre-migration row can contain NULL source metadata.
			if err := s.db.Exec("INSERT INTO memory_summaries (id,namespace,session_id,content,source_revision,source_job_id,updated_at) VALUES (?,?,?,?,0,NULL,?)", memoryHash(namespace, owner.SessionID), namespace, owner.SessionID, "legacy accepted", now).Error; err != nil {
				t.Fatal(err)
			}
			sourceTime := now.Add(time.Minute)
			if older {
				sourceTime = now.Add(-time.Minute)
			}
			job := ProcessingJob{ID: memoryHash(namespace, "legacy"), Namespace: namespace, SessionID: owner.SessionID, Kind: "summary", State: "pending", Payload: "{}", AvailableAt: now.Add(-time.Minute), CreatedAt: sourceTime}
			if err := s.db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.ProcessPending(ctx, owner, func(workCtx context.Context, _ ProcessingJob) error {
				return s.SaveSummary(workCtx, owner, "legacy updated")
			}); err != nil {
				t.Fatal(err)
			}
			want := "legacy updated"
			if older {
				want = "legacy accepted"
			}
			if got, err := s.LoadSummary(ctx, owner); err != nil || got != want {
				t.Fatalf("legacy migration result %q want %q: %v", got, want, err)
			}
		})
	}
}

func TestMemoryHardeningRetryBoundAndBackoff(t *testing.T) {
	for _, permanent := range []bool{true, false} {
		t.Run(fmt.Sprint(permanent), func(t *testing.T) {
			s, owner := hardeningService(t)
			ctx := context.Background()
			if err := s.Enqueue(ctx, owner, "derive", "bad"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			handler := func(context.Context, ProcessingJob) error {
				calls++
				if permanent {
					return PermanentProcessingError(errors.New("private raw error"))
				}
				return errors.New("private raw error")
			}
			for i := 0; i < MaxProcessingAttempts+2; i++ {
				if err := s.db.Model(&ProcessingJob{}).Where("state = ?", "pending").Update("available_at", time.Now().Add(-time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
				_ = s.ProcessPending(ctx, owner, handler)
				var job ProcessingJob
				if err := s.db.First(&job).Error; err != nil {
					t.Fatal(err)
				}
				if job.State == "pending" {
					minimum := time.Duration(30*(1<<i)) * time.Second
					if time.Until(job.AvailableAt) < minimum-time.Second {
						t.Fatalf("retry too early: %s", job.AvailableAt)
					}
					before := calls
					if err := s.ProcessPending(ctx, owner, handler); err != nil {
						t.Fatal(err)
					}
					if calls != before {
						t.Fatal("backoff ignored")
					}
				}
			}
			var job ProcessingJob
			if err := s.db.First(&job).Error; err != nil {
				t.Fatal(err)
			}
			want := MaxProcessingAttempts
			if permanent {
				want = 1
			}
			if calls != want || job.Attempts != want || job.State != "failed" || strings.Contains(job.LastError, "private") {
				t.Fatalf("retry bound: calls=%d job=%+v", calls, job)
			}
		})
	}
}

func TestMemoryHardeningExhaustedLeaseRetirement(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Enqueue(ctx, owner, "derive", "exhausted"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&ProcessingJob{}).Where("state = ?", "pending").Updates(map[string]interface{}{"state": "running", "attempts": MaxProcessingAttempts, "lease_until": time.Now().Add(time.Minute), "lease_token": "live"}).Error; err != nil {
		t.Fatal(err)
	}
	handler := func(context.Context, ProcessingJob) error { t.Fatal("exhausted job was retried"); return nil }
	if err := s.ProcessPending(ctx, owner, handler); err != nil {
		t.Fatal(err)
	}
	var job ProcessingJob
	if err := s.db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != "running" || job.LeaseToken != "live" {
		t.Fatal("active final attempt was retired")
	}
	if err := s.db.Model(&job).Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessPending(ctx, owner, handler); err != nil {
		t.Fatal(err)
	}
	if err := s.db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || job.LeaseToken != "" {
		t.Fatalf("exhausted claim not retired: %+v", job)
	}
}

func TestMemoryHardeningExtractorCommitsOnlyToAuthoritativeStore(t *testing.T) {
	for _, failAt := range []string{"load", "save", "none"} {
		t.Run(failAt, func(t *testing.T) {
			called := 0
			extractor := NewSessionMemoryExtractor(t.TempDir(), DefaultSessionMemoryConfig(), SessionExtractorDependencies{
				EstimateMessages: func([]map[string]interface{}) int { return 123 },
				CallLLM: func(_ context.Context, messages []map[string]interface{}, _ int) (string, error) {
					called++
					prompt, _ := messages[1]["content"].(string)
					if !strings.Contains(prompt, "database current") || strings.Contains(prompt, "legacy stale") {
						t.Fatalf("wrong prior summary: %s", prompt)
					}
					return "next summary", nil
				},
			}).ForSession("one")
			if err := os.WriteFile(extractor.MemoryPath(), []byte("legacy stale"), 0600); err != nil {
				t.Fatal(err)
			}
			committed := ""
			err := extractor.ExtractContextWithStore(context.Background(), []map[string]interface{}{{"role": "user", "content": "next turn"}}, "session",
				func(context.Context) (string, error) {
					if failAt == "load" {
						return "", errors.New("unavailable")
					}
					return "database current", nil
				},
				func(_ context.Context, value string) error {
					if failAt == "save" {
						return ErrMemoryLeaseLost
					}
					committed = value
					return nil
				})
			if failAt == "none" {
				if err != nil || committed != "next summary" || extractor.lastTokens != 123 || extractor.lastExtracted.IsZero() {
					t.Fatalf("commit did not update progress: %v", err)
				}
			} else if err == nil || committed != "" || extractor.lastTokens != 0 || !extractor.lastExtracted.IsZero() {
				t.Fatalf("failed commit advanced progress: %v", err)
			}
			if failAt == "load" && called != 0 {
				t.Fatal("model called after load failure")
			}
			data, readErr := os.ReadFile(extractor.MemoryPath())
			if readErr != nil || string(data) != "legacy stale" {
				t.Fatalf("compatibility file overwritten: %q %v", data, readErr)
			}
		})
	}
}
