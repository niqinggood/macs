package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
	"mac/internal/security"
)

func newMemoryServiceTestAgent(t *testing.T) *LLMAgent {
	t.Helper()
	agent, _ := newMemoryServiceTestAgentWithDB(t)
	return agent
}

func newMemoryServiceTestAgentWithDB(t *testing.T) (*LLMAgent, *gorm.DB) {
	t.Helper()
	workspace := projectctx.PersonalWorkspace(t.TempDir(), security.PersonalPartitionID("tenant-a", "alice", defaultExecutionTenantID))
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "memory.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := memorycore.InitMemoryDB(database); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := database.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	service := memorycore.NewService(database, workspace, "assistant")
	service.SetContextStore(scm)
	return &LLMAgent{name: "assistant", workspace: workspace, hasMemory: true, scm: scm, memoryService: service}, database
}

func TestMemoryServiceRuntimeRecallAndAuthenticatedOwner(t *testing.T) {
	agent := newMemoryServiceTestAgent(t)
	ctx := context.WithValue(context.Background(), "session_id", "one")
	ctx = projectctx.WithResolvedWorkspace(ctx, agent.workspace)
	ctx = withExecutionIdentity(ctx, ExecutionIdentity{TenantID: "tenant-a", UserID: "alice", SubjectID: "alice", TenantScoped: true, SubjectScoped: true})
	tool := memorycore.NewAgentMemoryServiceTool(agent.memoryService, agent.memoryOwner, agent.allowMemoryTool)
	if _, err := tool.Execute(memorycore.WithUserTurn(ctx, "请记住 prefer_short_conclusions"), map[string]interface{}{"action": "remember", "key": "format", "value": "prefer_short_conclusions", "scope": "user"}); err != nil {
		t.Fatal(err)
	}
	trustedOwner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.memoryService.SetPinned(ctx, trustedOwner, "format", memorycore.MemoryUser, 1, true); err != nil {
		t.Fatal(err)
	}
	next := context.WithValue(ctx, "session_id", "two")
	if prompt := agent.buildMemoryPrompt(next, "alice", "two", "写一份方案"); !strings.Contains(prompt, "prefer_short_conclusions") || !strings.Contains(prompt, "source_session=one") {
		t.Fatalf("not injected: %s", prompt)
	}
	forged := context.WithValue(next, "user_id", "bob")
	owner, err := agent.memoryOwner(forged)
	if err != nil || owner.SubjectID != "alice" {
		t.Fatalf("untrusted string changed owner: %#v %v", owner, err)
	}
	bob := withExecutionIdentity(next, ExecutionIdentity{TenantID: "tenant-a", UserID: "bob", SubjectID: "bob", TenantScoped: true, SubjectScoped: true})
	if _, err := agent.memoryOwner(bob); err == nil {
		t.Fatal("another subject adopted an existing personal agent workspace")
	}
	if prompt := agent.buildMemoryPrompt(bob, "bob", "two", "写一份方案"); strings.Contains(prompt, "prefer_short_conclusions") {
		t.Fatal("personal memory leaked")
	}
	foreign := projectctx.WithResolvedWorkspace(next, t.TempDir())
	if _, err := agent.memoryOwner(foreign); err == nil {
		t.Fatal("foreign workspace accepted")
	}
	if prompt := agent.buildMemoryPrompt(foreign, "alice", "two", "写一份方案"); prompt != "" {
		t.Fatal("foreign workspace recalled memory")
	}
	agent.hasMemory = false
	if prompt := agent.buildMemoryPrompt(next, "alice", "two", "写一份方案"); prompt != "" {
		t.Fatal("disabled service recalled memory")
	}
	if _, err := tool.Execute(next, map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("disabled service tool read")
	}
	if _, err := tool.Execute(next, map[string]interface{}{"action": "forget", "scope": "user", "key": "format"}); err != nil {
		t.Fatal(err)
	}
	agent.hasMemory = true
	if prompt := agent.buildMemoryPrompt(next, "alice", "two", "写一份方案"); strings.Contains(prompt, "prefer_short_conclusions") {
		t.Fatal("forgotten value recalled")
	}
	isolated := context.WithValue(next, "pipeline_isolated_context", true)
	if _, err := tool.Execute(isolated, map[string]interface{}{"action": "forget", "scope": "user", "key": "format"}); err == nil {
		t.Fatal("isolated task accessed memory")
	}
}

func TestMemoryServiceRuntimeProcessesAndRecallsSummary(t *testing.T) {
	agent := newMemoryServiceTestAgent(t)
	agent.sessionMemory = memorycore.NewSessionMemoryExtractor(agent.workspace, memorycore.DefaultSessionMemoryConfig(), memorycore.SessionExtractorDependencies{
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			return "项目SQLite部署需要保持单机运行", nil
		},
	})
	ctx := context.WithValue(context.Background(), "session_id", "old")
	if err := agent.enqueueMemoryJob(ctx, "old", "summary", memoryProcessingPayload{Messages: []map[string]interface{}{{"role": "user", "content": "SQLite部署"}}}); err != nil {
		t.Fatal(err)
	}
	agent.processMemoryJobs(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if prompt := agent.buildMemoryPrompt(ctx, "", "new", "SQLite部署"); strings.Contains(prompt, "项目SQLite部署") {
			// Wait for the worker to finish before closing its database.
			sem := agent.scm.DerivationSemaphore()
			sem <- struct{}{}
			<-sem
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("completed summary was not indexed and recalled")
}
