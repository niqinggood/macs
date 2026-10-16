package network

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ====================== Model Compare Tool ======================

type ModelCompareTool struct {
	modelConfigs map[string]ModelConfig
	logger       *log.Logger
}

type ModelConfig struct {
	APIKey      string
	ModelName   string
	BaseURL     string
	Temperature float64
	MaxTokens   int
}

func NewModelCompareTool(configs map[string]ModelConfig, logger *log.Logger) *ModelCompareTool {
	return &ModelCompareTool{modelConfigs: configs, logger: logger}
}

func (t *ModelCompareTool) Name() string { return "model_compare" }
func (t *ModelCompareTool) Description() string {
	return `Compare responses from two different AI models on the same prompt.
Shows both outputs side-by-side with timing. Useful for model selection and quality assessment.
Example: model_compare(model_a="deepseek_model", model_b="doubao_vision", prompt="解释量子计算")`
}
func (t *ModelCompareTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"model_a": map[string]interface{}{"type": "string", "description": "First model name (from model_cfg)"},
			"model_b": map[string]interface{}{"type": "string", "description": "Second model name (from model_cfg)"},
			"prompt":  map[string]interface{}{"type": "string", "description": "The prompt to send to both models"},
		},
		"required": []string{"model_a", "model_b", "prompt"},
	}
}
func (t *ModelCompareTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	modelA, _ := params["model_a"].(string)
	modelB, _ := params["model_b"].(string)
	prompt, _ := params["prompt"].(string)
	if modelA == "" || modelB == "" || prompt == "" {
		return "", fmt.Errorf("model_a, model_b, and prompt are required")
	}

	cfgA, okA := t.modelConfigs[modelA]
	cfgB, okB := t.modelConfigs[modelB]
	if !okA {
		return "", fmt.Errorf("model '%s' not found in model_cfg", modelA)
	}
	if !okB {
		return "", fmt.Errorf("model '%s' not found in model_cfg", modelB)
	}

	type modelResult struct {
		name     string
		response string
		duration time.Duration
		err      error
	}

	results := make(chan modelResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	callModel := func(name string, cfg ModelConfig) {
		defer wg.Done()
		start := time.Now()
		resp, err := callLLM(cfg, prompt)
		results <- modelResult{name: name, response: resp, duration: time.Since(start), err: err}
	}

	go callModel(modelA, cfgA)
	go callModel(modelB, cfgB)
	wg.Wait()
	close(results)

	var result strings.Builder
	result.WriteString("# Model Comparison\n\n")
	result.WriteString(fmt.Sprintf("**Prompt**: %s\n\n", truncateText(prompt, 200)))
	result.WriteString(fmt.Sprintf("| | %s | %s |\n", modelA, modelB))
	result.WriteString("|---|---|---|\n")

	rMap := make(map[string]modelResult)
	for r := range results {
		rMap[r.name] = r
	}

	ra, rb := rMap[modelA], rMap[modelB]
	result.WriteString(fmt.Sprintf("| Duration | %.1fs | %.1fs |\n", ra.duration.Seconds(), rb.duration.Seconds()))
	result.WriteString(fmt.Sprintf("| Length | %d chars | %d chars |\n", len(ra.response), len(rb.response)))

	if ra.err != nil {
		result.WriteString(fmt.Sprintf("| Error | %v | - |\n", ra.err))
	}
	if rb.err != nil {
		result.WriteString(fmt.Sprintf("| Error | - | %v |\n", rb.err))
	}

	result.WriteString(fmt.Sprintf("\n## %s\n\n%s\n\n---\n\n## %s\n\n%s\n",
		modelA, ra.response, modelB, rb.response))

	if t.logger != nil {
		t.logger.Printf("[Compare] %s (%.1fs) vs %s (%.1fs)", modelA, ra.duration.Seconds(), modelB, rb.duration.Seconds())
	}
	return result.String(), nil
}

// callLLM makes a simple chat completion call.
func callLLM(cfg ModelConfig, prompt string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"%s"}],"max_tokens":%d,"temperature":%.1f}`,
		cfg.ModelName, escapeJSON(prompt), cfg.MaxTokens, cfg.Temperature)

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "(empty response)", nil
	}
	return result.Choices[0].Message.Content, nil
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
