package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mac/internal/eventbus"
	ragcore "mac/internal/rag"
	"mac/internal/ragmodel"
	"mac/internal/textutil"
	contenttools "mac/internal/tools/content"
	knowledgetools "mac/internal/tools/knowledge"

	"github.com/ledongthuc/pdf"
	"github.com/xavier268/mydocx"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

type RAGKb = ragmodel.Kb
type RAGChunk = ragmodel.Chunk
type RAGFAQ = ragmodel.FAQ
type RAGSearchLog = ragmodel.SearchLog

// ====================== RAG API Helpers ======================

func NewExtractTool(workspace string, autoInstallDeps bool) *contenttools.ExtractTool {
	return contenttools.NewExtractTool(workspace, autoInstallDeps, contenttools.ExtractDependencies{
		EnforcePath:        enforcePersonalWorkspacePath,
		UnstructuredScript: getUnstructuredExtractScript,
		PDFScript:          getPdfExtractScript,
	})
}

func runPythonScript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", "-c", script)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.CommandContext(ctx, "python3", "-c", script)
		out, err = cmd.Output()
	}
	if err != nil {
		return "", fmt.Errorf("python: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ragTenant(r *http.Request) string {
	tid := r.Header.Get("X-Tenant-ID")
	if tid == "" {
		tid = "default"
	}
	return tid
}

// resolveKBSlugs converts KB slugs to IDs (tenant-scoped via composite unique index).
func resolveKBSlugs(slugs []string, tenantID string) []uint {
	if len(slugs) == 0 || gatewayDB == nil {
		return nil
	}
	var ids []uint
	for _, slug := range slugs {
		var kb RAGKb
		if err := gatewayDB.Where("slug = ? AND tenant_id = ?", slug, tenantID).First(&kb).Error; err == nil {
			ids = append(ids, kb.ID)
		}
	}
	return ids
}

func ragExtractText(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	switch ext {
	case ".txt", ".md", ".csv":
		return string(data), nil
	case ".pdf":
		return contenttools.ExtractPDFWithHeadings(filePath, ragExtractPDF), nil
	case ".docx":
		// Try Unstructured first (best quality: headings + tables preserved)
		if text := extractWithUnstructured(filePath); text != "" {
			return text, nil
		}
		// Try structured extraction (heading-aware), fall back to mydocx/python
		text := contenttools.ExtractDocxStructuredOrFallback(filePath, ragExtractDocxGo)
		if ragTextLooksGarbled(text) {
			// Structured may have failed; try python-docx as fallback
			if py, err := ragExtractDocxPython(filePath); err == nil && len(py) > len(text) {
				text = py
			}
		}
		return text, nil
	case ".xlsx":
		return ragExtractXLSX(data)
	case ".html", ".htm":
		return contenttools.ExtractHTML(data)
	case ".doc":
		return "", fmt.Errorf("不支持 .doc 格式（旧版 Word），请用 Word 另存为 .docx 后重新上传")
	default:
		return ragTryPlainText(data, ext)
	}
}

// pdfParserMode returns which PDF parser to use.
// Set via env OPENKITTY_PDF_PARSER: auto|unstructured|pdfplumber|pdfplumber_go|go|pdftotext
// Default "auto" tries all in quality order.
func pdfParserMode() string {
	m := os.Getenv("OPENKITTY_PDF_PARSER")
	switch strings.ToLower(m) {
	case "unstructured", "pdfplumber", "pdfplumber_go", "go", "pdftotext":
		return m
	default:
		return "auto"
	}
}

// ragExtractPDF extracts text from a PDF file, respecting OPENKITTY_PDF_PARSER.
// Parser priority in "auto" mode: unstructured > pdfplumber > go > pdftotext.
// Useful for comparing parser quality: set OPENKITTY_PDF_PARSER=go vs =unstructured.
func ragExtractPDF(filePath string) string {
	mode := pdfParserMode()
	var method string

	switch mode {
	case "unstructured":
		method = "unstructured"
		if text := extractWithUnstructured(filePath); text != "" {
			log.Printf("[RAG] PDF extracted via unstructured (%d chars)", len(text))
			return text
		}
		log.Printf("[RAG] unstructured failed, fallback to auto")
		// fall through to full chain
	case "pdfplumber_go":
		method = "pdfplumber_go"
		if text := extractWithPdfPlumberGo(filePath); text != "" {
			log.Printf("[RAG] PDF extracted via pdfplumber_go (%d chars)", len(text))
			return text
		}
		log.Printf("[RAG] pdfplumber_go failed, fallback to auto")
	case "pdfplumber":
		method = "pdfplumber"
		if text := extractWithPdfPlumber(filePath); text != "" {
			log.Printf("[RAG] PDF extracted via pdfplumber (%d chars)", len(text))
			return text
		}
		log.Printf("[RAG] pdfplumber failed, fallback to auto")
	case "go":
		method = "go"
		if text := extractPDFWithGoLib(filePath); text != "" {
			log.Printf("[RAG] PDF extracted via go (%d chars)", len(text))
			return text
		}
		log.Printf("[RAG] go parser failed, fallback to auto")
	case "pdftotext":
		method = "pdftotext"
		et := NewExtractTool(".", false)
		if t, err := et.ExtractPDFWithPdfToText(context.Background(), filePath); err == nil && t != "" {
			log.Printf("[RAG] PDF extracted via pdftotext (%d chars)", len(t))
			return t
		}
		log.Printf("[RAG] pdftotext failed, fallback to auto")
	}

	// "auto" mode or fallback from forced mode: try all in quality order
	_ = method
	if text := extractWithUnstructured(filePath); text != "" {
		return text
	}
	if text := extractWithPdfPlumberGo(filePath); text != "" {
		return text
	}
	if text := extractWithPdfPlumber(filePath); text != "" {
		return text
	}
	text := extractPDFWithGoLib(filePath)
	if textutil.IsTextGarbled(text) || strings.Count(text, "�") > len(text)/500 {
		et := NewExtractTool(".", false)
		if t, err := et.ExtractPDFWithPdfToText(context.Background(), filePath); err == nil && t != "" {
			return t
		}
	}
	if text != "" {
		return text
	}
	et := NewExtractTool(".", false)
	if t, err := et.ExtractPDFWithPdfToText(context.Background(), filePath); err == nil && t != "" {
		return t
	}
	return ""
}

// extractWithPdfPlumberGo uses the pure-Go pdfplumber port for layout-aware extraction.
func extractWithPdfPlumberGo(path string) string {
	text, err := contenttools.ExtractPDFWithLayout(path)
	if err != nil || len(text) < 50 {
		return ""
	}
	return text
}

// extractWithPdfPlumber runs pdfplumber Python script for high-quality PDF extraction.
// Uses the embedded tools/pdf_extract.py baked into the binary.
func extractWithPdfPlumber(path string) string {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return ""
	}
	script, cleanup, err := getPdfExtractScript()
	if err != nil {
		return ""
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[pdfplumber_py] ERROR: %v (script=%s)", err, script)
		return ""
	}
	text := strings.TrimSpace(string(out))
	log.Printf("[pdfplumber_py] OK: %d bytes, U+FFFD=%d", len(text), strings.Count(text, "�"))
	if len(text) < 50 {
		log.Printf("[pdfplumber_py] Too short: %d", len(text))
		return ""
	}
	return text
}

