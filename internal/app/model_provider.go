package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/eventbus"
	"mac/internal/fsutil"
	"mac/internal/processutil"

	"gorm.io/gorm"
)

// ====================== Model Provider System ======================
//
// Model providers abstract AI vendors (DeepSeek, Doubao, OpenAI, etc.) into
// a registry that DE employees and Pipelines can reference by name.
//
// Each provider declares its capabilities (text, vision, audio, video) so
// the system can auto-select the right model for the task — e.g. a quality
// inspector DE gets a vision model, while a chatbot gets a cheap text model.
//
// API keys remain compatible with openkitty-config.yaml. The DB provider table
// is a compatibility cache, but its secret values are encrypted with a
// per-installation key so a copied database does not expose provider keys.

// ====================== Types ======================

// ProviderModel describes one model within a provider.
type ProviderModel struct {
	Name         string  `json:"name" yaml:"name"`             // e.g. "deepseek-chat", "doubao-vision-pro-32k"
	Capability   string  `json:"capability" yaml:"capability"` // "text-to-text", "multi-modal", "text-to-image", "text-to-video"
	MaxTokens    int     `json:"max_tokens" yaml:"max_tokens"`
	Temperature  float64 `json:"temperature" yaml:"temperature"`
	ThinkingMode string  `json:"thinking_mode,omitempty" yaml:"thinking_mode,omitempty"`
}

// ModelProviderRecord persists a provider configuration.
type ModelProviderRecord struct {
	ID         uint   `gorm:"primaryKey"`
	Name       string `gorm:"uniqueIndex;size:128"` // "deepseek", "doubao", "openai"
	Label      string `gorm:"size:256"`             // display name "DeepSeek"
	Type       string `gorm:"size:32"`              // "deepseek" / "openai" / "volcengine"
	BaseURL    string `gorm:"size:512"`
	APIKey     string `gorm:"type:text"`     // enc:v1 AES-256-GCM value
	ModelsJSON string `gorm:"type:text"`     // JSON array of ProviderModel
	IsDefault  bool   `gorm:"default:false"` // system default provider
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (ModelProviderRecord) TableName() string { return "model_providers" }

// toProvider returns a runtime provider and reports whether an old plaintext
// database value should be migrated after the record is loaded.
func (r *ModelProviderRecord) toProvider(key []byte) (*ModelProvider, bool, error) {
	var models []ProviderModel
	if r.ModelsJSON != "" {
		if err := json.Unmarshal([]byte(r.ModelsJSON), &models); err != nil {
			return nil, false, fmt.Errorf("decode models for %s: %w", r.Name, err)
		}
	}
	apiKey := r.APIKey
	legacyPlaintext := apiKey != "" && !strings.HasPrefix(apiKey, providerSecretPrefix)
	if strings.HasPrefix(apiKey, providerSecretPrefix) {
		var err error
		apiKey, err = decryptProviderSecret(r.Name, apiKey, key)
		if err != nil {
			return nil, false, fmt.Errorf("decrypt API key for %s: %w", r.Name, err)
		}
	}
	return &ModelProvider{
		Name: r.Name, Label: r.Label, Type: r.Type,
		BaseURL: r.BaseURL, APIKey: apiKey,
		Models: models, IsDefault: r.IsDefault,
	}, legacyPlaintext, nil
}

// ModelProvider is the runtime (decrypted) provider config.
type ModelProvider struct {
	Name              string          `json:"name" yaml:"name"`
	Label             string          `json:"label" yaml:"label"`
	Type              string          `json:"type" yaml:"type"`
	BaseURL           string          `json:"base_url" yaml:"base_url"`
	APIKey            string          `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	ModelConfigRef    string          `json:"model_config_ref,omitempty" yaml:"model_config_ref,omitempty"`
	Models            []ProviderModel `json:"models" yaml:"models"`
	IsDefault         bool            `json:"is_default" yaml:"is_default"`
	Available         bool            `json:"available" yaml:"-"`
	RequiresAPIKey    bool            `json:"requires_api_key" yaml:"-"`
	UnavailableReason string          `json:"unavailable_reason,omitempty" yaml:"-"`
}

// Capabilities returns a deduplicated list of capabilities this provider supports.
func (p *ModelProvider) Capabilities() []string {
	seen := map[string]bool{}
	var caps []string
	for _, m := range p.Models {
		if !seen[m.Capability] {
			seen[m.Capability] = true
			caps = append(caps, m.Capability)
		}
	}
	sort.Strings(caps)
	return caps
}

// BestModel returns the best model for a given capability.
func (p *ModelProvider) BestModel(capability string) *ProviderModel {
	capability = strings.TrimSpace(capability)
	for _, m := range p.Models {
		if m.Capability == capability {
			return &m
		}
	}
	// Only text requests may fall back to another text model. Image/video/audio
	// capabilities must be explicit so a DE never routes image generation to a
	// chat-only model.
	if capability == "" || capability == "text-to-text" || capability == "text" {
		for _, m := range p.Models {
			if m.Capability == "text-to-text" || m.Capability == "text" {
				return &m
			}
		}
	}
	return nil
}

// ToModelConfig converts a provider+model to the legacy ModelConfig format
// used by the existing agent system.
func (p *ModelProvider) ToModelConfig(model *ProviderModel) ModelConfig {
	if model == nil {
		model = p.BestModel("text-to-text")
	}
	t := model.Temperature
	if t == 0 {
		t = 0.7
	}
	mt := model.MaxTokens
	if mt == 0 {
		mt = 4096
	}
	return ModelConfig{
		Type:           p.Type,
		APIKey:         p.APIKey,
		ModelName:      model.Name,
		BaseURL:        p.BaseURL,
		Temperature:    t,
		MaxTokens:      mt,
		ThinkingMode:   model.ThinkingMode,
		SupportsVision: model.Capability == "multi-modal",
	}
}

// ====================== Provider Registry ======================

// ModelProviderRegistry manages the provider catalog.
type ModelProviderRegistry struct {
	mu                 sync.RWMutex
	providers          map[string]*ModelProvider // name → runtime config
	db                 *gorm.DB
	logger             *log.Logger
	encryptionKey      []byte
	encryptionKeyPath  string
	encryptionKeyError error
}

var globalProviderRegistry *ModelProviderRegistry

const aiEmployeeDefaultModelBindingID = "__ai_employee_model_defaults__"

var aiEmployeeDefaultModelBindingMu sync.RWMutex
var aiEmployeeDefaultModelBinding string

type ModelRouteInfo struct {
	EmployeeID    string `json:"employee_id,omitempty"`
	Capability    string `json:"capability"`
	Provider      string `json:"provider,omitempty"`
	ProviderLabel string `json:"provider_label,omitempty"`
	Model         string `json:"model,omitempty"`
	Source        string `json:"source,omitempty"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
}

func NewModelProviderRegistry(db *gorm.DB, logger *log.Logger, workspace string) *ModelProviderRegistry {
	r := &ModelProviderRegistry{
		providers: make(map[string]*ModelProvider),
		db:        db,
		logger:    logger,
	}
	if db != nil {
		r.encryptionKey, r.encryptionKeyPath, r.encryptionKeyError = loadProviderEncryptionKey(workspace)
		if r.encryptionKeyError != nil && r.logger != nil {
			r.logger.Printf("[Provider] Database secret cache disabled: %v", r.encryptionKeyError)
		}
	}
	r.loadFromDB()
	globalProviderRegistry = r
	return r
}

func (r *ModelProviderRegistry) loadFromDB() {
	if r.db == nil {
		return
	}
	var records []ModelProviderRecord
	if err := r.db.Find(&records).Error; err != nil {
		if r.logger != nil {
			r.logger.Printf("[Provider] Failed to load provider cache: %v", err)
		}
		return
	}
	legacyProviders := make([]*ModelProvider, 0)
	r.mu.Lock()
	for _, rec := range records {
		p, legacyPlaintext, err := rec.toProvider(r.encryptionKey)
		if err != nil {
			if r.logger != nil {
				r.logger.Printf("[Provider] Failed to load %s: %v", rec.Name, err)
			}
			continue
		}
		r.providers[p.Name] = p
		if legacyPlaintext {
			legacyProviders = append(legacyProviders, cloneModelProvider(p))
		}
	}
	loaded := len(r.providers)
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Printf("[Provider] Loaded %d providers", loaded)
	}
	for _, provider := range legacyProviders {
		if err := r.Save(provider); err != nil {
			if r.logger != nil {
				r.logger.Printf("[Provider] Failed to encrypt legacy DB key for %s: %v", provider.Name, err)
			}
			continue
		}
		if r.logger != nil {
			r.logger.Printf("[Provider] Encrypted legacy DB key for %s", provider.Name)
		}
	}
}

