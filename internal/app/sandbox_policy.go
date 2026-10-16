package app

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	approvalcore "mac/internal/approval"
	middlewarecore "mac/internal/middleware"
	"mac/internal/permissionpolicy"
	"mac/internal/processutil"
	"mac/internal/toolcontract"
	"mac/internal/toolregistry"
	securitytools "mac/internal/tools/security"
)

func preflightToolApprovals(
	ctx context.Context,
	permissions *permissionpolicy.PermissionEngine,
	registry *toolregistry.Registry,
	sandbox *securitytools.Sandbox,
	calls []ToolCall,
) error {
	if permissions == nil || !approvalcore.RuntimeAvailable(ctx) {
		return nil
	}
	for _, call := range calls {
		// A user's explicit prohibition cannot be overridden by an approval.
		// Leave the normal execution path to return the specific policy error.
		if blocked, _ := permissionpolicy.UserToolPolicyBlocksTool(ctx, call, registry, toolcontract.IsReadOnlyToolName); blocked {
			continue
		}
		allowed, approvalRequired, reason := permissions.Check(call.Name, call.Arguments)
		if allowed || !approvalRequired {
			continue
		}
		if preflightSandboxToolCall(ctx, sandbox, registry, call) != nil {
			continue
		}
		if err := approvalcore.AuthorizeToolApproval(ctx, call, reason); err != nil {
			return err
		}
	}
	return nil
}

type sandboxPolicyError struct {
	code   string
	reason string
}

func (e *sandboxPolicyError) Error() string {
	if e == nil {
		return "sandbox policy blocked the tool"
	}
	return e.reason
}

func (e *sandboxPolicyError) MiddlewareErrorCode() string {
	if e == nil || strings.TrimSpace(e.code) == "" {
		return "sandbox_denied"
	}
	return e.code
}

type sandboxPolicyContextKey struct{}

// withSandboxPolicy appends an immutable policy boundary instead of replacing
// one. A child agent can therefore add restrictions but cannot discard or
// weaken the parent agent's sandbox.
func withSandboxPolicy(ctx context.Context, sandbox *securitytools.Sandbox) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sandbox == nil {
		return ctx
	}
	policies := sandboxPoliciesFromContext(ctx)
	for _, existing := range policies {
		if existing == sandbox {
			return ctx
		}
	}
	policies = append(policies, sandbox)
	return context.WithValue(ctx, sandboxPolicyContextKey{}, policies)
}

func sandboxPoliciesFromContext(ctx context.Context) []*securitytools.Sandbox {
	if ctx == nil {
		return nil
	}
	policies, _ := ctx.Value(sandboxPolicyContextKey{}).([]*securitytools.Sandbox)
	return append([]*securitytools.Sandbox(nil), policies...)
}

// SandboxPolicyMiddleware is the shared policy seam for native tools, MCP
// tools, dynamically registered tools, shell calls, network calls, and
// sub-agent launchers.
type SandboxPolicyMiddleware struct {
	middlewarecore.Base
	sandbox *securitytools.Sandbox
}

func NewSandboxPolicyMiddleware(sandbox *securitytools.Sandbox) *SandboxPolicyMiddleware {
	if sandbox == nil {
		return nil
	}
	return &SandboxPolicyMiddleware{
		Base:    middlewarecore.NewBase("sandbox_policy"),
		sandbox: sandbox,
	}
}

func (m *SandboxPolicyMiddleware) BeforeTool(ctx *middlewarecore.Context) error {
	if m == nil || ctx == nil {
		return nil
	}
	policies := []*securitytools.Sandbox{m.sandbox}
	policies = append(policies, sandboxPoliciesFromContext(ctx.Context)...)
	seen := make(map[*securitytools.Sandbox]bool, len(policies))
	for _, sandbox := range policies {
		if sandbox == nil || seen[sandbox] {
			continue
		}
		seen[sandbox] = true
		if err := validateSandboxToolCall(sandbox, ctx); err != nil {
			return err
		}
	}
	return nil
}

