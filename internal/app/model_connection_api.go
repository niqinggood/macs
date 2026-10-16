package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"mac/internal/modelruntime"
)

func (g *GatewayService) resolveSettingsModelKey(name string, model ModelConfig) (ModelConfig, string) {
	if model.APIKey != "[REDACTED]" {
		return model, ""
	}
	config, err := LoadConfig(g.configpath)
	if err != nil {
		return model, "invalid_config"
	}
	saved, exists := config.ModelConfig[name]
	if !exists || strings.TrimRight(saved.BaseURL, "/") != strings.TrimRight(model.BaseURL, "/") || saved.Type != model.Type {
		return model, "saved_key_address_changed"
	}
	model.APIKey = saved.APIKey
	return model, ""
}

// Catalog reads are local control operations and never trigger paid generation.
func (g *GatewayService) handleSettingsModelCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(status int, code string, models interface{}) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": code == "", "code": code, "models": models})
	}
	if !personalMemoryLocalRequest(r) {
		reply(http.StatusForbidden, "local_only", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		reply(http.StatusMethodNotAllowed, "invalid_config", nil)
		return
	}
	var input struct {
		Name  string      `json:"name"`
		Model ModelConfig `json:"model"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input) != nil {
		reply(http.StatusBadRequest, "invalid_config", nil)
		return
	}
	model, code := g.resolveSettingsModelKey(input.Name, input.Model)
	if code != "" {
		reply(http.StatusBadRequest, code, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var models []modelruntime.ChatCatalogModel
	if modelConfigUsesAccount(model) {
		models, code = modelruntime.ReadPlatformModelCatalog(ctx, &http.Client{Timeout: 15 * time.Second}, model.BaseURL)
		textModels := make([]modelruntime.ChatCatalogModel, 0, len(models))
		for _, item := range models {
			if item.Capability == "text" {
				textModels = append(textModels, item)
			}
		}
		models = textModels
	} else {
		models, code = modelruntime.ReadChatModelCatalog(ctx, &http.Client{Timeout: 15 * time.Second}, modelruntime.ChatEndpoint{BaseURL: model.BaseURL, APIKey: model.APIKey})
	}
	status := http.StatusOK
	if code != "" {
		status = http.StatusBadGateway
	}
	reply(status, code, models)
}

// Connection tests share the local personal-control boundary used by settings
// adjacent management surfaces. They cannot be used as a remote network proxy.
func (g *GatewayService) handleSettingsModelTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": code == "", "code": code})
	}
	if !personalMemoryLocalRequest(r) {
		reply(http.StatusForbidden, "local_only")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		reply(http.StatusMethodNotAllowed, "invalid_config")
		return
	}
	var input struct {
		Name  string      `json:"name"`
		Model ModelConfig `json:"model"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input) != nil {
		reply(http.StatusBadRequest, "invalid_config")
		return
	}
	model, keyCode := g.resolveSettingsModelKey(input.Name, input.Model)
	if keyCode != "" {
		reply(http.StatusBadRequest, keyCode)
		return
	}
	if !isModelConfigAvailable(model) {
		reply(http.StatusBadRequest, "invalid_config")
		return
	}
	if strings.EqualFold(model.Type, "anthropic") {
		reply(http.StatusBadRequest, "unsupported_provider")
		return
	}
	baseURL := strings.TrimRight(model.BaseURL, "/")
	if strings.EqualFold(model.Type, "ollama") && !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if modelConfigUsesAccount(model) {
		if g.accountHandler == nil {
			reply(http.StatusUnauthorized, "login_required")
			return
		}
		account, err := g.accountHandler.CapturePlatformAccount(r)
		if err != nil {
			reply(http.StatusUnauthorized, "login_required")
			return
		}
		ctx = account.Context(ctx)
	}
	code := modelruntime.TestChatConnection(ctx, &http.Client{Timeout: 20 * time.Second}, modelruntime.ChatEndpoint{
		APIKey: model.APIKey, ModelName: model.ModelName, BaseURL: baseURL,
		AccountAuth: modelConfigUsesAccount(model),
	})
	status := http.StatusOK
	if code != "" {
		status = http.StatusBadGateway
	}
	reply(status, code)
}
