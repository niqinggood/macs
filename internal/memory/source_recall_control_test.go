package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSourceRecallControlLifecyclePreservesOriginal(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	event := MemoryEvent{TenantID: owner.TenantID, AgentName: service.agent, SessionID: owner.SessionID, UserID: owner.SubjectID,
		UserQuestion: "quartz instructions", FinalAnswer: "quartz ORIGINAL_ANSWER", LearnNote: "quartz ORIGINAL_INSIGHT", Status: "success"}
	if err := service.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	var before MemoryEvent
	if err := service.db.First(&before, event.ID).Error; err != nil {
		t.Fatal(err)
	}
	locator := PromptLocator{Kind: "memory_event", ID: fmt.Sprint(event.ID), SessionID: owner.SessionID, Field: "final_answer", Digest: "old-digest", Key: "private source key"}
	insight := locator
	insight.Field, insight.Digest, insight.Key, insight.Version = "learn_note", "new-digest", "another display key", 4
	if SourceRecallKey(locator) != SourceRecallKey(insight) {
		t.Fatal("one event acquired different controls for answer and insight")
	}
	items := []RecallItem{
		{ID: "answer", Source: "task_execution", Content: event.FinalAnswer, Locator: locator},
		{ID: "insight", Source: "task_insight", Content: event.LearnNote, Locator: insight},
	}
	settings, err := service.SourceRecallSettings(ctx, owner, []PromptLocator{locator, insight})
	if err != nil || len(settings) != 1 || !settings[SourceRecallKey(locator)].Enabled || settings[SourceRecallKey(locator)].Version != 0 {
		t.Fatalf("default settings: %#v %v", settings, err)
	}
	result, err := service.Recall(ctx, owner, "quartz", 2000, items)
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("default recall: %d %v", len(result.Items), err)
	}
	setting, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 0)
	if err != nil || setting.Enabled || setting.Version != 1 || setting.UpdatedAt.IsZero() {
		t.Fatalf("disable: %#v %v", setting, err)
	}
	// A new current conversation does not reset a saved record's setting.
	nextOwner := owner
	nextOwner.SessionID = "next-conversation"
	result, err = service.Recall(ctx, nextOwner, "quartz", 2000, items)
	if err != nil || result.Prompt != "" || len(result.Items) != 0 || !fenceDecision(result, "answer", "disabled_by_user") || !fenceDecision(result, "insight", "disabled_by_user") {
		t.Fatalf("disabled event recalled: %#v %v", result, err)
	}
	if err := service.SaveRecallTrace(ctx, nextOwner, "disabled-source-turn", "empty", result); err != nil {
		t.Fatal(err)
	}
	traces, err := service.RecentRecallTraces(ctx, nextOwner, 1)
	if err != nil || len(traces) != 1 || len(traces[0].Decisions) != 2 || traces[0].Decisions[0].Reason != "disabled_by_user" || traces[0].Decisions[1].Reason != "disabled_by_user" {
		t.Fatalf("disabled reason lost from persisted trace: %#v %v", traces, err)
	}
	var after MemoryEvent
	if err := service.db.First(&after, event.ID).Error; err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("setting changed original event: %v", err)
	}
	var stored []sourceRecallRecord
	if err := service.db.Find(&stored).Error; err != nil || len(stored) != 1 {
		t.Fatalf("stored settings: %d %v", len(stored), err)
	}
	encoded, _ := json.Marshal(stored)
	for _, secret := range []string{owner.SessionID, "ORIGINAL_ANSWER", "ORIGINAL_INSIGHT", locator.Key, locator.Digest} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("source settings copied private original text or locator metadata")
		}
	}
	setting, err = service.SetSourceRecallEnabled(ctx, owner, insight, true, 1)
	if err != nil || !setting.Enabled || setting.Version != 2 {
		t.Fatalf("enable: %#v %v", setting, err)
	}
	result, err = service.Recall(ctx, nextOwner, "quartz", 2000, items)
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("enabled event unavailable: %d %v", len(result.Items), err)
	}
}

func TestSourceRecallControlKindsAndNamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	for _, kind := range []string{"memory_event", "knowledge", "knowledge_fact", "summary", "chat_message"} {
		t.Run(kind, func(t *testing.T) {
			locator := PromptLocator{Kind: kind, ID: "41", SessionID: "source-session", Field: "summary"}
			if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 0); err != nil {
				t.Fatal(err)
			}
			bodyLocator := locator
			bodyLocator.Field, bodyLocator.Revision, bodyLocator.Digest = "content", 6, "edited-body"
			if SourceRecallKey(locator) != SourceRecallKey(bodyLocator) {
				t.Fatal("source field/revision changes escaped the whole-record control")
			}
			item := RecallItem{ID: "original-" + kind, Source: kind, Content: "quartz " + kind, Locator: bodyLocator}
			result, err := service.Recall(ctx, owner, "quartz", 2000, []RecallItem{item})
			if err != nil || len(result.Items) != 0 || !fenceDecision(result, item.ID, "disabled_by_user") {
				t.Fatalf("kind not excluded: %#v %v", result, err)
			}
			for _, field := range []string{"tenant", "subject", "partition", "workspace", "agent", "source-session", "source-id"} {
				foreignOwner, foreignService, foreignLocator := owner, service, locator
				switch field {
				case "tenant":
					foreignOwner.TenantID += "-other"
				case "subject":
					foreignOwner.SubjectID += "-other"
				case "partition":
					foreignOwner.PartitionID += "-other"
				case "workspace":
					foreignService = NewService(service.db, t.TempDir(), service.agent)
				case "agent":
					foreignService = NewService(service.db, service.workspace, "another-agent")
				case "source-session":
					foreignLocator.SessionID += "-other"
				case "source-id":
					foreignLocator.ID = "42"
				}
				settings, err := foreignService.SourceRecallSettings(ctx, foreignOwner, []PromptLocator{foreignLocator})
				if err != nil || !settings[SourceRecallKey(foreignLocator)].Enabled || settings[SourceRecallKey(foreignLocator)].Version != 0 {
					t.Fatalf("%s crossed %s boundary: %#v %v", kind, field, settings, err)
				}
			}
		})
	}
}

func TestSourceRecallControlCASAndInvalidLocators(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	locator := PromptLocator{Kind: "knowledge", ID: "7", SessionID: "source-session"}
	if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 1); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatalf("nonexistent nonzero version accepted: %v", err)
	}
	if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 0); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{-1, 0, 2} {
		if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, true, version); err == nil {
			t.Fatalf("invalid/stale version %d accepted", version)
		}
	}
	invalid := []PromptLocator{
		{}, {Kind: "entry", ID: "1", SessionID: "s"}, {Kind: "instruction", ID: "1", SessionID: "s"}, {Kind: "context", ID: "1", SessionID: "s"},
		{Kind: "memory_event", ID: "0", SessionID: "s"}, {Kind: "memory_event", ID: "01", SessionID: "s"},
		{Kind: "knowledge", ID: "-1", SessionID: "s"}, {Kind: "knowledge", ID: "18446744073709551616", SessionID: "s"},
		{Kind: "knowledge_fact", ID: "1"}, {Kind: "chat_message", ID: "1", SessionID: "s\x00other"},
		{Kind: "summary", ID: "", SessionID: "s"}, {Kind: "summary", ID: "valid", SessionID: strings.Repeat("s", 257)},
		{Kind: "summary", ID: " valid", SessionID: "s"}, {Kind: "summary", ID: "valid", SessionID: "s", Path: "AGENTS.md"},
	}
	for i, invalidLocator := range invalid {
		if SourceRecallKey(invalidLocator) != "" {
			t.Fatalf("invalid locator %d has a canonical key", i)
		}
		if _, err := service.SetSourceRecallEnabled(ctx, owner, invalidLocator, false, 0); !errors.Is(err, ErrPromptSourceUnsupported) {
			t.Fatalf("invalid locator %d was persisted: %v", i, err)
		}
		if _, err := service.SourceRecallSettings(ctx, owner, []PromptLocator{locator, invalidLocator}); !errors.Is(err, ErrPromptSourceUnsupported) {
			t.Fatalf("invalid locator %d permitted partial lookup: %v", i, err)
		}
	}
	settings, err := service.SourceRecallSettings(ctx, owner, []PromptLocator{locator})
	if err != nil || settings[SourceRecallKey(locator)].Enabled || settings[SourceRecallKey(locator)].Version != 1 {
		t.Fatalf("conflicting updates changed setting: %#v %v", settings, err)
	}
	var count int64
	if err := service.db.Model(&sourceRecallRecord{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("invalid requests wrote rows: %d %v", count, err)
	}
}