// preflightSandboxToolCall evaluates a copy of each active policy. It is used
// only to avoid opening an approval for an action the sandbox will reject
// anyway; copies keep the real violation ledger free of duplicate entries.
func preflightSandboxToolCall(
	ctx context.Context,
	sandbox *securitytools.Sandbox,
	registry *toolregistry.Registry,
	call ToolCall,
) error {
	actx := &middlewarecore.Context{
		Context: ctx, ToolCall: call, Metadata: make(map[string]any),
	}
	if registry != nil {
		if tool, ok := registry.Get(call.Name); ok {
			toolregistry.PopulatePolicyMetadata(actx.Metadata, call.Name, tool, call.Arguments)
		}
	}
	policies := []*securitytools.Sandbox{sandbox}
	policies = append(policies, sandboxPoliciesFromContext(ctx)...)
	seen := make(map[*securitytools.Sandbox]bool, len(policies))
	for _, policy := range policies {
		if policy == nil || seen[policy] {
			continue
		}
		seen[policy] = true
		copy := securitytools.NewWorkspaceSandbox(policy.Root(), policy.ConfigSnapshot())
		if err := validateSandboxToolCall(copy, actx); err != nil {
			return err
		}
	}
	return nil
}

var sandboxShellTools = map[string]bool{
	"cli": true, "shell": true, "shell_analyze": true, "shell_sessions": true,
	"repl": true, "build": true, "test_runner": true, "code_check": true, "debug": true,
	"sys": true, "remote": true, "native_env": true, "container": true,
	"external_agent": true, "ptc_execute": true,
}

var sandboxSubAgentTools = map[string]bool{
	"sub_agent": true, "parallel_sub_agent": true, "assemble_team": true,
	"swarm": true, "delegate_to": true,
}

var sandboxNetworkTools = map[string]bool{
	"agent_reach": true, "web_fetch": true, "web_search": true, "http": true, "api_call": true,
	"browser_task": true, "web_page": true, "web_navigate": true,
	"web_targets": true, "web_eval": true, "web_click": true,
	"web_scroll": true, "web_screenshot": true, "web_extract": true,
	"web_input": true, "web_replay": true, "remote": true,
	"notify": true, "social_publish": true, "source_acquire": true,
}

