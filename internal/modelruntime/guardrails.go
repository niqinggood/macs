package modelruntime

import (
	"fmt"
	"mac/internal/textutil"
	"regexp"
	"strconv"
	"strings"
)

// APIErrorCategory defines error types with recovery strategies (借鉴 Hermes FailoverReason).
type APIErrorCategory struct {
	Code        string // machine-readable code
	Retryable   bool   // can this error be retried?
	Recovery    string // recovery strategy: "retry" | "backoff" | "rotate_cred" | "failover_model" | "compress" | "abort" | "shrink_image" | "strip_format"
	Hint        string // user-facing hint in Chinese
	BackoffSecs int    // suggested backoff seconds (0 = default)
}

var httpStatusPatterns = []*regexp.Regexp{
	// Standard HTTP status lines and common wrappers such as "HTTP/1.1 429"
	// or "HTTP error 500". A digit boundary prevents matching provider codes
	// whose first three digits happen to look like an HTTP status.
	regexp.MustCompile(`(?i)\bhttp(?:/[0-9]+(?:\.[0-9]+)?)?(?:\s+(?:error|status)(?:\s+code)?)?\s*[:=]?\s*["']?([1-5][0-9]{2})["']?(?:\b|$)`),
	// Structured/log fields: status, status_code, status-code, response status.
	// Deliberately does not accept a bare "code" field because providers often
	// use longer business error codes there.
	regexp.MustCompile(`(?i)\b(?:response\s+)?status(?:[_ -]?code)?["']?\s*(?:[:=]\s*|\s+)["']?([1-5][0-9]{2})["']?(?:\b|$)`),
	// Chinese status labels used by the API adapters.
	regexp.MustCompile(`(?:状态码|状态代码)\s*[:：=]?\s*["']?([1-5][0-9]{2})["']?(?:[^0-9]|$)`),
	// A leading, standalone three-digit value is the conventional compact HTTP
	// status representation (for example, "403 Forbidden").
	regexp.MustCompile(`(?i)^\s*["']?([1-5][0-9]{2})["']?(?:\s|:|-|$)`),
}

// extractHTTPStatus returns only an explicitly represented HTTP status. It
// intentionally ignores unlabelled numbers and generic provider "code" fields.
func extractHTTPStatus(errMsg string) (int, bool) {
	for _, pattern := range httpStatusPatterns {
		match := pattern.FindStringSubmatch(errMsg)
		if len(match) != 2 {
			continue
		}
		status, err := strconv.Atoi(match[1])
		if err == nil {
			return status, true
		}
	}
	return 0, false
}

