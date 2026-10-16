package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"mac/internal/toolcontract"
	plugintools "mac/internal/tools/plugins"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var mcpTestLogger = log.New(os.Stderr, "[mcp-test] ", log.LstdFlags)

func buildTestServerBinary(t *testing.T) string {
	t.Helper()
	exeName := "mcp_test_server"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exe := filepath.Join(t.TempDir(), exeName)

	output, err := runGo(20*time.Second, "build", "-o", exe, "./mcp_test_server")
	if err != nil {
		t.Fatalf("build test server: %v\n%s", err, output)
	}
	return exe
}

func TestMCP_StdioFullLifecycle(t *testing.T) {
	exe := buildTestServerBinary(t)

	transport, err := NewStdioTransport(exe, nil, nil, mcpTestLogger)
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer transport.Close()

	client := NewMCPClient(transport, mcpTestLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: Initialize
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	info := client.ServerInfo()
	if info.Name != "test-mcp-server" {
		t.Errorf("expected server name 'test-mcp-server', got '%s'", info.Name)
	}
	if !client.HasTools() {
		t.Fatal("server should support tools")
	}

	// Step 2: List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	for _, name := range []string{"add", "greet", "echo"} {
		if !toolNames[name] {
			t.Errorf("expected tool '%s' not found", name)
		}
	}

	// Step 3: Call each tool
	tests := []struct {
		name   string
		args   map[string]interface{}
		expect string
	}{
		{"add", map[string]interface{}{"a": 3, "b": 4}, "7.00"},
		{"add", map[string]interface{}{"a": 10.5, "b": 20.3}, "30.80"},
		{"greet", map[string]interface{}{"name": "Kitty"}, "Hello, Kitty!"},
		{"echo", map[string]interface{}{"text": "hello world"}, "hello world"},
	}

	for _, tt := range tests {
		result, err := client.CallTool(ctx, tt.name, tt.args)
		if err != nil {
			t.Errorf("%s(%v): %v", tt.name, tt.args, err)
			continue
		}
		if len(result.Content) == 0 || result.Content[0].Type != "text" {
			t.Errorf("%s: expected text content, got %d items", tt.name, len(result.Content))
			continue
		}
		text := result.Content[0].Text
		if !contains(text, tt.expect) {
			t.Errorf("%s: expected '%s' in result, got '%s'", tt.name, tt.expect, text)
		}
		t.Logf("✅ %s(%v) = %s", tt.name, tt.args, text)
	}
}

