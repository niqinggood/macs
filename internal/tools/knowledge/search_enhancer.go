package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SearchModelConfig struct {
	APIKey  string
	Model   string
	BaseURL string
}

// SearchEnhancer owns optional HyDE query expansion and LLM result reranking.
// It is safe to reconfigure while searches are running.
type SearchEnhancer struct {
	mu     sync.RWMutex
	client *http.Client

	rerankMode  string
	rerankModel SearchModelConfig
	rerankReady bool
	hydeEnabled bool
	hydeModel   SearchModelConfig
}

func NewSearchEnhancer(client *http.Client) *SearchEnhancer {
	if client == nil {
		client = http.DefaultClient
	}
	return &SearchEnhancer{client: client}
}

func (enhancer *SearchEnhancer) ConfigureReranker(mode string, model SearchModelConfig) {
	if enhancer == nil {
		return
	}
	enhancer.mu.Lock()
	enhancer.rerankMode = mode
	enhancer.rerankModel = model
	enhancer.rerankReady = mode == "api" && model.APIKey != ""
	ready := enhancer.rerankReady
	enhancer.mu.Unlock()
	if ready {
		log.Printf("[Reranker] mode=api model=%s (%s)", model.Model, model.BaseURL)
	} else {
		log.Printf("[Reranker] mode=%s (reranking disabled)", mode)
	}
}

func (enhancer *SearchEnhancer) ConfigureHyDE(enabled bool, model SearchModelConfig) {
	if enhancer == nil {
		return
	}
	enhancer.mu.Lock()
	enhancer.hydeEnabled = enabled
	enhancer.hydeModel = model
	enhancer.mu.Unlock()
	if enabled && model.APIKey != "" {
		log.Printf("[HyDE] Enabled with model=%s", model.Model)
	}
}

// Expand returns the original query whenever HyDE is disabled or unavailable.
func (enhancer *SearchEnhancer) Expand(query string) string {
	if enhancer == nil {
		return query
	}
	enhancer.mu.RLock()
	enabled, model, client := enhancer.hydeEnabled, enhancer.hydeModel, enhancer.client
	enhancer.mu.RUnlock()
	if !enabled || model.APIKey == "" {
		return query
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hypothetical, err := requestSearchChat(ctx, client, model, []map[string]string{
		{"role": "user", "content": fmt.Sprintf("Write a short passage that answers the question: %s\n\nPassage:", query)},
	}, 200, 0.7)
	hypothetical = strings.TrimSpace(hypothetical)
	if err != nil || len(hypothetical) < 10 {
		return query
	}
	return query + " " + hypothetical
}

// Rerank returns the original ordering whenever the reranker is disabled or
// its request fails.
func (enhancer *SearchEnhancer) Rerank(query string, results []RAGResult, topK int) []RAGResult {
	if enhancer == nil || len(results) == 0 {
		return results
	}
	enhancer.mu.RLock()
	ready, model, client := enhancer.rerankReady, enhancer.rerankModel, enhancer.client
	enhancer.mu.RUnlock()
	if !ready {
		return results
	}
	candidates := results
	if len(candidates) > 20 {
		candidates = candidates[:20]
	}

	var prompt strings.Builder
	prompt.WriteString("Rank the following passages by relevance to the query. Return ONLY the indices (1-based) of the most relevant passages, separated by commas. For example: 3,7,1,12,5\n\n")
	fmt.Fprintf(&prompt, "Query: %s\n\n", query)
	for index, result := range candidates {
		text := result.Content
		if len([]rune(text)) > 300 {
			text = string([]rune(text)[:300])
		}
		fmt.Fprintf(&prompt, "[%d] %s\n\n", index+1, text)
	}
	prompt.WriteString("Top indices (comma-separated):")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content, err := requestSearchChat(ctx, client, model, []map[string]string{
		{"role": "system", "content": "You are a search relevance ranker. Respond ONLY with comma-separated numbers."},
		{"role": "user", "content": prompt.String()},
	}, 100, 0)
	if err != nil {
		log.Printf("[Reranker] LLM call failed: %v, using original order", err)
		return results
	}
	ranked, err := parseRankingIndices(content)
	if err != nil {
		log.Printf("[Reranker] LLM response invalid: %v, using original order", err)
		return results
	}

	reranked := make([]RAGResult, 0, len(candidates))
	seen := make(map[int]bool, len(ranked))
	for _, index := range ranked {
		if index >= 1 && index <= len(candidates) && !seen[index] {
			reranked = append(reranked, candidates[index-1])
			seen[index] = true
		}
	}
	for index, result := range candidates {
		if !seen[index+1] {
			reranked = append(reranked, result)
		}
	}
	if topK > 0 && len(reranked) > topK {
		reranked = reranked[:topK]
	}
	return reranked
}

func requestSearchChat(
	ctx context.Context,
	client *http.Client,
	model SearchModelConfig,
	messages []map[string]string,
	maxTokens int,
	temperature float64,
) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model": model.Model, "messages": messages, "max_tokens": maxTokens, "temperature": temperature,
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(model.BaseURL, "/") + "/v1/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+model.APIKey)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search LLM HTTP %d: %s", response.StatusCode, string(responseBody))
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return "", err
	}
	choices, _ := decoded["choices"].([]interface{})
	if len(choices) == 0 {
		return "", fmt.Errorf("search LLM returned no choices")
	}
	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	content, _ := message["content"].(string)
	if content == "" {
		return "", fmt.Errorf("search LLM returned empty content")
	}
	return content, nil
}

func parseRankingIndices(content string) ([]int, error) {
	indices := make([]int, 0)
	seen := make(map[int]bool)
	for _, part := range strings.Split(strings.TrimSpace(content), ",") {
		index, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && index > 0 && !seen[index] {
			indices = append(indices, index)
			seen[index] = true
		}
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("no valid indices in response: %s", strings.TrimSpace(content))
	}
	return indices, nil
}
