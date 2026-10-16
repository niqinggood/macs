package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests deliberately cover different WebArena task families before any
// production fix is made: information seeking, multi-tab site navigation,
// content/configuration guarded by a native dialog, and open Shadow DOM.
// They are local and deterministic; they are not official WebArena scores.

func TestWebArenaPopupTabLifecycle(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run local browser generalization tests")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Operations Portal</title></head>
<body><h1>Operations portal</h1><a target="_blank" href="/workspace">Open development workspace</a></body></html>`)
	})
	mux.HandleFunc("/workspace", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Development Workspace</title></head>
<body><h1>Development workspace</h1><p>Popup record 77</p></body></html>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, targetID := newWebArenaGeneralizationBrowser(t, server.URL, 30*time.Second)
	extracted := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	popupIndex := webArenaElementIndex(t, extracted, "Open development workspace")
	clickResult := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(popupIndex),
	})

	var targets string
	var popupTargetID string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		targets, err = (&WebTargetsTool{}).Execute(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		popupTargetID = webArenaTargetIDByTitle(targets, "Development Workspace")
		if popupTargetID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if popupTargetID == "" {
		t.Fatalf("new tab was not discoverable:\n%s", targets)
	}
	if !strings.Contains(clickResult, popupTargetID) ||
		!strings.Contains(clickResult, "Continue the workflow in the new target_id") {
		t.Fatalf("click result did not disclose the newly opened tab:\n%s", clickResult)
	}

	popup := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": popupTargetID,
	})
	if !strings.Contains(popup, "Popup record 77") {
		t.Fatalf("new tab content unavailable:\n%s", popup)
	}

	if _, err := (&WebCloseTool{}).Execute(ctx, map[string]interface{}{"target_id": popupTargetID}); err != nil {
		t.Fatalf("close popup tab: %v", err)
	}
	targets, err := (&WebTargetsTool{}).Execute(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(targets, popupTargetID) {
		t.Fatalf("web_close reported success but popup target still exists:\n%s", targets)
	}
}

func TestWebArenaNativeDialogPrimitive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run local browser generalization tests")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Review Admin</title></head><body>
<button id="delete" onclick="if(confirm('Delete review R-17?')) document.querySelector('#result').textContent='Deleted R-17'">Delete R-17</button>
<p id="result"></p></body></html>`)
	}))
	defer server.Close()

	// Browser startup is part of this outer budget and can be slow on a cold
	// Windows host. Keep the click's own 3s deadline below as the behavior gate.
	ctx, targetID := newWebArenaGeneralizationBrowser(t, server.URL, 25*time.Second)
	extracted := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	deleteIndex := webArenaElementIndex(t, extracted, "Delete R-17")

	clickCtx, cancelClick := context.WithTimeout(ctx, 3*time.Second)
	clickResult, err := (&WebPageTool{}).Execute(clickCtx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(deleteIndex),
	})
	cancelClick()
	if err != nil {
		t.Fatalf("click should return a pending-dialog result instead of hanging: %v", err)
	}
	if !strings.Contains(strings.ToLower(clickResult), "dialog") {
		t.Fatalf("click did not report the opened native dialog: %s", clickResult)
	}

	if _, err := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "dialog", "target_id": targetID, "accept": true,
	}); err != nil {
		t.Fatalf("accept native dialog: %v", err)
	}
	result := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "wait", "target_id": targetID, "text": "Deleted R-17", "timeout_ms": float64(2000),
	})
	if !strings.Contains(result, "Wait condition satisfied") {
		t.Fatalf("dialog acceptance did not complete deletion: %s", result)
	}
}

