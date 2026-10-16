package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mac/internal/projectctx"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	agentcore "mac/internal/agents"
	skilldomain "mac/internal/skills"
	agenttools "mac/internal/tools/agents"
)

// SubAgentTool allows the main agent to spawn child agents for parallel/delegated work.
// Sub-agents CANNOT spawn more sub-agents (no recursion allowed).
type SubAgentTool struct {
	agentFactory *AgentFactory
	workspace    string
	fileLogger   *log.Logger
	skillsLoader *skilldomain.SkillsLoader
	activeAgents map[string]*subAgentSession
	definitions  map[string]agentcore.DiscoveredAgent
	mu           sync.Mutex
}

func (t *SubAgentTool) newProgressReportTool(session *subAgentSession) *agenttools.ProgressReportTool {
	return agenttools.NewProgressReportTool(func(message string) error {
		if t == nil {
			return fmt.Errorf("sub-agent manager is not initialized")
		}
		t.mu.Lock()
		sessionID := ""
		if session != nil {
			session.progress = append(session.progress, message)
			sessionID = session.id
		}
		t.mu.Unlock()
		if t.fileLogger != nil {
			t.fileLogger.Printf("[SubAgent] Progress from '%s': %s", sessionID, truncate(message, 100))
		}
		return nil
	})
}

func (f *AgentFactory) executeSwarmTask(ctx context.Context, task agenttools.SwarmTask, timeout time.Duration) (string, error) {
	if f == nil {
		return "", fmt.Errorf("agent factory not available")
	}

	systemPrompt := fmt.Sprintf("You are %s, a specialized AI agent. Your role: %s. "+
		"Focus ONLY on your assigned task. Be thorough but concise. "+
		"Use the available tools to accomplish your goal. Output your final result clearly.", task.AgentName, task.Role)
	config := AgentConfig{Name: task.AgentName, SystemMessage: systemPrompt, MaxIterations: 10}
	subAgent, err := f.GetOrCreateAgent(config)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	if llmAgent, ok := subAgent.(*LLMAgent); ok && len(task.Tools) > 0 {
		registry := newAppToolRegistry()
		for _, toolName := range task.Tools {
			if tool, exists := llmAgent.toolRegistry.Get(toolName); exists {
				registry.Register(tool)
			}
		}
		llmAgent.toolRegistry = registry
		llmAgent.skillsLoader = nil
	}

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fullPrompt := fmt.Sprintf("## Your Task\n%s\n\nComplete this task now. Be direct and output your result.", task.Task)
	var output string
	var generateErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				generateErr = fmt.Errorf("agent panic: %v", recovered)
			}
		}()
		output, generateErr = subAgent.Generate(subCtx, fullPrompt)
	}()
	return output, generateErr
}

const subAgentOutputContract = `## Output Contract
- Return the substantive result directly to the coordinator.
- If and only if the delegated task explicitly requires creating or modifying workspace files, include a "Files Created/Modified" section with the exact paths and then a "Result" section.
- For response-only research, analysis, review, planning, or drafting, do not create or modify files. Return a "Result" section and a short "Summary" section only.
- Do not invent a destination path, and do not create files merely to preserve notes, hand work to another agent, or fill an output template.`

func subAgentProgressReportingGuidance(agentType string, maxIterations int) string {
	// A progress heartbeat must never consume the only turn available for
	// substantive evidence or discard a completion-critical verifier's final
	// verdict. Longer ordinary workers keep the existing mandatory heartbeat so
	// coordinators can still observe and stop work that drifts off course.
	if maxIterations <= 2 || strings.EqualFold(strings.TrimSpace(agentType), "verification") {
		return `## Progress Reporting
` + "`report_progress`" + ` is optional for this bounded or completion-critical task. Prioritize the smallest required evidence check and a complete final result; if the task can finish directly, no progress heartbeat is needed.`
	}
	return `## Progress Reporting
During your task, use the ` + "`report_progress`" + ` tool to update the coordinator on your progress (e.g., "40% done, found 3 relevant files, analyzing patterns..."). This helps the coordinator track your work and kill you early if you're off-track.`
}

type subAgentSession struct {
	id        string
	task      string
	agentType string
	startTime time.Time
	endTime   time.Time
	status    string // "running", "completed", "failed", "stopped"
	result    string
	err       string
	progress  []string           // mid-task progress reports
	cancel    context.CancelFunc // kill the sub-agent
}

type SubAgentStatus = agenttools.SubAgentStatus

func NewSubAgentTool(factory *AgentFactory, workspace string, fileLogger *log.Logger, skillsLoader *skilldomain.SkillsLoader) *SubAgentTool {
	tool := &SubAgentTool{
		agentFactory: factory,
		workspace:    workspace,
		fileLogger:   fileLogger,
		skillsLoader: skillsLoader,
		activeAgents: make(map[string]*subAgentSession),
	}
	tool.reloadDefinitions()
	return tool
}

