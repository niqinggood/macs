package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const jevEndpoint = "https://api.typesafe.ai/v1/systemone"

type Jev struct {
	apiKey string
	model  string
	client *http.Client
}

// NewJev uses a fixed provider endpoint. Tool parameters cannot redirect state
// or credentials to another destination. No retries extend the fast path.
func NewJev(apiKey, model string, timeout time.Duration) (*Jev, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return nil, errors.New("Jev API key is missing or invalid")
	}
	if model = strings.TrimSpace(model); model == "" {
		model = "jev-latest"
	}
	if timeout <= 0 || timeout > 30*time.Second {
		return nil, errors.New("decision timeout must be positive and at most 30 seconds")
	}
	return &Jev{apiKey: apiKey, model: model, client: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (j *Jev) Evaluate(ctx context.Context, request Request) (Response, error) {
	var response Response
	if err := request.Validate(); err != nil {
		return response, err
	}
	payload := struct {
		Request
		Model string `json:"model"`
	}{request, j.model}
	body, err := json.Marshal(payload)
	if err != nil {
		return response, errors.New("cannot encode decision request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jevEndpoint, bytes.NewReader(body))
	if err != nil {
		return response, errors.New("cannot create decision request")
	}
	req.Header.Set("Authorization", "Bearer "+j.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := j.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return response, ctx.Err()
		}
		return response, errors.New("decision provider unavailable or timed out")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Never reflect the provider body: it can echo private input or keys.
		return response, fmt.Errorf("decision provider returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return response, errors.New("decision response is unreadable or too large")
	}
	if json.Unmarshal(data, &response) != nil {
		return Response{}, errors.New("decision response is not valid JSON")
	}
	if err := ValidateResponse(request, response); err != nil {
		return Response{}, err
	}
	return response, nil
}
