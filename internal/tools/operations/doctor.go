package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/gitutil"
	"mac/internal/permissionpolicy"
	"mac/internal/shellpolicy"
)

// DoctorConfigSummary is the application configuration state needed by the
// doctor tool. Keeping it data-only avoids coupling the tool to app.Config.
type DoctorConfigSummary struct {
	ModelCount                 int
	PermissionMode             string
	DangerouslySkipPermissions bool
	ToolCount                  int
}

// DoctorRuntime exposes application-owned subsystem state to the reusable
// doctor implementation.
type DoctorRuntime interface {
	ExecuteTool(context.Context, string, map[string]interface{}) (string, bool, error)
	ConfigSummary() (DoctorConfigSummary, bool)
	ToolNames() ([]string, bool)
	IsCoreTool(string) bool
	HasTool(string) bool
	LSPConfigured() bool
	HookNames() ([]string, bool)
	ActiveSnapshotCount() (int, bool)
	WorktreeHealth() ([]string, bool)
}

// DoctorTool runs health checks on all subsystems.
// Claude Code borrowed: /doctor command
type DoctorTool struct {
	runtime DoctorRuntime
}

func NewDoctorTool(runtime DoctorRuntime) *DoctorTool {
	return &DoctorTool{runtime: runtime}
}

func (t *DoctorTool) Name() string { return "doctor" }

func (t *DoctorTool) Description() string {
	return "Run comprehensive health checks on all subsystems: git, LSP, hooks, worktrees, MCP, tools, snapshots, config, network/search, Agent Reach. " +
		"Use this to diagnose issues before starting work or when things aren't working as expected."
}

func (t *DoctorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"check": map[string]interface{}{
				"type":        "string",
				"description": "Specific subsystem to check: all, git, lsp, hooks, mcp, tools, snapshots, config, shell, network, search, agent_reach",
				"enum":        []string{"all", "git", "lsp", "hooks", "mcp", "tools", "snapshots", "config", "shell", "network", "search", "agent_reach"},
			},
		},
	}
}

func (t *DoctorTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	check := configvalue.String(params, "check", "all")
	if check == "agent_reach" {
		return formatHealthReport([]HealthCheck{t.CheckAgentReach(ctx)}), nil
	}

	var results []HealthCheck
	checks := []struct {
		name string
		fn   func() HealthCheck
	}{
		{"system", t.checkSystem},
		{"git", t.checkGit},
		{"config", t.checkConfig},
		{"shell", t.checkShellSafety},
		{"tools", t.checkTools},
		{"lsp", t.checkLSP},
		{"hooks", t.checkHooks},
		{"mcp", t.checkMCP},
		{"snapshots", t.checkSnapshots},
		{"worktrees", t.checkWorktrees},
		{"network", t.checkSearchNetwork},
		{"agent_reach", func() HealthCheck { return t.CheckAgentReach(ctx) }},
	}

	for _, c := range checks {
		if check == "all" || check == c.name || (check == "search" && c.name == "network") {
			results = append(results, c.fn())
		}
	}

	return formatHealthReport(results), nil
}

func (t *DoctorTool) CheckAgentReach(ctx context.Context) HealthCheck {
	if t.runtime == nil {
		return warn("agent_reach", "Tool registry not available")
	}
	result, found, err := t.runtime.ExecuteTool(ctx, "agent_reach", map[string]interface{}{"action": "doctor"})
	if !found {
		return fail("agent_reach", "agent_reach tool is not registered")
	}
	if err != nil {
		return fail("agent_reach", err.Error())
	}
	var report struct {
		Channels map[string]struct {
			Status        string  `json:"status"`
			Name          string  `json:"name"`
			Message       string  `json:"message"`
			ActiveBackend *string `json:"active_backend"`
		} `json:"channels"`
		OpenKitty struct {
			NativeFallbacks []string `json:"native_fallbacks"`
			Cached          bool     `json:"cached"`
		} `json:"openkitty"`
	}
	if err := json.Unmarshal([]byte(result), &report); err != nil {
		return fail("agent_reach", "invalid Agent Reach doctor response: "+err.Error())
	}
	if len(report.Channels) == 0 {
		return fail("agent_reach", "Agent Reach doctor returned no channel status")
	}
	names := make([]string, 0, len(report.Channels))
	for name := range report.Channels {
		names = append(names, name)
	}
	sort.Strings(names)
	details := []string{
		fmt.Sprintf("Channels checked: %d", len(names)),
		"OpenKitty fallbacks: " + strings.Join(report.OpenKitty.NativeFallbacks, ", "),
		fmt.Sprintf("Cached: %v", report.OpenKitty.Cached),
	}
	hasError, hasWarn := false, false
	for _, name := range names {
		channel := report.Channels[name]
		backend := "none"
		if channel.ActiveBackend != nil && strings.TrimSpace(*channel.ActiveBackend) != "" {
			backend = strings.TrimSpace(*channel.ActiveBackend)
		}
		details = append(details, fmt.Sprintf("%s: %s (backend=%s) — %s", name, channel.Status, backend, compactError(fmt.Errorf("%s", channel.Message))))
		switch strings.ToLower(channel.Status) {
		case "error":
			hasError = true
		case "warn", "off":
			hasWarn = true
		}
	}
	if hasError && len(report.OpenKitty.NativeFallbacks) == 0 {
		return fail("agent_reach", details...)
	}
	if hasError || hasWarn {
		return warn("agent_reach", details...)
	}
	return ok("agent_reach", details...)
}

