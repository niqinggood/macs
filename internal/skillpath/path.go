// Package skillpath resolves executable-relative Skill installation paths.
package skillpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ExecutableSkillsDir returns the user Skill directory beside the
// running executable:
//
//	<executable directory>/.openkitty/skills
//
// It intentionally does not depend on the process working directory, the
// configured workspace, or the current user's home directory.
func ExecutableSkillsDir() string {
	executable, err := os.Executable()
	if err == nil {
		if directory, resolveErr := ForExecutable(executable); resolveErr == nil {
			return directory
		}
	}
	workingDirectory, err := os.Getwd()
	if err != nil || strings.TrimSpace(workingDirectory) == "" {
		workingDirectory = "."
	}
	absolute, err := filepath.Abs(workingDirectory)
	if err == nil {
		workingDirectory = absolute
	}
	return filepath.Join(filepath.Clean(workingDirectory), ".openkitty", "skills")
}

func ForExecutable(executable string) (string, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return "", errors.New("executable path is empty")
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Clean(absolute)), ".openkitty", "skills"), nil
}

// SanitizeName converts a display name into the legacy path-safe Skill name.
func SanitizeName(name string) string {
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", ".", "-")
	value := replacer.Replace(strings.ToLower(name))
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}
