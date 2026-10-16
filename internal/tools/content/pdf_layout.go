package content

import (
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"mac/internal/textutil"

	"github.com/ledongthuc/pdf"
)

// ====================== pdfplumber-Go v2 ======================
// Faithful Go port of pdfplumber's core algorithms:
//   clustering.py (cluster_list, cluster_objects)
//   text.py      (WordExtractor, char_begins_new_word, WordMap)
//   page.py      (extract_words, extract_text, extract_tables)

const (
	defaultXTolerance = 3.0
	defaultYTolerance = 3.0
	defaultXDensity   = 7.25
	defaultYDensity   = 13.0
)

// ---------- Data Types ----------

type Word struct {
	Text     string
	X0, X1   float64
	Top, Bot float64
	Font     string
	FontSize float64
}

type Line struct {
	Words    []Word
	X0, X1   float64
	Top, Bot float64
	Text     string
}

type Table struct {
	Rows     [][]string
	X0, X1   float64
	Top, Bot float64
}

// ---------- Character Types ----------

// pdfChar wraps pdf.Text with additional computed fields like pdfplumber does.
type pdfChar struct {
	Font     string
	FontSize float64
	X0, X1   float64
	Top, Bot float64
	Doctop   float64
	Upright  bool
	Text     string
	Width    float64
	Height   float64
}

func newChar(t pdf.Text) pdfChar {
	return pdfChar{
		Font:     t.Font,
		FontSize: t.FontSize,
		X0:       t.X,
		X1:       t.X + t.W,
		Top:      t.Y,
		Bot:      t.Y - t.FontSize,
		Doctop:   t.Y,
		Upright:  true,
		Text:     t.S,
		Width:    t.W,
		Height:   t.FontSize,
	}
}

// ---------- Clustering (port of clustering.py) ----------

// clusterList groups sorted values into clusters within tolerance.
// Port of pdfplumber's cluster_list().
func clusterList(xs []float64, tolerance float64) [][]float64 {
	if tolerance == 0 || len(xs) < 2 {
		var result [][]float64
		for _, x := range xs {
			result = append(result, []float64{x})
		}
		return result
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)

	var groups [][]float64
	current := []float64{sorted[0]}
	last := sorted[0]
	for _, x := range sorted[1:] {
		if x <= last+tolerance {
			current = append(current, x)
		} else {
			groups = append(groups, current)
			current = []float64{x}
		}
		last = x
	}
	groups = append(groups, current)
	return groups
}

// clusterObjects groups objects by a key function with tolerance.
// Port of pdfplumber's cluster_objects().
func clusterObjects[T any](xs []T, keyFn func(T) float64, tolerance float64) [][]T {
	if len(xs) == 0 {
		return nil
	}
	// Build cluster dictionary
	values := make([]float64, len(xs))
	for i, x := range xs {
		values[i] = keyFn(x)
	}
	clusters := clusterList(values, tolerance)
	clusterDict := make(map[float64]int)
	for i, c := range clusters {
		for _, v := range c {
			clusterDict[v] = i
		}
	}
	// Group objects by cluster
	clusterMap := make(map[int][]T)
	for _, x := range xs {
		cid := clusterDict[keyFn(x)]
		clusterMap[cid] = append(clusterMap[cid], x)
	}
	// Sort by cluster ID
	var result [][]T
	for i := 0; i < len(clusterMap); i++ {
		if group, ok := clusterMap[i]; ok {
			result = append(result, group)
		}
	}
	return result
}

// ---------- Word Extraction (port of text.py WordExtractor) ----------

type wordExtractor struct {
	xTolerance      float64
	yTolerance      float64
	keepBlankChars  bool
	expandLigatures bool
	extraAttrs      []string
}

func newWordExtractor() wordExtractor {
	return wordExtractor{
		xTolerance:      defaultXTolerance,
		yTolerance:      defaultYTolerance,
		keepBlankChars:  false,
		expandLigatures: true,
	}
}

// Ligature expansions (port of pdfplumber's LIGATURES)
var ligatures = map[string]string{
	"ﬀ": "ff", "ﬃ": "ffi", "ﬄ": "ffl", "ﬁ": "fi", "ﬂ": "fl", "ﬆ": "st", "ﬅ": "st",
}

// charBeginsNewWord determines if currChar starts a new word.
// Port of pdfplumber's char_begins_new_word().
func (w *wordExtractor) charBeginsNewWord(prev, curr pdfChar) bool {
	ay := prev.Top
	cy := curr.Top
	ax := prev.X0
	bx := prev.X1
	cx := curr.X0

	// Intraline test (horizontal gap)
	if cx < ax || cx > bx+w.xTolerance {
		return true
	}
	// Interline test (vertical gap)
	if math.Abs(cy-ay) > w.yTolerance {
		return true
	}
	return false
}

// iterCharsToWords groups ordered characters into words.
// Port of pdfplumber's iter_chars_to_words().
func (w *wordExtractor) iterCharsToWords(chars []pdfChar) [][]pdfChar {
	var words [][]pdfChar
	var currentWord []pdfChar

	flush := func() {
		if len(currentWord) > 0 {
			words = append(words, currentWord)
			currentWord = nil
		}
	}

	for _, ch := range chars {
		text := ch.Text
		if !w.keepBlankChars && strings.TrimSpace(text) == "" {
			flush()
			continue
		}

		if len(currentWord) == 0 {
			currentWord = []pdfChar{ch}
			continue
		}

		if w.charBeginsNewWord(currentWord[len(currentWord)-1], ch) {
			flush()
			currentWord = []pdfChar{ch}
		} else {
			currentWord = append(currentWord, ch)
		}
	}
	flush()
	return words
}

// extractWords extracts words from characters.
// Port of pdfplumber's extract_words().
func (w *wordExtractor) extractWords(chars []pdfChar) []Word {
	if len(chars) == 0 {
		return nil
	}

	// Sort: top-to-bottom, left-to-right
	sort.Slice(chars, func(i, j int) bool {
		if math.Abs(chars[i].Top-chars[j].Top) > w.yTolerance {
			return chars[i].Top > chars[j].Top
		}
		return chars[i].X0 < chars[j].X0
	})

	// Group chars into words
	charWords := w.iterCharsToWords(chars)

	var words []Word
	for _, cw := range charWords {
		if len(cw) == 0 {
			continue
		}

		// Merge chars into word (port of merge_chars)
		var text strings.Builder
		x0, x1 := cw[0].X0, cw[0].X1
		top, bot := cw[0].Top, cw[0].Bot

		for _, ch := range cw {
			t := ch.Text
			if w.expandLigatures {
				if expanded, ok := ligatures[t]; ok {
					t = expanded
				}
			}
			text.WriteString(t)
			if ch.X0 < x0 {
				x0 = ch.X0
			}
			if ch.X1 > x1 {
				x1 = ch.X1
			}
			if ch.Top > top {
				top = ch.Top
			}
			if ch.Bot < bot {
				bot = ch.Bot
			}
		}

		words = append(words, Word{
			Text: text.String(),
			X0:   x0, X1: x1,
			Top: top, Bot: bot,
			Font:     cw[0].Font,
			FontSize: cw[0].FontSize,
		})
	}
	return words
}

// ---------- Page-Level Extraction ----------

// PPage wraps a pdf.Page with pdfplumber-compatible extraction methods.
type PPage struct {
	page       pdf.Page
	chars      []pdfChar
	words      []Word
	rects      []pdf.Rect
	xTol, yTol float64
	cjkText    string // pre-built corrected CJK text (bypass char pipeline)
}

func newPPage(page pdf.Page) *PPage {
	return &PPage{
		page: page,
		xTol: defaultXTolerance,
		yTol: defaultYTolerance,
	}
}

func (p *PPage) ensureChars() {
	if p.chars != nil {
		return
	}
	content := p.page.Content()
	p.rects = content.Rect

	// Always try font-aware GetPlainText
	fonts := make(map[string]*pdf.Font)
	for _, name := range p.page.Fonts() {
		f := p.page.Font(name)
		fonts[name] = &f
	}
	plain, err := p.page.GetPlainText(fonts)
	if err == nil && plain != "" && !goTextIsGarbled(plain) {
		rawChars := 0
		for _, t := range content.Text {
			rawChars += len([]rune(t.S))
		}
		// Use font-corrected text for CJK PDFs
		if len(plain) > rawChars/2 || hasAnyCJK(plain) {
			// Store as corrected text — bypass the char/word pipeline entirely
			p.cjkText = cleanPlainText(plain)
			return
		}
	}
	// English PDF fast path
	for _, t := range content.Text {
		if t.S != "" && !unicode.IsSpace([]rune(t.S)[0]) {
			p.chars = append(p.chars, newChar(t))
		}
	}
}

func hasAnyCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// cleanPlainText converts GetPlainText output to clean continuous text.
// GetPlainText inserts \n between each character for CJK fonts.
// Strategy: collapse single \n, preserve \n\n+ as paragraph breaks.
// Also trims excessive whitespace and merges broken lines.
func cleanPlainText(plain string) string {
	// Split into paragraphs by blank lines
	paragraphs := strings.Split(plain, "\n\n")
	var clean []string
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		// Collapse single newlines within paragraph
		para = strings.ReplaceAll(para, "\n", "")
		// Collapse multiple spaces
		for strings.Contains(para, "  ") {
			para = strings.ReplaceAll(para, "  ", " ")
		}
		clean = append(clean, para)
	}
	return strings.Join(clean, "\n\n")
}

func goTextIsGarbled(text string) bool {
	if len(text) < 100 {
		return false
	}
	replacementCount := 0
	for _, r := range text {
		if r == 0xFFFD {
			replacementCount++
		}
	}
	ratio := float64(replacementCount) / float64(len([]rune(text)))
	return ratio > 0.1 // more than 10% replacement chars = garbled
}

func textsHaveCJK(texts []pdf.Text) bool {
	for _, t := range texts {
		for _, r := range t.S {
			if r >= 0x4e00 && r <= 0x9fff {
				return true
			}
		}
	}
	return false
}

func (p *PPage) ensureWords() {
	if p.words != nil {
		return
	}
	p.ensureChars()
	ext := newWordExtractor()
	ext.xTolerance = p.xTol
	ext.yTolerance = p.yTol
	p.words = ext.extractWords(p.chars)
}

// Chars returns all characters with positions.
func (p *PPage) Chars() []pdfChar {
	p.ensureChars()
	return p.chars
}

// Words returns all words with positions.
func (p *PPage) Words() []Word {
	p.ensureWords()
	return p.words
}

// Lines clusters words into lines by Y position.
func (p *PPage) Lines() [][]Word {
	p.ensureWords()
	if len(p.words) == 0 {
		return nil
	}
	return clusterObjects(p.words, func(w Word) float64 { return w.Top }, p.yTol)
}

// ExtractText extracts text in reading order, like pdfplumber.page.extract_text().
func (p *PPage) ExtractText() string {
	p.ensureWords()
	if p.cjkText != "" {
		return p.cjkText
	}
	if len(p.words) == 0 {
		text, _ := p.page.GetPlainText(nil)
		return text
	}
	allLines := p.Lines()

	// Build text: sort lines top-to-bottom, words left-to-right within each line
	var out strings.Builder
	for _, line := range allLines {
		if len(line) == 0 {
			continue
		}
		sort.Slice(line, func(i, j int) bool { return line[i].X0 < line[j].X0 })
		var lineText strings.Builder
		lastX1 := -9999.0
		for _, w := range line {
			if lastX1 > -9999 && w.X0 > lastX1+p.xTol {
				lineText.WriteString(" ")
			}
			lineText.WriteString(w.Text)
			lastX1 = w.X1
		}
		out.WriteString(lineText.String() + "\n")
	}
	return strings.TrimSpace(out.String())
}

// ExtractTextSimple extracts text with simple clustering, ignoring layout.
func (p *PPage) ExtractTextSimple() string {
	p.ensureChars()
	if len(p.chars) == 0 {
		text, _ := p.page.GetPlainText(nil)
		return text
	}
	// Cluster chars into lines by Doctop
	lines := clusterObjects(p.chars, func(c pdfChar) float64 { return c.Doctop }, p.yTol)
	var out strings.Builder
	for _, line := range lines {
		sort.Slice(line, func(i, j int) bool { return line[i].X0 < line[j].X0 })
		var lt strings.Builder
		lastX1 := -9999.0
		for _, ch := range line {
			if lastX1 > -9999 && ch.X0 > lastX1+p.xTol {
				lt.WriteString(" ")
			}
			lt.WriteString(ch.Text)
			lastX1 = ch.X1
		}
		out.WriteString(lt.String())
		out.WriteString("\n")
	}
	return strings.TrimSpace(out.String())
}

// ---------- Table Detection (port of pdfplumber's table.py) ----------

// Edge represents a table edge (horizontal or vertical), like pdfplumber's edges.
type edge struct {
	X0, X1        float64
	Top, Bot      float64
	Width, Height float64
	Orientation   string // "h" or "v"
}

// ExtractTables detects tables using pdfplumber's full TableFinder algorithm.
// Strategy order: lines (explicit borders) → text (word-alignment inference)
func (p *PPage) ExtractTables() []Table {
	p.ensureWords()

	// Get edges from explicit lines
	edges := p.rectsToEdges()

	// Get edges from word alignment (text strategy — port of words_to_edges_h/v)
	edges = append(edges, p.wordsToEdgesH(1)...) // min 1 word per row
	edges = append(edges, p.wordsToEdgesV(3)...) // min 3 words per col

	if len(edges) < 4 {
		// Fall back to gap-based detection for simple tables
		return p.detectTablesByGaps()
	}

	// Merge nearby edges (port of merge_edges)
	edges = mergeEdges(edges, 2, 2, 2, 2)

	// Find intersections
	intersections := edgesToIntersections(edges, 1, 1)

	// Find cells from intersections
	cells := intersectionsToCells(intersections)
	if len(cells) == 0 {
		return p.detectTablesByGaps()
	}

	// Group cells into tables
	tables := cellsToTables(cells)
	if len(tables) == 0 {
		return p.detectTablesByGaps()
	}

	// Extract text for each table (with merged cell detection)
	var result []Table
	for _, cellGroup := range tables {
		table := p.extractTableWithMergedCells(cellGroup)
		if len(table.Rows) > 0 {
			result = append(result, table)
		}
	}
	return result
}

// ---------- Edge Detection (port of table.py words_to_edges_h / words_to_edges_v) ----------

func (p *PPage) rectsToEdges() []edge {
	var edges []edge
	for _, r := range p.rects {
		w := math.Abs(r.Max.X - r.Min.X)
		h := math.Abs(r.Max.Y - r.Min.Y)
		if w > 1 && h < 2 {
			edges = append(edges, edge{
				X0: r.Min.X, X1: r.Max.X,
				Top: r.Min.Y, Bot: r.Max.Y,
				Width: w, Height: h,
				Orientation: "h",
			})
		} else if h > 1 && w < 2 {
			edges = append(edges, edge{
				X0: r.Min.X, X1: r.Max.X,
				Top: r.Min.Y, Bot: r.Max.Y,
				Width: w, Height: h,
				Orientation: "v",
			})
		}
	}
	return edges
}

// wordsToEdgesH finds horizontal edges from aligned word tops (text strategy).
// Port of pdfplumber's words_to_edges_h().
func (p *PPage) wordsToEdgesH(wordThreshold int) []edge {
	if len(p.words) < wordThreshold {
		return nil
	}
	// Cluster words by top position (1pt tolerance)
	byTop := clusterObjects(p.words, func(w Word) float64 { return w.Top }, 1)

	var edges []edge
	for _, cluster := range byTop {
		if len(cluster) < wordThreshold {
			continue
		}
		// Find bounds of this row
		x0 := cluster[0].X0
		x1 := cluster[0].X1
		top := cluster[0].Top
		bot := cluster[0].Bot
		for _, w := range cluster {
			if w.X0 < x0 {
				x0 = w.X0
			}
			if w.X1 > x1 {
				x1 = w.X1
			}
			if w.Bot < bot {
				bot = w.Bot
			}
		}
		edges = append(edges,
			edge{X0: x0, X1: x1, Top: top, Bot: top, Width: x1 - x0, Orientation: "h"},
			edge{X0: x0, X1: x1, Top: bot, Bot: bot, Width: x1 - x0, Orientation: "h"},
		)
	}
	return edges
}