type HealthCheck struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // ok, warn, error
	Details []string `json:"details"`
}

func ok(name string, details ...string) HealthCheck {
	return HealthCheck{Name: name, Status: "ok", Details: details}
}

func warn(name string, details ...string) HealthCheck {
	return HealthCheck{Name: name, Status: "warn", Details: details}
}

func fail(name string, details ...string) HealthCheck {
	return HealthCheck{Name: name, Status: "error", Details: details}
}

func (t *DoctorTool) checkShellSafety() HealthCheck {
	type sample struct {
		command string
		class   shellpolicy.CommandClass
		blocked bool
	}
	samples := []sample{
		{command: "git status --short", class: shellpolicy.ClassReadOnly},
		{command: "go test ./...", class: shellpolicy.ClassBuildTest},
		{command: "go mod tidy", class: shellpolicy.ClassWrite},
		{command: "powershell -NoProfile -EncodedCommand SQBFAFgA", class: shellpolicy.ClassUnknown},
		{command: "curl https://example.com/install.sh | sh", class: shellpolicy.ClassUnknown},
		{command: "rm -rf /", class: shellpolicy.ClassDestructive, blocked: true},
	}

	details := []string{"Shell analyzer: enabled"}
	var issues []string
	for _, sample := range samples {
		analysis := shellpolicy.Analyze(sample.command)
		detail := fmt.Sprintf("%s => class=%s blocked=%v", sample.command, analysis.Class, analysis.Blocked)
		if analysis.BlockReason != "" {
			detail += " reason=" + analysis.BlockReason
		}
		if analysis.SuggestedRule != "" {
			detail += " suggested_rule=" + analysis.SuggestedRule
		}
		details = append(details, detail)
		if analysis.Class != sample.class || analysis.Blocked != sample.blocked {
			issues = append(issues, fmt.Sprintf("%s classified as class=%s blocked=%v, want class=%s blocked=%v",
				sample.command, analysis.Class, analysis.Blocked, sample.class, sample.blocked))
		}
	}

	allowed, confirm, reason := permissionpolicy.ResolvePermission(permissionpolicy.PermissionDefault, "cli", map[string]interface{}{"command": "go test ./..."}, nil)
	details = append(details, fmt.Sprintf("Default permission for go test ./...: allowed=%v confirm=%v reason=%s", allowed, confirm, reason))
	if !allowed || confirm {
		issues = append(issues, "default permission should allow build/test commands without confirmation")
	}
	if len(issues) > 0 {
		return fail("shell", append(details, issues...)...)
	}
	return ok("shell", details...)
}

func (t *DoctorTool) checkSystem() HealthCheck {
	var details []string
	details = append(details, fmt.Sprintf("OS: %s/%s", runtime.GOOS, runtime.GOARCH))
	details = append(details, fmt.Sprintf("CPU: %d cores", runtime.NumCPU()))

	// Go version
	goCmd := exec.Command("go", "version")
	if out, err := goCmd.Output(); err == nil {
		details = append(details, strings.TrimSpace(string(out)))
	} else {
		details = append(details, "Go: not found")
	}

	// Git version
	gitCmd := exec.Command("git", "--version")
	if out, err := gitCmd.Output(); err == nil {
		details = append(details, strings.TrimSpace(string(out)))
	} else {
		details = append(details, "Git: not found")
	}

	// Disk space
	if dir, err := os.Getwd(); err == nil {
		details = append(details, fmt.Sprintf("CWD: %s", dir))
	}

	return ok("system", details...)
}

