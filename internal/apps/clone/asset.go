// Package clone provides website cloning functionality.
//
// asset.go: Standalone asset downloader module.
// Separate from the Chrome rendering pool, this uses a lightweight HTTP client
// to download static resources (CSS, images, fonts, media) efficiently.
// Features: size limits, retry with backoff, redirect following,
// transient error classification, Content-Type-based CSS detection.
package clone

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/httpclient"
)

// AssetDownloader downloads static web resources via HTTP.
// It is intentionally separate from the Chrome rendering pool because
// public assets rarely need a real browser engine.
type AssetDownloader struct {
	Client      *http.Client
	UserAgent   string
	Referer     string   // Default Referer header for asset requests.
	MaxBytes    int64    // 0 = no limit.
	Retries     int      // 0 = no retries (single attempt).
	cfClearance string   // Cloudflare bypass cookie (from Chrome render).
	ProxyURL    string   // Single proxy URL (http://, https://, socks5://)
	ProxyPool   []string // List of proxy URLs for rotation
	proxyIndex  int      // Current index in proxy pool
	proxyMu     sync.Mutex

	// RefererOverrides maps target domain patterns to custom Referer values.
	// Keys can be exact domains ("media.defense.gov") or suffix patterns
	// (".defense.gov" matches any subdomain of defense.gov).
	// When downloading an asset from a matching domain, the corresponding
	// Referer is used instead of the default Referer.
	RefererOverrides map[string]string
}

// DefaultAssetDownloader returns a downloader with sensible defaults.
func DefaultAssetDownloader() *AssetDownloader {
	opts := httpclient.DefaultOptions()
	opts.ForceIPv4 = true
	return &AssetDownloader{
		Client:    httpclient.New(opts).Client,
		UserAgent: httpclient.DefaultOptions().UserAgent,
		MaxBytes:  50 * 1024 * 1024, // 50 MB.
		Retries:   3,
	}
}

// DownloadResult holds the result of a successful asset download.
type DownloadResult struct {
	URL         string
	Body        []byte
	ContentType string
	StatusCode  int
	IsCSS       bool
	Size        int64
}

