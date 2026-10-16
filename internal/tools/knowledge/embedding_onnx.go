//go:build onnx && cgo

package knowledge

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"

	ort "github.com/yalue/onnxruntime_go"
)

// onnxEmbedder runs a local ONNX model for text embedding.
// Requires: ONNX Runtime C library, a compatible .onnx model (e.g., BGE-small-zh).
type onnxEmbedder struct {
	session    *ort.AdvancedSession
	inputName  string
	outputName string
	dim        int
	modelPath  string
	vocabPath  string
	maxLen     int // max input tokens
}

func newONNXEmbedder(cfg EmbeddingConfig) (Embedder, error) {
	if cfg.ONNXModelPath == "" {
		return nil, fmt.Errorf("onnx: model path not configured")
	}
	if _, err := os.Stat(cfg.ONNXModelPath); err != nil {
		return nil, fmt.Errorf("onnx: model not found at %s: %w", cfg.ONNXModelPath, err)
	}

	log.Printf("[ONNX] Loading model: %s", cfg.ONNXModelPath)

	// Initialize ONNX Runtime
	ort.SetLibraryPath("") // auto-detect from system paths

	// Load session
	session, err := ort.NewAdvancedSession(cfg.ONNXModelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state", "pooler_output"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("onnx: new session: %w", err)
	}

	// Determine dimension from output shape
	outputs := session.Outputs()
	dim := 768 // default for BGE-small
	if len(outputs) > 0 {
		shape := outputs[0].Shape
		if len(shape) >= 2 && shape[1] > 0 {
			dim = int(shape[1])
		}
	}

	maxLen := 512

	e := &onnxEmbedder{
		session:    session,
		inputName:  "input_ids",
		outputName: "last_hidden_state",
		dim:        dim,
		modelPath:  cfg.ONNXModelPath,
		vocabPath:  cfg.ONNXTokenPath,
		maxLen:     maxLen,
	}

	log.Printf("[ONNX] Model ready: dim=%d maxLen=%d", dim, maxLen)
	return e, nil
}

func (e *onnxEmbedder) Dimension() int { return e.dim }

func (e *onnxEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	// Tokenize
	inputIDs, attentionMask, err := e.tokenize(text)
	if err != nil {
		return nil, fmt.Errorf("onnx tokenize: %w", err)
	}

	seqLen := int64(len(inputIDs))

	// Create input tensors
	inputTensor, err := ort.NewTensor[int64](
		ort.NewShape(1, seqLen),
		inputIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("onnx input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	maskTensor, err := ort.NewTensor[int64](
		ort.NewShape(1, seqLen),
		attentionMask,
	)
	if err != nil {
		return nil, fmt.Errorf("onnx mask tensor: %w", err)
	}
	defer maskTensor.Destroy()

	// Token type IDs (all zeros for single-sequence)
	tokenTypeIDs := make([]int64, seqLen)
	typeTensor, err := ort.NewTensor[int64](
		ort.NewShape(1, seqLen),
		tokenTypeIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("onnx type tensor: %w", err)
	}
	defer typeTensor.Destroy()

	// Create output tensor
	outShape := ort.NewShape(1, seqLen, int64(e.dim))
	outputTensor, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return nil, fmt.Errorf("onnx output tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// Run inference
	err = e.session.Run(
		map[string]ort.ArbitraryTensor{
			"input_ids":      inputTensor,
			"attention_mask": maskTensor,
			"token_type_ids": typeTensor,
		},
		map[string]ort.ArbitraryTensor{
			"last_hidden_state": outputTensor,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}

	// Mean pooling: average token embeddings, using attention_mask
	outputData := outputTensor.GetData()
	vec := meanPooling(outputData, attentionMask, e.dim, int(seqLen))

	return vec, nil
}

func (e *onnxEmbedder) Close() error {
	if e.session != nil {
		return e.session.Destroy()
	}
	return nil
}

// ====================== Simple BPE Tokenizer ======================

// tokenize converts text to input_ids and attention_mask using simple rule-based tokenization.
// This is a minimal implementation for BGE-style models (BERT vocabulary mapping).
// For production, use a full tokenizer like go-tokenizers or load the vocab JSON.
func (e *onnxEmbedder) tokenize(text string) ([]int64, []int64, error) {
	tokens := simpleTokenizer(text)

	// Truncate to maxLen - 2 (for [CLS] and [SEP])
	if len(tokens) > e.maxLen-2 {
		tokens = tokens[:e.maxLen-2]
	}

	// BGE default mapping: [CLS]=101, [SEP]=102, [UNK]=100
	// Map tokens to IDs using a simple hash-based approximation for common tokens
	inputIDs := make([]int64, 0, len(tokens)+2)
	inputIDs = append(inputIDs, 101) // [CLS]

	for _, tok := range tokens {
		id := e.tokenToID(tok)
		inputIDs = append(inputIDs, id)
	}

	inputIDs = append(inputIDs, 102) // [SEP]

	// All tokens are real (attention_mask = 1)
	attentionMask := make([]int64, len(inputIDs))
	for i := range attentionMask {
		attentionMask[i] = 1
	}

	return inputIDs, attentionMask, nil
}

// tokenToID maps a token string to a vocabulary ID.
// Uses a simple hash for common CJK/ASCII tokens as approximation.
// For full accuracy, load the model's tokenizer.json and use a proper tokenizer.
func (e *onnxEmbedder) tokenToID(token string) int64 {
	// Simple hash-based ID generation for prototype
	// Maps to [100, 30000] range (typical BERT vocab size)
	var h uint64
	for _, b := range []byte(token) {
		h = h*31 + uint64(b)
	}
	return int64(h%29900) + 100
}

// ====================== Mean Pooling ======================

func meanPooling(hiddenStates []float32, attentionMask []int64, dim, seqLen int) []float64 {
	vec := make([]float64, dim)
	var totalWeight float64

	for t := 0; t < seqLen; t++ {
		if attentionMask[t] == 0 {
			continue
		}
		weight := 1.0
		totalWeight += weight
		offset := t * dim
		for d := 0; d < dim; d++ {
			vec[d] += float64(hiddenStates[offset+d]) * weight
		}
	}

	if totalWeight > 0 {
		for d := 0; d < dim; d++ {
			vec[d] /= totalWeight
		}
	}

	// L2 normalize
	var norm float64
	for d := 0; d < dim; d++ {
		norm += vec[d] * vec[d]
	}
	if norm > 0 {
		norm = float64(1.0 / mSqrt(float64(norm)))
		for d := 0; d < dim; d++ {
			vec[d] *= norm
		}
	}

	return vec
}

func mSqrt(x float64) float64 {
	return math.Sqrt(x)
}

// ====================== Simple Tokenizer (fallback) ======================
// This would normally be replaced by a proper BPE tokenizer from the model vocab.
// For the ONNX path, the default simpleTokenizer is defined in rag_embedding.go.
// If the model supplies a tokenizer.json, we could load it here.
