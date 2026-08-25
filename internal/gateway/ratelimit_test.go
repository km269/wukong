package gateway

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestRateLimiter_AllowCtxTimeout verifies that when the concurrency
// gate is saturated, a second AllowCtx returns (nil, false) promptly
// once its context deadline elapses — instead of blocking forever as
// the old sync.Cond implementation did.
//
// This is the regression test for the bug where a saturated gateway
// blocked the platform SDK's receive goroutine indefinitely, stalling
// all subsequent message reception (including dedup) for every user.
func TestRateLimiter_AllowCtxTimeout(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 1) // maxConcurrent = 1
	defer rl.Stop()

	// Acquire the only slot.
	release1, ok := rl.AllowCtx(context.Background(), "p", "u1")
	if !ok {
		t.Fatal("first AllowCtx should succeed")
	}
	defer release1()

	// Second acquisition must time out, not block forever.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	release2, ok := rl.AllowCtx(ctx, "p", "u2")
	elapsed := time.Since(start)

	if ok {
		if release2 != nil {
			release2()
		}
		t.Fatal("second AllowCtx should be rejected when saturated + ctx expired")
	}
	if elapsed > time.Second {
		t.Errorf("AllowCtx blocked %v; should have returned shortly after ctx deadline", elapsed)
	}
}

// TestRateLimiter_AllowCtxReleasedOnCancel verifies that an AllowCtx
// blocked waiting for a slot returns promptly when its context is
// cancelled (rather than waiting for a deadline).
func TestRateLimiter_AllowCtxReleasedOnCancel(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 1)
	defer rl.Stop()

	release1, _ := rl.AllowCtx(context.Background(), "p", "u1")
	defer release1()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, ok := rl.AllowCtx(ctx, "p", "u2")
		if ok {
			t.Error("expected rejection on cancel")
		}
		close(done)
	}()

	// Give the waiter a moment to park on the slot, then cancel.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good: returned promptly after cancel.
	case <-time.After(time.Second):
		t.Fatal("AllowCtx did not return after context cancellation")
	}
}

// TestRateLimiter_ReleaseFreesSlot verifies that releasing a slot
// allows a subsequent acquisition to proceed.
func TestRateLimiter_ReleaseFreesSlot(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 1)
	defer rl.Stop()

	release, ok := rl.AllowCtx(context.Background(), "p", "u1")
	if !ok {
		t.Fatal("first AllowCtx should succeed")
	}

	// While held, a second acquisition blocks.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, ok := rl.AllowCtx(ctx, "p", "u2"); ok {
		t.Fatal("second AllowCtx should block/fail while slot is held")
	}

	// Release, then a fresh acquisition must succeed immediately.
	release()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, ok := rl.AllowCtx(context.Background(), "p", "u2")
		if !ok {
			t.Error("AllowCtx should succeed after release")
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AllowCtx did not succeed promptly after release")
	}
}

// TestRateLimiter_PerUserRejectIsImmediate verifies the per-user
// sliding-window check rejects instantly (no ctx wait) once the user
// exceeds their quota, independent of the concurrency gate.
func TestRateLimiter_PerUserRejectIsImmediate(t *testing.T) {
	// maxReqPerWin=1, maxConcurrent large so the user gate is the
	// binding constraint.
	rl := NewRateLimiter(time.Minute, 1, 100)
	defer rl.Stop()

	if _, ok := rl.AllowCtx(context.Background(), "p", "u1"); !ok {
		t.Fatal("first request should be allowed")
	}
	// Second request from same user must be rejected at once.
	start := time.Now()
	if _, ok := rl.AllowCtx(context.Background(), "p", "u1"); ok {
		t.Fatal("second request from same user should be rejected")
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("per-user reject took %v; should be immediate", took)
	}
}

// TestRateLimiter_Disabled verifies that with both limits disabled,
// AllowCtx always succeeds with a no-op release.
func TestRateLimiter_Disabled(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 0)
	defer rl.Stop()

	for i := 0; i < 50; i++ {
		release, ok := rl.AllowCtx(context.Background(), "p", "u")
		if !ok {
			t.Fatalf("call %d: expected success when disabled", i)
		}
		release()
	}
}

// TestRateLimiter_MetricsReportsConcurrency verifies the Metrics
// snapshot reflects occupied slots after the channel-based rewrite.
func TestRateLimiter_MetricsReportsConcurrency(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 3)
	defer rl.Stop()

	r1, _ := rl.AllowCtx(context.Background(), "p", "u1")
	r2, _ := rl.AllowCtx(context.Background(), "p", "u2")
	defer r1()
	defer r2()

	m := rl.Metrics()
	if m.Concurrent != 2 {
		t.Errorf("Concurrent = %d, want 2", m.Concurrent)
	}
	if m.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent = %d, want 3", m.MaxConcurrent)
	}
}

// TestRateLimiter_ConcurrentStress sanity-checks the semaphore under
// concurrent access with -race, ensuring release never underflows and
// the slot count stays within bounds.
func TestRateLimiter_ConcurrentStress(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 0, 4)
	defer rl.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, ok := rl.AllowCtx(context.Background(), "p", "u")
			if !ok {
				return
			}
			defer release()
			time.Sleep(time.Millisecond)
		}()
	}
	wg.Wait()

	m := rl.Metrics()
	if m.Concurrent != 0 {
		t.Errorf("after all releases, Concurrent = %d, want 0", m.Concurrent)
	}
}
