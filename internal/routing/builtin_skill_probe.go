package routing

import (
	"regexp"
	"strings"

	"mac/internal/portablepath"
)

var builtinSkillProbePattern = regexp.MustCompile(`(?i)(?:^|/)\.openkitty/skills/([^/]+)(?:/|$)`)

// BuiltinSkillProbeCandidate recognizes attempts to inspect an embedded
// builtin Skill through a guessed workspace/runtime path.
func BuiltinSkillProbeCandidate(toolName string, arguments map[string]interface{}) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name == "use_skill" || name == "list_skills" {
		return ""
	}
	if name == "list_dir" || name == "read_file" || name == "glob" {
		if probePath := toolCallPathArgument(arguments); probePath != "" {
			normalized := portablepath.Normalize(probePath)
			if match := builtinSkillProbePattern.FindStringSubmatch(normalized); len(match) == 2 {
				return match[1]
			}
		}
	}
	return runtimeBuiltinSkillMainDocumentName(arguments)
}

// runtimeBuiltinSkillMainDocumentName finds attempts to re-read the generated
// runtime copy of an embedded Skill's main document.
func runtimeBuiltinSkillMainDocumentName(arguments map[string]interface{}) string {
	var inspect func(interface{}) string
	inspect = func(value interface{}) string {
		switch typed := value.(type) {
		case string:
			normalized := strings.ReplaceAll(typed, "\\", "/")
			lower := strings.ToLower(normalized)
			const marker = "/runtime/builtin/"
			start := strings.Index(lower, marker)
			if start < 0 {
				return ""
			}
			rest := normalized[start+len(marker):]
			restLower := lower[start+len(marker):]
			end := strings.Index(restLower, "/skill.md")
			if end < 0 {
				return ""
			}
			parts := strings.Split(strings.Trim(rest[:end], "/ \t\r\n\"'`"), "/")
			if len(parts) == 0 {
				return ""
			}
			return strings.TrimSpace(parts[len(parts)-1])
		case map[string]interface{}:
			for _, child := range typed {
				if found := inspect(child); found != "" {
					return found
				}
			}
		case []interface{}:
			for _, child := range typed {
				if found := inspect(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return inspect(arguments)
}

func toolCallPathArgument(arguments map[string]interface{}) string {
	for _, key := range []string{"path", "file", "file_path", "root"} {
		if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
