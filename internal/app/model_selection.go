package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mac/internal/modelruntime"
)

// selectedModelKey preserves legacy defaults until the user chooses a model.
// An explicit selection is never silently replaced by another paid provider.
func selectedModelKey(config *Config) string {
	if config == nil {
		return ""
	}
	if name := strings.TrimSpace(config.DefaultModel); name != "" {
		return name
	}
	if agent, ok := preferredConfiguredAgent(config); ok {
		name := strings.TrimSpace(agent.Model)
		if model, exists := config.ModelConfig[name]; exists && isModelConfigAvailable(model) {
			return name
		}
	}
	if names := preferredModelConfigKeys(config.ModelConfig); len(names) > 0 {
		return names[0]
	}
	return ""
}

func effectiveAgentModelKey(config *Config, agent AgentConfig) string {
	if name := strings.TrimSpace(agent.Model); name != "" {
		return name
	}
	return selectedModelKey(config)
}

// The composer only needs model identities and readiness, never credentials or
// upstream addresses. Writes reuse the settings persistence/revision contract.
func (g *GatewayService) handleComposerModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !personalMemoryLocalRequest(r) {
		sendJSONError(w, "local_only", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		sendJSONError(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	config, err := LoadConfig(g.configpath)
	if err != nil {
		sendJSONError(w, "invalid_config", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			DefaultModel  string `json:"default_model"`
			PlatformModel string `json:"platform_model,omitempty"`
			Revision      string `json:"config_revision"`
			InheritAgent  string `json:"inherit_agent,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(interface{})) != io.EOF {
			sendJSONError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Revision) == "" {
			sendJSONError(w, "revision_required", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(input.DefaultModel)
		platformID := strings.TrimSpace(input.PlatformModel)
		if (name == "") == (platformID == "") {
			sendJSONError(w, "invalid_request", http.StatusBadRequest)
			return
		}
		if platformID == "" {
			if model, exists := config.ModelConfig[name]; !exists || !isModelConfigAvailable(model) {
				sendJSONError(w, "model_unavailable", http.StatusBadRequest)
				return
			}
		}
		// Check before discovery, then let the settings writer check again after
		// discovery. A concurrent edit must not be overwritten by this snapshot.
		revision, err := configFileRevision(g.configpath)
		if err != nil {
			sendJSONError(w, "invalid_config", http.StatusInternalServerError)
			return
		}
		if revision != strings.TrimSpace(input.Revision) {
			sendJSONError(w, "config_conflict", http.StatusConflict)
			return
		}
		if platformID != "" {
			var code string
			name, code = g.configureCatalogModel(r.Context(), config, platformID)
			if code != "" {
				status := http.StatusBadRequest
				if code == "platform_catalog_unavailable" {
					status = http.StatusBadGateway
				}
				sendJSONError(w, code, status)
				return
			}
		}
		updates := map[string]interface{}{"default_model": name, "config_revision": input.Revision}
		if platformID != "" {
			updates["model_config"] = config.ModelConfig
		}
		if input.InheritAgent != "" {
			found := false
			for index := range config.Agents {
				if config.Agents[index].Name == input.InheritAgent {
					config.Agents[index].Model = ""
					found = true
					break
				}
			}
			if !found {
				sendJSONError(w, "agent_not_found", http.StatusBadRequest)
				return
			}
			updates["agents"] = config.Agents
		}
		data, _ := json.Marshal(updates)
		request := r.Clone(r.Context())
		request.Body = io.NopCloser(bytes.NewReader(data))
		g.handleConfigSections(w, request)
		return
	}
	models := make([]map[string]interface{}, 0, len(config.ModelConfig))
	for _, name := range sortedModelConfigNames(config) {
		model := config.ModelConfig[name]
		source := "custom"
		if modelruntime.IsPlatformModelURL(model.BaseURL) {
			source = "platform"
		}
		models = append(models, map[string]interface{}{
			"id": name, "model": model.ModelName, "source": source,
			"available":    isModelConfigAvailable(model),
			"account_auth": modelConfigUsesAccount(model),
		})
	}
	agents := make([]map[string]interface{}, 0, len(config.Agents))
	for _, agent := range config.Agents {
		agents = append(agents, map[string]interface{}{
			"name": agent.Name, "model_config": effectiveAgentModelKey(config, agent),
			"model_override": strings.TrimSpace(agent.Model),
		})
	}
	revision, err := configFileRevision(g.configpath)
	if err != nil {
		sendJSONError(w, "invalid_config", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"models": models, "agents": agents, "default_model": selectedModelKey(config),
		"default_agent": config.DefaultAgent, "config_revision": revision,
	})
}

// Selection installs only an available text model from the trusted account
// service catalog. The browser supplies an ID, never credentials or a URL.
func (g *GatewayService) configureCatalogModel(ctx context.Context, config *Config, id string) (string, string) {
	if !strings.HasPrefix(id, "platform:") || len(id) > 200 || strings.ContainsAny(id, "\r\n\x00") {
		return "", "model_unavailable"
	}
	baseURL := platformCatalogBaseURL(config)
	if g.accountHandler != nil {
		var err error
		baseURL, err = g.accountHandler.PlatformModelBaseURL()
		if err != nil {
			return "", "platform_catalog_unavailable"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	models, code := modelruntime.ReadPlatformModelCatalog(ctx, &http.Client{Timeout: 12 * time.Second}, baseURL)
	if code != "" {
		return "", "platform_catalog_unavailable"
	}
	available := false
	for _, model := range models {
		if model.ID == id && model.Capability == "text" && model.Available {
			available = true
			break
		}
	}
	if !available {
		return "", "model_unavailable"
	}
	for _, name := range sortedModelConfigNames(config) {
		model := config.ModelConfig[name]
		if model.ModelName == id && strings.TrimRight(model.BaseURL, "/") == baseURL && modelConfigUsesAccount(model) {
			return name, ""
		}
	}
	if config.ModelConfig == nil {
		config.ModelConfig = make(map[string]ModelConfig)
	}
	name := id
	for suffix := 2; ; suffix++ {
		if _, exists := config.ModelConfig[name]; !exists {
			break
		}
		name = fmt.Sprintf("%s-%d", id, suffix)
	}
	config.ModelConfig[name] = ModelConfig{Type: "openai", ModelName: id, BaseURL: baseURL, Temperature: 0.3, MaxTokens: 4096}
	return name, ""
}
