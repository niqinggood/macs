package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

// ExternalAgentTool lets openkitty spawn and converse with external AI agents.
// Supports Claude Code, OpenCode, OpenClaw, and custom agents via CLI.
type ExternalAgentTool struct {
	conversations  map[string]*externalConversation
	mu             sync.Mutex
	workDir        string
	projectContext func(string) string
}

type externalConversation struct {
	ID        string
	AgentType string
	History   []externalMessage
	CreatedAt time.Time
	LastUsed  time.Time
}

type externalMessage struct {
	Role    string // "user" or "assistant"
	Content string
	Time    time.Time
}

type externalAgentInvocation struct {
	Executable   string
	Args         []string
	Stdin        string
	Display      string
	UseShell     bool
	ShellCommand string
}

func NewExternalAgentTool(workDir string, loaders ...func(string) string) *ExternalAgentTool {
	tool := &ExternalAgentTool{
		conversations: make(map[string]*externalConversation),
		workDir:       workDir,
	}
	if len(loaders) > 0 {
		tool.projectContext = loaders[0]
	}
	return tool
}

func (t *ExternalAgentTool) Name() string { return "external_agent" }

func (t *ExternalAgentTool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	return toolcontract.SandboxCapabilities{Process: true, Network: true, SubAgent: true}
}

func (t *ExternalAgentTool) Description() string {
	return "Delegate tasks to an external AI coding agent (Claude Code / Codex CLI). " +
		"Auto-detects available agents ('auto' tries Claude Code first). " +
		"Reads project CLAUDE.md/AGENTS.md for context. " +
		"Use for: writing features, debugging, refactoring, code review, or multi-step coding work. " +
		"Use conversation_id for multi-turn conversations."
}

func (t *ExternalAgentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent": map[string]interface{}{
				"type":        "string",
				"description": "External agent: 'auto'(tries claude→codex), 'claude', 'codex', 'opencode', 'openclaw', or 'custom'",
				"enum":        []string{"auto", "claude", "codex", "opencode", "openclaw", "custom"},
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "The task or question to send to the external agent",
			},
			"work_dir": map[string]interface{}{
				"type":        "string",
				"description": "Working directory for the external agent (default: current workspace)",
			},
			"conversation_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional. Resume a previous conversation by its ID.",
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "For 'custom' agent: the full CLI command to run (use {prompt} placeholder for the prompt)",
			},
			"permission_mode": map[string]interface{}{
				"type":        "string",
				"description": "Permission: bypass (skip all, default) / acceptEdits / default (interactive)",
				"enum":        []string{"bypass", "acceptEdits", "default"},
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds (default 300, max 600)",
				"minimum":     10,
				"maximum":     600,
			},
		},
		"required": []string{"agent", "prompt"},
	}
}

