package knowledge

import (
	"path/filepath"
	"testing"

	"mac/internal/ragmodel"
)

func TestBleveIndexLoadsSearchesFiltersAndDeletes(t *testing.T) {
	chunks := []ragmodel.Chunk{
		{ID: 1, KbID: 10, SourceDoc: "alpha.md", Heading: "Alpha Guide", Content: "alpha modular retrieval guide"},
		{ID: 2, KbID: 20, SourceDoc: "other.md", Heading: "Other", Content: "alpha unrelated tenant document"},
	}
	store := NewBleveIndex(filepath.Join(t.TempDir(), "bleve"), func() ([]ragmodel.Chunk, error) {
		return chunks, nil
	})
	t.Cleanup(func() { _ = store.Close() })

	results, err := store.Search([]uint{10}, "alpha retrieval", "", "alpha.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SourceDoc != "alpha.md" {
		t.Fatalf("filtered search = %#v", results)
	}
	if !store.Ready() {
		t.Fatal("index must report ready after search")
	}
	count, err := store.DocumentCount()
	if err != nil || count != 2 {
		t.Fatalf("document count = %d, err = %v", count, err)
	}

	if err := store.DeleteChunkIDs([]uint{1}); err != nil {
		t.Fatal(err)
	}
	results, err = store.Search([]uint{10}, "alpha retrieval", "", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("deleted chunk still searchable: %#v", results)
	}
}

func TestBleveIndexIndexesAndSynchronizesChunks(t *testing.T) {
	store := NewBleveIndex(filepath.Join(t.TempDir(), "bleve"), nil)
	t.Cleanup(func() { _ = store.Close() })

	chunk := &ragmodel.Chunk{
		ID: 11, KbID: 5, SourceDoc: "new.md", Heading: "Fresh", Content: "fresh searchable material",
	}
	if err := store.IndexChunk(chunk); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncChunks([]ragmodel.Chunk{{
		ID: 12, KbID: 5, SourceDoc: "sync.md", Content: "synchronized searchable material",
	}}); err != nil {
		t.Fatal(err)
	}
	count, err := store.DocumentCount()
	if err != nil || count != 2 {
		t.Fatalf("document count = %d, err = %v", count, err)
	}
}
