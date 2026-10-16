package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mac/internal/configvalue"
)

type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type MCPListResourcesResult struct {
	Resources []MCPResource `json:"resources"`
}

type MCPReadResourceResult struct {
	Contents []MCPResourceContent `json:"contents"`
}

type MCPResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type MCPResourceClient interface {
	ListResources(context.Context) ([]MCPResource, error)
	ReadResource(context.Context, string) (*MCPReadResourceResult, error)
}

type MCPResourceProvider interface {
	ResourceClients() map[string]MCPResourceClient
}

type MCPListResourcesTool struct{ provider MCPResourceProvider }

func NewMCPListResourcesTool(provider MCPResourceProvider) *MCPListResourcesTool {
	return &MCPListResourcesTool{provider: provider}
}

func (t *MCPListResourcesTool) Name() string { return "mcp_list_resources" }
func (t *MCPListResourcesTool) Description() string {
	return "List resources from connected MCP servers. Returns URIs and descriptions."
}
func (t *MCPListResourcesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{"type": "string", "description": "MCP server name (empty = all)"},
		},
	}
}
func (t *MCPListResourcesTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.provider == nil {
		return "", fmt.Errorf("MCP resource provider is not available")
	}
	serverFilter := configvalue.String(params, "server", "")
	clients := t.provider.ResourceClients()
	names := sortedClientNames(clients)
	var output strings.Builder
	for _, name := range names {
		if serverFilter != "" && name != serverFilter {
			continue
		}
		resources, err := clients[name].ListResources(ctx)
		if err != nil {
			output.WriteString(fmt.Sprintf("**%s**: error listing resources: %v\n", name, err))
			continue
		}
		output.WriteString(fmt.Sprintf("## %s (%d resources)\n\n", name, len(resources)))
		for _, resource := range resources {
			output.WriteString(fmt.Sprintf("- **%s** `%s` — %s\n", resource.Name, resource.URI, resource.Description))
		}
	}
	return output.String(), nil
}

type MCPReadResourceTool struct{ provider MCPResourceProvider }

func NewMCPReadResourceTool(provider MCPResourceProvider) *MCPReadResourceTool {
	return &MCPReadResourceTool{provider: provider}
}

func (t *MCPReadResourceTool) Name() string { return "mcp_read_resource" }
func (t *MCPReadResourceTool) Description() string {
	return "Read the content of an MCP resource by its URI. Use mcp_list_resources first to find URIs."
}
func (t *MCPReadResourceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"uri": map[string]interface{}{"type": "string", "description": "Resource URI from mcp_list_resources"},
		},
		"required": []string{"uri"},
	}
}
func (t *MCPReadResourceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.provider == nil {
		return "", fmt.Errorf("MCP resource provider is not available")
	}
	uri := configvalue.String(params, "uri", "")
	if uri == "" {
		return "", fmt.Errorf("uri required")
	}
	clients := t.provider.ResourceClients()
	for _, name := range sortedClientNames(clients) {
		result, err := clients[name].ReadResource(ctx, uri)
		if err != nil {
			continue
		}
		var output strings.Builder
		for _, item := range result.Contents {
			if item.Text != "" {
				output.WriteString(item.Text)
			} else {
				output.WriteString(fmt.Sprintf("[%s blob, %d bytes]", item.MimeType, len(item.Blob)))
			}
		}
		return output.String(), nil
	}
	return "", fmt.Errorf("resource not found: %s", uri)
}

func sortedClientNames(clients map[string]MCPResourceClient) []string {
	names := make([]string, 0, len(clients))
	for name := range clients {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
