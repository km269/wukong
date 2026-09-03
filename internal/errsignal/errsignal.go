// Package errsignal provides signal-driven error classification for
// web crawling and search operations. Instead of ad-hoc string
// matching scattered across the codebase, errors are classified
// into canonical categories based on signals (HTTP status codes,
// error message patterns, network conditions), each with a
// recommended handling strategy.
//
// This improves system resilience by ensuring consistent error
// handling: transient errors are retried with backoff, rate limits
// respect Retry-After, bot detection escalates stealth, and
// permanent errors are skipped without wasted retries.
package errsignal

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

// ErrorClass is the canonical category of an error.
type ErrorClass int

const (
	// ClassUnknown is the default for unclassifiable errors.
	ClassUnknown ErrorClass = iota
	// ClassTransient: network timeouts, connection resets, 5xx.
	// Strategy: retry with exponential backoff.
	ClassTransient
	// ClassRateLimited: 429 Too Many Requests, 503 with Retry-After.
	// Strategy: wait for Retry-After, then retry.
	ClassRateLimited
	// ClassAuthRequired: 401 Unauthorized, 403 Forbidden.
	// Strategy: escalate credentials or skip.
	ClassAuthRequired
	// ClassBotDetection: Cloudflare challenge, CAPTCHA, WAF block.
	// Strategy: escalate stealth level or use visible browser.
	ClassBotDetection
	// ClassPermanent: 404 Not Found, 410 Gone, DNS failure.
	// Strategy: skip, do not retry.
	ClassPermanent
	// ClassInvalid: 400 Bad Request, malformed response.
	// Strategy: skip and log for investigation.
	ClassInvalid
)

