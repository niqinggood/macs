package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mac/internal/toolcontract"
)

// MCPProtocolVersion is the newest published MCP version supported by the
// official Go SDK used by OpenKitty.
const MCPProtocolVersion = "2025-11-25"

type sdkMCPState struct {
	client   *sdkmcp.Client
	session  *sdkmcp.ClientSession
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
}

func (s *sdkMCPState) close() error {
	s.once.Do(func() {
		if s.session != nil {
			s.closeErr = s.session.Close()
		}
		if s.cancel != nil {
			s.cancel()
		}
	})
	return s.closeErr
}

// connectSDKMCPClient establishes a production MCP connection using the
// official SDK. This gives OpenKitty protocol negotiation, concurrent JSON-RPC
// dispatch, Streamable HTTP sessions, SSE parsing, and graceful stdio shutdown.
func connectSDKMCPClient(
	ctx context.Context,
	cfg MCPServerConfig,
	clientVersion string,
	logger *log.Logger,
	onToolsChanged func(context.Context),
) (*MCPClient, error) {
	logger = ensureMCPLogger(logger)
	if cfg.Command != "" && cfg.URL != "" {
		return nil, errors.New("command and url are mutually exclusive")
	}
	if cfg.Command == "" && cfg.URL == "" {
		return nil, errors.New("must specify command or url")
	}

	handshakeCtx := ctx
	cancelHandshake := func() {}
	if cfg.TimeoutSeconds > 0 {
		handshakeCtx, cancelHandshake = context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	}
	defer cancelHandshake()

	options := &sdkmcp.ClientOptions{
		Capabilities: &sdkmcp.ClientCapabilities{},
	}
	if onToolsChanged != nil {
		options.ToolListChangedHandler = func(changeCtx context.Context, _ *sdkmcp.ToolListChangedRequest) {
			onToolsChanged(changeCtx)
		}
	}

	connect := func(transport sdkmcp.Transport, transportType string) (*MCPClient, error) {
		client := sdkmcp.NewClient(&sdkmcp.Implementation{
			Name:    "openkitty",
			Version: clientVersion,
		}, options)
		// The SDK uses the Connect context for the entire JSON-RPC connection.
		// Keep that context alive after the handshake; use handshakeCtx only to
		// bound how long the caller waits for initialization.
		sessionCtx, cancelSession := context.WithCancel(context.WithoutCancel(ctx))
		type connectResult struct {
			session *sdkmcp.ClientSession
			err     error
		}
		resultCh := make(chan connectResult, 1)
		go func() {
			session, err := client.Connect(sessionCtx, transport, nil)
			resultCh <- connectResult{session: session, err: err}
		}()

		var session *sdkmcp.ClientSession
		select {
		case result := <-resultCh:
			if result.err != nil {
				cancelSession()
				return nil, result.err
			}
			session = result.session
		case <-handshakeCtx.Done():
			cancelSession()
			return nil, handshakeCtx.Err()
		}
		result := session.InitializeResult()
		if result == nil {
			_ = session.Close()
			cancelSession()
			return nil, errors.New("server did not return initialize result")
		}

		wrapped := &MCPClient{
			sdk:           &sdkMCPState{client: client, session: session, cancel: cancelSession},
			logger:        logger,
			protocol:      result.ProtocolVersion,
			transportType: transportType,
			endpoint:      strings.TrimSpace(cfg.URL),
			clientVersion: clientVersion,
		}
		if result.ServerInfo != nil {
			wrapped.serverInfo = MCPServerInfo{
				Name:    result.ServerInfo.Name,
				Version: result.ServerInfo.Version,
			}
		}
		wrapped.serverCaps = convertSDKCapabilities(result.Capabilities)
		logger.Printf("[MCP] Initialized: server=%s v%s, protocol=%s, transport=%s",
			wrapped.serverInfo.Name, wrapped.serverInfo.Version, wrapped.protocol, transportType)
		return wrapped, nil
	}

	transportName := normalizeMCPTransport(cfg.Transport)
	if cfg.Command != "" {
		if transportName != "" && transportName != "auto" && transportName != "stdio" {
			return nil, fmt.Errorf("transport %q cannot be used with command", cfg.Transport)
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = append(os.Environ(), cfg.Env...)
		cmd.Dir = cfg.CWD
		return connect(&sdkmcp.CommandTransport{
			Command:           cmd,
			TerminateDuration: 5 * time.Second,
		}, "stdio")
	}

	httpClient := &http.Client{
		Transport: &mcpHeaderTransport{
			base:    http.DefaultTransport,
			token:   cfg.Token,
			headers: cloneMCPStringMap(cfg.Headers),
		},
	}
	switch transportName {
	case "", "auto":
		client, streamErr := connect(&sdkmcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
			MaxRetries: 2,
		}, "streamable_http")
		if streamErr == nil {
			return client, nil
		}
		if !shouldFallbackToLegacySSE(streamErr) {
			return nil, fmt.Errorf("streamable HTTP: %w", streamErr)
		}
		logger.Printf("[MCP] Streamable HTTP unavailable for '%s'; trying legacy SSE", cfg.Name)
		client, sseErr := connect(&sdkmcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}, "sse")
		if sseErr != nil {
			return nil, fmt.Errorf("streamable HTTP: %v; legacy SSE: %w", streamErr, sseErr)
		}
		return client, nil
	case "streamable_http":
		return connect(&sdkmcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
			MaxRetries: 2,
		}, "streamable_http")
	case "sse":
		return connect(&sdkmcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}, "sse")
	case "stdio":
		return nil, errors.New("stdio transport requires command")
	default:
		return nil, fmt.Errorf("unsupported transport %q (use auto, stdio, streamable_http, or sse)", cfg.Transport)
	}
}

