package app

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	attachmentcore "mac/internal/attachments"
	"mac/internal/processutil"
	"mac/internal/projectctx"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

// enforcePersonalWorkspacePath applies the authenticated personal workspace
// boundary to file-oriented tools.
func enforcePersonalWorkspacePath(ctx context.Context, toolWorkspace, requested string) error {
	identity, ok := executionIdentityFromContext(ctx)
	if !ok || identity.ControlPlane || !identity.TenantScoped || strings.TrimSpace(identity.SubjectID) == "" {
		return nil
	}

	toolRoot, err := filepath.Abs(strings.TrimSpace(toolWorkspace))
	if err != nil {
		return fmt.Errorf("resolve tool workspace: %w", err)
	}
	toolRoot = filepath.Clean(toolRoot)
	runRoot := projectctx.ResolvedWorkspace(ctx)
	if runRoot == "" {
		runRoot = toolRoot
	}
	runRoot, err = filepath.Abs(runRoot)
	if err != nil {
		return fmt.Errorf("resolve personal workspace: %w", err)
	}
	runRoot = filepath.Clean(runRoot)
	if !projectctx.SameWorkspace(toolRoot, runRoot) {
		return fmt.Errorf("personal workspace boundary: tool workspace does not match this run's workspace")
	}

	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = "."
	}
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(toolRoot, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve requested path %q: %w", requested, err)
	}
	target = filepath.Clean(target)
	if !projectctx.ContainsPath(runRoot, target) {
		return fmt.Errorf("personal workspace boundary: requested path is outside this user's workspace")
	}
	if err := projectctx.EnsureResolvedPathInside(runRoot, target); err != nil {
		return fmt.Errorf("personal workspace boundary: requested path resolves outside this user's workspace")
	}
	return nil
}

func explicitLocalPathGuidance(input string) string {
	var paths []string
	for _, candidate := range sandboxTextPaths(input) {
		candidate = trimTenantPathToken(candidate)
		if candidate == "" || (!filepath.IsAbs(candidate) && !tenantPathUsesForeignAbsoluteSyntax(candidate)) {
			continue
		}
		paths = append(paths, candidate)
	}
	paths = uniqueSandboxValues(paths)
	filtered := paths[:0]
	for i, candidate := range paths {
		truncatedQuotedPath := false
		for j, other := range paths {
			if i == j || len(other) <= len(candidate) {
				continue
			}
			lowerCandidate := strings.ToLower(candidate)
			lowerOther := strings.ToLower(other)
			if strings.HasPrefix(lowerOther, lowerCandidate) && other[len(candidate)] == ' ' {
				truncatedQuotedPath = true
				break
			}
		}
		if !truncatedQuotedPath {
			filtered = append(filtered, candidate)
		}
	}
	paths = filtered
	if len(paths) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Explicit Local Paths\n")
	sb.WriteString("The user explicitly requested these absolute local paths in this turn:\n")
	for _, path := range paths {
		fmt.Fprintf(&sb, "- `%s`\n", path)
	}
	sb.WriteString("Treat these paths as in scope and pass each exact path directly to list_dir for a directory, or read_file/extract for a file. Do not substitute the workspace root and do not infer a sandbox denial merely because a path is outside the workspace. Report blocked access only after a current-turn execution tool explicitly returns an access or sandbox error. shell_analyze does not execute commands.\n")
	return sb.String()
}

const (
	tenantWorkspaceBoundaryRefusal  = "无法访问或披露当前用户工作目录之外的文件、目录或宿主机信息。你可以查询、创建或处理当前用户工作目录内的内容。生成交付物时请改用当前工作区的相对路径，例如 .openkitty/storage/文件名。"
	tenantFilesystemEvidenceFailure = "我没有取得可验证的当前工作目录结果，因此不能据此回答。请重试；如持续失败，请检查 MACS 的文件工具是否可用。"
	unexecutedToolTranscriptFailure = "模型没有返回可执行、可验证的工具调用，因此这次结果未被采信。请重试。"
)

