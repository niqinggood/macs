package app

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newBaseChannelTaskTestGateway() *GatewayService {
	return &GatewayService{
		tasks:  make(map[string]*GatewayTask),
		logger: log.New(io.Discard, "", 0),
		engine: &MultiAgentEngine{
			agentFactory: &AgentFactory{
				agents: make(map[string]Agent),
			},
		},
	}
}

func TestBaseChannelProcessTaskAndReplyCleansInterruptOnMissingAgent(t *testing.T) {
	sessionID := "base_channel_missing_agent"
	interruptChannels.Delete(sessionID)
	g := newBaseChannelTaskTestGateway()
	ch := NewBaseChannel("base-test", "test")
	ch.SetGateway(g)
	task := &GatewayTask{
		ID:          "task-missing-agent",
		Request:     &MessageRequest{AgentName: "missing"},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		InterruptCh: make(chan string, 5),
	}
	interruptChannels.Store(sessionID, task.InterruptCh)

	ch.processTaskAndReply(task, "user-1", "recipient-1", "msg-1", sessionID, nil)

	if _, ok := interruptChannels.Load(sessionID); ok {
		t.Fatal("interrupt channel should be removed after missing-agent failure")
	}
	select {
	case result, ok := <-task.ResultCh:
		if !ok {
			t.Fatal("expected failed result before channel close")
		}
		resp, ok := result.(GatewayResponse)
		if !ok || resp.Status != "failed" {
			t.Fatalf("result = %#v, want failed GatewayResponse", result)
		}
	default:
		t.Fatal("expected failed result")
	}
	select {
	case _, ok := <-task.ResultCh:
		if ok {
			t.Fatal("result channel should be closed after failed result")
		}
	default:
		t.Fatal("result channel should be closed after failed result")
	}
	if task.Status != "failed" {
		t.Fatalf("task status = %q, want failed", task.Status)
	}
}

func TestBaseChannelProcessTaskAndReplyPassesContextMetadata(t *testing.T) {
	agent := &contextCapturingAgent{}
	g := &GatewayService{
		tasks:  make(map[string]*GatewayTask),
		logger: log.New(io.Discard, "", 0),
		engine: &MultiAgentEngine{
			agentFactory: &AgentFactory{
				agents: map[string]Agent{"Researcher": agent},
			},
		},
	}
	ch := NewBaseChannel("base-test", "test")
	ch.SetGateway(g)
	task := &GatewayTask{
		ID: "base-task-ctx",
		Request: &MessageRequest{
			TaskID:    "base-task-ctx",
			TaskType:  "chat",
			SessionId: "base-session-ctx",
			Message:   "hello base",
			Channel:   "base-test",
			UserId:    "user-1",
		},
		Status:      "pending",
		CreatedAt:   time.Now(),
		ResultCh:    make(chan interface{}, 1),
		InterruptCh: make(chan string, 5),
	}

	ch.processTaskAndReply(task, "user-1", "recipient-1", "msg-1", "base-session-ctx", nil)

	if agent.input != "hello base" {
		t.Fatalf("agent input = %q, want hello base", agent.input)
	}
	for key, want := range map[string]string{
		"task_id":    "base-task-ctx",
		"session_id": "base-session-ctx",
		"channel":    "base-test",
		"user_id":    "user-1",
	} {
		if got, _ := agent.ctx.Value(key).(string); got != want {
			t.Fatalf("ctx[%s] = %q, want %q", key, got, want)
		}
	}
	select {
	case result, ok := <-task.ResultCh:
		if !ok {
			t.Fatal("expected completed result before channel close")
		}
		resp, ok := result.(GatewayResponse)
		if !ok || resp.Status != "completed" {
			t.Fatalf("result = %#v, want completed GatewayResponse", result)
		}
	default:
		t.Fatal("expected completed result")
	}
}

func TestBaseChannelDoesNotInjectSkillMentionIntoRunningTask(t *testing.T) {
	loader := skillMentionTestLoader()
	agent := &LLMAgent{name: "Researcher", skillsLoader: loader}
	gateway := newBaseChannelTaskTestGateway()
	gateway.engine.agentFactory.agents["Researcher"] = agent
	base := NewBaseChannel("weixin-interrupt-test", "weixin")
	channel := &captureReplyChannel{BaseChannel: base}
	gateway.channelManager = NewChannelManager(gateway)
	if err := gateway.channelManager.RegisterChannel(channel); err != nil {
		t.Fatal(err)
	}

	const sessionID = "weixin_running_skill_mention"
	base.sessions["wx-user"] = sessionID
	interruptCh := make(chan string, 1)
	interruptChannels.Store(sessionID, interruptCh)
	t.Cleanup(func() { interruptChannels.Delete(sessionID) })

	base.ProcessUserMessage("wx-user", "wx-user", "wx-message", "@report build another report", nil)

	if len(interruptCh) != 0 {
		t.Fatal("@Skill mention was injected into the running task")
	}
	if channel.count != 1 || !strings.Contains(channel.content, "独立任务") {
		t.Fatalf("channel warning = count %d content %q", channel.count, channel.content)
	}
}

