package textutil

import (
	"strings"
	"testing"
)

func TestFormatClassifiedElementsPreservesSemanticBoundaries(t *testing.T) {
	formatted := FormatClassifiedElements([]ClassifiedElement{
		{Type: ElemTitle, Text: "Report"},
		{Type: ElemSectionHeader, Text: "Findings"},
		{Type: ElemNarrativeText, Text: "Evidence."},
		{Type: ElemListItem, Text: "First"},
		{Type: ElemPageBreak},
	})
	for _, expected := range []string{"# Report", "## Findings", "Evidence.", "* First", "---"} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("formatted output missing %q: %q", expected, formatted)
		}
	}
}
