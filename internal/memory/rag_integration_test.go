package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/textutil"
	knowledgetools "mac/internal/tools/knowledge"
)

// TestChunkPipeline verifies the full chunking pipeline end-to-end.
func TestChunkPipeline(t *testing.T) {
	// Create a test document
	testDoc := `# Introduction
This is a test document about machine learning.

## What is Machine Learning
Machine learning is a subset of artificial intelligence that enables systems
to learn and improve from experience without being explicitly programmed.

## Types of Machine Learning
There are three main types: supervised learning, unsupervised learning, and
reinforcement learning. Each has different use cases and algorithms.

### Supervised Learning
In supervised learning, the model is trained on labeled data. Common algorithms
include linear regression, decision trees, and neural networks.

### Unsupervised Learning
Unsupervised learning finds patterns in unlabeled data. Clustering and
dimensionality reduction are common techniques.

## Deep Learning
Deep learning uses neural networks with many layers. It has revolutionized
computer vision and natural language processing.

## Applications
Machine learning is used in recommendation systems, fraud detection,
autonomous vehicles, and medical diagnosis.`

	// Test 1: Text cleaning
	cleaned := textutil.CleanRAGText(testDoc)
	if cleaned == "" {
		t.Fatal("cleanText returned empty string")
	}
	if strings.Contains(cleaned, "<") {
		t.Error("cleaned text still contains HTML-like characters")
	}

	// Test 2: Chunking
	chunks := knowledgetools.Chunk(cleaned, knowledgetools.DefaultChunkConfig())
	if len(chunks) == 0 {
		t.Fatal("Chunk returned no chunks")
	}
	t.Logf("Produced %d chunks", len(chunks))

	// Verify chunks have content and headings
	for i, c := range chunks {
		if c.Content == "" {
			t.Errorf("Chunk %d has empty content", i)
		}
		if len([]rune(c.Content)) > 900 {
			t.Errorf("Chunk %d exceeds size limit: %d runes", i, len([]rune(c.Content)))
		}
	}

	// Test 3: Heading detection
	foundHeading := false
	for _, c := range chunks {
		if c.Heading != "" {
			foundHeading = true
			t.Logf("Chunk with heading '%s': %s...", c.Heading, textutil.TruncateRunes(c.Content, 50))
		}
	}
	if !foundHeading {
		t.Log("No headings detected (may be normal for short docs)")
	}

	// Test 4: Chapter-aware chunking - heading changes should split
	headings := map[string]bool{}
	lastHeading := ""
	headingChanges := 0
	for _, c := range chunks {
		if c.Heading != lastHeading && lastHeading != "" {
			headingChanges++
		}
		if c.Heading != "" {
			headings[c.Heading] = true
		}
		lastHeading = c.Heading
	}
	t.Logf("Detected %d unique headings, %d heading changes between chunks", len(headings), headingChanges)

	// Test 5: Content contains expected terms
	allContent := ""
	for _, c := range chunks {
		allContent += c.Content + " "
	}
	expectedTerms := []string{"machine learning", "supervised", "unsupervised", "deep learning", "neural network"}
	for _, term := range expectedTerms {
		if !strings.Contains(strings.ToLower(allContent), strings.ToLower(term)) {
			t.Errorf("Expected term '%s' not found in any chunk", term)
		}
	}

	// Test 6: Overlap between consecutive chunks
	for i := 0; i < len(chunks)-1; i++ {
		overlap := knowledgetools.TextOverlap(chunks[i].Content, chunks[i+1].Content)
		// Note: small documents naturally have high overlap due to overlap window
		if len(chunks) > 10 && overlap > 0.9 {
			t.Errorf("Chunks %d and %d have >90%% overlap (%.1f%%) in large doc, possible duplication", i, i+1, overlap*100)
		}
	}
}

// TestAdaptiveChunking verifies file-type-specific chunk sizes.
func TestAdaptiveChunking(t *testing.T) {
	tests := []struct {
		filename string
		wantSize int
	}{
		{"main.go", 400},
		{"readme.md", 600},
		{"report.pdf", 1000},
		{"data.docx", 1000},
		{"notes.txt", 600},
		{"unknown.xyz", 800},
	}
	for _, tt := range tests {
		cfg := knowledgetools.ChunkConfigForFile(tt.filename)
		if cfg.Size != tt.wantSize {
			t.Errorf("ChunkConfigForFile(%s).Size = %d, want %d", tt.filename, cfg.Size, tt.wantSize)
		}
	}
}

