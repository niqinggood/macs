package hooks

import (
	"path/filepath"
	"strings"
)

func matchGlob(pattern, value string) bool {
	if pattern == "*" || pattern == value {
		return true
	}
	if matched, err := filepath.Match(pattern, value); err == nil && matched {
		return true
	}
	if !strings.Contains(pattern, "**") {
		return false
	}
	parts := strings.Split(pattern, "**")
	if len(parts) == 2 {
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")
		switch {
		case prefix == "" && suffix == "":
			return true
		case prefix == "":
			return strings.HasSuffix(value, suffix) || strings.Contains(value, "/"+suffix)
		case suffix == "":
			return strings.HasPrefix(value, prefix)
		default:
			return strings.HasPrefix(value, prefix) && strings.HasSuffix(value, suffix)
		}
	}

	position := 0
	for index, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		if index == 0 {
			if !strings.HasPrefix(value, part) {
				return false
			}
			position = len(part)
			continue
		}
		if index == len(parts)-1 {
			found := strings.LastIndex(value, part)
			return found >= position && strings.HasSuffix(value[found:], part)
		}
		found := strings.Index(value[position:], part)
		if found < 0 {
			return false
		}
		position += found + len(part)
	}
	return position <= len(value)
}
