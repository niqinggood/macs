package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/agentos"
	"mac/internal/configvalue"
	"mac/internal/featureflags"
	"mac/internal/fsutil"
	hookscore "mac/internal/hooks"
	"mac/internal/modelruntime"
	"mac/internal/routing"
	developmenttools "mac/internal/tools/development"
	networktools "mac/internal/tools/network"
	operationstools "mac/internal/tools/operations"

	"gopkg.in/yaml.v3"
)

type FeatureFlagsConfig struct {
	Disabled []string `json:"disabled" yaml:"disabled"`
}

func normalizeLLMRuntimeConfig(runtime *RuntimeConfig) {
	if runtime == nil {
		return
	}
	policy := modelruntime.ResolveCallPolicy(runtime.LLMCallTimeoutSeconds, runtime.LLMMaxRetries)
	runtime.LLMCallTimeoutSeconds = int(policy.AttemptTimeout / time.Second)
	runtime.LLMMaxRetries = policy.MaxAttempts
}

// ToolsConfig is the global tool configuration persisted under tools_config.
type ToolsConfig struct {
	Enabled                  bool                             `json:"enabled" yaml:"enabled"`
	DisabledTools            []string                         `json:"disabled_tools,omitempty" yaml:"disabled_tools,omitempty"`
	PrivateNetworkProtection bool                             `json:"private_network_protection,omitempty" yaml:"private_network_protection,omitempty"`
	Modbus                   operationstools.ModbusToolConfig `json:"modbus,omitempty" yaml:"modbus,omitempty"`
	UART                     operationstools.UARTToolConfig   `json:"uart,omitempty" yaml:"uart,omitempty"`
	ROS2                     operationstools.ROS2ToolConfig   `json:"ros2,omitempty" yaml:"ros2,omitempty"`
	Robot                    operationstools.RobotToolConfig  `json:"robot,omitempty" yaml:"robot,omitempty"`
}

