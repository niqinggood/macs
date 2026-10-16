package knowledge

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestRAGSearchToolAuthorizesCompleteTenantSet(t *testing.T) {
	var authorizedTenant string
	var authorizedIDs []uint
	var searchedIDs []uint
	tool := NewRAGSearchTool(
		[]uint{7, 3},
		"configured-tenant",
		func(ids []uint, query, sessionID, sourceDoc string, limit int) ([]RAGResult, error) {
			searchedIDs = append([]uint(nil), ids...)
			if query != "退货政策" || sessionID != "session-1" || limit != 8 {
				t.Fatalf("unexpected search inputs: query=%q session=%q limit=%d", query, sessionID, limit)
			}
			return []RAGResult{{SourceDoc: "policy.md", Content: "七天内可以退货。", Score: 9}}, nil
		},
		func(context.Context) (string, bool) { return "sealed-tenant", true },
		func(tenant string, ids []uint) ([]uint, error) {
			authorizedTenant = tenant
			authorizedIDs = append([]uint(nil), ids...)
			return []uint{3, 7}, nil
		},
	)
	ctx := context.WithValue(context.Background(), "session_id", "session-1")
	result, err := tool.Execute(ctx, map[string]interface{}{"query": "退货政策", "top_k": float64(8)})
	if err != nil {
		t.Fatalf("execute RAG search: %v", err)
	}
	if authorizedTenant != "sealed-tenant" || !reflect.DeepEqual(authorizedIDs, []uint{7, 3}) || !reflect.DeepEqual(searchedIDs, []uint{3, 7}) {
		t.Fatalf("tenant authorization boundary was not preserved: tenant=%q requested=%v searched=%v", authorizedTenant, authorizedIDs, searchedIDs)
	}
	if !strings.Contains(result, "policy.md") || !strings.Contains(result, "七天内可以退货") {
		t.Fatalf("unexpected formatted result: %q", result)
	}
}

func TestRAGSearchToolDoesNotSearchWithoutKnowledgeBase(t *testing.T) {
	searched := false
	tool := NewRAGSearchTool(nil, "", func([]uint, string, string, string, int) ([]RAGResult, error) {
		searched = true
		return nil, nil
	}, nil, nil)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "anything"})
	if err != nil {
		t.Fatalf("execute empty RAG search: %v", err)
	}
	if searched || !strings.Contains(result, "未配置知识库") {
		t.Fatalf("unexpected empty configuration result: searched=%v result=%q", searched, result)
	}
}

func TestRAGSearchToolFailsClosedWhenTenantAuthorizationMissing(t *testing.T) {
	tool := NewRAGSearchTool([]uint{1}, "", nil, func(context.Context) (string, bool) {
		return "tenant-a", true
	}, nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{"query": "anything"})
	if err == nil || !strings.Contains(err.Error(), "authorization is unavailable") {
		t.Fatalf("expected authorization failure, got %v", err)
	}
}
