package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mac/internal/textutil"
)

func TestWebPageSchemaCoversWebArenaPrimitives(t *testing.T) {
	schema := (&WebPageTool{}).Parameters()
	properties, _ := schema["properties"].(map[string]interface{})
	action, _ := properties["action"].(map[string]interface{})
	values, _ := action["enum"].([]string)
	available := make(map[string]bool, len(values))
	for _, value := range values {
		available[value] = true
	}
	for _, required := range []string{
		"extract", "click", "dialog", "input", "select", "check", "hover", "press", "upload",
		"scroll", "back", "forward", "reload", "wait", "eval", "screenshot",
	} {
		if !available[required] {
			t.Fatalf("web_page action schema is missing %q: %v", required, values)
		}
	}
}

func TestFormatExtractResultIncludesVisibleTextAndTypedCounts(t *testing.T) {
	raw := `{
		"header":{"title":"Admin","url":"https://example.test/admin"},
		"content":"Top product\nQuest Lumaflex Band",
		"elements":[{"index":0,"tag":"select","role":"combobox","name":"Status","attrs":{"options":[{"label":"Open","value":"open","selected":true}]}}],
		"footer":{"scrollY":0,"scrollMax":100,"interactiveCount":1,"returnedCount":1,"textChars":31,"textTruncated":false,"elementsTruncated":false}
	}`
	formatted := formatExtractResult(raw)
	for _, want := range []string{"Top product", "Quest Lumaflex Band", `[0] combobox "Status"`, "Open (selected)", "1 interactive element(s), 1 returned"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted extract missing %q:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "%!") {
		t.Fatalf("formatted extract contains a fmt type error:\n%s", formatted)
	}
}