func TestWebArenaOpenShadowDOMPrimitives(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run local browser generalization tests")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Shadow Approval</title></head><body>
<approval-panel></approval-panel>
<script>
customElements.define('approval-panel', class extends HTMLElement {
  constructor() {
    super();
    const root = this.attachShadow({mode:'open'});
    root.innerHTML = '<h1>Inventory approval</h1>' +
      '<p>SKU NX-9 has 17 units</p>' +
      '<label for="request">Request title</label><input id="request">' +
      '<button id="save">Save request</button><p id="result"></p>';
    root.querySelector('#save').addEventListener('click', function() {
      root.querySelector('#result').textContent = 'Saved: ' + root.querySelector('#request').value;
    });
  }
});
</script></body></html>`)
	}))
	defer server.Close()

	ctx, targetID := newWebArenaGeneralizationBrowser(t, server.URL, 30*time.Second)
	extracted := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	for _, want := range []string{"Inventory approval", "SKU NX-9 has 17 units", "Request title", "Save request"} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("open Shadow DOM extract missing %q:\n%s", want, extracted)
		}
	}

	requestIndex := webArenaElementIndex(t, extracted, "Request title")
	saveIndex := webArenaElementIndex(t, extracted, "Save request")
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "input", "target_id": targetID, "index": float64(requestIndex), "text": "Restock NX-9",
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(saveIndex),
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "wait", "target_id": targetID, "text": "Saved: Restock NX-9", "timeout_ms": float64(2000),
	})
}

func TestWebArenaDynamicDOMStaleIndexRecovery(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run local browser generalization tests")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Dynamic Queue</title></head><body>
<div id="queue"><button id="refresh">Refresh queue</button><button>Old action</button></div>
<p id="result"></p>
<script>
document.querySelector('#refresh').addEventListener('click', function() {
  document.querySelector('#queue').innerHTML = '<button id="approve">Approve current item</button>';
  document.querySelector('#approve').addEventListener('click', function() {
    document.querySelector('#result').textContent = 'Current item approved';
  });
});
</script></body></html>`)
	}))
	defer server.Close()

	ctx, targetID := newWebArenaGeneralizationBrowser(t, server.URL, 30*time.Second)
	extracted := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	refreshIndex := webArenaElementIndex(t, extracted, "Refresh queue")
	oldActionIndex := webArenaElementIndex(t, extracted, "Old action")
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(refreshIndex),
	})

	_, staleErr := (&WebPageTool{}).Execute(ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(oldActionIndex),
	})
	if staleErr == nil || !strings.Contains(strings.ToLower(staleErr.Error()), "stale") {
		t.Fatalf("stale index should fail with recovery guidance, got: %v", staleErr)
	}

	refreshed := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "extract", "target_id": targetID,
	})
	approveIndex := webArenaElementIndex(t, refreshed, "Approve current item")
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "click", "target_id": targetID, "index": float64(approveIndex),
	})
	mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "wait", "target_id": targetID, "text": "Current item approved", "timeout_ms": float64(2000),
	})
}