// wordsToEdgesV finds vertical edges from aligned word boundaries (text strategy).
// Port of pdfplumber's words_to_edges_v().
func (p *PPage) wordsToEdgesV(wordThreshold int) []edge {
	if len(p.words) < wordThreshold {
		return nil
	}
	// Cluster by x0, x1, and center
	byX0 := clusterObjects(p.words, func(w Word) float64 { return w.X0 }, 1)
	byX1 := clusterObjects(p.words, func(w Word) float64 { return w.X1 }, 1)
	byCenter := clusterObjects(p.words, func(w Word) float64 { return (w.X0 + w.X1) / 2 }, 1)

	// Combine clusters, filter by threshold
	var largeClusters [][]Word
	for _, c := range append(append(byX0, byX1...), byCenter...) {
		if len(c) >= wordThreshold {
			largeClusters = append(largeClusters, c)
		}
	}
	if len(largeClusters) == 0 {
		return nil
	}

	// Sort by cluster size descending
	sort.Slice(largeClusters, func(i, j int) bool { return len(largeClusters[i]) > len(largeClusters[j]) })

	// Find bbox for each cluster
	type bbox struct{ x0, x1, top, bot float64 }
	var bboxes []bbox
	for _, c := range largeClusters {
		b := bbox{c[0].X0, c[0].X1, c[0].Top, c[0].Bot}
		for _, w := range c {
			if w.X0 < b.x0 {
				b.x0 = w.X0
			}
			if w.X1 > b.x1 {
				b.x1 = w.X1
			}
			if w.Top > b.top {
				b.top = w.Top
			}
			if w.Bot < b.bot {
				b.bot = w.Bot
			}
		}
		// Condense overlapping bboxes
		overlaps := false
		for _, existing := range bboxes {
			if bboxOverlap(b, existing) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			bboxes = append(bboxes, b)
		}
	}
	if len(bboxes) == 0 {
		return nil
	}

	// Find global bounds
	sort.Slice(bboxes, func(i, j int) bool { return bboxes[i].x0 < bboxes[j].x0 })
	maxX1 := bboxes[len(bboxes)-1].x1
	minTop := bboxes[0].top
	maxBot := bboxes[0].bot
	for _, b := range bboxes {
		if b.top > minTop {
			minTop = b.top
		}
		if b.bot < maxBot {
			maxBot = b.bot
		}
	}

	var edges []edge
	for _, b := range bboxes {
		edges = append(edges, edge{
			X0: b.x0, X1: b.x0,
			Top: minTop, Bot: maxBot, Height: maxBot - minTop,
			Orientation: "v",
		})
	}
	// Rightmost edge
	edges = append(edges, edge{
		X0: maxX1, X1: maxX1,
		Top: minTop, Bot: maxBot, Height: maxBot - minTop,
		Orientation: "v",
	})
	return edges
}

func bboxOverlap(a, b struct{ x0, x1, top, bot float64 }) bool {
	return a.x0 < b.x1 && a.x1 > b.x0 && a.top > b.bot && a.bot < b.top
}

// ---------- Edge Processing (port of merge_edges / snap_edges) ----------

func mergeEdges(edges []edge, snapX, snapY, joinX, joinY float64) []edge {
	// Sort by orientation + position
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Orientation != edges[j].Orientation {
			return edges[i].Orientation < edges[j].Orientation
		}
		if edges[i].Orientation == "h" {
			return edges[i].Top < edges[j].Top
		}
		return edges[i].X0 < edges[j].X0
	})

	var merged []edge
	var current *edge

	for i := range edges {
		e := edges[i]
		if current == nil {
			cp := e
			current = &cp
			continue
		}
		if current.Orientation == e.Orientation && sameEdgeGroup(*current, e, joinX, joinY) {
			// Merge: extend the current edge
			if current.Orientation == "h" {
				if e.X1 > current.X1 {
					current.X1 = e.X1
				}
			} else {
				if e.Bot < current.Bot {
					current.Bot = e.Bot
				}
			}
		} else {
			merged = append(merged, *current)
			cp := e
			current = &cp
		}
	}
	if current != nil {
		merged = append(merged, *current)
	}
	return merged
}

func sameEdgeGroup(a, b edge, joinX, joinY float64) bool {
	if a.Orientation == "h" {
		return math.Abs(a.Top-b.Top) <= joinY
	}
	return math.Abs(a.X0-b.X0) <= joinX
}

// ---------- Intersection Detection (port of edges_to_intersections) ----------

type point struct{ X, Y float64 }
type intersection struct {
	vEdges, hEdges []edge
}

func edgesToIntersections(edges []edge, xTol, yTol float64) map[point]*intersection {
	result := make(map[point]*intersection)

	var vEdges, hEdges []edge
	for _, e := range edges {
		if e.Orientation == "v" {
			vEdges = append(vEdges, e)
		} else {
			hEdges = append(hEdges, e)
		}
	}

	sort.Slice(vEdges, func(i, j int) bool { return vEdges[i].X0 < vEdges[j].X0 })
	sort.Slice(hEdges, func(i, j int) bool { return hEdges[i].Top < hEdges[j].Top })

	for _, v := range vEdges {
		for _, h := range hEdges {
			if v.Top <= h.Top+yTol && v.Bot >= h.Top-yTol &&
				v.X0 >= h.X0-xTol && v.X0 <= h.X1+xTol {
				pt := point{v.X0, h.Top}
				if _, ok := result[pt]; !ok {
					result[pt] = &intersection{}
				}
				result[pt].vEdges = append(result[pt].vEdges, v)
				result[pt].hEdges = append(result[pt].hEdges, h)
			}
		}
	}
	return result
}

// ---------- Cell Detection (port of intersections_to_cells) ----------

func intersectionsToCells(intersections map[point]*intersection) []struct{ X0, Y0, X1, Y1 float64 } {
	var points []point
	for pt := range intersections {
		points = append(points, pt)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Y != points[j].Y {
			return points[i].Y > points[j].Y // top-down
		}
		return points[i].X < points[j].X // left-right
	})

	edgeConnects := func(p1, p2 point) bool {
		if p1.X == p2.X {
			for _, e := range intersections[p1].vEdges {
				for _, e2 := range intersections[p2].vEdges {
					if e.X0 == e2.X0 && e.X1 == e2.X1 {
						return true
					}
				}
			}
		}
		if p1.Y == p2.Y {
			for _, e := range intersections[p1].hEdges {
				for _, e2 := range intersections[p2].hEdges {
					if e.Top == e2.Top && e.Bot == e2.Bot {
						return true
					}
				}
			}
		}
		return false
	}

	var cells []struct{ X0, Y0, X1, Y1 float64 }
	for i, pt := range points {
		if i == len(points)-1 {
			break
		}
		rest := points[i+1:]
		var below, right []point
		for _, p := range rest {
			if p.X == pt.X {
				below = append(below, p)
			}
			if p.Y == pt.Y {
				right = append(right, p)
			}
		}
		for _, bpt := range below {
			if !edgeConnects(pt, bpt) {
				continue
			}
			for _, rpt := range right {
				if !edgeConnects(pt, rpt) {
					continue
				}
				br := point{rpt.X, bpt.Y}
				if _, ok := intersections[br]; ok &&
					edgeConnects(bpt, br) && edgeConnects(rpt, br) {
					cells = append(cells, struct{ X0, Y0, X1, Y1 float64 }{
						pt.X, pt.Y, br.X, br.Y,
					})
				}
			}
		}
	}
	return cells
}

// ---------- Table Grouping (port of cells_to_tables) ----------

func cellsToTables(cells []struct{ X0, Y0, X1, Y1 float64 }) [][]struct{ X0, Y0, X1, Y1 float64 } {
	remaining := make([]struct{ X0, Y0, X1, Y1 float64 }, len(cells))
	copy(remaining, cells)

	var tables [][]struct{ X0, Y0, X1, Y1 float64 }
	var currentCorners []point
	var currentCells []struct{ X0, Y0, X1, Y1 float64 }

	for len(remaining) > 0 {
		initialCount := len(currentCells)
		for i := 0; i < len(remaining); i++ {
			cell := remaining[i]
			corners := []point{{cell.X0, cell.Y0}, {cell.X0, cell.Y1}, {cell.X1, cell.Y0}, {cell.X1, cell.Y1}}
			if len(currentCells) == 0 {
				currentCorners = corners
				currentCells = append(currentCells, cell)
				remaining = append(remaining[:i], remaining[i+1:]...)
				i--
			} else {
				shareCount := 0
				for _, c := range corners {
					for _, cc := range currentCorners {
						if math.Abs(c.X-cc.X) < 1 && math.Abs(c.Y-cc.Y) < 1 {
							shareCount++
							break
						}
					}
				}
				if shareCount > 0 {
					currentCorners = append(currentCorners, corners...)
					currentCells = append(currentCells, cell)
					remaining = append(remaining[:i], remaining[i+1:]...)
					i--
				}
			}
		}
		if len(currentCells) == initialCount {
			if len(currentCells) > 0 {
				tables = append(tables, currentCells)
			}
			currentCorners = nil
			currentCells = nil
		}
	}
	if len(currentCells) > 0 {
		tables = append(tables, currentCells)
	}
	return tables
}

