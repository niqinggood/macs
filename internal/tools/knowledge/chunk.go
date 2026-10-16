package knowledge

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"mac/internal/ragmodel"
	"mac/internal/textutil"
)

type ChunkConfig struct {
	Size    int
	Overlap int
}

type ChunkSegment struct {
	Heading string // nearest heading above this chunk
	Content string
}

// ChunkConfigForFile returns the optimal chunk config based on file type.
// Code files get smaller chunks, long documents get larger chunks.
func ChunkConfigForFile(filename string) ChunkConfig {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".rs", ".java", ".c", ".cpp", ".h", ".cs":
		return ChunkConfig{Size: 400, Overlap: 40}
	case ".md", ".txt", ".csv":
		return ChunkConfig{Size: 600, Overlap: 60}
	case ".pdf", ".docx":
		return ChunkConfig{Size: 1000, Overlap: 100}
	case ".xlsx", ".html", ".htm":
		return ChunkConfig{Size: 800, Overlap: 80}
	default:
		return DefaultChunkConfig()
	}
}

func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{Size: 800, Overlap: 80}
}

// Chunk splits text and returns segments with heading context.
func Chunk(text string, cfg ChunkConfig) []ChunkSegment {
	if cfg.Size <= 0 {
		cfg = DefaultChunkConfig()
	}
	text = textutil.CleanRAGText(text)

	// Step 1: split by headings, tracking heading context
	sections := splitByHeadings(text)
	var allSegments []segmentWithHeading

	for _, sec := range sections {
		sentences := splitSentences(sec.text)
		for _, s := range sentences {
			if utf8.RuneCountInString(s) > cfg.Size {
				for _, sub := range splitLongSentence(s, cfg.Size) {
					allSegments = append(allSegments, segmentWithHeading{heading: sec.heading, text: sub})
				}
			} else {
				allSegments = append(allSegments, segmentWithHeading{heading: sec.heading, text: s})
			}
		}
	}

	// Step 2: semantic grouping by keyword overlap, then size enforcement
	// Falls back to size-based merging if semantic grouping produces poor results
	return mergeSemanticOrSize(allSegments, cfg)
}

type section struct {
	heading string
	text    string
}

type segmentWithHeading struct {
	heading string
	text    string
}

// splitByHeadings splits text at chapter/section boundaries, preserving heading info.
// Also detects code fences and tables to avoid splitting inside them.
func splitByHeadings(text string) []section {
	lines := strings.Split(text, "\n")
	var sections []section
	var currentHeading string
	var current strings.Builder

	inFence := false // inside ``` code block
	inTable := false // inside |...| table

	flush := func() {
		if current.Len() > 0 {
			sections = append(sections, section{heading: currentHeading, text: current.String()})
			current.Reset()
		}
		inTable = false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fences — never split inside them
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			current.WriteString(line)
			continue
		}

		if inFence {
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			current.WriteString(line)
			continue
		}

		// Track table rows — group them together
		isTableLine := strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
		isTableSep := strings.HasPrefix(trimmed, "|-") || strings.HasPrefix(trimmed, "|:") || strings.HasPrefix(trimmed, "| -")
		if isTableLine || isTableSep {
			if !inTable && current.Len() > 0 {
				flush()
			}
			inTable = true
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			current.WriteString(line)
			continue
		}

		if inTable && !isTableLine {
			flush()
		}

		// Paragraph boundary: blank line = strong split point
		if trimmed == "" {
			if current.Len() > 40 {
				flush()
				// heading preserved for next paragraph
			}
			if current.Len() > 0 {
				current.WriteString("\n")
			}
			current.WriteString(line)
			continue
		}

		// Heading detection — more patterns
		isHeading := strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "Chapter ") ||
			strings.HasPrefix(trimmed, "Part ") ||
			strings.HasPrefix(trimmed, "CHAPTER ") ||
			strings.HasPrefix(trimmed, "PART ") ||
			textutil.IsNumberedHeading(trimmed) ||
			textutil.IsChineseNumberedHeading(trimmed) ||
			strings.HasPrefix(trimmed, "Section ")

		if isHeading && !inTable {
			flush()
			h := cleanHeading(trimmed)
			if len([]rune(h)) > 120 {
				h = string([]rune(h)[:120])
			}
			currentHeading = h
		}

		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	flush()
	return sections
}

