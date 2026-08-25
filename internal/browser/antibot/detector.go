// Package antibot provides automatic anti-bot detection and response
// for website cloning. It detects common blocking patterns (HTTP status
// codes, Cloudflare challenges, CAPTCHA pages) and triggers escalation
// of stealth measures through five configurable levels.
package antibot

import (
	"net/http"
	"strings"
)

// BlockReason categorises the type of blocking detected.
type BlockReason string

const (
	// ReasonNone indicates no blocking was detected.
	ReasonNone BlockReason = ""

	// ReasonForbidden is HTTP 403 — server explicitly denied access.
	ReasonForbidden BlockReason = "forbidden"

	// ReasonRateLimited is HTTP 429 — too many requests.
	ReasonRateLimited BlockReason = "rate_limited"

	// ReasonUnavailable is HTTP 503 — service temporarily unavailable.
	ReasonUnavailable BlockReason = "unavailable"

	// ReasonCloudflare indicates a Cloudflare challenge page (cf-ray header).
	ReasonCloudflare BlockReason = "cloudflare"

	// ReasonCaptcha indicates a CAPTCHA/verification page in the DOM.
	ReasonCaptcha BlockReason = "captcha"

	// ReasonBlocked indicates generic "access denied" / "blocked" content.
	ReasonBlocked BlockReason = "blocked"

	// ReasonTimeout indicates the page navigation timed out (possible bot wall).
	ReasonTimeout BlockReason = "timeout"

	// ReasonEmpty indicates the page returned empty content (suspicious).
	ReasonEmpty BlockReason = "empty"
)

// captchaKeywords are strings commonly found in anti-bot / CAPTCHA pages.
var captchaKeywords = []string{
	"captcha",
	"verify you are human",
	"verify you are a human",
	"are you a human",
	"please verify",
	"security check",
	"cf-challenge",
	"cf_challenge",
	"cf-browser-verification",
	"checking your browser",
	"ddos protection",
	"access denied",
	"blocked",
	"your request has been blocked",
	"please enable javascript",
	"javascript is required",
	"browser check",
	"automated access",
	"suspicious activity",
}

// turnstileIndicators are strings uniquely identifying Cloudflare Turnstile.
var turnstileIndicators = []string{
	"challenges.cloudflare.com",
	"challenges.cloudflare.com/turnstile",
	"cf-turnstile",
	"turnstile",
	"__cf_chl",
	"cf_chl_",
}

// cloudflareBlockHeaders are response headers that indicate active
// Cloudflare anti-bot challenges, not just Cloudflare CDN usage.
// Headers like cf-cache-status are common on any Cloudflare site and
// do NOT indicate blocking.
var cloudflareBlockHeaders = []string{
	"cf-chl-bypass",
	"cf-chl-out",
	"cf-chl-proxied",
}

// cloudflareInfoHeaders are informational Cloudflare headers that indicate
// the site uses Cloudflare but do NOT indicate blocking/challenges.
var cloudflareInfoHeaders = []string{
	"cf-ray",
	"cf-cache-status",
	"cf-connecting-ip",
	"cf-ipcountry",
	"cf-visitor",
}

// allCloudflareHeaders is the union of block and info headers, used for
// detecting whether a site uses Cloudflare at all.
var allCloudflareHeaders = append(cloudflareBlockHeaders, cloudflareInfoHeaders...)

// DetectHTTP analyses an HTTP response for anti-bot indicators.
// Returns the detected reason and a human-readable description.
func DetectHTTP(statusCode int, headers http.Header) (BlockReason, string) {
	switch statusCode {
	case 403:
		return ReasonForbidden,
			"HTTP 403 Forbidden — server denied access, " +
				"likely anti-bot protection"

	case 429:
		return ReasonRateLimited,
			"HTTP 429 Too Many Requests — rate limit exceeded"

	case 503:
		for _, h := range cloudflareBlockHeaders {
			if headers.Get(h) != "" {
				return ReasonCloudflare,
					"HTTP 503 + Cloudflare challenge headers — " +
						"anti-bot challenge page"
			}
		}
		return ReasonUnavailable,
			"HTTP 503 Service Unavailable — possible bot wall"
	}

	return ReasonNone, ""
}

// HasCloudflareHeaders returns true if the HTTP response contains any
// Cloudflare-specific header. This is a fast pre-flight check that can
// run before Chrome is started so stealth measures are in place from
// the very first page load.
func HasCloudflareHeaders(headers http.Header) bool {
	for _, h := range allCloudflareHeaders {
		if headers.Get(h) != "" {
			return true
		}
	}
	return false
}