func (t *DoctorTool) checkGit() HealthCheck {
	if !gitutil.IsRepository(".") {
		return warn("git", "Current directory is not a git repository")
	}

	var details []string
	branch := gitutil.Output(".", "rev-parse", "--abbrev-ref", "HEAD")
	details = append(details, fmt.Sprintf("Branch: %s", strings.TrimSpace(branch)))

	status := gitutil.Output(".", "status", "--short")
	fileCount := len(strings.Split(strings.TrimSpace(status), "\n"))
	if fileCount > 0 && strings.TrimSpace(status) != "" {
		details = append(details, fmt.Sprintf("Uncommitted files: %d", fileCount))
	}

	remotes := gitutil.Output(".", "remote", "-v")
	if strings.TrimSpace(remotes) != "" {
		details = append(details, "Remotes: configured")
	} else {
		details = append(details, "Remotes: none")
	}

	return ok("git", details...)
}

func (t *DoctorTool) checkConfig() HealthCheck {
	var details []string
	if t.runtime == nil {
		return warn("config", "Agent not available for config check")
	}

	summary, available := t.runtime.ConfigSummary()
	if !available {
		return warn("config", "Global config not loaded")
	}

	details = append(details, fmt.Sprintf("Models configured: %d", summary.ModelCount))
	details = append(details, fmt.Sprintf("Permission mode: %s", summary.PermissionMode))

	if summary.DangerouslySkipPermissions {
		details = append(details, "Legacy skip_permissions: enabled")
	}

	if summary.ToolCount >= 0 {
		details = append(details, fmt.Sprintf("Tools registered: %d", summary.ToolCount))
	}

	return ok("config", details...)
}

func (t *DoctorTool) checkTools() HealthCheck {
	if t.runtime == nil {
		return fail("tools", "Tool registry not available")
	}

	var details []string
	names, available := t.runtime.ToolNames()
	if !available {
		return fail("tools", "Tool registry not available")
	}
	details = append(details, fmt.Sprintf("Total tools: %d", len(names)))

	// Check core tools
	coreCount := 0
	for _, name := range names {
		if t.runtime.IsCoreTool(name) {
			coreCount++
		}
	}
	details = append(details, fmt.Sprintf("Core tools: %d", coreCount))

	// Check extended tools
	details = append(details, fmt.Sprintf("Extended tools: %d", len(names)-coreCount))

	// Verify critical tools
	critical := []string{"read_file", "write_file", "cli", "web_fetch", "grep", "glob"}
	for _, name := range critical {
		if !t.runtime.HasTool(name) {
			return fail("tools", fmt.Sprintf("Critical tool missing: %s", name))
		}
	}

	return ok("tools", details...)
}

func (t *DoctorTool) checkLSP() HealthCheck {
	if t.runtime == nil || !t.runtime.LSPConfigured() {
		return warn("lsp", "LSP manager not configured")
	}

	// Check if LSP servers are available
	var details []string
	servers := map[string]string{
		"gopls":                      "Go",
		"pyright-langserver":         "Python",
		"typescript-language-server": "TypeScript",
	}
	for cmd, lang := range servers {
		if _, err := exec.LookPath(cmd); err == nil {
			details = append(details, fmt.Sprintf("%s: %s available", lang, cmd))
		} else {
			details = append(details, fmt.Sprintf("%s: %s NOT found", lang, cmd))
		}
	}

	return ok("lsp", details...)
}

func (t *DoctorTool) checkHooks() HealthCheck {
	if t.runtime == nil {
		return warn("hooks", "Hook manager not configured")
	}

	registered, available := t.runtime.HookNames()
	if !available {
		return warn("hooks", "Hook manager not configured")
	}
	if len(registered) == 0 {
		return ok("hooks", "No hooks installed (optional)")
	}

	var details []string
	for _, ht := range registered {
		details = append(details, fmt.Sprintf("Hook: %s", ht))
	}
	return ok("hooks", details...)
}

func (t *DoctorTool) checkMCP() HealthCheck {
	// Check if MCP tools exist in tool registry
	if t.runtime == nil {
		return warn("mcp", "Tool registry not available")
	}
	mcpTools := []string{"mcp_list_resources", "mcp_read_resource"}
	found := 0
	for _, name := range mcpTools {
		if t.runtime.HasTool(name) {
			found++
		}
	}
	return ok("mcp", fmt.Sprintf("MCP tools available: %d/%d", found, len(mcpTools)))
}

