package renderkit

import (
	"context"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Global resource watermark scheduling.
//
// The process hosts several browser pools at once (long-lived
// Controller pools plus one pool per clone/download task), each with
// its own worker count. Before this file there was no cross-pool
// coordination: N concurrent clone tasks meant N Chrome processes ×
// their Workers render slots, unbounded by any process-wide ceiling.
//
// GlobalBudget is that ceiling: a process-wide pool of render slots
// shared by every Dispatcher (both backends). Workers acquire a slot
// before running a job and release it after, so the total number of
// concurrently running renders across all pools is capped regardless
// of how many pools exist. Resource watermarks adapt the ceiling at
// runtime: a heap monitor samples the Go process against its soft
// memory limit and reports a pressure level (0 normal / 1 high /
// 2 severe) that shrinks the effective budget to 1/2, then 1/4 of the
// maximum, restoring it as pressure recedes. Slot holders are never
// preempted — the budget converges as in-flight renders finish.

// Default watermark thresholds for the heap monitor: pressure rises
// before the soft limit is hit so the budget contracts while there is
// still headroom, and only relaxes once usage falls back under the
// high watermark.
const (
	defaultHighWatermark   = 0.70
	defaultSevereWatermark = 0.85
	defaultMonitorEvery    = 2 * time.Second
)

// Pressure levels reported to SetPressure. Higher pressure shrinks the
// effective budget.
const (
	PressureNormal = 0
	PressureHigh   = 1
	PressureSevere = 2
)

// GlobalBudget caps the number of renders running concurrently across
// every pool in the process, and adapts that cap to resource pressure.
// The zero value is not usable; construct with NewGlobalBudget.
type GlobalBudget struct {
	mu    sync.Mutex
	max   int // slot ceiling (fixed at construction)
	cur   int // effective budget after pressure adjustment, >= 1
	inUse int // slots currently held
	// pressure is the current watermark level; it defines cur as
	// max>>pressure (floored at 1) but never revokes held slots.
	pressure int
	// changed is closed (and replaced) on every state change that may
	// unblock waiters (Release, pressure shifts). A channel rather
	// than sync.Cond so Acquire can select against ctx cancellation.
	changed chan struct{}
}

// NewGlobalBudget creates a budget with the given slot ceiling.
// max < 1 is clamped to 1.
func NewGlobalBudget(max int) *GlobalBudget {
	if max < 1 {
		max = 1
	}
	return &GlobalBudget{
		max:     max,
		cur:     max,
		changed: make(chan struct{}),
	}
}

// Acquire takes a render slot, blocking while the effective budget is
// exhausted. Cancellation returns ctx.Err and takes no slot. Held
// slots are never revoked by pressure increases — waiters see the
// shrunk budget as in-flight renders release.
func (b *GlobalBudget) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		b.mu.Lock()
		if b.inUse < b.cur {
			b.inUse++
			b.mu.Unlock()
			return nil
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// Release returns a slot taken by Acquire.
func (b *GlobalBudget) Release() {
	b.mu.Lock()
	if b.inUse > 0 {
		b.inUse--
	}
	b.notifyLocked()
	b.mu.Unlock()
}

// SetPressure applies a watermark level (see PressureNormal &c).
// Level 0 restores the full ceiling; higher levels shrink the
// effective budget to half, then a quarter (floor 1). Values outside
// 0..2 are clamped. Idempotent: unchanged pressure is a no-op.
func (b *GlobalBudget) SetPressure(level int) {
	if level < PressureNormal {
		level = PressureNormal
	}
	if level > PressureSevere {
		level = PressureSevere
	}
	b.mu.Lock()
	if level == b.pressure {
		b.mu.Unlock()
		return
	}
	b.pressure = level
	cur := b.max >> level
	if cur < 1 {
		cur = 1
	}
	b.cur = cur
	b.notifyLocked() // relaxation wakes waiters; shrink is a no-op for them
	b.mu.Unlock()
}

// Stats reports held slots, the effective budget, and the ceiling —
// for observability and tests.
func (b *GlobalBudget) Stats() (inUse, cur, max int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inUse, b.cur, b.max
}

// notifyLocked wakes every Acquire waiter. Callers hold b.mu.
func (b *GlobalBudget) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// pressureFor maps a resource usage ratio to a pressure level using
// the high/severe watermarks (with hysteresis folded in by the
// caller's thresholds, not here).
func pressureFor(ratio, high, severe float64) int {
	switch {
	case ratio >= severe:
		return PressureSevere
	case ratio >= high:
		return PressureHigh
	default:
		return PressureNormal
	}
}

// StartHeapMonitor samples the process heap against its soft memory
// limit and drives SetPressure, until ctx is cancelled. Production
// entry point; tests use startMonitor with an injected sampler.
func (b *GlobalBudget) StartHeapMonitor(ctx context.Context) {
	b.startMonitor(ctx, heapInUse, memSoftLimit(), defaultMonitorEvery,
		defaultHighWatermark, defaultSevereWatermark)
}

func (b *GlobalBudget) startMonitor(ctx context.Context, sample func() uint64, limit uint64,
	every time.Duration, high, severe float64,
) {
	if every <= 0 {
		every = defaultMonitorEvery
	}
	if limit == 0 {
		limit = 1
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			b.SetPressure(pressureFor(float64(sample())/float64(limit), high, severe))
		}
	}()
}

// heapInUse samples the heap fraction the GC cannot shrink below
// without work — the closest Go-side proxy for allocation pressure
// from big DOMs, base64 payloads and ZIM packaging.
func heapInUse() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// memSoftLimit returns the GOMEMLIMIT soft limit; when unset (the
// runtime reports "infinite") it falls back to 4 GiB so the monitor
// still has a meaningful scale.
func memSoftLimit() uint64 {
	// SetMemoryLimit(-1) is the documented read-only probe.
	l := uint64(debug.SetMemoryLimit(-1))
	if l == 0 || l >= uint64(math.MaxInt64)/4 {
		return 4 << 30
	}
	return l
}

// The process-wide budget singleton. Dispatcher consults it (see
// Start); nil means no global ceiling and zero overhead.
var (
	globalBudget atomic.Pointer[GlobalBudget]
	// globalBudgetDisabled records an explicit "no global budget"
	// decision (EnsureGlobalBudget with a negative size). It survives
	// until a positive size re-enables, so the auto-install fallback
	// (slots == 0, e.g. task pools built via NewBackend without
	// config) cannot silently override a configured disable.
	globalBudgetDisabled atomic.Bool
)

// SetGlobalBudget installs (or, with nil, removes) the process-wide
// render budget. Intended for wiring and tests; production code uses
// EnsureGlobalBudget.
func SetGlobalBudget(b *GlobalBudget) {
	globalBudget.Store(b)
}

// Budget returns the installed process-wide budget, or nil when none
// is (Dispatchers then run without a global ceiling).
func Budget() *GlobalBudget {
	return globalBudget.Load()
}

// EnsureGlobalBudget installs the process-wide budget exactly once;
// the first caller's slot count wins and later calls only retrieve
// it. slots == 0 picks max(4, NumCPU); slots < 0 explicitly disables
// the budget (returns nil, installs nothing, and later auto-sized
// calls stay disabled until a positive size re-enables — the last
// explicit decision wins). Installing also starts the heap monitor
// for the process lifetime.
func EnsureGlobalBudget(slots int) *GlobalBudget {
	if slots < 0 {
		globalBudgetDisabled.Store(true)
		return nil
	}
	if b := globalBudget.Load(); b != nil {
		return b
	}
	if slots > 0 {
		globalBudgetDisabled.Store(false) // explicit enable
	} else if globalBudgetDisabled.Load() {
		return nil
	}
	if slots == 0 {
		slots = max(4, runtime.NumCPU())
	}
	b := NewGlobalBudget(slots)
	if !globalBudget.CompareAndSwap(nil, b) {
		return globalBudget.Load()
	}
	b.StartHeapMonitor(context.Background())
	return b
}