func validateSandboxToolCall(sandbox *securitytools.Sandbox, ctx *middlewarecore.Context) error {
	toolName := toolcontract.NormalizeToolName(ctx.ToolCall.Name)
	capabilities := sandboxCapabilitiesFromContext(ctx)
	processAccess := sandboxShellTools[toolName] || capabilities.Process
	networkAccess := sandboxNetworkTools[toolName] || capabilities.Network
	subAgentAccess := sandboxSubAgentTools[toolName] || capabilities.SubAgent
	if isRestrictedTenantExecution(ctx.Context) {
		if tenantHostIntrospectionToolCall(ctx.ToolCall) {
			sandbox.AddViolation("host_info", toolName+": tenant host information boundary", true)
			return &sandboxPolicyError{
				code:   "sandbox_host_info_denied",
				reason: "host system information is outside this user's workspace",
			}
		}
		// The authenticated tenant workspace is an authorization boundary, not
		// a sandbox-mode preference. Enforce it for every file-oriented tool,
		// including read-only helpers, even when the host sandbox mode is none.
		for _, candidate := range collectSandboxPaths(ctx.ToolCall, networkAccess) {
			if err := enforcePersonalWorkspacePath(ctx.Context, sandbox.Root(), candidate); err != nil {
				sandbox.AddViolation("tenant_path", toolName+": tenant workspace boundary", true)
				return &sandboxPolicyError{
					code:   "sandbox_tenant_path_denied",
					reason: "the requested path is outside this user's workspace",
				}
			}
		}
	}

	config := sandbox.ConfigSnapshot()
	if config.Mode == securitytools.SandboxNone {
		return nil
	}
	if config.Mode == securitytools.SandboxReadOnly && !sandboxCallIsReadOnly(ctx) {
		return blockSandbox(sandbox, "readonly", toolName, "sandbox is read-only and the tool may cause side effects")
	}
	if processAccess && strings.EqualFold(strings.TrimSpace(config.ShellPolicy), "deny") {
		return blockSandbox(sandbox, "shell", toolName, "shell and process execution is disabled by host policy")
	}
	if subAgentAccess && strings.EqualFold(strings.TrimSpace(config.SubAgentPolicy), "deny") {
		return blockSandbox(sandbox, "sub_agent", toolName, "sub-agent execution is disabled by host policy")
	}

	paths := collectSandboxPaths(ctx.ToolCall, networkAccess)
	for _, candidate := range paths {
		if allowed, reason := sandbox.ValidatePath(candidate); !allowed {
			return &sandboxPolicyError{
				code:   "sandbox_path_denied",
				reason: fmt.Sprintf("sandbox denied path %q for tool %q: %s", candidate, ctx.ToolCall.Name, reason),
			}
		}
	}
	if len(paths) > 0 && config.MaxFileSize > 0 {
		if payloadBytes := sandboxPayloadBytes(ctx.ToolCall.Arguments); payloadBytes > config.MaxFileSize {
			sandbox.AddViolation("file_size", fmt.Sprintf("%s: %d", toolName, payloadBytes), true)
			return &sandboxPolicyError{
				code: "sandbox_file_too_large",
				reason: fmt.Sprintf(
					"sandbox denied %d-byte payload for tool %q (limit %d bytes)",
					payloadBytes, ctx.ToolCall.Name, config.MaxFileSize,
				),
			}
		}
	}

	networkTargets := append(
		append([]string(nil), capabilities.NetworkTargets...),
		collectSandboxNetworkTargets(ctx.ToolCall, networkAccess || processAccess)...,
	)
	networkTargets = uniqueSandboxValues(networkTargets)
	if strings.EqualFold(strings.TrimSpace(config.NetworkPolicy), "deny") &&
		(networkAccess || len(networkTargets) > 0) {
		return blockSandbox(sandbox, "network", toolName, "network access is disabled by host policy")
	}
	if networkAccess && len(config.AllowedHosts) > 0 && len(networkTargets) == 0 {
		return blockSandbox(
			sandbox,
			"network",
			toolName,
			"network-capable tool has no inspectable target for the configured host allowlist",
		)
	}
	for _, target := range networkTargets {
		if allowed, reason := sandbox.ValidateNetwork(target); !allowed {
			return &sandboxPolicyError{
				code:   "sandbox_network_denied",
				reason: fmt.Sprintf("sandbox denied network target %q for tool %q: %s", target, ctx.ToolCall.Name, reason),
			}
		}
	}
	return nil
}

func sandboxCapabilitiesFromContext(ctx *middlewarecore.Context) ToolSandboxCapabilities {
	if ctx == nil || ctx.Metadata == nil {
		return ToolSandboxCapabilities{}
	}
	switch value := ctx.Metadata["sandbox_capabilities"].(type) {
	case ToolSandboxCapabilities:
		value.NetworkTargets = append([]string(nil), value.NetworkTargets...)
		return value
	case *ToolSandboxCapabilities:
		if value != nil {
			copy := *value
			copy.NetworkTargets = append([]string(nil), value.NetworkTargets...)
			return copy
		}
	}
	return ToolSandboxCapabilities{}
}

func blockSandbox(sandbox *securitytools.Sandbox, violationType, toolName, reason string) error {
	sandbox.AddViolation(violationType, toolName+": "+reason, true)
	return &sandboxPolicyError{code: "sandbox_" + violationType + "_denied", reason: reason}
}

func sandboxCallIsReadOnly(ctx *middlewarecore.Context) bool {
	if ctx != nil && ctx.Metadata != nil {
		if value, ok := ctx.Metadata["tool_read_only"].(bool); ok {
			return value
		}
	}
	if ctx == nil {
		return false
	}
	return toolcontract.IsReadOnlyToolName(toolcontract.NormalizeToolName(ctx.ToolCall.Name))
}

