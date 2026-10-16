// persistent_shell_common.go

package processutil

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mac/internal/textutil"
)

type ShellType string

const (
	ShellTypePowerShell ShellType = "powershell"
	ShellTypeBash       ShellType = "bash"
	ShellTypeCmd        ShellType = "cmd"
	ShellTypeGitBash    ShellType = "git-bash"
)

type PersistentShell struct {
	id          string
	shellType   ShellType
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	reader      *bufio.Reader
	workDir     string
	mu          sync.Mutex
	lastActive  time.Time
	healthy     bool
	closeOnce   sync.Once
	initDone    bool
	gitBashPath string
}

func NewPersistentShell(id, workDir string) (*PersistentShell, error) {
	return newPersistentShell(id, workDir, FindGitBash())
}

func newPersistentShell(id, workDir, gitBashPath string) (*PersistentShell, error) {
	log.Printf("[PersistentShell] 开始创建会话 id[%s], workDir[%s], OS[%s]", id, workDir, runtime.GOOS)

	// 验证工作目录
	if workDir != "" {
		if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
			log.Printf("[PersistentShell] 警告: 工作目录不存在或不是目录: %s, 使用当前目录", workDir)
			workDir = ""
		}
	}

	shell := &PersistentShell{
		id:          id,
		workDir:     workDir,
		lastActive:  time.Now(),
		healthy:     true,
		initDone:    false,
		gitBashPath: gitBashPath,
	}

	// 尝试创建持久化 Shell
	if err := shell.createShell(); err != nil {
		log.Printf("[PersistentShell] 创建 Shell 失败: %v, 降级为命令模式", err)
		return shell.createFallbackMode(), nil
	}

	// 如果 createShell 没有启动进程（比如没有找到合适的 Shell）
	if shell.cmd == nil {
		log.Printf("[PersistentShell] 未创建持久化进程，使用命令模式")
		return shell, nil
	}

	// 等待 Shell 启动
	time.Sleep(300 * time.Millisecond)

	// 初始化 Shell 环境
	if err := shell.initializeShell(); err != nil {
		log.Printf("[PersistentShell] Shell 初始化失败: %v, 降级为命令模式", err)
		shell.Close()
		return shell.createFallbackMode(), nil
	}

	// 测试连接（只测试一次）
	if err := shell.testConnection(); err != nil {
		log.Printf("[PersistentShell] 连接测试失败: %v, 降级为命令模式", err)
		shell.Close()
		return shell.createFallbackMode(), nil
	}

	shell.initDone = true
	log.Printf("[PersistentShell] Shell 创建成功, 类型[%s], PID[%d]", shell.shellType, shell.cmd.Process.Pid)
	return shell, nil
}

// createFallbackMode 创建降级模式的 Shell（每次执行新进程）
func (s *PersistentShell) createFallbackMode() *PersistentShell {
	fallbackType := ShellTypeCmd
	if runtime.GOOS == "windows" {
		fallbackType = ShellTypeCmd
	} else {
		fallbackType = ShellTypeBash
	}

	log.Printf("[PersistentShell] 降级为命令模式, 类型[%s]", fallbackType)
	return &PersistentShell{
		id:          s.id,
		shellType:   fallbackType,
		workDir:     s.workDir,
		lastActive:  time.Now(),
		healthy:     true,
		cmd:         nil,
		initDone:    true,
		gitBashPath: s.gitBashPath,
	}
}

var markerSeq atomic.Int64

func sanitizeShellMarkerName(value string) string {
	return strings.Map(func(current rune) rune {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' || current == '-' || current == '_' {
			return current
		}
		return '_'
	}, value)
}

// generateMarker 生成唯一标记符，使用原子递增序号保证唯一性
func (s *PersistentShell) generateMarker() string {
	seq := markerSeq.Add(1)
	return fmt.Sprintf("__SHELL_MARKER_%s_%d__", sanitizeShellMarkerName(s.id), seq)
}

