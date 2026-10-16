package spreadsheet

import (
	"context"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// WriteXLSXWorkbook writes a real OOXML workbook from rectangular text rows.
// Keeping this shared prevents write_file and convert from creating CSV bytes
// with a misleading .xlsx extension.
func WriteXLSXWorkbook(ctx context.Context, path string, rows [][]string, hasHeader bool) (int, error) {
	if len(rows) == 0 {
		return 0, fmt.Errorf("xlsx requires at least one row")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	workbook := excelize.NewFile()
	defer func() { _ = workbook.Close() }()
	sheet := workbook.GetSheetName(0)
	maxWidths := make(map[int]int)
	for rowIndex, row := range rows {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		for columnIndex, value := range row {
			axis, err := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err != nil {
				return 0, err
			}
			if err := workbook.SetCellStr(sheet, axis, value); err != nil {
				return 0, err
			}
			width := utf8.RuneCountInString(value) + 2
			if width > 40 {
				width = 40
			}
			if width > maxWidths[columnIndex] {
				maxWidths[columnIndex] = width
			}
		}
	}

	if hasHeader && len(rows[0]) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(rows[0]), 1)
		style, err := workbook.NewStyle(&excelize.Style{
			Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
			Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"1F4E78"}},
		})
		if err != nil {
			return 0, err
		}
		if err := workbook.SetCellStyle(sheet, "A1", last, style); err != nil {
			return 0, err
		}
		_ = workbook.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}
	for columnIndex, width := range maxWidths {
		column, err := excelize.ColumnNumberToName(columnIndex + 1)
		if err != nil {
			return 0, err
		}
		if width < 10 {
			width = 10
		}
		if err := workbook.SetColWidth(sheet, column, column, float64(width)); err != nil {
			return 0, err
		}
	}
	if err := workbook.SaveAs(path); err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return int(info.Size()), nil
}
