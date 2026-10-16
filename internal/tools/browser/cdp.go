package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"mac/internal/browserruntime"
	"mac/internal/configvalue"
	"mac/internal/textutil"

	"github.com/chromedp/cdproto/cdp"
	cdpinput "github.com/chromedp/cdproto/input"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// ───────────────── CDP Manager ─────────────────

type CDPManager struct {
	mu            sync.Mutex
	debugHost     string
	debugPort     string
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	browserCmd    *exec.Cmd
	connected     bool
	tabContext    map[string]context.Context    // targetID -> persistent attached context
	tabCancel     map[string]context.CancelFunc // targetID -> cancel
	lastTargetID  string
	pendingDialog map[string]*webPendingDialog
}

var browserWorkspace struct {
	sync.RWMutex
	path string
}

// SetWorkspace sets the project root used for managed browser installation.
// Browser profiles remain process-scoped under the user's OpenKitty home.
func SetWorkspace(path string) {
	browserWorkspace.Lock()
	browserWorkspace.path = strings.TrimSpace(path)
	browserWorkspace.Unlock()
}

func workspace() string {
	browserWorkspace.RLock()
	path := browserWorkspace.path
	browserWorkspace.RUnlock()
	if path == "" {
		path, _ = os.Getwd()
	}
	return path
}

func CaptureScreenshotWithGo(ctx context.Context, rawURL, outputPath string, width int, fullPage bool) error {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return err
	}
	normalizedURL, err := normalizeWebNavigationURL(rawURL)
	if err != nil {
		return err
	}
	tabContext, _, targetID, err := cdpManager.newTab()
	if err != nil {
		return err
	}
	defer cdpManager.removeTab(targetID)

	if width <= 0 {
		width = 1440
	}
	actionContext, cancel := context.WithTimeout(tabContext, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(actionContext,
		chromedp.EmulateViewport(int64(width), 900),
		chromedp.Navigate(normalizedURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return err
	}

	var data []byte
	if fullPage {
		err = chromedp.Run(actionContext, chromedp.FullScreenshot(&data, 90))
	} else {
		err = chromedp.Run(actionContext, chromedp.CaptureScreenshot(&data))
	}
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}

type webPendingDialog struct {
	Type          string
	Message       string
	DefaultPrompt string
	actionCtx     context.Context
	actionDone    <-chan error
	cancelAction  context.CancelFunc
}

type webClickWaitOutcome struct {
	dialog     *cdppage.EventJavascriptDialogOpening
	actionErr  error
	actionDone bool
	requestErr error
}

// waitForWebClickOutcome gives an observed native dialog priority over an
// action completion or request cancellation that becomes ready at the same
// time. A JavaScript confirm/alert blocks the originating CDP click command,
// so losing this race reports a misleading context deadline and leaves the
// caller no way to accept or dismiss the already-open dialog.
func waitForWebClickOutcome(
	requestCtx context.Context,
	dialogEvents <-chan *cdppage.EventJavascriptDialogOpening,
	actionDone <-chan error,
) webClickWaitOutcome {
	if requestCtx == nil {
		requestCtx = context.Background()
	}

	takeDialog := func() *cdppage.EventJavascriptDialogOpening {
		select {
		case dialog := <-dialogEvents:
			return dialog
		default:
			return nil
		}
	}

	// Fast-path an event already buffered before entering the blocking select.
	if dialog := takeDialog(); dialog != nil {
		return webClickWaitOutcome{dialog: dialog}
	}

	select {
	case dialog := <-dialogEvents:
		return webClickWaitOutcome{dialog: dialog}
	case err := <-actionDone:
		// Select chooses pseudo-randomly when several cases are ready. Recheck
		// the dialog channel so an observed dialog is never mistaken for a
		// completed/failed click.
		if dialog := takeDialog(); dialog != nil {
			return webClickWaitOutcome{dialog: dialog}
		}
		return webClickWaitOutcome{actionErr: err, actionDone: true}
	case <-requestCtx.Done():
		// The event and deadline can arrive in the same scheduler tick. Preserve
		// the actionable dialog state before propagating cancellation.
		if dialog := takeDialog(); dialog != nil {
			return webClickWaitOutcome{dialog: dialog}
		}
		return webClickWaitOutcome{requestErr: requestCtx.Err()}
	}
}

var cdpManager = &CDPManager{
	debugHost:  defaultCDPDebugHost(),
	debugPort:  defaultCDPDebugPort(),
	tabContext: make(map[string]context.Context),
	tabCancel:  make(map[string]context.CancelFunc),
}

var errBrowserExecutableNotFound = errors.New("Chrome, Chromium, or Edge executable not found")

func defaultCDPDebugHost() string {
	for _, key := range []string{"OPENKITTY_CDP_HOST", "CDP_DEBUG_HOST"} {
		if host := strings.TrimSpace(os.Getenv(key)); host != "" {
			return host
		}
	}
	return "127.0.0.1"
}

func defaultCDPDebugPort() string {
	for _, key := range []string{"OPENKITTY_CDP_PORT", "CDP_DEBUG_PORT"} {
		if port := strings.TrimSpace(os.Getenv(key)); port != "" {
			return port
		}
	}
	return "9222"
}

func openKittyHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openkitty"
	}
	return filepath.Join(home, ".openkitty")
}

func getInt(values map[string]interface{}, key string, fallback int) int {
	return configvalue.Int(values, key, fallback)
}

func (m *CDPManager) endpoint(path string) string {
	host := strings.TrimSpace(m.debugHost)
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s%s", host, m.debugPort, path)
}

func cdpAutoStartEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_CDP_AUTO_START")))
	return value != "0" && value != "false" && value != "no"
}

func isLocalCDPHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func cdpPortExplicitlyConfigured() bool {
	return strings.TrimSpace(os.Getenv("OPENKITTY_CDP_PORT")) != "" ||
		strings.TrimSpace(os.Getenv("CDP_DEBUG_PORT")) != ""
}

func (m *CDPManager) discoverWebSocket(ctx context.Context, client *http.Client) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint("/json/version"), nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", "", fmt.Errorf("failed to read /json/version: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", string(body), fmt.Errorf("CDP version endpoint returned %s", resp.Status)
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return "", string(body), fmt.Errorf("response is not Chrome DevTools JSON: %w", err)
	}
	if strings.TrimSpace(version.WebSocketDebuggerURL) == "" {
		return "", string(body), errors.New("CDP response has no WebSocket debugger URL")
	}
	return version.WebSocketDebuggerURL, string(body), nil
}

func unusedLocalTCPPort(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return "", err
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return "", err
	}
	return port, nil
}

func localTCPPortAvailable(host, port string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// tryConnect discovers the Chrome DevTools WebSocket endpoint and creates a
// RemoteAllocator. Returns nil if already connected, or an error describing
// how to enable the debug port.
func (m *CDPManager) tryConnect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected && m.allocCtx != nil {
		if m.allocCtx.Err() == nil {
			return nil
		}
		if m.allocCancel != nil {
			m.allocCancel()
		}
		m.allocCtx = nil
		m.allocCancel = nil
		m.connected = false
		m.tabContext = nil
		m.tabCancel = nil
		m.lastTargetID = ""
	}

	client := &http.Client{Timeout: 3 * time.Second}
	webSocketURL, responseBody, connectErr := m.discoverWebSocket(ctx, client)
	startErr := error(nil)
	originalPort := m.debugPort
	automaticPort := ""
	if connectErr != nil {
		// A healthy HTTP response without CDP JSON proves that the default port is
		// owned by an unrelated service. When the user did not pin a port, recover
		// locally instead of making the agent diagnose processes or mutate env vars.
		if cdpAutoStartEnabled() &&
			isLocalCDPHost(m.debugHost) &&
			!cdpPortExplicitlyConfigured() &&
			!localTCPPortAvailable(m.debugHost, m.debugPort) {
			if port, err := unusedLocalTCPPort(m.debugHost); err == nil {
				m.debugPort = port
				automaticPort = port
			}
		}
		startErr = m.ensureLocalBrowser(ctx)
		if startErr == nil {
			webSocketURL, responseBody, connectErr = m.discoverWebSocket(ctx, client)
		}
	}
	if connectErr != nil {
		portNote := ""
		if automaticPort != "" {
			portNote = fmt.Sprintf(" Default port %s was occupied by a non-CDP service; OpenKitty selected local port %s automatically.", originalPort, automaticPort)
		}
		responseNote := ""
		if strings.TrimSpace(responseBody) != "" {
			responseNote = "\nResponse was: " + textutil.TruncateRunes(responseBody, 200)
		}
		return fmt.Errorf(
			"Chrome DevTools Protocol endpoint %s:%s not available.%s\n\n"+
				"OpenKitty auto-starts a local headless Chrome unless OPENKITTY_CDP_AUTO_START=0. "+
				"Set OPENKITTY_CDP_PORT only when a fixed external CDP port is required.\n"+
				"Connect error: %v\nAuto-start error: %v%s",
			m.debugHost, m.debugPort, portNote, connectErr, startErr, responseNote,
		)
	}

	// The allocator belongs to the process-wide CDP manager, not to one agent
	// task. A request-scoped parent is cancelled as soon as that task ends and
	// leaves later tasks with a stale "connected" allocator.
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), webSocketURL)
	m.allocCtx = allocCtx
	m.allocCancel = cancel
	m.connected = true
	return nil
}

func (m *CDPManager) ensureLocalBrowser(ctx context.Context) error {
	if !cdpAutoStartEnabled() {
		return errors.New("disabled by OPENKITTY_CDP_AUTO_START")
	}
	if !isLocalCDPHost(m.debugHost) {
		return fmt.Errorf("auto-start only supports local CDP hosts, got %q", m.debugHost)
	}
	if m.browserCmd != nil && m.browserCmd.Process != nil && m.browserCmd.ProcessState == nil {
		return m.waitUntilReady(ctx, 4*time.Second)
	}

	chromePath, err := findChromeExecutable()
	if err != nil {
		if !errors.Is(err, errBrowserExecutableNotFound) {
			return err
		}
		log.Printf("[CDP] no system Chrome/Chromium/Edge found; using the workspace-managed browser fallback")
		chromePath, err = browserruntime.EnsureExecutable(ctx, workspace())
		if err != nil {
			return fmt.Errorf(
				"%w; managed browser fallback failed: %v. Configure OPENKITTY_CDP_HOST and OPENKITTY_CDP_PORT to use a remote browser",
				errBrowserExecutableNotFound,
				err,
			)
		}
	}
	profileDir := strings.TrimSpace(os.Getenv("OPENKITTY_CDP_PROFILE_DIR"))
	if profileDir == "" {
		// Port-scoped profiles allow several OpenKitty processes to run browser QA
		// concurrently without Chrome's profile-lock collision.
		profileDir = filepath.Join(openKittyHome(), "browser-cdp-profiles", "port-"+m.debugPort)
	}
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("create CDP profile dir: %w", err)
	}

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--remote-debugging-address=" + m.debugHost,
		"--remote-debugging-port=" + m.debugPort,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_CDP_HEADLESS"))); value == "0" || value == "false" || value == "no" {
		args = args[2:]
	}
	args = append(args, chromeHostCompatibilityArgs(runtime.GOOS, os.Geteuid())...)

	cmd := exec.Command(chromePath, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Chrome CDP: %w", err)
	}
	m.browserCmd = cmd
	go func() { _ = cmd.Wait() }()

	return m.waitUntilReady(ctx, 8*time.Second)
}

