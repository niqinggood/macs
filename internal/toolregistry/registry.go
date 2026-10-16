// Package toolregistry owns tool registration, schema discovery, result
// normalization, and middleware-backed execution.
package toolregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	memorycore "mac/internal/memory"
	middlewarecore "mac/internal/middleware"
	"mac/internal/toolcontract"
)

// Identity is the execution identity subset exposed to tool middleware.
type Identity struct {
	AgentName string
	UserID    string
	TenantID  string
	RoleID    string
	Source    string
	ServiceID string
}

// Dependencies connect the dependency-neutral registry to its host runtime.
type Dependencies struct {
	TaskID   func(context.Context) string
	Identity func(context.Context) (Identity, bool)
}

// Registry stores the tools available to one agent execution.
type Registry struct {
	mu         sync.RWMutex
	tools      map[string]toolcontract.Tool
	chain      *middlewarecore.Chain
	normalizer *toolcontract.ToolResultNormalizer
	deps       Dependencies
}

func New(deps Dependencies) *Registry {
	return &Registry{
		tools:      make(map[string]toolcontract.Tool),
		normalizer: toolcontract.NewToolResultNormalizer("."),
		deps:       deps,
	}
}

func ApplyPolicy(registry *Registry, allowed, disallowed []string) toolcontract.ToolPolicyResult {
	if registry == nil {
		return toolcontract.ToolPolicyResult{}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	names := make([]string, 0, len(registry.tools))
	for name := range registry.tools {
		names = append(names, name)
	}
	result := toolcontract.SelectToolPolicy(names, allowed, disallowed)
	for _, name := range result.Removed {
		delete(registry.tools, name)
	}
	return result
}

func (r *Registry) Register(tool toolcontract.Tool) {
	if r == nil || tool == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *Registry) Unregister(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

func (r *Registry) HasTool(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.Get(name)
	return ok
}

func (r *Registry) ExecuteForPTC(ctx context.Context, name string, arguments map[string]interface{}) (string, bool, error) {
	if r == nil {
		return "", false, fmt.Errorf("tool registry is unavailable")
	}
	result, err := r.Execute(ctx, toolcontract.Call{ID: "ptc:" + name, Name: name, Arguments: arguments})
	return result.Content, toolcontract.ToolResultExecutionSucceeded(result, err), err
}

func (r *Registry) AllNames() []string {
	names, _ := r.SnapshotTools(nil)
	sort.Strings(names)
	return names
}

// SnapshotTools returns a stable snapshot of the selected registered tools.
func (r *Registry) SnapshotTools(allowedNames map[string]bool) ([]string, map[string]toolcontract.Tool) {
	if r == nil {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	toolsByName := make(map[string]toolcontract.Tool, len(r.tools))
	for name, tool := range r.tools {
		if allowedNames != nil && !allowedNames[name] {
			continue
		}
		names = append(names, name)
		toolsByName[name] = tool
	}
	return names, toolsByName
}

func (r *Registry) RegisterBuiltinTools() {
	r.Register(&detailTool{registry: r})
}

type detailTool struct{ registry *Registry }

func (t *detailTool) Name() string { return "tool_detail" }

func (t *detailTool) Description() string {
	return "Get tool details. Actions: get (full schema by name), search (find tools by name, alias, or Chinese/English capability keywords). " +
		"Use search when you don't know the exact tool name. Use get before calling any extended tool."
}

func (t *detailTool) ToolSearchMetadata() toolcontract.ToolSearchMetadata {
	return toolcontract.ToolSearchMetadata{
		Aliases: []string{"tool_search", "search_tools", "工具搜索", "工具查找"},
		Tags:    []string{"tool discovery", "schema", "能力发现", "工具参数"},
	}
}

func (t *detailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "get (by name) or search (by keyword)",
				"enum":        []string{"get", "search"},
			},
			"tool_name": map[string]interface{}{
				"type":        "string",
				"description": "Tool name (for action=get) or a name, alias, Chinese/English capability, or domain query (for action=search)",
			},
		},
		"required": []string{"tool_name"},
	}
}

func (t *detailTool) searchTools(query string) string {
	if t == nil || t.registry == nil {
		return toolcontract.SearchToolSummaries(nil, query)
	}
	return toolcontract.SearchToolSummaries(t.registry.GetAllToolSummaries(), query)
}

