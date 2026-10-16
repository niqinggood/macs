package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ChannelMediaHandler provides unified file/image/audio processing for all channels.
// Channels (Feishu, DingTalk, QQ) can delegate media download and processing to this handler.
type ChannelMediaHandler struct {
	workspace string
	logger    *log.Logger
	resolver  BinaryResolver
}

func NewChannelMediaHandler(workspace string, logger *log.Logger, resolver BinaryResolver) *ChannelMediaHandler {
	if logger == nil {
		logger = log.Default()
	}
	return &ChannelMediaHandler{
		workspace: workspace,
		logger:    logger,
		resolver:  resolver,
	}
}

func (h *ChannelMediaHandler) Workspace() string { return h.workspace }

// MediaType classifies incoming media by MIME type and file extension.
type MediaType string

const (
	MediaImage MediaType = "image"
	MediaFile  MediaType = "file"
	MediaAudio MediaType = "audio"
	MediaOther MediaType = "other"
)

// DownloadedMedia represents a downloaded media file ready for processing.
type DownloadedMedia struct {
	Type     MediaType
	FilePath string // local path to downloaded file
	FileName string
	MimeType string
	FileSize int64
}

// ProcessFromReader saves media data from a reader and processes it into text.
// This is the primary method for SDK-based channels that provide data directly.
func (h *ChannelMediaHandler) ProcessFromReader(reader io.Reader, fileName, mimeType string) (string, error) {
	mediaDir := filepath.Join(h.workspace, ".openkitty", "storage", "channel_media")
	os.MkdirAll(mediaDir, 0755)

	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s_%s", time.Now().Format("150405"), fileName))

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("create temp file failed: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, reader); err != nil {
		os.Remove(localPath)
		return "", fmt.Errorf("save media failed: %w", err)
	}
	f.Close() // Close before processing

	media := h.classifyMedia(fileName, mimeType)
	media.FilePath = localPath
	if fi, err := os.Stat(localPath); err == nil {
		media.FileSize = fi.Size()
	}

	var result string
	var procErr error

	switch media.Type {
	case MediaImage:
		result, procErr = h.processImage(media)
	case MediaAudio:
		result, procErr = h.processAudio(media)
	case MediaFile:
		result, procErr = h.processFile(media)
	default:
		result = fmt.Sprintf("[文件] %s (%s, %d bytes) — 无法自动解析此类型文件", fileName, mimeType, media.FileSize)
	}

	// Keep the file for permanent reference
	os.Remove(localPath)
	return result, procErr
}

// DownloadAndProcess downloads media from a URL and processes it into text.
// Returns the extracted text content ready to pass to the agent.
func (h *ChannelMediaHandler) DownloadAndProcess(mediaURL, fileName, mimeType string) (string, error) {
	h.logger.Printf("[MediaHandler] Downloading: %s (%s)", fileName, mimeType)

	mediaDir := filepath.Join(h.workspace, ".openkitty", "storage", "channel_media")
	os.MkdirAll(mediaDir, 0755)

	media := h.classifyMedia(fileName, mimeType)

	localPath := filepath.Join(mediaDir, fmt.Sprintf("%s_%s", time.Now().Format("150405"), fileName))

	if err := h.download(mediaURL, localPath); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(localPath)

	media.FilePath = localPath
	fi, _ := os.Stat(localPath)
	if fi != nil {
		media.FileSize = fi.Size()
	}

	switch media.Type {
	case MediaImage:
		return h.processImage(media)
	case MediaAudio:
		return h.processAudio(media)
	case MediaFile:
		return h.processFile(media)
	default:
		return fmt.Sprintf("[文件] %s (%s, %d bytes) — 无法自动解析此类型文件", fileName, mimeType, media.FileSize), nil
	}
}

func (h *ChannelMediaHandler) classifyMedia(fileName, mimeType string) DownloadedMedia {
	ext := strings.ToLower(filepath.Ext(fileName))
	mimeLower := strings.ToLower(mimeType)

	// Image types
	if strings.HasPrefix(mimeLower, "image/") ||
		ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".bmp" {
		return DownloadedMedia{Type: MediaImage, FileName: fileName, MimeType: mimeType}
	}

	// Audio types
	if strings.HasPrefix(mimeLower, "audio/") ||
		ext == ".mp3" || ext == ".wav" || ext == ".ogg" || ext == ".m4a" || ext == ".aac" || ext == ".flac" || ext == ".amr" || ext == ".silk" {
		return DownloadedMedia{Type: MediaAudio, FileName: fileName, MimeType: mimeType}
	}

	// Everything else is a file
	return DownloadedMedia{Type: MediaFile, FileName: fileName, MimeType: mimeType}
}

