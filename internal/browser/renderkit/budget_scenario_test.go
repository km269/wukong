package renderkit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Scenario mock: high-concurrency render load against a small global
// budget while a virtual heap (the "mock memory watermark data")
// spikes through the pressure thresholds. Verifies the two promises
// of the global scheduler end-to-end through real Dispatchers:
//
//  1. Budget contraction — as the watermark crosses the high/severe
//     thresholds the effective budget halves, then quarters, and the
//     observed peak concurrency follows it down without preempting
//     in-flight renders.
//  2. Backpressure — when the (shrunk) budget is exhausted, queued
//     renders park inside Acquire, their submitters stay blocked, a
//     ctx-bounded submit times out, and releasing the slot drains
//     everything.

// fakeHeap is the mock resource data: a test-controlled baseline
// (MiB) plus a fixed contribution from every in-flight render, so the
// sampled watermark responds both to staged spikes and to actual
// render concurrency — 3 pools × 4 workers hammering slots visibly
// drives the usage ratio up.
type fakeHeap struct {
	base      atomic.Uint64 // MiB, staged by the test
	perRender uint64        // MiB each in-flight render contributes
	inFlight  atomic.Int32
}

func (f *fakeHeap) usage() uint64 {
	return f.base.Load() + uint64(f.inFlight.Load())*f.perRender
}

// peakTracker records the maximum number of simultaneously running
// renders observed by the run functions.
type peakTracker struct {
	cur  atomic.Int32
	peak atomic.Int32
}

func (p *peakTracker) enter() {
	c := p.cur.Add(1)
	for {
		old := p.peak.Load()
		if c <= old || p.peak.CompareAndSwap(old, c) {
			break
		}
	}
}

func (p *peakTracker) exit() { p.cur.Add(-1) }

// waitForCur polls the budget until its effective size reaches want.
func waitForCur(t *testing.T, b *GlobalBudget, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, cur, _ := b.Stats(); cur == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	_, cur, _ := b.Stats()
	t.Fatalf("budget cur=%d, never reached %d", cur, want)
}

// waitUntil polls cond until it holds or the deadline passes.
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

// submitAsync submits a render in the background, reporting errors
// via t.Errorf; the returned channel closes when Submit returns.
func submitAsync(t *testing.T, d *Dispatcher, url string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := d.Submit(ctx, url, ""); err != nil {
			t.Errorf("submit %s: %v", url, err)
		}
	}()
	return done
}

