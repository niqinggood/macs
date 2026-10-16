package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// JSON-RPC types (minimal, same structure as mcp_client.go)
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCPInitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

type Capabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ListToolsResult struct {
	Tools []ToolDef `json:"tools"`
}

type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type CallToolResult struct {
	Content []ContentItem `json:"content"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	// Tool definitions: a simple calculator + echo service
	tools := []ToolDef{
		{
			Name:        "add",
			Description: "Add two numbers",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{"type": "number", "description": "First number"},
					"b": map[string]interface{}{"type": "number", "description": "Second number"},
				},
				"required": []string{"a", "b"},
			},
		},
		{
			Name:        "greet",
			Description: "Greet someone by name",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "description": "Name to greet"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "echo",
			Description: "Echo back the input text",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{"type": "string", "description": "Text to echo"},
				},
				"required": []string{"text"},
			},
		},
	}

	send := func(resp Response) {
		data, _ := json.Marshal(resp)
		os.Stderr.WriteString(fmt.Sprintf("[test-mcp] → %s\n", string(data)))
		writer.Write(data)
		writer.WriteByte('\n')
		writer.Flush()
	}

	// MCP lifecycle state
	initialized := false

	for scanner.Scan() {
		line := scanner.Bytes()
		os.Stderr.WriteString(fmt.Sprintf("[test-mcp] ← %s\n", string(line)))

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			send(Response{
				JSONRPC: "2.0",
				ID:      0,
				Error:   &RPCError{Code: -32700, Message: "Parse error: " + err.Error()},
			})
			continue
		}

		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &params)
			initialized = true
			send(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: MCPInitializeResult{
					ProtocolVersion: params.ProtocolVersion,
					Capabilities:    Capabilities{Tools: &struct{}{}},
					ServerInfo:      ServerInfo{Name: "test-mcp-server", Version: "1.0.0"},
				},
			})

		case "notifications/initialized":
			// Just acknowledge silently — notification, no response
			os.Stderr.WriteString("[test-mcp] initialized OK\n")

		case "tools/list":
			if !initialized {
				send(Response{
					JSONRPC: "2.0", ID: req.ID,
					Error: &RPCError{Code: -32000, Message: "Not initialized"},
				})
				continue
			}
			send(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  ListToolsResult{Tools: tools},
			})

		case "tools/call":
			if !initialized {
				send(Response{
					JSONRPC: "2.0", ID: req.ID,
					Error: &RPCError{Code: -32000, Message: "Not initialized"},
				})
				continue
			}

			var params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			}
			json.Unmarshal(req.Params, &params)

			var text string
			switch params.Name {
			case "add":
				a, _ := toFloat64(params.Arguments["a"])
				b, _ := toFloat64(params.Arguments["b"])
				text = fmt.Sprintf("Result: %.2f + %.2f = %.2f", a, b, a+b)

			case "greet":
				name, _ := params.Arguments["name"].(string)
				text = fmt.Sprintf("Hello, %s! Welcome to MCP.", name)

			case "echo":
				txt, _ := params.Arguments["text"].(string)
				text = txt

			default:
				send(Response{
					JSONRPC: "2.0", ID: req.ID,
					Error: &RPCError{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", params.Name)},
				})
				continue
			}

			send(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: CallToolResult{
					Content: []ContentItem{{Type: "text", Text: text}},
				},
			})

		default:
			send(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
			})
		}
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case json.Number:
		f, _ := val.Float64()
		return f, true
	}
	return 0, false
}
