package ptc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mac/internal/processutil"
)

const (
	ptcMaxToolCalls   = 80
	ptcTimeoutSeconds = 300 // 借鉴hermes: 5分钟
	ptcMaxStdoutBytes = 50000
	CallLimitPerRun   = 80
)

type ptcRuntimeMode string

const (
	ptcRuntimeCompat  ptcRuntimeMode = "compat"
	ptcRuntimeGuarded ptcRuntimeMode = "guarded"
)

type Registry interface {
	ListNames() []string
	HasTool(string) bool
	ExecuteForPTC(context.Context, string, map[string]interface{}) (string, bool, error)
}

type StateScope func(context.Context) (string, bool)

// PTCServer exposes registry-backed tool execution over HTTP for Python scripts.
// 借鉴hermes PTC: LLM生成脚本→沙箱执行→中间结果不进上下文
type PTCServer struct {
	registry   Registry
	listener   net.Listener
	port       int
	callCount  atomic.Int32
	maxCalls   int32
	mu         sync.Mutex
	runMu      sync.Mutex
	runCtxMu   sync.RWMutex
	runCtx     context.Context
	runRuntime ptcRuntimeMode
	running    bool
	token      string // simple auth token per session
}

// NewPTCServer creates the server (doesn't start listening yet).
func NewServer(registry Registry) *PTCServer {
	return &PTCServer{
		registry: registry,
		maxCalls: ptcMaxToolCalls,
		token:    fmt.Sprintf("ptc_%d", time.Now().UnixNano()),
	}
}

// Start begins listening on a random localhost port.
func (s *PTCServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("ptc listen: %w", err)
	}
	s.listener = ln
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.running = true

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tools", s.handleListTools)
	mux.HandleFunc("/api/v1/tools/", s.handleToolCall)
	mux.HandleFunc("/health", s.handleHealth)

	go func() {
		http.Serve(ln, mux)
	}()
	log.Printf("[PTC] 服务器启动: http://127.0.0.1:%d", s.port)
	return nil
}

func (s *PTCServer) setRunContext(ctx context.Context) {
	s.setRunExecution(ctx, ptcRuntimeCompat)
}

func (s *PTCServer) setRunExecution(ctx context.Context, runtime ptcRuntimeMode) {
	s.runCtxMu.Lock()
	defer s.runCtxMu.Unlock()
	s.runCtx = ctx
	s.runRuntime = runtime
}

func (s *PTCServer) currentRunContext() context.Context {
	s.runCtxMu.RLock()
	defer s.runCtxMu.RUnlock()
	return s.runCtx
}

func (s *PTCServer) currentRunRuntime() ptcRuntimeMode {
	s.runCtxMu.RLock()
	defer s.runCtxMu.RUnlock()
	if s.runRuntime == ptcRuntimeGuarded {
		return ptcRuntimeGuarded
	}
	return ptcRuntimeCompat
}

// Stop shuts down the server.
func (s *PTCServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
	s.running = false
	_ = os.RemoveAll(s.guardedStateRoot())
}

func (s *PTCServer) guardedStateRoot() string {
	digest := sha256.Sum256([]byte(s.token))
	return filepath.Join(os.TempDir(), "openkitty-ptc-state", fmt.Sprintf("%x", digest[:8]))
}

// Port returns the listening port, or 0 if not started.
func (s *PTCServer) Port() int { return s.port }

// Token returns the session auth token.
func (s *PTCServer) Token() string { return s.token }

// ResetCallCount resets the per-run call counter.
func (s *PTCServer) ResetCallCount() { s.callCount.Store(0) }