// extractWithUnstructured runs the Unstructured library for best-in-class document parsing.
// Handles PDF, DOCX, PPTX, HTML, TXT, MD and more with automatic format detection.
// Outputs structured markdown with heading markers and table preservation.
func extractWithUnstructured(path string) string {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return ""
	}
	script, cleanup, err := getUnstructuredExtractScript()
	if err != nil {
		return ""
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(out))
	if len(text) < 50 {
		return ""
	}
	return text
}

// getUnstructuredExtractScript returns path to unstructured_extract.py.
func getUnstructuredExtractScript() (string, func(), error) {
	exe, _ := os.Executable()
	return getEmbeddedScript(
		"unstructured-extract", embeddedUnstructuredExtractPy,
		filepath.Join(filepath.Dir(exe), "tools", "unstructured_extract.py"),
		"tools/unstructured_extract.py",
	)
}

// getEmbeddedScript returns a path to a Python script. Prefers disk copy (dev mode),
// falls back to writing the embedded content to a temp file.
// name is used for disk lookup and temp file naming.
func getEmbeddedScript(name, embeddedContent string, diskPaths ...string) (string, func(), error) {
	noop := func() {}
	// Prefer on-disk copy (development mode)
	for _, p := range diskPaths {
		if _, err := os.Stat(p); err == nil {
			return p, noop, nil
		}
	}
	// Fall back to embedded copy
	if embeddedContent == "" {
		return "", noop, fmt.Errorf("%s not embedded", name)
	}
	tmp, err := os.CreateTemp("", "openkitty-"+name+"-*.py")
	if err != nil {
		return "", noop, err
	}
	if _, err := tmp.WriteString(embeddedContent); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", noop, err
	}
	tmp.Close()
	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}

// getPdfExtractScript returns path to pdf_extract.py.
func getPdfExtractScript() (string, func(), error) {
	exe, _ := os.Executable()
	return getEmbeddedScript(
		"pdf-extract", embeddedPdfExtractPy,
		filepath.Join(filepath.Dir(exe), "tools", "pdf_extract.py"),
		"tools/pdf_extract.py",
	)
}

func extractPDFWithGoLib(filePath string) string {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader, err := r.GetPlainText()
	if err != nil {
		return ""
	}
	data, _ := io.ReadAll(reader)
	return strings.TrimSpace(string(data))
}

func ragExtractXLSX(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("xlsx parse: %w", err)
	}
	defer f.Close()
	var out strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		out.WriteString("## " + sheet + "\n\n")
		for _, row := range rows {
			var cells []string
			for _, cell := range row {
				if s := strings.TrimSpace(cell); s != "" {
					cells = append(cells, s)
				}
			}
			if len(cells) > 0 {
				out.WriteString(strings.Join(cells, " | ") + "\n")
			}
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

// ragExtractDocxGo extracts text from .docx using mydocx (MIT, zero deps, pure Go).
func ragExtractDocxGo(filePath string) string {
	m, err := mydocx.ExtractText(filePath)
	if err != nil {
		return ""
	}
	var out strings.Builder
	// Iterate sections in order: body first, then headers/footers
	for _, key := range []string{"body", "header", "footer"} {
		if paras, ok := m[key]; ok {
			for _, p := range paras {
				if s := strings.TrimSpace(p); s != "" {
					out.WriteString(s + "\n\n")
				}
			}
		}
	}
	// Include any other sections not in the predefined order
	for key, paras := range m {
		if key == "body" || key == "header" || key == "footer" {
			continue
		}
		for _, p := range paras {
			if s := strings.TrimSpace(p); s != "" {
				out.WriteString(s + "\n\n")
			}
		}
	}
	return strings.TrimSpace(out.String())
}

// ragExtractDocxPython uses python-docx or docx2txt as a fallback.
func ragExtractDocxPython(filePath string) (string, error) {
	script := fmt.Sprintf(`
import sys
text = ""
try:
    import docx
    doc = docx.Document(r'%s')
    paras = [p.text for p in doc.paragraphs if p.text.strip()]
    text = '\n\n'.join(paras)
except ImportError:
    try:
        import subprocess
        r = subprocess.run(['python', '-m', 'docx2txt', r'%s'], capture_output=True, text=True, timeout=60)
        if r.returncode == 0:
            text = r.stdout
    except:
        pass
if not text.strip():
    sys.exit(1)
print(text)
`, filePath, filePath)
	return runPythonScript(script)
}

// ragTextLooksGarbled returns true if text looks like garbage (not valid text).
func ragTextLooksGarbled(text string) bool {
	if len(text) < 50 {
		return true // too short, likely failed
	}
	// Count valid text characters (Chinese, English, common punctuation, whitespace)
	runes := []rune(text)
	valid := 0
	for _, r := range runes {
		if r >= 0x4e00 && r <= 0x9fff || // CJK
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || // Latin
			r >= '0' && r <= '9' || // digits
			r == ' ' || r == '\n' || r == '\t' || r == '\r' || // whitespace
			r == '.' || r == ',' || r == '!' || r == '?' || r == ';' || r == ':' || // punctuation
			r == '。' || r == '，' || r == '！' || r == '？' || r == '；' || r == '：' || // Chinese punct
			r == '(' || r == ')' || r == '（' || r == '）' || r == '"' || r == '\'' ||
			r == '-' || r == '_' || r == '/' || r == '|' || r == '#' || r == '%' {
			valid++
		}
	}
	ratio := float64(valid) / float64(len(runes))
	return ratio < 0.6 // less than 60%% valid text = likely garbled
}

func ragTryPlainText(data []byte, ext string) (string, error) {
	nonPrintable := 0
	for _, b := range data {
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
	}
	if len(data) > 0 && float64(nonPrintable)/float64(len(data)) > 0.3 {
		return "", fmt.Errorf("无法提取 %s 文件内容（疑似二进制格式），请转为 .txt .md .pdf .docx .xlsx 后上传", ext)
	}
	return string(data), nil
}

func ragFilePath(kbID uint, filename string) string {
	dir := ragKbDir(kbID)
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, filename)
}

