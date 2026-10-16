package app

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"mac/internal/textutil"
	contenttools "mac/internal/tools/content"
)

// ====================== End-to-End Pipeline Tests ======================

var testPDFs = []string{
	`E:\openkitty\storage\smic_deck.pdf`,
	`E:\openkitty\storage\iData一站式数据智能平台讲解.pdf`,
	`E:\openkitty\storage\resume_cn.pdf`,
}

func TestPDFExtract(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			text, err := contenttools.ExtractPDFWithLayout(pdf)
			if err != nil {
				t.Fatalf("ExtractPDFWithLayout: %v", err)
			}
			if text == "" {
				t.Fatal("empty output")
			}
			t.Logf("Extracted: %d chars", len(text))
			log.Println("--- FIRST 500 CHARS ---")
			log.Println(truncate(text, 500))
		})
	}
}

func TestPDFClean(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			text, _ := contenttools.ExtractPDFWithLayout(pdf)
			cleaned := textutil.CleanDocument(text)
			t.Logf("Raw: %d → Cleaned: %d chars", len(text), len(cleaned))
			log.Println("--- FIRST 500 CLEAN ---")
			log.Println(truncate(cleaned, 500))
		})
	}
}

func TestPDFClassify(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			text, _ := contenttools.ExtractPDFWithLayout(pdf)
			cleaned := textutil.CleanDocument(text)
			elements := textutil.ClassifyText(cleaned)
			if len(elements) == 0 {
				t.Fatal("no elements classified")
			}
			t.Logf("Elements: %d", len(elements))
			// Count types
			counts := map[textutil.ElementType]int{}
			for _, el := range elements {
				counts[el.Type]++
			}
			for typ, cnt := range counts {
				t.Logf("  %s: %d", typ, cnt)
			}
			// Print first 10 elements
			for i, el := range elements {
				if i >= 10 {
					break
				}
				t.Logf("  [%d] %s: %s", i+1, el.Type, truncate(el.Text, 80))
			}
		})
	}
}

func TestPDFChunk(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			text, _ := contenttools.ExtractPDFWithLayout(pdf)
			cleaned := textutil.CleanDocument(text)
			elements := textutil.ClassifyText(cleaned)
			cfg := textutil.DefaultUnstructChunkConfig()
			chunks := textutil.ChunkByTitle(elements, cfg)
			if len(chunks) == 0 {
				t.Fatal("no chunks produced")
			}
			t.Logf("Chunks: %d", len(chunks))
			for i, c := range chunks {
				if i >= 3 {
					break
				}
				heading := c.Heading
				if len([]rune(heading)) > 50 {
					heading = string([]rune(heading)[:50])
				}
				t.Logf("  Chunk %d [%s]: %d chars", i+1, heading, len([]rune(c.Text)))
			}
		})
	}
}

func TestFullPipeline(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			t.Logf("=== %s ===", fileBase(pdf))

			// Step 1: Extract
			text, err := contenttools.ExtractPDFWithLayout(pdf)
			if err != nil {
				t.Fatalf("Step1 Extract: %v", err)
			}
			t.Logf("  Step1 Extract: %d chars", len(text))

			// Step 2: Clean
			cleaned := textutil.CleanDocument(text)
			t.Logf("  Step2 Clean: %d chars (%.0f%% of raw)", len(cleaned), float64(len(cleaned))/float64(len(text))*100)

			// Step 3: Classify
			elements := textutil.ClassifyText(cleaned)
			counts := map[textutil.ElementType]int{}
			for _, el := range elements {
				counts[el.Type]++
			}
			t.Logf("  Step3 Classify: %d elements", len(elements))
			t.Logf("    Title=%d  SectionHeader=%d  NarrativeText=%d  ListItem=%d  Table=%d",
				counts[textutil.ElemTitle], counts[textutil.ElemSectionHeader], counts[textutil.ElemNarrativeText],
				counts[textutil.ElemListItem], counts[textutil.ElemTable])

			// Step 4: Chunk
			cfg := textutil.DefaultUnstructChunkConfig()
			chunks := textutil.ChunkByTitle(elements, cfg)
			t.Logf("  Step4 Chunk: %d chunks", len(chunks))
			if len(chunks) > 0 {
				avgChars := 0
				for _, c := range chunks {
					avgChars += len([]rune(c.Text))
				}
				t.Logf("    Avg chunk size: %d chars", avgChars/len(chunks))
			}

			// Summary
			t.Logf("  Pipeline: %s → OK", pdf)
		})
	}
}

func TestImageExtract(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			doc, err := contenttools.OpenPDF(pdf)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer doc.Close()
			images := doc.AllImages()
			t.Logf("Images found: %d", len(images))
			for i, img := range images {
				t.Logf("  [%d] Page %d: %dx%d %s (%d bytes)",
					i+1, img.PageNumber, img.Width, img.Height, img.Format, len(img.Data))
			}
		})
	}
}

