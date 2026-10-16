// Package channelhost binds communication transports to the application
// runtime without importing the executable app package.
package channelhost

import (
	"context"

	channelcore "mac/internal/channels"
)

// Runtime is the host-facing port required by the channel orchestration layer.
// Concrete persistence, employee, Codex and approval implementations remain
// owned by internal/app.
type Runtime interface {
	channelcore.Runtime
	Available() bool
	ArtifactWorkspace() string
	HistoryContext(sessionID, userID, currentTaskID string) string
	ProcessMessage(channelName, channelType, userID, recipient, messageID, message string, metadata map[string]interface{})
	TryCodex(context.Context, string, string, string, string, string) (handled bool, taskID, reply string, err error)
	TryEmployee(context.Context, string, string, string, string, string) (handled bool, employeeName, taskID string, err error)
	ResolveApproval(id, decision string) (*channelcore.ApprovalRequest, bool)
}

func runtimeOf(value channelcore.Runtime) Runtime {
	runtime, _ := value.(Runtime)
	return runtime
}
