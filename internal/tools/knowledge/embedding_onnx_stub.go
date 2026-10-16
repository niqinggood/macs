//go:build !onnx || !cgo

package knowledge

import "fmt"

func newONNXEmbedder(EmbeddingConfig) (Embedder, error) {
	return nil, fmt.Errorf("ONNX embedding not compiled in (use -tags onnx)")
}
