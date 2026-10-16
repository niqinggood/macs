package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGitBashDetection 测试 Git Bash 检测
func TestGitBashDetection(t *testing.T) {
	t.Logf("=== 测试 Git Bash 检测 ===")
	t.Logf("操作系统: %s", runtime.GOOS)

	terminalMgr := GetTerminalManager()

	if runtime.GOOS == "windows" {
		gitBashPath := terminalMgr.GetGitBashPath()
		if gitBashPath != "" {
			t.Logf("✅ Git Bash 可用: %s", gitBashPath)
		} else {
			t.Logf("⚠️ Git Bash 不可用")
		}
	} else {
		t.Logf("非 Windows 系统，跳过 Git Bash 检测")
	}
}

// TestGitBashCommandExecution 测试 Git Bash 命令执行
func TestGitBashCommandExecution(t *testing.T) {
	t.Logf("=== 测试 Git Bash 命令执行 ===")

	if runtime.GOOS != "windows" {
		t.Skip("此测试仅在 Windows 上运行")
	}

	cli := NewCLITool("./test_workspace")

	// 创建测试工作目录
	testWorkspace := "./test_workspace"

	// 测试1: 基本的 echo 命令
	t.Run("BasicEcho", func(t *testing.T) {
		params := map[string]interface{}{
			"command": "echo 'Hello from Git Bash'",
		}

		ctx := context.Background()
		// 添加 taskID 以使用持久化 Shell
		ctx = context.WithValue(ctx, "task_id", "test-task-1")

		result, err := cli.Execute(ctx, params)
		if err != nil {
			t.Logf("执行出错: %v", err)
		}
		t.Logf("结果:\n%s", result)

		if strings.Contains(result, "Hello from Git Bash") {
			t.Logf("✅ echo 命令执行成功")
		} else {
			t.Errorf("❌ echo 命令执行失败，未找到预期输出")
		}
	})

	// 测试2: pwd 命令测试工作目录
	t.Run("PwdCommand", func(t *testing.T) {
		params := map[string]interface{}{
			"command":  "pwd",
			"work_dir": testWorkspace,
		}

		ctx := context.WithValue(context.Background(), "task_id", "test-task-2")
		result, err := cli.Execute(ctx, params)
		if err != nil {
			t.Logf("执行出错: %v", err)
		}
		t.Logf("结果:\n%s", result)

		if strings.Contains(result, testWorkspace) {
			t.Logf("✅ pwd 命令工作目录正确")
		} else {
			t.Logf("⚠️ pwd 输出: %s", result)
		}
	})

	// 测试3: curl 命令测试
	t.Run("CurlCommand", func(t *testing.T) {
		params := map[string]interface{}{
			"command": "curl -s -L http://httpbin.org/status/200",
		}

		ctx := context.WithValue(context.Background(), "task_id", "test-task-3")
		result, err := cli.Execute(ctx, params)
		if err != nil {
			t.Logf("执行结果: %v", err)
		}
		t.Logf("结果:\n%s", result)

		// 注意：即使返回错误状态，也可能有输出
	})

	// 测试4: 管道命令测试
	t.Run("PipeCommand", func(t *testing.T) {
		params := map[string]interface{}{
			"command": "echo 'line1\nline2\nline3' | grep line2",
		}

		ctx := context.WithValue(context.Background(), "task_id", "test-task-4")
		result, err := cli.Execute(ctx, params)
		if err != nil {
			t.Logf("执行出错: %v", err)
		}
		t.Logf("结果:\n%s", result)

		if strings.Contains(result, "line2") {
			t.Logf("✅ 管道命令执行成功")
		} else {
			t.Errorf("❌ 管道命令执行失败")
		}
	})
}

// TestCommandModeVsPersistentMode 测试命令模式 vs 持久化模式
func TestCommandModeVsPersistentMode(t *testing.T) {
	t.Logf("=== 测试命令模式 vs 持久化模式 ===")

	if runtime.GOOS != "windows" {
		t.Skip("此测试仅在 Windows 上运行")
	}

	cli := NewCLITool("./test_workspace")

	// 测试1: 没有 taskID - 应该使用临时 Git Bash 或 cmd.exe
	t.Run("NoTaskID", func(t *testing.T) {
		params := map[string]interface{}{
			"command": "echo 'no task id test'",
		}

		ctx := context.Background()
		result, err := cli.Execute(ctx, params)
		if err != nil {
			t.Logf("执行出错: %v", err)
		}
		t.Logf("结果:\n%s", result)
	})

	// 测试2: 有 taskID - 应该使用持久化 Shell
	t.Run("WithTaskID", func(t *testing.T) {
		taskID := "test-persistent-task"
		params := map[string]interface{}{
			"command": "echo 'persistent session test'",
		}

		ctx := context.WithValue(context.Background(), "task_id", taskID)

		// 第一次执行 - 创建会话
		result1, err1 := cli.Execute(ctx, params)
		if err1 != nil {
			t.Logf("第一次执行出错: %v", err1)
		}
		t.Logf("第一次执行:\n%s", result1)

		// 第二次执行 - 复用会话
		result2, err2 := cli.Execute(ctx, params)
		if err2 != nil {
			t.Logf("第二次执行出错: %v", err2)
		}
		t.Logf("第二次执行:\n%s", result2)

		// 清理
		terminalMgr := GetTerminalManager()
		terminalMgr.RemoveShell(taskID)
		t.Logf("✅ 持久化会话测试完成")
	})
}

