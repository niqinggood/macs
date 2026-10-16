package skills

import (
	"strings"
	"testing"
)

func TestSkillMetadataIgnoresMarkdownHorizontalRuleInBody(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	content := "# Investment framework\n\nOrdinary markdown body.\n\n---\n\nname: body-example\ndescription: this is prose, not metadata\n\n---\n\n## Principles\n"

	metadata := loader.ParseMetadata([]byte(content))
	if metadata.Name != "" || metadata.Description != "" {
		t.Fatalf("markdown horizontal rule was parsed as frontmatter: %+v", metadata)
	}
	if got := loader.StripFrontmatter(content); got != content {
		t.Fatalf("body without leading frontmatter must stay intact:\n%s", got)
	}
}

func TestSkillFrontmatterSupportsBOMAndCRLF(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	content := "\ufeff---\r\nname: demo\r\ndescription: CRLF metadata\r\n---\r\n# Body\r\n\r\nKeep me.\r\n"

	metadata := loader.ParseMetadata([]byte(content))
	if metadata.Name != "demo" || metadata.Description != "CRLF metadata" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	body := loader.StripFrontmatter(content)
	if strings.Contains(body, "description: CRLF metadata") || !strings.Contains(body, "# Body") {
		t.Fatalf("frontmatter was not stripped cleanly:\n%s", body)
	}
}

func TestSkillMetadataVersionCompatibilityAndPrecedence(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "legacy top-level version",
			content: "---\nversion: 1.2.3\n---\n# Body\n",
			want:    "1.2.3",
		},
		{
			name:    "standard nested version",
			content: "---\nmetadata:\n  version: 2.0.0\n---\n# Body\n",
			want:    "2.0.0",
		},
		{
			name:    "nested version takes precedence",
			content: "---\nversion: legacy\nmetadata:\n  version: standard\n---\n# Body\n",
			want:    "standard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loader.ParseMetadata([]byte(tt.content)).Version; got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}
