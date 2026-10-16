package content

import (
	"reflect"
	"testing"
)

func TestPDFLayoutClusterListSortsAndGroupsByTolerance(t *testing.T) {
	got := clusterList([]float64{10.2, 1, 10, 1.5, 30}, 0.5)
	want := [][]float64{{1, 1.5}, {10, 10.2}, {30}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clusterList() = %#v, want %#v", got, want)
	}
}

func TestPDFLayoutCleanPlainTextPreservesParagraphs(t *testing.T) {
	got := cleanPlainText("中\n文  文\n\nSecond\n line")
	want := "中文 文\n\nSecond line"
	if got != want {
		t.Fatalf("cleanPlainText() = %q, want %q", got, want)
	}
}

func TestPDFLayoutTableToMarkdownPadsShortRows(t *testing.T) {
	got := TableToMarkdown(Table{Rows: [][]string{{"A", "B"}, {"1"}}})
	want := "| A | B |\n| --- | --- |\n| 1 |  |\n"
	if got != want {
		t.Fatalf("TableToMarkdown() = %q, want %q", got, want)
	}
}
