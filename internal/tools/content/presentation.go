package content

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

// PPTXContainsSRGBColor scans bounded presentation XML parts for an exact
// DrawingML sRGB color value.
func PPTXContainsSRGBColor(path, colorHex string) (bool, error) {
	colorHex = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(colorHex, "#")))
	if len(colorHex) != 6 {
		return false, fmt.Errorf("invalid RGB color %q", colorHex)
	}
	reader, err := zip.OpenReader(filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("open PPTX theme archive: %w", err)
	}
	defer reader.Close()

	pattern := pptxSRGBColorPattern(colorHex)
	const (
		maxXMLPartBytes = 16 << 20
		maxScannedBytes = 64 << 20
	)
	var scanned int64
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if !strings.HasPrefix(name, "ppt/") || !strings.HasSuffix(strings.ToLower(name), ".xml") {
			continue
		}
		if file.UncompressedSize64 > maxXMLPartBytes {
			continue
		}
		if scanned+int64(file.UncompressedSize64) > maxScannedBytes {
			return false, fmt.Errorf("PPTX theme scan exceeds %d bytes", maxScannedBytes)
		}
		part, err := file.Open()
		if err != nil {
			return false, fmt.Errorf("open PPTX XML part %s: %w", name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(part, maxXMLPartBytes+1))
		closeErr := part.Close()
		if readErr != nil {
			return false, fmt.Errorf("read PPTX XML part %s: %w", name, readErr)
		}
		if closeErr != nil {
			return false, fmt.Errorf("close PPTX XML part %s: %w", name, closeErr)
		}
		if len(data) > maxXMLPartBytes {
			return false, fmt.Errorf("PPTX XML part %s exceeds %d bytes", name, maxXMLPartBytes)
		}
		scanned += int64(len(data))
		if pattern.Match(data) {
			return true, nil
		}
	}
	return false, nil
}

func pptxSRGBColorPattern(colorHex string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(colorHex)
	return regexp.MustCompile(`(?i)<(?:[[:alnum:]_]+:)?srgbClr\b[^>]*\bval\s*=\s*["']` + quoted + `["']`)
}
