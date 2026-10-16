package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mac/internal/textutil"
)

// TestWebArenaMiniMaxAgent is a deterministic end-to-end browser-agent proxy
// for representative WebArena read-and-write tasks. It is gated because it
// launches Chrome and makes real MiniMax API calls.
func TestWebArenaMiniMaxAgent(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPENKITTY_WEBARENA_MINIMAX")) != "1" {
		t.Skip("set OPENKITTY_WEBARENA_MINIMAX=1 to run the external MiniMax browser-agent test")
	}
	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	if apiKey == "" {
		t.Fatal("MINIMAX_API_KEY is required through the process environment")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html>
<html>
<head><title>Revenue Review</title></head>
<body>
  <h1>Quarterly revenue review</h1>
  <table>
    <thead><tr><th>Segment</th><th>Quarter</th><th>Revenue</th></tr></thead>
    <tbody>
      <tr><td>Consumer</td><td>Q2</td><td>$8,100</td></tr>
      <tr><td>Enterprise</td><td>Q2</td><td>$12,450</td></tr>
    </tbody>
  </table>
  <label for="title">Review title</label>
  <input id="title" placeholder="Review title">
  <label for="note">Revenue digits</label>
  <textarea id="note" placeholder="Revenue digits"></textarea>
  <label for="resolution">Resolution</label>
  <select id="resolution">
    <option value="open">Open</option>
    <option value="approved">Approved</option>
    <option value="rejected">Rejected</option>
  </select>
  <label><input id="finance" type="checkbox"> Notify Finance</label>
  <button id="save">Save review</button>
  <p id="result"></p>
  <script>
    document.querySelector('#save').addEventListener('click', function() {
      setTimeout(function() {
        const title = document.querySelector('#title').value;
        const note = document.querySelector('#note').value;
        const resolution = document.querySelector('#resolution').value;
        const finance = document.querySelector('#finance').checked;
        const valid = title === 'Approve Enterprise Q2' &&
          note === '12450' && resolution === 'approved' && finance;
        document.body.dataset.completed = String(valid);
        document.querySelector('#result').textContent = valid
          ? 'Review saved: Enterprise Q2 12450 approved finance-notified'
          : 'Validation failed';
      }, 100);
    });
  </script>
</body>
</html>`)
	}))
	defer server.Close()

	t.Setenv("OPENKITTY_CDP_AUTO_START", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	navigation, err := (&WebNavigateTool{}).Execute(ctx, map[string]interface{}{
		"url":     server.URL,
		"timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("navigate to WebArena proxy: %v", err)
	}
	targetID := parseSmokeTargetID(navigation)
	if targetID == "" {
		t.Fatalf("could not parse browser target from: %s", navigation)
	}
	t.Cleanup(func() {
		_, _ = (&WebCloseTool{}).Execute(context.Background(), map[string]interface{}{"target_id": targetID})
	})

	modelName := envOrDefault("OPENKITTY_WEBARENA_MODEL", "MiniMax-M3")
	modelConfig := ModelConfig{
		Type:         "minimax",
		APIKey:       apiKey,
		ModelName:    modelName,
		BaseURL:      envOrDefault("OPENKITTY_WEBARENA_BASE_URL", "https://api.minimaxi.com/v1"),
		Temperature:  0.1,
		MaxTokens:    8192,
		ThinkingMode: "disabled",
	}
	globalConfig := &Config{
		Workspace:                  t.TempDir(),
		ModelConfig:                map[string]ModelConfig{"webarena-minimax": modelConfig},
		PermissionMode:             "bypassPermissions",
		DangerouslySkipPermissions: true,
	}
	globalConfig.MemoryConfig.Enabled = false
	globalConfig.SkillsConfig.DisEnabled = true
	globalConfig.ToolsConfig.Enabled = true
	memoryDisabled := false
	agentConfig := AgentConfig{
		Name:          "WebArena",
		Type:          AgentTypeCustom,
		Model:         "webarena-minimax",
		SystemMessage: webArenaAgentSystemMessage,
		Temperature:   0.1,
		MaxTokens:     8192,
		MaxIterations: 30,
		HasMemory:     &memoryDisabled,
		EnabledTools:  []string{"web_page"},
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

	prompt := `Use the currently open browser page to complete this task:
Read the quarterly revenue table. For the Enterprise row in Q2, create an approved review with:
- Review title: Approve Enterprise Q2
- Revenue digits: the Enterprise Q2 revenue, digits only with no comma or currency symbol
- Resolution: Approved
- Notify Finance: checked
Save the review and wait until the page confirms it was saved. Then report the exact confirmation.`

	started := time.Now()
	answer, err := agent.Generate(ctx, prompt)
	if err != nil {
		t.Fatalf("WebArena MiniMax agent: %v", err)
	}
	t.Logf("browser agent completed in %s: %s", time.Since(started).Round(time.Millisecond), textutil.TruncateRunes(answer, 500))

	state := mustWebPageAction(t, ctx, map[string]interface{}{
		"action":    "eval",
		"target_id": targetID,
		"code": `JSON.stringify({
			completed: document.body.dataset.completed,
			title: document.querySelector('#title').value,
			note: document.querySelector('#note').value,
			resolution: document.querySelector('#resolution').value,
			finance: document.querySelector('#finance').checked,
			result: document.querySelector('#result').textContent
		})`,
	})
	for _, want := range []string{
		`"completed":"true"`,
		`"title":"Approve Enterprise Q2"`,
		`"note":"12450"`,
		`"resolution":"approved"`,
		`"finance":true`,
		`Review saved: Enterprise Q2 12450 approved finance-notified`,
	} {
		if !strings.Contains(state, want) {
			t.Fatalf("browser agent state missing %q: %s", want, state)
		}
	}
}

const webArenaAgentSystemMessage = `You are OpenKitty's browser agent under a WebArena-style evaluation.
- Work only through the web_page tool on the currently open page.
- Start with extract so you can read visible static text and receive stable element indices.
- Derive requested values from the page; do not guess.
- Use input, select, check, click, and wait as needed.
- After submitting, wait for and read the visible success confirmation.
- Do not use eval to bypass normal user interactions.
- Stop as soon as the requested state is visibly confirmed.`
