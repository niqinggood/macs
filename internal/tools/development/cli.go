package development

import (
	// 新增：用于打包 ZIP（DOCX 本质是 ZIP）
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"mac/internal/portablepath"
	"mac/internal/processutil"
	securitycore "mac/internal/security"
	"mac/internal/shellpolicy"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
	// 用于 Big5
	// 用于 Big5
)

type ShellCommandClass = shellpolicy.CommandClass
type ShellCommandAnalysis = shellpolicy.CommandAnalysis

const (
	ShellClassReadOnly    = shellpolicy.ClassReadOnly
	ShellClassBuildTest   = shellpolicy.ClassBuildTest
	ShellClassWrite       = shellpolicy.ClassWrite
	ShellClassLongRunning = shellpolicy.ClassLongRunning
	ShellClassDestructive = shellpolicy.ClassDestructive
	ShellClassUnknown     = shellpolicy.ClassUnknown
)

var ClassifyShellCommand = shellpolicy.Classify
var AnalyzeShellCommand = shellpolicy.Analyze

type CLITool struct {
	workspace        string            // 工作空间路径
	timeout          time.Duration     // 命令超时时间
	dangerous        map[string]bool   // 危险命令列表
	availableDevCmds map[string]string // 可用开发命令及版本
	enforceSafety    bool              // 默认启用系统破坏防护，不做审批确认
	strictSafety     bool              // 严格模式启用旧版更宽的命令拦截
	// 新增：命令执行控制字段
	currentCmd *exec.Cmd          // 当前执行的命令
	cancelFunc context.CancelFunc // 取消函数（手动停止）
	mu         sync.Mutex         // 并发安全锁
	isRunning  bool               // 命令是否正在运行
	runtime    CLIRuntime
}

// CLIRuntime supplies the process-wide resources owned by the application
// host without coupling the CLI tool package back to internal/app.
type CLIRuntime struct {
	TerminalManager *processutil.TerminalManager
	ShellJobs       *processutil.ShellJobManager
	ResolveOwner    processutil.ShellOwnerResolver
	Logger          interface{ Printf(string, ...interface{}) }
}

func (t *CLITool) logf(format string, args ...interface{}) {
	if t != nil && t.runtime.Logger != nil {
		t.runtime.Logger.Printf(format, args...)
	}
}

type windowsShellKind string

const (
	windowsShellPowerShell windowsShellKind = "powershell"
	windowsShellCmd        windowsShellKind = "cmd"
	windowsShellGitBash    windowsShellKind = "git-bash"
)

var (
	explicitPowerShellPattern = regexp.MustCompile(`(?i)^\s*(?:&\s*)?(?:powershell|pwsh)(?:\.exe)?(?:\s|$)`)
	explicitCmdPattern        = regexp.MustCompile(`(?i)^\s*@?cmd(?:\.exe)?(?:\s|$)`)
	powerShellCmdletPattern   = regexp.MustCompile(`(?i)(?:^|[|;]\s*)(?:get|set|new|remove|add|clear|copy|move|rename|test|invoke|start|stop|restart|write|read|select|where|foreach|measure|format|out|export|import|convertto|convertfrom|resolve|join|split|push|pop|wait|enable|disable|register|unregister|update)-[a-z][a-z0-9]*`)
	cmdBuiltinPattern         = regexp.MustCompile(`(?i)^\s*@?(?:assoc|break|call|cd|chdir|cls|color|copy|date|del|dir|echo|endlocal|erase|exit|for|ftype|goto|if|md|mkdir|mklink|move|path|pause|popd|prompt|pushd|rd|rem|ren|rename|rmdir|set|setlocal|shift|start|time|title|type|ver|verify|vol)(?:\s|$)`)
	cmdVariablePattern        = regexp.MustCompile(`(?i)(?:%[a-z_][a-z0-9_]*%|%~[fdpnxsatz]*[0-9]|![a-z_][a-z0-9_]*!)`)
	unixCommandPattern        = regexp.MustCompile(`(?i)(?:^|[|;&]\s*)(?:bash|sh|cat|head|tail|grep|sed|awk|ls|pwd|cp|mv|rm|chmod|chown|touch|wc|uniq|cut|xargs|tee|tr|du|df|which)(?:\s|$)`)
	unixPathPattern           = regexp.MustCompile(`(?:^|\s)(?:/(?:[a-z]/|dev/|etc/|home/|tmp/|usr/|var/)|\./|\.\./)`)
)

// selectWindowsShell classifies the command only after the native Go fast path
// has declined it. Keep this function side-effect free so routing can be tested
// without depending on the host OS or an installed Git Bash.
func selectWindowsShell(cmdStr string) windowsShellKind {
	trimmed := strings.TrimSpace(cmdStr)
	if trimmed == "" {
		return windowsShellPowerShell
	}

	// An explicit shell always wins. In particular, never let Bash pre-expand
	// PowerShell variables such as $_ or rewrite paths such as E:\work\x.ps1.
	if explicitPowerShellPattern.MatchString(trimmed) {
		return windowsShellPowerShell
	}
	if explicitCmdPattern.MatchString(trimmed) {
		return windowsShellCmd
	}

	lower := strings.ToLower(trimmed)
	if hasPowerShellSyntax(trimmed, lower) {
		return windowsShellPowerShell
	}
	if hasUnixShellSyntax(trimmed, lower) {
		return windowsShellGitBash
	}
	if hasCmdSyntax(trimmed, lower) {
		return windowsShellCmd
	}

	// External cross-platform programs (git, go, python, curl, etc.) do not
	// require Bash. PowerShell preserves native Windows paths for them.
	return windowsShellPowerShell
}

func hasPowerShellSyntax(cmdStr, lower string) bool {
	if powerShellCmdletPattern.MatchString(cmdStr) {
		return true
	}
	powerShellMarkers := []string{
		"$_", "$env:", "$psscriptroot", "$error", "@{", "@(", "::",
		"-erroraction", "-literalpath", "-recurse", "-force",
		"foreach-object", "where-object", "select-object", "write-host",
		"write-output", "out-file", "set-content", "add-content",
	}
	for _, marker := range powerShellMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.HasPrefix(lower, `.\`) && strings.Contains(lower, ".ps1") {
		return true
	}
	return strings.HasPrefix(lower, "& ") && strings.Contains(lower, ".ps1")
}

func hasCmdSyntax(cmdStr, lower string) bool {
	if cmdBuiltinPattern.MatchString(cmdStr) || cmdVariablePattern.MatchString(cmdStr) {
		return true
	}
	if strings.HasPrefix(lower, "@echo ") || strings.Contains(lower, "%comspec%") {
		return true
	}
	first := strings.ToLower(strings.Trim(strings.Fields(cmdStr)[0], `"`))
	return strings.HasSuffix(first, ".bat") || strings.HasSuffix(first, ".cmd")
}

func hasUnixShellSyntax(cmdStr, lower string) bool {
	if unixCommandPattern.MatchString(cmdStr) || unixPathPattern.MatchString(cmdStr) {
		return true
	}
	if strings.HasPrefix(lower, "find ./") || strings.HasPrefix(lower, "find . ") ||
		strings.Contains(lower, " -name ") || strings.Contains(lower, " -type ") {
		return true
	}
	unixMarkers := []string{"$(", "`", "/dev/null", "export ", "#!/bin/", "#!/usr/bin/"}
	for _, marker := range unixMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// hasUnquotedShellOperator prevents a compound command from being mistaken for
// a single native Go command (for example, "cat file | head -n 80").
func hasUnquotedShellOperator(cmdStr string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range cmdStr {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '|', '&', ';', '<', '>', '\n', '\r':
			if !inSingle && !inDouble {
				return true
			}
		}
	}
	return false
}

func NewCLITool(workspace string, runtime CLIRuntime) *CLITool {
	return NewCLIToolWithSafety(workspace, true, runtime)
}

func NewCLIToolWithSafety(workspace string, enforceSafety bool, runtime CLIRuntime) *CLITool {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		absWorkspace = workspace
	}

	cli := &CLITool{
		workspace:        absWorkspace,
		timeout:          120 * time.Second,
		dangerous:        map[string]bool{},
		availableDevCmds: make(map[string]string),
		enforceSafety:    enforceSafety,
		runtime:          runtime,
	}

	// 版本检测已禁用 —— Windows 下每个 where 超时 3s x 24 命令 ≈ 30s+
	// 如需启用: cli.availableDevCmds = cli.detectAvailableDevCommandsWithVersion()
	return cli
}

func NewCLIToolWithStrictSafety(workspace string, runtime CLIRuntime) *CLITool {
	cli := NewCLIToolWithSafety(workspace, true, runtime)
	cli.strictSafety = true
	return cli
}

