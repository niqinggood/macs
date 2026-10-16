package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTenantMemoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&MemoryEvent{},
		&UserProfile{},
		&Knowledge{},
		&ToolSkillStat{},
		&AgentLog{},
		&RAGKb{},
	); err != nil {
		t.Fatal(err)
	}
	return database
}

func tenantMemoryTestContext(tenantID string) context.Context {
	return withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID:     tenantID,
		UserID:       "service:research",
		ServiceID:    "research",
		AgentName:    "Researcher",
		Source:       "agent_os",
		Service:      true,
		TenantScoped: true,
	})
}

func TestTenantMemoryAndKnowledgeToolsDoNotCrossPartitions(t *testing.T) {
	database := newTenantMemoryTestDB(t)
	store := NewMemoryStore(
		filepath.Join(t.TempDir(), "memory"),
		database,
		"Researcher",
		10,
	)
	contextA := tenantMemoryTestContext("tenant-a")
	contextB := tenantMemoryTestContext("tenant-b")
	partitionA := memoryTenantIDFromContext(contextA)
	partitionB := memoryTenantIDFromContext(contextB)
	for _, event := range []MemoryEvent{
		{
			TenantID: partitionA, AgentName: "Researcher", SessionID: "tenant-a-session",
			UserName: "shared-user", UserQuestion: "sharedmarker tenant-a-history-secret",
			FinalAnswer: "tenant-a-answer", Status: "success",
		},
		{
			TenantID: partitionB, AgentName: "Researcher", SessionID: "tenant-b-session",
			UserName: "shared-user", UserQuestion: "sharedmarker tenant-b-history-secret",
			FinalAnswer: "tenant-b-answer", Status: "success",
		},
		{
			TenantID: defaultExecutionTenantID, AgentName: "Researcher", SessionID: "local-session",
			UserName: "local", UserQuestion: "localmarker local-history",
			FinalAnswer: "local-answer", Status: "success",
		},
	} {
		if err := database.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}

	history := NewSearchHistoryTool(database)
	resultA, err := history.Execute(contextA, map[string]interface{}{
		"query": "sharedmarker", "limit": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultA, "tenant-a-history-secret") ||
		strings.Contains(resultA, "tenant-b-history-secret") {
		t.Fatalf("tenant A history boundary failed: %s", resultA)
	}

	localResult, err := history.Execute(context.Background(), map[string]interface{}{
		"query": "localmarker", "limit": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(localResult, "local-history") {
		t.Fatalf("legacy local history compatibility failed: %s", localResult)
	}

	knowledge := NewKnowledgeTool(database, "Researcher")
	for _, tc := range []struct {
		ctx     context.Context
		content string
	}{
		{contextA, "sharedknowledge tenant-a-knowledge-secret"},
		{contextB, "sharedknowledge tenant-b-knowledge-secret"},
		{context.Background(), "localknowledge local-knowledge"},
	} {
		if _, err := knowledge.Execute(tc.ctx, map[string]interface{}{
			"action": "add", "title": tc.content, "content": tc.content,
		}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}

	knowledgeA, err := knowledge.Execute(contextA, map[string]interface{}{
		"action": "search", "query": "sharedknowledge", "get_detail": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(knowledgeA, "tenant-a-knowledge-secret") ||
		strings.Contains(knowledgeA, "tenant-b-knowledge-secret") {
		t.Fatalf("tenant A knowledge boundary failed: %s", knowledgeA)
	}

	queryTool := NewKnowledgeQueryTool(database, filepath.Join(t.TempDir(), "tenant-a"))
	queryA, err := queryTool.Execute(contextA, map[string]interface{}{
		"query": "sharedknowledge", "limit": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(queryA, "tenant-a-knowledge-secret") ||
		strings.Contains(queryA, "tenant-b-knowledge-secret") {
		t.Fatalf("tenant A knowledge_query boundary failed: %s", queryA)
	}

	ftsResults := store.SearchMemoryFTSForTenant(partitionA, "sharedmarker", "", 10)
	if len(ftsResults) != 1 || !strings.Contains(ftsResults[0].UserQuestion, "tenant-a-history-secret") {
		t.Fatalf("tenant-scoped FTS results = %#v", ftsResults)
	}
	if got := store.GetMemoryContextForTenant(
		partitionA, "shared-user", "tenant-a-session", "",
	); strings.Contains(got, "tenant-b-history-secret") {
		t.Fatalf("automatic memory context crossed tenant boundary: %s", got)
	}
}

func TestTenantKnowledgeBaseIDsRejectMixedTenantSet(t *testing.T) {
	database := newTenantMemoryTestDB(t)
	kbA := RAGKb{TenantID: "tenant-a", Slug: "a", Name: "A"}
	kbB := RAGKb{TenantID: "tenant-b", Slug: "b", Name: "B"}
	if err := database.Create(&kbA).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&kbB).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := tenantKnowledgeBaseIDs(database, "tenant-a", []uint{kbA.ID, kbB.ID}); err == nil {
		t.Fatal("mixed-tenant KB list was accepted")
	}
	ids, err := tenantKnowledgeBaseIDs(database, "tenant-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != kbA.ID {
		t.Fatalf("tenant KB discovery = %v, want [%d]", ids, kbA.ID)
	}
}

func TestAgentMemoryFilesystemRemainsWorkspaceScoped(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	storeA := NewAgentMemoryStore(workspaceA, "Researcher")
	storeB := NewAgentMemoryStore(workspaceB, "Researcher")
	storeA.Remember("customer", "tenant-a-only", MemoryProject, 5, nil, "session-a")

	if got := storeB.Recall("customer"); got != "" {
		t.Fatalf("agent memory crossed workspaces: %q", got)
	}
	if got := storeA.Recall("customer"); got != "tenant-a-only" {
		t.Fatalf("tenant A memory missing: %q", got)
	}
}
