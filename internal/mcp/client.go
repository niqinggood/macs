package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

// ====================== JSON-RPC Types ======================

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type JSONRPCNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// ====================== MCP Protocol Types ======================

type MCPInitializeParams struct {
	ProtocolVersion string        `json:"protocolVersion"`
	Capabilities    MCPClientCaps `json:"capabilities"`
	ClientInfo      MCPClientInfo `json:"clientInfo"`
}

type MCPClientCaps struct {
	Roots    *struct{} `json:"roots,omitempty"`
	Sampling *struct{} `json:"sampling,omitempty"`
}

type MCPClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MCPInitializeResult struct {
	ProtocolVersion string                `json:"protocolVersion"`
	Capabilities    MCPServerCapabilities `json:"capabilities"`
	ServerInfo      MCPServerInfo         `json:"serverInfo"`
}

type MCPServerCapabilities struct {
	Tools     *struct{} `json:"tools,omitempty"`
	Resources *struct{} `json:"resources,omitempty"`
	Prompts   *struct{} `json:"prompts,omitempty"`
}

type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ====================== Transport Interface ======================

type MCPTransport interface {
	SendRequest(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error)
	SendNotification(ctx context.Context, method string, params interface{}) error
	Close() error
}

// ====================== Stdio Transport ======================

type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
	logger *log.Logger
}

func NewStdioTransport(command string, args []string, env []string, logger *log.Logger) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command '%s': %w", command, err)
	}

	logger = ensureMCPLogger(logger)
	logger.Printf("[MCP:stdio] Started '%s %s' (pid=%d)", command, strings.Join(args, " "), cmd.Process.Pid)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	return &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: scanner,
		logger: logger,
	}, nil
}

func (t *StdioTransport) SendRequest(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	t.logger.Printf("[MCP:stdio] → %s", textutil.Truncate(string(data), 300))

	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response with timeout via context
	done := make(chan struct{})
	var resp JSONRPCResponse
	var readErr error

	go func() {
		if t.stdout.Scan() {
			line := t.stdout.Bytes()
			t.logger.Printf("[MCP:stdio] ← %s", textutil.Truncate(string(line), 300))
			readErr = json.Unmarshal(line, &resp)
		} else {
			readErr = t.stdout.Err()
			if readErr == nil {
				readErr = io.EOF
			}
		}
		close(done)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}
		return &resp, nil
	}
}

func (t *StdioTransport) SendNotification(ctx context.Context, method string, params interface{}) error {
	note := &JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(note)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stdin.Write(append(data, '\n'))
	return nil
}

func (t *StdioTransport) Close() error {
	t.stdin.Close()
	return t.cmd.Process.Kill()
}

// ====================== HTTP Transport ======================

type HTTPTransport struct {
	baseURL    string
	httpClient *http.Client
	token      string
	logger     *log.Logger
}

func NewHTTPTransport(baseURL string, token string, logger *log.Logger) (*HTTPTransport, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &HTTPTransport{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		token:      token,
		logger:     ensureMCPLogger(logger),
	}, nil
}

func (t *HTTPTransport) SendRequest(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	t.logger.Printf("[MCP:http] → method=%s bytes=%d", req.Method, len(data))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.baseURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.token)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	t.logger.Printf("[MCP:http] ← status=%d bytes=%d", resp.StatusCode, len(body))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (body: %s)", err, textutil.Truncate(string(body), 200))
	}

	return &rpcResp, nil
}

func (t *HTTPTransport) SendNotification(ctx context.Context, method string, params interface{}) error {
	// HTTP transport: notification = POST without waiting for meaningful response
	note := &JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(note)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.baseURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *HTTPTransport) Close() error {
	return nil
}

// ====================== MCP Client ======================

type MCPClient struct {
	transport     MCPTransport
	sdk           *sdkMCPState
	serverCaps    MCPServerCapabilities
	serverInfo    MCPServerInfo
	protocol      string
	transportType string
	endpoint      string
	reqID         int64
	mu            sync.Mutex
	logger        *log.Logger
	clientVersion string
}

func NewMCPClient(transport MCPTransport, logger *log.Logger, clientVersion ...string) *MCPClient {
	version := "unknown"
	if len(clientVersion) > 0 && strings.TrimSpace(clientVersion[0]) != "" {
		version = strings.TrimSpace(clientVersion[0])
	}
	client := &MCPClient{
		transport:     transport,
		logger:        ensureMCPLogger(logger),
		clientVersion: version,
	}
	if httpTransport, ok := transport.(*HTTPTransport); ok {
		client.endpoint = httpTransport.baseURL
	}
	return client
}

