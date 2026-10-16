package content

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ====================== PPTX Parser ======================
// Port of Unstructured's partition/pptx.py.
// Extracts text from PowerPoint slides, preserving slide boundaries.

func ExtractPPTX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	type pptxSlide struct {
		index int
		text  string
	}
	var slides []pptxSlide
	slideNamePattern := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	// PPTX stores slides as ppt/slides/slide1.xml, slide2.xml, etc.
	for _, f := range r.File {
		match := slideNamePattern.FindStringSubmatch(f.Name)
		if len(match) != 2 {
			continue
		}
		index, _ := strconv.Atoi(match[1])
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		text := extractPPTXSlideText(data)
		slides = append(slides, pptxSlide{index: index, text: text})
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].index < slides[j].index })

	if len(slides) == 0 {
		// Try notes slides
		for _, f := range r.File {
			if !strings.HasPrefix(f.Name, "ppt/notesSlides/notesSlide") {
				continue
			}
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			text := extractPPTXSlideText(data)
			if text != "" {
				slides = append(slides, pptxSlide{index: len(slides) + 1, text: text})
			}
		}
	}

	var out strings.Builder
	for _, slide := range slides {
		out.WriteString(fmt.Sprintf("\n# Slide %d\n\n", slide.index))
		if slide.text == "" {
			out.WriteString("[No editable text detected]")
		} else {
			out.WriteString(slide.text)
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

type PPTXStructureSummary struct {
	SlideCount        int
	TextShapeCount    int
	PictureCount      int
	TableCount        int
	GraphicFrameCount int
	WidthEMU          int64
	HeightEMU         int64
}

func InspectPPTXStructure(path string) (PPTXStructureSummary, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return PPTXStructureSummary{}, fmt.Errorf("open pptx: %w", err)
	}
	defer r.Close()

	var summary PPTXStructureSummary
	slideNamePattern := regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`)
	sizePattern := regexp.MustCompile(`<p:sldSz\b[^>]*\bcx="(\d+)"[^>]*\bcy="(\d+)"`)
	for _, f := range r.File {
		if f.Name == "ppt/presentation.xml" {
			rc, openErr := f.Open()
			if openErr != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if match := sizePattern.FindStringSubmatch(string(data)); len(match) == 3 {
				summary.WidthEMU, _ = strconv.ParseInt(match[1], 10, 64)
				summary.HeightEMU, _ = strconv.ParseInt(match[2], 10, 64)
			}
			continue
		}
		if !slideNamePattern.MatchString(f.Name) {
			continue
		}
		summary.SlideCount++
		rc, openErr := f.Open()
		if openErr != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		xmlText := string(data)
		summary.TextShapeCount += countXMLStartTags(xmlText, "p:sp")
		summary.PictureCount += countXMLStartTags(xmlText, "p:pic")
		summary.TableCount += countXMLStartTags(xmlText, "a:tbl")
		summary.GraphicFrameCount += countXMLStartTags(xmlText, "p:graphicFrame")
	}
	if summary.SlideCount == 0 {
		return summary, fmt.Errorf("pptx contains no slides")
	}
	return summary, nil
}

// validatePPTXIntegerGeometry enforces the OOXML coordinate type that
// PowerPoint itself writes: x/y/cx/cy geometry attributes are integer EMUs.
// python-pptx will serialize a raw float passed to a low-level shapes API, but
// the resulting fractional XML is not a portable PPTX and can render blank or
// vertically distorted in stricter consumers.
func ValidatePPTXIntegerGeometry(path string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open pptx for geometry validation: %w", err)
	}
	defer r.Close()

	integer := regexp.MustCompile(`^-?\d+$`)
	slideNamePattern := regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`)
	for _, file := range r.File {
		if !slideNamePattern.MatchString(file.Name) {
			continue
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return fmt.Errorf("open %s: %w", file.Name, openErr)
		}
		decoder := xml.NewDecoder(reader)
		for {
			token, tokenErr := decoder.Token()
			if tokenErr == io.EOF {
				break
			}
			if tokenErr != nil {
				reader.Close()
				return fmt.Errorf("parse %s: %w", file.Name, tokenErr)
			}
			start, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			for _, attribute := range start.Attr {
				switch attribute.Name.Local {
				case "x", "y", "cx", "cy":
					if !integer.MatchString(attribute.Value) {
						reader.Close()
						return fmt.Errorf(
							"fractional/invalid EMU in %s: %s.%s=%s",
							file.Name,
							start.Name.Local,
							attribute.Name.Local,
							attribute.Value,
						)
					}
				}
			}
		}
		if closeErr := reader.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", file.Name, closeErr)
		}
	}
	return nil
}

