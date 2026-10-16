package modelruntime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ChatEndpoint struct {
	AccountAuth bool
	APIKey      string
	ModelName   string
	BaseURL     string
}

// ProbeChatCompletion performs the minimal OpenAI-compatible health request.
func ProbeChatCompletion(ctx context.Context, client *http.Client, endpoint ChatEndpoint) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if IsPlatformModelURL(endpoint.BaseURL) {
		models, code := ReadChatModelCatalog(ctx, client, endpoint)
		if code != "" {
			return fmt.Errorf("platform catalog: %s", code)
		}
		for _, model := range models {
			if endpoint.ModelName == "platform:default" || model.ID == endpoint.ModelName {
				return nil
			}
		}
		return fmt.Errorf("platform model unavailable")
	}
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1,"temperature":0}`, endpoint.ModelName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint.BaseURL, "/")+"/chat/completions", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(endpoint.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// StartHealthCheck runs check periodically until the returned channel closes.
func StartHealthCheck(interval time.Duration, check func()) chan struct{} {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if check != nil {
					check()
				}
			case <-stop:
				return
			}
		}
	}()
	return stop
}
