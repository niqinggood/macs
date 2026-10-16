package permissionpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// PermissionBehavior is the action taken for a matching rule.
type PermissionBehavior string

const (
	BehaviorAllow PermissionBehavior = "allow"
	BehaviorDeny  PermissionBehavior = "deny"
	BehaviorAsk   PermissionBehavior = "ask"
)

// PermissionRuleSource defines where a rule came from, which determines its priority.
// Lower integer = higher priority (matches Claude Code's priority chain).
type PermissionRuleSource int

const (
	SourcePolicy  PermissionRuleSource = 0 // policySettings (managed, highest priority)
	SourceUser    PermissionRuleSource = 1 // userSettings (global ~/.claude/settings.json)
	SourceProject PermissionRuleSource = 2 // projectSettings (.claude/settings.json)
	SourceLocal   PermissionRuleSource = 3 // localSettings (.claude/settings.local.json)
	SourceCLI     PermissionRuleSource = 4 // --permission-* CLI flags
	SourceSession PermissionRuleSource = 5 // /permissions command during session (lowest priority)
)

func (s PermissionRuleSource) String() string {
	switch s {
	case SourcePolicy:
		return "policy"
	case SourceUser:
		return "user"
	case SourceProject:
		return "project"
	case SourceLocal:
		return "local"
	case SourceCLI:
		return "cli"
	case SourceSession:
		return "session"
	}
	return "unknown"
}

// PermissionRule defines a single permission rule.
// ToolName supports glob patterns: "Bash(*)", "Write(/src/**)", "Read(*)", "*"
// RuleContent is the tool's argument content to match (e.g., "npm run lint").
type PermissionRule struct {
	Source      PermissionRuleSource `json:"source" yaml:"source"`
	Behavior    PermissionBehavior   `json:"behavior" yaml:"behavior"`
	ToolName    string               `json:"tool_name" yaml:"tool_name"`
	RuleContent string               `json:"rule_content,omitempty" yaml:"rule_content,omitempty"`
}

type PermissionRulesFile struct {
	Allow []string `json:"allow,omitempty" yaml:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty" yaml:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty" yaml:"ask,omitempty"`
}

// PermissionRuleSet is an ordered set of rules, sorted by priority (lower source number = higher priority).
// Thread-safe for concurrent access.
type PermissionRuleSet struct {
	mu    sync.RWMutex
	rules []PermissionRule
}

func NewPermissionRuleSet() *PermissionRuleSet {
	return &PermissionRuleSet{}
}

// AddRule adds a rule and re-sorts by priority. Higher priority rules
// (lower source number) come first.
func (rs *PermissionRuleSet) AddRule(rule PermissionRule) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.rules = append(rs.rules, rule)
	rs.sort()
}

// AddRules adds multiple rules from a single source.
func (rs *PermissionRuleSet) AddRules(source PermissionRuleSource, rules []PermissionRule) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for i := range rules {
		rules[i].Source = source
		rs.rules = append(rs.rules, rules[i])
	}
	rs.sort()
}

// RemoveRulesBySource removes all rules from a given source.
func (rs *PermissionRuleSet) RemoveRulesBySource(source PermissionRuleSource) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var filtered []PermissionRule
	for _, r := range rs.rules {
		if r.Source != source {
			filtered = append(filtered, r)
		}
	}
	rs.rules = filtered
}

// Resolve checks all rules in priority order and returns the effective behavior
// for a tool call. Returns (behavior, matchingRule, matched).
// When no rule matches, returns (BehaviorAsk, nil, false) — caller should
// fall back to mode-based resolution.
func (rs *PermissionRuleSet) Resolve(toolName, content string) (PermissionBehavior, *PermissionRule, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	for i := range rs.rules {
		r := &rs.rules[i]
		if matchRule(toolName, content, r) {
			copy := *r
			return r.Behavior, &copy, true
		}
	}
	return BehaviorAsk, nil, false
}

// List returns all rules sorted by priority.
func (rs *PermissionRuleSet) List() []PermissionRule {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	result := make([]PermissionRule, len(rs.rules))
	copy(result, rs.rules)
	return result
}

func (rs *PermissionRuleSet) sort() {
	sort.SliceStable(rs.rules, func(i, j int) bool {
		left, right := rs.rules[i], rs.rules[j]
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		leftSpecificity := permissionRuleSpecificity(left)
		rightSpecificity := permissionRuleSpecificity(right)
		if leftSpecificity != rightSpecificity {
			return leftSpecificity > rightSpecificity
		}
		return permissionBehaviorPriority(left.Behavior) > permissionBehaviorPriority(right.Behavior)
	})
}