// ClientCode generates the Python sandbox helper that scripts import.
func (s *PTCServer) ClientCode() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`# PTC Sandbox Helper — 借鉴hermes PTC
import json, urllib.request, os

_PTC_URL = "http://127.0.0.1:%d"
_PTC_TOKEN = "%s"

def _ptc_call(tool_name, **kwargs):
    """Call an openkitty tool via HTTP RPC."""
    global _ptc_call_count
    _ptc_call_count = getattr(_ptc_call, '_count', 0) + 1
    _ptc_call._count = _ptc_call_count
    if _ptc_call_count > %d:
        raise RuntimeError("Tool call limit exceeded (%d)")
    req = urllib.request.Request(
        f"{_PTC_URL}/api/v1/tools/{tool_name}",
        data=json.dumps(kwargs).encode(),
        headers={"Content-Type": "application/json", "X-PTC-Token": _PTC_TOKEN},
    )
    resp = urllib.request.urlopen(req, timeout=30)
    result = json.loads(resp.read())
    if result.get("error"):
        raise RuntimeError(result["error"])
    return result["content"]

`, s.port, s.token, ptcMaxToolCalls, CallLimitPerRun))

	// Generate convenience wrappers for each tool
	for _, name := range s.registry.ListNames() {
		if _, ok := s.allowedToolsForRuntime(s.currentRunRuntime())[name]; !ok {
			continue
		}
		safeName := strings.ReplaceAll(name, "-", "_")
		b.WriteString(fmt.Sprintf(`def %s(**kwargs):
    """Convenience wrapper for tool '%s'."""
    return _ptc_call("%s", **kwargs)

`, safeName, name, name))
	}
	return b.String()
}

// Allowed tools for PTC sandbox — 借鉴hermes SANDBOX_ALLOWED_TOOLS
func (s *PTCServer) allowedTools() map[string]bool {
	return map[string]bool{
		"web_fetch":  true,
		"read_file":  true,
		"write_file": true,
		"edit_file":  true,
		"grep":       true,
		"list_dir":   true,
		"glob":       true,
		"code_check": true,
		"cli":        true,
		"git":        true,
		"notepad":    true,
		"execute":    true, // terminal alias
	}
}

func (s *PTCServer) allowedToolsForRuntime(runtime ptcRuntimeMode) map[string]bool {
	if runtime != ptcRuntimeGuarded {
		return s.allowedTools()
	}
	// Guarded Python must cross the registry for privileged operations. Shell
	// and Git aliases are intentionally absent because they would recreate an
	// unrestricted host process with the full service environment.
	return map[string]bool{
		"web_fetch":  true,
		"read_file":  true,
		"write_file": true,
		"edit_file":  true,
		"grep":       true,
		"list_dir":   true,
		"glob":       true,
		"code_check": true,
	}
}

func (s *PTCServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *PTCServer) handleListTools(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-PTC-Token") != s.token {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	names := []string{}
	allowed := s.allowedToolsForRuntime(s.currentRunRuntime())
	for _, name := range s.registry.ListNames() {
		if _, ok := allowed[name]; ok {
			names = append(names, name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"tools": names})
}

func (s *PTCServer) handleToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-PTC-Token") != s.token {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}

	toolName := strings.TrimPrefix(r.URL.Path, "/api/v1/tools/")
	if !s.registry.HasTool(toolName) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": fmt.Sprintf("unknown tool: %s", toolName)})
		return
	}
	if !s.allowedToolsForRuntime(s.currentRunRuntime())[toolName] {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": fmt.Sprintf("tool not allowed in sandbox: %s", toolName)})
		return
	}

	// Enforce call limit
	count := s.callCount.Add(1)
	if count > s.maxCalls {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": fmt.Sprintf("tool call limit exceeded (%d)", s.maxCalls)})
		return
	}

	// Parse params from body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": fmt.Sprintf("read request body: %v", err)})
		return
	}
	var params map[string]interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &params); err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"error": fmt.Sprintf("invalid JSON body: %v", err)})
			return
		}
	}

	parent := s.currentRunContext()
	if parent == nil {
		parent = r.Context()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	content, succeeded, err := s.registry.ExecuteForPTC(ctx, toolName, params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}
	if !succeeded {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"error": content})
		return
	}

	// Truncate result if too large
	content = truncatePTCTextPreservingTail(content, ptcMaxStdoutBytes, "PTC tool result")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"content": content})
}

// PTCExecuteTool is the tool exposed to LLM for script execution.
type PTCExecuteTool struct {
	server          *PTCServer
	workspace       string
	fileLogger      *log.Logger
	stateScope      StateScope
	runtimeOverride ptcRuntimeMode // tests and explicit host wiring; empty uses OPENKITTY_PTC_RUNTIME
}

func NewExecuteTool(server *PTCServer, workspace string, logger *log.Logger, stateScope StateScope) *PTCExecuteTool {
	return &PTCExecuteTool{server: server, workspace: workspace, fileLogger: logger, stateScope: stateScope}
}