// Get returns a provider by name.
func (r *ModelProviderRegistry) Get(name string) (*ModelProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Default returns the system default provider.
func (r *ModelProviderRegistry) Default() *ModelProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range r.providerNamesByPreferenceLocked() {
		p := r.providers[name]
		if p.IsDefault && providerAvailable(p) {
			return p
		}
	}
	// fallback: first available
	for _, name := range r.providerNamesByPreferenceLocked() {
		p := r.providers[name]
		if providerAvailable(p) {
			return p
		}
	}
	return nil
}

// FindModelByName searches all providers for a model by name.
func (r *ModelProviderRegistry) FindModelByName(modelName string) (*ModelProvider, *ProviderModel) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		for i := range p.Models {
			if p.Models[i].Name == modelName {
				return p, &p.Models[i]
			}
		}
	}
	return nil, nil
}

// ResolveForAE returns the best ModelConfig for a given DE and capability.
// Priority: model_binding exact match → capability search → default provider.
func (r *ModelProviderRegistry) ResolveForAE(deID, capability string) (*ModelProvider, *ProviderModel) {
	_, p, m := r.explainResolveForAEBinding(deID, modelBindingForAE(deID), capability)
	return p, m
}

func (r *ModelProviderRegistry) ExplainResolveForAE(deID, capability string) ModelRouteInfo {
	modelBinding, source := modelBindingForAEWithSource(deID)
	info, _, _ := r.explainResolveForAEBinding(deID, modelBinding, capability)
	if source == "ai_employee_default" {
		switch info.Source {
		case "model_binding":
			info.Source = "ai_employee_default"
		case "model_binding_default":
			info.Source = "ai_employee_default_text"
		}
	}
	return info
}

func (r *ModelProviderRegistry) ExplainResolveForAEBinding(deID, modelBinding, capability string) ModelRouteInfo {
	info, _, _ := r.explainResolveForAEBinding(deID, modelBinding, capability)
	return info
}

func modelBindingForAE(deID string) string {
	binding, _ := modelBindingForAEWithSource(deID)
	return binding
}

func modelBindingForAEWithSource(deID string) (string, string) {
	if deID != "" && globalAEManager != nil {
		if cfg, ok := globalAEManager.GetEmployee(deID); ok && strings.TrimSpace(cfg.ModelBinding) != "" {
			return cfg.ModelBinding, "model_binding"
		}
	}
	if binding := getAIEmployeeDefaultModelBinding(); strings.TrimSpace(binding) != "" {
		return binding, "ai_employee_default"
	}
	return "", ""
}

func getAIEmployeeDefaultModelBinding() string {
	aiEmployeeDefaultModelBindingMu.RLock()
	defer aiEmployeeDefaultModelBindingMu.RUnlock()
	return aiEmployeeDefaultModelBinding
}

func setAIEmployeeDefaultModelBinding(binding string) {
	aiEmployeeDefaultModelBindingMu.Lock()
	aiEmployeeDefaultModelBinding = strings.TrimSpace(binding)
	aiEmployeeDefaultModelBindingMu.Unlock()
}

func (r *ModelProviderRegistry) explainResolveForAEBinding(deID, modelBinding, capability string) (ModelRouteInfo, *ModelProvider, *ProviderModel) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	requestedCapability := normalizeModelCapability(capability)
	info := ModelRouteInfo{
		EmployeeID: deID,
		Capability: requestedCapability,
		Available:  false,
		Source:     "none",
	}
	var skipped []string

	if strings.TrimSpace(modelBinding) != "" {
		var binding map[string]string
		if json.Unmarshal([]byte(modelBinding), &binding) == nil {
			if modelName, ok := binding[requestedCapability]; ok {
				if p, m := r.findModelForCapability(modelName, requestedCapability); p != nil {
					return resolvedModelRouteInfo(info, p, m, "model_binding", ""), p, m
				}
				skipped = append(skipped, fmt.Sprintf("model_binding %s=%s unavailable or capability mismatch", requestedCapability, modelName))
			}
			if modelName, ok := binding["default"]; ok && isTextModelCapability(requestedCapability) {
				if p, m := r.findModelForCapability(modelName, "text-to-text"); p != nil {
					return resolvedModelRouteInfo(info, p, m, "model_binding_default", ""), p, m
				}
				skipped = append(skipped, fmt.Sprintf("model_binding default=%s unavailable for text", modelName))
			}
		}
	}

	for _, name := range r.providerNamesByPreferenceLocked() {
		p := r.providers[name]
		if reason := providerUnavailableReason(p); reason != "" {
			skipped = append(skipped, fmt.Sprintf("%s: %s", name, reason))
			continue
		}
		if isLocalModelProvider(p) && !p.IsDefault {
			skipped = append(skipped, fmt.Sprintf("%s: local provider requires explicit model binding or default selection", name))
			continue
		}
		if m := p.BestModel(requestedCapability); m != nil {
			return resolvedModelRouteInfo(info, p, m, "provider_preference", ""), p, m
		}
		skipped = append(skipped, fmt.Sprintf("%s: no %s model", name, requestedCapability))
	}

	if isTextModelCapability(requestedCapability) {
		def := r.Default()
		if def != nil {
			if m := def.BestModel("text-to-text"); m != nil {
				return resolvedModelRouteInfo(info, def, m, "default_provider", ""), def, m
			}
		}
	}
	info.Reason = "no available model route"
	if len(skipped) > 0 {
		info.Reason = info.Reason + ": " + strings.Join(skipped, "; ")
	}
	return info, nil, nil
}

func normalizeModelCapability(capability string) string {
	capability = strings.TrimSpace(capability)
	if capability == "" || capability == "text" || capability == "chat" {
		return "text-to-text"
	}
	return capability
}

func isTextModelCapability(capability string) bool {
	return normalizeModelCapability(capability) == "text-to-text"
}

func resolvedModelRouteInfo(base ModelRouteInfo, p *ModelProvider, m *ProviderModel, source, reason string) ModelRouteInfo {
	base.Source = source
	base.Reason = reason
	if p != nil {
		base.Provider = p.Name
		base.ProviderLabel = p.Label
	}
	if m != nil {
		base.Model = m.Name
	}
	base.Available = p != nil && m != nil
	if base.Available {
		if unavailable := providerUnavailableReason(p); unavailable != "" {
			base.Available = false
			base.Reason = unavailable
		}
	}
	if base.Reason == "" && base.Available {
		base.Reason = "available"
	}
	if base.Reason == "" && !base.Available {
		base.Reason = "model route unavailable"
	}
	return base
}

