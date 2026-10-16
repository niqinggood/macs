package channels

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ImageSender is implemented by transports that can deliver a local image as
// a native platform attachment.
type ImageSender interface {
	SendImage(recipient, imagePath string) error
}

type ImageArtifact struct {
	Alt         string
	Source      string
	LocalPath   string
	DisplayPath string
	aliases     []string
}

type PreparedContent struct {
	Text   string
	Images []ImageArtifact
}

var (
	markdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+["'][^)]*["'])?\)`)
	imageSourcePattern   = regexp.MustCompile(`(?i)(/api/files/download\?path=[^\s<>"')]+|/?(?:\.openkitty/)?storage/[^\s<>"');，；]+?\.(?:png|jpg|jpeg|gif|webp|bmp|svg))`)
)

func SendContent(workspace string, channel Transport, recipient, content string, logger *log.Logger) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	prepared := PrepareContent(workspace, content)
	sent := make([]bool, len(prepared.Images))
	if sender, ok := channel.(ImageSender); ok {
		for index, image := range prepared.Images {
			if strings.TrimSpace(image.LocalPath) == "" {
				continue
			}
			if err := sender.SendImage(recipient, image.LocalPath); err != nil {
				if logger != nil {
					logger.Printf("send image failed: channel=%s recipient=%s image=%s err=%v", channel.Name(), recipient, image.LocalPath, err)
				}
				continue
			}
			sent[index] = true
		}
	}
	text := appendImageFallbacks(strings.TrimSpace(prepared.Text), prepared.Images, sent)
	text = strings.TrimSpace(text)
	if text == "" {
		for _, delivered := range sent {
			if delivered {
				return nil
			}
		}
		text = strings.TrimSpace(content)
	}
	if text == "" {
		return nil
	}
	return channel.SendMessage(recipient, FormatReplyForTransport(text))
}

func FormatContentForTextFallback(workspace, content string) string {
	prepared := PrepareContent(workspace, content)
	text := appendImageFallbacks(strings.TrimSpace(prepared.Text), prepared.Images, make([]bool, len(prepared.Images)))
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(content)
	}
	return FormatReplyForTransport(text)
}

func PrepareContent(workspace, content string) PreparedContent {
	workspace = normalizeArtifactWorkspace(workspace)
	seen := make(map[string]bool)
	var images []ImageArtifact
	text := markdownImagePattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := markdownImagePattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		image, ok := resolveImageArtifact(workspace, parts[2], parts[1])
		if !ok {
			return match
		}
		key := imageArtifactKey(image)
		if !seen[key] {
			seen[key] = true
			images = append(images, image)
		}
		return ""
	})
	text = imageSourcePattern.ReplaceAllStringFunc(text, func(match string) string {
		image, ok := resolveImageArtifact(workspace, match, "")
		if ok {
			key := imageArtifactKey(image)
			if !seen[key] {
				seen[key] = true
				images = append(images, image)
			}
		}
		return match
	})
	return PreparedContent{Text: removeImageOnlyLines(text, images), Images: images}
}

func normalizeArtifactWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return filepath.Clean(workspace)
	}
	return filepath.Clean(absolute)
}

