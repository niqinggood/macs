package globalstate

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"mac/internal/eventbus"
	"mac/internal/featureflags"
	"mac/internal/permissionpolicy"
	"mac/internal/processutil"
	"mac/internal/runtimeutil"
	"mac/internal/security"
)

var (
	terminalInstance *processutil.TerminalManager
	terminalOnce     sync.Once
	shellJobs        *processutil.ShellJobManager
	shellJobsOnce    sync.Once
)

func TerminalManager(resolveOwner processutil.ShellOwnerResolver, canAccess processutil.ShellAccessChecker) *processutil.TerminalManager {
	terminalOnce.Do(func() {
		terminalInstance = processutil.NewTerminalManager(resolveOwner, canAccess)
	})
	return terminalInstance
}

func ShellJobs(resolveOwner processutil.ShellOwnerResolver, canAccess processutil.ShellAccessChecker) *processutil.ShellJobManager {
	shellJobsOnce.Do(func() {
		shellJobs = processutil.NewShellJobManager(resolveOwner, canAccess)
	})
	return shellJobs
}

var cacheBreakDetector = runtimeutil.NewCacheBreakDetector()

func CacheBreakDetector() *runtimeutil.CacheBreakDetector { return cacheBreakDetector }

var eventLog *eventbus.EventLogger

// InitEventLog initializes the global event logger. Safe to call multiple times.
func InitEventLog(workspace string) {
	if eventLog == nil {
		eventLog = eventbus.NewEventLogger(workspace)
	}
}

func EventLog() *eventbus.EventLogger { return eventLog }

var denialTracker *permissionpolicy.DenialTracker

// InitGlobalDenialTracker creates the global denial tracker singleton.
func InitGlobalDenialTracker(workspace string) {
	if denialTracker == nil {
		denialTracker = permissionpolicy.NewDenialTracker(workspace)
		_ = denialTracker.Load()
	}
}

func DenialTracker() *permissionpolicy.DenialTracker { return denialTracker }

var promptInjectionDetector *security.PromptInjectionDetector

func InitPromptInjectionDefense() {
	promptInjectionDetector = security.NewPromptInjectionDetector()
	log.Printf("[SECURITY] Prompt注入防御已初始化，规则数: %d", promptInjectionDetector.RuleCount())
}

func PromptInjectionDetector() *security.PromptInjectionDetector { return promptInjectionDetector }

// ====================== Global State Store ======================
// Wraps module-level singletons in Store[T] for testability and isolation.
// Each singleton now has: constructor, Get* accessor, Set*/Update* mutator, Subscribe* listener.
//
// Migration strategy: existing global variables remain as convenience accessors,
// but the backing state is in Store[T] so tests can fork/replace state cleanly.

// ====================== Feature Flags Store ======================

var (
	featureStore     *runtimeutil.KVStore
	featureStoreOnce sync.Once
)

// GetFeatureStore returns the global feature flag store, creating it lazily.
func GetFeatureStore() *runtimeutil.KVStore {
	featureStoreOnce.Do(func() {
		initial := make(map[string]interface{})
		if features := featureflags.Global(); features != nil {
			// Seed from existing global singleton
			initial["compaction"] = features.IsEnabled(featureflags.Compaction)
			initial["auto_mode"] = features.IsEnabled(featureflags.AutoMode)
			initial["hooks"] = features.IsEnabled(featureflags.Hooks)
			initial["rag"] = features.IsEnabled(featureflags.RAG)
		}
		featureStore = runtimeutil.NewKVStore(initial)
	})
	return featureStore
}

// ResetFeatureStore replaces the store (for testing).
func ResetFeatureStore(initial map[string]interface{}) {
	featureStore = runtimeutil.NewKVStore(initial)
	featureStoreOnce = sync.Once{}
	featureStoreOnce.Do(func() {}) // mark as done
}

// ====================== Denial Tracker Store ======================

var (
	denialStore     *runtimeutil.Store[[]permissionpolicy.DenialRecord]
	denialStoreOnce sync.Once
	denialStoreMu   sync.Mutex // for file persistence
	denialFilePath  string
)

func GetDenialStore() *runtimeutil.Store[[]permissionpolicy.DenialRecord] {
	denialStoreOnce.Do(func() {
		denialStore = runtimeutil.NewStore[[]permissionpolicy.DenialRecord](nil,
			runtimeutil.WithOnChange(func(newVal, oldVal []permissionpolicy.DenialRecord) {
				persistDenialStore(newVal)
			}),
		)
	})
	return denialStore
}

func InitDenialStore(baseDir string) {
	denialStoreMu.Lock()
	defer denialStoreMu.Unlock()
	dir := filepath.Join(baseDir, ".openkitty")
	os.MkdirAll(dir, 0755)
	denialFilePath = filepath.Join(dir, "denial_store.json")
	loadDenialStore()
}

