package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WatchTool captures images from a camera or screen for later vision analysis.
type WatchTool struct {
	workspace string
}

func NewWatchTool(workspace string) *WatchTool { return &WatchTool{workspace: workspace} }

func (tool *WatchTool) Name() string { return "watch" }

func (tool *WatchTool) Description() string {
	return "Watch the environment: capture from camera or screen and analyze what's happening. " +
		"Use when the user says 'watch', 'look at', 'monitor', 'record meeting', 'check the room'. " +
		"Actions: capture(take photo), monitor(take photos at interval for N seconds), screen(capture screen). " +
		"All captures are analyzed with vision for environment awareness."
}

func (tool *WatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"capture", "monitor", "screen", "list"},
				"description": "capture(one photo), monitor(photos at interval for N seconds), screen(screenshot), list(list recent captures)",
			},
			"duration": map[string]interface{}{
				"type": "integer", "description": "For monitor: total monitoring duration in seconds (default: 30).",
			},
			"interval": map[string]interface{}{
				"type": "integer", "description": "For monitor: interval between captures in seconds (default: 5).",
			},
			"camera": map[string]interface{}{
				"type": "integer", "description": "Camera device index (0=default, 1=external, etc.)",
			},
			"analyze": map[string]interface{}{
				"type": "boolean", "description": "Whether to analyze captures with vision model (default: true).",
			},
		},
	}
}

func (tool *WatchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action, _ := params["action"].(string)
	if action == "" {
		action = "capture"
	}
	taskID, _ := ctx.Value("task_id").(string)
	if strings.TrimSpace(taskID) == "" {
		taskID = fmt.Sprintf("watch_%s", time.Now().Format("20060102_150405"))
	}
	taskID = safeMediaTaskID(taskID)
	captureDir := filepath.Join(tool.workspace, ".openkitty", "storage", taskID, "captures")
	if err := os.MkdirAll(captureDir, 0755); err != nil {
		return "", fmt.Errorf("create capture directory: %w", err)
	}
	switch action {
	case "capture":
		return tool.capture(ctx, captureDir, params)
	case "monitor":
		return tool.monitor(ctx, captureDir, params)
	case "screen":
		return tool.captureScreen(ctx, captureDir)
	case "list":
		return tool.listCaptures(captureDir)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func safeMediaTaskID(taskID string) string {
	original := strings.TrimSpace(taskID)
	hash := sha256.Sum256([]byte(original))
	var name strings.Builder
	lastSeparator := false
	for _, character := range original {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		if letter || digit || character == '_' || character == '-' {
			name.WriteRune(character)
			lastSeparator = false
		} else if name.Len() > 0 && !lastSeparator {
			name.WriteByte('-')
			lastSeparator = true
		}
		if name.Len() >= 64 {
			break
		}
	}
	readable := strings.Trim(name.String(), "-_")
	if readable == "" {
		readable = "watch"
	}
	return readable + "-" + hex.EncodeToString(hash[:6])
}

func (tool *WatchTool) capture(ctx context.Context, directory string, params map[string]interface{}) (string, error) {
	cameraIndex := 0
	if value, ok := params["camera"].(float64); ok {
		cameraIndex = int(value)
	}
	analyze := true
	if value, ok := params["analyze"].(bool); ok {
		analyze = value
	}
	filename := fmt.Sprintf("capture_%s.jpg", time.Now().Format("150405"))
	outputPath := filepath.Join(directory, filename)
	if err := tool.captureFromCamera(ctx, cameraIndex, outputPath); err != nil {
		if screenErr := tool.captureScreenShot(ctx, outputPath); screenErr != nil {
			return "", fmt.Errorf("camera capture failed: %w; screen fallback: %v", err, screenErr)
		}
	}
	result := fmt.Sprintf("📸 Captured: %s%s", filename, captureFileSize(outputPath))
	if analyze {
		result += "\n[Vision analysis available — use vision tool on " + outputPath + "]"
	}
	return result, nil
}

func (tool *WatchTool) monitor(ctx context.Context, directory string, params map[string]interface{}) (string, error) {
	duration := boundedInteger(params["duration"], 30, 1, 300)
	interval := boundedInteger(params["interval"], 5, 1, 60)
	analyze := true
	if value, ok := params["analyze"].(bool); ok {
		analyze = value
	}
	deadline := time.NewTimer(time.Duration(duration) * time.Second)
	defer deadline.Stop()
	var captures []string
	for {
		filename := fmt.Sprintf("monitor_%s.jpg", time.Now().Format("150405.000"))
		outputPath := filepath.Join(directory, filename)
		err := tool.captureFromCamera(ctx, 0, outputPath)
		if err != nil {
			err = tool.captureScreenShot(ctx, outputPath)
		}
		if err == nil {
			captures = append(captures, filename)
		}
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return monitorSummary(directory, duration, interval, captures, analyze), nil
		case <-deadline.C:
			timer.Stop()
			return monitorSummary(directory, duration, interval, captures, analyze), nil
		case <-timer.C:
		}
	}
}

func boundedInteger(value interface{}, fallback, minimum, maximum int) int {
	if number, ok := value.(float64); ok {
		integer := int(number)
		if integer >= minimum && integer <= maximum {
			return integer
		}
	}
	return fallback
}

func monitorSummary(directory string, duration, interval int, captures []string, analyze bool) string {
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("📹 Monitored for %ds (every %ds): %d captures\n", duration, interval, len(captures)))
	for _, capture := range captures {
		summary.WriteString("  - " + capture + "\n")
	}
	if analyze && len(captures) > 0 {
		summary.WriteString(fmt.Sprintf("\n[Latest capture: %s — use vision tool to analyze]", filepath.Join(directory, captures[len(captures)-1])))
	}
	return summary.String()
}