// TestWebArenaBrowserPrimitives is a deterministic, local-browser proxy for the
// WebArena interaction patterns. It deliberately mixes links/buttons with form
// controls so an index returned by extract must identify the same DOM element in
// every later action.
func TestWebArenaBrowserPrimitives(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run the local WebArena browser proxy")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html>
<html>
<head><title>WebArena Proxy</title></head>
<body>
  <h1>2022 sales dashboard</h1>
  <table>
    <thead><tr><th>Product</th><th>Units sold</th></tr></thead>
    <tbody><tr><td>Quest Lumaflex Band</td><td>315</td></tr></tbody>
  </table>
  <a id="details" href="/details">View order details</a>
  <button id="account" onmouseenter="document.querySelector('#hover-result').hidden=false">Account menu</button>
  <p id="hover-result" hidden>Signed in as johndoe</p>
  <label for="title">Issue title</label>
  <input id="title" placeholder="Issue title">
  <label for="body">Issue body</label>
  <textarea id="body" placeholder="Issue body"></textarea>
  <label for="assignee">Assignee</label>
  <select id="assignee">
    <option value="">Choose an assignee</option>
    <option value="roshan">Roshanjossey</option>
  </select>
  <label><input id="notify" type="checkbox"> Notify watchers</label>
  <label for="attachment">Attachment</label>
  <input id="attachment" type="file">
  <button id="save">Create issue</button>
  <p id="key-result"></p>
  <p id="save-result"></p>
  <script>
    document.querySelector('#title').addEventListener('keydown', function(event) {
      if (event.key === 'Enter') document.querySelector('#key-result').textContent = 'enter received';
    });
    document.querySelector('#attachment').addEventListener('change', function(event) {
      document.querySelector('#save-result').textContent = 'file: ' + event.target.files[0].name;
    });
    document.querySelector('#save').addEventListener('click', function() {
      setTimeout(function() {
        document.querySelector('#save-result').textContent = [
          document.querySelector('#title').value,
          document.querySelector('#body').value,
          document.querySelector('#assignee').value,
          String(document.querySelector('#notify').checked)
        ].join('|');
      }, 75);
    });
  </script>
</body>
</html>`)
	})
	mux.HandleFunc("/details", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Order details</title></head><body><h1>Order 302</h1><p>Status: Pending</p></body></html>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	port, err := unusedLocalTCPPort("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	oldManager := cdpManager
	cdpManager = &CDPManager{
		debugHost: "127.0.0.1",
		debugPort: port,
		tabCancel: make(map[string]context.CancelFunc),
	}
	t.Setenv("OPENKITTY_CDP_AUTO_START", "1")
	t.Cleanup(func() {
		cdpManager.shutdown()
		cdpManager = oldManager
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	nav, err := (&WebNavigateTool{}).Execute(ctx, map[string]interface{}{
		"url":     server.URL,
		"timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("web_navigate failed: %v", err)
	}
	targetID := parseSmokeTargetID(nav)
	if targetID == "" {
		t.Fatalf("could not parse target id from: %s", nav)
	}
	defer func() {
		_, _ = (&WebCloseTool{}).Execute(context.Background(), map[string]interface{}{"target_id": targetID})
	}()

	extracted := mustWebPageAction(t, ctx, map[string]interface{}{
		"action":         "extract",
		"target_id":      targetID,
		"max_elements":   float64(50),
		"max_text_chars": float64(8000),
	})
	for _, want := range []string{
		"2022 sales dashboard",
		"Quest Lumaflex Band",
		"Units sold",
		"Issue title",
		"Roshanjossey",
		"Notify watchers",
	} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("extract should expose %q for WebArena-style reasoning:\n%s", want, extracted)
		}
	}

	titleIndex := webArenaElementIndex(t, extracted, "Issue title")
	bodyIndex := webArenaElementIndex(t, extracted, "Issue body")
	assigneeIndex := webArenaElementIndex(t, extracted, "Assignee")
	notifyIndex := webArenaElementIndex(t, extracted, "Notify watchers")
	attachmentIndex := webArenaElementIndex(t, extracted, "Attachment")
	saveIndex := webArenaElementIndex(t, extracted, "Create issue")
	accountIndex := webArenaElementIndex(t, extracted, "Account menu")
	detailsIndex := webArenaElementIndex(t, extracted, "View order details")

	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "input", "target_id": targetID, "index": float64(titleIndex), "text": "401 bad gateway",
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "input", "target_id": targetID, "index": float64(bodyIndex), "text": "Reproduce through the proxy",
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "select", "target_id": targetID, "index": float64(assigneeIndex), "value": "Roshanjossey",
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "check", "target_id": targetID, "index": float64(notifyIndex), "checked": true,
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "press", "target_id": targetID, "index": float64(titleIndex), "key": "Enter",
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "hover", "target_id": targetID, "index": float64(accountIndex),
	})

	uploadPath := filepath.Join(t.TempDir(), "webarena-note.txt")
	if err := os.WriteFile(uploadPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "upload", "target_id": targetID, "index": float64(attachmentIndex), "files": []interface{}{uploadPath},
	})

	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(saveIndex),
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "wait", "target_id": targetID, "text": "401 bad gateway|Reproduce through the proxy|roshan|true", "timeout_ms": float64(3000),
	})

	state := mustWebPageAction(t, ctx, map[string]interface{}{
		"action":    "eval",
		"target_id": targetID,
		"code": `JSON.stringify({
			title: document.querySelector('#title').value,
			body: document.querySelector('#body').value,
			assignee: document.querySelector('#assignee').value,
			notify: document.querySelector('#notify').checked,
			key: document.querySelector('#key-result').textContent,
			hover: document.querySelector('#hover-result').hidden
		})`,
	})
	for _, want := range []string{
		`"title":"401 bad gateway"`,
		`"body":"Reproduce through the proxy"`,
		`"assignee":"roshan"`,
		`"notify":true`,
		`"key":"enter received"`,
		`"hover":false`,
	} {
		if !strings.Contains(state, want) {
			t.Fatalf("browser state missing %s: %s", want, state)
		}
	}

	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(detailsIndex),
	})
	details := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	if !strings.Contains(details, "Order 302") || !strings.Contains(details, "Status: Pending") {
		t.Fatalf("click navigation should reach the detail page:\n%s", details)
	}
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "back", "target_id": targetID,
	})
	back := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	if !strings.Contains(back, "2022 sales dashboard") {
		t.Fatalf("back should restore the dashboard:\n%s", back)
	}
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "forward", "target_id": targetID,
	})
	forward := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	if !strings.Contains(forward, "Order 302") {
		t.Fatalf("forward should restore the detail page:\n%s", forward)
	}
}

func mustWebPageAction(t *testing.T, ctx context.Context, params map[string]interface{}) string {
	t.Helper()
	started := time.Now()
	out, err := (&WebPageTool{}).Execute(ctx, params)
	if err != nil {
		t.Fatalf("web_page action %q failed: %v", params["action"], err)
	}
	t.Logf("web_page %-8s completed in %s: %s", params["action"], time.Since(started).Round(time.Millisecond), textutil.TruncateRunes(strings.ReplaceAll(out, "\n", " | "), 240))
	return out
}

func webArenaElementIndex(t *testing.T, extracted, label string) int {
	t.Helper()
	linePattern := regexp.MustCompile(`(?m)^\s*\[(\d+)\].*` + regexp.QuoteMeta(label) + `.*$`)
	match := linePattern.FindStringSubmatch(extracted)
	if len(match) != 2 {
		t.Fatalf("could not find interactive element %q in:\n%s", label, extracted)
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return index
}
