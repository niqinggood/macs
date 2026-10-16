package network

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/toolcontract"
)

const agentReachDoctorCacheTTL = 5 * time.Minute

const agentReachUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36"

type agentReachCommandRunner func(context.Context, string, ...string) ([]byte, error)
type agentReachLookPath func(string) (string, error)

type agentReachDoctorCache struct {
	checkedAt time.Time
	output    string
}

// AgentReachTool is OpenKitty's read-only integration boundary for Agent Reach.
// It uses the upstream per-platform CLIs when they are healthy and preserves
// OpenKitty's existing web/Social tools as bounded fallbacks. The existing
// tools therefore remain independently callable and are never replaced.
type AgentReachTool struct {
	WebFetch  toolcontract.Tool
	WebSearch toolcontract.Tool
	Social    toolcontract.Tool

	LookPath agentReachLookPath
	Runner   agentReachCommandRunner
	Now      func() time.Time
	Client   *http.Client

	BilibiliSearchURL string
	doctorMu          sync.Mutex
	doctorCache       agentReachDoctorCache
	openCLIMu         sync.Mutex
	openCLIAt         time.Time
	openCLIReady      bool
	openCLIReason     string
}

func NewAgentReachTool(WebFetch, WebSearch, Social toolcontract.Tool) *AgentReachTool {
	return &AgentReachTool{
		WebFetch:          WebFetch,
		WebSearch:         WebSearch,
		Social:            Social,
		LookPath:          exec.LookPath,
		Runner:            defaultAgentReachCommandRunner,
		Now:               time.Now,
		Client:            &http.Client{Timeout: 15 * time.Second},
		BilibiliSearchURL: "https://api.bilibili.com/x/web-interface/search/all/v2",
	}
}

// HasDelegates reports whether all native OpenKitty fallback tools were
// supplied by the assembly layer.
func (t *AgentReachTool) HasDelegates() bool {
	return t != nil && t.WebFetch != nil && t.WebSearch != nil && t.Social != nil
}

// ConfigureCommandRuntime replaces executable discovery and invocation. It is
// primarily useful for deterministic tests and embedded hosts.
func (t *AgentReachTool) ConfigureCommandRuntime(LookPath func(string) (string, error), Runner func(context.Context, string, ...string) ([]byte, error)) {
	if t == nil {
		return
	}
	if LookPath != nil {
		t.LookPath = LookPath
	}
	if Runner != nil {
		t.Runner = Runner
	}
}

// ConfigureBilibiliEndpoint replaces the public API endpoint and HTTP Client.
func (t *AgentReachTool) ConfigureBilibiliEndpoint(endpoint string, Client *http.Client) {
	if t == nil {
		return
	}
	if strings.TrimSpace(endpoint) != "" {
		t.BilibiliSearchURL = endpoint
	}
	if Client != nil {
		t.Client = Client
	}
}

// SetWebFetch replaces the web-fetch fallback.
func (t *AgentReachTool) SetWebFetch(tool toolcontract.Tool) {
	if t != nil {
		t.WebFetch = tool
	}
}

// SetClock replaces the wall clock used by health-cache expiration.
func (t *AgentReachTool) SetClock(Now func() time.Time) {
	if t != nil && Now != nil {
		t.Now = Now
	}
}

// OpenCLIAvailable exposes the bounded OpenCLI readiness probe to diagnostics.
func (t *AgentReachTool) OpenCLIAvailable(ctx context.Context) (bool, string) {
	return t.openCLIAvailable(ctx)
}

func (t *AgentReachTool) Name() string { return "agent_reach" }

func (t *AgentReachTool) Description() string {
	return "Unified read-only Agent Reach router for web and platform research. " +
		"Actions: doctor, search, read, trending, transcribe. Supports web/Exa, GitHub, " +
		"YouTube, Bilibili, Twitter/X, Reddit, Xiaohongshu, V2EX, LinkedIn, Xiaoyuzhou, " +
		"Xueqiu and RSS. It selects installed upstream backends and automatically falls " +
		"back to OpenKitty web_search, web_fetch, or social_trending without replacing them."
}

