// Package featureflags owns the thread-safe runtime feature registry.
package featureflags

import (
	"fmt"
	"strings"
	"sync"
)

type Flag string

const (
	Hooks            Flag = "hooks"
	Compaction       Flag = "compaction"
	MCP              Flag = "mcp"
	Telemetry        Flag = "telemetry"
	Coordinator      Flag = "coordinator"
	Worktree         Flag = "worktree"
	SessionMemory    Flag = "session_memory"
	FileHistory      Flag = "file_history"
	TaskV2           Flag = "task_v2"
	Commercial       Flag = "commercial"
	CommercialDE     Flag = "commercial:de"
	CommercialTools  Flag = "commercial:tools"
	TokenBudget      Flag = "token_budget"
	LoopDetection    Flag = "loop_detection"
	ToolOutputBudget Flag = "tool_output_budget"
	DenialTracker    Flag = "denial_tracker"
	Guardrail        Flag = "guardrail"
	VoiceInput       Flag = "voice"
	RAG              Flag = "rag"
	Sandbox          Flag = "sandbox"
	AutoMode         Flag = "auto_mode"
	Ollama           Flag = "ollama"
)

var all = []Flag{
	Hooks, Compaction, MCP, Telemetry, Coordinator, Worktree,
	SessionMemory, FileHistory, TaskV2,
	Commercial, CommercialDE, CommercialTools,
	TokenBudget, LoopDetection, ToolOutputBudget, DenialTracker, Guardrail,
	VoiceInput, RAG, Sandbox, AutoMode, Ollama,
}

type Registry struct {
	mu         sync.RWMutex
	flags      map[Flag]bool
	defaultAll bool
}

var global = NewRegistry(true)

func NewRegistry(defaultAll bool) *Registry {
	return &Registry{flags: make(map[Flag]bool), defaultAll: defaultAll}
}

func Global() *Registry { return global }

func All() []Flag { return append([]Flag(nil), all...) }

func (registry *Registry) Enable(flag Flag) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.flags[flag] = true
}

func (registry *Registry) Disable(flag Flag) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.flags[flag] = false
}

func (registry *Registry) IsEnabled(flag Flag) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if enabled, ok := registry.flags[flag]; ok {
		return enabled
	}
	return registry.defaultAll
}

// Load applies product tier and explicitly disabled flag names.
func (registry *Registry) Load(commercial bool, disabled []string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.flags[Commercial] = commercial
	registry.flags[CommercialDE] = commercial
	registry.flags[CommercialTools] = commercial
	for _, name := range disabled {
		for _, flag := range all {
			if strings.EqualFold(string(flag), name) {
				registry.flags[flag] = false
				break
			}
		}
	}
}

func (registry *Registry) Status() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var result strings.Builder
	result.WriteString("## Feature Flags\n\n")
	for _, flag := range all {
		enabled := registry.defaultAll
		if value, ok := registry.flags[flag]; ok {
			enabled = value
		}
		icon := "✅"
		if !enabled {
			icon = "❌"
		}
		result.WriteString(fmt.Sprintf("- %s `%s`\n", icon, flag))
	}
	return result.String()
}
