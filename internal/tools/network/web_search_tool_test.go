package network

import (
	"context"
	"strings"
	"testing"
)

type recordingSearchDelegate struct {
	params map[string]interface{}
}

func (d *recordingSearchDelegate) Name() string { return "web_fetch" }

func (d *recordingSearchDelegate) Description() string { return "recording delegate" }

func (d *recordingSearchDelegate) Parameters() map[string]interface{} { return nil }

func (d *recordingSearchDelegate) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	d.params = params
	return "ok", nil
}

func TestWebSearchToolDelegatesToWebFetchSearchMode(t *testing.T) {
	delegate := &recordingSearchDelegate{}
	tool := NewWebSearchTool(delegate)

	got, err := tool.Execute(context.Background(), map[string]interface{}{
		"q":           "AI平台 潜在客户",
		"engine":      "bing",
		"max_results": float64(7),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("Execute returned %q", got)
	}
	if delegate.params["query"] != "AI平台 潜在客户" {
		t.Fatalf("query not delegated correctly: %#v", delegate.params)
	}
	if delegate.params["engine"] != "bing" {
		t.Fatalf("engine not delegated correctly: %#v", delegate.params)
	}
	if delegate.params["maxResults"] != float64(7) {
		t.Fatalf("max_results alias not delegated correctly: %#v", delegate.params)
	}
}

func TestWebSearchToolDefersAutomaticEngineRoutingAndRequiresQuery(t *testing.T) {
	delegate := &recordingSearchDelegate{}
	tool := NewWebSearchTool(delegate)

	if _, err := tool.Execute(context.Background(), map[string]interface{}{"query": "  "}); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected query required error, got %v", err)
	}

	if _, err := tool.Execute(context.Background(), map[string]interface{}{"query": "AI平台"}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if _, forced := delegate.params["engine"]; forced {
		t.Fatalf("unspecified engine should be delegated to web_fetch auto-routing: %#v", delegate.params)
	}
}

func TestWebSearchToolSchemaAllowsDocumentedNewsEngine(t *testing.T) {
	tool := NewWebSearchTool(&recordingSearchDelegate{})
	properties, ok := tool.Parameters()["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties schema has unexpected type: %#v", tool.Parameters()["properties"])
	}
	engine, ok := properties["engine"].(map[string]interface{})
	if !ok {
		t.Fatalf("engine schema has unexpected type: %#v", properties["engine"])
	}
	values, ok := engine["enum"].([]string)
	if !ok || !networkTestContainsString(values, "bing_news") {
		t.Fatalf("documented bing_news engine must pass wrapper validation: %#v", engine["enum"])
	}
}

func networkTestContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
