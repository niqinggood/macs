package configvalue

import (
	"fmt"
	"reflect"
	"sort"
)

// StartupOnlyFields lists top-level Config fields that require a process restart
// when changed. The config watcher checks this on every auto-reload and logs a
// warning for each changed startup-only field.
//
// Per-run fields (not listed here) take effect on the next request without
// restarting — models, tools, prompts, token limits, memory settings, etc.
var StartupOnlyFields = map[string]string{
	"Gateway":    "HTTP server port, auth, and listen address are bound once at startup; gateway.* edits need a restart.",
	"Channels":   "IM channel clients (Feishu/DingTalk/WeChat/QQ) are created and started once at startup; channels.* edits need a restart.",
	"Pipeline":   "Pipeline templates are loaded and cached once at startup; pipeline.* edits need a restart.",
	"Runtime":    "Runtime semaphore and compress thresholds are snapshotted at startup; runtime.* edits need a restart.",
	"Workspace":  "Workspace is resolved at startup; changing it at runtime may break file paths and sandbox isolation.",
	"DBOperator": "Database connection pool is created once at startup; db_operator.* edits need a restart.",
	"MCP":        "MCP server connections are established once at startup; mcp_servers.* edits need a restart.",
}

// startupOnlyFieldNames returns sorted field names for stable iteration.
func StartupOnlyFieldNames() []string {
	names := make([]string, 0, len(StartupOnlyFields))
	for k := range StartupOnlyFields {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// IsStartupOnlyField returns true if the top-level config field requires restart.
func IsStartupOnlyField(fieldName string) (bool, string) {
	reason, ok := StartupOnlyFields[fieldName]
	return ok, reason
}

// DetectStartupFieldChanges compares old and new config and reports which
// startup-only fields differ. Returns a list of human-readable warnings.
func DetectStartupFieldChanges(old, new interface{}) []string {
	if old == nil || new == nil {
		return nil
	}

	var warnings []string
	oldVal := reflect.Indirect(reflect.ValueOf(old))
	newVal := reflect.Indirect(reflect.ValueOf(new))
	if !oldVal.IsValid() || !newVal.IsValid() || oldVal.Kind() != reflect.Struct || newVal.Kind() != reflect.Struct {
		return nil
	}

	for _, fieldName := range StartupOnlyFieldNames() {
		reason := StartupOnlyFields[fieldName]

		oldField := oldVal.FieldByName(fieldName)
		newField := newVal.FieldByName(fieldName)

		if !oldField.IsValid() || !newField.IsValid() {
			continue
		}

		if !reflect.DeepEqual(oldField.Interface(), newField.Interface()) {
			warnings = append(warnings, fmt.Sprintf(
				"[ConfigReload] ⚠ '%s' changed — %s Restart required for this change to take effect.",
				fieldName, reason,
			))
		}
	}
	return warnings
}

// LogStartupFieldWarnings logs all warnings from DetectStartupFieldChanges.
func LogStartupFieldWarnings(warnings []string, logger func(string, ...interface{})) {
	for _, w := range warnings {
		logger(w)
	}
}
