package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

func promptMemoryRuntimeFixture(t *testing.T, providerURL string) (*GatewayService, *LLMAgent, context.Context) {
	t.Helper()
	g := memorySourceFixture(t)
	workspace := g.currentProjectWorkspace()
	config := &Config{Workspace: workspace}
	config.SkillsConfig.DisEnabled = true
	config.MemoryConfig.Enabled = false
	config.ToolsConfig.Enabled = true
	config.Runtime.LLMMaxRetries = 1
	config.Runtime.LLMCallTimeoutSeconds = 5
	agent := NewLLMAgent(AgentConfig{Name: "first", Model: "fixture", SystemMessage: "Answer briefly. FIXTURE_SYSTEM_NOT_A_SOURCE", MaxTokens: 256, MaxIterations: 3, EnabledTools: []string{"read_file", "agent_memory"}},
		&ModelConfig{Type: "openai", ModelName: "fixture-model", APIKey: "fixture-key-never-in-receipt", BaseURL: providerURL + "/v1"}, nil, config, log.New(io.Discard, "", 0))
	agent.hasMemory = true
	if agent.proactiveEngine != nil {
		agent.proactiveEngine.Stop()
	}
	agent.projectInstructions = projectctx.NewProjectInstructionLoader(t.TempDir())
	agent.memoryService = memorycore.NewService(g.db, workspace, "first", memorycore.WithRecallEmbedding(func() memorycore.RecallEmbedding { return memorycore.RecallEmbedding{} }))
	if err := agent.memoryService.SetContextStore(agent.scm); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agent.ptcServer.Stop)
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	ctx = context.WithValue(ctx, "task_id", "fixture-input-task")
	ctx = context.WithValue(ctx, "user_id", "fixture-user")
	ctx = projectctx.WithResolvedWorkspace(ctx, workspace)
	return g, agent, ctx
}

