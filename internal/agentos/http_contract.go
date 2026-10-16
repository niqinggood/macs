// Package agentos contains the transport-neutral Agent OS control-plane contract.
package agentos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	gatewayhttp "mac/internal/gateway/httpx"
	"mac/internal/textutil"
)

const (
	RequestIDHeader    = "X-Request-ID"
	TraceparentHeader  = "traceparent"
	maxRequestIDLength = 128
)

type httpContextKey uint8

const (
	requestIDContextKey httpContextKey = iota
	traceparentContextKey
)

var requestIDFallbackCounter atomic.Uint64

// ErrorResponse is the stable machine-readable Agent OS error envelope.
type ErrorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Retryable bool   `json:"retryable"`
}

// DecodeJSONBody accepts exactly one JSON value and rejects unknown fields.
func DecodeJSONBody(body io.Reader, target interface{}) error {
	if body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func NewRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "req_" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("req_%x_%x", time.Now().UnixNano(), requestIDFallbackCounter.Add(1))
}

func ValidRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}

func NormalizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if ValidRequestID(value) {
		return value
	}
	return ""
}

func allHex(value string) bool {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func allZeroHex(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '0' {
			return false
		}
	}
	return true
}

// NormalizeTraceparent validates the W3C version 00 wire format.
func NormalizeTraceparent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 55 {
		return ""
	}
	parts := strings.Split(value, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return ""
	}
	for _, part := range parts {
		if !allHex(part) {
			return ""
		}
	}
	if parts[0] != "00" || allZeroHex(parts[1]) || allZeroHex(parts[2]) {
		return ""
	}
	return value
}

func WithHTTPTrace(ctx context.Context, requestID, traceparent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestID = NormalizeRequestID(requestID); requestID != "" {
		ctx = context.WithValue(ctx, requestIDContextKey, requestID)
	}
	if traceparent = NormalizeTraceparent(traceparent); traceparent != "" {
		ctx = context.WithValue(ctx, traceparentContextKey, traceparent)
	}
	return ctx
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDContextKey).(string)
	return NormalizeRequestID(value)
}

func TraceparentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(traceparentContextKey).(string)
	return NormalizeTraceparent(value)
}

func EnsureRequestID(w http.ResponseWriter, r *http.Request) string {
	requestID := ""
	if r != nil {
		requestID = RequestIDFromContext(r.Context())
		if requestID == "" {
			requestID = NormalizeRequestID(r.Header.Get(RequestIDHeader))
		}
	}
	if requestID == "" && w != nil {
		requestID = NormalizeRequestID(w.Header().Get(RequestIDHeader))
	}
	if requestID == "" {
		requestID = NewRequestID()
	}
	if w != nil {
		w.Header().Set(RequestIDHeader, requestID)
	}
	return requestID
}

func RequestMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := NormalizeRequestID(r.Header.Get(RequestIDHeader))
		if requestID == "" {
			requestID = NewRequestID()
		}
		traceparent := NormalizeTraceparent(r.Header.Get(TraceparentHeader))
		ctx := WithHTTPTrace(r.Context(), requestID, traceparent)
		w.Header().Set(RequestIDHeader, requestID)
		if traceparent != "" {
			w.Header().Set(TraceparentHeader, traceparent)
		}
		next(w, r.WithContext(ctx))
	}
}

func CORSHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
				SendJSONError(w, r, "forbidden", "forbidden origin", http.StatusForbidden, false)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Auth-Token, X-Caller-ID, X-OpenKitty-Agent, X-OpenKitty-User-ID, X-OpenKitty-Session-ID, X-OpenWebUI-User-Id, X-OpenWebUI-Chat-Id, X-OpenWebUI-Message-Id, Idempotency-Key, X-Request-ID, traceparent")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-OpenKitty-Task-ID, X-OpenKitty-Session-ID, traceparent, Retry-After, Location")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func DefaultErrorCode(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		if statusCode >= 500 {
			return "internal_error"
		}
		return "request_failed"
	}
}

func DefaultErrorRetryable(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout ||
		statusCode >= 500
}

func PublicErrorMessage(message string, statusCode int) string {
	message = strings.TrimSpace(message)
	if statusCode >= 500 {
		if statusCode == http.StatusServiceUnavailable {
			return "service unavailable"
		}
		return "internal server error"
	}
	if message == "" {
		if text := http.StatusText(statusCode); text != "" {
			return strings.ToLower(text)
		}
		return "request failed"
	}
	return textutil.TruncateUTF8Bytes(message, 1024)
}

func PublicResponseValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var decoded interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return string(encoded)
	}
	return decoded
}

func PublicRoleError(err error) string {
	if err == nil {
		return "requested agent role is unavailable"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "caller is not authorized"):
		return "caller is not authorized for the requested agent role"
	case strings.Contains(message, "tenant is not authorized"):
		return "tenant is not authorized for the requested agent role"
	case strings.Contains(message, "service identity is not authorized"):
		return "service identity is not authorized for the requested agent role"
	case strings.Contains(message, "disabled"):
		return "requested agent role is disabled"
	default:
		return "requested agent role is unavailable"
	}
}

func PublicWorkspaceError(err error) string {
	if err == nil {
		return "session workspace conflicts with this request"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unknown web session"):
		return "unknown web session; create it with POST /api/sessions"
	case strings.Contains(message, "cannot run"):
		return "session is bound to a different workspace and cannot run in the requested workspace"
	default:
		return "session workspace conflicts with this request"
	}
}

func PublicAttachmentError(err error) string {
	if err == nil {
		return "one or more attachments are invalid"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "outside the workspace"):
		return "attachment is outside the workspace"
	case strings.Contains(message, "too many attachments"):
		return "too many attachments (max 20)"
	case strings.Contains(message, "path is required"):
		return "attachment path is required"
	case strings.Contains(message, "not a readable file"):
		return "attachment is not a readable file"
	case strings.Contains(message, "unavailable"):
		return "attachment is unavailable"
	default:
		return "one or more attachments are invalid"
	}
}

func SendJSONError(w http.ResponseWriter, r *http.Request, code, message string, statusCode int, retryable bool) {
	requestID := EnsureRequestID(w, r)
	if code = strings.TrimSpace(code); code == "" {
		code = DefaultErrorCode(statusCode)
	}
	message = PublicErrorMessage(message, statusCode)
	response := ErrorResponse{
		Error: message, Code: code, Message: message, RequestID: requestID, Retryable: retryable,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(response)
}

func SendStatusError(w http.ResponseWriter, r *http.Request, message string, statusCode int) {
	SendJSONError(w, r, DefaultErrorCode(statusCode), message, statusCode, DefaultErrorRetryable(statusCode))
}