// findModel searches all providers for a model by name (caller must hold lock).
func (r *ModelProviderRegistry) findModel(modelName string) (*ModelProvider, *ProviderModel) {
	for _, name := range r.providerNamesByPreferenceLocked() {
		p := r.providers[name]
		if !providerAvailable(p) {
			continue
		}
		for i := range p.Models {
			if p.Models[i].Name == modelName {
				return p, &p.Models[i]
			}
		}
	}
	return nil, nil
}

func (r *ModelProviderRegistry) findModelForCapability(modelName, capability string) (*ModelProvider, *ProviderModel) {
	modelName = strings.TrimSpace(modelName)
	capability = strings.TrimSpace(capability)
	if modelName == "" {
		return nil, nil
	}
	for _, name := range r.providerNamesByPreferenceLocked() {
		p := r.providers[name]
		if !providerAvailable(p) {
			continue
		}
		for i := range p.Models {
			if p.Models[i].Name == modelName && (capability == "" || p.Models[i].Capability == capability) {
				return p, &p.Models[i]
			}
		}
	}
	return nil, nil
}

func (r *ModelProviderRegistry) providerNamesByPreferenceLocked() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		left := providerPreferenceScore(r.providers[names[i]])
		right := providerPreferenceScore(r.providers[names[j]])
		if left != right {
			return left < right
		}
		return names[i] < names[j]
	})
	return names
}

func providerAvailable(p *ModelProvider) bool {
	return providerUnavailableReason(p) == ""
}

func providerUnavailableReason(p *ModelProvider) string {
	if p == nil {
		return "provider missing"
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return "base_url missing"
	}
	if !providerHasUsableModel(p) {
		return "models missing"
	}
	if providerRequiresAPIKey(p) && isAPIKeySuspicious(strings.TrimSpace(p.APIKey)) {
		return "api_key missing or placeholder"
	}
	return ""
}

func providerHasUsableModel(p *ModelProvider) bool {
	if p == nil {
		return false
	}
	for _, model := range p.Models {
		if strings.TrimSpace(model.Name) != "" && strings.TrimSpace(model.Capability) != "" {
			return true
		}
	}
	return false
}

func isModelAPIKeyConfigError(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"api_key missing",
		"api key missing",
		"apikey missing",
		"missing api key",
		"missing api",
		"no api key",
		"api_key invalid",
		"api key invalid",
		"apikey invalid",
		"invalid api key",
		"invalid key",
		"api_key missing or placeholder",
		"api key missing or placeholder",
		"placeholder api key",
		"your_api_key_here",
		"your api key",
		"unauthorized",
		"authentication failed",
		"authorization failed",
		"401 unauthorized",
		"鉴权",
		"认证失败",
		"密钥",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func providerRequiresAPIKey(p *ModelProvider) bool {
	if p == nil {
		return true
	}
	if isLocalModelProvider(p) {
		return false
	}
	return true
}

func isLocalModelProvider(p *ModelProvider) bool {
	if p == nil {
		return false
	}
	value := strings.ToLower(strings.Join([]string{p.Type, p.BaseURL, p.Name}, " "))
	if strings.Contains(value, "local") || strings.Contains(value, "ollama") || strings.Contains(value, "vllm") || strings.Contains(value, "localhost") || strings.Contains(value, "127.0.0.1") {
		return true
	}
	return false
}

func providerPreferenceScore(p *ModelProvider) int {
	if p == nil {
		return 1000
	}
	value := strings.ToLower(strings.Join([]string{p.Name, p.Label, p.Type, p.BaseURL}, " "))
	switch {
	case strings.Contains(value, "minimax") || strings.Contains(value, "mini-max"):
		return 0
	case strings.Contains(value, "hy3-preview") || strings.Contains(value, "openrouter") || strings.Contains(value, "hunyuan"):
		return 10
	case strings.Contains(value, "qwen") || strings.Contains(value, "tongyi"):
		return 20
	case strings.Contains(value, "doubao") || strings.Contains(value, "volcengine"):
		return 30
	case strings.Contains(value, "gpt-4o-mini"):
		return 40
	case strings.Contains(value, "openai"):
		return 50
	case strings.Contains(value, "deepseek"):
		return 80
	default:
		if p.IsDefault {
			return 10
		}
		return 60
	}
}

// List returns all providers.
func (r *ModelProviderRegistry) List() []*ModelProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ModelProvider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, modelProviderWithStatus(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func cloneModelProvider(p *ModelProvider) *ModelProvider {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Models != nil {
		cp.Models = append([]ProviderModel(nil), p.Models...)
	}
	return &cp
}

// mergeLegacyModelConfigIntoProviders bridges the old model_cfg catalog and
// the newer model_providers catalog during migration. A partially configured
// provider must not hide a still-usable legacy model route. Explicit provider
// values always win; legacy values only fill missing fields.
//
// ModelConfigRef is the unambiguous escape hatch when several legacy entries
// could belong to the same vendor. Without it, only a unique exact/canonical
// provider-name match or a unique declared-model match is accepted.
func mergeLegacyModelConfigIntoProviders(configured []ModelProvider, legacy map[string]ModelConfig) ([]ModelProvider, []string) {
	effective := make([]ModelProvider, len(configured))
	for i := range configured {
		effective[i] = *cloneModelProvider(&configured[i])
	}
	if len(effective) == 0 || len(legacy) == 0 {
		return effective, nil
	}

	diagnostics := make([]string, 0)
	for i := range effective {
		provider := &effective[i]
		if providerUnavailableReason(provider) == "" {
			continue
		}

		legacyName, legacyConfig, matchReason := matchLegacyModelConfig(*provider, legacy)
		if legacyName == "" {
			if matchReason != "" {
				diagnostics = append(diagnostics, fmt.Sprintf("provider %s legacy inheritance skipped: %s", provider.Name, matchReason))
			}
			continue
		}

		changed := false
		if strings.TrimSpace(provider.Type) == "" && strings.TrimSpace(legacyConfig.Type) != "" {
			provider.Type = legacyConfig.Type
			changed = true
		}
		if strings.TrimSpace(provider.BaseURL) == "" && strings.TrimSpace(legacyConfig.BaseURL) != "" {
			provider.BaseURL = strings.TrimSpace(legacyConfig.BaseURL)
			changed = true
		}
		if !providerHasUsableModel(provider) && strings.TrimSpace(legacyConfig.ModelName) != "" {
			provider.Models = providerModelsFromLegacyConfig(legacyConfig)
			changed = true
		}

		// Never copy a credential onto a different explicit endpoint. This also
		// prevents a broad type match (for example type=openai) from leaking a
		// vendor key to another OpenAI-compatible service.
		if (strings.TrimSpace(provider.APIKey) == "" || isAPIKeySuspicious(strings.TrimSpace(provider.APIKey))) && providerRequiresAPIKey(provider) &&
			!isAPIKeySuspicious(strings.TrimSpace(legacyConfig.APIKey)) &&
			sameProviderOrigin(provider.BaseURL, legacyConfig.BaseURL) {
			provider.APIKey = strings.TrimSpace(legacyConfig.APIKey)
			changed = true
		}

		if changed {
			diagnostics = append(diagnostics, fmt.Sprintf("provider %s inherited missing fields from model_cfg.%s", provider.Name, legacyName))
		}
		if reason := providerUnavailableReason(provider); reason != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("provider %s remains unavailable after legacy inheritance: %s", provider.Name, reason))
		}
	}
	return effective, diagnostics
}

