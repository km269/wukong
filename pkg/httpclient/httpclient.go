package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Client struct {
	*http.Client
	opts      Options
	userAgent string
	metrics   *metrics
	mu        sync.RWMutex
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

	client := &Client{
		opts:      opts,
		userAgent: opts.UserAgent,
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
		Timeout: opts.Timeout,
		Transport: &http.Transport{
			ForceAttemptHTTP2:   false,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: opts.TLSHandshakeTimeout,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
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
			fmt.Fprintf(os.Stderr, "[httpclient] [%s] request failed (attempt %d/%d) %s %s: %v\n",
				errorCategory, attempt+1, c.opts.MaxRetries+1, req.Method, req.URL, err)

			if attempt < c.opts.MaxRetries && c.shouldRetry(err) {
				c.mu.Lock()
				c.metrics.totalRetries++
				c.mu.Unlock()
				delay := time.Duration(attempt+1) * c.opts.RetryDelay
				fmt.Fprintf(os.Stderr, "[httpclient] retrying %s in %v...\n", req.URL, delay)
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
			fmt.Fprintf(os.Stderr, "[httpclient] server error %d (attempt %d/%d), retrying %s in %v...\n",
				resp.StatusCode, attempt+1, c.opts.MaxRetries+1, req.URL, delay)
			time.Sleep(delay)
			continue
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			c.mu.Lock()
			c.metrics.clientErrors++
			c.mu.Unlock()
			fmt.Fprintf(os.Stderr, "[httpclient] client error %d: %s %s\n", resp.StatusCode, req.Method, req.URL)
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
	fmt.Fprintf(os.Stderr, "[httpclient] Metrics:\n")
	fmt.Fprintf(os.Stderr, "  Total Requests: %d\n", m["total_requests"])
	fmt.Fprintf(os.Stderr, "  Total Retries: %d\n", m["total_retries"])
	fmt.Fprintf(os.Stderr, "  Success Count: %d\n", m["success_count"])
	fmt.Fprintf(os.Stderr, "  Failure Count: %d\n", m["failure_count"])
	fmt.Fprintf(os.Stderr, "  Success Rate: %.2f%%\n", m["success_rate"])
	fmt.Fprintf(os.Stderr, "  Avg Latency: %dms\n", m["avg_latency_ms"])
	fmt.Fprintf(os.Stderr, "\n  Error Distribution:\n")
	fmt.Fprintf(os.Stderr, "    Network Errors: %d\n", m["network_errors"])
	fmt.Fprintf(os.Stderr, "    Timeout Errors: %d\n", m["timeout_errors"])
	fmt.Fprintf(os.Stderr, "    TLS Errors: %d\n", m["tls_errors"])
	fmt.Fprintf(os.Stderr, "    Server Errors: %d\n", m["server_errors"])
	fmt.Fprintf(os.Stderr, "    Client Errors: %d\n", m["client_errors"])
	if m["last_error"] != "" {
		fmt.Fprintf(os.Stderr, "\n  Last Error: %s\n", m["last_error"])
		fmt.Fprintf(os.Stderr, "  Last Error Time: %s\n", m["last_error_time"])
	}
	if urls, ok := m["request_count_by_url"].(map[string]int64); ok && len(urls) > 0 {
		fmt.Fprintf(os.Stderr, "\n  Requests by URL:\n")
		for url, count := range urls {
			fmt.Fprintf(os.Stderr, "    %s: %d\n", url, count)
		}
	}
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