func TestSourceRecallControlFailureIsClosed(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	locator := PromptLocator{Kind: "knowledge", ID: "9", SessionID: "source-session"}
	if err := service.db.Migrator().DropTable(&sourceRecallRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SourceRecallSettings(ctx, owner, []PromptLocator{locator}); err == nil {
		t.Fatal("failed settings read returned an enabled default")
	}
	result, err := service.Recall(ctx, owner, "quartz", 2000, []RecallItem{{ID: "known", Source: "knowledge", Content: "quartz PRIVATE_CONTENT", Locator: locator}})
	if err == nil || result.Prompt != "" || len(result.Items) != 0 {
		t.Fatalf("failed settings query leaked prompt: %#v %v", result, err)
	}
	// This also exercises upgrading an existing database through the normal
	// production migration entry, followed by an idempotent second startup.
	if err := InitMemoryDB(service.db); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 0); err != nil {
		t.Fatal(err)
	}
	if err := InitMemoryDB(service.db); err != nil {
		t.Fatal(err)
	}
	settings, err := service.SourceRecallSettings(ctx, owner, []PromptLocator{locator})
	if err != nil || settings[SourceRecallKey(locator)].Enabled || settings[SourceRecallKey(locator)].Version != 1 {
		t.Fatalf("migration reset persisted recall choice: %#v %v", settings, err)
	}
}

func TestSourceRecallControlMalformedSelectedLocatorFailsClosed(t *testing.T) {
	service, owner := promptTestService(t)
	for _, locator := range []PromptLocator{
		{Kind: "summary", ID: "older-summary-without-session"},
		{Kind: "knowledge", ID: "9"},
		{Kind: "memory_event", ID: "bad", SessionID: "s"},
	} {
		result, err := service.Recall(context.Background(), owner, "quartz", 2000, []RecallItem{{ID: "invalid", Source: locator.Kind, Content: "quartz invalid source", Locator: locator}})
		if !errors.Is(err, ErrPromptSourceUnsupported) || result.Prompt != "" || len(result.Items) != 0 {
			t.Fatalf("malformed selected locator bypassed control: %#v %v", locator, err)
		}
	}
}

func TestSourceRecallControlDisableDuringEmbeddingAndReenable(t *testing.T) {
	ctx := context.Background()
	gate := newRecallFenceGate(t)
	backend := gate.backend()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{TenantID: "tenant", SubjectID: "alice", PartitionID: "alice", SessionID: "source-session"}
	if err := service.SaveSummary(ctx, owner, "quartz ORIGINAL_SUMMARY"); err != nil {
		t.Fatal(err)
	}
	var original Summary
	if err := service.db.First(&original).Error; err != nil {
		t.Fatal(err)
	}
	locator := PromptLocator{Kind: "summary", ID: original.ID, SessionID: owner.SessionID, Field: "content"}
	done := startFencedRecall(t, ctx, service, owner, gate)
	if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, false, 0); err != nil {
		t.Fatal(err)
	}
	outcome := finishFencedRecall(t, done, gate)
	if outcome.err != nil || outcome.result.Prompt != "" || len(outcome.result.Items) != 0 || !fenceDecision(outcome.result, original.ID, "disabled_by_user") {
		t.Fatalf("late disable lost at embedding fence: %#v %v", outcome.result, outcome.err)
	}
	// Explicit source reads remain possible, and no rewrite/delete was applied.
	current, err := service.ReadPromptSource(ctx, owner, locator)
	if err != nil || current == nil || current.Content != original.Content {
		t.Fatalf("disabled original became unreadable: %#v %v", current, err)
	}
	var after Summary
	if err := service.db.First(&after, "id = ?", original.ID).Error; err != nil || !reflect.DeepEqual(original, after) {
		t.Fatalf("summary was modified while disabled: %v", err)
	}
	if _, err := service.SetSourceRecallEnabled(ctx, owner, locator, true, 1); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, owner, "quartz requested", 2000, nil)
	if err != nil || len(result.Items) != 1 || !strings.Contains(result.Prompt, "ORIGINAL_SUMMARY") {
		t.Fatalf("re-enable could not reuse original/cache: %#v %v", result, err)
	}
}