func (tool *WatchTool) captureScreen(ctx context.Context, directory string) (string, error) {
	filename := fmt.Sprintf("screen_%s.png", time.Now().Format("150405"))
	outputPath := filepath.Join(directory, filename)
	if err := tool.captureScreenShot(ctx, outputPath); err != nil {
		return "", err
	}
	return fmt.Sprintf("🖥 Screen captured: %s%s\n[Use vision tool on %s to analyze]", filename, captureFileSize(outputPath), outputPath), nil
}

func captureFileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(" (%.1f KB)", float64(info.Size())/1024)
}

func (tool *WatchTool) listCaptures(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("📁 Captures in %s:\n", directory))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			result.WriteString(fmt.Sprintf("  - %s (%.1f KB)\n", entry.Name(), float64(info.Size())/1024))
		}
	}
	return result.String(), nil
}

func (tool *WatchTool) captureFromCamera(ctx context.Context, deviceIndex int, output string) error {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		commands := [][]string{
			{"-f", "dshow", "-i", fmt.Sprintf("video=%d", deviceIndex), "-vframes", "1", "-y", output},
			{"-f", "v4l2", "-i", fmt.Sprintf("/dev/video%d", deviceIndex), "-vframes", "1", "-y", output},
			{"-f", "avfoundation", "-i", fmt.Sprintf("%d", deviceIndex), "-vframes", "1", "-y", output},
		}
		var lastErr error
		for _, arguments := range commands {
			if err := exec.CommandContext(ctx, "ffmpeg", arguments...).Run(); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		return lastErr
	}
	if _, err := exec.LookPath("python"); err == nil {
		script := fmt.Sprintf(`import cv2, sys
cap = cv2.VideoCapture(%d)
if not cap.isOpened(): sys.exit(1)
ok, frame = cap.read()
if not ok: sys.exit(1)
cv2.imwrite(r'%s', frame)
cap.release()`, deviceIndex, strings.ReplaceAll(output, "\\", "\\\\"))
		return exec.CommandContext(ctx, "python", "-c", script).Run()
	}
	return fmt.Errorf("no camera capture tool available (install ffmpeg or opencv-python)")
}

func (tool *WatchTool) captureScreenShot(ctx context.Context, output string) error {
	if _, err := exec.LookPath("python"); err == nil {
		script := fmt.Sprintf(`from PIL import ImageGrab
img = ImageGrab.grab()
img.save(r'%s')`, strings.ReplaceAll(output, "\\", "\\\\"))
		if err := exec.CommandContext(ctx, "python", "-c", script).Run(); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return exec.CommandContext(ctx, "ffmpeg", "-f", "gdigrab", "-i", "desktop", "-vframes", "1", "-y", output).Run()
	}
	return fmt.Errorf("no screenshot tool available (install pillow or ffmpeg)")
}
