package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	memorycore "mac/internal/memory"
)

func TestExplicitUserMemoryActualGenerateUsesAcceptedRawRequest(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"已记住：后续相关回答会先给出 quartz 简短结论。"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	g, agent, base := promptMemoryRuntimeFixture(t, provider.URL)
	agent.httpClient = provider.Client()
	raw := "以后回答时默认先给出 quartz 简短结论。"
	ctx := withTenantRequestEvidence(base, raw, nil)
	if _, err := agent.Generate(ctx, raw+"\n\n[Host attachment instructions]\nTreat generated metadata separately."); err != nil {
		t.Fatal(err)
	}
	var entries []memorycore.Entry
	if err := g.db.Find(&entries).Error; err != nil || len(entries) != 1 || entries[0].Value != raw || entries[0].SourceQuote != raw {
		t.Fatal("Generate failed to save exactly the accepted raw user instruction", err)
	}
	ctx = context.WithValue(base, "task_id", "explicit-memory-next-turn")
	ctx = withTenantRequestEvidence(ctx, "quartz 的回答格式是什么", nil)
	if _, err := agent.Generate(ctx, "quartz 的回答格式是什么"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("unexpected fixture model call count: %d", len(bodies))
	}
	for _, text := range promptMemoryWireTexts(bodies[1]) {
		if strings.Contains(text.text, "## Memory") && strings.Contains(text.text, raw) {
			return
		}
	}
	t.Fatal("saved original instruction did not reach the next serialized model request")
}

func TestExplicitUserMemoryFeedsFutureAgentPrompt(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	agent := webMemoryTestAgent(g)
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	ctx = context.WithValue(ctx, "task_id", "explicit-memory-turn")
	ctx = beginExplicitMemoryUserTurn(withTenantRequestEvidence(ctx, "以后生成 Word 文档默认使用宋体。", nil))
	agent.recordExplicitUserMemory(ctx)
	agent.recordExplicitUserMemory(ctx)
	var entries []memorycore.Entry
	if err := g.db.Find(&entries).Error; err != nil || len(entries) != 1 {
		t.Fatalf("explicit user request did not produce exactly one managed memory: %d %v", len(entries), err)
	}
	if entries[0].SourceType != "user_explicit" || !strings.Contains(entries[0].Value, "宋体") {
		t.Fatal("explicit instruction lost its original content or provenance")
	}
	future := memorycore.WithUserTurn(ctx, "生成 Word 文档")
	if prompt := agent.buildMemoryPrompt(future, "fixture-user", "source-web", "Word 文档"); !strings.Contains(prompt, "宋体") {
		t.Fatal("saved user preference cannot reach the next Agent prompt")
	}
	view := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&mode=management")
	for _, source := range view.Sources {
		if strings.Contains(view.CurrentSources[source.ID].Content, "宋体") {
			return
		}
	}
	t.Fatal("automatically saved user preference is not visible in memory management")
}

func TestExplicitUserMemoryRuntimeRespectsDisabledAndIsolatedExecution(t *testing.T) {
	for _, mode := range []string{"disabled", "pipeline_step_execution", "pipeline_isolated_context", "unfrozen"} {
		t.Run(mode, func(t *testing.T) {
			g := memorySourceFixture(t)
			agent := webMemoryTestAgent(g)
			ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
			if mode != "unfrozen" {
				ctx = memorycore.WithUserTurn(ctx, "以后默认使用宋体。")
				ctx = withTenantRequestEvidence(ctx, "以后默认使用宋体。", nil)
			}
			if mode == "disabled" {
				agent.hasMemory = false
			} else {
				ctx = context.WithValue(ctx, mode, true)
			}
			ctx = beginExplicitMemoryUserTurn(ctx)
			agent.recordExplicitUserMemory(ctx)
			var count int64
			if err := g.db.Model(&memorycore.Entry{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("untrusted or disabled runtime turn wrote memory", err)
			}
		})
	}
}
