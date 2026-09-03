package errsignal

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestClassify_BotDetection(t *testing.T) {
	cases := []struct {
		err        error
		statusCode int
	}{
		{errors.New("cloudflare challenge page detected"), 403},
		{errors.New("CAPTCHA required"), 0},
		{errors.New("access denied - WAF block"), 403},
		{errors.New("cf-ray: 12345"), 0},
	}
	for _, c := range cases {
		cls := Classify(c.err, c.statusCode)
		if cls.Class != ClassBotDetection {
			t.Errorf("Classify(%v, %d) = %s; want bot_detection",
				c.err, c.statusCode, cls.Class)
		}
		if cls.ShouldRetry {
			t.Errorf("bot detection should not retry")
		}
		if !ShouldEscalate(cls) {
			t.Errorf("bot detection should escalate")
		}
	}
}

func TestClassify_RateLimited(t *testing.T) {
	cls := Classify(nil, http.StatusTooManyRequests)
	if cls.Class != ClassRateLimited {
		t.Errorf("429 = %s; want rate_limited", cls.Class)
	}
	if !cls.ShouldRetry {
		t.Error("rate limited should retry")
	}
	if cls.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d; want 3", cls.MaxRetries)
	}
}

func TestClassify_Permanent(t *testing.T) {
	cases := []struct {
		err        error
		statusCode int
	}{
		{nil, http.StatusNotFound},
		{nil, http.StatusGone},
		{errors.New("no such host"), 0},
		{errors.New("DNS resolution failed"), 0},
	}
	for _, c := range cases {
		cls := Classify(c.err, c.statusCode)
		if cls.Class != ClassPermanent {
			t.Errorf("Classify(%v, %d) = %s; want permanent",
				c.err, c.statusCode, cls.Class)
		}
		if cls.ShouldRetry {
			t.Error("permanent should not retry")
		}
		if !ShouldSkip(cls) {
			t.Error("permanent should skip")
		}
	}
}

func TestClassify_AuthRequired(t *testing.T) {
	cls := Classify(nil, http.StatusUnauthorized)
	if cls.Class != ClassAuthRequired {
		t.Errorf("401 = %s; want auth_required", cls.Class)
	}
	if cls.ShouldRetry {
		t.Error("auth required should not retry")
	}

	// 403 without bot detection signals = auth required.
	cls = Classify(errors.New("forbidden"), http.StatusForbidden)
	if cls.Class != ClassAuthRequired {
		t.Errorf("403 without bot signals = %s; want auth_required", cls.Class)
	}
}

func TestClassify_Invalid(t *testing.T) {
	cls := Classify(nil, http.StatusBadRequest)
	if cls.Class != ClassInvalid {
		t.Errorf("400 = %s; want invalid", cls.Class)
	}
	if !ShouldSkip(cls) {
		t.Error("invalid should skip")
	}
}

func TestClassify_Transient(t *testing.T) {
	cases := []struct {
		err        error
		statusCode int
	}{
		{errors.New("connection reset by peer"), 0},
		{errors.New("ERR_CONNECTION_CLOSED"), 0},
		{errors.New("timeout waiting for response"), 0},
		{errors.New("deadline exceeded"), 0},
		{nil, 500},
		{nil, 502},
		{nil, 503},
	}
	for _, c := range cases {
		cls := Classify(c.err, c.statusCode)
		if cls.Class != ClassTransient {
			t.Errorf("Classify(%v, %d) = %s; want transient",
				c.err, c.statusCode, cls.Class)
		}
		if !cls.ShouldRetry {
			t.Error("transient should retry")
		}
	}
}

func TestRetryDelay_Transient(t *testing.T) {
	cls := Classify(errors.New("timeout"), 0)
	// attempt 0: ~1s, attempt 1: ~2s, attempt 2: ~4s.
	d0 := RetryDelay(cls, 0)
	d1 := RetryDelay(cls, 1)
	d2 := RetryDelay(cls, 2)
	if d0 <= 0 || d1 <= d0 || d2 <= d1 {
		t.Errorf("expected increasing delays: %v, %v, %v", d0, d1, d2)
	}
	// Exceeded max retries: 0.
	d3 := RetryDelay(cls, 3)
	if d3 != 0 {
		t.Errorf("delay after max retries = %v; want 0", d3)
	}
}

func TestRetryDelay_RateLimited(t *testing.T) {
	cls := Classify(nil, http.StatusTooManyRequests)
	d := RetryDelay(cls, 0)
	if d <= 0 {
		t.Errorf("rate limited delay should be positive, got %v", d)
	}
}

func TestRetryDelay_RetryAfter(t *testing.T) {
	cls := Classify(errors.New("429 retry-after: 30"), 429)
	if cls.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v; want 30s", cls.RetryAfter)
	}
	d := RetryDelay(cls, 0)
	if d != 30*time.Second {
		t.Errorf("delay = %v; want 30s", d)
	}
}

func TestClassifyHTTP(t *testing.T) {
	cls := ClassifyHTTP(404, "not found")
	if cls.Class != ClassPermanent {
		t.Errorf("404 = %s; want permanent", cls.Class)
	}
}

func TestErrorClass_String(t *testing.T) {
	cases := []struct {
		class ErrorClass
		want  string
	}{
		{ClassTransient, "transient"},
		{ClassRateLimited, "rate_limited"},
		{ClassAuthRequired, "auth_required"},
		{ClassBotDetection, "bot_detection"},
		{ClassPermanent, "permanent"},
		{ClassInvalid, "invalid"},
		{ClassUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.class.String(); got != c.want {
			t.Errorf("%d.String() = %q; want %q", c.class, got, c.want)
		}
	}
}

func TestClassify_NilError(t *testing.T) {
	cls := Classify(nil, 0)
	if cls.Class != ClassUnknown {
		t.Errorf("nil error = %s; want unknown", cls.Class)
	}
}

func TestClassify_UnclassifiedError(t *testing.T) {
	// Generic error without known signals defaults to unknown
	// but should retry once (benefit of the doubt).
	cls := Classify(errors.New("some random error"), 0)
	if cls.Class != ClassUnknown {
		t.Errorf("random error = %s; want unknown", cls.Class)
	}
	if !cls.ShouldRetry {
		t.Error("unknown should retry once")
	}
	if cls.MaxRetries != 1 {
		t.Errorf("MaxRetries = %d; want 1", cls.MaxRetries)
	}
}