func matchLegacyModelConfig(provider ModelProvider, legacy map[string]ModelConfig) (string, ModelConfig, string) {
	if ref := strings.TrimSpace(provider.ModelConfigRef); ref != "" {
		config, ok := legacy[ref]
		if !ok {
			return "", ModelConfig{}, fmt.Sprintf("model_config_ref %q not found", ref)
		}
		return ref, config, ""
	}

	type candidate struct {
		name   string
		config ModelConfig
	}
	keys := make([]string, 0, len(legacy))
	for key := range legacy {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	selectUnique := func(matches []candidate, label string) (string, ModelConfig, string, bool) {
		switch len(matches) {
		case 0:
			return "", ModelConfig{}, "", false
		case 1:
			return matches[0].name, matches[0].config, "", true
		default:
			names := make([]string, 0, len(matches))
			for _, match := range matches {
				names = append(names, match.name)
			}
			return "", ModelConfig{}, fmt.Sprintf("ambiguous %s matches: %s", label, strings.Join(names, ", ")), true
		}
	}

	providerName := strings.TrimSpace(provider.Name)
	exact := make([]candidate, 0)
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), providerName) {
			exact = append(exact, candidate{name: key, config: legacy[key]})
		}
	}
	if name, config, reason, matched := selectUnique(exact, "exact-name"); matched {
		return name, config, reason
	}

	canonical := make([]candidate, 0)
	for _, key := range keys {
		if strings.EqualFold(canonicalLegacyModelConfigName(key), providerName) {
			canonical = append(canonical, candidate{name: key, config: legacy[key]})
		}
	}
	if name, config, reason, matched := selectUnique(canonical, "canonical-name"); matched {
		return name, config, reason
	}

	declaredModels := make(map[string]struct{})
	for _, model := range provider.Models {
		if name := strings.ToLower(strings.TrimSpace(model.Name)); name != "" {
			declaredModels[name] = struct{}{}
		}
	}
	modelMatches := make([]candidate, 0)
	for _, key := range keys {
		modelName := strings.ToLower(strings.TrimSpace(legacy[key].ModelName))
		if _, ok := declaredModels[modelName]; ok && modelName != "" {
			modelMatches = append(modelMatches, candidate{name: key, config: legacy[key]})
		}
	}
	if name, config, reason, matched := selectUnique(modelMatches, "declared-model"); matched {
		return name, config, reason
	}
	return "", ModelConfig{}, "no unique model_cfg match"
}

func canonicalLegacyModelConfigName(name string) string {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	for _, suffix := range []string{"_model", "-model"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimSpace(name[:len(name)-len(suffix)])
		}
	}
	return name
}

func providerModelsFromLegacyConfig(config ModelConfig) []ProviderModel {
	models := []ProviderModel{{
		Name:         strings.TrimSpace(config.ModelName),
		Capability:   "text-to-text",
		MaxTokens:    config.MaxTokens,
		Temperature:  config.Temperature,
		ThinkingMode: config.ThinkingMode,
	}}
	if config.SupportsVision {
		models = append(models, ProviderModel{
			Name:         strings.TrimSpace(config.ModelName),
			Capability:   "multi-modal",
			MaxTokens:    config.MaxTokens,
			Temperature:  config.Temperature,
			ThinkingMode: config.ThinkingMode,
		})
	}
	return models
}

func sameProviderOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil || leftURL.Scheme == "" || rightURL.Scheme == "" || leftURL.Host == "" || rightURL.Host == "" {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) && strings.EqualFold(leftURL.Host, rightURL.Host)
}

func modelProviderWithStatus(p *ModelProvider) *ModelProvider {
	cp := cloneModelProvider(p)
	if cp == nil {
		return nil
	}
	cp.RequiresAPIKey = providerRequiresAPIKey(cp)
	cp.UnavailableReason = providerUnavailableReason(cp)
	cp.Available = cp.UnavailableReason == ""
	return cp
}

func validateDefaultProvider(p *ModelProvider) error {
	if p == nil || !p.IsDefault {
		return nil
	}
	if reason := providerUnavailableReason(p); reason != "" {
		return fmt.Errorf("default provider %q is not available: %s", p.Name, reason)
	}
	return nil
}

func (r *ModelProviderRegistry) ReplaceFromConfig(providers []ModelProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = make(map[string]*ModelProvider, len(providers))
	for i := range providers {
		p := providers[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		if strings.TrimSpace(p.Label) == "" {
			p.Label = p.Name
		}
		r.providers[p.Name] = cloneModelProvider(&p)
	}
	if r.logger != nil {
		r.logger.Printf("[Provider] Loaded %d providers from openkitty-config.yaml", len(r.providers))
	}
}

func (r *ModelProviderRegistry) SyncDBFromConfig(providers []ModelProvider) error {
	if r == nil || r.db == nil {
		return nil
	}
	if err := r.db.Where("1 = 1").Delete(&ModelProviderRecord{}).Error; err != nil {
		return err
	}
	for i := range providers {
		p := providers[i]
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		if err := r.Save(&p); err != nil {
			return err
		}
	}
	r.ReplaceFromConfig(providers)
	return nil
}

// Save updates runtime cache and mirrors the provider to DB as a compatibility cache.
func (r *ModelProviderRegistry) Save(p *ModelProvider) error {
	if p == nil {
		return fmt.Errorf("provider is nil")
	}
	provider := cloneModelProvider(p)
	provider.Name = strings.TrimSpace(provider.Name)
	if provider.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if strings.TrimSpace(provider.Label) == "" {
		provider.Label = provider.Name
	}
	if r.logger != nil {
		r.logger.Printf("[Provider] Saving: %s (%s)", provider.Name, provider.Type)
	}

	r.mu.Lock()
	if provider.IsDefault {
		for name, existing := range r.providers {
			if name != provider.Name {
				existing.IsDefault = false
			}
		}
	}
	r.providers[provider.Name] = provider
	r.mu.Unlock()

	if r.db == nil {
		return nil
	}
	storedAPIKey := ""
	if provider.APIKey != "" {
		if r.encryptionKeyError != nil {
			return fmt.Errorf("provider secret cache unavailable: %w", r.encryptionKeyError)
		}
		var err error
		storedAPIKey, err = encryptProviderSecret(provider.Name, provider.APIKey, r.encryptionKey)
		if err != nil {
			return fmt.Errorf("encrypt API key for %s: %w", provider.Name, err)
		}
	}
	modelsJSON, _ := json.Marshal(provider.Models)
	rec := ModelProviderRecord{
		Name: provider.Name, Label: provider.Label, Type: provider.Type,
		BaseURL: provider.BaseURL, APIKey: storedAPIKey,
		ModelsJSON: string(modelsJSON), IsDefault: provider.IsDefault,
	}
	if provider.IsDefault {
		r.db.Model(&ModelProviderRecord{}).Where("name <> ?", provider.Name).Update("is_default", false)
	}

	var existing ModelProviderRecord
	lookup := r.db.Where("name = ?", provider.Name).Limit(1).Find(&existing)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected > 0 {
		rec.ID = existing.ID
		return r.db.Save(&rec).Error
	}
	return r.db.Create(&rec).Error
}

func (r *ModelProviderRegistry) UpsertRuntime(p *ModelProvider) {
	if r == nil || p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.IsDefault {
		for name, existing := range r.providers {
			if name != p.Name {
				existing.IsDefault = false
			}
		}
	}
	r.providers[p.Name] = cloneModelProvider(p)
}

func (r *ModelProviderRegistry) DeleteRuntime(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.providers, name)
	r.mu.Unlock()
}