func countXMLStartTags(xmlText, tag string) int {
	return strings.Count(xmlText, "<"+tag+">") +
		strings.Count(xmlText, "<"+tag+" ") +
		strings.Count(xmlText, "<"+tag+"/")
}

func extractPPTXSlideText(xmlData []byte) string {
	var out strings.Builder
	for _, paragraph := range extractPPTXTextParagraphs(xmlData) {
		out.WriteString(paragraph)
		out.WriteString("\n")
	}
	for tableIndex, table := range extractPPTXTables(xmlData) {
		if len(table) < 2 || len(table[0]) < 2 {
			continue
		}
		out.WriteString(fmt.Sprintf("\n\n### Original editable table %d\n\n", tableIndex+1))
		writePPTXMarkdownTable(&out, table)
	}
	return strings.TrimSpace(out.String())
}

func extractPPTXTextParagraphs(xmlData []byte) []string {
	decoder := xml.NewDecoder(strings.NewReader(string(xmlData)))
	var paragraphs []string
	var paragraphParts []string
	var text strings.Builder
	inParagraph, inText := false, false
	tableDepth := 0
	flushParagraph := func() {
		paragraph := strings.TrimSpace(strings.Join(paragraphParts, ""))
		if paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
		paragraphParts = nil
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return paragraphs
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "tbl":
				tableDepth++
			case "p":
				if tableDepth == 0 {
					inParagraph = true
					paragraphParts = nil
				}
			case "t":
				if tableDepth == 0 && inParagraph {
					inText = true
					text.Reset()
				}
			}
		case xml.CharData:
			if inText {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				if inText {
					paragraphParts = append(paragraphParts, text.String())
				}
				inText = false
			case "p":
				if tableDepth == 0 && inParagraph {
					flushParagraph()
					inParagraph = false
				}
			case "tbl":
				if tableDepth > 0 {
					tableDepth--
				}
			}
		}
	}
	if inParagraph {
		flushParagraph()
	}
	return paragraphs
}

func extractPPTXTables(xmlData []byte) [][][]string {
	decoder := xml.NewDecoder(strings.NewReader(string(xmlData)))
	var tables [][][]string
	var table [][]string
	var row []string
	var cell strings.Builder
	inTable, inRow, inCell, inText := false, false, false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return tables
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "tbl":
				inTable = true
				table = nil
			case "tr":
				if inTable {
					inRow = true
					row = nil
				}
			case "tc":
				if inRow {
					inCell = true
					cell.Reset()
				}
			case "t":
				if inCell {
					inText = true
				}
			}
		case xml.CharData:
			if inText && inCell {
				cell.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				inText = false
			case "tc":
				if inCell {
					row = append(row, strings.TrimSpace(cell.String()))
				}
				inCell = false
			case "tr":
				if inRow && len(row) > 0 {
					table = append(table, row)
				}
				inRow = false
			case "tbl":
				if inTable && len(table) > 0 {
					tables = append(tables, table)
				}
				inTable = false
			}
		}
	}
	return tables
}

func writePPTXMarkdownTable(out *strings.Builder, table [][]string) {
	columnCount := len(table[0])
	writeRow := func(row []string) {
		out.WriteString("|")
		for column := 0; column < columnCount; column++ {
			value := ""
			if column < len(row) {
				value = strings.ReplaceAll(row[column], "|", "\\|")
				value = strings.Join(strings.Fields(value), " ")
			}
			out.WriteString(" " + value + " |")
		}
		out.WriteString("\n")
	}
	writeRow(table[0])
	out.WriteString("|")
	for column := 0; column < columnCount; column++ {
		out.WriteString(" --- |")
	}
	out.WriteString("\n")
	for _, row := range table[1:] {
		writeRow(row)
	}
}