func getCommandVersion(cmd string, arg string) (string, error) {
	var output bytes.Buffer
	command := exec.Command(cmd, arg)
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func (t *CLITool) getAvailableBasicCommands() string {
	// 返回固定列表，不再动态检测（Windows 下每个 where 命令超时 3s，23 个命令 ≈ 30s+）
	if runtime.GOOS == "windows" {
		return "dir, echo, type, findstr, more, tasklist, ipconfig, netstat, ping, curl"
	}
	return "curl, ls, find, ping, grep, cat, echo, ps, df, du, wc, sort, uniq, head, tail"
}

func (t *CLITool) Name() string { return "cli" }

func (t *CLITool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	// CLI always starts a process, but it is not inherently a network call.
	// The sandbox inspects the command and blocks URLs/network executables
	// when network access is disabled.
	return toolcontract.SandboxCapabilities{Process: true}
}

func (t *CLITool) Description() string {
	basicCommands := t.getAvailableBasicCommands() // "curl, ls, dir, find, ping, grep, cat, echo, ps, df, du, top, wc, sort, uniq, head, tail"

	gitBashNote := ""
	if runtime.GOOS == "windows" {
		terminalMgr := t.runtime.TerminalManager
		if terminalMgr.IsGitBashAvailable() {
			gitBashNote = "\n✅ Git Bash detected. Unix-style commands and pipelines will run in Git Bash."
		} else {
			gitBashNote = "\n⚠️ Git Bash not found. Install Git for Windows for a better command-line experience."
		}
	}

	devCommandsStr := "None"
	if len(t.availableDevCmds) > 0 {
		var devItems []string
		for cmd, version := range t.availableDevCmds {
			devItems = append(devItems, fmt.Sprintf("%s (%s)", cmd, version))
		}
		devCommandsStr = strings.Join(devItems, ", ")
		subCmds := []string{}
		if _, ok := t.availableDevCmds["go"]; ok {
			subCmds = append(subCmds, "go available") //build, go run, go test
		}
		if _, ok := t.availableDevCmds["dotnet"]; ok {
			subCmds = append(subCmds, "dotnet build")
		}

		if _, ok := t.availableDevCmds["git"]; ok {
			subCmds = append(subCmds, "git available ") //clone, git pull, git push, git commit, git status
		}

		if len(subCmds) > 0 {
			devCommandsStr += " (supports subcommands: " + strings.Join(subCmds, ", ") + ")"
		}
	}

	basicDesc := fmt.Sprintf(`Execute commands in workspace (timeout 30-300s, supports manual stop). Available commands include: %s, %s.%s. OpenKitty does not require confirmation before command execution.`, basicCommands, devCommandsStr, gitBashNote)
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`%s
💡 Windows Tips:
- When using curl, wrap JSON with double quotes and escape inner double quotes
- Correct example: curl -d "{\"username\":\"test\",\"password\":\"123\"}" URL
- PowerShell/cmd syntax uses the matching native shell; Unix syntax uses Git Bash when available
- Simple external programs run directly without a shell. For a workspace executable, use .\program.exe ARG and set work_dir instead of prefixing the command with cd.`, basicDesc)
	}
	return basicDesc + `
- For a server, watcher, or other intentionally persistent process, set background=true. This is authoritative even for a workspace .exe that cannot be recognized by the command classifier.
- Never emulate background execution with Python Popen/detach, shell ampersands, or PowerShell Start-Process. Use background=true, then shell_sessions for status and logs.`

}

func (t *CLITool) Parameters() map[string]interface{} {
	exampleCmd := "'ls -la' or 'curl https://api.example.com'"
	if _, ok := t.availableDevCmds["go"]; ok {
		exampleCmd += " or 'go run main.go'"
	}
	if _, ok := t.availableDevCmds["python3"]; ok {
		exampleCmd += " or 'python3 script.py'"
	} else if _, ok := t.availableDevCmds["python"]; ok {
		exampleCmd += " or 'python script.py'"
	}
	if _, ok := t.availableDevCmds["git"]; ok {
		exampleCmd += " or 'git clone https://github.com/example/repo.git' or 'git status'"
	}

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": fmt.Sprintf("Command to execute (e.g., %s)", exampleCmd),
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "5-10 word description of what this command does and why (for safety audit)",
			},
			"work_dir": map[string]interface{}{
				"type":        "string",
				"description": "Working directory relative to the workspace. The resolved absolute work_dir is returned in every observation.",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Foreground command deadline in seconds, default 120. On expiry the complete process tree is terminated. Use background=true for intentionally long-running work.",
				"minimum":     1,
				"maximum":     300,
			},
			"input": map[string]interface{}{
				"type":        "string",
				"description": "Standard input content",
			},
			"background": map[string]interface{}{
				"type":        "boolean",
				"description": "Start any intentionally persistent command in OpenKitty's managed background job system and return job ID/PID/log path. Use this for dev servers and workspace executables; do not use Python Popen/detach or Start-Process.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *CLITool) fixWindowsQuotes(cmd string) string {
	if runtime.GOOS != "windows" {
		return cmd
	}

	// 快速检查：如果命令中没有单引号包裹的 -d 参数，直接返回
	if !strings.Contains(cmd, "-d '") && !strings.Contains(cmd, "--data '") {
		return cmd
	}

	// 使用状态机解析，更可靠
	return t.robustQuoteFix(cmd)
}

// robustQuoteFix 使用状态机修复引号问题
func (t *CLITool) robustQuoteFix(cmd string) string {
	var result strings.Builder

	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		// 处理转义
		if ch == '\\' && !escaped {
			escaped = true
			result.WriteByte(ch)
			continue
		}

		// 状态转换
		if ch == '"' && !escaped && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			result.WriteByte(ch)
		} else if ch == '\'' && !escaped && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			// 如果是单引号结束，可能需要转换为双引号
			if !inSingleQuote {
				// 这里做转换：单引号内容中的双引号需要转义
				// 但这需要知道前面的参数名，比较复杂
			}
			result.WriteByte(ch)
		} else {
			if escaped {
				escaped = false
			}
			result.WriteByte(ch)
		}
	}

	// 更简单的方法：直接替换常见模式
	// 但保持原有命令不变
	return cmd
}

func parseCurlCommand(cmdStr string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	escapeNext := false

	for i := 0; i < len(cmdStr); i++ {
		ch := cmdStr[i]

		if escapeNext {
			current.WriteByte(ch)
			escapeNext = false
			continue
		}

		if ch == '\\' {
			// 检查是否是转义的双引号
			if i+1 < len(cmdStr) && cmdStr[i+1] == '"' {
				escapeNext = true
				current.WriteByte('"')
				i++ // 跳过下一个字符
				continue
			}
			current.WriteByte(ch)
			continue
		}

		if ch == '"' {
			inQuote = !inQuote
			continue
		}

		if ch == ' ' && !inQuote {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteByte(ch)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	// 第一个参数应该是 curl，替换为 curl.exe
	if len(args) > 0 && strings.ToLower(args[0]) == "curl" {
		args[0] = "curl.exe"
	}

	return args
}

func (t *CLITool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	cmdStr, ok := params["command"].(string)
	if !ok || cmdStr == "" {
		return "", fmt.Errorf("需要指定 command 参数")
	}
	commandClass := ClassifyShellCommand(cmdStr)
	background := false
	if b, ok := params["background"].(bool); ok {
		background = b
	}
	if commandClass == ShellClassLongRunning && !background {
		return fmt.Sprintf("Command Execution\ncmd: %s\nclass: %s\nstatus: not_started\nLong-running command detected. Set background=true to start it in the background and write output to a log file, or run a bounded check command instead.", cmdStr, commandClass), nil
	}

	// 安全检查
	if err := t.checkSafety(cmdStr); err != nil {
		return "", err
	}

	// 从 context 中获取 taskID
	taskID := ""
	if id, ok := ctx.Value("task_id").(string); ok && id != "" {
		taskID = id
	}
	t.logf("[INFO] taskid %s ", taskID)

	// 获取工作目录
	workDir := t.workspace
	if workDirParam, ok := params["work_dir"].(string); ok && workDirParam != "" {
		safePath, err := portablepath.SafeJoin(t.workspace, workDirParam)
		if err != nil {
			return "", fmt.Errorf("不安全的工作目录: %v", err)
		}
		workDir = safePath
	}
	if background {
		return t.startBackgroundCommand(cmdStr, workDir, ctx)
	}

	// 获取超时时间
	timeout := t.timeout
	if timeoutParam, ok := params["timeout"].(float64); ok && timeoutParam > 0 {
		timeout = time.Duration(timeoutParam) * time.Second
	}

	// 创建带超时的 context
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Try native Go implementation first (fast, no shell needed). Compound
	// commands must reach a real shell instead of being treated as extra args.
	nativeParts := processutil.ParseCommandLine(cmdStr)
	if len(nativeParts) > 0 && !hasUnquotedShellOperator(cmdStr) {
		nativeCmdName := strings.ToLower(strings.TrimSuffix(nativeParts[0], ".exe"))
		useNative := true
		if nativeCmdName == "curl" {
			useNative = processutil.NativeCurlSupportsArgs(nativeParts[1:])
		}
		if nativeCmdName == "wget" && len(nativeParts) > 1 {
			useNative = processutil.NativeCurlSupportsArgs(nativeParts[1:])
		}
		if fn, ok := processutil.LookupNativeCommand(nativeCmdName); ok && useNative {
			t.logf("[INFO] using native Go %s", nativeCmdName)
			startedAt := time.Now()
			result, err := fn(execCtx, nativeParts[1:], workDir)
			duration := time.Since(startedAt)
			if err != nil {
				if execCtx.Err() != nil {
					err = execCtx.Err()
				}
				return t.formatOutputWithError(cmdStr, result, workDir, duration, err), err
			}
			return t.formatOutputSuccess(cmdStr, result, workDir, duration), nil
		}
		if runtime.GOOS == "windows" && nativeCmdName == "curl" && !useNative {
			cmdStr = forceWindowsCurlExecutable(cmdStr)
		}
		if executable, ok := resolveDirectCLIExecutable(nativeParts[0], workDir); ok {
			t.logf("[INFO] executing external program directly: %s", filepath.Base(executable))
			return t.executeDirectExternal(execCtx, cmdStr, executable, nativeParts[1:], workDir, params)
		}
	}

	terminalMgr := t.runtime.TerminalManager
	windowsShell := windowsShellPowerShell
	if runtime.GOOS == "windows" {
		windowsShell = selectWindowsShell(cmdStr)
		if windowsShell == windowsShellGitBash {
			if terminalMgr.IsGitBashAvailable() {
				t.logf("[INFO] Windows shell route: Git Bash")
				return t.executeWithGitBash(execCtx, cmdStr, workDir)
			}
			if translated, ok := t.translateUnixToPowerShell(cmdStr); ok {
				t.logf("[INFO] Git Bash unavailable; Unix command translated to PowerShell: %s -> %s", textutil.Truncate(cmdStr, 80), textutil.Truncate(translated, 80))
				cmdStr = translated
			}
			windowsShell = windowsShellPowerShell
		}
		t.logf("[INFO] Windows shell route: %s", windowsShell)
	}

	if runtime.GOOS != "windows" {
		t.logf("[INFO] 回退到 sh 执行命令")
	}
	return t.executeOnce(execCtx, cmdStr, workDir, params, windowsShell)
}

func resolveDirectCLIExecutable(name, workDir string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".ps1") {
		return "", false
	}

	localCandidate := name
	if !filepath.IsAbs(localCandidate) {
		localCandidate = filepath.Join(workDir, localCandidate)
	}
	if info, err := os.Stat(localCandidate); err == nil && !info.IsDir() {
		if absolute, absErr := filepath.Abs(localCandidate); absErr == nil {
			return absolute, true
		}
		return localCandidate, true
	}
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return "", false
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func (t *CLITool) executeDirectExternal(
	ctx context.Context,
	cmdStr string,
	executable string,
	args []string,
	workDir string,
	params map[string]interface{},
) (string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(runCtx, executable, args...)
	command.Dir = workDir
	command.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"LANG=zh_CN.UTF-8",
		"LC_ALL=zh_CN.UTF-8",
		"NO_COLOR=1",
	)
	if input, ok := params["input"].(string); ok && input != "" {
		command.Stdin = strings.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	t.noteCommandStart(command, cancel)
	defer t.noteCommandDone(command)
	startedAt := time.Now()
	err := processutil.RunTree(command, true)
	duration := time.Since(startedAt)
	if runCtx.Err() != nil {
		err = runCtx.Err()
	}
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	if err != nil {
		return t.formatOutputWithError(cmdStr, output, workDir, duration, err), err
	}
	return t.formatOutputSuccess(cmdStr, output, workDir, duration), nil
}

func forceWindowsCurlExecutable(cmdStr string) string {
	leadingLen := len(cmdStr) - len(strings.TrimLeft(cmdStr, " \t\r\n"))
	leading := cmdStr[:leadingLen]
	trimmed := cmdStr[leadingLen:]
	if len(trimmed) < len("curl") || !strings.EqualFold(trimmed[:len("curl")], "curl") {
		return cmdStr
	}
	if len(trimmed) > len("curl") {
		next := trimmed[len("curl")]
		if next != ' ' && next != '\t' {
			return cmdStr
		}
	}
	return leading + "curl.exe" + trimmed[len("curl"):]
}

// 新增：使用临时 Git Bash 执行命令
func (t *CLITool) executeWithGitBash(ctx context.Context, cmdStr, workDir string) (string, error) {
	gitBashPath := t.runtime.TerminalManager.GetGitBashPath()
	if gitBashPath == "" {
		return "", fmt.Errorf("Git Bash not found")
	}

	// 使用 Git Bash 执行命令
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, gitBashPath, "-c", cmdStr)
	cmd.Dir = workDir

	// 设置环境变量
	cmd.Env = append(os.Environ(),
		"LANG=zh_CN.UTF-8",
		"LC_ALL=zh_CN.UTF-8",
		"PYTHONIOENCODING=utf-8",
		"CHCP=65001",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.noteCommandStart(cmd, cancel)
	defer t.noteCommandDone(cmd)

	startedAt := time.Now()
	err := processutil.RunTree(cmd, true)
	duration := time.Since(startedAt)
	if runCtx.Err() != nil {
		err = runCtx.Err()
	}

	// 合并输出（Git Bash 的 stdout 和 stderr 都输出）
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		return t.formatOutputWithError(cmdStr, output, workDir, duration, err), err
	}
	return t.formatOutputSuccess(cmdStr, output, workDir, duration), nil
}

func (t *CLITool) executeOnce(ctx context.Context, cmdStr, workDir string, params map[string]interface{}, windowsShell windowsShellKind) (string, error) {
	var cmd *exec.Cmd
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	switch runtime.GOOS {
	case "windows":
		cmd = t.windowsShellCommandContext(runCtx, windowsShell, cmdStr)
		cmd.Env = append(os.Environ(),
			"PYTHONIOENCODING=utf-8",
			"LANG=zh_CN.UTF-8",
			"CHCP=65001",
		)
	default:
		cmd = exec.CommandContext(runCtx, "sh", "-c", cmdStr)
		cmd.Env = append(os.Environ(), "LANG=zh_CN.UTF-8")
	}

	cmd.Dir = workDir

	if input, ok := params["input"].(string); ok && input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.noteCommandStart(cmd, cancel)
	defer t.noteCommandDone(cmd)

	startedAt := time.Now()
	err := processutil.RunTree(cmd, true)
	duration := time.Since(startedAt)
	if runCtx.Err() != nil {
		err = runCtx.Err()
	}

	// 合并输出
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		return t.formatOutputWithError(cmdStr, output, workDir, duration, err), err
	}
	return t.formatOutputSuccess(cmdStr, output, workDir, duration), nil
}

