package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type codexInteractiveCapturePTY struct {
	mu     sync.Mutex
	writes []string
}

func (p *codexInteractiveCapturePTY) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (p *codexInteractiveCapturePTY) Write(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes = append(p.writes, string(buf))
	return len(buf), nil
}

func (p *codexInteractiveCapturePTY) Close() error {
	return nil
}

func (p *codexInteractiveCapturePTY) Wait(context.Context) error {
	return nil
}

func (p *codexInteractiveCapturePTY) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.writes, "")
}

func TestIsCodexInteractiveStopCommand(t *testing.T) {
	for _, msg := range []string{"/codex stop", "/codex exit", "/codex quit", "停止codex", "退出codex"} {
		if !isCodexInteractiveStopCommand(msg) {
			t.Fatalf("%q should stop codex interactive session", msg)
		}
	}
	if isCodexInteractiveStopCommand("继续 codex") {
		t.Fatalf("non-stop message should not stop codex interactive session")
	}
}

func TestShouldRouteToCodexInteractive(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		active bool
		want   bool
	}{
		{name: "active followup", msg: "继续", active: true, want: true},
		{name: "active mention starts new task", msg: "@codex专员 新任务", active: true, want: false},
		{name: "stop without active", msg: "/codex stop", active: false, want: true},
		{name: "normal without active", msg: "hi", active: false, want: false},
	}
	for _, tc := range cases {
		if got := shouldRouteToCodexInteractive(tc.msg, tc.active); got != tc.want {
			t.Fatalf("%s: route = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCodexDeliverOutputSendsNativeChannelImage(t *testing.T) {
	workspace := t.TempDir()
	imgPath := writeTestImage(t, workspace)
	g := &GatewayService{
		config:           &Config{Workspace: workspace},
		sseClients:       make(map[string]map[string]chan Event),
		taskStreams:      make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
	}
	g.channelManager = NewChannelManager(g)
	ch := &captureImageChannel{captureReplyChannel: &captureReplyChannel{BaseChannel: NewBaseChannel("feishu", "feishu")}}
	if err := g.channelManager.RegisterChannel(ch); err != nil {
		t.Fatalf("register channel: %v", err)
	}

	m := &CodexInteractiveManager{gateway: g}
	s := &CodexInteractiveSession{
		SessionID: "s1",
		TaskID:    "s1-codex-de",
		Channel:   "feishu",
		Recipient: "chat-1",
		UserID:    "user-1",
	}
	m.deliverOutput(s, "![Codex 图片](/api/files/download?path=%2F.openkitty%2Fstorage%2Fcodex-images%2Fs1%2Frag.png)\n\n正文", false)

	if len(ch.images) != 1 {
		t.Fatalf("sent images = %d, want 1", len(ch.images))
	}
	if filepath.Clean(ch.images[0]) != filepath.Clean(imgPath) {
		t.Fatalf("image path = %s, want %s", ch.images[0], imgPath)
	}
	if ch.count != 1 || ch.content != "正文" {
		t.Fatalf("text send count/content = %d/%q, want 1/正文", ch.count, ch.content)
	}
}

func TestInferCodexWorkDirFromTask(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows drive path inference is Windows-specific")
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := inferCodexWorkDirFromTask("请进入 "+filePath+" 后修复问题", "fallback")
	if filepath.Clean(got) != filepath.Clean(dir) {
		t.Fatalf("work dir = %q, want %q", got, dir)
	}

	if got := inferCodexWorkDirFromTask("没有路径", "fallback"); got != "fallback" {
		t.Fatalf("fallback work dir = %q, want fallback", got)
	}
}

func TestAEChannelContext(t *testing.T) {
	ctx := withAEChannelContext(context.Background(), "weixin", "room-1", "user-1")
	channel, recipient, userID := aeChannelContext(ctx)
	if channel != "weixin" || recipient != "room-1" || userID != "user-1" {
		t.Fatalf("context = (%q, %q, %q), want weixin/room-1/user-1", channel, recipient, userID)
	}
}

func TestCleanCodexInteractiveLine(t *testing.T) {
	got := cleanCodexInteractiveLine("\x1b]0;DEV\x07\x1b[32m\u2502 hello\x1b[0m\r world \u2502\r\n")
	if got != "hello world" {
		t.Fatalf("clean line = %q, want hello world", got)
	}
}

func TestShouldDropCodexInteractiveLineDropsTUIChrome(t *testing.T) {
	dropped := []string{
		"Do you trust the contents of this directory?",
		"1. Yes, continue 2. No, quit Press enter to continue",
		"\u256d\u2500\u2500\u2500\u2500\u256e",
		"\u203a Run /review on my current changes",
		"gpt-5.5 xhigh \u00b7 E:\\DEV",
		"\u280b",
	}
	for _, line := range dropped {
		if !shouldDropCodexInteractiveLine(line) {
			t.Fatalf("line %q should be dropped", line)
		}
	}
	if shouldDropCodexInteractiveLine("Created app.go and ran tests.") {
		t.Fatalf("normal assistant text should not be dropped")
	}
}

func TestCodexInteractiveArgsCarryInitialPrompt(t *testing.T) {
	args := codexInteractiveArgs("E:\\DEV", "first line\r\nsecond line")
	want := []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--no-alt-screen",
		"--cd",
		"E:\\DEV",
		"first line  second line",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestCodexTrustPromptAutoAnswer(t *testing.T) {
	proc := &codexInteractiveCapturePTY{}
	s := &CodexInteractiveSession{proc: proc, done: make(chan struct{})}
	s.maybeAnswerCodexTrustPrompt("Do you trust the contents of this directory?\n1. Yes, continue 2. No, quit")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if proc.String() == "1\r" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := proc.String(); got != "1\r" {
		t.Fatalf("trust prompt write = %q, want 1\\r", got)
	}

	s.maybeAnswerCodexTrustPrompt("Do you trust the contents of this directory?")
	time.Sleep(20 * time.Millisecond)
	if got := proc.String(); got != "1\r" {
		t.Fatalf("trust prompt should only be answered once, got %q", got)
	}
}

func TestCodexInteractiveInputCollapsesNewlines(t *testing.T) {
	got := codexInteractiveInput("first line\r\nsecond line\n")
	if got != "first line  second line" {
		t.Fatalf("input = %q, want newlines collapsed", got)
	}
}

func TestCodexInteractiveWantsVisualArtifact(t *testing.T) {
	if !codexInteractiveWantsVisualArtifact("generate a RAG architecture diagram") {
		t.Fatalf("diagram generation request should be detected")
	}
	if !codexInteractiveWantsVisualArtifact("draw an SVG flowchart for RAG") {
		t.Fatalf("svg flowchart request should be detected")
	}
	if !codexInteractiveWantsVisualArtifact("@codex 生成一张专业手写知识海报") {
		t.Fatalf("handwritten poster generation request should be detected")
	}
	if codexInteractiveWantsVisualArtifact("analyze this uploaded image") {
		t.Fatalf("image analysis should not be treated as image generation")
	}
}

func TestCodexInteractiveWantsPresentationAndMaterialArtifacts(t *testing.T) {
	if !codexInteractiveWantsPresentationArtifact("@codex 做一个产品演示PPT") {
		t.Fatalf("PPT generation request should be detected")
	}
	if !codexInteractiveWantsMaterialArtifact("@codex 生成一张专业手写知识海报") {
		t.Fatalf("poster material request should be detected")
	}
	if codexInteractiveWantsPresentationArtifact("@codex 分析这个PPT的问题") {
		t.Fatalf("PPT analysis should not be treated as PPT generation")
	}
}

func TestCodexInteractivePrepareTurnMessageAddsImageOutputInstruction(t *testing.T) {
	got := codexInteractivePrepareTurnMessage("generate a RAG diagram")
	for _, want := range []string{"built-in image generation capability", "$imagegen", "saved path"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prepared prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "cannot call bitmap image generation") {
		t.Fatalf("prepared prompt should not block Codex image generation:\n%s", got)
	}
}

func TestCodexInteractivePrepareTurnMessageAddsPPTReferenceInstruction(t *testing.T) {
	got := codexInteractivePrepareTurnMessage("@codex 做一个产品演示PPT")
	for _, want := range []string{
		"[OpenKitty PPT/material reference instruction]",
		"builtin/presentation/ppt-art-director/SKILL.md",
		"builtin/presentation/ppt-master/SKILL.md",
		"builtin/presentation/base-ppt/SKILL.md",
		"builtin/presentation/paper-operators-visual/SKILL.md",
		"legacy compatibility alias",
		"Runtime assets are bundled",
		"简单把做的事做成 PPT",
		"actual rendered pixels",
		"higher-end commercial",
		"Level 3 premium custom",
		"Level 2 regular custom",
		"Level 1 fallback structured",
		"default for substantive audience-facing PPTs",
		"task-specific python-pptx/PptxGenJS",
		"Never call convert for PPTX with an empty generator",
		"one targeted correction",
		"bounded render-and-visual-review",
		"fallback reason",
		".openkitty/storage",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prepared prompt missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"skills/presentation/ppt-art-director/SKILL.md",
		"skills/presentation/paper-operators-visual/SKILL.md",
		"reference/ppt/PPTmaster-main",
		"reference/ppt/专业手写知识海报.txt",
		"reference/ppt/planners-ppt-hell-main/planners-ppt-hell/SKILL.md",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("prepared prompt should use internal skill references, but contains %q:\n%s", unwanted, got)
		}
	}
}

func TestPresentationSkillsUseInternalReferenceResources(t *testing.T) {
	files := map[string][]string{
		filepath.Join("builtin", "presentation", "ppt-art-director", "SKILL.md"): {
			"references\\premium-visual-direction.md",
			"references\\template-rendering-contract.md",
		},
		filepath.Join("builtin", "presentation", "paper-operators-visual", "SKILL.md"): {
			"references/handwritten-knowledge-poster.md",
		},
		filepath.Join("builtin", "presentation", "ppt-master", "SKILL.md"): {
			"builtin/presentation/ppt-master/skills/ppt-master",
			"development reference source only",
		},
	}

	for path, wants := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing internal reference %q", path, want)
			}
		}
		for _, unwanted := range []string{
			"reference/ppt/专业手写知识海报.txt",
			"reference/ppt/planners-ppt-hell-main/planners-ppt-hell/SKILL.md",
		} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("%s should not route through external reference %q", path, unwanted)
			}
		}
	}

	for _, path := range []string{
		filepath.Join("builtin", "presentation", "ppt-art-director", "references", "premium-visual-direction.md"),
		filepath.Join("builtin", "presentation", "ppt-art-director", "references", "template-rendering-contract.md"),
		filepath.Join("builtin", "presentation", "paper-operators-visual", "references", "handwritten-knowledge-poster.md"),
		filepath.Join("builtin", "presentation", "base-ppt", "references", "review-gated-production.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("internal reference missing: %s: %v", path, err)
		}
	}
}

