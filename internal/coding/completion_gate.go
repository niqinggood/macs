package coding

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mac/internal/toolcontract"
)

type VerificationDescriptor struct {
	Family string
	Root   string
	Target string
	Masked bool
}

var (
	verificationCDPattern       = regexp.MustCompile(`(?i)(?:^|[;&]\s*)cd(?:\s+/d)?\s+(?:"([^"]+)"|'([^']+)'|([^&;\r\n]+?))\s*(?:&&|;)`)
	verificationUnittestPattern = regexp.MustCompile(`(?i)\b(?:python|py)(?:\.exe)?\b[^\r\n|;&]*?\s-m\s+unittest\b`)
	verificationPytestPattern   = regexp.MustCompile(`(?i)(?:^|\s)(?:python(?:\.exe)?\s+-m\s+)?pytest\b`)
	verificationGoTestPattern   = regexp.MustCompile(`(?i)(?:^|\s)go\s+test\b`)
	verificationJSTestPattern   = regexp.MustCompile(`(?i)(?:^|\s)(?:npm|pnpm|yarn)\s+(?:run\s+)?test\b`)
	verificationCargoPattern    = regexp.MustCompile(`(?i)(?:^|\s)cargo\s+test\b`)
	verificationDotnetPattern   = regexp.MustCompile(`(?i)(?:^|\s)dotnet\s+test\b`)
	verificationMavenPattern    = regexp.MustCompile(`(?i)(?:^|\s)(?:mvn|mvnw|gradle|gradlew)(?:\.cmd|\.bat)?\b[^\r\n|;&]*?\btest\b`)
	verificationScriptPattern   = regexp.MustCompile(`(?i)\b(?:python|py)(?:\.exe)?\b[^\r\n|;&]*?\s+["']?([^\s"']*test[^\s"']*\.py)\b`)
	verificationMaskedPattern   = regexp.MustCompile(`(?i)(?:\|\s*(?:head|tail|grep|findstr|select-string)\b|\|\|\s*(?:true|exit\s+0)\b|;\s*exit\s+0\b)`)
	verificationZeroPattern     = regexp.MustCompile(`(?i)(?:ran\s+0\s+tests?|collected\s+0\s+items?|no\s+tests?\s+(?:ran|found|collected)|0\s+tests?\s+passed|no\s+test\s+files?\s+found)`)
)

func DescriptorForToolCall(call toolcontract.Call) VerificationDescriptor {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "run_tests" || name == "test_runner" {
		if command := firstStringArg(call.Arguments, "command", "cmd"); strings.TrimSpace(command) != "" {
			return verificationDescriptorForCommand(call, command)
		}
		family := strings.ToLower(strings.TrimSpace(firstStringArg(call.Arguments, "framework", "runner")))
		if family == "" {
			family = name
		}
		target := strings.TrimSpace(firstStringArg(call.Arguments, "path", "package", "test", "target"))
		if target == "" {
			target = "*"
		}
		return VerificationDescriptor{Family: family, Root: verificationRoot(call, ""), Target: normalizeVerificationTarget(target)}
	}
	if !containsAny(name, "cli", "persistent_shell", "shell_analyze", "code_check") {
		return VerificationDescriptor{}
	}
	command := firstStringArg(call.Arguments, "command", "cmd", "script", "args", "arguments")
	if strings.TrimSpace(command) == "" {
		return VerificationDescriptor{}
	}
	descriptor := VerificationDescriptor{Root: verificationRoot(call, command), Masked: verificationMaskedPattern.MatchString(command)}
	setFromPattern := func(pattern *regexp.Regexp, family string) bool {
		match := pattern.FindStringIndex(command)
		if match == nil {
			return false
		}
		descriptor.Family = family
		descriptor.Target = verificationTargetFromTail(command[match[1]:], family)
		return true
	}
	switch {
	case setFromPattern(verificationUnittestPattern, "python-unittest"):
	case setFromPattern(verificationPytestPattern, "pytest"):
	case setFromPattern(verificationGoTestPattern, "go-test"):
	case setFromPattern(verificationJSTestPattern, "javascript-test"):
	case setFromPattern(verificationCargoPattern, "cargo-test"):
	case setFromPattern(verificationDotnetPattern, "dotnet-test"):
	case setFromPattern(verificationMavenPattern, "jvm-test"):
	default:
		match := verificationScriptPattern.FindStringSubmatch(command)
		if len(match) != 2 {
			return VerificationDescriptor{}
		}
		descriptor.Family = "python-test-script"
		descriptor.Target = normalizeVerificationTarget(match[1])
	}
	if descriptor.Target == "" {
		descriptor.Target = "*"
	}
	return descriptor
}