func (t *AgentReachTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Operation to perform.",
				"enum":        []string{"doctor", "search", "read", "trending", "transcribe"},
			},
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "Target platform. With action=read it can be inferred from target URL.",
				"enum": []string{
					"auto", "web", "exa", "github", "youtube", "bilibili", "twitter",
					"reddit", "xiaohongshu", "v2ex", "linkedin", "xiaoyuzhou", "xueqiu", "rss",
				},
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query. Required for action=search.",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "URL, post/video identifier, RSS URL, or local audio path for read/transcribe.",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "Alias for target.",
			},
			"kind": map[string]interface{}{
				"type":        "string",
				"description": "Optional subtype such as repositories, code, issues, article, subtitle, people, jobs, or company.",
			},
			"provider": map[string]interface{}{
				"type":        "string",
				"description": "Transcription provider.",
				"enum":        []string{"auto", "groq", "openai"},
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum result count (default 10, max 20).",
				"minimum":     1,
				"maximum":     20,
			},
			"refresh": map[string]interface{}{
				"type":        "boolean",
				"description": "With action=doctor, bypass the five-minute health cache.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *AgentReachTool) IsReadOnly() bool { return true }

func (t *AgentReachTool) MaxResultSize() int { return 100000 }

func (t *AgentReachTool) ResultIsContentPayload() bool { return true }

func (t *AgentReachTool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	return toolcontract.SandboxCapabilities{
		Process: true,
		Network: true,
		NetworkTargets: []string{
			"api.bilibili.com", "github.com", "reddit.com", "x.com", "youtube.com",
			"xiaohongshu.com", "v2ex.com", "linkedin.com", "xiaoyuzhoufm.com",
			"xueqiu.com", "r.jina.ai", "mcp.exa.ai",
		},
	}
}

func (t *AgentReachTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(stringParam(params, "action")))
	platform := normalizeAgentReachPlatform(stringParam(params, "platform"))
	query := strings.TrimSpace(stringParam(params, "query"))
	target := strings.TrimSpace(stringParam(params, "target"))
	if target == "" {
		target = strings.TrimSpace(stringParam(params, "url"))
	}
	if action == "doctor" {
		refresh, _ := params["refresh"].(bool)
		return t.doctor(ctx, platform, refresh)
	}
	if (platform == "" || platform == "auto") && target != "" {
		platform = inferAgentReachPlatform(target)
	}
	if platform == "" || platform == "auto" {
		platform = "web"
	}
	limit := clampLimit(params, 10, 20)

	switch action {
	case "search":
		if query == "" {
			return "", fmt.Errorf("query is required for agent_reach action=search")
		}
		return t.search(ctx, platform, query, strings.TrimSpace(stringParam(params, "kind")), limit)
	case "read":
		if target == "" {
			return "", fmt.Errorf("target or url is required for agent_reach action=read")
		}
		return t.read(ctx, platform, target, strings.TrimSpace(stringParam(params, "kind")))
	case "trending":
		if platform == "web" || platform == "exa" {
			return "", fmt.Errorf("platform is required for agent_reach action=trending")
		}
		return t.trending(ctx, platform, target, limit)
	case "transcribe":
		if target == "" {
			return "", fmt.Errorf("target or url is required for agent_reach action=transcribe")
		}
		provider := strings.ToLower(strings.TrimSpace(stringParam(params, "provider")))
		if provider == "" {
			provider = "auto"
		}
		if provider != "auto" && provider != "groq" && provider != "openai" {
			return "", fmt.Errorf("unsupported transcription provider %q", provider)
		}
		return t.transcribe(ctx, platform, target, provider)
	default:
		return "", fmt.Errorf("unsupported agent_reach action %q (use doctor, search, read, trending, or transcribe)", action)
	}
}

func defaultAgentReachCommandRunner(ctx context.Context, command string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, command, args...).CombinedOutput()
}

type agentReachCommand struct {
	backend string
	command string
	args    []string
	timeout time.Duration
}

