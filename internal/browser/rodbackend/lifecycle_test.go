package rodbackend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestPoolLifecycle_ContextCancelAutoCloses verifies the phase-3
// context-based lifecycle on the rod backend: cancelling the context
// passed to New drains the pool, closes the rod browser (rod's
// MustClose is guarded so it runs exactly once), and subsequent
// renders are rejected — all without an explicit Close() call.
// Requires a real Chrome, so it is skipped in -short mode.
func TestPoolLifecycle_ContextCancelAutoCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Deliberately no <title>: Element("title") used to wait
		// forever on title-less pages — this doubles as regression
		// coverage for the bounded title wait.
		w.Write([]byte(`<!doctype html><html><body>lifecycle</body></html>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	pool, err := New(ctx, Options{
		ChromeBin:     `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		Headless:      true,
		Workers:       1,
		Settle:        300 * time.Millisecond,
		RenderTimeout: 30 * time.Second,
		Stealth:       true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// No defer Close: ctx cancellation must close the pool by itself.

	renderCtx, renderCancel := context.WithTimeout(ctx, 60*time.Second)
	defer renderCancel()
	if _, err := pool.Render(renderCtx, srv.URL); err != nil {
		t.Fatalf("Render before cancel: %v", err)
	}

	cancel()
	if !waitFor(t, 10*time.Second, func() bool { return pool.disp.Closed() }) {
		t.Fatal("pool not drained after ctx cancel")
	}

	if _, err := pool.Render(context.Background(), srv.URL); err == nil {
		t.Fatal("Render after ctx cancel should fail with pool closed")
	}

	// Close after the watcher already closed the browser must be a
	// no-op (MustClose runs exactly once — guarded by Drain's return
	// value), not a panic.
	pool.Close()
}