func chromeHostCompatibilityArgs(goos string, effectiveUID int) []string {
	if goos != "linux" {
		return nil
	}
	args := []string{"--disable-dev-shm-usage"}
	// Chrome refuses to start as root unless its sandbox is disabled. OpenKitty
	// may be deployed as a root-owned binary on small servers, so make that
	// compatibility flag explicit instead of requiring an ad-hoc wrapper.
	if effectiveUID == 0 {
		args = append(args, "--no-sandbox")
	}
	return args
}

func (m *CDPManager) waitUntilReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		if _, _, err := m.discoverWebSocket(ctx, client); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timed out waiting for Chrome CDP")
	}
	return lastErr
}

func findChromeExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("OPENKITTY_CHROME_PATH")); configured != "" {
		if regularFileExists(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("OPENKITTY_CHROME_PATH does not exist: %s", configured)
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = append(candidates,
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		)
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			filepath.Join(os.Getenv("HOME"), "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
		)
	default:
		candidates = append(candidates, "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/microsoft-edge")
	}
	for _, candidate := range candidates {
		if regularFileExists(candidate) {
			return candidate, nil
		}
	}
	for _, name := range []string{"google-chrome", "chrome", "chromium", "chromium-browser", "msedge", "microsoft-edge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errBrowserExecutableNotFound
}

func regularFileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// newTab creates a fresh tab context (navigates to about:blank).
// Returns the tab context, its cancel, and the chromedp target ID string.
// The cancel func is stored in CDPManager.tabs for lifecycle management.
func (m *CDPManager) newTab() (context.Context, context.CancelFunc, string, error) {
	m.mu.Lock()
	allocCtx := m.allocCtx
	m.mu.Unlock()
	if allocCtx == nil {
		return nil, nil, "", errors.New("browser is not connected")
	}

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	// Run a no-op to ensure the tab is created so we can read its target ID.
	if err := chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error { return nil })); err != nil {
		tabCancel()
		m.invalidateAllocator()
		return nil, nil, "", fmt.Errorf("create browser tab: %w", err)
	}
	chromeContext := chromedp.FromContext(tabCtx)
	if chromeContext == nil || chromeContext.Target == nil {
		tabCancel()
		m.invalidateAllocator()
		return nil, nil, "", errors.New("create browser tab: browser target is unavailable")
	}
	targetID := chromeContext.Target.TargetID.String()
	m.mu.Lock()
	if m.tabContext == nil {
		m.tabContext = make(map[string]context.Context)
	}
	if m.tabCancel == nil {
		m.tabCancel = make(map[string]context.CancelFunc)
	}
	m.tabContext[targetID] = tabCtx
	m.tabCancel[targetID] = tabCancel
	m.lastTargetID = targetID
	m.mu.Unlock()
	return tabCtx, tabCancel, targetID, nil
}

func (m *CDPManager) invalidateAllocator() {
	m.mu.Lock()
	cancelAllocator := m.allocCancel
	tabCancels := make([]context.CancelFunc, 0, len(m.tabCancel))
	for _, cancelTab := range m.tabCancel {
		if cancelTab != nil {
			tabCancels = append(tabCancels, cancelTab)
		}
	}
	m.allocCtx = nil
	m.allocCancel = nil
	m.connected = false
	m.tabContext = nil
	m.tabCancel = nil
	m.lastTargetID = ""
	m.mu.Unlock()

	for _, cancelTab := range tabCancels {
		cancelTab()
	}
	if cancelAllocator != nil {
		cancelAllocator()
	}
}

func (m *CDPManager) removeTab(targetID string) {
	m.mu.Lock()
	delete(m.tabContext, targetID)
	var cancelTab context.CancelFunc
	if storedCancel, ok := m.tabCancel[targetID]; ok {
		cancelTab = storedCancel
		delete(m.tabCancel, targetID)
	}
	var cancelDialog context.CancelFunc
	if pending, ok := m.pendingDialog[targetID]; ok {
		cancelDialog = pending.cancelAction
		delete(m.pendingDialog, targetID)
	}
	if m.lastTargetID == targetID {
		m.lastTargetID = ""
	}
	m.mu.Unlock()
	if cancelTab != nil {
		cancelTab()
	}
	if cancelDialog != nil {
		cancelDialog()
	}
}

// persistentTabContext returns one long-lived chromedp context per target.
// Chromedp closes an attached target when its own context is cancelled, so
// request-scoped contexts must be children of this persistent context rather
// than newly attached contexts.
func (m *CDPManager) persistentTabContext(targetID string) (context.Context, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil, errors.New("target_id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if tabCtx := m.tabContext[targetID]; tabCtx != nil {
		return tabCtx, nil
	}
	if m.allocCtx == nil {
		return nil, errors.New("browser is not connected")
	}

	tabCtx, tabCancel := chromedp.NewContext(m.allocCtx, chromedp.WithTargetID(target.ID(targetID)))
	if err := chromedp.Run(tabCtx, chromedp.ActionFunc(func(context.Context) error { return nil })); err != nil {
		tabCancel()
		return nil, fmt.Errorf("attach browser target %s: %w", targetID, err)
	}
	if m.tabContext == nil {
		m.tabContext = make(map[string]context.Context)
	}
	if m.tabCancel == nil {
		m.tabCancel = make(map[string]context.CancelFunc)
	}
	m.tabContext[targetID] = tabCtx
	m.tabCancel[targetID] = tabCancel
	return tabCtx, nil
}

func (m *CDPManager) actionContext(requestCtx context.Context, targetID string) (context.Context, context.CancelFunc, error) {
	tabCtx, err := m.persistentTabContext(targetID)
	if err != nil {
		return nil, nil, err
	}
	if requestCtx == nil {
		return tabCtx, func() {}, nil
	}

	actionCtx, cancelAction := context.WithCancel(tabCtx)
	stopForwarding := context.AfterFunc(requestCtx, cancelAction)
	return actionCtx, func() {
		stopForwarding()
		cancelAction()
	}, nil
}

func (m *CDPManager) closeTab(ctx context.Context, targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return errors.New("target_id is required")
	}

	m.mu.Lock()
	allocCtx := m.allocCtx
	m.mu.Unlock()
	if allocCtx == nil {
		return errors.New("browser is not connected")
	}

	tabCtx, tabCancel, err := m.actionContext(ctx, targetID)
	if err != nil {
		return err
	}
	defer tabCancel()
	err = chromedp.Run(tabCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		chromeContext := chromedp.FromContext(actionCtx)
		if chromeContext == nil || chromeContext.Browser == nil {
			return errors.New("browser executor is unavailable")
		}
		browserCtx := cdp.WithExecutor(actionCtx, chromeContext.Browser)
		return target.CloseTarget(target.ID(targetID)).Do(browserCtx)
	}))
	if err != nil {
		return fmt.Errorf("close browser target %s: %w", targetID, err)
	}
	m.removeTab(targetID)
	return nil
}

func (m *CDPManager) pendingDialogFor(targetID string) *webPendingDialog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pendingDialog[targetID]
}

func (m *CDPManager) setPendingDialog(targetID string, pending *webPendingDialog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingDialog == nil {
		m.pendingDialog = make(map[string]*webPendingDialog)
	}
	if existing := m.pendingDialog[targetID]; existing != nil {
		return fmt.Errorf("a %s dialog is already pending on target %s", existing.Type, targetID)
	}
	m.pendingDialog[targetID] = pending
	return nil
}

func (m *CDPManager) clearPendingDialog(targetID string, expected *webPendingDialog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingDialog[targetID] == expected {
		delete(m.pendingDialog, targetID)
	}
}

func (m *CDPManager) resolveTargetID(targetID string) (string, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID != "" {
		return targetID, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(m.lastTargetID) != "" {
		return m.lastTargetID, nil
	}
	return "", fmt.Errorf("target_id is required; call web_navigate first or pass a tab ID from web_targets")
}

func (m *CDPManager) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pending := range m.pendingDialog {
		if pending != nil && pending.cancelAction != nil {
			pending.cancelAction()
		}
	}
	m.pendingDialog = nil
	m.tabContext = nil
	m.tabCancel = nil
	if m.allocCancel != nil {
		m.allocCancel()
		m.allocCancel = nil
	}
	m.allocCtx = nil
	m.connected = false
	m.lastTargetID = ""
	if m.browserCmd != nil && m.browserCmd.Process != nil && m.browserCmd.ProcessState == nil {
		_ = m.browserCmd.Process.Kill()
	}
	m.browserCmd = nil
}

// ───────────────── WebTargetsTool ─────────────────

type WebTargetsTool struct{}