func (t *AgentReachTool) runCommand(ctx context.Context, candidate agentReachCommand) (string, error) {
	if t == nil || t.LookPath == nil || t.Runner == nil {
		return "", fmt.Errorf("command runtime is not configured")
	}
	path, err := t.LookPath(candidate.command)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", candidate.command)
	}
	executable, prefix, err := t.resolveCommandInvocation(candidate.command, path)
	if err != nil {
		return "", err
	}
	runCtx := ctx
	cancel := func() {}
	if candidate.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, candidate.timeout)
	}
	defer cancel()
	output, runErr := t.Runner(runCtx, executable, append(prefix, candidate.args...)...)
	text := strings.TrimSpace(string(output))
	if len(text) > 100000 {
		text = text[:100000] + "\n[Agent Reach output truncated]"
	}
	if runErr != nil {
		if text != "" {
			return "", fmt.Errorf("%s failed: %v: %s", candidate.backend, runErr, truncate(text, 1200))
		}
		return "", fmt.Errorf("%s failed: %w", candidate.backend, runErr)
	}
	if text == "" {
		return "", fmt.Errorf("%s returned no content", candidate.backend)
	}
	return text, nil
}

func (t *AgentReachTool) resolveCommandInvocation(name, path string) (string, []string, error) {
	if runtime.GOOS != "windows" {
		return path, nil, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return path, nil, nil
	}
	// npm shims are batch files on Windows. Calling the underlying JS entry
	// keeps every user value as a separate argv item and avoids shell quoting.
	var relative string
	switch strings.ToLower(name) {
	case "opencli":
		relative = filepath.Join("node_modules", "@jackwener", "opencli", "dist", "src", "main.js")
	case "mcporter":
		relative = filepath.Join("node_modules", "mcporter", "dist", "cli.js")
	default:
		return "", nil, fmt.Errorf("unsupported Windows command shim %s", path)
	}
	node, err := t.LookPath("node")
	if err != nil {
		return "", nil, fmt.Errorf("%s requires node runtime", name)
	}
	return node, []string{filepath.Join(filepath.Dir(path), relative)}, nil
}

func (t *AgentReachTool) tryCommands(ctx context.Context, candidates []agentReachCommand) (string, string, []string) {
	failures := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.command), "opencli") {
			if ready, reason := t.openCLIAvailable(ctx); !ready {
				failures = append(failures, "OpenCLI skipped: "+reason)
				continue
			}
		}
		result, err := t.runCommand(ctx, candidate)
		if err == nil {
			return result, candidate.backend, failures
		}
		failures = append(failures, err.Error())
	}
	return "", "", failures
}

func (t *AgentReachTool) openCLIAvailable(ctx context.Context) (bool, string) {
	if t == nil {
		return false, "router is not configured"
	}
	t.openCLIMu.Lock()
	defer t.openCLIMu.Unlock()
	Now := t.Now()
	if !t.openCLIAt.IsZero() && Now.Sub(t.openCLIAt) < agentReachDoctorCacheTTL {
		return t.openCLIReady, t.openCLIReason
	}
	result, err := t.runCommand(ctx, agentReachCommand{
		backend: "OpenCLI doctor", command: "opencli", timeout: 5 * time.Second,
		args: []string{"daemon", "status"},
	})
	ready := err == nil && strings.Contains(strings.ToLower(result), "extension: connected") &&
		!strings.Contains(strings.ToLower(result), "extension: disconnected")
	reason := "browser extension is not connected"
	if err != nil {
		reason = err.Error()
	} else if ready {
		reason = "ready"
	}
	t.openCLIAt, t.openCLIReady, t.openCLIReason = Now, ready, reason
	return ready, reason
}

