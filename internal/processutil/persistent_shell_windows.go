//go:build windows
// +build windows

package processutil

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"mac/internal/textutil"
)

func (s *PersistentShell) createShell() error {
	log.Printf("[createShell] 创建 Shell, OS=windows")

	if s.gitBashPath != "" {
		gitBashPath := s.gitBashPath
		log.Printf("[createShell] 找到 Git Bash: %s", gitBashPath)

		s.shellType = ShellTypeGitBash

		cmd := exec.Command(gitBashPath, "--login", "--norc", "-i")

		// Windows 特定：隐藏窗口
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}

		// 设置环境变量
		cmd.Env = append(cmd.Environ(),
			"TERM=dumb",
			"COLUMNS=4096",
			"LINES=100",
			"GIT_PAGER=cat",
			"PAGER=cat",
			"EDITOR=:",
			"NO_COLOR=1",
			"CLICOLOR=0",
			"LS_COLORS=",
		)

		if s.workDir != "" {
			cmd.Dir = s.workDir
		}

		var err error
		s.stdin, err = cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("创建 stdin 管道失败: %w", err)
		}

		s.stdout, err = cmd.StdoutPipe()
		if err != nil {
			s.stdin.Close()
			return fmt.Errorf("创建 stdout 管道失败: %w", err)
		}

		s.stderr, err = cmd.StderrPipe()
		if err != nil {
			s.stdin.Close()
			s.stdout.Close()
			return fmt.Errorf("创建 stderr 管道失败: %w", err)
		}

		if err := cmd.Start(); err != nil {
			s.stdin.Close()
			s.stdout.Close()
			s.stderr.Close()
			return fmt.Errorf("启动 Git Bash 失败: %w", err)
		}

		s.cmd = cmd
		log.Printf("[createShell] Git Bash 进程启动成功, PID=%d", cmd.Process.Pid)
		return nil
	}

	// 尝试 PowerShell
	if psPath, err := exec.LookPath("powershell.exe"); err == nil {
		log.Printf("[createShell] 使用 PowerShell: %s", psPath)
		s.shellType = ShellTypePowerShell

		cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive", "-Command", "-")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000,
		}

		if s.workDir != "" {
			cmd.Dir = s.workDir
		}

		var err error
		s.stdin, err = cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("创建 PowerShell stdin 管道失败: %w", err)
		}

		s.stdout, err = cmd.StdoutPipe()
		if err != nil {
			s.stdin.Close()
			return fmt.Errorf("创建 PowerShell stdout 管道失败: %w", err)
		}

		s.stderr, err = cmd.StderrPipe()
		if err != nil {
			s.stdin.Close()
			s.stdout.Close()
			return fmt.Errorf("创建 PowerShell stderr 管道失败: %w", err)
		}

		if err := cmd.Start(); err != nil {
			s.stdin.Close()
			s.stdout.Close()
			s.stderr.Close()
			return fmt.Errorf("启动 PowerShell 失败: %w", err)
		}

		s.cmd = cmd
		log.Printf("[createShell] PowerShell 进程启动成功, PID=%d", cmd.Process.Pid)
		return nil
	}

	log.Printf("[createShell] 未找到可用 Shell，将使用命令模式")
	s.shellType = ShellTypeCmd
	return nil
}

func (s *PersistentShell) initializeShell() error {
	if s.cmd == nil {
		return nil
	}

	log.Printf("[initializeShell] 初始化 Shell 环境")

	s.mu.Lock()
	defer s.mu.Unlock()

	var initCommands []string

	switch s.shellType {
	case ShellTypeGitBash:
		initCommands = []string{
			"export PS1=''",
			"export PS2=''",
			"export PS4=''",
			"export PROMPT_COMMAND=''",
			"set +o history",
			"stty -echo 2>/dev/null || true",
			"exec 2>&1",
			"echo 'SHELL_READY'",
		}
	case ShellTypePowerShell:
		initCommands = []string{
			"function prompt { '' }",
			"$Host.UI.RawUI.BufferSize = New-Object System.Management.Automation.Host.Size(4096, 100)",
			"Write-Output 'SHELL_READY'",
		}
	}

	for i, cmd := range initCommands {
		log.Printf("[initializeShell] 发送初始化命令 %d: %s", i+1, cmd)
		if _, err := s.stdin.Write([]byte(cmd + "\n")); err != nil {
			return fmt.Errorf("写入初始化命令失败: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.reader = bufio.NewReader(s.stdout)

	// 等待初始化完成标记
	log.Printf("[initializeShell] 等待初始化完成标记...")
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			log.Printf("[initializeShell] 读取行失败: %v", err)
			if time.Now().After(deadline) {
				return fmt.Errorf("等待初始化超时")
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		line = strings.TrimSpace(line)
		log.Printf("[initializeShell] 接收到: %s", textutil.Truncate(line, 100))

		if strings.Contains(line, "SHELL_READY") {
			log.Printf("[initializeShell] Shell 初始化成功")
			time.Sleep(100 * time.Millisecond)
			return nil
		}
	}

	return fmt.Errorf("等待 SHELL_READY 标记超时")
}
