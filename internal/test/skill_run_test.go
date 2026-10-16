package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSkillRunTestHarness(t *testing.T, name, document string, supportFiles map[string]string) (*GatewayService, *GatewayTask, *LLMAgent, string) {
	t.Helper()
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(document), 0644); err != nil {
		t.Fatal(err)
	}
	for relative, content := range supportFiles {
		path := filepath.Join(skillDir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	loader := NewSkillsLoader(workspace, "Assistant", "")
	if _, err := loader.ReloadUserSkills([]string{filepath.Join(workspace, "skills")}); err != nil {
		t.Fatalf("load Skill: %v", err)
	}
	registry := NewToolRegistry()
	registry.Register(NewUseSkillTool(loader, registry))
	agent := &LLMAgent{
		name: "Assistant", workspace: workspace, skillsLoader: loader,
		toolRegistry: registry, fileLogger: log.New(io.Discard, "", 0),
	}
	db := openAgentOSScenarioDB(t)
	if err := db.AutoMigrate(&SkillRunRecord{}); err != nil {
		t.Fatalf("migrate Skill Run record: %v", err)
	}
	gateway := &GatewayService{
		db: db, logger: log.New(io.Discard, "", 0),
		tasks: make(map[string]*GatewayTask), taskStreams: make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
	}
	task := &GatewayTask{
		ID: "skill-run-" + name, Status: "running", ResolvedWorkspace: workspace,
		CreatedAt: time.Now(),
		Request: &MessageRequest{
			TaskType: "skill", Message: "produce the requested result", ResolvedWorkspace: workspace,
			SkillRun: &SkillRunRequest{SkillName: name},
		},
	}
	return gateway, task, agent, skillDir
}

func skillRunTestContext(dbGateway *GatewayService, task *GatewayTask) context.Context {
	ctx := withResolvedWorkspace(context.Background(), task.ResolvedWorkspace)
	ctx = context.WithValue(ctx, "task_id", task.ID)
	return withToolExecutionLedger(ctx, newGormToolExecutionLedger(dbGateway.db))
}

func TestSkillRunFreezesActivationDefaultsAndCompletesAtomically(t *testing.T) {
	document := `---
name: guided-report
description: deterministic report workflow
version: 1.2.3
type: builtin
parameters:
  - name: target
    type: string
    required: true
  - name: tone
    type: string
    default: concise
---

ORIGINAL-SKILL-INSTRUCTIONS
Create the requested report and return a link to it.`
	gateway, task, agent, skillDir := newSkillRunTestHarness(t, "guided-report", document, nil)
	task.Request.SkillRun.Version = "1.2.3"
	task.Request.SkillRun.Parameters = map[string]interface{}{"target": "market"}

	bound, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent)
	if err != nil {
		t.Fatalf("begin Skill Run: %v", err)
	}
	runContext := skillRunContextFromContext(bound)
	if runContext == nil || runContext.Snapshot.Version != "1.2.3" {
		t.Fatalf("missing frozen run context: %#v", runContext)
	}
	if got := runContext.Parameters["tone"]; got != "concise" {
		t.Fatalf("default parameter = %#v, want concise", got)
	}
	prompt := skillRunPrompt(bound)
	for _, want := range []string{
		"ORIGINAL-SKILL-INSTRUCTIONS",
		"1.2.3",
		runContext.Snapshot.ContentSHA256,
		"authoritative domain and output contract",
		"silently compare the draft",
		"read that reference before answering",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("frozen prompt missing %q: %s", want, prompt)
		}
	}

	var record SkillRunRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Phase != skillRunPhaseRunning || record.ActivationCount != 1 {
		t.Fatalf("checkpoint after activation = phase %q count %d", record.Phase, record.ActivationCount)
	}
	var toolReceipt ToolExecutionReceipt
	if err := gateway.db.Where("task_id = ? AND tool_call_id = ?", task.ID, skillRunActivationCallID).First(&toolReceipt).Error; err != nil {
		t.Fatalf("activation did not pass through the durable tool ledger: %v", err)
	}
	if toolReceipt.Status != "succeeded" || !toolReceipt.ReadOnly {
		t.Fatalf("activation receipt = status %q read_only=%v", toolReceipt.Status, toolReceipt.ReadOnly)
	}

	updated := strings.Replace(document, "ORIGINAL-SKILL-INSTRUCTIONS", "UPDATED-SKILL-INSTRUCTIONS", 1)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.skillsLoader.ReloadUserSkills(nil); err != nil {
		t.Fatal(err)
	}
	restored, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent)
	if err != nil {
		t.Fatalf("restore Skill Run: %v", err)
	}
	restoredPrompt := skillRunPrompt(restored)
	if !strings.Contains(restoredPrompt, "ORIGINAL-SKILL-INSTRUCTIONS") || strings.Contains(restoredPrompt, "UPDATED-SKILL-INSTRUCTIONS") {
		t.Fatalf("restart replaced the frozen activation: %s", restoredPrompt)
	}
	var activationReceipts int64
	if err := gateway.db.Model(&ToolExecutionReceipt{}).
		Where("task_id = ? AND tool_call_id = ?", task.ID, skillRunActivationCallID).
		Count(&activationReceipts).Error; err != nil {
		t.Fatal(err)
	}
	if activationReceipts != 1 {
		t.Fatalf("activation receipts = %d, want exactly one", activationReceipts)
	}

	if err := os.WriteFile(filepath.Join(task.ResolvedWorkspace, "output.md"), []byte("done"), 0644); err != nil {
		t.Fatal(err)
	}
	terminal, applied := gateway.finishGatewayTask(task, "completed", "[report](output.md)", nil)
	if !applied || terminal == nil || terminal.Status != "completed" {
		t.Fatalf("terminal transition: applied=%v task=%#v", applied, terminal)
	}
	var terminalTask GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&terminalTask).Error; err != nil {
		t.Fatal(err)
	}
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if terminalTask.Status != "completed" || record.Phase != skillRunPhaseCompleted {
		t.Fatalf("non-atomic terminal state: task=%q skill=%q", terminalTask.Status, record.Phase)
	}
	receipt := skillRunReceiptFromRecord(&record)
	if len(receipt.Artifacts) != 1 || receipt.Artifacts[0] != "output.md" {
		t.Fatalf("receipt artifacts = %#v", receipt.Artifacts)
	}
}

