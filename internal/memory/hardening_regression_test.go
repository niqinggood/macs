package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hardeningService(t *testing.T) (*Service, Owner) {
	t.Helper()
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "audit.db")), t.TempDir(), "assistant")
	return s, Owner{PartitionID: "audit", TenantID: "audit", SubjectID: "alice", SessionID: "one"}
}

func TestMemoryHardeningCommonChinesePhraseShouldNotRecallUnrelatedMemory(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Record(ctx, owner, "output_folder", "我们默认把文件放到输出目录", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Recall(ctx, owner, "我们今天聊一下天文", 2000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("unrelated file preference selected for astronomy: %+v", result.Items)
	}
}

func TestMemoryHardeningConfirmationMustMatchExactMemoryKey(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	for _, key := range []string{"color", "color_theme"} {
		if err := s.Record(ctx, owner, key, "candidate "+key, MemoryUser, "preference", "candidate", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewAgentMemoryServiceTool(s, func(context.Context) (Owner, error) { return owner, nil }, nil)
	result, err := tool.Execute(WithUserTurn(ctx, "确认记忆 color_theme"), map[string]interface{}{"action": "confirm", "key": "color", "scope": "user", "expected_version": 1})
	entry, lookupErr := s.Lookup(ctx, owner, "color", MemoryUser)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if err == nil || entry.State != "candidate" {
		t.Fatalf("user named color_theme but color was activated: state=%s result=%+v error=%v", entry.State, result, err)
	}
}

func TestMemoryHardeningRetriedOldSummaryMustNotReplaceNewSummary(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Enqueue(ctx, owner, "summary", map[string]string{"content": "old summary"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessPending(ctx, owner, func(context.Context, ProcessingJob) error { return errors.New("temporary failure") }); err == nil {
		t.Fatal("expected initial failure")
	}
	if err := s.Enqueue(ctx, owner, "summary", map[string]string{"content": "new summary"}); err != nil {
		t.Fatal(err)
	}
	write := func(ctx context.Context, job ProcessingJob) error {
		var payload map[string]string
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return err
		}
		return s.SaveSummary(ctx, owner, payload["content"])
	}
	if err := s.ProcessPending(ctx, owner, write); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&ProcessingJob{}).Where("state = ?", "pending").Update("available_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessPending(ctx, owner, write); err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := s.db.First(&summary).Error; err != nil {
		t.Fatal(err)
	}
	if summary.Content != "new summary" {
		t.Fatalf("successful retry of stale input replaced latest summary: %q", summary.Content)
	}
}

func TestMemoryHardeningPermanentFailuresMustNotStarveLaterJobs(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		payload := fmt.Sprintf("job-%d", i)
		if err := s.Enqueue(ctx, owner, "summary", payload); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(payload)
		if err := s.db.Model(&ProcessingJob{}).Where("payload = ?", string(raw)).Update("created_at", start.Add(time.Duration(i)*time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	processedGood := 0
	for cycle := 0; cycle < 3; cycle++ {
		if err := s.db.Model(&ProcessingJob{}).Where("state = ?", "pending").Update("available_at", time.Now().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		err := s.ProcessPending(ctx, owner, func(_ context.Context, job ProcessingJob) error {
			if strings.Contains(job.Payload, "job-4") {
				processedGood++
				return nil
			}
			return errors.New("permanent unsupported payload")
		})
		if err == nil {
			t.Fatal("expected permanent failures")
		}
	}
	if processedGood == 0 {
		t.Fatal("four oldest failing jobs took all four slots in every batch; fifth valid job was never attempted")
	}
}

func TestMemoryHardeningForgettingShortValueMustNotSuppressUnrelatedWord(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Record(ctx, owner, "programming_language", "Go", MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, owner, "programming_language", MemoryUser); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSummary(ctx, owner, "Google cloud billing procedure"); err != nil {
		t.Fatal(err)
	}
	result, err := s.Recall(ctx, owner, "Google cloud billing", 2000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) == 0 {
		t.Fatalf("forgetting Go suppressed unrelated Google summary: %+v", result.Decisions)
	}
}

func TestMemoryHardeningCommonPhrasePoolMustNotHideSpecificMatch(t *testing.T) {
	s, owner := hardeningService(t)
	ctx := context.Background()
	if err := s.Record(ctx, owner, "nebula_spectrum", "星云光谱的项目事实", MemoryProject, "fact", "active", 1, nil, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if err := s.Record(ctx, owner, fmt.Sprintf("noise_%03d", i), "我们默认把文件放到输出目录", MemoryProject, "fact", "active", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Recall(ctx, owner, "我们来看看星云光谱", 2000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Prompt, "nebula_spectrum") {
		t.Fatalf("specific match was absent from both capped pools; returned %d unrelated items", len(result.Items))
	}
}

func TestMemoryHardeningLegacyHistoryMustRespectAgentNamespace(t *testing.T) {
	s, owner := hardeningService(t)
	other := NewMemoryStore(t.TempDir(), s.db, "other-agent", 10)
	event := MemoryEvent{TenantID: "audit", AgentName: "private-agent", UserName: "alice", SessionID: "old-chat", UserQuestion: "quartz deployment", FinalAnswer: "quartz private-agent-only fact", Status: "success"}
	if err := s.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	history := other.GetMemoryContextForTenant("audit", "alice", "new-chat", "quartz deployment")
	otherService := NewService(s.db, s.workspace, "other-agent")
	result, err := otherService.Recall(context.Background(), owner, "quartz deployment", 2000, []RecallItem{{ID: "history-profile", Source: "history_and_profile", Rank: 60, Content: history}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Prompt, "private-agent-only") {
		t.Fatal("other-agent recalled private-agent history through legacy FTS supplemental despite distinct Service namespaces")
	}
}

func TestMemoryHardeningFailureLessonMustBeFindableByItsUser(t *testing.T) {
	s, _ := hardeningService(t)
	store := NewMemoryStore(t.TempDir(), s.db, "assistant", 10)
	// The same constructor is used for lessons and Skill hints in the host.
	event := NewLearningEvent(&MemoryEvent{TenantID: "audit", AgentName: "assistant", UserID: "alice-id", UserName: "alice", UserChannel: "web", SessionID: "old-chat"}, "lessons learned", "quartz task failure lesson")
	if err := s.db.Create(event).Error; err != nil {
		t.Fatal(err)
	}
	if all := store.SearchMemoryFTSForTenant("audit", "quartz", "", 10); len(all) != 1 {
		t.Fatalf("fixture not indexed: %+v", all)
	}
	results := store.SearchMemoryFTSForTenant("audit", "quartz", "alice", 10)
	if len(results) == 0 {
		t.Fatal("post-session lesson has UserID but no UserName; user-scoped history search cannot find it")
	}
}