func (t *SubAgentTool) Name() string {
	return "sub_agent"
}

func (t *SubAgentTool) SandboxCapabilities() ToolSandboxCapabilities {
	return ToolSandboxCapabilities{SubAgent: true}
}

func (t *SubAgentTool) Description() string {
	return `Spawn a child agent to handle a specific sub-task independently.
Use this for parallel work, research tasks, or delegating well-defined sub-problems.
The sub-agent has access to file operations, web search, and CLI tools but CANNOT spawn more sub-agents.
Set background=true to return immediately, then use agent_status to inspect progress/result or agent_stop to cancel.
Without background, the call waits and returns the completed result directly.`
}

// KillSubAgent stops a running sub-agent by name.
func (t *SubAgentTool) KillSubAgent(agentName string) error {
	t.mu.Lock()
	session, ok := t.activeAgents[agentName]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("sub-agent '%s' not found or not running", agentName)
	}
	if session.status != "running" {
		t.mu.Unlock()
		return fmt.Errorf("sub-agent '%s' is already %s", agentName, session.status)
	}
	session.status = "stopped"
	session.endTime = time.Now()
	cancel := session.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if t.fileLogger != nil {
		t.fileLogger.Printf("[SubAgent] Killed sub-agent '%s'", agentName)
	}
	return nil
}

// ListSubAgents returns all active sub-agent names.
func (t *SubAgentTool) ListSubAgents() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var names []string
	for name := range t.activeAgents {
		names = append(names, name)
	}
	return names
}

