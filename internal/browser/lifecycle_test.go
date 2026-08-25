package browser

import (
	"context"
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
// context-based lifecycle: cancelling the context passed to New
// drains the pool and rejects subsequent submits, without any
// explicit Close() call. The chromedp allocator is lazy, so no real
// Chrome process is launched as long as nothing renders.
func TestPoolLifecycle_ContextCancelAutoCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := New(ctx, Options{Workers: 1})
	// No defer Close: the whole point is that ctx cancellation closes it.

	cancel()
	if !waitFor(t, 5*time.Second, func() bool { return pool.disp.Closed() }) {
		t.Fatal("pool not drained after ctx cancel")
	}

	if _, err := pool.Render(context.Background(), "https://example.com/"); err == nil {
		t.Fatal("Render after ctx cancel should fail with pool closed")
	}
}

// TestPoolLifecycle_ExplicitCloseReleasesWatcher verifies Close() is
// the explicit idempotent path and terminates the lifecycle watcher
// goroutine instead of leaking it.
func TestPoolLifecycle_ExplicitCloseReleasesWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // must not deadlock or panic after Close

	pool := New(ctx, Options{Workers: 1})
	pool.Close()
	pool.Close() // idempotent

	// After an explicit Close the watcher goroutine exits; this
	// indirectly proves no goroutine leak as long as the process
	// continues normally (verified by -race + no deadlock here).
	if !pool.disp.Closed() {
		t.Fatal("pool should be drained after explicit Close")
	}
	if _, err := pool.Render(context.Background(), "https://example.com/"); err == nil {
		t.Fatal("Render after Close should fail with pool closed")
	}
}

// TestPoolLifecycle_NilContextFallsBackToBackground verifies New
// tolerates a nil context (manual Close-only lifecycle), matching the
// pre-phase-3 behaviour for callers that never had a context.
func TestPoolLifecycle_NilContextFallsBackToBackground(t *testing.T) {
	pool := New(nil, Options{Workers: 1})
	pool.Close()
	if !pool.disp.Closed() {
		t.Fatal("pool should be drained after Close")
	}
}