func TestBaseChannelSkillMentionBindsWorkspacePersistsAndCompletes(t *testing.T) {
	gateway, workspace := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	skillDir := filepath.Join(workspace, "skills", "channel-report")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: channel-report
description: channel Skill Run test
aliases: [channel-report-alias]
type: builtin
---

Create the report from references/checklist.md.`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "checklist.md"), []byte("verify-channel"), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewSkillsLoader(workspace, "Researcher", "")
	if _, err := loader.ReloadUserSkills([]string{filepath.Join(workspace, "skills")}); err != nil {
		t.Fatal(err)
	}
	registry := NewToolRegistry()
	registry.Register(NewUseSkillTool(loader, registry))
	agent := &LLMAgent{
		name: "Researcher", workspace: workspace, skillsLoader: loader,
		toolRegistry: registry, fileLogger: log.New(io.Discard, "", 0),
	}
	// The runtime requires an LLMAgent for host activation, so use a local
	// transport that captures the prompt and returns one terminal response.
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "Channel Skill completed."),
	}}}
	agent.modelConfig = &ModelConfig{Type: "openai", APIKey: "test-api-key-long-enough", ModelName: "test-model", BaseURL: "http://unit.test/v1", MaxTokens: 512}
	agent.httpClient = &http.Client{Transport: transport}
	agent.maxTokens = 512
	agent.maxIterations = 2
	agent.globalConfig = &Config{Workspace: workspace, Runtime: RuntimeConfig{
		LLMCallTimeoutSeconds: 15, LLMMaxRetries: 1, CompressMsgThreshold: 1000, MaxContextChars: 1 << 28,
	}}
	gateway.engine.agentFactory.agents["Researcher"] = agent

	base := NewBaseChannel("weixin-test", "weixin")
	channel := &captureReplyChannel{BaseChannel: base}
	gateway.channelManager = NewChannelManager(gateway)
	if err := gateway.channelManager.RegisterChannel(channel); err != nil {
		t.Fatal(err)
	}

	base.ProcessUserMessage("wx-user", "wx-user", "wx-message", "@channel-report-alias build it", nil)
	deadline := time.Now().Add(8 * time.Second)
	var record SkillRunRecord
	for time.Now().Before(deadline) {
		if err := gateway.db.Where("skill_name = ?", "channel-report").First(&record).Error; err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.TaskID == "" {
		t.Fatal("durable channel Skill Run was not created")
	}
	gateway.tasksMu.RLock()
	task := gateway.tasks[record.TaskID]
	gateway.tasksMu.RUnlock()
	if task == nil {
		t.Fatalf("channel task %s missing from runtime", record.TaskID)
	}
	select {
	case result := <-task.ResultCh:
		response, ok := result.(GatewayResponse)
		if !ok || response.Status != "completed" {
			t.Fatalf("channel Skill result = %#v", result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("channel Skill task did not complete")
	}
	if channel.count == 0 || !strings.Contains(channel.content, "Channel Skill completed") {
		t.Fatalf("channel reply = count %d content %q", channel.count, channel.content)
	}
	if err := gateway.db.Where("task_id = ?", record.TaskID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Phase != skillRunPhaseCompleted || record.ActivationCount != 1 || strings.TrimSpace(record.FrozenRuntimeDir) == "" {
		t.Fatalf("channel Skill Run record = %#v", record)
	}
	if !workspaceContainsPath(workspace, record.FrozenRuntimeDir) {
		t.Fatalf("frozen runtime escaped workspace: %s", record.FrozenRuntimeDir)
	}
	var taskRecord GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", record.TaskID).First(&taskRecord).Error; err != nil {
		t.Fatal(err)
	}
	if taskRecord.Status != "completed" || !sameProjectWorkspace(taskRecord.ResolvedWorkspace, workspace) {
		t.Fatalf("durable channel task = %#v", taskRecord)
	}
	var receipt ToolExecutionReceipt
	if err := gateway.db.Where("task_id = ? AND tool_call_id = ?", record.TaskID, skillRunActivationCallID).First(&receipt).Error; err != nil {
		t.Fatalf("channel activation receipt missing: %v", err)
	}
}
