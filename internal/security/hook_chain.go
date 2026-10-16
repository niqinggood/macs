package security

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

// ====================== Security Hook System ======================

// HookEvent represents a security-relevant event.
type HookEvent struct {
	Type    string // "tool_call", "message_in", "message_out", "file_access", "cli_exec"
	Agent   string
	Tool    string
	Content string
	Params  map[string]interface{}
}

// HookResult is the decision from a security hook.
type HookResult struct {
	Allowed bool
	Reason  string
	Action  string // "allow", "block", "warn", "redact"
}

// SecurityHook is a function that inspects events and returns a decision.
type SecurityHook func(event *HookEvent) *HookResult

// SecurityHookChain manages a chain of security hooks.
type SecurityHookChain struct {
	hooks  []SecurityHook
	logger *log.Logger
	mu     sync.RWMutex
	stats  map[string]int // event type → count
}

func NewSecurityHookChain(logger *log.Logger) *SecurityHookChain {
	chain := &SecurityHookChain{
		logger: logger,
		stats:  make(map[string]int),
	}
	// Register built-in hooks
	chain.Add(pathTraversalHook)
	chain.Add(commandInjectionHook)
	chain.Add(destructiveCommandHook)
	return chain
}

func (c *SecurityHookChain) Add(hook SecurityHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = append(c.hooks, hook)
}

// Check runs all hooks against an event. Returns the first blocking result, or allow.
func (c *SecurityHookChain) Check(event *HookEvent) *HookResult {
	c.mu.Lock()
	c.stats[event.Type]++
	c.mu.Unlock()

	for _, hook := range c.hooks {
		result := hook(event)
		if result == nil {
			continue
		}
		if !result.Allowed {
			c.logger.Printf("[SECURITY] BLOCKED %s: %s (reason: %s)", event.Type, event.Tool, result.Reason)
			return result
		}
		if result.Action == "redact" {
			c.logger.Printf("[SECURITY] REDACT %s: %s", event.Type, event.Tool)
			return result
		}
	}
	return &HookResult{Allowed: true, Action: "allow"}
}

// Stats returns hook invocation statistics.
func (c *SecurityHookChain) Stats() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int)
	for k, v := range c.stats {
		out[k] = v
	}
	return out
}

// ====================== Built-in Security Hooks ======================

// pathTraversalHook blocks path traversal attempts.
func pathTraversalHook(event *HookEvent) *HookResult {
	dangerous := []string{"../", "..\\", "/etc/passwd", "/etc/shadow", "C:\\Windows\\System32"}
	for _, pattern := range dangerous {
		if strings.Contains(event.Content, pattern) {
			return &HookResult{Allowed: false, Reason: fmt.Sprintf("path traversal detected: %s", pattern)}
		}
	}
	if event.Params != nil {
		for _, v := range event.Params {
			if s, ok := v.(string); ok {
				for _, pattern := range dangerous {
					if strings.Contains(s, pattern) {
						return &HookResult{Allowed: false, Reason: fmt.Sprintf("path traversal in params: %s", pattern)}
					}
				}
			}
		}
	}
	return nil
}

// commandInjectionHook blocks shell injection patterns.
func commandInjectionHook(event *HookEvent) *HookResult {
	if event.Type != "cli_exec" {
		return nil
	}
	patterns := []string{";rm ", ";rm -rf", "| sh", "| bash", "$(curl", "`curl", "; wget", "; chmod"}
	for _, p := range patterns {
		if strings.Contains(event.Content, p) {
			return &HookResult{Allowed: false, Reason: fmt.Sprintf("command injection: %s", p)}
		}
	}
	return nil
}

// destructiveCommandHook warns about destructive file operations.
func destructiveCommandHook(event *HookEvent) *HookResult {
	if event.Type != "cli_exec" && event.Type != "tool_call" {
		return nil
	}
	dangerous := map[string]string{
		"rm -rf /":   "attempting to delete root",
		"rm -rf ~":   "attempting to delete home",
		"drop table": "database table drop (case-insensitive match)",
		"format c:":  "disk format attempt",
		"> /dev/sda": "raw disk write",
	}
	lower := strings.ToLower(event.Content)
	for pattern, reason := range dangerous {
		if strings.Contains(lower, pattern) {
			return &HookResult{Allowed: false, Reason: reason}
		}
	}
	return nil
}