func permissionRuleSpecificity(rule PermissionRule) int {
	score := 0
	for _, value := range []string{rule.ToolName, rule.RuleContent} {
		for _, r := range value {
			switch r {
			case '*', '?', '[', ']':
				// Wildcards make a rule broader and therefore less specific.
			default:
				score++
			}
		}
	}
	if strings.TrimSpace(rule.RuleContent) != "" {
		score += 1000
	}
	return score
}

func permissionBehaviorPriority(behavior PermissionBehavior) int {
	switch behavior {
	case BehaviorDeny:
		return 3
	case BehaviorAsk:
		return 2
	case BehaviorAllow:
		return 1
	default:
		return 0
	}
}

// matchRule checks if a tool call matches a permission rule.
// ToolName supports glob patterns: "Bash(*)", "Write(/src/**)", etc.
func matchRule(toolName, content string, rule *PermissionRule) bool {
	// Check tool name match (supports glob)
	if !matchGlob(rule.ToolName, toolName) {
		return false
	}

	// If rule has content restriction, check that too
	if rule.RuleContent != "" {
		if !matchPermissionContent(rule.RuleContent, content) {
			return false
		}
	}

	return true
}

func matchPermissionContent(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" {
		return true
	}
	if matchGlob(pattern, value) {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.HasPrefix(value, pattern+" ")
	}
	return false
}

// matchGlob does simple glob matching: * matches any sequence, ? matches any single char.
// Also supports ** for recursive directory matching.
func matchGlob(pattern, value string) bool {
	// Fast path: exact match or wildcard
	if pattern == "*" || pattern == value {
		return true
	}

	// Use filepath.Match for standard glob patterns
	matched, err := filepath.Match(pattern, value)
	if err == nil && matched {
		return true
	}

	// Handle ** pattern (not supported by filepath.Match natively)
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, value)
	}

	return false
}

// matchDoubleStar handles ** glob patterns for recursive matching.
func matchDoubleStar(pattern, value string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) == 1 {
		// No ** — shouldn't happen, but try simple match
		matched, _ := filepath.Match(pattern, value)
		return matched
	}

	// Build a regex-like check: prefix + anything + suffix
	if len(parts) == 2 {
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")
		if prefix == "" && suffix == "" {
			return true
		}
		if prefix == "" {
			return strings.HasSuffix(value, suffix) || strings.Contains(value, "/"+suffix)
		}
		if suffix == "" {
			return strings.HasPrefix(value, prefix)
		}
		return strings.HasPrefix(value, prefix) && strings.HasSuffix(value, suffix)
	}

	// Multiple ** — try each segment
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		if i == 0 {
			// First segment: must match prefix
			if !strings.HasPrefix(value, part) {
				return false
			}
			pos = len(part)
		} else if i == len(parts)-1 {
			// Last segment: must match suffix
			idx := strings.LastIndex(value, part)
			if idx < pos {
				return false
			}
			return strings.HasSuffix(value[idx:], part)
		} else {
			// Middle segment: find anywhere after pos
			remainder := value[pos:]
			idx := strings.Index(remainder, part)
			if idx < 0 {
				return false
			}
			pos += idx + len(part)
		}
	}
	return pos <= len(value)
}

// ====================== Permission Engine ======================

type CallReadOnlyClassifier func(toolName string, args map[string]interface{}) bool

// PermissionEngine resolves managed, user, project, CLI, and session rules for
// every normal tool execution. "ask" decisions are handed to the durable
// approval runtime when AgentOS owns the task.
// Resolution order:
//  1. Apply the plan-mode read-only boundary, when active
//  2. Check rules (policy → user → project → local → cli → session)
//  3. If no rule matches, fall back to PermissionMode-based resolution
type PermissionEngine struct {
	mu               sync.RWMutex
	ruleSet          *PermissionRuleSet
	mode             PermissionMode
	classifyReadOnly ReadOnlyClassifier
	classifyCall     CallReadOnlyClassifier
}

func NewEngine(mode PermissionMode, classifyReadOnly ReadOnlyClassifier) *PermissionEngine {
	return &PermissionEngine{
		ruleSet:          NewPermissionRuleSet(),
		mode:             mode,
		classifyReadOnly: classifyReadOnly,
	}
}

// NewEngineForCalls uses registered per-call effects for plan-mode queries.
// Name-only callers retain the existing NewEngine constructor.
func NewEngineForCalls(mode PermissionMode, classify CallReadOnlyClassifier) *PermissionEngine {
	pe := NewEngine(mode, nil)
	pe.classifyCall = classify
	return pe
}

func (pe *PermissionEngine) CurrentMode() PermissionMode {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.mode
}

