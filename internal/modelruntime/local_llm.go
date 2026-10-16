package modelruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"mac/internal/fsutil"
	"mac/internal/processutil"
)

// ====================== Local LLM Engine (self-contained) ======================
// Auto-downloads llama-server + GGUF model, manages subprocess lifecycle.
// Zero install — just put a .gguf file or let it auto-download.

const (
	llamaServerVersion = "b4882"
	llamaServerBaseURL = "https://github.com/ggml-org/llama.cpp/releases/download"
	defaultEmbedModel  = "nomic-embed-text-v1.5-Q4_K_M"
	embedModelURL      = "https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5-Q4_K_M.gguf"
	defaultChatModel   = "Qwen2.5-1.5B-Instruct-Q4_K_M"
	chatModelURL       = "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf"
	llamaHealthTimeout = 30 * time.Second
)

type LocalLLM struct {
	mu        sync.Mutex
	serverCmd *exec.Cmd
	port      int
	baseURL   string
	modelPath string
	dataDir   string
	serverBin string
	running   bool
	hooks     LocalLLMHooks
}

// LocalLLMHooks connects the model runtime to an optional host UI without
// coupling modelruntime to the gateway package.
type LocalLLMHooks struct {
	Confirm  func(message, actionID, actionLabel string) <-chan struct{}
	Notify   func(message, severity string)
	Progress func(actionID string, progress int, message string)
}

// NewLocalLLM creates a self-contained local LLM engine.
func NewLocalLLM(modelPath string, hooks ...LocalLLMHooks) *LocalLLM {
	dir := filepath.Join(fsutil.ExecutableDataDir(), "llama")
	os.MkdirAll(dir, 0755)
	var hostHooks LocalLLMHooks
	if len(hooks) > 0 {
		hostHooks = hooks[0]
	}
	return &LocalLLM{
		modelPath: modelPath,
		dataDir:   dir,
		port:      18080, // different from Ollama's 11434
		hooks:     hostHooks,
	}
}

func (l *LocalLLM) notify(message, severity string) {
	if l != nil && l.hooks.Notify != nil {
		l.hooks.Notify(message, severity)
	}
}

func (l *LocalLLM) progress(actionID string, progress int, message string) {
	if l != nil && l.hooks.Progress != nil {
		l.hooks.Progress(actionID, progress, message)
	}
}

// Available checks if the necessary components are present or downloadable.
func (l *LocalLLM) Available() bool {
	// Either model is already present, or we can download it
	if l.modelPath != "" && fsutil.Exists(l.modelPath) {
		return true
	}
	if l.modelPath == "" {
		// Check for auto-download capability
		return true
	}
	return false
}

// Start downloads components if needed and starts the llama-server.
// Requires user confirmation via web UI before first download.
func (l *LocalLLM) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.running {
		return nil
	}

	needsServer := !fsutil.Exists(filepath.Join(l.dataDir, "bin", "llama-server"+serverBinExt()))
	needsModel := l.modelPath == "" || !fsutil.Exists(l.modelPath)

	if needsServer || needsModel {
		// Request user confirmation
		var parts []string
		if needsServer {
			parts = append(parts, fmt.Sprintf("推理引擎 (~40MB)"))
		}
		if needsModel {
			parts = append(parts, fmt.Sprintf("embedding 模型 (~270MB)"))
		}
		msg := fmt.Sprintf("检测到本地 AI 推理环境未就绪，需要下载: %s。下载后即可离线使用，确认开始？", strings.Join(parts, "、"))

		if l.hooks.Confirm == nil {
			return fmt.Errorf("local LLM download requires host confirmation")
		}
		ch := l.hooks.Confirm(msg, "local_llm_install", "确认下载")
		log.Printf("[LocalLLM] Waiting for user confirmation...")
		select {
		case <-ch:
			log.Printf("[LocalLLM] User confirmed, starting download...")
		case <-ctx.Done():
			return fmt.Errorf("user did not confirm: %w", ctx.Err())
		case <-time.After(5 * time.Minute):
			return fmt.Errorf("user confirmation timeout")
		}
	}

	// 1) Ensure server binary (with progress)
	if err := l.ensureServerBin(); err != nil {
		return fmt.Errorf("llama-server binary: %w", err)
	}

	// 2) Ensure model file (with progress)
	if err := l.ensureModel(); err != nil {
		return fmt.Errorf("model download: %w", err)
	}

	// 3) Find available port
	l.port = findFreePort(l.port)
	l.baseURL = fmt.Sprintf("http://127.0.0.1:%d", l.port)

	// 4) Start server
	log.Printf("[LocalLLM] Starting llama-server on %s with %s", l.baseURL, filepath.Base(l.modelPath))
	l.serverCmd = exec.CommandContext(ctx, l.serverBin,
		"-m", l.modelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", l.port),
		"-ngl", "99", // offload all layers to GPU if available
		"--embedding", // enable embedding endpoint
		"--ctx-size", "8192",
		"--parallel", "4",
		"-fa", // flash attention
	)
	l.serverCmd.Env = os.Environ()
	l.serverCmd.Stdout = nil // quiet
	l.serverCmd.Stderr = nil
	processutil.ConfigureLocalProcess(l.serverCmd)

	if err := l.serverCmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}

	// 5) Wait for health
	if err := l.waitForHealth(ctx); err != nil {
		l.Stop()
		return fmt.Errorf("llama-server health check: %w", err)
	}

	l.running = true
	log.Printf("[LocalLLM] Ready: %s/v1/embeddings", l.baseURL)
	return nil
}