func buildPersistentShellCommand(shellType ShellType, command, markerStart, markerStatus, markerEnd string) string {
	if shellType == ShellTypePowerShell {
		return fmt.Sprintf("Write-Output '%s'\n%s\n$__openkitty_ok = $?\n$__openkitty_last = $LASTEXITCODE\nif ($__openkitty_ok) { $__openkitty_exit = 0 } elseif ($null -ne $__openkitty_last) { $__openkitty_exit = $__openkitty_last } else { $__openkitty_exit = 1 }\nWrite-Output ('%s' + $__openkitty_exit)\nWrite-Output '%s'\n",
			markerStart, command, markerStatus, markerEnd)
	}
	return fmt.Sprintf("printf '%%s\\n' '%s'\n%s\n__openkitty_exit=$?\nprintf '%%s%%d\\n' '%s' \"$__openkitty_exit\"\nprintf '%%s\\n' '%s'\n",
		markerStart, command, markerStatus, markerEnd)
}

// Execute 执行命令
func (s *PersistentShell) Execute(ctx context.Context, command string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastActive = time.Now()

	if s.cmd == nil {
		return s.executeCommandMode(ctx, command)
	}

	return s.executePersistentMode(ctx, command)
}

// executePersistentMode 在持久化 Shell 中执行命令
func (s *PersistentShell) executePersistentMode(ctx context.Context, command string) (string, error) {
	log.Printf("[executePersistentMode] 执行命令: %s", textutil.Truncate(command, 100))

	if s.stdin == nil || s.stdout == nil {
		return "", fmt.Errorf("管道未初始化")
	}
	if s.reader == nil {
		s.reader = bufio.NewReader(s.stdout)
	}

	// 生成唯一标记
	markerStart := s.generateMarker()
	markerStatus := s.generateMarker() + ":"
	markerEnd := s.generateMarker()

	// Start must be emitted before the command so its output is captured. The
	// status marker stores the exit code immediately after execution; an
	// intervening echo would otherwise overwrite $? and turn failures into 0.
	markedCommand := buildPersistentShellCommand(s.shellType, command, markerStart, markerStatus, markerEnd)

	log.Printf("[executePersistentMode] 发送命令: %s", textutil.Truncate(markedCommand, 150))

	if _, err := io.WriteString(s.stdin, markedCommand); err != nil {
		log.Printf("[executePersistentMode] 写入命令失败: %v", err)
		return "", fmt.Errorf("写入命令失败: %w", err)
	}

	// 根据命令类型设置超时
	cmdTimeout := 30 * time.Second
	if strings.Contains(command, "git clone") || strings.Contains(command, "git pull") {
		cmdTimeout = 300 * time.Second
	} else if strings.Contains(command, "npm install") || strings.Contains(command, "pip install") {
		cmdTimeout = 120 * time.Second
	} else if strings.Contains(command, "curl") || strings.Contains(command, "wget") {
		cmdTimeout = 60 * time.Second
	}

	readCtx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	type commandResult struct {
		output string
		err    error
	}
	resultChan := make(chan commandResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[executePersistentMode] 读取协程 panic: %v", r)
				resultChan <- commandResult{err: fmt.Errorf("读取异常: %v", r)}
			}
		}()

		var output strings.Builder
		inOutput := false
		exitCode := ""

		for {
			// 检查是否超时
			select {
			case <-readCtx.Done():
				if output.Len() > 0 {
					log.Printf("[executePersistentMode] 超时但有部分输出")
				}
				resultChan <- commandResult{output: strings.TrimSpace(output.String()), err: fmt.Errorf("读取超时")}
				return
			default:
			}

			line, err := s.reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					log.Printf("[executePersistentMode] Shell 输出流关闭")
					resultChan <- commandResult{output: strings.TrimSpace(output.String()), err: fmt.Errorf("Shell 已关闭")}
					return
				}
				resultChan <- commandResult{output: strings.TrimSpace(output.String()), err: fmt.Errorf("读取失败: %w", err)}
				return
			}

			line = strings.TrimRight(line, "\r\n")
			log.Printf("[executePersistentMode] 读取行: %s", textutil.Truncate(line, 100))

			// 检测开始标记
			if strings.TrimSpace(line) == markerStart {
				log.Printf("[executePersistentMode] 检测到开始标记")
				inOutput = true
				continue
			}

			// 检测结束标记
			if strings.TrimSpace(line) == markerEnd {
				log.Printf("[executePersistentMode] 检测到结束标记, 退出码: %s, 输出长度: %d", exitCode, output.Len())
				if exitCode != "" && exitCode != "0" {
					resultChan <- commandResult{output: strings.TrimSpace(output.String()), err: fmt.Errorf("命令执行失败，退出码: %s", exitCode)}
					return
				}
				resultChan <- commandResult{output: strings.TrimSpace(output.String())}
				return
			}

			// 在输出区域内
			if inOutput {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, markerStatus) {
					exitCode = strings.TrimSpace(strings.TrimPrefix(trimmed, markerStatus))
					if _, err := strconv.Atoi(exitCode); err != nil {
						resultChan <- commandResult{output: strings.TrimSpace(output.String()), err: fmt.Errorf("无效退出码: %q", exitCode)}
						return
					}
					log.Printf("[executePersistentMode] 检测到退出码: %s", exitCode)
					continue
				}

				if output.Len() > 0 {
					output.WriteString("\n")
				}
				output.WriteString(line)
			}
		}
	}()

	select {
	case result := <-resultChan:
		cleaned := cleanShellOutput(result.output, s.shellType)
		if result.err != nil {
			log.Printf("[executePersistentMode] 命令执行失败: %v", result.err)
			return cleaned, result.err
		}
		log.Printf("[executePersistentMode] 命令执行成功, 原始长度: %d, 清理后: %d", len(result.output), len(cleaned))
		return cleaned, nil
	case <-readCtx.Done():
		log.Printf("[executePersistentMode] 读取超时")
		s.healthy = false
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		return "", fmt.Errorf("命令执行超时 (%v)", cmdTimeout)
	}
}

