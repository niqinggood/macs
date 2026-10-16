package eventbus

import (
	"sync"
	"time"
)

// SystemMessage is a notification shown in the web UI message center.
type SystemMessage struct {
	ID           int64  `json:"id"`
	Text         string `json:"text"`
	Severity     string `json:"severity"`
	Time         string `json:"time"`
	Read         bool   `json:"read"`
	ActionID     string `json:"action_id,omitempty"`
	ActionLabel  string `json:"action_label,omitempty"`
	ActionDone   bool   `json:"action_done,omitempty"`
	Progress     int    `json:"progress,omitempty"`
	ProgressText string `json:"progress_text,omitempty"`
}

// NotificationCenter owns bounded messages and confirmation channels.
type NotificationCenter struct {
	mu       sync.Mutex
	messages []SystemMessage
	nextID   int64
	actions  map[string]chan struct{}
	max      int
}

var DefaultNotificationCenter = NewNotificationCenter(100)

func PushSystemMessage(text, severity string) {
	_ = DefaultNotificationCenter.Push(text, severity, "", "")
}

func PushSystemMessageAction(text, severity, actionID, actionLabel string) chan struct{} {
	return DefaultNotificationCenter.Push(text, severity, actionID, actionLabel)
}

func UpdateMessageProgress(actionID string, progress int, text string) {
	DefaultNotificationCenter.UpdateProgress(actionID, progress, text)
}

func NewNotificationCenter(max int) *NotificationCenter {
	if max <= 0 {
		max = 100
	}
	return &NotificationCenter{actions: make(map[string]chan struct{}), max: max}
}

func (center *NotificationCenter) Push(text, severity, actionID, actionLabel string) chan struct{} {
	center.mu.Lock()
	defer center.mu.Unlock()
	center.nextID++
	message := SystemMessage{
		ID: center.nextID, Text: text, Severity: severity,
		Time: time.Now().Format("15:04"), ActionID: actionID, ActionLabel: actionLabel,
	}
	center.messages = append(center.messages, message)
	if len(center.messages) > center.max {
		center.messages = center.messages[len(center.messages)-center.max:]
	}
	channel := make(chan struct{})
	if actionID != "" {
		center.actions[actionID] = channel
	}
	return channel
}

func (center *NotificationCenter) UpdateProgress(actionID string, progress int, text string) {
	center.mu.Lock()
	defer center.mu.Unlock()
	for index := range center.messages {
		if center.messages[index].ActionID == actionID {
			center.messages[index].Progress = progress
			center.messages[index].ProgressText = text
			return
		}
	}
}

func (center *NotificationCenter) Confirm(actionID string) bool {
	center.mu.Lock()
	defer center.mu.Unlock()
	channel, ok := center.actions[actionID]
	if !ok {
		return false
	}
	delete(center.actions, actionID)
	for index := range center.messages {
		if center.messages[index].ActionID == actionID {
			center.messages[index].ActionDone = true
			center.messages[index].Read = true
		}
	}
	close(channel)
	return true
}

func (center *NotificationCenter) Messages() []SystemMessage {
	center.mu.Lock()
	defer center.mu.Unlock()
	messages := make([]SystemMessage, len(center.messages))
	copy(messages, center.messages)
	return messages
}