func (t *WebTargetsTool) Name() string { return "web_targets" }
func (t *WebTargetsTool) Description() string {
	return "List all open browser tabs via Chrome DevTools Protocol. Returns tab ID, title, and URL."
}
func (t *WebTargetsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

type targetInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"type"`
}

func listOpenPageTargets(ctx context.Context) ([]targetInfo, error) {
	resp, err := httpGet(ctx, cdpManager.endpoint("/json"))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var targets []targetInfo
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("decode CDP targets: %w", err)
	}
	pageTargets := make([]targetInfo, 0, len(targets))
	for _, candidate := range targets {
		if candidate.Type == "page" {
			pageTargets = append(pageTargets, candidate)
		}
	}
	return pageTargets, nil
}

func (t *WebTargetsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targets, err := listOpenPageTargets(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list tabs: %w", err)
	}

	if len(targets) == 0 {
		return "No open tabs.", nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("%d tab(s):\n", len(targets)))
	for _, t := range targets {
		title := t.Title
		if len(title) > 80 {
			title = title[:80] + "..."
		}
		url := t.URL
		if len(url) > 120 {
			url = url[:120] + "..."
		}
		out.WriteString(fmt.Sprintf("  %s | %s | %s\n", t.ID, title, url))
	}
	return out.String(), nil
}

// ───────────────── WebNavigateTool ─────────────────

type WebNavigateTool struct{}

func (t *WebNavigateTool) Name() string { return "web_navigate" }
func (t *WebNavigateTool) Description() string {
	return "Open an interactive browser tab via Chrome CDP. Accepts an http(s) URL or an absolute local HTML file path; local files open directly and do not need a temporary HTTP server. Do not use this as the first attempt for public/static/data pages: call web_fetch first, and use web_navigate when JavaScript, interaction, screenshot, or visual QA is required. Returns the tab target ID."
}
func (t *WebNavigateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "HTTP(S) URL or absolute local HTML file path to navigate to",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Page load timeout in seconds (default 15)",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebNavigateTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	rawURL, err := normalizeWebNavigationURL(params["url"])
	if err != nil {
		return "", err
	}

	timeout := 15
	if v, ok := params["timeout"].(float64); ok && v > 0 {
		timeout = int(v)
	}

	tabCtx, _, targetID, err := cdpManager.newTab()
	if err != nil {
		return "", err
	}

	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, time.Duration(timeout)*time.Second)
	defer cancelTimeout()

	err = chromedp.Run(tabCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
	)
	if err != nil {
		// close the tab on navigation failure
		cdpManager.removeTab(targetID)
		return "", fmt.Errorf("navigation failed: %w", err)
	}

	// tab stays open; lifecycle managed via web_close

	var title string
	chromedp.Run(tabCtx, chromedp.Title(&title))

	webRecorder.Record(WebActionRecord{Action: "navigate", TargetID: targetID, Value: rawURL, Result: title})
	return fmt.Sprintf("Opened tab %s\nTitle: %s\nURL: %s", targetID, title, rawURL), nil
}

func normalizeWebNavigationURL(value interface{}) (string, error) {
	rawURL, _ := value.(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	lower := strings.ToLower(rawURL)
	for _, prefix := range []string{"http://", "https://", "file://", "data:", "about:"} {
		if strings.HasPrefix(lower, prefix) {
			return rawURL, nil
		}
	}
	if filepath.IsAbs(rawURL) {
		cleanPath := filepath.Clean(rawURL)
		if _, err := os.Stat(cleanPath); err != nil {
			return "", fmt.Errorf("local browser file is not accessible: %w", err)
		}
		slashPath := filepath.ToSlash(cleanPath)
		if volume := filepath.VolumeName(cleanPath); volume != "" && !strings.HasPrefix(slashPath, "/") {
			slashPath = "/" + slashPath
		}
		return (&url.URL{Scheme: "file", Path: slashPath}).String(), nil
	}
	return "https://" + rawURL, nil
}

// ───────────────── WebEvalTool ─────────────────

type WebEvalTool struct{}

func (t *WebEvalTool) Name() string { return "web_eval" }
func (t *WebEvalTool) Description() string {
	return "Execute JavaScript in a browser tab and return the result. Use for reading DOM content, extracting data, checking page state, or manipulating elements."
}
func (t *WebEvalTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID (from web_navigate or web_targets)",
			},
			"code": map[string]interface{}{
				"type":        "string",
				"description": "JavaScript code to execute in the page context",
			},
		},
		"required": []string{"target_id", "code"},
	}
}

func (t *WebEvalTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targetID, _ := params["target_id"].(string)
	code, _ := params["code"].(string)
	if targetID == "" || code == "" {
		return "", fmt.Errorf("target_id and code are required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()
	var result interface{}
	err = chromedp.Run(tabCtx, chromedp.Evaluate(code, &result))
	if err != nil {
		return "", fmt.Errorf("eval failed: %w", err)
	}

	return fmt.Sprintf("%v", result), nil
}

// ───────────────── WebClickTool ─────────────────

type WebClickTool struct{}

func (t *WebClickTool) Name() string { return "web_click" }
func (t *WebClickTool) Description() string {
	return "Click an element in a browser tab by CSS selector. The selector must match a visible, clickable element."
}
func (t *WebClickTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"selector": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector of the element to click",
			},
		},
		"required": []string{"target_id", "selector"},
	}
}

func (t *WebClickTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targetID, _ := params["target_id"].(string)
	selector, _ := params["selector"].(string)
	if targetID == "" || selector == "" {
		return "", fmt.Errorf("target_id and selector are required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()
	err = chromedp.Run(tabCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
	if err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}
	webRecorder.Record(WebActionRecord{Action: "click", TargetID: targetID, Selector: selector})
	return fmt.Sprintf("Clicked '%s' on tab %s", selector, targetID), nil
}

// ───────────────── WebScrollTool ─────────────────

type WebScrollTool struct{}

func (t *WebScrollTool) Name() string { return "web_scroll" }
func (t *WebScrollTool) Description() string {
	return "Scroll a browser tab by pixel offset or to top/bottom. Useful for triggering lazy-loading."
}
func (t *WebScrollTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"y": map[string]interface{}{
				"type":        "integer",
				"description": "Pixels to scroll vertically (positive = down)",
			},
			"direction": map[string]interface{}{
				"type":        "string",
				"description": "Scroll direction: 'top' or 'bottom'",
			},
		},
		"required": []string{"target_id"},
	}
}

func (t *WebScrollTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targetID, _ := params["target_id"].(string)
	if targetID == "" {
		return "", fmt.Errorf("target_id is required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()

	dir, _ := params["direction"].(string)
	switch dir {
	case "top":
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(`window.scrollTo(0, 0)`, nil)); err != nil {
			return "", err
		}
		return "Scrolled to top", nil
	case "bottom":
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil)); err != nil {
			return "", err
		}
		chromedp.Run(tabCtx, chromedp.Sleep(time.Second))
		return "Scrolled to bottom", nil
	default:
		y := 500
		if v, ok := params["y"].(float64); ok {
			y = int(v)
		}
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(fmt.Sprintf(`window.scrollBy(0, %d)`, y), nil)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Scrolled %dpx", y), nil
	}
}

// ───────────────── WebScreenshotTool ─────────────────

type WebScreenshotTool struct{}

func (t *WebScreenshotTool) Name() string { return "web_screenshot" }
func (t *WebScreenshotTool) Description() string {
	return "Capture a screenshot of a browser tab and save to a PNG file. Use read_file to view the result."
}
func (t *WebScreenshotTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Output PNG path. Default: /tmp/web_screenshot.png",
			},
		},
		"required": []string{"target_id"},
	}
}

func (t *WebScreenshotTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targetID, _ := params["target_id"].(string)
	if targetID == "" {
		return "", fmt.Errorf("target_id is required")
	}

	outPath, _ := params["file"].(string)
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "web_screenshot.png")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()
	var buf []byte
	if err := chromedp.Run(tabCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return "", fmt.Errorf("screenshot failed: %w", err)
	}
	if err := os.WriteFile(outPath, buf, 0644); err != nil {
		return "", fmt.Errorf("failed to write screenshot: %w", err)
	}
	return fmt.Sprintf("Screenshot saved: %s (%d bytes)", outPath, len(buf)), nil
}

// ───────────────── WebCloseTool ─────────────────

type WebCloseTool struct{}

func (t *WebCloseTool) Name() string { return "web_close" }
func (t *WebCloseTool) Description() string {
	return "Close a browser tab by target ID. Always close tabs you created when done."
}
func (t *WebCloseTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID to close",
			},
		},
		"required": []string{"target_id"},
	}
}

func (t *WebCloseTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}

	targetID, _ := params["target_id"].(string)
	if targetID == "" {
		return "", fmt.Errorf("target_id is required")
	}

	if err := cdpManager.closeTab(ctx, targetID); err != nil {
		return "", err
	}
	webRecorder.Record(WebActionRecord{Action: "close", TargetID: targetID})
	return fmt.Sprintf("Closed tab %s", targetID), nil
}

// ───────────────── Helpers ─────────────────

func jsStringLiteral(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// ───────────────── Page Agent-style Indexed DOM Tools ─────────────────
// Borrowed from Page Agent: integer-indexed element interaction instead of CSS selectors.
// The LLM first calls web_extract to get a simplified interactive element tree,
// then uses web_click(index=N) or web_input(index=N) to interact.

// ───────────────── WebPage MacroTool (Page Agent unified pattern) ─────────────────
// Consolidates the WebArena-style browser action space into one tool.
// LLM calls web_page(action="extract") then web_page(action="click", index=3).

type WebPageTool struct{}

func (t *WebPageTool) Name() string { return "web_page" }
func (t *WebPageTool) Description() string {
	return "Inspect and operate a browser tab. Call extract first: it returns visible page text plus stable numbered controls. Actions: extract, click, dialog, input, select, check, hover, press, upload, scroll, back, forward, reload, wait, eval, screenshot. If click reports a native browser dialog, resolve it with dialog before any other page action. Re-run extract after navigation or a major DOM change before using another index."
}
func (t *WebPageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"extract", "click", "dialog", "input", "select", "check", "hover", "press", "upload",
					"scroll", "back", "forward", "reload", "wait", "eval", "screenshot",
				},
				"description": "Browser action. Use extract before any index-based action.",
			},
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID (from web_navigate). Optional: defaults to the most recent web_navigate tab.",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Element index from the latest extract (click/input/select/check/hover/press/upload)",
			},
			"selector": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector fallback for click or wait",
			},
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Replacement text for input, or expected page text for wait",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "Option value or visible label for select",
			},
			"checked": map[string]interface{}{
				"type":        "boolean",
				"description": "Desired checkbox/radio state for check (default true)",
			},
			"accept": map[string]interface{}{
				"type":        "boolean",
				"description": "For dialog: accept/confirm when true (default), dismiss/cancel when false",
			},
			"prompt_text": map[string]interface{}{
				"type":        "string",
				"description": "For a JavaScript prompt dialog: text to submit when accepting",
			},
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key or chord for press, for example Enter, Escape, ArrowDown, Control+Enter",
			},
			"files": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
				"description": "Absolute local file paths for upload",
			},
			"code": map[string]interface{}{
				"type":        "string",
				"description": "JavaScript code (for eval action)",
			},
			"y": map[string]interface{}{
				"type":        "integer",
				"description": "Scroll pixels (positive=down, negative=up)",
			},
			"direction": map[string]interface{}{
				"type":        "string",
				"description": "Scroll direction: 'top' or 'bottom'",
			},
			"timeout_ms": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout for wait, or for screenshot capture, in milliseconds (screenshot default 20000; maximum 60000)",
			},
			"max_elements": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum controls returned by extract (default 150, maximum 500)",
			},
			"max_text_chars": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum visible-text characters returned by extract (default 12000, maximum 50000)",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Output PNG path for screenshot (default: .openkitty/storage/screenshot.png)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *WebPageTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}
	action, _ := params["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	targetID, _ := params["target_id"].(string)
	resolvedTargetID, err := cdpManager.resolveTargetID(targetID)
	if err != nil {
		return "", err
	}
	targetID = resolvedTargetID

	if pending := cdpManager.pendingDialogFor(targetID); pending != nil && action != "dialog" {
		return "", fmt.Errorf(
			"native %s dialog is pending on this tab: %q; call web_page action=dialog with accept=true or accept=false before another page action",
			pending.Type,
			pending.Message,
		)
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()

	switch action {
	case "extract":
		return executeWebExtract(tabCtx, params)

	case "click":
		idx := getInt(params, "index", -1)
		selector, _ := params["selector"].(string)
		selector = strings.TrimSpace(selector)
		if idx >= 0 {
			element, err := validateWebElement(tabCtx, idx)
			if err != nil {
				return "", err
			}
			if element.Disabled {
				return "", fmt.Errorf("element [%d] is disabled; run extract again after the page changes", idx)
			}
			result, err := executeWebClickWithDialog(ctx, targetID, []chromedp.Action{
				chromedp.Click(webElementJSPath(idx), chromedp.ByJSPath),
				chromedp.Sleep(350 * time.Millisecond),
			}, fmt.Sprintf("Clicked [%d] <%s> %q", idx, element.Tag, element.Name))
			if err != nil {
				return "", err
			}
			webRecorder.Record(WebActionRecord{Action: "click", TargetID: targetID, Index: idx})
			return result, nil
		}
		if selector != "" {
			result, err := executeWebClickWithDialog(ctx, targetID, []chromedp.Action{
				chromedp.WaitVisible(selector, chromedp.ByQuery),
				chromedp.Click(selector, chromedp.ByQuery),
				chromedp.Sleep(350 * time.Millisecond),
			}, fmt.Sprintf("Clicked %q", selector))
			if err != nil {
				return "", err
			}
			webRecorder.Record(WebActionRecord{Action: "click", TargetID: targetID, Selector: selector})
			return result, nil
		}
		return "", fmt.Errorf("click requires index or selector")

	case "dialog":
		pending := cdpManager.pendingDialogFor(targetID)
		if pending == nil {
			return "", errors.New("no native browser dialog is pending on this tab")
		}
		accept := getWebBoolParam(params, "accept", true)
		promptText, _ := params["prompt_text"].(string)
		handleAction := cdppage.HandleJavaScriptDialog(accept)
		if promptText != "" {
			handleAction = handleAction.WithPromptText(promptText)
		}

		handleCtx, cancelHandle := context.WithCancel(pending.actionCtx)
		stopForwarding := context.AfterFunc(ctx, cancelHandle)
		err := chromedp.Run(handleCtx, handleAction)
		stopForwarding()
		cancelHandle()
		if err != nil {
			return "", fmt.Errorf("resolve native %s dialog: %w", pending.Type, err)
		}

		select {
		case actionErr := <-pending.actionDone:
			cdpManager.clearPendingDialog(targetID, pending)
			pending.cancelAction()
			if actionErr != nil {
				return "", fmt.Errorf("dialog resolved but originating click failed: %w", actionErr)
			}
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "", errors.New("dialog was resolved but the originating click did not finish within 5s")
		}

		decision := "Dismissed"
		if accept {
			decision = "Accepted"
		}
		webRecorder.Record(WebActionRecord{
			Action:   "dialog",
			TargetID: targetID,
			Value:    fmt.Sprintf("%s:%t", pending.Type, accept),
		})
		return webPageActionSummary(tabCtx, fmt.Sprintf("%s native %s dialog %q", decision, pending.Type, pending.Message)), nil

	case "input":
		idx := getInt(params, "index", -1)
		textValue, hasText := params["text"]
		text, textOK := textValue.(string)
		if idx < 0 || !hasText || !textOK {
			return "", fmt.Errorf("input requires index and text")
		}
		element, err := validateWebElement(tabCtx, idx)
		if err != nil {
			return "", err
		}
		var result string
		js := fmt.Sprintf(inputByIndexJS, idx, jsStringLiteral(text))
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(js, &result)); err != nil {
			return "", fmt.Errorf("input failed: %w", err)
		}
		if err := webActionResultError(result); err != nil {
			return "", err
		}
		webRecorder.Record(WebActionRecord{Action: "input", TargetID: targetID, Index: idx, Value: text})
		return webPageActionSummary(tabCtx, fmt.Sprintf("Filled [%d] <%s> %q with %d character(s)", idx, element.Tag, element.Name, len([]rune(text)))), nil

	case "select":
		idx := getInt(params, "index", -1)
		valueParam, hasValue := params["value"]
		value, valueOK := valueParam.(string)
		if idx < 0 || !hasValue || !valueOK {
			return "", fmt.Errorf("select requires index and value")
		}
		if _, err := validateWebElement(tabCtx, idx); err != nil {
			return "", err
		}
		var result string
		js := fmt.Sprintf(selectByIndexJS, idx, jsStringLiteral(value))
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(js, &result)); err != nil {
			return "", fmt.Errorf("select failed: %w", err)
		}
		if err := webActionResultError(result); err != nil {
			return "", err
		}
		webRecorder.Record(WebActionRecord{Action: "select", TargetID: targetID, Index: idx, Value: value})
		return webPageActionSummary(tabCtx, result), nil

	case "check":
		idx := getInt(params, "index", -1)
		if idx < 0 {
			return "", fmt.Errorf("check requires index")
		}
		if _, err := validateWebElement(tabCtx, idx); err != nil {
			return "", err
		}
		checked := getWebBoolParam(params, "checked", true)
		var result string
		js := fmt.Sprintf(checkByIndexJS, idx, checked)
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(js, &result)); err != nil {
			return "", fmt.Errorf("check failed: %w", err)
		}
		if err := webActionResultError(result); err != nil {
			return "", err
		}
		webRecorder.Record(WebActionRecord{Action: "check", TargetID: targetID, Index: idx, Value: fmt.Sprintf("%t", checked)})
		return webPageActionSummary(tabCtx, result), nil

	case "hover":
		idx := getInt(params, "index", -1)
		if idx < 0 {
			return "", fmt.Errorf("hover requires index")
		}
		element, err := validateWebElement(tabCtx, idx)
		if err != nil {
			return "", err
		}
		point, err := webElementCenter(tabCtx, idx)
		if err != nil {
			return "", err
		}
		if err := chromedp.Run(tabCtx,
			chromedp.MouseEvent(cdpinput.MouseMoved, point.X, point.Y),
			chromedp.Sleep(150*time.Millisecond),
		); err != nil {
			return "", fmt.Errorf("hover failed: %w", err)
		}
		webRecorder.Record(WebActionRecord{Action: "hover", TargetID: targetID, Index: idx})
		return webPageActionSummary(tabCtx, fmt.Sprintf("Hovered [%d] <%s> %q", idx, element.Tag, element.Name)), nil

	case "press":
		key, _ := params["key"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			return "", fmt.Errorf("press requires key")
		}
		idx := getInt(params, "index", -1)
		if idx >= 0 {
			if _, err := validateWebElement(tabCtx, idx); err != nil {
				return "", err
			}
			focusJS := fmt.Sprintf(`(function(){var el=%s;if(el){el.scrollIntoView({block:"center"});el.focus();return true;}return false;})()`, webElementJSPath(idx))
			var focused bool
			if err := chromedp.Run(tabCtx, chromedp.Evaluate(focusJS, &focused)); err != nil || !focused {
				if err == nil {
					err = errors.New("element is no longer available")
				}
				return "", fmt.Errorf("press focus failed: %w", err)
			}
		}
		encodedKey, modifiers, err := webKeyAndModifiers(key)
		if err != nil {
			return "", err
		}
		if len(modifiers) > 0 {
			err = chromedp.Run(tabCtx, chromedp.KeyEvent(encodedKey, chromedp.KeyModifiers(modifiers...)))
		} else {
			err = chromedp.Run(tabCtx, chromedp.KeyEvent(encodedKey))
		}
		if err != nil {
			return "", fmt.Errorf("press failed: %w", err)
		}
		webRecorder.Record(WebActionRecord{Action: "press", TargetID: targetID, Index: idx, Value: key})
		return webPageActionSummary(tabCtx, fmt.Sprintf("Pressed %s", key)), nil

	case "upload":
		idx := getInt(params, "index", -1)
		files, err := getWebFilePaths(params["files"])
		if idx < 0 || err != nil {
			if err == nil {
				err = errors.New("index is required")
			}
			return "", fmt.Errorf("upload requires index and files: %w", err)
		}
		if _, err := validateWebElement(tabCtx, idx); err != nil {
			return "", err
		}
		if err := chromedp.Run(tabCtx, chromedp.SetUploadFiles(webElementJSPath(idx), files, chromedp.ByJSPath)); err != nil {
			return "", fmt.Errorf("upload failed: %w", err)
		}
		webRecorder.Record(WebActionRecord{Action: "upload", TargetID: targetID, Index: idx, Value: strings.Join(files, "\n")})
		return webPageActionSummary(tabCtx, fmt.Sprintf("Uploaded %d file(s) into [%d]", len(files), idx)), nil

	case "scroll":
		y := getInt(params, "y", 0)
		dir, _ := params["direction"].(string)
		dir = strings.ToLower(strings.TrimSpace(dir))
		var scrollJS string
		if dir == "top" {
			scrollJS = `window.scrollTo(0, 0)`
		} else if dir == "bottom" {
			scrollJS = `window.scrollTo(0, (document.scrollingElement || document.body).scrollHeight)`
		} else {
			if y == 0 {
				y = 500
			}
			scrollJS = fmt.Sprintf(`window.scrollBy(0, %d)`, y)
		}
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(scrollJS, nil), chromedp.Sleep(150*time.Millisecond)); err != nil {
			return "", fmt.Errorf("scroll failed: %w", err)
		}
		scrollValue := fmt.Sprintf("%d", y)
		if dir == "top" || dir == "bottom" {
			scrollValue = dir
		}
		webRecorder.Record(WebActionRecord{Action: "scroll", TargetID: targetID, Value: scrollValue})
		return webPageActionSummary(tabCtx, "Scrolled "+scrollValue), nil

	case "back", "forward", "reload":
		if err := executeWebNavigation(tabCtx, action); err != nil {
			return "", fmt.Errorf("%s failed: %w", action, err)
		}
		webRecorder.Record(WebActionRecord{Action: action, TargetID: targetID})
		return webPageActionSummary(tabCtx, "Browser navigation: "+action), nil

	case "wait":
		selector, _ := params["selector"].(string)
		selector = strings.TrimSpace(selector)
		waitText, _ := params["text"].(string)
		timeoutMS := clampWebInt(getInt(params, "timeout_ms", 5000), 100, 60000)
		if _, supplied := params["timeout_ms"]; !supplied && selector == "" && waitText == "" {
			timeoutMS = 1000
		}
		switch {
		case selector != "":
			expression := fmt.Sprintf(`(function(selector){
				function deepQuery(root) {
					if (!root || !root.querySelector) return null;
					var direct = root.querySelector(selector);
					if (direct) return direct;
					for (var node of Array.from(root.querySelectorAll("*"))) {
						if (!node.shadowRoot) continue;
						var nested = deepQuery(node.shadowRoot);
						if (nested) return nested;
					}
					return null;
				}
				var element=deepQuery(document);
				if(!element)return false;
				var style=getComputedStyle(element);
				var rect=element.getBoundingClientRect();
				return style.display!=="none"&&style.visibility!=="hidden"&&rect.width>0&&rect.height>0;
			})(%s)`, jsStringLiteral(selector))
			err = chromedp.Run(tabCtx, chromedp.Poll(expression, nil,
				chromedp.WithPollingInterval(100*time.Millisecond),
				chromedp.WithPollingTimeout(time.Duration(timeoutMS)*time.Millisecond),
			))
		case waitText != "":
			expression := fmt.Sprintf(`(function(wanted){
				function visible(node) {
					if (!node || node.nodeType !== 1) return false;
					if (node.matches && node.matches('input[type="hidden"]')) return false;
					if (node.closest && node.closest('[hidden], [aria-hidden="true"], script, style, noscript, template')) return false;
					var style=getComputedStyle(node);
					var rect=node.getBoundingClientRect();
					return style.display!=="none"&&style.visibility!=="hidden"&&style.opacity!=="0"&&rect.width>0&&rect.height>0;
				}
				function deepText(root) {
					var chunks=[];
					if (!root || !root.children) return "";
					for (var child of Array.from(root.children)) {
						if (!visible(child)) continue;
						if (child.shadowRoot) {
							chunks.push(deepText(child.shadowRoot));
						} else if (child.children && child.children.length) {
							chunks.push(deepText(child));
						} else {
							chunks.push(String(child.innerText || child.textContent || ""));
						}
					}
					return chunks.join("\n");
				}
				return document.body && deepText(document.body).includes(wanted);
			})(%s)`, jsStringLiteral(waitText))
			err = chromedp.Run(tabCtx, chromedp.Poll(expression, nil,
				chromedp.WithPollingInterval(100*time.Millisecond),
				chromedp.WithPollingTimeout(time.Duration(timeoutMS)*time.Millisecond),
			))
		default:
			err = chromedp.Run(tabCtx, chromedp.Sleep(time.Duration(timeoutMS)*time.Millisecond))
		}
		if err != nil {
			return "", fmt.Errorf("wait failed after %dms: %w", timeoutMS, err)
		}
		return webPageActionSummary(tabCtx, fmt.Sprintf("Wait condition satisfied within %dms", timeoutMS)), nil

	case "eval":
		code, _ := params["code"].(string)
		if code == "" {
			return "", fmt.Errorf("eval requires code")
		}
		var result interface{}
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(code, &result)); err != nil {
			return "", fmt.Errorf("eval failed: %w", err)
		}
		return formatWebEvalResult(result), nil

	case "screenshot":
		file, _ := params["file"].(string)
		if file == "" {
			file = filepath.Join(".openkitty", "storage", fmt.Sprintf("screenshot_%s.png", time.Now().Format("150405")))
		}
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return "", fmt.Errorf("create screenshot directory: %w", err)
		}
		screenshotCtx, cancelScreenshot := context.WithTimeout(tabCtx, webScreenshotTimeout(params))
		defer cancelScreenshot()
		var buf []byte
		if err := chromedp.Run(screenshotCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
			return "", fmt.Errorf("screenshot failed: %w", err)
		}
		if err := os.WriteFile(file, buf, 0644); err != nil {
			return "", fmt.Errorf("save screenshot: %w", err)
		}
		return fmt.Sprintf("Screenshot saved: %s (%d KB)", file, len(buf)/1024), nil

	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func webScreenshotTimeout(params map[string]interface{}) time.Duration {
	timeoutMS := getInt(params, "timeout_ms", 20000)
	if timeoutMS < 1000 {
		timeoutMS = 1000
	}
	if timeoutMS > 60000 {
		timeoutMS = 60000
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func executeWebClickWithDialog(
	requestCtx context.Context,
	targetID string,
	actions []chromedp.Action,
	successMessage string,
) (string, error) {
	baseCtx, err := cdpManager.persistentTabContext(targetID)
	if err != nil {
		return "", err
	}
	beforeTargets, _ := listOpenPageTargets(requestCtx)
	actionCtx, cancelAction := context.WithCancel(baseCtx)
	listenerCtx, cancelListener := context.WithCancel(actionCtx)
	dialogEvents := make(chan *cdppage.EventJavascriptDialogOpening, 1)
	chromedp.ListenTarget(listenerCtx, func(event interface{}) {
		dialog, ok := event.(*cdppage.EventJavascriptDialogOpening)
		if !ok {
			return
		}
		select {
		case dialogEvents <- dialog:
		default:
		}
	})

	actionDone := make(chan error, 1)
	go func() {
		actionDone <- chromedp.Run(actionCtx, actions...)
	}()

	outcome := waitForWebClickOutcome(requestCtx, dialogEvents, actionDone)
	if outcome.dialog != nil {
		dialog := outcome.dialog
		cancelListener()
		pending := &webPendingDialog{
			Type:          string(dialog.Type),
			Message:       dialog.Message,
			DefaultPrompt: dialog.DefaultPrompt,
			actionCtx:     actionCtx,
			actionDone:    actionDone,
			cancelAction:  cancelAction,
		}
		if err := cdpManager.setPendingDialog(targetID, pending); err != nil {
			cancelAction()
			return "", err
		}
		promptNote := ""
		if dialog.DefaultPrompt != "" {
			promptNote = fmt.Sprintf(" Default prompt: %q.", dialog.DefaultPrompt)
		}
		return fmt.Sprintf(
			"Native %s dialog opened: %q.%s Resolve it with web_page action=dialog and accept=true or accept=false before continuing.",
			dialog.Type,
			dialog.Message,
			promptNote,
		), nil
	}

	cancelListener()
	if outcome.actionDone {
		err := outcome.actionErr
		if err != nil {
			cancelAction()
			return "", fmt.Errorf("click failed: %w", err)
		}
		result := webPageActionSummary(actionCtx, successMessage)
		if afterTargets, listErr := listOpenPageTargets(requestCtx); listErr == nil {
			known := make(map[string]bool, len(beforeTargets))
			for _, existing := range beforeTargets {
				known[existing.ID] = true
			}
			var opened []targetInfo
			for _, candidate := range afterTargets {
				if !known[candidate.ID] {
					opened = append(opened, candidate)
				}
			}
			if len(opened) > 0 {
				var note strings.Builder
				note.WriteString("\nNew browser tab opened by this click:")
				for _, candidate := range opened {
					note.WriteString(fmt.Sprintf(
						"\n  target_id=%s | title=%s | url=%s",
						candidate.ID,
						textutil.TruncateRunes(candidate.Title, 80),
						textutil.TruncateRunes(candidate.URL, 160),
					))
				}
				note.WriteString("\nContinue the workflow in the new target_id; the original tab remains open.")
				result += note.String()
				cdpManager.mu.Lock()
				cdpManager.lastTargetID = opened[len(opened)-1].ID
				cdpManager.mu.Unlock()
			}
		}
		cancelAction()
		return result, nil
	}

	cancelAction()
	return "", fmt.Errorf("click failed: %w", outcome.requestErr)
}

// formatExtractResult parses the extract JS output into LLM-readable markdown.
func formatExtractResult(raw string) string {
	var extracted struct {
		Header struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"header"`
		Content  string `json:"content"`
		Elements []struct {
			Index int                    `json:"index"`
			Tag   string                 `json:"tag"`
			Role  string                 `json:"role"`
			Name  string                 `json:"name"`
			Attrs map[string]interface{} `json:"attrs"`
		} `json:"elements"`
		Footer struct {
			ScrollY           float64 `json:"scrollY"`
			ScrollMax         float64 `json:"scrollMax"`
			InteractiveCount  int     `json:"interactiveCount"`
			ReturnedCount     int     `json:"returnedCount"`
			ElementsTruncated bool    `json:"elementsTruncated"`
			TextChars         int     `json:"textChars"`
			TextTruncated     bool    `json:"textTruncated"`
		} `json:"footer"`
	}
	if err := json.Unmarshal([]byte(raw), &extracted); err != nil {
		return raw
	}

	var sb strings.Builder
	title := strings.TrimSpace(extracted.Header.Title)
	if title == "" {
		title = "(untitled page)"
	}
	sb.WriteString(fmt.Sprintf("## Page: %s\nURL: %s\n", title, extracted.Header.URL))

	if strings.TrimSpace(extracted.Content) != "" {
		sb.WriteString("\n## Visible text\n")
		sb.WriteString(strings.TrimSpace(extracted.Content))
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Interactive elements\n")
	if len(extracted.Elements) == 0 {
		sb.WriteString("(none)\n")
	} else {
		for _, element := range extracted.Elements {
			role := strings.TrimSpace(element.Role)
			if role == "" {
				role = element.Tag
			}
			name := strings.TrimSpace(element.Name)
			if name == "" {
				name = webExtractStringAttr(element.Attrs, "placeholder")
			}

			sb.WriteString(fmt.Sprintf("[%d] %s", element.Index, role))
			if name != "" {
				sb.WriteString(fmt.Sprintf(" %q", name))
			}
			if element.Tag != "" && element.Tag != role {
				sb.WriteString(fmt.Sprintf(" <%s>", element.Tag))
			}
			for _, key := range []string{"type", "value", "href"} {
				if value := webExtractStringAttr(element.Attrs, key); value != "" {
					if key == "href" {
						value = textutil.TruncateRunes(value, 180)
					}
					sb.WriteString(fmt.Sprintf(" %s=%q", key, value))
				}
			}
			for _, key := range []string{"checked", "selected", "disabled", "multiple"} {
				if value, ok := element.Attrs[key].(bool); ok {
					sb.WriteString(fmt.Sprintf(" %s=%t", key, value))
				}
			}
			if options, ok := element.Attrs["options"].([]interface{}); ok && len(options) > 0 {
				optionNames := make([]string, 0, len(options))
				for _, optionRaw := range options {
					option, _ := optionRaw.(map[string]interface{})
					label, _ := option["label"].(string)
					value, _ := option["value"].(string)
					selected, _ := option["selected"].(bool)
					if label == "" {
						label = value
					}
					if selected {
						label += " (selected)"
					}
					optionNames = append(optionNames, label)
				}
				sb.WriteString(" options=[" + strings.Join(optionNames, ", ") + "]")
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n---\n%d interactive element(s), %d returned | scroll: %.0f/%.0f",
		extracted.Footer.InteractiveCount,
		extracted.Footer.ReturnedCount,
		extracted.Footer.ScrollY,
		extracted.Footer.ScrollMax,
	))
	if extracted.Footer.ElementsTruncated {
		sb.WriteString(" | controls truncated")
	}
	if extracted.Footer.TextTruncated {
		sb.WriteString(fmt.Sprintf(" | visible text truncated from %d chars", extracted.Footer.TextChars))
	}
	sb.WriteString("\n")
	return sb.String()
}

func webExtractStringAttr(attrs map[string]interface{}, key string) string {
	value, _ := attrs[key].(string)
	return strings.TrimSpace(value)
}

func executeWebExtract(tabCtx context.Context, params map[string]interface{}) (string, error) {
	maxElements := clampWebInt(getInt(params, "max_elements", 150), 1, 500)
	maxTextChars := clampWebInt(getInt(params, "max_text_chars", 12000), 500, 50000)
	var result string
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(buildExtractJS(maxElements, maxTextChars), &result)); err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}
	return formatExtractResult(result), nil
}