func TestSkillRunContractUsesDynamicSystemLayerWithoutChangingStaticPrompt(t *testing.T) {
	document := `---
name: system-layer-skill
description: prompt placement test
version: 1.0.0
type: builtin
---

SYSTEM-LAYER-SKILL-INSTRUCTIONS`
	gateway, task, agent, _ := newSkillRunTestHarness(t, "system-layer-skill", document, nil)
	bound, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent)
	if err != nil {
		t.Fatalf("begin Skill Run: %v", err)
	}
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "Skill task completed with a final deliverable."),
	}}}
	agent.modelConfig = &ModelConfig{
		Type: "openai", APIKey: "test-api-key-long-enough", ModelName: "test-model",
		BaseURL: "http://unit.test/v1", MaxTokens: 512,
	}
	agent.httpClient = &http.Client{Transport: transport}
	agent.maxTokens = 512
	agent.maxIterations = 2
	agent.globalConfig = &Config{Workspace: task.ResolvedWorkspace, Runtime: RuntimeConfig{
		LLMCallTimeoutSeconds: 15, LLMMaxRetries: 1,
		CompressMsgThreshold: 1000, MaxContextChars: 1 << 28,
	}}

	staticBefore := agent.buildSystemPromptStatic()
	if _, err := agent.Generate(bound, "produce the requested result"); err != nil {
		t.Fatalf("Generate under Skill Run: %v", err)
	}
	staticAfter := agent.buildSystemPromptStatic()
	if staticAfter != staticBefore {
		t.Fatal("per-turn Skill binding changed the cacheable global system prompt")
	}

	transport.mu.Lock()
	requests := append([][]byte(nil), transport.requests...)
	transport.mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("model request was not captured")
	}
	var request struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(requests[0], &request); err != nil {
		t.Fatalf("decode model request: %v", err)
	}
	if len(request.Messages) < 3 {
		t.Fatalf("model messages = %#v", request.Messages)
	}
	staticMessage := stringValue(request.Messages[0]["content"])
	dynamicMessage := stringValue(request.Messages[1]["content"])
	if strings.Contains(staticMessage, "Host-bound Skill Run") ||
		!strings.Contains(dynamicMessage, "Host-bound Skill Run") ||
		!strings.Contains(dynamicMessage, "SYSTEM-LAYER-SKILL-INSTRUCTIONS") {
		t.Fatalf("Skill contract system placement: static=%q dynamic=%q", staticMessage, dynamicMessage)
	}
	for _, message := range request.Messages {
		if message["role"] == "user" && strings.Contains(stringValue(message["content"]), "Host-bound Skill Run") {
			t.Fatalf("Skill contract leaked into user layer: %#v", message)
		}
	}
}

