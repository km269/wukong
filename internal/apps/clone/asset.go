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
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/httpclient"
)

// AssetDownloader downloads static web resources via HTTP.
// It is intentionally separate from the Chrome rendering pool because
// public assets rarely need a real browser engine.
type AssetDownloader struct {
	Client      *http.Client
	UserAgent   string
	MaxBytes    int64  // 0 = no limit.
	Retries     int    // 0 = no retries (single attempt).
	cfClearance string // Cloudflare bypass cookie (from Chrome render).
}

// DefaultAssetDownloader returns a downloader with sensible defaults.
func DefaultAssetDownloader() *AssetDownloader {
	return &AssetDownloader{
		Client:    httpclient.NewDefault().Client,
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

// tryDownload performs a single download attempt.
func (d *AssetDownloader) tryDownload(ctx context.Context, assetURL string) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, &DownloadError{URL: assetURL, Reason: "bad_request", Err: err}
	}
	req.Header.Set("User-Agent", d.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,ja;q=0.7")
	// Realistic sec-* headers — Cloudflare L3 detection distinguishes
	// browsers from simple HTTP clients by checking these exact headers.
	req.Header.Set("sec-ch-ua", `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "image")
	// Same-site Referer — sub-resource load context.
	if u, err := url.Parse(assetURL); err == nil {
		req.Header.Set("Referer", u.Scheme+"://"+u.Host+"/")
	}

	// Cloudflare bypass: if we have a cf_clearance cookie from Chrome,
	// include it so asset requests skip challenges.
	if d.cfClearance != "" {
		req.AddCookie(&http.Cookie{
			Name:  "cf_clearance",
			Value: d.cfClearance,
		})
	}

	resp, err := d.Client.Do(req)
	if err != nil {
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