func (t *CLITool) windowsShellCommandContext(ctx context.Context, shell windowsShellKind, cmdStr string) *exec.Cmd {
	parts := t.parseWindowsCommand(cmdStr)
	switch shell {
	case windowsShellCmd:
		if len(parts) > 0 && explicitCmdPattern.MatchString(cmdStr) {
			return exec.CommandContext(ctx, parts[0], parts[1:]...)
		}
		return exec.CommandContext(ctx, "cmd.exe", "/D", "/S", "/C", cmdStr)
	default:
		if len(parts) > 0 && explicitPowerShellPattern.MatchString(cmdStr) {
			return exec.CommandContext(ctx, parts[0], parts[1:]...)
		}
		// cmdStr is already one argv element. Do not add backslash escaping:
		// PowerShell does not use backslash to escape quotes, and doing so can
		// corrupt script blocks and $_ expressions.
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	}
}

func (t *CLITool) noteCommandStart(cmd *exec.Cmd, cancel context.CancelFunc) {
	t.mu.Lock()
	t.currentCmd = cmd
	t.cancelFunc = cancel
	t.isRunning = true
	t.mu.Unlock()
}

func (t *CLITool) noteCommandDone(cmd *exec.Cmd) {
	t.mu.Lock()
	if t.currentCmd == cmd {
		t.currentCmd = nil
		t.cancelFunc = nil
		t.isRunning = false
	}
	t.mu.Unlock()
}

func (t *CLITool) startBackgroundCommand(cmdStr, workDir string, ctx context.Context) (string, error) {
	taskID, _ := ctx.Value("task_id").(string)
	if taskID == "" {
		taskID = "manual"
	}
	if t.runtime.ResolveOwner == nil {
		return "", fmt.Errorf("shell owner resolver is unavailable")
	}
	owner, err := t.runtime.ResolveOwner(ctx)
	if err != nil {
		return "", err
	}
	logDir := filepath.Join(t.workspace, ".openkitty", "runtime", "shell")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", fmt.Errorf("create shell runtime dir: %w", err)
	}
	logName := fmt.Sprintf("%s_%d.log", processutil.SanitizeShellLogName(taskID), time.Now().UnixNano())
	logPath := filepath.Join(logDir, logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", fmt.Errorf("create background log: %w", err)
	}

	cmd := backgroundShellCommand(cmdStr)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"LANG=zh_CN.UTF-8",
		"LC_ALL=zh_CN.UTF-8",
		"NO_COLOR=1",
	)
	if err := processutil.StartTree(cmd, false); err != nil {
		logFile.Close()
		return "", fmt.Errorf("start background command: %w", err)
	}
	pid := cmd.Process.Pid
	// Capture the manager used for registration. Tests and lifecycle reloads may
	// replace the process-global manager while this command is still running;
	// completion must be recorded on the same manager that owns the job.
	manager := t.runtime.ShellJobs
	if manager == nil {
		return "", fmt.Errorf("shell job manager is unavailable")
	}
	job := manager.RegisterOwned(taskID, cmdStr, logPath, workDir, cmd.Process, owner)
	go func() {
		err := processutil.WaitTree(cmd)
		if err != nil {
			fmt.Fprintf(logFile, "\n[openkitty] process exited with error: %v\n", err)
		} else {
			fmt.Fprintln(logFile, "\n[openkitty] process exited successfully")
		}
		// Close the Windows file handle before publishing a terminal job state.
		// Consumers may archive or remove a completed job's workspace immediately.
		_ = logFile.Close()
		manager.Finish(job.ID, err)
	}()

	relLog := logPath
	if rel, err := filepath.Rel(t.workspace, logPath); err == nil {
		relLog = filepath.ToSlash(rel)
	}
	return fmt.Sprintf("Command Execution\ncmd: %s\nclass: %s\nwork_dir: %s\nstatus: background_started\njob_id: %s\npid: %d\nlog: %s\nUse shell_sessions action=log job_id=%q to inspect output and shell_sessions action=stop job_id=%q to stop it.", cmdStr, ClassifyShellCommand(cmdStr), workDir, job.ID, pid, relLog, job.ID, job.ID), nil
}

func backgroundShellCommand(cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// exec.Command already passes cmdStr as one argv element. Backslash is not
		// PowerShell's quote escape; rewriting quotes here corrupts JSON, paths,
		// test filters, and other common programming commands.
		return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	}
	return exec.Command("sh", "-c", cmdStr)
}

func (t *CLITool) parseWindowsCommand(cmdStr string) []string {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return nil
	}

	var args []string
	var current strings.Builder
	inQuote := false
	i := 0
	n := len(cmdStr)

	for i < n {
		ch := cmdStr[i]

		// 处理空格分隔符
		if ch == ' ' && !inQuote {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			i++
			continue
		}

		// 处理双引号
		if ch == '"' {
			if inQuote {
				// 结束引号
				inQuote = false
				i++
				// 处理连续双引号转义（Windows 风格："" 表示一个字面双引号）
				if i < n && cmdStr[i] == '"' {
					current.WriteByte('"')
					i++
				}
				continue
			} else {
				// 开始引号
				inQuote = true
				i++
				continue
			}
		}

		// 处理反斜杠转义（仅在引号内有效）
		if ch == '\\' && inQuote && i+1 < n && cmdStr[i+1] == '"' {
			current.WriteByte('"')
			i += 2
			continue
		}

		// 普通字符
		current.WriteByte(ch)
		i++
	}

	// 添加最后一个参数
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	// 后处理：移除参数中多余的引号（某些命令需要保持原样）
	for j := range args {
		args[j] = strings.Trim(args[j], "\"")
	}

	return args
}

