// Package toolcontract defines the dependency-neutral contracts shared by tool
// registries, middleware, runtimes, and concrete tool implementations.
package toolcontract

import "context"

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, params map[string]interface{}) (string, error)
}

// ToolSearchMetadata carries optional discovery terms that should not bloat a
// tool's model-facing description. Registry implementations expose this data to
// tool_detail(action=search), so tools can be found by aliases, Chinese terms,
// and domain vocabulary in addition to their exact registered name.
type ToolSearchMetadata struct {
	Aliases []string
	Tags    []string
}

type ToolSearchMetadataProvider interface {
	ToolSearchMetadata() ToolSearchMetadata
}

type SizeLimitedTool interface {
	Tool
	MaxResultSize() int
}

type ReadOnlyTool interface {
	Tool
	IsReadOnly() bool
}

type CallReadOnlyTool interface {
	Tool
	IsReadOnlyForCall(args map[string]interface{}) bool
}

type SandboxCapabilities struct {
	Process        bool
	Network        bool
	NetworkTargets []string
	SubAgent       bool
}

type SandboxCapabilityTool interface {
	Tool
	SandboxCapabilities() SandboxCapabilities
}

type SandboxCallCapabilityTool interface {
	Tool
	SandboxCapabilitiesForCall(args map[string]interface{}) SandboxCapabilities
}

type NeverParallelTool interface {
	Tool
	IsNeverParallel() bool
}

type PathScopedTool interface {
	Tool
	ScopePath(args map[string]interface{}) string
}

type SnapshotPathsTool interface {
	Tool
	SnapshotPaths(args map[string]interface{}) []string
}

type SchemaRepairAdvisor interface {
	SchemaRepairAdvice(args map[string]interface{}) string
}

type ContentPayloadTool interface {
	ResultIsContentPayload() bool
}

type Call struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Arguments   map[string]interface{} `json:"arguments"`
	HostManaged bool                   `json:"-"`
}

type RecoveryDirective struct {
	Code          string                 `json:"code"`
	ResourceKey   string                 `json:"resource_key,omitempty"`
	SuggestedTool string                 `json:"suggested_tool,omitempty"`
	Arguments     map[string]interface{} `json:"arguments,omitempty"`
	Instruction   string                 `json:"instruction,omitempty"`
	MaxPrompts    int                    `json:"max_prompts,omitempty"`
}

type RecoverableError interface {
	error
	ToolRecoveryDirective() RecoveryDirective
}

type Result struct {
	ToolCallID         string             `json:"tool_call_id"`
	Name               string             `json:"name"`
	Content            string             `json:"content"`
	ExecutionSucceeded *bool              `json:"-"`
	ErrorCode          string             `json:"-"`
	Recovery           *RecoveryDirective `json:"-"`
}