// classifyAPIErrorEnhanced returns a structured error classification (借鉴 Hermes error_classifier).
// Covers 20+ error categories with specific recovery strategies.
func ClassifyAPIErrorEnhanced(errMsg string) APIErrorCategory {
	lower := strings.ToLower(errMsg)
	httpStatus, hasHTTPStatus := extractHTTPStatus(errMsg)

	switch {
	// ── Explicit billing/auth statuses ──
	// Transport status is authoritative; numbers in an unlabelled provider code
	// never enter these branches.
	case hasHTTPStatus && httpStatus == 402:
		return APIErrorCategory{Code: "billing", Retryable: false, Recovery: "rotate_cred",
			Hint: "账户需要付费，请充值或更换 API Key", BackoffSecs: 0}
	case hasHTTPStatus && httpStatus == 401:
		return APIErrorCategory{Code: "auth", Retryable: false, Recovery: "rotate_cred",
			Hint: "API Key 验证失败，请检查模型配置中的 API Key 是否正确", BackoffSecs: 0}
	case hasHTTPStatus && httpStatus == 403:
		return APIErrorCategory{Code: "auth_forbidden", Retryable: false, Recovery: "rotate_cred",
			Hint: "API 访问被拒绝(403)，请确认账号权限", BackoffSecs: 0}

	// ── Billing / Quota semantic fallbacks ──
	// These provider-neutral meanings precede loose auth wording when no explicit
	// billing/auth transport status was present.
	case strings.Contains(lower, "payment required"):
		return APIErrorCategory{Code: "billing", Retryable: false, Recovery: "rotate_cred",
			Hint: "账户需要付费，请充值或更换 API Key", BackoffSecs: 0}
	case strings.Contains(lower, "balance") || strings.Contains(lower, "insufficient") ||
		strings.Contains(lower, "quota exceeded") || strings.Contains(lower, "credit"):
		return APIErrorCategory{Code: "quota_exhausted", Retryable: false, Recovery: "rotate_cred",
			Hint: "账户余额不足或配额已用完，建议充值或更换 API Key", BackoffSecs: 0}

	// ── Auth semantic fallbacks (terminal if refresh fails) ──
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "invalid token"):
		return APIErrorCategory{Code: "auth", Retryable: false, Recovery: "rotate_cred",
			Hint: "API Key 验证失败，请检查模型配置中的 API Key 是否正确", BackoffSecs: 0}
	case strings.Contains(lower, "forbidden"):
		return APIErrorCategory{Code: "auth_forbidden", Retryable: false, Recovery: "rotate_cred",
			Hint: "API 访问被拒绝(403)，请确认账号权限", BackoffSecs: 0}

	// ── Rate limiting ──
	case hasHTTPStatus && httpStatus == 429 || strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit exceeded"):
		return APIErrorCategory{Code: "rate_limit", Retryable: true, Recovery: "backoff",
			Hint: "请求频率超限，自动退避重试中...", BackoffSecs: 5}
	case strings.Contains(lower, "concurrent") && strings.Contains(lower, "limit"):
		return APIErrorCategory{Code: "concurrency_limit", Retryable: true, Recovery: "backoff",
			Hint: "并发请求超限，排队等待中...", BackoffSecs: 3}

	// ── Context overflow (recoverable via compression) ──
	case strings.Contains(lower, "context length") || strings.Contains(lower, "too long") ||
		strings.Contains(lower, "maximum context") || strings.Contains(lower, "reduce") && strings.Contains(lower, "length") ||
		strings.Contains(lower, "1115") || strings.Contains(lower, "11921") ||
		strings.Contains(lower, "prompt must be less than") ||
		strings.Contains(lower, "this model's maximum context length"):
		return APIErrorCategory{Code: "context_overflow", Retryable: true, Recovery: "compress",
			Hint: "上下文过长，正在自动压缩...", BackoffSecs: 0}

	// ── Image too large (recoverable via resize) ──
	case strings.Contains(lower, "image") && (strings.Contains(lower, "too large") ||
		strings.Contains(lower, "size exceeds") || strings.Contains(lower, "exceeds")) ||
		strings.Contains(lower, "image_file_size_limit") || strings.Contains(lower, "image dimension"):
		return APIErrorCategory{Code: "image_too_large", Retryable: true, Recovery: "shrink_image",
			Hint: "图片过大，正在压缩...", BackoffSecs: 0}

	// ── Payload too large ──
	case hasHTTPStatus && httpStatus == 413 || strings.Contains(lower, "payload too large") ||
		strings.Contains(lower, "request too large") || strings.Contains(lower, "entity too large"):
		return APIErrorCategory{Code: "payload_too_large", Retryable: true, Recovery: "compress",
			Hint: "请求体过大，正在压缩上下文...", BackoffSecs: 0}

	// ── Server overload ──
	case hasHTTPStatus && (httpStatus == 503 || httpStatus == 529) ||
		strings.Contains(lower, "overloaded") || strings.Contains(lower, "service unavailable") ||
		strings.Contains(lower, "temporarily unavailable"):
		return APIErrorCategory{Code: "overloaded", Retryable: true, Recovery: "failover_model",
			Hint: "模型服务过载，正在切换到备用模型...", BackoffSecs: 3}

	// ── Server errors ──
	case hasHTTPStatus && (httpStatus == 500 || httpStatus == 502) ||
		strings.Contains(lower, "server error") || strings.Contains(lower, "internal server"):
		return APIErrorCategory{Code: "server_error", Retryable: true, Recovery: "retry",
			Hint: "服务端暂时异常，正在重试...", BackoffSecs: 2}

	// ── Timeout / Network ──
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline exceeded"):
		return APIErrorCategory{Code: "timeout", Retryable: true, Recovery: "retry",
			Hint: "请求超时，正在重试...", BackoffSecs: 2}
	case strings.Contains(lower, "eof") || strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") || strings.Contains(lower, "network"):
		return APIErrorCategory{Code: "network", Retryable: true, Recovery: "retry",
			Hint: "网络连接异常，正在重试...", BackoffSecs: 3}

	// ── Model not found ──
	case hasHTTPStatus && httpStatus == 404 && (strings.Contains(lower, "model") ||
		strings.Contains(lower, "not found")) ||
		strings.Contains(lower, "model_not_found"):
		return APIErrorCategory{Code: "model_not_found", Retryable: false, Recovery: "failover_model",
			Hint: "模型不存在或已下线，正在切换到可用模型...", BackoffSecs: 0}

	// ── Content policy / Safety filter ──
	case strings.Contains(lower, "content policy") || strings.Contains(lower, "safety") ||
		strings.Contains(lower, "content_filter") || strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "blocked") && strings.Contains(lower, "content"):
		return APIErrorCategory{Code: "content_blocked", Retryable: false, Recovery: "abort",
			Hint: "内容被安全策略拦截，请修改输入后重试", BackoffSecs: 0}

	// ── Provider thinking/reasoning protocol ──
	// These checks must precede the generic HTTP 400 branch. Treating a missing
	// replay field as a tool_calls syntax error causes destructive history
	// cleanup and cannot repair the request.
	case IsReasoningContextProtocolError(lower):
		return APIErrorCategory{Code: "reasoning_context_missing", Retryable: false, Recovery: "abort",
			Hint: "模型思考上下文协议不完整：上一轮 reasoning_content 未被完整回传，已停止重复请求", BackoffSecs: 0}
	case strings.Contains(lower, "anthropic") && strings.Contains(lower, "thinking"):
		return APIErrorCategory{Code: "thinking_signature", Retryable: true, Recovery: "strip_format",
			Hint: "Thinking签名校验失败，正在修复...", BackoffSecs: 0}

	// ── Format errors ──
	case hasHTTPStatus && httpStatus == 400 || strings.Contains(lower, "bad request") ||
		strings.Contains(lower, "invalid_request") || strings.Contains(lower, "invalid parameter"):
		return APIErrorCategory{Code: "format_error", Retryable: false, Recovery: "strip_format",
			Hint: "请求格式错误，已停止无效重试", BackoffSecs: 0}

	default:
		return APIErrorCategory{Code: "unknown", Retryable: true, Recovery: "retry",
			Hint: fmt.Sprintf("API 调用失败: %s", textutil.Truncate(errMsg, 200)), BackoffSecs: 1}
	}
}