func resolveImageArtifact(workspace, source, alt string) (ImageArtifact, bool) {
	source = strings.TrimSpace(strings.Trim(source, `"'`))
	if source == "" || strings.HasPrefix(strings.ToLower(source), "data:image/") {
		return ImageArtifact{}, false
	}
	candidate := imageCandidatePath(source)
	if candidate == "" || strings.HasPrefix(strings.ToLower(candidate), "http://") || strings.HasPrefix(strings.ToLower(candidate), "https://") {
		return ImageArtifact{}, false
	}
	candidate = strings.ReplaceAll(strings.TrimSpace(strings.Trim(candidate, `"'`)), "\\", "/")
	for strings.HasPrefix(candidate, "/.openkitty/") || strings.HasPrefix(candidate, "/storage/") {
		candidate = strings.TrimPrefix(candidate, "/")
	}
	localPath := candidate
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(workspace, filepath.FromSlash(localPath))
	}
	localPath = filepath.Clean(localPath)
	if absolute, err := filepath.Abs(localPath); err == nil {
		localPath = filepath.Clean(absolute)
	}
	if !pathWithinWorkspace(workspace, localPath) || !isImagePath(localPath) {
		return ImageArtifact{}, false
	}
	if info, err := os.Stat(localPath); err != nil || info.IsDir() {
		return ImageArtifact{}, false
	}
	displayPath := filepath.ToSlash(localPath)
	if relative, err := filepath.Rel(workspace, localPath); err == nil && relative != "." {
		displayPath = filepath.ToSlash(relative)
	}
	downloadURL := "/api/files/download?path=" + url.QueryEscape("/"+displayPath)
	aliases := []string{source, candidate, displayPath, "/" + displayPath, downloadURL}
	if decoded, err := url.QueryUnescape(source); err == nil {
		aliases = append(aliases, decoded)
	}
	return ImageArtifact{
		Alt: strings.TrimSpace(alt), Source: source, LocalPath: localPath,
		DisplayPath: displayPath, aliases: compactArtifactStrings(aliases),
	}, true
}

func imageCandidatePath(source string) string {
	if strings.HasPrefix(source, "/api/files/download?") {
		if parsed, err := url.Parse(source); err == nil {
			if path := strings.TrimSpace(parsed.Query().Get("path")); path != "" {
				return path
			}
		}
	}
	if parsed, err := url.Parse(source); err == nil {
		if parsed.Path == "/api/files/download" {
			if path := strings.TrimSpace(parsed.Query().Get("path")); path != "" {
				return path
			}
		}
		if strings.HasPrefix(strings.ToLower(parsed.Scheme), "http") {
			return source
		}
	}
	return source
}

func pathWithinWorkspace(workspace, path string) bool {
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		absoluteWorkspace = filepath.Clean(workspace)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		absolutePath = filepath.Clean(path)
	}
	relative, err := filepath.Rel(absoluteWorkspace, absolutePath)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

func imageArtifactKey(image ImageArtifact) string {
	if image.LocalPath != "" {
		return strings.ToLower(filepath.Clean(image.LocalPath))
	}
	return strings.ToLower(strings.TrimSpace(image.Source))
}

func removeImageOnlyLines(text string, images []ImageArtifact) string {
	if len(images) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	output := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := line
		for _, image := range images {
			for _, alias := range image.aliases {
				if alias != "" {
					cleaned = strings.ReplaceAll(cleaned, alias, "")
				}
			}
		}
		trimmed := strings.Trim(strings.TrimSpace(cleaned), "：:；;，,。.`[]() \t")
		switch strings.ToLower(trimmed) {
		case "", "file", "文件", "图片", "image", "codex 生成图片", "codex 图片":
			continue
		default:
			output = append(output, line)
		}
	}
	return strings.TrimSpace(strings.Join(output, "\n"))
}

func appendImageFallbacks(text string, images []ImageArtifact, sent []bool) string {
	var missing []ImageArtifact
	for index, image := range images {
		if index < len(sent) && sent[index] {
			continue
		}
		missing = append(missing, image)
	}
	if len(missing) == 0 {
		return text
	}
	var output strings.Builder
	if strings.TrimSpace(text) != "" {
		output.WriteString(strings.TrimSpace(text))
		output.WriteString("\n\n")
	}
	output.WriteString("图片已生成，但当前渠道未原生发送图片：")
	for _, image := range missing {
		output.WriteString("\n- ")
		if image.Alt != "" {
			output.WriteString(image.Alt)
			output.WriteString(": ")
		}
		if image.DisplayPath != "" {
			output.WriteString(image.DisplayPath)
		} else {
			output.WriteString(image.Source)
		}
	}
	return output.String()
}

func compactArtifactStrings(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