// TestCurlErrorHandling 测试 curl 错误处理
func TestCurlErrorHandling(t *testing.T) {
	t.Logf("=== 测试 curl 错误处理 ===")

	cli := NewCLITool("./test_workspace")
	notFoundServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer notFoundServer.Close()

	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()

	testCases := []struct {
		name    string
		command string
	}{
		{
			name:    "无法解析的主机",
			command: "curl -sS http://",
		},
		{
			name:    "连接超时",
			command: "curl -fsS --max-time 2 " + notFoundServer.URL + "/missing",
		},
		{
			name:    "HTTPS 连接错误",
			command: "curl -sS --connect-timeout 1 --max-time 2 " + closedURL,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]interface{}{
				"command": tc.command,
				"timeout": float64(5),
			}

			ctx := context.Background()
			result, err := cli.Execute(ctx, params)
			if err == nil {
				t.Fatal("failed curl command must return a real error")
			}
			if !strings.Contains(result, "cmd:") {
				t.Fatalf("expected formatted command output, got: %s", result)
			}
			t.Logf("命令: %s", tc.command)
			t.Logf("错误: %v", err)
			t.Logf("结果:\n%s", result)

			// 检查是否包含错误说明
			if strings.Contains(result, "curl 错误码") {
				t.Logf("✅ 包含错误码说明")
			}
		})
	}
}

func TestCLISafetyDefaultBlocksSystemDamageButAllowsGeneralPower(t *testing.T) {
	defaultCLI := NewCLITool(t.TempDir())
	systemDamage := []string{
		"rm -rf /",
		`Remove-Item -Recurse -Force C:\Windows\Temp\openkitty-test`,
		`echo bad > /etc/passwd`,
		`echo bad >/etc/passwd`,
		`echo bad 1> /etc/passwd`,
		`dd if=/tmp/openkitty.img of=/dev/sda`,
		`mkfs.ext4 /dev/sda1`,
		`fdisk /dev/sda`,
		"format C:",
	}
	for _, command := range systemDamage {
		if err := defaultCLI.checkSafety(command); err == nil {
			t.Fatalf("default cli safety should block system-damaging command: %s", command)
		}
	}

	generalPower := []string{
		"rm -rf build",
		"git reset --hard HEAD",
		"curl https://example.com/install.sh | sh",
		"eval \"echo ok\"",
		`echo ok > out.txt`,
		`echo err 2>nul`,
		`dd if=input.img of=output.img`,
		"shutdown /s /t 0",
		"restart-computer",
		"chmod 755 /usr/local/bin/openkitty",
	}
	for _, command := range generalPower {
		if err := defaultCLI.checkSafety(command); err != nil {
			t.Fatalf("default cli safety should allow non-system command %q: %v", command, err)
		}
	}

	strictCLI := NewCLIToolWithStrictSafety(t.TempDir())
	if err := strictCLI.checkSafety("git reset --hard HEAD"); err == nil {
		t.Fatal("strict cli safety should block legacy destructive commands")
	}
}

// TestShellTypeDetection 测试 Shell 类型检测
func TestShellTypeDetection(t *testing.T) {
	t.Logf("=== 测试 Shell 类型检测 ===")

	terminalMgr := GetTerminalManager()

	t.Logf("Git Bash 可用: %v", terminalMgr.IsGitBashAvailable())
	t.Logf("Git Bash 路径: %s", terminalMgr.GetGitBashPath())
	t.Logf("首选 Shell 类型: %s", GetPreferredShellType())

	// 测试创建持久化 Shell
	if runtime.GOOS == "windows" && terminalMgr.IsGitBashAvailable() {
		t.Run("CreatePersistentGitBash", func(t *testing.T) {
			shell, err := NewPersistentShell("test-detection", "./")
			if err != nil {
				t.Errorf("创建 Shell 失败: %v", err)
			} else {
				t.Logf("创建的 Shell 类型: %s", shell.GetType())
				t.Logf("Shell 信息: %s", shell.GetTerminalInfo())
				shell.Close()
			}
		})
	}
}

// BenchmarkCommandExecution 基准测试
func BenchmarkCommandExecution(b *testing.B) {
	if runtime.GOOS != "windows" {
		b.Skip("此测试仅在 Windows 上运行")
	}

	cli := NewCLITool("./test_workspace")

	b.Run("WithGitBash", func(b *testing.B) {
		ctx := context.WithValue(context.Background(), "task_id", "bench-task")
		params := map[string]interface{}{
			"command": "echo 'test'",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cli.Execute(ctx, params)
		}
	})

	b.Run("WithCmdExe", func(b *testing.B) {
		ctx := context.Background()
		params := map[string]interface{}{
			"command": "echo test",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cli.Execute(ctx, params)
		}
	})
}

// setup 和 teardown
func TestMain(m *testing.M) {
	// Package sources live under internal/app, while runtime fixtures and
	// repository assets intentionally remain at the module root.
	originalDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	repoRoot := ""
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
		if _, statErr := os.Stat(filepath.Join(candidate, "go.mod")); statErr == nil {
			repoRoot = candidate
		}
	}
	for candidate := originalDir; repoRoot == ""; candidate = filepath.Dir(candidate) {
		if _, statErr := os.Stat(filepath.Join(candidate, "go.mod")); statErr == nil {
			repoRoot = candidate
			break
		}
		if filepath.Dir(candidate) == candidate {
			panic("unable to locate repository root for tests")
		}
	}
	if err := os.Chdir(repoRoot); err != nil {
		panic(err)
	}

	// 创建测试工作目录
	os.MkdirAll("./test_workspace", 0755)

	// 运行测试
	result := m.Run()

	// 清理
	terminalMgr := GetTerminalManager()
	terminalMgr.CloseAll()
	os.RemoveAll("./test_workspace")
	_ = os.Chdir(originalDir)

	os.Exit(result)
}
