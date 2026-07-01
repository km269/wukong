// Package gateway — Message Deduplication
//
// MessageDeduplicator provides thread-safe, TTL-based message dedup
// for all incoming platform messages. When a platform (e.g. WeCom or
// Feishu) retries a callback, it sends the same MessageID; the dedup
// layer catches those duplicates and returns silently without hitting
// the agent loop.
//
// Implementation:
//   - Keys are "platform:messageID" strings stored in a sync.Map.
//   - Each entry carries the insertion time; a background goroutine
//     periodically scans and evicts expired entries.
//   - Cleanup runs every 30s (or half the TTL, whichever is smaller).
//   - Default TTL is 5 minutes (configurable via MessageDedupTTL).
package gateway

import (
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

// MessageDeduplicator checks incoming message IDs and filters out
// duplicates within a configurable TTL window.
type MessageDeduplicator struct {
	mu     sync.Mutex
	kv     map[string]time.Time
	ttl    time.Duration
	ticker *time.Ticker
	stopCh chan struct{}
}

// NewMessageDeduplicator creates a deduplicator and starts the
// background eviction goroutine.
//
// ttl is the deduplication window (e.g. 5 * time.Minute). 0 means
// dedup is disabled (IsDuplicate always returns false).
func NewMessageDeduplicator(ttl time.Duration) *MessageDeduplicator {
	if ttl <= 0 {
		return &MessageDeduplicator{}
	}

	cleanupInterval := 30 * time.Second
	if ttl < 2*cleanupInterval {
		cleanupInterval = ttl / 2
	}

	md := &MessageDeduplicator{
		kv:     make(map[string]time.Time, 128),
		ttl:    ttl,
		ticker: time.NewTicker(cleanupInterval),
		stopCh: make(chan struct{}),
	}

	go md.evictLoop()

	util.Logger.Info("gateway: dedup started",
		slog.String("ttl", ttl.String()),
		slog.Duration("cleanup_interval", cleanupInterval),
	)
	return md
}

// IsDuplicate checks whether messageID has already been seen for the
// given platform within the dedup window.  Returns true if it is a
// duplicate (should be dropped).  Thread-safe.
//
// When dedup is disabled (ttl == 0) this always returns false.
func (md *MessageDeduplicator) IsDuplicate(
	platform, messageID string,
) bool {
	if md.ttl <= 0 || messageID == "" {
		return false
	}

	key := platform + ":" + messageID

	md.mu.Lock()
	defer md.mu.Unlock()

	if _, exists := md.kv[key]; exists {
		return true
	}
	md.kv[key] = time.Now()
	return false
}

// Size returns the current number of tracked message IDs.
func (md *MessageDeduplicator) Size() int {
	if md.ttl <= 0 {
		return 0
	}
	md.mu.Lock()
	defer md.mu.Unlock()
	return len(md.kv)
}

// Stop gracefully shuts down the background eviction goroutine.
func (md *MessageDeduplicator) Stop() {
	if md.ttl <= 0 {
		return
	}
	md.ticker.Stop()
	close(md.stopCh)
	util.Logger.Info("gateway: dedup stopped")
}

// evictLoop periodically removes expired entries.
func (md *MessageDeduplicator) evictLoop() {
	for {
		select {
		case <-md.ticker.C:
			md.evict()
		case <-md.stopCh:
			return
		}
	}
}

func (md *MessageDeduplicator) evict() {
	now := time.Now()
	md.mu.Lock()
	defer md.mu.Unlock()

	before := len(md.kv)
	for k, ts := range md.kv {
		if now.Sub(ts) > md.ttl {
			delete(md.kv, k)
		}
	}
	after := len(md.kv)
	if before != after {
		util.Logger.Debug("gateway: dedup eviction",
			slog.Int("before", before),
			slog.Int("after", after),
		)
	}
}
