package document

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/textutil"
	contenttools "mac/internal/tools/content"
)

type Dependencies struct {
	ExtractPDF  func(string) string
	ExtractDOCX func(string) string
	ExtractXLSX func([]byte) (string, error)
}

type Handler struct {
	dependencies Dependencies
}

func NewHandler(dependencies Dependencies) *Handler {
	return &Handler{dependencies: dependencies}
}

// ====================== Document Analysis API ======================
// POST /api/document/analyze — upload PDF, get full pipeline result

type DocAnalyzeRequest struct {
	Mode        string `json:"mode"`          // "full" (default), "simple" (no chunk)
	MaxFileSize int64  `json:"max_file_size"` // max 50MB
}

type DocAnalyzeResponse struct {
	OK           bool            `json:"ok"`
	Filename     string          `json:"filename"`
	FileSize     int64           `json:"file_size"`
	Pages        int             `json:"pages"`
	CharCount    int             `json:"char_count"`
	CleanCount   int             `json:"clean_count"`
	Elements     []DocElement    `json:"elements"`
	ElementStats DocElementStats `json:"element_stats"`
	Chunks       []DocChunk      `json:"chunks,omitempty"`
	Tables       []DocTable      `json:"tables,omitempty"`
	Images       []DocImage      `json:"images,omitempty"`
	Timing       DocTiming       `json:"timing"`
	Error        string          `json:"error,omitempty"`
}

type DocElement struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type DocElementStats struct {
	Title         int `json:"title"`
	SectionHeader int `json:"section_header"`
	SubHeader     int `json:"sub_header"`
	NarrativeText int `json:"narrative_text"`
	ListItem      int `json:"list_item"`
	Table         int `json:"table"`
	PageBreak     int `json:"page_break"`
	Uncategorized int `json:"uncategorized"`
}

type DocChunk struct {
	Index   int    `json:"index"`
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text"`
	Chars   int    `json:"chars"`
}

type DocTable struct {
	Page    int        `json:"page"`
	Index   int        `json:"index"`
	Rows    int        `json:"rows"`
	Cols    int        `json:"cols"`
	Headers []string   `json:"headers,omitempty"`
	Data    [][]string `json:"data"`
}