// executeCommandMode 在命令模式下执行（每次创建新进程）
func (s *PersistentShell) executeCommandMode(ctx context.Context, command string) (string, error) {
	log.Printf("[executeCommandMode] 执行命令: %s", textutil.Truncate(command, 100))

	var cmd *exec.Cmd

	switch s.shellType {
	case ShellTypePowerShell:
		cmd = exec.CommandContext(ctx, "powershell", "-Command", command)
	case ShellTypeBash, ShellTypeGitBash:
		if runtime.GOOS == "windows" {
			if s.gitBashPath != "" {
				cmd = exec.CommandContext(ctx, s.gitBashPath, "-c", command)
			} else {
				cmd = exec.CommandContext(ctx, "bash", "-c", command)
			}
		} else {
			cmd = exec.CommandContext(ctx, "bash", "-c", command)
		}
	case ShellTypeCmd:
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd.exe", "/c", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
	default:
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd.exe", "/c", command)
		} else {
			cmd = exec.CommandContext(ctx, "bash", "-c", command)
		}
	}

	if s.workDir != "" {
		cmd.Dir = s.workDir
	}

	// 设置环境变量
	env := cmd.Environ()
	env = append(env,
		"PYTHONIOENCODING=utf-8",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
	)
	if runtime.GOOS == "windows" {
		env = append(env, "CHCP=65001")
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		log.Printf("[executeCommandMode] 命令执行失败: %v, 输出: %s", err, textutil.Truncate(output, 200))
		return output, err
	}

	log.Printf("[executeCommandMode] 命令执行成功, 输出长度: %d", len(output))
	return output, nil
}

// cleanShellOutput 清理 Shell 输出中的提示符等无用内容
func cleanShellOutput(output string, shellType ShellType) string {
	lines := strings.Split(output, "\n")
	var cleaned []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 跳过空行
		if trimmed == "" {
			continue
		}

		// 跳过提示符行
		if strings.Contains(trimmed, "$ ") || strings.Contains(trimmed, "# ") ||
			strings.Contains(trimmed, "> ") || strings.HasPrefix(trimmed, "PS ") {
			continue
		}

		// 跳过 Shell 初始化输出
		if strings.Contains(trimmed, "SHELL_READY") || strings.HasPrefix(trimmed, "__SHELL_MARKER_") {
			continue
		}

		// 跳过 echo 命令回显
		if strings.HasPrefix(trimmed, "echo ") &&
			(strings.Contains(trimmed, "SHELL_READY") || strings.Contains(trimmed, "SHELL_MARKER")) {
			continue
		}

		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "\n")
}