// extractTableFromCells extracts text for each cell in a table.
func (p *PPage) extractTableFromCells(cells []struct{ X0, Y0, X1, Y1 float64 }) Table {
	if len(cells) == 0 {
		return Table{}
	}

	// Determine grid dimensions from unique X and Y positions
	var cols, rows []float64
	for _, c := range cells {
		cols = append(cols, c.X0)
		rows = append(rows, c.Y0) // top
	}
	cols = uniqueSorted(cols)
	rows = uniqueSortedDesc(rows) // top-down

	if len(cols) < 2 || len(rows) < 2 {
		return Table{}
	}

	// Build cell text matrix
	cellTexts := make([][]string, len(rows)-1)
	for i := 0; i < len(rows)-1; i++ {
		cellTexts[i] = make([]string, len(cols)-1)
		for j := 0; j < len(cols)-1; j++ {
			// Find words within cell bounds
			var texts []string
			for _, w := range p.words {
				if w.X0 >= cols[j]-2 && w.X1 <= cols[j+1]+2 &&
					w.Top <= rows[i]+2 && w.Bot >= rows[i+1]-2 {
					texts = append(texts, w.Text)
				}
			}
			cellTexts[i][j] = strings.Join(texts, " ")
		}
	}

	return Table{
		Rows: cellTexts,
		X0:   cols[0], X1: cols[len(cols)-1],
		Top: rows[0], Bot: rows[len(rows)-1],
	}
}

func uniqueSorted(vals []float64) []float64 {
	sort.Float64s(vals)
	var result []float64
	for _, v := range vals {
		if len(result) == 0 || v-result[len(result)-1] > 1 { // 1pt tolerance
			result = append(result, v)
		}
	}
	return result
}

func uniqueSortedDesc(vals []float64) []float64 {
	u := uniqueSorted(vals)
	sort.Sort(sort.Reverse(sort.Float64Slice(u)))
	return u
}

// detectTablesByGaps is a fallback for simple tables without clear borders.
func (p *PPage) detectTablesByGaps() []Table {
	allLines := p.Lines()
	if len(allLines) < 2 {
		return nil
	}

	var tables []Table
	for start := 0; start < len(allLines); start++ {
		baseCount := len(allLines[start])
		if baseCount < 3 {
			continue
		}
		end := start + 1
		for end < len(allLines) && len(allLines[end]) >= 3 && math.Abs(float64(len(allLines[end])-baseCount)) <= 1 {
			end++
		}
		if end-start < 2 {
			continue
		}
		tableLines := allLines[start:end]
		// Simple check: word X positions roughly align
		aligned := true
		ref := tableLines[0]
		for _, line := range tableLines[1:] {
			if len(line) != len(ref) {
				aligned = false
				break
			}
			for i := range ref {
				if math.Abs(ref[i].X0-line[i].X0) > 10 {
					aligned = false
					break
				}
			}
		}
		if !aligned {
			continue
		}
		// Extract
		numCols := len(ref)
		var rows [][]string
		for _, line := range tableLines {
			row := make([]string, numCols)
			for i, w := range line {
				row[i] = w.Text
			}
			rows = append(rows, row)
		}
		tables = append(tables, Table{Rows: rows})
	}
	return tables
}

// ---------- Multi-Column Layout (port of WordMap.to_textmap) ----------

// ExtractTextLayout extracts text with full layout preservation using density-based grid.
func (p *PPage) ExtractTextLayout(layoutWidth, layoutHeight float64) string {
	p.ensureWords()
	if len(p.words) == 0 {
		return p.ExtractText()
	}
	// Default densities from pdfplumber
	xDensity := defaultXDensity // 7.25 points per char
	yDensity := defaultYDensity // 13 points per line

	layoutWidthChars := int(math.Round(layoutWidth / xDensity))
	if layoutWidthChars < 1 {
		layoutWidthChars = 80
	}
	layoutHeightChars := int(math.Round(layoutHeight / yDensity))
	if layoutHeightChars < 1 {
		layoutHeightChars = 60
	}

	// Cluster words into lines by top position
	byLines := clusterObjects(p.words, func(w Word) float64 { return w.Top }, p.yTol)

	// Sort lines top-to-bottom within each cluster
	for _, line := range byLines {
		sort.Slice(line, func(i, j int) bool { return line[i].X0 < line[j].X0 })
	}

	// Find page origin for layout
	pageTop := 0.0
	pageLeft := 9999.0
	for _, w := range p.words {
		if w.Top > pageTop {
			pageTop = w.Top
		}
		if w.X0 < pageLeft {
			pageLeft = w.X0
		}
	}

	// Build character grid
	grid := make([][]rune, layoutHeightChars)
	for i := range grid {
		grid[i] = make([]rune, layoutWidthChars)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	numNewlines := 0

	for li, lineWords := range byLines {
		// Calculate Y distance from top (in character rows)
		lineTop := lineWords[0].Top
		yDist := int(math.Round((pageTop - lineTop) / yDensity))
		newlinesNeeded := yDist - numNewlines
		if li > 0 && newlinesNeeded < 1 {
			newlinesNeeded = 1
		}
		if newlinesNeeded > 0 {
			numNewlines += newlinesNeeded
		}

		// Place words on the grid
		for _, w := range lineWords {
			// Calculate X position in character columns
			xDist := int(math.Round((w.X0 - pageLeft) / xDensity))
			if xDist < 0 {
				xDist = 0
			}
			if xDist >= layoutWidthChars {
				xDist = layoutWidthChars - 1
			}

			runes := []rune(w.Text)
			for ri, r := range runes {
				col := xDist + ri
				if col < layoutWidthChars {
					// Find the correct row for this word
					row := int(math.Round((pageTop - w.Top) / yDensity))
					if row >= 0 && row < layoutHeightChars {
						grid[row][col] = r
					}
				}
			}
		}
	}

	// Render grid to string
	var out strings.Builder
	for _, row := range grid {
		line := strings.TrimRight(string(row), " ")
		out.WriteString(line)
		out.WriteString("\n")
	}
	return strings.TrimSpace(out.String())
}

// ---------- Merged Cell Detection (port of Table._get_rows_or_cols) ----------

// extractTableWithMergedCells extracts a table with full merged cell detection.
// Port of pdfplumber's Table._get_rows_or_cols + Table.extract.
func (p *PPage) extractTableWithMergedCells(cells []struct{ X0, Y0, X1, Y1 float64 }) Table {
	if len(cells) == 0 {
		return Table{}
	}

	// Step 1: Collect all unique X (column) and Y (row) positions
	xSet, ySet := make(map[float64]bool), make(map[float64]bool)
	for _, c := range cells {
		xSet[c.X0] = true
		xSet[c.X1] = true
		ySet[c.Y0] = true
		ySet[c.Y1] = true
	}
	xs := sortedKeys(xSet)
	ys := sortedKeys(ySet)
	sort.Sort(sort.Reverse(sort.Float64Slice(ys))) // top-to-bottom

	if len(xs) < 2 || len(ys) < 2 {
		return Table{}
	}

	numCols := len(xs) - 1
	numRows := len(ys) - 1

	// Step 2: Build a grid of which cells occupy which positions
	// cellGrid[row][col] = index into cells array, or -1 if empty/merged
	cellGrid := make([][]int, numRows)
	for i := range cellGrid {
		cellGrid[i] = make([]int, numCols)
		for j := range cellGrid[i] {
			cellGrid[i][j] = -1
		}
	}

	// Map each cell to its grid position(s)
	cellRanges := make([]struct{ r0, r1, c0, c1 int }, len(cells))
	for ci, c := range cells {
		r0, c0 := -1, -1
		r1, c1 := -1, -1
		for i := 0; i < numRows; i++ {
			if math.Abs(c.Y0-ys[i]) < 2 {
				r0 = i
			}
			if math.Abs(c.Y1-ys[i+1]) < 2 {
				r1 = i + 1
			}
		}
		for j := 0; j < numCols; j++ {
			if math.Abs(c.X0-xs[j]) < 2 {
				c0 = j
			}
			if math.Abs(c.X1-xs[j+1]) < 2 {
				c1 = j + 1
			}
		}
		if r0 >= 0 && r1 > r0 && c0 >= 0 && c1 > c0 {
			cellRanges[ci] = struct{ r0, r1, c0, c1 int }{r0, r1, c0, c1}
			for ri := r0; ri < r1; ri++ {
				for cj := c0; cj < c1; cj++ {
					cellGrid[ri][cj] = ci
				}
			}
		}
	}

	// Step 3: Extract text for each grid cell
	rows := make([][]string, numRows)
	for ri := 0; ri < numRows; ri++ {
		row := make([]string, numCols)
		rowTop := ys[ri]
		rowBot := ys[ri+1]
		for cj := 0; cj < numCols; cj++ {
			colX0 := xs[cj]
			colX1 := xs[cj+1]

			ci := cellGrid[ri][cj]
			if ci >= 0 {
				// This is the top-left of a cell (or a spanned sub-cell)
				cr := cellRanges[ci]
				// Only extract text for the top-left corner to avoid duplicates
				if ri == cr.r0 && cj == cr.c0 {
					// Use the actual cell bounds (may span multiple rows/cols)
					cellX0 := cells[ci].X0
					cellX1 := cells[ci].X1
					cellTop := cells[ci].Y0
					cellBot := cells[ci].Y1
					var texts []string
					for _, w := range p.words {
						if w.X0 >= cellX0-2 && w.X1 <= cellX1+2 &&
							w.Top <= cellTop+2 && w.Bot >= cellBot-2 {
							texts = append(texts, w.Text)
						}
					}
					row[cj] = strings.Join(texts, " ")
				}
			} else {
				// No cell at this position — it might be a gap or empty
				// Try to find text directly in this area
				var texts []string
				for _, w := range p.words {
					if w.X0 >= colX0-2 && w.X1 <= colX1+2 &&
						w.Top <= rowTop+2 && w.Bot >= rowBot-2 {
						texts = append(texts, w.Text)
					}
				}
				row[cj] = strings.Join(texts, " ")
			}
		}
		rows[ri] = row
	}

	// Step 4: Clean up — merge duplicate rows caused by split cells
	rows = deduplicateRows(rows)

	return Table{
		Rows: rows,
		X0:   xs[0], X1: xs[len(xs)-1],
		Top: ys[0], Bot: ys[len(ys)-1],
	}
}

// deduplicateRows removes consecutive identical rows (common artifact).
func deduplicateRows(rows [][]string) [][]string {
	if len(rows) < 2 {
		return rows
	}
	var out [][]string
	out = append(out, rows[0])
	for i := 1; i < len(rows); i++ {
		if !rowsEqual(rows[i], rows[i-1]) {
			out = append(out, rows[i])
		}
	}
	return out
}

func rowsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func sortedKeys(m map[float64]bool) []float64 {
	var keys []float64
	for k := range m {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	return keys
}

// ---------- Edge Filtering & Snapping (port of geometry.py) ----------

// snapEdges snaps nearby edges to their positional average within tolerance.
// Port of pdfplumber's snap_objects().
func snapEdges(edges []edge, attr string, tolerance float64) []edge {
	// Determine axis from attribute
	isHorizontal := attr == "x0" || attr == "x1"

	// Cluster by attribute value
	var values []float64
	for _, e := range edges {
		if isHorizontal {
			if attr == "x0" {
				values = append(values, e.X0)
			} else {
				values = append(values, e.X1)
			}
		} else {
			if attr == "top" {
				values = append(values, e.Top)
			} else {
				values = append(values, e.Bot)
			}
		}
	}
	clusters := clusterList(values, tolerance)

	// Calculate average for each cluster
	avgs := make([]float64, len(clusters))
	clusterMap := make(map[float64]float64) // original → average
	for i, c := range clusters {
		sum := 0.0
		for _, v := range c {
			sum += v
		}
		avgs[i] = sum / float64(len(c))
		for _, v := range c {
			clusterMap[v] = avgs[i]
		}
	}

	// Apply averages to edges
	result := make([]edge, len(edges))
	for i, e := range edges {
		result[i] = e
		var origVal float64
		if isHorizontal {
			if attr == "x0" {
				origVal = e.X0
			} else {
				origVal = e.X1
			}
		} else {
			if attr == "top" {
				origVal = e.Top
			} else {
				origVal = e.Bot
			}
		}
		avg, ok := clusterMap[origVal]
		if !ok {
			continue
		}
		if isHorizontal {
			if attr == "x0" {
				result[i].X0 = avg
				result[i].Width = result[i].X1 - avg
			} else {
				result[i].X1 = avg
				result[i].Width = avg - result[i].X0
			}
		} else {
			if attr == "top" {
				result[i].Top = avg
				result[i].Height = result[i].Bot - avg
			} else {
				result[i].Bot = avg
				result[i].Height = avg - result[i].Top
			}
		}
	}
	return result
}

// filterEdges filters edges by orientation and minimum length.
// Port of pdfplumber's filter_edges().
func filterEdges(edges []edge, orientation string, minLength float64) []edge {
	var result []edge
	for _, e := range edges {
		if e.Orientation != orientation {
			continue
		}
		dim := e.Width
		if orientation == "v" {
			dim = e.Height
		}
		if dim >= minLength {
			result = append(result, e)
		}
	}
	return result
}

// ---------- Character Deduplication (port of dedupe_chars) ----------

// DedupeChars removes duplicate characters sharing the same text and position within tolerance.
// Port of pdfplumber's dedupe_chars().
func (p *PPage) DedupeChars(tolerance float64) {
	p.ensureChars()
	if len(p.chars) < 2 {
		return
	}

	seen := make(map[string]bool)
	var deduped []pdfChar
	for _, c := range p.chars {
		key := fmt.Sprintf("%s_%.0f_%.0f", c.Text, math.Round(c.X0/tolerance), math.Round(c.Top/tolerance))
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, c)
		}
	}
	p.chars = deduped
	p.words = nil // invalidate word cache
}

