package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeCurlSupportsOnlyImplementedFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "simple get", args: []string{"-sS", "http://example.test"}, want: true},
		{name: "json post", args: []string{"-sS", "-X", "POST", "-H", "Content-Type: application/json", "--data-raw", `{}`, "http://example.test"}, want: true},
		{name: "cookie jar", args: []string{"-c", "cookies.txt", "http://example.test"}, want: false},
		{name: "cookie input", args: []string{"-b", "cookies.txt", "http://example.test"}, want: false},
		{name: "basic auth", args: []string{"-u", "user:pass", "http://example.test"}, want: false},
		{name: "output file", args: []string{"-o", "song.mp3", "http://example.test"}, want: false},
		{name: "unknown option", args: []string{"--retry", "2", "http://example.test"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeCurlSupportsArgs(tc.args); got != tc.want {
				t.Fatalf("nativeCurlSupportsArgs(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestForceWindowsCurlExecutable(t *testing.T) {
	tests := map[string]string{
		`curl -sS https://example.test`:        `curl.exe -sS https://example.test`,
		" \tcUrL -c cookies.txt http://x":      " \tcurl.exe -c cookies.txt http://x",
		`curl.exe -sS https://example.test`:    `curl.exe -sS https://example.test`,
		`curlx -sS https://example.test`:       `curlx -sS https://example.test`,
		`powershell curl https://example.test`: `powershell curl https://example.test`,
	}
	for input, want := range tests {
		if got := forceWindowsCurlExecutable(input); got != want {
			t.Errorf("forceWindowsCurlExecutable(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCLICurlCookieJarRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific curl.exe routing test")
	}
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Skipf("curl.exe is unavailable: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "music_session", Value: "authenticated", Path: "/"})
			_, _ = w.Write([]byte("login-ok"))
		case "/check":
			cookie, err := r.Cookie("music_session")
			if err != nil || cookie.Value != "authenticated" {
				http.Error(w, "missing session cookie", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("cookie-ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	cookiePath := filepath.Join(workspace, "music-cookies.txt")
	cli := NewCLIToolWithSafety(workspace, false)

	loginCommand := fmt.Sprintf(`curl -fsS -c "%s" "%s/login"`, cookiePath, server.URL)
	loginOutput, err := cli.Execute(context.Background(), map[string]interface{}{
		"command": loginCommand,
		"timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("login command failed: %v\n%s", err, loginOutput)
	}
	if !strings.Contains(loginOutput, "login-ok") {
		t.Fatalf("login output missing success payload: %s", loginOutput)
	}
	if info, err := os.Stat(cookiePath); err != nil || info.Size() == 0 {
		t.Fatalf("cookie jar was not written: info=%v err=%v", info, err)
	}

	checkCommand := fmt.Sprintf(`curl -fsS -b "%s" "%s/check"`, cookiePath, server.URL)
	checkOutput, err := cli.Execute(context.Background(), map[string]interface{}{
		"command": checkCommand,
		"timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("authenticated command failed: %v\n%s", err, checkOutput)
	}
	if !strings.Contains(checkOutput, "cookie-ok") {
		t.Fatalf("authenticated output missing cookie confirmation: %s", checkOutput)
	}
}
