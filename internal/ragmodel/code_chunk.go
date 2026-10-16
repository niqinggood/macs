package ragmodel

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/zoobz-io/chisel"
	golangchisel "github.com/zoobz-io/chisel/golang"
)

var codeChunker *chisel.Chunker

func initCodeChunker() {
	if codeChunker != nil {
		return
	}
	codeChunker = chisel.New(
		golangchisel.New(),
	)
	log.Println("[RAG] Code chunker ready: Go")
}

// detectLanguage maps file extension to chisel Language.
func DetectLanguage(filename string) (chisel.Language, bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go":
		return chisel.Go, true
	// Future: .py → Python, .ts/.js → TypeScript, .rs → Rust, .md → Markdown
	default:
		return "", false
	}
}

// chunkCodeFile uses AST-aware chunking for supported source code files.
// Returns chisel chunks; caller maps them to RAGChunk.
func ChunkCodeFile(filePath string, data []byte) ([]chisel.Chunk, error) {
	initCodeChunker()

	filename := filepath.Base(filePath)
	lang, ok := DetectLanguage(filename)
	if !ok {
		return nil, fmt.Errorf("unsupported language: %s", filePath)
	}

	chunks, err := codeChunker.Chunk(context.Background(), lang, filename, data)
	if err != nil {
		return nil, fmt.Errorf("chisel chunk: %w", err)
	}

	return chunks, nil
}

// chiselToRAGChunks converts chisel.Chunk to our RAGChunk format.
func ChiselToChunks(kbID uint, sourceDoc string, chunks []chisel.Chunk) []Chunk {
	out := make([]Chunk, 0, len(chunks))
	for i, c := range chunks {
		// Build rich heading from symbol + kind + context
		heading := string(c.Kind) + ": " + c.Symbol
		if len(c.Context) > 0 {
			heading = strings.Join(c.Context, " > ") + " > " + heading
		}

		ch := Chunk{
			KbID:       kbID,
			SourceDoc:  sourceDoc,
			ChunkIndex: i,
			Heading:    heading,
			Content:    c.Content,
		}
		out = append(out, ch)
	}
	return out
}

// ragExtractTextCodeAware extends ragExtractText with AST-aware chunking
// for supported source code files.
func ExtractCodeAware(filePath string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		// Let AST chunker handle it — return empty to skip text extraction
		return "", true
	}
	return "", false
}
