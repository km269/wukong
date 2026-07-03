// Package gateway — Rate Limiting
//
// RateLimiter provides per-user (platform + platformUserID) rate
// limiting to protect the agent backend from abuse.  It uses a
// sliding-window counter algorithm with a configurable window and
// maximum request count.
//
// Additionally, a concurrency limiter (maxConcurrent) caps the number
// of agent runs that may execute simultaneously across all channels.
// This prevents unbounded goroutine growth during spikes.
//
// Usage:
//
//	rl := NewRateLimiter(
//	    10 * time.Second, // window
//	    5,               // max requests per window per user
//	    100,             // max concurrent sessions
//	)
//	defer rl.Stop()
package gateway

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

// slidingWindow tracks request timestamps for a single user.
type slidingWindow struct {
	times []time.Time
}

// allow returns true if the request is allowed under the rate limit.
// It prunes timestamps outside the window before checking.
func (sw *slidingWindow) allow(now time.Time, window time.Duration, maxReq int) bool {
	cutoff := now.Add(-window)

	// Prune stale entries.
	n := 0
	for _, t := range sw.times {
		if !t.Before(cutoff) {
			sw.times[n] = t
			n++
		}
	}
	sw.times = sw.times[:n]

	if len(sw.times) >= maxReq {
		return false
	}
	sw.times = append(sw.times, now)
	return true
}

// RateLimiter throttles incoming gateway requests by platform user.
type RateLimiter struct {
	mu            sync.Mutex
	users         map[string]*slidingWindow
	window        time.Duration
	maxReqPerWin  int
	maxConcurrent int

	// Semaphore-style concurrency control. slots is a buffered channel
	// of capacity maxConcurrent; acquiring a slot sends into it and
	// releasing drains one. Unlike the previous sync.Cond approach,
	// this lets acquisition honour a context (AllowCtx) so a saturated
	// gateway no longer blocks the platform SDK's receive loop
	// indefinitely — when ctx is cancelled (e.g. gateway shutdown) the
	// waiter returns immediately instead of parking forever.
	slots chan struct{}

	ticker *time.Ticker
	stopCh chan struct{}
}

// NewRateLimiter creates a rate limiter with the given parameters.
//
//   - window: The sliding window duration (e.g. 10s).
//   - maxReqPerWin: Max requests allowed per user per window.
//   - maxConcurrent: Max simultaneous agent runs across all channels.
//     Set to 0 to disable concurrency limiting.
func NewRateLimiter(
	window time.Duration,
	maxReqPerWin int,
	maxConcurrent int,
) *RateLimiter {
	rl := &RateLimiter{
		users:         make(map[string]*slidingWindow, 64),
		window:        window,
		maxReqPerWin:  maxReqPerWin,
		maxConcurrent: maxConcurrent,
		ticker:        time.NewTicker(60 * time.Second),
		stopCh:        make(chan struct{}),
	}
	if maxConcurrent > 0 {
		rl.slots = make(chan struct{}, maxConcurrent)
	}

	if maxReqPerWin > 0 {
		go rl.cleanupLoop()
		util.Logger.Info("gateway: rate limiter started",
			slog.String("window", window.String()),
			slog.Int("max_req_per_window", maxReqPerWin),
			slog.Int("max_concurrent", maxConcurrent),
		)
	}
	return rl
}

// Allow checks whether a request from a given platform user is allowed
// without any cancellation. It is a convenience wrapper around AllowCtx
// with a background context, kept for backward compatibility.
func (rl *RateLimiter) Allow(
	platform, userID string,
) (release func(), allowed bool) {
	return rl.AllowCtx(context.Background(), platform, userID)
}