// formatOutputSuccess 格式化成功输出
func (t *CLITool) formatOutputSuccess(cmd, output, workDir string, duration time.Duration) string {
	var result strings.Builder
	result.WriteString("Command Execution\n")
	result.WriteString(fmt.Sprintf("cmd: %s\n", cmd))
	result.WriteString(fmt.Sprintf("class: %s\n", ClassifyShellCommand(cmd)))
	result.WriteString(fmt.Sprintf("work_dir: %s\n", workDir))
	result.WriteString(fmt.Sprintf("duration: %s\n", duration.Round(time.Millisecond)))
	result.WriteString("status: succeeded\n")
	result.WriteString("状态: 成功\n")
	if output != "" {
		result.WriteString(fmt.Sprintf("output result: %s\n", output))
	}
	return result.String()
}

func (t *CLITool) formatOutputWithError(cmd, output, workDir string, duration time.Duration, err error) string {
	var result strings.Builder
	result.WriteString("Command Execution\n")
	result.WriteString(fmt.Sprintf("cmd: %s\n", cmd))
	result.WriteString(fmt.Sprintf("class: %s\n", ClassifyShellCommand(cmd)))
	result.WriteString(fmt.Sprintf("work_dir: %s\n", workDir))
	result.WriteString(fmt.Sprintf("duration: %s\n", duration.Round(time.Millisecond)))
	status := "failed"
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timeout"
	} else if errors.Is(err, context.Canceled) {
		status = "cancelled"
	}
	result.WriteString(fmt.Sprintf("status: %s\n", status))
	result.WriteString(fmt.Sprintf("状态: 失败, 失败原因: %v\n", err))

	// 解释常见的 curl 错误码
	if strings.Contains(cmd, "curl") && err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode := status.ExitStatus()
				result.WriteString(fmt.Sprintf("curl 错误码 %d 说明: %s\n", exitCode, getCurlErrorDescription(exitCode)))
			}
		}
	}

	if output != "" {
		// 如果输出包含 HTML，可以截断或提示
		if strings.Contains(output, "<!DOCTYPE html>") || strings.Contains(output, "<html") {
			// 如果是 HTML 输出，可能不是期望的结果
			result.WriteString(fmt.Sprintf("output result (可能是错误页面):\n%s\n", textutil.Truncate(output, 1000)))
		} else {
			result.WriteString(fmt.Sprintf("output result:\n%s\n", output))
		}
	}
	return result.String()
}

// 新增：获取 curl 错误码说明
func getCurlErrorDescription(code int) string {
	descriptions := map[int]string{
		1:  "不支持的协议",
		2:  "初始化失败",
		3:  "URL 格式错误",
		4:  "构建请求时出错",
		5:  "无法解析代理",
		6:  "无法解析主机地址",
		7:  "无法连接到主机",
		8:  "服务器返回异常数据",
		9:  "拒绝访问",
		10: "FTP 接受命令失败",
		11: "FTP 接受回复失败",
		12: "HTTP 返回错误",
		13: "认证失败",
		14: "FTP 登录失败",
		15: "FTP 文件打开失败",
		16: "FTP 用户取消",
		17: "磁盘空间不足",
		18: "部分文件传输完成",
		19: "FTP 下载文件失败",
		20: "FTP 获取文件大小失败",
		21: "FTP 重命名文件失败",
		22: "HTTP 页面未找到 (404)",
		23: "写入文件失败",
		24: "读取文件失败",
		25: "上传文件失败",
		26: "读取回调错误",
		27: "内存不足",
		28: "连接超时",
		29: "FTP 连接超时",
		30: "FTP 端口错误",
		31: "FTP 回复错误",
		32: "FTP 套接字错误",
		33: "FTP 数据连接错误",
		34: "HTTP 范围请求错误",
		35: "SSL/TLS 连接错误",
		36: "FTP 恢复传输错误",
		37: "文件无法打开",
		38: "Kerberos 错误",
		39: "SSL 证书错误",
		40: "SSL 证书算法错误",
		41: "FTP 功能错误",
		42: "内存分配错误",
		43: "内部错误",
		44: "内部调用了不正确的函数",
		45: "接口错误",
		46: "FTP 坏的套接字",
		47: "重定向过多",
		48: "FTP 不知道的主机",
		49: "FTP 服务器错误",
		50: "FTP 文件操作错误",
		51: "FTP SSL 错误",
		52: "没有数据",
		53: "SSL 引擎未找到",
		54: "SSL 引擎设置失败",
		55: "发送网络数据失败",
		56: "接收网络数据失败",
		57: "SSL 证书问题",
		58: "SSL 颁发者错误",
		59: "无法使用 SSL",
		60: "SSL 对等证书错误",
		61: "不支持 HTTP 编码",
		62: "不支持 HTTP 授权",
		63: "最大文件大小超出限制",
		64: "FTP SSL 请求失败",
		65: "发送数据失败",
		66: "FTP 读取数据失败",
		67: "密码错误",
		68: "未找到 TLS 会话",
		69: "连接中止",
		70: "磁盘空间满",
		71: "SSL 客户端证书错误",
		72: "FTP 文件上传成功但无法读取回复",
		73: "服务器返回了无法满足的范围",
		74: "FTP 发送命令时出错",
		75: "FTP 数据方向错误",
		76: "FTP 无法打开数据连接",
		77: "回调错误",
		78: "文件不存在",
		79: "FTP 未知的错误",
		80: "SSL 引擎初始化失败",
		81: "加密引擎未找到",
		82: "加密引擎设置失败",
		83: "FTP 传输速度限制",
		84: "FTP 传输中止",
		85: "FTP 传输参数错误",
		86: "FTP 连接数限制",
		87: "FTP 服务器返回了未知状态",
		88: "FTP 服务不可用",
		89: "FTP 连接被拒绝",
		90: "FTP 文件验证失败",
		91: "FTP TLS 恢复失败",
		92: "FTP 无效命令",
		93: "FTP 访问被拒绝",
	}

	if desc, ok := descriptions[code]; ok {
		return desc
	}
	return "未知错误"
}

func (t *CLITool) checkSafety(cmd string) error {
	if t == nil || !t.enforceSafety {
		return nil
	}
	if err := t.checkSystemDestructiveSafety(cmd); err != nil {
		return err
	}
	if !t.strictSafety {
		return nil
	}
	return t.checkStrictSafety(cmd)
}

func (t *CLITool) checkSystemDestructiveSafety(cmd string) error {
	analysis := AnalyzeShellCommand(cmd)
	if analysis.Blocked {
		return fmt.Errorf("command blocked by shell safety analyzer: %s", analysis.BlockReason)
	}
	cmdLower := strings.ToLower(cmd)

	// Block only system-destructive operations in default mode.
	if t.isRootDeletion(cmdLower) {
		return fmt.Errorf("检测到删除根目录或系统关键目录的操作，已禁止执行")
	}
	if t.isDiskFormatting(cmdLower) {
		return fmt.Errorf("检测到磁盘格式化操作，已禁止执行")
	}
	if t.isDiskWrite(cmdLower) {
		return fmt.Errorf("检测到直接写入磁盘设备的操作，已禁止执行")
	}
	if err := t.checkDangerousRedirection(cmd); err != nil {
		return err
	}
	return nil
}

// IsSystemDestructiveCommand is the shared, prompt-free hard boundary used by
// OpenKitty-owned command runtimes. It intentionally covers only operations
// that can erase or overwrite the operating system/disk. Ordinary destructive
// development commands, installs, networking and system administration remain
// unrestricted.
func IsSystemDestructiveCommand(cmd string) bool {
	return (&CLITool{}).checkSystemDestructiveSafety(cmd) != nil
}

func (t *CLITool) checkStrictSafety(cmd string) error {
	analysis := AnalyzeShellCommand(cmd)
	if analysis.Blocked {
		return fmt.Errorf("command blocked by shell safety analyzer: %s", analysis.BlockReason)
	}
	cmdLower := strings.ToLower(cmd)

	if t.isDangerousRecursiveDelete(cmdLower) {
		return fmt.Errorf("检测到危险的递归删除操作，已禁止执行")
	}

	if analysis := t.checkChmodExecutable(cmd); analysis != "" {
		return fmt.Errorf("%s", analysis)
	}

	if strings.Contains(cmdLower, "|") && t.hasDangerousCommand(cmdLower, []string{"rm", "del", "erase"}) {
		return fmt.Errorf("检测到管道配合删除命令的危险操作，已禁止执行")
	}

	if strings.Contains(cmd, "`") && t.hasDangerousCommand(cmdLower, []string{"rm", "del", "format", "dd"}) {
		return fmt.Errorf("检测到命令替换配合危险操作，已禁止执行")
	}

	if strings.Contains(cmdLower, "git clean") && strings.Contains(cmdLower, "-f") {
		return fmt.Errorf("git clean -f 会删除未跟踪的文件，已禁止执行")
	}
	if strings.Contains(cmdLower, "git reset --hard") && strings.Contains(cmdLower, "head") {
		return fmt.Errorf("git reset --hard 会丢失未提交的修改，已禁止执行")
	}
	if strings.Contains(cmdLower, "git push --force") {
		return fmt.Errorf("git push --force 会覆盖远程历史，已禁止执行")
	}

	if strings.Contains(cmd, "&") && !strings.Contains(cmd, "&&") {
		// 排除重定向中的 &（如 2>&1）
		isRedirectionAmpersand := false
		for _, pattern := range []string{"2>&1", "1>&2", ">&", "&>"} {
			if strings.Contains(cmd, pattern) {
				isRedirectionAmpersand = true
				break
			}
		}
		if !isRedirectionAmpersand {
			if t.hasDangerousCommand(cmdLower, []string{"rm", "del", "format", "dd"}) {
				return fmt.Errorf("检测到后台执行配合危险操作，已禁止执行")
			}
			fmt.Println("⚠️ 命令包含后台执行操作 &")
		}
	}

	if (strings.Contains(cmdLower, "curl") || strings.Contains(cmdLower, "wget")) &&
		(strings.Contains(cmdLower, "| bash") || strings.Contains(cmdLower, "| sh") ||
			strings.Contains(cmdLower, "| python") || strings.Contains(cmdLower, "| perl")) {
		return fmt.Errorf("检测到下载后管道执行的模式(curl/wget | sh)，已禁止执行")
	}

	if strings.Contains(cmdLower, "eval ") {
		return fmt.Errorf("检测到eval命令，已禁止执行")
	}
	if strings.Contains(cmdLower, "base64") && (strings.Contains(cmdLower, "-d") || strings.Contains(cmdLower, "--decode")) {
		return fmt.Errorf("检测到base64解码操作，已禁止执行")
	}

	if strings.Contains(cmdLower, "nc ") && (strings.Contains(cmdLower, "-e") ||
		strings.Contains(cmdLower, "-l") || strings.Contains(cmdLower, "--listen")) {
		return fmt.Errorf("检测到nc监听/反弹shell操作，已禁止执行")
	}

	if err := t.checkCommandExists(cmd); err != nil {
		return err
	}

	return nil
}