func (t *DoctorTool) checkSnapshots() HealthCheck {
	if t.runtime == nil {
		return warn("snapshots", "Snapshot manager not configured")
	}

	active, available := t.runtime.ActiveSnapshotCount()
	if !available {
		return warn("snapshots", "Snapshot manager not configured")
	}
	if active == 0 {
		return ok("snapshots", "No active snapshots (clean state)")
	}

	return ok("snapshots", fmt.Sprintf("Active snapshots: %d", active))
}

func (t *DoctorTool) checkWorktrees() HealthCheck {
	if t.runtime == nil {
		return warn("worktrees", "Worktree manager not configured")
	}

	issues, available := t.runtime.WorktreeHealth()
	if !available {
		return warn("worktrees", "Worktree manager not configured")
	}
	if len(issues) == 1 && issues[0] == "All worktrees healthy" {
		return ok("worktrees", "All worktrees healthy")
	}

	hasError := false
	for _, issue := range issues {
		if strings.Contains(issue, "failed") || strings.Contains(issue, "missing") {
			hasError = true
		}
	}
	if hasError {
		return fail("worktrees", issues...)
	}
	return warn("worktrees", issues...)
}

type searchNetworkTarget struct {
	Name string
	URL  string
}

type endpointDiagnosis struct {
	Name       string
	Host       string
	IPs        []string
	DNSMS      int64
	TCP        string
	TCPMS      int64
	HTTP       string
	HTTPMS     int64
	Suspicious []string
	Error      string
}

func (t *DoctorTool) checkSearchNetwork() HealthCheck {
	targets := []searchNetworkTarget{
		{Name: "DuckDuckGo", URL: "https://html.duckduckgo.com/html/?q=openkitty+network+check"},
		{Name: "Bing", URL: "https://www.bing.com/search?q=openkitty+network+check"},
		{Name: "Sogou", URL: "https://www.sogou.com/web?query=openkitty+network+check"},
	}

	details := []string{"Proxy: " + proxyEnvSummary()}
	hasError := false
	hasWarn := false
	okCount := 0

	for _, target := range targets {
		diag := diagnoseSearchEndpoint(target)
		details = append(details, formatEndpointDiagnosis(diag))
		if diag.Error != "" {
			hasError = true
			continue
		}
		if len(diag.Suspicious) > 0 || strings.HasPrefix(diag.TCP, "fail") || strings.HasPrefix(diag.HTTP, "fail") || strings.HasPrefix(diag.HTTP, "HTTP 4") || strings.HasPrefix(diag.HTTP, "HTTP 5") {
			hasWarn = true
		} else {
			okCount++
		}
	}

	switch {
	case okCount == 0 && hasError:
		return fail("network/search", details...)
	case hasError || hasWarn:
		return warn("network/search", details...)
	default:
		return ok("network/search", details...)
	}
}

func diagnoseSearchEndpoint(target searchNetworkTarget) endpointDiagnosis {
	diag := endpointDiagnosis{Name: target.Name}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		diag.Error = "invalid URL: " + err.Error()
		return diag
	}
	diag.Host = parsed.Hostname()
	if diag.Host == "" {
		diag.Error = "empty host"
		return diag
	}

	dnsStart := time.Now()
	dnsCtx, dnsCancel := context.WithTimeout(context.Background(), 2*time.Second)
	addrs, err := net.DefaultResolver.LookupIPAddr(dnsCtx, diag.Host)
	dnsCancel()
	diag.DNSMS = time.Since(dnsStart).Milliseconds()
	if err != nil {
		diag.Error = "DNS failed: " + err.Error()
		return diag
	}
	for _, addr := range addrs {
		ip := addr.IP.String()
		diag.IPs = append(diag.IPs, ip)
		if reason := suspiciousResolvedIP(diag.Host, ip); reason != "" {
			diag.Suspicious = append(diag.Suspicious, ip+" "+reason)
		}
	}
	sort.Strings(diag.IPs)

	tcpHost := firstIPv4(diag.IPs)
	network := "tcp4"
	if tcpHost == "" {
		tcpHost = firstIP(diag.IPs)
		network = "tcp"
	}
	tcpStart := time.Now()
	if tcpHost == "" {
		diag.TCP = "fail: no resolved IP"
	} else {
		conn, err := net.DialTimeout(network, net.JoinHostPort(tcpHost, "443"), 3*time.Second)
		diag.TCPMS = time.Since(tcpStart).Milliseconds()
		if err != nil {
			diag.TCP = "fail: " + compactError(err)
		} else {
			diag.TCP = "ok " + tcpHost + ":443"
			_ = conn.Close()
		}
	}

	httpStart := time.Now()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, target.URL, nil)
	if err != nil {
		diag.HTTP = "fail: " + compactError(err)
	} else {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		resp, err := httpClient.Do(req)
		diag.HTTPMS = time.Since(httpStart).Milliseconds()
		if err != nil {
			diag.HTTP = "fail: " + compactError(err)
		} else {
			diag.HTTP = fmt.Sprintf("HTTP %d", resp.StatusCode)
			_ = resp.Body.Close()
		}
	}

	return diag
}