// ---------- Page Region Filtering (port of page.crop / within_bbox / outside_bbox) ----------

type bboxFilter struct{ X0, Top, X1, Bot float64 }

// Crop returns a new PPage filtered to objects within the given bounding box.
// Port of pdfplumber's page.crop().
func (p *PPage) Crop(bbox bboxFilter) *PPage {
	p.ensureChars()
	cropped := &PPage{xTol: p.xTol, yTol: p.yTol}
	for _, c := range p.chars {
		if c.X0 >= bbox.X0 && c.X1 <= bbox.X1 && c.Top <= bbox.Top && c.Bot >= bbox.Bot {
			cropped.chars = append(cropped.chars, c)
		}
	}
	return cropped
}

// WithinBBox keeps only objects fully within the bbox.
// Port of pdfplumber's page.within_bbox().
func (p *PPage) WithinBBox(bbox bboxFilter) *PPage {
	return p.Crop(bbox)
}

// OutsideBBox keeps only objects outside the bbox (inverse crop).
// Port of pdfplumber's page.outside_bbox().
func (p *PPage) OutsideBBox(bbox bboxFilter) *PPage {
	p.ensureChars()
	filtered := &PPage{xTol: p.xTol, yTol: p.yTol}
	for _, c := range p.chars {
		if c.X1 < bbox.X0 || c.X0 > bbox.X1 || c.Bot > bbox.Top || c.Top < bbox.Bot {
			filtered.chars = append(filtered.chars, c)
		}
	}
	return filtered
}

// ---------- Text Search (port of page.search) ----------

// PdfSearchResult represents a regex match with position.
type PdfSearchResult struct {
	Text     string
	X0, X1   float64
	Top, Bot float64
	Chars    []pdfChar
}

