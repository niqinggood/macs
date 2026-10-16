package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mac/internal/routing"
	networktools "mac/internal/tools/network"
)

type AgentReachTool = networktools.AgentReachTool

var NewAgentReachTool = networktools.NewAgentReachTool

type agentReachDelegateStub struct {
	name   string
	result string
	err    error
	calls  []map[string]interface{}
}

func (s *agentReachDelegateStub) Name() string { return s.name }

func (s *agentReachDelegateStub) Description() string { return "Agent Reach test delegate" }

func (s *agentReachDelegateStub) Parameters() map[string]interface{} { return nil }

func (s *agentReachDelegateStub) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	copyParams := make(map[string]interface{}, len(params))
	for key, value := range params {
		copyParams[key] = value
	}
	s.calls = append(s.calls, copyParams)
	return s.result, s.err
}

func TestAgentReachToolContractAndEvidenceRouting(t *testing.T) {
	tool := NewAgentReachTool(nil, nil, nil)
	if tool.Name() != "agent_reach" || !tool.IsReadOnly() || !tool.ResultIsContentPayload() {
		t.Fatalf("unexpected Agent Reach contract: name=%q read_only=%v content_payload=%v", tool.Name(), tool.IsReadOnly(), tool.ResultIsContentPayload())
	}
	if !CoreTools[tool.Name()] || !isReadOnlyTool(tool.Name()) || !isExternalEvidenceToolName(tool.Name()) || !isSearchToolName(tool.Name()) {
		t.Fatalf("Agent Reach routing flags are inconsistent: core=%v read_only=%v evidence=%v search=%v",
			CoreTools[tool.Name()], isReadOnlyTool(tool.Name()), isExternalEvidenceToolName(tool.Name()), isSearchToolName(tool.Name()))
	}
	if got := searchModeForToolCall(ToolCall{Name: tool.Name(), Arguments: map[string]interface{}{"action": "search"}}); got != "discovery" {
		t.Fatalf("search mode = %q, want discovery", got)
	}
	readCall := ToolCall{Name: tool.Name(), Arguments: map[string]interface{}{"action": "read", "target": "https://example.com/article"}}
	if got := searchModeForToolCall(readCall); got != "direct" {
		t.Fatalf("read mode = %q, want direct", got)
	}
	if got := sourceURLForToolCall(readCall); got != "https://example.com/article" {
		t.Fatalf("read source = %q", got)
	}
	if got := searchModeForToolCall(ToolCall{Name: tool.Name(), Arguments: map[string]interface{}{"action": "doctor"}}); got != "" {
		t.Fatalf("doctor must not be evidence, got mode %q", got)
	}
	doctorCall := ToolCall{Name: tool.Name(), Arguments: map[string]interface{}{"action": "doctor"}}
	if isExternalEvidenceToolCall(doctorCall) {
		t.Fatal("Agent Reach doctor call must not be classified as external evidence")
	}
	doctorResult := toolResultForFinalization(doctorCall, `{"status":"ok","url":"https://mcp.exa.ai","checked_at":"2026-08-13"}`, true)
	doctorRecord, ok := parseOpenKittyToolRecord(doctorResult)
	if !ok || isExternalEvidenceRecord(doctorRecord) || len(doctorRecord.EvidenceHints) != 0 || len(doctorRecord.EvidenceDateKeys) != 0 {
		t.Fatalf("doctor output leaked into evidence metadata: record=%#v ok=%v", doctorRecord, ok)
	}
	if got := externalEvidenceText([]string{doctorResult}); got != "" {
		t.Fatalf("doctor output entered final evidence: %q", got)
	}
	doctorMessage := buildToolResultAPIMessageForCall(doctorCall, ToolResult{Name: tool.Name(), Content: "doctor output"}, true)
	if strings.Contains(fmt.Sprint(doctorMessage["content"]), "[EVIDENCE BOUNDARY]") {
		t.Fatal("doctor result must not carry the retrieval evidence boundary")
	}
	searchCall := ToolCall{Name: tool.Name(), Arguments: map[string]interface{}{"action": "search", "query": "OpenKitty"}}
	if !isExternalEvidenceToolCall(searchCall) {
		t.Fatal("Agent Reach search call should be classified as external evidence")
	}
	searchMessage := buildToolResultAPIMessageForCall(searchCall, ToolResult{Name: tool.Name(), Content: "result"}, true)
	if !strings.Contains(fmt.Sprint(searchMessage["content"]), "[EVIDENCE BOUNDARY]") {
		t.Fatal("search result is missing the retrieval evidence boundary")
	}
}