// Stop gracefully shuts down the llama-server.
func (l *LocalLLM) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.serverCmd != nil && l.serverCmd.Process != nil {
		log.Printf("[LocalLLM] Stopping llama-server")
		if runtime.GOOS == "windows" {
			l.serverCmd.Process.Kill()
		} else {
			l.serverCmd.Process.Signal(syscall.SIGTERM)
		}
		l.serverCmd.Wait()
	}
	l.running = false
}

// BaseURL returns the server's HTTP base URL.
func (l *LocalLLM) BaseURL() string { return l.baseURL }

// ====================== Internal: Download & Setup ======================

func (l *LocalLLM) ensureServerBin() error {
	srvDir := filepath.Join(l.dataDir, "bin")
	os.MkdirAll(srvDir, 0755)

	binName := "llama-server"
	if runtime.GOOS == "windows" {
		binName = "llama-server.exe"
	}
	l.serverBin = filepath.Join(srvDir, binName)

	if fsutil.Exists(l.serverBin) {
		return nil
	}

	// Download from llama.cpp releases
	asset := llamaServerAsset()
	if asset == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	url := fmt.Sprintf("%s/%s/%s", llamaServerBaseURL, llamaServerVersion, asset)
	log.Printf("[LocalLLM] Downloading llama-server from %s", url)
	l.notify("正在下载本地 AI 推理引擎...", "info")
	l.progress("local_llm_install", 10, "下载推理引擎中...")

	zipPath := filepath.Join(l.dataDir, asset)
	if err := l.downloadFileWithProgress(url, zipPath, "local_llm_install", 10, 50); err != nil {
		return fmt.Errorf("download llama-server: %w", err)
	}
	defer os.Remove(zipPath)

	l.progress("local_llm_install", 55, "解压中...")
	if err := fsutil.Unzip(zipPath, srvDir); err != nil {
		return fmt.Errorf("extract llama-server: %w", err)
	}

	// Find the binary in extracted files
	if !fsutil.Exists(l.serverBin) {
		// Search in subdirectories
		l.serverBin = fsutil.FindFileNamed(srvDir, "llama-server"+serverBinExt())
		if l.serverBin == "" {
			return fmt.Errorf("llama-server not found in extracted archive")
		}
	}

	// Make executable on Unix
	if runtime.GOOS != "windows" {
		os.Chmod(l.serverBin, 0755)
	}
	log.Printf("[LocalLLM] llama-server installed to %s", l.serverBin)
	return nil
}

func llamaServerAsset() string {
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return "llama-b4882-bin-win-cuda-cu13-x64.zip"
		case "arm64":
			return "llama-b4882-bin-win-arm64.zip"
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "llama-b4882-bin-ubuntu-x64.zip"
		case "arm64":
			return "llama-b4882-bin-ubuntu-arm64.zip"
		}
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return "llama-b4882-bin-macos-arm64.zip"
		case "amd64":
			return "llama-b4882-bin-macos-x64.zip"
		}
	}
	return ""
}

func (l *LocalLLM) ensureModel() error {
	if l.modelPath == "" {
		// Default: embedding model
		l.modelPath = filepath.Join(l.dataDir, "models", defaultEmbedModel+".gguf")
	}
	os.MkdirAll(filepath.Dir(l.modelPath), 0755)

	if fsutil.Exists(l.modelPath) {
		return nil
	}

	// Determine download URL based on model name
	url := modelDownloadURL(filepath.Base(l.modelPath))
	if url == "" {
		return fmt.Errorf("unknown model: %s. Place a .gguf file at %s", filepath.Base(l.modelPath), l.modelPath)
	}

	log.Printf("[LocalLLM] Downloading model (~%s)...", modelSize(url))
	l.progress("local_llm_install", 55, "下载模型文件中...")
	if err := l.downloadFileWithProgress(url, l.modelPath, "local_llm_install", 55, 95); err != nil {
		os.Remove(l.modelPath)
		return fmt.Errorf("download model: %w", err)
	}
	log.Printf("[LocalLLM] Model downloaded to %s", l.modelPath)
	l.progress("local_llm_install", 100, "就绪")
	l.notify("本地 AI 推理环境安装完成！", "success")
	return nil
}

