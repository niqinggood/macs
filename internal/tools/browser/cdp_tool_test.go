package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
)

func TestDefaultCDPDebugPort(t *testing.T) {
	t.Setenv("OPENKITTY_CDP_PORT", "")
	t.Setenv("CDP_DEBUG_PORT", "")
	if got := defaultCDPDebugPort(); got != "9222" {
		t.Fatalf("defaultCDPDebugPort()=%q, want 9222", got)
	}

	t.Setenv("CDP_DEBUG_PORT", "9333")
	if got := defaultCDPDebugPort(); got != "9333" {
		t.Fatalf("defaultCDPDebugPort()=%q, want CDP_DEBUG_PORT", got)
	}

	t.Setenv("OPENKITTY_CDP_PORT", "9444")
	if got := defaultCDPDebugPort(); got != "9444" {
		t.Fatalf("defaultCDPDebugPort()=%q, want OPENKITTY_CDP_PORT override", got)
	}
}

func TestDefaultCDPDebugHost(t *testing.T) {
	t.Setenv("OPENKITTY_CDP_HOST", "")
	t.Setenv("CDP_DEBUG_HOST", "")
	if got := defaultCDPDebugHost(); got != "127.0.0.1" {
		t.Fatalf("defaultCDPDebugHost()=%q, want 127.0.0.1", got)
	}

	t.Setenv("CDP_DEBUG_HOST", "localhost")
	if got := defaultCDPDebugHost(); got != "localhost" {
		t.Fatalf("defaultCDPDebugHost()=%q, want CDP_DEBUG_HOST", got)
	}

	t.Setenv("OPENKITTY_CDP_HOST", "192.168.1.10")
	if got := defaultCDPDebugHost(); got != "192.168.1.10" {
		t.Fatalf("defaultCDPDebugHost()=%q, want OPENKITTY_CDP_HOST override", got)
	}
}

func TestCDPManagerEndpointDefaultsToIPv4Loopback(t *testing.T) {
	manager := &CDPManager{debugPort: "9446"}
	if got := manager.endpoint("/json/version"); got != "http://127.0.0.1:9446/json/version" {
		t.Fatalf("endpoint()=%q", got)
	}
}

func TestCDPAutoStartSettings(t *testing.T) {
	for _, disabled := range []string{"0", "false", "no"} {
		t.Setenv("OPENKITTY_CDP_AUTO_START", disabled)
		if cdpAutoStartEnabled() {
			t.Fatalf("OPENKITTY_CDP_AUTO_START=%q should disable auto-start", disabled)
		}
	}

	t.Setenv("OPENKITTY_CDP_AUTO_START", "")
	if !cdpAutoStartEnabled() {
		t.Fatal("CDP auto-start should be enabled by default")
	}

	for _, host := range []string{"", "127.0.0.1", "localhost", "::1"} {
		if !isLocalCDPHost(host) {
			t.Fatalf("%q should be treated as local CDP host", host)
		}
	}
	if isLocalCDPHost("192.168.1.10") {
		t.Fatal("remote CDP host should not be auto-started locally")
	}
}

func TestChromeHostCompatibilityArgs(t *testing.T) {
	rootArgs := chromeHostCompatibilityArgs("linux", 0)
	if !slices.Contains(rootArgs, "--disable-dev-shm-usage") ||
		!slices.Contains(rootArgs, "--no-sandbox") {
		t.Fatalf("linux root args = %v", rootArgs)
	}

	userArgs := chromeHostCompatibilityArgs("linux", 1000)
	if !slices.Contains(userArgs, "--disable-dev-shm-usage") ||
		slices.Contains(userArgs, "--no-sandbox") {
		t.Fatalf("linux user args = %v", userArgs)
	}

	if got := chromeHostCompatibilityArgs("windows", -1); len(got) != 0 {
		t.Fatalf("non-linux args = %v, want none", got)
	}
}

