package ragmodel

import "testing"

func TestExtractedModelsPreserveLegacyTableNames(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "knowledge base", got: (Kb{}).TableName(), want: "rag_kbs"},
		{name: "chunk", got: (Chunk{}).TableName(), want: "rag_chunks"},
		{name: "FAQ", got: (FAQ{}).TableName(), want: "ragfaqs"},
		{name: "search log", got: (SearchLog{}).TableName(), want: "rag_search_logs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("table name = %q, want %q", test.got, test.want)
			}
		})
	}
}
