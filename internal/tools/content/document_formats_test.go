package content

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPPTXSlideTextPreservesParagraphsAndTables(t *testing.T) {
	xml := []byte(`<p:sld xmlns:p="p" xmlns:a="a"><p:sp><p:txBody><a:p><a:r><a:t>Quarterly update</a:t></a:r></a:p></p:txBody></p:sp><a:tbl><a:tr><a:tc><a:txBody><a:p><a:r><a:t>Metric</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>Value</a:t></a:r></a:p></a:txBody></a:tc></a:tr><a:tr><a:tc><a:txBody><a:p><a:r><a:t>Revenue</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>42</a:t></a:r></a:p></a:txBody></a:tc></a:tr></a:tbl></p:sld>`)
	text := extractPPTXSlideText(xml)
	if !strings.Contains(text, "Quarterly update") || !strings.Contains(text, "Metric") || !strings.Contains(text, "Value") {
		t.Fatalf("slide extraction lost visible content: %q", text)
	}
}

func TestValidatePPTXIntegerGeometryRejectsFractionalEMU(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fractional.pptx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	slide, err := writer.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = slide.Write([]byte(`<p:sld><a:xfrm><a:off x="12.5" y="20"/><a:ext cx="30" cy="40"/></a:xfrm></p:sld>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePPTXIntegerGeometry(path); err == nil {
		t.Fatal("fractional EMU geometry should be rejected")
	}
}