func TestFindChromeExecutableHonorsExplicitPath(t *testing.T) {
	configuredBrowser := filepath.Join(t.TempDir(), "portable-browser")
	if err := os.WriteFile(configuredBrowser, []byte("browser"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENKITTY_CHROME_PATH", configuredBrowser)
	if got, err := findChromeExecutable(); err != nil || got != configuredBrowser {
		t.Fatalf("findChromeExecutable()=(%q,%v), want configured path", got, err)
	}

	missingBrowser := filepath.Join(t.TempDir(), "missing-browser")
	t.Setenv("OPENKITTY_CHROME_PATH", missingBrowser)
	if _, err := findChromeExecutable(); err == nil ||
		errors.Is(err, errBrowserExecutableNotFound) {
		t.Fatalf("an invalid explicit path must fail without silently selecting a managed fallback: %v", err)
	}
}

func TestWebScreenshotTimeoutIsBounded(t *testing.T) {
	if got := webScreenshotTimeout(nil); got != 20*time.Second {
		t.Fatalf("default screenshot timeout=%s", got)
	}
	if got := webScreenshotTimeout(map[string]interface{}{"timeout_ms": float64(2500)}); got != 2500*time.Millisecond {
		t.Fatalf("configured screenshot timeout=%s", got)
	}
	if got := webScreenshotTimeout(map[string]interface{}{"timeout_ms": float64(10)}); got != time.Second {
		t.Fatalf("minimum screenshot timeout=%s", got)
	}
	if got := webScreenshotTimeout(map[string]interface{}{"timeout_ms": float64(120000)}); got != time.Minute {
		t.Fatalf("maximum screenshot timeout=%s", got)
	}
}

func TestWaitForWebClickOutcomePrefersDialogAtRequestDeadline(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	dialogEvents := make(chan *cdppage.EventJavascriptDialogOpening, 1)
	actionDone := make(chan error, 1)
	dialogEvents <- &cdppage.EventJavascriptDialogOpening{
		Type:    cdppage.DialogTypeConfirm,
		Message: "Delete review R-17?",
	}
	cancel()

	outcome := waitForWebClickOutcome(requestCtx, dialogEvents, actionDone)
	if outcome.dialog == nil {
		t.Fatalf("dialog was lost to request cancellation: %#v", outcome)
	}
	if outcome.dialog.Type != cdppage.DialogTypeConfirm || outcome.dialog.Message != "Delete review R-17?" {
		t.Fatalf("unexpected dialog outcome: %#v", outcome.dialog)
	}
	if outcome.requestErr != nil || outcome.actionDone {
		t.Fatalf("dialog must take priority over cancellation/action completion: %#v", outcome)
	}
}

func TestCDPPortExplicitConfiguration(t *testing.T) {
	t.Setenv("OPENKITTY_CDP_PORT", "")
	t.Setenv("CDP_DEBUG_PORT", "")
	if cdpPortExplicitlyConfigured() {
		t.Fatal("empty CDP port environment should allow automatic collision recovery")
	}
	t.Setenv("CDP_DEBUG_PORT", "9333")
	if !cdpPortExplicitlyConfigured() {
		t.Fatal("explicit CDP_DEBUG_PORT must disable automatic port replacement")
	}
}

func TestUnusedLocalTCPPortCanBeBound(t *testing.T) {
	port, err := unusedLocalTCPPort("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatalf("selected port %s was not available: %v", port, err)
	}
	_ = listener.Close()
}

func TestNormalizeWebNavigationURLSupportsLocalFiles(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "local page.html")
	if err := os.WriteFile(localPath, []byte("<!doctype html><title>local</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeWebNavigationURL(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "file://") || !strings.Contains(got, "local%20page.html") {
		t.Fatalf("local path should become an escaped file URL, got %q", got)
	}
	got, err = normalizeWebNavigationURL("example.com/app")
	if err != nil || got != "https://example.com/app" {
		t.Fatalf("domain normalization=(%q,%v)", got, err)
	}
}

func TestCDPManagerResolveTargetIDUsesLastTab(t *testing.T) {
	manager := &CDPManager{lastTargetID: "tab-123"}
	if got, err := manager.resolveTargetID(""); err != nil || got != "tab-123" {
		t.Fatalf("resolveTargetID empty=(%q,%v), want tab-123", got, err)
	}
	if got, err := manager.resolveTargetID("explicit-tab"); err != nil || got != "explicit-tab" {
		t.Fatalf("resolveTargetID explicit=(%q,%v), want explicit-tab", got, err)
	}
	manager.lastTargetID = ""
	if _, err := manager.resolveTargetID(""); err == nil {
		t.Fatal("resolveTargetID should fail when no explicit or recent target exists")
	}
}

func TestCDPManagerNewTabReturnsErrorForInvalidAllocator(t *testing.T) {
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &CDPManager{
		allocCtx:   cancelledContext,
		connected:  true,
		tabCancel:  make(map[string]context.CancelFunc),
		tabContext: make(map[string]context.Context),
	}

	if _, _, _, err := manager.newTab(); err == nil {
		t.Fatal("newTab should return an error for a cancelled/invalid allocator")
	}
	if manager.connected || manager.allocCtx != nil {
		t.Fatal("failed tab creation should invalidate the stale allocator")
	}
}

func TestJSStringLiteralEscapesInputText(t *testing.T) {
	raw := "AI \"platform\"\npath\\name"
	literal := jsStringLiteral(raw)
	if !strings.HasPrefix(literal, `"`) || !strings.HasSuffix(literal, `"`) {
		t.Fatalf("literal should be a quoted JSON string: %q", literal)
	}
	if strings.Contains(literal, "\n") {
		t.Fatalf("literal should escape newlines, got %q", literal)
	}
	if !strings.Contains(literal, `\"platform\"`) || !strings.Contains(literal, `\\`) {
		t.Fatalf("literal did not escape quotes/backslashes: %q", literal)
	}
}

func TestWebActionRecorderExportsReplayableScripts(t *testing.T) {
	recorder := &WebActionRecorder{}
	recorder.StartRecording("task-1")
	recorder.Record(WebActionRecord{Action: "navigate", TargetID: "tab-1", Value: `https://example.com/a?x="1"`})
	recorder.Record(WebActionRecord{Action: "input", TargetID: "tab-1", Selector: "#q", Value: "AI \"platform\"\npath\\name"})
	recorder.Record(WebActionRecord{Action: "click", TargetID: "tab-1", Index: 2})
	recorder.Record(WebActionRecord{Action: "close", TargetID: "tab-1"})

	playwright := recorder.ExportPlaywright("browser replay")
	if !strings.Contains(playwright, `page.goto("https://example.com/a?x=\"1\"")`) {
		t.Fatalf("playwright export should include escaped navigation, got:\n%s", playwright)
	}
	if !strings.Contains(playwright, `page.locator("#q").fill("AI \"platform\"\npath\\name")`) {
		t.Fatalf("playwright export should include escaped fill, got:\n%s", playwright)
	}
	if strings.Contains(playwright, `locator("")`) || strings.Contains(playwright, `locator('').click`) {
		t.Fatalf("playwright export should not emit empty locators for index-only actions:\n%s", playwright)
	}
	if !strings.Contains(playwright, "TODO: click recorded DOM index 2") {
		t.Fatalf("playwright export should preserve index-only click as TODO, got:\n%s", playwright)
	}
	if !strings.Contains(playwright, "close recorded browser tab") {
		t.Fatalf("playwright export should include close action, got:\n%s", playwright)
	}

	export := recorder.Export()
	for _, want := range []string{`"action": "navigate"`, `"action": "close"`} {
		if !strings.Contains(export, want) {
			t.Fatalf("JSON export should include %s, got:\n%s", want, export)
		}
	}
}

func TestCDPBrowserToolsSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 with Chrome remote debugging enabled to run")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>OpenKitty CDP Smoke</title></head><body><textarea id="q"></textarea><button id="ok">OK</button></body></html>`)
	}))
	defer server.Close()

	oldManager := cdpManager
	cdpManager = &CDPManager{debugHost: defaultCDPDebugHost(), debugPort: defaultCDPDebugPort(), tabCancel: make(map[string]context.CancelFunc)}
	t.Cleanup(func() {
		cdpManager.shutdown()
		cdpManager = oldManager
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nav, err := (&WebNavigateTool{}).Execute(ctx, map[string]interface{}{"url": server.URL, "timeout": float64(10)})
	if err != nil {
		t.Fatalf("web_navigate failed: %v", err)
	}
	targetID := parseSmokeTargetID(nav)
	if targetID == "" {
		t.Fatalf("could not parse target id from: %s", nav)
	}

	title, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "eval",
		"code":   "document.title",
	})
	if err != nil {
		t.Fatalf("web_page eval failed: %v", err)
	}
	if !strings.Contains(title, "OpenKitty CDP Smoke") {
		t.Fatalf("unexpected page title: %q", title)
	}

	extracted, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "extract",
	})
	if err != nil {
		t.Fatalf("web_page extract failed: %v", err)
	}
	if !strings.Contains(extracted, `[0] textbox`) {
		t.Fatalf("expected textarea index in extract output, got:\n%s", extracted)
	}

	inputText := "AI \"platform\"\npath\\name"
	if _, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "input",
		"index":  float64(0),
		"text":   inputText,
	}); err != nil {
		t.Fatalf("web_page input failed: %v", err)
	}
	value, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "eval",
		"code":   "document.querySelector('#q').value",
	})
	if err != nil {
		t.Fatalf("web_page eval input value failed: %v", err)
	}
	if value != inputText {
		t.Fatalf("input value=%q, want %q", value, inputText)
	}

	screenshotPath := filepath.Join(t.TempDir(), "cdp-smoke.png")
	if _, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action":     "screenshot",
		"target_id":  targetID,
		"file":       screenshotPath,
		"timeout_ms": float64(10000),
	}); err != nil {
		t.Fatalf("web_page screenshot failed: %v", err)
	}
	if info, err := os.Stat(screenshotPath); err != nil || info.Size() == 0 {
		t.Fatalf("web_page screenshot was not persisted: info=%v err=%v", info, err)
	}

	if _, err := (&WebCloseTool{}).Execute(ctx, map[string]interface{}{"target_id": targetID}); err != nil {
		t.Fatalf("web_close failed: %v", err)
	}
	cancel()

	// A process-wide browser connection must survive the request context that
	// first created it. AgentOS executes each task with a different context.
	secondRequestCtx, cancelSecondRequest := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSecondRequest()
	secondNavigation, err := (&WebNavigateTool{}).Execute(
		secondRequestCtx,
		map[string]interface{}{"url": server.URL, "timeout": float64(10)},
	)
	if err != nil {
		t.Fatalf("second request web_navigate failed after first request ended: %v", err)
	}
	secondTargetID := parseSmokeTargetID(secondNavigation)
	if secondTargetID == "" {
		t.Fatalf("could not parse second target id from: %s", secondNavigation)
	}
	if _, err := (&WebCloseTool{}).Execute(
		secondRequestCtx,
		map[string]interface{}{"target_id": secondTargetID},
	); err != nil {
		t.Fatalf("second request web_close failed: %v", err)
	}
}

func parseSmokeTargetID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Opened tab ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Opened tab "))
		}
	}
	return ""
}
