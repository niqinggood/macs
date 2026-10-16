package skills

import "strings"

// RuntimeDirectoryFromActivation extracts the materialized runtime directory
// reported by ExecuteSkill.
func RuntimeDirectoryFromActivation(result string) string {
	for _, raw := range strings.Split(strings.ReplaceAll(result, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		for _, prefix := range []string{"运行时目录:", "运行时目录：", "runtime directory:", "runtime directory："} {
			if strings.HasPrefix(lower, strings.ToLower(prefix)) {
				return strings.Trim(strings.TrimSpace(line[len(prefix):]), "`\"'")
			}
		}
	}
	return ""
}
