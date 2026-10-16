package httpx

import (
	"encoding/json"
	"net/http"

	"mac/internal/eventbus"
)

func HandleMessages(w http.ResponseWriter, center *eventbus.NotificationCenter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	messages := center.Messages()
	if messages == nil {
		messages = []eventbus.SystemMessage{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "messages": messages})
}

func HandleConfirmAction(w http.ResponseWriter, r *http.Request, center *eventbus.NotificationCenter) {
	var request struct {
		ActionID string `json:"action_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ActionID == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "action_id required"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": center.Confirm(request.ActionID)})
}
