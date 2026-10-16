package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearningNotesEditOriginalPreservesMetadataAndFencesRevision(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "learning")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "lesson.md")
	original := "---\nname: A learned lesson\ndescription: Tested workflow\nmetadata:\n  source: learn_tool\n---\n\nKeep the original lesson.\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := ListLearningNotes(context.Background(), workspace, directory, 20)
	if err != nil || len(items) != 1 || items[0].Content != "Keep the original lesson.\n" || items[0].Title != "A learned lesson" {
		t.Fatalf("original lesson not visible: %v %#v", err, items)
	}
	current, err := ChangeLearningNote(workspace, path, items[0].Current.Revision, "update", "A corrected lesson.")
	if err != nil || current.Content != "A corrected lesson." {
		t.Fatal("edit failed", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(bytes), "source: learn_tool") || !strings.Contains(string(bytes), "name: A learned lesson") {
		t.Fatal("editing the body destroyed metadata", err)
	}
	if _, err := ChangeLearningNote(workspace, path, items[0].Current.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatal("stale deletion accepted", err)
	}
	if _, err := ChangeLearningNote(workspace, path, current.Revision, "delete", ""); err != nil {
		t.Fatal(err)
	}
	items, err = ListLearningNotes(context.Background(), workspace, directory, 20)
	if err != nil || len(items) != 0 {
		t.Fatal("deleted original lesson remained visible", err)
	}
}

func TestLearningNotesRejectEscapesAndCancelledReads(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	if _, err := ListLearningNotes(context.Background(), workspace, outside, 20); err == nil {
		t.Fatal("outside directory accepted")
	}
	if _, err := ReadLearningNote(workspace, filepath.Join(outside, "secret.md")); err == nil {
		t.Fatal("outside source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListLearningNotes(ctx, workspace, workspace, 20); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read ignored", err)
	}
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := ListLearningNotes(context.Background(), workspace, link, 20); err == nil {
			t.Fatal("replacement link escaped workspace")
		}
	}
}