func TestMCP_ToolAdapter(t *testing.T) {
	exe := buildTestServerBinary(t)

	transport, _ := NewStdioTransport(exe, nil, nil, mcpTestLogger)
	defer transport.Close()

	client := NewMCPClient(transport, mcpTestLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client.Initialize(ctx)
	tools, _ := client.ListTools(ctx)

	// Wrap each tool and test Name/Description/Parameters compatibility
	for _, def := range tools {
		tool := plugintools.NewMCPTool(def, client, mcpTestLogger)

		// Verify Tool interface compliance
		if tool.Name() != "mcp_"+def.Name {
			t.Errorf("tool name mismatch: expected mcp_%s, got %s", def.Name, tool.Name())
		}
		if tool.Description() == "" {
			t.Errorf("tool %s has empty description", tool.Name())
		}
		params := tool.Parameters()
		if params == nil || params["type"] != "object" {
			t.Errorf("tool %s has invalid parameters", tool.Name())
		}
	}
}

func TestMCP_AddTool(t *testing.T) {
	exe := buildTestServerBinary(t)

	transport, _ := NewStdioTransport(exe, nil, nil, mcpTestLogger)
	defer transport.Close()

	client := NewMCPClient(transport, mcpTestLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client.Initialize(ctx)
	tools, _ := client.ListTools(ctx)

	addTool := plugintools.NewMCPTool(tools[0], client, mcpTestLogger)

	result, err := addTool.Execute(ctx, map[string]interface{}{"a": 7, "b": 8})
	if err != nil {
		t.Fatalf("execute add: %v", err)
	}
	if !contains(result, "15.00") {
		t.Errorf("7+8 should equal 15.00, got: %s", result)
	}
}

func TestMCP_Manager(t *testing.T) {
	exe := buildTestServerBinary(t)

	mgr := NewMCPManager(mcpTestLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := mgr.Connect(ctx, MCPServerConfig{
		Name:    "test",
		Command: exe,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer mgr.DisconnectAll()

	// Check tools were registered
	tools := mgr.GetTools()
	if len(tools) != 3 {
		t.Errorf("expected 3 tools from manager, got %d", len(tools))
	}

	// Use a tool through the manager
	addTool, ok := mgr.GetTool("mcp_add")
	if !ok {
		t.Fatal("mcp_add not found in manager")
	}

	result, err := addTool.Execute(ctx, map[string]interface{}{"a": 100, "b": 200})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !contains(result, "300.00") {
		t.Errorf("100+200 should equal 300.00, got: %s", result)
	}
	if client := mgr.GetClient("test"); client == nil || client.TransportType() != "stdio" {
		t.Fatalf("manager did not use SDK stdio transport: %#v", client)
	}
}

func TestMCP_ManagerStreamableHTTP(t *testing.T) {
	server := newSDKMCPTestServer()
	var sawAuth atomic.Bool
	var sawCustomHeader atomic.Bool
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return server },
		&sdkmcp.StreamableHTTPOptions{JSONResponse: true},
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		sawAuth.Store(true)
		if r.Header.Get("X-OpenKitty-Test") == "present" {
			sawCustomHeader.Store(true)
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	mgr := NewMCPManager(mcpTestLogger)
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	err := mgr.Connect(connectCtx, MCPServerConfig{
		Name:           "remote",
		Transport:      "streamable_http",
		URL:            httpServer.URL,
		Token:          "test-token",
		Headers:        map[string]string{"X-OpenKitty-Test": "present"},
		TimeoutSeconds: 2,
		Enabled:        true,
	})
	cancelConnect()
	if err != nil {
		t.Fatalf("connect Streamable HTTP: %v", err)
	}
	defer mgr.DisconnectAll()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mgr.GetClient("remote")
	if client == nil {
		t.Fatal("remote client not registered")
	}
	if got := client.TransportType(); got != "streamable_http" {
		t.Fatalf("transport = %q, want streamable_http", got)
	}
	if got := client.ProtocolVersion(); got != MCPProtocolVersion {
		t.Fatalf("protocol = %q, want %q", got, MCPProtocolVersion)
	}
	if !client.HasResources() {
		t.Fatal("resource capability was not preserved")
	}
	if !sawAuth.Load() || !sawCustomHeader.Load() {
		t.Fatalf("headers not forwarded: auth=%t custom=%t", sawAuth.Load(), sawCustomHeader.Load())
	}

	echo, ok := mgr.GetTool("mcp_echo")
	if !ok {
		t.Fatal("mcp_echo not registered")
	}
	output, err := echo.Execute(ctx, map[string]interface{}{"text": "streamable"})
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if output != "streamable" {
		t.Fatalf("echo output = %q", output)
	}

	read, err := client.ReadResource(ctx, "memory://welcome")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(read.Contents) != 1 || read.Contents[0].Text != "hello from MCP" {
		t.Fatalf("unexpected resource: %#v", read)
	}

	registry := newTestMCPRegistry()
	mgr.BindRegistry(registry)
	if _, ok := registry.Get("mcp_echo"); !ok {
		t.Fatal("bound ToolRegistry did not receive mcp_echo")
	}
	server.AddTool(&sdkmcp.Tool{
		Name:        "new_tool",
		Description: "Added after initialization",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "new"}},
		}, nil
	})
	if err := mgr.RefreshTools(ctx, "remote"); err != nil {
		t.Fatalf("refresh tools: %v", err)
	}
	if _, ok := registry.Get("mcp_new_tool"); !ok {
		t.Fatal("ToolRegistry did not receive dynamically added tool")
	}
}

func TestMCP_ManagerLegacySSEAndAutoFallback(t *testing.T) {
	for _, transport := range []string{"sse", "auto"} {
		t.Run(transport, func(t *testing.T) {
			server := newSDKMCPTestServer()
			handler := sdkmcp.NewSSEHandler(
				func(*http.Request) *sdkmcp.Server { return server },
				nil,
			)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			mgr := NewMCPManager(mcpTestLogger)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := mgr.Connect(ctx, MCPServerConfig{
				Name:      "legacy",
				Transport: transport,
				URL:       httpServer.URL,
				Enabled:   true,
			})
			if err != nil {
				t.Fatalf("connect legacy SSE: %v", err)
			}
			defer mgr.DisconnectAll()

			client := mgr.GetClient("legacy")
			if client == nil || client.TransportType() != "sse" {
				t.Fatalf("transport = %#v, want sse", client)
			}
			echo, ok := mgr.GetTool("mcp_echo")
			if !ok {
				t.Fatal("mcp_echo not registered")
			}
			output, err := echo.Execute(ctx, map[string]interface{}{"text": transport})
			if err != nil {
				t.Fatalf("call echo: %v", err)
			}
			if output != transport {
				t.Fatalf("echo output = %q, want %q", output, transport)
			}
		})
	}
}

func TestMCP_ManagerAvoidsToolNameCollisions(t *testing.T) {
	exe := buildTestServerBinary(t)
	mgr := NewMCPManager(mcpTestLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer mgr.DisconnectAll()

	for _, name := range []string{"one", "two"} {
		if err := mgr.Connect(ctx, MCPServerConfig{
			Name:      name,
			Command:   exe,
			Transport: "stdio",
			Enabled:   true,
		}); err != nil {
			t.Fatalf("connect %s: %v", name, err)
		}
	}
	for _, name := range []string{"mcp_add", "mcp_two_add"} {
		if _, ok := mgr.GetTool(name); !ok {
			t.Errorf("expected collision-safe tool %q", name)
		}
	}
}

func newSDKMCPTestServer() *sdkmcp.Server {
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "sdk-test-server", Version: "1.0.0"},
		nil,
	)
	server.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{"type": "string"},
			},
			"required": []string{"text"},
		},
	}, func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: args.Text}},
		}, nil
	})
	server.AddResource(&sdkmcp.Resource{
		URI:      "memory://welcome",
		Name:     "welcome",
		MIMEType: "text/plain",
	}, func(_ context.Context, request *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{{
				URI:      request.Params.URI,
				MIMEType: "text/plain",
				Text:     "hello from MCP",
			}},
		}, nil
	})
	return server
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type testMCPRegistry struct {
	tools map[string]toolcontract.Tool
}

func newTestMCPRegistry() *testMCPRegistry {
	return &testMCPRegistry{tools: make(map[string]toolcontract.Tool)}
}

func (r *testMCPRegistry) Register(tool toolcontract.Tool) {
	r.tools[tool.Name()] = tool
}

func (r *testMCPRegistry) Unregister(name string) {
	delete(r.tools, name)
}

func (r *testMCPRegistry) Get(name string) (toolcontract.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// runGo runs a go command with a hard timeout and returns combined output for diagnostics.
func runGo(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("go %v timed out after %s", args, timeout)
	}
	return string(output), err
}
