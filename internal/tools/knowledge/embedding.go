package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ====================== Embedding Configuration ======================

// EmbeddingMode controls the embedding backend.
type EmbeddingMode string

const (
	EmbeddingModeNone       EmbeddingMode = "none"        // pure BM25, no vectors
	EmbeddingModeAPI        EmbeddingMode = "api"         // call external /v1/embeddings
	EmbeddingModeOllama     EmbeddingMode = "ollama"      // local Ollama /api/embeddings
	EmbeddingModeLocalLlama EmbeddingMode = "local_llama" // auto-download llama.cpp + model
	EmbeddingModeLocalONNX  EmbeddingMode = "local_onnx"  // local ONNX Runtime model
)

// EmbeddingConfig is loaded from config.yaml at startup.
type EmbeddingConfig struct {
	Mode          EmbeddingMode `json:"mode" yaml:"mode"`
	APIKey        string        `json:"api_key" yaml:"api_key"`
	ModelName     string        `json:"model_name" yaml:"model_name"`
	BaseURL       string        `json:"base_url" yaml:"base_url"`
	Dimension     int           `json:"dimension" yaml:"dimension"`
	ONNXModelPath string        `json:"onnx_model_path" yaml:"onnx_model_path"`
	ONNXTokenPath string        `json:"onnx_token_path" yaml:"onnx_token_path"`
}

// DefaultEmbeddingConfig returns safe defaults (no embedding).
func DefaultEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		Mode:      EmbeddingModeNone,
		Dimension: 768,
	}
}

// IsEnabled reports whether the configuration requests a supported backend.
// Use EmbedderEnabled after InitEmbedder to check runtime availability.
func (c EmbeddingConfig) IsEnabled() bool {
	return c.Mode == EmbeddingModeAPI || c.Mode == EmbeddingModeOllama || c.Mode == EmbeddingModeLocalLlama || c.Mode == EmbeddingModeLocalONNX
}

// ====================== Embedder Interface ======================

// Embedder converts text to vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	Dimension() int
	Close() error
}

// LocalEmbeddingProvider is the minimal local-LLM surface needed by the
// embedding runtime. The root application injects its concrete provider.
type LocalEmbeddingProvider interface {
	Embed(context.Context, string) ([]float64, error)
	Stop()
}

type LocalProviderFactory func(modelPath string) LocalEmbeddingProvider

// ====================== API Embedder ======================

// apiEmbedder calls a /v1/embeddings endpoint (OpenAI-compatible).
type apiEmbedder struct {
	apiKey    string
	modelName string
	baseURL   string
	client    *http.Client
}

func newAPIEmbedder(cfg EmbeddingConfig) (*apiEmbedder, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("embedding: base_url required for api mode")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("embedding: api_key required for api mode")
	}
	model := cfg.ModelName
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &apiEmbedder{
		apiKey:    cfg.APIKey,
		modelName: model,
		baseURL:   base,
		client:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *apiEmbedder) Dimension() int { return 1536 } // default, overridden by config

func (e *apiEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"input": text,
		"model": e.modelName,
	})
	url := e.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedding HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("embedding parse: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embedding: empty response")
	}
	return result.Data[0].Embedding, nil
}

func (e *apiEmbedder) Close() error { return nil }

// ====================== Ollama Embedder ======================

// ollamaEmbedder calls Ollama's local /api/embeddings endpoint.
type ollamaEmbedder struct {
	baseURL   string
	modelName string
	dimension int
	client    *http.Client
}

func newOllamaEmbedder(cfg EmbeddingConfig) (*ollamaEmbedder, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "http://localhost:11434"
	}
	model := cfg.ModelName
	if model == "" {
		model = "nomic-embed-text"
	}
	// Probe the dimension from Ollama
	dim := cfg.Dimension
	if dim <= 0 {
		dim = probeOllamaDimension(base, model)
	}
	return &ollamaEmbedder{
		baseURL:   base,
		modelName: model,
		dimension: dim,
		client:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *ollamaEmbedder) Dimension() int { return e.dimension }

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"model":  e.modelName,
		"prompt": text,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed call: %w (is Ollama running? ollama serve)", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ollama embed HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ollama embed parse: %w", err)
	}
	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embed: empty response (model '%s' pulled? ollama pull %s)", e.modelName, e.modelName)
	}
	return result.Embedding, nil
}

func (e *ollamaEmbedder) Close() error { return nil }

type localLlamaEmbedder struct {
	provider LocalEmbeddingProvider
}

func (e *localLlamaEmbedder) Dimension() int {
	v, err := e.provider.Embed(context.Background(), "dimension probe")
	if err != nil {
		return 768
	}
	return len(v)
}

func (e *localLlamaEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	return e.provider.Embed(ctx, text)
}

func (e *localLlamaEmbedder) Close() error {
	e.provider.Stop()
	return nil
}

// probeOllamaDimension queries Ollama for the model's embedding dimension.
func probeOllamaDimension(baseURL, model string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(map[string]string{"model": model, "prompt": "test"})
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/embeddings", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 768 // default fallback
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if json.Unmarshal(body, &result) == nil && len(result.Embedding) > 0 {
		return len(result.Embedding)
	}
	return 768
}

// ====================== Global Embedder Lifecycle ======================

var (
	globalEmbedder     Embedder
	globalEmbedderCfg  EmbeddingConfig
	globalEmbedderID   string
	globalEmbedderOnce sync.Once
	globalEmbedderMu   sync.RWMutex
)

