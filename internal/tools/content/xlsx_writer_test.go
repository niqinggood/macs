package content

import (
	"context"
	"path/filepath"
	"testing"

	"mac/internal/toolcontract"

	"github.com/xuri/excelize/v2"
)

func TestConvertMarkdownTableCreatesRealXLSXWorkbook(t *testing.T) {
	workspace := t.TempDir()
	tool := NewConvertTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"markdown": "| Market | Customers |\n|---|---|\n| Singapore | 3 |\n| Malaysia | 5 |\n",
		"output":   "plan.xlsx",
	})
	if err != nil || !toolcontract.ToolExecutionSucceededForTool("convert", result, err) {
		t.Fatalf("convert xlsx failed: result=%q err=%v", result, err)
	}
	workbook, err := excelize.OpenFile(filepath.Join(workspace, "plan.xlsx"))
	if err != nil {
		t.Fatalf("open converted xlsx: %v", err)
	}
	defer func() { _ = workbook.Close() }()
	if got, _ := workbook.GetCellValue(workbook.GetSheetName(0), "B3"); got != "5" {
		t.Fatalf("B3=%q, want 5", got)
	}
}