type DocImage struct {
	Page      int    `json:"page"`
	Name      string `json:"name"`
	Format    string `json:"format"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int    `json:"size_bytes"`
}

type DocTiming struct {
	ExtractMs  int64 `json:"extract_ms"`
	CleanMs    int64 `json:"clean_ms"`
	ClassifyMs int64 `json:"classify_ms"`
	ChunkMs    int64 `json:"chunk_ms"`
	TableMs    int64 `json:"table_ms"`
	ImageMs    int64 `json:"image_ms"`
	TotalMs    int64 `json:"total_ms"`
}

func (h *Handler) HandleAnalyze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeDocError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	t0 := time.Now()

	// Parse form
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		writeDocError(w, "File too large (max 50MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeDocError(w, "No file provided. Use multipart form with field 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Get optional params
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "full"
	}
	chunkStr := r.FormValue("chunk")
	doChunk := chunkStr == "true" || chunkStr == "1"
	if mode == "full" {
		doChunk = true
	}

	// Save to temp
	tmpDir := filepath.Join(os.TempDir(), "openkitty-doc-api")
	os.MkdirAll(tmpDir, 0755)
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("upload_%d_%s", time.Now().UnixNano(), header.Filename))
	dst, err := os.Create(tmpPath)
	if err != nil {
		writeDocError(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	written, _ := io.Copy(dst, file)
	dst.Close()
	defer os.Remove(tmpPath)

	log.Printf("[DocAPI] Analyzing: %s (%d bytes)", header.Filename, written)

	resp := DocAnalyzeResponse{
		OK:       true,
		Filename: header.Filename,
		FileSize: written,
	}

	// Detect file type
	cat := textutil.DetectFileType(tmpPath)
	if cat == textutil.CatUnknown {
		writeDocError(w, fmt.Sprintf("Unsupported file format: %s. Supported: PDF, DOCX, PPTX, EPUB, RTF, HTML, TXT, MD", filepath.Ext(header.Filename)), http.StatusBadRequest)
		return
	}

	// Step 1: Extract (route by format)
	t1 := time.Now()
	var doc *contenttools.PDoc
	var text string
	partitionDependencies := contenttools.PartitionDependencies{
		PDF: h.dependencies.ExtractPDF,
		DOCX: func(path string) string {
			return contenttools.ExtractDocxStructuredOrFallback(path, h.dependencies.ExtractDOCX)
		},
		XLSX: h.dependencies.ExtractXLSX,
	}

	switch cat {
	case textutil.CatPDF:
		// Pure Go extraction (CJK fixed via GetPlainText + cleanPlainText)
		if d, err := contenttools.OpenPDF(tmpPath); err == nil {
			doc = d
			resp.Pages = doc.NumPages()
			var out strings.Builder
			for i := 1; i <= doc.NumPages(); i++ {
				page := doc.Page(i)
				out.WriteString(fmt.Sprintf("\n--- Page %d ---\n\n", i))
				pageWidth, pageHeight := page.PageSize()
				out.WriteString(page.ExtractTextLayout(pageWidth, pageHeight))
			}
			text = out.String()
		}
	case textutil.CatDOCX:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatPPTX:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatEPUB:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatRTF:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatEmail:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatHTML, textutil.CatXML:
		data, _ := os.ReadFile(tmpPath)
		raw, _ := contenttools.ExtractHTML(data)
		text = raw
	case textutil.CatODT:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	case textutil.CatMarkdown, textutil.CatText, textutil.CatCSV, textutil.CatTSV, textutil.CatOrgMode, textutil.CatRST:
		data, _ := os.ReadFile(tmpPath)
		text = string(data)
	default:
		text, _ = contenttools.ExtractDocumentText(tmpPath, cat, partitionDependencies)
	}
	if text == "" {
		// Fallback: PartitionDoc
		if pdoc, err := contenttools.PartitionDoc(tmpPath, partitionDependencies); err == nil && pdoc != nil {
			text = textutil.FormatClassifiedElements(pdoc.Elements)
		}
	}
	if text == "" {
		writeDocError(w, "Failed to extract text from file", http.StatusInternalServerError)
		return
	}
	if doc != nil {
		defer doc.Close()
	}
	resp.CharCount = len(text)
	resp.Timing.ExtractMs = time.Since(t1).Milliseconds()
	logf := func(step string, s string) {
		r := []rune(s)
		preview := s
		if len(r) > 1000 {
			preview = string(r[:1000])
		}
		log.Printf("[DocAPI] %s: %d bytes, FF=%d", step, len(s), strings.Count(s, "�"))
		log.Printf("[DocAPI] %s preview: %q", step, preview)
	}
	logf("Step1-Extract", text)

	// Step 2: Clean
	t2 := time.Now()
	cleaned := textutil.CleanDocument(text)
	resp.CleanCount = len(cleaned)
	resp.Timing.CleanMs = time.Since(t2).Milliseconds()
	logf("Step2-Clean", cleaned)
	resp.CleanCount = len(cleaned)
	resp.Timing.CleanMs = time.Since(t2).Milliseconds()

	// Step 3: Classify
	t3 := time.Now()
	elements := textutil.ClassifyText(cleaned)
	ffTotal := 0
	for _, e := range elements {
		ffTotal += strings.Count(e.Text, "�")
	}
	log.Printf("[DocAPI] Step3-Classify: %d elements, FF=%d", len(elements), ffTotal)
	stats := DocElementStats{}
	for _, el := range elements {
		switch el.Type {
		case textutil.ElemTitle:
			stats.Title++
		case textutil.ElemSectionHeader:
			stats.SectionHeader++
		case textutil.ElemSubHeader:
			stats.SubHeader++
		case textutil.ElemNarrativeText:
			stats.NarrativeText++
		case textutil.ElemListItem:
			stats.ListItem++
		case textutil.ElemTable:
			stats.Table++
		case textutil.ElemPageBreak:
			stats.PageBreak++
		default:
			stats.Uncategorized++
		}
	}
	resp.ElementStats = stats
	for _, el := range elements {
		resp.Elements = append(resp.Elements, DocElement{Type: el.Type.String(), Text: el.Text})
	}
	resp.Timing.ClassifyMs = time.Since(t3).Milliseconds()

	// Step 4: Chunk (if requested)
	if doChunk {
		t4 := time.Now()
		cfg := textutil.DefaultUnstructChunkConfig()
		chunks := textutil.ChunkByTitle(elements, cfg)
		resp.Timing.ChunkMs = time.Since(t4).Milliseconds()
		chunkFF := 0
		for i, c := range chunks {
			ff := strings.Count(c.Text, "�")
			if ff > 0 {
				chunkFF += ff
				n := len(c.Text)
				if n > 100 {
					n = 100
				}
				log.Printf("[DocAPI] Chunk[%d] FF=%d, heading=%q, text[:100]=%q", i, ff, c.Heading, c.Text[:n])
			}
			resp.Chunks = append(resp.Chunks, DocChunk{
				Index: i + 1, Heading: c.Heading, Text: c.Text,
				Chars: len([]rune(c.Text)),
			})
		}
		if chunkFF > 0 {
			log.Printf("[DocAPI] Step4-Chunk: %d total U+FFFD across %d chunks", chunkFF, len(chunks))
		}
	}

	// Step 5: Tables
	if doc != nil {
		t5 := time.Now()
		if d, _ := contenttools.OpenPDF(tmpPath); d != nil {
			defer d.Close()
			for p := 1; p <= d.NumPages(); p++ {
				for _, t := range d.Page(p).ExtractTables() {
					dt := DocTable{Page: p, Index: len(resp.Tables) + 1, Rows: len(t.Rows)}
					if len(t.Rows) > 0 {
						dt.Cols = len(t.Rows[0])
						dt.Headers = t.Rows[0]
						if len(t.Rows) > 1 {
							dt.Data = t.Rows[1:]
						}
					}
					resp.Tables = append(resp.Tables, dt)
				}
			}
			resp.Timing.TableMs = time.Since(t5).Milliseconds()
		}
	}

	// Step 6: Images
	t6 := time.Now()
	if d, err := contenttools.OpenPDF(tmpPath); err == nil {
		for _, img := range d.AllImages() {
			resp.Images = append(resp.Images, DocImage{
				Page: img.PageNumber, Name: img.Name, Format: img.Format,
				Width: img.Width, Height: img.Height, SizeBytes: len(img.Data),
			})
		}
		d.Close()
		resp.Timing.ImageMs = time.Since(t6).Milliseconds()
	}

	resp.Timing.TotalMs = time.Since(t0).Milliseconds()
	log.Printf("[DocAPI] Done: %s → %d elements, %d chunks, %d tables, %d images (%dms)",
		header.Filename, len(elements), len(resp.Chunks), len(resp.Tables), len(resp.Images), resp.Timing.TotalMs)

	json.NewEncoder(w).Encode(resp)
}

func writeDocError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(DocAnalyzeResponse{OK: false, Error: msg})
}

// ====================== Quick text test API ======================

func (h *Handler) HandleClassify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeDocError(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text  string `json:"text"`
		Chunk bool   `json:"chunk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeDocError(w, "Missing 'text' field", http.StatusBadRequest)
		return
	}

	elements := textutil.ClassifyText(req.Text)
	stats := DocElementStats{}
	for _, el := range elements {
		switch el.Type {
		case textutil.ElemTitle:
			stats.Title++
		case textutil.ElemSectionHeader:
			stats.SectionHeader++
		case textutil.ElemSubHeader:
			stats.SubHeader++
		case textutil.ElemNarrativeText:
			stats.NarrativeText++
		case textutil.ElemListItem:
			stats.ListItem++
		case textutil.ElemTable:
			stats.Table++
		case textutil.ElemPageBreak:
			stats.PageBreak++
		default:
			stats.Uncategorized++
		}
	}

	resp := map[string]interface{}{"ok": true, "elements": len(elements), "stats": stats}
	if req.Chunk {
		cfg := textutil.DefaultUnstructChunkConfig()
		chunks := textutil.ChunkByTitle(elements, cfg)
		var cdata []DocChunk
		for i, c := range chunks {
			cdata = append(cdata, DocChunk{Index: i + 1, Heading: c.Heading, Text: c.Text, Chars: len([]rune(c.Text))})
		}
		resp["chunks"] = cdata
	}
	json.NewEncoder(w).Encode(resp)
}