// ====================== EPUB Parser ======================
// Port of Unstructured's partition/epub.py.
// EPUB is ZIP containing XHTML content. Extract chapter text.

func ExtractEPUB(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open epub: %w", err)
	}
	defer r.Close()

	// Find the rootfile path from META-INF/container.xml
	var rootfilePath string
	for _, f := range r.File {
		if f.Name == "META-INF/container.xml" {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			rootfilePath = extractEPUBRootfile(string(data))
			break
		}
	}
	if rootfilePath == "" {
		// Default: content.opf
		rootfilePath = "content.opf"
	}

	// Find content files from the OPF manifest
	var contentFiles []string
	for _, f := range r.File {
		if f.Name == rootfilePath || strings.HasSuffix(f.Name, ".opf") {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			contentFiles = extractEPUBManifest(string(data))
			break
		}
	}
	if len(contentFiles) == 0 {
		// Fallback: all .xhtml and .html files
		for _, f := range r.File {
			name := strings.ToLower(f.Name)
			if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") {
				contentFiles = append(contentFiles, f.Name)
			}
		}
	}

	var out strings.Builder
	for _, cf := range contentFiles {
		for _, f := range r.File {
			if f.Name == cf || strings.HasSuffix(f.Name, cf) {
				rc, _ := f.Open()
				data, _ := io.ReadAll(rc)
				rc.Close()
				text, _ := ExtractHTML(data)
				if text != "" {
					out.WriteString(text)
					out.WriteString("\n\n")
				}
				break
			}
		}
	}
	return strings.TrimSpace(out.String()), nil
}

func extractEPUBRootfile(containerXML string) string {
	// Extract full-path attribute from <rootfile> element
	start := strings.Index(containerXML, "full-path=\"")
	if start < 0 {
		return ""
	}
	start += 11
	end := strings.Index(containerXML[start:], "\"")
	if end < 0 {
		return ""
	}
	return containerXML[start : start+end]
}

func extractEPUBManifest(opfXML string) []string {
	// Extract hrefs from <item> elements with media-type "application/xhtml+xml"
	var items []string
	for {
		start := strings.Index(opfXML, "<item")
		if start < 0 {
			break
		}
		end := strings.Index(opfXML[start:], "/>")
		if end < 0 {
			end = strings.Index(opfXML[start:], ">")
		}
		if end < 0 {
			break
		}
		item := opfXML[start : start+end+2]
		// Check media-type
		if strings.Contains(item, "application/xhtml+xml") || strings.Contains(item, "text/html") {
			// Extract href
			hrefStart := strings.Index(item, "href=\"")
			if hrefStart >= 0 {
				hrefStart += 6
				hrefEnd := strings.Index(item[hrefStart:], "\"")
				if hrefEnd >= 0 {
					items = append(items, item[hrefStart:hrefStart+hrefEnd])
				}
			}
		}
		opfXML = opfXML[start+end+2:]
	}
	return items
}

// ====================== RTF Parser ======================
// Port of Unstructured's partition/rtf.py.
// RTF is plain text with control words. Strip control words, keep text.

func ExtractRTF(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read rtf: %w", err)
	}
	content := string(data)

	// Check RTF header
	if !strings.HasPrefix(content, "{\\rtf") {
		return "", fmt.Errorf("not a valid RTF file")
	}

	var out strings.Builder
	inGroup := 0
	inControl := false
	inSkippable := false // skip binary data like \bin, \pict groups
	skipDepth := 0

	for i := 0; i < len(content); i++ {
		ch := content[i]

		if ch == '{' {
			inGroup++
			if inSkippable {
				skipDepth++
			}
			continue
		}
		if ch == '}' {
			inGroup--
			if inSkippable && skipDepth > 0 {
				skipDepth--
				if skipDepth == 0 {
					inSkippable = false
				}
			}
			// End of paragraph
			if !inSkippable {
				out.WriteString("\n")
			}
			continue
		}

		if inSkippable {
			continue
		}

		if ch == '\\' {
			inControl = true
			continue
		}

		if inControl {
			if ch == '\'' {
				// Hex-encoded character: \'hh
				if i+2 < len(content) {
					i += 2
					// Skip hex decode for simplicity, just output a space
					out.WriteByte(' ')
				}
				inControl = false
				continue
			}
			if ch == '*' {
				// Skip group marker: \* indicates skippable content
				inSkippable = true
				skipDepth = 1
				inControl = false
				continue
			}
			if ch == ' ' || ch == '\n' || ch == '\r' {
				// Control word delimiter
				inControl = false
			}
			if ch == '\\' || ch == '{' || ch == '}' {
				// Escaped character
				out.WriteByte(ch)
				inControl = false
			}
			// Otherwise still in control word, keep consuming
			continue
		}

		if !inControl {
			out.WriteByte(ch)
		}
	}

	// Clean up: collapse multiple blank lines
	result := strings.TrimSpace(out.String())
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result, nil
}