func TestLegacyProcessTaskSkillRunPersistsWorkspaceReceiptAndTerminalState(t *testing.T) {
	document := `---
name: websocket-report
description: legacy WebSocket Skill Run test
type: builtin
---

WEBSOCKET-SKILL-INSTRUCTIONS`
	gateway, task, agent, _ := newSkillRunTestHarness(t, "websocket-report", document, map[string]string{
		"references/checklist.md": "verify-websocket",
	})
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "WebSocket Skill completed."),
	}}}
	agent.modelConfig = &ModelConfig{
		Type: "openai", APIKey: "test-api-key-long-enough", ModelName: "test-model",
		BaseURL: "http://unit.test/v1", MaxTokens: 512,
	}
	agent.httpClient = &http.Client{Transport: transport}
	agent.maxTokens = 512
	agent.maxIterations = 2
	agent.globalConfig = &Config{Workspace: task.ResolvedWorkspace, Runtime: RuntimeConfig{
		LLMCallTimeoutSeconds: 15, LLMMaxRetries: 1,
		CompressMsgThreshold: 1000, MaxContextChars: 1 << 28,
	}}
	gateway.config = agent.globalConfig
	gateway.project = NewProjectContext(task.ResolvedWorkspace)
	gateway.engine = &MultiAgentEngine{
		config: gateway.config,
		agentFactory: &AgentFactory{
			agents: map[string]Agent{"Assistant": agent}, globalConfig: gateway.config,
			workspace: task.ResolvedWorkspace, fileLogger: log.New(io.Discard, "", 0),
		},
	}
	gateway.initSSE()
	task.Request.SessionId = "ws-skill-session"
	task.Request.Channel = "ws"
	task.Request.UserId = "ws-user"
	task.Request.ResolvedWorkspace = task.ResolvedWorkspace
	task.ResultCh = make(chan interface{}, 1)
	if err := gateway.db.Create(&ChatSession{
		ID: task.Request.SessionId, Name: task.Request.SessionId,
		Workspace: task.ResolvedWorkspace, ProjectID: gateway.project.ProjectID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatal(err)
	}
	gateway.tasks[task.ID] = task

	gateway.processTask(task)

	result := <-task.ResultCh
	response, ok := result.(GatewayResponse)
	if !ok || response.Status != "completed" || response.SkillRun == nil || response.SkillRun.Phase != skillRunPhaseCompleted {
		t.Fatalf("legacy processTask Skill result = %#v", result)
	}
	var taskRecord GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&taskRecord).Error; err != nil {
		t.Fatal(err)
	}
	if taskRecord.Status != "completed" || !sameProjectWorkspace(taskRecord.ResolvedWorkspace, task.ResolvedWorkspace) {
		t.Fatalf("legacy durable task = %#v", taskRecord)
	}
	var runRecord SkillRunRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&runRecord).Error; err != nil {
		t.Fatal(err)
	}
	if runRecord.Phase != skillRunPhaseCompleted || runRecord.ActivationCount != 1 ||
		!workspaceContainsPath(task.ResolvedWorkspace, runRecord.FrozenRuntimeDir) {
		t.Fatalf("legacy durable Skill Run = %#v", runRecord)
	}
	var receipt ToolExecutionReceipt
	if err := gateway.db.Where("task_id = ? AND tool_call_id = ?", task.ID, skillRunActivationCallID).First(&receipt).Error; err != nil {
		t.Fatalf("legacy activation receipt missing: %v", err)
	}
}