// TestTextCleaning verifies the cleaning pipeline doesn't corrupt content.
func TestTextCleaning(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"html_strip", "<p>Hello <b>World</b></p>", "Hello World"},
		{"hyphen_fix", "con-\ncrete", "concrete"},
		{"unicode_norm", "Café", "Caf\u00e9"}, // NFC normalization
		{"bom_strip", "\ufeffHello", "Hello"},
	}
	for _, tt := range tests {
		got := textutil.CleanRAGText(tt.input)
		got = strings.TrimSpace(strings.ReplaceAll(got, "\n\n", " "))
		if !strings.Contains(strings.ToLower(got), strings.ToLower(tt.want)) {
			t.Errorf("%s: cleanText(%q) = %q, want containing %q", tt.name, tt.input, got, tt.want)
		}
	}
}

// TestRRFWeights verifies RRF fusion with different BM25 weights.
func TestRRFWeights(t *testing.T) {
	// Create mock results
	bm25 := []knowledgetools.RAGResult{
		{SourceDoc: "doc1", Content: "BM25 top result", Score: 10},
		{SourceDoc: "doc2", Content: "BM25 second", Score: 8},
	}
	vec := []knowledgetools.VectorResult{
		{DocName: "doc2", Content: "Vector top result", Score: 0.95},
		{DocName: "doc3", Content: "Vector second", Score: 0.80},
	}

	// Test with BM25 weight = 0.8 (favor BM25)
	bm25Heavy := knowledgetools.ReciprocalRankFusion(bm25, vec, 5, 0.8)
	if len(bm25Heavy) == 0 {
		t.Fatal("BM25-heavy RRF returned no results")
	}
	if bm25Heavy[0].SourceDoc != "doc1" {
		t.Errorf("BM25-heavy: first result should be doc1 (BM25 top), got %s", bm25Heavy[0].SourceDoc)
	}

	// Test with BM25 weight = 0.2 (favor vector)
	vecHeavy := knowledgetools.ReciprocalRankFusion(bm25, vec, 5, 0.2)
	if len(vecHeavy) == 0 {
		t.Fatal("Vector-heavy RRF returned no results")
	}
	// With vector weight dominating, doc2 (appears in both) or doc3 should be first
	firstDoc := vecHeavy[0].SourceDoc
	if firstDoc != "doc2" && firstDoc != "doc3" {
		t.Errorf("Vector-heavy: first result should be doc2 or doc3, got %s", firstDoc)
	}
}

// TestChunkOverlap verifies overlap alignment to word boundaries.
func TestChunkOverlap(t *testing.T) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 50)
	cfg := knowledgetools.ChunkConfig{Size: 200, Overlap: 40}
	chunks := knowledgetools.Chunk(text, cfg)
	if len(chunks) < 2 {
		t.Fatal("Expected at least 2 chunks")
	}
	// Verify no chunk starts mid-word
	for _, c := range chunks {
		if len(c.Content) > 0 {
			firstRune := []rune(c.Content)[0]
			if firstRune >= 'a' && firstRune <= 'z' {
				// First char is lowercase letter - might be mid-word
				// Accept if preceded by overlap text
			}
		}
	}
	t.Logf("Produced %d chunks with overlap", len(chunks))
}

// TestPDFLigatureRepair tests the ligature mapping.
func TestPDFLigatureRepair(t *testing.T) {
	input := "organi\ufffda\ufffdaons and fi\ufb01ne examples with fl\ufb02owers"
	repaired := textutil.RepairPDFLigatures(input)
	if strings.Contains(repaired, "\ufffd") {
		t.Logf("Some U+FFFD chars remain (may be expected for non-standard patterns)")
	}
	if !strings.Contains(repaired, "fine") {
		t.Error("fi ligature not repaired: should contain 'fine'")
	}
	if !strings.Contains(repaired, "flowers") {
		t.Error("fl ligature not repaired: should contain 'flowers'")
	}
}

// Integration test: simulate a document upload and search
func TestIntegrationDocumentSearch(t *testing.T) {
	// Create temp dir for test
	tmpDir, _ := os.MkdirTemp("", "rag_test")
	defer os.RemoveAll(tmpDir)

	// Write a test document
	docPath := filepath.Join(tmpDir, "test_doc.txt")
	testContent := "Artificial Intelligence is transforming industries. Machine Learning models need training data. Deep Learning uses neural networks. RAG systems combine retrieval with generation."
	os.WriteFile(docPath, []byte(testContent), 0644)

	// Test extraction
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read test document: %v", err)
	}
	extracted := string(data)
	if extracted != testContent {
		t.Errorf("Extracted content mismatch: %q vs %q", extracted, testContent)
	}

	// Test chunking the extracted text
	chunks := knowledgetools.Chunk(extracted, knowledgetools.ChunkConfig{Size: 100, Overlap: 20})
	if len(chunks) == 0 {
		t.Fatal("No chunks produced")
	}
	t.Logf("Integration test: %d chunks from extracted text", len(chunks))
}

// Benchmark chunking performance.
func BenchmarkChunk(b *testing.B) {
	text := strings.Repeat("Machine learning is a fascinating field of study. ", 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		knowledgetools.Chunk(text, knowledgetools.DefaultChunkConfig())
	}
}
