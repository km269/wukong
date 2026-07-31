package httpclient

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

// Public DNS servers used as fallback when the system resolver fails.
// Many corporate/government DNS servers (e.g. DoD .mil/.gov) work with
// these public resolvers even when the local system DNS can't resolve.
var publicDNSFallback = []string{
	"8.8.8.8:53", // Google Public DNS
	"8.8.4.4:53", // Google Public DNS
	"1.1.1.1:53", // Cloudflare DNS
	"1.0.0.1:53", // Cloudflare DNS
	"9.9.9.9:53", // Quad9
}

type Client struct {
	*http.Client
	opts      Options
	userAgent string
	metrics   *metrics
	mu        sync.RWMutex
	dnsCache  *DNSCache
	limiter   *RateLimiter
}

type metrics struct {
	totalRequests     int64
	totalRetries      int64
	successCount      int64
	failureCount      int64
	totalLatency      time.Duration
	lastError         string
	lastErrorTime     time.Time
	networkErrors     int64
	timeoutErrors     int64
	tlsErrors         int64
	serverErrors      int64
	clientErrors      int64
	requestCountByURL map[string]int64
	latencyByURL      map[string]time.Duration
}

type ErrorCategory string

const (
	ErrorCategoryNetwork ErrorCategory = "network"
	ErrorCategoryTimeout ErrorCategory = "timeout"
	ErrorCategoryTLS     ErrorCategory = "tls"
	ErrorCategoryServer  ErrorCategory = "server"
	ErrorCategoryClient  ErrorCategory = "client"
	ErrorCategoryUnknown ErrorCategory = "unknown"
)

type Options struct {
	Timeout             time.Duration
	MaxRetries          int
	UserAgent           string
	RetryDelay          time.Duration
	MaxIdleConns        int
	IdleConnTimeout     time.Duration
	TLSHandshakeTimeout time.Duration
	ProxyURL            string
	ProxyPool           []string
	ProxyRotateEvery    int
	ForceIPv4           bool
	EnableDNSCache      bool
	DNSCacheTTL         time.Duration
	EnableRateLimit     bool
	RateLimitPerSecond  float64
	RateLimitBurst      int
	InsecureSkipVerify  bool // Skip TLS certificate verification (for .mil/.gov sites)
}