// HasTurnstileMarkers returns true if the HTML contains Cloudflare
// Turnstile challenge indicators. Turnstile requires JS execution to
// pass; stealth scripts alone may not be sufficient.
func HasTurnstileMarkers(html string) bool {
	lower := strings.ToLower(html)
	for _, kw := range turnstileIndicators {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

var maintenanceKeywords = []string{
	"technical difficulties",
	"under maintenance",
	"currently unavailable",
	"service unavailable",
	"site is down",
	"down for maintenance",
	"we'll be back soon",
}

// maintenancePageMaxSize is the maximum size (in bytes) for a page to be
// considered a maintenance/block page. Real content pages are almost always
// larger than this threshold, while maintenance pages are typically small.
const maintenancePageMaxSize = 15000

func isMaintenancePage(html string) bool {
	if len(html) > maintenancePageMaxSize {
		return false
	}
	lower := strings.ToLower(html)
	matches := 0
	for _, kw := range maintenanceKeywords {
		if strings.Contains(lower, kw) {
			matches++
			if matches >= 1 {
				return true
			}
		}
	}
	return false
}

// suspiciousPageMaxSize is the maximum size (in bytes) for a page to be
// considered suspicious for CAPTCHA/detection keywords. Real content pages
// are typically much larger, while anti-bot pages are small.
const suspiciousPageMaxSize = 50000

// DetectDOM analyses HTML content for anti-bot / CAPTCHA indicators.
// Turnstile (Cloudflare) challenges are checked first to produce the most
// specific reason, followed by generic CAPTCHA keywords.
func DetectDOM(html string) (BlockReason, string) {
	if len(html) == 0 {
		return ReasonEmpty, "empty response body"
	}

	lower := strings.ToLower(html)

	if isMaintenancePage(html) {
		return ReasonUnavailable, "site maintenance page"
	}

	// Short blocked pages.
	if len(html) < 150 {
		if strings.Contains(lower, "forbidden") ||
			strings.Contains(lower, "denied") {
			return ReasonBlocked, "short blocked page"
		}
	}

	// === Cloudflare Turnstile: check BEFORE generic keywords. ===
	// Turnstile is a specific Cloudflare product — produce a clear,
	// actionable message rather than a raw HTML snippet.
	for _, kw := range turnstileIndicators {
		if strings.Contains(lower, kw) {
			return ReasonCloudflare,
				"Cloudflare Turnstile challenge detected — " +
					"this site requires browser-interactive challenge passes " +
					"(consider running with --stealth or " +
					"--no-antibot-auto to skip retries)"
		}
	}

	// Generic CAPTCHA / anti-bot keywords.
	// Only check these if the page is small enough to be suspicious.
	// Large pages (>50KB) are unlikely to be anti-bot pages even if they
	// contain keywords like "blocked" or "denied" in normal content.
	if len(html) <= suspiciousPageMaxSize {
		for _, kw := range captchaKeywords {
			idx := strings.Index(lower, kw)
			if idx >= 0 {
				snippet := extractSnippet(html, idx, len(kw), 60)
				return ReasonCaptcha,
					"anti-bot page: " + snippet
			}
		}
	}

	return ReasonNone, ""
}

// Detect analyses both HTTP response and page content.
// Returns the most specific blocking reason detected.
func Detect(statusCode int, headers http.Header,
	htmlContent string) (BlockReason, string) {
	// Check HTTP-level first (fastest and most reliable).
	reason, desc := DetectHTTP(statusCode, headers)
	if reason != ReasonNone {
		return reason, desc
	}

	// Check DOM content for text-based anti-bot patterns.
	reason, desc = DetectDOM(htmlContent)
	if reason != ReasonNone {
		return reason, desc
	}

	return ReasonNone, ""
}

// IsBlocked returns true if the given error reason indicates blocking.
func IsBlocked(reason BlockReason) bool {
	return reason != ReasonNone && reason != ReasonTimeout
}

// ShouldRetry returns true if this block type typically can be bypassed
// with stronger stealth measures.
func ShouldRetry(reason BlockReason) bool {
	switch reason {
	case ReasonForbidden, ReasonRateLimited, ReasonCloudflare,
		ReasonCaptcha, ReasonBlocked, ReasonTimeout:
		return true
	case ReasonUnavailable:
		return true // 503 may resolve with retry
	case ReasonEmpty, ReasonNone:
		return false
	default:
		return true
	}
}

// extractSnippet returns a short context window around text position.
func extractSnippet(html string, pos, kwLen, window int) string {
	start := max(pos-window, 0)
	end := min(pos+kwLen+window, len(html))
	snippet := strings.TrimSpace(html[start:end])
	// Replace newlines for single-line display.
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	// Collapse whitespace.
	snippet = strings.Join(strings.Fields(snippet), " ")
	if len(snippet) > 120 {
		snippet = snippet[:120] + "..."
	}
	return snippet
}