func normalizeMCPTransport(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "http", "streamable", "streamablehttp":
		return "streamable_http"
	case "legacy_sse":
		return "sse"
	default:
		return value
	}
}

func shouldFallbackToLegacySSE(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unauthorized") || strings.Contains(message, "forbidden") {
		return false
	}
	for _, marker := range []string{
		"bad request",
		"not found",
		"method not allowed",
		"unsupported media type",
		"not acceptable",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type mcpHeaderTransport struct {
	base    http.RoundTripper
	token   string
	headers map[string]string
}

func (t *mcpHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	if t.token != "" && clone.Header.Get("Authorization") == "" {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func cloneMCPStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func convertSDKCapabilities(capabilities *sdkmcp.ServerCapabilities) MCPServerCapabilities {
	var converted MCPServerCapabilities
	if capabilities == nil {
		return converted
	}
	if capabilities.Tools != nil {
		converted.Tools = &struct{}{}
	}
	if capabilities.Resources != nil {
		converted.Resources = &struct{}{}
	}
	if capabilities.Prompts != nil {
		converted.Prompts = &struct{}{}
	}
	return converted
}

func (c *MCPClient) sdkListTools(ctx context.Context) ([]MCPToolDef, error) {
	var tools []MCPToolDef
	for tool, err := range c.sdk.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		tools = append(tools, MCPToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schemaObject(tool.InputSchema),
		})
	}
	return tools, nil
}

func (c *MCPClient) sdkListResources(ctx context.Context) ([]MCPResource, error) {
	var resources []MCPResource
	for resource, err := range c.sdk.session.Resources(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("resources/list: %w", err)
		}
		resources = append(resources, MCPResource{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: resource.Description,
			MimeType:    resource.MIMEType,
		})
	}
	return resources, nil
}

func (c *MCPClient) sdkReadResource(ctx context.Context, uri string) (*MCPReadResourceResult, error) {
	result, err := c.sdk.session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("resources/read: %w", err)
	}
	converted := &MCPReadResourceResult{Contents: make([]MCPResourceContent, 0, len(result.Contents))}
	for _, content := range result.Contents {
		if content == nil {
			continue
		}
		converted.Contents = append(converted.Contents, MCPResourceContent{
			URI:      content.URI,
			MimeType: content.MIMEType,
			Text:     content.Text,
			Blob:     base64.StdEncoding.EncodeToString(content.Blob),
		})
	}
	return converted, nil
}

func (c *MCPClient) sdkCallTool(ctx context.Context, name string, args map[string]interface{}) (*MCPCallToolResult, error) {
	result, err := c.sdk.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("tools/call %q: %w", name, err)
	}
	converted := &MCPCallToolResult{
		Content: make([]MCPContentItem, 0, len(result.Content)+1),
		IsError: result.IsError,
	}
	for _, content := range result.Content {
		converted.Content = append(converted.Content, convertSDKContent(content))
	}
	if len(converted.Content) == 0 && result.StructuredContent != nil {
		if data, marshalErr := json.Marshal(result.StructuredContent); marshalErr == nil {
			converted.Content = append(converted.Content, MCPContentItem{Type: "text", Text: string(data)})
		}
	}
	if converted.IsError {
		var messages []string
		for _, content := range converted.Content {
			if content.Text != "" {
				messages = append(messages, content.Text)
			}
		}
		detail := strings.Join(messages, "\n")
		if detail == "" {
			detail = "MCP tool reported an error"
		}
		return nil, fmt.Errorf("tool %q returned error: %s", name, detail)
	}
	return converted, nil
}

func convertSDKContent(content sdkmcp.Content) MCPContentItem {
	switch value := content.(type) {
	case *sdkmcp.TextContent:
		return MCPContentItem{Type: "text", Text: value.Text}
	case *sdkmcp.ImageContent:
		return MCPContentItem{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(value.Data),
			MimeType: value.MIMEType,
		}
	case *sdkmcp.AudioContent:
		return MCPContentItem{
			Type:     "audio",
			Data:     base64.StdEncoding.EncodeToString(value.Data),
			MimeType: value.MIMEType,
		}
	case *sdkmcp.ResourceLink:
		return MCPContentItem{
			Type:     "resource",
			URI:      value.URI,
			Name:     value.Name,
			MimeType: value.MIMEType,
		}
	case *sdkmcp.EmbeddedResource:
		if value.Resource == nil {
			return MCPContentItem{Type: "resource"}
		}
		return MCPContentItem{
			Type:     "resource",
			Text:     value.Resource.Text,
			Data:     base64.StdEncoding.EncodeToString(value.Resource.Blob),
			MimeType: value.Resource.MIMEType,
			URI:      value.Resource.URI,
		}
	default:
		data, err := content.MarshalJSON()
		if err != nil {
			return MCPContentItem{Type: "unknown"}
		}
		return MCPContentItem{Type: "text", Text: string(data)}
	}
}

