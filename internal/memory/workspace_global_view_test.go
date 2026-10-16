package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManagementPageExcludesKindsBeforeCountAndPagination(t *testing.T) {
	db := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db"))
	service := NewService(db, t.TempDir(), "assistant")
	owner := Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	for _, row := range []struct{ key, kind string }{
		{"preference-one", "preference"}, {"fact-one", "fact"}, {"decision-one", "decision"}, {"experience-one", "experience"}, {"preference-two", "preference"},
	} {
		if err := service.Record(context.Background(), owner, row.key, row.key+" value", MemoryUser, row.kind, "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	foreign := owner
	foreign.SubjectID = "foreign"
	if err := service.Record(context.Background(), foreign, "foreign", "foreign value", MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	rows, total, err := service.PageWithFilters(context.Background(), owner, MemoryUser, "", "", "", 1, 1, "preference")
	if err != nil || total != 3 || len(rows) != 1 || rows[0].Kind == "preference" || rows[0].Key == "foreign" {
		t.Fatalf("exclusion must precede pagination and counting: %#v %d %v", rows, total, err)
	}
	rows, total, err = service.PageWithFilters(context.Background(), owner, MemoryUser, "preference", "", "", 0, 20, "preference")
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("conflicting kind filters must not bypass exclusion: %#v %d %v", rows, total, err)
	}
	if _, _, err := service.PageWithFilters(context.Background(), owner, MemoryUser, "", "", "", 0, 20, "not-a-kind"); err == nil {
		t.Fatal("unknown excluded kind accepted")
	}
	_, total, err = service.PageWithFilters(context.Background(), owner, MemoryUser, "", "", "", 0, 20)
	if err != nil || total != 5 {
		t.Fatal("legacy PageWithFilters callers changed", total, err)
	}
}
