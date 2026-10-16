package development

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LSPClient communicates with a Language Server over stdio.
// Claude Code borrowed: LSPTool in tools/LSPTool/
type LSPClient struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	reader      *bufio.Reader
	writeMu     sync.Mutex
	pendingMu   sync.Mutex
	pending     map[int64]chan lspResponse
	idCounter   int64
	docMu       sync.Mutex
	documents   map[string]lspDocumentState
	diagMu      sync.RWMutex
	diagnostics map[string][]LSPDiagnostic
	diagNotify  chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	closeErr    error
	serverName  string
	rootDir     string
	rootURI     string
	logger      *log.Logger
}

type lspResponse struct {
	Result json.RawMessage
	Error  error
}

type lspDocumentState struct {
	Version int
	Text    string
}

// LSP position/structure types
type LSPPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type LSPRange struct {
	Start LSPPosition `json:"start"`
	End   LSPPosition `json:"end"`
}

type LSPLocation struct {
	URI   string   `json:"uri"`
	Range LSPRange `json:"range"`
}

type LSPTextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type LSPTextDocumentPositionParams struct {
	TextDocument LSPTextDocumentIdentifier `json:"textDocument"`
	Position     LSPPosition               `json:"position"`
}

type LSPDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          LSPRange            `json:"range"`
	SelectionRange LSPRange            `json:"selectionRange"`
	Children       []LSPDocumentSymbol `json:"children,omitempty"`
	Detail         string              `json:"detail,omitempty"`
}

type LSPHoverResult struct {
	Contents LSPMarkupContent `json:"contents"`
	Range    *LSPRange        `json:"range,omitempty"`
}

type LSPMarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type LSPCallHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	URI            string   `json:"uri"`
	Range          LSPRange `json:"range"`
	SelectionRange LSPRange `json:"selectionRange"`
}

