package ard

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type RateLimiterConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	MaxPerMinute int           `mapstructure:"max_per_minute"`
	Window       time.Duration `mapstructure:"window"`
}

type TokenBucket struct {
	tokens   map[string]*bucketInfo
	rate     float64
	capacity float64
	window   time.Duration
	mu       sync.Mutex
}

type bucketInfo struct {
	tokens   float64
	lastTime time.Time
}

func NewTokenBucket(maxPerMinute int, window time.Duration) *TokenBucket {
	if maxPerMinute <= 0 {
		maxPerMinute = 100
	}
	if window <= 0 {
		window = time.Minute
	}
	rate := float64(maxPerMinute) / window.Seconds()
	return &TokenBucket{
		tokens:   make(map[string]*bucketInfo),
		rate:     rate,
		capacity: float64(maxPerMinute),
		window:   window,
	}
}

func (tb *TokenBucket) Allow(key string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	b, exists := tb.tokens[key]
	if !exists {
		b = &bucketInfo{tokens: tb.capacity, lastTime: now}
		tb.tokens[key] = b
	}

	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > tb.capacity {
		b.tokens = tb.capacity
	}
	b.lastTime = now

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}

func (tb *TokenBucket) Cleanup() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	for key, b := range tb.tokens {
		if now.Sub(b.lastTime) > tb.window*2 {
			delete(tb.tokens, key)
		}
	}
}

func RateLimitMiddleware(config RateLimiterConfig) func(http.Handler) http.Handler {
	if !config.Enabled {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	bucket := NewTokenBucket(config.MaxPerMinute, config.Window)

	go func() {
		ticker := time.NewTicker(config.Window)
		defer ticker.Stop()
		for range ticker.C {
			bucket.Cleanup()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := getClientIP(r)
			if !bucket.Allow(key) {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `{"error":"rate_limit","message":"too many requests from %s, retry after 60s"}`, key)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func getClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.Split(forwarded, ",")[0]
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}
	return r.RemoteAddr
}