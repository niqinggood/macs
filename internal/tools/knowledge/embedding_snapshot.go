package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

// EmbeddingSnapshot binds a provider and its cache identity in one read. The
// identity contains no credentials, and changes with model/backend/dimension or
// replaced local model files. In-flight requests keep their original provider.
func EmbeddingSnapshot() (Embedder, string) {
	globalEmbedderMu.RLock()
	embedder, cfg, id := globalEmbedder, globalEmbedderCfg, globalEmbedderID
	globalEmbedderMu.RUnlock()
	if embedder == nil || !cfg.IsEnabled() {
		return nil, ""
	}
	return embedder, id
}

// Capture file metadata when a provider is initialized. Rechecking it during
// recall would label an already loaded model with a replacement file's identity.
func embeddingIdentity(cfg EmbeddingConfig) string {
	identity := []interface{}{"embedding-v1", cfg.Mode, cfg.ModelName, cfg.BaseURL, cfg.Dimension, cfg.ONNXModelPath, cfg.ONNXTokenPath}
	for _, path := range []string{cfg.ONNXModelPath, cfg.ONNXTokenPath} {
		if info, err := os.Stat(path); err == nil {
			identity = append(identity, info.Size(), info.ModTime().UnixNano())
		}
	}
	data, _ := json.Marshal(identity)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
