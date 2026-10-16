package content

import (
	"context"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"mac/internal/processutil"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ==================== screenshot tool ====================

type screenshotCaptureFunc func(context.Context, string, string, int, bool) error

type ScreenshotTool struct {
	workspace     string
	nativeCapture screenshotCaptureFunc
	nodeCapture   screenshotCaptureFunc
}

func NewScreenshotTool(ws string, nativeCaptures ...func(context.Context, string, string, int, bool) error) *ScreenshotTool {
	tool := &ScreenshotTool{workspace: ws}
	if len(nativeCaptures) > 0 {
		tool.nativeCapture = nativeCaptures[0]
	}
	return tool
}
func (t *ScreenshotTool) Name() string { return "screenshot" }
func (t *ScreenshotTool) Description() string {
	return "Capture a webpage screenshot using Go-native Chrome CDP with a Node.js fallback. " +
		"Saves as PNG. Useful for visual inspection of web pages."
}
func (t *ScreenshotTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":      map[string]interface{}{"type": "string", "description": "Webpage URL to capture"},
			"output":   map[string]interface{}{"type": "string", "description": "Output file path (e.g. storage/screenshot.png)"},
			"width":    map[string]interface{}{"type": "integer", "description": "Viewport width in pixels (default 1440)"},
			"fullPage": map[string]interface{}{"type": "boolean", "description": "Capture full page height (default true)"},
		},
		"required": []string{"url", "output"},
	}
}
func (t *ScreenshotTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	url, _ := params["url"].(string)
	out, _ := params["output"].(string)
	if url == "" || out == "" {
		return "", fmt.Errorf("url and output are required")
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(t.workspace, out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	width := 1440
	if w, ok := params["width"].(float64); ok {
		width = int(w)
	}
	fullPage := true
	if fp, ok := params["fullPage"].(bool); ok && !fp {
		fullPage = false
	}

	nativeCapture := t.nativeCapture
	if nativeCapture == nil {
		nativeCapture = func(context.Context, string, string, int, bool) error {
			return fmt.Errorf("native screenshot provider is not configured")
		}
	}
	if nativeErr := nativeCapture(ctx, url, out, width, fullPage); nativeErr == nil {
		return screenshotResult(out, "go-native")
	} else {
		_ = os.Remove(out)
		nodeCapture := t.nodeCapture
		if nodeCapture == nil {
			nodeCapture = captureScreenshotWithNode
		}
		if nodeErr := nodeCapture(ctx, url, out, width, fullPage); nodeErr == nil {
			return screenshotResult(out, "node")
		} else {
			return "", fmt.Errorf("screenshot failed (go-native: %v; node: %v)", nativeErr, nodeErr)
		}
	}
}

func captureScreenshotWithNode(ctx context.Context, rawURL, out string, width int, fullPage bool) error {
	scriptPath := processutil.ResolveBuiltinRuntimeScript("screenshot.js")
	if scriptPath == "" {
		return fmt.Errorf("bundled screenshot.js not found")
	}
	cmd := exec.CommandContext(ctx, "node", scriptPath, rawURL, out, strconv.Itoa(width), strconv.FormatBool(fullPage))
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func screenshotResult(out, provider string) (string, error) {
	info, err := os.Stat(out)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Screenshot: %s (%.1f KB, provider=%s)", out, float64(info.Size())/1024, provider), nil
}

// ==================== image_process tool ====================

type imageProcessFunc func(context.Context, string, string, int, int, int) (string, error)

type ImageProcessTool struct {
	workspace     string
	nativeProcess imageProcessFunc
	nodeProcess   imageProcessFunc
	pythonProcess imageProcessFunc
}

func NewImageProcessTool(ws string) *ImageProcessTool { return &ImageProcessTool{workspace: ws} }
func (t *ImageProcessTool) Name() string              { return "image_process" }
func (t *ImageProcessTool) Description() string {
	return "Resize, convert, or compress images using Go first, Node.js second, and Python last. " +
		"Supports PNG, JPG, WebP, AVIF, and GIF depending on the selected provider."
}
func (t *ImageProcessTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input":   map[string]interface{}{"type": "string", "description": "Input image path"},
			"output":  map[string]interface{}{"type": "string", "description": "Output image path (format determined by extension)"},
			"width":   map[string]interface{}{"type": "integer", "description": "Target width in pixels (optional)"},
			"height":  map[string]interface{}{"type": "integer", "description": "Target height in pixels (optional)"},
			"quality": map[string]interface{}{"type": "integer", "description": "Quality 1-100 (default 85)"},
		},
		"required": []string{"input", "output"},
	}
}
func (t *ImageProcessTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	inp, _ := params["input"].(string)
	out, _ := params["output"].(string)
	if inp == "" || out == "" {
		return "", fmt.Errorf("input and output are required")
	}
	if !filepath.IsAbs(inp) {
		inp = filepath.Join(t.workspace, inp)
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(t.workspace, out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	width, height, quality := 0, 0, 85
	if w, ok := params["width"].(float64); ok {
		width = int(w)
	}
	if h, ok := params["height"].(float64); ok {
		height = int(h)
	}
	if q, ok := params["quality"].(float64); ok {
		quality = int(q)
	}

	providers := []struct {
		name string
		run  imageProcessFunc
	}{
		{name: "go-native", run: firstImageProcessFunc(t.nativeProcess, processImageWithGo)},
		{name: "node", run: firstImageProcessFunc(t.nodeProcess, processImageWithNode)},
		{name: "python", run: firstImageProcessFunc(t.pythonProcess, processImageWithPython)},
	}
	failures := make([]string, 0, len(providers))
	for _, provider := range providers {
		result, err := provider.run(ctx, inp, out, width, height, quality)
		if err == nil {
			return strings.TrimSpace(result) + " (provider=" + provider.name + ")", nil
		}
		_ = os.Remove(out)
		failures = append(failures, provider.name+": "+err.Error())
	}
	return "", fmt.Errorf("image process failed (%s)", strings.Join(failures, "; "))
}

func firstImageProcessFunc(current, fallback imageProcessFunc) imageProcessFunc {
	if current != nil {
		return current
	}
	return fallback
}

func processImageWithGo(_ context.Context, input, output string, width, height, quality int) (string, error) {
	in, err := os.Open(input)
	if err != nil {
		return "", err
	}
	decoded, _, err := image.Decode(in)
	_ = in.Close()
	if err != nil {
		return "", err
	}

	targetWidth, targetHeight := fittedImageDimensions(decoded.Bounds().Dx(), decoded.Bounds().Dy(), width, height)
	var rendered image.Image = decoded
	if targetWidth != decoded.Bounds().Dx() || targetHeight != decoded.Bounds().Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), decoded, decoded.Bounds(), xdraw.Over, nil)
		rendered = dst
	}

	ext := strings.ToLower(filepath.Ext(output))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" {
		return "", fmt.Errorf("Go encoder does not support %s output", ext)
	}
	out, err := os.Create(output)
	if err != nil {
		return "", err
	}
	defer out.Close()
	switch ext {
	case ".jpg", ".jpeg":
		if quality < 1 || quality > 100 {
			quality = 85
		}
		err = jpeg.Encode(out, rendered, &jpeg.Options{Quality: quality})
	case ".gif":
		err = gif.Encode(out, rendered, nil)
	default:
		err = png.Encode(out, rendered)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Image processed: %s (%dx%d)", output, targetWidth, targetHeight), nil
}