func TestWebArenaScreenshotPrimitive(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_CDP_SMOKE")) != "1" {
		t.Skip("set OPENKITTY_CDP_SMOKE=1 to run local browser generalization tests")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Screenshot Fixture</title></head><body>
<shadow-card></shadow-card><script>
customElements.define('shadow-card', class extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({mode:'open'}).innerHTML = '<h1>Visible shadow heading</h1>';
  }
});
</script></body></html>`)
	}))
	defer server.Close()

	ctx, targetID := newWebArenaGeneralizationBrowser(t, server.URL, 20*time.Second)
	screenshotPath := filepath.Join(t.TempDir(), "shadow-page.png")
	result := mustWebPageAction(t, ctx, map[string]interface{}{
		"action": "screenshot", "target_id": targetID, "file": screenshotPath,
	})
	info, err := os.Stat(screenshotPath)
	if err != nil {
		t.Fatalf("screenshot file unavailable: %v; result=%s", err, result)
	}
	if info.Size() < 100 {
		t.Fatalf("screenshot unexpectedly small: %d bytes", info.Size())
	}
}

func TestWebArenaGeneralizationMiniMax(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_WEBARENA_GENERALIZATION")) != "1" {
		t.Skip("set OPENKITTY_WEBARENA_GENERALIZATION=1 to run the MiniMax generalization batch")
	}
	if strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")) == "" {
		t.Fatal("MINIMAX_API_KEY is required through the process environment")
	}

	t.Run("information-seeking-pagination", testWebArenaMiniMaxPagination)
	t.Run("multi-tab-development-workflow", testWebArenaMiniMaxMultiTab)
	t.Run("native-dialog-batch-delete", testWebArenaMiniMaxDialogDelete)
	t.Run("open-shadow-dom-form", testWebArenaMiniMaxShadowForm)
}

func testWebArenaMiniMaxPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "2":
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Order History - Page 2</title></head><body>
<h1>Order history</h1><p>Page 2 of 3</p><table>
<tr><th>Order</th><th>Item</th><th>Date</th></tr>
<tr><td>ORD-208</td><td>Shampoo</td><td>2026-01-14</td></tr>
<tr><td>ORD-199</td><td>Desk lamp</td><td>2025-12-20</td></tr></table>
<a href="/orders?page=1">Previous</a><a href="/orders?page=3">Next</a></body></html>`)
		case "3":
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Order History - Page 3</title></head><body>
<h1>Order history</h1><p>Page 3 of 3</p><table>
<tr><th>Order</th><th>Item</th><th>Date</th></tr>
<tr><td>ORD-184</td><td>Keyboard</td><td>2025-11-02</td></tr>
<tr><td>ORD-177</td><td>Shampoo</td><td>2025-09-08</td></tr></table>
<a href="/orders?page=2">Previous</a></body></html>`)
		default:
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Order History - Page 1</title></head><body>
<h1>Order history</h1><p>Page 1 of 3</p><table>
<tr><th>Order</th><th>Item</th><th>Date</th></tr>
<tr><td>ORD-221</td><td>USB cable</td><td>2026-02-03</td></tr>
<tr><td>ORD-143</td><td>Shampoo</td><td>2025-04-19</td></tr></table>
<a href="/orders?page=2">Next</a></body></html>`)
		}
	}))
	defer server.Close()

	run := newWebArenaGeneralizationAgent(t, "information-seeking-pagination", server.URL,
		[]string{"web_page"}, 2*time.Minute)
	prompt := `Review every page of the currently open order history. Find the most recent purchase of Shampoo.
Report exactly the order ID and purchase date. Do not stop after finding the first matching row.`
	started := time.Now()
	answer, err := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)
	writeWebArenaGeneralizationArtifact(t, run.Workspace, webArenaGeneralizationArtifact{
		Case: "information-seeking-pagination", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(err),
	})
	if err != nil {
		t.Fatalf("pagination agent: %v", err)
	}
	if !strings.Contains(answer, "ORD-208") || !strings.Contains(answer, "2026-01-14") {
		t.Fatalf("agent did not return the latest Shampoo order: %s", answer)
	}
}