// isNumberedHeading checks for patterns like "1.", "1.1", "1. Title", "1.1 Title"
// cleanHeading strips markup and normalizes heading text.
func cleanHeading(line string) string {
	h := strings.TrimLeft(line, "# ")
	h = strings.TrimLeft(h, "0123456789. ")
	// Strip "、", "：" suffix commonly used in Chinese headings
	h = strings.TrimRight(h, "、：:")
	h = strings.TrimSpace(h)
	return h
}

// splitSentences splits text at sentence boundaries.
func splitSentences(text string) []string {
	lines := strings.Split(text, "\n")
	var sentences []string
	var current strings.Builder
	inFence := false

	for li, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fences — don't sentence-split inside them
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				// End of fence: flush the code block as one chunk
				if current.Len() > 0 {
					current.WriteString("\n")
				}
				current.WriteString(line)
				inFence = false
				s := strings.TrimSpace(current.String())
				if len(s) > 0 {
					sentences = append(sentences, s)
				}
				current.Reset()
				continue
			} else {
				// Start of fence: flush any pending text first
				s := strings.TrimSpace(current.String())
				if len(s) > 0 {
					sentences = append(sentences, s)
				}
				current.Reset()
				current.WriteString(line)
				inFence = true
				continue
			}
		}

		if inFence {
			current.WriteString("\n")
			current.WriteString(line)
			continue
		}

		// Normal sentence detection within this line
		runes := []rune(line)
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			current.WriteRune(r)

			end := false
			switch r {
			case '。', '！', '？', '；':
				end = true
			case '.', '!', '?':
				if i+1 < len(runes) {
					next := runes[i+1]
					if next == ' ' || next == '\n' || isCJK(next) || unicode.IsUpper(next) {
						if r == '.' && i > 0 && i+1 < len(runes) && isDigit(runes[i-1]) && isDigit(next) {
							continue
						}
						end = true
					}
				} else {
					end = true
				}
			}

			if end {
				s := strings.TrimSpace(current.String())
				if len(s) > 0 {
					sentences = append(sentences, s)
				}
				current.Reset()
			}
		}

		// Line boundary: add space between lines
		if li < len(lines)-1 {
			current.WriteRune(' ')
		}
	}

	s := strings.TrimSpace(current.String())
	if len(s) > 0 {
		sentences = append(sentences, s)
	}
	return sentences
}

