package knowledge

import (
	"context"
	"reflect"
	"testing"
)

type fixedEmbedder struct {
	vector []float64
}

func (embedder fixedEmbedder) Embed(context.Context, string) ([]float64, error) {
	return append([]float64(nil), embedder.vector...), nil
}

func (embedder fixedEmbedder) Dimension() int { return len(embedder.vector) }
func (fixedEmbedder) Close() error            { return nil }

func TestEmbeddingConfigEnabledModes(t *testing.T) {
	if (EmbeddingConfig{Mode: EmbeddingModeNone}).IsEnabled() {
		t.Fatal("none mode must be disabled")
	}
	for _, mode := range []EmbeddingMode{
		EmbeddingModeAPI,
		EmbeddingModeOllama,
		EmbeddingModeLocalLlama,
		EmbeddingModeLocalONNX,
	} {
		if !(EmbeddingConfig{Mode: mode}).IsEnabled() {
			t.Fatalf("mode %q must be enabled", mode)
		}
	}
}

func TestEmbedTextUsesConfiguredRuntime(t *testing.T) {
	restore := SetEmbedderForTesting(
		fixedEmbedder{vector: []float64{0.25, 0.75}},
		EmbeddingConfig{Mode: EmbeddingModeAPI, Dimension: 2},
	)
	t.Cleanup(restore)

	got, err := EmbedText(context.Background(), "query")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []float64{0.25, 0.75}) {
		t.Fatalf("EmbedText = %#v", got)
	}
	if !EmbedderEnabled() || EmbedderDim() != 2 || CurrentEmbeddingMode() != EmbeddingModeAPI {
		t.Fatal("embedding runtime did not expose its configured state")
	}
}

func TestSimpleTokenizerSeparatesCJKAndASCII(t *testing.T) {
	got := simpleTokenizer("Go语言 123!")
	want := []string{"go", "语", "言", "123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("simpleTokenizer = %#v, want %#v", got, want)
	}
}