func TestPresentationRoutingUsesArtDirectorAutonomy(t *testing.T) {
	files := map[string][]string{
		filepath.Join("builtin", "presentation", "ppt-master", "SKILL.md"): {
			"Full upstream route",
			"strong autonomous candidate",
			"not a native PPTX template",
			"Do not silently downgrade",
			"complete upstream",
		},
		filepath.Join("builtin", "presentation", "ppt-art-director", "SKILL.md"): {
			"task-specific `python-pptx`/PptxGenJS",
			"Do not choose `base-ppt` merely because it is editable",
			"Treat PPTMaster as the higher-end commercial route",
			"Call `use_skill base-ppt` when the user explicitly prioritizes simple or fast output",
			"template source and renderer as orthogonal decisions",
			"Fallback in quality order: PPTMaster → task-specific native renderer → `base-ppt`",
			"executive/board/strategy",
			"audience-ready quality floor",
			"actual image/vision inspection tool",
		},
		filepath.Join("builtin", "presentation", "base-ppt", "SKILL.md"): {
			"Native JSON schema",
			"generate.py",
			"diagram",
			"fast polished structured renderer",
			"--template-mode strict",
			"Do not use a `content` field in new workflows",
		},
	}

	for path, wants := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing autonomous presentation-routing marker %q", path, want)
			}
		}
		if strings.Contains(text, "pptmaster_generate") {
			t.Fatalf("%s still references removed Go presentation generator", path)
		}
	}
}