func ragKbDir(kbID uint) string {
	return filepath.Join("knowledge", "rag", fmt.Sprintf("%d", kbID), "files")
}

// ====================== FTS5 Sync ======================

func ragSyncChunkFTS(db *gorm.DB, chunk RAGChunk) {
	db.Exec("INSERT INTO rag_chunks_fts(rowid, kb_id, source_doc, content) VALUES(?,?,?,?)",
		chunk.ID, chunk.KbID, chunk.SourceDoc, chunk.Content)
}

func ragDeleteChunksFTS(db *gorm.DB, ids []uint) {
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		db.Exec("INSERT INTO rag_chunks_fts(rag_chunks_fts, rowid, kb_id, source_doc, content) VALUES('delete',?,?,?,?)",
			id, 0, "", "")
	}
}

func ragSearchFTS(db *gorm.DB, kbIDs []uint, query string, limit int) ([]RAGSearchResult, error) {
	var rows *gorm.DB
	if len(kbIDs) > 0 {
		// FTS5 MATCH on kb_id can be done via content filter after search
		placeholders := make([]string, len(kbIDs))
		args := make([]interface{}, len(kbIDs)+2)
		args[0] = query
		for i, id := range kbIDs {
			placeholders[i] = "?"
			args[i+1] = id
		}
		args[len(args)-1] = limit
		rows = db.Raw("SELECT rowid, kb_id, source_doc, content, bm25(rag_chunks_fts) as score FROM rag_chunks_fts WHERE rag_chunks_fts MATCH ? AND kb_id IN ("+strings.Join(placeholders, ",")+") ORDER BY score LIMIT ?", args...)
	} else {
		rows = db.Raw("SELECT rowid, kb_id, source_doc, content, bm25(rag_chunks_fts) as score FROM rag_chunks_fts WHERE rag_chunks_fts MATCH ? ORDER BY score LIMIT ?", query, limit)
	}

	type ftsRow struct {
		RowID     uint    `gorm:"column:rowid"`
		KbID      uint    `gorm:"column:kb_id"`
		SourceDoc string  `gorm:"column:source_doc"`
		Content   string  `gorm:"column:content"`
		Score     float64 `gorm:"column:score"`
	}
	var ftsResults []ftsRow
	if err := rows.Scan(&ftsResults).Error; err != nil {
		return nil, err
	}

	results := make([]RAGSearchResult, 0, len(ftsResults))
	for _, r := range ftsResults {
		results = append(results, RAGSearchResult{
			SourceDoc:  r.SourceDoc,
			ChunkIndex: int(r.RowID),
			Content:    r.Content,
			Score:      r.Score,
		})
	}
	return results, nil
}

type RAGSearchResult = knowledgetools.RAGResult

// ====================== KB CRUD ======================

func (g *GatewayService) handleRAGKbList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var kbs []RAGKb
	q := g.db.Order("updated_at DESC")
	if tid != "" {
		q = q.Where("tenant_id = ?", tid)
	}
	q.Find(&kbs)
	if kbs == nil {
		kbs = []RAGKb{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"kbs": kbs, "ok": true})
}

func (g *GatewayService) handleRAGKbCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Name) == "" {
		http.Error(w, `{"error":"板块名称不能为空"}`, 400)
		return
	}
	// Generate slug: tenant_name + "_" + kb_name, lowercase, alphanum+underscore only
	slug := tid + "_" + body.Name
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, strings.ToLower(slug))
	// Collapse consecutive underscores and trim
	for strings.Contains(slug, "__") {
		slug = strings.ReplaceAll(slug, "__", "_")
	}
	slug = strings.Trim(slug, "_-")
	if slug == "" {
		slug = fmt.Sprintf("%s_kb_%d", tid, time.Now().Unix())
	}

	kb := &RAGKb{
		Slug:        slug,
		Name:        strings.TrimSpace(body.Name),
		Description: body.Description,
		TenantID:    tid,
		BuildStatus: "idle",
	}
	g.db.Create(kb)
	ragKbDir(kb.ID)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "kb": kb})
}

func (g *GatewayService) handleRAGKbDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	id, _ := strconv.Atoi(idStr)
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", id, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	// Remove from FTS5 first
	var chunkIDs []uint
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Pluck("id", &chunkIDs)
	ragDeleteChunksFTS(g.db, chunkIDs)
	g.db.Where("kb_id = ?", kb.ID).Delete(&RAGChunk{})
	os.RemoveAll(filepath.Join("knowledge", "rag", fmt.Sprintf("%d", kb.ID)))
	g.db.Delete(&kb)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (g *GatewayService) handleRAGKbRebuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	idStr = strings.TrimSuffix(idStr, "/rebuild")
	id, _ := strconv.Atoi(idStr)
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", id, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	g.db.Model(&kb).Update("build_status", "building 0%")

	// Snapshot before rebuild
	g.createKBSnapshot(kb.ID)
	// Clean Bleve + FTS5 + DB before rebuild
	ragcore.DeleteKBChunks(kb.ID)
	var chunkIDs []uint
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Pluck("id", &chunkIDs)
	ragDeleteChunksFTS(g.db, chunkIDs)

	filesDir := ragKbDir(kb.ID)
	entries, err := os.ReadDir(filesDir)
	docCount, chunkCount := 0, 0
	cfg := knowledgetools.DefaultChunkConfig()
	if err == nil {
		totalFiles := 0
		for _, e := range entries {
			if !e.IsDir() {
				totalFiles++
			}
		}
		processed := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fp := filepath.Join(filesDir, e.Name())
			// Try AST-aware chunking for code files
			var ragChunks []RAGChunk
			if data, readErr := os.ReadFile(fp); readErr == nil {
				if csChunks, csErr := ragmodel.ChunkCodeFile(fp, data); csErr == nil && len(csChunks) > 0 {
					ragChunks = ragmodel.ChiselToChunks(kb.ID, e.Name(), csChunks)
				}
			}

			// Fall back to text chunking
			if len(ragChunks) == 0 {
				text, err := ragExtractText(fp)
				if err != nil {
					processed++
					continue
				}
				for i, c := range knowledgetools.Chunk(text, cfg) {
					if strings.TrimSpace(c.Content) == "" {
						continue
					}
					ragChunks = append(ragChunks, RAGChunk{
						KbID: kb.ID, SourceDoc: e.Name(),
						ChunkIndex: i, Heading: c.Heading, Content: strings.TrimSpace(c.Content),
					})
				}
			}

			for i := range ragChunks {
				ragChunks[i].KbID = kb.ID
				ragChunks[i].SourceDoc = e.Name()
				ragChunks[i].ChunkIndex = i
				if err := ragcore.SaveChunk(&ragChunks[i]); err != nil {
					log.Printf("[RAG] SaveChunk error (kb=%d doc=%s chunk=%d): %v", kb.ID, e.Name(), i, err)
				}
				ragSyncChunkFTS(g.db, ragChunks[i])
			}
			docCount++
			chunkCount += len(ragChunks)
			processed++
			// Update progress every file
			if totalFiles > 0 {
				pct := processed * 100 / totalFiles
				g.db.Model(&kb).Update("build_status", fmt.Sprintf("building %d%%", pct))
			}
		}
	}
	g.db.Model(&kb).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount,
		"build_status": "ready", "updated_at": time.Now(),
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "doc_count": docCount, "chunk_count": chunkCount,
	})
	eventbus.PushSystemMessage(fmt.Sprintf("KB %s rebuild: %d docs, %d chunks", kb.Name, docCount, chunkCount), "success")
}

