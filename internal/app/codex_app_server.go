package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/projectctx"
	securitycore "mac/internal/security"
	"mac/internal/shellpolicy"
	agenttools "mac/internal/tools/agents"
	developmenttools "mac/internal/tools/development"
)

type codexAppServerProgress struct {
	Type     string
	Message  string
	ToolName string
	Status   string
	Metadata map[string]interface{}
}

type codexAppServerRunOptions struct {
	WorkDir         string
	Prompt          string
	TaskID          string
	SessionID       string
	Channel         string
	Recipient       string
	ApprovalManager *CodexApprovalManager
	OnProgress      func(codexAppServerProgress)
}

type CodexRuntime struct {
	ApprovalManager *CodexApprovalManager
}

func NewCodexRuntime(approvalManager *CodexApprovalManager) *CodexRuntime {
	if approvalManager == nil {
		approvalManager = globalCodexApprovalManager
	}
	return &CodexRuntime{ApprovalManager: approvalManager}
}

func (r *CodexRuntime) Run(ctx context.Context, opts codexAppServerRunOptions) (string, error) {
	if opts.ApprovalManager == nil && r != nil {
		opts.ApprovalManager = r.ApprovalManager
	}
	if opts.ApprovalManager == nil {
		opts.ApprovalManager = globalCodexApprovalManager
	}
	return runCodexAppServerAgent(ctx, opts)
}

type codexRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *codexRPCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return fmt.Sprintf("codex app-server error %d", e.Code)
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

type codexPendingRequest struct {
	method string
	ch     chan map[string]interface{}
}

type codexAppServerClient struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	sendMu         sync.Mutex
	pending        map[string]*codexPendingRequest
	pendingMu      sync.Mutex
	nextID         int64
	notifications  chan map[string]interface{}
	serverRequests chan map[string]interface{}
	stderrMu       sync.Mutex
	stderrLines    []string
	done           chan struct{}
	closed         bool
	closeMu        sync.Mutex
}

func newCodexAppServerClient(ctx context.Context, workDir string) (*codexAppServerClient, error) {
	exe, err := findExternalCodingExecutable("codex")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe, "app-server", "--stdio")
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "RUST_LOG=warn")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	c := &codexAppServerClient{
		cmd:            cmd,
		stdin:          stdin,
		pending:        make(map[string]*codexPendingRequest),
		notifications:  make(chan map[string]interface{}, 200),
		serverRequests: make(chan map[string]interface{}, 50),
		done:           make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		close(c.done)
	}()
	go c.readStdout(stdout)
	go c.readStderr(stderr)
	return c, nil
}

func (c *codexAppServerClient) initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]interface{}{
		"clientInfo": map[string]interface{}{
			"name":    "openkitty",
			"title":   "MACS",
			"version": CurrentVersion,
		},
		"capabilities": map[string]interface{}{},
	}, 15*time.Second)
	if err != nil {
		return err
	}
	return c.notify("initialized", nil)
}

func (c *codexAppServerClient) request(ctx context.Context, method string, params map[string]interface{}, timeout time.Duration) (map[string]interface{}, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	id := c.takeID()
	key := codexIDKey(id)
	ch := make(chan map[string]interface{}, 1)
	c.pendingMu.Lock()
	c.pending[key] = &codexPendingRequest{method: method, ch: ch}
	c.pendingMu.Unlock()

	if err := c.send(map[string]interface{}{"id": id, "method": method, "params": params}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-timer.C:
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex app-server method %q timed out after %s", method, timeout)
	case msg := <-ch:
		if errVal, ok := msg["error"]; ok && errVal != nil {
			return nil, codexRPCErrorFromValue(errVal)
		}
		if result, ok := msg["result"].(map[string]interface{}); ok {
			return result, nil
		}
		return map[string]interface{}{}, nil
	}
}

func (c *codexAppServerClient) notify(method string, params map[string]interface{}) error {
	if params == nil {
		params = map[string]interface{}{}
	}
	return c.send(map[string]interface{}{"method": method, "params": params})
}

func (c *codexAppServerClient) respond(id interface{}, result map[string]interface{}) error {
	return c.send(map[string]interface{}{"id": id, "result": result})
}