// AllowCtx checks whether a request from a given platform user is
// allowed. It first applies the per-user sliding-window rate check
// (immediate reject), then acquires a concurrency slot honouring ctx:
// if all slots are taken it blocks until either a slot frees or ctx is
// cancelled (in which case it returns (nil, false) promptly).
//
// On success it returns a release func that the caller MUST invoke
// after the agent run completes (typically via defer) to free the slot.
// On failure it returns a nil release and allowed=false.
//
// The ctx-awareness is critical: dispatch() is invoked synchronously by
// a Channel (e.g. the Feishu SDK's receive goroutine), so an
// unbounded wait here would stall all subsequent message reception
// whenever concurrency is saturated. Passing the gateway run context
// ensures shutdown (or any cancellation) promptly unblocks the waiter.
func (rl *RateLimiter) AllowCtx(
	ctx context.Context, platform, userID string,
) (release func(), allowed bool) {
	if rl.maxReqPerWin <= 0 && rl.maxConcurrent <= 0 {
		return func() {}, true
	}

	// 1. Per-user rate check (instant reject, never blocks).
	if rl.maxReqPerWin > 0 {
		rl.mu.Lock()
		key := platform + ":" + userID
		sw := rl.users[key]
		if sw == nil {
			sw = &slidingWindow{}
			rl.users[key] = sw
		}
		ok := sw.allow(time.Now(), rl.window, rl.maxReqPerWin)
		rl.mu.Unlock()
		if !ok {
			return nil, false
		}
	}

	// 2. Concurrency gate via a buffered-channel semaphore. select on
	//    ctx.Done() so a cancelled dispatch returns immediately rather
	//    than blocking the SDK receive loop.
	if rl.maxConcurrent > 0 {
		select {
		case rl.slots <- struct{}{}:
			// Acquired.
		case <-ctx.Done():
			return nil, false
		}
	}

	return rl.release, true
}

// release drains one concurrency slot. It is safe to call even when
// concurrency limiting is disabled (no-op) and is idempotent only in
// the sense that the caller is contractually required to call it once
// per successful AllowCtx.
func (rl *RateLimiter) release() {
	if rl.maxConcurrent <= 0 || rl.slots == nil {
		return
	}
	select {
	case <-rl.slots:
	default:
		// Defensive: a release without a matching acquire would
		// underflow; ignore rather than block forever.
	}
}

// Stop cleanly shuts down the background cleanup goroutine. The
// channel-based semaphore needs no explicit wake-up: any AllowCtx
// waiter blocked on rl.slots is unblocked by ctx cancellation at the
// caller (the gateway cancels the run context during Stop).
func (rl *RateLimiter) Stop() {
	rl.ticker.Stop()
	close(rl.stopCh)
}

// cleanupLoop periodically prunes stale sliding windows for users
// who haven't been active recently.
func (rl *RateLimiter) cleanupLoop() {
	for {
		select {
		case <-rl.ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-rl.window)
	for k, sw := range rl.users {
		n := 0
		for _, t := range sw.times {
			if !t.Before(cutoff) {
				sw.times[n] = t
				n++
			}
		}
		sw.times = sw.times[:n]
		if len(sw.times) == 0 {
			delete(rl.users, k)
		}
	}
}

// Metrics returns the current rate limiter stats for monitoring.
type RateLimiterMetrics struct {
	ActiveUsers   int `json:"active_users"`
	Concurrent    int `json:"concurrent"`
	MaxConcurrent int `json:"max_concurrent"`
}

// Metrics returns a snapshot of current rate limiter state.
func (rl *RateLimiter) Metrics() RateLimiterMetrics {
	rl.mu.Lock()
	activeUsers := len(rl.users)
	rl.mu.Unlock()

	concurrent := 0
	if rl.maxConcurrent > 0 && rl.slots != nil {
		// Each element in slots is one occupied token (acquired by
		// sending, freed by receiving), so len(slots) == in-use count.
		concurrent = len(rl.slots)
	}

	return RateLimiterMetrics{
		ActiveUsers:   activeUsers,
		Concurrent:    concurrent,
		MaxConcurrent: rl.maxConcurrent,
	}
}

// Adapted from original author's note: the sliding-window counter
// approach was chosen over token-bucket for simplicity — it is
// easier to reason about "N requests per W window" than
// "tokens/sec with burst".  For IM gateway workloads the
// difference is negligible.
//
// Concurrency-slot safety: when the agent run is canceled (e.g.
// timeout), the concurrency slot is still released via the release
// func returned by Allow(), ensuring no slot leaks.