func TestNewLLMAgentRegistersAgentReachWithoutReplacingNativeTools(t *testing.T) {
	workspace := t.TempDir()
	model := ModelConfig{ModelName: "test-model", BaseURL: "http://127.0.0.1:1", APIKey: "test"}
	config := &Config{Workspace: workspace, ModelConfig: map[string]ModelConfig{"test": model}}
	config.MemoryConfig.Enabled = false
	config.SkillsConfig.DisEnabled = true
	config.ToolsConfig.Enabled = true
	memoryDisabled := false
	agent := NewLLMAgent(AgentConfig{
		Name: "agent-reach-registration", Type: AgentTypeCustom, Model: "test", HasMemory: &memoryDisabled,
	}, &model, nil, config, log.New(io.Discard, "", 0))
	t.Cleanup(func() {
		if agent.ptcServer != nil {
			agent.ptcServer.Stop()
		}
		if agent.lspManager != nil {
			agent.lspManager.Close()
		}
	})
	for _, name := range []string{"agent_reach", "web_search", "web_fetch", "social_trending"} {
		if _, ok := agent.toolRegistry.Get(name); !ok {
			t.Fatalf("NewLLMAgent registry missing %s", name)
		}
	}
	tool, _ := agent.toolRegistry.Get("agent_reach")
	router, ok := tool.(*AgentReachTool)
	if !ok || router.WebFetch == nil || router.WebSearch == nil || router.Social == nil {
		t.Fatalf("registered Agent Reach delegates are incomplete: %#v", tool)
	}
}

func TestAgentReachIsAutoModeAndPlanModeReadOnly(t *testing.T) {
	classifier := NewAutoModeClassifier()
	classifier.Enable()
	allowed, reason := classifier.Classify("agent_reach", map[string]interface{}{
		"action": "search", "platform": "github", "query": "openkitty",
	})
	if !allowed || !strings.Contains(reason, "read-only") {
		t.Fatalf("auto mode should accept Agent Reach as read-only: allowed=%v reason=%q", allowed, reason)
	}
	allowed, confirm, reason := ResolvePermission(PermissionPlan, "agent_reach", map[string]interface{}{"action": "read", "target": "https://example.com"})
	if !allowed || confirm || reason != "" {
		t.Fatalf("plan mode should preserve read-only Agent Reach: allowed=%v confirm=%v reason=%q", allowed, confirm, reason)
	}
}

func TestAgentReachAutomaticIntentIsPrecise(t *testing.T) {
	positive := []string{
		"全网调研 OpenKitty 的评价",
		"搜索 GitHub 上的 checkpoint 代码",
		"读一下 https://example.com/article",
		"search the web for agent harness patterns",
		"看看小红书上怎么评价这个产品",
	}
	for _, query := range positive {
		if !routing.AgentReachAutomaticIntent(query) {
			t.Fatalf("expected Agent Reach intent for %q", query)
		}
	}
	negative := []string{
		"查找当前项目代码中的 TODO",
		"搜索工作区里的 checkpoint 实现，不要访问网络",
		"分析这份本地报告并翻译成英文",
		"找一下 README 里的机械状态骨架",
	}
	for _, query := range negative {
		if routing.AgentReachAutomaticIntent(query) {
			t.Fatalf("local-only query must not trigger Agent Reach: %q", query)
		}
	}
}

func TestAgentReachGitHubSearchUsesCLIWhenAvailable(t *testing.T) {
	fallback := &agentReachDelegateStub{name: "web_search", result: "fallback"}
	tool := NewAgentReachTool(nil, fallback, nil)
	tool.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return "gh", nil
		}
		return "", errors.New("not found")
	}
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command != "gh" {
			t.Fatalf("command = %q, want gh", command)
		}
		want := []string{"search", "repos", "checkpoint recovery", "--limit", "4", "--json", "fullName,description,url,stargazersCount,updatedAt"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args = %#v, want %#v", args, want)
		}
		return []byte(`[{"fullName":"openkitty/example","url":"https://github.com/openkitty/example"}]`), nil
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "github", "query": "checkpoint recovery", "limit": 4,
	})
	if err != nil {
		t.Fatalf("Agent Reach GitHub search failed: %v", err)
	}
	if !strings.Contains(result, "Backend: gh CLI") || !strings.Contains(result, "https://github.com/openkitty/example") {
		t.Fatalf("CLI route not exposed in result:\n%s", result)
	}
	if len(fallback.calls) != 0 {
		t.Fatalf("native fallback should not run after successful gh call: %#v", fallback.calls)
	}
}