// ====================== Sync Index ======================
func (g *GatewayService) handleRAGSyncIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	idStr = strings.TrimSuffix(idStr, "/sync-index")
	id, _ := strconv.Atoi(idStr)
	n, err := ragcore.SyncIndex(uint(id))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "indexed": n})
}

// ====================== Doc Upload / Delete ======================

func (g *GatewayService) handleRAGDocUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	r.ParseMultipartForm(64 << 20)
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"请选择文件"}`, 400)
		return
	}
	defer file.Close()

	kbIDStr := r.FormValue("kb_id")
	kbID, _ := strconv.Atoi(kbIDStr)
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", kbID, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}

	filename := header.Filename
	savePath := ragFilePath(kb.ID, filename)
	if _, err := os.Stat(savePath); err == nil {
		var chunkIDs []uint
		g.db.Model(&RAGChunk{}).Where("kb_id = ? AND source_doc = ?", kb.ID, filename).Pluck("id", &chunkIDs)
		ragDeleteChunksFTS(g.db, chunkIDs)
		g.db.Where("kb_id = ? AND source_doc = ?", kb.ID, filename).Delete(&RAGChunk{})
	}

	data, _ := io.ReadAll(file)

	// Duplicate detection
	h := sha256.Sum256(data)
	fileHash := hex.EncodeToString(h[:])
	var existingCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ? AND file_hash = ?", kb.ID, fileHash).Count(&existingCount)
	if existingCount > 0 {
		http.Error(w, `{"error":"duplicate document: same content already exists in this KB (use force=true to override)"}`, 409)
		return
	}
	os.WriteFile(savePath, data, 0644)

	// Try AST-aware chunking for code files first
	var ragChunks []RAGChunk
	if data, err := os.ReadFile(savePath); err == nil {
		if csChunks, err := ragmodel.ChunkCodeFile(savePath, data); err == nil && len(csChunks) > 0 {
			ragChunks = ragmodel.ChiselToChunks(kb.ID, filename, csChunks)
			log.Printf("[RAG] AST chunked %s: %d chunks", filename, len(ragChunks))
		}
	}

	// Fall back to text chunking
	if len(ragChunks) == 0 {
		text, err := ragExtractText(savePath)
		if err != nil {
			os.Remove(savePath)
			http.Error(w, fmt.Sprintf(`{"error":"文档解析失败: %s"}`, err.Error()), 500)
			return
		}
		cfg := knowledgetools.ChunkConfigForFile(filename)
		for i, c := range knowledgetools.Chunk(text, cfg) {
			if strings.TrimSpace(c.Content) == "" {
				continue
			}
			ragChunks = append(ragChunks, RAGChunk{
				KbID: kb.ID, SourceDoc: filename, FileHash: fileHash,
				ChunkIndex: i, Heading: c.Heading, Content: strings.TrimSpace(c.Content),
			})
		}
	}

	chunkN := 0
	for i := range ragChunks {
		ragChunks[i].KbID = kb.ID
		ragChunks[i].SourceDoc = filename
		ragChunks[i].ChunkIndex = i
		if err := ragcore.SaveChunk(&ragChunks[i]); err != nil {
			log.Printf("[RAG] SaveChunk error (kb=%d doc=%s chunk=%d): %v", kb.ID, filename, i, err)
		}
		ragSyncChunkFTS(g.db, ragChunks[i])
		chunkN++
	}

	var docCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
	var chunkCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Count(&chunkCount)
	g.db.Model(&kb).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount,
		"build_status": "ready", "updated_at": time.Now(),
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "filename": filename, "chunks": chunkN, "kb_name": kb.Name,
	})
}

func (g *GatewayService) handleRAGDocDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/rag/doc/"), "/", 2)
	if len(parts) < 2 {
		http.Error(w, `{"error":"invalid path"}`, 400)
		return
	}
	kbID, _ := strconv.Atoi(parts[0])
	filename := parts[1]
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", kbID, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	os.Remove(ragFilePath(kb.ID, filename))
	var chunkIDs []uint
	g.db.Model(&RAGChunk{}).Where("kb_id = ? AND source_doc = ?", kb.ID, filename).Pluck("id", &chunkIDs)
	ragDeleteChunksFTS(g.db, chunkIDs)
	g.db.Where("kb_id = ? AND source_doc = ?", kb.ID, filename).Delete(&RAGChunk{})

	var docCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
	var chunkCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Count(&chunkCount)
	g.db.Model(&kb).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount, "updated_at": time.Now(),
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// ====================== Files & Chunks ======================

func (g *GatewayService) handleRAGKbFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	idStr = strings.TrimSuffix(idStr, "/files")
	id, _ := strconv.Atoi(idStr)
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", id, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	var docs []struct {
		SourceDoc string `json:"source_doc"`
		Chunks    int    `json:"chunks"`
	}
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).
		Select("source_doc, COUNT(*) as chunks").Group("source_doc").Scan(&docs)
	if docs == nil {
		docs = []struct {
			SourceDoc string `json:"source_doc"`
			Chunks    int    `json:"chunks"`
		}{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"docs": docs, "ok": true})
}

func (g *GatewayService) handleRAGDocChunks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	idStr = strings.TrimSuffix(idStr, "/chunks")
	id, _ := strconv.Atoi(idStr)
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", id, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	doc := r.URL.Query().Get("doc")
	var chunks []RAGChunk
	g.db.Where("kb_id = ? AND source_doc = ?", kb.ID, doc).Order("chunk_index").Find(&chunks)
	if chunks == nil {
		chunks = []RAGChunk{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"chunks": chunks, "ok": true})
}

// ====================== RAG Chat/Search (FTS5 BM25) ======================

func (g *GatewayService) handleRAGChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body struct {
		KBIDs []uint `json:"kb_ids"`
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Query == "" {
		http.Error(w, `{"error":"query required"}`, 400)
		return
	}
	if body.TopK <= 0 || body.TopK > 10 {
		body.TopK = 5
	}
	results, err := ragcore.CachedSearchHybrid(body.KBIDs, body.Query, "", "", body.TopK)
	if err != nil || results == nil {
		results = []RAGSearchResult{}
	}
	tokens := ragmodel.SegTextForSearch(ragmodel.RewriteQuery(body.Query))
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results, "tokens": tokens, "ok": true,
	})
}

// ====================== FAQ CRUD ======================

func (g *GatewayService) handleRAGFAQList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	kbIDStr := r.URL.Query().Get("kb_id")
	var faqs []RAGFAQ
	q := g.db.Order("hit_count DESC")
	if tid != "" {
		q = q.Where("tenant_id = ?", tid)
	}
	if kbIDStr != "" {
		q = q.Where("kb_id = ?", kbIDStr)
	}
	q.Find(&faqs)
	if faqs == nil {
		faqs = []RAGFAQ{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"faqs": faqs, "ok": true})
}

func (g *GatewayService) handleRAGFAQCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		KbID     uint   `json:"kb_id"`
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Question == "" || body.Answer == "" {
		http.Error(w, `{"error":"question and answer required"}`, 400)
		return
	}
	faq := RAGFAQ{
		KbID: body.KbID, Question: body.Question,
		Answer: body.Answer, TenantID: tid,
	}
	g.db.Create(&faq)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "faq": faq})
}

func (g *GatewayService) handleRAGFAQDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/faq/")
	id, _ := strconv.Atoi(idStr)
	g.db.Delete(&RAGFAQ{}, id)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// ====================== Plain Text Entry ======================

func (g *GatewayService) handleRAGTextAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		KbID    uint   `json:"kb_id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Content == "" {
		http.Error(w, `{"error":"content required"}`, 400)
		return
	}
	if body.Title == "" {
		body.Title = fmt.Sprintf("note_%d", time.Now().Unix())
	}

	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", body.KbID, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}

	// Save as .txt file
	filename := body.Title + ".txt"
	savePath := ragFilePath(kb.ID, filename)
	// Overwrite if exists: clean old chunks
	if _, err := os.Stat(savePath); err == nil {
		var oldIDs []uint
		g.db.Model(&RAGChunk{}).Where("kb_id = ? AND source_doc = ?", kb.ID, filename).Pluck("id", &oldIDs)
		ragDeleteChunksFTS(g.db, oldIDs)
		ragcore.DeleteChunks(kb.ID, filename)
	}
	os.WriteFile(savePath, []byte(body.Content), 0644)

	// Chunk and index
	chunks := knowledgetools.Chunk(body.Content, knowledgetools.DefaultChunkConfig())
	chunkN := 0
	for i, c := range chunks {
		if strings.TrimSpace(c.Content) == "" {
			continue
		}
		ch := RAGChunk{
			KbID: kb.ID, SourceDoc: filename,
			ChunkIndex: i, Heading: c.Heading, Content: strings.TrimSpace(c.Content),
		}
		if err := ragcore.SaveChunk(&ch); err != nil {
			log.Printf("[RAG] SaveChunk error: %v", err)
		}
		ragSyncChunkFTS(g.db, ch)
		chunkN++
	}

	var docCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
	var chunkCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Count(&chunkCount)
	g.db.Model(&kb).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount,
		"build_status": "ready", "updated_at": time.Now(),
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "filename": filename, "chunks": chunkN,
	})
}