// isRootDeletion 检测是否删除根目录
func (t *CLITool) isRootDeletion(cmdLower string) bool {
	// 首先确保是 rm 命令（不是 echo rm 或 alias rm）
	if !strings.HasPrefix(cmdLower, "rm ") && !strings.Contains(cmdLower, " rm ") {
		// Windows: rd /s /q C:\
		if strings.Contains(cmdLower, "rd /s") && (strings.Contains(cmdLower, " c:") || strings.Contains(cmdLower, " c:\\")) {
			return true
		}
		return false
	}

	// 提取 rm 命令部分（避免匹配到 echo 等）
	var rmPart string
	if strings.HasPrefix(cmdLower, "rm ") {
		rmPart = cmdLower
	} else {
		// 找到 " rm " 的位置
		idx := strings.Index(cmdLower, " rm ")
		if idx == -1 {
			return false
		}
		rmPart = cmdLower[idx+1:]
	}

	// 找到 rm 命令的结束位置（遇到 && || ; | 等）
	for _, delim := range []string{" && ", " || ", " | ", "; "} {
		if idx := strings.Index(rmPart, delim); idx != -1 {
			rmPart = rmPart[:idx]
			break
		}
	}

	// 检查是否有 -rf 或 -fr 参数
	if !strings.Contains(rmPart, "-rf") && !strings.Contains(rmPart, "-fr") {
		return false
	}

	// 提取 rm 命令的目标路径（第一个非选项参数）
	parts := strings.Fields(rmPart)
	if len(parts) < 2 {
		return false
	}

	var targetPath string
	for i := 1; i < len(parts); i++ {
		arg := parts[i]
		// 跳过选项参数
		if strings.HasPrefix(arg, "-") {
			continue
		}
		targetPath = arg
		break
	}

	if targetPath == "" {
		return false
	}

	// 检查目标路径是否是根目录
	if targetPath == "/" || targetPath == "/*" {
		return true
	}

	return false
}

func (t *CLITool) isDangerousRecursiveDelete(cmdLower string) bool {
	// 匹配 rm 命令的递归删除模式
	// 模式: rm (任意选项包含r) (当前目录或上级目录)
	patterns := []string{
		`rm\s+.*-[^-]*r[^-]*\s+(\.|\./|\.\.|\.\./|/|\*|~)`,   // rm -rf .
		`rm\s+.*-[^-]*r[^-]*\s+(\.|\./|\.\.|\.\./|/|\*|~)/?`, // rm -rf ./
		`rm\s+.*-[^-]*r[^-]*\s+(\.|\./|\.\.|\.\./|/|\*|~)\*`, // rm -rf ./*
		`rm\s+.*-[^-]*r[^-]*\s+(\.|\.\.)(?:$|\s)`,            // rm -rf .
		`rm\s+.*-r[^-]*f[^-]*\s+(\.|\.\.)(?:$|\s)`,           // rm -rf .
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, cmdLower); matched {
			return true
		}
	}

	return false
}

// checkDeleteOutsideWorkspace 检查删除工作空间外的文件
func (t *CLITool) checkDeleteOutsideWorkspace(cmd string) error {
	// 提取命令中的路径
	paths := t.extractPaths(cmd)

	for _, path := range paths {
		// 跳过相对路径和空路径
		if path == "" || strings.HasPrefix(path, ".") {
			continue
		}

		// 获取绝对路径
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}

		// 检查是否在工作空间内
		if !strings.HasPrefix(absPath, t.workspace) {
			return fmt.Errorf("检测到删除工作空间外的文件: %s，已禁止执行", path)
		}
	}

	return nil
}

// isDiskFormatting 检测磁盘格式化
func (t *CLITool) isDiskFormatting(cmdLower string) bool {
	formatCommands := []string{"mkfs", "format", "fdisk", "parted", "gparted"}
	for _, fc := range formatCommands {
		if strings.Contains(cmdLower, fc) {
			// 检查是否针对设备文件
			if strings.Contains(cmdLower, "/dev/") || strings.Contains(cmdLower, " /") {
				return true
			}
			if matched, _ := regexp.MatchString(`(^|[;&|]\s*)format(\.com)?\s+[a-z]:`, cmdLower); matched {
				return true
			}
		}
	}
	return false
}

// isDiskWrite 检测直接写入磁盘
func (t *CLITool) isDiskWrite(cmdLower string) bool {
	if strings.Contains(cmdLower, "dd") {
		// 检查 dd 命令是否写入磁盘设备
		if strings.Contains(cmdLower, "of=/dev/") ||
			strings.Contains(cmdLower, "of=/dev/sd") ||
			strings.Contains(cmdLower, "of=/dev/hd") {
			return true
		}
	}
	return false
}

// checkChmodExecutable 检查 chmod 设置可执行权限
func (t *CLITool) checkChmodExecutable(cmd string) string {
	if !strings.Contains(strings.ToLower(cmd), "chmod") {
		return ""
	}

	cmdLower := strings.ToLower(cmd)

	// 检查是否添加执行权限
	hasExecute := strings.Contains(cmdLower, "+x") ||
		strings.Contains(cmdLower, "755") ||
		strings.Contains(cmdLower, "777")

	if !hasExecute {
		return ""
	}

	// 提取被修改的文件
	parts := strings.Fields(cmd)
	var targetFile string
	for i, part := range parts {
		if strings.ToLower(part) == "chmod" && i+1 < len(parts) {
			if i+2 < len(parts) {
				targetFile = parts[i+2]
			}
			break
		}
	}

	if targetFile != "" {
		// 检查是否是用户传入的文件（通常在 workspace 内）
		if strings.HasPrefix(targetFile, t.workspace) ||
			strings.HasPrefix(targetFile, "./") ||
			strings.HasPrefix(targetFile, ".") {
			return fmt.Sprintf("检测到给文件 %s 添加可执行权限。如果这是必要的，请确认操作", targetFile)
		}
	}

	return ""
}

// isChmodSystemFile 检查修改系统文件权限
func (t *CLITool) isChmodSystemFile(cmdLower string) bool {
	if !strings.Contains(cmdLower, "chmod") {
		return false
	}

	systemPaths := []string{"/bin/", "/usr/", "/etc/", "/boot/", "/sys/", "/proc/"}
	for _, sp := range systemPaths {
		if strings.Contains(cmdLower, sp) {
			return true
		}
	}

	return false
}

func (t *CLITool) isSystemControlCommand(cmdLower string) bool {
	systemControlCmds := []string{
		"shutdown", "reboot", "halt", "poweroff", "init 0", "init 6",
		"systemctl poweroff", "systemctl reboot", "systemctl halt",
		"stop-computer", "restart-computer",
	}
	for _, sc := range systemControlCmds {
		if strings.Contains(cmdLower, sc) {
			return true
		}
	}
	return false
}

// hasDangerousCommand 检查命令中是否包含危险命令
func (t *CLITool) hasDangerousCommand(cmdLower string, dangerousCmds []string) bool {
	for _, dc := range dangerousCmds {
		if strings.Contains(cmdLower, dc) {
			// 确保是独立的命令而不是其他命令的一部分
			fields := strings.Fields(cmdLower)
			for _, field := range fields {
				if field == dc {
					return true
				}
			}
		}
	}
	return false
}

// checkDangerousRedirection 检查危险的重定向
func (t *CLITool) checkDangerousRedirection(cmd string) error {
	for _, target := range securitycore.ShellRedirectionTargets(cmd) {
		if securitycore.IsAllowedRedirectionTarget(target) {
			continue
		}
		if securitycore.IsProtectedPath(target) || securitycore.IsRawBlockDevicePath(target) {
			return fmt.Errorf("检测到重定向到系统保护路径或磁盘设备: %s，已禁止执行", target)
		}
	}
	return nil
}

func (t *CLITool) checkCommandExists(cmd string) error {
	// 提取第一个单词作为命令
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil
	}

	command := fields[0]
	// 移除路径前缀
	command = strings.TrimPrefix(command, "./")
	command = strings.TrimPrefix(command, "/")
	command = strings.Split(command, ".")[0]

	// 根据操作系统检查命令是否可用
	if !t.isCommandAvailable(command) {
		// On Windows without Git Bash, check if the command can be translated to PowerShell
		if runtime.GOOS == "windows" && !t.runtime.TerminalManager.IsGitBashAvailable() {
			if _, ok := t.translateUnixToPowerShell(cmd); ok {
				return nil // will be translated later, allow it
			}
		}
		// 提供替代建议
		suggestion := t.getCommandSuggestion(command)
		if suggestion != "" {
			return fmt.Errorf("命令 '%s' 在当前环境中不存在。%s", command, suggestion)
		}
		return fmt.Errorf("命令 '%s' 在当前环境中不存在，请先安装或检查命令拼写", command)
	}

	return nil
}

