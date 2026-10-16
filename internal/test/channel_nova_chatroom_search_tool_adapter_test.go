package app

import (
	"net/http/httptest"
	"testing"
	"time"

	knowledgetools "mac/internal/tools/knowledge"
)

func TestNovaChatroomSearchToolRootRegistration(t *testing.T) {
	tool := knowledgetools.NewNovaChatroomSearchTool(t.TempDir())
	if !CoreTools[tool.Name()] {
		t.Fatalf("%s must be a core tool so a pipeline allowlist receives its schema", tool.Name())
	}
	registry := NewToolRegistry()
	registry.Register(tool)
	if _, ok := registry.Get(tool.Name()); !ok {
		t.Fatalf("tool registry did not retain %s", tool.Name())
	}
}

func newTestNovaChatroomSearchTool(workspace string, server *httptest.Server) *knowledgetools.NovaChatroomSearchTool {
	tool := knowledgetools.NewNovaChatroomSearchTool(workspace)
	tool.ConfigureEndpoint(
		server.URL+"/nova/chatroom",
		server.URL+"/nova/chatroom",
		server.Client(),
		time.Second,
		3*time.Second,
	)
	return tool
}
