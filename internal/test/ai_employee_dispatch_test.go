package app

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIEmployeePreflightResolvesBareWorkspaceFile(t *testing.T) {
	tmpDir := t.TempDir()
	dataPath := filepath.Join(tmpDir, "diamonds.csv")
	if err := os.WriteFile(dataPath, []byte("carat,price\n0.23,326\n"), 0644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	gw := &GatewayService{config: &Config{Workspace: tmpDir}}
	mgr := NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	got := mgr.buildAIEmployeePreflightContext(&AIEmployeeConfig{ID: "data-analyst"}, "针对你自己整理的 diamonds.csv 数据，整理成 ppt 报告")

	for _, want := range []string{"AI员工任务预解析", "diamonds.csv", dataPath, "Cold data"} {
		if !strings.Contains(got, want) {
			t.Fatalf("preflight should contain %q, got:\n%s", want, got)
		}
	}
}

func TestAIEmployeePreflightDoesNotAffectInvestmentAdvisor(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "diamonds.csv"), []byte("carat,price\n0.23,326\n"), 0644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	gw := &GatewayService{config: &Config{Workspace: tmpDir}}
	mgr := NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	got := mgr.buildAIEmployeePreflightContext(&AIEmployeeConfig{ID: "investment-advisor"}, "分析 diamonds.csv 里的投资机会")
	if got != "" {
		t.Fatalf("investment advisor should not receive AI employee preflight context, got:\n%s", got)
	}
}

func TestExtractLocalFileMentionsStopsBeforeChineseSentenceAfterWindowsPath(t *testing.T) {
	got := extractLocalFileMentions(`在E盘下有一个E:\DEV\ai-node是我的一个项目，你帮我找潜客`)
	if len(got) != 1 || got[0] != `E:\DEV\ai-node` {
		t.Fatalf("mentions = %#v, want exact current project path", got)
	}
}

func TestResolveLocalFileMentionAcceptsExplicitDirectory(t *testing.T) {
	dir := t.TempDir()
	got := resolveLocalFileMention(filepath.Dir(dir), dir, 3)
	if len(got) != 1 || !got[0].IsDir || filepath.Clean(got[0].Path) != filepath.Clean(dir) {
		t.Fatalf("resolved directory = %#v, want %s", got, dir)
	}
}

func TestDispatchFailsBeforePipelineWhenAPIKeyMissing(t *testing.T) {
	oldRegistry := globalProviderRegistry
	defer func() { globalProviderRegistry = oldRegistry }()

	globalProviderRegistry = &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"minimax": {
				Name:    "minimax",
				Label:   "MiniMax",
				Type:    "minimax",
				BaseURL: "https://api.minimaxi.com/v1",
				APIKey:  "YOUR_MINIMAX_API_KEY_HERE",
				Models: []ProviderModel{{
					Name:       "minimax-m3",
					Capability: "text-to-text",
				}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}

	logger := log.New(io.Discard, "", 0)
	gw := &GatewayService{logger: logger}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, logger)
	mgr := NewAIEmployeeManager(gw, logger)
	gw.aeManager = mgr
	mgr.Register([]AIEmployeeConfig{{
		ID:           "investment-advisor",
		Name:         "投资顾问",
		AtName:       "投资顾问",
		Role:         "analysis",
		ModelBinding: `{"text-to-text":"minimax-m3"}`,
		Direct:       &DirectConfig{Prompt: "answer", MaxIter: 1},
	}})

	record, err := mgr.Dispatch(context.Background(), "investment-advisor", "分析下新安股份的后市展望", "session-1")
	if err == nil {
		t.Fatal("expected missing api key error")
	}
	if record != nil {
		t.Fatalf("record = %#v, want nil", record)
	}
	if got := err.Error(); !strings.Contains(got, "API Key") || !strings.Contains(got, "未配置") {
		t.Fatalf("error = %q, want explicit API Key configuration message", got)
	}
	if got := len(mgr.workLog["investment-advisor"]); got != 0 {
		t.Fatalf("workLog length = %d, want 0", got)
	}
}

func TestExternalCodingEmployeeAgentRoute(t *testing.T) {
	if agent, ok := externalCodingEmployeeAgent("codex-specialist"); !ok || agent != "codex" {
		t.Fatalf("codex-specialist route = %q, %v; want codex, true", agent, ok)
	}
	if agent, ok := externalCodingEmployeeAgent("cc-specialist"); !ok || agent != "claude" {
		t.Fatalf("cc-specialist route = %q, %v; want claude, true", agent, ok)
	}
	if agent, ok := externalCodingEmployeeAgent("meeting-secretary"); ok || agent != "" {
		t.Fatalf("meeting-secretary route = %q, %v; want empty, false", agent, ok)
	}
}

func TestExternalCodingCancelBindsToGatewayTask(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	gw := &GatewayService{
		logger: logger,
		tasks:  make(map[string]*GatewayTask),
	}
	mgr := NewAIEmployeeManager(gw, logger)
	gw.aeManager = mgr

	ctx, cancel := context.WithCancel(context.Background())
	taskID := "external_codex_1-de"
	mgr.putExternalCodingCancel(taskID, cancel)

	task := &GatewayTask{ID: taskID}
	if !mgr.bindExternalCodingGatewayTask(taskID, task) {
		t.Fatalf("expected cancel to bind")
	}
	if task.Cancel == nil {
		t.Fatalf("task cancel was not attached")
	}
	task.Cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatalf("bound cancel did not cancel runtime context")
	}

	mgr.clearExternalCodingCancel(taskID)
	if mgr.bindExternalCodingGatewayTask(taskID, &GatewayTask{ID: taskID}) {
		t.Fatalf("cancel should be cleared")
	}
}

func TestExternalCodingRunStatusClassifiesCancelAndTimeout(t *testing.T) {
	status, result := externalCodingRunStatus("partial", context.Canceled)
	if status != "stopped" || !strings.Contains(result, "stopped by user") {
		t.Fatalf("cancel classified as (%q, %q), want stopped", status, result)
	}

	status, result = externalCodingRunStatus("", context.DeadlineExceeded)
	if status != "failed" || !strings.Contains(result, "timed out") {
		t.Fatalf("timeout classified as (%q, %q), want failed timeout", status, result)
	}
}

func TestExternalCodingRunStatusDropsCodexProgressTranscript(t *testing.T) {
	raw := strings.Join([]string{
		"Codex started userMessage",
		"Codex completed userMessage",
		"Codex started reasoning",
		"Codex completed reasoning",
		"Codex generated a reply",
		`Run command: "powershell.exe" -Command "Get-ChildItem E:\ai-moive" @ E:\adebug2\0705`,
		`Command completed: "powershell.exe" -Command "Get-ChildItem E:\ai-moive" (exit 0)`,
		"Codex task cancelled",
	}, "\n")

	status, result := externalCodingRunStatus(raw, context.DeadlineExceeded)
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if strings.Contains(result, "Codex started userMessage") || strings.Contains(result, "Run command:") {
		t.Fatalf("progress transcript leaked into final result:\n%s", result)
	}
	if !strings.Contains(result, "timed out") {
		t.Fatalf("result missing timeout cause: %q", result)
	}
}
