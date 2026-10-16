package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HookType identifies when a hook runs.
// Claude Code borrowed: HookEvent types
type HookType string

const (
	HookPreToolUse   HookType = "pre_tool_use"
	HookPostToolUse  HookType = "post_tool_use"
	HookPreCompact   HookType = "pre_compact"
	HookPostCompact  HookType = "post_compact"
	HookSessionStart HookType = "session_start"
	HookStop         HookType = "stop"
)

// HookContext provides information to hook scripts.
type HookContext struct {
	HookType  HookType               `json:"hook_type"`
	AgentName string                 `json:"agent_name"`
	TaskID    string                 `json:"task_id,omitempty"`
	ToolName  string                 `json:"tool_name,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"`
	Result    string                 `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// HookResult is returned by a hook execution.
type HookExecutionResult struct {
	Allowed   bool                   `json:"allowed"`            // false = block the action
	Modified  bool                   `json:"modified,omitempty"` // true = params/result were modified
	NewParams map[string]interface{} `json:"new_params,omitempty"`
	NewResult string                 `json:"new_result,omitempty"`
	Message   string                 `json:"message,omitempty"`
}

// HookManager discovers and executes hooks from .openkitty/hooks/.
type HookManager struct {
	hooksDir    string
	enabled     bool
	maxTimeout  time.Duration
	maxRetries  int
	sandboxMode bool
}

func NewHookManager(hooksDir string) *HookManager {
	if hooksDir == "" {
		hooksDir = ".openkitty/hooks"
	}
	os.MkdirAll(hooksDir, 0755)
	return &HookManager{
		hooksDir:    hooksDir,
		enabled:     true,
		maxTimeout:  30 * time.Second,
		maxRetries:  2,
		sandboxMode: true,
	}
}

// auditLog writes a structured audit entry for hook executions.
func (hm *HookManager) auditLog(hookType HookType, agentName, toolName string, allowed bool, duration time.Duration, err error) {
	status := "allowed"
	if !allowed {
		status = "blocked"
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	log.Printf("[HOOK:AUDIT] type=%s agent=%s tool=%s status=%s duration=%v error=%s",
		hookType, agentName, toolName, status, duration, errStr)
}

func (hm *HookManager) SetConfig(timeout time.Duration, retries int, sandbox bool) {
	hm.maxTimeout = timeout
	hm.maxRetries = retries
	hm.sandboxMode = sandbox
}

func (hm *HookManager) MaxTimeout() time.Duration {
	if hm == nil {
		return 0
	}
	return hm.maxTimeout
}

func (hm *HookManager) MaxRetries() int {
	if hm == nil {
		return 0
	}
	return hm.maxRetries
}

func (hm *HookManager) SandboxMode() bool {
	return hm != nil && hm.sandboxMode
}

func (hm *HookManager) ListRegistered() []HookType {
	var r []HookType
	for _, ht := range []HookType{HookPreToolUse, HookPostToolUse, HookPreCompact, HookPostCompact, HookSessionStart, HookStop} {
		if hm.findHookScript(ht) != "" {
			r = append(r, ht)
		}
	}
	return r
}

// isHookSafe checks if a hook script passes sandbox validation.
func (hm *HookManager) isHookSafe(scriptPath string) bool {
	if !hm.sandboxMode {
		return true
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return false
	}
	content := string(data)
	dangerous := []string{
		"rm -rf /", "mkfs.", "dd if=", "> /dev/sd",
		":(){ :|:& };:", "chmod 777 /", "chown root",
		"curl http://", "wget http://", "nc -l",
		"sudo ", "su -", "passwd", "iptables", "ufw ",
	}
	for _, p := range dangerous {
		if strings.Contains(content, p) {
			log.Printf("[HOOK] BLOCKED dangerous pattern: %s in %s", p, scriptPath)
			return false
		}
	}
	return true
}

func (hm *HookManager) IsHookSafe(scriptPath string) bool {
	return hm.isHookSafe(scriptPath)
}

// findHookScript returns the path to a hook script if it exists.
func (hm *HookManager) findHookScript(hookType HookType) string {
	// Check for .sh and .bat scripts
	for _, ext := range []string{".sh", ".bat", ".ps1"} {
		path := filepath.Join(hm.hooksDir, string(hookType)+ext)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (hm *HookManager) FindHookScript(hookType HookType) string {
	return hm.findHookScript(hookType)
}

func (hm *HookManager) HooksDir() string {
	if hm == nil {
		return ""
	}
	return hm.hooksDir
}

func (hm *HookManager) SetHooksDir(dir string) {
	if hm == nil || strings.TrimSpace(dir) == "" {
		return
	}
	hm.hooksDir = dir
	os.MkdirAll(dir, 0755)
}

// executeHook runs a hook script and returns the result.
func (hm *HookManager) executeHook(scriptPath string, ctx HookContext) (*HookExecutionResult, error) {
	if !hm.enabled {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctxJSON, err := json.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hook context: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	ext := filepath.Ext(scriptPath)
	switch ext {
	case ".ps1":
		cmd = exec.CommandContext(timeoutCtx, "powershell", "-File", scriptPath)
	case ".bat":
		cmd = exec.CommandContext(timeoutCtx, "cmd", "/c", scriptPath)
	default:
		// .sh — use bash on unix, git bash on windows
		bashPath := "bash"
		if _, err := exec.LookPath("bash"); err != nil {
			bashPath = "sh"
		}
		cmd = exec.CommandContext(timeoutCtx, bashPath, scriptPath)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	go func() {
		defer stdin.Close()
		stdin.Write(ctxJSON)
	}()

	output, err := cmd.CombinedOutput()
	exitCode := cmd.ProcessState.ExitCode()

	result := &HookExecutionResult{Allowed: exitCode == 0}

	// If hook returned non-zero, it's a block
	if exitCode != 0 && err != nil {
		result.Message = fmt.Sprintf("hook exited with code %d: %s", exitCode, string(output))
		return result, nil
	}

	// Try to parse output as modified result
	if len(output) > 0 {
		var parsed HookExecutionResult
		if jsonErr := json.Unmarshal(output, &parsed); jsonErr == nil {
			result = &parsed
		} else {
			// Output is plain text message
			result.Message = string(output)
		}
	}

	return result, nil
}

// RunPreToolUse runs the pre_tool_use hook before a tool executes.
func (hm *HookManager) RunPreToolUse(agentName, taskID, toolName string, params map[string]interface{}) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookPreToolUse)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctx := HookContext{
		HookType:  HookPreToolUse,
		AgentName: agentName,
		TaskID:    taskID,
		ToolName:  toolName,
		Params:    params,
		Timestamp: time.Now().Unix(),
	}

	start := time.Now()
	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] pre_tool_use '%s': allowed=%v (%v)", toolName, result.Allowed, time.Since(start))
	return result, err
}

// RunPostToolUse runs the post_tool_use hook after a tool executes.
func (hm *HookManager) RunPostToolUse(agentName, taskID, toolName string, params map[string]interface{}, toolResult string, toolErr error) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookPostToolUse)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	errStr := ""
	if toolErr != nil {
		errStr = toolErr.Error()
	}

	ctx := HookContext{
		HookType:  HookPostToolUse,
		AgentName: agentName,
		TaskID:    taskID,
		ToolName:  toolName,
		Params:    params,
		Result:    toolResult,
		Error:     errStr,
		Timestamp: time.Now().Unix(),
	}

	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] post_tool_use '%s': allowed=%v", toolName, result.Allowed)
	return result, err
}

// RunPreCompact runs the pre_compact hook before compression.
func (hm *HookManager) RunPreCompact(agentName string) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookPreCompact)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctx := HookContext{
		HookType:  HookPreCompact,
		AgentName: agentName,
		Timestamp: time.Now().Unix(),
	}

	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] pre_compact: allowed=%v", result.Allowed)
	return result, err
}

// RunPostCompact runs the post_compact hook after compression.
func (hm *HookManager) RunPostCompact(agentName string) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookPostCompact)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctx := HookContext{
		HookType:  HookPostCompact,
		AgentName: agentName,
		Timestamp: time.Now().Unix(),
	}

	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] post_compact: allowed=%v", result.Allowed)
	return result, err
}

// RunSessionStart runs the session_start hook when a session begins.
func (hm *HookManager) RunSessionStart(agentName, taskID string) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookSessionStart)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctx := HookContext{
		HookType:  HookSessionStart,
		AgentName: agentName,
		TaskID:    taskID,
		Timestamp: time.Now().Unix(),
	}

	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] session_start '%s': allowed=%v", agentName, result.Allowed)
	return result, err
}

// RunStop runs the stop hook when an agent session ends.
func (hm *HookManager) RunStop(agentName, taskID string) (*HookExecutionResult, error) {
	script := hm.findHookScript(HookStop)
	if script == "" {
		return &HookExecutionResult{Allowed: true}, nil
	}

	ctx := HookContext{
		HookType:  HookStop,
		AgentName: agentName,
		TaskID:    taskID,
		Timestamp: time.Now().Unix(),
	}

	result, err := hm.executeHook(script, ctx)
	log.Printf("[HOOK] stop '%s': allowed=%v", agentName, result.Allowed)
	return result, err
}