func (h *ChannelMediaHandler) download(url, localPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func (h *ChannelMediaHandler) processImage(media DownloadedMedia) (string, error) {
	h.logger.Printf("[MediaHandler] Processing image: %s (%d bytes)", media.FileName, media.FileSize)

	// Try to extract text via OCR or describe the image
	// For now, provide a description that the Vision tool can handle
	absPath, _ := filepath.Abs(media.FilePath)

	// Check if we can do basic OCR with tesseract
	if tesseractPath, err := exec.LookPath("tesseract"); err == nil {
		cmd := exec.Command(tesseractPath, absPath, "stdout", "-l", "chi_sim+eng", "--psm", "3")
		out, err := cmd.CombinedOutput()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			text := strings.TrimSpace(string(out))
			return fmt.Sprintf("[图片OCR结果]\n%s", text), nil
		}
	}

	// Fallback: reference the image for Vision tool processing
	return fmt.Sprintf("[图片] %s (%d bytes)\n文件路径: %s\n请使用 vision 工具分析此图片内容。",
		media.FileName, media.FileSize, absPath), nil
}

func (h *ChannelMediaHandler) processAudio(media DownloadedMedia) (string, error) {
	h.logger.Printf("[MediaHandler] Processing audio: %s (%d bytes)", media.FileName, media.FileSize)
	absPath, _ := filepath.Abs(media.FilePath)

	// Delegate to unified ChannelVoiceHandler (multi-engine pipeline)
	voiceHandler := NewChannelVoiceHandler(h.workspace, h.logger, h.resolver)
	result := voiceHandler.ProcessFile(absPath)

	// If no engine available, use the result which includes install instructions
	if strings.Contains(result, "安装语音转写引擎") {
		return result, nil
	}
	return result, nil
}

func (h *ChannelMediaHandler) processFile(media DownloadedMedia) (string, error) {
	h.logger.Printf("[MediaHandler] Processing file: %s (%d bytes)", media.FileName, media.FileSize)

	absPath, _ := filepath.Abs(media.FilePath)
	ext := strings.ToLower(filepath.Ext(media.FileName))

	switch ext {
	case ".txt", ".md", ".csv", ".log", ".json", ".xml", ".yaml", ".yml", ".toml":
		data, err := os.ReadFile(absPath)
		if err != nil {
			return "", err
		}
		text := string(data)
		if len(text) > 4000 {
			text = text[:4000] + "\n...(截断)"
		}
		return fmt.Sprintf("[文件内容: %s]\n%s", media.FileName, text), nil

	case ".pdf":
		// Try pdftotext
		if cmdPath, err := exec.LookPath("pdftotext"); err == nil {
			cmd := exec.Command(cmdPath, "-layout", absPath, "-")
			out, err := cmd.CombinedOutput()
			if err == nil {
				text := string(out)
				if len(text) > 4000 {
					text = text[:4000] + "\n...(截断)"
				}
				return fmt.Sprintf("[PDF内容: %s]\n%s", media.FileName, text), nil
			}
		}

	case ".docx":
		// Try python-docx
		if pythonPath, err := exec.LookPath("python"); err == nil {
			script := fmt.Sprintf(`
from docx import Document
doc = Document(r'%s')
for p in doc.paragraphs:
    print(p.text)
`, absPath)
			cmd := exec.Command(pythonPath, "-c", script)
			out, err := cmd.CombinedOutput()
			if err == nil {
				text := string(out)
				if len(text) > 4000 {
					text = text[:4000] + "\n...(截断)"
				}
				return fmt.Sprintf("[DOCX内容: %s]\n%s", media.FileName, text), nil
			}
		}
	}

	return fmt.Sprintf("[文件] %s (%s, %d bytes)\n文件路径: %s\n请使用 extract 工具读取此文件。",
		media.FileName, media.MimeType, media.FileSize, absPath), nil
}

// BuildMediaMessage constructs a user-facing message describing the media that was received.
// Used by channels when passing media content to the agent.
func BuildMediaMessage(channelName, senderName string, processedContent string) string {
	return fmt.Sprintf("[%s 渠道消息，来自: %s]\n%s", channelName, senderName, processedContent)
}

// Feishu-specific: download file/image from Lark API
// Lark API requires authentication, so we use the SDK client's http client
func (h *ChannelMediaHandler) DownloadFeishuMedia(client interface{}, messageID, fileKey, mediaType string) (string, error) {
	// For Feishu, the media download is done through the SDK
	// This is a placeholder that should be called with the actual SDK client
	h.logger.Printf("[MediaHandler] Feishu %s download: messageID=%s, fileKey=%s", mediaType, messageID, fileKey)
	return "", fmt.Errorf("use SDK client to download media")
}

// SaveMediaPermanently copies a media file to permanent storage for later reference.
func (h *ChannelMediaHandler) SaveMediaPermanently(mediaPath, channelName, senderID string) (string, error) {
	permDir := filepath.Join(h.workspace, ".openkitty", "storage", "channel_media", channelName, senderID)
	os.MkdirAll(permDir, 0755)

	baseName := filepath.Base(mediaPath)
	destPath := filepath.Join(permDir, fmt.Sprintf("%s_%s", time.Now().Format("20060102_150405"), baseName))

	src, err := os.Open(mediaPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	io.Copy(dst, src)
	return destPath, nil
}

// extractJSONField extracts a string field from JSON bytes
func extractJSONField(data []byte, key string) string {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