func testWebArenaMiniMaxMultiTab(t *testing.T) {
	var state struct {
		sync.Mutex
		readyID string
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Engineering Portal</title></head><body>
<h1>Engineering portal</h1><a target="_blank" href="/workspace">Open development workspace</a></body></html>`)
	})
	mux.HandleFunc("/workspace", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Development Workspace</title></head><body>
<h1>Open merge requests</h1><table><tr><th>MR</th><th>Title</th><th>Assignee</th><th>Priority</th></tr>
<tr><td><a href="/workspace/mr/18">!18</a></td><td>Refactor cache</td><td>alex</td><td>P1</td></tr>
<tr><td><a href="/workspace/mr/42">!42</a></td><td>Fix payment race</td><td>jdoe</td><td>P0</td></tr>
<tr><td><a href="/workspace/mr/57">!57</a></td><td>Update docs</td><td>jdoe</td><td>P2</td></tr>
</table></body></html>`)
	})
	mux.HandleFunc("/workspace/mr/42", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>MR !42</title></head><body>
<h1>!42 Fix payment race</h1><p>Assignee: jdoe</p><p>Priority: P0</p>
<button id="ready">Mark ready for review</button><p id="result"></p>
<script>document.querySelector('#ready').addEventListener('click', async function() {
  const response = await fetch('/api/ready?id=42', {method:'POST'});
  document.querySelector('#result').textContent = await response.text();
});</script></body></html>`)
	})
	mux.HandleFunc("/workspace/mr/18", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>MR !18</title></head><body><h1>!18 Refactor cache</h1></body></html>`)
	})
	mux.HandleFunc("/workspace/mr/57", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>MR !57</title></head><body><h1>!57 Update docs</h1></body></html>`)
	})
	mux.HandleFunc("/api/ready", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		state.readyID = r.URL.Query().Get("id")
		state.Unlock()
		_, _ = io.WriteString(w, "MR !42 is ready for review")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	run := newWebArenaGeneralizationAgent(t, "multi-tab-development-workflow", server.URL,
		[]string{"web_targets", "web_page", "web_close"}, 2*time.Minute)
	prompt := `Use the currently open engineering portal. Open the development workspace.
Among open merge requests assigned to jdoe, choose the one with the highest priority and mark it ready for review.
Wait for the exact confirmation, close the workspace tab you opened, and report that confirmation.`
	started := time.Now()
	answer, err := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)

	state.Lock()
	readyID := state.readyID
	state.Unlock()
	targets, targetsErr := (&WebTargetsTool{}).Execute(run.Context, nil)
	popupStillOpen := targetsErr == nil && strings.Contains(targets, server.URL+"/workspace")
	writeWebArenaGeneralizationArtifact(t, run.Workspace, webArenaGeneralizationArtifact{
		Case: "multi-tab-development-workflow", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(err),
		State: map[string]interface{}{"ready_id": readyID, "popup_still_open": popupStillOpen},
	})
	if err != nil {
		t.Fatalf("multi-tab agent: %v", err)
	}
	if readyID != "42" {
		t.Fatalf("agent marked wrong merge request ready: %q; answer=%s", readyID, answer)
	}
	if !strings.Contains(answer, "MR !42 is ready for review") {
		t.Fatalf("agent did not report exact confirmation: %s", answer)
	}
	if popupStillOpen {
		t.Fatalf("agent/web_close reported completion but workspace tab is still open:\n%s", targets)
	}
}

func testWebArenaMiniMaxDialogDelete(t *testing.T) {
	var state struct {
		sync.Mutex
		deleted map[string]bool
	}
	state.deleted = make(map[string]bool)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Review Moderation</title></head><body>
<h1>Product reviews</h1><div id="reviews">
<article id="R-11"><p>R-11 by Yoke: scam link</p><button onclick="deleteReview('R-11','Yoke')">Delete R-11</button></article>
<article id="R-12"><p>R-12 by Ada: useful review</p><button onclick="deleteReview('R-12','Ada')">Delete R-12</button></article>
<article id="R-13"><p>R-13 by Yoke: fake coupon</p><button onclick="deleteReview('R-13','Yoke')">Delete R-13</button></article>
</div><p id="result"></p>
<script>
let yokeDeleted = 0;
async function deleteReview(id, author) {
  if (!confirm('Delete review ' + id + ' by ' + author + '?')) return;
  const response = await fetch('/api/delete?id=' + encodeURIComponent(id), {method:'POST'});
  if (!response.ok) return;
  document.getElementById(id).remove();
  if (author === 'Yoke') yokeDeleted++;
  document.querySelector('#result').textContent = yokeDeleted === 2
    ? 'Deleted 2 reviews by Yoke' : 'Deleted ' + id;
}
</script></body></html>`)
	})
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		state.deleted[r.URL.Query().Get("id")] = true
		state.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	run := newWebArenaGeneralizationAgent(t, "native-dialog-batch-delete", server.URL,
		[]string{"web_page"}, 90*time.Second)
	prompt := `Delete every product review authored by Yoke and accept each confirmation dialog.
Do not delete reviews by anyone else. After both deletions, wait for and report the exact visible confirmation.`
	started := time.Now()
	answer, err := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)

	state.Lock()
	deleted11 := state.deleted["R-11"]
	deleted12 := state.deleted["R-12"]
	deleted13 := state.deleted["R-13"]
	state.Unlock()
	writeWebArenaGeneralizationArtifact(t, run.Workspace, webArenaGeneralizationArtifact{
		Case: "native-dialog-batch-delete", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(err),
		State: map[string]interface{}{"R-11": deleted11, "R-12": deleted12, "R-13": deleted13},
	})
	if err != nil {
		t.Fatalf("dialog-delete agent: %v", err)
	}
	if !deleted11 || deleted12 || !deleted13 {
		t.Fatalf("wrong deletion state: R-11=%t R-12=%t R-13=%t; answer=%s", deleted11, deleted12, deleted13, answer)
	}
	if !strings.Contains(answer, "Deleted 2 reviews by Yoke") {
		t.Fatalf("agent did not report exact confirmation: %s", answer)
	}
}