func (t *PTCExecuteTool) Name() string       { return "ptc_execute" }
func (t *PTCExecuteTool) MaxResultSize() int { return 50000 }
func (t *PTCExecuteTool) Description() string {
	return `Execute a Python script that can call openkitty tools as functions. Two modes:
- "stateless" (default): fresh Python process each call.
- "stateful": persistent kernel, variables survive across calls within the same task. Use for iterative exploration (load data once, query multiple times).
In the default compatibility runtime, the script has access to web_fetch, read_file, write_file, grep, list_dir, glob, code_check, and terminal; Python inherits the configured environment and runs with the workspace as cwd. For files from use_skill runtime_dir, use absolute paths or subprocess.run(..., cwd=runtime_dir); relative scripts/... paths do not resolve automatically.
Compatibility mode is not an OS security sandbox. A host can explicitly set OPENKITTY_PTC_RUNTIME=guarded to use an ephemeral cwd, scrub inherited credentials, task-scope state, block direct workspace/network/subprocess access, remove shell/Git RPC helpers, and require privileged operations to use the remaining registry-backed helpers. Guarded mode is defense in depth and does not claim VM-level isolation.
Financial charts must be built from exact retrieved observations. Scripts that use random/synthetic/reconstructed financial series are rejected; use a dedicated, visibly labeled scenario-simulation workflow instead.`
}

func (t *PTCExecuteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"script": map[string]interface{}{
				"type":        "string",
				"description": "Python code to execute. Tool functions are auto-imported. Results printed to stdout.",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "Execution mode: 'stateless' (fresh process, default) or 'stateful' (persistent kernel, variables survive across calls)",
				"enum":        []string{"stateless", "stateful"},
			},
		},
		"required": []string{"script"},
	}
}

