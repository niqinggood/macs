package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryPartitionIsOwnedByAuthenticatedSubject(t *testing.T) {
	base := context.Background()
	alice := withExecutionIdentity(base, ExecutionIdentity{
		TenantID: "default", SubjectID: "user:alice",
		UserID: "alice", TenantScoped: true,
	})
	bob := withExecutionIdentity(base, ExecutionIdentity{
		TenantID: "default", SubjectID: "user:bob",
		UserID: "bob", TenantScoped: true,
	})

	alicePartition := memoryTenantIDFromContext(alice)
	bobPartition := memoryTenantIDFromContext(bob)
	if alicePartition == defaultExecutionTenantID ||
		bobPartition == defaultExecutionTenantID {
		t.Fatalf(
			"authenticated users fell back to shared default partition: alice=%q bob=%q",
			alicePartition,
			bobPartition,
		)
	}
	if alicePartition == bobPartition {
		t.Fatalf("two users share memory partition %q", alicePartition)
	}
	if again := memoryTenantIDFromContext(alice); again != alicePartition {
		t.Fatalf("personal memory partition is unstable: first=%q again=%q", alicePartition, again)
	}
}

func TestMemoryPartitionKeepsSameSubjectSeparateAcrossTenants(t *testing.T) {
	first := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-a", SubjectID: "user:42",
		TenantScoped: true,
	})
	second := withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: "tenant-b", SubjectID: "user:42",
		TenantScoped: true,
	})
	if left, right := memoryTenantIDFromContext(first), memoryTenantIDFromContext(second); left == right {
		t.Fatalf("same external subject collided across tenants: %q", left)
	}
}

func TestPersonalMemoryAndKnowledgeToolsDoNotCrossSubjects(t *testing.T) {
	database := newTenantMemoryTestDB(t)
	contextFor := func(subjectID string) context.Context {
		return withExecutionIdentity(context.Background(), ExecutionIdentity{
			TenantID: "default", SubjectID: subjectID, UserID: subjectID,
			ServiceID: "shared-frontend", Source: "agent_os",
			Service: true, TenantScoped: true, AgentName: "Researcher",
		})
	}
	alice := contextFor("alice")
	bob := contextFor("bob")
	alicePartition := memoryTenantIDFromContext(alice)
	bobPartition := memoryTenantIDFromContext(bob)

	for _, event := range []MemoryEvent{
		{
			TenantID: alicePartition, AgentName: "Researcher",
			SessionID: "shared-session", UserName: "alice",
			UserQuestion: "personalmarker alice-history-secret",
			FinalAnswer:  "alice-answer", Status: "success",
		},
		{
			TenantID: bobPartition, AgentName: "Researcher",
			SessionID: "shared-session", UserName: "bob",
			UserQuestion: "personalmarker bob-history-secret",
			FinalAnswer:  "bob-answer", Status: "success",
		},
	} {
		if err := database.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}

	history := NewSearchHistoryTool(database)
	aliceHistory, err := history.Execute(alice, map[string]interface{}{
		"query": "personalmarker", "limit": float64(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(aliceHistory, "alice-history-secret") ||
		strings.Contains(aliceHistory, "bob-history-secret") {
		t.Fatalf("Alice history crossed subject boundary: %s", aliceHistory)
	}

	knowledge := NewKnowledgeTool(database, "Researcher")
	for _, item := range []struct {
		ctx     context.Context
		content string
	}{
		{alice, "personalknowledge alice-knowledge-secret"},
		{bob, "personalknowledge bob-knowledge-secret"},
	} {
		if _, err := knowledge.Execute(item.ctx, map[string]interface{}{
			"action": "add", "title": item.content, "content": item.content,
		}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	aliceKnowledge, err := knowledge.Execute(alice, map[string]interface{}{
		"action": "search", "query": "personalknowledge", "get_detail": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(aliceKnowledge, "alice-knowledge-secret") ||
		strings.Contains(aliceKnowledge, "bob-knowledge-secret") {
		t.Fatalf("Alice knowledge crossed subject boundary: %s", aliceKnowledge)
	}

	query := NewKnowledgeQueryTool(database, filepath.Join(t.TempDir(), "alice"))
	aliceQuery, err := query.Execute(alice, map[string]interface{}{
		"query": "personalknowledge", "limit": float64(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(aliceQuery, "alice-knowledge-secret") ||
		strings.Contains(aliceQuery, "bob-knowledge-secret") {
		t.Fatalf("Alice knowledge query crossed subject boundary: %s", aliceQuery)
	}
}
