package httpclient

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryBackoff_Bounds(t *testing.T) {
	c := &Client{opts: Options{RetryDelay: 500 * time.Millisecond}}

	for attempt := 0; attempt < 5; attempt++ {
		base := time.Duration(attempt+1) * c.opts.RetryDelay
		lo, hi := base/2, base
		for i := 0; i < 200; i++ {
			got := c.retryBackoff(attempt)
			if got < lo || got > hi {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, got, lo, hi)
			}
		}
	}
}

func TestRetryBackoff_JitterVaries(t *testing.T) {
	c := &Client{opts: Options{RetryDelay: time.Second}}

	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		seen[c.retryBackoff(2)] = true // base 3s → range [1.5s, 3s)
	}
	// With real jitter, 100 draws over a 1.5s range must produce more
	// than one distinct value; exactly one value means jitter is dead.
	if len(seen) < 2 {
		t.Fatalf("retryBackoff produced a single fixed delay %v — no jitter", c.retryBackoff(2))
	}
}

func TestRetryBackoff_TinyDelay(t *testing.T) {
	// RetryDelay so small that base/2 truncates to zero: must degrade
	// to the raw base instead of panicking in rand.Int64N(0).
	c := &Client{opts: Options{RetryDelay: time.Nanosecond}}

	const base = 1 * time.Nanosecond
	for i := 0; i < 50; i++ {
		if got := c.retryBackoff(0); got != base {
			t.Fatalf("tiny delay: got %v, want base %v", got, base)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"120", 120 * time.Second},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"-3", 0},
		{"abc", 0},
		{"soon", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 95*time.Second {
		t.Errorf("parseRetryAfter(future date %q) = %v, want (0, 95s]", future, got)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, want 0", got)
	}
}

func TestRetryStatusDelay_TooLargeRetryAfter(t *testing.T) {
	// Retry-After longer than the client's own timeout must not retry.
	c := New(Options{Timeout: 5 * time.Second, RetryDelay: 10 * time.Millisecond})

	resp := &http.Response{StatusCode: 429, Header: http.Header{}}
	resp.Header.Set("Retry-After", "3600")
	if _, retry := c.retryStatusDelay(resp, 0); retry {
		t.Fatal("Retry-After 3600s > Timeout 5s should not retry")
	}
}

func TestDo_429WithRetryAfter(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&count, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{Timeout: 10 * time.Second, MaxRetries: 2, RetryDelay: 10 * time.Millisecond})
	start := time.Now()

	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after retry", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&count); n != 2 {
		t.Fatalf("server hit %d times, want 2", n)
	}
	// Retry-After: 1 must have been honored (≥1s wait before retry 2).
	if elapsed := time.Since(start); elapsed < 1*time.Second {
		t.Fatalf("retried after only %v — Retry-After: 1 not honored", elapsed)
	}
}

func TestDoWithTimeout_Enforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{Timeout: 10 * time.Second, MaxRetries: 0, RetryDelay: time.Millisecond})
	req, _ := http.NewRequest("GET", srv.URL, nil)

	start := time.Now()
	_, err := c.DoWithTimeout(req, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("returned after %v — per-request timeout not enforced", elapsed)
	}
}

func TestDoWithTimeout_Passthrough(t *testing.T) {
	// timeout >= client Timeout falls through to plain Do — no error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{Timeout: 100 * time.Millisecond, MaxRetries: 0})
	req, _ := http.NewRequest("GET", srv.URL, nil)

	resp, err := c.DoWithTimeout(req, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDo_503RetryAfterExceedsTimeout(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(Options{Timeout: 5 * time.Second, MaxRetries: 3, RetryDelay: 10 * time.Millisecond})
	start := time.Now()

	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 surfaced", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&count); n != 1 {
		t.Fatalf("server hit %d times, want 1 (no retry on oversized Retry-After)", n)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %v — should return immediately, not wait an hour", elapsed)
	}
}