type webElementMeta struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Tag      string `json:"tag"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

type webPoint struct {
	OK    bool    `json:"ok"`
	Error string  `json:"error"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

func webElementJSPath(index int) string {
	return fmt.Sprintf("window.__openkittyElements && window.__openkittyElements[%d]", index)
}

func validateWebElement(tabCtx context.Context, index int) (webElementMeta, error) {
	var element webElementMeta
	script := fmt.Sprintf(`
(function(index) {
	var list = window.__openkittyElements;
	if (!Array.isArray(list)) return {ok:false,error:"run web_page extract before using an element index"};
	var element = list[index];
	if (!element || !element.isConnected) return {ok:false,error:"element index is stale; run web_page extract again"};
	var name = element.getAttribute("aria-label") || "";
	if (!name && element.labels && element.labels.length) {
		name = Array.from(element.labels).map(function(label){return (label.innerText || label.textContent || "").trim();}).filter(Boolean).join(" ");
	}
	if (!name) name = element.placeholder || element.title || (element.innerText || element.textContent || "").trim() || element.name || element.id || "";
	return {
		ok:true,
		tag:(element.tagName || "").toLowerCase(),
		type:String(element.type || "").toLowerCase(),
		name:String(name).replace(/\s+/g, " ").trim().substring(0, 160),
		disabled:Boolean(element.disabled || element.getAttribute("aria-disabled") === "true")
	};
})(%d)`, index)
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(script, &element)); err != nil {
		return webElementMeta{}, fmt.Errorf("resolve element [%d] failed: %w", index, err)
	}
	if !element.OK {
		return webElementMeta{}, fmt.Errorf("element [%d] unavailable: %s", index, element.Error)
	}
	return element, nil
}

