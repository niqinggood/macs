package codeutil

import (
	"os"
	"path/filepath"
)

// DetectProjectLanguage identifies the primary build ecosystem from its
// conventional project manifest.
func DetectProjectLanguage(directory string) string {
	manifests := []struct {
		name     string
		language string
	}{
		{name: "go.mod", language: "Go"},
		{name: "package.json", language: "Node"},
		{name: "requirements.txt", language: "Python"},
		{name: "pyproject.toml", language: "Python"},
		{name: "Cargo.toml", language: "Rust"},
	}
	for _, manifest := range manifests {
		if _, err := os.Stat(filepath.Join(directory, manifest.name)); err == nil {
			return manifest.language
		}
	}
	return ""
}
