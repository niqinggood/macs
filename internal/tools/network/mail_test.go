package network

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestEmailToolMetadataAndMarkdownRendering(t *testing.T) {
	tool := NewEmailTool(&EmailConfig{}, log.New(io.Discard, "", 0))
	if tool.Name() != "email" {
		t.Fatalf("Name() = %q, want email", tool.Name())
	}
	properties, ok := tool.Parameters()["properties"].(map[string]interface{})
	if !ok || properties["action"] == nil || properties["to"] == nil {
		t.Fatalf("email parameters do not expose action and recipient: %#v", tool.Parameters())
	}

	rendered := tool.generateHTMLContent("# Status\n\n**Ready**", true, "")
	if !strings.Contains(rendered, "<h1") || !strings.Contains(rendered, "<strong>Ready</strong>") {
		t.Fatalf("markdown rendering lost expected elements: %q", rendered)
	}
}

func TestEmailTriageRejectsDisabledConfiguration(t *testing.T) {
	tool := NewEmailTriageTool(EmailTriageConfig{}, nil)
	if _, err := tool.Execute(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("Execute() error = %v, want disabled configuration error", err)
	}
}
