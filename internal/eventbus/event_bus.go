// Package eventbus provides process-local publish/subscribe for runtime events.
package eventbus

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type Event struct {
	Type      string                 `json:"type"`
	TaskID    string                 `json:"task_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Data      map[string]interface{} `json:"data"`
	Timestamp time.Time              `json:"timestamp"`
}

type Handler func(event Event)

type subscriber struct {
	handler Handler
	filter  string
	id      string
}

type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	nextID      int
	logger      *log.Logger
}

func New(logger *log.Logger) *Bus {
	if logger == nil {
		logger = log.Default()
	}
	return &Bus{subscribers: make(map[string]*subscriber), logger: logger}
}

func (bus *Bus) Subscribe(filter string, handler Handler) func() {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.nextID++
	id := fmt.Sprintf("sub_%d", bus.nextID)
	bus.subscribers[id] = &subscriber{handler: handler, filter: filter, id: id}
	return func() { bus.Unsubscribe(id) }
}

func (bus *Bus) SubscribeAll(handler Handler) func() { return bus.Subscribe("", handler) }

func (bus *Bus) Unsubscribe(id string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	delete(bus.subscribers, id)
}

func (bus *Bus) Emit(event Event) {
	event.Timestamp = time.Now()
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	for _, current := range bus.subscribers {
		if current.filter == "" || current.filter == event.Type {
			go func(target *subscriber, emitted Event) {
				defer func() {
					if recovered := recover(); recovered != nil {
						bus.logger.Printf("[EventBus] subscriber %s panicked: %v", target.id, recovered)
					}
				}()
				target.handler(emitted)
			}(current, event)
		}
	}
}

func (bus *Bus) SubscriberCount() int {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	return len(bus.subscribers)
}

func (bus *Bus) Close() {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.subscribers = make(map[string]*subscriber)
}

const (
	TaskStarted       = "task:started"
	TaskCompleted     = "task:completed"
	TaskFailed        = "task:failed"
	TaskProgress      = "task:progress"
	TaskStopped       = "task:stopped"
	SessionCreated    = "session:created"
	SessionUpdated    = "session:updated"
	MessageSaved      = "message:saved"
	InterruptReceived = "interrupt:received"
	AgentListChanged  = "agent:list_changed"
	ConfigReloaded    = "config:reloaded"
	SkillInstalled    = "skill:installed"
)