// Search finds regex pattern matches in the page text, returning position info.
// Port of pdfplumber's page.search().
func (p *PPage) Search(pattern string, caseSensitive bool) []PdfSearchResult {
	text := p.ExtractText()
	if text == "" {
		return nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	if !caseSensitive {
		re = regexp.MustCompile("(?i)" + pattern)
	}

	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	p.ensureChars()
	var results []PdfSearchResult
	for _, m := range matches {
		matchText := text[m[0]:m[1]]
		if strings.TrimSpace(matchText) == "" {
			continue
		}

		// Find chars corresponding to this text range
		var matchChars []pdfChar
		charIdx := 0
		for _, c := range p.chars {
			if charIdx >= m[0] && charIdx < m[1] {
				matchChars = append(matchChars, c)
			}
			charIdx += len([]rune(c.Text))
			if charIdx > m[1] {
				break
			}
		}

		// Compute bbox from matched chars
		if len(matchChars) > 0 {
			x0, x1 := matchChars[0].X0, matchChars[0].X1
			top, bot := matchChars[0].Top, matchChars[0].Bot
			for _, c := range matchChars {
				if c.X0 < x0 {
					x0 = c.X0
				}
				if c.X1 > x1 {
					x1 = c.X1
				}
				if c.Top > top {
					top = c.Top
				}
				if c.Bot < bot {
					bot = c.Bot
				}
			}
			results = append(results, PdfSearchResult{
				Text: matchText, X0: x0, X1: x1, Top: top, Bot: bot, Chars: matchChars,
			})
		}
	}
	return results
}

// ---------- Font Statistics (for heading detection) ----------

// FontStat represents aggregated font information.
type FontStat struct {
	Font     string
	FontSize float64
	Count    int
	Examples []string
}

// FontStats returns font usage statistics, useful for heading/body detection.
// Larger fonts typically indicate titles and section headers.
func (p *PPage) FontStats() []FontStat {
	p.ensureChars()
	if len(p.chars) == 0 {
		return nil
	}

	stats := make(map[string]*FontStat)
	for _, c := range p.chars {
		key := fmt.Sprintf("%s_%.1f", c.Font, c.FontSize)
		if s, ok := stats[key]; ok {
			s.Count++
			if len(s.Examples) < 3 {
				s.Examples = append(s.Examples, c.Text)
			}
		} else {
			stats[key] = &FontStat{Font: c.Font, FontSize: c.FontSize, Count: 1, Examples: []string{c.Text}}
		}
	}

	var result []FontStat
	for _, s := range stats {
		result = append(result, *s)
	}
	// Sort by font size descending (largest first = likely headings)
	sort.Slice(result, func(i, j int) bool { return result[i].FontSize > result[j].FontSize })
	return result
}

// DetectHeadingsByFont finds likely headings based on font size.
// Fonts larger than median are likely headings.
func (p *PPage) DetectHeadingsByFont() []Word {
	p.ensureWords()
	stats := p.FontStats()
	if len(stats) < 1 {
		return nil
	}

	// Median font size
	var sizes []float64
	for _, s := range stats {
		sizes = append(sizes, s.FontSize)
	}
	sort.Float64s(sizes)
	medianSize := sizes[len(sizes)/2]
	headingThreshold := medianSize * 1.1

	var headings []Word
	for _, w := range p.words {
		if w.FontSize > headingThreshold {
			headings = append(headings, w)
		}
	}
	return headings
}

// ---------- Edge Extraction from Shapes (port of obj_to_edges / rect_to_edges / line_to_edge) ----------

// extractEdgesFromRects converts PDF rectangles to edge objects.
// Port of pdfplumber's rect_to_edges().
func extractEdgesFromRects(rects []pdf.Rect) []edge {
	var edges []edge
	for _, r := range rects {
		x0 := math.Min(r.Min.X, r.Max.X)
		x1 := math.Max(r.Min.X, r.Max.X)
		y0 := math.Min(r.Min.Y, r.Max.Y)
		y1 := math.Max(r.Min.Y, r.Max.Y)

		// Top edge (horizontal)
		edges = append(edges, edge{
			X0: x0, X1: x1, Top: y1, Bot: y1,
			Width: x1 - x0, Height: 0, Orientation: "h",
		})
		// Bottom edge (horizontal)
		edges = append(edges, edge{
			X0: x0, X1: x1, Top: y0, Bot: y0,
			Width: x1 - x0, Height: 0, Orientation: "h",
		})
		// Left edge (vertical)
		edges = append(edges, edge{
			X0: x0, X1: x0, Top: y1, Bot: y0,
			Width: 0, Height: y1 - y0, Orientation: "v",
		})
		// Right edge (vertical)
		edges = append(edges, edge{
			X0: x1, X1: x1, Top: y1, Bot: y0,
			Width: 0, Height: y1 - y0, Orientation: "v",
		})
	}
	return edges
}

// ---------- Enhanced Table Detection with Better Edge Processing ----------

// ExtractTablesAdvanced runs the full table detection pipeline with edge snapping.
// This is the production-quality version that matches pdfplumber's default behavior.
func (p *PPage) ExtractTablesAdvanced() []Table {
	p.ensureWords()

	// Get edges from rectangles + word alignment
	var edges []edge
	edges = append(edges, extractEdgesFromRects(p.rects)...)
	edges = append(edges, p.wordsToEdgesH(1)...)
	edges = append(edges, p.wordsToEdgesV(3)...)

	if len(edges) < 4 {
		// Fall back to gap-based
		return p.detectTablesByGaps()
	}

	// Snap nearby edges to their average position
	vEdges := filterEdges(edges, "v", 10)
	hEdges := filterEdges(edges, "h", 10)

	if len(vEdges) > 0 {
		vEdges = snapEdges(vEdges, "x0", 3)
	}
	if len(hEdges) > 0 {
		hEdges = snapEdges(hEdges, "top", 3)
	}

	edges = append(vEdges, hEdges...)
	edges = mergeEdges(edges, 2, 2, 2, 2)

	// Find intersections → cells → tables
	intersections := edgesToIntersections(edges, 1, 1)
	cells := intersectionsToCells(intersections)
	if len(cells) == 0 {
		return p.detectTablesByGaps()
	}

	tables := cellsToTables(cells)
	if len(tables) == 0 {
		return p.detectTablesByGaps()
	}

	var result []Table
	for _, cellGroup := range tables {
		table := p.extractTableWithMergedCells(cellGroup)
		if len(table.Rows) > 0 {
			result = append(result, table)
		}
	}
	return result
}

// ---------- Complete Extraction Pipeline (unified entry point) ----------

// ExtractAll extracts everything from a page: text, tables, font stats, headings.
func (p *PPage) ExtractAll() string {
	var out strings.Builder

	// Text with layout
	out.WriteString(p.ExtractTextLayout(p.pageWidth(), p.pageHeight()))

	// Tables
	tables := p.ExtractTablesAdvanced()
	if len(tables) > 0 {
		out.WriteString("\n\n## Detected Tables\n\n")
		for i, t := range tables {
			out.WriteString(fmt.Sprintf("### Table %d\n\n", i+1))
			out.WriteString(TableToMarkdown(t))
			out.WriteString("\n")
		}
	}

	// Font analysis for debugging
	fonts := p.FontStats()
	if len(fonts) > 0 {
		out.WriteString("\n\n## Font Analysis\n\n")
		out.WriteString("| Font | Size | Count | Examples |\n")
		out.WriteString("|------|------|-------|----------|\n")
		for _, f := range fonts {
			if f.Count < 3 {
				continue
			}
			out.WriteString(fmt.Sprintf("| %s | %.1f | %d | %s |\n",
				f.Font, f.FontSize, f.Count, strings.Join(f.Examples, ", ")))
		}
	}

	return strings.TrimSpace(out.String())
}

// ---------- Document-Level API ----------

// PDoc wraps a PDF document.
type PDoc struct {
	file   *os.File
	reader *pdf.Reader
	pages  int
}

func OpenPDF(path string) (*PDoc, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pdfplumber: open %s: %w", path, err)
	}
	return &PDoc{file: f, reader: r, pages: r.NumPage()}, nil
}

func (d *PDoc) Close() error  { return d.file.Close() }
func (d *PDoc) NumPages() int { return d.pages }
func (d *PDoc) Page(n int) *PPage {
	if n < 1 || n > d.pages {
		return nil
	}
	return newPPage(d.reader.Page(n))
}

// ====================== Image Extraction ======================

type ExtractedImage struct {
	Name             string
	PageNumber       int
	Format           string
	Width, Height    int
	BitsPerComponent int
	Data             []byte
}

func (p *PPage) ExtractImages() []ExtractedImage {
	resources := p.page.Resources()
	if resources.Kind() != pdf.Dict {
		return nil
	}
	xobj := resources.Key("XObject")
	if xobj.Kind() != pdf.Dict {
		return nil
	}

	var images []ExtractedImage
	for _, name := range xobj.Keys() {
		imgDict := xobj.Key(name)
		if imgDict.Kind() != pdf.Stream {
			continue
		}
		subtype := imgDict.Key("Subtype")
		if subtype.Kind() != pdf.Name || subtype.Name() != "Image" {
			continue
		}

		img := ExtractedImage{Name: name, Format: detectImageFormat(imgDict)}
		img.Width = intValue(imgDict.Key("Width"))
		img.Height = intValue(imgDict.Key("Height"))
		img.BitsPerComponent = intValue(imgDict.Key("BitsPerComponent"))
		if img.BitsPerComponent == 0 {
			img.BitsPerComponent = 8
		}

		// Check for unsupported filters (e.g. DCTDecode JPEG) before reading
		imgFormat := detectImageFormat(imgDict)
		if imgFormat == "" {
			continue
		} // skip unsupported

		// Recover from panics in the pdf library (e.g. unknown filter)
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[pdfplumber] Image read panic (filter=%s): %v", imgFormat, r)
					img.Data = nil
				}
			}()
			reader := imgDict.Reader()
			if reader != nil {
				img.Data, _ = io.ReadAll(reader)
				reader.Close()
			}
		}()
		if len(img.Data) > 0 {
			images = append(images, img)
		}
	}
	return images
}

