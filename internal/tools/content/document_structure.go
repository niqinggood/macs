package content

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"log"
	"os"
	"strings"

	"mac/internal/textutil"
)

// ====================== DOCX Structure-Aware Extraction ======================

// docxParagraph represents a <w:p> element in document.xml.
type docxParagraph struct {
	Style string // w:pStyle value, e.g. "Heading1", "Heading2"
	Text  string // accumulated text from w:t runs
}

// extractDocxStructured parses word/document.xml and preserves heading structure.
// Output uses # markers for headings, compatible with splitByHeadings.
func ExtractDocxStructured(filePath string) string {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return ""
	}
	defer reader.Close()

	var docFile *zip.File
	for _, f := range reader.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return ""
	}

	rc, err := docFile.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()

	paragraphs, err := parseDocxXML(rc)
	if err != nil || len(paragraphs) == 0 {
		return ""
	}

	// Build structured text with # markers for headings
	var out strings.Builder
	for _, p := range paragraphs {
		if p.Text == "" {
			continue
		}
		switch {
		case strings.HasPrefix(p.Style, "Heading1") || p.Style == "Title":
			out.WriteString("\n# " + p.Text + "\n\n")
		case strings.HasPrefix(p.Style, "Heading2"):
			out.WriteString("\n## " + p.Text + "\n\n")
		case strings.HasPrefix(p.Style, "Heading3"):
			out.WriteString("\n### " + p.Text + "\n\n")
		case strings.HasPrefix(p.Style, "Heading"):
			out.WriteString("\n## " + p.Text + "\n\n")
		default:
			out.WriteString(p.Text + "\n\n")
		}
	}
	return strings.TrimSpace(out.String())
}

// parseDocxXML parses the document.xml stream and extracts paragraph styles + text.
func parseDocxXML(r io.Reader) ([]docxParagraph, error) {
	decoder := xml.NewDecoder(r)
	var paragraphs []docxParagraph
	var current *docxParagraph
	var inRun bool
	var inStyle bool
	var depth int

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "p": // <w:p> — paragraph start
				current = &docxParagraph{}
				paragraphs = append(paragraphs, *current)

			case "pStyle": // <w:pStyle> — paragraph style
				if current != nil {
					inStyle = true
					// Read val attribute
					for _, attr := range t.Attr {
						if attr.Name.Local == "val" {
							current.Style = attr.Value
						}
					}
				}

			case "t": // <w:t> — text run
				inRun = true

			case "r": // <w:r> — run
				// Just track, text is in child <w:t>
			}

		case xml.CharData:
			if inRun && current != nil {
				current.Text += string(t)
			}
			if inStyle && current != nil {
				// Some styles use CharData instead of attr
				val := strings.TrimSpace(string(t))
				if val != "" && current.Style == "" {
					current.Style = val
				}
			}

		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inRun = false
			case "pStyle":
				inStyle = false
			case "p":
				if current != nil {
					// Update the stored paragraph (slices are value types)
					paragraphs[len(paragraphs)-1] = *current
				}
				current = nil
			}
			depth--
		}
		_ = depth
	}

	// Filter out empty paragraphs
	var result []docxParagraph
	for _, p := range paragraphs {
		if p.Text != "" {
			result = append(result, p)
		}
	}
	return result, nil
}

// ====================== PDF Heading Heuristic Detection ======================

// detectPDFHeadings scans extracted PDF text and inserts # markers for likely headings.
// PDF text has no structural metadata, so we use heuristics:
//   - Short lines without sentence-ending punctuation → possible heading
//   - All-uppercase short lines → likely heading
//   - Lines matching numbered patterns → section heading
//   - Lines ending with Chinese colon → sub-heading
func detectPDFHeadings(text string) string {
	lines := strings.Split(text, "\n")
	var out strings.Builder
	prevBlank := true // start of doc is a heading boundary

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out.WriteString("\n")
			prevBlank = true
			continue
		}

		// Check if this line looks like a heading
		headingLevel := classifyPDFLine(trimmed, prevBlank)

		switch headingLevel {
		case 1:
			out.WriteString("\n# " + trimmed + "\n\n")
			prevBlank = true
		case 2:
			out.WriteString("\n## " + trimmed + "\n\n")
			prevBlank = true
		default:
			out.WriteString(trimmed)
			if i < len(lines)-1 {
				out.WriteString("\n")
			}
			prevBlank = false
		}
	}
	return strings.TrimSpace(out.String())
}