func TestCodexInteractiveEnvUsesRealTerminal(t *testing.T) {
	env := codexInteractiveEnv()
	values := map[string]string{}
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") || strings.HasPrefix(item, "COLORTERM=") || strings.HasPrefix(item, "NO_COLOR=") {
			parts := strings.SplitN(item, "=", 2)
			values[parts[0]] = parts[1]
		}
	}
	if values["TERM"] == "dumb" || values["TERM"] == "" {
		t.Fatalf("TERM = %q, want a real terminal type", values["TERM"])
	}
	if values["COLORTERM"] == "" {
		t.Fatalf("COLORTERM should be set")
	}
}

func TestCodexInteractiveTurnStartParamsPermissive(t *testing.T) {
	params := codexInteractiveTurnStartParams("thread-1", "E:\\openkitty", "hello", true)
	if params["threadId"] != "thread-1" || params["cwd"] != "E:\\openkitty" {
		t.Fatalf("unexpected thread/cwd params: %#v", params)
	}
	if params["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", params["approvalPolicy"])
	}
	sandbox, ok := params["sandboxPolicy"].(map[string]interface{})
	if !ok || sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandboxPolicy = %#v, want dangerFullAccess", params["sandboxPolicy"])
	}
	input, ok := params["input"].([]map[string]interface{})
	if !ok || len(input) != 1 || input[0]["type"] != "text" || input[0]["text"] != "hello" {
		t.Fatalf("input = %#v", params["input"])
	}
}