func (t *ExternalAgentTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	agentType, _ := params["agent"].(string)
	prompt, _ := params["prompt"].(string)
	workDir, _ := params["work_dir"].(string)
	convID, _ := params["conversation_id"].(string)
	customCmd, _ := params["command"].(string)
	timeoutSec := 300
	if ts, ok := params["timeout"].(float64); ok && ts > 0 {
		timeoutSec = int(ts)
	}

	if agentType == "" || prompt == "" {
		return "Error: agent and prompt are required.", nil
	}
	if agentType == "auto" {
		agentType = t.detectAgent()
	}

	if workDir == "" {
		workDir = t.workDir
	}

	// Build project context from CLAUDE.md / AGENTS.md
	projectCtx := t.ProjectContext(workDir)
	if projectCtx != "" {
		prompt = projectCtx + "\n\n## Current Task\n" + prompt
	}

	// Get or create conversation
	var conv *externalConversation
	if convID != "" {
		t.mu.Lock()
		conv = t.conversations[convID]
		t.mu.Unlock()
		if conv == nil {
			return fmt.Sprintf("Conversation '%s' not found. Start a new one without conversation_id.", convID), nil
		}
	} else {
		conv = &externalConversation{
			ID:        fmt.Sprintf("ext_%d", time.Now().UnixNano()),
			AgentType: agentType,
			History:   []externalMessage{},
			CreatedAt: time.Now(),
			LastUsed:  time.Now(),
		}
	}

	// Build context from history
	var fullPrompt strings.Builder
	for _, msg := range conv.History {
		if msg.Role == "user" {
			fullPrompt.WriteString(fmt.Sprintf("Previous request: %s\n", msg.Content))
		} else {
			fullPrompt.WriteString(fmt.Sprintf("Previous response: %s\n", textutil.Truncate(msg.Content, 500)))
		}
	}
	fullPrompt.WriteString(fmt.Sprintf("\nCurrent request: %s", prompt))

	// Build command
	cmdStr, err := t.buildCommand(agentType, fullPrompt.String(), workDir, customCmd, conv)
	if err != nil {
		return "", err
	}

	// Execute
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	if _, err := exec.LookPath("sh"); err != nil {
		// Windows: use cmd
		cmd = exec.CommandContext(execCtx, "cmd", "/c", cmdStr)
	}
	cmd.Dir = workDir
	// 跳过所有人工确认提示，让外部 agent 自主执行
	cmd.Env = append(os.Environ(),
		"CLAUDE_CODE_PERMISSION_MODE=bypassPermissions",
		"CLAUDE_PERMISSION_MODE=acceptEdits",
		"DISABLE_PERMISSIONS=1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err = cmd.Run()
	elapsed := time.Since(startTime)

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}

	// Save to conversation history
	conv.History = append(conv.History,
		externalMessage{Role: "user", Content: prompt, Time: time.Now()},
		externalMessage{Role: "assistant", Content: output, Time: time.Now()},
	)
	conv.LastUsed = time.Now()

	// Store conversation
	t.mu.Lock()
	t.conversations[conv.ID] = conv
	t.mu.Unlock()

	// Clean old conversations (keep last 50)
	t.cleanupOldConversations()

	status := "success"
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			status = fmt.Sprintf("timeout after %ds", timeoutSec)
		} else {
			status = fmt.Sprintf("error: %v", err)
		}
	}

	changedFiles := CollectExternalAgentChangedFiles(workDir)
	reportPath, reportErr := t.WriteExecutionReport(workDir, agentType, conv.ID, status, elapsed, prompt, output, changedFiles)
	reportLine := ""
	if reportErr == nil && reportPath != "" {
		reportLine = fmt.Sprintf("[Report: %s]\n", reportPath)
	} else if reportErr != nil {
		reportLine = fmt.Sprintf("[Report error: %v]\n", reportErr)
	}
	changedLine := ""
	if len(changedFiles) > 0 {
		changedLine = "[Changed files]\n" + strings.Join(PrefixLines(changedFiles, "- "), "\n") + "\n"
	}

	return fmt.Sprintf("[External Agent: %s] [Conversation: %s] [Status: %s] [Duration: %.1fs]\n%s%s\n%s",
		agentType, conv.ID, status, elapsed.Seconds(), reportLine, changedLine, output), nil
}

func (t *ExternalAgentTool) buildCommand(agentType, prompt, workDir, customCmd string, conv *externalConversation) (string, error) {
	// Escape prompt for shell
	escaped := strings.ReplaceAll(prompt, "'", "'\\''")

	switch agentType {
	case "claude":
		return fmt.Sprintf("claude -p '%s' --output-format text --dangerously-skip-permissions --no-show-cost 2>&1", escaped), nil

	case "codex":
		escapedWorkDir := strings.ReplaceAll(workDir, "'", "'\\''")
		return fmt.Sprintf("codex exec --color never --skip-git-repo-check -C '%s' --dangerously-bypass-approvals-and-sandbox '%s' 2>&1", escapedWorkDir, escaped), nil

	case "opencode":
		// OpenCode: assume CLI with -p flag
		return fmt.Sprintf("opencode -p '%s' 2>&1", escaped), nil

	case "openclaw":
		// OpenClaw: Node.js based, typically run via npx or node
		return fmt.Sprintf("node openclaw/cli.js chat '%s' 2>&1", escaped), nil

	case "custom":
		if customCmd == "" {
			return "", fmt.Errorf("custom agent requires 'command' parameter")
		}
		return strings.ReplaceAll(customCmd, "{prompt}", escaped), nil

	default:
		return "", fmt.Errorf("unknown agent type: %s", agentType)
	}
}

