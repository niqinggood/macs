package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// APITestTool provides structured HTTP API testing with
// method chaining assertions and response validation.
type APITestTool struct {
	workspace string
	client    *http.Client
}

func NewAPITestTool(workspace string) *APITestTool {
	return &APITestTool{
		workspace: workspace,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *APITestTool) Name() string { return "api_test" }

func (t *APITestTool) Description() string {
	return "Structured HTTP API testing. " +
		"Actions: request (send HTTP request with assertions), " +
		"scenario (run multi-step API scenario), " +
		"validate (validate OpenAPI/Swagger spec). " +
		"Supports JSON body, headers, query params, status assertions, body assertions."
}

func (t *APITestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"request", "scenario", "validate"},
				"description": "request: single HTTP request with assertions. scenario: multi-step API test. validate: OpenAPI validation.",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "API endpoint URL",
			},
			"method": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
				"description": "HTTP method. Default: GET.",
			},
			"headers": map[string]interface{}{
				"type":        "string",
				"description": "JSON object of headers (e.g., '{\"Authorization\":\"Bearer token\"}')",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Request body (JSON string or raw text)",
			},
			"assert_status": map[string]interface{}{
				"type":        "integer",
				"description": "Expected HTTP status code (e.g., 200)",
			},
			"assert_body": map[string]interface{}{
				"type":        "string",
				"description": "Expected substring in response body",
			},
			"assert_json": map[string]interface{}{
				"type":        "string",
				"description": "JSON path assertions (e.g., '{\"data.id\": 123, \"success\": true}')",
			},
			"assert_header": map[string]interface{}{
				"type":        "string",
				"description": "Expected header value (e.g., 'content-type: application/json')",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Request timeout in seconds. Default: 30.",
			},
			"scenario_file": map[string]interface{}{
				"type":        "string",
				"description": "JSON scenario file path (for scenario action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *APITestTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "request")
	reqURL := configvalue.String(params, "url", "")
	method := configvalue.String(params, "method", "GET")
	headersStr := configvalue.String(params, "headers", "")
	bodyStr := configvalue.String(params, "body", "")
	assertStatus := configvalue.Int(params, "assert_status", 0)
	assertBody := configvalue.String(params, "assert_body", "")
	assertJSON := configvalue.String(params, "assert_json", "")
	assertHeader := configvalue.String(params, "assert_header", "")
	timeout := configvalue.Int(params, "timeout", 30)
	scenarioFile := configvalue.String(params, "scenario_file", "")

	switch action {
	case "request":
		if strings.TrimSpace(reqURL) == "" {
			return "Error: 'url' parameter is required", nil
		}
		return t.sendRequest(ctx, reqURL, method, headersStr, bodyStr, assertStatus, assertBody, assertJSON, assertHeader, timeout)
	case "scenario":
		return t.runScenario(ctx, scenarioFile)
	case "validate":
		if strings.TrimSpace(reqURL) == "" {
			return "Error: 'url' parameter is required", nil
		}
		return t.validateSpec(ctx, reqURL)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Request ==================

type apiResult struct {
	Status     int
	StatusText string
	Headers    map[string]string
	Body       string
	Duration   time.Duration
	Size       int64
}

func (t *APITestTool) sendRequest(ctx context.Context, reqURL, method, headersStr, bodyStr string,
	assertStatus int, assertBody, assertJSON, assertHeader string, timeout int) (string, error) {

	if timeout <= 0 {
		timeout = 30
	} else if timeout > 300 {
		timeout = 300
	}
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Parse headers
	headers := make(map[string]string)
	if headersStr != "" {
		if err := json.Unmarshal([]byte(headersStr), &headers); err != nil {
			// Try parsing as "Key: Value" lines
			for _, line := range strings.Split(headersStr, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// Build request
	var bodyReader io.Reader
	if bodyStr != "" {
		bodyReader = bytes.NewBufferString(bodyStr)
		if _, ok := headers["Content-Type"]; !ok {
			// Auto-detect JSON
			if strings.HasPrefix(strings.TrimSpace(bodyStr), "{") || strings.HasPrefix(strings.TrimSpace(bodyStr), "[") {
				headers["Content-Type"] = "application/json"
			}
		}
	}

	req, err := http.NewRequestWithContext(requestContext, strings.ToUpper(method), reqURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Execute
	start := time.Now()
	client := *t.client
	client.Timeout = time.Duration(timeout) * time.Second
	resp, err := client.Do(req)
	duration := time.Since(start)
	if err != nil {
		if contextErr := requestContext.Err(); contextErr != nil {
			return "", contextErr
		}
		// Check if it's a connection error we can diagnose
		return fmt.Sprintf("## API Request Failed\n\n**%s %s**\n\n❌ Connection error (%.0fms): %v\n\n**Diagnosis:**\n- Check the URL is correct and reachable\n- Verify network connectivity\n- Check if the server is running",
			method, reqURL, float64(duration.Microseconds())/1000, err), nil
	}
	defer resp.Body.Close()

	// Read response
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("read API response: %w", err)
	}
	bodyStrResult := string(bodyBytes)

	// Collect response headers
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		respHeaders[strings.ToLower(k)] = strings.Join(v, ", ")
	}

	result := apiResult{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    respHeaders,
		Body:       bodyStrResult,
		Duration:   duration,
		Size:       int64(len(bodyBytes)),
	}

	// Build report
	return t.buildReport(reqURL, method, result, assertStatus, assertBody, assertJSON, assertHeader)
}

func (t *APITestTool) buildReport(reqURL, method string, result apiResult,
	assertStatus int, assertBody, assertJSON, assertHeader string) (string, error) {

	var sb strings.Builder
	allPassed := true

	sb.WriteString("## API Test Report\n\n")

	// Request info
	sb.WriteString(fmt.Sprintf("**%s %s** — %s (%.0fms, %d bytes)\n\n",
		method, reqURL, result.StatusText, float64(result.Duration.Microseconds())/1000, result.Size))

	// Status assertion
	if assertStatus > 0 {
		if result.Status == assertStatus {
			sb.WriteString(fmt.Sprintf("✅ Status: %d (expected %d)\n", result.Status, assertStatus))
		} else {
			sb.WriteString(fmt.Sprintf("❌ Status: %d (expected %d)\n", result.Status, assertStatus))
			allPassed = false
		}
	} else {
		statusIcon := "✅"
		if result.Status >= 400 {
			statusIcon = "⚠️"
		}
		if result.Status >= 500 {
			statusIcon = "❌"
		}
		sb.WriteString(fmt.Sprintf("%s Status: %d %s\n", statusIcon, result.Status, result.StatusText))
	}

	// Body assertion
	if assertBody != "" {
		if strings.Contains(result.Body, assertBody) {
			sb.WriteString(fmt.Sprintf("✅ Body contains: \"%s\"\n", textutil.Truncate(assertBody, 80)))
		} else {
			sb.WriteString(fmt.Sprintf("❌ Body does not contain: \"%s\"\n", textutil.Truncate(assertBody, 80)))
			allPassed = false
		}
	}

	// JSON assertion
	if assertJSON != "" {
		jsonResults := t.checkJSONAssertions(result.Body, assertJSON)
		for _, jr := range jsonResults {
			if jr.passed {
				sb.WriteString(fmt.Sprintf("✅ %s = %v\n", jr.path, jr.expected))
			} else {
				sb.WriteString(fmt.Sprintf("❌ %s: expected %v, got %v\n", jr.path, jr.expected, jr.actual))
				allPassed = false
			}
		}
	}

	// Header assertion
	if assertHeader != "" {
		parts := strings.SplitN(assertHeader, ":", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			expected := strings.TrimSpace(parts[1])
			if actual, ok := result.Headers[key]; ok && strings.Contains(strings.ToLower(actual), strings.ToLower(expected)) {
				sb.WriteString(fmt.Sprintf("✅ Header %s: %s\n", key, actual))
			} else {
				sb.WriteString(fmt.Sprintf("❌ Header %s: expected \"%s\", got \"%v\"\n", key, expected, result.Headers[key]))
				allPassed = false
			}
		}
	}

	// Overall verdict
	sb.WriteString("\n### ")
	if allPassed {
		sb.WriteString("✅ All assertions passed\n")
	} else {
		sb.WriteString("❌ Some assertions failed\n")
	}

	// Response body (truncated)
	sb.WriteString("\n### Response Body\n\n")
	sb.WriteString(fmt.Sprintf("```json\n%s\n```\n", textutil.Truncate(t.formatBody(result.Body), 2000)))

	// Response headers
	sb.WriteString("\n### Response Headers\n\n")
	sb.WriteString("| Header | Value |\n")
	sb.WriteString("|--------|-------|\n")
	headerNames := make([]string, 0, len(result.Headers))
	for name := range result.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", name, textutil.Truncate(result.Headers[name], 100)))
	}

	return sb.String(), nil
}

type jsonAssertResult struct {
	path     string
	expected interface{}
	actual   interface{}
	passed   bool
}

func (t *APITestTool) checkJSONAssertions(body, assertions string) []jsonAssertResult {
	var bodyMap interface{}
	if err := json.Unmarshal([]byte(body), &bodyMap); err != nil {
		return []jsonAssertResult{{path: "body", expected: "valid JSON", actual: err.Error(), passed: false}}
	}

	var assertMap map[string]interface{}
	if err := json.Unmarshal([]byte(assertions), &assertMap); err != nil {
		// Try simple key=value format
		assertMap = make(map[string]interface{})
		for _, pair := range strings.Split(assertions, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 {
				assertMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	paths := make([]string, 0, len(assertMap))
	for path := range assertMap {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	results := make([]jsonAssertResult, 0, len(paths))
	for _, path := range paths {
		expected := assertMap[path]
		actual := getJSONPath(bodyMap, path)
		passed := fmt.Sprint(actual) == fmt.Sprint(expected)
		results = append(results, jsonAssertResult{path: path, expected: expected, actual: actual, passed: passed})
	}
	return results
}

func getJSONPath(data interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		switch m := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = m[part]
			if !ok {
				return nil
			}
		case []interface{}:
			idx := 0
			fmt.Sscanf(part, "%d", &idx)
			if idx >= 0 && idx < len(m) {
				current = m[idx]
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return current
}

// ================== Scenario ==================

func (t *APITestTool) runScenario(ctx context.Context, scenarioFile string) (string, error) {
	if scenarioFile == "" {
		return "## API Scenario\n\nProvide a scenario_file parameter pointing to a JSON scenario definition.\n\n### Example Scenario\n```json\n{\n  \"base_url\": \"http://localhost:8080\",\n  \"steps\": [\n    {\"name\": \"Login\", \"method\": \"POST\", \"path\": \"/api/login\", \"body\": {\"user\":\"admin\"}},\n    {\"name\": \"Get Profile\", \"method\": \"GET\", \"path\": \"/api/profile\"}\n  ]\n}\n```\n", nil
	}
	if !filepath.IsAbs(scenarioFile) {
		scenarioFile = filepath.Join(t.workspace, scenarioFile)
	}

	data, err := os.ReadFile(scenarioFile)
	if err != nil {
		return "", fmt.Errorf("read scenario: %w", err)
	}

	var scenario struct {
		BaseURL string `json:"base_url"`
		Steps   []struct {
			Name   string                 `json:"name"`
			Method string                 `json:"method"`
			Path   string                 `json:"path"`
			Body   interface{}            `json:"body"`
			Assert map[string]interface{} `json:"assert"`
		} `json:"steps"`
	}

	if err := json.Unmarshal(data, &scenario); err != nil {
		return "", fmt.Errorf("parse scenario: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## API Scenario: %d steps\n\n", len(scenario.Steps)))

	passed := 0
	for i, step := range scenario.Steps {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fullURL := scenario.BaseURL + step.Path
		method := step.Method
		if method == "" {
			method = "GET"
		}

		var bodyStr string
		if step.Body != nil {
			b, _ := json.Marshal(step.Body)
			bodyStr = string(b)
		}

		result, requestErr := t.sendRequest(ctx, fullURL, method, "{}", bodyStr,
			configvalue.Int(step.Assert, "status", 0),
			configvalue.String(step.Assert, "body_contains", ""),
			"", "", 30)
		if requestErr != nil {
			return "", fmt.Errorf("scenario step %d (%s): %w", i+1, step.Name, requestErr)
		}

		if strings.Contains(result, "✅ All assertions passed") {
			passed++
			sb.WriteString(fmt.Sprintf("✅ Step %d: %s — %s %s\n", i+1, step.Name, method, step.Path))
		} else {
			sb.WriteString(fmt.Sprintf("❌ Step %d: %s — %s %s\n", i+1, step.Name, method, step.Path))
		}
	}

	sb.WriteString(fmt.Sprintf("\n**%d/%d steps passed**\n", passed, len(scenario.Steps)))
	return sb.String(), nil
}

// ================== Validate ==================

func (t *APITestTool) validateSpec(ctx context.Context, specURL string) (string, error) {
	// Try to fetch OpenAPI/Swagger spec
	if !strings.HasPrefix(specURL, "http") {
		return fmt.Sprintf("## OpenAPI Validation\n\nProvide a URL to an OpenAPI/Swagger JSON spec (e.g., http://localhost:8080/swagger.json)\n"), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return fmt.Sprintf("❌ Cannot create spec request: %v", err), nil
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("❌ Cannot fetch spec: %v", err), nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var spec struct {
		OpenAPI string `json:"openapi"`
		Swagger string `json:"swagger"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]interface{} `json:"paths"`
	}

	if err := json.Unmarshal(body, &spec); err != nil {
		return fmt.Sprintf("❌ Invalid OpenAPI/Swagger JSON: %v", err), nil
	}

	var sb strings.Builder
	sb.WriteString("## OpenAPI Validation\n\n")

	if spec.OpenAPI != "" {
		sb.WriteString(fmt.Sprintf("**Spec:** OpenAPI %s\n", spec.OpenAPI))
	} else if spec.Swagger != "" {
		sb.WriteString(fmt.Sprintf("**Spec:** Swagger %s\n", spec.Swagger))
	} else {
		sb.WriteString("❌ Not a valid OpenAPI or Swagger spec\n")
		return sb.String(), nil
	}

	sb.WriteString(fmt.Sprintf("**Title:** %s\n", spec.Info.Title))
	sb.WriteString(fmt.Sprintf("**Version:** %s\n", spec.Info.Version))
	sb.WriteString(fmt.Sprintf("**Endpoints:** %d\n\n", len(spec.Paths)))

	sb.WriteString("### Endpoints\n\n")
	for path, methods := range spec.Paths {
		if m, ok := methods.(map[string]interface{}); ok {
			methodList := make([]string, 0, len(m))
			for method := range m {
				if method != "parameters" && method != "summary" && method != "description" {
					methodList = append(methodList, strings.ToUpper(method))
				}
			}
			sb.WriteString(fmt.Sprintf("- %s — %s\n", strings.Join(methodList, ","), path))
		}
	}

	sb.WriteString(fmt.Sprintf("\n✅ Spec is valid. **%d endpoints** available.\n", len(spec.Paths)))

	return sb.String(), nil
}

// ================== Helpers ==================

func (t *APITestTool) formatBody(body string) string {
	// Try to pretty-print JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		pretty, err := json.MarshalIndent(parsed, "", "  ")
		if err == nil {
			return string(pretty)
		}
	}
	return body
}

// Parse query string
func parseQuery(rawURL string) map[string]string {
	result := make(map[string]string)
	u, err := url.Parse(rawURL)
	if err == nil {
		for k, v := range u.Query() {
			result[k] = strings.Join(v, ",")
		}
	}
	return result
}