func collectSandboxPaths(call ToolCall, networkTool bool) []string {
	var paths []string
	var walk func(interface{}, string)
	walk = func(value interface{}, key string) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for childKey, childValue := range typed {
				walk(childValue, strings.ToLower(strings.TrimSpace(childKey)))
			}
		case []interface{}:
			for _, child := range typed {
				walk(child, key)
			}
		case []string:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			if sandboxArgumentIsPath(key, networkTool) {
				if candidate := strings.TrimSpace(typed); candidate != "" {
					paths = append(paths, candidate)
				}
			} else if key == "args" {
				paths = append(paths, sandboxProcessArgumentPaths(typed)...)
			}
		}
	}
	walk(call.Arguments, "")
	// ExtractTool historically calls its destination argument "output" rather
	// than "output_path". Keep this tool-specific so prose/output fields on
	// unrelated tools are not misclassified as filesystem paths.
	if toolcontract.NormalizeToolName(call.Name) == "extract" {
		if output, _ := call.Arguments["output"].(string); strings.TrimSpace(output) != "" {
			paths = append(paths, strings.TrimSpace(output))
		}
	}
	if command, _ := call.Arguments["command"].(string); strings.TrimSpace(command) != "" {
		paths = append(paths, sandboxExecutionCommandPaths(command)...)
	}
	return uniqueSandboxValues(paths)
}

func sandboxArgumentIsPath(key string, networkTool bool) bool {
	switch key {
	case "file", "files", "file_path", "filepath", "paths",
		"input_path", "output_path", "source_path", "target_path",
		"old_path", "new_path", "script_path", "config_path",
		"image_path", "audio_path", "video_path", "archive_path",
		"directory", "dir", "cwd", "work_dir", "working_dir",
		"workspace", "workspace_path", "repo", "repo_path", "root_dir":
		return true
	case "path":
		return !networkTool
	}
	return strings.HasSuffix(key, "_file") ||
		strings.HasSuffix(key, "_directory") ||
		strings.HasSuffix(key, "_dir")
}

var (
	sandboxWindowsPathPattern = regexp.MustCompile(`(?i)(?:^|[\s"'=])([a-z]:[\\/][^\s"'|;&<>]+)`)
	sandboxUnixPathPattern    = regexp.MustCompile(`(?:^|[\s"'=])((?:\.\.?/|/)[^\s"'|;&<>]+)`)
	// A standalone slash is also common prose punctuation (for example,
	// "0-30 / 31-60 / 61-90"). Request/output text therefore needs a
	// little more context than an actual shell command before treating "/" as
	// the filesystem root. Tool arguments continue to use sandboxCommandPaths
	// directly, so hard execution-time workspace enforcement is unchanged.
	sandboxExplicitRootTargetPattern = regexp.MustCompile(`(?i)(?:(?:^|[\r\n;|&])\s*(?:sudo\s+)?(?:rm|rmdir|mv|cp|cat|chmod|chown|find|ls|du|read|open|inspect|read_file|write_file|edit_file|delete|remove-item|get-childitem)\b[^\r\n]{0,160}(?:\s|=)["'` + "`" + `]?/["'` + "`" + `]?(?:\s|$)|\b(?:path|file|filepath|file_path|directory|dir|cwd|workspace|root_dir)\s*[:=]\s*["'` + "`" + `]?/["'` + "`" + `]?(?:[\s,)}\]]|$)|(?:删除|清空|读取|写入|移动|复制|查看|打开).{0,32}(?:\s|=)["'` + "`" + `]?/["'` + "`" + `]?(?:\s|$))`)
)

