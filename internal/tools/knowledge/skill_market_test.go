package knowledge

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type marketRoundTripper func(*http.Request) (*http.Response, error)

func (transport marketRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestMarketToolInstallsSkillInsideConfiguredDirectory(t *testing.T) {
	tool := NewMarketTool("owner/repo", t.TempDir())
	tool.httpClient = &http.Client{Transport: marketRoundTripper(func(req *http.Request) (*http.Response, error) {
		body := `[{"name":"demo-skill","description":"demo","version":"1.0.0","author":"OpenKitty"}]`
		if strings.HasSuffix(req.URL.Path, "/demo-skill/SKILL.md") {
			body = "---\nname: demo-skill\n---\n"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "install",
		"query":  "demo-skill",
	})
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if !strings.Contains(result, "demo-skill") {
		t.Fatalf("installation result does not name skill: %q", result)
	}
	data, err := os.ReadFile(filepath.Join(tool.skillsDir, "demo-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if !strings.Contains(string(data), "name: demo-skill") {
		t.Fatalf("unexpected installed content: %q", data)
	}
}

func TestMarketToolRejectsTraversalBeforeNetworkAccess(t *testing.T) {
	requests := 0
	tool := NewMarketTool("owner/repo", t.TempDir())
	tool.httpClient = &http.Client{Transport: marketRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, nil
	})}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "install",
		"query":  "../outside",
	})
	if err == nil {
		t.Fatal("expected traversal name to be rejected")
	}
	if requests != 0 {
		t.Fatalf("unsafe name triggered %d network request(s)", requests)
	}
}
