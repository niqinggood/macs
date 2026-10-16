package llmapp

import (
	"context"
	"errors"
	"strings"
)

// ModelResponse is the settled result of one provider call. Token values are
// optional because some OpenAI-compatible providers do not return usage.
type ModelResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	RequestID        string
}

// DetailedModelCaller lets the managed runtime account for actual provider
// usage while ModelCaller remains available as a compatibility adapter.
type DetailedModelCaller func(
	ctx context.Context,
	model string,
	messages []map[string]interface{},
	maxTokens int,
	temperature float64,
) (ModelResponse, error)

// ToolCall is the dependency-neutral graph-node request passed back to the
// host registry. CallID is stable across recovery and is therefore suitable
// for the registry's existing execution receipts.
type ToolCall struct {
	CallID    string
	Name      string
	Arguments map[string]interface{}
}

type ToolCallResult struct {
	Content   string
	Succeeded bool
}

// ToolPolicy contains only execution facts required for recovery. It does not
// replace or widen the host's permission or middleware policy.
type ToolPolicy struct {
	ReadOnly bool
}

type ToolExecutor func(context.Context, ToolCall) (ToolCallResult, error)
type ToolPolicyResolver func(name string, arguments map[string]interface{}) (ToolPolicy, error)
type ContextRestorer func(context.Context, LLMAppRunRecord) context.Context

// Runtime contains host-owned execution callbacks. Graph tool nodes always go
// through ToolExecutor, which the MACS host binds to its existing registry and
// middleware chain.
type Runtime struct {
	// BindingID identifies the stable host-side Agent/runtime that owns this
	// execution surface. It is persisted with a run so crash recovery cannot
	// accidentally use another Agent's model or tool registry in a shared
	// workspace. Empty keeps the legacy single-runtime behavior.
	BindingID         string
	ModelCaller       DetailedModelCaller
	ToolExecutor      ToolExecutor
	ToolPolicy        ToolPolicyResolver
	RestoreContext    ContextRestorer
	pdfExtractor      llmAppPDFExtractor
	DisableAutoResume bool
}

func runtimeFromLegacyModelCaller(caller ModelCaller) Runtime {
	if caller == nil {
		return Runtime{}
	}
	return Runtime{ModelCaller: func(
		ctx context.Context,
		model string,
		messages []map[string]interface{},
		maxTokens int,
		temperature float64,
	) (ModelResponse, error) {
		content, err := caller(ctx, model, messages, maxTokens, temperature)
		return ModelResponse{Content: content}, err
	}}
}

func normalizeRuntime(runtime Runtime) Runtime {
	runtime.BindingID = strings.ToLower(strings.TrimSpace(runtime.BindingID))
	if runtime.pdfExtractor == nil {
		runtime.pdfExtractor = extractLLMAppPDFPages
	}
	return runtime
}

func (runtime Runtime) executeTool(ctx context.Context, call ToolCall) (ToolCallResult, error) {
	if runtime.ToolExecutor == nil {
		return ToolCallResult{}, errors.New("llm_app graph tool executor is unavailable")
	}
	call.CallID = strings.TrimSpace(call.CallID)
	call.Name = strings.TrimSpace(call.Name)
	if call.CallID == "" || call.Name == "" {
		return ToolCallResult{}, errors.New("llm_app graph tool call requires call_id and name")
	}
	return runtime.ToolExecutor(ctx, call)
}

func (runtime Runtime) resolveToolPolicy(name string, arguments map[string]interface{}) (ToolPolicy, error) {
	if runtime.ToolPolicy == nil {
		return ToolPolicy{}, errors.New("llm_app graph tool policy resolver is unavailable")
	}
	return runtime.ToolPolicy(strings.TrimSpace(name), arguments)
}
