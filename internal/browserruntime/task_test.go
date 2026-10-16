package browserruntime

import (
	"strings"
	"testing"
)

func TestBrowserTaskManagerStoresAuditableRecord(t *testing.T) {
	manager := NewBrowserTaskManager()
	record := manager.Start("inspect login page", "https://example.com/login")
	manager.AddStep(record.ID, "navigate", "completed", "Opened tab tab-1", 25, nil)
	manager.Update(record.ID, func(current *BrowserTaskRecord) {
		current.TargetID = "tab-1"
		current.Status = BrowserTaskCompleted
		current.ScreenshotPath = ".openkitty/storage/browser_tasks/task/screenshot.png"
		current.ScreenshotURL = "/api/files/download?path=%2F.openkitty%2Fstorage%2Fbrowser_tasks%2Ftask%2Fscreenshot.png"
		current.ExtractExcerpt = "[0]<input> placeholder=\"Email\""
	})

	stored, ok := manager.Get(record.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if stored.Status != BrowserTaskCompleted || stored.TargetID != "tab-1" {
		t.Fatalf("unexpected task: %#v", stored)
	}
	if stored.EstimatedCredit == 0 || !strings.Contains(stored.Markdown, "Browser task") {
		t.Fatalf("derived audit fields missing: %#v", stored)
	}
}

func TestBrowserTaskManagerListReturnsClonedNewestFirst(t *testing.T) {
	manager := NewBrowserTaskManager()
	first := manager.Start("first", "https://example.com/1")
	second := manager.Start("second", "https://example.com/2")

	list := manager.List(10)
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("tasks not sorted newest first: %#v", list)
	}
	list[0].Status = BrowserTaskFailed
	again, _ := manager.Get(second.ID)
	if again.Status == BrowserTaskFailed {
		t.Fatal("List should return cloned records")
	}
}

func TestParseBrowserTaskTargetID(t *testing.T) {
	got := ParseBrowserTaskTargetID("Opened tab ABC123\nTitle: Example\nURL: https://example.com")
	if got != "ABC123" {
		t.Fatalf("target id = %q", got)
	}
}