func (t *ExternalAgentTool) cleanupOldConversations() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.conversations) > 50 {
		// Remove oldest
		var oldest *externalConversation
		for _, c := range t.conversations {
			if oldest == nil || c.LastUsed.Before(oldest.LastUsed) {
				oldest = c
			}
		}
		if oldest != nil {
			delete(t.conversations, oldest.ID)
		}
	}
}

func (c *externalConversation) lastAssistantContent() string {
	for i := len(c.History) - 1; i >= 0; i-- {
		if c.History[i].Role == "assistant" {
			return c.History[i].Content
		}
	}
	return ""
}

func (t *ExternalAgentTool) detectAgent() string {
	if _, err := exec.LookPath("claude"); err == nil {
		return "claude"
	}
	if _, err := exec.LookPath("codex"); err == nil {
		return "codex"
	}
	return "claude" // default, will fail with clear error
}

func (t *ExternalAgentTool) ProjectContext(workDir string) string {
	if t == nil || t.projectContext == nil {
		return ""
	}
	return t.projectContext(workDir)
}

func (t *ExternalAgentTool) WriteExecutionReport(workDir, agentType, convID, status string, elapsed time.Duration, prompt, output string, changedFiles []string) (string, error) {
	baseDir := filepath.Join(workDir, ".openkitty", "storage", "external_agents")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", err
	}
	fileName := fmt.Sprintf("%s_%s_%s.md", sanitizeExternalAgentName(agentType), sanitizeExternalAgentName(convID), time.Now().Format("20060102_150405"))
	path := filepath.Join(baseDir, fileName)

	var b strings.Builder
	b.WriteString("# External Coding Agent Report\n\n")
	b.WriteString(fmt.Sprintf("- Agent: %s\n", agentType))
	b.WriteString(fmt.Sprintf("- Conversation: %s\n", convID))
	b.WriteString(fmt.Sprintf("- Status: %s\n", status))
	b.WriteString(fmt.Sprintf("- Duration: %.1fs\n", elapsed.Seconds()))
	b.WriteString(fmt.Sprintf("- Workdir: %s\n", workDir))
	if len(changedFiles) > 0 {
		b.WriteString("\n## Changed Files\n")
		for _, file := range changedFiles {
			b.WriteString("- ")
			b.WriteString(file)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Prompt\n\n```text\n")
	b.WriteString(prompt)
	b.WriteString("\n```\n\n## Agent Output\n\n```text\n")
	b.WriteString(output)
	b.WriteString("\n```\n")

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", err
	}
	return filepath.ToSlash(path), nil
}

func CollectExternalAgentChangedFiles(workDir string) []string {
	return CollectExternalAgentChangedFilesLimit(workDir, 30)
}

func CollectExternalAgentChangedFilesLimit(workDir string, limit int) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files
}

func DiffExternalAgentChangedFiles(before, after []string) []string {
	seenBefore := make(map[string]bool, len(before))
	for _, line := range before {
		line = strings.TrimSpace(line)
		if line != "" {
			seenBefore[line] = true
		}
	}
	var diff []string
	seenAfter := map[string]bool{}
	for _, line := range after {
		line = strings.TrimSpace(line)
		if line == "" || seenBefore[line] || seenAfter[line] {
			continue
		}
		seenAfter[line] = true
		diff = append(diff, line)
		if len(diff) >= 30 {
			break
		}
	}
	return diff
}

func sanitizeExternalAgentName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "agent"
	}
	return regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(s, "_")
}

func PrefixLines(lines []string, prefix string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, prefix+line)
	}
	return out
}
