package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gatewayhttp "mac/internal/gateway/httpx"
)

type agentOSSeedCaptureAgent struct {
	started     chan struct{}
	release     <-chan struct{}
	startedOnce sync.Once
	calls       atomic.Int32

	mu       sync.Mutex
	input    string
	history  []map[string]interface{}
	identity gatewayhttp.AgentOSAssistantIdentity
	hasID    bool
}

func (a *agentOSSeedCaptureAgent) Name() string       { return "Researcher" }
func (a *agentOSSeedCaptureAgent) GetType() AgentType { return AgentTypeCustom }
func (a *agentOSSeedCaptureAgent) Generate(ctx context.Context, input string) (string, error) {
	a.calls.Add(1)
	history, _ := ctx.Value("history_messages").([]map[string]interface{})
	historyCopy := make([]map[string]interface{}, 0, len(history))
	for _, message := range history {
		copyMessage := make(map[string]interface{}, len(message))
		for key, value := range message {
			copyMessage[key] = value
		}
		historyCopy = append(historyCopy, copyMessage)
	}
	identity, hasIdentity := gatewayhttp.AgentOSAssistantIdentityFromContext(ctx)
	a.mu.Lock()
	a.input = input
	a.history = historyCopy
	a.identity = identity
	a.hasID = hasIdentity
	a.mu.Unlock()
	a.startedOnce.Do(func() { close(a.started) })
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "captured", nil
}

func (a *agentOSSeedCaptureAgent) snapshot() (string, []map[string]interface{}, gatewayhttp.AgentOSAssistantIdentity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.input, append([]map[string]interface{}(nil), a.history...), a.identity, a.hasID
}

func stopAgentOSScenarioQueue(t *testing.T, g *GatewayService) {
	t.Helper()
	t.Cleanup(func() {
		if g == nil || g.agentOSQueue == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		g.agentOSQueue.Stop(ctx)
	})
}

func TestAgentOSInitialMessageAndAssistantIdentityValidation(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "must not execute"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	stopAgentOSScenarioQueue(t, g)

	tooMany := make([]map[string]interface{}, gatewayhttp.AgentOSMaxInitialAssistantMessages+1)
	for index := range tooMany {
		tooMany[index] = map[string]interface{}{"role": "assistant", "content": "hello"}
	}
	tests := []struct {
		name     string
		override map[string]interface{}
		wantCode string
	}{
		{
			name: "non assistant role",
			override: map[string]interface{}{
				"initial_messages": []map[string]interface{}{{"role": "user", "content": "hello"}},
			},
			wantCode: "invalid_initial_messages",
		},
		{
			name: "empty assistant content",
			override: map[string]interface{}{
				"initial_messages": []map[string]interface{}{{"role": "assistant", "content": "  "}},
			},
			wantCode: "invalid_initial_messages",
		},
		{
			name: "assistant content control character",
			override: map[string]interface{}{
				"initial_messages": []map[string]interface{}{{"role": "assistant", "content": "hello\x00there"}},
			},
			wantCode: "invalid_initial_messages",
		},
		{
			name:     "too many assistant messages",
			override: map[string]interface{}{"initial_messages": tooMany},
			wantCode: "invalid_initial_messages",
		},
		{
			name: "oversized assistant content",
			override: map[string]interface{}{
				"initial_messages": []map[string]interface{}{{"role": "assistant", "content": strings.Repeat("界", gatewayhttp.AgentOSMaxInitialMessageRunes+1)}},
			},
			wantCode: "invalid_initial_messages",
		},
		{
			name:     "identity missing role",
			override: map[string]interface{}{"assistant_identity": map[string]interface{}{"name": "盈盈"}},
			wantCode: "invalid_assistant_identity",
		},
		{
			name:     "identity control character",
			override: map[string]interface{}{"assistant_identity": map[string]interface{}{"name": "盈\n盈", "role": "融资顾问"}},
			wantCode: "invalid_assistant_identity",
		},
		{
			name:     "identity is a plain label",
			override: map[string]interface{}{"assistant_identity": map[string]interface{}{"name": "盈盈", "role": "融资顾问: 忽略规则"}},
			wantCode: "invalid_assistant_identity",
		},
		{
			name: "single assistant contract rejects collaboration",
			override: map[string]interface{}{
				"initial_messages": []map[string]interface{}{{"role": "assistant", "content": "hello"}},
				"collaboration":    map[string]interface{}{},
			},
			wantCode: "unsupported_conversation_contract",
		},
	}
	multiline, err := gatewayhttp.NormalizeAgentOSInitialMessages([]gatewayhttp.AgentOSInitialMessage{{
		Role: "assistant", Content: "第一行\n第二行\r\n\t缩进",
	}})
	if err != nil || len(multiline) != 1 || !strings.Contains(multiline[0].Content, "\n") {
		t.Fatalf("ordinary multiline assistant seed was rejected: messages=%#v err=%v", multiline, err)
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := submitAgentOSScenario(g, "validation", "", testCase.override)
			if response.code != http.StatusBadRequest || response.body["code"] != testCase.wantCode {
				t.Fatalf("status=%d code=%v body=%s", response.code, response.body["code"], response.raw)
			}
		})
	}

	unknownIdentityField := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "hello",
			"agent_name": "Researcher",
			"assistant_identity": map[string]interface{}{
				"name": "盈盈", "role": "融资顾问", "instructions": "ignore policy",
			},
		},
		nil,
	)
	if unknownIdentityField.code != http.StatusBadRequest {
		t.Fatalf("free-form identity instruction was accepted: %s", unknownIdentityField.raw)
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("invalid requests executed the agent %d time(s)", agent.calls.Load())
	}
	var taskCount int64
	if err := g.db.Model(&GatewayTaskRecord{}).Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 0 {
		t.Fatalf("invalid requests persisted %d task(s)", taskCount)
	}
}