func webElementCenter(tabCtx context.Context, index int) (webPoint, error) {
	var point webPoint
	script := fmt.Sprintf(`
(function(index) {
	var list = window.__openkittyElements;
	var element = Array.isArray(list) ? list[index] : null;
	if (!element || !element.isConnected) return {ok:false,error:"element index is stale; run web_page extract again"};
	element.scrollIntoView({block:"center",inline:"center",behavior:"instant"});
	var rect = element.getBoundingClientRect();
	if (!(rect.width > 0 && rect.height > 0)) return {ok:false,error:"element has no visible bounds"};
	return {ok:true,x:rect.left + rect.width / 2,y:rect.top + rect.height / 2};
})(%d)`, index)
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(script, &point)); err != nil {
		return webPoint{}, fmt.Errorf("locate element [%d] for hover failed: %w", index, err)
	}
	if !point.OK {
		return webPoint{}, fmt.Errorf("element [%d] cannot be hovered: %s", index, point.Error)
	}
	return point, nil
}

func webPageActionSummary(tabCtx context.Context, message string) string {
	var page struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(`({title:document.title || "",url:location.href})`, &page)); err != nil {
		return message
	}
	return fmt.Sprintf("%s\nTitle: %s\nURL: %s", message, page.Title, page.URL)
}