func (t *AgentReachTool) search(ctx context.Context, platform, query, kind string, limit int) (string, error) {
	switch platform {
	case "web", "exa":
		if result, err := t.searchExa(ctx, query, limit); err == nil {
			return formatAgentReachResult("search", platform, "Exa via Agent Reach", nil, result), nil
		} else if fallback, fallbackErr := t.delegateWebSearch(ctx, query, limit); fallbackErr == nil {
			return formatAgentReachResult("search", platform, "OpenKitty web_search fallback", []string{err.Error()}, fallback), nil
		} else {
			return "", fmt.Errorf("Agent Reach Exa failed: %v; OpenKitty web_search fallback failed: %w", err, fallbackErr)
		}
	case "github":
		return t.searchGitHub(ctx, query, kind, limit)
	case "youtube":
		candidate := agentReachCommand{backend: "yt-dlp", command: "yt-dlp", timeout: 30 * time.Second,
			args: []string{"--dump-json", "--flat-playlist", "--playlist-end", strconv.Itoa(limit), fmt.Sprintf("ytsearch%d:%s", limit, query)}}
		return t.commandSearchWithFallback(ctx, platform, query, limit, []agentReachCommand{candidate})
	case "bilibili":
		// The public API is a verified zero-login backend. Try it before
		// browser-session OpenCLI so a disconnected extension cannot add a full
		// command timeout to an otherwise healthy Bilibili search.
		if result, backend, failures := t.tryCommands(ctx, []agentReachCommand{
			{backend: "bili-cli", command: "bili", timeout: 12 * time.Second, args: []string{"search", query, "--type", "video", "-n", strconv.Itoa(limit)}},
		}); result != "" {
			return formatAgentReachResult("search", platform, backend, failures, result), nil
		} else if apiResult, apiErr := t.searchBilibiliAPI(ctx, query, limit); apiErr == nil {
			return formatAgentReachResult("search", platform, "Bilibili search API", failures, apiResult), nil
		} else {
			failures = append(failures, apiErr.Error())
			if openCLIResult, backend, cliFailures := t.tryCommands(ctx, []agentReachCommand{
				{backend: "OpenCLI", command: "opencli", timeout: 12 * time.Second, args: []string{"bilibili", "search", query, "-f", "yaml", "--limit", strconv.Itoa(limit)}},
			}); openCLIResult != "" {
				return formatAgentReachResult("search", platform, backend, append(failures, cliFailures...), openCLIResult), nil
			} else {
				failures = append(failures, cliFailures...)
			}
			return t.siteSearchFallback(ctx, platform, query, limit, failures)
		}
	case "twitter":
		return t.commandSearchWithFallback(ctx, platform, query, limit, []agentReachCommand{
			{backend: "twitter-cli", command: "twitter", timeout: 20 * time.Second, args: []string{"search", query, "-n", strconv.Itoa(limit)}},
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"twitter", "search", query, "-f", "yaml", "--limit", strconv.Itoa(limit)}},
		})
	case "reddit":
		return t.commandSearchWithFallback(ctx, platform, query, limit, []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"reddit", "search", query, "-f", "yaml", "--limit", strconv.Itoa(limit)}},
			{backend: "rdt-cli", command: "rdt", timeout: 20 * time.Second, args: []string{"search", query, "--limit", strconv.Itoa(limit)}},
		})
	case "xiaohongshu":
		return t.commandSearchWithFallback(ctx, platform, query, limit, []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"xiaohongshu", "search", query, "-f", "yaml", "--limit", strconv.Itoa(limit)}},
			{backend: "xhs-cli", command: "xhs", timeout: 20 * time.Second, args: []string{"search", query}},
		})
	case "xiaoyuzhou":
		if result, err := t.delegateSocial(ctx, platform, query, "", limit); err == nil {
			return formatAgentReachResult("search", platform, "OpenKitty social_trending fallback", nil, result), nil
		}
		return t.siteSearchFallback(ctx, platform, query, limit, nil)
	default:
		return t.siteSearchFallback(ctx, platform, query, limit, nil)
	}
}

func (t *AgentReachTool) searchExa(ctx context.Context, query string, limit int) (string, error) {
	result, err := t.runCommand(ctx, agentReachCommand{
		backend: "Exa via Agent Reach",
		command: "mcporter",
		timeout: 18 * time.Second,
		args: []string{
			"call", "https://mcp.exa.ai/mcp.web_search_exa",
			"query=" + query, fmt.Sprintf("numResults=%d", limit),
			"--timeout", "15000", "--output", "json",
		},
	})
	if err != nil {
		return "", err
	}
	return parseAgentReachExaOutput([]byte(result))
}

func (t *AgentReachTool) searchGitHub(ctx context.Context, query, kind string, limit int) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "auto" {
		kind = "repositories"
	}
	var args []string
	switch kind {
	case "repositories", "repos", "repo":
		args = []string{"search", "repos", query, "--limit", strconv.Itoa(limit), "--json", "fullName,description,url,stargazersCount,updatedAt"}
	case "code":
		args = []string{"search", "code", query, "--limit", strconv.Itoa(limit), "--json", "repository,path,url"}
	case "issues", "issue", "prs", "pr":
		args = []string{"search", "issues", query, "--limit", strconv.Itoa(limit), "--json", "repository,title,url,state,updatedAt"}
	default:
		return "", fmt.Errorf("unsupported GitHub search kind %q", kind)
	}
	return t.commandSearchWithFallback(ctx, "github", query, limit, []agentReachCommand{
		{backend: "gh CLI", command: "gh", timeout: 25 * time.Second, args: args},
	})
}

