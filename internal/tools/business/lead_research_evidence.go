package business

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"mac/internal/toolcontract"
)

type LeadResearchEvidenceOptions struct {
	DirectURLAllowed     func(string) bool
	CurrentOutputAllowed func(string) bool
	SensitivePath        func(string) bool
}

// LeadResearchEvidenceGuard filters application state and historical output
// from product-research evidence tools.
type LeadResearchEvidenceGuard struct {
	inner   toolcontract.Tool
	options LeadResearchEvidenceOptions
}

func NewLeadResearchEvidenceGuard(inner toolcontract.Tool, options LeadResearchEvidenceOptions) *LeadResearchEvidenceGuard {
	return &LeadResearchEvidenceGuard{inner: inner, options: options}
}

func (t *LeadResearchEvidenceGuard) Name() string { return t.inner.Name() }
func (t *LeadResearchEvidenceGuard) Description() string {
	return t.inner.Description() + " Lead-research evidence guard excludes runtime state, prior reports, databases, backups, executables, logs, and credentials."
}
func (t *LeadResearchEvidenceGuard) Parameters() map[string]interface{} {
	return t.inner.Parameters()
}
func (t *LeadResearchEvidenceGuard) IsReadOnly() bool { return true }
func (t *LeadResearchEvidenceGuard) MaxResultSize() int {
	if limited, ok := t.inner.(toolcontract.SizeLimitedTool); ok {
		return limited.MaxResultSize()
	}
	return 8000
}

func (t *LeadResearchEvidenceGuard) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	safeParams := make(map[string]interface{}, len(params)+1)
	for key, value := range params {
		safeParams[key] = value
	}
	pathValue, _ := safeParams["path"].(string)
	pattern, _ := safeParams["pattern"].(string)
	toolName := strings.ToLower(strings.TrimSpace(t.inner.Name()))
	if (toolName == "web_fetch" || toolName == "web_navigate") && t.options.DirectURLAllowed != nil {
		directURL, _ := safeParams["url"].(string)
		if strings.TrimSpace(directURL) != "" && !t.options.DirectURLAllowed(directURL) {
			return "", fmt.Errorf("lead-research product identity guard denied guessed URL; use a URL from the user, host product catalog, or a successful web_search result: %s", directURL)
		}
	}
	allowedCurrentOutput := toolName == "read_file" &&
		t.options.CurrentOutputAllowed != nil && t.options.CurrentOutputAllowed(pathValue)
	if (leadResearchRuntimeEvidencePath(pathValue) && !allowedCurrentOutput) || leadResearchRuntimeEvidencePath(pattern) {
		return "", fmt.Errorf("lead-research evidence guard denied runtime or historical artifact: %s", firstLeadEvidenceValue(pathValue, pattern))
	}
	if toolName == "read_file" && leadResearchNonProductFile(pathValue, t.options.SensitivePath) {
		return "", fmt.Errorf("lead-research evidence guard denied non-product file: %s", pathValue)
	}
	if toolName == "list_dir" {
		safeParams["show_hidden"] = false
	}
	result, err := t.inner.Execute(ctx, safeParams)
	if err != nil {
		return result, err
	}
	if toolName == "list_dir" || toolName == "glob" {
		result = filterLeadResearchDirectoryEvidence(result)
	}
	return result, nil
}

func leadResearchRuntimeEvidencePath(pathValue string) bool {
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(pathValue))))
	if normalized == "" || normalized == "." {
		return false
	}
	normalized = strings.TrimPrefix(normalized, "./")
	for _, marker := range []string{
		".openkitty", "/.openkitty/", "storage/pipe_", "/storage/pipe_", "/logs/", "pipeline_metrics.json",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func leadResearchNonProductFile(pathValue string, sensitivePath func(string) bool) bool {
	base := strings.ToLower(filepath.Base(filepath.Clean(strings.TrimSpace(pathValue))))
	for _, extension := range []string{".exe", ".db", ".db-shm", ".db-wal", ".bak", ".log", ".tmp"} {
		if strings.HasSuffix(base, extension) {
			return true
		}
	}
	return sensitivePath != nil && sensitivePath(pathValue)
}

func filterLeadResearchDirectoryEvidence(result string) string {
	lines := strings.Split(result, "\n")
	filtered := make([]string, 0, len(lines)+1)
	filtered = append(filtered, "[lead-research evidence view: runtime state, databases, backups, executables, logs, and credentials are filtered]")
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			filtered = append(filtered, line)
			continue
		}
		if leadResearchRuntimeEvidencePath(lower) ||
			strings.Contains(lower, "__pycache__") ||
			(strings.HasPrefix(lower, "found ") && strings.Contains(lower, " files matching")) ||
			strings.Contains(lower, "总文件数") || strings.Contains(lower, "总大小") ||
			strings.Contains(lower, "total files") || strings.Contains(lower, "total size") ||
			leadResearchDirectoryLineHasRuntimeFile(lower) {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func leadResearchDirectoryLineHasRuntimeFile(line string) bool {
	for _, marker := range []string{
		".exe", ".db-shm", ".db-wal", ".db", ".bak", ".log", ".tmp", ".pyc", ".pyo",
		"chatroom_key", "pipeline_metrics", "dbfix",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func firstLeadEvidenceValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "(empty)"
}