func promptMemoryRuntimeInputs(t *testing.T, agent *LLMAgent, ctx context.Context) []memorycore.PromptInput {
	t.Helper()
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "source-web"
	inputs, err := agent.memoryService.RecentPromptInputs(ctx, owner, owner.SessionID, 20)
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func TestPromptMemoryRuntimeActualGenerateAndNextToolRound(t *testing.T) {
	var mu sync.Mutex
	var wireBodies [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		wireBodies = append(wireBodies, body)
		n := len(wireBodies)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"fixture-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"fixture-proof.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"quartz fixture complete"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	g, agent, ctx := promptMemoryRuntimeFixture(t, provider.URL)
	agent.httpClient = provider.Client()
	workspace := g.currentProjectWorkspace()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Use quartz fixture conventions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "fixture-proof.txt"), []byte("fixture-tool-result-body"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, _ := agent.memoryOwner(ctx)
	owner.SessionID = "source-web"
	if err := agent.memoryService.Record(ctx, owner, "quartz-format", "quartz prefer plain headings", memorycore.MemoryUser, "preference", "active", 8, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := agent.scm.Set("quartz-context", "quartz current working fact", "fact", 8, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := agent.memoryService.SaveSummary(ctx, owner, "quartz previous session summary"); err != nil {
		t.Fatal(err)
	}
	message := ChatMessage{SessionID: owner.SessionID, TenantID: "default", AgentName: "first", UserId: "fixture-user", Role: "user", Content: "quartz original user history"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	history := buildGatewaySessionHistory([]ChatMessage{message})
	ctx = context.WithValue(ctx, "history_messages", history.Messages)
	ctx = context.WithValue(ctx, promptMemoryHistoryKey{}, history.Sources)
	if answer, err := agent.Generate(ctx, "Read fixture-proof.txt and report the quartz fixture result."); err != nil || !strings.Contains(answer, "quartz fixture complete") {
		t.Fatalf("actual tool loop failed: %q %v", answer, err)
	}
	inputs := promptMemoryRuntimeInputs(t, agent, ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || len(wireBodies) != 2 {
		t.Fatalf("expected a receipt for the first and tool-following call: receipts=%d calls=%d", len(inputs), len(wireBodies))
	}
	if !strings.Contains(string(wireBodies[1]), "fixture-tool-result-body") || !strings.Contains(string(wireBodies[1]), `"role":"tool"`) {
		var request struct {
			Messages []map[string]interface{} `json:"messages"`
		}
		_ = json.Unmarshal(wireBodies[1], &request)
		var toolContents []interface{}
		for _, message := range request.Messages {
			if message["role"] == "tool" {
				toolContents = append(toolContents, message["content"])
			}
		}
		t.Fatalf("the second real provider request did not replay the fixture file: %v", toolContents)
	}
	for _, input := range inputs {
		if input.Status != "succeeded" || input.TaskID != "fixture-input-task" || input.Agent != "first" {
			t.Fatal("receipt lost current task/agent/send-attempt semantics")
		}
		found := map[string]bool{}
		texts := promptMemoryWireTexts(wireBodies[input.CallIndex-1])
		for _, source := range input.Sources {
			found[source.Locator.Kind] = true
			contained := false
			for _, text := range texts {
				contained = contained || strings.Contains(text.text, source.Excerpt)
			}
			if !contained || source.CapturedRevision == "" || source.SourceLabel == "" {
				t.Fatal("receipt invented or modified an actual sent excerpt")
			}
			if source.Locator.Kind == "chat_message" && source.Locator.Digest != promptMemoryDigest(message.Content) {
				t.Fatal("history digest was computed from transformed content instead of the original record")
			}
		}
		for _, kind := range []string{"entry", "context", "summary", "chat_message", "project_instruction"} {
			if !found[kind] {
				t.Errorf("source %s is missing from actual request %d", kind, input.CallIndex)
			}
		}
		public, _ := json.Marshal(input)
		for _, secret := range []string{"fixture-key-never-in-receipt", "FIXTURE_SYSTEM_NOT_A_SOURCE", `"Locator"`, `"locator"`, workspace} {
			if strings.Contains(string(public), secret) {
				t.Fatal("private request or source address leaked in HTTP DTO")
			}
		}
	}
}

func TestPromptMemoryRuntimeHistoryRequiresRealMessageIdentity(t *testing.T) {
	g, agent, ctx := promptMemoryRuntimeFixture(t, "http://127.0.0.1:1")
	message := ChatMessage{SessionID: "source-web", TenantID: "default", AgentName: "first", UserId: "fixture-user", Role: "user", Content: "same text must not fabricate old source"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	history := buildGatewaySessionHistory([]ChatMessage{message})
	ctx = context.WithValue(ctx, promptMemoryHistoryKey{}, history.Sources)
	ctx = agent.beginPromptMemoryInput(ctx, "source-web", "fixture-input-task")
	original := cloneAPIMessage(history.Messages[0])
	bindPromptMemoryHistoryMessage(ctx, 0, original)
	for _, test := range []struct {
		name     string
		messages []map[string]interface{}
		tools    interface{}
		wanted   int
	}{
		{name: "real_original", messages: []map[string]interface{}{original, {"role": "user", "content": "new current input"}}, wanted: 1},
		{name: "only_current_identical_input", messages: []map[string]interface{}{{"role": "user", "content": message.Content}}},
		{name: "substring_current_input", messages: []map[string]interface{}{{"role": "user", "content": "prefix " + message.Content}}},
		{name: "in_tools_schema", messages: []map[string]interface{}{{"role": "user", "content": "other"}}, tools: map[string]string{"description": message.Content}},
		{name: "reconstructed_or_summarized_message", messages: []map[string]interface{}{cloneAPIMessage(original)}},
		{name: "changed_original", messages: []map[string]interface{}{original}, wanted: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.wanted == -1 {
				original["content"] = "summary only: old message omitted"
				test.wanted = 0
			}
			body, _ := json.Marshal(map[string]interface{}{"messages": test.messages, "tools": test.tools})
			attempt := agent.capturePromptMemoryInput(ctx, body, test.messages)
			if attempt == nil {
				t.Fatal("fixture request did not produce an input receipt")
			}
			input, err := attempt.collector.service.GetPromptInput(ctx, attempt.collector.owner, attempt.id)
			if err != nil {
				t.Fatal(err)
			}
			if len(input.Sources) != test.wanted {
				t.Fatalf("incorrect history attribution: got=%d expected=%d", len(input.Sources), test.wanted)
			}
		})
	}

	// Once history is removed from the turn, its origin still retains that
	// specific map. Equal new input must not inherit a reclaimed address.
	original = nil
	runtime.GC()
	for i := 0; i < 512; i++ {
		current := map[string]interface{}{"role": "user", "content": message.Content}
		if got := promptMemoryHistoryOrigins(ctx, []map[string]interface{}{current}); len(got) != 1 || got[0] != 0 {
			t.Fatal("a new message acquired removed history's identity")
		}
	}
}

func TestPromptMemoryRuntimeFinalProviderBodiesAndNoUnsentReceipt(t *testing.T) {
	var mu sync.Mutex
	var wireBodies [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		wireBodies = append(wireBodies, body)
		mu.Unlock()
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else if strings.Contains(r.URL.Path, "responses") {
			_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	_, agent, ctx := promptMemoryRuntimeFixture(t, provider.URL)
	agent.httpClient = provider.Client()
	ctx = agent.beginPromptMemoryInput(ctx, "source-web", "fixture-input-task")
	block := "\n[long_term:user:fact; id=fixture; version=1; source_session=source-web]\nquartz exact field\n"
	registerPromptMemoryCandidate(ctx, memorycore.PromptSource{Kind: "entry", Title: "quartz", SourceLabel: "memory_entries#fixture.value", Excerpt: "quartz exact field", Locator: memorycore.PromptLocator{Kind: "entry", ID: "fixture", Version: 1, Field: "value", Digest: promptMemoryDigest("quartz exact field")}}, "user", block)
	messages := []map[string]interface{}{{"role": "user", "content": block + "current input"}}
	if _, err := agent.callLLM(ctx, messages); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.callLLMStream(ctx, messages, nil, func(StreamChunk) {}); err != nil {
		t.Fatal(err)
	}
	agent.modelConfig.Type = "doubao"
	if _, err := agent.callLLM(ctx, messages); err != nil {
		t.Fatal(err)
	}
	inputs := promptMemoryRuntimeInputs(t, agent, ctx)
	if len(inputs) != 3 {
		t.Fatalf("final nonstream/stream/Responses hooks missing: %d", len(inputs))
	}
	for _, input := range inputs {
		if len(input.Sources) != 1 || input.Sources[0].Excerpt != "quartz exact field" {
			t.Fatal("final body matching changed content or lost its source")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	agent.capturePromptMemoryInput(cancelled, []byte(`{"messages":[{"role":"user","content":"quartz exact field"}]}`))
	if _, err := agent.callLLM(cancelled, messages); err == nil {
		t.Fatal("canceled call reached provider")
	}
	if len(promptMemoryRuntimeInputs(t, agent, ctx)) != 3 {
		t.Fatal("a canceled or unsent call acquired a receipt")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(wireBodies) != 3 {
		t.Fatal("receipt generation changed the number of provider calls")
	}
}

func TestPromptMemoryRuntimeFailedHTTPAndTransportAreNotSent(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":"fixture failure, no real billing"}`)
	}))
	defer provider.Close()
	_, agent, ctx := promptMemoryRuntimeFixture(t, provider.URL)
	agent.httpClient = provider.Client()
	ctx = agent.beginPromptMemoryInput(ctx, "source-web", "fixture-input-task")
	messages := []map[string]interface{}{{"role": "user", "content": "fixture unsent source"}}
	if _, err := agent.callLLM(ctx, messages); err == nil {
		t.Fatal("a fake HTTP failure was ignored")
	}
	inputs := promptMemoryRuntimeInputs(t, agent, ctx)
	if len(inputs) != 1 || inputs[0].Status != "failed" {
		t.Fatal("provider rejection was presented as successful delivery")
	}
	agent.httpClient = &http.Client{Transport: promptMemoryFailedTransport{}}
	if _, err := agent.callLLMStream(ctx, messages, nil, func(StreamChunk) {}); err == nil {
		t.Fatal("a fake connection failure was ignored")
	}
	inputs = promptMemoryRuntimeInputs(t, agent, ctx)
	if len(inputs) != 2 || inputs[0].Status != "failed" {
		t.Fatal("connection failure was presented as successful delivery")
	}
}

type promptMemoryFailedTransport struct{}

func (promptMemoryFailedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestPromptMemoryRuntimeRawLocatorsAndFailClosedAudience(t *testing.T) {
	g, agent, ctx := promptMemoryRuntimeFixture(t, "http://127.0.0.1:1")
	ctx = agent.beginPromptMemoryInput(ctx, "source-web", "fixture-input-task")
	quote := "以后 quartz 文档都使用 plain headings。"
	message := ChatMessage{SessionID: "source-web", TenantID: "default", AgentName: "first", UserId: "fixture-user", Role: "user", Content: quote}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz fixture", "quartz "+strings.Repeat("raw-answer ", 200))
	event.Insights = datatypes.JSON(`["quartz fallback insights"]`)
	if err := g.db.Model(&event).Update("insights", event.Insights).Error; err != nil {
		t.Fatal(err)
	}
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", SourceID: strconv.FormatUint(uint64(event.ID), 10), Title: "quartz learned", Summary: "quartz learned summary", Content: "separate raw knowledge content"}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	items, err := agent.webMemorySources(ctx, "source-web")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{"user_statement": promptMemoryDigest(message.Content), "task_execution": promptMemoryDigest(event.FinalAnswer), "task_insight": promptMemoryDigest(string(event.Insights)), "learning_knowledge": promptMemoryDigest(knowledge.Summary)}
	for _, item := range items {
		if digest, ok := wanted[item.Source]; ok {
			if item.Locator.Digest != digest || item.SourceSessionID != "source-web" || item.Source == "task_insight" && item.Locator.Field != "insights" {
				t.Fatalf("sanitized source became a fake original revision: %s %+v", item.Source, item.Locator)
			}
			delete(wanted, item.Source)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing original-source locators: %v", wanted)
	}
	for _, test := range []struct {
		name    string
		ctx     context.Context
		session string
	}{
		{name: "unsealed_web", ctx: context.WithValue(context.Background(), "channel", "web"), session: "source-web"},
		{name: "wrong_session", ctx: ctx, session: "another-session"},
		{name: "isolated_pipeline", ctx: context.WithValue(ctx, "pipeline_step_execution", true), session: "source-web"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Start from a context without a collector. A nested unrelated call
			// cannot borrow the ordinary Web caller's observation authority.
			clean := context.WithValue(test.ctx, promptMemoryCollectorKey{}, (*promptMemoryCollector)(nil))
			if got := agent.beginPromptMemoryInput(clean, test.session, "fixture-input-task"); promptMemoryCollectorFromContext(got) != nil {
				t.Fatal("an unauthorized execution acquired a prompt source collector")
			}
		})
	}
}

func TestPromptMemoryRuntimeToolReadEntersOnlyFollowingModelCall(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"memory-read","type":"function","function":{"name":"agent_memory","arguments":"{\"action\":\"recall\",\"key\":\"unique-old-field\",\"scope\":\"user\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture memory verified"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	_, agent, ctx := promptMemoryRuntimeFixture(t, provider.URL)
	agent.httpClient = provider.Client()
	agent.enabledTools = append(agent.enabledTools, "agent_memory")
	agent.toolRegistry.Register(memorycore.NewAgentMemoryServiceTool(agent.memoryService, agent.memoryOwner, agent.allowMemoryTool))
	owner, _ := agent.memoryOwner(ctx)
	owner.SessionID = "source-web"
	const value = "cobalt \"quoted\"\nmagenta"
	if err := agent.memoryService.Record(ctx, owner, "unique-old-field", value, memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	entry, err := agent.memoryService.Lookup(ctx, owner, "unique-old-field", memorycore.MemoryUser)
	if err != nil || entry == nil {
		t.Fatal("missing fixture entry", err)
	}
	if answer, err := agent.Generate(ctx, "Retrieve the stored record through the memory tool and confirm it."); err != nil || !strings.Contains(answer, "fixture memory verified") {
		t.Fatalf("tool recall did not complete: %q %v", answer, err)
	}
	inputs := promptMemoryRuntimeInputs(t, agent, ctx)
	if len(inputs) != 2 {
		t.Fatalf("tool round receipts: %d", len(inputs))
	}
	for _, input := range inputs {
		found := false
		for _, source := range input.Sources {
			if source.Locator.Kind == "entry" && source.Locator.ID == entry.ID {
				found = true
				if source.Locator.Digest != promptMemoryDigest(value) {
					t.Fatal("tool recall locator hashed JSON representation instead of raw field")
				}
				mu.Lock()
				wire := promptMemoryWireTexts(bodies[input.CallIndex-1])
				mu.Unlock()
				included := false
				for _, text := range wire {
					included = included || text.role == "tool" && strings.Contains(text.text, source.Excerpt)
				}
				if !included {
					t.Fatal("JSON-escaped tool excerpt was not actually in the following provider request")
				}
			}
		}
		if found != (input.CallIndex == 2) {
			mu.Lock()
			for _, text := range promptMemoryWireTexts(bodies[input.CallIndex-1]) {
				if text.role == "tool" {
					t.Logf("fixture tool wire: %q", text.text)
				}
			}
			mu.Unlock()
			t.Fatalf("tool-only source was incorrectly attributed to call %d: found=%t", input.CallIndex, found)
		}
	}
}

func TestPromptMemoryRuntimeSessionLocatorUsesScopeNotOrigin(t *testing.T) {
	g, agent, ctx := promptMemoryRuntimeFixture(t, "http://127.0.0.1:1")
	owner, _ := agent.memoryOwner(ctx)
	owner.SessionID = "source-web"
	if err := agent.memoryService.Record(ctx, owner, "scope-proof", "quartz session working fact", memorycore.MemorySession, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	entry, err := agent.memoryService.Lookup(ctx, owner, "scope-proof", memorycore.MemorySession)
	if err != nil || entry == nil {
		t.Fatal("missing session fixture", err)
	}
	if err := g.db.Model(&memorycore.Entry{}).Where("id = ?", entry.ID).Update("source_session_id", "earlier-origin-session").Error; err != nil {
		t.Fatal(err)
	}
	result, err := agent.memoryService.Recall(ctx, owner, "quartz session working fact", memorycore.DefaultRecallTokens, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.ID == entry.ID {
			if item.Locator.SessionID != owner.SessionID || item.SourceSessionID != "earlier-origin-session" {
				t.Fatalf("session scope confused with record origin: %+v", item)
			}
			if _, err := agent.memoryService.ReadPromptSource(ctx, owner, item.Locator); err != nil {
				t.Fatalf("a legitimate session source could not be read: %v", err)
			}
			return
		}
	}
	t.Fatal("fixture session fact was not recalled")
}
