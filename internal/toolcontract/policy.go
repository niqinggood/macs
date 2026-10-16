package toolcontract

import (
	"sort"
	"strings"
)

type ToolPolicyResult struct {
	Kept    []string
	Removed []string
}

// SelectToolPolicy classifies registered tool names without knowing about a
// concrete registry implementation.
func SelectToolPolicy(names, allowed, disallowed []string) ToolPolicyResult {
	allowedSet := StringSet(allowed)
	disallowedSet := StringSet(disallowed)
	hasAllowList := len(allowedSet) > 0

	result := ToolPolicyResult{}
	for _, name := range names {
		normalized := NormalizeToolName(name)
		remove := disallowedSet[normalized]
		if hasAllowList && !allowedSet[normalized] {
			remove = true
		}
		if remove {
			result.Removed = append(result.Removed, name)
			continue
		}
		result.Kept = append(result.Kept, name)
	}
	result.Kept = uniqueSortedToolNames(result.Kept)
	result.Removed = uniqueSortedToolNames(result.Removed)
	return result
}

func StringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = NormalizeToolName(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func NormalizeToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func uniqueSortedToolNames(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