// ====================== Directory Import ======================

func (g *GatewayService) handleRAGImportDir(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		KbID uint   `json:"kb_id"`
		Dir  string `json:"dir"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", body.KbID, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}

	if body.Dir == "" {
		http.Error(w, `{"error":"dir required"}`, 400)
		return
	}

	entries, err := os.ReadDir(body.Dir)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"读取目录失败: %s"}`, err.Error()), 500)
		return
	}

	imported, failed := 0, 0
	supported := map[string]bool{".txt": true, ".md": true, ".pdf": true, ".docx": true, ".xlsx": true, ".csv": true, ".html": true, ".htm": true, ".go": true}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supported[ext] {
			continue
		}
		srcPath := filepath.Join(body.Dir, e.Name())
		data, err := os.ReadFile(srcPath)
		if err != nil {
			failed++
			continue
		}
		dstPath := ragFilePath(kb.ID, e.Name())
		os.WriteFile(dstPath, data, 0644)

		// Try AST chunking first, then text
		var ragChunks []RAGChunk
		if csChunks, csErr := ragmodel.ChunkCodeFile(dstPath, data); csErr == nil && len(csChunks) > 0 {
			ragChunks = ragmodel.ChiselToChunks(kb.ID, e.Name(), csChunks)
		} else {
			text, _ := ragExtractText(dstPath)
			cfg := knowledgetools.ChunkConfigForFile(e.Name())
			for i, c := range knowledgetools.Chunk(text, cfg) {
				if strings.TrimSpace(c.Content) == "" {
					continue
				}
				ragChunks = append(ragChunks, RAGChunk{
					KbID: kb.ID, SourceDoc: e.Name(),
					ChunkIndex: i, Heading: c.Heading, Content: strings.TrimSpace(c.Content),
				})
			}
		}
		for i := range ragChunks {
			ragChunks[i].ChunkIndex = i
			ragcore.SaveChunk(&ragChunks[i])
			ragSyncChunkFTS(g.db, ragChunks[i])
		}
		imported++
	}

	var docCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
	var chunkCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", kb.ID).Count(&chunkCount)
	g.db.Model(&kb).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount,
		"build_status": "ready", "updated_at": time.Now(),
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "imported": imported, "failed": failed,
	})
}