// serializableModelConfig is deliberately separate from the runtime shape.
// APIKey may contain a resolved secret at runtime, while APIKeyEnv records the
// non-secret source that is safe to persist.
type serializableModelConfig struct {
	Type           string  `json:"type" yaml:"type"`
	APIKey         string  `json:"api_key" yaml:"api_key"`
	APIKeyEnv      string  `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	ModelName      string  `json:"model_name" yaml:"model_name"`
	BaseURL        string  `json:"base_url" yaml:"base_url"`
	Temperature    float64 `json:"temperature" yaml:"temperature"`
	MaxTokens      int     `json:"max_tokens" yaml:"max_tokens"`
	ThinkingMode   string  `json:"thinking_mode,omitempty" yaml:"thinking_mode,omitempty"`
	SupportsVision bool    `json:"supports_vision" yaml:"supports_vision"`
}

func modelAPIKeyValue(model ModelConfig) configvalue.ModelAPIKey {
	return configvalue.ModelAPIKey{APIKey: model.APIKey, APIKeyEnv: model.APIKeyEnv, Resolved: model.apiKeyResolved}
}

func (m ModelConfig) serializableValue() serializableModelConfig {
	key := modelAPIKeyValue(m).Serializable()
	return serializableModelConfig{
		Type:           m.Type,
		APIKey:         key.APIKey,
		APIKeyEnv:      key.APIKeyEnv,
		ModelName:      m.ModelName,
		BaseURL:        m.BaseURL,
		Temperature:    m.Temperature,
		MaxTokens:      m.MaxTokens,
		ThinkingMode:   m.ThinkingMode,
		SupportsVision: m.SupportsVision,
	}
}

// MarshalJSON and MarshalYAML prevent runtime-resolved model keys from
// leaking through snapshots, settings responses, or persisted config files.
func (m ModelConfig) MarshalJSON() ([]byte, error) { return json.Marshal(m.serializableValue()) }

func (m ModelConfig) MarshalYAML() (interface{}, error) { return m.serializableValue(), nil }

func sortedModelConfigNames(config *Config) []string {
	if config == nil || len(config.ModelConfig) == 0 {
		return nil
	}
	names := make([]string, 0, len(config.ModelConfig))
	for name := range config.ModelConfig {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func modelAPIKeyEnvironmentName(modelName string, model ModelConfig) (string, error) {
	return configvalue.ModelAPIKeyEnvironmentName(modelName, modelAPIKeyValue(model))
}

func validateModelConfigEnvironmentReferences(config *Config) error {
	for _, name := range sortedModelConfigNames(config) {
		if _, err := modelAPIKeyEnvironmentName(name, config.ModelConfig[name]); err != nil {
			return err
		}
	}
	return nil
}

func configForPersistence(config *Config) (*Config, error) {
	if config == nil {
		return nil, nil
	}
	persisted := *config
	if config.ModelConfig == nil {
		return &persisted, nil
	}
	persisted.ModelConfig = make(map[string]ModelConfig, len(config.ModelConfig))
	for _, modelName := range sortedModelConfigNames(config) {
		model := config.ModelConfig[modelName]
		key := modelAPIKeyValue(model).Serializable()
		if _, err := configvalue.ModelAPIKeyEnvironmentName(modelName, modelAPIKeyValue(model)); err != nil {
			return nil, err
		}
		model.APIKey = key.APIKey
		model.APIKeyEnv = key.APIKeyEnv
		model.apiKeyResolved = false
		persisted.ModelConfig[modelName] = model
	}
	return &persisted, nil
}

func mergeModelConfigAPIKeyFields(model *ModelConfig, update map[string]interface{}) {
	if model == nil {
		return
	}
	key := configvalue.MergeModelAPIKeyFields(modelAPIKeyValue(*model), update)
	model.APIKey = key.APIKey
	model.APIKeyEnv = key.APIKeyEnv
	model.apiKeyResolved = key.Resolved
}

func resolveModelConfigEnvironment(config *Config) error {
	return resolveModelConfigEnvironmentWithLookup(config, os.LookupEnv)
}

func resolveModelConfigEnvironmentWithLookup(config *Config, lookup func(string) (string, bool)) error {
	if config == nil || len(config.ModelConfig) == 0 {
		return nil
	}
	if lookup == nil {
		return fmt.Errorf("model API key environment lookup is unavailable")
	}
	for _, modelName := range sortedModelConfigNames(config) {
		model := config.ModelConfig[modelName]
		key, err := configvalue.ResolveModelAPIKey(modelName, modelAPIKeyValue(model), lookup)
		if err != nil {
			return err
		}
		model.APIKey = key.APIKey
		model.APIKeyEnv = key.APIKeyEnv
		model.apiKeyResolved = key.Resolved
		config.ModelConfig[modelName] = model
	}
	return nil
}

// EmailConfig is shared by the root configuration schema and the network
// mail tool. Keeping the owner in the network module prevents configuration
// DTOs from forcing the implementation back into package main.
type EmailConfig = networktools.EmailConfig

func loadFeatureFlagsFromConfig(config *Config) {
	commercial := false
	var disabled []string
	if config != nil {
		for _, employee := range config.AIEmployees {
			if employee.Tier == "commercial" {
				commercial = true
				break
			}
		}
		if config.Runtime.FeatureFlags != nil {
			disabled = config.Runtime.FeatureFlags.Disabled
		}
	}
	featureflags.Global().Load(commercial, disabled)
}

func applyGatewayEnvironment(config *Config) error {
	if config == nil {
		return nil
	}
	settings := configvalue.GatewayEnvironment{
		Workspace:          config.Workspace,
		Headless:           config.Headless,
		ListenAddr:         config.Gateway.ListenAddr,
		AgentOSListenAddr:  config.Gateway.AgentOSListenAddr,
		AgentOSMode:        config.Gateway.AgentOSMode,
		MaxTasks:           config.Gateway.MaxTasks,
		MaxQueuedTasks:     config.Gateway.MaxQueuedTasks,
		TaskLeaseSeconds:   config.Gateway.TaskLeaseSeconds,
		TaskPollIntervalMS: config.Gateway.TaskPollIntervalMS,
	}
	if err := configvalue.ApplyGatewayEnvironment(&settings, agentos.NormalizeMode); err != nil {
		return err
	}
	config.Workspace = settings.Workspace
	config.Headless = settings.Headless
	config.Gateway.ListenAddr = settings.ListenAddr
	config.Gateway.AgentOSListenAddr = settings.AgentOSListenAddr
	config.Gateway.AgentOSMode = settings.AgentOSMode
	config.Gateway.MaxTasks = settings.MaxTasks
	config.Gateway.MaxQueuedTasks = settings.MaxQueuedTasks
	config.Gateway.TaskLeaseSeconds = settings.TaskLeaseSeconds
	config.Gateway.TaskPollIntervalMS = settings.TaskPollIntervalMS
	return nil
}

func ValidateModelConfig(name string, cfg *ModelConfig) error {
	if cfg == nil {
		return configvalue.ValidateModel(name, nil)
	}
	return configvalue.ValidateModel(name, &configvalue.Model{
		APIKey:         cfg.APIKey,
		BaseURL:        cfg.BaseURL,
		ModelName:      cfg.ModelName,
		APIKeyOptional: !modelConfigRequiresAPIKey(*cfg),
	})
}

func GetModelConfigHelp() string {
	return configvalue.ModelConfigHelp()
}

func modelCandidate(cfg ModelConfig) routing.ModelCandidate {
	return routing.ModelCandidate{
		Type:        cfg.Type,
		APIKey:      cfg.APIKey,
		ModelName:   cfg.ModelName,
		BaseURL:     cfg.BaseURL,
		AccountAuth: modelConfigUsesAccount(cfg),
	}
}

func modelConfigUsesAccount(cfg ModelConfig) bool {
	return modelruntime.IsPlatformModelURL(cfg.BaseURL) && strings.TrimSpace(cfg.APIKey) == "" && strings.TrimSpace(cfg.APIKeyEnv) == ""
}

func preferredModelConfigKeys(configs map[string]ModelConfig) []string {
	candidates := make(map[string]routing.ModelCandidate, len(configs))
	for name, cfg := range configs {
		candidates[name] = modelCandidate(cfg)
	}
	return routing.PreferredModelKeys(candidates)
}

func isModelConfigAvailable(cfg ModelConfig) bool {
	return routing.IsModelAvailable(modelCandidate(cfg))
}

func modelConfigRequiresAPIKey(cfg ModelConfig) bool {
	return routing.ModelRequiresAPIKey(modelCandidate(cfg))
}

func modelPreferenceScore(name string, cfg ModelConfig) int {
	return routing.ModelPreferenceScore(name, modelCandidate(cfg))
}

func NewEmailTriageTool(cfg EmailConfig, logger *log.Logger) *networktools.EmailTriageTool {
	return networktools.NewEmailTriageTool(networktools.EmailTriageConfig{
		Enabled:  cfg.Enabled,
		IMAPHost: cfg.IMAPHost,
		IMAPPort: cfg.IMAPPort,
		Username: cfg.Username,
		Password: cfg.Password,
	}, logger)
}

func NewModelCompareTool(configs map[string]ModelConfig, logger *log.Logger) *networktools.ModelCompareTool {
	models := make(map[string]networktools.ModelConfig, len(configs))
	for name, cfg := range configs {
		models[name] = networktools.ModelConfig{
			APIKey:      cfg.APIKey,
			ModelName:   cfg.ModelName,
			BaseURL:     cfg.BaseURL,
			Temperature: cfg.Temperature,
			MaxTokens:   cfg.MaxTokens,
		}
	}
	return networktools.NewModelCompareTool(models, logger)
}

func getString(values map[string]interface{}, key, fallback string) string {
	return configvalue.String(values, key, fallback)
}

func getBool(values map[string]interface{}, key string, fallback bool) bool {
	return configvalue.Bool(values, key, fallback)
}

func getInt(values map[string]interface{}, key string, fallback int) int {
	return configvalue.Int(values, key, fallback)
}

func getFloat(values map[string]interface{}, key string, fallback float64) float64 {
	return configvalue.Float(values, key, fallback)
}

func getStringSlice(values map[string]interface{}, key string, fallback []string) []string {
	return configvalue.StringSlice(values, key, fallback)
}

func getMap(values map[string]interface{}, key string, fallback map[string]interface{}) map[string]interface{} {
	return configvalue.Map(values, key, fallback)
}

func recoverCorruptStartupConfig(configPath string) (*Config, string, error) {
	backupPath, err := configvalue.RecoverCorruptFile(
		configPath,
		defaultConfigBytesForPath,
		func(path string) error {
			_, loadErr := LoadConfig(path)
			return loadErr
		},
		fsutil.WriteAtomic,
	)
	if err != nil {
		return nil, backupPath, err
	}
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, backupPath, err
	}
	return config, backupPath, nil
}

func defaultConfigBytesForPath(configPath string) ([]byte, error) {
	var config Config
	if err := yaml.Unmarshal(defaultConfigYAML, &config); err != nil {
		return nil, fmt.Errorf("embedded default config is malformed: %w", err)
	}
	normalizeLLMRuntimeConfig(&config.Runtime)
	if valid, reason := ValidateConfig(&config); !valid {
		return nil, fmt.Errorf("embedded default config is invalid: %s", reason)
	}

	switch strings.ToLower(filepath.Ext(configPath)) {
	case ".yaml", ".yml":
		return append([]byte(nil), defaultConfigYAML...), nil
	case ".json":
		data, err := json.MarshalIndent(&config, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("serialize embedded default config as JSON: %w", err)
		}
		return append(data, '\n'), nil
	default:
		return nil, fmt.Errorf("unsupported config format for recovery %q", configPath)
	}
}

func runtimeHookSettings(config *Config) (*hookscore.V2HookSettings, []string, error) {
	if config == nil {
		return nil, nil, fmt.Errorf("hook runtime config is nil")
	}
	userCandidates, projectCandidates, err := runtimeHookSettingPaths(config.Workspace)
	if err != nil {
		return nil, nil, err
	}
	userSettings, userPaths, err := hookscore.LoadHookSettingsSources(userCandidates...)
	if err != nil {
		return nil, nil, err
	}
	projectSettings, projectPaths, err := hookscore.LoadHookSettingsSources(projectCandidates...)
	if err != nil {
		return nil, nil, err
	}
	configSettings := hookscore.CloneSettings(&config.Hooks)
	merged, err := hookscore.MergeValidatedHookSettings(userSettings, configSettings, projectSettings)
	if err != nil {
		return nil, nil, fmt.Errorf("validate main config hooks: %w", err)
	}
	loaded := append(append([]string(nil), userPaths...), projectPaths...)
	if len(config.Hooks.Enabled) > 0 {
		loaded = append(loaded, "openkitty-config:hooks")
	}
	return merged, loaded, nil
}

func runtimeHookSettingPaths(workspace string) ([]string, []string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "."
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve hook workspace: %w", err)
	}
	userDir := OpenKittyHome()
	projectDir := filepath.Join(workspace, ".openkitty")
	user := []string{
		filepath.Join(userDir, "settings.json"),
		filepath.Join(userDir, "settings.yaml"),
		filepath.Join(userDir, "settings.yml"),
	}
	project := []string{
		filepath.Join(projectDir, "settings.json"),
		filepath.Join(projectDir, "settings.yaml"),
		filepath.Join(projectDir, "settings.yml"),
		filepath.Join(projectDir, "settings.local.json"),
		filepath.Join(projectDir, "settings.local.yaml"),
		filepath.Join(projectDir, "settings.local.yml"),
	}
	return user, project, nil
}

func ReloadGlobalHookSettings(config *Config, logger *log.Logger) ([]string, error) {
	settings, sources, err := runtimeHookSettings(config)
	if err != nil {
		return nil, err
	}
	installGlobalHookSettings(settings, config, sources, logger)
	return sources, nil
}

func installGlobalHookSettings(settings *hookscore.V2HookSettings, config *Config, sources []string, logger *log.Logger) {
	if logger == nil {
		logger = log.New(os.Stdout, "[HookV2] ", log.LstdFlags)
	}
	manager := hookscore.GetOrCreateGlobalHookManagerV2(logger, hookscore.ShellSafetyCheck(developmenttools.IsSystemDestructiveCommand))
	manager.LoadConfig(*settings)
	manager.SetEnabled(hooksEnabledForConfig(config))
	logger.Printf("Hook V2 loaded: %d hooks from %d source(s)", manager.ConfigCount(), len(sources))
}

func isWindowsPathCaseInsensitive() bool { return hookscore.IsCaseInsensitivePath() }

func hooksEnabledForConfig(config *Config) bool {
	if config == nil || config.Runtime.FeatureFlags == nil {
		return true
	}
	for _, disabled := range config.Runtime.FeatureFlags.Disabled {
		if strings.EqualFold(strings.TrimSpace(disabled), string(featureflags.Hooks)) {
			return false
		}
	}
	return true
}