func executeWebNavigation(tabCtx context.Context, action string) error {
	switch action {
	case "back", "forward":
		var current int64
		var entries []*cdppage.NavigationEntry
		if err := chromedp.Run(tabCtx, chromedp.NavigationEntries(&current, &entries)); err != nil {
			return err
		}
		next := current - 1
		if action == "forward" {
			next = current + 1
		}
		if next < 0 || next >= int64(len(entries)) {
			return fmt.Errorf("no %s history entry", action)
		}
		if err := chromedp.Run(tabCtx, cdppage.NavigateToHistoryEntry(entries[next].ID)); err != nil {
			return err
		}
	case "reload":
		if err := chromedp.Run(tabCtx, cdppage.Reload()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported navigation action %q", action)
	}

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		var state string
		if err := chromedp.Run(tabCtx, chromedp.Evaluate(`document.readyState`, &state)); err == nil {
			if state == "interactive" || state == "complete" {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-tabCtx.Done():
			return tabCtx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return fmt.Errorf("page did not become ready: %w", lastErr)
	}
	return errors.New("page did not become ready within 10s")
}

func formatWebEvalResult(result interface{}) string {
	if result == nil {
		return "null"
	}
	if text, ok := result.(string); ok {
		return text
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprint(result)
	}
	return string(encoded)
}

func webActionResultError(result string) error {
	trimmed := strings.TrimSpace(result)
	upper := strings.ToUpper(trimmed)
	for _, prefix := range []string{"ERROR:", "NOT FOUND:", "EXTRACT REQUIRED:"} {
		if strings.HasPrefix(upper, prefix) {
			return errors.New(trimmed)
		}
	}
	return nil
}

func getWebBoolParam(params map[string]interface{}, key string, defaultValue bool) bool {
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultValue
}

func getWebFilePaths(value interface{}) ([]string, error) {
	var requested []string
	switch typed := value.(type) {
	case []string:
		requested = append(requested, typed...)
	case []interface{}:
		for _, item := range typed {
			path, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("every file path must be a string")
			}
			requested = append(requested, path)
		}
	case string:
		requested = append(requested, typed)
	default:
		return nil, fmt.Errorf("files must be a non-empty array of paths")
	}
	if len(requested) == 0 {
		return nil, fmt.Errorf("files must not be empty")
	}

	files := make([]string, 0, len(requested))
	for _, requestedPath := range requested {
		requestedPath = strings.TrimSpace(requestedPath)
		if requestedPath == "" {
			return nil, fmt.Errorf("file path must not be empty")
		}
		absolutePath, err := filepath.Abs(requestedPath)
		if err != nil {
			return nil, fmt.Errorf("resolve upload path %q: %w", requestedPath, err)
		}
		info, err := os.Stat(absolutePath)
		if err != nil {
			return nil, fmt.Errorf("upload file %q is not accessible: %w", absolutePath, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("upload path %q is not a regular file", absolutePath)
		}
		files = append(files, absolutePath)
	}
	return files, nil
}

func clampWebInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func webKeyAndModifiers(raw string) (string, []cdpinput.Modifier, error) {
	parts := strings.Split(raw, "+")
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("key must not be empty")
	}
	modifiers := make([]cdpinput.Modifier, 0, len(parts)-1)
	for _, modifier := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(modifier)) {
		case "control", "ctrl":
			modifiers = append(modifiers, cdpinput.ModifierCtrl)
		case "alt", "option":
			modifiers = append(modifiers, cdpinput.ModifierAlt)
		case "shift":
			modifiers = append(modifiers, cdpinput.ModifierShift)
		case "meta", "command", "cmd":
			modifiers = append(modifiers, cdpinput.ModifierMeta)
		default:
			return "", nil, fmt.Errorf("unsupported key modifier %q", modifier)
		}
	}

	keyName := strings.TrimSpace(parts[len(parts)-1])
	var encoded string
	switch strings.ToLower(keyName) {
	case "enter", "return":
		encoded = kb.Enter
	case "tab":
		encoded = kb.Tab
	case "escape", "esc":
		encoded = kb.Escape
	case "backspace":
		encoded = kb.Backspace
	case "delete", "del":
		encoded = kb.Delete
	case "arrowup", "up":
		encoded = kb.ArrowUp
	case "arrowdown", "down":
		encoded = kb.ArrowDown
	case "arrowleft", "left":
		encoded = kb.ArrowLeft
	case "arrowright", "right":
		encoded = kb.ArrowRight
	case "home":
		encoded = kb.Home
	case "end":
		encoded = kb.End
	case "pageup":
		encoded = kb.PageUp
	case "pagedown":
		encoded = kb.PageDown
	case "space", "spacebar":
		encoded = " "
	default:
		if len([]rune(keyName)) != 1 {
			return "", nil, fmt.Errorf("unsupported key %q", keyName)
		}
		encoded = keyName
	}
	return encoded, modifiers, nil
}

const selectByIndexJS = `
(function(index, wanted) {
	var list = window.__openkittyElements;
	var element = Array.isArray(list) ? list[index] : null;
	if (!element || !element.isConnected) return "ERROR: element index is stale; run extract again";
	if (element.tagName.toLowerCase() !== "select") return "ERROR: element [" + index + "] is not a select";
	var options = Array.from(element.options);
	var normalized = String(wanted).trim().toLowerCase();
	var option = options.find(function(candidate) {
		return String(candidate.value) === String(wanted) ||
			String(candidate.textContent || "").trim() === String(wanted);
	});
	if (!option) {
		option = options.find(function(candidate) {
			return String(candidate.value).trim().toLowerCase() === normalized ||
				String(candidate.textContent || "").trim().toLowerCase() === normalized;
		});
	}
	if (!option) return "ERROR: option not found: " + wanted;
	element.value = option.value;
	option.selected = true;
	element.dispatchEvent(new Event("input", {bubbles:true}));
	element.dispatchEvent(new Event("change", {bubbles:true}));
	return "Selected " + String(option.textContent || option.value).trim() + " in [" + index + "]";
})(%d, %s)
`

const checkByIndexJS = `
(function(index, wanted) {
	var list = window.__openkittyElements;
	var element = Array.isArray(list) ? list[index] : null;
	if (!element || !element.isConnected) return "ERROR: element index is stale; run extract again";
	var type = String(element.type || "").toLowerCase();
	var role = String(element.getAttribute("role") || "").toLowerCase();
	if (type !== "checkbox" && type !== "radio" && role !== "checkbox" && role !== "radio" && role !== "switch") {
		return "ERROR: element [" + index + "] is not checkable";
	}
	if (element.disabled || element.getAttribute("aria-disabled") === "true") return "ERROR: element [" + index + "] is disabled";
	wanted = Boolean(wanted);
	var current = "checked" in element ? Boolean(element.checked) : element.getAttribute("aria-checked") === "true";
	if (current !== wanted) {
		if ("checked" in element && !(type === "radio" && !wanted)) {
			element.click();
		} else {
			if ("checked" in element) element.checked = wanted;
			element.setAttribute("aria-checked", wanted ? "true" : "false");
			element.dispatchEvent(new Event("input", {bubbles:true}));
			element.dispatchEvent(new Event("change", {bubbles:true}));
		}
	}
	var finalValue = "checked" in element ? Boolean(element.checked) : element.getAttribute("aria-checked") === "true";
	if (finalValue !== wanted) return "ERROR: check state did not change to " + wanted;
	return "Set [" + index + "] checked=" + finalValue;
})(%d, %t)
`

// WebExtractTool extracts the page as a simplified indexed interactive element tree.
// Page Agent pattern: flat DOM → index-based interaction.
type WebExtractTool struct{}

func (t *WebExtractTool) Name() string { return "web_extract" }
func (t *WebExtractTool) Description() string {
	return "Extract visible page text and numbered interactive elements. The same indices are used by click/input/select/check/hover/press/upload until navigation or a major DOM change."
}
func (t *WebExtractTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"max_elements": map[string]interface{}{
				"type":        "integer",
				"description": "Max interactive elements to return (default: 150, maximum: 500)",
			},
			"max_text_chars": map[string]interface{}{
				"type":        "integer",
				"description": "Max visible-text characters to return (default: 12000, maximum: 50000)",
			},
		},
		"required": []string{"target_id"},
	}
}