func (r *ModelProviderRegistry) CacheToDB() error {
	if r == nil || r.db == nil {
		return nil
	}
	for _, p := range r.List() {
		if err := r.Save(p); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a provider.
func (r *ModelProviderRegistry) Delete(name string) error {
	r.DeleteRuntime(name)
	if r.db != nil {
		return r.db.Where("name = ?", name).Delete(&ModelProviderRecord{}).Error
	}
	return nil
}

// SeedDefaults seeds built-in providers on fresh DB.
func (r *ModelProviderRegistry) SeedDefaults() {
	if r.db == nil {
		return
	}
	var count int64
	r.db.Model(&ModelProviderRecord{}).Count(&count)
	if count > 0 {
		return // already seeded
	}

	defaults := []*ModelProvider{
		{
			Name: "deepseek", Label: "DeepSeek", Type: "deepseek",
			BaseURL: "https://api.deepseek.com/v1",
			Models: []ProviderModel{
				{Name: "deepseek-v4-pro", Capability: "text-to-text", MaxTokens: 393216, Temperature: 0.7},
			},
		},
		{
			Name: "doubao", Label: "豆包/火山引擎", Type: "volcengine",
			BaseURL: "https://ark.cn-beijing.volces.com/api/v3",
			Models: []ProviderModel{
				{Name: "doubao-seed-2-0-pro-260215", Capability: "text-to-text", MaxTokens: 32768, Temperature: 0.7},
				{Name: "doubao-vision-pro-32k", Capability: "multi-modal", MaxTokens: 32768, Temperature: 0.7},
				{Name: "doubao-seedream-4-0", Capability: "text-to-image", MaxTokens: 4096, Temperature: 0.7},
			},
		},
		{
			Name: "openai", Label: "OpenAI", Type: "openai",
			BaseURL: "https://api.openai.com/v1",
			Models: []ProviderModel{
				{Name: "gpt-4o", Capability: "text-to-text", MaxTokens: 16384, Temperature: 0.7},
				{Name: "gpt-4o", Capability: "multi-modal", MaxTokens: 16384, Temperature: 0.7},
				{Name: "dall-e-3", Capability: "text-to-image", MaxTokens: 4096, Temperature: 0.7},
			},
		},
		{
			Name: "gemini", Label: "Google Gemini", Type: "gemini",
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			Models: []ProviderModel{
				{Name: "gemini-2.5-pro", Capability: "text-to-text", MaxTokens: 65536, Temperature: 0.7},
				{Name: "gemini-2.5-pro", Capability: "multi-modal", MaxTokens: 65536, Temperature: 0.7},
			},
		},
		{
			Name: "minimax", Label: "MiniMax", Type: "openai",
			BaseURL: "https://api.minimaxi.com/v1",
			Models: []ProviderModel{
				{Name: "minimax-m3", Capability: "text-to-text", MaxTokens: 16384, Temperature: 0.7, ThinkingMode: "disabled"},
				{Name: "minimax-m3", Capability: "multi-modal", MaxTokens: 8192, Temperature: 0.7, ThinkingMode: "disabled"},
				{Name: "MiniMax-M1", Capability: "text-to-text", MaxTokens: 32768, Temperature: 0.7},
			},
			IsDefault: true,
		},
		{
			Name: "hy3_openrouter", Label: "Tencent HY3 Preview (OpenRouter)", Type: "openai",
			BaseURL: "https://openrouter.ai/api/v1",
			Models: []ProviderModel{
				{Name: "tencent/hy3-preview", Capability: "text-to-text", MaxTokens: 262144, Temperature: 0.7},
			},
		},
		{
			Name: "ollama", Label: "Ollama (Local)", Type: "local",
			BaseURL: "http://localhost:11434/v1",
			Models: []ProviderModel{
				{Name: "qwen3", Capability: "text-to-text", MaxTokens: 32768, Temperature: 0.7},
				{Name: "llama3.2-vision", Capability: "multi-modal", MaxTokens: 8192, Temperature: 0.7},
			},
		},
	}

	for _, p := range defaults {
		if err := r.Save(p); err != nil {
			r.logger.Printf("[Provider] Seed %s failed: %v", p.Name, err)
		}
	}
	r.logger.Printf("[Provider] Seeded %d default providers", len(defaults))
}

// ====================== AES Encryption ======================

// providerEncryptionKey derives the encryption key from env var or falls back to a built-in key.
// Set OPENKITTY_KEY_ENC in production. The fallback is deliberately visible so users
// know to override it — the key is 32 bytes to support AES-256-GCM.
const (
	providerSecretPrefix = "enc:v1:"
	providerKeyEnv       = "OPENKITTY_KEY_ENC"
)

func loadProviderEncryptionKey(workspace string) ([]byte, string, error) {
	if secret := os.Getenv(providerKeyEnv); secret != "" {
		sum := sha256.Sum256([]byte(secret))
		return sum[:], "environment:" + providerKeyEnv, nil
	}

	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("resolve workspace for provider key: %w", err)
		}
	}
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace for provider key: %w", err)
	}
	keyPath := filepath.Join(absoluteWorkspace, ".openkitty", "secrets", "model-provider.key")
	if raw, readErr := os.ReadFile(keyPath); readErr == nil {
		key, decodeErr := decodeProviderEncryptionKey(raw)
		if decodeErr != nil {
			return nil, keyPath, fmt.Errorf("read provider key %s: %w", keyPath, decodeErr)
		}
		_ = os.Chmod(keyPath, 0600)
		return key, keyPath, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, keyPath, fmt.Errorf("read provider key %s: %w", keyPath, readErr)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, keyPath, fmt.Errorf("generate provider key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, keyPath, fmt.Errorf("create provider secret directory: %w", err)
	}
	_ = os.Chmod(filepath.Dir(keyPath), 0700)
	encoded := []byte(base64.StdEncoding.EncodeToString(key) + "\n")
	if err := fsutil.WriteAtomic(keyPath, encoded, 0600); err != nil {
		return nil, keyPath, fmt.Errorf("persist provider key: %w", err)
	}
	_ = os.Chmod(keyPath, 0600)
	return key, keyPath, nil
}

func decodeProviderEncryptionKey(raw []byte) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length %d, want 32 bytes", len(key))
	}
	return key, nil
}

func encryptProviderSecret(providerName, plaintext string, key []byte) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	encoded, err := encryptAESWithAAD(plaintext, key, []byte(strings.TrimSpace(providerName)))
	if err != nil {
		return "", err
	}
	return providerSecretPrefix + encoded, nil
}

func decryptProviderSecret(providerName, encoded string, key []byte) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, providerSecretPrefix) {
		return "", fmt.Errorf("unsupported provider secret format")
	}
	return decryptAESWithAAD(
		strings.TrimPrefix(encoded, providerSecretPrefix),
		key,
		[]byte(strings.TrimSpace(providerName)),
	)
}

func encryptAES(plaintext string, key []byte) (string, error) {
	return encryptAESWithAAD(plaintext, key, nil)
}

func encryptAESWithAAD(plaintext string, key, additionalData []byte) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), additionalData)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptAES(encoded string, key []byte) (string, error) {
	return decryptAESWithAAD(encoded, key, nil)
}

