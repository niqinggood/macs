package mcp

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"mac/internal/toolcontract"
)

type MCPListToolsResult struct {
	Tools []MCPToolDef `json:"tools"`
}

type MCPToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type MCPCallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type MCPCallToolResult struct {
	Content []MCPContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type MCPContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
}

type MCPToolClient interface {
	ServerName() string
	TransportType() string
	Endpoint() string
	CallTool(context.Context, string, map[string]interface{}) (*MCPCallToolResult, error)
}

// MCPTool adapts a remotely advertised MCP tool to the shared Tool contract.
type MCPTool struct {
	definition   MCPToolDef
	client       MCPToolClient
	logger       *log.Logger
	registryName string
}

func NewMCPTool(definition MCPToolDef, client MCPToolClient, logger *log.Logger) *MCPTool {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &MCPTool{definition: definition, client: client, logger: logger}
}

func (t *MCPTool) SetRegistryName(name string) { t.registryName = name }

func (t *MCPTool) UsesClient(client MCPToolClient) bool {
	return t != nil && t.client == client
}

func (t *MCPTool) Name() string {
	if t.registryName != "" {
		return t.registryName
	}
	return "mcp_" + t.definition.Name
}

func (t *MCPTool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	capabilities := toolcontract.SandboxCapabilities{Network: true}
	if t.client != nil {
		capabilities.Process = t.client.TransportType() == "stdio"
		if endpoint := strings.TrimSpace(t.client.Endpoint()); endpoint != "" {
			capabilities.NetworkTargets = []string{endpoint}
		}
	}
	return capabilities
}

func (t *MCPTool) Description() string {
	serverName := ""
	if t.client != nil {
		serverName = t.client.ServerName()
	}
	if t.definition.Description == "" {
		return fmt.Sprintf("MCP tool '%s' from %s", t.definition.Name, serverName)
	}
	return fmt.Sprintf("[MCP:%s] %s", serverName, t.definition.Description)
}

func (t *MCPTool) Parameters() map[string]interface{} {
	if t.definition.InputSchema != nil {
		return t.definition.InputSchema
	}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (t *MCPTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.client == nil {
		return "", fmt.Errorf("MCP client is not available")
	}
	t.logger.Printf("[MCP] Calling %s/%s with params: %v", t.client.ServerName(), t.definition.Name, params)
	result, err := t.client.CallTool(ctx, t.definition.Name, params)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for index, item := range result.Content {
		switch item.Type {
		case "text":
			output.WriteString(item.Text)
		case "image":
			output.WriteString(fmt.Sprintf("[image: %s, %d bytes]", item.MimeType, len(item.Data)))
		case "resource":
			switch {
			case item.Text != "":
				output.WriteString(item.Text)
			case item.URI != "":
				output.WriteString(fmt.Sprintf("[resource: %s %s]", item.MimeType, item.URI))
			default:
				output.WriteString(fmt.Sprintf("[resource: %s]", item.MimeType))
			}
		case "audio":
			output.WriteString(fmt.Sprintf("[audio: %s, %d bytes]", item.MimeType, decodedContentSize(item.Data)))
		default:
			output.WriteString(fmt.Sprintf("[%s content]", item.Type))
		}
		if index < len(result.Content)-1 {
			output.WriteString("\n")
		}
	}
	return output.String(), nil
}
