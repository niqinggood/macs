package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This protects the exact class of workflow that previously stalled:
// fetch a full LAN skill, log in with a cookie jar, inspect an HTTP-200
// business error, correct the payload, and produce multiple deliverables.
func TestMusicSkillLANLoginAndBusinessRetryFlow(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the production regression is specifically for Windows curl.exe routing")
	}
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Skipf("curl.exe is unavailable: %v", err)
	}

	var skillRequests atomic.Int32
	var successfulSongs atomic.Int32
	skillDocument := "# Music Skill\n\nPOST /api/login\nPOST /api/generate\n" +
		strings.Repeat("Required generation field: style. Preserve this instruction.\n", 180) +
		"END_OF_COMPLETE_SKILL\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/music-skill.md":
			skillRequests.Add(1)
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = w.Write([]byte(skillDocument))
		case "/api/login":
			var credentials map[string]string
			if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil ||
				credentials["username"] != "user1" || credentials["password"] != "user1" {
				http.Error(w, `{"status":"error","error":"invalid credentials"}`, http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "music_session", Value: "authenticated", Path: "/"})
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/generate":
			cookie, err := r.Cookie("music_session")
			if err != nil || cookie.Value != "authenticated" {
				http.Error(w, `{"status":"error","error":"login required"}`, http.StatusUnauthorized)
				return
			}
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if strings.TrimSpace(payload["style"]) == "" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"error","error":"AI could not parse a valid style"}`))
				return
			}
			id := successfulSongs.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"status":"ok","data":{"status":"completed","audio_url":"%s/audio/%d.mp3"}}`, serverURLFromRequest(r), id)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	webTool := NewWebFetchTool(3, "", false, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	skillOutput, err := webTool.Execute(ctx, map[string]interface{}{"url": server.URL + "/music-skill.md"})
	if err != nil {
		t.Fatalf("fetch complete skill: %v", err)
	}
	if skillRequests.Load() != 1 || !strings.Contains(skillOutput, "END_OF_COMPLETE_SKILL") {
		t.Fatalf("skill was not fetched once and preserved completely: requests=%d tail=%v", skillRequests.Load(), strings.Contains(skillOutput, "END_OF_COMPLETE_SKILL"))
	}

	workspace := t.TempDir()
	cookiePath := filepath.Join(workspace, "cookies.txt")
	loginJSON := filepath.Join(workspace, "login.json")
	invalidJSON := filepath.Join(workspace, "invalid.json")
	validJSON := filepath.Join(workspace, "valid.json")
	for path, body := range map[string]string{
		loginJSON:   `{"username":"user1","password":"user1"}`,
		invalidJSON: `{"prompt":"make a hit song"}`,
		validJSON:   `{"prompt":"make a hit song","style":"modern Mandarin pop"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	cli := NewCLIToolWithSafety(workspace, false)
	run := func(command string) string {
		t.Helper()
		output, err := cli.Execute(context.Background(), map[string]interface{}{
			"command": command,
			"timeout": float64(10),
		})
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, output)
		}
		return output
	}

	loginOutput := run(fmt.Sprintf(`curl -fsS -c "%s" -H "Content-Type: application/json" --data-binary "@%s" "%s/api/login"`, cookiePath, loginJSON, server.URL))
	if !strings.Contains(loginOutput, `"status":"ok"`) {
		t.Fatalf("login did not succeed: %s", loginOutput)
	}

	invalidOutput := run(fmt.Sprintf(`curl -fsS -b "%s" -H "Content-Type: application/json" --data-binary "@%s" "%s/api/generate"`, cookiePath, invalidJSON, server.URL))
	if !strings.Contains(invalidOutput, `"status":"error"`) || !strings.Contains(invalidOutput, "valid style") {
		t.Fatalf("business error detail was lost: %s", invalidOutput)
	}
	if !ToolExecutionSucceededForTool("cli", invalidOutput, nil) {
		t.Fatalf("HTTP-200 business payload must remain visible data, not become a tool crash: %s", invalidOutput)
	}

	for i := 1; i <= 3; i++ {
		output := run(fmt.Sprintf(`curl -fsS -b "%s" -H "Content-Type: application/json" --data-binary "@%s" "%s/api/generate"`, cookiePath, validJSON, server.URL))
		want := fmt.Sprintf("/audio/%d.mp3", i)
		if !strings.Contains(output, `"status":"completed"`) || !strings.Contains(output, want) {
			t.Fatalf("song %d was not completed: %s", i, output)
		}
	}
	if successfulSongs.Load() != 3 {
		t.Fatalf("generated songs = %d, want 3", successfulSongs.Load())
	}
}

func serverURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