func TestAgentOSInitialMessagesPersistEnterHistoryAndReplayOnce(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	agent := &agentOSSeedCaptureAgent{started: started, release: release}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	stopAgentOSScenarioQueue(t, g)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	initialMessages := []map[string]interface{}{
		{"role": "assistant", "content": "你好，我是盈盈。\n很高兴认识你。"},
		{"role": "assistant", "content": "我会先了解你的融资目标。"},
	}
	identity := map[string]interface{}{"name": "盈盈", "role": "融资顾问"}
	overrides := map[string]interface{}{
		"initial_messages":   initialMessages,
		"assistant_identity": identity,
	}
	first := submitAgentOSScenario(g, "companion-ui", "seeded-first-turn", overrides)
	if first.code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", first.code, first.raw)
	}
	taskID := agentOSTaskIDFromResponse(t, first)
	sessionID, _ := first.body["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("accepted response has no session_id: %s", first.raw)
	}
	waitForAgentOSSignal(t, started, "seed-aware agent start")

	var messages []ChatMessage
	if err := g.db.Where("session_id = ?", sessionID).
		Order("created_at ASC, id ASC").
		Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("persisted messages=%+v, want two seeds and current user", messages)
	}
	wantRoles := []string{"assistant", "assistant", "user"}
	wantContents := []string{"你好，我是盈盈。\n很高兴认识你。", "我会先了解你的融资目标。", "solve a complex task"}
	for index := range messages {
		if messages[index].Role != wantRoles[index] || messages[index].Content != wantContents[index] {
			t.Fatalf("message[%d]=%+v want role=%q content=%q", index, messages[index], wantRoles[index], wantContents[index])
		}
		if index > 0 && !messages[index-1].CreatedAt.Before(messages[index].CreatedAt) {
			t.Fatalf("message timestamps are not strictly ordered: %+v", messages)
		}
	}
	if messages[0].TaskID != "" || messages[1].TaskID != "" || messages[2].TaskID != taskID {
		t.Fatalf("seed/current task IDs are not isolated: %+v", messages)
	}
	if messages[0].AgentName != "盈盈" || messages[1].AgentName != "盈盈" {
		t.Fatalf("seed messages lost display identity: %+v", messages)
	}

	var session ChatSession
	if err := g.db.Where("id = ?", sessionID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.MessageCount != 3 {
		t.Fatalf("session message_count=%d, want 3", session.MessageCount)
	}

	var record GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	recovered := gatewayTaskFromRecord(record)
	if recovered.Request.AssistantIdentity == nil ||
		recovered.Request.AssistantIdentity.Name != "盈盈" ||
		recovered.Request.AssistantIdentity.Role != "融资顾问" ||
		len(recovered.Request.InitialMessages) != 2 {
		t.Fatalf("durable request did not recover seed contract: %+v", recovered.Request)
	}
	var reservationCount int64
	if err := g.db.Model(&AgentTaskRequestRecord{}).Where("task_id = ?", taskID).Count(&reservationCount).Error; err != nil {
		t.Fatal(err)
	}
	if reservationCount != 1 {
		t.Fatalf("idempotency reservations=%d, want 1", reservationCount)
	}

	input, history, capturedIdentity, hasIdentity := agent.snapshot()
	if !strings.Contains(input, "solve a complex task") {
		t.Fatalf("current user turn was not passed as current input: %q", input)
	}
	if !hasIdentity || capturedIdentity.Name != "盈盈" || capturedIdentity.Role != "融资顾问" {
		t.Fatalf("execution identity=%+v present=%t", capturedIdentity, hasIdentity)
	}
	if len(history) != 2 {
		t.Fatalf("model history=%#v, want two assistant seeds", history)
	}
	for index, message := range history {
		if message["role"] != "assistant" || message["content"] != wantContents[index] {
			t.Fatalf("history[%d]=%#v", index, message)
		}
	}

	replay := submitAgentOSScenario(g, "companion-ui", "seeded-first-turn", overrides)
	if replay.code != http.StatusOK || replay.body["idempotent_replay"] != true || agentOSTaskIDFromResponse(t, replay) != taskID {
		t.Fatalf("idempotent replay mismatch: code=%d body=%s", replay.code, replay.raw)
	}
	changedSeed := submitAgentOSScenario(g, "companion-ui", "seeded-first-turn", map[string]interface{}{
		"initial_messages":   []map[string]interface{}{{"role": "assistant", "content": "不同的欢迎语"}},
		"assistant_identity": identity,
	})
	if changedSeed.code != http.StatusConflict || changedSeed.body["code"] != "idempotency_payload_mismatch" {
		t.Fatalf("changed seed reused idempotency key: code=%d body=%s", changedSeed.code, changedSeed.raw)
	}
	changedIdentity := submitAgentOSScenario(g, "companion-ui", "seeded-first-turn", map[string]interface{}{
		"initial_messages":   initialMessages,
		"assistant_identity": map[string]interface{}{"name": "晶晶", "role": "潜客挖掘顾问"},
	})
	if changedIdentity.code != http.StatusConflict || changedIdentity.body["code"] != "idempotency_payload_mismatch" {
		t.Fatalf("changed identity reused idempotency key: code=%d body=%s", changedIdentity.code, changedIdentity.raw)
	}
	var beforeCompletionCount int64
	if err := g.db.Model(&ChatMessage{}).Where("session_id = ?", sessionID).Count(&beforeCompletionCount).Error; err != nil {
		t.Fatal(err)
	}
	if beforeCompletionCount != 3 {
		t.Fatalf("replay duplicated transcript rows: count=%d", beforeCompletionCount)
	}

	releaseOnce.Do(func() { close(release) })
	_ = waitForAgentOSTask(t, g, taskID, "completed")
	if agent.calls.Load() != 1 {
		t.Fatalf("replay executed agent %d times, want 1", agent.calls.Load())
	}
}

