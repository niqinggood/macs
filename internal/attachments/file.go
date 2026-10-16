package attachments

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/projectctx"
)

type File struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

func SaveWorkspaceUpload(workspaceRoot, originalName, contentType string, source io.Reader) (File, error) {
	fileName := filepath.Base(strings.TrimSpace(originalName))
	if fileName == "." || fileName == string(os.PathSeparator) || fileName == "" {
		fileName = fmt.Sprintf("upload_%d", time.Now().UnixNano())
	}
	root := strings.TrimSpace(workspaceRoot)
	storageDir, err := projectctx.ResolveLegacyWorkspacePath(root, "storage")
	if err != nil {
		return File{}, fmt.Errorf("resolve upload directory: %w", err)
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return File{}, fmt.Errorf("create upload directory: %w", err)
	}

	extension := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, extension)
	var destination string
	var output *os.File
	for index := 0; index < 1000; index++ {
		candidateName := fileName
		if index > 0 {
			candidateName = fmt.Sprintf("%s_%d%s", base, index, extension)
		}
		candidate, resolveErr := projectctx.ResolveLegacyWorkspacePath(root, filepath.Join("storage", candidateName))
		if resolveErr != nil {
			return File{}, fmt.Errorf("resolve upload target: %w", resolveErr)
		}
		output, err = os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			destination = candidate
			fileName = candidateName
			break
		}
		if !os.IsExist(err) {
			return File{}, fmt.Errorf("create upload: %w", err)
		}
	}
	if output == nil {
		return File{}, fmt.Errorf("cannot allocate a unique upload filename")
	}
	if err := projectctx.RejectReservedTenantWorkspacePath(root, destination); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return File{}, fmt.Errorf("validate upload target: %w", err)
	}
	written, copyErr := io.Copy(output, source)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		if copyErr != nil {
			return File{}, fmt.Errorf("write upload: %w", copyErr)
		}
		return File{}, fmt.Errorf("close upload: %w", closeErr)
	}
	detectedContentType := MIMEType(destination)
	if detectedContentType != "application/octet-stream" || strings.TrimSpace(contentType) == "" {
		contentType = detectedContentType
	}
	return File{
		Name:     fileName,
		Path:     filepath.ToSlash(filepath.Join("storage", fileName)),
		MIMEType: contentType,
		Size:     written,
		Kind:     Kind(contentType, destination),
	}, nil
}

func Kind(mimeType, path string) string {
	normalizedMIME := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if normalizedMIME == "image/svg+xml" {
		return "file"
	}
	if strings.HasPrefix(normalizedMIME, "image/") {
		return "image"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".avif":
		return "image"
	default:
		return "file"
	}
}

func MIMEType(path string) string {
	if byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExtension != "" {
		return byExtension
	}
	file, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()
	buffer := make([]byte, 512)
	count, _ := file.Read(buffer)
	if count == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(buffer[:count])
}

// Normalize treats attachment metadata as untrusted input and resolves every
// file against the selected workspace before returning stable relative paths.
func Normalize(input []File, workspaceRoot string, allowReservedTenantWorkspace bool) ([]File, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > 20 {
		return nil, fmt.Errorf("too many attachments (max 20)")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment workspace: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment workspace: %w", err)
	}
	seen := make(map[string]bool, len(input))
	result := make([]File, 0, len(input))
	for _, candidate := range input {
		requestedPath := strings.TrimSpace(candidate.Path)
		if requestedPath == "" {
			return nil, fmt.Errorf("attachment path is required")
		}
		fullPath := filepath.Clean(requestedPath)
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, fullPath)
		}
		fullPath, err = filepath.Abs(fullPath)
		if err != nil {
			return nil, fmt.Errorf("resolve attachment %q: %w", candidate.Name, err)
		}
		resolvedPath, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			return nil, fmt.Errorf("attachment %q is unavailable: %w", candidate.Name, err)
		}
		relativePath, err := filepath.Rel(resolvedRoot, resolvedPath)
		if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relativePath) {
			return nil, fmt.Errorf("attachment %q is outside the workspace", candidate.Name)
		}
		if !allowReservedTenantWorkspace {
			if err := projectctx.RejectReservedTenantWorkspacePath(root, resolvedPath); err != nil {
				if errors.Is(err, projectctx.ErrReservedTenantWorkspacePath) {
					return nil, fmt.Errorf("attachment %q is in a reserved tenant workspace", candidate.Name)
				}
				return nil, fmt.Errorf("validate attachment %q: %w", candidate.Name, err)
			}
		}
		info, err := os.Stat(resolvedPath)
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("attachment %q is not a readable file", candidate.Name)
		}
		stablePath := filepath.ToSlash(relativePath)
		key := strings.ToLower(stablePath)
		if seen[key] {
			continue
		}
		seen[key] = true
		mimeType := MIMEType(resolvedPath)
		result = append(result, File{
			Name:     filepath.Base(resolvedPath),
			Path:     stablePath,
			MIMEType: mimeType,
			Size:     info.Size(),
			Kind:     Kind(mimeType, stablePath),
		})
	}
	return result, nil
}

func AgentMessage(message string, files []File) string {
	message = strings.TrimSpace(message)
	if len(files) == 0 {
		return message
	}
	if message == "" {
		message = "请读取并分析我提供的附件。"
	}
	return message + manifest(files)
}

func VisibleMessage(message string, files []File) string {
	message = strings.TrimSpace(message)
	attachmentManifest := manifest(files)
	if attachmentManifest == "" || !strings.HasSuffix(message, attachmentManifest) {
		return message
	}
	return strings.TrimSpace(strings.TrimSuffix(message, attachmentManifest))
}

func manifest(files []File) string {
	if len(files) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(files)
	return "\n\n[OpenKitty 用户附件清单]\n" + string(encoded) +
		"\n这些附件是本轮用户明确提供的输入。请按 path 读取需要的文件，并结合用户消息完成任务。"
}
