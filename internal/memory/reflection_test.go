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

func reviewTestService(t *testing.T) (*Service, Owner) {
	t.Helper()
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(nil))
	return s, Owner{TenantID: "local", PartitionID: "alice", SessionID: "agent-chat"}
}

func enqueueTestReview(t *testing.T, s *Service, owner Owner, task, input string) {
	t.Helper()
	p, err := s.PrepareReview(context.Background(), owner, ReviewTurn{TaskID: task, SessionID: owner.SessionID, Mode: "agent", Outcome: "completed", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Enqueue(context.Background(), s.db); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&ProcessingJob{}).Where("id = ?", p.job.ID).Update("available_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
}

func reviewResponse(proposals ...ReviewProposal) string {
	data, _ := json.Marshal(map[string]interface{}{"proposals": proposals})
	return string(data)
}

func sampleReviewProposal() ReviewProposal {
	return ReviewProposal{Content: "客户甲的报价默认交付 Excel。", WhenToUse: "为客户甲制作报价时", Kind: "preference",
		Evidence: []ReviewQuote{{SourceRef: "source_0", ProofRef: "user", Quote: "客户甲报价，默认使用 Excel"}}}
}

func TestReviewCandidateEvidenceConfirmationAndDeletion(t *testing.T) {
	s, owner := reviewTestService(t)
	ctx := context.Background()
	input := "以后给客户甲报价，默认使用 Excel。"
	enqueueTestReview(t, s, owner, "task-1", input)
	proposal := sampleReviewProposal()
	if err := s.ProcessReviews(ctx, owner, func(ctx context.Context, messages []map[string]interface{}, tokens int) (string, error) {
		if tokens != 2000 || len(messages) != 2 || ReviewRouteFromContext(ctx).TaskID != "task-1" || strings.Contains(fmt.Sprint(messages), "unrelated-qa-history") {
			t.Fatalf("unbounded or wrong review input: %#v", messages)
		}
		return reviewResponse(proposal), nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, total, err := s.Page(ctx, owner, "", "candidate", "", 0, 20)
	if err != nil || total != 1 || len(entries) != 1 || len(entries[0].Evidence) != 1 || entries[0].WhenToUse != proposal.WhenToUse {
		t.Fatalf("candidate/evidence missing: %#v, %d, %v", entries, total, err)
	}
	entry := entries[0]
	if entry.Evidence[0].Quote != proposal.Evidence[0].Quote || entry.Evidence[0].TaskID != "task-1" {
		t.Fatalf("evidence not traceable: %#v", entry.Evidence)
	}
	if _, err := s.EvidenceForEntry(ctx, Owner{TenantID: "local", PartitionID: "bob"}, entry.ID); err == nil {
		t.Fatal("foreign owner read evidence")
	}
	recalled, err := s.Recall(ctx, owner, "客户甲报价", 2000, nil)
	if err != nil || strings.Contains(recalled.Prompt, proposal.Content) {
		t.Fatalf("candidate was automatically recalled: %q %v", recalled.Prompt, err)
	}
	active, err := s.Confirm(ctx, owner, entry.Key, entry.Scope, entry.Version)
	if err != nil {
		t.Fatal(err)
	}
	recalled, err = s.Recall(ctx, owner, "客户甲报价", 2000, nil)
	if err != nil || !strings.Contains(recalled.Prompt, proposal.Content) || !strings.Contains(recalled.Prompt, proposal.WhenToUse) {
		t.Fatalf("confirmed memory not in prompt: %q %v", recalled.Prompt, err)
	}
	if ok, err := s.ForgetExpected(ctx, owner, active.Key, active.Scope, active.Version); err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	// Replaying exactly the original task, including under a rebuilt Service,
	// cannot recreate a source/job or keep copied quotes in the memory store.
	s = NewService(s.db, s.workspace, s.agent, WithRecallEmbedding(nil))
	enqueueTestReview(t, s, owner, "task-1", input)
	var source reviewSource
	if err := s.db.First(&source).Error; err != nil || !source.Retired || source.Payload != "" {
		t.Fatalf("source tombstone missing: %#v %v", source, err)
	}
	var proofs int64
	s.db.Model(&Evidence{}).Count(&proofs)
	if proofs != 0 {
		t.Fatal("deleted memory retained quote payloads")
	}
	if err := s.ProcessReviews(ctx, owner, func(context.Context, []map[string]interface{}, int) (string, error) {
		t.Fatal("deleted source invoked model again")
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, total, err = s.Page(ctx, owner, "", "", "", 0, 20)
	if err != nil || total != 0 || len(entries) != 0 {
		t.Fatalf("deleted memory resurrected: %#v %v", entries, err)
	}
}

func TestReviewModeBoundsAndTransactionalOutbox(t *testing.T) {
	s, owner := reviewTestService(t)
	ctx := context.Background()
	base := ReviewTurn{TaskID: "task", SessionID: owner.SessionID, Mode: "agent", Outcome: "completed", Input: "以后报告默认先写结论。"}
	for _, mode := range []string{"qa", "", "image", "music"} {
		turn := base
		turn.Mode = mode
		if _, err := s.PrepareReview(ctx, owner, turn); err == nil {
			t.Fatalf("accepted non-Agent mode %q", mode)
		}
	}
	for _, status := range []string{"running", "pending", "paused", "waiting_approval", "stopped"} {
		turn := base
		turn.Outcome = status
		if _, err := s.PrepareReview(ctx, owner, turn); err == nil {
			t.Fatalf("accepted provisional outcome %q", status)
		}
	}
	turn := base
	turn.Input = strings.Repeat("x", 8001)
	if _, err := s.PrepareReview(ctx, owner, turn); err == nil {
		t.Fatal("accepted oversized input")
	}
	p, err := s.PrepareReview(ctx, owner, base)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.db.Begin()
	if err := p.Enqueue(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	var count int64
	s.db.Model(&ProcessingJob{}).Count(&count)
	if count != 0 {
		t.Fatal("outbox survived rejected task transaction")
	}
	if err := p.Enqueue(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if err := p.Enqueue(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	s.db.Model(&ProcessingJob{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate completion duplicated review")
	}
	// The old summary/derive handler cannot accidentally claim review work.
	s.db.Model(&ProcessingJob{}).Update("available_at", time.Now().Add(-time.Minute))
	if err := s.ProcessPending(ctx, owner, func(context.Context, ProcessingJob) error { t.Fatal("legacy handler claimed review"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRejectsFabricatedAndNonUserEvidence(t *testing.T) {
	s, owner := reviewTestService(t)
	p, err := s.PrepareReview(context.Background(), owner, ReviewTurn{TaskID: "one", SessionID: owner.SessionID, Mode: "agent", Outcome: "failed",
		Input: "这次先检查表格。", Output: "用户总是偏好蓝色。", Receipts: []ReviewReceipt{{Name: "inspect", Success: false, Result: "missing required column"}}})
	if err != nil {
		t.Fatal(err)
	}
	var turn ReviewTurn
	json.Unmarshal([]byte(p.source.Payload), &turn)
	inputs := []reviewInput{makeReviewInput(p.source, turn, 0)}
	for _, proof := range []ReviewQuote{
		{SourceRef: "source_0", ProofRef: "assistant", Quote: "用户总是偏好蓝色"},
		{SourceRef: "foreign_source", ProofRef: "user", Quote: "这次先检查表格"},
		{SourceRef: "source_0", ProofRef: "user", Quote: "虚构的原话"},
		{SourceRef: "source_0", ProofRef: "tool_0", Quote: "missing required column"},
	} {
		proposal := ReviewProposal{Content: "用户喜欢蓝色。", WhenToUse: "用户的报告", Kind: "preference", Evidence: []ReviewQuote{proof}}
		if _, valid := validateReviewProposal(proposal, inputs); valid {
			t.Fatalf("unsupported preference accepted: %#v", proof)
		}
	}
	lesson := ReviewProposal{Content: "该表格缺少必要列，填写前先核对列。", WhenToUse: "处理相同结构表格时", Kind: "experience",
		Evidence: []ReviewQuote{{SourceRef: "source_0", ProofRef: "tool_0", Quote: "missing required column"}}}
	if _, valid := validateReviewProposal(lesson, inputs); !valid {
		t.Fatal("real failure evidence rejected")
	}
}

func TestReviewHabitsCountIndependentTasks(t *testing.T) {
	proposal := ReviewProposal{Content: "报告先写简短摘要。", WhenToUse: "制作该类报告时", Kind: "preference", Habit: true}
	var inputs []reviewInput
	for i := 0; i < 3; i++ {
		turn := ReviewTurn{Input: "报告请改成简短摘要。"}
		input := makeReviewInput(reviewSource{ID: fmt.Sprint(i), TaskID: fmt.Sprint(i)}, turn, i)
		inputs = append(inputs, input)
		proposal.Evidence = append(proposal.Evidence, ReviewQuote{SourceRef: input.Ref, ProofRef: "user", Quote: "改成简短摘要"})
	}
	if _, valid := validateReviewProposal(proposal, inputs); !valid {
		t.Fatal("three independent tasks rejected")
	}
	inputs[2].source.TaskID = inputs[1].source.TaskID
	if _, valid := validateReviewProposal(proposal, inputs); valid {
		t.Fatal("repeated task counted as independent support")
	}
	inputs[2].source.TaskID = "2"
	proposal.Evidence = proposal.Evidence[1:]
	if _, valid := validateReviewProposal(proposal, inputs); valid {
		t.Fatal("old evidence alone created a new memory")
	}
}

func TestReviewLateResultsAreFenced(t *testing.T) {
	for _, mode := range []string{"lease", "source", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, owner := reviewTestService(t)
			allowed := true
			s.reviewAllowed = func() bool { return allowed }
			enqueueTestReview(t, s, owner, "task-1", "以后给客户甲报价，默认使用 Excel。")
			err := s.ProcessReviews(context.Background(), owner, func(ctx context.Context, _ []map[string]interface{}, _ int) (string, error) {
				switch mode {
				case "lease":
					claim := ctx.Value(processingClaimKey{}).(ProcessingJob)
					s.db.Model(&ProcessingJob{}).Where("id = ?", claim.ID).Update("lease_token", "new-worker")
				case "source":
					s.db.Model(&reviewSource{}).Where("task_id = ?", "task-1").Update("retired", true)
				case "disabled":
					allowed = false
				}
				return reviewResponse(sampleReviewProposal()), nil
			})
			if mode == "lease" && err == nil {
				t.Fatal("lease takeover was not reported")
			}
			var count int64
			if err := s.db.Model(&Entry{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("late result wrote entries: %d %v", count, err)
			}
		})
	}
}

func TestReviewDailyBudgetAndStrictResponse(t *testing.T) {
	s, owner := reviewTestService(t)
	for i := 0; i < 12; i++ {
		if err := s.claimReviewBudget(context.Background(), owner); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.claimReviewBudget(context.Background(), owner); !errors.Is(err, ErrReviewBudget) {
		t.Fatalf("budget not enforced: %v", err)
	}
	enqueueTestReview(t, s, owner, "task-1", "以后给客户甲报价，默认使用 Excel。")
	_ = s.ProcessReviews(context.Background(), owner, func(context.Context, []map[string]interface{}, int) (string, error) {
		t.Fatal("exhausted budget invoked provider")
		return "", nil
	})
	var job ProcessingJob
	s.db.First(&job)
	if job.State != "pending" || job.Attempts != 0 || job.LastError != "daily_budget" || !job.AvailableAt.After(time.Now()) {
		t.Fatalf("budget did not defer safely: %#v", job)
	}
	for _, output := range []string{`{"proposals":[],"execute":"shell"}`, `{"proposals":[]} {"proposals":[]}`, "not json"} {
		s, owner := reviewTestService(t)
		enqueueTestReview(t, s, owner, "task-1", "以后报告默认简短。")
		if err := s.ProcessReviews(context.Background(), owner, func(context.Context, []map[string]interface{}, int) (string, error) { return output, nil }); err == nil {
			t.Fatalf("accepted invalid response: %s", output)
		}
	}
}

func TestReviewPreservesHumanEditDuringModelCall(t *testing.T) {
	s, owner := reviewTestService(t)
	ctx := context.Background()
	input := "以后给客户甲报价，默认使用 Excel。"
	enqueueTestReview(t, s, owner, "task-1", input)
	call := func(context.Context, []map[string]interface{}, int) (string, error) {
		return reviewResponse(sampleReviewProposal()), nil
	}
	if err := s.ProcessReviews(ctx, owner, call); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := s.Page(ctx, owner, "", "candidate", "", 0, 20)
	entry := entries[0]
	enqueueTestReview(t, s, owner, "task-2", input)
	if err := s.ProcessReviews(ctx, owner, func(context.Context, []map[string]interface{}, int) (string, error) {
		if err := s.RecordWithSource(ctx, owner, entry.Key, "客户甲先给 PDF。", entry.Scope, entry.Kind, entry.State, entry.Priority, nil, entry.Version, WriteSource{Type: "user_explicit", Ref: "memory-manager"}); err != nil {
			t.Fatal(err)
		}
		return reviewResponse(sampleReviewProposal()), nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Lookup(ctx, owner, entry.Key, entry.Scope)
	if err != nil || updated.Value != "客户甲先给 PDF。" || updated.Version != 2 {
		t.Fatalf("review overwrote human edit: %#v %v", updated, err)
	}
	proofs, err := s.EvidenceForEntry(ctx, owner, entry.ID)
	if err != nil || len(proofs) != 0 {
		t.Fatalf("old evidence was attached to new human content: %#v %v", proofs, err)
	}
}

func TestReviewOneOffAndCredentialBounds(t *testing.T) {
	for _, text := range []string{"这次报价给 Excel。", "临时使用蓝色。", "This time use Excel."} {
		if reviewDurablePreference(text) {
			t.Fatalf("one-off classified as durable: %s", text)
		}
	}
	s, owner := reviewTestService(t)
	_, err := s.PrepareReview(context.Background(), owner, ReviewTurn{TaskID: "task", SessionID: owner.SessionID, Mode: "agent", Outcome: "completed", Input: "记住我的 API-key 是 placeholder"})
	if err == nil {
		t.Fatal("credential request accepted for automatic learning")
	}
}

func TestReviewBoundsSerializedInput(t *testing.T) {
	turn := ReviewTurn{Input: strings.Repeat("\"", 8000), Output: strings.Repeat("x", 3000)}
	for i := 0; i < 20; i++ {
		turn.Receipts = append(turn.Receipts, ReviewReceipt{Name: "inspect", Result: strings.Repeat("x", 600)})
	}
	inputs := boundedReviewInputs([]reviewInput{makeReviewInput(reviewSource{}, turn, 0)})
	data, _ := json.Marshal(inputs)
	if len([]rune(string(data))) > 16000 || len(inputs) != 1 || len(inputs[0].Proofs) == 0 {
		t.Fatalf("provider input is unbounded: %d", len([]rune(string(data))))
	}
}
