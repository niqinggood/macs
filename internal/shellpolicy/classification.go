package shellpolicy

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type CommandClass string

const (
	ClassReadOnly    CommandClass = "read_only"
	ClassBuildTest   CommandClass = "build_test"
	ClassWrite       CommandClass = "write"
	ClassLongRunning CommandClass = "long_running"
	ClassDestructive CommandClass = "destructive"
	ClassUnknown     CommandClass = "unknown"
)

type CommandAnalysis struct {
	Command       string       `json:"command"`
	Normalized    string       `json:"normalized"`
	Class         CommandClass `json:"class"`
	Blocked       bool         `json:"blocked"`
	BlockReason   string       `json:"block_reason,omitempty"`
	SuggestedRule string       `json:"suggested_rule,omitempty"`
}

func Classify(cmd string) CommandClass {
	return Analyze(cmd).Class
}

func Analyze(cmd string) CommandAnalysis {
	normalized := normalizeShellCommand(cmd)
	lower := strings.ToLower(normalized)
	analysis := CommandAnalysis{
		Command:       cmd,
		Normalized:    normalized,
		Class:         ClassUnknown,
		SuggestedRule: suggestShellPermissionRule(normalized),
	}
	if lower == "" {
		return analysis
	}

	if reason := blockedShellCommandReason(normalized, lower); reason != "" {
		analysis.Class = ClassDestructive
		analysis.Blocked = true
		analysis.BlockReason = reason
		return analysis
	}
	if containsAnyShellToken(lower, []string{
		"git reset --hard", "git clean -f", "git push --force",
	}) || startsDestructiveShellCommand(lower) {
		analysis.Class = ClassDestructive
		return analysis
	}
	if isPowerShellRemoveRecurse(lower) {
		analysis.Class = ClassDestructive
		return analysis
	}
	if looksLongRunningCommand(lower) {
		analysis.Class = ClassLongRunning
		return analysis
	}
	if containsAnyShellToken(lower, []string{
		"go test", "go build", "go vet", "go run",
		"npm test", "npm run build", "pnpm test", "pnpm build", "yarn test", "yarn build",
		"pytest", "python -m pytest", "cargo test", "cargo build", "mvn test", "gradle test",
	}) {
		analysis.Class = ClassBuildTest
		return analysis
	}
	if looksWriteCommand(lower) {
		analysis.Class = ClassWrite
		return analysis
	}
	if looksReadOnlyCommand(lower) || looksReadOnlyCommandChain(lower) {
		analysis.Class = ClassReadOnly
		return analysis
	}
	return analysis
}

// startsDestructiveShellCommand recognizes destructive executables only at a
// shell command boundary. A substring check is too broad here: ordinary flags
// such as "--format report", "--model x", and "--platform linux" end with
// "format ", "del ", and "rm " and used to be mislabeled as destructive.
func startsDestructiveShellCommand(cmd string) bool {
	destructive := []string{
		"rm", "del", "erase", "rmdir", "rd", "format", "format.com", "mkfs", "fdisk", "parted",
		"shutdown", "reboot", "remove-item", "ri", "rm-item", "clear-content",
		"stop-computer", "restart-computer", "remove-aduser", "remove-localuser",
		"remove-localgroup", "disable-localuser",
	}
	for _, name := range destructive {
		pattern := `(?i)(^|[;&|]\s*)` + regexp.QuoteMeta(name) + `(?:\s|$)`
		if regexp.MustCompile(pattern).MatchString(cmd) {
			return true
		}
	}
	return false
}