// isCommandAvailable 检查命令在当前操作系统是否可用
func (t *CLITool) isCommandAvailable(cmd string) bool {
	// 1. 先检查是否是当前操作系统的内置命令
	if t.isBuiltinForCurrentOS(cmd) {
		return true
	}

	// 2. 检查是否是外部命令（通过 which/where）
	return t.commandExists(cmd)
}

// isBuiltinForCurrentOS 检查是否是当前操作系统的内置命令
func (t *CLITool) isBuiltinForCurrentOS(cmd string) bool {
	switch runtime.GOOS {
	case "windows":
		// Windows 内置命令（CMD 和 PowerShell）
		windowsBuiltins := map[string]bool{
			// CMD 内置命令
			"cd": true, "dir": true, "cls": true, "copy": true,
			"move": true, "del": true, "erase": true, "rd": true,
			"md": true, "type": true, "findstr": true,
			"sort": true, "more": true, "fc": true, "comp": true,
			"ren": true, "rename": true, "mkdir": true, "rmdir": true,
			"echo": true, "set": true, "exit": true, "pause": true,
			"title": true, "color": true, "prompt": true, "path": true,

			// PowerShell cmdlet（注意：这些不是所有 PowerShell 版本都有）
			"get-content": true, "set-content": true, "add-content": true,
			"select-object": true, "where-object": true, "foreach-object": true,
			"out-file": true, "out-string": true,
		}
		return windowsBuiltins[cmd]

	default:
		// Unix/Linux Shell 内置命令
		unixBuiltins := map[string]bool{
			// Shell 内置命令
			"cd": true, "echo": true, "pwd": true, "export": true,
			"set": true, "unset": true, "alias": true, "unalias": true,
			"history": true, "type": true, "which": true, "command": true,

			// 常见 Unix 命令（这些通常都预装了）
			"ls": true, "cat": true, "grep": true, "find": true,
			"head": true, "tail": true, "wc": true, "sort": true,
			"uniq": true, "ps": true, "df": true, "du": true,
			"top": true, "free": true, "uptime": true, "whoami": true,
			"man": true, "less": true, "more": true, "cut": true,
			"awk": true, "sed": true, "tr": true, "tee": true,
			"touch": true, "mkdir": true, "cp": true, "mv": true,
			"ln": true, "chmod": true, "chown": true,
		}
		return unixBuiltins[cmd]
	}
}

// getCommandSuggestion 为不存在的命令提供替代建议
func (t *CLITool) getCommandSuggestion(cmd string) string {
	switch runtime.GOOS {
	case "windows":
		switch cmd {
		case "head":
			return "建议使用: Get-Content <file> -TotalCount <n> 或 (Get-Content <file>)[0..<n-1]"
		case "tail":
			return "建议使用: Get-Content <file> -Tail <n> 或 (Get-Content <file>)[-<n>..-1]"
		case "wc":
			return "建议使用: (Get-Content <file>).Count 或 (Get-Content <file> -Raw).Length"
		case "grep":
			return "建议使用: Select-String <pattern> <file> 或 findstr <pattern> <file>"
		case "ls":
			return "建议使用: dir 或 Get-ChildItem"
		case "cat":
			return "建议使用: type 或 Get-Content"
		case "ps":
			return "建议使用: Get-Process"
		case "df":
			return "建议使用: Get-PSDrive 或 wmic logicaldisk get size,freespace"
		case "du":
			return "建议使用: (Get-ChildItem -Recurse | Measure-Object -Property Length -Sum).Sum"
		case "find":
			return "建议使用: Get-ChildItem -Path <path> -Recurse -Depth <n> -File 或 -Directory | Where-Object { $_.Name -like '<pattern>' }"
		default:
			return "可考虑安装 Git Bash、Cygwin 或 WSL 来使用 Unix 命令"
		}

	default:
		// Unix/Linux 系统，如果命令不存在，提供安装建议
		switch cmd {
		case "head", "tail", "wc", "grep":
			return "这些命令通常是系统自带的，请检查 coreutils 是否安装"
		case "python3":
			return "请安装 Python 3: sudo apt-get install python3 (Ubuntu) 或 brew install python3 (Mac)"
		case "go":
			return "请安装 Go: https://golang.org/dl/"
		case "node":
			return "请安装 Node.js: https://nodejs.org/"
		default:
			return ""
		}
	}
}

// extractPaths 从命令中提取路径
func (t *CLITool) extractPaths(cmd string) []string {
	var paths []string
	fields := strings.Fields(cmd)

	for _, field := range fields {
		// 跳过命令本身和参数
		if strings.HasPrefix(field, "-") {
			continue
		}

		// 检查是否是路径
		if strings.Contains(field, "/") || strings.Contains(field, "\\") {
			paths = append(paths, field)
		}
	}

	return paths
}

// translateUnixToPowerShell 将常见 Unix 命令翻译为 PowerShell 等效命令
// 返回 (translatedCommand, ok). 如果无法翻译则 ok=false.
func (t *CLITool) translateUnixToPowerShell(cmdStr string) (string, bool) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", false
	}

	fields := t.tokenizeCommand(cmdStr)
	if len(fields) == 0 {
		return "", false
	}

	baseCmd := strings.ToLower(fields[0])
	args := fields[1:]

	switch baseCmd {
	case "find":
		return t.translateFind(args)
	case "grep":
		return t.translateGrep(args)
	case "wc":
		return t.translateWc(args)
	case "head":
		return t.translateHead(args)
	case "tail":
		return t.translateTail(args)
	case "sed":
		return t.translateSed(args)
	case "awk":
		return t.translateAwk(args)
	case "xargs":
		return t.translateXargs(args)
	case "du":
		return t.translateDu(args)
	case "df":
		return t.translateDf(args)
	case "ln":
		return t.translateLn(args)
	case "touch":
		return t.translateTouch(args)
	}
	return "", false
}

// tokenizeCommand tokenizes a command string respecting quotes
func (t *CLITool) tokenizeCommand(cmdStr string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(cmdStr); i++ {
		ch := cmdStr[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// translateFind converts Unix find to PowerShell Get-ChildItem
func (t *CLITool) translateFind(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}

	var path string
	maxDepth := ""
	var excludePatterns []string
	var namePattern string
	var nameNotPatterns []string
	fileType := "" // -type f or -type d

	i := 0
	// First arg is the search path
	if !strings.HasPrefix(args[0], "-") {
		path = args[0]
		// Convert Unix path like /e/ai-node to E:/ai-node for PowerShell
		path = t.convertUnixPathToWindows(path)
		i = 1
	}
	if path == "" {
		path = "."
	}

	for ; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-maxdepth":
			if i+1 < len(args) {
				maxDepth = args[i+1]
				i++
			}
		case "-mindepth":
			// PowerShell doesn't have direct equivalent, skip
			if i+1 < len(args) {
				i++
			}
		case "-name":
			if i+1 < len(args) {
				namePattern = args[i+1]
				i++
			}
		case "-iname":
			if i+1 < len(args) {
				namePattern = args[i+1] // case-insensitive, PowerShell -like is case-insensitive by default
				i++
			}
		case "-type":
			if i+1 < len(args) {
				fileType = args[i+1]
				i++
			}
		case "-not", "!":
			if i+1 < len(args) {
				nextArg := args[i+1]
				if nextArg == "-path" || nextArg == "-regex" {
					if i+2 < len(args) {
						pattern := args[i+2]
						// Convert glob */dir/* to regex dir
						pattern = t.convertGlobToRegex(pattern)
						excludePatterns = append(excludePatterns, pattern)
						i += 2
					}
				} else if nextArg == "-name" {
					if i+2 < len(args) {
						nameNotPatterns = append(nameNotPatterns, args[i+2])
						i += 2
					}
				}
			}
		case "-path":
			// Standalone -path (rare, usually -not -path)
			if i+1 < len(args) {
				excludePatterns = append(excludePatterns, t.convertGlobToRegex(args[i+1]))
				i++
			}
		case "-regex":
			if i+1 < len(args) {
				excludePatterns = append(excludePatterns, args[i+1])
				i++
			}
		}
	}

	var ps strings.Builder

	// Build Get-ChildItem
	ps.WriteString("powershell -Command \"")
	ps.WriteString(fmt.Sprintf("Get-ChildItem -Path '%s'", path))

	// Add -Recurse with optional -Depth
	if maxDepth != "" {
		ps.WriteString(fmt.Sprintf(" -Depth %s", maxDepth))
		ps.WriteString(" -Recurse")
	} else {
		ps.WriteString(" -Recurse")
	}

	// Add -File or -Directory
	if fileType == "f" {
		ps.WriteString(" -File")
	} else if fileType == "d" {
		ps.WriteString(" -Directory")
	}

	// Build filter pipeline
	hasFilter := len(excludePatterns) > 0 || namePattern != "" || len(nameNotPatterns) > 0
	if hasFilter {
		ps.WriteString(" | Where-Object { ")
		conditions := []string{}

		for _, pattern := range excludePatterns {
			conditions = append(conditions,
				fmt.Sprintf("$_.FullName -notmatch '%s'", strings.ReplaceAll(pattern, "'", "''")))
		}

		if namePattern != "" {
			// Convert glob to PowerShell -like pattern
			likePattern := strings.ReplaceAll(namePattern, "*", "*")
			conditions = append(conditions,
				fmt.Sprintf("$_.Name -like '%s'", strings.ReplaceAll(likePattern, "'", "''")))
		}

		for _, notPattern := range nameNotPatterns {
			conditions = append(conditions,
				fmt.Sprintf("$_.Name -notlike '%s'", strings.ReplaceAll(notPattern, "'", "''")))
		}

		ps.WriteString(strings.Join(conditions, " -and "))
		ps.WriteString(" }")
	}

	// Select fullname for find-like output
	ps.WriteString(" | Select-Object -ExpandProperty FullName")
	ps.WriteString("\"")

	return ps.String(), true
}

