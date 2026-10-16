package content

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceToolCloudTranscriptionResolvesWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	audioPath := filepath.Join(workspace, "sample.wav")
	if err := os.WriteFile(audioPath, []byte("audio bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Accept-Language") != "en" {
			t.Errorf("Accept-Language = %q, want en", request.Header.Get("Accept-Language"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
		}
		if string(body) != "audio bytes" {
			t.Errorf("body = %q", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"text":"hello world"}`))
	}))
	defer server.Close()
	t.Setenv("OPENKITTY_STT_ENDPOINT", server.URL)

	result, err := NewVoiceTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":   "transcribe",
		"file":     "sample.wav",
		"language": "en",
		"engine":   "cloud",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(result, "hello world") || !strings.Contains(result, "cloud STT") {
		t.Fatalf("unexpected transcription result:\n%s", result)
	}
}

func TestVoiceToolCloudTranscriptionHonorsCanceledContext(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.wav"), []byte("audio"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("OPENKITTY_STT_ENDPOINT", server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewVoiceTool(workspace).Execute(ctx, map[string]interface{}{
		"action": "transcribe",
		"file":   "sample.wav",
		"engine": "cloud",
	})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Execute() error = %v, want context canceled", err)
	}
}

func TestVoiceToolRejectsUnknownAction(t *testing.T) {
	_, err := NewVoiceTool(t.TempDir()).Execute(nil, map[string]interface{}{"action": "unsupported"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("Execute() error = %v, want unknown action", err)
	}
}