func (t *AgentReachTool) commandSearchWithFallback(ctx context.Context, platform, query string, limit int, candidates []agentReachCommand) (string, error) {
	if result, backend, failures := t.tryCommands(ctx, candidates); result != "" {
		return formatAgentReachResult("search", platform, backend, failures, result), nil
	} else {
		return t.siteSearchFallback(ctx, platform, query, limit, failures)
	}
}

func (t *AgentReachTool) siteSearchFallback(ctx context.Context, platform, query string, limit int, failures []string) (string, error) {
	domain := agentReachPlatformDomain(platform)
	fallbackQuery := query
	if domain != "" {
		fallbackQuery = "site:" + domain + " " + query
	}
	result, err := t.delegateWebSearch(ctx, fallbackQuery, limit)
	if err != nil {
		return "", fmt.Errorf("Agent Reach %s backends failed (%s); OpenKitty web_search fallback failed: %w",
			platform, strings.Join(failures, "; "), err)
	}
	return formatAgentReachResult("search", platform, "OpenKitty web_search fallback", failures, result), nil
}

func (t *AgentReachTool) read(ctx context.Context, platform, target, kind string) (string, error) {
	var candidates []agentReachCommand
	switch platform {
	case "twitter":
		command := "tweet"
		if strings.EqualFold(kind, "article") {
			command = "article"
		}
		candidates = []agentReachCommand{
			{backend: "twitter-cli", command: "twitter", timeout: 25 * time.Second, args: []string{command, target}},
			{backend: "OpenCLI", command: "opencli", timeout: 25 * time.Second, args: []string{"twitter", command, target, "-f", "yaml"}},
		}
	case "reddit":
		candidates = []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 25 * time.Second, args: []string{"reddit", "read", target, "-f", "yaml"}},
			{backend: "rdt-cli", command: "rdt", timeout: 25 * time.Second, args: []string{"read", target}},
		}
	case "xiaohongshu":
		candidates = []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 25 * time.Second, args: []string{"xiaohongshu", "note", target, "-f", "yaml"}},
			{backend: "xhs-cli", command: "xhs", timeout: 25 * time.Second, args: []string{"read", target}},
		}
	case "bilibili":
		if strings.EqualFold(kind, "subtitle") {
			candidates = []agentReachCommand{{backend: "OpenCLI", command: "opencli", timeout: 30 * time.Second, args: []string{"bilibili", "subtitle", target, "-f", "yaml"}}}
		} else {
			candidates = []agentReachCommand{
				{backend: "bili-cli", command: "bili", timeout: 25 * time.Second, args: []string{"video", target}},
				{backend: "OpenCLI", command: "opencli", timeout: 25 * time.Second, args: []string{"bilibili", "video", target, "-f", "yaml"}},
			}
		}
	case "youtube":
		candidates = []agentReachCommand{{backend: "yt-dlp", command: "yt-dlp", timeout: 35 * time.Second, args: []string{"--dump-single-json", "--skip-download", target}}}
	}
	if len(candidates) > 0 {
		if result, backend, failures := t.tryCommands(ctx, candidates); result != "" {
			return formatAgentReachResult("read", platform, backend, failures, result), nil
		} else if fallback, err := t.delegateWebFetch(ctx, target); err == nil {
			return formatAgentReachResult("read", platform, "OpenKitty web_fetch fallback", failures, fallback), nil
		} else {
			return "", fmt.Errorf("Agent Reach %s read failed (%s); OpenKitty web_fetch fallback failed: %w", platform, strings.Join(failures, "; "), err)
		}
	}

	// Agent Reach's generic web and LinkedIn fallback route is Jina Reader.
	// If Jina cannot read the page, preserve OpenKitty's direct fetch path.
	if isHTTPURL(target) {
		jinaTarget := "https://r.jina.ai/" + target
		if result, err := t.delegateWebFetch(ctx, jinaTarget); err == nil && strings.TrimSpace(result) != "" {
			return formatAgentReachResult("read", platform, "Jina Reader", nil, result), nil
		} else if fallback, fallbackErr := t.delegateWebFetch(ctx, target); fallbackErr == nil {
			failures := []string{}
			if err != nil {
				failures = append(failures, "Jina Reader: "+err.Error())
			}
			return formatAgentReachResult("read", platform, "OpenKitty web_fetch fallback", failures, fallback), nil
		} else {
			return "", fmt.Errorf("Jina Reader failed: %v; OpenKitty web_fetch fallback failed: %w", err, fallbackErr)
		}
	}
	return "", fmt.Errorf("agent_reach read requires an HTTP(S) URL for platform %s", platform)
}