func (t *detailTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	name, _ := params["tool_name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("tool_name is required")
	}
	action, _ := params["action"].(string)
	if action == "" {
		action = "get"
	}
	if action == "search" {
		return t.searchTools(name), nil
	}
	def, ok := t.registry.GetToolDefinition(name)
	if !ok {
		return "", fmt.Errorf("tool '%s' not found.\n%s", name, t.searchTools(name))
	}
	if activation := ActivationFromContext(ctx); activation != nil {
		if err := activation.Activate(name); err != nil {
			return "", err
		}
	}
	jsonDef, _ := json.Marshal(def)
	return string(jsonDef), nil
}

func (r *Registry) ListNames() []string { return r.AllNames() }

func (r *Registry) Get(name string) (toolcontract.Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) unavailableToolError(name string) error {
	names := r.AllNames()
	if len(names) == 0 {
		return fmt.Errorf(
			"tool %q is not available in this task; no tools are currently available. "+
				"Do not invent a replacement tool or widen the filesystem/network scope; "+
				"finish from the evidence already collected",
			name,
		)
	}
	const maxVisibleTools = 40
	visible := names
	suffix := ""
	if len(visible) > maxVisibleTools {
		visible = visible[:maxVisibleTools]
		suffix = fmt.Sprintf(" (%d additional registered tools omitted)", len(names)-len(visible))
	}
	return fmt.Errorf(
		"tool %q is not available in this task. Available tools: %s%s. "+
			"Use only an available tool whose advertised schema fits the task, or finish from existing evidence; "+
			"do not simulate the missing tool by broadly scanning the workspace or network",
		name,
		strings.Join(visible, ", "),
		suffix,
	)
}

func (r *Registry) GetDefinitions() []map[string]interface{} { return r.BuildDefinitions(nil) }

func (r *Registry) GetCoreDefinitions() []map[string]interface{} {
	return r.BuildDefinitions(CoreTools)
}

func (r *Registry) GetToolDefinition(name string) (map[string]interface{}, bool) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, false
	}
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		},
	}, true
}

func (r *Registry) GetAllToolSummaries() []map[string]string {
	names, toolsByName := r.SnapshotTools(nil)
	sort.Strings(names)
	out := make([]map[string]string, 0, len(names))
	for _, name := range names {
		tool := toolsByName[name]
		summary := map[string]string{"name": tool.Name(), "desc": tool.Description()}
		if provider, ok := tool.(toolcontract.ToolSearchMetadataProvider); ok {
			metadata := provider.ToolSearchMetadata()
			summary["aliases"] = strings.Join(metadata.Aliases, " ")
			summary["tags"] = strings.Join(metadata.Tags, " ")
		}
		out = append(out, summary)
	}
	return out
}

// BuildDefinitions builds tool definitions, optionally filtered by name.
func (r *Registry) BuildDefinitions(allowedNames map[string]bool) []map[string]interface{} {
	defs := make([]map[string]interface{}, 0)
	names, toolsByName := r.SnapshotTools(allowedNames)
	sort.Strings(names)
	for _, name := range names {
		tool := toolsByName[name]
		t0 := time.Now()
		desc := tool.Description()
		params := tool.Parameters()
		if d := time.Since(t0); d > 100*time.Millisecond {
			log.Printf("[GetDefs] SLOW tool %s: %v", name, d)
		}
		defs = append(defs, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": name, "description": desc, "parameters": params,
			},
		})
	}
	return defs
}

func (r *Registry) SetChain(chain *middlewarecore.Chain) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chain = chain
}

func (r *Registry) Chain() *middlewarecore.Chain {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.chain
}

func (r *Registry) SetResultNormalizer(normalizer *toolcontract.ToolResultNormalizer) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.normalizer = normalizer
}

func (r *Registry) ResultNormalizer() *toolcontract.ToolResultNormalizer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.normalizer
}

func (r *Registry) normalizeResultWithOutcome(toolName, content string, succeeded bool) string {
	if r == nil {
		return content
	}
	r.mu.RLock()
	normalizer := r.normalizer
	r.mu.RUnlock()
	if normalizer == nil {
		return content
	}
	return normalizer.NormalizeWithOutcome(toolName, content, succeeded)
}