func (t *PTCExecuteTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	script, _ := params["script"].(string)
	if script == "" {
		return "", fmt.Errorf("script parameter is required")
	}
	runtimeMode, runtimeErr := t.runtimeMode()
	if runtimeErr != nil {
		return "", runtimeErr
	}
	t.server.runMu.Lock()
	defer t.server.runMu.Unlock()
	t.server.setRunExecution(ctx, runtimeMode)
	defer t.server.setRunContext(nil)
	// Ensure server is running
	if err := t.server.Start(); err != nil {
		return "", fmt.Errorf("ptc server start failed: %w", err)
	}
	t.server.ResetCallCount()

	// Compatibility mode preserves the historical workspace cwd and inherited
	// environment. Guarded mode is opt-in and uses a private ephemeral cwd.
	executionDir := t.workspace
	if runtimeMode == ptcRuntimeGuarded {
		var err error
		executionDir, err = os.MkdirTemp("", "openkitty-ptc-")
		if err != nil {
			return "", fmt.Errorf("create guarded PTC runtime: %w", err)
		}
		if err := os.Chmod(executionDir, 0700); err != nil {
			_ = os.RemoveAll(executionDir)
			return "", fmt.Errorf("protect guarded PTC runtime: %w", err)
		}
		defer os.RemoveAll(executionDir)
	}

	// Write the RPC helper into the effective runtime directory. Guarded mode
	// therefore never drops generated Python into the user's workspace.
	helperPath := filepath.Join(executionDir, "ptc_client.py")
	preamble := `# Auto-generated PTC sandbox helper
import sys
_ptc_call_count = 0
`
	if err := os.WriteFile(helperPath, []byte(preamble+t.server.ClientCode()), 0600); err != nil {
		return "", fmt.Errorf("write PTC helper: %w", err)
	}
	defer os.Remove(helperPath)

	// Stateful mode: persist namespace via pickle for iterative exploration
	mode, _ := params["mode"].(string)
	stateFile := filepath.Join(t.workspace, ".openkitty", "storage", ".ptc_state.pkl")
	stateScoped := true
	if runtimeMode == ptcRuntimeGuarded {
		var scoped bool
		stateFile, scoped = t.guardedStateFile(ctx, executionDir)
		stateScoped = scoped
	}
	statePreamble := ""
	stateEpilogue := ""
	if mode == "stateful" {
		if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
			return "", fmt.Errorf("create PTC state directory: %w", err)
		}
		statePreamble = fmt.Sprintf(`
import pickle, os as _os
_state_file = %q
if _os.path.exists(_state_file):
    try:
        with open(_state_file, 'rb') as f:
            globals().update(pickle.load(f))
        print("[PTC state restored]")
    except Exception as _e:
        print(f"[PTC state restore failed: {_e}]")
`, stateFile)
		stateEpilogue = fmt.Sprintf(`
# --- Save state for next call ---
try:
    _saved = {}
    for _key, _value in list(globals().items()):
        if (_key.startswith('_') or _key in ('sys', 'os', 'json', 're', 'pickle', 'ptc_client')
                or callable(_value)):
            continue
        try:
            pickle.dumps(_value)
        except Exception:
            continue
        _saved[_key] = _value
    with open(_state_file, 'wb') as f:
        pickle.dump(_saved, f)
    print("[PTC state saved]")
except Exception as _e:
    print(f"[PTC state save failed: {_e}]")
`)
	}

	guardPreamble := ""
	if runtimeMode == ptcRuntimeGuarded {
		guardPreamble = guardedPTCPreamble(t.workspace, executionDir, stateFile, t.server.Port())
	}

	// Build the full script
	fullScript := fmt.Sprintf(`
import sys
sys.path.insert(0, %q)
try:
    import ptc_client
except ImportError as e:
    print(f"PTC init error: {e}", file=sys.stderr)
    sys.exit(1)
%s
%s
# User script follows
%s
%s
`, executionDir, statePreamble, guardPreamble, script, stateEpilogue)

	// Execute in subprocess
	ctx, cancel := context.WithTimeout(ctx, ptcTimeoutSeconds*time.Second)
	defer cancel()

	pythonExe, pythonArgs := processutil.ResolvePythonCommand()
	cmdArgs := append(append([]string{}, pythonArgs...), "-c", fullScript)
	cmd := exec.CommandContext(ctx, pythonExe, cmdArgs...)
	cmd.Dir = executionDir
	// PTC participates in the trusted operational workflow. Preserve the
	// process environment so scripts can use explicitly configured credentials;
	// account information is sanitized only when content is exported.
	cmd.Env = ensurePythonUTF8Env(os.Environ())
	if runtimeMode == ptcRuntimeGuarded {
		cmd.Env = guardedPTCEnv(os.Environ(), executionDir)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.logf("[PTC] Executing script (%d chars) with %s %s runtime=%s", len(script), pythonExe, strings.Join(pythonArgs, " "), runtimeMode)
	startTime := time.Now()
	var err error
	if runtimeMode == ptcRuntimeGuarded {
		err = processutil.RunTree(cmd, true)
	} else {
		err = cmd.Run()
	}
	elapsed := time.Since(startTime)

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	if err == nil && looksLikePythonReaderUnicodeDecodeError(stderrStr) {
		stderrStr = "[Non-fatal encoding warning] A background output reader hit UnicodeDecodeError after the subprocess completed. The command exit code was 0. Set PYTHONIOENCODING=utf-8 or decode output with errors='replace'."
	}

	// Truncate stdout if needed
	stdoutStr = truncatePTCTextPreservingTail(stdoutStr, ptcMaxStdoutBytes, "stdout")

	var result strings.Builder
	result.WriteString(fmt.Sprintf("## PTC Execution (%.1fs, %d tool calls)\n\n", elapsed.Seconds(), t.server.callCount.Load()))
	if runtimeMode == ptcRuntimeGuarded {
		result.WriteString("[PTC runtime: guarded; privileged operations require registry-backed helpers]\n")
		if mode == "stateful" && !stateScoped {
			result.WriteString("[PTC state: execution-local because no task/session identity was available]\n")
		}
	}
	if stdoutStr != "" {
		result.WriteString(stdoutStr)
	}
	if stderrStr != "" {
		if len(stderrStr) > 5000 {
			stderrStr = stderrStr[:5000] + "\n...[stderr truncated]..."
		}
		result.WriteString("\n\n### Stderr\n```\n" + stderrStr + "\n```")
	}
	if err != nil {
		result.WriteString(fmt.Sprintf("\n\n### Execution Error\n%v", err))
	}

	t.logf("[PTC] Done: %.1fs, stdout=%d, stderr=%d, calls=%d, runtime=%s, err=%v",
		elapsed.Seconds(), len(stdoutStr), len(stderrStr), t.server.callCount.Load(), runtimeMode, err)

	return result.String(), nil
}

