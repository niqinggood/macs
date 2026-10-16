package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ====================== Shell Hook Executor ======================
// Executes hook scripts (.sh/.bat/.ps1) with JSON stdin → JSON stdout protocol.
// Compatible with existing .openkitty/hooks/*.sh scripts.

type HookShellExecutor struct {
	sandboxMode         bool
	maxTimeout          time.Duration
	isSystemDestructive ShellSafetyCheck
}

type ShellSafetyCheck func(command string) bool

func NewHookShellExecutor(sandbox bool, safetyCheck ShellSafetyCheck) *HookShellExecutor {
	return &HookShellExecutor{
		sandboxMode:         sandbox,
		maxTimeout:          30 * time.Second,
		isSystemDestructive: safetyCheck,
	}
}

func (e *HookShellExecutor) Execute(cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	return e.ExecuteContext(context.Background(), cfg, hookCtx)
}

func (e *HookShellExecutor) ExecuteContext(parent context.Context, cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	scriptPath := cfg.Command
	if scriptPath == "" {
		return nil, fmt.Errorf("shell hook requires 'command' field")
	}

	absPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("hook script path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		altPath := filepath.Join(".openkitty", "hooks", filepath.Base(scriptPath))
		if _, err2 := os.Stat(altPath); err2 == nil {
			absPath, err = filepath.Abs(altPath)
			if err != nil {
				return nil, fmt.Errorf("hook fallback script path: %w", err)
			}
		} else {
			return nil, fmt.Errorf("hook script not found: %s", scriptPath)
		}
	}
	if e.sandboxMode && !IsScriptSafe(absPath, e.isSystemDestructive) {
		return nil, fmt.Errorf("hook script contains a system-destructive command: %s", absPath)
	}

	timeout := 30 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	if timeout > e.maxTimeout {
		timeout = e.maxTimeout
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	cmd := buildHookCommand(ctx, absPath)
	stdin, _ := cmd.StdinPipe()

	go func() {
		defer stdin.Close()
		stdin.Write(hookCtx.ToJSON())
	}()

	output, execErr := cmd.CombinedOutput()
	duration := time.Since(start)
	if parent.Err() != nil {
		return nil, fmt.Errorf("shell hook canceled: %w", parent.Err())
	}

	result, parseErr := ValidateHookOutput(output)
	if parseErr != nil {
		return &HookResultV2{
			Decision:     HookDecisionAllow,
			Message:      fmt.Sprintf("hook output parse warning: %v (raw: %.200s)", parseErr, string(output)),
			ExecutorType: HookExecShell,
			Duration:     duration,
			RawOutput:    string(output),
		}, nil
	}

	result.ExecutorType = HookExecShell
	result.Duration = duration
	result.RawOutput = string(output)

	if execErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Decision = HookDecisionBlock
			result.Message = fmt.Sprintf("hook timed out after %v", timeout)
		} else if result.Decision == HookDecisionAllow && result.Message == "" {
			result.Decision = HookDecisionBlock
			result.Message = fmt.Sprintf("hook exited with error: %v", execErr)
		}
	}

	return result, nil
}

func buildHookCommand(ctx context.Context, scriptPath string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	switch ext {
	case ".ps1":
		return exec.CommandContext(ctx, "powershell", "-File", scriptPath)
	case ".bat", ".cmd":
		return exec.CommandContext(ctx, "cmd", "/c", scriptPath)
	default:
		bashPath := "bash"
		if _, err := exec.LookPath("bash"); err != nil {
			bashPath = "sh"
		}
		return exec.CommandContext(ctx, bashPath, scriptPath)
	}
}

func IsScriptSafe(scriptPath string, safetyCheck ShellSafetyCheck) bool {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return false
	}
	return safetyCheck == nil || !safetyCheck(string(data))
}
