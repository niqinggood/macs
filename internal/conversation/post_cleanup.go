package conversation

import "sync"

type CleanupScope string

const (
	CleanupMainThread CleanupScope = "main"
	CleanupSubAgent   CleanupScope = "subagent"
)

type cleanableCache struct {
	name  string
	clear func()
	scope CleanupScope
}

type PostCompactCleanup struct {
	mu     sync.Mutex
	caches []cleanableCache
}

var globalPostCompactCleanup = &PostCompactCleanup{}

func NewPostCompactCleanup() *PostCompactCleanup {
	return &PostCompactCleanup{}
}

func (cleanup *PostCompactCleanup) Register(name string, clear func(), scope CleanupScope) {
	cleanup.mu.Lock()
	defer cleanup.mu.Unlock()
	cleanup.caches = append(cleanup.caches, cleanableCache{name: name, clear: clear, scope: scope})
}

func (cleanup *PostCompactCleanup) Run(scope CleanupScope) {
	cleanup.mu.Lock()
	defer cleanup.mu.Unlock()
	for _, cache := range cleanup.caches {
		if cache.scope == CleanupSubAgent || cache.scope == scope {
			cache.clear()
		}
	}
}

func RegisterCleanableCache(name string, clear func(), scope CleanupScope) {
	globalPostCompactCleanup.Register(name, clear, scope)
}

func RunPostCompactCleanup(scope CleanupScope) {
	globalPostCompactCleanup.Run(scope)
}

type cleanableFileCache struct {
	mu    sync.RWMutex
	cache map[string]string
}

var fileContentCache = &cleanableFileCache{cache: make(map[string]string)}

type cleanableSessionCache struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

var sessionMessageCache = &cleanableSessionCache{data: make(map[string]interface{})}

type cleanableContextCache struct {
	mu      sync.RWMutex
	gitInfo *cachedGitInfo
}

type cachedGitInfo struct {
	status string
	hash   string
}

var contextCache = &cleanableContextCache{}

type cleanableMemoryCache struct {
	mu       sync.RWMutex
	memFiles []string
}

var memoryFileCache = &cleanableMemoryCache{}

func init() {
	RegisterCleanableCache("file_content", func() {
		fileContentCache.mu.Lock()
		defer fileContentCache.mu.Unlock()
		fileContentCache.cache = make(map[string]string)
	}, CleanupMainThread)
	RegisterCleanableCache("session_messages", func() {
		sessionMessageCache.mu.Lock()
		defer sessionMessageCache.mu.Unlock()
		sessionMessageCache.data = make(map[string]interface{})
	}, CleanupMainThread)
	RegisterCleanableCache("context", func() {
		contextCache.mu.Lock()
		defer contextCache.mu.Unlock()
		contextCache.gitInfo = nil
	}, CleanupSubAgent)
	RegisterCleanableCache("memory_files", func() {
		memoryFileCache.mu.Lock()
		defer memoryFileCache.mu.Unlock()
		memoryFileCache.memFiles = nil
	}, CleanupSubAgent)
}
