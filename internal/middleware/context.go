// Package middleware defines the dependency-neutral middleware context and
// reusable middleware implementations.
package middleware

import (
	"context"

	"mac/internal/toolcontract"
)

type Context struct {
	Context   context.Context
	SessionID string
	TaskID    string
	AgentName string
	UserID    string
	TenantID  string
	RoleID    string
	Source    string
	ServiceID string

	SystemPrompt string
	Messages     []map[string]interface{}

	ToolCall   toolcontract.Call
	ToolResult *toolcontract.Result
	ToolErr    error

	Metadata map[string]any
}

func (ctx *Context) SetMeta(key string, value any) {
	if ctx.Metadata == nil {
		ctx.Metadata = make(map[string]any)
	}
	ctx.Metadata[key] = value
}

func (ctx *Context) GetMeta(key string) (any, bool) {
	if ctx.Metadata == nil {
		return nil, false
	}
	value, ok := ctx.Metadata[key]
	return value, ok
}

type Middleware interface {
	Name() string
	BeforeTool(ctx *Context) error
	AfterTool(ctx *Context) error
	BeforeModel(ctx *Context) error
	AfterModel(ctx *Context) error
}

type Base struct{ name string }

func NewBase(name string) Base { return Base{name: name} }

func (base *Base) Name() string {
	if base == nil {
		return ""
	}
	return base.name
}

func (base *Base) BeforeTool(*Context) error  { return nil }
func (base *Base) AfterTool(*Context) error   { return nil }
func (base *Base) BeforeModel(*Context) error { return nil }
func (base *Base) AfterModel(*Context) error  { return nil }