// InitEmbedder creates the global embedder from config. Safe to call multiple times.
func InitEmbedder(cfg EmbeddingConfig, localFactories ...LocalProviderFactory) {
	globalEmbedderMu.Lock()
	defer func() {
		globalEmbedderID = embeddingIdentity(globalEmbedderCfg)
		globalEmbedderMu.Unlock()
	}()
	globalEmbedder = nil
	globalEmbedderOnce = sync.Once{}
	globalEmbedderOnce.Do(func() {
		globalEmbedderCfg = cfg
		if cfg.Mode == EmbeddingModeNone {
			log.Println("[Embedding] mode=none, hybrid search disabled")
			return
		}
		if cfg.Mode == EmbeddingModeAPI {
			e, err := newAPIEmbedder(cfg)
			if err != nil {
				log.Printf("[Embedding] API embedder init failed: %v, falling back to none", err)
				globalEmbedderCfg.Mode = EmbeddingModeNone
				return
			}
			log.Printf("[Embedding] API embedder ready: model=%s url=%s", cfg.ModelName, cfg.BaseURL)
			globalEmbedder = e
			return
		}
		if cfg.Mode == EmbeddingModeOllama {
			e, err := newOllamaEmbedder(cfg)
			if err != nil {
				log.Printf("[Embedding] Ollama embedder init failed: %v, falling back to none", err)
				globalEmbedderCfg.Mode = EmbeddingModeNone
				return
			}
			log.Printf("[Embedding] Ollama embedder ready: model=%s dim=%d url=%s", e.modelName, e.dimension, e.baseURL)
			globalEmbedder = e
			return
		}
		if cfg.Mode == EmbeddingModeLocalLlama {
			var provider LocalEmbeddingProvider
			if len(localFactories) > 0 && localFactories[0] != nil {
				provider = localFactories[0](cfg.ONNXModelPath)
			}
			if provider == nil {
				log.Printf("[Embedding] LocalLlama init failed, falling back to none")
				globalEmbedderCfg.Mode = EmbeddingModeNone
				return
			}
			e := &localLlamaEmbedder{provider: provider}
			log.Printf("[Embedding] LocalLlama ready: auto-downloaded llama.cpp + model")
			globalEmbedder = e
			return
		}
		if cfg.Mode == EmbeddingModeLocalONNX {
			e, err := newONNXEmbedder(cfg)
			if err != nil {
				log.Printf("[Embedding] ONNX embedder init failed: %v, falling back to none", err)
				globalEmbedderCfg.Mode = EmbeddingModeNone
				return
			}
			log.Printf("[Embedding] ONNX embedder ready: model=%s dim=%d", filepath.Base(cfg.ONNXModelPath), e.Dimension())
			globalEmbedder = e
			return
		}
	})
}

// EmbedderEnabled returns true if a working embedder is available.
func EmbedderEnabled() bool {
	globalEmbedderMu.RLock()
	defer globalEmbedderMu.RUnlock()
	return globalEmbedder != nil && globalEmbedderCfg.Mode != EmbeddingModeNone
}

// EmbedderDim returns the configured vector dimension.
func EmbedderDim() int {
	globalEmbedderMu.RLock()
	defer globalEmbedderMu.RUnlock()
	if globalEmbedder != nil {
		return globalEmbedder.Dimension()
	}
	return globalEmbedderCfg.Dimension
}

// CurrentEmbeddingMode reports the initialized backend mode.
func CurrentEmbeddingMode() EmbeddingMode {
	globalEmbedderMu.RLock()
	defer globalEmbedderMu.RUnlock()
	return globalEmbedderCfg.Mode
}

// SetEmbedderForTesting replaces the process embedder and returns a restore
// function. It is intended for deterministic tests in host packages.
func SetEmbedderForTesting(embedder Embedder, cfg EmbeddingConfig) func() {
	globalEmbedderMu.Lock()
	previousEmbedder, previousConfig, previousID := globalEmbedder, globalEmbedderCfg, globalEmbedderID
	globalEmbedder, globalEmbedderCfg = embedder, cfg
	globalEmbedderID = embeddingIdentity(cfg)
	globalEmbedderMu.Unlock()
	return func() {
		globalEmbedderMu.Lock()
		defer globalEmbedderMu.Unlock()
		globalEmbedder, globalEmbedderCfg = previousEmbedder, previousConfig
		globalEmbedderID = previousID
	}
}

// EmbedText generates an embedding for a single text string.
func EmbedText(ctx context.Context, text string) ([]float64, error) {
	globalEmbedderMu.RLock()
	embedder := globalEmbedder
	globalEmbedderMu.RUnlock()
	if embedder == nil {
		return nil, fmt.Errorf("embedder not initialized")
	}
	return embedder.Embed(ctx, text)
}

// EmbedTexts generates embeddings for a batch of texts.
func EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	globalEmbedderMu.RLock()
	embedder := globalEmbedder
	globalEmbedderMu.RUnlock()
	if embedder == nil {
		return nil, fmt.Errorf("embedder not initialized")
	}
	vecs := make([][]float64, len(texts))
	for i, t := range texts {
		v, err := embedder.Embed(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("embed text[%d]: %w", i, err)
		}
		vecs[i] = v
	}
	return vecs, nil
}

// ShutdownEmbedder closes the global embedder if one exists.
func ShutdownEmbedder() {
	globalEmbedderMu.Lock()
	embedder := globalEmbedder
	globalEmbedder = nil
	globalEmbedderMu.Unlock()
	if embedder != nil {
		embedder.Close()
	}
}

// ====================== Tokenizer Helper ======================

// simpleTokenizer is a minimal tokenizer for mean-pooling ONNX models.
// Splits CJK by character, English by whitespace/punctuation.
func simpleTokenizer(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	var tokens []string
	var current strings.Builder

	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			flush()
			tokens = append(tokens, string(r))
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}