const extractJS = `
(function(maxElements, maxTextChars) {
	maxElements = Math.max(1, Math.min(Number(maxElements) || 150, 500));
	maxTextChars = Math.max(500, Math.min(Number(maxTextChars) || 12000, 50000));
	var out = {
		header: { title: document.title || "", url: location.href },
		content: "",
		elements: [],
		footer: {}
	};

	function cleanText(value, limit) {
		var text = String(value || "").replace(/\s+/g, " ").trim();
		return limit && text.length > limit ? text.substring(0, limit) : text;
	}

	function isVisible(node) {
		if (!node || node.nodeType !== 1) return false;
		if (node.matches && node.matches('input[type="hidden"]')) return false;
		if (node.closest && node.closest('[hidden], [aria-hidden="true"], script, style, noscript, template')) return false;
		var style = window.getComputedStyle(node);
		if (!style || style.display === "none" || style.visibility === "hidden" || style.opacity === "0") return false;
		var rect = node.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0;
	}

	function accessibleName(node) {
		var direct = cleanText(node.getAttribute && node.getAttribute("aria-label"), 160);
		if (direct) return direct;
		var labelledBy = cleanText(node.getAttribute && node.getAttribute("aria-labelledby"), 200);
		if (labelledBy) {
			var nameRoot = node.getRootNode ? node.getRootNode() : document;
			var labelled = labelledBy.split(/\s+/).map(function(id) {
				var label = nameRoot && nameRoot.getElementById ? nameRoot.getElementById(id) : document.getElementById(id);
				return label ? cleanText(label.innerText || label.textContent, 160) : "";
			}).filter(Boolean).join(" ");
			if (labelled) return labelled;
		}
		if (node.labels && node.labels.length) {
			var labels = Array.from(node.labels).map(function(label) {
				return cleanText(label.innerText || label.textContent, 160);
			}).filter(Boolean).join(" ");
			if (labels) return labels;
		}
		for (var attr of ["alt", "title", "placeholder"]) {
			var value = cleanText(node.getAttribute && node.getAttribute(attr), 160);
			if (value) return value;
		}
		var text = cleanText(node.innerText || node.textContent, 160);
		if (text) return text;
		return cleanText(node.getAttribute && (node.getAttribute("name") || node.getAttribute("id")), 160);
	}

	function inferredRole(node) {
		var explicit = cleanText(node.getAttribute && node.getAttribute("role"), 40);
		if (explicit) return explicit;
		var tag = node.tagName.toLowerCase();
		if (tag === "a") return "link";
		if (tag === "button" || tag === "summary") return "button";
		if (tag === "textarea" || node.contentEditable === "true") return "textbox";
		if (tag === "select") return node.multiple ? "listbox" : "combobox";
		if (tag === "input") {
			var type = (node.type || "text").toLowerCase();
			if (type === "checkbox") return "checkbox";
			if (type === "radio") return "radio";
			if (type === "range") return "slider";
			if (["button", "submit", "reset", "image"].includes(type)) return "button";
			if (type === "file") return "file";
			return "textbox";
		}
		return tag;
	}

	function isInteractive(node) {
		var tag = node.tagName.toLowerCase();
		if (["a", "button", "input", "textarea", "select", "summary"].includes(tag)) return true;
		var role = cleanText(node.getAttribute && node.getAttribute("role"), 40).toLowerCase();
		if (["button", "link", "checkbox", "radio", "switch", "textbox", "combobox", "listbox", "menuitem", "option", "tab", "slider", "spinbutton"].includes(role)) return true;
		if (node.contentEditable === "true") return true;
		if (node.onclick || (node.hasAttribute && (node.hasAttribute("onclick") || node.hasAttribute("ng-click") || node.hasAttribute("data-action")))) return true;
		return node.tabIndex >= 0 && tag !== "body";
	}

	function collectElementTree(root, output) {
		if (!root || !root.children) return;
		for (var child of Array.from(root.children)) {
			output.push(child);
			if (child.shadowRoot) collectElementTree(child.shadowRoot, output);
			collectElementTree(child, output);
		}
	}

	function composedVisibleText(root) {
		var parts = [];
		var blockTags = new Set([
			"address", "article", "aside", "blockquote", "br", "button", "dd", "details",
			"dialog", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer",
			"form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "label",
			"legend", "li", "main", "nav", "ol", "p", "pre", "section", "summary",
			"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"
		]);

		function visit(node) {
			if (!node) return;
			if (node.nodeType === Node.TEXT_NODE) {
				var text = cleanText(node.nodeValue);
				if (text) parts.push(text);
				return;
			}
			if (node.nodeType === Node.DOCUMENT_FRAGMENT_NODE) {
				for (var fragmentChild of Array.from(node.childNodes || [])) visit(fragmentChild);
				return;
			}
			if (node.nodeType !== Node.ELEMENT_NODE || !isVisible(node)) return;

			var tag = node.tagName.toLowerCase();
			if (["script", "style", "noscript", "template"].includes(tag)) return;
			if (tag === "slot" && typeof node.assignedNodes === "function") {
				var assigned = node.assignedNodes({flatten:true});
				if (assigned.length) {
					for (var assignedNode of assigned) visit(assignedNode);
				} else {
					for (var slotChild of Array.from(node.childNodes || [])) visit(slotChild);
				}
			} else {
				var contentRoot = node.shadowRoot || node;
				for (var childNode of Array.from(contentRoot.childNodes || [])) visit(childNode);
			}
			if (blockTags.has(tag)) parts.push("\n");
		}

		visit(root);
		return parts.join(" ")
			.replace(/[ \t]*\n[ \t]*/g, "\n")
			.replace(/\n{3,}/g, "\n\n")
			.replace(/[ \t]+/g, " ")
			.trim();
	}

	var indexed = [];
	var interactiveCount = 0;
	var nodes = [];
	if (document.body) collectElementTree(document.body, nodes);
	for (var node of nodes) {
		if (!isVisible(node) || !isInteractive(node)) continue;
		interactiveCount++;
		if (indexed.length >= maxElements) continue;

		var tag = node.tagName.toLowerCase();
		var role = inferredRole(node);
		var name = accessibleName(node);
		var attrs = {};
		if (node.type) attrs.type = String(node.type).toLowerCase();
		if (node.placeholder) attrs.placeholder = cleanText(node.placeholder, 160);
		if (node.href) attrs.href = String(node.href);
		if ("value" in node && !["password", "file", "checkbox", "radio"].includes(String(node.type || "").toLowerCase())) {
			var value = cleanText(node.value, 240);
			if (value) attrs.value = value;
		}
		if ("checked" in node) attrs.checked = Boolean(node.checked);
		if ("disabled" in node) attrs.disabled = Boolean(node.disabled);
		if ("multiple" in node) attrs.multiple = Boolean(node.multiple);
		if (node.getAttribute && node.getAttribute("aria-selected") !== null) {
			attrs.selected = node.getAttribute("aria-selected") === "true";
		}
		if (tag === "select") {
			attrs.options = Array.from(node.options).slice(0, 40).map(function(option) {
				return {
					label: cleanText(option.textContent, 100),
					value: String(option.value),
					selected: Boolean(option.selected)
				};
			});
		}
		for (var ariaAttr of ["aria-expanded", "aria-pressed", "aria-current"]) {
			var ariaValue = node.getAttribute && node.getAttribute(ariaAttr);
			if (ariaValue !== null && ariaValue !== "") attrs[ariaAttr] = ariaValue;
		}

		var index = indexed.length;
		indexed.push(node);
		out.elements.push({
			index: index,
			tag: tag,
			role: role,
			name: name,
			attrs: attrs
		});
	}
	window.__openkittyElements = indexed;
	window.__openkittyExtractURL = location.href;
	window.__openkittyExtractedAt = Date.now();

	var visibleText = document.body ? composedVisibleText(document.body) : "";
	var textLines = visibleText.replace(/\r/g, "").split("\n").map(function(line) {
		return line.replace(/[ \t]+/g, " ").trim();
	}).filter(function(line, index, all) {
		return line && (index === 0 || line !== all[index - 1]);
	});
	visibleText = textLines.join("\n");
	var textChars = visibleText.length;
	var textTruncated = textChars > maxTextChars;
	if (textTruncated) visibleText = visibleText.substring(0, maxTextChars) + "\n… [visible text truncated]";
	out.content = visibleText;

	var scrolling = document.scrollingElement || document.documentElement || document.body;
	var scrollMax = scrolling ? Math.max(0, scrolling.scrollHeight - window.innerHeight) : 0;
	out.footer = {
		scrollY: Math.max(0, window.scrollY || 0),
		scrollMax: scrollMax,
		interactiveCount: interactiveCount,
		returnedCount: indexed.length,
		elementsTruncated: interactiveCount > indexed.length,
		textChars: textChars,
		textTruncated: textTruncated,
		viewportWidth: window.innerWidth,
		viewportHeight: window.innerHeight
	};
	return JSON.stringify(out);
})
`

func buildExtractJS(maxElements, maxTextChars int) string {
	return fmt.Sprintf("%s(%d, %d)", extractJS, maxElements, maxTextChars)
}

func (t *WebExtractTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}
	targetID, _ := params["target_id"].(string)
	if targetID == "" {
		return "", fmt.Errorf("target_id is required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()

	return executeWebExtract(tabCtx, params)
}

// WebClickByIndexTool clicks an element by its numeric index from web_extract.
type WebClickByIndexTool struct{}

func (t *WebClickByIndexTool) Name() string { return "web_click_index" }
func (t *WebClickByIndexTool) Description() string {
	return "Click an element by its numeric index (from web_extract output). More reliable than CSS selectors — indices are stable within one page view."
}
func (t *WebClickByIndexTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Element index from web_extract",
			},
		},
		"required": []string{"target_id", "index"},
	}
}

func (t *WebClickByIndexTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}
	targetID, _ := params["target_id"].(string)
	idx := getInt(params, "index", -1)
	if targetID == "" || idx < 0 {
		return "", fmt.Errorf("target_id and index are required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()

	element, err := validateWebElement(tabCtx, idx)
	if err != nil {
		return "", err
	}
	if element.Disabled {
		return "", fmt.Errorf("element [%d] is disabled", idx)
	}
	if err := chromedp.Run(tabCtx,
		chromedp.Click(webElementJSPath(idx), chromedp.ByJSPath),
		chromedp.Sleep(350*time.Millisecond),
	); err != nil {
		return "", fmt.Errorf("click by index failed: %w", err)
	}
	webRecorder.Record(WebActionRecord{Action: "click", TargetID: targetID, Index: idx})
	return webPageActionSummary(tabCtx, fmt.Sprintf("Clicked [%d] <%s> %q", idx, element.Tag, element.Name)), nil
}

// WebInputByIndexTool types text into an input element by its numeric index.
type WebInputByIndexTool struct{}

func (t *WebInputByIndexTool) Name() string { return "web_input" }
func (t *WebInputByIndexTool) Description() string {
	return "Type text into an input/textarea by its numeric index (from web_extract). Supports both regular inputs and contentEditable elements."
}
func (t *WebInputByIndexTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Tab target ID",
			},
			"index": map[string]interface{}{
				"type":        "integer",
				"description": "Input element index from web_extract",
			},
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to type",
			},
		},
		"required": []string{"target_id", "index", "text"},
	}
}