func (c *codexAppServerClient) respondError(id interface{}, code int, message string) error {
	return c.send(map[string]interface{}{
		"id": id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}

func (c *codexAppServerClient) takeNotification(timeout time.Duration) (map[string]interface{}, bool) {
	if timeout <= 0 {
		select {
		case msg := <-c.notifications:
			return msg, true
		default:
			return nil, false
		}
	}
	select {
	case msg := <-c.notifications:
		return msg, true
	case <-time.After(timeout):
		return nil, false
	}
}

func (c *codexAppServerClient) takeServerRequest() (map[string]interface{}, bool) {
	select {
	case msg := <-c.serverRequests:
		return msg, true
	default:
		return nil, false
	}
}

func (c *codexAppServerClient) exited() bool {
	if c == nil || c.done == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *codexAppServerClient) close() {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closed = true
	c.closeMu.Unlock()

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-c.done
		}
	}
}

func (c *codexAppServerClient) stderrTail(n int) string {
	if n <= 0 {
		n = 20
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if len(c.stderrLines) == 0 {
		return ""
	}
	start := len(c.stderrLines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(c.stderrLines[start:], "\n")
}

func (c *codexAppServerClient) takeID() int64 {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *codexAppServerClient) send(obj map[string]interface{}) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.closeMu.Lock()
	closed := c.closed
	c.closeMu.Unlock()
	if closed {
		return fmt.Errorf("codex app-server client is closed")
	}
	payload, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	_, err = c.stdin.Write(payload)
	return err
}

func (c *codexAppServerClient) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		msg, err := decodeCodexJSONMap(line)
		if err != nil {
			c.appendStderr(fmt.Sprintf("<non-json stdout> %s", truncate(string(line), 200)))
			continue
		}
		c.dispatch(msg)
	}
	if err := scanner.Err(); err != nil {
		c.appendStderr(fmt.Sprintf("<stdout reader error> %v", err))
	}
}

func (c *codexAppServerClient) dispatch(msg map[string]interface{}) {
	_, hasResult := msg["result"]
	_, hasError := msg["error"]
	method, hasMethod := msg["method"].(string)
	id, hasID := msg["id"]
	if hasID && (hasResult || hasError) {
		key := codexIDKey(id)
		c.pendingMu.Lock()
		pending := c.pending[key]
		delete(c.pending, key)
		c.pendingMu.Unlock()
		if pending != nil {
			select {
			case pending.ch <- msg:
			default:
			}
		}
		return
	}
	if hasID && hasMethod && method != "" {
		c.serverRequests <- msg
		return
	}
	if hasMethod && method != "" {
		select {
		case c.notifications <- msg:
		default:
			c.appendStderr(fmt.Sprintf("<dropped notification> %s", method))
		}
	}
}

func (c *codexAppServerClient) readStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		c.appendStderr(scanner.Text())
	}
}

func (c *codexAppServerClient) appendStderr(line string) {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	c.stderrLines = append(c.stderrLines, line)
	if len(c.stderrLines) > 500 {
		c.stderrLines = c.stderrLines[len(c.stderrLines)-500:]
	}
}

