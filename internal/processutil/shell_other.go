//go:build !windows

package processutil

import (
	"log"
	"os/exec"
	"strings"
)

// PreferredShell returns the default interactive shell on non-Windows hosts.
func PreferredShell() string {
	return "bash"
}

// FindGitBash is empty on non-Windows hosts because the regular bash is used.
func FindGitBash() string {
	log.Printf("[FindGitBash] Unix 平台，不使用 Git Bash")
	return ""
}

// TestShellCreation checks whether bash can execute on a non-Windows host.
func TestShellCreation() error {
	log.Printf("[TestShellCreation] Unix 平台测试 Shell 创建")
	if _, err := exec.LookPath("bash"); err != nil {
		log.Printf("[TestShellCreation] bash 不可用: %v", err)
		return nil
	}
	log.Printf("[TestShellCreation] bash 可用")
	output, err := exec.Command("bash", "-c", "echo hello").Output()
	if err != nil {
		log.Printf("[TestShellCreation] bash 测试失败: %v", err)
		return nil
	}
	log.Printf("[TestShellCreation] bash 测试成功: %s", strings.TrimSpace(string(output)))
	return nil
}
