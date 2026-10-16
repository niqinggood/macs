package permissionpolicy

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DenialRecord tracks a single user denial for learning.
type DenialRecord struct {
	ToolName  string    `json:"tool_name"`
	Pattern   string    `json:"pattern"` // argument pattern that was denied
	Reason    string    `json:"reason"`  // user-provided reason
	Count     int       `json:"count"`   // how many times denied
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	AutoDeny  bool      `json:"auto_deny"` // if true, auto-deny without asking
}

// DenialTracker learns from user denials and persists rules.
// Claude Code borrowed: denialTracking in utils/permissions/
type DenialTracker struct {
	mu                 sync.RWMutex
	records            map[string]*DenialRecord // key: toolName|pattern
	filePath           string
	consecutiveDenials int  // consecutive "deny" responses from user
	totalDenials       int  // total denials across all patterns
	maxConsecutive     int  // fallback threshold (default 3)
	maxTotal           int  // max total before fallback (default 20)
	fallbackMode       bool // true = stop auto-deny, always ask user
}

// ShouldFallback returns true if denial tracking suggests falling back to user prompts.
func (dt *DenialTracker) ShouldFallback() bool {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.fallbackMode ||
		dt.consecutiveDenials >= dt.maxConsecutive ||
		dt.totalDenials >= dt.maxTotal
}

// RecordUserDenial tracks when the user explicitly denies a tool call.
func (dt *DenialTracker) RecordUserDenial() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.consecutiveDenials++
	dt.totalDenials++
	if dt.consecutiveDenials >= dt.maxConsecutive || dt.totalDenials >= dt.maxTotal {
		dt.fallbackMode = true
	}
}

// RecordUserApproval resets the consecutive denial counter (user approved something).
func (dt *DenialTracker) RecordUserApproval() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.consecutiveDenials = 0
	if !dt.fallbackMode {
		dt.consecutiveDenials = 0
	}
}

// ResetFallback clears the fallback mode.
func (dt *DenialTracker) ResetFallback() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.fallbackMode = false
	dt.consecutiveDenials = 0
}

func NewDenialTracker(baseDir string) *DenialTracker {
	dir := filepath.Join(baseDir, ".openkitty")
	os.MkdirAll(dir, 0755)
	return &DenialTracker{
		records:        make(map[string]*DenialRecord),
		filePath:       filepath.Join(dir, "denial_rules.json"),
		maxConsecutive: 3,
		maxTotal:       20,
	}
}

// Load reads persisted denial rules from disk.
func (dt *DenialTracker) Load() error {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	data, err := os.ReadFile(dt.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh start
		}
		return err
	}
	return json.Unmarshal(data, &dt.records)
}

// Save persists denial rules to disk (public, acquires lock).
func (dt *DenialTracker) Save() error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	return dt.saveLocked()
}

// saveLocked writes records to disk. Caller must hold dt.mu write lock.
func (dt *DenialTracker) saveLocked() error {
	data, err := json.MarshalIndent(dt.records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dt.filePath, data, 0644)
}

// RecordDenial records a user denial and decides if auto-deny should be enabled.
func (dt *DenialTracker) RecordDenial(toolName string, args map[string]interface{}, reason string) *DenialRecord {
	dt.mu.Lock()
	pattern := ExtractDenialPattern(toolName, args)
	key := toolName + "|" + pattern

	record, exists := dt.records[key]
	if !exists {
		record = &DenialRecord{
			ToolName:  toolName,
			Pattern:   pattern,
			Reason:    reason,
			Count:     1,
			FirstSeen: time.Now(),
			LastSeen:  time.Now(),
		}
		dt.records[key] = record
	} else {
		record.Count++
		record.LastSeen = time.Now()
		if reason != "" {
			record.Reason = reason
		}
		if record.Count >= 2 {
			record.AutoDeny = true
			log.Printf("[DENIAL_TRACK] Auto-deny enabled for %s: %s (denied %d times)", toolName, pattern, record.Count)
		}
	}
	dt.saveLocked()

	dt.mu.Unlock()
	return record
}

// ShouldAutoDeny checks if a tool call should be auto-denied based on past patterns.
func (dt *DenialTracker) ShouldAutoDeny(toolName string, args map[string]interface{}) (bool, string) {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	pattern := ExtractDenialPattern(toolName, args)
	key := toolName + "|" + pattern

	if record, exists := dt.records[key]; exists && record.AutoDeny {
		return true, record.Reason
	}

	// Also check tool-level denials (any arguments)
	keyTool := toolName + "|*"
	if record, exists := dt.records[keyTool]; exists && record.AutoDeny {
		return true, record.Reason
	}

	return false, ""
}

// GetRules returns all denial rules for display.
func (dt *DenialTracker) GetRules() []DenialRecord {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	var rules []DenialRecord
	for _, r := range dt.records {
		if r.AutoDeny {
			rules = append(rules, *r)
		}
	}
	return rules
}

// RemoveRule removes a denial rule (user override).
func (dt *DenialTracker) RemoveRule(toolName, pattern string) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	key := toolName + "|" + pattern
	delete(dt.records, key)
	return dt.saveLocked()
}

// Reset clears all denial rules.
func (dt *DenialTracker) Reset() error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.records = make(map[string]*DenialRecord)
	return dt.saveLocked()
}

// extractDenialPattern creates a stable key from tool arguments.
func (dt *DenialTracker) MergeFrom(other *DenialTracker) {
	other.mu.RLock()
	defer other.mu.RUnlock()
	dt.mu.Lock()
	defer dt.mu.Unlock()
	for key, rec := range other.records {
		if existing, ok := dt.records[key]; ok {
			existing.Count += rec.Count
			if rec.LastSeen.After(existing.LastSeen) {
				existing.LastSeen = rec.LastSeen
				existing.AutoDeny = rec.AutoDeny
			}
		} else {
			cp := *rec
			dt.records[key] = &cp
		}
	}
	dt.saveLocked()
}

// ExtractDenialPattern creates a stable permission-learning key from tool arguments.
func ExtractDenialPattern(toolName string, args map[string]interface{}) string {
	if args == nil {
		return "*"
	}
	switch toolName {
	case "cli":
		if cmd, ok := args["command"].(string); ok {
			// Extract base command (first word)
			parts := strings.Fields(cmd)
			if len(parts) > 0 {
				return "cmd:" + parts[0]
			}
		}
	case "web_fetch":
		if url, ok := args["url"].(string); ok {
			// Extract domain
			domain := url
			if idx := strings.Index(url, "://"); idx > 0 {
				domain = url[idx+3:]
			}
			if idx := strings.Index(domain, "/"); idx > 0 {
				domain = domain[:idx]
			}
			return "domain:" + domain
		}
	case "write_file", "edit_file", "json_patch":
		if path, ok := args["path"].(string); ok {
			// Extract directory pattern
			dir := filepath.ToSlash(filepath.Dir(path))
			return "dir:" + dir
		}
	case "git":
		if action, ok := args["action"].(string); ok {
			return "action:" + action
		}
	}
	// Default: use a hash of args
	argsJSON, _ := json.Marshal(args)
	return fmt.Sprintf("args:%x", HashString(string(argsJSON))[:8])
}

// HashString returns the legacy stable hash used by persisted denial rules.
func HashString(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%x", h)
}
