package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
)

func TestTaskMemoryReviewCommitsWithHostTerminal(t *testing.T) {
	for _, outcome := range []string{"completed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			g := memorySourceFixture(t)
			if err := g.db.AutoMigrate(&GatewayTaskRecord{}, &MessageRequestRecord{}, &SkillRunRecord{}); err != nil {
				t.Fatal(err)
			}
			agent := webMemoryTestAgent(g)
			ctx, audience := webMemoryTestContext(t, g, "source-web", "fixture-user")
			ctx = context.WithValue(ctx, "task_id", "review-task")
			task := &GatewayTask{ID: "review-task", Status: "running", CreatedAt: time.Now(), ResolvedWorkspace: agent.workspace,
				Request: &MessageRequest{SessionId: "source-web", AgentName: "first", UserId: "fixture-user", Channel: "web", TaskType: "chat"}, webMemoryAudience: audience}
			g.tasks = map[string]*GatewayTask{task.ID: task}
			ctx = g.beginTaskMemoryReview(ctx, task, agent.name)
			ctx = withTenantRequestEvidence(ctx, "以后报价默认使用 Excel。", nil)
			ctx = beginExplicitMemoryUserTurn(ctx)
			// Neither old QA history nor a recalled/augmented prompt supplies the
			// original user turn. The host terminal overrides provisional success.
			ctx = context.WithValue(ctx, "history_messages", []map[string]interface{}{{"role": "user", "content": "OLD_QA_MUST_NOT_BE_LEARNED"}})
			event := &memorycore.MemoryEvent{UserQuestion: "AUGMENTED_PROMPT_MUST_NOT_BE_LEARNED", Status: "success",
				CallChain: datatypes.JSON(`[{"type":"tool","name":"inspect","success":false,"error":"required column missing"}]`)}
			agent.captureTaskMemoryReview(ctx, event)
			var count int64
			g.db.Model(&memorycore.ProcessingJob{}).Where("kind = ?", memorycore.ReviewJobKind).Count(&count)
			if count != 0 {
				t.Fatal("Generate published provisional memory work")
			}
			var taskErr error
			if outcome == "failed" {
				taskErr = errors.New("host rejected result")
			}
			terminal, applied := g.finishGatewayTask(task, outcome, "HOST_ACCEPTED_OUTPUT", taskErr)
			if !applied || terminal == nil || terminal.Status != outcome {
				t.Fatalf("host completion failed: %#v %v", terminal, applied)
			}
			var source struct{ Payload string }
			if err := g.db.Table("memory_review_sources").First(&source).Error; err != nil {
				t.Fatal(err)
			}
			var turn memorycore.ReviewTurn
			if json.Unmarshal([]byte(source.Payload), &turn) != nil || turn.Input != "以后报价默认使用 Excel。" || turn.Outcome != outcome || turn.Output != "HOST_ACCEPTED_OUTPUT" || len(turn.Receipts) != 1 || turn.Receipts[0].Success {
				t.Fatalf("untrusted review content/outcome: %s", source.Payload)
			}
			for _, forbidden := range []string{"OLD_QA_MUST_NOT", "AUGMENTED_PROMPT_MUST_NOT"} {
				if strings.Contains(source.Payload, forbidden) {
					t.Fatal("history entered long-term learning")
				}
			}
			if _, applied := g.finishGatewayTask(task, "completed", "LATE_RESULT", nil); applied {
				t.Fatal("late completion was accepted")
			}
			g.db.Model(&memorycore.ProcessingJob{}).Where("kind = ?", memorycore.ReviewJobKind).Count(&count)
			if count != 1 {
				t.Fatalf("terminal replay duplicated outbox: %d", count)
			}
		})
	}
}

