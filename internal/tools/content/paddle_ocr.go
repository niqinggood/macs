package content

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ====================== PaddleOCR Integration ======================
// Uses Python paddleocr (already installed). Same pattern as pdfplumber/tesseract.

type PaddleOCREngine struct {
	PythonPath string
	ScriptPath string
	TimeoutSec int
}

func NewPaddleOCREngine() *PaddleOCREngine {
	python, _ := exec.LookPath("python")
	if python == "" {
		python, _ = exec.LookPath("python3")
	}
	script := findPaddleScript()
	return &PaddleOCREngine{
		PythonPath: python,
		ScriptPath: script,
		TimeoutSec: 120,
	}
}

func findPaddleScript() string {
	exe, _ := os.Executable()
	disk := filepath.Join(filepath.Dir(exe), "tools", "paddle_ocr.py")
	if _, err := os.Stat(disk); err == nil {
		return disk
	}
	disk = "tools/paddle_ocr.py"
	if _, err := os.Stat(disk); err == nil {
		return disk
	}
	return "paddle_ocr.py"
}

func (e *PaddleOCREngine) Available() bool {
	if e.PythonPath == "" {
		return false
	}
	_, err := os.Stat(e.ScriptPath)
	return err == nil
}

type PaddleOCRResult struct {
	Text  string         `json:"text"`
	Boxes []PaddleOCRBox `json:"boxes,omitempty"`
}

type PaddleOCRBox struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Box        [][]int `json:"box"`
}

func (e *PaddleOCREngine) OCR(ctx context.Context, imagePath string) (*PaddleOCRResult, error) {
	if !e.Available() {
		return nil, fmt.Errorf("PaddleOCR not available. Install: pip install paddleocr paddlepaddle")
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.PythonPath, e.ScriptPath, imagePath, "--json")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("paddleocr: %w", err)
	}

	var result PaddleOCRResult
	if err := json.Unmarshal(out, &result); err == nil {
		return &result, nil
	}
	// Plain text fallback
	result.Text = strings.TrimSpace(string(out))
	return &result, nil
}

func (e *PaddleOCREngine) OCRImageBytes(ctx context.Context, data []byte, ext string) (*PaddleOCRResult, error) {
	tmp, err := os.CreateTemp("", "paddle_ocr_*"+ext)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	return e.OCR(ctx, tmp.Name())
}
