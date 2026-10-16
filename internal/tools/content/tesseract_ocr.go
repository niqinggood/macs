package content

import (
	"archive/zip"
	"context"
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

// ====================== Tesseract OCR Integration ======================
// Uses system-installed tesseract binary (no CGO, no Go bindings).
// Consistent with pdfplumber/pdftotext external-tool pattern.

// OCRResult holds the OCR output for a single image.
type OCRResult struct {
	Text       string
	Confidence float64
	Language   string
	ImagePath  string
}

// OCREngine wraps the tesseract CLI.
type OCREngine struct {
	TesseractPath string
	Languages     string // e.g., "chi_sim+eng"
	TimeoutSec    int
}

// NewOCREngine creates an OCR engine with auto-download of Tesseract if needed.
func NewOCREngine(languages string) *OCREngine {
	if languages == "" {
		languages = "chi_sim+eng"
	}
	path := findOrInstallTesseract()
	if path != "" {
		ensureLanguages(path, languages)
	}
	return &OCREngine{
		TesseractPath: path,
		Languages:     languages,
		TimeoutSec:    60,
	}
}

// findOrInstallTesseract finds system tesseract or auto-installs locally.
func findOrInstallTesseract() string {
	// 1) System PATH
	if p, _ := exec.LookPath("tesseract"); p != "" {
		return p
	}
	// 2) Common install paths
	for _, p := range []string{
		`C:\Program Files\Tesseract-OCR\tesseract.exe`,
		`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 3) Local embedded copy
	localDir := filepath.Join(dataDir(), "tesseract")
	localBin := filepath.Join(localDir, "tesseract.exe")
	if _, err := os.Stat(localBin); err == nil {
		return localBin
	}
	// Linux/macOS: tesseract in local dir
	if p := filepath.Join(localDir, "bin", "tesseract"); fileExists(p) {
		return p
	}

	// 4) Auto-download portable tesseract
	log.Printf("[OCR] Tesseract not found, auto-downloading...")
	_ = os.MkdirAll(filepath.Join(localDir, "tessdata"), 0755)
	if err := downloadTesseract(localDir); err != nil {
		log.Printf("[OCR] Auto-download failed: %v. Install manually: %s", err, InstallTesseract())
		return ""
	}
	if fileExists(localBin) {
		return localBin
	}
	if p := filepath.Join(localDir, "bin", "tesseract"); fileExists(p) {
		return p
	}
	return ""
}

// downloadTesseract downloads the portable tesseract for the current platform.
func downloadTesseract(localDir string) error {
	var url string
	if isWindows() {
		// Use UB-Mannheim portable build (no installer needed)
		url = "https://github.com/UB-Mannheim/tesseract/releases/download/v5.5.0/tesseract-ocr-w64-setup-5.5.0.20241111.exe"
		// Try portable zip instead (extract, no install)
		zipURL := "https://github.com/UB-Mannheim/tesseract/releases/download/v5.5.0/tesseract-ocr-w64-portable-5.5.0.20241111.zip"
		zipPath := filepath.Join(localDir, "tesseract.zip")
		if err := downloadFile(zipURL, zipPath); err == nil {
			if err := unzipFile(zipPath, localDir); err == nil {
				os.Remove(zipPath)
				// Find tesseract.exe in extracted dir
				if tesseractExe := findTesseractInDir(localDir); tesseractExe != "" {
					log.Printf("[OCR] Tesseract installed to %s", tesseractExe)
					return nil
				}
			}
			os.Remove(zipPath)
		}
		_ = url // avoid unused variable
	} else {
		// Linux: use system package manager hint
		return fmt.Errorf("auto-download not supported on this platform. Run: apt install tesseract-ocr tesseract-ocr-chi-sim")
	}

	// Fallback: download tessdata only (if tesseract is available but no langs)
	return fmt.Errorf("download failed. Install manually: %s", InstallTesseract())
}

func findTesseractInDir(dir string) string {
	// Walk directory tree looking for tesseract.exe
	var result string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.ToLower(info.Name()) == "tesseract.exe" {
			result = path
			return filepath.SkipAll
		}
		return nil
	})
	return result
}

// ensureLanguages downloads missing language data files.
func ensureLanguages(tesseractPath, languages string) {
	tessdataDir := filepath.Join(filepath.Dir(tesseractPath), "tessdata")
	// Also check sibling tessdata dir
	if !fileExists(tessdataDir) {
		tessdataDir = filepath.Join(filepath.Dir(filepath.Dir(tesseractPath)), "tessdata")
	}
	os.MkdirAll(tessdataDir, 0755)

	for _, lang := range strings.Split(languages, "+") {
		lang = strings.TrimSpace(lang)
		traineddata := filepath.Join(tessdataDir, lang+".traineddata")
		if fileExists(traineddata) {
			continue
		}

		log.Printf("[OCR] Downloading language pack: %s", lang)
		url := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata/raw/main/%s.traineddata", lang)
		if err := downloadFile(url, traineddata); err != nil {
			log.Printf("[OCR] Failed to download %s: %v", lang, err)
			// Try tessdata_best as fallback
			url2 := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata_best/raw/main/%s.traineddata", lang)
			if err2 := downloadFile(url2, traineddata); err2 != nil {
				log.Printf("[OCR] Failed to download %s (best): %v", lang, err2)
			}
		}
	}
}

// ====================== File & Download Utilities ======================

func dataDir() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "data")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isWindows() bool {
	// Use path separator as simple heuristic
	return os.PathSeparator == '\\'
}

func downloadFile(url, dest string) error {
	resp, err := httpGetWithTimeout(url, 300*time.Second)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, io.LimitReader(resp.Body, 200<<20)) // max 200MB
	return err
}

func httpGetWithTimeout(url string, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "openkitty/1.0")
	return client.Do(req)
}

func unzipFile(src, dest string) error {
	if _, err := exec.LookPath("unzip"); err == nil {
		cmd := exec.Command("unzip", "-o", src, "-d", dest)
		cmd.Env = os.Environ()
		return cmd.Run()
	}
	return unzipNative(src, dest)
}

func unzipNative(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(dest, f.Name)
		// Prevent zip slip
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(dest)+string(os.PathSeparator)) {
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(path), 0755)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(path)
		if err != nil {
			rc.Close()
			continue
		}
		io.Copy(out, rc)
		out.Close()
		rc.Close()
	}
	return nil
}

// Available returns true if tesseract is installed and usable.
func (e *OCREngine) Available() bool {
	return e.TesseractPath != ""
}

// Version returns the tesseract version string.
func (e *OCREngine) Version() string {
	if !e.Available() {
		return "not installed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.TesseractPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "error: " + err.Error()
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return string(out)
}

// ListLanguages returns installed language packs.
func (e *OCREngine) ListLanguages() []string {
	if !e.Available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.TesseractPath, "--list-langs")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 1 {
		return lines[1:] // skip first line (header)
	}
	return lines
}

// OCRImage runs OCR on an image file and returns extracted text.
func (e *OCREngine) OCRImage(imagePath string) (*OCRResult, error) {
	if !e.Available() {
		return nil, fmt.Errorf("tesseract not installed. Install: winget install tesseract-ocr")
	}

	// Create temp output file (tesseract appends .txt)
	tmpDir := os.TempDir()
	base := filepath.Base(imagePath)
	baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	outBase := filepath.Join(tmpDir, fmt.Sprintf("ocr_%s_%d", baseNoExt, time.Now().UnixNano()))

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(e.TimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.TesseractPath,
		imagePath,
		outBase,
		"-l", e.Languages,
		"--psm", "3", // auto page segmentation
	)
	cmd.Env = os.Environ()

	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("tesseract start: %w", err)
	}
	errOutput, _ := io.ReadAll(stderr)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("tesseract: %w (stderr: %s)", err, string(errOutput))
	}

	// Read output text file
	txtPath := outBase + ".txt"
	data, err := os.ReadFile(txtPath)
	os.Remove(txtPath) // clean up

	if err != nil {
		return nil, fmt.Errorf("read ocr output: %w", err)
	}

	return &OCRResult{
		Text:      strings.TrimSpace(string(data)),
		Language:  e.Languages,
		ImagePath: imagePath,
	}, nil
}

// OCRImageBytes runs OCR on raw image bytes (writes to temp file first).
func (e *OCREngine) OCRImageBytes(data []byte, ext string) (*OCRResult, error) {
	tmp, err := os.CreateTemp("", "ocr_image_*"+ext)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	return e.OCRImage(tmp.Name())
}

// InstallTesseract returns install instructions for the current platform.
func InstallTesseract() string {
	return `Tesseract OCR 安装:
  Windows:  winget install tesseract-ocr
            然后下载中文语言包: https://github.com/tesseract-ocr/tessdata/raw/main/chi_sim.trained_data
            放到: C:\Program Files\Tesseract-OCR\tessdata\
  Linux:    apt install tesseract-ocr tesseract-ocr-chi-sim tesseract-ocr-eng
  macOS:    brew install tesseract tesseract-lang`
}
