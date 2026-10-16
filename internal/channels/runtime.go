package channels

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Agent is the smallest model-facing contract needed by a communication
// channel. Application agents satisfy it without the channel package importing
// the executable root package.
type Agent interface {
	Generate(ctx context.Context, message string) (string, error)
}

// Runtime is the application port used by concrete channel adapters.
type Runtime interface {
	DefaultAgent() (name string, agent Agent, ok bool)
	SaveChatMessage(sessionID, role, agentName, content, status, taskID, channelName, userID string)
	TryInjectInterrupt(sessionID, channelName, userID, message string) bool
	RegisterInterrupt(sessionID string) chan string
	UnregisterInterrupt(sessionID string, interruptCh chan string)
}

// Base owns platform-neutral channel identity, sessions, logging and lifecycle
// state. Business/Gateway behavior is supplied through Runtime.
type Base struct {
	name        string
	channelType string
	runtime     Runtime
	logger      *log.Logger
	sessions    map[string]string
	mu          sync.RWMutex
	stopCh      chan struct{}
}

func NewBase(name, channelType string) *Base {
	return &Base{
		name:        name,
		channelType: channelType,
		logger:      log.New(os.Stdout, fmt.Sprintf("[%s:%s] ", channelType, name), log.LstdFlags),
		sessions:    make(map[string]string),
		stopCh:      make(chan struct{}),
	}
}

func (base *Base) Name() string         { return base.name }
func (base *Base) Type() string         { return base.channelType }
func (base *Base) SetRuntime(r Runtime) { base.runtime = r }

func (base *Base) GetOrCreateSession(userID string) string {
	base.mu.RLock()
	sessionID, exists := base.sessions[userID]
	base.mu.RUnlock()
	if exists {
		return sessionID
	}
	sessionID = fmt.Sprintf("%s_%s_%d", base.channelType, userID, time.Now().UnixNano())
	base.mu.Lock()
	base.sessions[userID] = sessionID
	base.mu.Unlock()
	return sessionID
}

func AgentContext(parent context.Context, taskID, sessionID, channelName, userID string) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	ctx := context.WithValue(parent, "task_id", taskID)
	ctx = context.WithValue(ctx, "session_id", sessionID)
	ctx = context.WithValue(ctx, "channel", channelName)
	return context.WithValue(ctx, "user_id", userID)
}

func GenerateWithInterrupts(ctx context.Context, agent Agent, initialMessage string, interruptCh <-chan string, onInterrupt func(string)) (string, error) {
	currentMessage := initialMessage
	for {
		stepCtx, cancel := context.WithCancel(ctx)
		type result struct {
			text string
			err  error
		}
		resultCh := make(chan result, 1)
		go func(message string) {
			text, err := agent.Generate(stepCtx, message)
			resultCh <- result{text: text, err: err}
		}(currentMessage)
		select {
		case generated := <-resultCh:
			cancel()
			return generated.text, generated.err
		case interruptMessage := <-interruptCh:
			cancel()
			currentMessage += "\n\n[用户补充]: " + interruptMessage
			if onInterrupt != nil {
				onInterrupt(interruptMessage)
			}
		case <-ctx.Done():
			cancel()
			return "", ctx.Err()
		}
	}
}

func truncateLog(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