func (t *SubAgentTool) StatusSnapshot(agentName string, includeResult bool) []SubAgentStatus {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	names := make([]string, 0, len(t.activeAgents))
	for name := range t.activeAgents {
		if agentName == "" || strings.EqualFold(name, agentName) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	statuses := make([]SubAgentStatus, 0, len(names))
	for _, name := range names {
		session := t.activeAgents[name]
		end := session.endTime
		if end.IsZero() {
			end = now
		}
		status := SubAgentStatus{
			ID:        session.id,
			Task:      session.task,
			AgentType: session.agentType,
			Status:    session.status,
			StartedAt: session.startTime,
			EndedAt:   session.endTime,
			Elapsed:   end.Sub(session.startTime).Round(time.Millisecond).String(),
			Progress:  append([]string(nil), session.progress...),
			Error:     session.err,
		}
		if includeResult {
			status.Result = session.result
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (t *SubAgentTool) pruneFinishedSessionsLocked(maxEntries int) {
	if maxEntries <= 0 || len(t.activeAgents) < maxEntries {
		return
	}
	type finishedSession struct {
		name string
		end  time.Time
	}
	var finished []finishedSession
	for name, session := range t.activeAgents {
		if session.status != "running" {
			finished = append(finished, finishedSession{name: name, end: session.endTime})
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].end.Before(finished[j].end) })
	for len(t.activeAgents) >= maxEntries && len(finished) > 0 {
		delete(t.activeAgents, finished[0].name)
		finished = finished[1:]
	}
}

func (t *SubAgentTool) reloadDefinitions() {
	agentsDir := filepath.Join(t.workspace, ".openkitty", "agents")
	loader := agentcore.NewAgentLoader(agentsDir)
	defs, err := loader.DiscoverAllAgents()
	if err != nil {
		if t.fileLogger != nil {
			t.fileLogger.Printf("[SubAgent] agent definition load failed: %v", err)
		}
		defs = agentcore.BuiltinAgentDefinitions()
	}
	t.definitions = make(map[string]agentcore.DiscoveredAgent, len(defs))
	for _, def := range defs {
		key := agentcore.DefinitionKey(def.Name)
		if key == "" {
			continue
		}
		t.definitions[key] = def
	}
}

func (t *SubAgentTool) resolveDefinition(agentName, agentType string) (agentcore.DiscoveredAgent, bool) {
	if len(t.definitions) == 0 {
		t.reloadDefinitions()
	}
	for _, name := range []string{agentType, agentName} {
		if def, ok := t.definitions[agentcore.DefinitionKey(name)]; ok {
			return def, true
		}
	}
	return agentcore.DiscoveredAgent{}, false
}

func agentConfigFromDefinition(definition agentcore.DiscoveredAgent, runtimeName string, fallbackMaxIterations int) AgentConfig {
	name := strings.TrimSpace(runtimeName)
	if name == "" {
		name = definition.Name
	}
	return AgentConfig{
		Name:            name,
		OriginalName:    definition.Name,
		Model:           definition.EffectiveModel(),
		SystemMessage:   definition.EffectivePrompt(),
		EnabledTools:    append([]string(nil), definition.Tools...),
		DisallowedTools: append([]string(nil), definition.DisallowedTools...),
		PermissionMode:  definition.PermissionMode,
		MaxIterations:   definition.EffectiveMaxIterations(fallbackMaxIterations),
	}
}

func (t *SubAgentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The specific task for the sub-agent to accomplish. Be clear and specific about what you need.",
			},
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "Name for this sub-agent (e.g., 'researcher', 'coder', 'analyst'). Used for identification.",
			},
			"agent_type": map[string]interface{}{
				"type":        "string",
				"description": "Optional registered agent definition to apply, e.g. 'verification'. Defaults to matching agent_name.",
			},
			"max_iterations": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum iterations for the sub-agent (default 30, max 60). Lower values save time for simple tasks.",
				"minimum":     1,
				"maximum":     60,
			},
			"background": map[string]interface{}{
				"type":        "boolean",
				"description": "Run asynchronously and return immediately. Query progress and result with agent_status.",
			},
		},
		"required": []string{"task", "agent_name"},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	task, ok := params["task"].(string)
	if !ok || task == "" {
		return "", fmt.Errorf("task parameter is required")
	}
	task = strings.TrimSpace(task)

	// Reject too-simple tasks that don't need a sub-agent
	if len(strings.Fields(task)) < 5 {
		return "", fmt.Errorf("task too simple for sub_agent (%d words). Sub-agents are for complex, well-defined tasks. For simple operations, use the tool directly.", len(strings.Fields(task)))
	}
	taskLower := strings.ToLower(task)
	singleActions := []string{"curl ", "echo ", "cat ", "ls ", "pwd", "dir ", "type "}
	for _, action := range singleActions {
		if strings.HasPrefix(taskLower, action) {
			return "", fmt.Errorf("this looks like a single command, not a sub-agent task. Use the %s tool directly instead of spawning a sub-agent.", strings.TrimSpace(action))
		}
	}

	agentName, ok := params["agent_name"].(string)
	if !ok || agentName == "" {
		agentName = fmt.Sprintf("sub_%d", time.Now().UnixNano()%10000)
	}
	agentType, _ := params["agent_type"].(string)
	background, _ := params["background"].(bool)

	maxIter := 30
	switch mi := params["max_iterations"].(type) {
	case float64:
		if mi > 0 {
			maxIter = int(mi)
		}
	case int:
		if mi > 0 {
			maxIter = mi
		}
	}
	if maxIter > 60 {
		maxIter = 60
	}
	agentDef, hasAgentDef := t.resolveDefinition(agentName, agentType)
	if hasAgentDef && agentDef.MaxTurns > 0 && maxIter > agentDef.MaxTurns {
		maxIter = agentDef.MaxTurns
	}

	// Check concurrent sub-agent limit
	t.mu.Lock()
	t.pruneFinishedSessionsLocked(50)
	running := 0
	for _, active := range t.activeAgents {
		if active.status == "running" {
			running++
		}
	}
	if running >= 5 {
		t.mu.Unlock()
		return "", fmt.Errorf("max 5 concurrent sub-agents allowed, currently %d active", running)
	}
	if existing, exists := t.activeAgents[agentName]; exists && existing.status == "running" {
		t.mu.Unlock()
		return "", fmt.Errorf("sub-agent '%s' is already running", agentName)
	}
	session := &subAgentSession{
		id:        agentName,
		task:      task,
		agentType: agentType,
		startTime: time.Now(),
		status:    "running",
	}
	if hasAgentDef && session.agentType == "" {
		session.agentType = firstNonEmptyString(agentDef.AgentType, agentDef.Role, agentDef.Name)
	}
	t.activeAgents[agentName] = session
	t.mu.Unlock()

	if t.fileLogger != nil {
		t.fileLogger.Printf("[SubAgent] Starting sub-agent '%s' for task: %s", agentName, truncate(task, 200))
	}

	if background {
		backgroundCtx := context.WithoutCancel(ctx)
		go func() {
			if _, err := t.runSubAgentSession(backgroundCtx, session, task, agentName, maxIter, agentDef, hasAgentDef); err != nil && t.fileLogger != nil {
				t.fileLogger.Printf("[SubAgent] Background sub-agent '%s' ended with error: %v", agentName, err)
			}
		}()
		return fmt.Sprintf(
			"Sub-agent %q started in the background. Use agent_status with agent_name=%q to inspect progress or retrieve its result; use agent_stop to cancel it.",
			agentName,
			agentName,
		), nil
	}

	return t.runSubAgentSession(ctx, session, task, agentName, maxIter, agentDef, hasAgentDef)
}