func DefaultOptions() Options {
	return Options{
		Timeout:             30 * time.Second,
		MaxRetries:          3,
		UserAgent:           "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		RetryDelay:          500 * time.Millisecond,
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultOptions().Timeout
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = DefaultOptions().MaxRetries
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultOptions().UserAgent
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = DefaultOptions().RetryDelay
	}
	if opts.MaxIdleConns <= 0 {
		opts.MaxIdleConns = DefaultOptions().MaxIdleConns
	}
	if opts.IdleConnTimeout <= 0 {
		opts.IdleConnTimeout = DefaultOptions().IdleConnTimeout
	}
	if opts.TLSHandshakeTimeout <= 0 {
		opts.TLSHandshakeTimeout = DefaultOptions().TLSHandshakeTimeout
	}

	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     false,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       opts.IdleConnTimeout,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if opts.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	// buildDialer constructs a net.Dialer with standard settings.
	buildDialer := func() *net.Dialer {
		return &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
	}

	// dialWithDNSFallback splits host:port, tries to resolve the host via
	// the system resolver, and on failure retries with public DNS servers.
	// Time budgets are kept tight so that DNS-blocked environments (where
	// both system DNS and public DNS are unreachable) fail fast and let
	// the caller fall back to alternative resolution (e.g. browser DoH).
	dialWithDNSFallback := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if opts.ForceIPv4 && network == "tcp" {
			network = "tcp4"
		}

		dialer := buildDialer()
		conn, err := dialer.DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}

		// Only retry DNS fallback on lookup errors.
		if !isDNSError(err) {
			return nil, err
		}

		host, port, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			return nil, err
		}

		// Skip for IP addresses — there's nothing to resolve.
		if net.ParseIP(host) != nil {
			return nil, err
		}

		logutil.Warn("[httpclient] system DNS failed, trying public DNS fallback",
			slog.String("host", host), slog.Any("error", err))

		// Try public DNS servers. Only try the first 2 servers (not all 5)
		// with a short 3s timeout each, so total fallback time is ≤6s.
		// In DNS-blocked networks (e.g. ISP-level UDP 53 blocking), trying
		// all 5 servers wastes 25s per request for no benefit.
		ipFamily := "ip"
		if opts.ForceIPv4 {
			ipFamily = "ip4"
		}

		maxDNSServers := 2
		if maxDNSServers > len(publicDNSFallback) {
			maxDNSServers = len(publicDNSFallback)
		}
		for _, dnsAddr := range publicDNSFallback[:maxDNSServers] {
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					d := &net.Dialer{Timeout: 3 * time.Second}
					return d.DialContext(ctx, "udp", dnsAddr)
				},
			}

			resolveCtx, resolveCancel := context.WithTimeout(ctx, 3*time.Second)
			ips, lookupErr := resolver.LookupIP(resolveCtx, ipFamily, host)
			resolveCancel()

			if lookupErr != nil || len(ips) == 0 {
				continue
			}

			for _, ip := range ips {
				targetAddr := net.JoinHostPort(ip.String(), port)
				dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
				conn2, dialErr := dialer.DialContext(dialCtx, network, targetAddr)
				dialCancel()
				if dialErr == nil {
					logutil.Info("[httpclient] public DNS fallback resolved",
						slog.String("host", host),
						slog.String("ip", ip.String()),
						slog.String("dns", dnsAddr))
					return conn2, nil
				}
			}
		}

		// All fallbacks exhausted — return the original error.
		return nil, err
	}

	var dnsCache *DNSCache
	if opts.EnableDNSCache {
		dnsCache = NewDNSCache(opts.DNSCacheTTL)
		transport.DialContext = dnsCache.WrapDialContext(dialWithDNSFallback)
	} else {
		transport.DialContext = dialWithDNSFallback
	}

	var limiter *RateLimiter
	if opts.EnableRateLimit {
		perSecond := opts.RateLimitPerSecond
		if perSecond <= 0 {
			perSecond = 10.0
		}
		burst := opts.RateLimitBurst
		if burst <= 0 {
			burst = 20
		}
		limiter = NewRateLimiter(perSecond, burst)
	}

	if opts.ProxyURL != "" {
		proxyURL, err := url.Parse(opts.ProxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			logutil.Info("[httpclient] using proxy", "proxy", opts.ProxyURL)
		} else {
			logutil.Error("[httpclient] invalid proxy URL", "error", err)
		}
	} else if len(opts.ProxyPool) > 0 {
		proxyURL, err := url.Parse(opts.ProxyPool[0])
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			logutil.Info("[httpclient] using proxy pool", "proxy", opts.ProxyPool[0])
		} else {
			logutil.Error("[httpclient] invalid proxy pool URL", "error", err)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	var httpTransport http.RoundTripper = transport
	if limiter != nil {
		httpTransport = limiter.RoundTripper(transport)
	}

	client := &Client{
		opts:      opts,
		userAgent: opts.UserAgent,
		dnsCache:  dnsCache,
		limiter:   limiter,
		metrics: &metrics{
			totalRequests:     0,
			totalRetries:      0,
			successCount:      0,
			failureCount:      0,
			totalLatency:      0,
			networkErrors:     0,
			timeoutErrors:     0,
			tlsErrors:         0,
			serverErrors:      0,
			clientErrors:      0,
			requestCountByURL: make(map[string]int64),
			latencyByURL:      make(map[string]time.Duration),
		},
	}

	client.Client = &http.Client{
		Timeout:   opts.Timeout,
		Transport: httpTransport,
	}

	return client
}

