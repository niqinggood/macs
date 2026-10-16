package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

const serverProtocolVersion = "2025-03-26"

// ToolDefinition is the MCP-facing description of a host tool.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ExecutionResult is the small portion of a host tool result exposed by MCP.
type ExecutionResult struct {
	Content            string
	ExecutionSucceeded *bool
}

// ServerHost supplies application-specific authentication, identity, tool and
// resource behavior while Server owns the MCP transport and JSON-RPC protocol.
type ServerHost interface {
	AuthenticateRequest(*http.Request) (context.Context, error)
	WithRequestedRole(context.Context, string) context.Context
	CanInspectResources(context.Context) bool
	ListTools(context.Context) ([]ToolDefinition, error)
	ExecuteTool(context.Context, string, map[string]interface{}, string) (ExecutionResult, error)
	ReadResource(context.Context, string) (string, error)
	StdioContext() context.Context
	Logf(string, ...interface{})
}

// Server exposes a host's tools and resources over MCP HTTP or stdio.
type Server struct {
	host    ServerHost
	version string
	reqID   atomic.Int64
}

func NewServer(host ServerHost, version string) *Server {
	if version == "" {
		version = "unknown"
	}
	return &Server{host: host, version: version}
}

// ServeHTTP handles the MCP Streamable HTTP request surface.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Auth-Token, X-Caller-ID, X-OpenKitty-Agent")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.host == nil {
		http.Error(w, `{"error":"MCP host unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	requestCtx, err := s.host.AuthenticateRequest(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	requestCtx = s.host.WithRequestedRole(requestCtx, r.Header.Get("X-OpenKitty-Agent"))
	r = r.WithContext(requestCtx)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"read failed"}`, http.StatusBadRequest)
		return
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, 0, -32700, "Parse error")
		return
	}
	s.host.Logf("[MCP] method=%s id=%d", req.Method, req.ID)

	switch req.Method {
	case "initialize":
		s.writeResult(w, req.ID, s.initializeResult(true))
	case "initialized":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	case "tools/list":
		s.handleToolsList(w, r.Context(), req.ID)
	case "tools/call":
		s.handleToolsCall(w, r.Context(), req.ID, body)
	case "resources/list":
		if !s.host.CanInspectResources(r.Context()) {
			s.writeError(w, req.ID, -32003, "Tenant-scoped MCP identities cannot inspect global resources")
			return
		}
		s.writeResult(w, req.ID, map[string]interface{}{"resources": builtinResources()})
	case "resources/read":
		if !s.host.CanInspectResources(r.Context()) {
			s.writeError(w, req.ID, -32003, "Tenant-scoped MCP identities cannot inspect global resources")
			return
		}
		s.handleResourceRead(w, r.Context(), req.ID, body)
	case "prompts/list":
		s.writeResult(w, req.ID, map[string]interface{}{"prompts": builtinPrompts()})
	case "ping":
		s.writeResult(w, req.ID, map[string]interface{}{})
	default:
		s.writeError(w, req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) handleToolsList(w http.ResponseWriter, ctx context.Context, id int64) {
	tools, err := s.host.ListTools(ctx)
	if err != nil {
		s.writeError(w, id, -32003, err.Error())
		return
	}
	s.writeResult(w, id, map[string]interface{}{"tools": tools})
}

func (s *Server) handleToolsCall(w http.ResponseWriter, ctx context.Context, id int64, body []byte) {
	name, arguments, err := decodeToolCall(body)
	if err != nil {
		s.writeError(w, id, -32602, err.Error())
		return
	}
	result, err := s.executeTool(ctx, name, arguments, id)
	if err != nil {
		s.writeResult(w, id, toolErrorPayload(err))
		return
	}
	s.writeResult(w, id, toolSuccessPayload(result))
}

func (s *Server) handleResourceRead(w http.ResponseWriter, ctx context.Context, id int64, body []byte) {
	var request struct {
		Params struct {
			URI string `json:"uri"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Params.URI == "" {
		s.writeError(w, id, -32602, "Invalid params")
		return
	}
	content, err := s.host.ReadResource(ctx, request.Params.URI)
	if err != nil {
		s.writeError(w, id, -32602, err.Error())
		return
	}
	s.writeResult(w, id, map[string]interface{}{
		"contents": []map[string]interface{}{{
			"uri": request.Params.URI, "mimeType": "application/json", "text": content,
		}},
	})
}

func (s *Server) executeTool(ctx context.Context, name string, arguments map[string]interface{}, requestID int64) (ExecutionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	callID := fmt.Sprintf("mcp_%d_%d", requestID, s.reqID.Add(1))
	return s.host.ExecuteTool(ctx, name, arguments, callID)
}

func decodeToolCall(body []byte) (string, map[string]interface{}, error) {
	var request struct {
		Params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return "", nil, fmt.Errorf("Invalid params")
	}
	if request.Params.Name == "" {
		return "", nil, fmt.Errorf("Tool name required")
	}
	return request.Params.Name, request.Params.Arguments, nil
}

func toolSuccessPayload(result ExecutionResult) map[string]interface{} {
	payload := map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": result.Content}},
	}
	if result.ExecutionSucceeded != nil && !*result.ExecutionSucceeded {
		payload["isError"] = true
	}
	return payload
}

func toolErrorPayload(err error) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": fmt.Sprintf("Error: %v", err)}},
		"isError": true,
	}
}

func (s *Server) initializeResult(includeResources bool) map[string]interface{} {
	capabilities := map[string]interface{}{"tools": map[string]interface{}{}}
	if includeResources {
		capabilities["resources"] = map[string]interface{}{}
		capabilities["prompts"] = map[string]interface{}{}
	}
	return map[string]interface{}{
		"protocolVersion": serverProtocolVersion,
		"serverInfo":      map[string]interface{}{"name": "openkitty", "version": s.version},
		"capabilities":    capabilities,
	}
}

func (s *Server) writeResult(w http.ResponseWriter, id int64, result interface{}) {
	response := JSONRPCResponse{JSONRPC: "2.0", ID: id}
	response.Result, _ = json.Marshal(result)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) writeError(w http.ResponseWriter, id int64, code int, message string) {
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0", ID: id, Error: &JSONRPCError{Code: code, Message: message},
	})
}

func builtinResources() []map[string]interface{} {
	return []map[string]interface{}{
		{"uri": "openkitty://sessions", "name": "Active Sessions", "description": "List of active chat sessions", "mimeType": "application/json"},
		{"uri": "openkitty://tasks", "name": "Running Tasks", "description": "Currently running gateway tasks", "mimeType": "application/json"},
		{"uri": "openkitty://digital-employees", "name": "Digital Employees", "description": "Configured AI employees with their pipelines", "mimeType": "application/json"},
		{"uri": "openkitty://skills", "name": "Available Skills", "description": "Loaded skill manifest", "mimeType": "application/json"},
	}
}

func builtinPrompts() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "research", "description": "Academic research — literature search, statistical analysis, paper writing"},
		{"name": "coding", "description": "Code generation, debugging, refactoring, testing"},
		{"name": "business-finance", "description": "Financial analysis, market research, sales intelligence, SEO"},
		{"name": "devops", "description": "Deployment, monitoring, CI/CD, ML engineering"},
	}
}

// HandleStdioRequest processes one MCP JSON-RPC request in stdio mode.
func (s *Server) HandleStdioRequest(req *JSONRPCRequest, rawBody []byte) *JSONRPCResponse {
	if s == nil || s.host == nil {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: -32000, Message: "MCP host unavailable"}}
	}
	switch req.Method {
	case "initialize":
		data, _ := json.Marshal(s.initializeResult(false))
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	case "initialized":
		return nil
	case "tools/list":
		tools, err := s.host.ListTools(s.host.StdioContext())
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: -32003, Message: err.Error()}}
		}
		data, _ := json.Marshal(map[string]interface{}{"tools": tools})
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	case "tools/call":
		name, arguments, err := decodeToolCall(rawBody)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: -32602, Message: "Invalid params"}}
		}
		result, err := s.executeTool(s.host.StdioContext(), name, arguments, req.ID)
		payload := toolSuccessPayload(result)
		if err != nil {
			payload = toolErrorPayload(err)
		}
		data, _ := json.Marshal(payload)
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	case "ping":
		data, _ := json.Marshal(map[string]interface{}{})
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
	default:
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)}}
	}
}

// RunStdio serves newline-delimited MCP JSON-RPC on stdin/stdout.
func RunStdio(server *Server) {
	if server == nil {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if response := server.HandleStdioRequest(&req, line); response != nil {
			data, _ := json.Marshal(response)
			_, _ = os.Stdout.Write(append(data, '\n'))
		}
	}
}