func (t *SubAgentTool) runSubAgentSession(
	ctx context.Context,
	session *subAgentSession,
	task string,
	agentName string,
	maxIter int,
	agentDef agentcore.DiscoveredAgent,
	hasAgentDef bool,
) (string, error) {
	if t.agentFactory == nil {
		err := fmt.Errorf("sub-agent factory is not initialized")
		t.finishSession(session, "failed", "", err.Error())
		return "", err
	}

	// Build a lightweight agent config for the sub-agent.
	subAgentConfig := AgentConfig{Name: agentName, MaxIterations: maxIter}
	if hasAgentDef {
		subAgentConfig = agentConfigFromDefinition(agentDef, agentName, maxIter)
		// A caller may deliberately request a smaller budget than the reusable
		// agent definition. The definition is a ceiling, not a reason to expand a
		// fast-path verification from 2 turns back to 12.
		subAgentConfig.MaxIterations = maxIter
	}

	// Get or create the sub-agent through the factory
	subAgent, err := t.agentFactory.GetOrCreateSubAgent(subAgentConfig, task)
	if err != nil {
		t.finishSession(session, "failed", "", err.Error())
		return "", fmt.Errorf("failed to create sub-agent: %w", err)
	}

	// Register progress reporting tool for this sub-agent session
	if llmSub, ok := subAgent.(*LLMAgent); ok && llmSub.toolRegistry != nil {
		llmSub.toolRegistry.Register(t.newProgressReportTool(session))
	}

	// Build sub-agent's task prompt with clear instructions
	skillContext := ""
	if t.skillsLoader != nil {
		if skill, err := t.skillsLoader.GetSkill(agentName); err == nil && skill != nil {
			skillContext = fmt.Sprintf("\n\n## Domain Knowledge (%s)\n%s\n", agentName, skill.Content)
			if t.fileLogger != nil {
				t.fileLogger.Printf("[SubAgent] Injected skill '%s' (%d chars) into '%s'", agentName, len(skill.Content), agentName)
			}
		}
	}
	definitionContext := ""
	if hasAgentDef {
		var b strings.Builder
		b.WriteString("\n\n## Agent Definition\n")
		b.WriteString(fmt.Sprintf("- Type: %s\n", firstNonEmptyString(agentDef.AgentType, agentDef.Role, agentDef.Name)))
		if agentDef.WhenToUse != "" {
			b.WriteString(fmt.Sprintf("- When to use: %s\n", agentDef.WhenToUse))
		}
		if agentDef.PermissionMode != "" {
			b.WriteString(fmt.Sprintf("- Permission mode: %s\n", agentDef.PermissionMode))
		}
		if len(agentDef.Tools) > 0 {
			b.WriteString(fmt.Sprintf("- Allowed tools: %s\n", strings.Join(agentDef.Tools, ", ")))
		}
		if len(agentDef.DisallowedTools) > 0 {
			b.WriteString(fmt.Sprintf("- Disallowed tools: %s\n", strings.Join(agentDef.DisallowedTools, ", ")))
		}
		if agentDef.InitialPrompt != "" {
			b.WriteString("\n## Initial Prompt\n")
			b.WriteString(agentDef.InitialPrompt)
			b.WriteString("\n")
		}
		definitionContext = b.String()
	}

	subTaskPrompt := fmt.Sprintf(`You are a sub-agent named "%s" working on a delegated task.

## Parent Task Context
%s

## Your Specific Task
%s
%s
%s

## Important Rules for Sub-Agents
1. Focus ONLY on your specific task. Do not expand scope.
2. You CANNOT spawn more sub-agents (the sub_agent tool is not available to you).
3. Be efficient: use at most %d iterations. Prefer fewer.
4. When done, return a clear, structured result.
5. If you hit an error, explain what went wrong and suggest next steps.
6. Do not create todo items for this sub-task.

%s

%s`, agentName, truncate(task, 500), task, skillContext, definitionContext, maxIter, subAgentOutputContract, subAgentProgressReportingGuidance(session.agentType, maxIter))

	// Execute with timeout (cancellable via KillSubAgent)
	subCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	t.mu.Lock()
	if session.status != "running" {
		status := session.status
		t.mu.Unlock()
		cancel()
		return "", fmt.Errorf("sub-agent '%s' was %s before execution started", agentName, status)
	}
	session.cancel = cancel
	t.mu.Unlock()
	defer cancel()

	startTime := time.Now()
	result, err := subAgent.Generate(subCtx, subTaskPrompt)
	duration := time.Since(startTime)

	if err != nil {
		t.finishSession(session, "failed", "", err.Error())
		if t.fileLogger != nil {
			t.fileLogger.Printf("[SubAgent] Sub-agent '%s' FAILED after %v: %v", agentName, duration, err)
		}
		return "", fmt.Errorf("sub-agent '%s' failed after %v: %w", agentName, duration, err)
	}

	// Gate verification: scan output for claimed file paths, verify they exist
	verification := t.verifyWorkerOutput(result)

	var response strings.Builder
	response.WriteString(fmt.Sprintf("## Sub-Agent '%s' Result\n\n", agentName))
	response.WriteString(fmt.Sprintf("**Duration**: %v | **Status**: completed\n\n", duration))
	response.WriteString(result)
	if verification != "" {
		response.WriteString("\n\n" + verification)
	}

	finalResult := response.String()
	t.finishSession(session, "completed", finalResult, "")
	if t.fileLogger != nil {
		t.fileLogger.Printf("[SubAgent] Sub-agent '%s' completed in %v, result length: %d", agentName, duration, len(result))
	}
	return finalResult, nil
}