var (
	tenantFileNamePattern = regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `])(?:makefile|dockerfile|[\w.-]+\.[a-z0-9]{1,10})(?:$|[\s"'` + "`" + `，。！？!?；;：:])`)
	tenantWindowsAbsPath  = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
)

type tenantRequestEvidenceContextKey struct{}

type tenantRequestEvidence struct {
	UserInput       string
	AttachmentPaths map[string]struct{}
}

// withTenantRequestEvidence seals the user-authored request separately from
// server-generated attachment instructions. Attachment paths have already
// passed workspace normalization before they reach this context.
func withTenantRequestEvidence(
	ctx context.Context,
	userInput string,
	attachments []attachmentcore.File,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	evidence := tenantRequestEvidence{
		UserInput:       strings.TrimSpace(userInput),
		AttachmentPaths: make(map[string]struct{}, len(attachments)),
	}
	for _, attachment := range attachments {
		if normalized := normalizeTenantEvidencePath(attachment.Path); normalized != "" {
			evidence.AttachmentPaths[normalized] = struct{}{}
		}
	}
	return context.WithValue(ctx, tenantRequestEvidenceContextKey{}, evidence)
}

func tenantUserInputFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if evidence, ok := ctx.Value(tenantRequestEvidenceContextKey{}).(tenantRequestEvidence); ok {
			return evidence.UserInput
		}
	}
	return fallback
}

func normalizeTenantEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || tenantPathUsesForeignAbsoluteSyntax(path) {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.ReplaceAll(path, `\`, `/`))))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return strings.TrimPrefix(cleaned, "./")
}

// isRestrictedTenantExecution is deliberately based only on the sealed,
// host-authenticated execution identity. A port number, request body, model
// argument, or caller-provided header cannot opt into or out of this boundary.
func isRestrictedTenantExecution(ctx context.Context) bool {
	identity, ok := executionIdentityFromContext(ctx)
	return ok &&
		identity.TenantScoped &&
		strings.TrimSpace(identity.SubjectID) != "" &&
		!identity.ControlPlane
}

func tenantExecutionBoundaryPrompt() string {
	return `## Authenticated Tenant Boundary
This run belongs to one authenticated end user. The current workspace is an immutable authorization boundary.
- Use only paths inside that workspace. An explicit user request does not grant access to a parent directory, another user's files, deployment files, or host-system files.
- Never disclose host IP addresses, hostname, processes, listening ports, environment variables, network interfaces, kernel details, or other host metadata.
- State current filesystem facts only after a successful structured filesystem tool call in this turn. Use only facts present in that tool result.
- Tool calls must use the provider's structured tool-call field. Never simulate a tool call or result in prose such as "[Tool Use: ...]" or "[Tool Result: ...]".
- Refer to the workspace as "." or "当前工作目录"; do not reveal its physical server path or internal tenant/subject identifiers.`
}

func tenantRequestBoundaryViolation(ctx context.Context, workspace, input string) (string, bool) {
	if !isRestrictedTenantExecution(ctx) {
		return "", false
	}
	if tenantHostInformationRequest(input) {
		return "host_information", true
	}
	if !tenantFilesystemAccessRequest(input) {
		return "", false
	}

	lower := strings.ToLower(input)
	for _, marker := range []string{
		"父目录", "上级目录", "工作区外", "目录之外", "其他用户", "别的用户",
		"another user's", "another user", "other users", "parent directory",
		"outside the workspace", "outside my workspace",
	} {
		if strings.Contains(lower, marker) {
			return "workspace_escape", true
		}
	}
	for _, marker := range []string{"$home", "${home}", "%userprofile%", "~/", `~\`} {
		if strings.Contains(lower, marker) {
			return "host_home", true
		}
	}

	for _, candidate := range sandboxTextPaths(input) {
		candidate = trimTenantPathToken(candidate)
		if candidate == "" {
			continue
		}
		if tenantPathUsesForeignAbsoluteSyntax(candidate) {
			return "outside_workspace_path", true
		}
		if err := enforcePersonalWorkspacePath(ctx, workspace, candidate); err != nil {
			return "outside_workspace_path", true
		}
	}
	return "", false
}

func tenantFilesystemAccessRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	if sandboxExplicitRootTargetPattern.MatchString(input) {
		return true
	}
	if (strings.Contains(lower, "api") || strings.Contains(lower, "接口") || strings.Contains(lower, "url")) &&
		!containsAnyText(lower, []string{"目录", "文件", "文件夹", "工作区", "磁盘", "workspace", "file", "folder", "directory"}) {
		return false
	}

	for _, clause := range splitTenantFilesystemIntentClauses(input) {
		clause = strings.ToLower(strings.TrimSpace(clause))
		if clause == "" {
			continue
		}
		hasFilesystemNoun := containsAnyText(clause, []string{
			"当前目录", "工作目录", "工作区", "项目目录", "父目录", "上级目录",
			"目录", "文件夹", "文件", "路径", "源码", "源代码",
			"workspace", "working directory", "directory", "folder", "file", "filesystem",
			"repository", "repo", "source tree",
		})
		hasAction := containsAnyText(clause, []string{
			"看看", "查看", "列出", "读取", "打开", "搜索", "查找", "扫描", "遍历",
			"有什么", "有哪些", "是否存在", "创建", "新建", "写入",
			"修改", "编辑", "删除", "移动", "复制", "上传", "下载",
			"list", "show", "inspect", "read", "open", "find", "search", "scan",
			"what is in", "what's in", "exists", "create", "write", "edit", "delete",
			"move", "copy", "upload", "download",
		})
		if hasFilesystemNoun && hasAction {
			return true
		}
		hasExplicitPath := len(sandboxTextPaths(clause)) > 0 ||
			tenantFileNamePattern.MatchString(clause)
		if hasExplicitPath && hasAction {
			return true
		}
	}
	switch strings.TrimSpace(lower) {
	case "pwd", "ls", "ls -la", "dir", "tree":
		return true
	}
	return false
}

func splitTenantFilesystemIntentClauses(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '；', ';', '！', '!', '？', '?', '，', ',':
			return true
		default:
			return false
		}
	})
}

func tenantFilesystemFactRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if !tenantFilesystemAccessRequest(lower) {
		return false
	}
	return containsAnyText(lower, []string{
		"看看", "查看", "列出", "读取", "打开", "搜索", "查找", "扫描", "遍历",
		"有什么", "有哪些", "内容", "结构", "是否存在", "大小", "状态",
		"list", "show", "inspect", "read", "open", "find", "search", "scan",
		"what is in", "what's in", "exists", "size", "status",
	}) || lower == "pwd" || lower == "ls" || lower == "ls -la" || lower == "dir" || lower == "tree"
}

func tenantDirectoryEnumerationRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if !tenantFilesystemFactRequest(lower) {
		return false
	}
	return containsAnyText(lower, []string{
		"目录", "文件夹", "工作区", "当前目录", "有什么", "有哪些", "结构", "遍历",
		"directory", "folder", "workspace", "what is in", "what's in", "list", "tree",
	}) || lower == "pwd" || lower == "ls" || lower == "ls -la" || lower == "dir" || lower == "tree"
}

func tenantHostInformationRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}

	queryIntent := containsAnyText(lower, []string{
		"什么", "多少", "哪些", "查看", "看看", "获取", "告诉", "显示", "列出", "查询",
		"what", "which", "show", "list", "get", "tell", "inspect", "check",
	})
	hostNoun := containsAnyText(lower, []string{
		"服务器", "宿主机", "主机", "本机", "这台机器", "运行机器", "当前系统",
		"server", "host machine", "host system", "this machine", "local machine",
		"runtime host", "operating system",
	})
	hostFact := containsAnyText(lower, []string{
		"ip", "主机名", "hostname", "进程", "process", "端口", "port", "监听",
		"环境变量", "environment variable", "网卡", "网络接口", "network interface",
		"路由", "route", "内核", "kernel", "系统版本", "os version", "cpu", "内存",
		"memory", "磁盘", "disk", "挂载", "mount", "容器", "container",
	})
	if queryIntent && hostNoun && hostFact {
		return true
	}
	if queryIntent && containsAnyText(lower, []string{
		"公网ip", "内网ip", "本机ip", "当前ip", "我的ip", "你的ip",
		"public ip", "private ip", "local ip", "server ip", "host ip", "your ip",
		"运行了哪些进程", "有哪些进程", "监听哪些端口", "有哪些环境变量",
	}) {
		return true
	}
	// The tool-call guard accepts raw shell/code and can therefore treat a
	// leading word such as "who" as a command. A user request is natural
	// language: only apply that command parser when the whole request actually
	// has command-like shape. This avoids classifying "Who are you?" as Linux
	// host introspection.
	return tenantHostIntrospectionRequestCommand(lower)
}

func tenantHostIntrospectionRequestCommand(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	if strings.ContainsAny(trimmed, "？?。！!") {
		return false
	}
	tokens := processutil.ParseCommandLine(trimmed)
	if len(tokens) == 0 {
		return false
	}
	first := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(tokens[0], `"'()`), `\`, `/`)))
	if first == "who" && len(tokens) > 1 && !strings.HasPrefix(tokens[1], "-") {
		return false
	}
	return tenantHostIntrospectionCommand(trimmed)
}

func tenantFilesystemEvidenceToolCall(ctx context.Context, call ToolCall) bool {
	switch toolcontract.NormalizeToolName(call.Name) {
	case "list_dir", "read_file", "glob", "grep", "project_map", "snapshot":
		return true
	case "extract":
		file, _ := call.Arguments["file"].(string)
		normalized := normalizeTenantEvidencePath(file)
		if normalized == "" || ctx == nil {
			return false
		}
		evidence, ok := ctx.Value(tenantRequestEvidenceContextKey{}).(tenantRequestEvidence)
		if !ok {
			return false
		}
		_, authorized := evidence.AttachmentPaths[normalized]
		return authorized
	default:
		return false
	}
}

// hasUnexecutedToolTranscript detects provider/model prose that impersonates
// tool protocol. Markdown code fences are excluded so documentation and code
// examples can still discuss the syntax safely.
func hasUnexecutedToolTranscript(content string) bool {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	inFence := false
	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		trimmed := strings.TrimSpace(line)
		for {
			before := trimmed
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
			if trimmed == before {
				break
			}
		}
		lower := strings.ToLower(trimmed)
		for _, prefix := range []string{
			"[tool use:", "[tool use]", "[tool call:", "[tool call]",
			"[tool result:", "[tool result]", "[工具调用:", "[工具调用]",
			"[工具结果:", "[工具结果]",
		} {
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		}
	}
	return false
}