type LSPCallHierarchyIncomingCall struct {
	From       LSPCallHierarchyItem `json:"from"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

type LSPCallHierarchyOutgoingCall struct {
	To         LSPCallHierarchyItem `json:"to"`
	FromRanges []LSPRange           `json:"fromRanges"`
}

// lspDetectLanguage auto-detects language from file extension
func lspDetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cpp", ".cc", ".cxx", ".h", ".hpp":
		return "cpp"
	case ".c":
		return "c"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	}
	return ""
}

// GetServerCommand returns the LSP server command for a language.
func GetServerCommand(lang string) (string, []string) {
	servers := map[string][]string{
		"go":         {"gopls"},
		"python":     {"pyright-langserver", "--stdio"},
		"typescript": {"typescript-language-server", "--stdio"},
		"javascript": {"typescript-language-server", "--stdio"},
		"rust":       {"rust-analyzer"},
		"cpp":        {"clangd"},
		"c":          {"clangd"},
		"csharp":     {"omnisharp", "-lsp"},
		"java":       {"jdtls"},
		"ruby":       {"solargraph", "stdio"},
		"swift":      {"sourcekit-lsp"},
		"kotlin":     {"kotlin-language-server"},
	}
	if args, ok := servers[lang]; ok {
		if len(args) > 1 {
			return args[0], args[1:]
		}
		return args[0], nil
	}
	return "", nil
}

func isLSPServerAvailable(cmd string) bool {
	if cmd == "" {
		return false
	}
	_, err := exec.LookPath(cmd)
	return err == nil
}

func NewLSPClient(rootDir, filePath string, logger *log.Logger) (*LSPClient, error) {
	lang := lspDetectLanguage(filePath)
	if lang == "" {
		return nil, fmt.Errorf("unsupported file type: %s", filePath)
	}

	cmd, args := GetServerCommand(lang)
	if cmd == "" || !isLSPServerAvailable(cmd) {
		return nil, fmt.Errorf("no LSP server found for language: %s", lang)
	}

	absRoot, _ := filepath.Abs(rootDir)
	c := &LSPClient{
		serverName:  cmd,
		rootDir:     absRoot,
		rootURI:     lspFileURI(absRoot),
		logger:      logger,
		pending:     make(map[int64]chan lspResponse),
		documents:   make(map[string]lspDocumentState),
		diagnostics: make(map[string][]LSPDiagnostic),
		diagNotify:  make(chan struct{}, 1),
		done:        make(chan struct{}),
	}

	execCmd := exec.Command(cmd, args...)
	execCmd.Dir = absRoot

	stdin, err := execCmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	execCmd.Stderr = nil

	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd, err)
	}

	c.cmd = execCmd
	c.stdin = stdin
	c.reader = bufio.NewReaderSize(stdout, 64*1024)
	go c.readLoop()

	// Send initialize request
	if err := c.initialize(); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	if logger != nil {
		logger.Printf("[LSP] Connected to %s for %s", cmd, lang)
	}
	return c, nil
}

func (c *LSPClient) initialize() error {
	params := map[string]interface{}{
		"processId": nil,
		"rootUri":   c.rootURI,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"hover":              map[string]interface{}{"contentFormat": []string{"markdown"}},
				"definition":         map[string]interface{}{"linkSupport": true},
				"references":         map[string]interface{}{},
				"documentSymbol":     map[string]interface{}{"hierarchicalDocumentSymbolSupport": true},
				"callHierarchy":      map[string]interface{}{},
				"synchronization":    map[string]interface{}{"dynamicRegistration": false, "didSave": true},
				"publishDiagnostics": map[string]interface{}{"relatedInformation": true},
				"diagnostic":         map[string]interface{}{"dynamicRegistration": false},
			},
			"workspace": map[string]interface{}{"configuration": true},
		},
	}

	_, err := c.sendRequest("initialize", params)
	if err != nil {
		return err
	}

	// Send initialized notification
	c.sendNotification("initialized", map[string]interface{}{})
	return nil
}

func (c *LSPClient) sendRequest(method string, params interface{}) (json.RawMessage, error) {
	return c.sendRequestWithTimeout(method, params, 30*time.Second)
}

func (c *LSPClient) sendRequestWithTimeout(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	c.pendingMu.Lock()
	c.idCounter++
	id := c.idCounter
	responseCh := make(chan lspResponse, 1)
	c.pending[id] = responseCh
	c.pendingMu.Unlock()

	req := struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      int64       `json:"id"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{"2.0", id, method, params}
	data, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return nil, err
	}
	if err := c.writeFrame(data); err != nil {
		c.removePending(id)
		return nil, fmt.Errorf("write LSP request: %w", err)
	}

	select {
	case response := <-responseCh:
		return response.Result, response.Error
	case <-c.done:
		c.removePending(id)
		return nil, c.transportError()
	case <-time.After(timeout):
		c.removePending(id)
		return nil, fmt.Errorf("LSP request %s timed out after %s", method, timeout)
	}
}

func (c *LSPClient) sendNotification(method string, params interface{}) error {
	note := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{"2.0", method, params}
	data, err := json.Marshal(note)
	if err != nil {
		return err
	}
	return c.writeFrame(data)
}

func (c *LSPClient) writeFrame(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return c.transportError()
	default:
	}
	if c.stdin == nil {
		return fmt.Errorf("LSP stdin is not connected")
	}
	var frame bytes.Buffer
	fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(payload))
	frame.Write(payload)
	_, err := c.stdin.Write(frame.Bytes())
	return err
}

func (c *LSPClient) readLoop() {
	for {
		payload, err := readLSPFrame(c.reader)
		if err != nil {
			c.failTransport(err)
			return
		}
		c.handleIncoming(payload)
	}
}

func readLSPFrame(reader *bufio.Reader) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("LSP stdout is not connected")
	}
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || contentLength < 0 {
				return nil, fmt.Errorf("invalid LSP Content-Length %q", parts[1])
			}
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("LSP frame is missing Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *LSPClient) handleIncoming(payload []byte) {
	var message struct {
		ID     json.RawMessage `json:"id,omitempty"`
		Method string          `json:"method,omitempty"`
		Params json.RawMessage `json:"params,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		if c.logger != nil {
			c.logger.Printf("[LSP] invalid JSON-RPC message: %v", err)
		}
		return
	}
	if message.Method != "" {
		if message.Method == "textDocument/publishDiagnostics" {
			c.cachePublishedDiagnostics(message.Params)
		}
		if len(message.ID) > 0 && string(message.ID) != "null" {
			c.respondToServerRequest(message.ID, message.Method, message.Params)
		}
		return
	}

	var id int64
	if err := json.Unmarshal(message.ID, &id); err != nil {
		return
	}
	response := lspResponse{Result: message.Result}
	if message.Error != nil {
		response.Error = fmt.Errorf("LSP error: %s (code=%d)", message.Error.Message, message.Error.Code)
	}
	c.pendingMu.Lock()
	responseCh := c.pending[id]
	delete(c.pending, id)
	c.pendingMu.Unlock()
	if responseCh != nil {
		responseCh <- response
	}
}

func (c *LSPClient) respondToServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var result interface{}
	if method == "workspace/configuration" {
		var request struct {
			Items []interface{} `json:"items"`
		}
		_ = json.Unmarshal(params, &request)
		result = make([]interface{}, len(request.Items))
	}
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	}
	data, err := json.Marshal(response)
	if err == nil {
		_ = c.writeFrame(data)
	}
}

func (c *LSPClient) cachePublishedDiagnostics(params json.RawMessage) {
	var published struct {
		URI         string          `json:"uri"`
		Diagnostics []LSPDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &published); err != nil || published.URI == "" {
		return
	}
	c.diagMu.Lock()
	c.diagnostics[published.URI] = append([]LSPDiagnostic(nil), published.Diagnostics...)
	c.diagMu.Unlock()
	select {
	case c.diagNotify <- struct{}{}:
	default:
	}
}

func (c *LSPClient) cachedDiagnostics(uri string) ([]LSPDiagnostic, bool) {
	c.diagMu.RLock()
	diagnostics, ok := c.diagnostics[uri]
	c.diagMu.RUnlock()
	return append([]LSPDiagnostic(nil), diagnostics...), ok
}

func (c *LSPClient) storeDiagnostics(uri string, diagnostics []LSPDiagnostic) {
	c.diagMu.Lock()
	c.diagnostics[uri] = append([]LSPDiagnostic(nil), diagnostics...)
	c.diagMu.Unlock()
}

func (c *LSPClient) removePending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *LSPClient) failTransport(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.done)
		c.pendingMu.Lock()
		for id, responseCh := range c.pending {
			responseCh <- lspResponse{Error: fmt.Errorf("LSP transport closed: %w", err)}
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
	})
}

func (c *LSPClient) transportError() error {
	if c.closeErr != nil {
		return fmt.Errorf("LSP transport closed: %w", c.closeErr)
	}
	return fmt.Errorf("LSP transport closed")
}

func (c *LSPClient) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func lspFileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func (c *LSPClient) resolveFilePath(filePath string) string {
	if !filepath.IsAbs(filePath) && c.rootDir != "" {
		filePath = filepath.Join(c.rootDir, filePath)
	}
	abs, err := filepath.Abs(filePath)
	if err == nil {
		filePath = abs
	}
	return filepath.Clean(filePath)
}

func (c *LSPClient) fileURI(filePath string) string {
	return lspFileURI(c.resolveFilePath(filePath))
}

// SyncDocument keeps the language server's in-memory document identical to
// the final file content. Full-content changes are deliberate: they are
// deterministic across formatters and avoid fragile range calculations.
func (c *LSPClient) SyncDocument(filePath, text string, saved bool) error {
	filePath = c.resolveFilePath(filePath)
	uri := c.fileURI(filePath)
	c.docMu.Lock()
	defer c.docMu.Unlock()

	state, opened := c.documents[uri]
	if !opened {
		state = lspDocumentState{Version: 1, Text: text}
		if err := c.sendNotification("textDocument/didOpen", map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":        uri,
				"languageId": lspDetectLanguage(filePath),
				"version":    state.Version,
				"text":       text,
			},
		}); err != nil {
			return err
		}
		c.documents[uri] = state
	} else if state.Text != text {
		state.Version++
		state.Text = text
		if err := c.sendNotification("textDocument/didChange", map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":     uri,
				"version": state.Version,
			},
			"contentChanges": []map[string]interface{}{{"text": text}},
		}); err != nil {
			return err
		}
		c.documents[uri] = state
	}
	if saved {
		if err := c.sendNotification("textDocument/didSave", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": uri},
			"text":         text,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *LSPClient) syncDocumentFromDisk(filePath string, saved bool) error {
	filePath = c.resolveFilePath(filePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read document for LSP: %w", err)
	}
	return c.SyncDocument(filePath, string(data), saved)
}

func (c *LSPClient) CloseDocument(filePath string) error {
	uri := c.fileURI(filePath)
	c.docMu.Lock()
	defer c.docMu.Unlock()
	if _, ok := c.documents[uri]; !ok {
		return nil
	}
	if err := c.sendNotification("textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	}); err != nil {
		return err
	}
	delete(c.documents, uri)
	return nil
}

// GoToDefinition finds the definition of a symbol at the given position.
func (c *LSPClient) GoToDefinition(filePath string, line, character int) ([]LSPLocation, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	uri := c.fileURI(filePath)
	params := LSPTextDocumentPositionParams{
		TextDocument: LSPTextDocumentIdentifier{URI: uri},
		Position:     LSPPosition{Line: line, Character: character},
	}

	result, err := c.sendRequest("textDocument/definition", params)
	if err != nil {
		return nil, err
	}

	var locations []LSPLocation
	if err := json.Unmarshal(result, &locations); err != nil {
		// Try single location
		var loc LSPLocation
		if err2 := json.Unmarshal(result, &loc); err2 != nil {
			return nil, fmt.Errorf("unmarshal definition: %s", string(result[:min(len(result), 200)]))
		}
		locations = []LSPLocation{loc}
	}
	return locations, nil
}

// FindReferences finds all references to a symbol.
func (c *LSPClient) FindReferences(filePath string, line, character int) ([]LSPLocation, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	uri := c.fileURI(filePath)
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line, "character": character},
		"context":      map[string]interface{}{"includeDeclaration": false},
	}

	result, err := c.sendRequest("textDocument/references", params)
	if err != nil {
		return nil, err
	}

	var locations []LSPLocation
	if err := json.Unmarshal(result, &locations); err != nil {
		return nil, fmt.Errorf("unmarshal references: %w", err)
	}
	return locations, nil
}

// Hover gets documentation for a symbol.
func (c *LSPClient) Hover(filePath string, line, character int) (string, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return "", err
	}
	uri := c.fileURI(filePath)
	params := LSPTextDocumentPositionParams{
		TextDocument: LSPTextDocumentIdentifier{URI: uri},
		Position:     LSPPosition{Line: line, Character: character},
	}

	result, err := c.sendRequest("textDocument/hover", params)
	if err != nil {
		return "", err
	}
	if result == nil || string(result) == "null" {
		return "(no documentation available)", nil
	}

	var hover LSPHoverResult
	if err := json.Unmarshal(result, &hover); err != nil {
		// Try plain string
		var s string
		if err2 := json.Unmarshal(result, &s); err2 != nil {
			return string(result), nil
		}
		return s, nil
	}
	return hover.Contents.Value, nil
}

// DocumentSymbol returns all symbols in a document.
func (c *LSPClient) DocumentSymbol(filePath string) ([]LSPDocumentSymbol, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	uri := c.fileURI(filePath)

	result, err := c.sendRequest("textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	})
	if err != nil {
		return nil, err
	}

	var symbols []LSPDocumentSymbol
	if err := json.Unmarshal(result, &symbols); err != nil {
		return nil, fmt.Errorf("unmarshal symbols: %w", err)
	}
	return symbols, nil
}

// PrepareCallHierarchy prepares for call hierarchy requests.
func (c *LSPClient) PrepareCallHierarchy(filePath string, line, character int) ([]LSPCallHierarchyItem, error) {
	if err := c.syncDocumentFromDisk(filePath, false); err != nil {
		return nil, err
	}
	uri := c.fileURI(filePath)
	params := LSPTextDocumentPositionParams{
		TextDocument: LSPTextDocumentIdentifier{URI: uri},
		Position:     LSPPosition{Line: line, Character: character},
	}

	result, err := c.sendRequest("textDocument/prepareCallHierarchy", params)
	if err != nil {
		return nil, err
	}

	var items []LSPCallHierarchyItem
	if err := json.Unmarshal(result, &items); err != nil {
		return nil, fmt.Errorf("unmarshal call hierarchy: %w", err)
	}
	return items, nil
}

// IncomingCalls returns functions that call the given function.
func (c *LSPClient) IncomingCalls(item LSPCallHierarchyItem) ([]LSPCallHierarchyIncomingCall, error) {
	result, err := c.sendRequest("callHierarchy/incomingCalls", map[string]interface{}{
		"item": item,
	})
	if err != nil {
		return nil, err
	}

	var calls []LSPCallHierarchyIncomingCall
	if err := json.Unmarshal(result, &calls); err != nil {
		return nil, fmt.Errorf("unmarshal incoming calls: %w", err)
	}
	return calls, nil
}

// OutgoingCalls returns functions called by the given function.
func (c *LSPClient) OutgoingCalls(item LSPCallHierarchyItem) ([]LSPCallHierarchyOutgoingCall, error) {
	result, err := c.sendRequest("callHierarchy/outgoingCalls", map[string]interface{}{
		"item": item,
	})
	if err != nil {
		return nil, err
	}

	var calls []LSPCallHierarchyOutgoingCall
	if err := json.Unmarshal(result, &calls); err != nil {
		return nil, fmt.Errorf("unmarshal outgoing calls: %w", err)
	}
	return calls, nil
}

func (c *LSPClient) readFileContent(filePath string) string {
	// Read file content for LSP server — required by tsserver and others that don't read from disk.
	// Cap at 1MB to avoid overwhelming the server with large files.
	data, err := os.ReadFile(filePath)
	if err != nil || len(data) > 1024*1024 {
		if len(data) > 1024*1024 {
			return string(data[:1024*1024]) // truncated
		}
		return ""
	}
	return string(data)
}

func (c *LSPClient) Close() error {
	_ = c.sendNotification("exit", nil)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	var killErr error
	if c.cmd != nil && c.cmd.Process != nil {
		killErr = c.cmd.Process.Kill()
	}
	c.failTransport(io.EOF)
	return killErr
}

// ==================== LSP Tools ====================

type LSPManager struct {
	mu        sync.Mutex
	clients   map[string]*LSPClient // project root + language -> client
	logger    *log.Logger
	workspace string
}

func NewLSPManager(logger *log.Logger, workspaces ...string) *LSPManager {
	manager := &LSPManager{
		clients: make(map[string]*LSPClient),
		logger:  logger,
	}
	if len(workspaces) > 0 {
		manager.workspace = workspaces[0]
	}
	return manager
}

func (m *LSPManager) InvalidateCache(rootDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleanRoot, _ := filepath.Abs(rootDir)
	for key, client := range m.clients {
		if strings.HasPrefix(key, cleanRoot+"\x00") {
			_ = client.Close()
			delete(m.clients, key)
		}
	}
	if m.logger != nil {
		m.logger.Printf("[LSP] Cache invalidated for %s", cleanRoot)
	}
}

func (m *LSPManager) ReconnectClient(rootDir, filePath string) (*LSPClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lang := lspDetectLanguage(filePath)
	cleanRoot, _ := filepath.Abs(rootDir)
	key := cleanRoot + "\x00" + lang
	// Close old client if exists
	if old, ok := m.clients[key]; ok {
		_ = old.Close()
		delete(m.clients, key)
	}
	client, err := NewLSPClient(cleanRoot, filePath, m.logger)
	if err != nil {
		return nil, err
	}
	m.clients[key] = client
	return client, nil
}

func (m *LSPManager) GetClient(filePath string) (*LSPClient, string, error) {
	absPath, err := m.resolveFilePath(filePath)
	if err != nil {
		return nil, "", err
	}
	filePath = filepath.Clean(absPath)
	rootDir := findProjectRoot(filePath)
	if rootDir == "" {
		rootDir = filepath.Dir(filePath)
	}
	lang := lspDetectLanguage(filePath)
	if lang == "" {
		return nil, "", fmt.Errorf("unsupported file type: %s", filePath)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := rootDir + "\x00" + lang
	if client, ok := m.clients[key]; ok {
		if !client.isClosed() {
			return client, rootDir, nil
		}
		_ = client.Close()
		delete(m.clients, key)
	}

	client, err := NewLSPClient(rootDir, filePath, m.logger)
	if err != nil {
		return nil, "", err
	}
	m.clients[key] = client
	return client, rootDir, nil
}

// SyncFileAndDiagnostics is the post-edit bridge used by file mutation tools.
// available=false means the language/file type has no installed server and is
// intentionally non-fatal.
func (m *LSPManager) SyncFileAndDiagnostics(filePath string) ([]LSPDiagnostic, bool, error) {
	resolved, err := m.resolveFilePath(filePath)
	if err != nil {
		return nil, false, err
	}
	filePath = resolved
	lang := lspDetectLanguage(filePath)
	server, _ := GetServerCommand(lang)
	if lang == "" || !isLSPServerAvailable(server) {
		return nil, false, nil
	}
	client, _, err := m.GetClient(filePath)
	if err != nil {
		return nil, true, err
	}
	diagnostics, err := client.GetDiagnostics(filePath)
	return diagnostics, true, err
}

func (m *LSPManager) resolveFilePath(filePath string) (string, error) {
	if !filepath.IsAbs(filePath) && m.workspace != "" {
		filePath = filepath.Join(m.workspace, filePath)
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absPath), nil
}

func (m *LSPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, client := range m.clients {
		_ = client.Close()
		delete(m.clients, key)
	}
}

func findProjectRoot(filePath string) string {
	dir := filepath.Dir(filePath)
	for {
		// Check for common project markers
		for _, marker := range []string{"go.mod", "package.json", "Cargo.toml", "setup.py", "pyproject.toml", "build.gradle", "pom.xml", ".git"} {
			if exists(filepath.Join(dir, marker)) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Format locations for LLM consumption
func formatLSPLocations(locs []LSPLocation, filePath string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d location(s):\n", len(locs)))
	for i, loc := range locs {
		displayPath := loc.URI
		if strings.HasPrefix(displayPath, "file://") {
			p := displayPath[7:]
			p = filepath.ToSlash(p)
			displayPath = p
		}
		sb.WriteString(fmt.Sprintf("  %d. %s:%d:%d-%d:%d\n",
			i+1, displayPath,
			loc.Range.Start.Line, loc.Range.Start.Character,
			loc.Range.End.Line, loc.Range.End.Character))
		if i >= 9 {
			sb.WriteString(fmt.Sprintf("  ... (%d more)\n", len(locs)-10))
			break
		}
	}
	return sb.String()
}

func FormatLSPLocations(locs []LSPLocation, filePath string) string {
	return formatLSPLocations(locs, filePath)
}

func formatDocumentSymbols(symbols []LSPDocumentSymbol, indent int) string {
	var sb strings.Builder
	prefix := strings.Repeat("  ", indent)
	for _, s := range symbols {
		kindName := symbolKindName(s.Kind)
		sb.WriteString(fmt.Sprintf("%s%s %s (L%d:%d-L%d:%d)\n",
			prefix, kindName, s.Name,
			s.Range.Start.Line, s.Range.Start.Character,
			s.Range.End.Line, s.Range.End.Character))
		if len(s.Children) > 0 {
			sb.WriteString(formatDocumentSymbols(s.Children, indent+1))
		}
	}
	return sb.String()
}

func FormatDocumentSymbols(symbols []LSPDocumentSymbol, indent int) string {
	return formatDocumentSymbols(symbols, indent)
}

func symbolKindName(kind int) string {
	names := map[int]string{
		1: "📁", 2: "📦", 3: "📂", 4: "📄", 5: "🏛️",
		6: "🔧", 7: "⚙️", 8: "📝", 9: "🏗️", 10: "📊",
		11: "⚡", 12: "🔌", 13: "📌", 14: "📎", 15: "🔑",
		23: "📋", 24: "📐", 25: "🔍", 26: "📏",
	}
	if name, ok := names[kind]; ok {
		return name
	}
	return fmt.Sprintf("[%d]", kind)
}