func TestAgentReachMissingCLIUsesExistingWebSearch(t *testing.T) {
	fallback := &agentReachDelegateStub{name: "web_search", result: "1. OpenKitty\n   https://github.com/openkitty/example"}
	tool := NewAgentReachTool(nil, fallback, nil)
	tool.LookPath = func(string) (string, error) { return "", errors.New("not installed") }

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "github", "query": "durable checkpoint", "limit": 6,
	})
	if err != nil {
		t.Fatalf("fallback search failed: %v", err)
	}
	if !strings.Contains(result, "Backend: OpenKitty web_search fallback") || !strings.Contains(result, "gh is not installed") {
		t.Fatalf("fallback provenance missing:\n%s", result)
	}
	if len(fallback.calls) != 1 || fallback.calls[0]["query"] != "site:github.com durable checkpoint" {
		t.Fatalf("unexpected fallback calls: %#v", fallback.calls)
	}
}

func TestAgentReachExaUsesPortableMcporterInvocation(t *testing.T) {
	fallback := &agentReachDelegateStub{name: "web_search", result: "fallback"}
	tool := NewAgentReachTool(nil, fallback, nil)
	tool.LookPath = func(name string) (string, error) {
		switch name {
		case "mcporter":
			return "mcporter", nil
		default:
			return "", errors.New("not installed")
		}
	}
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command != "mcporter" {
			t.Fatalf("command = %q, want mcporter", command)
		}
		if !containsString(args, "query=portable routing") || !containsString(args, "numResults=3") {
			t.Fatalf("mcporter args missing query/count: %#v", args)
		}
		return []byte(`{"content":[{"type":"text","text":"1. Portable result https://example.com/result"}]}`), nil
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "exa", "query": "portable routing", "limit": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Backend: Exa via Agent Reach") || !strings.Contains(result, "https://example.com/result") {
		t.Fatalf("unexpected Exa result:\n%s", result)
	}
	if len(fallback.calls) != 0 {
		t.Fatalf("web fallback should not run after Exa success: %#v", fallback.calls)
	}
}

func TestAgentReachBilibiliSearchUsesPublicAPIBeforeWebFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("keyword") != "Agent harness" {
			t.Fatalf("keyword = %q", r.URL.Query().Get("keyword"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"result":[{"result_type":"video","data":[{"bvid":"BV1test","title":"<em class=\"keyword\">Agent</em> harness","author":"Kitty","arcurl":"https://www.bilibili.com/video/BV1test","pubdate":1786377600}]}]}}`)
	}))
	defer server.Close()

	fallback := &agentReachDelegateStub{name: "web_search", result: "unexpected fallback"}
	tool := NewAgentReachTool(nil, fallback, nil)
	tool.LookPath = func(string) (string, error) { return "", errors.New("not installed") }
	tool.BilibiliSearchURL = server.URL
	tool.Client = server.Client()

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "bilibili", "query": "Agent harness", "limit": 5,
	})
	if err != nil {
		t.Fatalf("Bilibili API route failed: %v", err)
	}
	for _, want := range []string{"Backend: Bilibili search API", "Agent harness", "https://www.bilibili.com/video/BV1test"} {
		if !strings.Contains(result, want) {
			t.Fatalf("Bilibili result missing %q:\n%s", want, result)
		}
	}
	if len(fallback.calls) != 0 {
		t.Fatalf("web fallback should not run after API success: %#v", fallback.calls)
	}
}

func TestAgentReachBilibiliPublicAPIComesBeforeOpenCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"result":[{"result_type":"video","data":[{"bvid":"BV1fast","title":"Fast API route","author":"Kitty","arcurl":"https://www.bilibili.com/video/BV1fast"}]}]}}`)
	}))
	defer server.Close()

	tool := NewAgentReachTool(nil, &agentReachDelegateStub{name: "web_search", result: "fallback"}, nil)
	tool.BilibiliSearchURL = server.URL
	tool.Client = server.Client()
	tool.LookPath = func(name string) (string, error) {
		if name == "opencli" {
			return "opencli", nil
		}
		return "", errors.New("not installed")
	}
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		t.Fatalf("OpenCLI must not run when the public Bilibili API is healthy: command=%s args=%v", command, args)
		return nil, nil
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "bilibili", "query": "fast", "limit": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Backend: Bilibili search API") {
		t.Fatalf("unexpected route:\n%s", result)
	}
}

