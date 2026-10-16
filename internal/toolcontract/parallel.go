package toolcontract

import (
	"encoding/json"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"mac/internal/shellpolicy"
)

// ====================== Tool Parallel Safety ======================
// 借鉴 Hermes tool_dispatch_helpers.py 三层分类 + 破坏性命令检测 + 路径重叠检测

// DestructiveCommandTool is an optional interface for tools that execute shell commands.
// The tool can report whether a specific command is destructive.
type DestructiveCommandTool interface {
	Tool
	IsDestructiveCommand(args map[string]interface{}) bool
}

// ToolLookup is the narrow registry surface needed for parallel-safety
// classification.
type ToolLookup interface {
	Get(string) (Tool, bool)
}

type ReadOnlyNameClassifier func(string) bool

// ====================== Destructive Command Detection ======================
// 借鉴 Hermes: _DESTRUCTIVE_PATTERNS + _REDIRECT_OVERWRITE

var destructivePatterns = regexp.MustCompile(
	`(?:^|\s|&&|\|\||;|` + "`" + `)(?:` +
		`rm\s+|rmdir\s+|` +
		`cp\s+|install\s+|` +
		`mv\s+|` +
		`sed\s+-i\s|` +
		`truncate\s+|` +
		`dd\s+|` +
		`shred\s+|` +
		`git\s+(?:reset|clean|checkout)\s|` +
		`chmod\s+|chown\s+|` +
		`mkfs\s+|mkswap\s+|` +
		`wget\s+.*\|\s*sh|` +
		`curl\s+.*\|\s*sh|` +
		`>(?:[^>]|$)|\d>\s*\S|` + // output redirect that overwrites (not >>)
		`)`,
)

var destructiveKeywords = []string{
	"format c:", "del /f", "del /q", "rd /s", "rd /q",
	"drop table", "drop database", "truncate table",
	"shutdown", "reboot", "halt",
}

// IsDestructiveCommand checks if a shell command looks destructive.
func IsDestructiveCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	if destructivePatterns.MatchString(cmd) {
		return true
	}
	lower := strings.ToLower(cmd)
	for _, kw := range destructiveKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ====================== Path Overlap Detection ======================
// 借鉴 Hermes: _paths_overlap() — two paths overlap if one is a prefix of the other

// PathsOverlap returns true if two paths may refer to the same file or subtree.
func PathsOverlap(a, b string) bool {
	left := normalizeOverlapPath(a)
	right := normalizeOverlapPath(b)
	if left == "" || right == "" {
		return left == right
	}

	leftParts := strings.Split(left, "/")
	rightParts := strings.Split(right, "/")

	minLen := len(leftParts)
	if len(rightParts) < minLen {
		minLen = len(rightParts)
	}

	for i := 0; i < minLen; i++ {
		if leftParts[i] != rightParts[i] {
			return false
		}
	}
	return true
}

func normalizeOverlapPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	clean := filepath.Clean(p)
	clean = strings.ReplaceAll(clean, "\\", "/")
	clean = pathpkg.Clean(clean)
	if shouldFoldPathCase(clean) {
		clean = strings.ToLower(clean)
	}
	return clean
}

func shouldFoldPathCase(p string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	if len(p) >= 2 && p[1] == ':' {
		return true
	}
	return strings.HasPrefix(p, "//")
}

// ====================== Enhanced Tool Partitioning ======================

// ExtractCLICommand tries to extract the shell command from CLI tool arguments.
func ExtractCLICommand(args map[string]interface{}) string {
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		return cmd
	}
	if cmd, ok := args["cmd"].(string); ok && cmd != "" {
		return cmd
	}
	if cmd, ok := args["shell"].(string); ok && cmd != "" {
		return cmd
	}
	if cmd, ok := args["exec"].(string); ok && cmd != "" {
		return cmd
	}
	return ""
}

// ExtractPathFromArgs extracts the file path from tool arguments.
func ExtractPathFromArgs(toolName string, args map[string]interface{}) string {
	candidates := []string{"path", "file_path", "file", "target", "destination", "dest"}
	for _, key := range candidates {
		if p, ok := args[key].(string); ok && p != "" {
			return p
		}
	}
	return ""
}

// ParallelSafetyClassification classifies a tool call for parallel execution.
type ParallelSafetyClassification int