func testWebArenaMiniMaxShadowForm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Inventory Request</title></head><body>
<inventory-request></inventory-request>
<script>
customElements.define('inventory-request', class extends HTMLElement {
  constructor() {
    super();
    const root = this.attachShadow({mode:'open'});
    root.innerHTML = '<h1>Inventory request</h1><table>' +
      '<tr><th>SKU</th><th>Current stock</th></tr><tr><td>NX-9</td><td>17</td></tr></table>' +
      '<label for="title">Request title</label><input id="title">' +
      '<label for="quantity">Requested quantity</label><input id="quantity">' +
      '<label for="priority">Priority</label><select id="priority">' +
      '<option value="normal">Normal</option><option value="high">High</option></select>' +
      '<label><input id="notify" type="checkbox"> Notify warehouse</label>' +
      '<button id="save">Save request</button><p id="result"></p>';
    root.querySelector('#save').addEventListener('click', function() {
      const title = root.querySelector('#title').value;
      const quantity = root.querySelector('#quantity').value;
      const priority = root.querySelector('#priority').value;
      const notify = root.querySelector('#notify').checked;
      const valid = title === 'Restock NX-9' && quantity === '34' && priority === 'high' && notify;
      document.body.dataset.completed = String(valid);
      root.querySelector('#result').textContent = valid
        ? 'Request saved: NX-9 quantity 34 high warehouse-notified' : 'Validation failed';
    });
  }
});
</script></body></html>`)
	}))
	defer server.Close()

	run := newWebArenaGeneralizationAgent(t, "open-shadow-dom-form", server.URL,
		[]string{"web_page"}, 90*time.Second)
	prompt := `Use the inventory request component on the current page.
