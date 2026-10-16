// Package portablepath performs lexical path operations without assuming that
// the path syntax matches the host operating system.
package portablepath

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var windowsAbsolutePattern = regexp.MustCompile(`(?i)^[a-z]:/`)

// Normalize cleans Windows, Unix, and UNC paths into slash-separated form.
func Normalize(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, `\`, "/")
	wasUNC := strings.HasPrefix(value, "//")
	cleaned := path.Clean(value)
	if wasUNC && strings.HasPrefix(cleaned, "/") && !strings.HasPrefix(cleaned, "//") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// IsAbs reports whether value is an absolute Unix, Windows, or UNC path.
func IsAbs(value string) bool {
	value = Normalize(value)
	return strings.HasPrefix(value, "/") || windowsAbsolutePattern.MatchString(value)
}

// RelativeTo returns target as a lexical child path of base. Windows drive and
// UNC paths compare case-insensitively; Unix paths remain case-sensitive.
func RelativeTo(base, target string) (string, bool) {
	base = Normalize(base)
	target = Normalize(target)
	if base == "" || target == "" {
		return "", false
	}
	baseTrimmed := strings.TrimSuffix(base, "/")
	baseKey := baseTrimmed
	targetKey := target
	windowsStyle := windowsAbsolutePattern.MatchString(base) || strings.HasPrefix(base, "//")
	if windowsStyle {
		baseKey = strings.ToLower(baseKey)
		targetKey = strings.ToLower(targetKey)
	}
	if targetKey == baseKey {
		return ".", true
	}
	prefix := baseKey + "/"
	if !strings.HasPrefix(targetKey, prefix) {
		return "", false
	}
	relative := strings.TrimPrefix(target[len(baseTrimmed):], "/")
	relative = path.Clean(relative)
	if relative == "" || relative == "." {
		return ".", true
	}
	if relative == ".." || strings.HasPrefix(relative, "../") {
		return "", false
	}
	return relative, true
}

// SafeJoin resolves a relative child beneath base while preserving an
// explicitly supplied absolute path. Lexically escaping relative paths are
// rejected.
func SafeJoin(base, value string) (string, error) {
	value = strings.TrimSpace(value)
	if IsAbs(value) {
		if filepath.IsAbs(value) {
			return filepath.Abs(filepath.Clean(value))
		}
		return value, nil
	}
	value = filepath.Clean(value)
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(filepath.Join(baseAbs, value))
	relative, err := filepath.Rel(baseAbs, cleanPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", value)
	}
	return cleanPath, nil
}