func verificationDescriptorForCommand(call toolcontract.Call, command string) VerificationDescriptor {
	descriptor := VerificationDescriptor{Root: verificationRoot(call, command), Masked: verificationMaskedPattern.MatchString(command)}
	setFromPattern := func(pattern *regexp.Regexp, family string) bool {
		match := pattern.FindStringIndex(command)
		if match == nil {
			return false
		}
		descriptor.Family = family
		descriptor.Target = verificationTargetFromTail(command[match[1]:], family)
		return true
	}
	switch {
	case setFromPattern(verificationUnittestPattern, "python-unittest"):
	case setFromPattern(verificationPytestPattern, "pytest"):
	case setFromPattern(verificationGoTestPattern, "go-test"):
	case setFromPattern(verificationJSTestPattern, "javascript-test"):
	case setFromPattern(verificationCargoPattern, "cargo-test"):
	case setFromPattern(verificationDotnetPattern, "dotnet-test"):
	case setFromPattern(verificationMavenPattern, "jvm-test"):
	default:
		match := verificationScriptPattern.FindStringSubmatch(command)
		if len(match) != 2 {
			return VerificationDescriptor{}
		}
		descriptor.Family = "python-test-script"
		descriptor.Target = normalizeVerificationTarget(match[1])
	}
	if descriptor.Target == "" {
		descriptor.Target = "*"
	}
	return descriptor
}

func verificationRoot(call toolcontract.Call, command string) string {
	root := strings.TrimSpace(firstStringArg(call.Arguments, "workdir", "work_dir", "cwd", "dir"))
	if root == "" && command != "" {
		if match := verificationCDPattern.FindStringSubmatch(command); len(match) == 4 {
			for _, candidate := range match[1:] {
				if strings.TrimSpace(candidate) != "" {
					root = candidate
					break
				}
			}
		}
	}
	root = strings.Trim(strings.TrimSpace(root), `"'`)
	if root == "" {
		return "."
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
}

func verificationTargetFromTail(tail, family string) string {
	if index := strings.IndexAny(tail, "|;&\r\n"); index >= 0 {
		tail = tail[:index]
	}
	fields := strings.Fields(tail)
	var targets []string
	for index := 0; index < len(fields); index++ {
		field := strings.Trim(fields[index], `"'`)
		lower := strings.ToLower(field)
		if lower == "discover" || lower == "./..." || lower == "..." {
			return "*"
		}
		if strings.HasPrefix(lower, "-") || strings.Contains(lower, ">") || lower == "2" || lower == "1" {
			continue
		}
		if family == "javascript-test" || family == "cargo-test" || family == "dotnet-test" || family == "jvm-test" {
			// The ordinary command is project-wide. Filters are not allowed to
			// silently resolve a prior broader failure, so only an unfiltered call
			// receives the wildcard below.
			if lower == "--" {
				continue
			}
		}
		targets = append(targets, normalizeVerificationTarget(field))
	}
	if len(targets) == 0 {
		return "*"
	}
	sort.Strings(targets)
	return strings.Join(targets, ",")
}

func normalizeVerificationTarget(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	if value == "" {
		return ""
	}
	if value == "./..." || value == "..." {
		return "*"
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(value)))
}

// CommandMasksVerification applies even to custom acceptance scripts whose
// test framework is unknown. A recognized framework is not a safety boundary.
func CommandMasksVerification(command string) bool {
	return verificationMaskedPattern.MatchString(command)
}

func OutputIsEmpty(content string) bool {
	return verificationZeroPattern.MatchString(content)
}

