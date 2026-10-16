package app

import (
	"mac/internal/permissionpolicy"
	"mac/internal/toolcontract"
)

// The execution engine owns the effective mode; SafetyGuard remains a
// compatibility view. Host and tool transitions update both under one lock.
func (a *LLMAgent) permissionModeLocked() permissionpolicy.PermissionMode {
	if a.permissionEngine != nil {
		return a.permissionEngine.CurrentMode()
	}
	if a.safetyGuard != nil && a.safetyGuard.PermissionMode != "" {
		return a.safetyGuard.PermissionMode
	}
	return permissionpolicy.PermissionDefault
}

func (a *LLMAgent) currentPermissionMode() permissionpolicy.PermissionMode {
	a.planModeMu.RLock()
	defer a.planModeMu.RUnlock()
	return a.permissionModeLocked()
}

func (a *LLMAgent) applyPermissionModeLocked(mode permissionpolicy.PermissionMode) {
	if a.permissionEngine == nil && a.safetyGuard == nil {
		a.permissionEngine = permissionpolicy.NewEngineForCalls(mode, func(name string, args map[string]interface{}) bool {
			return permissionpolicy.ToolCallIsReadOnlyForPolicy(ToolCall{Name: name, Arguments: args}, a.toolRegistry, toolcontract.IsReadOnlyToolName)
		})
	}
	if a.permissionEngine != nil {
		a.permissionEngine.SetMode(mode)
	}
	if a.safetyGuard != nil {
		a.safetyGuard.SetPermissionMode(mode)
	}
}

func (a *LLMAgent) setPermissionMode(mode permissionpolicy.PermissionMode) {
	a.planModeMu.Lock()
	defer a.planModeMu.Unlock()
	current := a.permissionModeLocked()
	if mode == permissionpolicy.PermissionPlan && current != permissionpolicy.PermissionPlan {
		a.prePlanMode = current
	}
	a.applyPermissionModeLocked(mode)
}

func (a *LLMAgent) EnterPlanMode() {
	a.setPermissionMode(permissionpolicy.PermissionPlan)
	if a.fileLogger != nil {
		a.fileLogger.Printf("[PLAN_MODE] entered plan mode")
	}
}

func (a *LLMAgent) ExitPlanMode() {
	a.planModeMu.Lock()
	defer a.planModeMu.Unlock()
	if a.permissionModeLocked() != permissionpolicy.PermissionPlan {
		return
	}
	mode := a.prePlanMode
	if mode == permissionpolicy.PermissionPlan || !permissionpolicy.IsValidPermissionMode(string(mode)) {
		mode = permissionpolicy.PermissionDefault
	}
	a.applyPermissionModeLocked(mode)
	if a.fileLogger != nil {
		a.fileLogger.Printf("[PLAN_MODE] exited plan mode, restored: %s", mode)
	}
}

func (a *LLMAgent) IsInPlanMode() bool {
	return a.currentPermissionMode() == permissionpolicy.PermissionPlan
}