func TestSkillRunTerminalTransactionRollsBackBothRecords(t *testing.T) {
	document := `---
name: atomic-skill
description: terminal transaction test
version: 1.0.0
type: builtin
---

Complete the task.`
	gateway, task, agent, _ := newSkillRunTestHarness(t, "atomic-skill", document, nil)
	if _, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent); err != nil {
		t.Fatalf("begin Skill Run: %v", err)
	}
	if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
		t.Fatalf("persist running task: %v", err)
	}
	if err := gateway.db.Exec(`
		CREATE TRIGGER reject_skill_run_completion
		BEFORE UPDATE OF phase ON skill_run_records
		WHEN OLD.task_id = 'skill-run-atomic-skill'
			AND NEW.phase = 'completed'
		BEGIN
			SELECT RAISE(ABORT, 'forced Skill Run terminal persistence failure');
		END
	`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	terminal, applied := gateway.finishGatewayTask(task, "completed", "done", nil)
	if applied || terminal == nil || terminal.Status != "running" || task.Status != "running" {
		t.Fatalf("uncommitted terminal state was published: applied=%v terminal=%#v task=%#v", applied, terminal, task)
	}
	var taskRecord GatewayTaskRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&taskRecord).Error; err != nil {
		t.Fatal(err)
	}
	var runRecord SkillRunRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&runRecord).Error; err != nil {
		t.Fatal(err)
	}
	if taskRecord.Status != "running" || runRecord.Phase != skillRunPhaseRunning {
		t.Fatalf("terminal transaction split: task=%q skill=%q", taskRecord.Status, runRecord.Phase)
	}
}

func TestSkillRunRejectsInvalidVersionAndParametersBeforeActivation(t *testing.T) {
	document := `---
name: parameterized-skill
description: validates host parameters
version: 2.0.0
type: builtin
parameters:
  - name: target
    type: string
    required: true
---

Use the target parameter.`
	gateway, task, agent, _ := newSkillRunTestHarness(t, "parameterized-skill", document, nil)
	task.Request.SkillRun.Version = "1.0.0"
	task.Request.SkillRun.Parameters = map[string]interface{}{"target": "alpha"}
	if _, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent); err == nil || !strings.Contains(err.Error(), "version conflict") {
		t.Fatalf("version mismatch error = %v", err)
	}

	task.ID += "-missing"
	task.Request.SkillRun.Version = "2.0.0"
	task.Request.SkillRun.Parameters = map[string]interface{}{}
	if _, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent); err == nil || !strings.Contains(err.Error(), "parameters.target is required") {
		t.Fatalf("missing parameter error = %v", err)
	}
	var records int64
	if err := gateway.db.Model(&SkillRunRecord{}).Count(&records).Error; err != nil {
		t.Fatal(err)
	}
	if records != 0 {
		t.Fatalf("invalid requests persisted %d Skill Run records", records)
	}
}