func TestAgentReachLiveBilibiliRoute(t *testing.T) {
	if os.Getenv("OPENKITTY_AGENT_REACH_LIVE") != "1" {
		t.Skip("set OPENKITTY_AGENT_REACH_LIVE=1 to run the live Agent Reach route")
	}
	fetch := NewWebFetchTool(5, "", false, nil, false)
	search := NewWebSearchTool(fetch)
	social := networktools.NewSocialTrendingTool(false)
	tool := NewAgentReachTool(fetch, search, social)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "bilibili", "query": "OpenAI", "limit": 3,
	})
	if err != nil {
		t.Fatalf("live Bilibili route failed: %v", err)
	}
	if !strings.Contains(result, "Backend:") || !strings.Contains(result, "bilibili.com/video/") {
		t.Fatalf("live Bilibili route returned no attributable video results:\n%s", result)
	}
}

func TestAgentReachLiveDoctorRoute(t *testing.T) {
	if os.Getenv("OPENKITTY_AGENT_REACH_LIVE") != "1" {
		t.Skip("set OPENKITTY_AGENT_REACH_LIVE=1 to run the live Agent Reach route")
	}
	tool := NewAgentReachTool(
		&agentReachDelegateStub{name: "web_fetch"},
		&agentReachDelegateStub{name: "web_search"},
		&agentReachDelegateStub{name: "social_trending"},
	)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "doctor"})
	if err != nil {
		t.Fatalf("live Agent Reach doctor failed: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("live doctor returned invalid JSON: %v\n%s", err, result)
	}
	for _, want := range []string{`"channels"`, `"bilibili"`, `"native_fallbacks"`} {
		if !strings.Contains(result, want) {
			t.Fatalf("live doctor missing %s:\n%s", want, result)
		}
	}
}

func TestAgentReachLiveExaRoute(t *testing.T) {
	if os.Getenv("OPENKITTY_AGENT_REACH_LIVE") != "1" {
		t.Skip("set OPENKITTY_AGENT_REACH_LIVE=1 to run the live Agent Reach route")
	}
	fallback := &agentReachDelegateStub{name: "web_search", result: "fallback"}
	tool := NewAgentReachTool(nil, fallback, nil)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "exa", "query": "OpenKitty agent framework", "limit": 2,
	})
	if err != nil {
		t.Fatalf("live Exa route failed: %v", err)
	}
	if !strings.Contains(result, "Backend: Exa via Agent Reach") || !strings.Contains(result, "https://") {
		t.Fatalf("live Exa route returned no attributable results:\n%s", result)
	}
	if len(fallback.calls) != 0 {
		t.Fatalf("native fallback ran despite live Exa success: %#v", fallback.calls)
	}
}

func TestAgentReachLiveOpenCLIProbe(t *testing.T) {
	if os.Getenv("OPENKITTY_AGENT_REACH_LIVE") != "1" {
		t.Skip("set OPENKITTY_AGENT_REACH_LIVE=1 to run the live Agent Reach route")
	}
	tool := NewAgentReachTool(nil, nil, nil)
	ready, reason := tool.OpenCLIAvailable(context.Background())
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("OpenCLI probe returned no status detail (ready=%v)", ready)
	}
	t.Logf("OpenCLI ready=%v reason=%s", ready, reason)
}

