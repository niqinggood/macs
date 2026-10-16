package httpx

import (
	"strings"
	"sync"
)

const agentOSSSEKeyPrefix = "agentos:"

func AgentOSSSEKey(taskID string) string {
	return agentOSSSEKeyPrefix + strings.TrimSpace(taskID)
}

func IsReservedAgentOSSSEKey(taskID string) bool {
	return strings.HasPrefix(strings.TrimSpace(taskID), agentOSSSEKeyPrefix)
}

func IsMissingGatewayTaskTableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "gateway_task_records") {
		return false
	}
	return strings.Contains(message, "no such table") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "doesn't exist") ||
		strings.Contains(message, "undefined table")
}

type AgentOSTaskLookup func(taskID string) (queueName string, found bool, err error)
type AgentOSSessionLookup func(sessionID string) (found bool, err error)

// AgentOSLegacyBoundary owns the compatibility marker cache and fail-closed
// decisions shared by legacy HTTP and SSE entrypoints. Storage and in-memory
// lookups are injected by the host.
type AgentOSLegacyBoundary struct {
	taskIDs sync.Map
}

func (boundary *AgentOSLegacyBoundary) MarkTaskID(taskID string) {
	if boundary == nil {
		return
	}
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		boundary.taskIDs.Store(taskID, struct{}{})
	}
}

func (boundary *AgentOSLegacyBoundary) UnmarkTaskID(taskID string) {
	if boundary != nil {
		boundary.taskIDs.Delete(strings.TrimSpace(taskID))
	}
}

func (boundary *AgentOSLegacyBoundary) IsTaskID(
	taskID, agentOSQueueName string,
	durableLookup, memoryLookup AgentOSTaskLookup,
	logger PrintfLogger,
) bool {
	if boundary == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	if _, ok := boundary.taskIDs.Load(taskID); ok {
		return true
	}

	classificationFailed := false
	if durableLookup != nil {
		queueName, found, err := durableLookup(taskID)
		if err == nil && found {
			if queueName == agentOSQueueName {
				boundary.MarkTaskID(taskID)
				return true
			}
			return false
		}
		if err != nil && !IsMissingGatewayTaskTableError(err) {
			classificationFailed = true
			if logger != nil {
				logger.Printf("[AgentOS] failed to classify task %s for legacy boundary: %v", taskID, err)
			}
		}
	}

	var memoryQueue string
	var memoryFound bool
	if memoryLookup != nil {
		memoryQueue, memoryFound, _ = memoryLookup(taskID)
	}
	isAgentOS := memoryFound && memoryQueue == agentOSQueueName
	if isAgentOS {
		boundary.MarkTaskID(taskID)
	}
	return isAgentOS || (classificationFailed && !memoryFound)
}

func (boundary *AgentOSLegacyBoundary) IsSessionID(
	sessionID string,
	memoryLookup func(string) bool,
	durableLookup AgentOSSessionLookup,
	logger PrintfLogger,
) bool {
	if boundary == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if memoryLookup != nil && memoryLookup(sessionID) {
		return true
	}
	if durableLookup == nil {
		return false
	}
	found, err := durableLookup(sessionID)
	if err == nil {
		return found
	}
	if IsMissingGatewayTaskTableError(err) {
		return false
	}
	if logger != nil {
		logger.Printf("[AgentOS] failed to classify session %s for legacy boundary: %v", sessionID, err)
	}
	return true
}

func (boundary *AgentOSLegacyBoundary) SSETargets(taskID string, isAgentOSTask func(string) bool) []string {
	if isAgentOSTask != nil && isAgentOSTask(taskID) {
		return []string{AgentOSSSEKey(taskID)}
	}
	return []string{taskID, "*"}
}