func TestGlobalBudget_HighConcurrencyWatermarkScenario(t *testing.T) {
	const (
		heapLimitMiB = 200 // scale of the mock heap
		perRenderMiB = 10  // each in-flight render adds this much
		monitorEvery = 2 * time.Millisecond

		pools       = 3 // simultaneous dispatchers ≙ browser pools
		poolWorkers = 4 // 12 workers in total …
		budgetMax   = 4 // … against a 4-slot process-wide budget
	)

	// The mock: 12 concurrent renders would push usage to
	// 120/200 = 60% by themselves; staged baselines then spike the
	// watermark through the thresholds regardless of in-flight count:
	//   base 145 → 72.5–82.5%  (high band, contracts 4→2)
	//   base 180 → ≥90%        (severe band, contracts →1)
	b := NewGlobalBudget(budgetMax)
	SetGlobalBudget(b)
	defer SetGlobalBudget(nil)

	heap := &fakeHeap{perRender: perRenderMiB}
	monCtx, monCancel := context.WithCancel(context.Background())
	defer monCancel()
	b.startMonitor(monCtx, heap.usage, heapLimitMiB, monitorEvery, 0.70, 0.85)

	// Swappable per-phase state: renders park on the current gate and
	// count into the current tracker.
	var (
		gateCh     = make(chan struct{})
		gatePtr    atomic.Pointer[chan struct{}]
		tracker    = &peakTracker{}
		trackerPtr atomic.Pointer[peakTracker]
	)
	gatePtr.Store(&gateCh)
	trackerPtr.Store(tracker)

	disp := make([]*Dispatcher, pools)
	for i := range disp {
		disp[i] = NewDispatcher(poolWorkers)
		disp[i].Start(poolWorkers, func(idx int, job *RenderJob) {
			tr := trackerPtr.Load()
			tr.enter()
			heap.inFlight.Add(1)
			<-*gatePtr.Load() // park until the phase releases
			heap.inFlight.Add(-1)
			tr.exit()
			job.ResultCh <- RenderResultOrErr{}
		})
	}
	defer func() {
		for _, d := range disp {
			d.Drain()
		}
	}()

	// phase floods submits across the pools and asserts that exactly
	// wantPeak renders run concurrently under the current watermark,
	// then releases them and waits for a full drain.
	phase := func(name string, baseMiB uint64, wantCur int, submits int, wantPeak int32) {
		t.Helper()
		heap.base.Store(baseMiB)
		waitForCur(t, b, wantCur)

		tracker = &peakTracker{}
		trackerPtr.Store(tracker)
		ch := make(chan struct{})
		gatePtr.Store(&ch)

		var wg sync.WaitGroup
		for i := 0; i < int(submits); i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := disp[i%pools].Submit(ctx, "http://scenario.test/"+name, ""); err != nil {
					t.Errorf("%s submit %d: %v", name, i, err)
				}
			}(i)
		}

		// The budget-sized batch starts and parks on the gate; the
		// rest must be stuck (workers in Acquire or jobs in queues).
		waitUntil(t, func() bool { return tracker.cur.Load() == wantPeak },
			2*time.Second, name+": parked renders never reached the budget cap")
		time.Sleep(80 * time.Millisecond) // window for any leak to show up
		if got := tracker.peak.Load(); got != wantPeak {
			t.Fatalf("%s (base=%dMiB, cur=%d): peak concurrent renders = %d, want %d",
				name, baseMiB, wantCur, got, wantPeak)
		}

		close(ch) // release the parked batch
		wg.Wait()
		waitUntil(t, func() bool { return tracker.cur.Load() == 0 },
			2*time.Second, name+": renders never drained after release")
		if got := tracker.peak.Load(); got != wantPeak {
			t.Fatalf("%s: post-drain peak = %d, want %d", name, got, wantPeak)
		}
		t.Logf("%s: watermark base=%dMiB -> cur=%d, %d submits, peak concurrent=%d",
			name, baseMiB, wantCur, submits, wantPeak)
	}

	// Baseline: low watermark, full budget. 12 workers vs 4 slots.
	phase("normal", 0, 4, 12, 4)

	// Watermark spike 1: high pressure halves the budget.
	phase("high-pressure", 145, 2, 6, 2)

	// Watermark spike 2: severe pressure quarters it (floor 1).
	phase("severe-pressure", 180, 1, 3, 1)

	// Recovery: watermark recedes, full budget returns.
	phase("recovered", 0, 4, 12, 4)

	// End-to-end backpressure under the shrunk budget: one gated
	// render holds the only remaining slot.
	heap.base.Store(180)
	waitForCur(t, b, 1)

	tracker = &peakTracker{}
	trackerPtr.Store(tracker)
	holdCh := make(chan struct{})
	gatePtr.Store(&holdCh)

	doneHold := submitAsync(t, disp[0], "http://scenario.test/holder")
	waitUntil(t, func() bool { return tracker.cur.Load() == 1 },
		2*time.Second, "holder render never started")

	doneB := submitAsync(t, disp[1], "http://scenario.test/blocked-b")
	doneC := submitAsync(t, disp[2], "http://scenario.test/blocked-c")

	// A ctx-bounded submit must give up with its deadline while the
	// only slot is held — cancellation flows through the global
	// budget wait, not just the pool queue.
	ctxD, cancelD := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancelD()
	if _, err := disp[0].Submit(ctxD, "http://scenario.test/ctx-timeout", ""); err == nil {
		t.Fatal("ctx-bounded submit succeeded although the budget was exhausted")
	}

	// Backpressure assertions: nothing else may run or return.
	time.Sleep(120 * time.Millisecond)
	if got := tracker.peak.Load(); got != 1 {
		t.Fatalf("backpressure leak: peak concurrent renders = %d, want 1", got)
	}
	for name, done := range map[string]<-chan struct{}{"B": doneB, "C": doneC} {
		select {
		case <-done:
			t.Fatalf("blocked submit %s returned while the budget was exhausted", name)
		default:
		}
	}

	// Releasing the holder drains the whole backlog.
	close(holdCh)
	<-doneHold
	<-doneB
	<-doneC
	waitUntil(t, func() bool { return tracker.cur.Load() == 0 },
		2*time.Second, "backlogged renders never drained after release")
	if got := tracker.peak.Load(); got != 1 {
		t.Fatalf("post-drain peak = %d, want 1 (leak during drain)", got)
	}
	t.Logf("backpressure: 3 submits against 1 slot — 1 ran, 2 queued, 1 ctx-timeout; all drained after release")
}