func TestTableExtract(t *testing.T) {
	for _, pdf := range testPDFs {
		t.Run(fileBase(pdf), func(t *testing.T) {
			if !contenttools.FileExists(pdf) {
				t.Skip("file not found: " + pdf)
			}
			doc, err := contenttools.OpenPDF(pdf)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer doc.Close()
			for i := 1; i <= doc.NumPages(); i++ {
				page := doc.Page(i)
				tables := page.ExtractTables()
				for j, table := range tables {
					t.Logf("  Page %d Table %d: %dx%d",
						i, j+1, len(table.Rows), len(table.Rows[0]))
					if len(table.Rows) > 0 {
						row := table.Rows[0]
						t.Logf("    Header: %s", strings.Join(row, " | "))
					}
				}
			}
		})
	}
}

func TestChunkUFFFD(t *testing.T) {
	// Reproduce the "chunk starts with �" bug from web frontend
	doc, err := contenttools.OpenPDF(`E:\openkitty\storage\resume_cn.pdf`)
	if err != nil {
		t.Skip("no test file")
		return
	}
	defer doc.Close()

	text := ""
	var out strings.Builder
	for i := 1; i <= doc.NumPages(); i++ {
		page := doc.Page(i)
		out.WriteString(fmt.Sprintf("\n--- Page %d ---\n\n", i))
		pageWidth, pageHeight := page.PageSize()
		out.WriteString(page.ExtractTextLayout(pageWidth, pageHeight))
	}
	text = out.String()
	t.Logf("Extract: %d bytes, FF=%d", len(text), strings.Count(text, "�"))

	cleaned := textutil.CleanDocument(text)
	t.Logf("Clean: %d bytes, FF=%d", len(cleaned), strings.Count(cleaned, "�"))

	elements := textutil.ClassifyText(cleaned)
	for i, el := range elements {
		if ff := strings.Count(el.Text, "�"); ff > 0 {
			t.Errorf("Element[%d] %s FF=%d: %q", i, el.Type, ff, el.Text[:minInt2(len(el.Text), 100)])
		}
	}
	t.Logf("Classify: %d elements", len(elements))

	cfg := textutil.DefaultUnstructChunkConfig()
	chunks := textutil.ChunkByTitle(elements, cfg)
	t.Logf("Chunks: %d", len(chunks))
	for i, c := range chunks {
		if ff := strings.Count(c.Text, "�"); ff > 0 {
			t.Errorf("Chunk[%d] FF=%d heading=%q text[:100]=%q", i, ff, c.Heading, c.Text[:minInt2(len(c.Text), 100)])
		}
	}

	markdown := textutil.UnstructChunksToMarkdown(chunks)
	t.Logf("Markdown: %d bytes, FF=%d", len(markdown), strings.Count(markdown, "�"))
}

func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestAPIPipelineWithPython(t *testing.T) {
	// Test the exact API pipeline: Python extraction → CleanDocument → ClassifyText
	pyPath := os.Getenv("OPENKITTY_TEST_PDF")
	if pyPath == "" {
		t.Skip("OPENKITTY_TEST_PDF not set")
	}
	pyText := extractWithPdfPlumber(pyPath)
	if pyText == "" {
		t.Skip("Python pdfplumber not available")
	}
	t.Logf("Python: %d bytes, U+FFFD=%d", len(pyText), strings.Count(pyText, "�"))
	if c := strings.Count(pyText, "�"); c > 0 {
		t.Fatalf("Python output has %d U+FFFD - extraction is broken!", c)
	}

	// Simulate API pipeline
	cleaned := textutil.CleanDocument(pyText)
	t.Logf("After CleanDocument: %d bytes, U+FFFD=%d", len(cleaned), strings.Count(cleaned, "�"))

	elements := textutil.ClassifyText(cleaned)
	ffCount := 0
	for _, e := range elements {
		ffCount += strings.Count(e.Text, "�")
	}
	t.Logf("After ClassifyText: %d elements, U+FFFD=%d", len(elements), ffCount)
	if ffCount > 0 {
		for i, e := range elements {
			if c := strings.Count(e.Text, "�"); c > 0 {
				maxLen := len(e.Text)
				if maxLen > 100 {
					maxLen = 100
				}
				t.Errorf("Element[%d] %s has %d U+FFFD: %q", i, e.Type, c, e.Text[:maxLen])
			}
		}
		t.Fatalf("Pipeline introduced %d U+FFFD!", ffCount)
	}
	t.Log("SUCCESS: Pipeline is clean!")
}

func minIntStr(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func BenchmarkFullPipeline(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, pdf := range testPDFs {
			if !contenttools.FileExists(pdf) {
				continue
			}
			text, _ := contenttools.ExtractPDFWithLayout(pdf)
			cleaned := textutil.CleanDocument(text)
			elements := textutil.ClassifyText(cleaned)
			cfg := textutil.DefaultUnstructChunkConfig()
			textutil.ChunkByTitle(elements, cfg)
		}
	}
}

func fileBase(path string) string {
	s := path
	if idx := strings.LastIndexByte(s, '\\'); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndexByte(s, '/'); idx >= 0 {
		s = s[idx+1:]
	}
	return s
}

func init() {
	os.MkdirAll("data", 0755)
	os.Setenv("OPENKITTY_PDF_PARSER", "pdfplumber_go")
}
