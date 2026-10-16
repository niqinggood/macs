package textutil

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileCategory is the normalized family selected by extension and magic-byte
// inspection before the application dispatches to a concrete parser.
type FileCategory int

const (
	CatUnknown FileCategory = iota
	CatPDF
	CatDOCX
	CatPPTX
	CatXLSX
	CatHTML
	CatXML
	CatEPUB
	CatRTF
	CatEmail
	CatCSV
	CatTSV
	CatJSON
	CatNDJSON
	CatMarkdown
	CatText
	CatImage
	CatAudio
	CatODT
	CatOrgMode
	CatRST
)

var categoryNames = map[FileCategory]string{
	CatPDF: "pdf", CatDOCX: "docx", CatPPTX: "pptx", CatXLSX: "xlsx",
	CatHTML: "html", CatXML: "xml", CatEPUB: "epub", CatRTF: "rtf",
	CatEmail: "email", CatCSV: "csv", CatTSV: "tsv", CatJSON: "json",
	CatNDJSON: "ndjson", CatMarkdown: "md", CatText: "txt", CatImage: "image",
	CatAudio: "audio", CatODT: "odt", CatOrgMode: "org", CatRST: "rst",
	CatUnknown: "unknown",
}

func (category FileCategory) String() string { return categoryNames[category] }

var extensionCategories = map[string]FileCategory{
	".pdf": CatPDF, ".docx": CatDOCX, ".doc": CatDOCX,
	".pptx": CatPPTX, ".ppt": CatPPTX, ".xlsx": CatXLSX, ".xls": CatXLSX,
	".html": CatHTML, ".htm": CatHTML, ".xml": CatXML,
	".epub": CatEPUB, ".rtf": CatRTF, ".eml": CatEmail, ".mbox": CatEmail,
	".csv": CatCSV, ".tsv": CatTSV, ".json": CatJSON, ".ndjson": CatNDJSON,
	".md": CatMarkdown, ".txt": CatText, ".org": CatOrgMode, ".rst": CatRST,
	".odt": CatODT,
	".png": CatImage, ".jpg": CatImage, ".jpeg": CatImage, ".gif": CatImage,
	".bmp": CatImage, ".tiff": CatImage, ".tif": CatImage, ".webp": CatImage, ".heic": CatImage,
	".mp3": CatAudio, ".wav": CatAudio, ".m4a": CatAudio, ".ogg": CatAudio, ".flac": CatAudio,
}

// DetectFileType returns a file category using the extension first, then magic
// bytes for files without a recognized suffix.
func DetectFileType(path string) FileCategory {
	if category, ok := extensionCategories[strings.ToLower(filepath.Ext(path))]; ok {
		return category
	}
	return detectByMagic(path)
}

func detectByMagic(path string) FileCategory {
	file, err := os.Open(path)
	if err != nil {
		return CatUnknown
	}
	defer file.Close()

	header := make([]byte, 8)
	n, _ := io.ReadFull(file, header)
	if n < 4 {
		return CatUnknown
	}
	if string(header[:4]) == "%PDF" {
		return CatPDF
	}
	if header[0] == 0x50 && header[1] == 0x4b && header[2] == 0x03 && header[3] == 0x04 {
		return detectZipType(file)
	}
	if n >= 5 && string(header[:5]) == "{\\rtf" {
		return CatRTF
	}
	if string(header[:4]) == "GIF8" {
		return CatImage
	}
	if header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4e && header[3] == 0x47 {
		return CatImage
	}
	if n >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff {
		return CatImage
	}
	if n >= 5 && (string(header[:5]) == "From:" || string(header[:5]) == "Date:" || string(header[:8]) == "Subject:") {
		return CatEmail
	}
	return CatUnknown
}

func detectZipType(file *os.File) FileCategory {
	_, _ = file.Seek(0, 0)
	data := make([]byte, 2048)
	n, _ := file.Read(data)
	content := string(data[:n])
	switch {
	case strings.Contains(content, "word/document.xml"):
		return CatDOCX
	case strings.Contains(content, "ppt/presentation.xml"):
		return CatPPTX
	case strings.Contains(content, "xl/workbook.xml") || strings.Contains(content, "xl/worksheets"):
		return CatXLSX
	case strings.Contains(content, "META-INF/container.xml") && strings.Contains(content, "application/epub+zip"):
		return CatEPUB
	case strings.Contains(content, "mimetypeapplication/vnd.oasis.opendocument.text"):
		return CatODT
	default:
		return CatUnknown
	}
}