func decodeCodexJSONMap(data []byte) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var msg map[string]interface{}
	if err := dec.Decode(&msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func codexIDKey(id interface{}) string {
	switch v := id.(type) {
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case int:
		return fmt.Sprintf("%d", v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func codexRPCErrorFromValue(v interface{}) error {
	m, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("codex app-server error: %v", v)
	}
	errObj := &codexRPCError{Message: codexMapString(m, "message")}
	switch code := m["code"].(type) {
	case json.Number:
		if i, err := code.Int64(); err == nil {
			errObj.Code = int(i)
		}
	case float64:
		errObj.Code = int(code)
	case int:
		errObj.Code = code
	}
	errObj.Data = m["data"]
	return errObj
}

func runCodexAppServerAgent(ctx context.Context, opts codexAppServerRunOptions) (string, error) {
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}
	start := time.Now()
	initialChangedFiles := agenttools.CollectExternalAgentChangedFilesLimit(opts.WorkDir, 0)
	client, err := newCodexAppServerClient(ctx, opts.WorkDir)
	if err != nil {
		return "", err
	}
	defer client.close()

	emit := func(evt codexAppServerProgress) {
		if opts.OnProgress != nil && strings.TrimSpace(evt.Message) != "" {
			opts.OnProgress(evt)
		}
	}

	emit(codexAppServerProgress{Type: "task_start", Message: "Codex app-server started", Status: "running"})
	if err := client.initialize(ctx); err != nil {
		return "", codexAppServerWrapError(client, "codex app-server initialize failed", err)
	}

	threadRes, err := client.request(ctx, "thread/start", map[string]interface{}{"cwd": opts.WorkDir}, 15*time.Second)
	if err != nil {
		return "", codexAppServerWrapError(client, "codex thread/start failed", err)
	}
	threadID := extractCodexThreadID(threadRes)
	if threadID == "" {
		return "", fmt.Errorf("codex thread/start returned no thread id")
	}
	emit(codexAppServerProgress{Type: "session_start", Message: "Codex session started", Status: "running", Metadata: map[string]interface{}{"thread_id": threadID}})

	turnRes, err := client.request(ctx, "turn/start", map[string]interface{}{
		"threadId": threadID,
		"input": []map[string]interface{}{{
			"type": "text",
			"text": opts.Prompt,
		}},
	}, 15*time.Second)
	if err != nil {
		return "", codexAppServerWrapError(client, "codex turn/start failed", err)
	}
	turnID := extractCodexTurnID(turnRes)

	var transcript []string
	var finalText string
	fileChangeSummaries := make(map[string]string)
	turnComplete := false

	for !turnComplete {
		if req, ok := client.takeServerRequest(); ok {
			if err := handleCodexServerRequest(ctx, client, req, opts, fileChangeSummaries, emit); err != nil {
				transcript = append(transcript, "approval error: "+err.Error())
			}
			continue
		}

		select {
		case <-ctx.Done():
			if turnID != "" {
				_, _ = client.request(context.Background(), "turn/interrupt", map[string]interface{}{"threadId": threadID, "turnId": turnID}, 5*time.Second)
			}
			emit(codexAppServerProgress{Type: "cancelled", Message: "Codex task cancelled", Status: "stopped"})
			return strings.Join(transcript, "\n"), ctx.Err()
		default:
		}

		if client.exited() {
			return strings.Join(transcript, "\n"), codexAppServerWrapError(client, "codex app-server process exited", nil)
		}

		note, ok := client.takeNotification(250 * time.Millisecond)
		if !ok {
			continue
		}
		progress, extractedFinal := projectCodexNotification(note, fileChangeSummaries)
		if strings.TrimSpace(progress.Message) != "" {
			emit(progress)
			transcript = append(transcript, progress.Message)
		}
		if strings.TrimSpace(extractedFinal) != "" {
			finalText = strings.TrimSpace(extractedFinal)
		}
		if note["method"] == "turn/completed" {
			turnComplete = true
			status, turnErr := codexTurnCompletionStatus(note)
			if status != "" && status != "completed" && status != "interrupted" {
				if turnErr == "" {
					turnErr = status
				}
				if codexTurnFailureCanUseDeliveredFinal(finalText, status, turnErr) {
					transcript = append(transcript, "Codex reported turn status "+status+" after delivering final output; treated as completed because the failure was non-fatal to the user artifact.")
					break
				}
				return buildCodexAppServerReport(opts.WorkDir, start, opts.Prompt, finalText, transcript, "error: "+turnErr, initialChangedFiles), fmt.Errorf("codex turn ended status=%s: %s", status, turnErr)
			}
		}
	}

	if finalText == "" {
		finalText = "Codex completed, but returned no final text."
	}
	return buildCodexAppServerReport(opts.WorkDir, start, opts.Prompt, finalText, transcript, "success", initialChangedFiles), nil
}

func codexAppServerWrapError(client *codexAppServerClient, prefix string, err error) error {
	base := prefix
	if err != nil {
		base += ": " + err.Error()
	}
	if client != nil {
		if tail := strings.TrimSpace(client.stderrTail(20)); tail != "" {
			base += "\ncodex stderr:\n" + tail
		}
	}
	return fmt.Errorf("%s", base)
}

func extractCodexThreadID(result map[string]interface{}) string {
	if thread, ok := result["thread"].(map[string]interface{}); ok {
		for _, key := range []string{"id", "sessionId"} {
			if v := codexMapString(thread, key); v != "" {
				return v
			}
		}
	}
	for _, key := range []string{"sessionId", "threadId", "id"} {
		if v := codexMapString(result, key); v != "" {
			return v
		}
	}
	return ""
}

func extractCodexTurnID(result map[string]interface{}) string {
	if turn, ok := result["turn"].(map[string]interface{}); ok {
		if v := codexMapString(turn, "id"); v != "" {
			return v
		}
	}
	return codexMapString(result, "turnId")
}

func codexTurnCompletionStatus(note map[string]interface{}) (string, string) {
	params, _ := note["params"].(map[string]interface{})
	turn, _ := params["turn"].(map[string]interface{})
	status := codexMapString(turn, "status")
	if errObj, ok := turn["error"].(map[string]interface{}); ok {
		if msg := codexMapString(errObj, "message"); msg != "" {
			return status, msg
		}
		data, _ := json.Marshal(errObj)
		return status, string(data)
	}
	return status, ""
}

func codexTurnFailureCanUseDeliveredFinal(finalText, status, turnErr string) bool {
	finalText = strings.TrimSpace(finalText)
	if finalText == "" {
		return false
	}
	lower := strings.ToLower(status + "\n" + turnErr + "\n" + finalText)
	hasArtifact := len(extractArtifactPaths(finalText)) > 0 ||
		strings.Contains(lower, ".png") ||
		strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".pptx") ||
		strings.Contains(lower, ".pdf")
	verifyPass := strings.Contains(lower, "verify_work") && strings.Contains(lower, "pass")
	encodingOnly := strings.Contains(lower, "unicodedecodeerror") ||
		strings.Contains(lower, "codec can't decode") ||
		strings.Contains(lower, "encoding") ||
		strings.Contains(lower, "decode")
	toolOnly := strings.Contains(lower, "ptc_execute") ||
		strings.Contains(lower, "tool failed") ||
		strings.Contains(lower, "tool call failed")
	return verifyPass || (hasArtifact && (encodingOnly || toolOnly))
}

func handleCodexServerRequest(ctx context.Context, client *codexAppServerClient, req map[string]interface{}, opts codexAppServerRunOptions, fileChangeSummaries map[string]string, emit func(codexAppServerProgress)) error {
	method := codexMapString(req, "method")
	id := req["id"]
	params, _ := req["params"].(map[string]interface{})

	switch method {
	case "item/commandExecution/requestApproval":
		decision, reason := codexAutomaticCommandDecision(params)
		if decision == "decline" {
			emit(codexAppServerProgress{Type: "permission_denied", Message: reason, Status: "error", ToolName: "exec"})
		} else {
			emit(codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved command execution: " + strings.ReplaceAll(summarizeCodexCommandApproval(params), "\n", " "), Status: "running", ToolName: "exec"})
		}
		return client.respond(id, map[string]interface{}{"decision": decision})
	case "item/fileChange/requestApproval":
		summary := summarizeCodexFileApproval(params, fileChangeSummaries)
		decision := "acceptForSession"
		if codexFileChangeTargetsProtectedSystemPath(params, fileChangeSummaries) {
			decision = "decline"
			emit(codexAppServerProgress{Type: "permission_denied", Message: "Blocked Codex file change targeting a protected system path: " + summary, Status: "error", ToolName: "apply_patch"})
		} else {
			emit(codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved file changes: " + strings.ReplaceAll(summary, "\n", " "), Status: "running", ToolName: "apply_patch"})
		}
		return client.respond(id, map[string]interface{}{"decision": decision})
	case "item/permissions/requestApproval":
		emit(codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved Codex permission expansion", Status: "running"})
		return client.respond(id, codexInteractivePermissionGrantResponse())
	case "mcpServer/elicitation/request":
		return client.respond(id, map[string]interface{}{"action": "decline", "content": nil, "_meta": nil})
	default:
		return client.respondError(id, -32601, "Unsupported method: "+method)
	}
}

func codexAutomaticCommandDecision(params map[string]interface{}) (string, string) {
	command := strings.TrimSpace(codexMapString(params, "command"))
	if developmenttools.IsSystemDestructiveCommand(command) {
		return "decline", "Blocked system-destructive command: " + truncate(command, 180)
	}
	return "acceptForSession", ""
}

func codexFileChangeTargetsProtectedSystemPath(params map[string]interface{}, fileChangeSummaries map[string]string) bool {
	if root := strings.TrimSpace(codexMapString(params, "grantRoot")); root != "" && securitycore.IsProtectedPath(root) {
		return true
	}
	itemID := strings.TrimSpace(codexMapString(params, "itemId"))
	summary := strings.ToLower(strings.ReplaceAll(fileChangeSummaries[itemID], "\\", "/"))
	for _, protected := range []string{
		"/etc/", "/boot/", "/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/", "/sys/", "/proc/", "/dev/",
		"c:/windows", "c:/program files", "c:/program files (x86)", "c:/boot", "c:/efi",
	} {
		if strings.Contains(summary, protected) {
			return true
		}
	}
	return false
}

func codexApprovalMetadata(req *CodexApprovalRequest) map[string]interface{} {
	if req == nil {
		return nil
	}
	return map[string]interface{}{
		"approval_id": req.ID,
		"session_id":  req.SessionID,
		"channel":     req.Channel,
		"recipient":   req.Recipient,
	}
}

func createCodexApprovalRequest(opts codexAppServerRunOptions, kind, summary string, params map[string]interface{}) *CodexApprovalRequest {
	if opts.ApprovalManager == nil {
		opts.ApprovalManager = globalCodexApprovalManager
	}
	return opts.ApprovalManager.Create(CodexApprovalRequest{
		TaskID:        opts.TaskID,
		SessionID:     opts.SessionID,
		Channel:       opts.Channel,
		Recipient:     opts.Recipient,
		Kind:          kind,
		Summary:       summary,
		Command:       codexMapString(params, "command"),
		CWD:           codexMapString(params, "cwd"),
		ShellAnalysis: buildCodexApprovalShellAnalysis(codexMapString(params, "command")),
	})
}

func buildCodexApprovalShellAnalysis(command string) *CodexApprovalShellAnalysis {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	analysis := shellpolicy.Analyze(command)
	return &CodexApprovalShellAnalysis{
		Class:         analysis.Class,
		Risk:          securitycore.ClassifyToolAction("cli", command).String(),
		Blocked:       analysis.Blocked,
		BlockReason:   analysis.BlockReason,
		SuggestedRule: analysis.SuggestedRule,
	}
}

func waitCodexApprovalDecision(ctx context.Context, req *CodexApprovalRequest) string {
	if req == nil {
		return "decline"
	}
	select {
	case decision := <-req.Response:
		if decision == "" {
			return "decline"
		}
		return decision
	case <-ctx.Done():
		globalCodexApprovalManager.Cancel(req.ID)
		return "decline"
	}
}

func formatCodexApprovalPrompt(req *CodexApprovalRequest) string {
	if req == nil {
		return "Codex approval requested."
	}
	var b strings.Builder
	b.WriteString("Codex approval requested")
	if req.ID != "" {
		b.WriteString(" #")
		b.WriteString(req.ID)
	}
	if req.Kind != "" {
		b.WriteString(" (")
		b.WriteString(req.Kind)
		b.WriteString(")")
	}
	if req.Summary != "" {
		b.WriteString("\n")
		b.WriteString(req.Summary)
	}
	b.WriteString("\nReply `/approve ")
	b.WriteString(req.ID)
	b.WriteString("` to approve, or `/reject ")
	b.WriteString(req.ID)
	b.WriteString("` to reject.")
	return b.String()
}

func summarizeCodexCommandApproval(params map[string]interface{}) string {
	command := strings.TrimSpace(codexMapString(params, "command"))
	cwd := strings.TrimSpace(codexMapString(params, "cwd"))
	reason := strings.TrimSpace(codexMapString(params, "reason"))
	var parts []string
	if command != "" {
		parts = append(parts, "Command: "+command)
		analysis := shellpolicy.Analyze(command)
		shellSummary := fmt.Sprintf("Shell analysis: class=%s, blocked=%v", analysis.Class, analysis.Blocked)
		if analysis.BlockReason != "" {
			shellSummary += ", reason=" + analysis.BlockReason
		}
		parts = append(parts, shellSummary)
		if analysis.SuggestedRule != "" {
			parts = append(parts, "Suggested permission rule: "+analysis.SuggestedRule)
		}
	}
	if cwd != "" {
		parts = append(parts, "CWD: "+cwd)
	}
	if reason != "" {
		parts = append(parts, "Reason: "+reason)
	}
	if len(parts) == 0 {
		return "Codex requests command execution."
	}
	return strings.Join(parts, "\n")
}

func summarizeCodexFileApproval(params map[string]interface{}, fileChangeSummaries map[string]string) string {
	reason := strings.TrimSpace(codexMapString(params, "reason"))
	grantRoot := strings.TrimSpace(codexMapString(params, "grantRoot"))
	itemID := strings.TrimSpace(codexMapString(params, "itemId"))
	var parts []string
	if reason != "" {
		parts = append(parts, "Reason: "+reason)
	}
	if itemID != "" {
		if summary := strings.TrimSpace(fileChangeSummaries[itemID]); summary != "" {
			parts = append(parts, "Changes: "+summary)
		}
	}
	if grantRoot != "" {
		parts = append(parts, "Grant root: "+grantRoot)
	}
	if len(parts) == 0 {
		return "Codex requests file changes."
	}
	return strings.Join(parts, "\n")
}

func projectCodexNotification(note map[string]interface{}, fileChangeSummaries map[string]string) (codexAppServerProgress, string) {
	method := codexMapString(note, "method")
	params, _ := note["params"].(map[string]interface{})
	if method == "thread/tokenUsage/updated" {
		return codexAppServerProgress{}, ""
	}
	if method == "turn/completed" {
		return codexAppServerProgress{Type: "complete", Message: "Codex turn completed", Status: "success"}, ""
	}
	item, _ := params["item"].(map[string]interface{})
	if len(item) == 0 {
		return codexAppServerProgress{}, ""
	}
	itemType := codexMapString(item, "type")
	itemID := codexMapString(item, "id")
	if itemType == "fileChange" {
		if method == "item/started" && itemID != "" {
			fileChangeSummaries[itemID] = summarizeCodexFileChangeItem(item)
		}
		if method == "item/completed" && itemID != "" {
			delete(fileChangeSummaries, itemID)
		}
	}
	switch itemType {
	case "agentMessage":
		text := strings.TrimSpace(extractCodexText(item))
		if text == "" {
			return codexAppServerProgress{}, ""
		}
		return codexAppServerProgress{Type: "text_delta", Message: "Codex generated a reply", Status: "running"}, text
	case "commandExecution":
		return projectCodexCommandItem(method, item), ""
	case "fileChange":
		return projectCodexFileChangeItem(method, item), ""
	case "mcpToolCall", "dynamicToolCall":
		return projectCodexToolCallItem(method, item), ""
	case "imageGeneration":
		return projectCodexImageGenerationItem(method, item), ""
	case "imageView":
		return projectCodexImageViewItem(method, item), ""
	default:
		if method == "item/started" {
			return codexAppServerProgress{Type: "agent_task_progress", Message: "Codex started " + itemType, ToolName: itemType, Status: "running"}, ""
		}
		if method == "item/completed" {
			return codexAppServerProgress{Type: "tool_result", Message: "Codex completed " + itemType, ToolName: itemType, Status: "success"}, ""
		}
	}
	return codexAppServerProgress{}, ""
}

func projectCodexCommandItem(method string, item map[string]interface{}) codexAppServerProgress {
	command := strings.TrimSpace(codexMapString(item, "command"))
	cwd := strings.TrimSpace(codexMapString(item, "cwd"))
	if command == "" {
		command = "shell command"
	}
	msg := "Run command: " + truncate(command, 180)
	if cwd != "" {
		msg += " @ " + cwd
	}
	if method == "item/completed" {
		exitCode := codexMapString(item, "exitCode")
		if exitCode == "" {
			exitCode = codexMapString(item, "exit_code")
		}
		if exitCode != "" {
			msg = "Command completed: " + truncate(command, 160) + " (exit " + exitCode + ")"
		} else {
			msg = "Command completed: " + truncate(command, 180)
		}
		return codexAppServerProgress{Type: "tool_result", Message: msg, ToolName: "exec_command", Status: "success"}
	}
	return codexAppServerProgress{Type: "tool_start", Message: msg, ToolName: "exec_command", Status: "running"}
}

func projectCodexFileChangeItem(method string, item map[string]interface{}) codexAppServerProgress {
	summary := summarizeCodexFileChangeItem(item)
	if summary == "" {
		summary = "file changes"
	}
	if method == "item/completed" {
		return codexAppServerProgress{Type: "tool_result", Message: "File changes completed: " + summary, ToolName: "apply_patch", Status: "success"}
	}
	return codexAppServerProgress{Type: "tool_start", Message: "Preparing file changes: " + summary, ToolName: "apply_patch", Status: "running"}
}

func projectCodexToolCallItem(method string, item map[string]interface{}) codexAppServerProgress {
	name := codexMapString(item, "name")
	if name == "" {
		name = codexMapString(item, "toolName")
	}
	if name == "" {
		name = codexMapString(item, "type")
	}
	if method == "item/completed" {
		return codexAppServerProgress{Type: "tool_result", Message: "Tool completed: " + name, ToolName: name, Status: "success"}
	}
	return codexAppServerProgress{Type: "tool_start", Message: "Calling tool: " + name, ToolName: name, Status: "running"}
}

func projectCodexImageGenerationItem(method string, item map[string]interface{}) codexAppServerProgress {
	status := strings.TrimSpace(codexMapString(item, "status"))
	savedPath := strings.TrimSpace(codexMapString(item, "savedPath"))
	result := strings.TrimSpace(codexMapString(item, "result"))
	if method == "item/completed" {
		msg := "Image generation completed"
		if savedPath != "" {
			msg += ": " + savedPath
		} else if result != "" {
			msg += ": " + truncate(result, 180)
		}
		return codexAppServerProgress{Type: "tool_result", Message: msg, ToolName: "imageGeneration", Status: "success"}
	}
	msg := "Generating image"
	if status != "" {
		msg += ": " + status
	}
	return codexAppServerProgress{Type: "tool_start", Message: msg, ToolName: "imageGeneration", Status: "running"}
}

func projectCodexImageViewItem(method string, item map[string]interface{}) codexAppServerProgress {
	path := strings.TrimSpace(codexMapString(item, "path"))
	if path == "" {
		path = "image"
	}
	if method == "item/completed" {
		return codexAppServerProgress{Type: "tool_result", Message: "Image ready: " + path, ToolName: "imageView", Status: "success"}
	}
	return codexAppServerProgress{Type: "tool_start", Message: "Opening image: " + path, ToolName: "imageView", Status: "running"}
}

func summarizeCodexFileChangeItem(item map[string]interface{}) string {
	raw, _ := item["changes"].([]interface{})
	if len(raw) == 0 {
		if path := codexMapString(item, "path"); path != "" {
			return path
		}
		return ""
	}
	counts := map[string]int{}
	var paths []string
	for _, entry := range raw {
		ch, _ := entry.(map[string]interface{})
		if len(ch) == 0 {
			continue
		}
		kind := "update"
		if kindMap, ok := ch["kind"].(map[string]interface{}); ok {
			if t := codexMapString(kindMap, "type"); t != "" {
				kind = t
			}
		} else if t := codexMapString(ch, "kind"); t != "" {
			kind = t
		}
		counts[kind]++
		if p := codexMapString(ch, "path"); p != "" {
			paths = append(paths, p)
		}
	}
	var countParts []string
	for kind, n := range counts {
		countParts = append(countParts, fmt.Sprintf("%d %s", n, kind))
	}
	sort.Strings(countParts)
	preview := strings.Join(paths, ", ")
	if len(paths) > 3 {
		preview = strings.Join(paths[:3], ", ") + fmt.Sprintf(", +%d more", len(paths)-3)
	}
	if len(countParts) == 0 {
		return preview
	}
	if preview == "" {
		return strings.Join(countParts, ", ")
	}
	return strings.Join(countParts, ", ") + ": " + preview
}

func extractCodexText(item map[string]interface{}) string {
	for _, key := range []string{"text", "message", "content"} {
		if s := strings.TrimSpace(codexMapString(item, key)); s != "" {
			return s
		}
	}
	if raw, ok := item["content"].([]interface{}); ok {
		var parts []string
		for _, entry := range raw {
			switch v := entry.(type) {
			case string:
				parts = append(parts, v)
			case map[string]interface{}:
				if s := codexMapString(v, "text"); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func buildCodexAppServerReport(workDir string, start time.Time, prompt, finalText string, transcript []string, status string, initialChangedFiles []string) string {
	elapsed := time.Since(start)
	changedFiles := agenttools.DiffExternalAgentChangedFiles(initialChangedFiles, agenttools.CollectExternalAgentChangedFilesLimit(workDir, 0))
	tool := agenttools.NewExternalAgentTool(workDir, projectctx.ExternalAgentProjectContext)
	output := strings.TrimSpace(finalText)
	if len(transcript) > 0 {
		outputForReport := output + "\n\n[progress]\n" + strings.Join(transcript, "\n")
		reportPath, reportErr := tool.WriteExecutionReport(workDir, "codex", fmt.Sprintf("codex_app_%d", time.Now().UnixNano()), status, elapsed, prompt, outputForReport, changedFiles)
		return formatCodexAppServerOutput(status, elapsed, reportPath, reportErr, changedFiles, output, transcript)
	}
	reportPath, reportErr := tool.WriteExecutionReport(workDir, "codex", fmt.Sprintf("codex_app_%d", time.Now().UnixNano()), status, elapsed, prompt, output, changedFiles)
	return formatCodexAppServerOutput(status, elapsed, reportPath, reportErr, changedFiles, output, transcript)
}

func formatCodexAppServerOutput(status string, elapsed time.Duration, reportPath string, reportErr error, changedFiles []string, output string, transcript []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[External Agent: codex-app-server] [Status: %s] [Duration: %.1fs]\n", status, elapsed.Seconds()))
	if reportErr == nil && reportPath != "" {
		b.WriteString("[Report: ")
		b.WriteString(reportPath)
		b.WriteString("]\n")
	} else if reportErr != nil {
		b.WriteString("[Report error: ")
		b.WriteString(reportErr.Error())
		b.WriteString("]\n")
	}
	if len(changedFiles) > 0 {
		b.WriteString("[Changed files]\n")
		b.WriteString(strings.Join(agenttools.PrefixLines(changedFiles, "- "), "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(output))
	return strings.TrimSpace(b.String())
}

func codexMapString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

type CodexApprovalRequest struct {
	ID            string                      `json:"id"`
	TaskID        string                      `json:"task_id,omitempty"`
	SessionID     string                      `json:"session_id,omitempty"`
	Channel       string                      `json:"channel,omitempty"`
	Recipient     string                      `json:"recipient,omitempty"`
	Kind          string                      `json:"kind,omitempty"`
	Summary       string                      `json:"summary,omitempty"`
	Command       string                      `json:"command,omitempty"`
	CWD           string                      `json:"cwd,omitempty"`
	ShellAnalysis *CodexApprovalShellAnalysis `json:"shell_analysis,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	Response      chan string                 `json:"-"`
}

type CodexApprovalShellAnalysis struct {
	Class         shellpolicy.CommandClass `json:"class"`
	Risk          string                   `json:"risk"`
	Blocked       bool                     `json:"blocked"`
	BlockReason   string                   `json:"block_reason,omitempty"`
	SuggestedRule string                   `json:"suggested_rule,omitempty"`
}

type CodexApprovalManager struct {
	mu      sync.Mutex
	seq     int64
	pending map[string]*CodexApprovalRequest
}

var globalCodexApprovalManager = NewCodexApprovalManager()

func NewCodexApprovalManager() *CodexApprovalManager {
	return &CodexApprovalManager{pending: make(map[string]*CodexApprovalRequest)}
}

func (m *CodexApprovalManager) Create(req CodexApprovalRequest) *CodexApprovalRequest {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	req.ID = fmt.Sprintf("cxap_%06d", m.seq)
	req.CreatedAt = time.Now()
	req.Response = make(chan string, 1)
	m.pending[req.ID] = &req
	return &req
}

func (m *CodexApprovalManager) Resolve(id, decision string) (*CodexApprovalRequest, bool) {
	if m == nil {
		return nil, false
	}
	id = strings.TrimSpace(id)
	decision = normalizeCodexApprovalDecision(decision)
	if id == "" || decision == "" {
		return nil, false
	}
	m.mu.Lock()
	req, ok := m.pending[id]
	if ok {
		delete(m.pending, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case req.Response <- decision:
	default:
	}
	return req, true
}

func (m *CodexApprovalManager) Cancel(id string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.pending, strings.TrimSpace(id))
	m.mu.Unlock()
}

func (m *CodexApprovalManager) Get(id string) (*CodexApprovalRequest, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.pending[strings.TrimSpace(id)]
	if !ok {
		return nil, false
	}
	copyReq := cloneCodexApprovalRequest(req)
	return &copyReq, true
}

func (m *CodexApprovalManager) List(sessionID string) []CodexApprovalRequest {
	if m == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CodexApprovalRequest, 0, len(m.pending))
	for _, req := range m.pending {
		if sessionID != "" && req.SessionID != sessionID {
			continue
		}
		copyReq := cloneCodexApprovalRequest(req)
		out = append(out, copyReq)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func cloneCodexApprovalRequest(req *CodexApprovalRequest) CodexApprovalRequest {
	if req == nil {
		return CodexApprovalRequest{}
	}
	copyReq := *req
	copyReq.Response = nil
	if req.ShellAnalysis != nil {
		analysis := *req.ShellAnalysis
		copyReq.ShellAnalysis = &analysis
	}
	return copyReq
}

func normalizeCodexApprovalDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "accept", "approve", "approved", "once":
		return "accept"
	case "acceptforsession", "session", "always":
		return "acceptForSession"
	case "decline", "deny", "reject", "rejected", "cancel":
		return "decline"
	default:
		return ""
	}
}

var codexVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func codexAppServerSupported(ctx context.Context) (bool, string) {
	exe, err := findExternalCodingExecutable("codex")
	if err != nil {
		return false, err.Error()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, exe, "--version").CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	match := codexVersionRe.FindStringSubmatch(string(out))
	if len(match) != 4 {
		return true, strings.TrimSpace(string(out))
	}
	var major, minor, patch int
	fmt.Sscanf(match[0], "%d.%d.%d", &major, &minor, &patch)
	if major == 0 && minor < 125 {
		return false, fmt.Sprintf("codex %d.%d.%d does not support app-server; upgrade with npm i -g @openai/codex", major, minor, patch)
	}
	return true, strings.TrimSpace(string(out))
}

func resolveCodexWorkDir(workDir string) string {
	if strings.TrimSpace(workDir) != "" {
		return workDir
	}
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	return filepath.Clean(".")
}
