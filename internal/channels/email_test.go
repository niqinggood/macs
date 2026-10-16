package channels

import "testing"

func TestEmailChannelAgentContextIncludesTaskMetadata(t *testing.T) {
	channel := NewEmailChannel("email", EmailChannelConfig{})
	ctx := channel.AgentContext("email_user", "email_user-t1", "user@example.com")
	for key, expected := range map[string]string{
		"task_id": "email_user-t1", "session_id": "email_user",
		"channel": "email", "user_id": "user@example.com",
	} {
		if actual, _ := ctx.Value(key).(string); actual != expected {
			t.Fatalf("ctx[%s] = %q, want %q", key, actual, expected)
		}
	}
}
