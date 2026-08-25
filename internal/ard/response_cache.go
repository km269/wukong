package ard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type ResponseCache struct {
	mu    sync.RWMutex
	items map[string]*cacheItem
	ttl   time.Duration
}

type cacheItem struct {
	data     []byte
	expireAt time.Time
}

func NewResponseCache(ttl time.Duration) *ResponseCache {
	return &ResponseCache{
		items: make(map[string]*cacheItem),
		ttl:   ttl,
	}
}

func (c *ResponseCache) key(url string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(url))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *ResponseCache) Get(url string, body []byte) ([]byte, bool) {
	c.mu.RLock()
	key := c.key(url, body)
	item, ok := c.items[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Now().After(item.expireAt) {
		return nil, false
	}
	return item.data, true
}

func (c *ResponseCache) Set(url string, body, data []byte) {
	c.mu.Lock()
	key := c.key(url, body)
	c.items[key] = &cacheItem{
		data:     data,
		expireAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *ResponseCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, item := range c.items {
		if now.After(item.expireAt) {
			delete(c.items, key)
		}
	}
}

func (c *ResponseCache) StartCleanup(interval time.Duration) {
	c.StartCleanupCtx(context.Background(), interval)
}

// StartCleanupCtx is like StartCleanup but stops the cleanup
// goroutine when ctx is cancelled, preventing goroutine leaks in
// long-running servers. Returns a stop function for explicit
// shutdown when the caller does not have a ctx to cancel.
func (c *ResponseCache) StartCleanupCtx(ctx context.Context, interval time.Duration) (stop func()) {
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				c.Cleanup()
			}
		}
	}()
	return func() { close(stopCh) }
}