func loadDenialStore() {
	data, err := os.ReadFile(denialFilePath)
	if err != nil {
		return
	}
	var records []permissionpolicy.DenialRecord
	if json.Unmarshal(data, &records) == nil {
		st := GetDenialStore()
		st.SetState(func(prev []permissionpolicy.DenialRecord) []permissionpolicy.DenialRecord { return records })
	}
}

func persistDenialStore(records []permissionpolicy.DenialRecord) {
	if denialFilePath == "" {
		return
	}
	data, _ := json.MarshalIndent(records, "", "  ")
	os.WriteFile(denialFilePath, data, 0644)
}

// AddDenialRecord adds a denial to the global store.
func AddDenialRecord(record permissionpolicy.DenialRecord) {
	st := GetDenialStore()
	st.Mutate(func(state *[]permissionpolicy.DenialRecord) {
		for i, r := range *state {
			if r.ToolName == record.ToolName && r.Pattern == record.Pattern {
				(*state)[i].Count++
				(*state)[i].LastSeen = record.LastSeen
				return
			}
		}
		*state = append(*state, record)
	})
}

// QueryDenialStore checks if a tool+pattern should be auto-denied.
func QueryDenialStore(toolName, pattern string) (bool, string) {
	for _, r := range GetDenialStore().GetState() {
		if r.AutoDeny && r.ToolName == toolName && (r.Pattern == pattern || r.Pattern == "*") {
			return true, r.Reason
		}
	}
	return false, ""
}

// ResetDenialStore clears the store (for testing).
func ResetDenialStore() {
	denialStore = nil
	denialStoreOnce = sync.Once{}
}

// ====================== Event Log Store ======================

var (
	eventLogStore     *runtimeutil.Store[[]eventbus.AgentEvent]
	eventLogStoreOnce sync.Once
	eventLogMu        sync.Mutex
	eventLogFilePath  string
)

func GetEventLogStore() *runtimeutil.Store[[]eventbus.AgentEvent] {
	eventLogStoreOnce.Do(func() {
		eventLogStore = runtimeutil.NewStore[[]eventbus.AgentEvent](nil,
			runtimeutil.WithOnChange(func(newVal, oldVal []eventbus.AgentEvent) {
				persistEventLogStore(newVal)
			}),
		)
	})
	return eventLogStore
}

func InitEventLogStore(baseDir string) {
	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	dir := filepath.Join(baseDir, ".openkitty")
	os.MkdirAll(dir, 0755)
	eventLogFilePath = filepath.Join(dir, "event_store.jsonl")
}

func persistEventLogStore(events []eventbus.AgentEvent) {
	if eventLogFilePath == "" {
		return
	}
	f, err := os.OpenFile(eventLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("[EventStore] persist failed: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		enc.Encode(e)
	}
}

func AppendEventLog(event eventbus.AgentEvent) {
	st := GetEventLogStore()
	st.SetState(func(prev []eventbus.AgentEvent) []eventbus.AgentEvent {
		if len(prev) > 5000 {
			prev = prev[len(prev)-4000:] // keep last 4000
		}
		return append(prev, event)
	})
}

func ResetEventLogStore() {
	eventLogStore = nil
	eventLogStoreOnce = sync.Once{}
}

// ====================== Concurrency Store ======================

// ConcurrentStore is a typed wrapper that combines Store[T] with persistence.
type ConcurrentStore[T any] struct {
	inner     *runtimeutil.Store[T]
	mu        sync.Mutex
	filePath  string
	marshal   func(T) ([]byte, error)
	unmarshal func([]byte) (T, error)
}

// NewConcurrentStore creates a typed store with optional file persistence.
func NewConcurrentStore[T any](filePath string, marshal func(T) ([]byte, error), unmarshal func([]byte) (T, error), zero T) *ConcurrentStore[T] {
	cs := &ConcurrentStore[T]{
		inner:     runtimeutil.NewStore[T](zero),
		filePath:  filePath,
		marshal:   marshal,
		unmarshal: unmarshal,
	}
	if filePath != "" {
		cs.load()
		// Auto-persist on change
		cs.inner.Subscribe(func() {
			cs.persist()
		})
	}
	return cs
}

func (cs *ConcurrentStore[T]) Get() T {
	return cs.inner.GetState()
}

func (cs *ConcurrentStore[T]) Update(updater func(prev T) T) bool {
	return cs.inner.SetState(updater)
}

func (cs *ConcurrentStore[T]) Subscribe(fn func()) func() {
	return cs.inner.Subscribe(fn)
}

func (cs *ConcurrentStore[T]) load() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	data, err := os.ReadFile(cs.filePath)
	if err != nil {
		return
	}
	val, err := cs.unmarshal(data)
	if err != nil {
		return
	}
	cs.inner.SetState(func(prev T) T { return val })
}

func (cs *ConcurrentStore[T]) persist() {
	if cs.filePath == "" {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	data, err := cs.marshal(cs.inner.GetState())
	if err != nil {
		return
	}
	os.WriteFile(cs.filePath, data, 0644)
}