// splitLongSentence splits an oversized sentence at word boundaries.
func splitLongSentence(text string, maxLen int) []string {
	runes := []rune(text)
	var result []string

	cjk := 0
	for _, r := range runes {
		if isCJK(r) {
			cjk++
		}
	}
	isCJKText := cjk > len(runes)/2

	for len(runes) > 0 {
		if len(runes) <= maxLen {
			result = append(result, string(runes))
			break
		}
		cut := maxLen
		if !isCJKText {
			for i := maxLen; i > maxLen/2; i-- {
				if runes[i] == ' ' {
					cut = i + 1
					break
				}
			}
		}
		result = append(result, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	return result
}

// mergeSemanticOrSize tries keyword-overlap grouping first.
// Falls back to size-based merging if semantic grouping creates too few or too many chunks.
func mergeSemanticOrSize(segments []segmentWithHeading, cfg ChunkConfig) []ChunkSegment {
	if len(segments) < 3 {
		return mergeChunksWithHeadings(segments, cfg.Size, cfg.Overlap)
	}

	// Build sentence list
	sentences := make([]string, len(segments))
	for i, s := range segments {
		sentences[i] = s.text
	}

	// Semantic grouping
	groups := groupByKeywordOverlap(sentences, 0.2) // 20% overlap threshold

	// If semantic grouping is reasonable (not all in one, not every sentence alone), use it
	avgGroupSize := float64(len(sentences)) / float64(len(groups))
	if avgGroupSize >= 2 && avgGroupSize <= 20 {
		// Build chunks from groups, using first segment's heading
		groupStart := 0
		var chunks []ChunkSegment
		for _, g := range groups {
			text := strings.Join(g, " ")
			heading := segments[groupStart].heading
			if utf8.RuneCountInString(text) > cfg.Size {
				// Oversized: split by max size
				for _, sub := range splitLongSentence(text, cfg.Size) {
					chunks = append(chunks, ChunkSegment{Heading: heading, Content: sub})
				}
			} else {
				chunks = append(chunks, ChunkSegment{Heading: heading, Content: text})
			}
			groupStart += len(g)
		}
		return chunks
	}

	// Fallback: size-based merging
	return mergeChunksWithHeadings(segments, cfg.Size, cfg.Overlap)
}

// alignToWordStart backs up from cutPos in runes to the nearest word boundary.
// Word boundaries are: space, CJK punctuation, or ASCII sentence punctuation.
// Returns the adjusted position (always <= cutPos).
func alignToWordStart(runes []rune, cutPos int) int {
	if cutPos <= 0 || cutPos > len(runes) {
		return 0
	}
	for i := cutPos - 1; i >= 0; i-- {
		r := runes[i]
		if isWordBoundary(r) {
			return i + 1
		}
	}
	return 0
}

// isWordBoundary returns true if r is a word separator character.
func isWordBoundary(r rune) bool {
	switch r {
	case ' ', '\n', '\t', '.', '!', '?', ',', ';', ':':
		return true
	default:
		return false
	}
}

// mergeChunksWithHeadings combines segments into target-sized chunks with overlap.
// Uses the first segment's heading as the chunk's heading.
func mergeChunksWithHeadings(segments []segmentWithHeading, size, overlap int) []ChunkSegment {
	var chunks []ChunkSegment
	var currentSegs []segmentWithHeading
	curLen := 0

	flush := func() {
		if len(currentSegs) > 0 {
			var contents []string
			heading := currentSegs[0].heading
			for _, s := range currentSegs {
				contents = append(contents, s.text)
			}
			chunks = append(chunks, ChunkSegment{
				Heading: heading,
				Content: strings.TrimSpace(strings.Join(contents, " ")),
			})
			currentSegs = nil
			curLen = 0
		}
	}

	var lastHeading string
	for _, seg := range segments {
		// Flush on chapter/section boundary change (heading change = new chapter)
		if lastHeading != "" && seg.heading != lastHeading && curLen > 40 {
			flush()
		}
		lastHeading = seg.heading
		segLen := utf8.RuneCountInString(seg.text)
		if curLen+segLen > size && curLen > 0 {
			flush()
			if overlap > 0 && len(chunks) > 0 {
				prev := chunks[len(chunks)-1]
				prevRunes := []rune(prev.Content)
				if len(prevRunes) > overlap {
					// Align overlap to word boundary instead of hard rune cut
					cutPos := len(prevRunes) - overlap
					cutPos = alignToWordStart(prevRunes, cutPos)
					overlapText := string(prevRunes[cutPos:])
					currentSegs = []segmentWithHeading{{heading: seg.heading, text: overlapText}}
					curLen = utf8.RuneCountInString(overlapText)
				}
			}
		}
		currentSegs = append(currentSegs, seg)
		curLen += segLen
	}
	flush()

	// Merge short final chunk into previous
	if len(chunks) >= 2 && utf8.RuneCountInString(chunks[len(chunks)-1].Content) < 80 {
		chunks[len(chunks)-2].Content = chunks[len(chunks)-2].Content + "\n" + chunks[len(chunks)-1].Content
		chunks = chunks[:len(chunks)-1]
	}
	return chunks
}

func isCJK(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// ====================== Semantic Chunking (inspired by fogfish/scanner) ======================
// Uses keyword overlap between adjacent sentences as a proxy for semantic similarity,
// avoiding the need for an embedding API.

// ChunkSemantic groups sentences by keyword overlap, creating topic-coherent chunks.
// Falls back to size-based merging for oversized segments.
func ChunkSemantic(text string, cfg ChunkConfig) []ChunkSegment {
	if cfg.Size <= 0 {
		cfg = DefaultChunkConfig()
	}
	text = textutil.CleanRAGText(text)

	// Step 1: split by headings first (structural boundaries are strongest)
	sections := splitByHeadings(text)

	var allChunks []ChunkSegment
	for _, sec := range sections {
		// Step 2: split into sentences
		sentences := splitSentences(sec.text)
		if len(sentences) == 0 {
			continue
		}

		// Step 3: merge sentences by keyword overlap (semantic grouping)
		groups := groupByKeywordOverlap(sentences, 0.25) // 25% overlap threshold

		// Step 4: enforce size limits on groups
		for _, g := range groups {
			text := strings.Join(g, " ")
			if utf8.RuneCountInString(text) <= cfg.Size {
				allChunks = append(allChunks, ChunkSegment{Heading: sec.heading, Content: text})
			} else {
				// Oversized group: split by size
				for _, sub := range splitLongSentence(text, cfg.Size) {
					allChunks = append(allChunks, ChunkSegment{Heading: sec.heading, Content: sub})
				}
			}
		}
	}

	// Step 5: enforce overlap for continuity
	if cfg.Overlap > 0 && len(allChunks) > 1 {
		return addOverlap(allChunks, cfg.Overlap)
	}

	// Merge short trailing chunk
	if len(allChunks) >= 2 && utf8.RuneCountInString(allChunks[len(allChunks)-1].Content) < 80 {
		allChunks[len(allChunks)-2].Content += "\n" + allChunks[len(allChunks)-1].Content
		allChunks = allChunks[:len(allChunks)-1]
	}

	return allChunks
}

// groupByKeywordOverlap groups sentences where adjacent sentences share keywords.
// threshold: minimum fraction of shared keywords to stay in same group.
func groupByKeywordOverlap(sentences []string, threshold float64) [][]string {
	if len(sentences) == 0 {
		return nil
	}

	// Pre-compute keyword sets for each sentence
	type sentKW struct {
		text string
		kws  map[string]bool
	}
	items := make([]sentKW, len(sentences))
	for i, s := range sentences {
		items[i] = sentKW{text: s, kws: keywordSet(s)}
	}

	var groups [][]string
	current := []string{items[0].text}
	currentKW := items[0].kws

	for i := 1; i < len(items); i++ {
		overlap := keywordOverlap(currentKW, items[i].kws)

		if overlap >= threshold {
			// Same topic: merge in
			current = append(current, items[i].text)
			// Enrich current keyword set
			for k := range items[i].kws {
				currentKW[k] = true
			}
		} else {
			// Topic shift: flush current group, start new one
			groups = append(groups, current)
			current = []string{items[i].text}
			currentKW = items[i].kws
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// keywordSet extracts meaningful keywords from a sentence.
func keywordSet(text string) map[string]bool {
	kws := map[string]bool{}
	words := ragmodel.GoJiebaCut(text)
	for _, w := range words {
		w = strings.TrimSpace(w)
		if len([]rune(w)) < 2 || ragmodel.IsStopToken(w) || ragmodel.IsPurePunctuation(w) {
			continue
		}
		kws[w] = true
	}
	return kws
}

// keywordOverlap returns Jaccard-like overlap between two keyword sets.
func keywordOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if b[k] {
			shared++
		}
	}
	// Use the smaller set as denominator (like scanner's approach)
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	if minLen == 0 {
		return 0
	}
	return float64(shared) / float64(minLen)
}

// addOverlap ensures continuity between consecutive chunks.
func addOverlap(chunks []ChunkSegment, overlap int) []ChunkSegment {
	result := make([]ChunkSegment, len(chunks))
	copy(result, chunks)
	for i := 0; i < len(result)-1; i++ {
		prev := []rune(result[i].Content)
		next := []rune(result[i+1].Content)
		if len(prev) > overlap && len(next) > overlap/2 {
			// Prepend end of previous chunk to next chunk for continuity (word-aligned)
			cutPos := len(prev) - overlap
			cutPos = alignToWordStart(prev, cutPos)
			overlapText := string(prev[cutPos:])
			result[i+1].Content = overlapText + " " + result[i+1].Content
		}
	}
	return result
}
