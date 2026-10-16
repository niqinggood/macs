package nativeenv

import (
	"context"
	"strings"
)

func (m *NativeEnvironmentManager) ListEnvironments() (interface{}, error) {
	entries, err := m.List()
	return map[string]interface{}{"cache_dir": m.CacheDir(), "entries": entries}, err
}

func (m *NativeEnvironmentManager) PruneEnvironments(dryRun bool) (interface{}, error) {
	return m.Prune(dryRun)
}

func (m *NativeEnvironmentManager) InspectEnvironment(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	spec, err := SpecFromParams(m.Workspace(), params)
	if err != nil {
		return nil, err
	}
	return m.Inspect(ctx, spec)
}

func (m *NativeEnvironmentManager) PrepareEnvironment(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	spec, err := SpecFromParams(m.Workspace(), params)
	if err != nil {
		return nil, err
	}
	metadata, reused, output, prepareErr := m.Prepare(ctx, spec)
	result := map[string]interface{}{
		"reused":     reused,
		"cache_dir":  m.CacheDir(),
		"size_bytes": metadata.SizeBytes,
	}
	if metadata.ID != "" {
		result["environment_id"] = metadata.ID
		result["entry_dir"] = m.EntryDir(metadata.ID)
	}
	if strings.TrimSpace(output) != "" {
		result["setup_output"] = output
	}
	return result, prepareErr
}