// Initialize performs the MCP handshake.
func (c *MCPClient) Initialize(ctx context.Context) error {
	if c.sdk != nil {
		return nil
	}
	req := c.newRequest("initialize", MCPInitializeParams{
		ProtocolVersion: MCPProtocolVersion,
		Capabilities:    MCPClientCaps{},
		ClientInfo: MCPClientInfo{
			Name:    "openkitty",
			Version: c.clientVersion,
		},
	})

	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s (code=%d)", resp.Error.Message, resp.Error.Code)
	}

	var initResult MCPInitializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		return fmt.Errorf("unmarshal initialize result: %w", err)
	}

	c.serverCaps = initResult.Capabilities
	c.serverInfo = initResult.ServerInfo
	c.protocol = initResult.ProtocolVersion

	c.logger.Printf("[MCP] Initialized: server=%s v%s, protocol=%s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version, initResult.ProtocolVersion)

	// Send "notifications/initialized" notification (fire-and-forget)
	c.logger.Printf("[MCP] → notifications/initialized")
	c.transport.SendNotification(ctx, "notifications/initialized", nil)

	return nil
}

// ListResources discovers available resources from the MCP server.
func (c *MCPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	if c.sdk != nil {
		return c.sdkListResources(ctx)
	}
	req := c.newRequest("resources/list", nil)
	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}
	var result MCPListResourcesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal resources: %w", err)
	}
	return result.Resources, nil
}

// ReadResource reads the content of an MCP resource by URI.
func (c *MCPClient) ReadResource(ctx context.Context, uri string) (*MCPReadResourceResult, error) {
	if c.sdk != nil {
		return c.sdkReadResource(ctx, uri)
	}
	req := c.newRequest("resources/read", map[string]string{"uri": uri})
	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resources/read: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}
	var result MCPReadResourceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal resource content: %w", err)
	}
	return &result, nil
}

// ListTools discovers available tools from the MCP server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	if c.sdk != nil {
		return c.sdkListTools(ctx)
	}
	req := c.newRequest("tools/list", nil)

	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	var result MCPListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*MCPCallToolResult, error) {
	if c.sdk != nil {
		return c.sdkCallTool(ctx, name, args)
	}
	req := c.newRequest("tools/call", MCPCallToolParams{
		Name:      name,
		Arguments: args,
	})

	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("tools/call '%s': %w", name, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call '%s' error: %s", name, resp.Error.Message)
	}

	var result MCPCallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tool result: %w", err)
	}

	if result.IsError {
		var errText string
		for _, item := range result.Content {
			if item.Type == "text" {
				errText += item.Text
			}
		}
		return nil, fmt.Errorf("tool '%s' returned error: %s", name, errText)
	}

	return &result, nil
}

// Ping checks liveness via a lightweight RPC call.
func (c *MCPClient) Ping(ctx context.Context) error {
	if c.sdk != nil {
		return c.sdk.session.Ping(ctx, nil)
	}
	req := c.newRequest("ping", nil)
	resp, err := c.transport.SendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("ping error: %s", resp.Error.Message)
	}
	return nil
}