// isReasoningContextProtocolError identifies provider rejections caused by a
// missing assistant reasoning replay. It deliberately keys off the protocol
// field and requirement language rather than a provider or model name.
func IsReasoningContextProtocolError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	if !strings.Contains(lower, "reasoning_content") {
		return false
	}
	return strings.Contains(lower, "passed back") ||
		strings.Contains(lower, "pass back") ||
		strings.Contains(lower, "must be passed") ||
		strings.Contains(lower, "thinking mode") ||
		strings.Contains(lower, "missing") ||
		strings.Contains(lower, "required")
}

// isToolCallProtocolError is intentionally narrower than "any HTTP 400".
// Only errors that actually name the assistant/tool message protocol are safe
// to route through the bounded tool-call history repair path.
func IsToolCallProtocolError(errMsg string) bool {
	if IsReasoningContextProtocolError(errMsg) {
		return false
	}
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "tool_calls") ||
		strings.Contains(lower, "tool_call_id") ||
		strings.Contains(lower, "tool call id") ||
		strings.Contains(lower, "tool message") && strings.Contains(lower, "assistant")
}

// formatAPIErrorHint returns a user-friendly hint based on error category.
func FormatAPIErrorHint(errMsg string) string {
	return ClassifyAPIErrorEnhanced(errMsg).Hint
}

