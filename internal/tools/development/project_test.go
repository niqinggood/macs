package development

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProjectLanguage(t *testing.T) {
	tests := []struct {
		manifest string
		want     string
	}{
		{manifest: "go.mod", want: "Go"},
		{manifest: "package.json", want: "Node"},
		{manifest: "requirements.txt", want: "Python"},
		{manifest: "pyproject.toml", want: "Python"},
		{manifest: "Cargo.toml", want: "Rust"},
	}
	for _, test := range tests {
		t.Run(test.manifest, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, test.manifest), nil, 0644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			if got := DetectProjectLanguage(directory); got != test.want {
				t.Fatalf("DetectProjectLanguage() = %q, want %q", got, test.want)
			}
		})
	}
}
