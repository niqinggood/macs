package httpx

import (
	"encoding/json"
	"net/http"

	"mac/internal/runtimeutil"
)

// HandleSharedState serves the shared-state CRUD API. onUpdate is invoked
// after a successful merge so the gateway can broadcast an SSE event.
func HandleSharedState(w http.ResponseWriter, r *http.Request, state *runtimeutil.SharedState, onUpdate func(map[string]interface{})) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if state == nil {
		http.Error(w, "shared state unavailable", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		key := r.URL.Query().Get("key")
		if key != "" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"key": key, "value": state.Get(key)})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"state": state.Snapshot()})
	case http.MethodPost:
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		state.Merge(updates)
		if onUpdate != nil {
			onUpdate(updates)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	case http.MethodDelete:
		if key := r.URL.Query().Get("key"); key != "" {
			state.Delete(key)
		} else {
			state.Clear()
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