// toolResultPreview extracts a compact summary from tool output for context compression.
// Replaces thousands of chars of raw output with a structured one-liner.
func ToolResultPreview(toolName, content string) string {
	lines := strings.Count(content, "\n") + 1
	head := content
	if len(head) > 200 {
		head = head[:200]
	}
	head = strings.ReplaceAll(head, "\n", " ")
	switch toolName {
	case "cli":
		exitInfo := ""
		if strings.Contains(content, "exit status") {
			idx := strings.Index(content, "exit status")
			if idx > 0 {
				exitInfo = " " + strings.TrimSpace(content[idx:min(len(content), idx+15)])
			}
		}
		headTrim := ""
		if len(head) > 80 {
			headTrim = head[:80] + "..."
		} else {
			headTrim = head
		}
		return fmt.Sprintf("[cli] %d lines%s | %s", lines, exitInfo, headTrim)
	case "read_file":
		pathPart := head
		if len(pathPart) > 60 {
			pathPart = pathPart[:60] + "..."
		}
		return fmt.Sprintf("[read_file] %d chars, %d lines | %s", len(content), lines, pathPart)
	case "agent_reach", "web_fetch":
		return fmt.Sprintf("[%s] %d chars, ~%d lines", toolName, len(content), lines)
	case "grep":
		matches := lines
		return fmt.Sprintf("[grep] %d matches | %s", matches, head)
	case "glob":
		return fmt.Sprintf("[glob] %d results | %s", lines, head)
	case "list_dir":
		return fmt.Sprintf("[list_dir] %d entries | %s", lines, head)
	default:
		if len(content) > 5000 {
			// Content-type aware smart compression
			compressed := smartCompress(content, 3000)
			return fmt.Sprintf("[%s] %d→%d chars | %s", toolName, len(content), len(compressed), compressed)
		}
		return content // keep as-is if reasonably sized
	}
}

// ====================== Content-Type Aware Compression (借鉴 Headroom) ======================

// smartCompress detects content type via tiered confidence scoring and applies the best strategy.
// taskHint optionally provides user task keywords for context-aware line scoring.
func smartCompress(content string, maxLen int, taskHint ...string) string {
	if len(content) <= maxLen {
		return content
	}
	// Tiered detection: run all detectors, pick highest confidence
	bestType, bestConf := detectContentType(content)
	if bestConf < 0.3 {
		// Low confidence → neutral text compression
		return compressByLineScore(content, "text", maxLen, taskHint...)
	}
	switch bestType {
	case "json":
		return compressJSON(content, maxLen)
	case "code":
		return compressCode(content, maxLen)
	case "log":
		return compressLogs(content, maxLen)
	default:
		return compressByLineScore(content, "text", maxLen, taskHint...)
	}
}