func modelDownloadURL(filename string) string {
	_ = strings.TrimSuffix(filename, ".gguf")
	switch {
	case strings.Contains(filename, "nomic-embed"):
		return embedModelURL
	case strings.Contains(filename, "qwen2.5") || strings.Contains(filename, "Qwen2.5"):
		return chatModelURL
	default:
		return "" // user-provided model, no auto-download
	}
}

func modelSize(url string) string {
	switch {
	case strings.Contains(url, "nomic-embed"):
		return "~270MB"
	case strings.Contains(url, "qwen"):
		return "~1GB"
	}
	return "unknown"
}

// ====================== Health Check ======================

func (l *LocalLLM) waitForHealth(ctx context.Context) error {
	deadline := time.Now().Add(llamaHealthTimeout)
	healthURL := l.baseURL + "/health"

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("llama-server not ready after %v", llamaHealthTimeout)
}

// ====================== Local LLM Provider (OpenAI-compatible) ======================

// LocalLLMProvider implements the same interface as Ollama for chat/embedding.
type LocalLLMProvider struct {
	engine *LocalLLM
	client *http.Client
}

func NewLocalLLMProvider(modelPath string, hooks ...LocalLLMHooks) *LocalLLMProvider {
	return &LocalLLMProvider{
		engine: NewLocalLLM(modelPath, hooks...),
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *LocalLLMProvider) Start(ctx context.Context) error {
	return p.engine.Start(ctx)
}

func (p *LocalLLMProvider) Stop() {
	p.engine.Stop()
}

func (p *LocalLLMProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"input": text,
	})
	u := p.engine.BaseURL() + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", u, io.NopCloser(strings.NewReader(string(reqBody))))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llama embed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("llama embed parse: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("llama embed: empty response")
	}
	return result.Data[0].Embedding, nil
}

// Chat sends a chat completion request to the local model.
func (p *LocalLLMProvider) Chat(ctx context.Context, messages []map[string]string) (string, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"messages":    messages,
		"temperature": 0.7,
		"max_tokens":  1024,
		"stream":      false,
	})
	u := p.engine.BaseURL() + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", u, io.NopCloser(strings.NewReader(string(reqBody))))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llama chat: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("llama chat parse: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("llama chat: empty response")
	}
	return result.Choices[0].Message.Content, nil
}

// ====================== Port Finder ======================

func findFreePort(start int) int {
	for port := start; port < start+100; port++ {
		if isPortAvailable(port) {
			return port
		}
	}
	return start
}

func serverBinExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (l *LocalLLM) downloadFileWithProgress(url, dest, actionID string, progressStart, progressEnd int) error {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "openkitty/1.0")
	resp, err := (&http.Client{Timeout: 600 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	total := resp.ContentLength
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	var downloaded int64
	buf := make([]byte, 32*1024)
	lastReport := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			downloaded += int64(n)
			if time.Since(lastReport) > 500*time.Millisecond && total > 0 && actionID != "" {
				pct := progressStart + int(float64(progressEnd-progressStart)*float64(downloaded)/float64(total))
				mb := float64(downloaded) / (1024 * 1024)
				totalMB := float64(total) / (1024 * 1024)
				l.progress(actionID, pct, fmt.Sprintf("下载中 %.0f/%.0f MB", mb, totalMB))
				lastReport = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	return nil
}

func isPortAvailable(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	resp, err := http.Get("http://" + addr + "/health")
	if err == nil {
		resp.Body.Close()
		return false // already in use
	}
	return true
}

// ====================== Global Lifecycle ======================

var (
	globalLocalLLM     *LocalLLMProvider
	globalLocalLLMOnce sync.Once
)

// GetLocalLLM returns the global local LLM engine (lazy init).
func GetLocalLLM(modelPath string, hooks ...LocalLLMHooks) *LocalLLMProvider {
	globalLocalLLMOnce.Do(func() {
		globalLocalLLM = NewLocalLLMProvider(modelPath, hooks...)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := globalLocalLLM.Start(ctx); err != nil {
			log.Printf("[LocalLLM] Failed to start: %v", err)
			globalLocalLLM = nil
		}
	})
	return globalLocalLLM
}

// ShutdownLocalLLM stops the local LLM engine.
func ShutdownLocalLLM() {
	if globalLocalLLM != nil {
		globalLocalLLM.Stop()
	}
}