func decryptAESWithAAD(encoded string, key, additionalData []byte) (string, error) {
	if encoded == "" {
		return "", nil
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// migrateLegacyConfig imports model_config from config.yaml into the provider system.
// Called once on startup. If providers already exist, does nothing.
func (r *ModelProviderRegistry) migrateLegacyConfig(config map[string]ModelConfig) {
	if r.db == nil {
		return
	}

	imported := 0
	preferred := preferredModelConfigKeys(config)
	if len(preferred) == 0 {
		return
	}
	defaultName := preferred[0]
	for _, name := range preferred {
		mc := config[name]
		if !isModelConfigAvailable(mc) {
			continue
		}
		models := []ProviderModel{
			{Name: mc.ModelName, Capability: "text-to-text", MaxTokens: mc.MaxTokens, Temperature: mc.Temperature, ThinkingMode: mc.ThinkingMode},
		}
		if mc.SupportsVision {
			models = append(models, ProviderModel{Name: mc.ModelName, Capability: "multi-modal", MaxTokens: mc.MaxTokens, Temperature: mc.Temperature, ThinkingMode: mc.ThinkingMode})
		}
		p := &ModelProvider{
			Name: name, Label: name, Type: mc.Type,
			BaseURL: mc.BaseURL, APIKey: mc.APIKey,
			Models:    models,
			IsDefault: name == defaultName,
		}
		if err := r.Save(p); err != nil {
			r.logger.Printf("[Provider] Migrate %s failed: %v", name, err)
		} else {
			imported++
		}
	}
	r.logger.Printf("[Provider] Migrated %d providers from config.yaml", imported)
}

// ====================== API Handlers ======================

// handleSkillInstall clones a GitHub repo into skills/ directory.
func (g *GatewayService) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		writeSkillAPIError(w, http.StatusBadRequest, "url required")
		return
	}
	if req.Name == "" {
		parts := strings.Split(strings.TrimRight(req.URL, "/"), "/")
		req.Name = parts[len(parts)-1]
		req.Name = strings.TrimSuffix(req.Name, ".git")
	}
	name, targetDir, err := g.writableSkillTarget(req.Name)
	if err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(targetDir); err == nil {
		writeSkillAPIError(w, http.StatusConflict, "skill "+name+" already exists")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeSkillAPIError(w, http.StatusInternalServerError, "inspect skill target: "+err.Error())
		return
	}
	if err := os.MkdirAll(g.writableSkillDir(), 0755); err != nil {
		writeSkillAPIError(w, http.StatusInternalServerError, "create skills directory: "+err.Error())
		return
	}
	g.logger.Printf("[Skill] Installing %s from %s", name, req.URL)
	cmd := exec.Command("git", "clone", "--", req.URL, targetDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(targetDir)
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		writeSkillAPIError(w, http.StatusBadGateway, "git clone failed: "+msg)
		return
	}
	os.RemoveAll(filepath.Join(targetDir, ".git"))
	if g.engine != nil && g.engine.agentFactory != nil {
		result, reloadErr := g.reloadUserSkillsTransactional()
		if reloadErr != nil {
			_ = os.RemoveAll(targetDir)
			writeSkillAPIError(w, http.StatusUnprocessableEntity, "installed skill failed validation; installation rolled back: "+reloadErr.Error())
			return
		}
		eventbus.PushSystemMessage(fmt.Sprintf("Skill %s installed, %d loaders reloaded", name, result.Loaders), "success")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name, "path": targetDir, "message": fmt.Sprintf("Installed & loaded on %d loaders", result.Loaders)})
		return
	}
	eventbus.PushSystemMessage("Skill "+name+" installed; reload when the engine is ready", "success")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name, "path": targetDir, "message": "Installed. POST /api/skill/reload to activate."})
}

// handleSkillReload rescans the skills/ directory without restart.
func (g *GatewayService) handleSkillReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	if g.engine == nil || g.engine.agentFactory == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "engine not ready"})
		return
	}
	result, err := g.reloadUserSkillsTransactional()
	if err != nil {
		if g.logger != nil {
			g.logger.Printf("[Skill] Transactional reload rejected: %v", err)
		}
		writeSkillAPIError(w, http.StatusUnprocessableEntity, "skill reload rejected; previous in-memory catalog preserved: "+err.Error())
		return
	}
	eventbus.PushSystemMessage(fmt.Sprintf("Skills reloaded transactionally on %d loaders", result.Loaders), "success")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "reloaded": result.Loaders, "builtin_count": result.BuiltinCount,
		"user_count": result.UserCount, "directories": result.Directories,
	})
}

// handleSkillInstallLocal copies a local directory or git repo into skills/.
func (g *GatewayService) handleSkillInstallLocal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeSkillAPIError(w, http.StatusBadRequest, "path required")
		return
	}

	src := req.Path
	srcInfo, err := os.Stat(src)
	if err != nil {
		writeSkillAPIError(w, http.StatusNotFound, "source not found: "+src)
		return
	}
	if !srcInfo.IsDir() {
		writeSkillAPIError(w, http.StatusBadRequest, "source must be a directory")
		return
	}

	name, targetDir, err := g.writableSkillTarget(filepath.Base(filepath.Clean(src)))
	if err != nil {
		writeSkillAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(targetDir); err == nil {
		writeSkillAPIError(w, http.StatusConflict, "skill "+name+" already exists")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeSkillAPIError(w, http.StatusInternalServerError, "inspect skill target: "+err.Error())
		return
	}

	g.logger.Printf("[Skill] Installing from local: %s → %s", src, targetDir)
	if err := processutil.CopyDir(src, targetDir); err != nil {
		_ = os.RemoveAll(targetDir)
		writeSkillAPIError(w, http.StatusInternalServerError, "copy failed: "+err.Error())
		return
	}

	// Auto-reload skills on all agents from their complete configured directory
	// sets. Loading targetDir directly misses its top-level SKILL.md.
	if g.engine != nil && g.engine.agentFactory != nil {
		result, reloadErr := g.reloadUserSkillsTransactional()
		if reloadErr != nil {
			_ = os.RemoveAll(targetDir)
			writeSkillAPIError(w, http.StatusUnprocessableEntity, "installed skill failed validation; installation rolled back: "+reloadErr.Error())
			return
		}
		eventbus.PushSystemMessage(fmt.Sprintf("Skill %s installed from local dir, %d loaders reloaded", name, result.Loaders), "success")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name, "path": targetDir, "message": fmt.Sprintf("Installed & loaded on %d loaders", result.Loaders)})
		return
	}

	eventbus.PushSystemMessage("Skill "+name+" installed from local dir", "success")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": name, "path": targetDir, "message": "Installed. Hot reload to activate."})
}

