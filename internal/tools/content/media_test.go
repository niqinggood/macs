package content

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScreenshotToolPrefersGoNativeProvider(t *testing.T) {
	workspace := t.TempDir()
	nodeCalled := false
	tool := &ScreenshotTool{
		workspace: workspace,
		nativeCapture: func(_ context.Context, _, out string, _ int, _ bool) error {
			return os.WriteFile(out, []byte("png"), 0o600)
		},
		nodeCapture: func(context.Context, string, string, int, bool) error {
			nodeCalled = true
			return nil
		},
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url":    "https://example.com",
		"output": "shot.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if nodeCalled {
		t.Fatal("Node screenshot fallback ran after the Go provider succeeded")
	}
	if !strings.Contains(result, "provider=go-native") {
		t.Fatalf("screenshot result did not report Go provider: %s", result)
	}
}

func TestScreenshotToolFallsBackToNode(t *testing.T) {
	workspace := t.TempDir()
	tool := &ScreenshotTool{
		workspace: workspace,
		nativeCapture: func(context.Context, string, string, int, bool) error {
			return errors.New("native unavailable")
		},
		nodeCapture: func(_ context.Context, _, out string, _ int, _ bool) error {
			return os.WriteFile(out, []byte("png"), 0o600)
		},
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url":    "https://example.com",
		"output": "shot.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "provider=node") {
		t.Fatalf("screenshot result did not report Node fallback: %s", result)
	}
}

func TestImageProcessUsesGoForCommonFormats(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.png")
	output := filepath.Join(workspace, "output.png")
	imageFile, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			source.Set(x, y, color.RGBA{R: 30, G: 120, B: 200, A: 255})
		}
	}
	if err := png.Encode(imageFile, source); err != nil {
		t.Fatal(err)
	}
	_ = imageFile.Close()

	result, err := NewImageProcessTool(workspace).Execute(context.Background(), map[string]interface{}{
		"input":  input,
		"output": output,
		"width":  float64(40),
		"height": float64(40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "provider=go-native") {
		t.Fatalf("image result did not report Go provider: %s", result)
	}
	processed, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(processed)
	_ = processed.Close()
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 40 || config.Height != 20 {
		t.Fatalf("processed dimensions=%dx%d, want 40x20", config.Width, config.Height)
	}
}

func TestImageProcessFallsBackGoThenNodeThenPython(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.bin")
	if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	var order []string
	tool := &ImageProcessTool{
		workspace: workspace,
		nativeProcess: func(context.Context, string, string, int, int, int) (string, error) {
			order = append(order, "go-native")
			return "", errors.New("unsupported")
		},
		nodeProcess: func(context.Context, string, string, int, int, int) (string, error) {
			order = append(order, "node")
			return "", errors.New("missing sharp")
		},
		pythonProcess: func(_ context.Context, _, out string, _, _, _ int) (string, error) {
			order = append(order, "python")
			return "ok", os.WriteFile(out, []byte("output"), 0o600)
		},
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"input":  input,
		"output": "output.webp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"go-native", "node", "python"}) {
		t.Fatalf("provider order=%v", order)
	}
	if !strings.Contains(result, "provider=python") {
		t.Fatalf("image result did not report Python fallback: %s", result)
	}
}