func TestAgentOSInitialMessagesRejectNonEmptySession(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "first answer"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	stopAgentOSScenarioQueue(t, g)

	first := submitAgentOSScenario(g, "companion-ui", "existing-session-first", nil)
	if first.code != http.StatusAccepted {
		t.Fatalf("first submit status=%d body=%s", first.code, first.raw)
	}
	_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, first), "completed")
	sessionID, _ := first.body["session_id"].(string)
	var beforeMessages int64
	if err := g.db.Model(&ChatMessage{}).Where("session_id = ?", sessionID).Count(&beforeMessages).Error; err != nil {
		t.Fatal(err)
	}

	second := submitAgentOSScenario(g, "companion-ui", "existing-session-seed", map[string]interface{}{
		"session_id": sessionID,
		"initial_messages": []map[string]interface{}{
			{"role": "assistant", "content": "This must not be inserted."},
		},
		"assistant_identity": map[string]interface{}{"name": "盈盈", "role": "融资顾问"},
	})
	if second.code != http.StatusConflict || second.body["code"] != "initial_messages_require_empty_session" {
		t.Fatalf("non-empty seed status=%d body=%s", second.code, second.raw)
	}
	var afterMessages int64
	if err := g.db.Model(&ChatMessage{}).Where("session_id = ?", sessionID).Count(&afterMessages).Error; err != nil {
		t.Fatal(err)
	}
	if afterMessages != beforeMessages {
		t.Fatalf("rejected seed changed transcript count: before=%d after=%d", beforeMessages, afterMessages)
	}
	var taskCount int64
	if err := g.db.Model(&GatewayTaskRecord{}).Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 || agent.calls.Load() != 1 {
		t.Fatalf("rejected seed persisted/executed: tasks=%d calls=%d", taskCount, agent.calls.Load())
	}
}