func (r *Registry) ExecuteRaw(ctx context.Context, call toolcontract.Call) (toolcontract.Result, error) {
	tool, ok := r.Get(call.Name)
	if !ok {
		return toolcontract.Result{}, r.unavailableToolError(call.Name)
	}
	if err := toolcontract.ValidateArguments(tool.Parameters(), call.Arguments); err != nil {
		content := fmt.Sprintf("Tool call rejected [invalid_args]: %v", err)
		if repair := toolcontract.BuildSchemaRepairHint(call.Name, tool.Parameters(), call.Arguments, err); repair != "" {
			content += "\n\n" + repair
			if advisor, ok := tool.(toolcontract.SchemaRepairAdvisor); ok {
				if advice := strings.TrimSpace(advisor.SchemaRepairAdvice(call.Arguments)); advice != "" {
					content += "\n" + advice
				}
			}
		}
		return toolcontract.Result{
			ToolCallID: call.ID, Name: call.Name,
			Content:            r.normalizeResultWithOutcome(call.Name, content, false),
			ExecutionSucceeded: toolcontract.OutcomePointer(false),
		}, nil
	}

	result, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		content := fmt.Sprintf("执行失败: %v", err)
		if strings.TrimSpace(result) != "" {
			content += "\n\nTool output:\n" + result
		}
		toolResult := toolcontract.Result{
			ToolCallID: call.ID, Name: call.Name,
			Content:            r.normalizeResultWithOutcome(call.Name, content, false),
			ExecutionSucceeded: toolcontract.OutcomePointer(false),
		}
		var recoverable toolcontract.RecoverableError
		if errors.As(err, &recoverable) {
			directive := recoverable.ToolRecoveryDirective()
			if directive.MaxPrompts <= 0 {
				directive.MaxPrompts = 1
			}
			toolResult.ErrorCode = directive.Code
			toolResult.Recovery = &directive
		}
		return toolResult, nil
	}

	succeeded := toolcontract.ToolExecutionSucceededForTool(call.Name, result, nil)
	if payloadTool, ok := tool.(toolcontract.ContentPayloadTool); ok && payloadTool.ResultIsContentPayload() {
		succeeded = true
	}
	return toolcontract.Result{
		ToolCallID: call.ID, Name: call.Name,
		Content:            r.normalizeResultWithOutcome(call.Name, result, succeeded),
		ExecutionSucceeded: toolcontract.OutcomePointer(succeeded),
	}, nil
}

func (r *Registry) Execute(ctx context.Context, call toolcontract.Call) (toolcontract.Result, error) {
	chain := r.Chain()
	if chain == nil {
		return r.ExecuteRaw(ctx, call)
	}
	actx := &middlewarecore.Context{
		Context: ctx, ToolCall: call,
		SessionID: memorycore.SessionContextIDFromContext(ctx),
		Metadata:  make(map[string]any),
	}
	if r.deps.TaskID != nil {
		actx.TaskID = r.deps.TaskID(ctx)
	}
	if tool, ok := r.Get(call.Name); ok {
		PopulatePolicyMetadata(actx.Metadata, call.Name, tool, call.Arguments)
	}
	if r.deps.Identity != nil {
		if identity, ok := r.deps.Identity(ctx); ok {
			actx.AgentName = identity.AgentName
			actx.UserID = identity.UserID
			actx.TenantID = identity.TenantID
			actx.RoleID = identity.RoleID
			actx.Source = identity.Source
			actx.ServiceID = identity.ServiceID
		}
	}
	return chain.WrapToolExecution(actx, func() (toolcontract.Result, error) {
		return r.ExecuteRaw(ctx, actx.ToolCall)
	})
}

func PopulatePolicyMetadata(metadata map[string]any, toolName string, tool toolcontract.Tool, callArgs ...map[string]interface{}) {
	if metadata == nil || tool == nil {
		return
	}
	readOnly := toolcontract.IsReadOnlyToolName(toolName)
	var args map[string]interface{}
	if len(callArgs) > 0 {
		args = callArgs[0]
	}
	if declared, ok := tool.(toolcontract.CallReadOnlyTool); ok {
		readOnly = declared.IsReadOnlyForCall(args)
	} else if declared, ok := tool.(toolcontract.ReadOnlyTool); ok {
		readOnly = declared.IsReadOnly()
	}
	metadata["tool_read_only"] = readOnly
	if declared, ok := tool.(toolcontract.SandboxCallCapabilityTool); ok {
		metadata["sandbox_capabilities"] = declared.SandboxCapabilitiesForCall(args)
	} else if declared, ok := tool.(toolcontract.SandboxCapabilityTool); ok {
		metadata["sandbox_capabilities"] = declared.SandboxCapabilities()
	}
}

func MaxResultSize(registry *Registry, toolName string) int {
	if registry != nil {
		if tool, ok := registry.Get(toolName); ok {
			if limited, ok := tool.(toolcontract.SizeLimitedTool); ok {
				return limited.MaxResultSize()
			}
		}
	}
	return 8000
}
