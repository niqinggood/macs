package app

import (
	"fmt"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTaskIDTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&ChatMessage{}); err != nil {
		t.Fatalf("migrate chat messages: %v", err)
	}
	return &GatewayService{
		db: db,
	}
}

func TestNextSessionTaskIDUsesMaxExistingSequence(t *testing.T) {
	g := newTaskIDTestGateway(t)
	for _, taskID := range []string{"web_session-t1", "web_session-t3"} {
		if err := g.db.Create(&ChatMessage{
			SessionID: "web_session",
			Role:      "user",
			Content:   "demo",
			TaskID:    taskID,
		}).Error; err != nil {
			t.Fatalf("insert chat message: %v", err)
		}
	}

	if got := g.nextSessionTaskID("web_session"); got != "web_session-t4" {
		t.Fatalf("first task id = %s, want web_session-t4", got)
	}
	if got := g.nextSessionTaskID("web_session"); got != "web_session-t5" {
		t.Fatalf("second task id = %s, want web_session-t5", got)
	}
}

func TestNextSessionTaskIDNoDuplicatesUnderConcurrentAllocation(t *testing.T) {
	g := newTaskIDTestGateway(t)
	const n = 32
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- g.nextSessionTaskID("web_concurrent")
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool, n)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate task id allocated: %s", id)
		}
		seen[id] = true
	}
	for i := 1; i <= n; i++ {
		want := fmt.Sprintf("web_concurrent-t%d", i)
		if !seen[want] {
			t.Fatalf("missing allocated task id %s; got %#v", want, seen)
		}
	}
}