const (
	// SafeParallel: tool can run concurrently with any other safe tools
	SafeParallel ParallelSafetyClassification = iota
	// PathScopedParallel: can run concurrently only if paths don't overlap
	PathScopedParallel
	// MustSerial: must run alone (destructive, stateful, interactive)
	MustSerial
	// NeverSerial: explicitly marked as never-parallel
	NeverSerial
)

// ClassifyToolParallel determines the parallel safety classification for a tool call.
// 借鉴 Hermes: _should_parallelize_tool_batch
func ClassifyToolParallel(toolName string, args map[string]interface{}, registry ToolLookup, readOnlyByName ReadOnlyNameClassifier) ParallelSafetyClassification {
	if registry == nil {
		return MustSerial
	}

	tool, ok := registry.Get(toolName)
	if !ok {
		return MustSerial
	}

	// 1. Never-parallel check
	if np, ok := tool.(NeverParallelTool); ok && np.IsNeverParallel() {
		return NeverSerial
	}

	// 2. CLI safety: if this is a shell tool, check for destructive commands
	if cmd := ExtractCLICommand(args); cmd != "" {
		cmdClass := shellpolicy.Classify(cmd)
		if cmdClass == shellpolicy.ClassDestructive {
			return MustSerial
		}
		if cmdClass == shellpolicy.ClassWrite || cmdClass == shellpolicy.ClassLongRunning {
			return MustSerial
		}
		// Shell tools share terminal/process state, so they remain serial even when read-only.
		if toolName == "cli" || toolName == "persistent_shell" {
			return MustSerial
		}
	}

	// 3. Read-only tools are safe even when they target the same path. Dispatch
	// tools get the effective arguments first; a false call-specific result must
	// not fall back to the legacy name allowlist.
	if ro, ok := tool.(CallReadOnlyTool); ok {
		if ro.IsReadOnlyForCall(args) {
			return SafeParallel
		}
	} else {
		if ro, ok := tool.(ReadOnlyTool); ok && ro.IsReadOnly() {
			return SafeParallel
		}
		if readOnlyByName != nil && readOnlyByName(toolName) {
			return SafeParallel
		}
	}

	// 3. Path-scoped tools: can be parallel if paths don't overlap
	if ps, ok := tool.(PathScopedTool); ok {
		if ps.ScopePath(args) != "" {
			return PathScopedParallel
		}
	}
	// Also detect path-scoped by argument heuristics for tools that don't implement the interface
	if ExtractPathFromArgs(toolName, args) != "" {
		switch toolName {
		case "write_file", "edit_file", "json_patch", "read_file", "patch", "code_check":
			return PathScopedParallel
		}
	}

	// 5. Unknown — assume unsafe
	return MustSerial
}

// CanRunConcurrently checks if two tool calls can run concurrently.
func CanRunConcurrently(a Call, b Call, registry ToolLookup, readOnlyByName ReadOnlyNameClassifier) bool {
	classA := ClassifyToolParallel(a.Name, a.Arguments, registry, readOnlyByName)
	classB := ClassifyToolParallel(b.Name, b.Arguments, registry, readOnlyByName)

	// Neither must be MustSerial or NeverSerial
	if classA >= MustSerial || classB >= MustSerial {
		return false
	}

	// If both are SafeParallel, they can run together
	if classA == SafeParallel && classB == SafeParallel {
		return true
	}

	// If both are PathScopedParallel, check path overlap
	if classA == PathScopedParallel && classB == PathScopedParallel {
		pathA := ResolveScopePath(a.Name, a.Arguments, registry)
		pathB := ResolveScopePath(b.Name, b.Arguments, registry)
		if pathA != "" && pathB != "" && !PathsOverlap(pathA, pathB) {
			return true
		}
		return false
	}

	// SafeParallel + PathScopedParallel = OK
	if (classA == SafeParallel && classB == PathScopedParallel) ||
		(classA == PathScopedParallel && classB == SafeParallel) {
		return true
	}

	return false
}

// ResolveScopePath resolves the absolute path from tool arguments.
func ResolveScopePath(toolName string, args map[string]interface{}, registry ToolLookup) string {
	if registry != nil {
		if tool, ok := registry.Get(toolName); ok {
			if ps, ok := tool.(PathScopedTool); ok {
				if path := ps.ScopePath(args); path != "" {
					return path
				}
			}
		}
	}
	return ExtractPathFromArgs(toolName, args)
}

// SerializeToolCallForSignature serializes a tool call args to compact JSON for logging.
func SerializeToolCallForSignature(tc Call) string {
	b, _ := json.Marshal(tc.Arguments)
	return tc.Name + ":" + string(b)
}