func (t *SubAgentTool) finishSession(session *subAgentSession, status, result, errorText string) {
	if t == nil || session == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if session.status != "stopped" {
		session.status = status
	}
	if result != "" {
		session.result = result
	}
	if errorText != "" {
		session.err = errorText
	}
	if session.endTime.IsZero() {
		session.endTime = time.Now()
	}
	session.cancel = nil
}

// verifyWorkerOutput scans sub-agent output for file paths and verifies they exist.
// Returns an empty string if all claimed files exist, or a warning block if any are missing.
func (t *SubAgentTool) verifyWorkerOutput(output string) string {
	// Extract file paths from "Files Created/Modified" section or general path patterns
	paths := extractClaimedFiles(output)
	if len(paths) == 0 {
		return ""
	}

	var missing []string
	var found []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		} else {
			missing = append(missing, p)
		}
	}

	var sb strings.Builder
	if len(found) > 0 {
		sb.WriteString(fmt.Sprintf("### ✅ Files Verified (%d)\n", len(found)))
		for _, p := range found {
			sb.WriteString(fmt.Sprintf("- `%s` — exists\n", p))
		}
	}
	if len(missing) > 0 {
		sb.WriteString(fmt.Sprintf("\n### ⚠️ Files MISSING (%d)\n", len(missing)))
		for _, p := range missing {
			sb.WriteString(fmt.Sprintf("- `%s` — NOT found on disk\n", p))
		}
		sb.WriteString("\nThe sub-agent claimed these files were created but they do not exist. Treat its output with skepticism.\n")
	}
	return sb.String()
}

// extractClaimedFiles parses sub-agent output for file paths.
func extractClaimedFiles(output string) []string {
	var paths []string
	seen := make(map[string]bool)
	// Look for paths in "Files Created/Modified" section and general patterns
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// Match lines like: "- /path/to/file — description" or "/path/to/file"
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "/") || strings.HasPrefix(line, "./") ||
			(strings.Contains(line, "/") && !strings.Contains(line, "http")) {
			// Extract path-like tokens
			for _, word := range strings.Fields(line) {
				word = strings.Trim(word, "`-*:,")
				if strings.HasPrefix(word, "/") || (strings.HasPrefix(word, "./") && len(word) > 2) {
					if !seen[word] && len(word) < 500 {
						seen[word] = true
						paths = append(paths, word)
					}
				}
			}
		}
	}
	// Max 10 paths to avoid spam
	if len(paths) > 10 {
		paths = paths[:10]
	}
	return paths
}

// GetOrCreateSubAgent creates a fresh lightweight agent for one delegated run.
// LLMAgent contains mutable conversation, plan, transcript, tool, and budget
// state, so reusing an instance across tasks can leak state or race concurrent
// pipeline runs. Only immutable factory resources are shared.
func (f *AgentFactory) GetOrCreateSubAgent(config AgentConfig, parentTask string) (Agent, error) {
	return f.GetOrCreateSubAgentInWorkspace(config, parentTask, "")
}