func tenantHostIntrospectionToolCall(call ToolCall) bool {
	toolName := toolcontract.NormalizeToolName(call.Name)
	switch toolName {
	case "system_status", "doctor", "sys", "native_env":
		return true
	}
	if toolName == "container" {
		if action, _ := call.Arguments["action"].(string); strings.EqualFold(strings.TrimSpace(action), "status") {
			return true
		}
	}
	for _, value := range tenantToolArgumentStrings(call.Arguments) {
		lower := strings.ToLower(value)
		if containsAnyText(lower, []string{
			"169.254.169.254", "metadata.google.internal", "metadata.azure.com",
			"100.100.100.200", "ifconfig.me", "api.ipify.org", "icanhazip.com",
			"checkip.amazonaws.com",
		}) {
			return true
		}
	}
	if command, _ := call.Arguments["command"].(string); tenantHostIntrospectionCommand(command) {
		return true
	}
	if script, _ := call.Arguments["script"].(string); tenantHostIntrospectionCommand(script) {
		return true
	}
	if code, _ := call.Arguments["code"].(string); tenantHostIntrospectionCommand(code) {
		return true
	}
	return false
}

func tenantHostIntrospectionCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	if containsAnyText(lower, []string{
		"socket.gethostname", "socket.gethostbyname", "os.hostname()", "os.uname",
		"os.environ", "process.env", "platform.uname", "platform.node",
		"psutil.", "netifaces.", "networkinterfaces()", "get-netipaddress",
		"get-nettcpconnection", "get-computerinfo", "get-ciminstance",
		"get-process", "get-service", "get-childitem env:", "$env:",
		"/proc/", "/sys/", "/etc/hostname", "/etc/os-release",
		"169.254.169.254", "metadata.google.internal", "metadata.azure.com",
		"100.100.100.200", "ifconfig.me", "api.ipify.org", "icanhazip.com",
		"checkip.amazonaws.com",
	}) {
		return true
	}

	segments := strings.FieldsFunc(lower, func(r rune) bool {
		switch r {
		case ';', '|', '&', '\n', '\r':
			return true
		default:
			return false
		}
	})
	for _, segment := range segments {
		tokens := processutil.ParseCommandLine(strings.TrimSpace(segment))
		for len(tokens) > 0 {
			token := strings.Trim(strings.TrimSpace(tokens[0]), `"'()`)
			base := strings.ToLower(filepath.Base(strings.ReplaceAll(token, `\`, `/`)))
			if base == "sudo" || base == "command" || base == "nohup" {
				tokens = tokens[1:]
				continue
			}
			if base == "env" {
				return true
			}
			if base == "bash" || base == "sh" || base == "zsh" ||
				base == "cmd" || base == "cmd.exe" ||
				base == "powershell" || base == "powershell.exe" || base == "pwsh" {
				if len(tokens) > 1 {
					return tenantHostIntrospectionCommand(strings.Join(tokens[1:], " "))
				}
				break
			}
			switch base {
			case "hostname", "hostname.exe", "uname", "whoami", "who", "w", "id",
				"ps", "top", "htop", "pgrep", "pidof", "ss", "netstat", "lsof",
				"ifconfig", "ipconfig", "getmac", "route", "arp", "nmcli",
				"printenv", "systeminfo", "systemctl", "journalctl", "dmesg",
				"free", "lscpu", "lsblk", "mount":
				return true
			}
			break
		}
	}
	return false
}

func tenantToolArgumentStrings(arguments map[string]interface{}) []string {
	var values []string
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for _, child := range typed {
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		case []string:
			values = append(values, typed...)
		case string:
			values = append(values, typed)
		}
	}
	walk(arguments)
	return values
}

func redactTenantWorkspacePath(content, workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if content == "" || workspace == "" {
		return content
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		absolute = workspace
	}
	candidates := []string{
		filepath.Clean(absolute),
		filepath.ToSlash(filepath.Clean(absolute)),
		strings.ReplaceAll(filepath.Clean(absolute), `/`, `\`),
	}
	for _, candidate := range uniqueSandboxValues(candidates) {
		if candidate != "" && candidate != "." {
			content = strings.ReplaceAll(content, candidate, ".")
		}
	}
	return content
}

func sanitizeTenantToolResult(content, workspace string) string {
	lower := strings.ToLower(content)
	if containsAnyText(lower, []string{
		"personal workspace boundary",
		"sandbox_host_info_denied",
		"sandbox_tenant_path_denied",
		"host system information is outside",
	}) {
		return tenantWorkspaceBoundaryRefusal
	}
	return redactTenantWorkspacePath(content, workspace)
}

func tenantOutputClaimsOutsideWorkspace(ctx context.Context, workspace, output string) bool {
	if !isRestrictedTenantExecution(ctx) {
		return false
	}
	for _, candidate := range sandboxTextPaths(output) {
		candidate = trimTenantPathToken(candidate)
		if candidate == "" {
			continue
		}
		if tenantPathUsesForeignAbsoluteSyntax(candidate) {
			return true
		}
		if err := enforcePersonalWorkspacePath(ctx, workspace, candidate); err != nil {
			return true
		}
	}
	return false
}

func tenantPathUsesForeignAbsoluteSyntax(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	hostAbsolute := filepath.IsAbs(path)
	unixAbsolute := strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//")
	windowsAbsolute := tenantWindowsAbsPath.MatchString(path)
	return !hostAbsolute && (unixAbsolute || windowsAbsolute)
}

func trimTenantPathToken(path string) string {
	return strings.Trim(strings.TrimSpace(path), "`'\"，。！？!?；;：:)]}>")
}