func formatEndpointDiagnosis(diag endpointDiagnosis) string {
	if diag.Error != "" {
		return fmt.Sprintf("%s (%s) DNS: %s", diag.Name, diag.Host, diag.Error)
	}
	ips := strings.Join(limitStrings(diag.IPs, 6), ", ")
	parts := []string{
		fmt.Sprintf("%s (%s)", diag.Name, diag.Host),
		fmt.Sprintf("DNS: %s (%dms)", ips, diag.DNSMS),
		fmt.Sprintf("TCP: %s (%dms)", diag.TCP, diag.TCPMS),
		fmt.Sprintf("HTTP: %s (%dms)", diag.HTTP, diag.HTTPMS),
	}
	if len(diag.Suspicious) > 0 {
		parts = append(parts, "suspicious DNS: "+strings.Join(diag.Suspicious, "; "))
	}
	return strings.Join(parts, " | ")
}

func proxyEnvSummary() string {
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			parts = append(parts, key+"=unset")
			continue
		}
		parts = append(parts, key+"="+sanitizeProxyValue(value))
	}
	return strings.Join(parts, ", ")
}

func sanitizeProxyValue(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[set]"
	}
	parsed.User = nil
	return parsed.String()
}

func suspiciousResolvedIP(host, ip string) string {
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified() {
		return "(private/local address)"
	}
	for _, cidr := range suspiciousDNSCIDRs(host) {
		prefix, err := netip.ParsePrefix(cidr)
		if err == nil && prefix.Contains(parsed) {
			return "(known Meta/Facebook range; likely DNS pollution for search host)"
		}
	}
	return ""
}

func suspiciousDNSCIDRs(host string) []string {
	if !strings.Contains(strings.ToLower(host), "duckduckgo.com") {
		return nil
	}
	return []string{
		"157.240.0.0/16",
		"31.13.64.0/18",
		"66.220.144.0/20",
		"69.63.176.0/20",
		"69.171.224.0/19",
		"173.252.64.0/18",
		"2a03:2880::/32",
	}
}

func firstIPv4(ips []string) string {
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed != nil && parsed.To4() != nil {
			return ip
		}
	}
	return ""
}

func firstIP(ips []string) string {
	if len(ips) == 0 {
		return ""
	}
	return ips[0]
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	out := append([]string(nil), values[:limit]...)
	out = append(out, fmt.Sprintf("...+%d", len(values)-limit))
	return out
}

func compactError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 160 {
		msg = msg[:160] + "..."
	}
	return msg
}

func formatHealthReport(results []HealthCheck) string {
	var sb strings.Builder
	sb.WriteString("## System Health Report\n\n")

	okCount, warnCount, errCount := 0, 0, 0
	for _, r := range results {
		icon := map[string]string{"ok": "✅", "warn": "⚠️", "error": "❌"}[r.Status]
		sb.WriteString(fmt.Sprintf("### %s %s\n", icon, r.Name))
		for _, d := range r.Details {
			sb.WriteString(fmt.Sprintf("  - %s\n", d))
		}
		sb.WriteString("\n")

		switch r.Status {
		case "ok":
			okCount++
		case "warn":
			warnCount++
		case "error":
			errCount++
		}
	}

	sb.WriteString(fmt.Sprintf("---\n**Summary**: %d ok, %d warnings, %d errors\n", okCount, warnCount, errCount))
	return sb.String()
}

func FormatHealthReport(results []HealthCheck) string {
	return formatHealthReport(results)
}
