package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
	utls "github.com/refraction-networking/utls"
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
	// RootCAsPath points to a PEM-encoded CA bundle (e.g. a DoD root CA
	// package) used instead of the system roots. Enables strict verification
	// against .mil/.gov certificates without disabling checks entirely.
	RootCAsPath string
	// TLSFingerprint emulates a real Chrome TLS ClientHello using utls.
	// This is useful for sites that fingerprint TLS (e.g. Cloudflare) to
	// block non-browser HTTP clients.
	TLSFingerprint bool
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

	if opts.InsecureSkipVerify || opts.RootCAsPath != "" {
		tlsCfg := &tls.Config{}
		if opts.InsecureSkipVerify {
			tlsCfg.InsecureSkipVerify = true //nolint:gosec // opt-in via Options.InsecureSkipVerify
		}
		if opts.RootCAsPath != "" {
			pemBytes, rerr := os.ReadFile(opts.RootCAsPath)
			if rerr != nil {
				logutil.Warn("[httpclient] failed to read root CA bundle",
					slog.String("path", opts.RootCAsPath), slog.Any("error", rerr))
			} else {
				pool, perr := x509.SystemCertPool()
				if perr != nil || pool == nil {
					pool = x509.NewCertPool()
				}
				if pool.AppendCertsFromPEM(pemBytes) {
					tlsCfg.RootCAs = pool
				} else {
					logutil.Warn("[httpclient] no valid CA certs in bundle",
						slog.String("path", opts.RootCAsPath))
				}
			}
		}
		transport.TLSClientConfig = tlsCfg
	}

	// Construction-time TLS policy + timeout summary: the knobs that
	// govern whether a slow handshake counts as a "connection timeout"
	// (client timeout vs dial timeout vs TLS handshake timeout), so
	// log-only triage of hanging requests starts from known values.
	tlsMode := "verify"
	if opts.InsecureSkipVerify {
		tlsMode = "insecure"
	}
	if opts.RootCAsPath != "" {
		tlsMode += "+custom_ca"
	}
	logutil.Debug("[httpclient] client configured",
		slog.String("tls_mode", tlsMode),
		slog.String("root_cas", opts.RootCAsPath),
		slog.Duration("client_timeout", opts.Timeout),
		slog.Duration("dial_timeout", 30*time.Second),
		slog.Duration("tls_handshake_timeout", opts.TLSHandshakeTimeout),
		slog.Int("max_retries", opts.MaxRetries),
		slog.Duration("retry_delay", opts.RetryDelay),
		slog.Bool("force_ipv4", opts.ForceIPv4),
	)

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

	// TLS fingerprinting (utls): emulate a real Chrome ClientHello so that
	// TLS-fingerprinting WAFs (e.g. Cloudflare) accept us as a browser.
	// Only applied on the direct (non-proxy) path — proxied HTTP already
	// has its own utls handling in internal/browser/proxy_pool.go, and
	// mutually setting DialTLSContext + Proxy risks breaking CONNECT.
	if opts.TLSFingerprint && opts.ProxyURL == "" && len(opts.ProxyPool) == 0 {
		transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialWithDNSFallback(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				host = addr
			}

			// Insecure mode is opt-in via Options.InsecureSkipVerify.
			uconn := utls.UClient(conn, &utls.Config{
				ServerName:         host,
				InsecureSkipVerify: opts.InsecureSkipVerify, //nolint:gosec
			}, utls.HelloChrome_Auto)
			if err := uconn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return uconn, nil
		}
		// DialTLSContext takes over TLS setup, so drop the standard config.
		transport.TLSClientConfig = nil
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

		if attempt < c.opts.MaxRetries {
			if delay, retry := c.retryStatusDelay(resp, attempt); retry {
				resp.Body.Close()
				c.mu.Lock()
				c.metrics.totalRetries++
				c.metrics.serverErrors++
				c.mu.Unlock()
				logutil.Warn("[httpclient] server error, retrying",
					slog.Int("status", resp.StatusCode),
					slog.Int("attempt", attempt+1),
					slog.String("url", req.URL.String()),
					slog.Duration("delay", delay),
				)
				time.Sleep(delay)
				continue
			}
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

// DoWithTimeout performs req using the shared connection pool but with
// a per-request timeout applied via context deadline. The deadline is
// advisory for the body read: the transport returns as soon as the
// headers arrive, and the response body is wired to abort if read
// after the deadline passes. The context is released when the body is
// closed.
func (c *Client) DoWithTimeout(req *http.Request, timeout time.Duration) (*http.Response, error) {
	if timeout <= 0 || timeout >= c.Timeout {
		return c.Do(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnCloseBody releases the per-request context timer when the
// body is closed, so short-timeout requests cannot leak timers.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelOnCloseBody) Close() error {
	b.once.Do(b.cancel)
	return b.ReadCloser.Close()
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

// retryBackoff returns the delay before retry attempt (0-based) with
// equal jitter applied to the linear base ((attempt+1) * RetryDelay).
// Without jitter, concurrent clients that fail simultaneously would
// retry at exactly the same instants — a thundering herd against an
// already-struggling server. Equal jitter (delay = base/2 +
// rand[0, base/2)) keeps the expected delay at 75% of the linear base
// while spreading retries across the interval, and never delays less
// than base/2.
func (c *Client) retryBackoff(attempt int) time.Duration {
	base := time.Duration(attempt+1) * c.opts.RetryDelay
	half := base / 2
	if half <= 0 {
		return base
	}
	return half + time.Duration(rand.Int64N(int64(half)))
}

// retryStatusDelay decides whether an HTTP response warrants a retry
// and returns the delay to wait. It retries 429 and 5xx responses,
// honoring a Retry-After header when the server provides one (with a
// small jitter on top so concurrent clients that received the same
// value do not re-align on one instant). A Retry-After longer than
// the client's own timeout returns ok=false: blocking that long is
// pointless, so the response is surfaced to the caller instead.
func (c *Client) retryStatusDelay(resp *http.Response, attempt int) (time.Duration, bool) {
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return 0, false
	}

	delay := c.retryBackoff(attempt)
	if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
		if ra > c.opts.Timeout {
			return 0, false
		}
		if withJitter := ra + retryAfterJitter(ra); withJitter > delay {
			delay = withJitter
		}
	}
	return delay, true
}

// retryAfterJitter spreads concurrent clients that received the same
// Retry-After value: 10% of the wait, capped at 1s so short waits
// stay short. Never negative; 0 for tiny values.
func retryAfterJitter(ra time.Duration) time.Duration {
	jitterCap := ra / 10
	if jitterCap > time.Second {
		jitterCap = time.Second
	}
	if jitterCap <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(jitterCap)))
}

// parseRetryAfter parses a Retry-After response header. The value may
// be delay-seconds ("120") or an HTTP-date ("Wed, 21 Oct 2015
// 07:28:00 GMT"). Returns 0 when absent, in the past, or unparseable.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
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