func (t *PTCExecuteTool) runtimeMode() (ptcRuntimeMode, error) {
	value := strings.TrimSpace(strings.ToLower(string(t.runtimeOverride)))
	if value == "" {
		value = strings.TrimSpace(strings.ToLower(os.Getenv("OPENKITTY_PTC_RUNTIME")))
	}
	switch value {
	case "", string(ptcRuntimeCompat):
		return ptcRuntimeCompat, nil
	case string(ptcRuntimeGuarded):
		return ptcRuntimeGuarded, nil
	default:
		return "", fmt.Errorf("invalid OPENKITTY_PTC_RUNTIME %q: use %q or %q", value, ptcRuntimeCompat, ptcRuntimeGuarded)
	}
}

func (t *PTCExecuteTool) logf(format string, args ...interface{}) {
	if t.fileLogger != nil {
		t.fileLogger.Printf(format, args...)
	}
}

func (t *PTCExecuteTool) guardedStateFile(ctx context.Context, executionDir string) (string, bool) {
	if t.stateScope == nil {
		return filepath.Join(executionDir, ".ptc_state.pkl"), false
	}
	scope, scoped := t.stateScope(ctx)
	if !scoped || strings.TrimSpace(scope) == "" {
		return filepath.Join(executionDir, ".ptc_state.pkl"), false
	}
	digest := sha256.Sum256([]byte(t.server.token + "\x00" + scope))
	return filepath.Join(t.server.guardedStateRoot(), fmt.Sprintf("%x.pkl", digest[:16])), true
}

func guardedPTCEnv(environ []string, executionDir string) []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true,
		"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TZ": true,
	}
	filtered := make([]string, 0, len(allowed)+8)
	seen := make(map[string]bool)
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if !ok || !allowed[upper] || seen[upper] {
			continue
		}
		filtered = append(filtered, entry)
		seen[upper] = true
	}
	for _, key := range []string{"TEMP", "TMP", "TMPDIR"} {
		filtered = upsertEnv(filtered, key, executionDir)
	}
	filtered = ensurePythonUTF8Env(filtered)
	filtered = upsertEnv(filtered, "PYTHONNOUSERSITE", "1")
	filtered = upsertEnv(filtered, "PYTHONDONTWRITEBYTECODE", "1")
	filtered = upsertEnv(filtered, "OPENKITTY_PTC_RUNTIME", string(ptcRuntimeGuarded))
	return filtered
}