func TestAgentReachTrendingFallsBackToExistingSocialTool(t *testing.T) {
	social := &agentReachDelegateStub{name: "social_trending", result: "## Reddit hot\n\n1. item"}
	tool := NewAgentReachTool(nil, nil, social)
	tool.LookPath = func(string) (string, error) { return "", errors.New("not installed") }

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "trending", "platform": "reddit", "limit": 7,
	})
	if err != nil {
		t.Fatalf("social fallback failed: %v", err)
	}
	if !strings.Contains(result, "Backend: OpenKitty social_trending fallback") {
		t.Fatalf("social fallback route missing:\n%s", result)
	}
	if len(social.calls) != 1 || social.calls[0]["source"] != "reddit" || social.calls[0]["limit"] != 7 {
		t.Fatalf("unexpected social fallback args: %#v", social.calls)
	}
}

func TestAgentReachSkipsDisconnectedOpenCLIWithoutPlatformTimeout(t *testing.T) {
	social := &agentReachDelegateStub{name: "social_trending", result: "## Reddit hot\n\n1. item"}
	tool := NewAgentReachTool(nil, nil, social)
	tool.LookPath = func(name string) (string, error) {
		if name == "opencli" {
			return "opencli", nil
		}
		return "", errors.New("not installed")
	}
	runs := 0
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		runs++
		if command != "opencli" || !reflect.DeepEqual(args, []string{"daemon", "status"}) {
			t.Fatalf("disconnected OpenCLI must only run its bounded status probe: command=%s args=%v", command, args)
		}
		return []byte("Daemon: running\nExtension: disconnected\n"), nil
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "trending", "platform": "reddit", "limit": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || !strings.Contains(result, "OpenCLI skipped: browser extension is not connected") ||
		!strings.Contains(result, "Backend: OpenKitty social_trending fallback") {
		t.Fatalf("OpenCLI fast-skip/fallback missing (runs=%d):\n%s", runs, result)
	}
}

func TestAgentReachUsesConnectedOpenCLI(t *testing.T) {
	tool := NewAgentReachTool(nil, &agentReachDelegateStub{name: "web_search", result: "fallback"}, nil)
	tool.LookPath = func(name string) (string, error) {
		if name == "opencli" {
			return "opencli", nil
		}
		return "", errors.New("not installed")
	}
	runs := 0
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		runs++
		if reflect.DeepEqual(args, []string{"daemon", "status"}) {
			return []byte("Daemon: running\nExtension: connected\n"), nil
		}
		if len(args) >= 3 && args[0] == "reddit" && args[1] == "search" && args[2] == "agent systems" {
			return []byte("- title: Agent systems\n  url: https://reddit.com/r/test/1"), nil
		}
		t.Fatalf("unexpected OpenCLI invocation: command=%s args=%v", command, args)
		return nil, nil
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "search", "platform": "reddit", "query": "agent systems", "limit": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 2 || !strings.Contains(result, "Backend: OpenCLI") || !strings.Contains(result, "https://reddit.com/r/test/1") {
		t.Fatalf("connected OpenCLI route missing (runs=%d):\n%s", runs, result)
	}
}

func TestAgentReachDoctorCachesAndFiltersChannel(t *testing.T) {
	tool := NewAgentReachTool(nil, &agentReachDelegateStub{name: "web_search"}, &agentReachDelegateStub{name: "social_trending"})
	tool.WebFetch = &agentReachDelegateStub{name: "web_fetch"}
	tool.LookPath = func(name string) (string, error) {
		if name == "agent-reach" {
			return "agent-reach", nil
		}
		return "", errors.New("not installed")
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	tool.Now = func() time.Time { return now }
	runs := 0
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		runs++
		return []byte(`{"github":{"status":"warn","name":"GitHub","message":"not authenticated","active_backend":"gh CLI"},"web":{"status":"ok","name":"Web","message":"ready","active_backend":"Jina Reader"}}`), nil
	}

	first, err := tool.Execute(context.Background(), map[string]interface{}{"action": "doctor", "platform": "github"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(context.Background(), map[string]interface{}{"action": "doctor", "platform": "github"})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("doctor process runs = %d, want 1 due to cache", runs)
	}
	if strings.Contains(first, `"web"`) || !strings.Contains(first, `"github"`) || !strings.Contains(first, `"cached": false`) {
		t.Fatalf("first filtered doctor result unexpected:\n%s", first)
	}
	if !strings.Contains(second, `"cached": true`) || !strings.Contains(second, "web_search") || !strings.Contains(second, "web_fetch") || !strings.Contains(second, "social_trending") {
		t.Fatalf("cached doctor/fallback metadata missing:\n%s", second)
	}
}

func TestAgentReachDoctorWithoutPlatformReturnsAllChannels(t *testing.T) {
	tool := NewAgentReachTool(nil, nil, nil)
	tool.LookPath = func(name string) (string, error) {
		if name == "agent-reach" {
			return "agent-reach", nil
		}
		return "", errors.New("not installed")
	}
	tool.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		return []byte(`{"github":{"status":"ok","name":"GitHub","message":"ready","active_backend":"gh CLI"},"web":{"status":"ok","name":"Web","message":"ready","active_backend":"Jina Reader"}}`), nil
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "doctor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"github"`) || !strings.Contains(result, `"web"`) {
		t.Fatalf("unfiltered doctor should return all channels:\n%s", result)
	}
}