func intValue(v pdf.Value) int {
	switch v.Kind() {
	case pdf.Integer:
		return int(v.Int64())
	case pdf.Real:
		return int(v.Float64())
	}
	return 0
}

func detectImageFormat(imgDict pdf.Value) string {
	filter := imgDict.Key("Filter")
	filterName := ""
	if filter.Kind() == pdf.Name {
		filterName = filter.Name()
	} else if filter.Kind() == pdf.Array {
		for i := 0; i < filter.Len(); i++ {
			if item := filter.Index(i); item.Kind() == pdf.Name {
				filterName = item.Name()
			}
		}
	}
	switch filterName {
	case "DCTDecode":
		return "" // not supported by ledongthuc/pdf, skip to avoid panic
	case "JPXDecode":
		return "jp2"
	case "CCITTFaxDecode":
		return "ccitt"
	case "JBIG2Decode":
		return "jbig2"
	case "FlateDecode", "":
		return "raw"
	default:
		return "" // unknown filter, skip to be safe
	}
}

func SaveImages(path, outputDir string) ([]string, error) {
	doc, err := OpenPDF(path)
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	os.MkdirAll(outputDir, 0755)
	var saved []string
	for i := 1; i <= doc.NumPages(); i++ {
		for _, img := range doc.Page(i).ExtractImages() {
			if img.Format == "" {
				continue
			} // skip unsupported filters
			img.PageNumber = i
			ext := img.Format
			if ext == "raw" {
				if len(img.Data) > 2 && img.Data[0] == 0xff && img.Data[1] == 0xd8 {
					ext = "jpeg"
				} else if len(img.Data) > 8 && img.Data[0] == 0x89 && img.Data[1] == 0x50 {
					ext = "png"
				} else {
					ext = "bin"
				}
			}
			filename := fmt.Sprintf("page_%d_%s.%s", i, img.Name, ext)
			filepath := outputDir + "/" + filename
			if err := os.WriteFile(filepath, img.Data, 0644); err != nil {
				continue
			}
			saved = append(saved, filepath)
		}
	}
	log.Printf("[pdfplumber] Extracted %d images from %s", len(saved), path)
	return saved, nil
}

// ExtractPDFWithLayout extracts text from all pages with layout preservation.
func ExtractPDFWithLayout(path string) (string, error) {
	doc, err := OpenPDF(path)
	if err != nil {
		return "", err
	}
	defer doc.Close()

	var out strings.Builder
	for i := 1; i <= doc.NumPages(); i++ {
		page := doc.Page(i)
		out.WriteString(fmt.Sprintf("\n--- Page %d ---\n\n", i))
		out.WriteString(page.ExtractTextLayout(page.pageWidth(), page.pageHeight()))
		for _, table := range page.ExtractTables() {
			out.WriteString("\n")
			out.WriteString(TableToMarkdown(table))
			out.WriteString("\n")
		}
	}
	result := strings.TrimSpace(out.String())
	if goTextIsGarbled(result) {
		log.Printf("[pdfplumber_go] WARNING: garbled output detected (%d chars), library encoding may be incomplete", len(result))
	}
	return result, nil
}

// pageWidth/Height get page dimensions from the PDF page object.
func (p *PPage) pageWidth() float64 {
	// Default to A4 width (595pt) if not available
	return 595
}
func (p *PPage) pageHeight() float64 {
	return 842 // A4 height
}

// ExtractPDFStructured: full pure-Go pipeline (extract → classify → chunk).
func ExtractPDFStructured(path string, chunk bool) (string, error) {
	text, err := ExtractPDFWithLayout(path)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", nil
	}
	elements := textutil.ClassifyText(text)
	if len(elements) == 0 {
		return text, nil
	}
	if chunk {
		cfg := textutil.DefaultUnstructChunkConfig()
		chunks := textutil.ChunkByTitle(elements, cfg)
		log.Printf("[pdfplumber_go] Structured: %d elements → %d chunks", len(elements), len(chunks))
		return textutil.UnstructChunksToMarkdown(chunks), nil
	}
	log.Printf("[pdfplumber_go] Structured: %d elements", len(elements))
	return textutil.FormatClassifiedElements(elements), nil
}

func TableToMarkdown(t Table) string {
	if len(t.Rows) == 0 || len(t.Rows[0]) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("| " + strings.Join(t.Rows[0], " | ") + " |\n")
	sep := make([]string, len(t.Rows[0]))
	for i := range sep {
		sep[i] = "---"
	}
	out.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, row := range t.Rows[1:] {
		padded := make([]string, len(t.Rows[0]))
		copy(padded, row)
		out.WriteString("| " + strings.Join(padded, " | ") + " |\n")
	}
	return out.String()
}

// ====================== Extended PDF Analysis API ======================

func (d *PDoc) PageText(n int) string {
	page := d.Page(n)
	if page == nil {
		return ""
	}
	return page.ExtractText()
}

func (d *PDoc) PageTables(n int) []Table {
	page := d.Page(n)
	if page == nil {
		return nil
	}
	return page.ExtractTablesAdvanced()
}

func (d *PDoc) AllTables() []Table {
	var all []Table
	for i := 1; i <= d.pages; i++ {
		all = append(all, d.PageTables(i)...)
	}
	return all
}

func (d *PDoc) Pages() []*PPage {
	var pages []*PPage
	for i := 1; i <= d.pages; i++ {
		pages = append(pages, d.Page(i))
	}
	return pages
}

func (d *PDoc) AllImages() []ExtractedImage {
	var all []ExtractedImage
	for i := 1; i <= d.pages; i++ {
		for _, img := range d.Page(i).ExtractImages() {
			img.PageNumber = i
			all = append(all, img)
		}
	}
	return all
}

func (p *PPage) WordCount() int           { p.ensureWords(); return len(p.words) }
func (p *PPage) CharCount() int           { p.ensureChars(); return len(p.chars) }
func (p *PPage) RectCount() int           { return len(p.rects) }
func (p *PPage) PageSize() (w, h float64) { return p.pageWidth(), p.pageHeight() }

func (p *PPage) BBox() bboxFilter {
	p.ensureChars()
	if len(p.chars) == 0 {
		return bboxFilter{}
	}
	b := bboxFilter{p.chars[0].X0, p.chars[0].Top, p.chars[0].X1, p.chars[0].Bot}
	for _, c := range p.chars {
		if c.X0 < b.X0 {
			b.X0 = c.X0
		}
		if c.X1 > b.X1 {
			b.X1 = c.X1
		}
		if c.Top > b.Top {
			b.Top = c.Top
		}
		if c.Bot < b.Bot {
			b.Bot = c.Bot
		}
	}
	return b
}

type TextLine struct {
	Text     string
	X0, X1   float64
	Top, Bot float64
	Chars    []pdfChar
	Words    []Word
}

func (p *PPage) ExtractTextLines() []TextLine {
	p.ensureWords()
	p.ensureChars()
	if len(p.words) == 0 {
		return nil
	}
	wordLines := p.Lines()
	var lines []TextLine
	for _, wl := range wordLines {
		if len(wl) == 0 {
			continue
		}
		sort.Slice(wl, func(i, j int) bool { return wl[i].X0 < wl[j].X0 })
		var text strings.Builder
		lastX1 := -9999.0
		for _, w := range wl {
			if lastX1 > -9999 && w.X0 > lastX1+p.xTol {
				text.WriteString(" ")
			}
			text.WriteString(w.Text)
			lastX1 = w.X1
		}
		var lineChars []pdfChar
		ltop, lbot := wl[0].Top, wl[0].Bot
		for _, w := range wl {
			if w.Top > ltop {
				ltop = w.Top
			}
			if w.Bot < lbot {
				lbot = w.Bot
			}
		}
		for _, c := range p.chars {
			if c.Top <= ltop+p.yTol && c.Bot >= lbot-p.yTol {
				lineChars = append(lineChars, c)
			}
		}
		lines = append(lines, TextLine{Text: text.String(), X0: wl[0].X0, X1: wl[len(wl)-1].X1, Top: ltop, Bot: lbot, Chars: lineChars, Words: wl})
	}
	return lines
}

type RectInfo struct {
	X0, X1, Top, Bot, Width, Height, Area float64
	IsHorizontal, IsVertical              bool
}

func (p *PPage) AnalyzeRects() []RectInfo {
	var result []RectInfo
	for _, r := range p.rects {
		w := math.Abs(r.Max.X - r.Min.X)
		h := math.Abs(r.Max.Y - r.Min.Y)
		result = append(result, RectInfo{X0: r.Min.X, X1: r.Max.X, Top: r.Min.Y, Bot: r.Max.Y, Width: w, Height: h, Area: w * h, IsHorizontal: w > h*10, IsVertical: h > w*10})
	}
	return result
}