func (t *AgentReachTool) trending(ctx context.Context, platform, target string, limit int) (string, error) {
	var candidates []agentReachCommand
	switch platform {
	case "twitter":
		candidates = []agentReachCommand{
			{backend: "twitter-cli", command: "twitter", timeout: 20 * time.Second, args: []string{"feed", "-n", strconv.Itoa(limit)}},
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"twitter", "timeline", "-f", "yaml", "--limit", strconv.Itoa(limit)}},
		}
	case "reddit":
		candidates = []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"reddit", "hot", "-f", "yaml", "--limit", strconv.Itoa(limit)}},
			{backend: "rdt-cli", command: "rdt", timeout: 20 * time.Second, args: []string{"popular", "--limit", strconv.Itoa(limit)}},
		}
	case "xiaohongshu":
		candidates = []agentReachCommand{
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"xiaohongshu", "feed", "-f", "yaml", "--limit", strconv.Itoa(limit)}},
			{backend: "xhs-cli", command: "xhs", timeout: 20 * time.Second, args: []string{"hot"}},
		}
	case "bilibili":
		candidates = []agentReachCommand{
			{backend: "bili-cli", command: "bili", timeout: 20 * time.Second, args: []string{"hot", "-n", strconv.Itoa(limit)}},
			{backend: "OpenCLI", command: "opencli", timeout: 20 * time.Second, args: []string{"bilibili", "hot", "-f", "yaml", "--limit", strconv.Itoa(limit)}},
		}
	}
	result, backend, failures := t.tryCommands(ctx, candidates)
	if result != "" {
		return formatAgentReachResult("trending", platform, backend, failures, result), nil
	}
	fallback, err := t.delegateSocial(ctx, platform, "", target, limit)
	if err != nil {
		return "", fmt.Errorf("Agent Reach %s trending backends failed (%s); OpenKitty social_trending fallback failed: %w", platform, strings.Join(failures, "; "), err)
	}
	return formatAgentReachResult("trending", platform, "OpenKitty social_trending fallback", failures, fallback), nil
}

func (t *AgentReachTool) transcribe(ctx context.Context, platform, target, provider string) (string, error) {
	result, backend, failures := t.tryCommands(ctx, []agentReachCommand{{
		backend: "Agent Reach transcription", command: "agent-reach", timeout: 15 * time.Minute,
		args: []string{"transcribe", target, "--provider", provider},
	}})
	if result == "" {
		return "", fmt.Errorf("Agent Reach transcription is unavailable: %s", strings.Join(failures, "; "))
	}
	return formatAgentReachResult("transcribe", platform, backend, failures, result), nil
}

func (t *AgentReachTool) delegateWebSearch(ctx context.Context, query string, limit int) (string, error) {
	if t == nil || t.WebSearch == nil {
		return "", fmt.Errorf("web_search fallback is not configured")
	}
	return t.WebSearch.Execute(ctx, map[string]interface{}{"query": query, "maxResults": limit})
}

func (t *AgentReachTool) delegateWebFetch(ctx context.Context, target string) (string, error) {
	if t == nil || t.WebFetch == nil {
		return "", fmt.Errorf("web_fetch fallback is not configured")
	}
	return t.WebFetch.Execute(ctx, map[string]interface{}{"url": target})
}

func (t *AgentReachTool) delegateSocial(ctx context.Context, platform, query, target string, limit int) (string, error) {
	if t == nil || t.Social == nil {
		return "", fmt.Errorf("social_trending fallback is not configured")
	}
	params := map[string]interface{}{"source": platform, "limit": limit}
	if query != "" {
		params["query"] = query
	}
	if target != "" {
		params["url"] = target
	}
	return t.Social.Execute(ctx, params)
}