// detectContentType runs all detectors and returns the best match with confidence.
// Confidence 0.0 = no match, 1.0 = certain. Threshold 0.3+ required to select a type.
func detectContentType(content string) (string, float64) {
	type candidate struct {
		typ  string
		conf float64
	}
	var candidates []candidate

	s := strings.TrimSpace(content)
	head := s[:min(len(s), 500)]

	// JSON detector: high confidence on structural match, lower on partial
	jsonConf := 0.0
	if (strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")) &&
		(strings.HasSuffix(s, "}") || strings.HasSuffix(s, "]")) {
		// Count structural JSON elements
		colons := strings.Count(head, "\":")
		braces := strings.Count(head, "{") + strings.Count(head, "[")
		if colons >= 3 && braces >= 1 {
			jsonConf = 0.95
		} else if colons >= 1 {
			jsonConf = 0.70
		} else {
			jsonConf = 0.50
		}
	}
	candidates = append(candidates, candidate{"json", jsonConf})

	// Code detector: keyword density
	codeConf := codeConfidence(head)
	candidates = append(candidates, candidate{"code", codeConf})

	// Log detector: timestamp/level patterns
	logConf := logConfidence(head)
	candidates = append(candidates, candidate{"log", logConf})

	// Pick highest confidence
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.conf > best.conf {
			best = c
		}
	}
	return best.typ, best.conf
}

func codeConfidence(head string) float64 {
	indicators := []string{"func ", "function ", "def ", "class ", "import ", "package ",
		"public ", "private ", "void ", "int ", "string ", "const ", "var ", "let ",
		"return ", "#include", "export ", "async ", "await "}
	lower := strings.ToLower(head)
	count := 0
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			count++
		}
	}
	// Also check for structural patterns
	hasBraces := strings.Contains(head, "{") && strings.Contains(head, "}")
	hasSemicolons := strings.Count(head, ";") >= 3

	if count >= 5 {
		return 0.90
	} else if count >= 3 && hasBraces {
		return 0.75
	} else if count >= 2 || (count >= 1 && hasSemicolons) {
		return 0.50
	} else if count >= 1 {
		return 0.30
	}
	return 0.0
}

func logConfidence(head string) float64 {
	logLevels := []string{"ERROR", "WARN", "INFO", "DEBUG", "FATAL", "TRACE",
		"[error]", "[warn]", "[info]", "[debug]", "[ERROR]", "[WARN]"}
	lower := strings.ToUpper(head)
	count := 0
	for _, lvl := range logLevels {
		if strings.Contains(lower, lvl) {
			count++
		}
	}
	// Timestamp pattern
	hasTimestamp := false
	if len(head) > 15 {
		// Check for patterns like "2024-01-01" or "01:02:03"
		if (head[4] == '-' && head[7] == '-') || (head[2] == ':' && head[5] == ':') {
			hasTimestamp = true
		}
	}
	if count >= 3 {
		return 0.95
	} else if count >= 1 && hasTimestamp {
		return 0.80
	} else if count >= 1 {
		return 0.60
	} else if hasTimestamp {
		return 0.40
	}
	return 0.0
}

func isJSON(s string) bool {
	_, conf := detectContentType(s)
	return conf >= 0.5
}

func compressJSON(content string, maxLen int) string {
	lines := strings.Split(content, "\n")
	var out []string
	charCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "{" || trimmed == "}" || trimmed == "[" || trimmed == "]" {
			out = append(out, line)
			charCount += len(line)
			continue
		}
		// Truncate long values: "key": "very long value..." → "key": "...(N chars)"
		if len(trimmed) > 120 {
			if colon := strings.Index(trimmed, ":"); colon > 0 {
				key := strings.TrimSpace(trimmed[:colon])
				valLen := len(trimmed) - colon - 1
				trimmed = key + ": \"...(" + fmt.Sprintf("%d", valLen) + " chars)\""
			}
		}
		out = append(out, trimmed)
		charCount += len(trimmed)
		if charCount > maxLen {
			out = append(out, fmt.Sprintf("...[JSON truncated: %d→%d lines, %d→%d chars]",
				len(lines), len(out), len(content), charCount))
			break
		}
	}
	return strings.Join(out, "\n")
}