// translateGrep converts Unix grep to PowerShell Select-String
func (t *CLITool) translateGrep(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}

	var pattern string
	var filePath string
	recursive := false
	caseInsensitive := false

	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-r" || arg == "-R" || arg == "--recursive":
			recursive = true
		case arg == "-i" || arg == "--ignore-case":
			caseInsensitive = true
		case arg == "-v":
			return "", false // too complex for simple translation
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			if pattern == "" {
				pattern = arg
			} else {
				filePath = arg
			}
		}
	}

	if pattern == "" {
		return "", false
	}

	var ps strings.Builder
	ps.WriteString("powershell -Command \"")

	if recursive {
		if filePath != "" {
			ps.WriteString(fmt.Sprintf("Get-ChildItem -Path '%s' -Recurse -File | ", filePath))
		} else {
			ps.WriteString("Get-ChildItem -Recurse -File | ")
		}
		ps.WriteString(fmt.Sprintf("Select-String -Pattern '%s'", strings.ReplaceAll(pattern, "'", "''")))
		if caseInsensitive {
			// PowerShell Select-String is case-insensitive by default
		}
	} else {
		if filePath != "" {
			ps.WriteString(fmt.Sprintf("Select-String -Path '%s' -Pattern '%s'",
				strings.ReplaceAll(filePath, "'", "''"),
				strings.ReplaceAll(pattern, "'", "''")))
		} else {
			// grep from stdin not supported in this translation
			return "", false
		}
	}

	ps.WriteString("\"")
	return ps.String(), true
}

// translateWc converts Unix wc to PowerShell Measure-Object
func (t *CLITool) translateWc(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}

	linesOnly := false
	wordsOnly := false
	charsOnly := false
	var filePath string

	for _, arg := range args {
		switch {
		case arg == "-l":
			linesOnly = true
		case arg == "-w":
			wordsOnly = true
		case arg == "-c" || arg == "-m":
			charsOnly = true
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			filePath = arg
		}
	}

	var ps strings.Builder
	ps.WriteString("powershell -Command \"")

	if filePath != "" {
		ps.WriteString(fmt.Sprintf("$content = Get-Content -Path '%s' -Raw; ", filePath))
	} else {
		ps.WriteString("$content = $input | Out-String; ")
	}

	parts := []string{}
	if linesOnly || (!wordsOnly && !charsOnly) {
		parts = append(parts, "Write-Host \\\"Lines: $((Get-Content -Path '$filePath' | Measure-Object -Line).Lines)\\\"")
	}
	if wordsOnly || (!linesOnly && !charsOnly) {
		parts = append(parts, "Write-Host \\\"Words: $((Get-Content -Path '$filePath' -Raw | Select-String -Pattern '\\S+' -AllMatches).Matches.Count)\\\"")
	}
	if charsOnly || (!linesOnly && !wordsOnly) {
		parts = append(parts, "Write-Host \\\"Chars: $($content.Length)\\\"")
	}

	ps.WriteString(strings.Join(parts, "; "))
	ps.WriteString("\"")

	return ps.String(), true
}

// translateHead converts Unix head to PowerShell
func (t *CLITool) translateHead(args []string) (string, bool) {
	n := "10"
	var filePath string

	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "-n"):
			if len(arg) > 2 {
				n = arg[2:]
			} else if i+1 < len(args) {
				n = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			filePath = arg
		}
	}

	if filePath == "" {
		return "", false
	}

	return fmt.Sprintf("powershell -Command \"Get-Content -Path '%s' -TotalCount %s\"", filePath, n), true
}

// translateTail converts Unix tail to PowerShell
func (t *CLITool) translateTail(args []string) (string, bool) {
	n := "10"
	var filePath string

	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "-n"):
			if len(arg) > 2 {
				n = arg[2:]
			} else if i+1 < len(args) {
				n = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			filePath = arg
		}
	}

	if filePath == "" {
		return "", false
	}

	return fmt.Sprintf("powershell -Command \"Get-Content -Path '%s' -Tail %s\"", filePath, n), true
}

// translateSed converts simple sed substitutions to PowerShell -replace
func (t *CLITool) translateSed(args []string) (string, bool) {
	// Only handle the common case: sed 's/from/to/g' file
	if len(args) < 2 {
		return "", false
	}

	var expression string
	var filePath string

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			if arg == "-e" || arg == "--expression" {
				continue
			}
			continue
		}
		if strings.HasPrefix(arg, "s/") || strings.HasPrefix(arg, "'s/") {
			expression = strings.Trim(arg, "'\"")
		} else {
			filePath = arg
		}
	}

	if expression == "" {
		return "", false
	}

	// Parse s/pattern/replacement/flags
	parts := strings.SplitN(expression, "/", 4)
	if len(parts) < 4 || parts[0] != "s" {
		return "", false
	}

	pattern := parts[1]
	replacement := parts[2]
	flags := ""
	if len(parts) > 3 {
		flags = parts[3]
	}

	globalFlag := strings.Contains(flags, "g")

	if filePath != "" {
		if globalFlag {
			return fmt.Sprintf("powershell -Command \"(Get-Content -Path '%s' -Raw) -replace '%s', '%s'\"",
				filePath, pattern, replacement), true
		}
		return fmt.Sprintf("powershell -Command \"(Get-Content -Path '%s' -Raw) -replace '%s', '%s', 1\"",
			filePath, pattern, replacement), true
	}

	return "", false
}

// translateAwk - basic awk translation
func (t *CLITool) translateAwk(args []string) (string, bool) {
	// Most awk usage is too complex; only translate simple {print $N}
	if len(args) < 1 {
		return "", false
	}

	script := strings.Trim(args[0], "'\"")
	filePath := ""
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		filePath = args[1]
	}

	// Only handle {print $N} pattern
	if strings.Contains(script, "{print") {
		fields := strings.TrimPrefix(script, "{")
		fields = strings.TrimSuffix(fields, "}")
		fields = strings.TrimSpace(fields)
		fields = strings.TrimPrefix(fields, "print ")
		fields = strings.ReplaceAll(fields, "$", "")

		if filePath != "" {
			return fmt.Sprintf("powershell -Command \"Get-Content '%s' | ForEach-Object { ($_ -split '\\s+')[%s] }\"",
				filePath, fields), true
		}
	}

	return "", false
}

// translateXargs - basic xargs translation
func (t *CLITool) translateXargs(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}

	// xargs <command> → ForEach-Object { <command> $_ }
	remainingCmd := strings.Join(args, " ")
	return fmt.Sprintf("powershell -Command \"$input | ForEach-Object { %s $_ }\"", remainingCmd), true
}

// translateDu converts du to PowerShell
func (t *CLITool) translateDu(args []string) (string, bool) {
	path := "."
	summaryOnly := false
	maxDepth := ""

	for i, arg := range args {
		switch {
		case arg == "-s" || arg == "--summarize":
			summaryOnly = true
		case arg == "--max-depth" || arg == "-d":
			if i+1 < len(args) {
				maxDepth = args[i+1]
			}
		case !strings.HasPrefix(arg, "-"):
			path = t.convertUnixPathToWindows(arg)
		}
	}

	if summaryOnly {
		return fmt.Sprintf("powershell -Command \"(Get-ChildItem -Path '%s' -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1KB\"", path), true
	}

	depth := "3"
	if maxDepth != "" {
		depth = maxDepth
	}
	return fmt.Sprintf("powershell -Command \"Get-ChildItem -Path '%s' -Depth %s -Directory | ForEach-Object { $size = (Get-ChildItem $_.FullName -Recurse -File -ErrorAction SilentlyContinue | Measure-Object -Property Length -Sum).Sum; Write-Host ('{0,10} KB  {1}' -f [math]::Round($size/1KB, 0), $_.FullName) }\"", path, depth), true
}

// translateDf converts df to PowerShell
func (t *CLITool) translateDf(args []string) (string, bool) {
	return "powershell -Command \"Get-PSDrive -PSProvider FileSystem | Select-Object Name, @{N='Total(GB)';E={[math]::Round($_.Used/1GB+$_.Free/1GB,1)}}, @{N='Used(GB)';E={[math]::Round($_.Used/1GB,1)}}, @{N='Free(GB)';E={[math]::Round($_.Free/1GB,1)}}\"", true
}

// translateLn converts ln -s to New-Item -ItemType SymbolicLink
func (t *CLITool) translateLn(args []string) (string, bool) {
	symbolic := false
	var target, linkPath string

	for _, arg := range args {
		switch {
		case arg == "-s" || arg == "--symbolic":
			symbolic = true
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			if target == "" {
				target = arg
			} else {
				linkPath = arg
			}
		}
	}

	if !symbolic || target == "" || linkPath == "" {
		return "", false
	}

	return fmt.Sprintf("powershell -Command \"New-Item -ItemType SymbolicLink -Path '%s' -Target '%s'\"", linkPath, target), true
}

// translateTouch converts touch to PowerShell New-Item or file timestamp update
func (t *CLITool) translateTouch(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}

	var files []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		return "", false
	}

	// Build PowerShell script to touch each file
	quotedFiles := make([]string, len(files))
	for i, f := range files {
		quotedFiles[i] = fmt.Sprintf("'%s'", f)
	}

	return fmt.Sprintf("powershell -Command \"%s | ForEach-Object { if (Test-Path $_) { (Get-Item $_).LastWriteTime = Get-Date } else { New-Item -ItemType File -Path $_ | Out-Null } }\"",
		strings.Join(quotedFiles, ",")), true
}