func (t *AgentReachTool) searchBilibiliAPI(ctx context.Context, query string, limit int) (string, error) {
	base := strings.TrimSpace(t.BilibiliSearchURL)
	if base == "" {
		return "", fmt.Errorf("Bilibili search API is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("keyword", query)
	values.Set("page", "1")
	parsed.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", agentReachUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := t.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Bilibili search API returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Result []struct {
				ResultType string `json:"result_type"`
				Data       []struct {
					BVID        string          `json:"bvid"`
					Title       string          `json:"title"`
					Author      string          `json:"author"`
					Description string          `json:"description"`
					ArcURL      string          `json:"arcurl"`
					Play        json.RawMessage `json:"play"`
					PubDate     int64           `json:"pubdate"`
				} `json:"data"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Code != 0 {
		return "", fmt.Errorf("Bilibili search API code=%d message=%s", payload.Code, payload.Message)
	}
	stripTags := regexp.MustCompile(`<[^>]+>`)
	var out strings.Builder
	out.WriteString("## Bilibili search · " + query + "\n\n")
	count := 0
	for _, group := range payload.Data.Result {
		if group.ResultType != "video" {
			continue
		}
		for _, item := range group.Data {
			if count >= limit {
				break
			}
			title := strings.TrimSpace(html.UnescapeString(stripTags.ReplaceAllString(item.Title, "")))
			if title == "" {
				continue
			}
			link := strings.TrimSpace(item.ArcURL)
			if link == "" && item.BVID != "" {
				link = "https://www.bilibili.com/video/" + item.BVID
			}
			count++
			fmt.Fprintf(&out, "%d. **%s**\n", count, title)
			fmt.Fprintf(&out, "   Author: %s", strings.TrimSpace(item.Author))
			if item.PubDate > 0 {
				fmt.Fprintf(&out, " | Published: %s", time.Unix(item.PubDate, 0).Format("2006-01-02"))
			}
			if link != "" {
				fmt.Fprintf(&out, " | %s", link)
			}
			out.WriteString("\n\n")
		}
	}
	if count == 0 {
		return "", fmt.Errorf("Bilibili search API returned no video results")
	}
	return strings.TrimSpace(out.String()), nil
}

func (t *AgentReachTool) doctor(ctx context.Context, platform string, refresh bool) (string, error) {
	t.doctorMu.Lock()
	defer t.doctorMu.Unlock()
	Now := t.Now()
	if !refresh && t.doctorCache.output != "" && Now.Sub(t.doctorCache.checkedAt) < agentReachDoctorCacheTTL {
		return filterAgentReachDoctorOutput(t.doctorCache.output, platform, true, t.nativeFallbackNames()), nil
	}

	var raw string
	path, err := t.LookPath("agent-reach")
	if err != nil {
		raw = `{"agent_reach":{"status":"off","message":"agent-reach CLI is not installed; OpenKitty native fallbacks remain available"}}`
	} else {
		doctorCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		output, runErr := t.Runner(doctorCtx, path, "doctor", "--json")
		cancel()
		if runErr != nil {
			rawBytes, _ := json.Marshal(map[string]interface{}{
				"agent_reach": map[string]interface{}{"status": "error", "message": compactAgentReachFailure(string(output), runErr)},
			})
			raw = string(rawBytes)
		} else if !json.Valid(output) {
			rawBytes, _ := json.Marshal(map[string]interface{}{
				"agent_reach": map[string]interface{}{"status": "error", "message": "agent-reach doctor returned invalid JSON"},
			})
			raw = string(rawBytes)
		} else {
			raw = strings.TrimSpace(string(output))
		}
	}
	t.doctorCache = agentReachDoctorCache{checkedAt: Now, output: raw}
	return filterAgentReachDoctorOutput(raw, platform, false, t.nativeFallbackNames()), nil
}

func (t *AgentReachTool) nativeFallbackNames() []string {
	var names []string
	if t.WebSearch != nil {
		names = append(names, "web_search")
	}
	if t.WebFetch != nil {
		names = append(names, "web_fetch")
	}
	if t.Social != nil {
		names = append(names, "social_trending")
	}
	return names
}

func filterAgentReachDoctorOutput(raw, platform string, cached bool, fallbacks []string) string {
	channels := make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(raw), &channels); err != nil {
		channels["agent_reach"] = json.RawMessage(`{"status":"error","message":"unable to parse doctor output"}`)
	}
	if platform != "" && platform != "auto" {
		key := platform
		if platform == "exa" {
			key = "exa_search"
		}
		if selected, ok := channels[key]; ok {
			channels = map[string]json.RawMessage{key: selected}
		}
	}
	payload := map[string]interface{}{
		"channels": channels,
		"openkitty": map[string]interface{}{
			"integration":      "agent_reach read-only router",
			"native_fallbacks": fallbacks,
			"cached":           cached,
			"cache_ttl":        agentReachDoctorCacheTTL.String(),
		},
	}
	formatted, _ := json.MarshalIndent(payload, "", "  ")
	return string(formatted)
}

func compactAgentReachFailure(output string, err error) string {
	message := strings.TrimSpace(output)
	if message == "" && err != nil {
		message = err.Error()
	}
	return truncate(message, 1200)
}

func formatAgentReachResult(action, platform, backend string, failures []string, content string) string {
	var out strings.Builder
	out.WriteString("## Agent Reach route\n\n")
	fmt.Fprintf(&out, "- Action: %s\n- Platform: %s\n- Backend: %s\n", action, platform, backend)
	if len(failures) > 0 {
		out.WriteString("- Fallback reason: " + strings.Join(failures, "; ") + "\n")
	}
	out.WriteString("\n" + strings.TrimSpace(content))
	return strings.TrimSpace(out.String())
}

func normalizeAgentReachPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	switch platform {
	case "x", "x.com", "推特":
		return "twitter"
	case "b站", "哔哩哔哩":
		return "bilibili"
	case "小红书", "xhs":
		return "xiaohongshu"
	case "领英":
		return "linkedin"
	case "小宇宙":
		return "xiaoyuzhou"
	case "雪球":
		return "xueqiu"
	default:
		return platform
	}
}

func inferAgentReachPlatform(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return "web"
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "x.com" || strings.HasSuffix(host, ".x.com") || strings.Contains(host, "twitter.com"):
		return "twitter"
	case strings.Contains(host, "youtube.com") || host == "youtu.be":
		return "youtube"
	case strings.Contains(host, "bilibili.com") || host == "b23.tv":
		return "bilibili"
	case strings.Contains(host, "reddit.com") || host == "redd.it":
		return "reddit"
	case strings.Contains(host, "xiaohongshu.com") || host == "xhslink.com":
		return "xiaohongshu"
	case strings.Contains(host, "github.com"):
		return "github"
	case strings.Contains(host, "v2ex.com"):
		return "v2ex"
	case strings.Contains(host, "linkedin.com"):
		return "linkedin"
	case strings.Contains(host, "xiaoyuzhoufm.com"):
		return "xiaoyuzhou"
	case strings.Contains(host, "xueqiu.com"):
		return "xueqiu"
	default:
		return "web"
	}
}

func agentReachPlatformDomain(platform string) string {
	switch platform {
	case "github":
		return "github.com"
	case "youtube":
		return "youtube.com"
	case "bilibili":
		return "bilibili.com"
	case "twitter":
		return "x.com"
	case "reddit":
		return "reddit.com"
	case "xiaohongshu":
		return "xiaohongshu.com"
	case "v2ex":
		return "v2ex.com"
	case "linkedin":
		return "linkedin.com"
	case "xiaoyuzhou":
		return "xiaoyuzhoufm.com"
	case "xueqiu":
		return "xueqiu.com"
	default:
		return ""
	}
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

// IsHTTPURL reports whether value is an absolute HTTP or HTTPS URL.
func IsHTTPURL(value string) bool {
	return isHTTPURL(value)
}

func clampLimit(params map[string]interface{}, defaultValue, maximum int) int {
	value, ok := params["limit"]
	if !ok {
		return defaultValue
	}
	var limit int
	switch number := value.(type) {
	case float64:
		limit = int(number)
	case int:
		limit = number
	default:
		return defaultValue
	}
	if limit < 1 {
		return 1
	}
	if limit > maximum {
		return maximum
	}
	return limit
}

func truncate(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

type agentReachExaResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func parseAgentReachExaOutput(output []byte) (string, error) {
	var response agentReachExaResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("invalid Exa JSON: %w", err)
	}
	parts := make([]string, 0, len(response.Content))
	for _, item := range response.Content {
		if strings.EqualFold(item.Type, "text") && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("Exa returned no text results")
	}
	return strings.Join(parts, "\n\n"), nil
}