func schemaObject(schema any) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
	if object, ok := schema.(map[string]interface{}); ok {
		return object
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]interface{}{"type": "object"}
	}
	var object map[string]interface{}
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return map[string]interface{}{"type": "object"}
	}
	return object
}

func ensureMCPLogger(logger *log.Logger) *log.Logger {
	if logger != nil {
		return logger
	}
	return log.New(io.Discard, "", 0)
}

func decodedContentSize(encoded string) int {
	if encoded == "" {
		return 0
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return len(encoded)
	}
	return len(decoded)
}

func (m *MCPManager) removeClientToolsLocked(client *MCPClient) {
	for name, tool := range m.tools {
		if mcpTool, ok := tool.(*MCPTool); ok && mcpTool.UsesClient(client) {
			delete(m.tools, name)
		}
	}
}

func (m *MCPManager) registerToolsLocked(cfg MCPServerConfig, client *MCPClient, defs []MCPToolDef) {
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	for _, def := range defs {
		tool := NewMCPTool(def, client, m.logger)
		name := "mcp_" + def.Name
		if prefix := sanitizeMCPToolSegment(cfg.ToolPrefix); prefix != "" {
			name = "mcp_" + prefix + "_" + def.Name
		}
		if _, exists := m.tools[name]; exists || isReservedMCPRegistryName(name) {
			server := sanitizeMCPToolSegment(cfg.Name)
			if server == "" {
				server = "server"
			}
			name = "mcp_" + server + "_" + def.Name
			for suffix := 2; ; suffix++ {
				if _, exists := m.tools[name]; !exists {
					break
				}
				name = fmt.Sprintf("mcp_%s_%s_%d", server, def.Name, suffix)
			}
		}
		tool.SetRegistryName(name)
		m.tools[name] = tool
		m.logger.Printf("[MCP] Registered tool: %s (from %s)", name, cfg.Name)
	}
}

// RefreshTools re-reads a server's tool catalog. The official SDK invokes this
// when the server emits notifications/tools/list_changed.
func (m *MCPManager) RefreshTools(ctx context.Context, name string) error {
	m.mu.RLock()
	client := m.servers[name]
	cfg, configured := m.configs[name]
	m.mu.RUnlock()
	if client == nil || !configured {
		return fmt.Errorf("MCP server %q is not connected", name)
	}
	if !client.HasTools() {
		return nil
	}
	defs, err := client.ListTools(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.servers[name] != client {
		return nil
	}
	m.removeClientToolsLocked(client)
	m.registerToolsLocked(cfg, client, defs)
	m.syncRegistriesLocked()
	return nil
}

// BindRegistry keeps a ToolRegistry synchronized with this manager, including
// tools added or removed after notifications/tools/list_changed.
func (m *MCPManager) BindRegistry(registry Registry) {
	if registry == nil {
		return
	}
	registry.Register(NewMCPListResourcesTool(mcpResourceProvider{manager: m}))
	registry.Register(NewMCPReadResourceTool(mcpResourceProvider{manager: m}))
	m.mu.Lock()
	if _, exists := m.registryBindings[registry]; !exists {
		m.registryBindings[registry] = make(map[string]struct{})
	}
	m.syncRegistryLocked(registry)
	m.mu.Unlock()
}

func (m *MCPManager) syncRegistriesLocked() {
	for registry := range m.registryBindings {
		m.syncRegistryLocked(registry)
	}
}

func (m *MCPManager) syncRegistryLocked(registry Registry) {
	previous := m.registryBindings[registry]
	for name := range previous {
		registry.Unregister(name)
	}
	next := make(map[string]struct{}, len(m.tools))
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		registry.Register(m.tools[name])
		next[name] = struct{}{}
	}
	m.registryBindings[registry] = next
}

type mcpResourceProvider struct{ manager *MCPManager }

func (p mcpResourceProvider) ResourceClients() map[string]MCPResourceClient {
	if p.manager == nil {
		return nil
	}
	clients := p.manager.GetClients()
	result := make(map[string]MCPResourceClient, len(clients))
	for name, client := range clients {
		result[name] = client
	}
	return result
}

var _ toolcontract.Tool = (*MCPListResourcesTool)(nil)

func isReservedMCPRegistryName(name string) bool {
	return name == "mcp_list_resources" || name == "mcp_read_resource"
}

func sanitizeMCPToolSegment(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	result.Grow(len(value))
	lastUnderscore := false
	for _, r := range value {
		valid := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'
		if valid {
			result.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			result.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(result.String(), "_")
}
