package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authcore "mac/internal/auth"
	"mac/internal/modelruntime"
)

const defaultPlatformModelBaseURL = "https://www.sicurrent.com/api/macs/v1"

func platformCatalogBaseURL(config *Config) string {
	accountConfig := authcore.AccountConfig{}
	if config != nil {
		accountConfig.ServiceURL = config.Gateway.AccountServiceURL
	}
	if baseURL, err := authcore.PlatformModelBaseURL(accountConfig); err == nil {
		return baseURL
	}
	return defaultPlatformModelBaseURL
}

func (g *GatewayService) handlePlatformModelCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(status int, code string, models []modelruntime.ChatCatalogModel) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": code == "", "code": code, "models": models})
	}
	if !personalMemoryLocalRequest(r) {
		reply(http.StatusForbidden, "local_only", nil)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		reply(http.StatusMethodNotAllowed, "method_not_allowed", nil)
		return
	}
	// The client cannot choose an upstream URL or forward any account credential.
	if r.URL.RawQuery != "" {
		reply(http.StatusBadRequest, "invalid_request", nil)
		return
	}
	var baseURL string
	if g.accountHandler != nil {
		var err error
		baseURL, err = g.accountHandler.PlatformModelBaseURL()
		if err != nil {
			reply(http.StatusInternalServerError, "invalid_config", nil)
			return
		}
	} else {
		config, err := LoadConfig(g.configpath)
		if err != nil {
			reply(http.StatusInternalServerError, "invalid_config", nil)
			return
		}
		baseURL = platformCatalogBaseURL(config)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	models, code := modelruntime.ReadPlatformModelCatalog(ctx, &http.Client{Timeout: 12 * time.Second}, baseURL)
	if code != "" {
		reply(http.StatusBadGateway, "platform_catalog_unavailable", nil)
		return
	}
	visible := make([]modelruntime.ChatCatalogModel, 0, len(models))
	for _, model := range models {
		if !strings.HasPrefix(model.ID, "platform:") || model.Capability != "text" && model.Capability != "image" && model.Capability != "music" && model.Capability != "video" {
			continue
		}
		model.Source = "platform"
		// The platform creation API already accepts configured video models.
		// Older catalogs only marked the former image/music modal as direct.
		if model.Capability == "video" && model.Available {
			model.DirectGeneration = true
		}
		visible = append(visible, model)
	}
	reply(http.StatusOK, "", visible)
}