func verificationResolutionRequired(input string) bool {
	lower := strings.ToLower(input)
	if containsAny(lower,
		"只诊断", "仅诊断", "不要修改", "不要改代码", "本次未改代码", "只解释", "只分析",
		"diagnose only", "do not modify", "do not change", "no code changes", "analysis only",
	) {
		return false
	}
	return containsAny(lower,
		"修复", "实现", "开发", "重构", "改代码", "修改代码", "补测试", "添加功能", "加入", "改造", "可运行的新版本", "新建",
		"fix", "implement", "develop", "refactor", "patch", "modify the code", "change the code", "add tests", "add a feature", "build a working",
	)
}

func UnresolvedIssues(input string, results []string) []string {
	if !verificationResolutionRequired(input) {
		return nil
	}
	pending := make(map[string]toolcontract.Record)
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || strings.TrimSpace(record.VerificationFamily) == "" {
			continue
		}
		key := verificationRecordKey(record)
		if record.VerificationBaseline {
			// A pre-edit baseline is evidence, not post-edit verification. Keep it
			// pending until the same or a broader scope is rerun normally.
			pending[key] = record
			continue
		}
		if record.VerificationBaselineOnly {
			// The command still exits non-zero, but the test runner proved that
			// every current failure was already present in the captured baseline.
			// It may close only an earlier baseline/failure record in this trace.
			if record.VerificationMasked || record.VerificationEmpty {
				continue
			}
			for pendingKey, failed := range pending {
				if verificationSuccessCovers(record, failed) {
					delete(pending, pendingKey)
				}
			}
			continue
		}
		if !record.Succeeded {
			pending[key] = record
			continue
		}
		if record.VerificationMasked || record.VerificationEmpty {
			continue
		}
		for pendingKey, failed := range pending {
			if verificationSuccessCovers(record, failed) {
				delete(pending, pendingKey)
			}
		}
	}
	issues := make([]string, 0, len(pending))
	for _, record := range pending {
		root := record.VerificationRoot
		if root == "" {
			root = "."
		}
		target := record.VerificationTarget
		if target == "" {
			target = "*"
		}
		issues = append(issues, fmt.Sprintf("%s target=%s root=%s", record.VerificationFamily, target, root))
	}
	sort.Strings(issues)
	return issues
}

func verificationRecordKey(record toolcontract.Record) string {
	return strings.ToLower(strings.Join([]string{record.VerificationFamily, record.VerificationRoot, record.VerificationTarget}, "|"))
}

func verificationSuccessCovers(success, failure toolcontract.Record) bool {
	if !strings.EqualFold(success.VerificationFamily, failure.VerificationFamily) {
		return false
	}
	if success.VerificationRoot != "" && failure.VerificationRoot != "" && !strings.EqualFold(success.VerificationRoot, failure.VerificationRoot) {
		return false
	}
	successTarget := strings.TrimSpace(success.VerificationTarget)
	failureTarget := strings.TrimSpace(failure.VerificationTarget)
	return successTarget == "*" || strings.EqualFold(successTarget, failureTarget)
}

func BuildRepairPrompt(issues []string) map[string]interface{} {
	return map[string]interface{}{
		"role": "system",
		"content": `[UNRESOLVED VERIFICATION FAILURE]
The task changes code, but a failed test verification is still unresolved:
- ` + strings.Join(issues, "\n- ") + `

Do not claim completion yet. Read the actual failure, make one focused correction, and rerun the same test target or a demonstrably broader suite. A passing different subset, a piped/head/tail command, or a zero-test run does not resolve this failure. If it is truly pre-existing and out of scope, provide concrete baseline evidence and return an explicitly partial/blocked result instead of saying all tests passed.`,
	}
}

func AppendFailure(output string, issues []string) string {
	output = strings.TrimSpace(output)
	notice := "任务未能按成功状态收口：仍有未解决的测试验证失败：\n- " + strings.Join(issues, "\n- ")
	if output == "" {
		return notice
	}
	return output + "\n\n" + notice
}

func firstStringArg(arguments map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