For SKU NX-9, request twice the current stock with title "Restock NX-9", priority High, and Notify warehouse checked.
Save the request, wait for the visible confirmation, and report it exactly.`
	started := time.Now()
	answer, err := run.Agent.Generate(run.Context, prompt)
	duration := time.Since(started)
	state, stateErr := (&WebPageTool{}).Execute(run.Context, map[string]interface{}{
		"action": "eval", "target_id": run.TargetID,
		"code": `JSON.stringify({
			completed: document.body.dataset.completed,
			result: document.querySelector('inventory-request').shadowRoot.querySelector('#result').textContent
		})`,
	})
	writeWebArenaGeneralizationArtifact(t, run.Workspace, webArenaGeneralizationArtifact{
		Case: "open-shadow-dom-form", Prompt: prompt, Answer: answer,
		DurationMS: durationMilliseconds(duration), Error: errorString(err),
		State: map[string]interface{}{"browser_state": state, "state_error": errorString(stateErr)},
	})
	if err != nil {
		t.Fatalf("shadow-form agent: %v", err)
	}
	if stateErr != nil {
		t.Fatalf("read shadow-form state: %v", stateErr)
	}
	if !strings.Contains(state, `"completed":"true"`) ||
		!strings.Contains(state, "Request saved: NX-9 quantity 34 high warehouse-notified") {
		t.Fatalf("shadow-form state incorrect: %s; answer=%s", state, answer)
	}
	if !strings.Contains(answer, "Request saved: NX-9 quantity 34 high warehouse-notified") {
		t.Fatalf("agent did not report exact confirmation: %s", answer)
	}
}

type webArenaGeneralizationAgentRun struct {
	Agent     *LLMAgent
	Context   context.Context
	TargetID  string
	Workspace string
}

func newWebArenaGeneralizationAgent(
	t *testing.T,
	caseName string,
	startURL string,
	enabledTools []string,
	timeout time.Duration,
) webArenaGeneralizationAgentRun {
	t.Helper()
	ctx, targetID := newWebArenaGeneralizationBrowser(t, startURL, timeout)
	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	modelConfig := ModelConfig{
		Type:         "minimax",
		APIKey:       apiKey,
		ModelName:    envOrDefault("OPENKITTY_WEBARENA_MODEL", "MiniMax-M3"),
		BaseURL:      envOrDefault("OPENKITTY_WEBARENA_BASE_URL", "https://api.minimaxi.com/v1"),
		Temperature:  0.1,
		MaxTokens:    8192,
		ThinkingMode: "disabled",
	}
	workspace := webArenaGeneralizationWorkspace(t, caseName)
	globalConfig := &Config{
		Workspace:                  workspace,
		ModelConfig:                map[string]ModelConfig{"webarena-generalization": modelConfig},
		PermissionMode:             "bypassPermissions",
		DangerouslySkipPermissions: true,
	}
	globalConfig.MemoryConfig.Enabled = false
	globalConfig.SkillsConfig.DisEnabled = true
	globalConfig.ToolsConfig.Enabled = true
	memoryDisabled := false
	agentConfig := AgentConfig{
		Name:          "WebArenaGeneralization",
		Type:          AgentTypeCustom,
		Model:         "webarena-generalization",
		SystemMessage: webArenaGeneralizationSystemMessage,
		Temperature:   0.1,
		MaxTokens:     8192,
		MaxIterations: 40,
		HasMemory:     &memoryDisabled,
		EnabledTools:  enabledTools,
	}
	agent := NewLLMAgent(agentConfig, &modelConfig, nil, globalConfig, log.New(io.Discard, "", 0))
	t.Cleanup(func() {
		if agent.ptcServer != nil {
			agent.ptcServer.Stop()
		}
		if agent.lspManager != nil {
			agent.lspManager.Close()
		}
	})
	return webArenaGeneralizationAgentRun{
		Agent: agent, Context: ctx, TargetID: targetID, Workspace: workspace,
	}
}

func newWebArenaGeneralizationBrowser(
	t *testing.T,
	startURL string,
	timeout time.Duration,
) (context.Context, string) {
	t.Helper()
	t.Setenv("OPENKITTY_CDP_AUTO_START", "1")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	navigation, err := (&WebNavigateTool{}).Execute(ctx, map[string]interface{}{
		"url": startURL, "timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("navigate to generalization fixture: %v", err)
	}
	targetID := parseSmokeTargetID(navigation)
	if targetID == "" {
		t.Fatalf("could not parse browser target from: %s", navigation)
	}
	t.Cleanup(func() {
		_, _ = (&WebCloseTool{}).Execute(context.Background(), map[string]interface{}{"target_id": targetID})
	})
	return ctx, targetID
}

func webArenaTargetIDByTitle(targets, title string) string {
	for _, line := range strings.Split(targets, "\n") {
		if !strings.Contains(line, "| "+title+" |") {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		return strings.TrimSpace(parts[0])
	}
	return ""
}

func webArenaGeneralizationWorkspace(t *testing.T, caseName string) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv("OPENKITTY_WEBARENA_ARTIFACT_DIR"))
	if root == "" {
		return t.TempDir()
	}
	workspace := filepath.Join(root, caseName)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create WebArena artifact workspace: %v", err)
	}
	return workspace
}

type webArenaGeneralizationArtifact struct {
	Case       string                 `json:"case"`
	Prompt     string                 `json:"prompt"`
	Answer     string                 `json:"answer,omitempty"`
	DurationMS int64                  `json:"duration_ms"`
	Error      string                 `json:"error,omitempty"`
	State      map[string]interface{} `json:"state,omitempty"`
}

func writeWebArenaGeneralizationArtifact(
	t *testing.T,
	workspace string,
	artifact webArenaGeneralizationArtifact,
) {
	t.Helper()
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("encode WebArena artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "result.json"), data, 0o600); err != nil {
		t.Fatalf("write WebArena artifact: %v", err)
	}
}

func durationMilliseconds(duration time.Duration) int64 {
	return duration.Round(time.Millisecond).Milliseconds()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

const webArenaGeneralizationSystemMessage = `You are OpenKitty's general-purpose browser agent in a WebArena-style evaluation.
- Work only through the browser tools provided for the current task.
- Begin by extracting the current page and derive requested values from visible page content.
- Use normal user-interface actions. Do not use eval to bypass interactions or write final state.
- Re-extract after navigation, a major DOM change, or a stale-index error.
- If a click opens another tab, use web_targets to discover it and pass its target_id to web_page.
- Handle visible confirmation prompts and wait for the requested final confirmation.
- Close only tabs that the task asked you to open.
- Stop as soon as the independently verifiable requested state is complete.`