// GetOrCreateSubAgentInWorkspace creates the per-run pipeline agent inside the
// authenticated execution workspace. A pipeline may be dispatched by a global
// AgentFactory while its tools and embedded Skill runtime must remain scoped to
// a tenant's personal workspace.
func (f *AgentFactory) GetOrCreateSubAgentInWorkspace(config AgentConfig, parentTask, workspace string) (Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.testSubAgentCreator != nil {
		return f.testSubAgentCreator(config, parentTask)
	}

	modelConfig := f.resolveSubAgentModelConfig(config.Model)
	if modelConfig == nil {
		return nil, fmt.Errorf("no model config available for sub-agent")
	}

	// Set defaults (same as AgentFactory.createAgent)
	if config.MaxTokens == 0 {
		config.MaxTokens = modelConfig.MaxTokens
	}
	if config.Temperature == 0 {
		config.Temperature = modelConfig.Temperature
	}

	runtimeConfig := f.globalConfig
	if strings.TrimSpace(workspace) != "" && f.globalConfig != nil {
		workspaceConfig := *f.globalConfig
		workspaceConfig.Workspace = projectctx.NormalizeWorkspace(workspace)
		runtimeConfig = &workspaceConfig
	}

	// Create the LLM agent
	agent := NewLLMAgent(config, modelConfig, f.callback, runtimeConfig, f.fileLogger)

	// AE identity: name + system prompt. Assembled in Generate()'s if/else.
	if config.AEName != "" {
		agent.aeName = config.AEName
	}
	if config.SystemMessage != "" {
		agent.systemPromptPrefix = config.SystemMessage
	}
	// Prompt cache optimization: all sub-agents share the parent's static system prefix.
	if f.parentSystemPrompt != "" {
		agent.systemPromptOverride = f.parentSystemPrompt
	}

	// Sub-agents get FULL tools minus agent spawning (prevent recursion)
	if agent.toolRegistry != nil {
		agent.toolRegistry.Unregister("sub_agent")
		agent.toolRegistry.Unregister("parallel_sub_agent")
	}

	// Reduce max iterations for efficiency
	if config.MaxIterations > 0 {
		agent.maxIterations = config.MaxIterations
	} else {
		agent.maxIterations = 30
	}

	// AE sub-agents inherit memory from global config (respects user's memory settings)
	// Previously forced to false, now let the agent keep whatever NewLLMAgent set up

	return agent, nil
}

func (f *AgentFactory) resolveSubAgentModelConfig(requested string) *ModelConfig {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if mc := f.modelConfigs[requested]; mc != nil && isModelConfigAvailable(*mc) {
			return mc
		}
		for _, key := range sortedModelConfigKeys(f.modelConfigs) {
			mc := f.modelConfigs[key]
			if mc != nil && isModelConfigAvailable(*mc) && strings.EqualFold(mc.ModelName, requested) {
				return mc
			}
		}
	}
	if f.preferredModel != "" {
		if mc := f.modelConfigs[f.preferredModel]; mc != nil && isModelConfigAvailable(*mc) {
			return mc
		}
	}
	for _, key := range sortedModelConfigKeys(f.modelConfigs) {
		if mc := f.modelConfigs[key]; mc != nil {
			return mc
		}
	}
	return nil
}

func sortedModelConfigKeys(configs map[string]*ModelConfig) []string {
	keys := make([]string, 0, len(configs))
	for key, cfg := range configs {
		if cfg != nil && isModelConfigAvailable(*cfg) {
			keys = append(keys, key)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left := ModelConfig{}
		right := ModelConfig{}
		if configs[keys[i]] != nil {
			left = *configs[keys[i]]
		}
		if configs[keys[j]] != nil {
			right = *configs[keys[j]]
		}
		leftScore := modelPreferenceScore(keys[i], left)
		rightScore := modelPreferenceScore(keys[j], right)
		if leftScore != rightScore {
			return leftScore < rightScore
		}
		return keys[i] < keys[j]
	})
	return keys
}

// SubAgentResult represents a structured result from a sub-agent
type SubAgentResult struct {
	AgentName  string        `json:"agent_name"`
	Task       string        `json:"task"`
	Status     string        `json:"status"`
	Result     string        `json:"result"`
	Duration   time.Duration `json:"duration"`
	ToolCalls  []string      `json:"tool_calls"`
	Iterations int           `json:"iterations"`
}

// RunSubAgentsParallel runs multiple sub-agents in parallel and collects results.
// This is a helper for batch sub-agent operations.
func RunSubAgentsParallel(ctx context.Context, tasks []SubAgentTask, tool *SubAgentTool) ([]SubAgentResult, error) {
	var wg sync.WaitGroup
	results := make([]SubAgentResult, len(tasks))
	errors := make([]error, len(tasks))

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t SubAgentTask) {
			defer wg.Done()
			params := map[string]interface{}{
				"task":           t.Task,
				"agent_name":     t.AgentName,
				"max_iterations": float64(t.MaxIterations),
			}
			resultStr, err := tool.Execute(ctx, params)
			results[idx] = SubAgentResult{
				AgentName: t.AgentName,
				Task:      t.Task,
				Status:    "completed",
				Result:    resultStr,
			}
			if err != nil {
				results[idx].Status = "failed"
				results[idx].Result = err.Error()
				errors[idx] = err
			}
		}(i, task)
	}
	wg.Wait()

	// Collect non-nil errors
	var errList []string
	for _, err := range errors {
		if err != nil {
			errList = append(errList, err.Error())
		}
	}

	if len(errList) > 0 {
		return results, fmt.Errorf("some sub-agents failed: %s", strings.Join(errList, "; "))
	}

	return results, nil
}

