package renderkit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tryAcquireSoon polls until Acquire succeeds or the deadline passes.
func tryAcquireSoon(t *testing.T, b *GlobalBudget, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := b.Acquire(ctx)
		cancel()
		if err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestGlobalBudget_CapsConcurrentHolders(t *testing.T) {
	b := NewGlobalBudget(2)

	// Two slots are immediately available.
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := b.Acquire(ctx); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	inUse, cur, max := b.Stats()
	if inUse != 2 || cur != 2 || max != 2 {
		t.Fatalf("stats after filling: inUse=%d cur=%d max=%d", inUse, cur, max)
	}

	// The third acquirer blocks until a slot frees.
	acquired := make(chan struct{})
	go func() {
		if err := b.Acquire(ctx); err != nil {
			t.Errorf("third acquire: %v", err)
		}
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("third acquire slipped through a full budget")
	case <-time.After(50 * time.Millisecond):
	}

	b.Release()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("third acquire never unblocked after Release")
	}
	b.Release() // third holder
	b.Release() // second holder
}

func TestGlobalBudget_AcquireCancelsWhileWaiting(t *testing.T) {
	b := NewGlobalBudget(1)
	if err := b.Acquire(context.Background()); err != nil {
		t.Fatalf("fill: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- b.Acquire(ctx) }()

	// Give the waiter a moment to park, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Acquire ignored cancellation")
	}

	// The cancelled waiter must not hold a slot.
	inUse, _, _ := b.Stats()
	if inUse != 1 {
		t.Fatalf("cancelled waiter leaked a slot: inUse=%d", inUse)
	}
}

func TestGlobalBudget_PressureShrinksThenRestores(t *testing.T) {
	b := NewGlobalBudget(4)

	// Fill to 2/4.
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := b.Acquire(ctx); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}

	// High pressure halves the budget to 2 — exactly the current
	// usage, so a new acquirer must wait.
	b.SetPressure(PressureHigh)
	_, cur, _ := b.Stats()
	if cur != 2 {
		t.Fatalf("high pressure: cur=%d, want 2", cur)
	}
	if tryAcquireSoon(t, b, 100*time.Millisecond) {
		t.Fatal("acquire passed while inUse == cur under pressure")
	}

	// Releasing one slot (inUse=1 < cur=2) lets a waiter through.
	b.Release()
	if !tryAcquireSoon(t, b, 2*time.Second) {
		t.Fatal("acquire still blocked after inUse dropped below cur")
	}

	// Pressure receding restores the full ceiling (cur=4, inUse=2).
	b.SetPressure(PressureNormal)
	_, cur, max := b.Stats()
	if cur != 4 || max != 4 {
		t.Fatalf("restored: cur=%d max=%d, want 4/4", cur, max)
	}
	if err := b.Acquire(ctx); err != nil {
		t.Fatalf("acquire after restore: %v", err)
	}
}

func TestGlobalBudget_PressureClampsAndFloors(t *testing.T) {
	b := NewGlobalBudget(4)

	// Out-of-range levels clamp to severe.
	b.SetPressure(99)
	_, cur, _ := b.Stats()
	if cur != 1 {
		t.Fatalf("severe: cur=%d, want floor 1", cur)
	}
	b.SetPressure(-5)
	_, cur, _ = b.Stats()
	if cur != 4 {
		t.Fatalf("negative: cur=%d, want 4", cur)
	}

	// A ceiling of 1 under any pressure stays 1.
	b1 := NewGlobalBudget(1)
	b1.SetPressure(PressureSevere)
	_, cur, _ = b1.Stats()
	if cur != 1 {
		t.Fatalf("max=1 severe: cur=%d, want 1", cur)
	}
}