// convertUnixPathToWindows converts Unix-style paths to Windows
func (t *CLITool) convertUnixPathToWindows(path string) string {
	// /e/ai-node → E:/ai-node
	if len(path) >= 2 && path[0] == '/' && path[1] != '/' {
		parts := strings.SplitN(path[1:], "/", 2)
		if len(parts) >= 1 && len(parts[0]) == 1 {
			driveLetter := strings.ToUpper(parts[0])
			if len(parts) == 1 {
				return fmt.Sprintf("%s:/", driveLetter)
			}
			return fmt.Sprintf("%s:/%s", driveLetter, parts[1])
		}
	}
	return path
}

// convertGlobToRegex converts shell glob patterns to regex
func (t *CLITool) convertGlobToRegex(glob string) string {
	// Remove surrounding quotes
	glob = strings.Trim(glob, "'\"")
	// */dir/* → dir (extract directory name pattern)
	s := strings.ReplaceAll(glob, "*/", "")
	s = strings.ReplaceAll(s, "/*", "")
	s = strings.ReplaceAll(s, "*", ".*")
	return s
}

func (t *CLITool) formatOutput(cmd, stdout, stderr string, err error, duration time.Duration, workDir string) string {
	var result strings.Builder
	result.WriteString("Command Execution" + strings.Repeat("─", 3) + "\n")
	result.WriteString(fmt.Sprintf("cmd: %s\n", cmd))
	result.WriteString(fmt.Sprintf("duration: %v\n", duration))
	// result.WriteString(fmt.Sprintf("目录: %s\n", workDir))

	if err == nil {
		result.WriteString("状态:成功\n")
	} else {
		result.WriteString(fmt.Sprintf("状态:失败,失败原因是:%v\n", err))
	}
	cleanstdout := textutil.Clean(stdout)
	cleanstderr := textutil.Clean(stderr)
	if cleanstdout != "" {

		result.WriteString(fmt.Sprintf("output result:\n%s\n", cleanstdout)) //":"

		if !strings.HasSuffix(cleanstdout, "\n") {
			result.WriteString("\n")
		}
	}

	if cleanstderr != "" && cleanstderr != "\n" {
		if len(cleanstderr) > 500 {
			cleanstderr = textutil.Truncate(cleanstderr, 500) + "\n... (error information truncated)"
		}
		result.WriteString("\n⚠️ error:\n")
		result.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			result.WriteString("\n")
		}
	}
	return result.String()
}

func (t *CLITool) GetWorkspace() string {
	return t.workspace
}

func (t *CLITool) IsCommandSafe(cmd string) (bool, string) {
	err := t.checkSafety(cmd)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

// IsRunning 查询命令是否正在运行
func (t *CLITool) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isRunning
}

// -------------------------- 原有功能：命令检测与版本获取 --------------------------

type cmdVersionConfig struct {
	args     []string
	filterFn func(string) string
}

func (t *CLITool) detectAvailableDevCommandsWithVersion() map[string]string {
	devCmdConfigs := map[string]cmdVersionConfig{
		"python":        {args: []string{"--version"}, filterFn: filterPythonVersion},
		"python3":       {args: []string{"--version"}, filterFn: filterPythonVersion},
		"python2":       {args: []string{"--version"}, filterFn: filterPythonVersion},
		"go":            {args: []string{"version"}, filterFn: filterGoVersion},
		"gcc":           {args: []string{"--version"}, filterFn: filterGccVersion},
		"g++":           {args: []string{"--version"}, filterFn: filterGccVersion},
		"make":          {args: []string{"--version"}, filterFn: filterMakeVersion},
		"cmake":         {args: []string{"--version"}, filterFn: filterCmakeVersion},
		"java":          {args: []string{"-version"}, filterFn: filterJavaVersion},
		"javac":         {args: []string{"-version"}, filterFn: filterJavaVersion},
		"node":          {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"npm":           {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"yarn":          {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"pnpm":          {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"deno":          {args: []string{"--version"}, filterFn: filterDenoVersion},
		"rustc":         {args: []string{"--version"}, filterFn: filterRustVersion},
		"cargo":         {args: []string{"--version"}, filterFn: filterRustVersion},
		"dotnet":        {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"pip":           {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"pip3":          {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"awd":           {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"sed":           {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"she":           {args: []string{"--version"}, filterFn: filterSimpleVersion},
		"agent-browser": {args: []string{"--version"}, filterFn: filterSimpleVersion},
	}

	available := make(map[string]string)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for cmd, config := range devCmdConfigs {
		wg.Add(1)
		go func(command string, cfg cmdVersionConfig) {
			defer wg.Done()
			if !t.commandExists(command) {
				return
			}
			version, err := t.getCommandVersion(command, cfg.args)
			if err != nil {
				version = "未知版本"
			} else {
				version = cfg.filterFn(version)
			}
			mu.Lock()
			available[command] = version
			mu.Unlock()
		}(cmd, config)
	}
	wg.Wait()

	return available
}

func (t *CLITool) commandExists(cmd string) bool {
	// 如果是 Windows PowerShell，检查是否为有效的 cmdlet
	if runtime.GOOS == "windows" {
		// 先检查是否是 PowerShell cmdlet
		if t.isPowerShellCmdlet(cmd) {
			return true
		}

		// 使用 where 命令检查（适用于 CMD 和 PowerShell）
		checkCmd := exec.Command("where", cmd)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- checkCmd.Run() }()

		select {
		case err := <-done:
			return err == nil
		case <-ctx.Done():
			return false
		}
	}

	// Unix/Linux 使用 which 命令
	checkCmd := exec.Command("which", cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- checkCmd.Run() }()

	select {
	case err := <-done:
		return err == nil
	case <-ctx.Done():
		return false
	}
}

// isPowerShellCmdlet 检查是否是 PowerShell cmdlet
func (t *CLITool) isPowerShellCmdlet(cmd string) bool {
	// PowerShell 常用 cmdlet
	powerShellCmdlets := map[string]bool{
		"get-content": true, "set-content": true, "add-content": true,
		"select-object": true, "where-object": true, "foreach-object": true,
		"out-file": true, "out-string": true, "convertto-csv": true,
		"import-csv": true, "export-csv": true, "get-childitem": true,
		"get-process": true, "get-service": true, "get-eventlog": true,
	}

	if powerShellCmdlets[cmd] {
		return true
	}

	// 动态检查：尝试执行 Get-Command
	checkCmd := exec.Command("powershell", "-Command", "Get-Command", cmd)
	if err := checkCmd.Run(); err == nil {
		return true
	}

	return false
}

func (t *CLITool) getCommandVersion(cmd string, args []string) (string, error) {
	versionCmd := exec.Command(cmd, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	versionCmd.Stdout = &stdout
	versionCmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- versionCmd.Run() }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		return "", fmt.Errorf("获取版本超时")
	}

	if err != nil {
		return "", err
	}

	output := stdout.String()
	output = textutil.Clean(output)
	if output == "" {
		output = stderr.String()
	}
	return strings.TrimSpace(output), nil
}

// Stop 手动停止当前执行的命令（优雅取消）
func (t *CLITool) Stop() error {
	t.mu.Lock()
	if !t.isRunning || (t.cancelFunc == nil && (t.currentCmd == nil || t.currentCmd.Process == nil)) {
		t.mu.Unlock()
		return fmt.Errorf("没有正在运行的命令")
	}
	cancel := t.cancelFunc
	cmd := t.currentCmd
	t.mu.Unlock()

	// 1. 先调用 context 取消（优雅终止）
	if cancel != nil {
		cancel()
	}

	// 2. 最多等待 3 秒，但进程树一退出就立即返回。
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		t.mu.Lock()
		stillRunning := t.isRunning && t.currentCmd == cmd && cmd != nil && cmd.Process != nil
		t.mu.Unlock()
		if !stillRunning {
			return nil
		}
		select {
		case <-ticker.C:
			continue
		case <-deadline.C:
		}
		break
	}
	// 超时未退出，强制 Kill
	if err := t.Kill(); err != nil {
		return fmt.Errorf("优雅停止失败，强制终止也失败: %v", err)
	}
	return fmt.Errorf("命令响应超时，已强制终止")
}

// Kill 强制终止当前命令及其派生的完整进程树。
func (t *CLITool) Kill() error {
	t.mu.Lock()
	if !t.isRunning || t.currentCmd == nil || t.currentCmd.Process == nil {
		t.mu.Unlock()
		return fmt.Errorf("没有正在运行的进程")
	}
	process := t.currentCmd.Process
	t.isRunning = false
	t.currentCmd = nil
	t.cancelFunc = nil
	t.mu.Unlock()

	if err := processutil.TerminateTree(process); err != nil {
		return fmt.Errorf("强制终止进程树失败: %v", err)
	}
	return nil
}

// 版本过滤函数（略，同之前代码）
func filterPythonVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 2 {
		return parts[1]
	}
	return version
}
func filterGoVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 3 {
		return strings.TrimPrefix(parts[2], "go")
	}
	return version
}
func filterGccVersion(version string) string {
	lines := strings.Split(version, "\n")
	if len(lines) == 0 {
		return version
	}
	parts := strings.Fields(lines[0])
	for _, part := range parts {
		if strings.Contains(part, ".") && strings.IndexFunc(part, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r == '.')
		}) == -1 {
			return part
		}
	}
	return version
}

func filterMakeVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 3 {
		return parts[2]
	}
	return version
}
func filterCmakeVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 3 {
		return parts[2]
	}
	return version
}
func filterJavaVersion(version string) string {
	parts := strings.Fields(version)
	for _, part := range parts {
		trimmed := strings.Trim(part, "\"")
		if strings.HasPrefix(trimmed, "1.") || strings.Contains(trimmed, ".") && strings.IndexFunc(trimmed, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r == '.')
		}) == -1 {
			return trimmed
		}
	}
	return version
}
func filterSimpleVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

func filterDenoVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 2 {
		return parts[1]
	}
	return version
}
func filterRustVersion(version string) string {
	parts := strings.Fields(version)
	if len(parts) >= 2 {
		return parts[1]
	}
	return version
}
