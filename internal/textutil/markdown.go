package textutil

import "strings"

func IsMarkdownFenceBoundary(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}