// ====================== Search Analytics ======================

func (g *GatewayService) handleRAGSearchLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	kbIDStr := r.URL.Query().Get("kb_id")
	limit := 50
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 200 {
		limit = n
	}
	var logs []RAGSearchLog
	q := g.db.Order("created_at DESC").Limit(limit)
	if tid != "" {
		q = q.Where("tenant_id = ?", tid)
	}
	if kbIDStr != "" {
		q = q.Where("kb_id = ?", kbIDStr)
	}
	q.Find(&logs)
	if logs == nil {
		logs = []RAGSearchLog{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"logs": logs, "ok": true})
}

func (g *GatewayService) handleRAGZeroResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var queries []struct {
		Query string `json:"query"`
		Cnt   int    `json:"cnt"`
	}
	g.db.Model(&RAGSearchLog{}).
		Select("query, COUNT(*) as cnt").
		Where("hit_count = 0").
		Group("query").Order("cnt DESC").Limit(20).
		Find(&queries)
	// tenant filter (post-group)
	if tid != "" {
		filtered := make([]struct {
			Query string `json:"query"`
			Cnt   int    `json:"cnt"`
		}, 0)
		for _, q := range queries {
			var log RAGSearchLog
			if g.db.Where("query = ? AND tenant_id = ?", q.Query, tid).First(&log).Error == nil {
				filtered = append(filtered, q)
			}
		}
		queries = filtered
	}
	if queries == nil {
		queries = []struct {
			Query string `json:"query"`
			Cnt   int    `json:"cnt"`
		}{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"zero_queries": queries, "ok": true})
}

// ====================== Rechunk Single Doc ======================

func (g *GatewayService) handleRAGKbRechunk(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/rag/kb/")
	idStr = strings.TrimSuffix(idStr, "/rechunk")
	id, _ := strconv.Atoi(idStr)
	doc := r.URL.Query().Get("doc")
	if doc == "" {
		http.Error(w, `{"error":"doc required"}`, 400)
		return
	}
	var kb RAGKb
	if g.db.Where("id = ? AND tenant_id = ?", id, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"板块不存在"}`, 404)
		return
	}
	fp := ragFilePath(kb.ID, doc)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		http.Error(w, `{"error":"文档不存在"}`, 404)
		return
	}
	// Clean old chunks for this doc
	var oldIDs []uint
	g.db.Model(&RAGChunk{}).Where("kb_id = ? AND source_doc = ?", kb.ID, doc).Pluck("id", &oldIDs)
	ragDeleteChunksFTS(g.db, oldIDs)
	ragcore.DeleteChunks(kb.ID, doc)

	// Try AST-aware chunking for code files
	var ragChunks []RAGChunk
	if data, readErr := os.ReadFile(fp); readErr == nil {
		if csChunks, csErr := ragmodel.ChunkCodeFile(fp, data); csErr == nil && len(csChunks) > 0 {
			ragChunks = ragmodel.ChiselToChunks(kb.ID, doc, csChunks)
		}
	}
	if len(ragChunks) == 0 {
		text, err := ragExtractText(fp)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"解析失败: %s"}`, err.Error()), 500)
			return
		}
		cfg := knowledgetools.ChunkConfigForFile(doc)
		for i, c := range knowledgetools.Chunk(text, cfg) {
			if strings.TrimSpace(c.Content) == "" {
				continue
			}
			ragChunks = append(ragChunks, RAGChunk{
				KbID: kb.ID, SourceDoc: doc,
				ChunkIndex: i, Heading: c.Heading, Content: strings.TrimSpace(c.Content),
			})
		}
	}
	chunkN := 0
	for i := range ragChunks {
		ragChunks[i].KbID = kb.ID
		ragChunks[i].SourceDoc = doc
		ragChunks[i].ChunkIndex = i
		if err := ragcore.SaveChunk(&ragChunks[i]); err != nil {
			log.Printf("[RAG] SaveChunk error (kb=%d doc=%s chunk=%d): %v", kb.ID, doc, i, err)
		}
		ragSyncChunkFTS(g.db, ragChunks[i])
		chunkN++
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "chunks": chunkN})
}

// ====================== FAQ Batch Import ======================