func TestTaskMemoryReviewExcludesQAUnsealedAndNestedTurns(t *testing.T) {
	g := memorySourceFixture(t)
	agent := webMemoryTestAgent(g)
	ctx, audience := webMemoryTestContext(t, g, "source-web", "fixture-user")
	for _, mode := range []string{"qa", "image", "music"} {
		task := &GatewayTask{ID: mode, Request: &MessageRequest{Channel: "web", TaskType: mode}, webMemoryAudience: audience}
		g.beginTaskMemoryReview(ctx, task, agent.name)
		if task.memoryReview != nil {
			t.Fatalf("%s received a review capture", mode)
		}
	}
	plain := &GatewayTask{ID: "unsealed", Request: &MessageRequest{Channel: "web", TaskType: "chat"}}
	g.beginTaskMemoryReview(ctx, plain, agent.name)
	if plain.memoryReview != nil {
		t.Fatal("client-provided channel granted review authority")
	}
	task := &GatewayTask{ID: "nested", Request: &MessageRequest{Channel: "web", TaskType: "chat"}, webMemoryAudience: audience}
	ctx = g.beginTaskMemoryReview(ctx, task, agent.name)
	ctx = withTenantRequestEvidence(ctx, "以后默认使用 Excel。", nil)
	ctx = beginExplicitMemoryUserTurn(ctx)
	ctx = beginExplicitMemoryUserTurn(ctx) // Nested Generate loses auto-write authority.
	agent.captureTaskMemoryReview(ctx, &memorycore.MemoryEvent{})
	if task.memoryReview.service != nil {
		t.Fatal("nested Generate borrowed parent user evidence")
	}
}

func TestTaskMemoryReviewWorkerUsesSelectedModelAndStops(t *testing.T) {
	g := memorySourceFixture(t)
	agent := webMemoryTestAgent(g)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string                   `json:"model"`
			Messages []map[string]interface{} `json:"messages"`
			Tools    interface{}              `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "selected-review-model" || request.Tools != nil || len(request.Messages) != 2 {
			t.Errorf("review did not use selected tool-free model: %#v", request)
		}
		content, _ := json.Marshal(map[string]interface{}{"proposals": []memorycore.ReviewProposal{{Content: "客户甲报价交付 Excel。", WhenToUse: "给客户甲报价时", Kind: "preference",
			Evidence: []memorycore.ReviewQuote{{SourceRef: "source_0", ProofRef: "user", Quote: "客户甲报价，默认使用 Excel"}}}}})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": string(content)}}}})
	}))
	defer server.Close()
	agent.httpClient = server.Client()
	agent.modelConfig = &ModelConfig{Type: "openai", ModelName: "default-must-not-be-used", BaseURL: server.URL + "/v1", APIKey: "fixture-key"}
	g.engine.agentFactory.agents[agent.name] = agent
	g.tasks = map[string]*GatewayTask{"worker-task": {ID: "worker-task", ResolvedWorkspace: agent.workspace,
		Request: &MessageRequest{Channel: "web", AgentName: agent.name}, webModelSelection: &webModelSelection{alias: "selected", model: ModelConfig{Type: "openai", ModelName: "selected-review-model", BaseURL: server.URL + "/v1", APIKey: "fixture-key"}}}}
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, SessionID: "source-web"}
	p, err := agent.memoryService.PrepareReview(context.Background(), owner, memorycore.ReviewTurn{TaskID: "worker-task", SessionID: owner.SessionID, Mode: "agent", Outcome: "completed", Input: "以后给客户甲报价，默认使用 Excel。", ModelAlias: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Enqueue(context.Background(), g.db); err != nil {
		t.Fatal(err)
	}
	g.db.Model(&memorycore.ProcessingJob{}).Where("kind = ?", memorycore.ReviewJobKind).Update("available_at", time.Now().Add(-time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); g.runMemoryReviews(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		var job memorycore.ProcessingJob
		if g.db.Where("kind = ?", memorycore.ReviewJobKind).First(&job).Error == nil && job.State == "done" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("background review never completed")
		case <-poll.C:
		}
	}
	entries, total, err := agent.memoryService.Page(context.Background(), owner, "", "candidate", "", 0, 20)
	if err != nil || total != 1 || len(entries[0].Evidence) != 1 {
		t.Fatalf("worker did not persist candidate evidence: %#v %d %v", entries, total, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("review worker ignored lifecycle cancellation")
	}
}