// testConnection 测试 Shell 连接
func (s *PersistentShell) testConnection() error {
	log.Printf("[testConnection] 测试连接")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := s.executePersistentMode(ctx, "echo ok")
	if err != nil {
		log.Printf("[testConnection] 测试失败: %v", err)
		return fmt.Errorf("连接测试失败: %w", err)
	}

	log.Printf("[testConnection] 测试成功, 输出: %s", textutil.Truncate(output, 50))
	return nil
}

// GetWorkDir 获取工作目录
func (s *PersistentShell) GetWorkDir() (string, error) {
	if s.cmd != nil && (s.shellType == ShellTypeBash || s.shellType == ShellTypeGitBash) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		output, err := s.Execute(ctx, "pwd")
		if err != nil {
			return s.workDir, err
		}
		return strings.TrimSpace(output), nil
	}

	return s.workDir, nil
}

// SetWorkDir 设置工作目录
func (s *PersistentShell) SetWorkDir(path string) error {
	s.workDir = path

	if s.cmd != nil && (s.shellType == ShellTypeBash || s.shellType == ShellTypeGitBash) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := s.Execute(ctx, fmt.Sprintf("cd %s", path))
		return err
	}
	return nil
}

// SetEnv 设置环境变量
func (s *PersistentShell) SetEnv(key, value string) error {
	if s.cmd != nil && (s.shellType == ShellTypeBash || s.shellType == ShellTypeGitBash) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := s.Execute(ctx, fmt.Sprintf("export %s=%s", key, value))
		return err
	}
	return nil
}

// GetEnv 获取环境变量
func (s *PersistentShell) GetEnv(key string) (string, error) {
	if s.cmd != nil && (s.shellType == ShellTypeBash || s.shellType == ShellTypeGitBash) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		output, err := s.Execute(ctx, fmt.Sprintf("echo $%s", key))
		return strings.TrimSpace(output), err
	}
	return "", nil
}

// IsHealthy 检查健康状态
func (s *PersistentShell) IsHealthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.healthy {
		return false
	}

	// 命令模式总是健康的（可以创建新进程）
	if s.cmd == nil {
		return true
	}

	// 检查进程是否存在
	if s.cmd.Process == nil {
		s.healthy = false
		return false
	}
	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		s.healthy = false
		return false
	}

	return true
}

// LastActive 获取最后活动时间
func (s *PersistentShell) LastActive() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}

func (s *PersistentShell) SessionInfo(taskID string) TerminalSessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TerminalSessionInfo{
		TaskID:     taskID,
		Type:       s.shellType,
		WorkDir:    s.workDir,
		LastActive: s.lastActive,
		Healthy:    s.healthy,
	}
}

// MarkIdle 标记为空闲
func (s *PersistentShell) MarkIdle() {
	s.lastActive = time.Now()
}

// GetTerminalInfo 获取终端信息
func (s *PersistentShell) GetTerminalInfo() string {
	if s.cmd == nil {
		return fmt.Sprintf("Type=%s (command mode), WorkDir=%s", s.shellType, s.workDir)
	}
	return fmt.Sprintf("Type=%s (persistent), WorkDir=%s, PID=%d, InitDone=%v",
		s.shellType, s.workDir, s.cmd.Process.Pid, s.initDone)
}

// GetType 获取 Shell 类型
func (s *PersistentShell) GetType() ShellType {
	return s.shellType
}

// Close 关闭 Shell
func (s *PersistentShell) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.healthy = false
	s.initDone = false

	var lastErr error

	// 尝试优雅关闭
	if s.stdin != nil {
		io.WriteString(s.stdin, "exit\n")
		time.Sleep(200 * time.Millisecond)
		s.stdin.Close()
	}

	// 关闭管道
	if s.stdout != nil {
		if err := s.stdout.Close(); err != nil {
			lastErr = err
		}
	}
	if s.stderr != nil {
		if err := s.stderr.Close(); err != nil {
			lastErr = err
		}
	}

	// 强制终止进程
	if s.cmd != nil && s.cmd.Process != nil {
		if err := s.cmd.Process.Kill(); err != nil {
			log.Printf("[Close] 终止进程失败: %v", err)
			lastErr = err
		}
		done := make(chan struct{})
		go func() {
			s.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Printf("[Close] Wait timeout, id[%s]", s.id)
		}
	}

	log.Printf("[Close] Shell 已关闭, id[%s]", s.id)
	return lastErr
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