// String returns the human-readable name of the error class.
func (c ErrorClass) String() string {
	switch c {
	case ClassTransient:
		return "transient"
	case ClassRateLimited:
		return "rate_limited"
	case ClassAuthRequired:
		return "auth_required"
	case ClassBotDetection:
		return "bot_detection"
	case ClassPermanent:
		return "permanent"
	case ClassInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// Classification holds the result of error analysis.
type Classification struct {
	Class       ErrorClass
	StatusCode  int
	Reason      string
	RetryAfter  time.Duration // for rate-limited responses
	MaxRetries  int           // recommended max retries
	ShouldRetry bool          // whether retrying is worthwhile
}

// Classify analyzes an error and optional HTTP status code to
// determine the error class and recommended handling strategy.
// The error message is inspected for known signal patterns.
func Classify(err error, statusCode int) Classification {
	if err == nil && statusCode == 0 {
		return Classification{Class: ClassUnknown}
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	lowErr := strings.ToLower(errStr)

	// --- Signal-based classification ---

	// Bot detection signals.
	if hasAny(lowErr,
		"cloudflare", "turnstile", "captcha", "challenge",
		"access denied", "blocked", "attention required",
		"cf-ray", "cf-mitigated",
	) || statusCode == http.StatusForbidden && hasAny(lowErr, "cloudflare", "captcha") {
		return Classification{
			Class:       ClassBotDetection,
			StatusCode:  statusCode,
			Reason:      "bot detection or WAF block detected",
			ShouldRetry: false, // escalate stealth, not simple retry
			MaxRetries:  0,
		}
	}

	// Rate limiting signals.
	if statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusServiceUnavailable && hasAny(lowErr, "rate", "limit", "retry") {
		return Classification{
			Class:       ClassRateLimited,
			StatusCode:  statusCode,
			Reason:      "rate limited by server",
			RetryAfter:  extractRetryAfter(err),
			ShouldRetry: true,
			MaxRetries:  3,
		}
	}

	// Permanent error signals.
	if statusCode == http.StatusNotFound ||
		statusCode == http.StatusGone ||
		hasAny(lowErr, "no such host", "dns", "name resolution") {
		return Classification{
			Class:       ClassPermanent,
			StatusCode:  statusCode,
			Reason:      "resource not found or DNS failure",
			ShouldRetry: false,
			MaxRetries:  0,
		}
	}

	// Auth required signals.
	if statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden {
		return Classification{
			Class:       ClassAuthRequired,
			StatusCode:  statusCode,
			Reason:      "authentication or authorization required",
			ShouldRetry: false,
			MaxRetries:  0,
		}
	}

	// Invalid request signals.
	if statusCode == http.StatusBadRequest ||
		statusCode == http.StatusUnprocessableEntity {
		return Classification{
			Class:       ClassInvalid,
			StatusCode:  statusCode,
			Reason:      "invalid request or malformed response",
			ShouldRetry: false,
			MaxRetries:  0,
		}
	}

	// Transient error signals.
	if statusCode >= 500 ||
		hasAny(lowErr,
			"timeout", "timed out", "deadline exceeded",
			"connection reset", "connection refused", "connection closed",
			"err_connection_closed", "err_connection_refused",
			"err_connection_reset", "err_connection_aborted",
			"err_network_changed", "err_internet_disconnected",
			"temporary failure", "eof",
			"no route to host", "network is unreachable",
		) {
		return Classification{
			Class:       ClassTransient,
			StatusCode:  statusCode,
			Reason:      "transient network or server error",
			ShouldRetry: true,
			MaxRetries:  3,
		}
	}

	// Unclassified error with a status code.
	if statusCode > 0 {
		return Classification{
			Class:       ClassUnknown,
			StatusCode:  statusCode,
			Reason:      fmt.Sprintf("unclassified HTTP %d", statusCode),
			ShouldRetry: false,
			MaxRetries:  0,
		}
	}

	// Unclassified error without a status code — default to
	// transient (benefit of the doubt for retry).
	return Classification{
		Class:       ClassUnknown,
		Reason:      errStr,
		ShouldRetry: true,
		MaxRetries:  1,
	}
}

// ClassifyHTTP is a convenience wrapper for HTTP response errors.
func ClassifyHTTP(statusCode int, body string) Classification {
	err := errors.New(body)
	return Classify(err, statusCode)
}

// RetryDelay returns the recommended delay before the next retry
// for the given classification and attempt number (0-based).
// Uses exponential backoff for transient errors, and respects
// Retry-After for rate-limited responses.
// Note: no jitter is applied here; callers that fan out concurrently
// should add jitter on top (see pkg/httpclient retryBackoff).
// The decision is logged at debug level with the branch taken, so
// "why did the next attempt fire N seconds later" is answerable from
// logs alone.
func RetryDelay(c Classification, attempt int) time.Duration {
	d, branch := retryDelay(c, attempt)
	logutil.Debug("[errsignal] retry delay",
		slog.String("class", c.Class.String()),
		slog.Int("attempt", attempt),
		slog.Int("max_retries", c.MaxRetries),
		slog.Bool("should_retry", c.ShouldRetry),
		slog.String("branch", branch),
		slog.Duration("delay", d),
	)
	return d
}

func retryDelay(c Classification, attempt int) (time.Duration, string) {
	if !c.ShouldRetry || attempt >= c.MaxRetries {
		return 0, "skip:no-retry-or-exhausted"
	}

	switch c.Class {
	case ClassRateLimited:
		if c.RetryAfter > 0 {
			return c.RetryAfter, "rate-limit:retry-after"
		}
		// Default backoff for rate limiting: 5s, 10s, 20s.
		return time.Duration(5*(1<<attempt)) * time.Second, "rate-limit:default-backoff"

	case ClassTransient, ClassUnknown:
		// Exponential backoff: 1s, 2s, 4s, 8s...
		base := time.Second * time.Duration(int(math.Pow(2, float64(attempt))))
		if base > 30*time.Second {
			return 30 * time.Second, "transient:exponential-capped"
		}
		return base, "transient:exponential"

	default:
		return 0, "skip:class-not-retried"
	}
}

// ShouldEscalate reports whether the error class warrants
// escalating anti-bot measures (stealth level, visible browser).
func ShouldEscalate(c Classification) bool {
	return c.Class == ClassBotDetection
}

// ShouldSkip reports whether the error is permanent and the
// resource should be skipped without retrying.
func ShouldSkip(c Classification) bool {
	return c.Class == ClassPermanent || c.Class == ClassInvalid ||
		c.Class == ClassAuthRequired
}

// --- Helpers ---

// hasAny reports whether s contains any of the substrings.
func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// extractRetryAfter attempts to parse a Retry-After duration from
// error messages. Returns 0 if not found.
func extractRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	s := strings.ToLower(err.Error())
	// Look for "retry-after: N" or "retry after N seconds".
	for _, prefix := range []string{"retry-after:", "retry after "} {
		idx := strings.Index(s, prefix)
		if idx < 0 {
			continue
		}
		rest := s[idx+len(prefix):]
		num := extractNumber(rest)
		if num > 0 {
			return time.Duration(num) * time.Second
		}
	}
	return 0
}

// extractNumber extracts the first positive integer from s.
func extractNumber(s string) int {
	n := 0
	found := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
			found = true
		} else if found {
			break
		}
	}
	if !found {
		return 0
	}
	return n
}
