package operations

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestOpsHostConfigDefaults(t *testing.T) {
	host := OpsHostConfig{}
	if got := host.sshPort(); got != 22 {
		t.Fatalf("default SSH port = %d, want 22", got)
	}
	if got := host.sshUser(); got != "root" {
		t.Fatalf("default SSH user = %q, want root", got)
	}

	host.Port = 2222
	host.User = "deploy"
	if got := host.sshPort(); got != 2222 {
		t.Fatalf("configured SSH port = %d, want 2222", got)
	}
	if got := host.sshUser(); got != "deploy" {
		t.Fatalf("configured SSH user = %q, want deploy", got)
	}
}

func TestServerHealthToolRejectsUnknownTarget(t *testing.T) {
	tool := NewServerHealthTool(
		[]OpsHostConfig{{Name: "production"}},
		log.New(io.Discard, "", 0),
	)

	_, err := tool.Execute(context.Background(), map[string]interface{}{"target": "missing"})
	if err == nil || !strings.Contains(err.Error(), "no hosts matching target") {
		t.Fatalf("Execute error = %v, want unknown-target error", err)
	}
}

func TestLogTraceToolReturnsRemoteGuidance(t *testing.T) {
	tool := NewLogTraceTool(log.New(io.Discard, "", 0))
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"host":    "production",
		"path":    "/var/log/service.log",
		"pattern": "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "requires ops host config") {
		t.Fatalf("Execute result = %q, want remote configuration guidance", result)
	}
}

func TestKubeMedicToolRejectsUnknownSubcommand(t *testing.T) {
	tool := NewKubeMedicTool(log.New(io.Discard, "", 0))
	_, err := tool.Execute(context.Background(), map[string]interface{}{"subcommand": "delete"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("Execute error = %v, want unknown-subcommand error", err)
	}
}