// handleSkillMerge uses AI to analyze source skills and intelligently merge them.
// It compares with existing skills, deduplicates, and only copies valuable new ones.
func (g *GatewayService) handleSkillMerge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Path == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "path required"})
		return
	}

	src := req.Path
	if _, err := os.Stat(src); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "source not found: " + src})
		return
	}

	// Find first available agent to do the analysis
	var analyzer *LLMAgent
	if g.engine != nil && g.engine.agentFactory != nil {
		for _, agent := range g.engine.agentFactory.GetAllAgents() {
			if llm, ok := agent.(*LLMAgent); ok {
				analyzer = llm
				break
			}
		}
	}
	if analyzer == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "no agent available for analysis"})
		return
	}

	// Scan source skills
	srcSkills := scanSkillDir(src)
	existingSkills := listExistingSkillNames(analyzer)

	if len(srcSkills) == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "no SKILL.md files found in " + src})
		return
	}

	// Build analysis prompt
	var sb strings.Builder
	sb.WriteString("Analyze these source skills and decide which to integrate:\n\n")
	sb.WriteString("## Source Skills (from " + src + ")\n")
	for _, s := range srcSkills {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", s.name, s.description))
	}
	sb.WriteString("\n## Existing Skills\n")
	for _, name := range existingSkills {
		sb.WriteString(fmt.Sprintf("- %s\n", name))
	}
	sb.WriteString("\nFor each source skill, reply with JUDGMENT: KEEP or SKIP and a reason.\n")
	sb.WriteString("KEEP = valuable and not duplicate. SKIP = duplicate/niche/useless.\n")
	sb.WriteString("Reply in format:\n")
	sb.WriteString("KEEP: skill-name — reason\n")
	sb.WriteString("SKIP: skill-name — reason\n")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	judgment, err := analyzer.callLLMInternal(ctx, []map[string]interface{}{
		{"role": "system", "content": "You are a skill curator. Judge each source skill: KEEP (valuable, not duplicate) or SKIP (duplicate, niche, useless). Be decisive."},
		{"role": "user", "content": sb.String()},
	}, 1500)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "AI analysis failed: " + err.Error()})
		return
	}

	// Parse judgment and execute
	var kept, skipped []string
	for _, line := range strings.Split(judgment, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "KEEP:") || strings.HasPrefix(line, "SKIP:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) < 2 {
				continue
			}
			name := strings.TrimSpace(strings.SplitN(parts[1], "—", 2)[0])
			name = strings.TrimPrefix(name, "- ")
			name = strings.TrimSpace(name)
			if strings.HasPrefix(line, "KEEP:") {
				for _, s := range srcSkills {
					if s.name == name {
						cleanName, targetDir, targetErr := g.writableSkillTarget(name)
						if targetErr != nil {
							break
						}
						if _, statErr := os.Stat(targetDir); statErr == nil {
							skipped = append(skipped, cleanName)
							break
						}
						if err := processutil.CopyDir(filepath.Join(src, cleanName), targetDir); err == nil {
							kept = append(kept, cleanName)
						} else {
							_ = os.RemoveAll(targetDir)
						}
						break
					}
				}
			} else {
				skipped = append(skipped, name)
			}
		}
	}

	// Reload if any were kept
	if len(kept) > 0 && g.engine != nil && g.engine.agentFactory != nil {
		for _, agent := range g.engine.agentFactory.GetAllAgents() {
			if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.skillsLoader != nil {
				llmAgent.skillsLoader.LoadAllSkills(g.writableSkillDir(), "ai_merge")
			}
		}
	}

	report := fmt.Sprintf("## AI Skill Merge Report\n\n**Source:** %s\n**Analyzed:** %d skills\n\n### Kept (%d):\n", src, len(srcSkills), len(kept))
	for _, k := range kept {
		report += fmt.Sprintf("- ✅ %s\n", k)
	}
	report += fmt.Sprintf("\n### Skipped (%d):\n", len(skipped))
	for _, s := range skipped {
		report += fmt.Sprintf("- ⏭️ %s\n", s)
	}

	eventbus.PushSystemMessage(fmt.Sprintf("AI merge: %d kept, %d skipped", len(kept), len(skipped)), "success")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "report": report, "kept": kept, "skipped": skipped})
}

type skillInfo struct{ name, description string }

func scanSkillDir(dir string) []skillInfo {
	var skills []skillInfo
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		desc := ""
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "description:") {
				desc = strings.TrimPrefix(line, "description:")
				desc = strings.Trim(desc, "\"")
				if len(desc) > 120 {
					desc = desc[:120] + "..."
				}
				break
			}
		}
		skills = append(skills, skillInfo{name: e.Name(), description: desc})
	}
	return skills
}

func listExistingSkillNames(agent *LLMAgent) []string {
	if agent == nil || agent.skillsLoader == nil {
		return nil
	}
	snapshots := agent.skillsLoader.SnapshotSkills()
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		names = append(names, snapshot.Name)
	}
	return names
}

func (g *GatewayService) handleProviderList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if globalProviderRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "registry not ready"})
		return
	}
	providers := globalProviderRegistry.List()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"providers":   providers,
		"source":      "openkitty-config.yaml",
		"config_path": g.activeConfigPath(),
	})
}

func (g *GatewayService) handleProviderSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	if globalProviderRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "registry not ready"})
		return
	}
	var p ModelProvider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "invalid json"})
		return
	}
	if p.Name == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "name required"})
		return
	}
	if err := validateDefaultProvider(&p); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	globalProviderRegistry.UpsertRuntime(&p)
	if err := g.saveModelProvidersToConfig(globalProviderRegistry.List()); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if err := globalProviderRegistry.CacheToDB(); err != nil && g.logger != nil {
		g.logger.Printf("[Provider] DB cache update failed after YAML save: %v", err)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": p.Name, "config_path": g.activeConfigPath()})
}

func (g *GatewayService) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"POST/DELETE only"}`, 405)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "name required"})
		return
	}
	if globalProviderRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "registry not ready"})
		return
	}
	globalProviderRegistry.DeleteRuntime(name)
	if err := g.saveModelProvidersToConfig(globalProviderRegistry.List()); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if err := globalProviderRegistry.Delete(name); err != nil && g.logger != nil {
		g.logger.Printf("[Provider] DB cache delete failed after YAML save: %v", err)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "deleted": name, "config_path": g.activeConfigPath()})
}

func (g *GatewayService) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if globalProviderRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "registry not ready"})
		return
	}
	aeID := strings.TrimSpace(r.URL.Query().Get("ae_id"))
	if aeID == "" {
		aeID = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	capabilities := []string{}
	if raw := strings.TrimSpace(r.URL.Query().Get("capabilities")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if capability := strings.TrimSpace(part); capability != "" {
				capabilities = append(capabilities, capability)
			}
		}
	}
	if len(capabilities) == 0 {
		capabilities = []string{"text-to-text", "multi-modal", "text-to-image", "text-to-video", "audio"}
	}
	routes := make([]ModelRouteInfo, 0, len(capabilities))
	for _, capability := range capabilities {
		routes = append(routes, globalProviderRegistry.ExplainResolveForAE(aeID, capability))
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "ae_id": aeID, "routes": routes})
}

func (g *GatewayService) handleAEModelDefaults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if globalProviderRegistry == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "registry not ready"})
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			ModelBinding map[string]string `json:"model_binding"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "invalid json"})
			return
		}
		raw, _ := json.Marshal(body.ModelBinding)
		normalized, err := normalizeAEModelBinding(string(raw))
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if err := g.saveAIEmployeeDefaultModelBinding(normalized); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		setAIEmployeeDefaultModelBinding(normalized)
	}
	binding := g.loadAIEmployeeDefaultModelBinding()
	resp := buildAIEmployeeModelDefaultsResponse(binding)
	resp["source"] = "openkitty-config.yaml"
	resp["config_path"] = g.activeConfigPath()
	json.NewEncoder(w).Encode(resp)
}

func (g *GatewayService) loadAIEmployeeDefaultModelBinding() string {
	if g != nil && g.config != nil && len(g.config.AIEmployeeModel.Defaults) > 0 {
		raw, _ := json.Marshal(g.config.AIEmployeeModel.Defaults)
		normalized, err := normalizeAEModelBinding(string(raw))
		if err == nil {
			setAIEmployeeDefaultModelBinding(normalized)
			return normalized
		}
	}
	if g.db == nil {
		return getAIEmployeeDefaultModelBinding()
	}
	var rec AEConfigRecord
	if err := g.db.Where("ae_id = ?", aiEmployeeDefaultModelBindingID).First(&rec).Error; err == nil {
		setAIEmployeeDefaultModelBinding(rec.ModelBinding)
		return rec.ModelBinding
	}
	return getAIEmployeeDefaultModelBinding()
}

func (g *GatewayService) saveAIEmployeeDefaultModelBinding(binding string) error {
	if g != nil && g.config != nil {
		g.config.AIEmployeeModel.Defaults = modelBindingJSONToMap(binding)
		if path := g.activeConfigPath(); path != "" {
			if err := SaveConfig(path, g.config); err != nil {
				return err
			}
		}
	}
	if g == nil || g.db == nil {
		return nil
	}
	var rec AEConfigRecord
	g.db.Where("ae_id = ? AND tenant_id = ?", aiEmployeeDefaultModelBindingID, "default").First(&rec)
	rec.AeID = aiEmployeeDefaultModelBindingID
	rec.TenantID = "default"
	rec.Name = "AI员工默认模型"
	rec.AtName = aiEmployeeDefaultModelBindingID
	rec.Role = "internal model defaults"
	rec.ModelBinding = binding
	if rec.ID == 0 {
		return g.db.Create(&rec).Error
	}
	return g.db.Save(&rec).Error
}