// Download fetches a single asset URL and returns the result.
// It handles redirects transparently, enforces size limits, and retries
// on transient errors with exponential backoff.
func (d *AssetDownloader) Download(ctx context.Context, assetURL string) (*DownloadResult, error) {
	if d.Retries < 0 {
		d.Retries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= d.Retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 500ms, 1s, 2s, 4s, max 5s.
			backoff := time.Duration(500*(1<<(attempt-1))) * time.Millisecond
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, err := d.tryDownload(ctx, assetURL)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Stop retrying if error is not transient.
		if !d.transient(err) {
			break
		}

		// Respect context cancellation.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	return nil, lastErr
}

// getRefererForURL returns the appropriate Referer header for the given
// asset URL. It checks RefererOverrides for domain-specific overrides first,
// then falls back to the default Referer, and finally uses the asset's own
// origin as a last resort.
func (d *AssetDownloader) getRefererForURL(assetURL string) string {
	if len(d.RefererOverrides) > 0 {
		if u, err := url.Parse(assetURL); err == nil {
			host := u.Host
			// Try exact match first
			if ref, ok := d.RefererOverrides[host]; ok {
				return ref
			}
			// Try suffix match (e.g., ".defense.gov" matches "media.defense.gov")
			for pattern, ref := range d.RefererOverrides {
				if strings.HasPrefix(pattern, ".") &&
					strings.HasSuffix(host, pattern) {
					return ref
				}
			}
		}
	}
	if d.Referer != "" {
		return d.Referer
	}
	// Fallback: use the asset's own origin
	if u, err := url.Parse(assetURL); err == nil {
		return u.Scheme + "://" + u.Host + "/"
	}
	return ""
}

// tryDownload performs a single download attempt.
func (d *AssetDownloader) tryDownload(ctx context.Context, assetURL string) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, &DownloadError{URL: assetURL, Reason: "bad_request", Err: err}
	}
	req.Header.Set("User-Agent", d.UserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	// Realistic sec-* headers — Cloudflare L3 detection distinguishes
	// browsers from simple HTTP clients by checking these exact headers.
	req.Header.Set("sec-ch-ua", `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	// Additional realistic headers sent by real Chrome browsers
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("DNT", "1")
	// Determine sec-fetch-site based on whether referer is same-origin.
	referer := d.getRefererForURL(assetURL)
	secFetchSite := "same-origin"
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if assetURLParsed, err2 := url.Parse(assetURL); err2 == nil {
				if refURL.Host != assetURLParsed.Host {
					secFetchSite = "cross-site"
				}
			}
		}
	}
	req.Header.Set("sec-fetch-site", secFetchSite)
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "image")
	req.Header.Set("sec-fetch-user", "?1")
	// Same-site Referer — sub-resource load context.
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	// Cloudflare bypass: if we have a cf_clearance cookie from Chrome,
	// include it so asset requests skip challenges.
	if d.cfClearance != "" {
		req.AddCookie(&http.Cookie{
			Name:  "cf_clearance",
			Value: d.cfClearance,
		})
	}

	client := d.Client
	if len(d.ProxyPool) > 0 {
		d.proxyMu.Lock()
		proxyURL := d.ProxyPool[d.proxyIndex%len(d.ProxyPool)]
		d.proxyMu.Unlock()

		proxyParsed, err := url.Parse(proxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyParsed),
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if network == "tcp" {
						network = "tcp4"
					}
					return (&net.Dialer{
						Timeout:   30 * time.Second,
						KeepAlive: 30 * time.Second,
					}).DialContext(ctx, network, addr)
				},
			}
			client = &http.Client{
				Transport: transport,
				Timeout:   d.Client.Timeout,
			}
		}
	} else if d.ProxyURL != "" {
		proxyParsed, err := url.Parse(d.ProxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyParsed),
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if network == "tcp" {
						network = "tcp4"
					}
					return (&net.Dialer{
						Timeout:   30 * time.Second,
						KeepAlive: 30 * time.Second,
					}).DialContext(ctx, network, addr)
				},
			}
			client = &http.Client{
				Transport: transport,
				Timeout:   d.Client.Timeout,
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if len(d.ProxyPool) > 0 {
			d.proxyMu.Lock()
			d.proxyIndex++
			d.proxyMu.Unlock()
		}
		return nil, &DownloadError{URL: assetURL, Reason: "network", Err: err}
	}
	defer resp.Body.Close()

	// Check status code.
	if resp.StatusCode != http.StatusOK {
		err := &DownloadError{
			URL:        assetURL,
			Reason:     "http_status",
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("HTTP %d", resp.StatusCode),
		}
		return nil, err
	}

	// Early-out: Content-Length exceeds MaxBytes.
	if d.MaxBytes > 0 && resp.ContentLength > d.MaxBytes {
		return nil, &DownloadError{
			URL:    assetURL,
			Reason: "too_large",
			Err:    ErrAssetTooLarge,
		}
	}

	// Read body with limit.
	var body []byte
	if d.MaxBytes > 0 {
		// Read up to MaxBytes+1 to detect overflow.
		limited := io.LimitReader(resp.Body, d.MaxBytes+1)
		body, err = io.ReadAll(limited)
		if err != nil {
			return nil, &DownloadError{URL: assetURL, Reason: "read", Err: err}
		}
		if int64(len(body)) > d.MaxBytes {
			return nil, &DownloadError{
				URL:    assetURL,
				Reason: "too_large",
				Err:    ErrAssetTooLarge,
			}
		}
	} else {
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, &DownloadError{URL: assetURL, Reason: "read", Err: err}
		}
	}

	contentType := resp.Header.Get("Content-Type")
	isCSS := isCSSContentType(contentType) ||
		strings.HasSuffix(strings.ToLower(assetURL), ".css")

	return &DownloadResult{
		URL:         assetURL,
		Body:        body,
		ContentType: contentType,
		StatusCode:  resp.StatusCode,
		IsCSS:       isCSS,
		Size:        int64(len(body)),
	}, nil
}

// transient reports whether the error is likely to resolve on retry.
// Transient errors include: 403, 408, 425, 429, 5xx, and network errors.
// Non-transient: context cancellation, timeout, too large, 404, 401, 410.
func (d *AssetDownloader) transient(err error) bool {
	var de *DownloadError
	if AsDownloadError(err, &de) {
		switch de.Reason {
		case "http_status":
			switch de.StatusCode {
			case 403, 408, 425, 429:
				return true
			case 500, 502, 503, 504:
				return true
			default:
				return false
			}
		case "network":
			return true
		default:
			return false
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Error types.
// ---------------------------------------------------------------------------

// ErrAssetTooLarge is returned when an asset exceeds the size limit.
var ErrAssetTooLarge = fmt.Errorf("asset exceeds maximum size")

// DownloadError categorizes asset download failures.
type DownloadError struct {
	URL    string
	Reason string // "bad_request", "network", "http_status",
	// "read", "too_large"
	StatusCode int
	Err        error
}

func (e *DownloadError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("download %s: %s (%v)", e.URL, e.Reason, e.Err)
	}
	return fmt.Sprintf("download %s: %s (HTTP %d)", e.URL, e.Reason, e.StatusCode)
}

func (e *DownloadError) Unwrap() error {
	return e.Err
}

// AsDownloadError extracts a *DownloadError from an error chain.
func AsDownloadError(err error, target **DownloadError) bool {
	for {
		if de, ok := err.(*DownloadError); ok {
			*target = de
			return true
		}
		if ue, ok := err.(interface{ Unwrap() error }); ok {
			err = ue.Unwrap()
		} else {
			return false
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func isCSSContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return ct == "text/css"
}