// SubAgentTask represents a task to be delegated to a sub-agent
type SubAgentTask struct {
	AgentName     string `json:"agent_name"`
	Task          string `json:"task"`
	MaxIterations int    `json:"max_iterations"`
}

// SerializeSubAgentResults formats sub-agent results for the parent agent
func SerializeSubAgentResults(results []SubAgentResult) string {
	var output strings.Builder
	output.WriteString("## Parallel Sub-Agent Results\n\n")

	for i, r := range results {
		output.WriteString(fmt.Sprintf("### Sub-Agent %d: %s [%s]\n", i+1, r.AgentName, r.Status))
		output.WriteString(fmt.Sprintf("**Duration**: %v\n\n", r.Duration))
		output.WriteString(r.Result)
		output.WriteString("\n\n---\n\n")
	}

	return output.String()
}

// MarshalJSON serializes SubAgentResult to JSON
func (r *SubAgentResult) MarshalJSON() ([]byte, error) {
	type Alias SubAgentResult
	return json.Marshal(&struct {
		*Alias
		Duration string `json:"duration"`
	}{
		Alias:    (*Alias)(r),
		Duration: r.Duration.String(),
	})
}

// ========== Parallel Sub-Agent Tool ==========

// ParallelSubAgentTool dispatches multiple sub-agents concurrently.
// Takes an array of tasks, runs them all in parallel via goroutines,
// waits for completion, and returns aggregated results.
type ParallelSubAgentTool struct {
	subAgentTool *SubAgentTool
	fileLogger   *log.Logger
}

func NewParallelSubAgentTool(subAgentTool *SubAgentTool, fileLogger *log.Logger) *ParallelSubAgentTool {
	return &ParallelSubAgentTool{
		subAgentTool: subAgentTool,
		fileLogger:   fileLogger,
	}
}

func (t *ParallelSubAgentTool) Name() string {
	return "parallel_sub_agent"
}

func (t *ParallelSubAgentTool) Description() string {
	return `Dispatch MULTIPLE sub-agents to work on independent tasks concurrently.
Use this when a complex task can be decomposed into parallel sub-tasks (e.g., research + code + analysis at the same time).
All sub-agents run simultaneously and results are collected when all complete.
Each sub-agent has access to file operations, web search, and CLI tools but CANNOT spawn more sub-agents.
Max 5 concurrent sub-agents. All must be independent (no data dependencies between them).`
}

func (t *ParallelSubAgentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tasks": map[string]interface{}{
				"type":        "array",
				"description": "Array of sub-tasks to run in parallel. Each task has: agent_name (string, required), task (string, required), agent_type (string, optional), max_iterations (int, optional, default 20).",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"agent_name": map[string]interface{}{
							"type":        "string",
							"description": "Name for this sub-agent (e.g., 'researcher', 'coder', 'tester')",
						},
						"task": map[string]interface{}{
							"type":        "string",
							"description": "The specific task for this sub-agent. Be clear and self-contained.",
						},
						"agent_type": map[string]interface{}{
							"type":        "string",
							"description": "Optional registered agent definition to apply, e.g. 'verification'. Defaults to matching agent_name.",
						},
						"max_iterations": map[string]interface{}{
							"type":        "integer",
							"description": "Max iterations (default 20, max 150). Lower for simple tasks.",
							"minimum":     1,
							"maximum":     150,
						},
					},
					"required": []string{"agent_name", "task"},
				},
			},
			"timeout_minutes": map[string]interface{}{
				"type":        "integer",
				"description": "Overall timeout in minutes (default 8, max 15).",
				"minimum":     1,
				"maximum":     15,
			},
		},
		"required": []string{"tasks"},
	}
}