func TestPressureForWatermarks(t *testing.T) {
	cases := []struct {
		ratio        float64
		want         int
		high, severe float64
	}{
		{0.10, PressureNormal, 0.70, 0.85},
		{0.69, PressureNormal, 0.70, 0.85},
		{0.70, PressureHigh, 0.70, 0.85},
		{0.84, PressureHigh, 0.70, 0.85},
		{0.85, PressureSevere, 0.70, 0.85},
		{0.99, PressureSevere, 0.70, 0.85},
	}
	for _, tc := range cases {
		if got := pressureFor(tc.ratio, tc.high, tc.severe); got != tc.want {
			t.Errorf("pressureFor(%v) = %d, want %d", tc.ratio, got, tc.want)
		}
	}
}

func TestGlobalBudget_MonitorDrivesPressure(t *testing.T) {
	b := NewGlobalBudget(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// limit=100; the sampler reports the currently staged ratio, so
	// each phase holds steady instead of racing a transient window.
	var ratio atomic.Uint64
	sample := func() uint64 { return ratio.Load() }
	b.startMonitor(ctx, sample, 100, 2*time.Millisecond, 0.70, 0.85)

	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, cur, _ := b.Stats(); cur == want {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		_, cur, _ := b.Stats()
		t.Fatalf("budget cur=%d, want %d", cur, want)
	}

	ratio.Store(90) // ≥ severe watermark
	waitFor(2)      // severe: 8>>2

	ratio.Store(80) // high band
	waitFor(4)      // high: 8>>1

	ratio.Store(50) // below high watermark
	waitFor(8)      // recovered: 8>>0

	cancel()
}

func TestDispatcher_RespectsGlobalBudget(t *testing.T) {
	b := NewGlobalBudget(1)
	SetGlobalBudget(b)
	defer SetGlobalBudget(nil)

	d := NewDispatcher(4)
	release := make(chan struct{})
	var inside, peak atomic.Int32

	d.Start(4, func(idx int, job *RenderJob) {
		cur := inside.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		<-release
		inside.Add(-1)
		job.ResultCh <- RenderResultOrErr{}
	})

	const jobs = 4
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := d.Submit(ctx, "http://budget.test", ""); err != nil {
				t.Errorf("submit: %v", err)
			}
		}()
	}

	// All four jobs queue, but only one run may be in flight: the
	// other workers park in Acquire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if inside.Load() == 1 && peak.Load() == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrent runs = %d, want 1 (budget=1)", got)
	}

	close(release)
	wg.Wait()
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak after completion = %d, want 1", got)
	}
}

func TestEnsureGlobalBudgetSingleton(t *testing.T) {
	SetGlobalBudget(nil)
	defer SetGlobalBudget(nil)

	// Explicit disable: installs nothing.
	if b := EnsureGlobalBudget(-1); b != nil {
		t.Fatalf("EnsureGlobalBudget(-1) = %v, want nil", b)
	}
	if Budget() != nil {
		t.Fatal("disable installed a budget anyway")
	}

	// A disabled process stays disabled for auto-sized callers (e.g.
	// task pools via NewBackend without config); only a positive size
	// re-enables.
	if b := EnsureGlobalBudget(0); b != nil {
		t.Fatalf("EnsureGlobalBudget(0) after disable = %v, want nil", b)
	}

	// First install wins; later calls (even different sizes) retrieve
	// the same instance.
	b1 := EnsureGlobalBudget(5)
	if b1 == nil {
		t.Fatal("EnsureGlobalBudget(5) = nil")
	}
	if _, _, max := b1.Stats(); max != 5 {
		t.Fatalf("installed max = %d, want 5", max)
	}
	if b2 := EnsureGlobalBudget(9); b2 != b1 {
		t.Fatal("second Ensure returned a different budget")
	}

	// Auto sizing: positive slots and at least 4.
	SetGlobalBudget(nil)
	b3 := EnsureGlobalBudget(0)
	if b3 == nil {
		t.Fatal("EnsureGlobalBudget(0) = nil")
	}
	if _, _, max := b3.Stats(); max < 4 {
		t.Fatalf("auto max = %d, want >= 4", max)
	}
}
