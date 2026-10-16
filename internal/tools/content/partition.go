package content

import (
	"fmt"
	"os"

	"mac/internal/textutil"
)

// ====================== File Type Detection ======================
// Go port of Unstructured's file_utils/filetype.py + auto.py routing logic.
// Uses extension + magic bytes detection (no CGO, no libmagic).

type FileCategory = textutil.FileCategory

const (
	CatUnknown  = textutil.CatUnknown
	CatPDF      = textutil.CatPDF
	CatDOCX     = textutil.CatDOCX
	CatPPTX     = textutil.CatPPTX
	CatXLSX     = textutil.CatXLSX
	CatHTML     = textutil.CatHTML
	CatXML      = textutil.CatXML
	CatEPUB     = textutil.CatEPUB
	CatRTF      = textutil.CatRTF
	CatEmail    = textutil.CatEmail
	CatCSV      = textutil.CatCSV
	CatTSV      = textutil.CatTSV
	CatJSON     = textutil.CatJSON
	CatNDJSON   = textutil.CatNDJSON
	CatMarkdown = textutil.CatMarkdown
	CatText     = textutil.CatText
	CatImage    = textutil.CatImage
	CatAudio    = textutil.CatAudio
	CatODT      = textutil.CatODT
	CatOrgMode  = textutil.CatOrgMode
	CatRST      = textutil.CatRST
)

var DetectFileType = textutil.DetectFileType

// ====================== Document Auto-Partition Router ======================
// Port of Unstructured's auto.partition() dispatch logic.

type UnstructuredDoc struct {
	Path     string
	Category FileCategory
	Elements []textutil.ClassifiedElement
	Metadata map[string]string
}

type PartitionDependencies struct {
	PDF  func(string) string
	DOCX func(string) string
	XLSX func([]byte) (string, error)
}

// PartitionDoc auto-detects file type and partitions into elements.
// This is the Go equivalent of Unstructured's auto.partition().
// Set OPENKITTY_CHUNK=true env var to enable title-based chunking output.
func PartitionDoc(path string, dependencies ...PartitionDependencies) (*UnstructuredDoc, error) {
	cat := DetectFileType(path)
	if cat == CatUnknown {
		return nil, fmt.Errorf("unsupported file format: %s", path)
	}

	doc := &UnstructuredDoc{Path: path, Category: cat, Metadata: map[string]string{}}

	text, err := ExtractDocumentText(path, cat, dependencies...)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, fmt.Errorf("no text extracted from %s", path)
	}

	// Apply Unstructured-style cleaning before classification
	cleaned := textutil.CleanDocument(text)
	doc.Elements = textutil.ClassifyText(cleaned)
	return doc, nil
}

// PartitionAndChunk partitions a document and optionally chunks the output.
// This combines partition + chunk_by_title in one call, like Unstructured's full pipeline.
func PartitionAndChunk(path string, chunkCfg textutil.UnstructChunkConfig, dependencies ...PartitionDependencies) ([]textutil.UnstructChunk, error) {
	doc, err := PartitionDoc(path, dependencies...)
	if err != nil {
		return nil, err
	}
	return textutil.ChunkByTitle(doc.Elements, chunkCfg), nil
}

// extractDocText routes to the appropriate parser based on file category.
func ExtractDocumentText(path string, cat FileCategory, dependencies ...PartitionDependencies) (string, error) {
	var deps PartitionDependencies
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	switch cat {
	case CatPDF:
		if deps.PDF == nil {
			return "", fmt.Errorf("PDF extractor is not configured")
		}
		return deps.PDF(path), nil
	case CatDOCX:
		if deps.DOCX == nil {
			return "", fmt.Errorf("DOCX extractor is not configured")
		}
		return deps.DOCX(path), nil
	case CatPPTX:
		return ExtractPPTX(path)
	case CatXLSX:
		if deps.XLSX == nil {
			return "", fmt.Errorf("XLSX extractor is not configured")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return deps.XLSX(data)
	case CatHTML, CatXML:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return ExtractHTML(data)
	case CatEPUB:
		return ExtractEPUB(path)
	case CatRTF:
		return ExtractRTF(path)
	case CatEmail:
		return ExtractEmail(path)
	case CatCSV, CatTSV, CatMarkdown, CatText, CatOrgMode, CatRST:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case CatJSON, CatNDJSON:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case CatODT:
		return ExtractODT(path)
	case CatImage:
		return "", fmt.Errorf("image text extraction requires OCR (not yet implemented)")
	case CatAudio:
		return "", fmt.Errorf("audio transcription requires external service (not yet implemented)")
	default:
		return "", fmt.Errorf("unsupported format: %s", cat)
	}
}
