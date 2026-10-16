package hooks

import (
	"fmt"
	"sync"
)

// ====================== Callback Hook Executor ======================
// Allows Go code to register callback functions as hooks.

type HookCallbackExecutor struct {
	mu        sync.RWMutex
	callbacks map[string]HookCallbackFunc
}

func NewHookCallbackExecutor() *HookCallbackExecutor {
	return &HookCallbackExecutor{
		callbacks: make(map[string]HookCallbackFunc),
	}
}

func (e *HookCallbackExecutor) Register(id string, fn HookCallbackFunc) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callbacks[id] = fn
	return func() { e.Unregister(id) }
}

func (e *HookCallbackExecutor) Unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.callbacks, id)
}

func (e *HookCallbackExecutor) Execute(cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	id := cfg.Command
	if id == "" {
		return nil, fmt.Errorf("callback hook requires 'command' field with callback ID")
	}

	e.mu.RLock()
	fn, ok := e.callbacks[id]
	e.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("callback hook %q not registered", id)
	}

	result := fn(hookCtx)
	if result == nil {
		result = &HookResultV2{Decision: HookDecisionAllow}
	}
	result.ExecutorType = HookExecCallback
	return result, nil
}

func (e *HookCallbackExecutor) ListRegistered() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]string, 0, len(e.callbacks))
	for id := range e.callbacks {
		ids = append(ids, id)
	}
	return ids
}