func compressCode(content string, maxLen int) string {
	lines := strings.Split(content, "\n")
	var out []string
	charCount := 0
	inFunc := false
	funcDepth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Always keep: package/import declarations, function signatures, comments
		isSig := strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "export ") ||
			strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "package ") ||
			strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "# ") ||
			strings.HasPrefix(trimmed, "/*")

		if isSig {
			out = append(out, line)
			charCount += len(line)
			inFunc = false
			funcDepth = 0
			continue
		}

		// Track brace depth
		funcDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if strings.Contains(trimmed, "{") && !inFunc {
			inFunc = true
			out = append(out, line) // opening line
			charCount += len(line)
			continue
		}
		if inFunc && funcDepth <= 0 {
			inFunc = false
			out = append(out, line) // closing line
			charCount += len(line)
			continue
		}

		// Inside function body: keep first 3 lines, then skip
		if inFunc {
			if i > 0 && !inFunc {
				// skip body lines, just note them
				if !strings.Contains(strings.Join(out, ""), "...(") {
					out = append(out, fmt.Sprintf("    // ...(%d lines of body omitted)", countBodyLines(lines, i)))
					charCount += 40
				}
			}
			continue
		}

		out = append(out, line)
		charCount += len(line)
		if charCount > maxLen {
			break
		}
	}
	return strings.Join(out, "\n")
}

func countBodyLines(lines []string, from int) int {
	depth := 1
	count := 0
	for i := from; i < len(lines) && depth > 0; i++ {
		depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		count++
	}
	return count
}

func compressLogs(content string, maxLen int) string {
	return compressByLineScore(content, "log", maxLen)
}

func truncateSmart(content string, maxLen int) string {
	return compressByLineScore(content, "text", maxLen)
}

// ====================== Line-Level Importance Scoring (借鉴 Headroom signals) ======================

// lineScore returns 0.0 (drop first) to 1.0 (keep at all costs) for a single line.
// ctx is "text", "code", or "log" — same line in different contexts scores differently.
func lineScore(line string, ctx string) float64 {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)

	// Empty / nearly empty
	if trimmed == "" || trimmed == "{" || trimmed == "}" || trimmed == "(" || trimmed == ")" {
		return 0.05
	}

	// Error/failure — always keep (universal across contexts)
	errWords := []string{"error", "exception", "failed", "fatal", "panic", "traceback",
		"cannot", "invalid", "timeout", "refused", "denied", "broken", "corrupt",
		"错误", "失败", "异常", "超时"}
	for _, w := range errWords {
		if strings.Contains(lower, w) {
			return 0.95
		}
	}

	// Security-sensitive
	secWords := []string{"password", "secret", "token", "api_key", "private", "credential",
		"injection", "xss", "csrf", "auth", "permission"}
	for _, w := range secWords {
		if strings.Contains(lower, w) {
			return 0.90
		}
	}

	switch ctx {
	case "code":
		return lineScoreCode(trimmed, lower)
	case "log":
		return lineScoreLog(trimmed, lower)
	default:
		return lineScoreText(trimmed, lower)
	}
}

func lineScoreCode(trimmed, lower string) float64 {
	// Function/class signatures
	if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
		strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "export ") ||
		strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "package ") {
		return 0.92
	}
	// Comments (especially doc comments)
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, " *") {
		if strings.Contains(lower, "todo") || strings.Contains(lower, "fixme") ||
			strings.Contains(lower, "hack") || strings.Contains(lower, "note") {
			return 0.75
		}
		return 0.55
	}
	// Return/control flow
	if strings.HasPrefix(trimmed, "return ") || strings.HasPrefix(trimmed, "if ") ||
		strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "switch ") {
		return 0.50
	}
	// Variable declarations
	if strings.Contains(trimmed, ":=") || strings.HasPrefix(trimmed, "var ") ||
		strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "let ") {
		return 0.40
	}
	// Closing braces
	if trimmed == "}" || trimmed == ");" || trimmed == "]" {
		return 0.15
	}
	return 0.25 // body lines — keep some, drop most under pressure
}

