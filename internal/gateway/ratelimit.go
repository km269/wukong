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

	// Semaphore-style concurrency control.
	concurrent int
	cond       *sync.Cond
	ticker     *time.Ticker
	stopCh     chan struct{}
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
	rl.cond = sync.NewCond(&rl.mu)

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

// Allow checks whether a request from a given platform user is
// allowed.  It first checks the per-user rate limit, then acquires a
// concurrency slot (if maxConcurrent > 0).
//
// On success it returns a release func that the caller must invoke
// after the agent run completes.  On failure it returns an error
// describing the rejection reason.
func (rl *RateLimiter) Allow(
	platform, userID string,
) (release func(), allowed bool) {
	if rl.maxReqPerWin <= 0 && rl.maxConcurrent <= 0 {
		return func() {}, true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// 1. Per-user rate check.
	if rl.maxReqPerWin > 0 {
		key := platform + ":" + userID
		sw := rl.users[key]
		if sw == nil {
			sw = &slidingWindow{}
			rl.users[key] = sw
		}
		if !sw.allow(time.Now(), rl.window, rl.maxReqPerWin) {
			return nil, false
		}
	}

	// 2. Concurrency gate (block until a slot opens).
	if rl.maxConcurrent > 0 {
		for rl.concurrent >= rl.maxConcurrent {
			rl.cond.Wait()
		}
		rl.concurrent++
	}

	return func() { rl.release() }, true
}

// release decrements the concurrency counter and signals waiters.
func (rl *RateLimiter) release() {
	if rl.maxConcurrent <= 0 {
		return
	}
	rl.mu.Lock()
	rl.concurrent--
	rl.cond.Signal()
	rl.mu.Unlock()
}

// Stop cleanly shuts down the background cleanup goroutine.
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
	defer rl.mu.Unlock()

	return RateLimiterMetrics{
		ActiveUsers:   len(rl.users),
		Concurrent:    rl.concurrent,
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
