package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"mac/internal/configvalue"
)

// NativeEnvironmentRuntime owns environment caching and host-specific setup.
type NativeEnvironmentRuntime interface {
	Enabled() bool
	ListEnvironments() (interface{}, error)
	PruneEnvironments(bool) (interface{}, error)
	InspectEnvironment(context.Context, map[string]interface{}) (interface{}, error)
	PrepareEnvironment(context.Context, map[string]interface{}) (interface{}, error)
}

type NativeEnvironmentTool struct {
	runtime NativeEnvironmentRuntime
}

func NewNativeEnvironmentTool(runtime NativeEnvironmentRuntime) *NativeEnvironmentTool {
	return &NativeEnvironmentTool{runtime: runtime}
}

func (t *NativeEnvironmentTool) Name() string { return "native_env" }

func (t *NativeEnvironmentTool) Description() string {
	return "Prepare and reuse an isolated host-native development environment without Docker. " +
		"Environments are content-addressed by OS/architecture, runtime path and version, dependency-file hashes, setup recipe, activation settings, and optional project scope. " +
		"Use inspect before prepare; a cache hit never reruns setup. Pass the returned environment_id to run_tests. " +
		"setup_command may use {runtime}, {env_python}, {env_dir}, {project_dir}, and {cache_dir}; it must write the environment under {env_dir}. " +
		"Use scope=shared only for dependency-only, portable environments; use scope=project for editable installs or local path dependencies. " +
		"Never place API keys or passwords in setup_command or environment values—reference process environment variables instead. " +
		"Prune is dry-run by default and only ever targets this managed cache."
}

func (t *NativeEnvironmentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"inspect", "prepare", "list", "prune"},
			},
			"ecosystem": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"python", "node", "go", "rust", "jvm", "ruby", "dotnet", "other"},
				"description": "Runtime ecosystem. Required for inspect and prepare.",
			},
			"dir": map[string]interface{}{
				"type":        "string",
				"description": "Project directory, relative to the workspace or absolute. Defaults to the workspace.",
			},
			"runtime": map[string]interface{}{
				"type":        "string",
				"description": "Runtime executable name or path. Defaults by ecosystem; required for ecosystem=other.",
			},
			"dependency_files": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Dependency manifests relative to dir. If omitted, common manifests for the ecosystem are detected at the project root.",
			},
			"setup_command": map[string]interface{}{
				"type":        "string",
				"description": "Bounded setup command. Its hash is part of the cache key. It must put all reusable files under {env_dir}.",
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"shared", "project"},
				"description": "shared reuses identical environments across workspaces; project also keys by the current project path. Default: shared.",
			},
			"path_entries": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Relative directories under {env_dir} to prepend to PATH when attached, such as Scripts on Windows or bin on Linux.",
			},
			"environment": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": map[string]interface{}{"type": "string"},
				"description":          "Non-secret activation variables. Values may use {env_dir}, {project_dir}, and {cache_dir}.",
			},
			"dry_run": map[string]interface{}{
				"type":        "boolean",
				"description": "For prune, report candidates without deleting them. Defaults to true.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *NativeEnvironmentTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil || !t.runtime.Enabled() {
		return "", errors.New("native execution environments are disabled")
	}
	action := strings.ToLower(strings.TrimSpace(configvalue.String(params, "action", "")))
	var value interface{}
	var err error
	switch action {
	case "list":
		value, err = t.runtime.ListEnvironments()
	case "prune":
		value, err = t.runtime.PruneEnvironments(configvalue.Bool(params, "dry_run", true))
	case "inspect":
		value, err = t.runtime.InspectEnvironment(ctx, params)
	case "prepare":
		value, err = t.runtime.PrepareEnvironment(ctx, params)
	default:
		return "", errors.New("action must be inspect, prepare, list, or prune")
	}
	if err != nil && action != "prepare" {
		return "", err
	}
	if value == nil && err != nil {
		return "", err
	}
	encoded, marshalErr := json.MarshalIndent(value, "", "  ")
	if marshalErr != nil {
		return "", marshalErr
	}
	return string(encoded), err
}

func (t *NativeEnvironmentTool) IsReadOnly() bool      { return false }
func (t *NativeEnvironmentTool) IsNeverParallel() bool { return true }
func (t *NativeEnvironmentTool) MaxResultSize() int    { return 12 * 1024 }
