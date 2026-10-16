package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mac/internal/toolcontract"

	"github.com/xuri/excelize/v2"
)

func TestWriteFileCreatesRealXLSXWorkbook(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWriteFileTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    "deliverables/plan.xlsx",
		"content": "Market,Budget,Owner\nSingapore,900000,Partner A\nMalaysia,900000,Partner B\n",
	})
	if err != nil || !toolcontract.ToolExecutionSucceededForTool("write_file", result, err) {
		t.Fatalf("write xlsx failed: result=%q err=%v", result, err)
	}
	path := filepath.Join(workspace, "deliverables", "plan.xlsx")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:2]) != "PK" {
		t.Fatalf("xlsx is not an OOXML zip: first bytes=%q size=%d", data[:min(len(data), 8)], len(data))
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open generated xlsx: %v", err)
	}
	defer func() { _ = workbook.Close() }()
	sheet := workbook.GetSheetName(0)
	if got, _ := workbook.GetCellValue(sheet, "A1"); got != "Market" {
		t.Fatalf("A1=%q, want Market", got)
	}
	if got, _ := workbook.GetCellValue(sheet, "C3"); got != "Partner B" {
		t.Fatalf("C3=%q, want Partner B", got)
	}
}