func TestAgentOSAssistantIdentityBuildsDynamicSystemContract(t *testing.T) {
	identity := gatewayhttp.AgentOSAssistantIdentity{Name: "盈盈", Role: "融资顾问"}
	dynamic := gatewayhttp.BuildAgentOSAssistantIdentitySystemPrompt(identity)
	for _, required := range []string{
		`"name":"盈盈"`,
		`"role":"融资顾问"`,
		"display metadata only",
		"OpenKitty is the host platform, not your name",
		"Do not introduce yourself as generic OpenKitty",
	} {
		if !strings.Contains(dynamic, required) {
			t.Fatalf("dynamic identity prompt is missing %q: %s", required, dynamic)
		}
	}
	static := gatewayhttp.HostedAgentOSIdentityStaticContract("follow the published role", "")
	if !strings.Contains(static, "overrides internal agent labels") ||
		strings.Contains(static, "user-facing product identity is OpenKitty") {
		t.Fatalf("hosted static identity contract conflicts with display identity: %s", static)
	}
}

func TestAutoGenAssistantReceivesHostedIdentityAndRoleAwareHistory(t *testing.T) {
	type providerRequest struct {
		Messages []map[string]string `json:"messages"`
	}
	requestCh := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request providerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		requestCh <- request
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{
				"message": map[string]interface{}{"content": "ok"},
			}},
		})
	}))
	defer server.Close()

	agent := NewAutoGenAssistant(AgentConfig{
		Name: "test-autogen", SystemMessage: "Follow the published business role.", MaxTokens: 128,
	}, &ModelConfig{
		ModelName: "test-model", BaseURL: server.URL,
	}, log.New(io.Discard, "", 0))
	ctx := gatewayhttp.WithAgentOSAssistantIdentity(
		context.Background(),
		&gatewayhttp.AgentOSAssistantIdentity{Name: "盈盈", Role: "融资顾问"},
	)
	ctx = context.WithValue(ctx, "history_messages", []map[string]interface{}{
		{"role": "assistant", "content": "欢迎语第一段"},
		{"role": "assistant", "content": "欢迎语第二段"},
	})
	if _, err := agent.Generate(ctx, "你是谁"); err != nil {
		t.Fatal(err)
	}
	var request providerRequest
	select {
	case request = <-requestCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not receive request")
	}
	if len(request.Messages) != 4 {
		t.Fatalf("provider messages=%#v, want system + two assistant seeds + current user", request.Messages)
	}
	wantRoles := []string{"system", "assistant", "assistant", "user"}
	wantContents := []string{"", "欢迎语第一段", "欢迎语第二段", "你是谁"}
	for index, message := range request.Messages {
		if message["role"] != wantRoles[index] {
			t.Fatalf("message[%d] role=%q want=%q: %#v", index, message["role"], wantRoles[index], request.Messages)
		}
		if index > 0 && message["content"] != wantContents[index] {
			t.Fatalf("message[%d] content=%q want=%q", index, message["content"], wantContents[index])
		}
	}
	system := request.Messages[0]["content"]
	for _, required := range []string{`"name":"盈盈"`, `"role":"融资顾问"`, "display metadata only"} {
		if !strings.Contains(system, required) {
			t.Fatalf("system identity is missing %q: %s", required, system)
		}
	}
	if strings.Contains(system, "user-facing product identity is OpenKitty") {
		t.Fatalf("generic OpenKitty identity leaked into hosted identity: %s", system)
	}
}

