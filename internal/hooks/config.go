package hooks

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ====================== Hook Configuration Loading ======================
// Hook settings may be a direct document ({"enabled": ...}) or a block in a
// larger settings file ({"hooks": {"enabled": ...}}). JSON and YAML are both
// supported so the same schema can be used in settings.json and the main
// openkitty-config.yaml.

type hookSettingsDocument struct {
	Hooks   *V2HookSettings                   `json:"hooks" yaml:"hooks"`
	Enabled map[LifecycleEvent][]V2HookConfig `json:"enabled" yaml:"enabled"`
}

// LoadHookSettingsFromFile loads and validates hook settings. A missing file
// is an empty source; malformed or semantically invalid files return an error.
func LoadHookSettingsFromFile(path string) (*V2HookSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultHookSettings(), nil
		}
		return nil, fmt.Errorf("read hook settings %s: %w", path, err)
	}

	var document hookSettingsDocument
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse hook settings %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse hook settings %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported hook settings format %s", filepath.Ext(path))
	}

	settings := document.Hooks
	if settings == nil {
		settings = &V2HookSettings{Enabled: document.Enabled}
	}
	settings = CloneSettings(settings)
	if err := ValidateHookSettings(settings); err != nil {
		return nil, fmt.Errorf("validate hook settings %s: %w", path, err)
	}
	return settings, nil
}

// LoadHookSettingsSources loads all existing files in order, then combines
// their hooks. It does all parsing and validation before returning so callers
// can atomically replace a live configuration.
func LoadHookSettingsSources(paths ...string) (*V2HookSettings, []string, error) {
	sources := make([]*V2HookSettings, 0, len(paths))
	loadedPaths := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))

	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve hook settings path %s: %w", path, err)
		}
		key := filepath.Clean(abs)
		if IsCaseInsensitivePath() {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("inspect hook settings %s: %w", abs, err)
		}
		settings, err := LoadHookSettingsFromFile(abs)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, settings)
		loadedPaths = append(loadedPaths, abs)
	}

	merged, err := MergeValidatedHookSettings(sources...)
	if err != nil {
		return nil, nil, err
	}
	return merged, loadedPaths, nil
}

// MergeValidatedHookSettings combines hook sources in priority order. Hooks
// are additive (as in Claude-style settings); the manager later removes exact
// duplicates while preserving deterministic source order.
func MergeValidatedHookSettings(sources ...*V2HookSettings) (*V2HookSettings, error) {
	merged := DefaultHookSettings()
	for _, src := range sources {
		if src == nil {
			continue
		}
		if err := ValidateHookSettings(src); err != nil {
			return nil, err
		}
		for event, configs := range src.Enabled {
			merged.Enabled[event] = append(merged.Enabled[event], configs...)
		}
	}
	return merged, nil
}

// MergeHookSettings is retained for callers that use the original no-error
// API. Invalid input produces an empty configuration instead of a partial one.
func MergeHookSettings(sources ...*V2HookSettings) *V2HookSettings {
	merged, err := MergeValidatedHookSettings(sources...)
	if err != nil {
		return DefaultHookSettings()
	}
	return merged
}

// ValidateHookSettings rejects unknown events and incomplete executor configs.
// Validation never edits the caller's map, which is important for reload
// rollback semantics.
func ValidateHookSettings(settings *V2HookSettings) error {
	if settings == nil {
		return nil
	}
	for event, configs := range settings.Enabled {
		if !IsValidLifecycleEvent(string(event)) {
			return fmt.Errorf("unknown lifecycle event %q", event)
		}
		for index, cfg := range configs {
			if err := validateHookConfig(cfg); err != nil {
				return fmt.Errorf("event %s hook %d: %w", event, index+1, err)
			}
		}
	}
	return nil
}

func validateHookConfig(cfg V2HookConfig) error {
	switch cfg.Type {
	case HookExecShell, HookExecCallback:
		if strings.TrimSpace(cfg.Command) == "" {
			return fmt.Errorf("%s hook requires command", cfg.Type)
		}
	case HookExecHTTP:
		rawURL := strings.TrimSpace(cfg.URL)
		if rawURL == "" {
			return fmt.Errorf("http hook requires url")
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("http hook url must be an absolute http(s) URL")
		}
	case HookExecPrompt, HookExecAgent:
		// Prompt and agent hooks use the lifecycle context as their prompt.
	default:
		return fmt.Errorf("unknown hook executor type %q", cfg.Type)
	}

	if cfg.Timeout < 0 {
		return fmt.Errorf("timeout must be >= 0")
	}
	maxTimeout := 30
	if cfg.Type == HookExecPrompt || cfg.Type == HookExecAgent {
		maxTimeout = 60
	}
	if cfg.Timeout > maxTimeout {
		return fmt.Errorf("timeout must be <= %d seconds for %s hooks", maxTimeout, cfg.Type)
	}
	return nil
}

func CloneSettings(settings *V2HookSettings) *V2HookSettings {
	cloned := DefaultHookSettings()
	if settings == nil {
		return cloned
	}
	for event, configs := range settings.Enabled {
		cloned.Enabled[event] = append([]V2HookConfig(nil), configs...)
	}
	return cloned
}

func IsCaseInsensitivePath() bool {
	return os.PathSeparator == '\\'
}

// DefaultHookSettings returns a valid configuration with no hooks configured.
func DefaultHookSettings() *V2HookSettings {
	return &V2HookSettings{Enabled: make(map[LifecycleEvent][]V2HookConfig)}
}