func (pe *PermissionEngine) SetMode(mode PermissionMode) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.mode = mode
}

func (pe *PermissionEngine) AddRule(rule PermissionRule) {
	pe.ruleSet.AddRule(rule)
}

func (pe *PermissionEngine) ReplaceRules(source PermissionRuleSource, rules []PermissionRule) {
	if pe == nil || pe.ruleSet == nil {
		return
	}
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.ruleSet.RemoveRulesBySource(source)
	pe.ruleSet.AddRules(source, rules)
}

func (pe *PermissionEngine) ListRules() []PermissionRule {
	if pe == nil || pe.ruleSet == nil {
		return nil
	}
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.ruleSet.List()
}

func (pe *PermissionEngine) HasRules() bool {
	return len(pe.ListRules()) > 0
}

// Check evaluates a tool call against rules + mode.
func (pe *PermissionEngine) Check(toolName string, args map[string]interface{}) (allowed bool, confirmNeeded bool, blockReason string) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	classify := pe.classifyReadOnly
	if pe.classifyCall != nil {
		classify = func(name string) bool { return pe.classifyCall(name, args) }
	}
	// Plan is an execution boundary. A persistent allow/ask rule must not
	// authorize mutations or create an approval that cannot run in this mode.
	modeAllowed, modeConfirm, modeReason := ResolvePermission(pe.mode, toolName, args, classify)
	if pe.mode == PermissionPlan && !modeAllowed {
		return modeAllowed, modeConfirm, modeReason
	}
	content := ExtractAction(args)

	// Step 1: Check rules
	behavior, rule, matched := pe.ruleSet.Resolve(toolName, content)
	if matched {
		switch behavior {
		case BehaviorAllow:
			return true, false, ""
		case BehaviorDeny:
			reason := fmt.Sprintf("tool '%s' denied by %s permission rule", toolName, rule.Source)
			if rule.RuleContent != "" {
				reason += fmt.Sprintf(" [%s %s]", rule.ToolName, rule.RuleContent)
			}
			return false, false, reason
		case BehaviorAsk:
			reason := fmt.Sprintf("tool '%s' requires approval by %s permission rule", toolName, rule.Source)
			if rule.RuleContent != "" {
				reason += fmt.Sprintf(" [%s %s]", rule.ToolName, rule.RuleContent)
			}
			return false, true, reason
		}
	}

	// Step 2: Fall back to mode-based resolution
	return modeAllowed, modeConfirm, modeReason
}

// FormatRuleString returns a human-readable rule string like "Bash(npm run lint)".
func FormatRuleString(toolName, content string) string {
	if content == "" {
		return toolName
	}
	return toolName + "(" + content + ")"
}

func ProjectPermissionRulesPath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "permissions.json")
}

func LoadProjectPermissionRules(workspace string) ([]PermissionRule, error) {
	path := ProjectPermissionRulesPath(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file PermissionRulesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.ToRules(SourceProject), nil
}

func SaveProjectPermissionRules(workspace string, rules []PermissionRule) error {
	var file PermissionRulesFile
	for _, rule := range rules {
		if rule.Source != SourceProject {
			continue
		}
		entry := FormatRuleString(rule.ToolName, rule.RuleContent)
		switch rule.Behavior {
		case BehaviorAllow:
			file.Allow = appendUniqueString(file.Allow, entry)
		case BehaviorDeny:
			file.Deny = appendUniqueString(file.Deny, entry)
		case BehaviorAsk:
			file.Ask = appendUniqueString(file.Ask, entry)
		}
	}
	path := ProjectPermissionRulesPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func (f PermissionRulesFile) ToRules(source PermissionRuleSource) []PermissionRule {
	var rules []PermissionRule
	add := func(behavior PermissionBehavior, entries []string) {
		for _, entry := range entries {
			toolName, content := ParsePermissionRuleString(entry)
			if strings.TrimSpace(toolName) == "" {
				continue
			}
			rules = append(rules, PermissionRule{
				Source:      source,
				Behavior:    behavior,
				ToolName:    toolName,
				RuleContent: content,
			})
		}
	}
	add(BehaviorAllow, f.Allow)
	add(BehaviorDeny, f.Deny)
	add(BehaviorAsk, f.Ask)
	return rules
}

func ParsePermissionRuleString(s string) (toolName, content string) {
	s = strings.TrimSpace(s)
	parenIdx := strings.Index(s, "(")
	if parenIdx < 0 {
		return s, ""
	}
	toolName = strings.TrimSpace(s[:parenIdx])
	if end := strings.LastIndex(s, ")"); end > parenIdx {
		content = strings.TrimSpace(s[parenIdx+1 : end])
	}
	return toolName, content
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
