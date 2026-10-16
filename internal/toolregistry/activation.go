package toolregistry

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

const MaxTurnActivatedTools = 4

var activationSafetyBlacklist = map[string]bool{
	"agent_stop": true, "auto_mode": true, "delete_file": true,
	"manage_permissions": true, "manage_plugins": true, "vault_explore": true,
}

func ActivationBlocked(name string) bool {
	return activationSafetyBlacklist[normalizeName(name)]
}

type activationContextKey struct{}
type suppressDefinitionsContextKey struct{}
type restrictDefinitionsContextKey struct{}
type requiredDefinitionsContextKey struct{}
type requiredObligationsContextKey struct{}

type Activation struct {
	mu     sync.RWMutex
	limit  int
	names  []string
	active map[string]bool
}

func NewActivation(limit int) *Activation {
	if limit <= 0 || limit > MaxTurnActivatedTools {
		limit = MaxTurnActivatedTools
	}
	return &Activation{limit: limit, active: make(map[string]bool, limit)}
}

func WithNewTurnActivation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, activationContextKey{}, NewActivation(MaxTurnActivatedTools))
}

func ActivationFromContext(ctx context.Context) *Activation {
	if ctx == nil {
		return nil
	}
	activation, _ := ctx.Value(activationContextKey{}).(*Activation)
	return activation
}

func ActivatedNames(ctx context.Context) []string {
	if activation := ActivationFromContext(ctx); activation != nil {
		return activation.Names()
	}
	return nil
}

func (a *Activation) Activate(name string) error {
	if a == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name is required for activation")
	}
	if activationSafetyBlacklist[strings.ToLower(name)] {
		return fmt.Errorf("tool '%s' is blocked from dynamic activation by safety policy", name)
	}
	if CoreTools[name] {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active[name] {
		return nil
	}
	if len(a.names) >= a.limit {
		return fmt.Errorf("dynamic tool activation limit reached (%d per turn)", a.limit)
	}
	a.active[name] = true
	a.names = append(a.names, name)
	return nil
}

func (a *Activation) Names() []string {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]string(nil), a.names...)
}

func WithSuppressedDefinitions(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, suppressDefinitionsContextKey{}, true)
}

func DefinitionsSuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(suppressDefinitionsContextKey{}).(bool)
	return suppressed
}

func WithRestrictedDefinitions(ctx context.Context, names ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		if name = normalizeName(name); name != "" {
			allowed[name] = true
		}
	}
	return context.WithValue(ctx, restrictDefinitionsContextKey{}, allowed)
}

func WithRequiredDefinitions(ctx context.Context, names ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	required := make(map[string]bool, len(names))
	for _, name := range names {
		if name = normalizeName(name); name != "" && !activationSafetyBlacklist[name] {
			required[name] = true
		}
	}
	return context.WithValue(ctx, requiredDefinitionsContextKey{}, required)
}

func RequiredDefinitions(ctx context.Context) map[string]bool {
	if ctx == nil {
		return nil
	}
	required, _ := ctx.Value(requiredDefinitionsContextKey{}).(map[string]bool)
	return required
}

func WithRequiredObligations(ctx context.Context, names ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	seen := make(map[string]bool, len(names))
	required := make([]string, 0, len(names))
	for _, name := range names {
		name = normalizeName(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		required = append(required, name)
	}
	return context.WithValue(ctx, requiredObligationsContextKey{}, required)
}

func RequiredObligations(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	required, _ := ctx.Value(requiredObligationsContextKey{}).([]string)
	return append([]string(nil), required...)
}

func (r *Registry) CoreDefinitionsWithActivated(activated []string) []map[string]interface{} {
	return r.DefinitionsWithActivatedAndRequired(activated, nil)
}

func (r *Registry) DefinitionsWithActivatedAndRequired(activated []string, required map[string]bool) []map[string]interface{} {
	allowed := make(map[string]bool, len(CoreTools)+len(required)+len(activated))
	for name, enabled := range CoreTools {
		if enabled {
			allowed[name] = true
		}
	}
	for name, enabled := range required {
		name = normalizeName(name)
		if enabled && !activationSafetyBlacklist[name] {
			if _, ok := r.Get(name); ok {
				allowed[name] = true
			}
		}
	}
	added := 0
	for _, name := range activated {
		name = normalizeName(name)
		if name == "" || allowed[name] || activationSafetyBlacklist[name] {
			continue
		}
		if _, ok := r.Get(name); !ok {
			continue
		}
		if len(required) == 0 && added >= MaxTurnActivatedTools {
			break
		}
		allowed[name] = true
		added++
	}
	return r.BuildDefinitions(allowed)
}

func RestrictDefinitions(ctx context.Context, definitions []map[string]interface{}) []map[string]interface{} {
	if ctx == nil {
		return definitions
	}
	allowed, _ := ctx.Value(restrictDefinitionsContextKey{}).(map[string]bool)
	if len(allowed) == 0 {
		return definitions
	}
	filtered := make([]map[string]interface{}, 0, len(allowed))
	for _, definition := range definitions {
		function, _ := definition["function"].(map[string]interface{})
		name, _ := function["name"].(string)
		if allowed[normalizeName(name)] {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }
