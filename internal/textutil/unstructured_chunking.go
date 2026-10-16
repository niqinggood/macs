package textutil

import (
	"fmt"
	"strings"
)

// ====================== Unstructured-style Chunking (title-based) ======================
// Port of Unstructured's chunking/title.py + chunking/base.py core algorithm.
// Chunks classified elements into semantic sections based on heading boundaries.

const (
	unstructChunkMaxChars      = 500
	unstructNewAfterNChars     = 500
	unstructCombineUnderNChars = 200
	unstructChunkOverlap       = 0
)

type UnstructChunkConfig struct {
	MaxChars          int
	NewAfterNChars    int
	CombineUnderN     int
	Overlap           int
	MultipageSections bool
}

func DefaultUnstructChunkConfig() UnstructChunkConfig {
	return UnstructChunkConfig{
		MaxChars:          unstructChunkMaxChars,
		NewAfterNChars:    unstructNewAfterNChars,
		CombineUnderN:     unstructCombineUnderNChars,
		Overlap:           unstructChunkOverlap,
		MultipageSections: true,
	}
}

type UnstructChunk struct {
	Heading  string
	Text     string
	Elements []ClassifiedElement
}

// ChunkByTitle chunks classified elements into semantic sections.
func ChunkByTitle(elements []ClassifiedElement, cfg UnstructChunkConfig) []UnstructChunk {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = unstructChunkMaxChars
	}
	if cfg.NewAfterNChars <= 0 {
		cfg.NewAfterNChars = cfg.MaxChars
	}

	preChunks := splitAtTitles(elements, cfg.MultipageSections)

	if cfg.CombineUnderN > 0 {
		preChunks = combineSmallChunks(preChunks, cfg.CombineUnderN, cfg.NewAfterNChars)
	}

	var chunks []UnstructChunk
	for _, pc := range preChunks {
		chunks = append(chunks, splitOversizedChunk(pc, cfg)...)
	}

	if cfg.Overlap > 0 && len(chunks) > 1 {
		chunks = addChunkOverlap(chunks, cfg.Overlap)
	}

	return chunks
}

// ====================== Pre-chunking ======================

type unstructPreChunk struct {
	Heading  string
	Elements []ClassifiedElement
	Text     string
	Chars    int
}

func splitAtTitles(elements []ClassifiedElement, multipage bool) []unstructPreChunk {
	var chunks []unstructPreChunk
	var current unstructPreChunk
	currentHeading := ""

	flush := func() {
		if len(current.Elements) > 0 {
			current.Heading = currentHeading
			current.Text = concatElements(current.Elements)
			current.Chars = len([]rune(current.Text))
			chunks = append(chunks, current)
			current = unstructPreChunk{}
		}
	}

	for _, el := range elements {
		switch el.Type {
		case ElemTitle:
			flush()
			currentHeading = el.Text
			current.Elements = append(current.Elements, el)

		case ElemSectionHeader:
			flush()
			if currentHeading == "" {
				currentHeading = el.Text
			}
			current.Elements = append(current.Elements, el)

		case ElemSubHeader:
			flush()
			current.Elements = append(current.Elements, el)

		case ElemPageBreak:
			if !multipage {
				flush()
			}

		case ElemTable:
			flush()
			current.Elements = append(current.Elements, el)
			flush()

		default:
			current.Elements = append(current.Elements, el)
		}
	}
	flush()
	return chunks
}

func combineSmallChunks(chunks []unstructPreChunk, combineUnder, softMax int) []unstructPreChunk {
	if len(chunks) <= 1 {
		return chunks
	}
	var result []unstructPreChunk
	i := 0
	for i < len(chunks) {
		current := chunks[i]
		for i+1 < len(chunks) && current.Chars < combineUnder {
			next := chunks[i+1]
			if current.Chars+next.Chars > softMax*2 {
				break
			}
			current.Elements = append(current.Elements, next.Elements...)
			if current.Heading == "" {
				current.Heading = next.Heading
			}
			current.Text = concatElements(current.Elements)
			current.Chars = len([]rune(current.Text))
			i++
		}
		result = append(result, current)
		i++
	}
	return result
}

func splitOversizedChunk(pc unstructPreChunk, cfg UnstructChunkConfig) []UnstructChunk {
	if pc.Chars <= cfg.MaxChars {
		return []UnstructChunk{{Heading: pc.Heading, Text: pc.Text, Elements: pc.Elements}}
	}

	var chunks []UnstructChunk
	remaining := pc.Text
	for len([]rune(remaining)) > cfg.MaxChars {
		splitPoint := findChunkSplitPoint(remaining, cfg.MaxChars)
		chunks = append(chunks, UnstructChunk{
			Heading: pc.Heading,
			Text:    strings.TrimSpace(remaining[:splitPoint]),
		})
		remaining = remaining[splitPoint:]
	}
	if strings.TrimSpace(remaining) != "" {
		chunks = append(chunks, UnstructChunk{
			Heading: pc.Heading,
			Text:    strings.TrimSpace(remaining),
		})
	}
	return chunks
}

func findChunkSplitPoint(text string, maxChars int) int {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return len(text) // byte len
	}
	minBound := maxChars * 8 / 10

	// toByteIdx converts rune index to byte index.
	toByteIdx := func(runeIdx int) int {
		return len(string(runes[:runeIdx]))
	}

	// Try sentence boundary first
	for i := maxChars; i >= minBound; i-- {
		if runes[i] == '.' || runes[i] == '。' || runes[i] == '!' || runes[i] == '！' || runes[i] == '?' || runes[i] == '？' {
			if i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\n') {
				return toByteIdx(i + 2)
			}
			return toByteIdx(i + 1)
		}
	}
	// Try newline
	for i := maxChars; i >= minBound; i-- {
		if runes[i] == '\n' {
			return toByteIdx(i + 1)
		}
	}
	// Try space
	for i := maxChars; i >= minBound; i-- {
		if runes[i] == ' ' {
			return toByteIdx(i + 1)
		}
	}
	return toByteIdx(maxChars)
}

func addChunkOverlap(chunks []UnstructChunk, overlap int) []UnstructChunk {
	for i := 1; i < len(chunks); i++ {
		prevRunes := []rune(chunks[i-1].Text)
		if len(prevRunes) <= overlap {
			continue
		}
		tail := string(prevRunes[len(prevRunes)-overlap:])
		chunks[i].Text = tail + "\n" + chunks[i].Text
	}
	return chunks
}

func concatElements(elements []ClassifiedElement) string {
	var parts []string
	for _, el := range elements {
		if el.Text != "" {
			parts = append(parts, el.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// UnstructChunksToMarkdown formats chunks as markdown with heading context.
func UnstructChunksToMarkdown(chunks []UnstructChunk) string {
	var out strings.Builder
	for i, c := range chunks {
		out.WriteString(fmt.Sprintf("\n--- chunk %d", i+1))
		if c.Heading != "" {
			heading := c.Heading
			if len([]rune(heading)) > 50 {
				heading = string([]rune(heading)[:50]) + "..."
			}
			out.WriteString(fmt.Sprintf(" [%s]", heading))
		}
		out.WriteString(" ---\n\n")
		if c.Heading != "" && !strings.HasPrefix(c.Text, c.Heading) {
			out.WriteString(fmt.Sprintf("**%s**\n\n", c.Heading))
		}
		out.WriteString(c.Text + "\n")
	}
	return out.String()
}