func fittedImageDimensions(sourceWidth, sourceHeight, width, height int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return 1, 1
	}
	if width <= 0 && height <= 0 {
		return sourceWidth, sourceHeight
	}
	scale := math.Inf(1)
	if width > 0 {
		scale = math.Min(scale, float64(width)/float64(sourceWidth))
	}
	if height > 0 {
		scale = math.Min(scale, float64(height)/float64(sourceHeight))
	}
	if scale > 1 {
		scale = 1
	}
	targetWidth := int(math.Round(float64(sourceWidth) * scale))
	targetHeight := int(math.Round(float64(sourceHeight) * scale))
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	return targetWidth, targetHeight
}

func processImageWithNode(ctx context.Context, input, output string, width, height, quality int) (string, error) {
	scriptPath := processutil.ResolveBuiltinRuntimeScript("image_process.js")
	if scriptPath == "" {
		return "", fmt.Errorf("bundled image_process.js not found")
	}
	args := []string{scriptPath, input, output, optionalPositiveInt(width), optionalPositiveInt(height), strconv.Itoa(quality)}
	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Env = os.Environ()
	commandOutput, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(commandOutput)))
	}
	return strings.TrimSpace(string(commandOutput)), nil
}

func processImageWithPython(ctx context.Context, input, output string, width, height, quality int) (string, error) {
	const script = `
import sys
from PIL import Image

source, target = sys.argv[1], sys.argv[2]
width, height, quality = int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
image = Image.open(source)
limit = (width or image.width, height or image.height)
image.thumbnail(limit, Image.Resampling.LANCZOS)
if target.lower().endswith((".jpg", ".jpeg")) and image.mode not in ("RGB", "L"):
    image = image.convert("RGB")
save_args = {}
if target.lower().endswith((".jpg", ".jpeg", ".webp", ".avif")):
    save_args["quality"] = quality
image.save(target, **save_args)
print(f"Image processed: {target} ({image.width}x{image.height})")
`
	cmd, err := processutil.PythonCommandContext(ctx, "-c", script, input, output, strconv.Itoa(width), strconv.Itoa(height), strconv.Itoa(quality))
	if err != nil {
		return "", err
	}
	commandOutput, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(commandOutput)))
	}
	return strings.TrimSpace(string(commandOutput)), nil
}

func optionalPositiveInt(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}
