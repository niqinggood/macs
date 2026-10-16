package agents

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/permissionpolicy"

	"gopkg.in/yaml.v3"
)

// AgentLoader discovers and loads agent definitions from directory.
// Claude Code borrowed: loadAgentsDir in tools/AgentTool/loadAgentsDir.ts
type AgentLoader struct {
	agentsDir  string
	autoReload bool
	lastMod    map[string]time.Time
}

func NewAgentLoader(agentsDir string) *AgentLoader {
	if agentsDir == "" {
		agentsDir = ".openkitty/agents"
	}
	os.MkdirAll(agentsDir, 0755)
	return &AgentLoader{
		agentsDir:  agentsDir,
		autoReload: false,
		lastMod:    make(map[string]time.Time),
	}
}

// DiscoveredAgent is an OpenKitty agent definition.
// It keeps the old lightweight fields while accepting richer Claude Code-style
// governance fields such as tool deny-lists, permission mode, skills and memory.
type DiscoveredAgent struct {
	Name            string            `json:"name" yaml:"name"`
	Description     string            `json:"description" yaml:"description"`
	Role            string            `json:"role" yaml:"role"`
	AgentType       string            `json:"type,omitempty" yaml:"type,omitempty"`
	WhenToUse       string            `json:"when_to_use,omitempty" yaml:"when_to_use,omitempty"`
	SystemPrompt    string            `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Prompt          string            `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	InitialPrompt   string            `json:"initial_prompt,omitempty" yaml:"initial_prompt,omitempty"`
	Tools           []string          `json:"tools,omitempty" yaml:"tools,omitempty"`
	DisallowedTools []string          `json:"disallowed_tools,omitempty" yaml:"disallowed_tools,omitempty"`
	Model           string            `json:"model,omitempty" yaml:"model,omitempty"`
	Effort          string            `json:"effort,omitempty" yaml:"effort,omitempty"`
	PermissionMode  string            `json:"permission_mode,omitempty" yaml:"permission_mode,omitempty"`
	MCPServers      []string          `json:"mcp_servers,omitempty" yaml:"mcp_servers,omitempty"`
	Hooks           []string          `json:"hooks,omitempty" yaml:"hooks,omitempty"`
	MaxTurns        int               `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
	Skills          []string          `json:"skills,omitempty" yaml:"skills,omitempty"`
	Memory          AgentMemoryPolicy `json:"memory,omitempty" yaml:"memory,omitempty"`
	Background      bool              `json:"background,omitempty" yaml:"background,omitempty"`
	Isolation       string            `json:"isolation,omitempty" yaml:"isolation,omitempty"`
	Tier            string            `json:"tier,omitempty" yaml:"tier,omitempty"`
	Source          string            `json:"source,omitempty" yaml:"source,omitempty"`
	SourceFile      string            `json:"source_file,omitempty"`
}

type AgentMemoryPolicy struct {
	User    bool `json:"user,omitempty" yaml:"user,omitempty"`
	Project bool `json:"project,omitempty" yaml:"project,omitempty"`
	Local   bool `json:"local,omitempty" yaml:"local,omitempty"`
}

// DiscoverAgents scans the agents directory and returns all discovered agents.
func (al *AgentLoader) DiscoverAgents() ([]DiscoveredAgent, error) {
	entries, err := os.ReadDir(al.agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read agents directory: %w", err)
	}

	var agents []DiscoveredAgent
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}

		filePath := filepath.Join(al.agentsDir, entry.Name())
		fileAgents, err := al.parseAgentFile(filePath, ext)
		if err != nil {
			log.Printf("[AGENT_LOADER] Failed to parse '%s': %v", entry.Name(), err)
			continue
		}

		for i := range fileAgents {
			fileAgents[i].SourceFile = filePath
			fileAgents[i].normalize()
		}
		agents = append(agents, fileAgents...)
		log.Printf("[AGENT_LOADER] Loaded %d agent(s) from '%s'", len(fileAgents), entry.Name())
	}

	return agents, nil
}

// DiscoverAllAgents returns built-in definitions plus file-based definitions.
// File-based definitions override built-ins with the same name.
func (al *AgentLoader) DiscoverAllAgents() ([]DiscoveredAgent, error) {
	fileAgents, err := al.DiscoverAgents()
	if err != nil {
		return nil, err
	}
	return mergeAgentDefinitions(BuiltinAgentDefinitions(), fileAgents), nil
}

// parseAgentFile parses a single agent definition file.
// Supports both single agent and array of agents.
func (al *AgentLoader) parseAgentFile(filePath, ext string) ([]DiscoveredAgent, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var agents []DiscoveredAgent

	// Try parsing as array first
	var array []DiscoveredAgent
	var parseErr error
	if ext == ".json" {
		parseErr = json.Unmarshal(data, &array)
	} else {
		parseErr = yaml.Unmarshal(data, &array)
	}

	if parseErr == nil && len(array) > 0 {
		for i := range array {
			array[i].normalize()
		}
		return array, nil
	}

	// Try single agent
	var single DiscoveredAgent
	if ext == ".json" {
		parseErr = json.Unmarshal(data, &single)
	} else {
		parseErr = yaml.Unmarshal(data, &single)
	}

	if parseErr != nil {
		return nil, parseErr
	}

	if single.Name == "" {
		// Use filename as agent name
		single.Name = strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	}

	if single.Tools == nil {
		// Default tools for a generic agent
		single.Tools = []string{"read_file", "write_file", "edit_file", "json_patch", "list_dir", "grep", "glob", "cli", "web_fetch"}
	}

	single.normalize()
	agents = append(agents, single)
	return agents, nil
}

func (da *DiscoveredAgent) normalize() {
	if da == nil {
		return
	}
	da.Name = strings.TrimSpace(da.Name)
	da.Description = strings.TrimSpace(da.Description)
	da.Role = strings.TrimSpace(da.Role)
	da.AgentType = strings.TrimSpace(da.AgentType)
	da.WhenToUse = strings.TrimSpace(da.WhenToUse)
	da.SystemPrompt = strings.TrimSpace(da.SystemPrompt)
	da.Prompt = strings.TrimSpace(da.Prompt)
	da.InitialPrompt = strings.TrimSpace(da.InitialPrompt)
	da.Model = strings.TrimSpace(da.Model)
	da.Effort = strings.TrimSpace(da.Effort)
	da.PermissionMode = strings.TrimSpace(da.PermissionMode)
	da.Isolation = strings.TrimSpace(da.Isolation)
	da.Source = strings.TrimSpace(da.Source)
	da.Tools = uniqueSortedStrings(da.Tools)
	da.DisallowedTools = uniqueSortedStrings(da.DisallowedTools)
	da.MCPServers = uniqueSortedStrings(da.MCPServers)
	da.Hooks = uniqueSortedStrings(da.Hooks)
	da.Skills = uniqueSortedStrings(da.Skills)
	if da.SystemPrompt == "" && da.Prompt != "" {
		da.SystemPrompt = da.Prompt
	}
	if da.Description == "" && da.WhenToUse != "" {
		da.Description = da.WhenToUse
	}
	if da.Role == "" && da.AgentType != "" {
		da.Role = da.AgentType
	}
}

func (da DiscoveredAgent) EffectivePrompt() string {
	if strings.TrimSpace(da.SystemPrompt) != "" {
		return da.SystemPrompt
	}
	return da.Prompt
}

func (da DiscoveredAgent) EffectiveDescription() string {
	if strings.TrimSpace(da.Description) != "" {
		return da.Description
	}
	return da.WhenToUse
}

func (da DiscoveredAgent) EffectiveModel() string {
	model := strings.TrimSpace(da.Model)
	if strings.EqualFold(model, "inherit") {
		return ""
	}
	return model
}

func (da DiscoveredAgent) EffectiveMaxIterations(fallback int) int {
	if da.MaxTurns > 0 {
		return da.MaxTurns
	}
	return fallback
}

func mergeAgentDefinitions(base, override []DiscoveredAgent) []DiscoveredAgent {
	merged := make([]DiscoveredAgent, 0, len(base)+len(override))
	index := map[string]int{}
	add := func(agent DiscoveredAgent) {
		agent.normalize()
		key := DefinitionKey(agent.Name)
		if key == "" {
			return
		}
		if i, ok := index[key]; ok {
			merged[i] = agent
			return
		}
		index[key] = len(merged)
		merged = append(merged, agent)
	}
	for _, agent := range base {
		add(agent)
	}
	for _, agent := range override {
		add(agent)
	}
	return merged
}

func DefinitionKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// BuiltinAgentDefinitions returns product-level agents that are available
// without a file in .openkitty/agents.
func BuiltinAgentDefinitions() []DiscoveredAgent {
	verification := DiscoveredAgent{
		Name:           "verification",
		Description:    "Read-only verification agent that validates changes with concrete command output and evidence.",
		Role:           "verification",
		AgentType:      "verification",
		WhenToUse:      "Use after code, config, pipeline, or content changes to independently verify the outcome.",
		Model:          "inherit",
		PermissionMode: string(permissionpolicy.PermissionDefault),
		MaxTurns:       12,
		Background:     true,
		Tools: []string{
			"read_file",
			"list_dir",
			"grep",
			"glob",
			"project_map",
			"code_check",
			"cli",
			"shell_analyze",
			"shell_sessions",
			"tool_detail",
		},
		DisallowedTools: []string{
			"write_file",
			"edit_file",
			"json_patch",
			"multiedit",
			"notepad",
			"create_todo",
			"update_todo",
			"cancel_todo",
			"external_agent",
			"verify_work",
			"sub_agent",
			"parallel_sub_agent",
			"swarm",
			"ptc_execute",
		},
		Memory:     AgentMemoryPolicy{Project: true, Local: true},
		Source:     "builtin",
		SourceFile: "builtin:verification",
		SystemPrompt: `You are OpenKitty's verification agent.

Mission:
- Independently verify whether the claimed work is actually complete.
- Prefer direct evidence from commands, tests, logs, file inspection, and reproducible checks.
- Be skeptical of claims that are not backed by output.

Hard constraints:
- Do not modify project files, configuration, dependencies, commits, or persistent state.
- Do not spawn other agents.
- Do not create todo items or write reports to disk.
- If a command may mutate state, do not run it. Use read-only inspection or explain the gap.

Verification method:
1. Restate the acceptance criteria you are checking.
2. Inspect the relevant files or outputs.
3. Run the smallest useful read-only commands or tests.
   For a claimed DOCX, XLSX, or PPTX deliverable, prefer code_check with action=syntax on the exact file; a ZIP signature, non-zero size, or member listing alone does not prove that its OPC relationships are usable.
4. Probe likely edge cases and regressions.
5. Report exact evidence: command names, important output lines, and file paths.

Final line must be exactly one of:
VERDICT: PASS
VERDICT: FAIL
VERDICT: PARTIAL`,
	}
	verification.normalize()
	return []DiscoveredAgent{verification}
}

// RecheckAgents re-scans and returns newly added or changed agents.
func (al *AgentLoader) RecheckAgents() ([]DiscoveredAgent, error) {
	agents, err := al.DiscoverAgents()
	if err != nil {
		return nil, err
	}
	var changed []DiscoveredAgent
	for _, a := range agents {
		modTime := time.Time{}
		if info, err := os.Stat(a.SourceFile); err == nil {
			modTime = info.ModTime()
		}
		if lastMod, exists := al.lastMod[a.SourceFile]; !exists || modTime.After(lastMod) {
			changed = append(changed, a)
		}
		al.lastMod[a.SourceFile] = modTime
	}
	return changed, nil
}

const DefaultWatchInterval = 30 * time.Second

// WatchAndReload starts a background goroutine that periodically checks for agent file changes.
// Default interval: 30 seconds. Set to 0 to disable, 5s minimum.
func (al *AgentLoader) WatchAndReload(interval time.Duration, onReload func([]DiscoveredAgent)) chan struct{} {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	al.autoReload = true
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				changed, err := al.RecheckAgents()
				if err != nil {
					log.Printf("[AGENT_LOADER] Recheck failed: %v", err)
					continue
				}
				if len(changed) > 0 {
					log.Printf("[AGENT_LOADER] Detected %d changed agent(s)", len(changed))
					if onReload != nil {
						onReload(changed)
					}
				}
			case <-stop:
				al.autoReload = false
				return
			}
		}
	}()
	return stop
}

// ValidateAgentSchema checks a discovered agent for basic correctness.
func ValidateAgentSchema(agent *DiscoveredAgent) []string {
	var issues []string
	if agent.Name == "" {
		issues = append(issues, "name is required")
	}
	if len(agent.Name) > 64 {
		issues = append(issues, "name too long (max 64)")
	}
	if agent.Description == "" {
		issues = append(issues, "description is recommended")
	}
	if agent.PermissionMode != "" && !permissionpolicy.IsValidPermissionMode(agent.PermissionMode) {
		issues = append(issues, "permission_mode is invalid")
	}
	if overlap := intersectStrings(agent.Tools, agent.DisallowedTools); len(overlap) > 0 {
		issues = append(issues, "tools and disallowed_tools overlap: "+strings.Join(overlap, ", "))
	}
	return issues
}

func intersectStrings(left, right []string) []string {
	seen := map[string]bool{}
	for _, value := range left {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = true
		}
	}
	var out []string
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value != "" && seen[value] {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// GenerateDefaultAgentFile creates a default agent definition file as a starting point.
func GenerateDefaultAgentFile(dir string) error {
	os.MkdirAll(dir, 0755)
	examplePath := filepath.Join(dir, "example-agent.yaml")

	if _, err := os.Stat(examplePath); err == nil {
		return nil // Already exists
	}

	example := `# OpenKitty Agent Definition
# Place .yaml or .json files in .openkitty/agents/ to define custom agents.
# They will be auto-discovered on startup.

name: my-assistant
description: A custom AI assistant for general tasks
role: assistant
system_prompt: |
  You are a helpful AI assistant. Be concise and thorough.
  Use tools when needed to accomplish the user's goals.
tools:
  - read_file
  - write_file
  - edit_file
  - list_dir
  - grep
  - web_fetch
  - cli
disallowed_tools:
  - sub_agent
  - parallel_sub_agent
permission_mode: default
max_turns: 20
skills: []
tier: community
`
	return os.WriteFile(examplePath, []byte(example), 0644)
}