func TestAgentReachBuiltinSkillRoutesAndMaterializesReferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatalf("load builtin skills: %v", err)
	}
	skill, err := loader.GetSkill("agent-reach")
	if err != nil {
		t.Fatalf("agent-reach builtin skill missing: %v", err)
	}
	if !skill.RequiresRuntime {
		t.Fatal("agent-reach references and diagnostic script should be materialized on use")
	}
	top := loader.GetTopSkillsForQuery("看看 Reddit 上大家怎么评价 OpenKitty，再搜一下 GitHub 代码", 8)
	if !containsString(top, "agent-reach") {
		t.Fatalf("platform research should route to agent-reach, got %v", top)
	}
	for _, localQuery := range []string{
		"查找当前项目代码中的 TODO 并修复",
		"搜索工作区里的 checkpoint 实现，不要访问网络",
		"分析这份本地报告并翻译成英文",
	} {
		if localTop := loader.GetTopSkillsForQuery(localQuery, 8); containsString(localTop, "agent-reach") {
			t.Fatalf("local-only query %q must not auto-route to Agent Reach, got %v", localQuery, localTop)
		}
	}
	if _, err := loader.EnsureSkillRuntime(skill); err != nil {
		t.Fatalf("materialize agent-reach runtime: %v", err)
	}
	for _, relative := range []string{"references/search.md", "references/social.md", "references/web-video.md", "scripts/doctor.py"} {
		info, err := os.Stat(filepath.Join(skill.RuntimeDir, filepath.FromSlash(relative)))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("runtime resource %s missing/empty: info=%v err=%v", relative, info, err)
		}
	}
}

func TestAgentReachDoctorOutputIsValidJSONWhenCLIIsMissing(t *testing.T) {
	tool := NewAgentReachTool(nil, nil, nil)
	tool.LookPath = func(string) (string, error) { return "", errors.New("missing") }
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "doctor"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("doctor fallback is not JSON: %v\n%s", err, result)
	}
	if !strings.Contains(result, "native_fallbacks") || !strings.Contains(result, "agent_reach") {
		t.Fatalf("doctor fallback metadata missing:\n%s", result)
	}
}

func TestDoctorAgentReachKeepsPartialBackendOutageAsWarning(t *testing.T) {
	registry := NewToolRegistry()
	router := NewAgentReachTool(
		&agentReachDelegateStub{name: "web_fetch"},
		&agentReachDelegateStub{name: "web_search"},
		&agentReachDelegateStub{name: "social_trending"},
	)
	router.LookPath = func(name string) (string, error) {
		if name == "agent-reach" {
			return "agent-reach", nil
		}
		return "", errors.New("not installed")
	}
	router.Runner = func(_ context.Context, command string, args ...string) ([]byte, error) {
		return []byte(`{"web":{"status":"ok","name":"Web","message":"ready","active_backend":"Jina Reader"},"linkedin":{"status":"off","name":"LinkedIn","message":"MCP not configured","active_backend":null}}`), nil
	}
	registry.Register(router)
	agent := &LLMAgent{toolRegistry: registry}
	check := NewDoctorTool(agent).CheckAgentReach(context.Background())
	if check.Status != "warn" {
		t.Fatalf("partial Agent Reach outage with native fallbacks should be warn, got %s details=%v", check.Status, check.Details)
	}
	joined := strings.Join(check.Details, "\n")
	if !strings.Contains(joined, "OpenKitty fallbacks: web_search, web_fetch, social_trending") || !strings.Contains(joined, "linkedin: off") {
		t.Fatalf("doctor detail missing fallbacks/channel state:\n%s", joined)
	}
}
