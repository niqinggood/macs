package processutil

import "strings"

// SanitizeShellLogName converts an external task identifier into a safe log
// file name component.
func SanitizeShellLogName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
}