func guardedPTCPreamble(workspace, executionDir, stateFile string, port int) string {
	return fmt.Sprintf(`
# --- OpenKitty guarded PTC runtime ---
# This is a defense-in-depth Python audit policy, not VM isolation. The fixed
# roots are captured before user code runs so changing sys.path cannot grant
# direct filesystem access. Registry-backed ptc_client calls remain available.
def _ptc_install_guard():
    import os as _guard_os, sys as _guard_sys
    _execution_dir = _guard_os.path.normcase(_guard_os.path.realpath(%q))
    _workspace = _guard_os.path.normcase(_guard_os.path.realpath(%q))
    _state_file_guard = _guard_os.path.normcase(_guard_os.path.realpath(%q))
    _read_roots = []

    def _real(path):
        if path is None:
            return _execution_dir
        if isinstance(path, int):
            return None
        try:
            return _guard_os.path.normcase(_guard_os.path.realpath(_guard_os.fsdecode(_guard_os.fspath(path))))
        except (TypeError, ValueError, OSError):
            return None

    def _add_read_root(path):
        resolved = _real(path)
        if resolved and resolved not in _read_roots:
            _read_roots.append(resolved)

    _add_read_root(_execution_dir)
    _add_read_root(getattr(_guard_sys, 'base_prefix', ''))
    _add_read_root(getattr(_guard_sys, 'prefix', ''))
    for _entry in tuple(_guard_sys.path):
        if _entry:
            _add_read_root(_entry)
    _read_roots = tuple(_read_roots)

    def _within(path, roots):
        resolved = _real(path)
        if resolved is None:
            return False
        for root in roots:
            try:
                if resolved == root or _guard_os.path.commonpath((resolved, root)) == root:
                    return True
            except (ValueError, OSError):
                pass
        return False

    def _can_read(path):
        resolved = _real(path)
        return resolved == _state_file_guard or _within(resolved, _read_roots)

    def _can_write(path):
        resolved = _real(path)
        return resolved == _state_file_guard or _within(resolved, (_execution_dir,))

    _write_flags = (_guard_os.O_WRONLY | _guard_os.O_RDWR | _guard_os.O_CREAT |
                    _guard_os.O_TRUNC | _guard_os.O_APPEND)
    _mutation_events = {
        'os.remove', 'os.rename', 'os.rmdir', 'os.mkdir', 'os.chmod',
        'os.chown', 'os.link', 'os.symlink', 'os.truncate', 'os.utime'
    }
    _blocked_process_events = {
        'subprocess.Popen', 'os.system', 'os.fork', 'os.forkpty',
        'os.posix_spawn', 'os.posix_spawnp', 'os.startfile'
    }

    def _deny(event, detail=''):
        suffix = f': {detail}' if detail else ''
        raise PermissionError(f'guarded PTC blocked {event}{suffix}; use a registry-backed ptc_client helper')

    def _audit(event, args):
        if event == 'open':
            path = args[0] if args else None
            mode = args[1] if len(args) > 1 else 'r'
            flags = args[2] if len(args) > 2 and isinstance(args[2], int) else 0
            wants_write = any(ch in str(mode or '') for ch in 'wax+') or bool(flags & _write_flags)
            if wants_write:
                if not _can_write(path):
                    _deny(event, path)
            elif not _can_read(path):
                _deny(event, path)
            return
        if event in _mutation_events:
            paths = args[:2] if event in ('os.rename', 'os.link', 'os.symlink') else args[:1]
            if not paths or any(not _can_write(path) for path in paths):
                _deny(event, paths)
            return
        if event in ('os.listdir', 'os.scandir'):
            path = args[0] if args else _execution_dir
            if not _can_read(path):
                _deny(event, path)
            return
        if event == 'os.chdir':
            if not args or not _within(args[0], (_execution_dir,)):
                _deny(event, args[0] if args else '')
            return
        if event in _blocked_process_events or event.startswith('os.exec') or event.startswith('os.spawn'):
            _deny(event)
        if event == 'socket.connect':
            address = args[1] if len(args) > 1 else None
            if not (isinstance(address, tuple) and len(address) >= 2 and
                    address[0] in ('127.0.0.1', '::1') and int(address[1]) == %d):
                _deny(event, address)
            return
        if event == 'socket.getaddrinfo':
            host = args[0] if args else None
            target_port = args[1] if len(args) > 1 else None
            if host not in ('127.0.0.1', '::1') or int(target_port) != %d:
                _deny(event, (host, target_port))
            return
        if event in ('socket.bind', 'socket.sendto') or event.startswith('socket.gethostby'):
            _deny(event)
        if event.startswith('winreg.'):
            _deny(event)

    _guard_sys.addaudithook(_audit)

_ptc_install_guard()
del _ptc_install_guard
`, executionDir, workspace, stateFile, port, port)
}

func truncatePTCTextPreservingTail(text string, maxBytes int, label string) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	marker := fmt.Sprintf("\n...[%s truncated; tail preserved]...\n", label)
	budget := maxBytes - len(marker)
	if budget <= 1 {
		return strings.ToValidUTF8(text[:max(0, maxBytes)], "?")
	}
	headBytes := budget * 3 / 4
	tailBytes := budget - headBytes
	head := strings.ToValidUTF8(text[:headBytes], "?")
	tail := strings.ToValidUTF8(text[len(text)-tailBytes:], "?")
	return head + marker + tail
}

func ensurePythonUTF8Env(environ []string) []string {
	env := append([]string{}, environ...)
	env = upsertEnv(env, "PYTHONIOENCODING", "utf-8")
	env = upsertEnv(env, "PYTHONUTF8", "1")
	return env
}

func upsertEnv(environ []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	for i, env := range environ {
		if strings.HasPrefix(strings.ToUpper(env), prefix) {
			environ[i] = key + "=" + value
			return environ
		}
	}
	return append(environ, key+"="+value)
}

func looksLikePythonReaderUnicodeDecodeError(stderr string) bool {
	lower := strings.ToLower(stderr)
	if !strings.Contains(lower, "unicodedecodeerror") {
		return false
	}
	return strings.Contains(lower, "thread") ||
		strings.Contains(lower, "_readerthread") ||
		strings.Contains(lower, "subprocess.py")
}