func lineScoreLog(trimmed, lower string) float64 {
	// Log level prefixes
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
		strings.Contains(lower, "crit") {
		return 0.95
	}
	if strings.Contains(lower, "warn") {
		return 0.80
	}
	// Timestamped lines
	if len(trimmed) > 10 && (trimmed[0] >= '0' && trimmed[0] <= '9') {
		return 0.35
	}
	// Stack traces
	if strings.HasPrefix(trimmed, "\tat ") || strings.HasPrefix(trimmed, "  File ") {
		return 0.60
	}
	return 0.20
}

func lineScoreText(trimmed, lower string) float64 {
	// Headers / structural
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "##") ||
		strings.HasPrefix(trimmed, "###") {
		return 0.85
	}
	// Lists with numbers or bullets
	if len(trimmed) > 2 && (trimmed[0] == '-' || trimmed[0] == '*' ||
		(trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.')) {
		return 0.65
	}
	// Links / references
	if strings.Contains(trimmed, "http") || strings.Contains(trimmed, "://") {
		return 0.50
	}
	// Short lines likely boilerplate
	if len(trimmed) < 20 {
		return 0.20
	}
	return 0.40
}

type lineScoreEntry struct {
	idx   int
	line  string
	score float64
}

// compressByLineScore scores each line and keeps the top-N by score under maxLen.
// Optional taskHint adjusts scoring: lines matching the task get a boost.
func compressByLineScore(content string, ctx string, maxLen int, taskHint ...string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 1 {
		if len(content) <= maxLen {
			return content
		}
		return content[:maxLen] + "..."
	}

	entries := make([]lineScoreEntry, len(lines))
	// Build task keywords for context-aware boost
	taskWords := make(map[string]bool)
	if len(taskHint) > 0 && taskHint[0] != "" {
		for _, w := range strings.Fields(strings.ToLower(taskHint[0])) {
			if len(w) > 2 {
				taskWords[w] = true
			}
		}
	}

	for i, l := range lines {
		score := lineScore(l, ctx)
		// Context boost: lines matching task keywords get +0.15
		if len(taskWords) > 0 {
			lower := strings.ToLower(l)
			for w := range taskWords {
				if strings.Contains(lower, w) {
					score += 0.15
					if score > 1.0 {
						score = 1.0
					}
					break
				}
			}
		}
		entries[i] = lineScoreEntry{idx: i, line: l, score: score}
	}

	// Select lines by score under char budget
	kept := make([]bool, len(lines))
	charCount := 0
	// Phase 1: keep everything above 0.8
	for _, e := range entries {
		if e.score >= 0.8 {
			kept[e.idx] = true
			charCount += len(e.line)
		}
	}
	// Phase 2: fill remaining budget with highest-scoring remaining lines
	if charCount < maxLen {
		remaining := make([]lineScoreEntry, 0)
		for _, e := range entries {
			if !kept[e.idx] {
				remaining = append(remaining, e)
			}
		}
		sortByScore(remaining)
		for _, e := range remaining {
			if charCount+len(e.line) > maxLen {
				break
			}
			kept[e.idx] = true
			charCount += len(e.line)
		}
	}

	// Build output preserving original order
	var out []string
	skipped := 0
	for i, l := range lines {
		if kept[i] {
			if skipped > 0 {
				out = append(out, fmt.Sprintf("  ...(%d lines omitted)", skipped))
				skipped = 0
			}
			out = append(out, l)
		} else {
			skipped++
		}
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("  ...(%d lines omitted)", skipped))
	}
	return strings.Join(out, "\n")
}

func sortByScore(lines []lineScoreEntry) {
	// Simple insertion sort by score desc (small enough N, no imports needed)
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && lines[j].score > lines[j-1].score; j-- {
			lines[j], lines[j-1] = lines[j-1], lines[j]
		}
	}
}
