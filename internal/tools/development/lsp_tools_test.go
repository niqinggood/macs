package development

import (
	"testing"
)

func TestLSPDetectLanguage(t *testing.T) {
	tests := []struct {
		path string
		lang string
	}{
		{"/src/main.go", "go"},
		{"/app/test.py", "python"},
		{"/src/Component.tsx", "typescript"},
		{"/src/App.ts", "typescript"},
		{"/src/index.js", "javascript"},
		{"/src/main.rs", "rust"},
		{"/src/App.java", "java"},
		{"/src/main.cpp", "cpp"},
		{"/src/main.c", "c"},
		{"/src/App.cs", "csharp"},
		{"/src/main.rb", "ruby"},
		{"/src/App.kt", "kotlin"},
		{"/unknown/file.xyz", ""},
	}
	for _, tt := range tests {
		got := lspDetectLanguage(tt.path)
		if got != tt.lang {
			t.Errorf("lspDetectLanguage(%s)=%q, want %q", tt.path, got, tt.lang)
		}
	}
}

func TestGetServerCommand(t *testing.T) {
	tests := []struct {
		lang    string
		wantCmd string
	}{
		{"go", "gopls"},
		{"python", "pyright-langserver"},
		{"typescript", "typescript-language-server"},
		{"rust", "rust-analyzer"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		cmd, _ := GetServerCommand(tt.lang)
		if cmd != tt.wantCmd {
			t.Errorf("GetServerCommand(%s)=%q, want %q", tt.lang, cmd, tt.wantCmd)
		}
	}
}

func TestFormatLSPLocations(t *testing.T) {
	locs := []LSPLocation{
		{URI: "file:///src/main.go", Range: LSPRange{Start: LSPPosition{10, 5}, End: LSPPosition{10, 15}}},
		{URI: "file:///src/util.go", Range: LSPRange{Start: LSPPosition{20, 0}, End: LSPPosition{22, 1}}},
	}
	result := formatLSPLocations(locs, "/src/main.go")
	if result == "" {
		t.Error("formatLSPLocations returned empty string")
	}
	if !containsStr(result, "2 location") {
		t.Error("should show 2 locations")
	}
}

func TestFormatDocumentSymbols(t *testing.T) {
	symbols := []LSPDocumentSymbol{
		{Name: "main", Kind: 12, Range: LSPRange{Start: LSPPosition{0, 0}, End: LSPPosition{10, 1}}},
		{Name: "helper", Kind: 6, Range: LSPRange{Start: LSPPosition{11, 0}, End: LSPPosition{20, 1}},
			Children: []LSPDocumentSymbol{{Name: "innerFunc", Kind: 12, Range: LSPRange{Start: LSPPosition{15, 2}, End: LSPPosition{18, 1}}}}},
	}
	result := formatDocumentSymbols(symbols, 0)
	if result == "" {
		t.Error("formatDocumentSymbols returned empty string")
	}
	if !containsStr(result, "main") || !containsStr(result, "helper") || !containsStr(result, "innerFunc") {
		t.Error("should contain all symbol names")
	}
}

func TestSymbolKindName(t *testing.T) {
	if symbolKindName(12) == "[12]" {
		t.Error("kind 12 should have a named icon")
	}
	if symbolKindName(999) != "[999]" {
		t.Error("unknown kind should show as [999]")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