func (c *MCPClient) Close() error {
	if c.sdk != nil {
		return c.sdk.close()
	}
	if c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

func (c *MCPClient) ServerInfo() MCPServerInfo {
	return c.serverInfo
}

func (c *MCPClient) HasTools() bool {
	return c.serverCaps.Tools != nil
}

func (c *MCPClient) HasResources() bool {
	return c.serverCaps.Resources != nil
}

// ProtocolVersion returns the version negotiated during initialization.
func (c *MCPClient) ProtocolVersion() string {
	return c.protocol
}

// TransportType returns stdio, streamable_http, or sse.
func (c *MCPClient) TransportType() string {
	return c.transportType
}

// ServerName identifies the connected MCP server.
func (c *MCPClient) ServerName() string {
	if c == nil {
		return ""
	}
	return c.serverInfo.Name
}

// Endpoint returns the configured remote endpoint, when the transport has one.
func (c *MCPClient) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

func (c *MCPClient) newRequest(method string, params interface{}) *JSONRPCRequest {
	c.mu.Lock()
	c.reqID++
	id := c.reqID
	c.mu.Unlock()

	return &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
}

// ====================== MCP Client Manager ======================

// MCPServerConfig describes an MCP server connection.
type MCPServerConfig struct {
	Name           string            `json:"name" yaml:"name"`
	Transport      string            `json:"transport,omitempty" yaml:"transport,omitempty"` // auto, stdio, streamable_http, sse
	Command        string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args           []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env            []string          `json:"env,omitempty" yaml:"env,omitempty"`
	CWD            string            `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	URL            string            `json:"url,omitempty" yaml:"url,omitempty"`
	Token          string            `json:"token,omitempty" yaml:"token,omitempty"`
	Headers        map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	ToolPrefix     string            `json:"tool_prefix,omitempty" yaml:"tool_prefix,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	Enabled        bool              `json:"enabled" yaml:"enabled"`
}

// MCPManager manages multiple MCP server connections.
type MCPManager struct {
	servers          map[string]*MCPClient
	tools            map[string]toolcontract.Tool // mcp_<name> -> Tool
	configs          map[string]MCPServerConfig
	registryBindings map[Registry]map[string]struct{}
	logger           *log.Logger
	clientVersion    string
	mu               sync.RWMutex
}

// Registry is the minimal tool registry surface required by MCP discovery.
type Registry interface {
	Register(toolcontract.Tool)
	Unregister(string)
}

func NewMCPManager(logger *log.Logger, clientVersion ...string) *MCPManager {
	version := "unknown"
	if len(clientVersion) > 0 && strings.TrimSpace(clientVersion[0]) != "" {
		version = strings.TrimSpace(clientVersion[0])
	}
	return &MCPManager{
		servers:          make(map[string]*MCPClient),
		tools:            make(map[string]toolcontract.Tool),
		configs:          make(map[string]MCPServerConfig),
		registryBindings: make(map[Registry]map[string]struct{}),
		logger:           ensureMCPLogger(logger),
		clientVersion:    version,
	}
}

// ConnectAll connects to all configured MCP servers and registers their tools.
// Returns aggregated errors for all failed connections.
func (m *MCPManager) ConnectAll(ctx context.Context, configs []MCPServerConfig) error {
	var errs []string
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		if err := m.Connect(ctx, cfg); err != nil {
			m.logger.Printf("[MCP] Failed to connect to '%s': %v", cfg.Name, err)
			errs = append(errs, fmt.Sprintf("%s: %v", cfg.Name, err))
			continue
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("MCP connection failures: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Reconnect attempts to reconnect to a single MCP server by name.
func (m *MCPManager) Reconnect(ctx context.Context, name string, cfg MCPServerConfig) error {
	cfg.Name = name
	return m.Connect(ctx, cfg)
}

// Connect establishes connection to a single MCP server.
func (m *MCPManager) Connect(ctx context.Context, cfg MCPServerConfig) error {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		return fmt.Errorf("MCP server name is required")
	}

	client, err := connectSDKMCPClient(ctx, cfg, m.clientVersion, m.logger, func(changeCtx context.Context) {
		if refreshErr := m.RefreshTools(changeCtx, cfg.Name); refreshErr != nil {
			m.logger.Printf("[MCP] Failed to refresh tools for '%s': %v", cfg.Name, refreshErr)
		}
	})
	if err != nil {
		return fmt.Errorf("connect '%s': %w", cfg.Name, err)
	}

	var tools []MCPToolDef
	if client.HasTools() {
		tools, err = client.ListTools(ctx)
		if err != nil {
			client.Close()
			return fmt.Errorf("list tools for '%s': %w", cfg.Name, err)
		}
	}

	m.mu.Lock()
	old := m.servers[cfg.Name]
	if old != nil {
		m.removeClientToolsLocked(old)
	}
	m.servers[cfg.Name] = client
	m.configs[cfg.Name] = cfg
	m.registerToolsLocked(cfg, client, tools)
	m.syncRegistriesLocked()
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	m.logger.Printf("[MCP] Connected to '%s' over %s (protocol=%s, tools=%d, resources=%t)",
		cfg.Name, client.TransportType(), client.ProtocolVersion(), len(tools), client.HasResources())
	return nil
}

// Disconnect closes all MCP connections.
func (m *MCPManager) DisconnectAll() {
	m.mu.Lock()
	servers := m.servers
	m.servers = make(map[string]*MCPClient)
	m.tools = make(map[string]toolcontract.Tool)
	m.configs = make(map[string]MCPServerConfig)
	m.syncRegistriesLocked()
	m.mu.Unlock()
	for name, client := range servers {
		client.Close()
		m.logger.Printf("[MCP] Disconnected '%s'", name)
	}
}

// GetTools returns all MCP tools for registration in ToolRegistry.
func (m *MCPManager) GetTools() []toolcontract.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]toolcontract.Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, m.tools[name])
	}
	return tools
}

// GetTool returns a specific MCP tool by name.
// GetClient returns an MCP client by server name.
func (m *MCPManager) GetClient(name string) *MCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

// GetClients returns all connected MCP clients.
func (m *MCPManager) GetClients() map[string]*MCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*MCPClient, len(m.servers))
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

func (m *MCPManager) GetTool(name string) (toolcontract.Tool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tools[name]
	return t, ok
}
