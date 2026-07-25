package httpclient

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

type RateLimiter struct {
	mu          sync.Mutex
	tokens      float64
	maxTokens   float64
	refillRate  float64
	lastRefill  time.Time
	blockedHosts map[string]time.Time
}

func NewRateLimiter(maxPerSecond float64, burstSize int) *RateLimiter {
	return &RateLimiter{
		tokens:       float64(burstSize),
		maxTokens:    float64(burstSize),
		refillRate:   maxPerSecond,
		lastRefill:   time.Now(),
		blockedHosts: make(map[string]time.Time),
	}
}

func (r *RateLimiter) Allow(host string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if blocked, ok := r.blockedHosts[host]; ok {
		if time.Now().Before(blocked) {
			return false
		}
		delete(r.blockedHosts, host)
	}

	now := time.Now()
	elapsed := now.Sub(r.lastRefill).Seconds()
	r.tokens += elapsed * r.refillRate
	if r.tokens > r.maxTokens {
		r.tokens = r.maxTokens
	}
	r.lastRefill = now

	if r.tokens >= 1.0 {
		r.tokens -= 1.0
		return true
	}

	return false
}

func (r *RateLimiter) BlockHost(host string, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockedHosts[host] = time.Now().Add(duration)
	logutil.Warn("[ratelimit] host blocked", "host", host, "duration", duration)
}

func (r *RateLimiter) RoundTripper(transport http.RoundTripper) http.RoundTripper {
	return &rateLimitedTransport{
		transport: transport,
		limiter:   r,
	}
}

type rateLimitedTransport struct {
	transport http.RoundTripper
	limiter   *RateLimiter
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if !t.limiter.Allow(host) {
		t.limiter.BlockHost(host, 30*time.Second)
		return nil, &rateLimitError{host: host}
	}
	return t.transport.RoundTrip(req)
}

type rateLimitError struct {
	host string
}

func (e *rateLimitError) Error() string {
	return "rate limited for host: " + e.host
}

func IsRateLimitError(err error) bool {
	_, ok := err.(*rateLimitError)
	return ok
}

type RateLimitContextKey struct{}

func WithRateLimiter(ctx context.Context, limiter *RateLimiter) context.Context {
	return context.WithValue(ctx, RateLimitContextKey{}, limiter)
}