func buildAIEmployeeModelDefaultsResponse(binding string) map[string]interface{} {
	capabilities := []string{"text-to-text", "multi-modal", "text-to-image"}
	bindingMap := map[string]string{}
	if strings.TrimSpace(binding) != "" {
		_ = json.Unmarshal([]byte(binding), &bindingMap)
	}
	routes := make([]ModelRouteInfo, 0, len(capabilities))
	for _, capability := range capabilities {
		route := globalProviderRegistry.ExplainResolveForAEBinding("", binding, capability)
		switch route.Source {
		case "model_binding":
			route.Source = "ai_employee_default"
		case "model_binding_default":
			route.Source = "ai_employee_default_text"
		}
		routes = append(routes, route)
	}
	return map[string]interface{}{
		"ok":            true,
		"model_binding": bindingMap,
		"routes":        routes,
		"options":       aiEmployeeModelOptions(capabilities),
	}
}

func aiEmployeeModelOptions(capabilities []string) map[string][]map[string]interface{} {
	wanted := map[string]bool{}
	for _, capability := range capabilities {
		wanted[normalizeModelCapability(capability)] = true
	}
	options := map[string][]map[string]interface{}{}
	for _, capability := range capabilities {
		options[capability] = []map[string]interface{}{}
	}
	if globalProviderRegistry == nil {
		return options
	}
	for _, provider := range globalProviderRegistry.List() {
		reason := providerUnavailableReason(provider)
		for _, model := range provider.Models {
			capability := normalizeModelCapability(model.Capability)
			if !wanted[capability] {
				continue
			}
			options[capability] = append(options[capability], map[string]interface{}{
				"provider":       provider.Name,
				"provider_label": provider.Label,
				"model":          model.Name,
				"available":      reason == "",
				"reason":         reason,
			})
		}
	}
	return options
}

func (g *GatewayService) activeConfigPath() string {
	if g == nil {
		return ""
	}
	if strings.TrimSpace(g.configpath) != "" {
		return g.configpath
	}
	return g.configPath
}

func (g *GatewayService) saveModelProvidersToConfig(providers []*ModelProvider) error {
	if g == nil || g.config == nil {
		return nil
	}
	configProviders := make([]ModelProvider, 0, len(providers))
	for _, provider := range providers {
		if provider == nil || strings.TrimSpace(provider.Name) == "" {
			continue
		}
		cp := cloneModelProvider(provider)
		configProviders = append(configProviders, *cp)
	}
	sort.Slice(configProviders, func(i, j int) bool { return configProviders[i].Name < configProviders[j].Name })
	previousProviders := append([]ModelProvider(nil), g.config.ModelProviders...)
	path := g.activeConfigPath()
	firstExplicitMigration := len(previousProviders) == 0 && len(configProviders) > 0
	if firstExplicitMigration && path != "" {
		backupPath, err := backupConfigBeforeProviderMigration(path)
		if err != nil {
			return err
		}
		if backupPath != "" && g.logger != nil {
			g.logger.Printf("[Provider] 原配置已备份，开始显式迁移 model_providers: %s", backupPath)
		}
	}

	g.config.ModelProviders = configProviders
	if path != "" {
		if err := SaveConfig(path, g.config); err != nil {
			g.config.ModelProviders = previousProviders
			return err
		}
	}
	return nil
}

func backupConfigBeforeProviderMigration(configPath string) (string, error) {
	info, err := os.Stat(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect config before provider migration: %w", err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read config before provider migration: %w", err)
	}
	backupPath := configPath + ".pre-provider-migration-" + time.Now().Format("20060102-150405.000000000") + ".bak"
	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0600
	}
	if err := fsutil.WriteAtomic(backupPath, raw, perm); err != nil {
		return "", fmt.Errorf("backup config before provider migration: %w", err)
	}
	return backupPath, nil
}

func modelBindingJSONToMap(binding string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(binding) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(binding), &out)
	for capability, model := range out {
		if strings.TrimSpace(model) == "" {
			delete(out, capability)
		}
	}
	return out
}

// handleDEConfig wraps handleAESave to also support model binding.
func (g *GatewayService) handleAEConfigSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	cfg, _, err := decodeAIEmployeeConfigPayload(r.Body)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "invalid json"})
		return
	}
	if cfg.ID == "" || cfg.Name == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "id and name required"})
		return
	}
	if err := g.saveAEToDB(&cfg); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": fmt.Sprintf("save: %v", err)})
		return
	}
	if g.aeManager != nil {
		g.aeManager.Register([]AIEmployeeConfig{cfg})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": cfg.ID})
}

// ResolveModelForDE looks up the appropriate model for a DE and capability.
// Called by the pipeline executor before running a DE step.
func ResolveModelForDE(deID string, capability string) *ModelConfig {
	if globalProviderRegistry == nil {
		return nil
	}
	p, m := globalProviderRegistry.ResolveForAE(deID, capability)
	if p == nil || m == nil {
		return nil
	}
	mc := p.ToModelConfig(m)
	return &mc
}

// ====================== Registration ======================

func (g *GatewayService) initModelProviders(config *Config) {
	logger := log.New(os.Stdout, "[Provider] ", log.LstdFlags)
	workspace := ""
	if config != nil {
		workspace = config.Workspace
	} else if g != nil && g.config != nil {
		workspace = g.config.Workspace
	}
	registry := NewModelProviderRegistry(g.db, logger, workspace)
	if config == nil {
		registry.SeedDefaults()
		return
	}
	if len(config.ModelProviders) > 0 {
		effectiveProviders, diagnostics := mergeLegacyModelConfigIntoProviders(config.ModelProviders, config.ModelConfig)
		for _, diagnostic := range diagnostics {
			logger.Printf("[Provider] %s", diagnostic)
		}
		registry.ReplaceFromConfig(effectiveProviders)
		if err := registry.SyncDBFromConfig(effectiveProviders); err != nil {
			logger.Printf("[Provider] Failed to sync DB provider cache from config: %v", err)
		}
	} else {
		registry.migrateLegacyConfig(config.ModelConfig)
		registry.SeedDefaults()
		logger.Printf("[Provider] Loaded legacy model_cfg into the runtime registry without rewriting the config file")
	}
	if len(config.AIEmployeeModel.Defaults) > 0 {
		raw, _ := json.Marshal(config.AIEmployeeModel.Defaults)
		if normalized, err := normalizeAEModelBinding(string(raw)); err == nil {
			setAIEmployeeDefaultModelBinding(normalized)
		} else {
			logger.Printf("[Provider] Invalid ai_employee_model.defaults ignored: %v", err)
		}
	} else if binding := g.loadAIEmployeeDefaultModelBinding(); strings.TrimSpace(binding) != "" {
		config.AIEmployeeModel.Defaults = modelBindingJSONToMap(binding)
		logger.Printf("[Provider] Loaded AI employee model defaults from the compatibility cache without rewriting the config file")
	}
}

// init registers routes and the provider table.
func initModelProviderRoutes(mux *http.ServeMux, g *GatewayService) {
	mux.HandleFunc("/api/providers", g.handleProviderList)
	mux.HandleFunc("/api/model/routes", g.handleModelRoutes)
	mux.HandleFunc("/api/provider/save", g.handleProviderSave)
	mux.HandleFunc("/api/provider/delete", g.handleProviderDelete)
	// Also register the enhanced DE config endpoint
	mux.HandleFunc("/api/ae/config", g.handleAEConfigSave)
}
