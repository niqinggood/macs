package content

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/projectctx"
)

// VisionTool calls a vision-capable model to describe an image as text.
type VisionTool struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

func NewVisionTool(apiKey, baseURL, model string) *VisionTool {
	return &VisionTool{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (tool *VisionTool) Name() string { return "vision" }

func (tool *VisionTool) Description() string {
	return "Analyze and describe an image file using AI vision. " +
		"Returns a detailed text description of what is in the image. " +
		"Use this when you need to understand charts, screenshots, photos, or diagrams."
}

func (tool *VisionTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the image file (png, jpg, gif, webp)",
			},
			"question": map[string]interface{}{
				"type":        "string",
				"description": "Optional: specific question about the image (default: describe everything)",
			},
		},
		"required": []string{"path"},
	}
}

func (tool *VisionTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	path, _ := params["path"].(string)
	question, _ := params["question"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	path = ResolveVisionPath(ctx, path)

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}

	mime := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
	if question == "" {
		question = "请详细描述这张图片的内容，包括物体、颜色、文字、布局等所有可见信息。"
	}

	body := map[string]interface{}{
		"model": tool.model,
		"input": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "input_image", "image_url": dataURL},
					{"type": "input_text", "text": question},
				},
			},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode vision request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tool.baseURL+"/responses", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tool.apiKey)

	resp, err := tool.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision API: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read vision response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vision API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse vision response: %w", err)
	}
	var text strings.Builder
	for _, output := range result.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	description := strings.TrimSpace(text.String())
	if description == "" {
		return "", fmt.Errorf("vision model returned empty description")
	}
	return fmt.Sprintf("## 图片分析\n\n%s", description), nil
}

// ResolveVisionPath resolves a relative image path against the current run's
// workspace. Absolute paths retain their existing behavior for compatibility.
func ResolveVisionPath(ctx context.Context, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if workspace := projectctx.ResolvedWorkspace(ctx); workspace != "" {
		return filepath.Clean(filepath.Join(workspace, filepath.FromSlash(path)))
	}
	return filepath.Clean(filepath.Join(".", filepath.FromSlash(path)))
}