func TestCodexInteractiveTurnStartParamsDefault(t *testing.T) {
	params := codexInteractiveTurnStartParams("thread-1", "", "hello", false)
	if _, ok := params["approvalPolicy"]; ok {
		t.Fatalf("default params should not override approval policy: %#v", params)
	}
	if _, ok := params["sandboxPolicy"]; ok {
		t.Fatalf("default params should not override sandbox policy: %#v", params)
	}
	if _, ok := params["cwd"]; ok {
		t.Fatalf("empty workDir should not set cwd: %#v", params)
	}
}

func TestCodexInteractivePermissionGrantResponse(t *testing.T) {
	resp := codexInteractivePermissionGrantResponse()
	if resp["scope"] != "session" || resp["strictAutoReview"] != false {
		t.Fatalf("unexpected grant response metadata: %#v", resp)
	}
	permissions, ok := resp["permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("permissions missing: %#v", resp)
	}
	network, ok := permissions["network"].(map[string]interface{})
	if !ok || network["enabled"] != true {
		t.Fatalf("network permission = %#v", permissions["network"])
	}
	fs, ok := permissions["fileSystem"].(map[string]interface{})
	if !ok {
		t.Fatalf("fileSystem permission missing: %#v", permissions)
	}
	entries, ok := fs["entries"].([]map[string]interface{})
	if !ok || len(entries) != 1 || entries[0]["access"] != "write" {
		t.Fatalf("fileSystem entries = %#v", fs["entries"])
	}
}