// ====================== Email Parser (.eml) ======================
// Port of Unstructured's partition/email.py.
// Parses RFC 2822 email headers and body.

func ExtractEmail(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read email: %w", err)
	}
	content := string(data)

	var out strings.Builder
	// Parse headers
	headers := map[string]string{}
	inHeaders := true
	bodyStart := 0
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		if inHeaders {
			trimmed := strings.TrimRight(line, "\r")
			if trimmed == "" {
				inHeaders = false
				bodyStart = i + 1
				continue
			}
			// Continuation line (starts with whitespace)
			if (line[0] == ' ' || line[0] == '\t') && len(headers) > 0 {
				continue // append to previous header (simplified)
			}
			colon := strings.Index(trimmed, ":")
			if colon > 0 {
				key := strings.TrimSpace(trimmed[:colon])
				val := strings.TrimSpace(trimmed[colon+1:])
				headers[strings.ToLower(key)] = val
			}
		}
	}

	// Output structured format
	from := headers["from"]
	subject := headers["subject"]
	date := headers["date"]
	to := headers["to"]

	out.WriteString(fmt.Sprintf("# Email\n\n**From:** %s\n**To:** %s\n**Date:** %s\n**Subject:** %s\n\n",
		from, to, date, subject))

	// Body
	body := strings.Join(lines[bodyStart:], "\n")
	// Remove quoted-printable encoding markers (simplified)
	body = strings.ReplaceAll(body, "=\r\n", "")
	body = strings.ReplaceAll(body, "=3D", "=")
	body = strings.ReplaceAll(body, "=20", " ")

	out.WriteString(strings.TrimSpace(body))
	return out.String(), nil
}

// ====================== ODT Parser ======================
// ODT is ZIP containing content.xml (OpenDocument format).

func ExtractODT(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open odt: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "content.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()

			// ODT uses <text:p> for paragraphs, <text:h> for headings
			decoder := xml.NewDecoder(strings.NewReader(string(data)))
			var out strings.Builder
			var inP, inH bool
			var headingLevel int
			var textBuf strings.Builder

			for {
				tok, err := decoder.Token()
				if err != nil {
					break
				}
				switch t := tok.(type) {
				case xml.StartElement:
					switch t.Name.Local {
					case "p":
						inP = true
						textBuf.Reset()
					case "h":
						inH = true
						textBuf.Reset()
						// Try to determine heading level from style name
						for _, attr := range t.Attr {
							if attr.Name.Local == "style-name" || attr.Name.Local == "outline-level" {
								fmt.Sscanf(attr.Value, "%d", &headingLevel)
							}
						}
					case "tab":
						textBuf.WriteString("\t")
					case "line-break":
						textBuf.WriteString("\n")
					}
				case xml.CharData:
					if inP || inH {
						textBuf.WriteString(string(t))
					}
				case xml.EndElement:
					switch t.Name.Local {
					case "p":
						inP = false
						if s := strings.TrimSpace(textBuf.String()); s != "" {
							out.WriteString(s + "\n\n")
						}
					case "h":
						inH = false
						if s := strings.TrimSpace(textBuf.String()); s != "" {
							prefix := "## "
							if headingLevel >= 2 {
								prefix = strings.Repeat("#", headingLevel+1) + " "
							}
							out.WriteString(prefix + s + "\n\n")
						}
					}
				}
			}
			return strings.TrimSpace(out.String()), nil
		}
	}
	return "", fmt.Errorf("content.xml not found in ODT")
}