func normalizeShellCommand(cmd string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.TrimSpace(cmd) {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func blockedShellCommandReason(cmd, lower string) string {
	if deletesProtectedSystemPath(lower) {
		return "deleting a protected system path is not allowed"
	}
	return ""
}

func deletesProtectedSystemPath(lower string) bool {
	if !containsAnyShellToken(lower, []string{"rm ", "del ", "erase ", "rmdir ", "rd ", "remove-item", "ri "}) {
		return false
	}
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)rm\s+[^;&|]*(\s|^)/($|\s|[;&|])`).MatchString(lower) {
		return true
	}
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)(rd|rmdir)\s+[^;&|]*(\s|^)(c:\\|/)$`).MatchString(lower) {
		return true
	}
	protected := []string{
		"/etc/", "/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/", "/boot/", "/root/", "/dev/", "/sys/", "/proc/",
		`c:\windows`, `c:\program files`, `c:\program files (x86)`, `c:\boot`, `c:\efi`,
	}
	for _, token := range protected {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func containsPipelineToInterpreter(lower string) bool {
	if !strings.Contains(lower, "|") {
		return false
	}
	if !(strings.Contains(lower, "curl") || strings.Contains(lower, "wget") || strings.Contains(lower, "invoke-webrequest") || strings.Contains(lower, "iwr ")) {
		return false
	}
	return regexp.MustCompile(`(?i)\|\s*(sh|bash|zsh|fish|python|python3|perl|ruby|node|powershell|pwsh)\b`).MatchString(lower)
}

func isRootOrWorkspaceDeletion(lower string) bool {
	patterns := []string{
		`(?i)(^|[;&|]\s*)rm\s+[^;&|]*-[^-;&|]*r[^;&|]*\s+(/|\./?|\.\./?|~|\*)($|\s|[;&|])`,
		`(?i)(^|[;&|]\s*)rm\s+[^;&|]*-[^-;&|]*r[^;&|]*\s+(\./\*|\.\./\*)($|\s|[;&|])`,
		`(?i)(^|[;&|]\s*)(rd|rmdir)\s+(/s|-r|/q|/s\s+/q)[^;&|]*(c:\\|c:|\\|\.)(\s|$)`,
		`(?i)\bremove-item\b[^;&|]*(-recurse|-r)\b[^;&|]*(\s\.($|\s)|\s\*($|\s)|c:\\|c:)`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(lower) {
			return true
		}
	}
	return false
}

func redirectsToProtectedPath(lower string) bool {
	if !strings.Contains(lower, ">") {
		return false
	}
	protected := []string{
		"> /etc/", "> /bin/", "> /sbin/", "> /usr/bin/", "> /usr/sbin/", "> /dev/",
		`> c:\windows`, `> c:\program files`, `> c:\boot`, `> c:\efi`,
	}
	for _, token := range protected {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func suggestShellPermissionRule(cmd string) string {
	words := shellWords(cmd)
	if len(words) == 0 {
		return ""
	}
	first := strings.ToLower(words[0])
	switch first {
	case "git":
		if len(words) >= 2 {
			return fmt.Sprintf("cli(%s %s)", words[0], words[1])
		}
	case "go":
		if len(words) >= 2 {
			return fmt.Sprintf("cli(%s %s)", words[0], words[1])
		}
	case "npm", "pnpm", "yarn":
		if len(words) >= 3 && words[1] == "run" {
			return fmt.Sprintf("cli(%s run %s)", words[0], words[2])
		}
		if len(words) >= 2 {
			return fmt.Sprintf("cli(%s %s)", words[0], words[1])
		}
	case "python", "python3":
		if len(words) >= 3 && words[1] == "-m" {
			return fmt.Sprintf("cli(%s -m %s)", words[0], words[2])
		}
	}
	return fmt.Sprintf("cli(%s)", words[0])
}

func shellWords(cmd string) []string {
	var words []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range cmd {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

func looksReadOnlyCommand(cmd string) bool {
	first := firstShellWord(cmd)
	switch first {
	case "ls", "dir", "pwd", "cat", "type", "head", "tail", "more", "less", "grep", "rg", "findstr",
		"find", "wc", "sort", "uniq", "git", "go", "npm", "pnpm", "yarn", "pytest", "python", "node":
		if strings.HasPrefix(cmd, "git ") {
			return containsAnyShellToken(cmd, []string{"git status", "git diff", "git log", "git show", "git branch", "git rev-parse"})
		}
		if strings.HasPrefix(cmd, "go ") {
			return containsAnyShellToken(cmd, []string{"go env", "go version", "go list"})
		}
		if strings.HasPrefix(cmd, "node ") || strings.HasPrefix(cmd, "python ") {
			return strings.Contains(cmd, " --version") || strings.Contains(cmd, " -v")
		}
		return first != "go" && first != "npm" && first != "pnpm" && first != "yarn" && first != "pytest" && first != "python" && first != "node"
	default:
		return false
	}
}

func looksReadOnlyCommandChain(cmd string) bool {
	if !strings.ContainsAny(cmd, "|;&") {
		return false
	}
	segments := splitShellReadOnlySegments(cmd)
	if len(segments) < 2 {
		return false
	}
	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return false
		}
		if isReadOnlyDirectoryChange(segment) {
			continue
		}
		if i == 0 && isShellEnvAssignment(segment) {
			continue
		}
		if !looksReadOnlyCommand(segment) {
			return false
		}
	}
	return true
}

func splitShellReadOnlySegments(cmd string) []string {
	var segments []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case escaped:
			current.WriteByte(ch)
			escaped = false
			continue
		case ch == '\\':
			current.WriteByte(ch)
			escaped = true
			continue
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteByte(ch)
			continue
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteByte(ch)
			continue
		case !inSingle && !inDouble && ch == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			segments = append(segments, strings.TrimSpace(current.String()))
			current.Reset()
			i++
			continue
		case !inSingle && !inDouble && ch == '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				return nil
			}
			segments = append(segments, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		case !inSingle && !inDouble && ch == ';':
			segments = append(segments, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		segments = append(segments, strings.TrimSpace(current.String()))
	}
	return segments
}

func isReadOnlyDirectoryChange(cmd string) bool {
	words := shellWords(cmd)
	if len(words) < 1 || strings.ToLower(words[0]) != "cd" {
		return false
	}
	if len(words) > 2 {
		return false
	}
	if len(words) == 1 {
		return true
	}
	path := strings.TrimSpace(words[1])
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	return !strings.ContainsAny(path, ">|&;") &&
		!strings.Contains(lower, "$(") &&
		!strings.Contains(lower, "`") &&
		!strings.Contains(lower, "<(") &&
		!strings.Contains(lower, ">(")
}

func isShellEnvAssignment(cmd string) bool {
	words := shellWords(cmd)
	return len(words) == 1 && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=.*$`).MatchString(words[0])
}

func looksWriteCommand(cmd string) bool {
	if hasMeaningfulOutputRedirection(cmd) {
		return true
	}
	return containsAnyShellToken(cmd, []string{
		"touch", "mkdir", "copy", "cp", "move", "mv", "ren", "rename",
		"new-item", "set-content", "add-content", "out-file", "copy-item", "move-item",
		"git add", "git commit", "git checkout", "git switch", "git merge", "git rebase",
		"go mod tidy", "npm install", "pnpm install", "yarn install", "pip install",
		"prettier --write", "black ", "gofmt -w",
	})
}

func hasMeaningfulOutputRedirection(cmd string) bool {
	if !strings.Contains(cmd, ">") {
		return false
	}
	stripped := stripHarmlessOutputRedirections(cmd)
	return strings.Contains(stripped, ">")
}

func stripHarmlessOutputRedirections(cmd string) string {
	patterns := []string{
		`(?i)\s+\d*>\s*(nul|/dev/null|\$null)\b`,
		`(?i)\s+\d*>>\s*(nul|/dev/null|\$null)\b`,
		`(?i)\s+\d*>\s*&\s*\d\b`,
		`(?i)\s+&>\s*(nul|/dev/null|\$null)\b`,
	}
	out := cmd
	for _, pattern := range patterns {
		out = regexp.MustCompile(pattern).ReplaceAllString(out, "")
	}
	return out
}

func looksLongRunningCommand(cmd string) bool {
	return strings.Contains(cmd, " npm run dev") || strings.HasPrefix(cmd, "npm run dev") ||
		strings.Contains(cmd, " pnpm dev") || strings.HasPrefix(cmd, "pnpm dev") ||
		strings.Contains(cmd, " yarn dev") || strings.HasPrefix(cmd, "yarn dev") ||
		strings.Contains(cmd, " vite ") || strings.HasPrefix(cmd, "vite") ||
		strings.Contains(cmd, " next dev") || strings.HasPrefix(cmd, "next dev") ||
		strings.Contains(cmd, "python -m http.server") || strings.Contains(cmd, "python3 -m http.server") ||
		strings.Contains(cmd, "py -m http.server") ||
		strings.Contains(cmd, " flask run") || strings.HasPrefix(cmd, "flask run") ||
		strings.Contains(cmd, " uvicorn ") || strings.HasPrefix(cmd, "uvicorn ") ||
		strings.Contains(cmd, " gunicorn ") || strings.HasPrefix(cmd, "gunicorn ") ||
		strings.Contains(cmd, " manage.py runserver") ||
		strings.Contains(cmd, " php -s ") || strings.HasPrefix(cmd, "php -s ") ||
		strings.Contains(cmd, " ruby -run -e httpd") || strings.HasPrefix(cmd, "ruby -run -e httpd") ||
		strings.Contains(cmd, " npx http-server") || strings.HasPrefix(cmd, "npx http-server") ||
		strings.Contains(cmd, " npx serve") || strings.HasPrefix(cmd, "npx serve") ||
		strings.Contains(cmd, " dotnet watch") || strings.HasPrefix(cmd, "dotnet watch") ||
		(strings.Contains(cmd, " go run") || strings.HasPrefix(cmd, "go run")) && (strings.Contains(cmd, "server") || strings.Contains(cmd, "serve")) ||
		strings.HasSuffix(cmd, " &")
}

func containsAnyShellToken(cmd string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(cmd, token) {
			return true
		}
	}
	return false
}

func firstShellWord(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "\"'")
}

func isPowerShellRemoveRecurse(cmd string) bool {
	if !strings.Contains(cmd, "remove-item") && !strings.HasPrefix(cmd, "ri ") && !strings.Contains(cmd, " ri ") {
		return false
	}
	return strings.Contains(cmd, "-recurse") || strings.Contains(cmd, " -r ") ||
		strings.Contains(cmd, "-force") || strings.Contains(cmd, " -fo ")
}