func (g *GatewayService) handleRAGFAQImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		KbID uint `json:"kb_id"`
		FAQs []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
		} `json:"faqs"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.KbID == 0 || len(body.FAQs) == 0 {
		http.Error(w, `{"error":"kb_id and faqs required"}`, 400)
		return
	}
	imported := 0
	for _, f := range body.FAQs {
		if f.Question == "" || f.Answer == "" {
			continue
		}
		faq := RAGFAQ{
			KbID: body.KbID, Question: f.Question,
			Answer: f.Answer, TenantID: tid,
		}
		g.db.Create(&faq)
		imported++
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "imported": imported})
}

// ====================== Router ======================

// handleRAGFeedback records user feedback for a search result.
func (g *GatewayService) handleRAGFeedback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body struct {
		Query    string `json:"query"`
		Feedback string `json:"feedback"`
		KbID     uint   `json:"kb_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Query == "" || (body.Feedback != "up" && body.Feedback != "down") {
		http.Error(w, `{"error":"query and feedback (up/down) required"}`, 400)
		return
	}
	var log RAGSearchLog
	if err := g.db.Where("query = ? AND kb_id = ?", body.Query, body.KbID).Order("created_at DESC").First(&log).Error; err == nil {
		g.db.Model(&log).Update("feedback", body.Feedback)
	} else {
		g.db.Create(&RAGSearchLog{
			KbID: body.KbID, Query: body.Query, HitCount: 1,
			Feedback: body.Feedback, TenantID: ragTenant(r),
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleRAGKbExport exports a knowledge base as a JSON dump.
func (g *GatewayService) handleRAGKbExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	kbIDStr := r.URL.Query().Get("kb_id")
	kbID, _ := strconv.Atoi(kbIDStr)
	var kb RAGKb
	if g.db.First(&kb, kbID).Error != nil {
		http.Error(w, `{"error":"KB not found"}`, 404)
		return
	}
	var chunks []RAGChunk
	g.db.Where("kb_id = ?", kbID).Order("chunk_index").Find(&chunks)
	var faqs []RAGFAQ
	g.db.Where("kb_id = ?", kbID).Find(&faqs)
	// Load vectors
	vectors := knowledgetools.SnapshotVectors(uint(kbID))
	if chunks == nil {
		chunks = []RAGChunk{}
	}
	if faqs == nil {
		faqs = []RAGFAQ{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "kb": kb, "chunks": chunks, "faqs": faqs, "vectors": vectors,
	})
}

// handleRAGKbImport imports a knowledge base from a JSON dump.
func (g *GatewayService) handleRAGKbImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	var body struct {
		KB      RAGKb                        `json:"kb"`
		Chunks  []RAGChunk                   `json:"chunks"`
		FAQs    []RAGFAQ                     `json:"faqs"`
		Vectors []knowledgetools.VectorEntry `json:"vectors"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid json: %s"}`, err.Error()), 400)
		return
	}
	if body.KB.Name == "" {
		http.Error(w, `{"error":"kb.name required"}`, 400)
		return
	}
	// Create KB with new ID
	body.KB.ID = 0
	body.KB.TenantID = tid
	body.KB.BuildStatus = "ready"
	g.db.Create(&body.KB)
	ragKbDir(body.KB.ID)

	// Import chunks
	chunkCount := 0
	for i := range body.Chunks {
		body.Chunks[i].ID = 0
		body.Chunks[i].KbID = body.KB.ID
		g.db.Create(&body.Chunks[i])
		ragSyncChunkFTS(g.db, body.Chunks[i])
		ragcore.SaveChunk(&body.Chunks[i])
		chunkCount++
	}

	// Import FAQs
	faqCount := 0
	for i := range body.FAQs {
		body.FAQs[i].ID = 0
		body.FAQs[i].KbID = body.KB.ID
		body.FAQs[i].TenantID = tid
		g.db.Create(&body.FAQs[i])
		faqCount++
	}

	// Import vectors
	vecCount := 0
	if len(body.Vectors) > 0 {
		for i := range body.Vectors {
			body.Vectors[i].KBID = body.KB.ID
		}
		knowledgetools.AddVectors(body.KB.ID, body.Vectors)
		knowledgetools.SaveVectorIndex(body.KB.ID)
		vecCount = len(body.Vectors)
	}

	// Update counts
	var docCount int64
	g.db.Model(&RAGChunk{}).Where("kb_id = ?", body.KB.ID).Select("COUNT(DISTINCT source_doc)").Scan(&docCount)
	g.db.Model(&body.KB).Updates(map[string]interface{}{
		"doc_count": docCount, "chunk_count": chunkCount,
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "kb_id": body.KB.ID, "chunks": chunkCount, "faqs": faqCount, "vectors": vecCount,
	})
}

// handleRAGAnalytics returns aggregated stats for the analytics dashboard.
func (g *GatewayService) handleRAGAnalytics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	kbIDStr := r.URL.Query().Get("kb_id")
	var kbID uint
	if kbIDStr != "" {
		fmt.Sscanf(kbIDStr, "%d", &kbID)
	}

	type dayStat struct {
		Date  string `json:"date"`
		Total int    `json:"total"`
		Hits  int    `json:"hits"`
		Zeros int    `json:"zeros"`
	}
	var daily []dayStat
	if g.db != nil {
		q := g.db.Model(&RAGSearchLog{}).Select("DATE(created_at) as date, COUNT(*) as total, SUM(CASE WHEN hit_count > 0 THEN 1 ELSE 0 END) as hits, SUM(CASE WHEN hit_count = 0 THEN 1 ELSE 0 END) as zeros").Group("DATE(created_at)").Order("date DESC").Limit(7)
		if kbID > 0 {
			q = q.Where("kb_id = ?", kbID)
		}
		q.Scan(&daily)
	}

	type topQuery struct {
		Query string `json:"query"`
		Cnt   int    `json:"cnt"`
		Zeros int    `json:"zeros"`
	}
	var topQueries []topQuery
	if g.db != nil {
		q := g.db.Model(&RAGSearchLog{}).Select("query, COUNT(*) as cnt, SUM(CASE WHEN hit_count = 0 THEN 1 ELSE 0 END) as zeros").Group("query").Order("cnt DESC").Limit(10)
		if kbID > 0 {
			q = q.Where("kb_id = ?", kbID)
		}
		q.Scan(&topQueries)
	}

	type feedbackStat struct {
		Up   int `json:"up"`
		Down int `json:"down"`
	}
	var feedback feedbackStat
	if g.db != nil {
		q := g.db.Model(&RAGSearchLog{}).Select("SUM(CASE WHEN feedback = 'up' THEN 1 ELSE 0 END) as up, SUM(CASE WHEN feedback = 'down' THEN 1 ELSE 0 END) as down")
		if kbID > 0 {
			q = q.Where("kb_id = ?", kbID)
		}
		q.Scan(&feedback)
	}

	// DE stats
	type deSummary struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	}
	var deStats deSummary
	if g.aeManager != nil {
		g.aeManager.mu.RLock()
		deStats.Total = len(g.aeManager.employees)
		for id := range g.aeManager.employees {
			if wl, ok := g.aeManager.workLog[id]; ok {
				for _, w := range wl {
					if w.Status == "completed" {
						deStats.Completed++
					}
					if w.Status == "failed" {
						deStats.Failed++
					}
				}
			}
		}
		g.aeManager.mu.RUnlock()
	}

	if daily == nil {
		daily = []dayStat{}
	}
	if topQueries == nil {
		topQueries = []topQuery{}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "daily": daily, "top_queries": topQueries,
		"feedback": feedback, "de_stats": deStats,
	})
}