// classifyPDFLine determines if a PDF text line is a heading.
// Returns 0 (body), 1 (H1), or 2 (H2).
func classifyPDFLine(line string, prevBlank bool) int {
	runes := []rune(line)
	if len(runes) < 3 || len(runes) > 120 {
		return 0 // too short or too long to be a heading
	}

	// Must be preceded by blank line (or start of doc)
	if !prevBlank {
		return 0
	}

	// Number pattern: "1.", "1.1", "Chapter 1", "第X章"
	if textutil.IsNumberedHeading(line) || textutil.IsChineseNumberedHeading(line) ||
		strings.HasPrefix(line, "Chapter ") || strings.HasPrefix(line, "Part ") {
		return 1
	}

	// All uppercase short line (English heading)
	if len(runes) <= 60 && isAllUpper(line) {
		return 1
	}

	// Short line without sentence-ending punctuation
	hasEndPunct := strings.HasSuffix(line, ".") || strings.HasSuffix(line, "。") ||
		strings.HasSuffix(line, "！") || strings.HasSuffix(line, "?") || strings.HasSuffix(line, "？")
	if len(runes) <= 40 && !hasEndPunct {
		// Check if it ends with colon (Chinese sub-heading)
		if strings.HasSuffix(line, "：") || strings.HasSuffix(line, ":") {
			return 2
		}
		// Bold-ish detection: if it looks like a title (no lowercase english words)
		if looksLikeTitle(line) {
			return 1
		}
	}

	return 0
}

// isAllUpper checks if a string contains only uppercase letters, spaces, and digits.
func isAllUpper(s string) bool {
	hasLetter := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false // lowercase = not all-upper
		}
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
	}
	return hasLetter // must have at least one letter
}

// looksLikeTitle returns true if the line looks like a title (few lowercase words).
func looksLikeTitle(line string) bool {
	runes := []rune(line)
	if len(runes) < 3 || len(runes) > 50 {
		return false
	}
	lowerCount := 0
	letterCount := 0
	for _, r := range line {
		if r >= 'a' && r <= 'z' {
			lowerCount++
			letterCount++
		}
		if (r >= 'A' && r <= 'Z') || (r >= 0x4e00 && r <= 0x9fff) {
			letterCount++
		}
	}
	if letterCount == 0 {
		return false
	}
	// If < 30% of letters are lowercase, it's title-like
	return float64(lowerCount)/float64(letterCount) < 0.3
}

// extractDocxStructuredOrFallback tries structured extraction, falls back to mydocx.
func ExtractDocxStructuredOrFallback(filePath string, fallback func(string) string) string {
	text := ExtractDocxStructured(filePath)
	if text != "" && len(text) > 50 {
		log.Printf("[RAG] DOCX structured: %d chars with heading markers", len(text))
		return text
	}
	// Fall back to mydocx
	if fallback == nil {
		return ""
	}
	return fallback(filePath)
}

// extractPDFWithHeadings tries to detect headings in PDF text.
// Uses Unstructured-style classifier (ClassifyText) for superior heading/table/list detection.
// Set OPENKITTY_CHUNK=true to enable title-based semantic chunking output.
func ExtractPDFWithHeadings(filePath string, extractor func(string) string) string {
	if extractor == nil {
		return ""
	}
	text := extractor(filePath)
	if text == "" {
		return ""
	}
	text = textutil.CleanDocument(text)
	elements := textutil.ClassifyText(text)
	if len(elements) == 0 {
		return text
	}

	// Optional title-based chunking
	if os.Getenv("OPENKITTY_CHUNK") == "true" {
		cfg := textutil.DefaultUnstructChunkConfig()
		chunks := textutil.ChunkByTitle(elements, cfg)
		log.Printf("[RAG] PDF chunked: %d elements → %d chunks", len(elements), len(chunks))
		return textutil.UnstructChunksToMarkdown(chunks)
	}

	formatted := textutil.FormatClassifiedElements(elements)
	if len(formatted) > len(text)/2 {
		log.Printf("[RAG] PDF classified: %d elements, output %d chars",
			len(elements), len(formatted))
	}
	return formatted
}
