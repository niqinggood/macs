package runtimeutil

import (
	"reflect"
	"sync"
)

// ====================== Reactive State Store ======================
// Claude Code 借鉴: Store<T> in src/state/store.ts
//
// Minimal reactive state container — a simplified Redux/Zustand without dependencies.
// Key design:
//   - Functional updater pattern: setState(prev => next), not setState(next)
//   - Deep equality bail-out: if updater returns same value, no listeners fire
//   - Set-based listeners: O(1) add/remove, natural dedup
//   - onChange callback: separate from subscriber list for side effects (persistence, logging)
//
// Usage pattern to replace global singletons:
//   var appStore = NewStore[AppState](initialState)

// Option is a functional option for Store configuration.
type StoreOption[T any] func(*Store[T])

// WithOnChange sets a callback fired after every successful state update.
func WithOnChange[T any](fn func(newVal, oldVal T)) StoreOption[T] {
	return func(s *Store[T]) {
		s.onChange = fn
	}
}

// Store is a thread-safe reactive state container.
type Store[T any] struct {
	mu        sync.RWMutex
	state     T
	listeners map[uint64]func() // Set-based, keyed by registration order
	nextID    uint64
	onChange  func(newVal, oldVal T)
}

// NewStore creates a new reactive store with initial state.
func NewStore[T any](initial T, opts ...StoreOption[T]) *Store[T] {
	s := &Store[T]{
		state:     initial,
		listeners: make(map[uint64]func()),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// GetState returns the current state snapshot.
func (s *Store[T]) GetState() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState applies a functional update. If the updater returns a value
// deeply equal to the current state, no listeners are notified.
// Returns true if state actually changed.
func (s *Store[T]) SetState(updater func(prev T) T) bool {
	s.mu.Lock()
	oldVal := s.state
	newVal := updater(oldVal)

	if reflect.DeepEqual(oldVal, newVal) {
		s.mu.Unlock()
		return false
	}

	s.state = newVal
	onChange := s.onChange
	listeners := make([]func(), 0, len(s.listeners))
	for _, ln := range s.listeners {
		listeners = append(listeners, ln)
	}
	s.mu.Unlock()

	// Fire onChange first (synchronous side effects)
	if onChange != nil {
		onChange(newVal, oldVal)
	}

	// Fire subscribers (fire-and-forget, non-blocking)
	for _, ln := range listeners {
		ln()
	}

	return true
}

// Subscribe registers a listener that fires on every state change.
// Returns an unsubscribe function. Listener receives no arguments
// (call GetState() inside the listener to read current value).
func (s *Store[T]) Subscribe(listener func()) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.listeners[id] = listener
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}

// Mutate applies a mutating function to the state in-place. The function
// receives a pointer to the current state. After mutation, listeners are fired.
// This is a convenience for callers that need pointer-based mutation.
// It always fires listeners (no equality check possible with in-place mutation).
func (s *Store[T]) Mutate(fn func(state *T)) {
	s.mu.Lock()
	fn(&s.state)
	onChange := s.onChange
	listeners := make([]func(), 0, len(s.listeners))
	for _, ln := range s.listeners {
		listeners = append(listeners, ln)
	}
	s.mu.Unlock()

	if onChange != nil {
		onChange(s.state, s.state) // oldVal inaccessible after mutation
	}
	for _, ln := range listeners {
		ln()
	}
}

// ====================== Key-value Store ======================

// KVStore is a reactive key-value store built on Store.
// Suitable for simple configuration or feature flag state.
type KVStore struct {
	inner *Store[map[string]interface{}]
}

// NewKVStore creates a new key-value store.
func NewKVStore(initial map[string]interface{}) *KVStore {
	return &KVStore{inner: NewStore(initial)}
}

// Get retrieves a value by key.
func (kv *KVStore) Get(key string) (interface{}, bool) {
	state := kv.inner.GetState()
	v, ok := state[key]
	return v, ok
}

// Set updates or inserts a key-value pair. Returns true if changed.
func (kv *KVStore) Set(key string, value interface{}) bool {
	return kv.inner.SetState(func(prev map[string]interface{}) map[string]interface{} {
		if prev == nil {
			prev = make(map[string]interface{})
		}
		if old, ok := prev[key]; ok && reflect.DeepEqual(old, value) {
			return prev // no change
		}
		next := make(map[string]interface{}, len(prev)+1)
		for k, v := range prev {
			next[k] = v
		}
		next[key] = value
		return next
	})
}

// Delete removes a key. Returns true if the key existed.
func (kv *KVStore) Delete(key string) bool {
	return kv.inner.SetState(func(prev map[string]interface{}) map[string]interface{} {
		if prev == nil {
			return prev
		}
		if _, ok := prev[key]; !ok {
			return prev
		}
		next := make(map[string]interface{}, len(prev)-1)
		for k, v := range prev {
			if k != key {
				next[k] = v
			}
		}
		return next
	})
}

// Subscribe subscribes to KVStore changes.
func (kv *KVStore) Subscribe(listener func()) func() {
	return kv.inner.Subscribe(listener)
}

// Snapshot returns the current key-value map.
func (kv *KVStore) Snapshot() map[string]interface{} {
	return kv.inner.GetState()
}
