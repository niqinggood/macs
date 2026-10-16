package agentos

import (
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

const (
	ArtifactStoragePrefix           = ".openkitty/storage/"
	DefaultArtifactDownloadEndpoint = "/api/v1/agent/files"
)

type Artifact struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Kind        string `json:"kind,omitempty"`
	DownloadURL string `json:"download_url"`
}

func ArtifactDownloadURL(path string) string {
	return ArtifactDownloadURLAt(path, "")
}

func NormalizeArtifactDownloadEndpoint(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "\\?#\r\n\x00") || strings.ContainsAny(value, " \t") {
		return "", false
	}
	clean := pathpkg.Clean(value)
	if clean == "." || clean == "/" || clean == ".." || strings.HasPrefix(clean, "/../") {
		return "", false
	}
	return clean, true
}

func ArtifactDownloadURLAt(artifactPath, endpoint string) string {
	endpoint, ok := NormalizeArtifactDownloadEndpoint(endpoint)
	if !ok || endpoint == "" {
		endpoint = DefaultArtifactDownloadEndpoint
	}
	return endpoint + "?path=" + url.QueryEscape(strings.TrimSpace(artifactPath))
}

func NormalizeArtifactPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, `\`) || strings.ContainsAny(value, "\r\n\x00") ||
		filepath.IsAbs(filepath.FromSlash(value)) {
		return "", fmt.Errorf("artifact path is invalid")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	lower := strings.ToLower(clean)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		!strings.HasPrefix(lower, ArtifactStoragePrefix) ||
		strings.HasPrefix(lower, ArtifactStoragePrefix+".system/") {
		return "", fmt.Errorf("artifact path is not downloadable")
	}
	return clean, nil
}

// ReferencedStorageFiles finds regular storage files named verbatim in a result.
func ReferencedStorageFiles(workspace, resultText string) []string {
	if !strings.Contains(strings.ToLower(resultText), ArtifactStoragePrefix) {
		return nil
	}
	storageDir := filepath.Join(workspace, filepath.FromSlash(strings.TrimSuffix(ArtifactStoragePrefix, "/")))
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		return nil
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if strings.Contains(resultText, name) {
			paths = append(paths, ArtifactStoragePrefix+name)
		}
	}
	return paths
}
