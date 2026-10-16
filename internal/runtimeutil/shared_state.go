package runtimeutil

import "sync"

// SharedState is a concurrency-safe state object shared by agents and UI
// adapters in one process.
type SharedState struct {
	mu    sync.RWMutex
	state map[string]interface{}
}

func NewSharedState() *SharedState {
	return &SharedState{state: make(map[string]interface{})}
}

func (s *SharedState) Get(key string) interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return nil
	}
	if key != "" {
		return s.state[key]
	}
	return s.state
}

func (s *SharedState) Set(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]interface{})
	}
	s.state[key] = value
}

func (s *SharedState) Merge(updates map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]interface{})
	}
	for key, value := range updates {
		s.state[key] = value
	}
}

func (s *SharedState) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyOfState := make(map[string]interface{}, len(s.state))
	for key, value := range s.state {
		copyOfState[key] = value
	}
	return copyOfState
}

func (s *SharedState) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
}

func (s *SharedState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = make(map[string]interface{})
}
