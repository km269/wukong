package ard

import (
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
	data      []byte
	expireAt  time.Time
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
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			c.Cleanup()
		}
	}()
}