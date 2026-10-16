//go:build windows

package processutil

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// PreferredShell returns the default interactive shell on Windows.
func PreferredShell() string {
	return "git-bash"
}

// FindGitBash locates a usable Git Bash executable on Windows.
func FindGitBash() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\GitForWindows`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		installPath, _, valueErr := k.GetStringValue("InstallPath")
		if valueErr == nil && installPath != "" {
			bashPath := filepath.Join(installPath, "bin", "bash.exe")
			if _, statErr := os.Stat(bashPath); statErr == nil {
				log.Printf("[FindGitBash] 从注册表 (HKLM) 找到 Git Bash: %s", bashPath)
				return bashPath
			}
		}
	}

	k, err = registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\GitForWindows`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		installPath, _, valueErr := k.GetStringValue("InstallPath")
		if valueErr == nil && installPath != "" {
			bashPath := filepath.Join(installPath, "bin", "bash.exe")
			if _, statErr := os.Stat(bashPath); statErr == nil {
				log.Printf("[FindGitBash] 从注册表 (HKCU) 找到 Git Bash: %s", bashPath)
				return bashPath
			}
		}
	}

	k, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		installPath, _, valueErr := k.GetStringValue("InstallLocation")
		if valueErr == nil && installPath != "" {
			bashPath := filepath.Join(installPath, "bin", "bash.exe")
			if _, statErr := os.Stat(bashPath); statErr == nil {
				log.Printf("[FindGitBash] 从注册表 (Uninstall) 找到 Git Bash: %s", bashPath)
				return bashPath
			}
		}
	}

	if path, lookErr := exec.LookPath("bash.exe"); lookErr == nil && strings.Contains(strings.ToLower(path), "git") {
		log.Printf("[FindGitBash] 从 PATH 找到 Git Bash: %s", path)
		return path
	}
	if path, lookErr := exec.LookPath("git-bash.exe"); lookErr == nil {
		log.Printf("[FindGitBash] 从 PATH 找到 git-bash.exe: %s", path)
		dir := filepath.Dir(path)
		for _, bashPath := range []string{
			filepath.Join(dir, "bash.exe"),
			filepath.Join(filepath.Dir(dir), "bin", "bash.exe"),
		} {
			if _, statErr := os.Stat(bashPath); statErr == nil {
				return bashPath
			}
		}
		return path
	}

	possiblePaths := []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\usr\bin\bash.exe`,
		`C:\Users\Administrator\AppData\Local\Programs\Git\bin\bash.exe`,
		`C:\Users\Administrator\AppData\Local\Programs\Git\usr\bin\bash.exe`,
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		possiblePaths = append(possiblePaths,
			filepath.Join(userProfile, "AppData", "Local", "Programs", "Git", "bin", "bash.exe"),
			filepath.Join(userProfile, "AppData", "Local", "Programs", "Git", "usr", "bin", "bash.exe"),
			filepath.Join(userProfile, "scoop", "apps", "git", "current", "usr", "bin", "bash.exe"),
		)
	}
	for _, path := range possiblePaths {
		if _, statErr := os.Stat(path); statErr == nil {
			log.Printf("[FindGitBash] 从常见路径找到 Git Bash: %s", path)
			return path
		}
	}

	if path := findWithWhere("bash", true); path != "" {
		return path
	}
	if path := findWithWhere("git-bash", false); path != "" {
		dir := filepath.Dir(path)
		bashPath := filepath.Join(dir, "bash.exe")
		if _, statErr := os.Stat(bashPath); statErr == nil {
			return bashPath
		}
		return path
	}
	if output, whereErr := exec.Command("where", "git").Output(); whereErr == nil {
		for _, path := range strings.Split(string(output), "\n") {
			path = strings.TrimSpace(path)
			if path == "" || !strings.Contains(strings.ToLower(path), "git") {
				continue
			}
			gitDir := filepath.Dir(path)
			parentDir := filepath.Dir(gitDir)
			for _, candidate := range []string{
				filepath.Join(parentDir, "usr", "bin", "bash.exe"),
				filepath.Join(parentDir, "bin", "bash.exe"),
				filepath.Join(gitDir, "..", "usr", "bin", "bash.exe"),
				filepath.Join(gitDir, "..", "bin", "bash.exe"),
			} {
				if _, statErr := os.Stat(candidate); statErr == nil {
					log.Printf("[FindGitBash] 通过 git.exe 位置推断出 Git Bash: %s", candidate)
					return candidate
				}
			}
			break
		}
	}

	log.Printf("[FindGitBash] 未找到 Git Bash")
	return ""
}

func findWithWhere(name string, requireGitPath bool) string {
	output, err := exec.Command("where", name).Output()
	if err != nil {
		return ""
	}
	for _, path := range strings.Split(string(output), "\n") {
		path = strings.TrimSpace(path)
		if path == "" || (requireGitPath && !strings.Contains(strings.ToLower(path), "git")) {
			continue
		}
		log.Printf("[FindGitBash] 从 where 找到 %s: %s", name, path)
		return path
	}
	return ""
}

// TestShellCreation checks whether the preferred Windows shell can execute.
func TestShellCreation() error {
	log.Printf("[TestShellCreation] Windows 平台测试 Shell 创建")
	gitBashPath := FindGitBash()
	if gitBashPath == "" {
		log.Printf("[TestShellCreation] Git Bash 不可用")
		return nil
	}
	log.Printf("[TestShellCreation] Git Bash 可用: %s", gitBashPath)
	output, err := exec.Command(gitBashPath, "-c", "echo hello").Output()
	if err != nil {
		log.Printf("[TestShellCreation] Git Bash 测试失败: %v", err)
		return nil
	}
	log.Printf("[TestShellCreation] Git Bash 测试成功: %s", strings.TrimSpace(string(output)))
	return nil
}