func (t *ParallelSubAgentTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// Parse tasks array
	tasksRaw, ok := params["tasks"].([]interface{})
	if !ok {
		return "", fmt.Errorf("tasks parameter must be an array")
	}

	if len(tasksRaw) == 0 {
		return "", fmt.Errorf("tasks array is empty")
	}
	if len(tasksRaw) > 5 {
		return "", fmt.Errorf("max 5 parallel tasks, got %d", len(tasksRaw))
	}

	// Parse timeout
	timeoutMinutes := 8
	if tm, ok := params["timeout_minutes"].(float64); ok && tm > 0 {
		timeoutMinutes = int(tm)
		if timeoutMinutes > 15 {
			timeoutMinutes = 15
		}
	}

	// Parse individual tasks
	type parallelTask struct {
		AgentName     string `json:"agent_name"`
		AgentType     string `json:"agent_type,omitempty"`
		Task          string `json:"task"`
		MaxIterations int    `json:"max_iterations"`
	}

	tasks := make([]parallelTask, len(tasksRaw))
	for i, raw := range tasksRaw {
		taskMap, ok := raw.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("task[%d] is not a valid object", i)
		}
		tasks[i].AgentName, _ = taskMap["agent_name"].(string)
		tasks[i].AgentType, _ = taskMap["agent_type"].(string)
		tasks[i].Task, _ = taskMap["task"].(string)
		if tasks[i].AgentName == "" || tasks[i].Task == "" {
			return "", fmt.Errorf("task[%d] missing agent_name or task", i)
		}
		tasks[i].MaxIterations = 20
		if mi, ok := taskMap["max_iterations"].(float64); ok && mi > 0 {
			tasks[i].MaxIterations = int(mi)
			if tasks[i].MaxIterations > 150 {
				tasks[i].MaxIterations = 150
			}
		}
	}

	t.fileLogger.Printf("[ParallelSubAgent] Dispatching %d sub-agents in parallel, timeout=%dm", len(tasks), timeoutMinutes)

	// Create context with timeout
	subCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	// Run all tasks in parallel
	type taskResult struct {
		Index    int
		Result   string
		Duration time.Duration
		Err      error
	}

	resultCh := make(chan taskResult, len(tasks))
	startTime := time.Now()

	subTool := t.subAgentTool // capture before goroutine
	for i, task := range tasks {
		go func(idx int, pt parallelTask) {
			params := map[string]interface{}{
				"task":           pt.Task,
				"agent_name":     pt.AgentName,
				"max_iterations": float64(pt.MaxIterations),
			}
			if strings.TrimSpace(pt.AgentType) != "" {
				params["agent_type"] = strings.TrimSpace(pt.AgentType)
			}
			taskStart := time.Now()
			result, err := subTool.Execute(subCtx, params)
			resultCh <- taskResult{
				Index:    idx,
				Result:   result,
				Duration: time.Since(taskStart),
				Err:      err,
			}
		}(i, task)
	}

	// Collect results
	var response strings.Builder
	response.WriteString(fmt.Sprintf("# Parallel Sub-Agent Results (%d tasks)\n\n", len(tasks)))
	response.WriteString(fmt.Sprintf("**Total duration**: pending...\n\n"))

	successCount := 0
	failCount := 0
	var errors []string

	for i := 0; i < len(tasks); i++ {
		select {
		case <-subCtx.Done():
			response.WriteString(fmt.Sprintf("\n## ⚠️ Timeout after %v\n", time.Since(startTime)))
			response.WriteString(fmt.Sprintf("Completed: %d/%d tasks before timeout.\n", i, len(tasks)))
			return response.String(), nil
		case r := <-resultCh:
			task := tasks[r.Index]
			statusIcon := "✅"
			statusLabel := "COMPLETED"
			if r.Err != nil {
				statusIcon = "❌"
				statusLabel = "FAILED"
				failCount++
				errors = append(errors, fmt.Sprintf("%s: %v", task.AgentName, r.Err))
			} else {
				successCount++
			}

			response.WriteString(fmt.Sprintf("### %s %s (%s)\n", statusIcon, task.AgentName, statusLabel))
			if task.AgentType != "" {
				response.WriteString(fmt.Sprintf("**Agent type**: %s\n", task.AgentType))
			}
			response.WriteString(fmt.Sprintf("**Task**: %s\n", truncate(task.Task, 200)))
			response.WriteString(fmt.Sprintf("**Duration**: %v | **Max Iter**: %d\n\n", r.Duration, task.MaxIterations))

			if r.Result != "" {
				// Truncate long results but keep substantial content
				if len(r.Result) > 3000 {
					response.WriteString(r.Result[:3000])
					response.WriteString("\n\n... (result truncated, full result stored in log)\n")
				} else {
					response.WriteString(r.Result)
				}
			}
			response.WriteString("\n\n---\n\n")
		}
	}

	totalDuration := time.Since(startTime)
	// Fix the duration line at the top
	summary := fmt.Sprintf("# Parallel Sub-Agent Results (%d tasks)\n\n**Total duration**: %v | **Success**: %d | **Failed**: %d\n\n",
		len(tasks), totalDuration.Round(time.Second), successCount, failCount)

	t.fileLogger.Printf("[ParallelSubAgent] All done: %d success, %d failed, %v", successCount, failCount, totalDuration)

	body := response.String()
	if idx := strings.Index(body, "###"); idx >= 0 {
		return summary + body[idx:], nil
	}
	// No task result headers — likely all goroutines timed out before any returned
	if body != "" {
		return summary + body, nil
	}
	return summary, nil
}