const inputByIndexJS = `
(function(idx, text) {
	var list = window.__openkittyElements;
	var el = Array.isArray(list) ? list[idx] : null;
	if (!el || !el.isConnected) return 'ERROR: element index is stale; run extract again';
	var tag = el.tagName.toLowerCase();
	var type = String(el.type || '').toLowerCase();
	var editable = tag === 'textarea' || el.contentEditable === 'true' ||
		(tag === 'input' && !['button','submit','reset','checkbox','radio','file','range','color'].includes(type));
	if (!editable) return 'ERROR: element [' + idx + '] is not a text-editable control';
	if (el.disabled || el.readOnly || el.getAttribute('aria-disabled') === 'true') return 'ERROR: element [' + idx + '] is disabled or read-only';
	el.scrollIntoView({block:'center',behavior:'instant'});
	el.focus();
	if (el.contentEditable === 'true') {
		el.textContent = text;
		el.dispatchEvent(new InputEvent('input', {bubbles:true,inputType:'insertText',data:text}));
	} else {
		var proto = tag === 'textarea' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
		var nativeSetter = Object.getOwnPropertyDescriptor(proto, 'value');
		if (nativeSetter && nativeSetter.set) {
			nativeSetter.set.call(el, text);
		} else {
			el.value = text;
		}
		el.dispatchEvent(new Event('input', {bubbles:true}));
		el.dispatchEvent(new Event('change', {bubbles:true}));
	}
	return 'Filled [' + idx + '] <' + tag + '> with ' + text.length + ' character(s)';
})(%d, %s)
`

func (t *WebInputByIndexTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if err := cdpManager.tryConnect(ctx); err != nil {
		return "", err
	}
	targetID, _ := params["target_id"].(string)
	idx := getInt(params, "index", -1)
	textValue, hasText := params["text"]
	text, textOK := textValue.(string)
	if targetID == "" || idx < 0 || !hasText || !textOK {
		return "", fmt.Errorf("target_id, index, and text are required")
	}

	tabCtx, tabCancel, err := cdpManager.actionContext(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer tabCancel()

	var result string
	js := fmt.Sprintf(inputByIndexJS, idx, jsStringLiteral(text))
	err = chromedp.Run(tabCtx, chromedp.Evaluate(js, &result))
	if err != nil {
		return "", fmt.Errorf("input by index failed: %w", err)
	}
	if err := webActionResultError(result); err != nil {
		return "", err
	}
	webRecorder.Record(WebActionRecord{Action: "input", TargetID: targetID, Index: idx, Value: text})
	return webPageActionSummary(tabCtx, result), nil
}

// ───────────────── Web Action Recorder (Page Agent Playwright pattern) ─────────────────

// WebActionRecord captures a single web interaction for replay/export.
type WebActionRecord struct {
	Step      int    `json:"step"`
	Action    string `json:"action"` // navigate, click, input, scroll, extract
	TargetID  string `json:"tab_id"`
	Selector  string `json:"selector,omitempty"`
	Index     int    `json:"index,omitempty"`
	Value     string `json:"value,omitempty"` // URL for navigate, text for input
	Result    string `json:"result,omitempty"`
	Timestamp int64  `json:"ts"`
}

// WebActionRecorder collects web interactions during a task.
type WebActionRecorder struct {
	mu      sync.Mutex
	actions []WebActionRecord
	taskID  string
}

var webRecorder = &WebActionRecorder{}

// Recorder returns the process-wide browser action recorder used by the CDP
// tools. The application layer uses it to wire browser-task replay support.
func Recorder() *WebActionRecorder { return webRecorder }

// StartRecording begins a new recording session.
func (r *WebActionRecorder) StartRecording(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taskID = taskID
	r.actions = nil
}

// Record adds an action to the session.
func (r *WebActionRecorder) Record(action WebActionRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	action.Step = len(r.actions) + 1
	action.Timestamp = time.Now().UnixMilli()
	r.actions = append(r.actions, action)
}

// Export returns all recorded actions as a JSON replay script.
func (r *WebActionRecorder) Export() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.actions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Web Action Replay (%d steps)\n\n", len(r.actions)))
	sb.WriteString("```json\n")
	data, _ := json.MarshalIndent(map[string]interface{}{
		"task_id": r.taskID,
		"steps":   r.actions,
		"total":   len(r.actions),
	}, "", "  ")
	sb.Write(data)
	sb.WriteString("\n```\n\n")
	// Generate Playwright-style pseudocode
	sb.WriteString("### Playwright Replay Pseudocode\n```javascript\n")
	sb.WriteString("const { chromium } = require('playwright');\n")
	sb.WriteString("(async () => {\n")
	sb.WriteString("  const browser = await chromium.launch();\n")
	sb.WriteString("  const page = await browser.newPage();\n")
	for _, a := range r.actions {
		switch a.Action {
		case "navigate":
			sb.WriteString(fmt.Sprintf("  await page.goto(%s);\n", jsStringLiteral(a.Value)))
		case "click":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).click();\n", jsStringLiteral(a.Selector)))
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: click recorded DOM index %d; rerun web_page extract to choose a stable selector.\n", a.Index))
			}
		case "input":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).fill(%s);\n", jsStringLiteral(a.Selector), jsStringLiteral(a.Value)))
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: fill recorded DOM index %d with %s; rerun web_page extract to choose a stable selector.\n", a.Index, jsStringLiteral(a.Value)))
			}
		case "scroll":
			sb.WriteString(fmt.Sprintf("  await page.evaluate(() => window.scrollBy(0, %s));\n", a.Value))
		case "extract":
			sb.WriteString(fmt.Sprintf("  // extracted page state at step %d\n", a.Step))
		case "close":
			sb.WriteString("  // close recorded browser tab\n")
		}
	}
	sb.WriteString("  await browser.close();\n")
	sb.WriteString("})();\n```\n")
	return sb.String()
}

// ExportPlaywright generates a complete Playwright .spec.ts file.
func (r *WebActionRecorder) ExportPlaywright(testName string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.actions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("import { test, expect } from '@playwright/test';\n\n")
	sb.WriteString(fmt.Sprintf("test('%s', async ({ page }) => {\n", testName))
	firstNav := true
	for _, a := range r.actions {
		switch a.Action {
		case "navigate":
			sb.WriteString(fmt.Sprintf("  await page.goto(%s);\n", jsStringLiteral(a.Value)))
			if firstNav {
				sb.WriteString("  await page.waitForLoadState('networkidle');\n")
				firstNav = false
			}
		case "click":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).click();\n", jsStringLiteral(a.Selector)))
				sb.WriteString("  await page.waitForTimeout(500);\n")
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: click recorded DOM index %d; rerun web_page extract to choose a stable selector.\n", a.Index))
			}
		case "input":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).fill(%s);\n", jsStringLiteral(a.Selector), jsStringLiteral(a.Value)))
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: fill recorded DOM index %d with %s; rerun web_page extract to choose a stable selector.\n", a.Index, jsStringLiteral(a.Value)))
			}
		case "scroll":
			sb.WriteString(fmt.Sprintf("  await page.evaluate(() => window.scrollBy(0, %s));\n", a.Value))
			sb.WriteString("  await page.waitForTimeout(300);\n")
		case "close":
			sb.WriteString("  // close recorded browser tab\n")
		}
	}
	sb.WriteString("});\n")
	return sb.String()
}

// ExportStandalone generates a standalone Node.js script (no test runner needed).
func (r *WebActionRecorder) ExportStandalone() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.actions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("const { chromium } = require('playwright');\n\n")
	sb.WriteString(";(async () => {\n")
	sb.WriteString("  const browser = await chromium.launch({ headless: false, slowMo: 300 });\n")
	sb.WriteString("  const context = await browser.newContext();\n")
	sb.WriteString("  const page = await context.newPage();\n\n")
	firstNav := true
	for _, a := range r.actions {
		switch a.Action {
		case "navigate":
			sb.WriteString(fmt.Sprintf("  await page.goto(%s);\n", jsStringLiteral(a.Value)))
			if firstNav {
				sb.WriteString("  await page.waitForLoadState('networkidle');\n")
				firstNav = false
			}
		case "click":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).click();\n", jsStringLiteral(a.Selector)))
				sb.WriteString("  await page.waitForTimeout(500);\n")
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: click recorded DOM index %d; rerun web_page extract to choose a stable selector.\n", a.Index))
			}
		case "input":
			if a.Selector != "" {
				sb.WriteString(fmt.Sprintf("  await page.locator(%s).fill(%s);\n", jsStringLiteral(a.Selector), jsStringLiteral(a.Value)))
			} else {
				sb.WriteString(fmt.Sprintf("  // TODO: fill recorded DOM index %d with %s; rerun web_page extract to choose a stable selector.\n", a.Index, jsStringLiteral(a.Value)))
			}
		case "scroll":
			sb.WriteString(fmt.Sprintf("  await page.evaluate(() => window.scrollBy(0, %s));\n", a.Value))
			sb.WriteString("  await page.waitForTimeout(300);\n")
		case "close":
			sb.WriteString("  // close recorded browser tab\n")
		}
	}
	sb.WriteString("\n  console.log('Done!');\n")
	sb.WriteString("  await page.waitForTimeout(2000);\n")
	sb.WriteString("  await browser.close();\n")
	sb.WriteString("})();\n")
	return sb.String()
}

// ───────────────── Web Action Recorder Tool ─────────────────

// WebReplayTool exposes recorded web actions for export.
type WebReplayTool struct{}

func (t *WebReplayTool) Name() string { return "web_replay" }
func (t *WebReplayTool) Description() string {
	return "Export recorded web actions as replay scripts. Actions: export (JSON log), playwright (.spec.ts), standalone (Node.js script), clear. Use after browser automation."
}
func (t *WebReplayTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"export", "playwright", "standalone", "clear"},
				"description": "export: JSON log. playwright: .spec.ts. standalone: Node.js script. clear: reset.",
			},
			"test_name": map[string]interface{}{
				"type":        "string",
				"description": "Test name for playwright export (default: 'web-automation')",
			},
		},
		"required": []string{"action"},
	}
}

func (t *WebReplayTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	testName, _ := params["test_name"].(string)
	if testName == "" {
		testName = "web-automation"
	}

	switch action {
	case "export":
		result := webRecorder.Export()
		if result == "" {
			return "No web actions recorded yet. Perform some browser operations first.", nil
		}
		return result, nil
	case "playwright":
		result := webRecorder.ExportPlaywright(testName)
		if result == "" {
			return "No web actions recorded yet.", nil
		}
		filePath := filepath.Join(".openkitty", "storage", fmt.Sprintf("%s.spec.ts", testName))
		os.MkdirAll(filepath.Dir(filePath), 0755)
		os.WriteFile(filePath, []byte(result), 0644)
		return fmt.Sprintf("Playwright test saved to %s\n\n```typescript\n%s\n```", filePath, result), nil
	case "standalone":
		result := webRecorder.ExportStandalone()
		if result == "" {
			return "No web actions recorded yet.", nil
		}
		filePath := filepath.Join(".openkitty", "storage", fmt.Sprintf("%s.js", testName))
		os.MkdirAll(filepath.Dir(filePath), 0755)
		os.WriteFile(filePath, []byte(result), 0644)
		return fmt.Sprintf("Standalone script saved to %s\nRun: node %s\n\n```javascript\n%s\n```", filePath, filePath, result), nil
	case "clear":
		webRecorder.StartRecording("")
		return "Recording cleared.", nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// httpGet performs a simple HTTP GET with a short timeout.
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Do(req)
}