func sandboxCommandPaths(command string) []string {
	var paths []string
	for _, matches := range sandboxWindowsPathPattern.FindAllStringSubmatch(command, -1) {
		if len(matches) > 1 {
			paths = append(paths, matches[1])
		}
	}
	for _, matches := range sandboxUnixPathPattern.FindAllStringSubmatch(command, -1) {
		if len(matches) > 1 && !strings.HasPrefix(matches[1], "//") {
			paths = append(paths, matches[1])
		}
	}
	for _, token := range processutil.ParseCommandLine(command) {
		token = strings.Trim(strings.TrimSpace(token), `"'(),;|&<>`)
		if strings.Contains(token, "://") {
			continue
		}
		if filepath.IsAbs(token) || tenantPathUsesForeignAbsoluteSyntax(token) ||
			token == ".." || strings.HasPrefix(token, `..\`) || strings.HasPrefix(token, "../") {
			paths = append(paths, token)
		}
	}
	return paths
}

// sandboxTextPaths extracts paths from natural-language request or response
// text. Unlike sandboxCommandPaths, it ignores a bare slash when the surrounding
// text does not explicitly target the filesystem root. This prevents visual
// briefs, ratios, and roadmap separators from becoming false boundary escapes.
func sandboxTextPaths(input string) []string {
	paths := sandboxCommandPaths(input)
	explicitRoot := sandboxExplicitRootTargetPattern.MatchString(input)
	if len(paths) == 0 && !explicitRoot {
		return nil
	}
	filtered := make([]string, 0, len(paths)+1)
	hasRoot := false
	for _, candidate := range paths {
		if trimTenantPathToken(candidate) == "/" {
			hasRoot = true
			if !explicitRoot {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	// Quoted key/value forms such as path="/" are deliberately recognized by
	// the text-level expression even though the command tokenizer may not emit
	// the quoted slash as an independent token.
	if explicitRoot && !hasRoot {
		filtered = append(filtered, "/")
	}
	return filtered
}

// sandboxExecutionCommandPaths distinguishes the executable from the data it
// is asked to access. AgentOS may invoke a host-installed interpreter such as
// /usr/bin/python3, but every script, input, output, and other explicit path
// remains constrained to the authenticated workspace.
func sandboxExecutionCommandPaths(command string) []string {
	paths := sandboxCommandPaths(command)
	if len(paths) == 0 {
		return nil
	}
	trustedExecutables := make(map[string]struct{})
	for _, segment := range splitSandboxCommandSegments(command) {
		words := processutil.ParseCommandLine(strings.TrimSpace(segment))
		for _, word := range words {
			candidate := trimSandboxPathToken(word)
			if candidate == "" {
				continue
			}
			if sandboxEnvironmentAssignment(candidate) {
				continue
			}
			if trustedSystemExecutablePath(candidate) {
				trustedExecutables[normalizeSandboxPathToken(candidate)] = struct{}{}
			}
			break
		}
	}

	filtered := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if _, ok := trustedExecutables[normalizeSandboxPathToken(candidate)]; ok {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func sandboxProcessArgumentPaths(arguments string) []string {
	paths := sandboxCommandPaths(arguments)
	for _, word := range processutil.ParseCommandLine(arguments) {
		candidate := trimSandboxPathToken(word)
		if candidate == "" {
			continue
		}
		fromAtFile := strings.HasPrefix(candidate, "@")
		candidate = strings.TrimPrefix(candidate, "@")
		candidate = trimSandboxPathToken(candidate)
		lower := strings.ToLower(candidate)
		if candidate == "" || strings.Contains(lower, "://") {
			continue
		}
		if fromAtFile || tenantPathUsesForeignAbsoluteSyntax(candidate) ||
			strings.HasPrefix(candidate, "../") || strings.HasPrefix(candidate, `..\`) ||
			strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, `.\`) {
			paths = append(paths, candidate)
		}
	}
	return uniqueSandboxValues(paths)
}

func splitSandboxCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case escaped:
			current.WriteByte(ch)
			escaped = false
		case ch == '\\':
			current.WriteByte(ch)
			escaped = true
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteByte(ch)
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteByte(ch)
		case !inSingle && !inDouble && (ch == ';' || ch == '|' || ch == '&'):
			if value := strings.TrimSpace(current.String()); value != "" {
				segments = append(segments, value)
			}
			current.Reset()
			if i+1 < len(command) && command[i+1] == ch {
				i++
			}
		default:
			current.WriteByte(ch)
		}
	}
	if value := strings.TrimSpace(current.String()); value != "" {
		segments = append(segments, value)
	}
	return segments
}

func sandboxEnvironmentAssignment(value string) bool {
	if index := strings.IndexByte(value, '='); index > 0 {
		name := value[:index]
		for position, r := range name {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || position > 0 && r >= '0' && r <= '9') {
				return false
			}
		}
		return true
	}
	return false
}

func trustedSystemExecutablePath(candidate string) bool {
	normalized := normalizeSandboxPathToken(candidate)
	if normalized == "" || strings.Contains(normalized, "/../") || strings.HasSuffix(normalized, "/..") {
		return false
	}
	for _, root := range []string{"/bin/", "/usr/bin/", "/usr/local/bin/"} {
		if strings.HasPrefix(normalized, root) && len(normalized) > len(root) {
			return true
		}
	}
	windowsPath := normalized
	if len(windowsPath) >= 3 && windowsPath[1] == ':' && windowsPath[2] == '/' {
		windowsPath = windowsPath[2:]
	}
	for _, root := range []string{
		"/windows/system32/", "/windows/syswow64/",
		"/program files/", "/program files (x86)/",
	} {
		if strings.HasPrefix(windowsPath, root) && len(windowsPath) > len(root) {
			switch {
			case strings.HasSuffix(windowsPath, ".exe"), strings.HasSuffix(windowsPath, ".com"),
				strings.HasSuffix(windowsPath, ".bat"), strings.HasSuffix(windowsPath, ".cmd"):
				return true
			}
		}
	}
	return false
}

func trimSandboxPathToken(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'(),;|&<>`)
}

func normalizeSandboxPathToken(value string) string {
	value = strings.TrimPrefix(trimSandboxPathToken(value), "@")
	return strings.ToLower(strings.ReplaceAll(value, `\`, "/"))
}

func sandboxPayloadBytes(arguments map[string]interface{}) int64 {
	var total int64
	var walk func(interface{}, string)
	walk = func(value interface{}, key string) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for childKey, childValue := range typed {
				walk(childValue, strings.ToLower(strings.TrimSpace(childKey)))
			}
		case []interface{}:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			switch key {
			case "content", "data", "patch", "replacement", "new_content", "body":
				total += int64(len(typed))
			}
		case []byte:
			total += int64(len(typed))
		}
	}
	walk(arguments, "")
	return total
}

func collectSandboxNetworkTargets(call ToolCall, scanEmbeddedTargets bool) []string {
	var targets []string
	var walk func(interface{}, string)
	walk = func(value interface{}, key string) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for childKey, childValue := range typed {
				walk(childValue, strings.ToLower(strings.TrimSpace(childKey)))
			}
		case []interface{}:
			for _, child := range typed {
				walk(child, key)
			}
		case []string:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			value := strings.TrimSpace(typed)
			switch key {
			case "url", "uri", "endpoint", "base_url", "webhook_url", "callback_url", "host", "hostname":
				if value != "" {
					targets = append(targets, value)
				}
			default:
				if !scanEmbeddedTargets {
					return
				}
				for _, token := range strings.Fields(value) {
					token = strings.Trim(token, `"'(),[]{}<>`)
					if parsed, err := url.Parse(token); err == nil && parsed.Hostname() != "" {
						targets = append(targets, parsed.Hostname())
					}
				}
			}
		}
	}
	walk(call.Arguments, "")
	if command, _ := call.Arguments["command"].(string); command != "" {
		parts := processutil.ParseCommandLine(command)
		if len(parts) > 1 {
			executable := strings.ToLower(strings.TrimSuffix(parts[0], ".exe"))
			if executable == "curl" || executable == "wget" || executable == "ping" {
				for _, part := range parts[1:] {
					part = strings.TrimSpace(strings.Trim(part, `"'`))
					if part == "" || strings.HasPrefix(part, "-") || strings.HasPrefix(part, "/") {
						continue
					}
					targets = append(targets, part)
				}
			}
		}
	}
	return uniqueSandboxValues(targets)
}

func uniqueSandboxValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
