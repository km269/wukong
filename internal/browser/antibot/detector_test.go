package antibot

import (
	"net/http"
	"testing"
)

func TestDetectHTTP(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		headers    map[string]string
		reason     BlockReason
	}{
		{"403 forbidden", 403, nil, ReasonForbidden},
		{"429 rate limited", 429, nil, ReasonRateLimited},
		{"503 unavailable", 503, nil, ReasonUnavailable},
		{"503 cloudflare challenge", 503, map[string]string{"cf-chl-bypass": "1"}, ReasonCloudflare},
		{"503 with cf-ray only", 503, map[string]string{"cf-ray": "abc123"}, ReasonUnavailable},
		{"200 ok", 200, nil, ReasonNone},
		{"200 with cf-ray (CDN only)", 200, map[string]string{"cf-ray": "abc"}, ReasonNone},
		{"200 with cf-cache-status (CDN only)", 200, map[string]string{"cf-cache-status": "HIT"}, ReasonNone},
		{"404 not found", 404, nil, ReasonNone},
		{"500 server error", 500, nil, ReasonNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			reason, _ := DetectHTTP(tt.statusCode, h)
			if reason != tt.reason {
				t.Errorf("DetectHTTP(%d, ...) = %s, want %s",
					tt.statusCode, reason, tt.reason)
			}
		})
	}
}

func TestDetectDOM(t *testing.T) {
	tests := []struct {
		name   string
		html   string
		reason BlockReason
	}{
		{
			"captcha page",
			"<html><body>Please verify you are human</body></html>",
			ReasonCaptcha,
		},
		{
			"cloudflare challenge",
			"<html><body>Checking your browser before accessing</body></html>",
			ReasonCaptcha,
		},
		{
			"access denied",
			"<html><body><h1>Access Denied</h1></body></html>",
			ReasonBlocked,
		},
		{
			"ddos protection",
			"<html><body>DDoS protection by Cloudflare</body></html>",
			ReasonCaptcha,
		},
		{
			"ddos misspelled",
			"<html><body>ddos protection</body></html>",
			ReasonCaptcha,
		},
		{
			"normal page",
			"<html><head><title>Welcome</title></head><body>Hello World</body></html>",
			ReasonNone,
		},
		{
			"empty page",
			"",
			ReasonEmpty,
		},
		{
			"suspicious short forbidden",
			"<html>403 forbidden</html>",
			ReasonBlocked,
		},
		{
			"short normal page",
			"<html>hello</html>",
			ReasonNone,
		},
		{
			"security check",
			"<title>Security Check</title>",
			ReasonCaptcha,
		},
		{
			"browser check",
			"browser check required",
			ReasonCaptcha,
		},
		{
			"maintenance page",
			"We're sorry, this site is currently experiencing technical difficulties. Please try again in a few moments.",
			ReasonUnavailable,
		},
		{
			"maintenance page with forbidden",
			"We're sorry, this site is currently experiencing technical difficulties. Exception: forbidden",
			ReasonUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, _ := DetectDOM(tt.html)
			if reason != tt.reason {
				t.Errorf("DetectDOM(%q) = %s, want %s",
					tt.html, reason, tt.reason)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		html       string
		reason     BlockReason
	}{
		{"html captcha wins over 200 ok", 200,
			"<html>captcha required</html>", ReasonCaptcha},
		{"http 403 wins over normal html", 403,
			"<html>hello</html>", ReasonForbidden},
		{"both normal", 200,
			"<html>hello</html>", ReasonNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, _ := Detect(tt.statusCode, nil, tt.html)
			if reason != tt.reason {
				t.Errorf("Detect() = %s, want %s", reason, tt.reason)
			}
		})
	}
}

func TestIsBlocked(t *testing.T) {
	blocked := []BlockReason{
		ReasonForbidden, ReasonRateLimited, ReasonCloudflare,
		ReasonCaptcha, ReasonBlocked,
	}
	for _, r := range blocked {
		if !IsBlocked(r) {
			t.Errorf("IsBlocked(%s) = false, want true", r)
		}
	}
	if IsBlocked(ReasonNone) {
		t.Error("IsBlocked(ReasonNone) = true, want false")
	}
}

func TestShouldRetry(t *testing.T) {
	retryable := []BlockReason{
		ReasonForbidden, ReasonRateLimited, ReasonCloudflare,
		ReasonCaptcha, ReasonBlocked, ReasonTimeout, ReasonUnavailable,
	}
	for _, r := range retryable {
		if !ShouldRetry(r) {
			t.Errorf("ShouldRetry(%s) = false, want true", r)
		}
	}
	if ShouldRetry(ReasonNone) {
		t.Error("ShouldRetry(ReasonNone) = true, want false")
	}
	if ShouldRetry(ReasonEmpty) {
		t.Error("ShouldRetry(ReasonEmpty) = true, want false")
	}
}

func TestDetectDOM_Turnstile(t *testing.T) {
	tests := []struct {
		name   string
		html   string
		reason BlockReason
	}{
		{
			"Cloudflare Turnstile script",
			`<script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script>`,
			ReasonCloudflare,
		},
		{
			"Cloudflare challenge page",
			`<meta http-equiv="Content-Security-Policy" content="script-src 'nonce-abc' https://challenges.cloudflare.com">`,
			ReasonCloudflare,
		},
		{
			"cf-turnstile div",
			`<div class="cf-turnstile" data-sitekey="..."></div>`,
			ReasonCloudflare,
		},
		{
			"__cf_chl cookie marker",
			`<script>var __cf_chl_opt={cHash:'abc'};</script>`,
			ReasonCloudflare,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, desc := DetectDOM(tt.html)
			if reason != tt.reason {
				t.Errorf("DetectDOM() = %s (desc=%q), want %s",
					reason, desc, tt.reason)
			}
		})
	}
}

func TestHasCloudflareHeaders(t *testing.T) {
	h := http.Header{}
	if HasCloudflareHeaders(h) {
		t.Error("empty headers should not match")
	}
	h.Set("cf-ray", "abc123")
	if !HasCloudflareHeaders(h) {
		t.Error("cf-ray header should match")
	}
}

func TestHasTurnstileMarkers(t *testing.T) {
	if HasTurnstileMarkers("normal html") {
		t.Error("normal html should not match")
	}
	if !HasTurnstileMarkers(
		"<script src=\"https://challenges.cloudflare.com/turnstile/v0/api.js\">") {
		t.Error("turnstile url should match")
	}
}
