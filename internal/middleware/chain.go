package middleware

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	"mac/internal/toolcontract"
)

// Chain is an ordered middleware list. Add and Prepend return independent
// chains so registries can safely customize policy without sharing slices.
type Chain struct {
	mu          sync.RWMutex
	middlewares []Middleware
}

func NewChain(middlewares ...Middleware) *Chain {
	seen := make(map[string]int)
	filtered := make([]Middleware, 0, len(middlewares))
	for _, item := range middlewares {
		if nilMiddleware(item) {
			continue
		}
		name := item.Name()
		if name == "" {
			continue
		}
		if index, ok := seen[name]; ok {
			filtered[index] = item
			continue
		}
		seen[name] = len(filtered)
		filtered = append(filtered, item)
	}
	return &Chain{middlewares: filtered}
}

func nilMiddleware(item Middleware) bool {
	if item == nil {
		return true
	}
	value := reflect.ValueOf(item)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (chain *Chain) Add(items ...Middleware) *Chain {
	chain.mu.RLock()
	existing := append([]Middleware(nil), chain.middlewares...)
	chain.mu.RUnlock()
	return NewChain(append(existing, items...)...)
}

func (chain *Chain) Prepend(items ...Middleware) *Chain {
	chain.mu.RLock()
	existing := append([]Middleware(nil), chain.middlewares...)
	chain.mu.RUnlock()
	return NewChain(append(items, existing...)...)
}

func (chain *Chain) List() []string {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	names := make([]string, len(chain.middlewares))
	for index, item := range chain.middlewares {
		names[index] = item.Name()
	}
	return names
}

func (chain *Chain) runBeforeTool(ctx *Context) error {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	for _, item := range chain.middlewares {
		if err := item.BeforeTool(ctx); err != nil {
			return fmt.Errorf("middleware %s: %w", item.Name(), err)
		}
	}
	return nil
}

func (chain *Chain) runAfterTool(ctx *Context) error {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	var failures []string
	for _, item := range chain.middlewares {
		if err := item.AfterTool(ctx); err != nil {
			log.Printf("[middleware] %s.AfterTool: %v", item.Name(), err)
			failures = append(failures, fmt.Sprintf("%s: %v", item.Name(), err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("after-tool errors: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (chain *Chain) RunBeforeModel(ctx *Context) error {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	for _, item := range chain.middlewares {
		if err := item.BeforeModel(ctx); err != nil {
			return fmt.Errorf("middleware %s: %w", item.Name(), err)
		}
	}
	return nil
}

func (chain *Chain) RunAfterModel(ctx *Context) error {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	for _, item := range chain.middlewares {
		if err := item.AfterModel(ctx); err != nil {
			log.Printf("[middleware] %s.AfterModel: %v", item.Name(), err)
		}
	}
	return nil
}

func (chain *Chain) WrapToolExecution(ctx *Context, execute func() (toolcontract.Result, error)) (toolcontract.Result, error) {
	if err := chain.runBeforeTool(ctx); err != nil {
		code := "middleware_blocked"
		var coded interface{ MiddlewareErrorCode() string }
		if errors.As(err, &coded) && strings.TrimSpace(coded.MiddlewareErrorCode()) != "" {
			code = coded.MiddlewareErrorCode()
		}
		result := toolcontract.Result{
			ToolCallID:         ctx.ToolCall.ID,
			Name:               ctx.ToolCall.Name,
			Content:            fmt.Sprintf("[BLOCKED] %v", err),
			ExecutionSucceeded: toolcontract.OutcomePointer(false),
			ErrorCode:          code,
		}
		if code == "approval_required" {
			return result, err
		}
		return result, nil
	}

	result, err := execute()
	ctx.ToolResult = &result
	ctx.ToolErr = err
	if afterErr := chain.runAfterTool(ctx); afterErr != nil {
		log.Printf("[middleware] AfterTool warnings: %v", afterErr)
	}
	if ctx.ToolResult != nil {
		return *ctx.ToolResult, ctx.ToolErr
	}
	return result, err
}
