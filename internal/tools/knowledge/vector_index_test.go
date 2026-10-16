package knowledge

import "testing"

func TestCosineSimilarity(t *testing.T) {
	if got := CosineSimilarity([]float64{1, 0}, []float64{1, 0}); got != 1 {
		t.Fatalf("identical cosine = %v, want 1", got)
	}
	if got := CosineSimilarity([]float64{1}, []float64{1, 0}); got != 0 {
		t.Fatalf("mismatched cosine = %v, want 0", got)
	}
}

func TestVectorIndexSearchFiltersKnowledgeBaseAndDocument(t *testing.T) {
	const firstKB, secondKB = uint(991001), uint(991002)
	t.Cleanup(func() {
		RemoveKnowledgeBaseVectors(firstKB)
		RemoveKnowledgeBaseVectors(secondKB)
	})
	AddVectors(firstKB, []VectorEntry{
		{ChunkID: 1, KBID: firstKB, DocName: "a.md", Content: "best", Vector: []float64{1, 0}},
		{ChunkID: 2, KBID: firstKB, DocName: "b.md", Content: "other", Vector: []float64{0, 1}},
	})
	AddVector(secondKB, VectorEntry{
		ChunkID: 3, KBID: secondKB, DocName: "a.md", Content: "outside", Vector: []float64{1, 0},
	})

	got := SearchVectors([]uint{firstKB}, []float64{1, 0}, 5, "a.md")
	if len(got) != 1 || got[0].ChunkID != 1 {
		t.Fatalf("SearchVectors = %#v", got)
	}
	snapshot := SnapshotVectors(firstKB)
	if len(snapshot) != 2 || VectorCount(firstKB) != 2 {
		t.Fatalf("snapshot/count = %d/%d", len(snapshot), VectorCount(firstKB))
	}
}