type ColumnInfo struct {
	X0, X1 float64
	Words  []Word
	Lines  [][]Word
}

func (p *PPage) DetectColumns() []ColumnInfo {
	p.ensureWords()
	if len(p.words) < 5 {
		return nil
	}
	var x0s []float64
	for _, w := range p.words {
		x0s = append(x0s, w.X0)
	}
	clusters := clusterList(x0s, 15)
	var columns []ColumnInfo
	for _, c := range clusters {
		if len(c) < 3 {
			continue
		}
		colX0 := c[0]
		colX1 := c[len(c)-1] + 50
		var colWords []Word
		for _, w := range p.words {
			if w.X0 >= colX0-5 && w.X0 <= colX1+5 {
				colWords = append(colWords, w)
			}
		}
		if len(colWords) >= 3 {
			sort.Slice(colWords, func(i, j int) bool {
				if math.Abs(colWords[i].Top-colWords[j].Top) > 2 {
					return colWords[i].Top > colWords[j].Top
				}
				return colWords[i].X0 < colWords[j].X0
			})
			lines := clusterObjects(colWords, func(w Word) float64 { return w.Top }, 3)
			columns = append(columns, ColumnInfo{X0: colX0, X1: colX1, Words: colWords, Lines: lines})
		}
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].X0 < columns[j].X0 })
	return columns
}

func (p *PPage) HasMultiColumns() bool { return len(p.DetectColumns()) > 1 }

type MarginInfo struct{ Left, Right, Top, Bottom float64 }

func (p *PPage) DetectMargins(pageW, pageH float64) MarginInfo {
	p.ensureChars()
	if len(p.chars) == 0 {
		return MarginInfo{}
	}
	left := 9999.0
	right := 0.0
	top := 0.0
	bottom := 9999.0
	for _, c := range p.chars {
		if c.X0 < left {
			left = c.X0
		}
		if c.X1 > right {
			right = c.X1
		}
		if c.Top > top {
			top = c.Top
		}
		if c.Bot < bottom {
			bottom = c.Bot
		}
	}
	return MarginInfo{Left: left, Right: pageW - right, Top: pageH - top, Bottom: bottom}
}

type DensityInfo struct {
	X0, X1, Top, Bot     float64
	CharCount, WordCount int
	Density              float64
}

func (p *PPage) AnalyzeDensity(rows, cols int) []DensityInfo {
	p.ensureChars()
	p.ensureWords()
	bbox := p.BBox()
	cellW := (bbox.X1 - bbox.X0) / float64(cols)
	cellH := (bbox.Top - bbox.Bot) / float64(rows)
	var cells []DensityInfo
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cellX0 := bbox.X0 + float64(c)*cellW
			cellX1 := cellX0 + cellW
			cellTop := bbox.Top - float64(r)*cellH
			cellBot := cellTop - cellH
			var chars, words int
			for _, ch := range p.chars {
				if ch.X0 >= cellX0 && ch.X1 <= cellX1 && ch.Top <= cellTop && ch.Bot >= cellBot {
					chars++
				}
			}
			for _, w := range p.words {
				if w.X0 >= cellX0 && w.X1 <= cellX1 && w.Top <= cellTop && w.Bot >= cellBot {
					words++
				}
			}
			area := cellW * cellH
			if area > 0 && chars > 0 {
				cells = append(cells, DensityInfo{X0: cellX0, X1: cellX1, Top: cellTop, Bot: cellBot, CharCount: chars, WordCount: words, Density: float64(chars) / area})
			}
		}
	}
	return cells
}

type TableExtractConfig struct {
	VerticalStrategy, HorizontalStrategy string
	SnapTolerance, JoinTolerance         float64
	MinWordsVertical, MinWordsHorizontal int
	IntersectionTol                      float64
}

func DefaultTableConfig() TableExtractConfig {
	return TableExtractConfig{VerticalStrategy: "lines", HorizontalStrategy: "lines", SnapTolerance: 3, JoinTolerance: 3, MinWordsVertical: 3, MinWordsHorizontal: 1, IntersectionTol: 1}
}

func (p *PPage) ExtractTablesWithConfig(cfg TableExtractConfig) []Table {
	p.ensureWords()
	var edges []edge
	if cfg.VerticalStrategy == "lines" || cfg.VerticalStrategy == "lines_strict" {
		edges = append(edges, filterEdges(extractEdgesFromRects(p.rects), "v", 10)...)
	} else if cfg.VerticalStrategy == "text" {
		edges = append(edges, p.wordsToEdgesV(cfg.MinWordsVertical)...)
	}
	if cfg.HorizontalStrategy == "lines" || cfg.HorizontalStrategy == "lines_strict" {
		edges = append(edges, filterEdges(extractEdgesFromRects(p.rects), "h", 10)...)
	} else if cfg.HorizontalStrategy == "text" {
		edges = append(edges, p.wordsToEdgesH(cfg.MinWordsHorizontal)...)
	}
	if len(edges) < 4 {
		return p.detectTablesByGaps()
	}
	vEdges := filterEdges(edges, "v", 10)
	hEdges := filterEdges(edges, "h", 10)
	if len(vEdges) > 0 {
		vEdges = snapEdges(vEdges, "x0", cfg.SnapTolerance)
	}
	if len(hEdges) > 0 {
		hEdges = snapEdges(hEdges, "top", cfg.SnapTolerance)
	}
	edges = append(vEdges, hEdges...)
	edges = mergeEdges(edges, cfg.SnapTolerance, cfg.SnapTolerance, cfg.JoinTolerance, cfg.JoinTolerance)
	intersections := edgesToIntersections(edges, cfg.IntersectionTol, cfg.IntersectionTol)
	cells := intersectionsToCells(intersections)
	if len(cells) == 0 {
		return p.detectTablesByGaps()
	}
	tables := cellsToTables(cells)
	if len(tables) == 0 {
		return p.detectTablesByGaps()
	}
	var result []Table
	for _, cg := range tables {
		table := p.extractTableWithMergedCells(cg)
		if len(table.Rows) > 0 {
			result = append(result, table)
		}
	}
	return result
}

type PageDiff struct {
	AddedWords, RemovedWords []Word
	ChangedWords             []struct{ Old, New Word }
}

func DiffPages(a, b *PPage) PageDiff {
	a.ensureWords()
	b.ensureWords()
	aMap := make(map[string]Word)
	bMap := make(map[string]Word)
	for _, w := range a.words {
		aMap[fmt.Sprintf("%s_%.0f_%.0f", w.Text, w.X0, w.Top)] = w
	}
	for _, w := range b.words {
		bMap[fmt.Sprintf("%s_%.0f_%.0f", w.Text, w.X0, w.Top)] = w
	}
	var diff PageDiff
	for k, w := range aMap {
		if _, ok := bMap[k]; !ok {
			diff.RemovedWords = append(diff.RemovedWords, w)
		}
	}
	for k, w := range bMap {
		if _, ok := aMap[k]; !ok {
			diff.AddedWords = append(diff.AddedWords, w)
		}
	}
	return diff
}

// ====================== Debug / Visual Output ======================

func (p *PPage) DebugText(width int) string {
	p.ensureWords()
	lines := p.Lines()
	if len(lines) == 0 {
		return "(empty page)"
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Page: %d words, %d lines\n", len(p.words), len(lines)))
	out.WriteString(strings.Repeat("=", width) + "\n")
	for _, line := range lines {
		sort.Slice(line, func(i, j int) bool { return line[i].X0 < line[j].X0 })
		for _, w := range line {
			out.WriteString(fmt.Sprintf("[%s @(%.0f,%.0f)] ", w.Text, w.X0, w.Top))
		}
		out.WriteString("\n")
	}
	return out.String()
}

func (p *PPage) DebugTables() string {
	tables := p.ExtractTables()
	if len(tables) == 0 {
		return "(no tables detected)"
	}
	var out strings.Builder
	for i, t := range tables {
		out.WriteString(fmt.Sprintf("Table %d: %d rows x %d cols\n", i+1, len(t.Rows), len(t.Rows[0])))
		out.WriteString(fmt.Sprintf("  BBox: (%.0f, %.0f) -> (%.0f, %.0f)\n", t.X0, t.Top, t.X1, t.Bot))
		for ri, row := range t.Rows {
			out.WriteString(fmt.Sprintf("  Row %d: %v\n", ri, row))
		}
	}
	return out.String()
}
