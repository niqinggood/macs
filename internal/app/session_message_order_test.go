package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mac/internal/conversation"
)

func TestSessionMessagesKeepSubmissionOrderWhenTimestampsMatch(t *testing.T) {
	f := newConversationMediaFixture(t)
	stamp := time.Date(2026, 10, 12, 15, 15, 38, 995040000, time.FixedZone("CST", 8*60*60))
	messages := []ChatMessage{
		{Role: "user", Content: "First question"},
		{Role: "assistant", Content: "First answer", Generation: &conversation.Generation{RequestID: strings.Repeat("a", 32), Capability: "image", Task: conversation.GenerationTask{Status: "completed"}}},
		{Role: "user", Content: "Followup question"},
		{Role: "assistant", Content: "Followup answer"},
		{Role: "assistant", Content: "Older imported message", CreatedAt: stamp.Add(-time.Minute)},
	}
	for i := range messages {
		messages[i].SessionID = "web_media"
		messages[i].TenantID = defaultExecutionTenantID
		messages[i].Status = "completed"
		if messages[i].CreatedAt.IsZero() {
			messages[i].CreatedAt = stamp
		}
		if err := f.g.db.Create(&messages[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		query string
		want  []uint
	}{
		{"", []uint{messages[4].ID, messages[0].ID, messages[1].ID, messages[2].ID, messages[3].ID}},
		{"?limit=3", []uint{messages[1].ID, messages[2].ID, messages[3].ID}},
		{"?limit=1", []uint{messages[3].ID}},
	} {
		t.Run(test.query, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://localhost:8082/api/session/web_media"+test.query, nil)
			response := httptest.NewRecorder()
			f.g.handleSessionMessages(response, request)
			var result struct {
				Messages []ChatMessage `json:"messages"`
				Total    int64         `json:"total"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Total != 5 || len(result.Messages) != len(test.want) {
				t.Fatalf("invalid history response: %d %s", response.Code, response.Body.String())
			}
			for i, message := range result.Messages {
				if message.ID != test.want[i] {
					t.Fatalf("history position %d: got id %d (%s), want id %d", i, message.ID, message.Role, test.want[i])
				}
			}
		})
	}
	request := httptest.NewRequest("GET", "http://localhost:8082/api/session/export/web_media", nil)
	response := httptest.NewRecorder()
	f.g.handleSessionExport(response, request)
	if response.Code != 200 {
		t.Fatal("export failed", response.Body.String())
	}
	previous := -1
	for _, content := range []string{"Older imported message", "First question", "First answer", "Followup question", "Followup answer"} {
		position := strings.Index(response.Body.String(), content)
		if position <= previous {
			t.Fatalf("export is out of order at %q", content)
		}
		previous = position
	}
}
