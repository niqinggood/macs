package httpx

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RequestHasNoOriginOrSameHost accepts non-browser clients without an Origin
// header and same-origin browser requests.
func RequestHasNoOriginOrSameHost(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

// WithWebAssetHeaders supplies explicit MIME and nosniff headers for embedded
// frontend assets.
func WithWebAssetHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.ToLower(filepath.Ext(r.URL.Path)) {
		case ".js", ".mjs":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".html", ".htm":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case ".json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		case ".svg":
			w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// RateLimiter is a per-key token-bucket limiter.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	rate     float64
	capacity int
}

type tokenBucket struct {
	tokens   float64
	lastTime time.Time
}

func NewRateLimiter(requestsPerMinute, burst int) *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*tokenBucket),
		rate:     float64(requestsPerMinute) / 60.0,
		capacity: burst,
	}
}

func (limiter *RateLimiter) Allow(key string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	bucket, ok := limiter.buckets[key]
	if !ok {
		bucket = &tokenBucket{tokens: float64(limiter.capacity), lastTime: time.Now()}
		limiter.buckets[key] = bucket
	}
	now := time.Now()
	bucket.tokens += now.Sub(bucket.lastTime).Seconds() * limiter.rate
	if bucket.tokens > float64(limiter.capacity) {
		bucket.tokens = float64(limiter.capacity)
	}
	bucket.lastTime = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

// RateLimitMiddleware applies the gateway JSON error contract when a key is
// exhausted.
func RateLimitMiddleware(limiter *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Session-Id")
		if key == "" {
			key = r.RemoteAddr
		}
		if !limiter.Allow(key) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":       "请求过于频繁，请稍后再试",
				"retry_after": "1s",
			})
			return
		}
		next(w, r)
	}
}