func TestAgentOSExplicitAIEmployeeRetainsHostedIdentityAndSeedHistory(t *testing.T) {
	g := newAEHTTPTestGateway(t)
	capture := &agentOSSeedCaptureAgent{started: make(chan struct{})}
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(AgentConfig, string) (Agent, error) {
			return capture, nil
		},
	}
	g.pipelineExec = NewPipelineExecutor(factory, g, log.New(io.Discard, "", 0))

	task := &GatewayTask{
		ID:        "agt_explicit_employee_identity",
		QueueName: agentOSQueueName,
		Request: &MessageRequest{
			TaskID: "agt_explicit_employee_identity", TaskType: "chat",
			SessionId: "explicit-employee-session", Message: "@潜客研究员 你是谁",
			AssistantIdentity: &gatewayhttp.AgentOSAssistantIdentity{Name: "晶晶", Role: "潜客挖掘顾问"},
		},
	}
	ctx := context.WithValue(context.Background(), "task_id", task.ID)
	ctx = gatewayhttp.WithAgentOSAssistantIdentity(ctx, task.Request.AssistantIdentity)
	ctx = context.WithValue(ctx, "history_messages", []map[string]interface{}{
		{"role": "assistant", "content": "你好，我是晶晶。"},
	})
	cfg, employeeTask, _ := matchAgentOSExplicitAIEmployee(g.aeManager, task.Request.Message)
	if cfg == nil {
		t.Fatal("explicit AI employee mention did not match")
	}
	result, err := g.runAgentOSExplicitAIEmployee(ctx, task, cfg, employeeTask)
	if err != nil {
		t.Fatalf("explicit AI employee run failed: %v", err)
	}
	if !strings.Contains(result, "captured") {
		t.Fatalf("explicit AI employee result=%q", result)
	}
	input, history, identity, hasIdentity := capture.snapshot()
	if !hasIdentity || identity.Name != "晶晶" || identity.Role != "潜客挖掘顾问" {
		t.Fatalf("pipeline model identity=%+v present=%t", identity, hasIdentity)
	}
	if len(history) != 1 || history[0]["role"] != "assistant" || history[0]["content"] != "你好，我是晶晶。" {
		t.Fatalf("pipeline model history=%#v", history)
	}
	if !strings.Contains(input, "你是谁") {
		t.Fatalf("pipeline current user turn was lost: %q", input)
	}
}

func TestAgentOSAssistantIdentityContextIsTaskLocal(t *testing.T) {
	base := context.Background()
	left := gatewayhttp.WithAgentOSAssistantIdentity(base, &gatewayhttp.AgentOSAssistantIdentity{Name: "盈盈", Role: "融资顾问"})
	right := gatewayhttp.WithAgentOSAssistantIdentity(base, &gatewayhttp.AgentOSAssistantIdentity{Name: "晶晶", Role: "潜客挖掘顾问"})
	leftIdentity, leftOK := gatewayhttp.AgentOSAssistantIdentityFromContext(left)
	rightIdentity, rightOK := gatewayhttp.AgentOSAssistantIdentityFromContext(right)
	if _, ok := gatewayhttp.AgentOSAssistantIdentityFromContext(base); ok {
		t.Fatal("hosted identity mutated the parent context")
	}
	if !leftOK || leftIdentity.Name != "盈盈" || leftIdentity.Role != "融资顾问" {
		t.Fatalf("left task identity=%+v present=%t", leftIdentity, leftOK)
	}
	if !rightOK || rightIdentity.Name != "晶晶" || rightIdentity.Role != "潜客挖掘顾问" {
		t.Fatalf("right task identity=%+v present=%t", rightIdentity, rightOK)
	}
}

func TestAgentOSFingerprintIncludesSeedsAndAssistantIdentity(t *testing.T) {
	base := agentOSTaskSubmitRequest{Message: "hello"}
	baseHash, err := agentOSRequestFingerprint(base, "Researcher", "revision", "user", "subject")
	if err != nil {
		t.Fatal(err)
	}
	withSeed := base
	withSeed.InitialMessages = []gatewayhttp.AgentOSInitialMessage{{Role: "assistant", Content: "welcome"}}
	seedHash, err := agentOSRequestFingerprint(withSeed, "Researcher", "revision", "user", "subject")
	if err != nil {
		t.Fatal(err)
	}
	withIdentity := base
	withIdentity.AssistantIdentity = &gatewayhttp.AgentOSAssistantIdentity{Name: "盈盈", Role: "融资顾问"}
	identityHash, err := agentOSRequestFingerprint(withIdentity, "Researcher", "revision", "user", "subject")
	if err != nil {
		t.Fatal(err)
	}
	if baseHash == seedHash || baseHash == identityHash || seedHash == identityHash {
		t.Fatalf("new request fields did not produce distinct fingerprints: base=%s seed=%s identity=%s", baseHash, seedHash, identityHash)
	}
}