func TestSkillRunFreezesRuntimeBundleAndMarksStop(t *testing.T) {
	document := `---
name: runtime-skill
description: workflow with a helper script
version: 3.0.0
type: builtin
---

RUNTIME-SKILL-INSTRUCTIONS
Use scripts/helper.py when needed.`
	gateway, task, agent, skillDir := newSkillRunTestHarness(t, "runtime-skill", document, map[string]string{
		"scripts/helper.py": "print('ORIGINAL-RUNTIME')\n",
	})
	bound, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent)
	if err != nil {
		t.Fatalf("begin runtime Skill Run: %v", err)
	}
	runContext := skillRunContextFromContext(bound)
	if runContext == nil || strings.TrimSpace(runContext.FrozenRuntimeDir) == "" {
		t.Fatalf("runtime directory was not frozen: %#v", runContext)
	}
	frozenHelper := filepath.Join(runContext.FrozenRuntimeDir, "scripts", "helper.py")
	data, err := os.ReadFile(frozenHelper)
	if err != nil || !strings.Contains(string(data), "ORIGINAL-RUNTIME") {
		t.Fatalf("read frozen helper: data=%q err=%v", data, err)
	}
	if !strings.Contains(skillRunPrompt(bound), "Host-frozen Skill bundle") {
		t.Fatal("activation output does not identify the authoritative frozen runtime")
	}
	var toolReceipt ToolExecutionReceipt
	if err := gateway.db.Where("task_id = ? AND tool_call_id = ?", task.ID, skillRunActivationCallID).First(&toolReceipt).Error; err != nil {
		t.Fatal(err)
	}
	if toolReceipt.ReadOnly {
		t.Fatal("runtime-materializing activation was incorrectly recorded as read-only")
	}

	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "helper.py"), []byte("print('UPDATED-RUNTIME')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.skillsLoader.ReloadUserSkills(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent); err != nil {
		t.Fatalf("restore frozen runtime after installed Skill update: %v", err)
	}
	data, err = os.ReadFile(frozenHelper)
	if err != nil || strings.Contains(string(data), "UPDATED-RUNTIME") {
		t.Fatalf("installed update changed in-flight runtime: data=%q err=%v", data, err)
	}

	gateway.tasks[task.ID] = task
	stopped, found, changed := gateway.stopGatewayTask(task.ID)
	if !found || !changed || stopped == nil || stopped.Status != "stopped" {
		t.Fatalf("stop result: found=%v changed=%v task=%#v", found, changed, stopped)
	}
	var record SkillRunRecord
	if err := gateway.db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Phase != skillRunPhaseStopped || record.CompletedAt == nil {
		t.Fatalf("stopped checkpoint = phase %q completed_at=%v", record.Phase, record.CompletedAt)
	}
}

func TestSkillRunFreezesReferenceOnlyBundle(t *testing.T) {
	document := `---
name: reference-skill
description: workflow with a reference but no executable script
version: 1.0.0
type: builtin
---

Read references/rules.md and follow the original rules.`
	gateway, task, agent, skillDir := newSkillRunTestHarness(t, "reference-skill", document, map[string]string{
		"references/rules.md": "ORIGINAL-REFERENCE\n",
	})
	bound, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent)
	if err != nil {
		t.Fatalf("begin reference-only Skill Run: %v", err)
	}
	runContext := skillRunContextFromContext(bound)
	if runContext == nil || runContext.Snapshot.RequiresRuntime || !runContext.Snapshot.HasSupportFiles || runContext.FrozenRuntimeDir == "" {
		t.Fatalf("reference-only bundle was not frozen: %#v", runContext)
	}
	frozenReference := filepath.Join(runContext.FrozenRuntimeDir, "references", "rules.md")
	if err := os.WriteFile(filepath.Join(skillDir, "references", "rules.md"), []byte("UPDATED-REFERENCE\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.skillsLoader.ReloadUserSkills(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.beginSkillRun(skillRunTestContext(gateway, task), task, agent); err != nil {
		t.Fatalf("restore reference-only Skill Run: %v", err)
	}
	data, err := os.ReadFile(frozenReference)
	if err != nil || string(data) != "ORIGINAL-REFERENCE\n" {
		t.Fatalf("frozen reference changed: data=%q err=%v", data, err)
	}
}