func NewDefault() *Client {
	return New(DefaultOptions())
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.metrics.totalRequests++
	c.metrics.requestCountByURL[req.URL.Host]++
	c.mu.Unlock()

	startTime := time.Now()

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	}

	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")
	}

	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	}

	if req.Header.Get("Sec-Ch-Ua") == "" {
		req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="130", "Chromium";v="130", "Not_A Brand";v="24"`)
	}

	if req.Header.Get("Sec-Ch-Ua-Mobile") == "" {
		req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	}

	if req.Header.Get("Sec-Ch-Ua-Platform") == "" {
		req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	}

	if req.Header.Get("Sec-Fetch-Site") == "" {
		req.Header.Set("Sec-Fetch-Site", "none")
	}

	if req.Header.Get("Sec-Fetch-Mode") == "" {
		req.Header.Set("Sec-Fetch-Mode", "navigate")
	}

	if req.Header.Get("Sec-Fetch-Dest") == "" {
		req.Header.Set("Sec-Fetch-Dest", "document")
	}

	if req.Header.Get("Connection") == "" {
		req.Header.Set("Connection", "keep-alive")
	}

	var lastErr error
	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		resp, err := c.Client.Do(req)
		latency := time.Since(startTime)

		c.mu.Lock()
		c.metrics.totalLatency += latency
		c.metrics.latencyByURL[req.URL.Host] += latency
		c.mu.Unlock()

		if err != nil {
			lastErr = err
			errorCategory := c.categorizeError(err)
			logutil.Error("[httpclient] request failed",
				slog.String("category", string(errorCategory)),
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", c.opts.MaxRetries+1),
				slog.String("method", req.Method),
				slog.String("url", req.URL.String()),
				slog.String("error", err.Error()),
			)

			if attempt < c.opts.MaxRetries && c.shouldRetry(err) {
				c.mu.Lock()
				c.metrics.totalRetries++
				c.mu.Unlock()
				delay := time.Duration(attempt+1) * c.opts.RetryDelay
				logutil.Debug("[httpclient] retrying",
					slog.String("url", req.URL.String()),
					slog.Duration("delay", delay),
				)
				time.Sleep(delay)
				continue
			}

			c.mu.Lock()
			c.metrics.failureCount++
			c.metrics.lastError = err.Error()
			c.metrics.lastErrorTime = time.Now()
			c.incrementErrorCategory(errorCategory)
			c.mu.Unlock()
			return nil, err
		}

		c.mu.Lock()
		c.metrics.successCount++
		c.mu.Unlock()

		if resp.StatusCode >= 500 && attempt < c.opts.MaxRetries {
			resp.Body.Close()
			c.mu.Lock()
			c.metrics.totalRetries++
			c.metrics.serverErrors++
			c.mu.Unlock()
			delay := time.Duration(attempt+1) * c.opts.RetryDelay
			logutil.Warn("[httpclient] server error, retrying",
				slog.Int("status", resp.StatusCode),
				slog.Int("attempt", attempt+1),
				slog.String("url", req.URL.String()),
				slog.Duration("delay", delay),
			)
			time.Sleep(delay)
			continue
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			c.mu.Lock()
			c.metrics.clientErrors++
			c.mu.Unlock()
			logutil.Warn("[httpclient] client error",
				slog.Int("status", resp.StatusCode),
				slog.String("method", req.Method),
				slog.String("url", req.URL.String()),
			)
		}

		return resp, nil
	}

	c.mu.Lock()
	c.metrics.failureCount++
	c.metrics.lastError = lastErr.Error()
	c.metrics.lastErrorTime = time.Now()
	c.incrementErrorCategory(c.categorizeError(lastErr))
	c.mu.Unlock()
	return nil, lastErr
}

func (c *Client) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func (c *Client) GetWithContext(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func (c *Client) GetWithHeaders(url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.Do(req)
}

func (c *Client) Post(url string, contentType string, body string) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

func (c *Client) PostWithContext(ctx context.Context, url string, contentType string, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

func (c *Client) shouldRetry(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "TLS handshake") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "i/o timeout")
}

func (c *Client) categorizeError(err error) ErrorCategory {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout"):
		return ErrorCategoryTimeout
	case strings.Contains(errStr, "TLS") || strings.Contains(errStr, "ssl"):
		return ErrorCategoryTLS
	case strings.Contains(errStr, "connection") || strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "no route") || strings.Contains(errStr, "reset by peer") ||
		strings.Contains(errStr, "broken pipe"):
		return ErrorCategoryNetwork
	default:
		return ErrorCategoryUnknown
	}
}

func (c *Client) incrementErrorCategory(category ErrorCategory) {
	switch category {
	case ErrorCategoryNetwork:
		c.metrics.networkErrors++
	case ErrorCategoryTimeout:
		c.metrics.timeoutErrors++
	case ErrorCategoryTLS:
		c.metrics.tlsErrors++
	case ErrorCategoryServer:
		c.metrics.serverErrors++
	case ErrorCategoryClient:
		c.metrics.clientErrors++
	}
}

func (c *Client) Metrics() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	successRate := 0.0
	if c.metrics.totalRequests > 0 {
		successRate = float64(c.metrics.successCount) / float64(c.metrics.totalRequests) * 100
	}

	avgLatencyMs := int64(0)
	if c.metrics.totalRequests > 0 {
		avgLatencyMs = c.metrics.totalLatency.Milliseconds() / c.metrics.totalRequests
	}

	return map[string]interface{}{
		"total_requests":       c.metrics.totalRequests,
		"total_retries":        c.metrics.totalRetries,
		"success_count":        c.metrics.successCount,
		"failure_count":        c.metrics.failureCount,
		"success_rate":         successRate,
		"avg_latency_ms":       avgLatencyMs,
		"last_error":           c.metrics.lastError,
		"last_error_time":      c.metrics.lastErrorTime,
		"network_errors":       c.metrics.networkErrors,
		"timeout_errors":       c.metrics.timeoutErrors,
		"tls_errors":           c.metrics.tlsErrors,
		"server_errors":        c.metrics.serverErrors,
		"client_errors":        c.metrics.clientErrors,
		"request_count_by_url": c.metrics.requestCountByURL,
		"latency_by_url":       c.metrics.latencyByURL,
	}
}

func (c *Client) PrintMetrics() {
	m := c.Metrics()
	logutil.Info("[httpclient] metrics",
		slog.Int64("total_requests", m["total_requests"].(int64)),
		slog.Int64("total_retries", m["total_retries"].(int64)),
		slog.Int64("success_count", m["success_count"].(int64)),
		slog.Int64("failure_count", m["failure_count"].(int64)),
		slog.Float64("success_rate", m["success_rate"].(float64)),
		slog.Int64("avg_latency_ms", m["avg_latency_ms"].(int64)),
		slog.Int64("network_errors", m["network_errors"].(int64)),
		slog.Int64("timeout_errors", m["timeout_errors"].(int64)),
		slog.Int64("tls_errors", m["tls_errors"].(int64)),
		slog.Int64("server_errors", m["server_errors"].(int64)),
		slog.Int64("client_errors", m["client_errors"].(int64)),
	)
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// isDNSError reports whether err is caused by a DNS resolution failure.
// This is used to trigger the public-DNS fallback path.
func isDNSError(err error) bool {
	if err == nil {
		return false
	}
	// Type assertion path first — most reliable.
	if _, ok := err.(*net.DNSError); ok {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "DNS") ||
		strings.Contains(errStr, "lookup") ||
		strings.Contains(errStr, "Temporary failure in name resolution") ||
		strings.Contains(errStr, "server misbehaving") ||
		strings.Contains(errStr, "name or service not known") ||
		strings.Contains(errStr, "nodename nor servname provided")
}