// handleRAGFAQImportCSV imports FAQs from a CSV file (columns: question, answer).
func (g *GatewayService) handleRAGFAQImportCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tid := ragTenant(r)
	r.ParseMultipartForm(16 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file required"}`, 400)
		return
	}
	defer file.Close()
	kbIDStr := r.FormValue("kb_id")
	kbID, _ := strconv.Atoi(kbIDStr)

	var kb RAGKb
	if kbID > 0 && g.db.Where("id = ? AND tenant_id = ?", kbID, tid).First(&kb).Error != nil {
		http.Error(w, `{"error":"KB not found"}`, 404)
		return
	}

	reader := csv.NewReader(file)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CSV parse: %s"}`, err.Error()), 400)
		return
	}

	imported := 0
	for i, row := range records {
		if i == 0 {
			continue
		} // skip header
		if len(row) < 2 {
			continue
		}
		q, a := strings.TrimSpace(row[0]), strings.TrimSpace(row[1])
		if q == "" || a == "" {
			continue
		}
		faq := RAGFAQ{KbID: uint(kbID), Question: q, Answer: a, TenantID: tid}
		g.db.Create(&faq)
		ragcore.EmbedFAQOnSave(&faq)
		imported++
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "imported": imported})
}

// handleRAGDocPreview returns extracted text content of a document for preview.
func (g *GatewayService) handleRAGDocPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	kbIDStr := r.URL.Query().Get("kb_id")
	doc := r.URL.Query().Get("doc")
	if kbIDStr == "" || doc == "" {
		http.Error(w, `{"error":"kb_id and doc required"}`, 400)
		return
	}
	kbID, _ := strconv.Atoi(kbIDStr)
	fp := ragFilePath(uint(kbID), doc)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		http.Error(w, `{"error":"document not found"}`, 404)
		return
	}
	text, err := ragExtractText(fp)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"extract failed: %s"}`, err.Error()), 500)
		return
	}
	// Truncate to 5000 chars for preview
	runes := []rune(text)
	if len(runes) > 5000 {
		text = string(runes[:5000]) + "..."
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "content": text, "filename": doc})
}

// createKBSnapshot exports the KB state before a destructive rebuild.
func (g *GatewayService) createKBSnapshot(kbID uint) {
	if g.db == nil {
		return
	}
	var chunks []RAGChunk
	g.db.Where("kb_id = ?", kbID).Order("chunk_index").Find(&chunks)
	var faqs []RAGFAQ
	g.db.Where("kb_id = ?", kbID).Find(&faqs)

	var kb RAGKb
	g.db.First(&kb, kbID)

	snap := map[string]interface{}{"kb": kb, "chunks": chunks, "faqs": faqs}
	data, _ := json.Marshal(snap)
	dir := filepath.Join("knowledge", "rag", fmt.Sprintf("%d", kbID), "snapshots")
	os.MkdirAll(dir, 0755)
	fname := fmt.Sprintf("%s_%s.json", kb.Slug, time.Now().Format("20060102_150405"))
	os.WriteFile(filepath.Join(dir, fname), data, 0644)
	log.Printf("[RAG] Snapshot saved: %s (%d chunks, %d faqs)", fname, len(chunks), len(faqs))
}

// handleRAGKbWeight updates the BM25 weight for a KB.
func (g *GatewayService) handleRAGKbWeight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/rag/kb/"), "/")
	if len(parts) < 2 || parts[1] != "weight" {
		http.Error(w, `{"error":"invalid path"}`, 400)
		return
	}
	kbID, _ := strconv.Atoi(parts[0])
	var body struct {
		BM25Weight float64 `json:"bm25_weight"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.BM25Weight < 0 || body.BM25Weight > 1 {
		http.Error(w, `{"error":"bm25_weight must be 0.0-1.0"}`, 400)
		return
	}
	g.db.Model(&RAGKb{}).Where("id = ?", kbID).Update("bm25_weight", body.BM25Weight)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}
func (g *GatewayService) handleRAG(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/rag/kb/list" && r.Method == "GET":
		g.handleRAGKbList(w, r)
	case p == "/api/rag/kb/create" && r.Method == "POST":
		g.handleRAGKbCreate(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/rebuild") && r.Method == "POST":
		g.handleRAGKbRebuild(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/rechunk") && r.Method == "POST":
		g.handleRAGKbRechunk(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/sync-index") && r.Method == "POST":
		g.handleRAGSyncIndex(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/files") && r.Method == "GET":
		g.handleRAGKbFiles(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/chunks") && r.Method == "GET":
		g.handleRAGDocChunks(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && r.Method == "DELETE":
		g.handleRAGKbDelete(w, r)
	case p == "/api/rag/doc/preview" && r.Method == "GET":
		g.handleRAGDocPreview(w, r)
	case p == "/api/rag/doc/upload" && r.Method == "POST":
		g.handleRAGDocUpload(w, r)
	case p == "/api/rag/doc/text" && r.Method == "POST":
		g.handleRAGTextAdd(w, r)
	case p == "/api/rag/doc/import-dir" && r.Method == "POST":
		g.handleRAGImportDir(w, r)
	case strings.HasPrefix(p, "/api/rag/doc/") && r.Method == "DELETE":
		g.handleRAGDocDelete(w, r)
	case p == "/api/rag/chat" && r.Method == "POST":
		g.handleRAGChat(w, r)
	// FAQ CRUD
	case p == "/api/rag/faq/list" && r.Method == "GET":
		g.handleRAGFAQList(w, r)
	case p == "/api/rag/faq/create" && r.Method == "POST":
		g.handleRAGFAQCreate(w, r)
	case p == "/api/rag/faq/import-csv" && r.Method == "POST":
		g.handleRAGFAQImportCSV(w, r)
	case p == "/api/rag/faq/import" && r.Method == "POST":
		g.handleRAGFAQImport(w, r)
	case strings.HasPrefix(p, "/api/rag/faq/") && r.Method == "DELETE":
		g.handleRAGFAQDelete(w, r)
	// Search analytics
	case p == "/api/rag/search-log" && r.Method == "GET":
		g.handleRAGSearchLog(w, r)
	case strings.HasPrefix(p, "/api/rag/kb/") && strings.HasSuffix(p, "/weight") && r.Method == "PUT":
		g.handleRAGKbWeight(w, r)
	case p == "/api/rag/search-zero" && r.Method == "GET":
		g.handleRAGZeroResults(w, r)
	case p == "/api/rag/analytics" && r.Method == "GET":
		g.handleRAGAnalytics(w, r)
	case p == "/api/rag/kb/export" && r.Method == "GET":
		g.handleRAGKbExport(w, r)
	case p == "/api/rag/kb/import" && r.Method == "POST":
		g.handleRAGKbImport(w, r)
	case p == "/api/rag/feedback" && r.Method == "POST":
		g.handleRAGFeedback(w, r)
	default:
		http.Error(w, `{"error":"not found"}`, 404)
	}
}
