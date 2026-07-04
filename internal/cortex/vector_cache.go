package cortex

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

const (
	defaultCacheTTL        = 5 * time.Minute
	defaultMaxEntries      = 1000
	defaultCleanupInterval = 1 * time.Minute
)

type VectorCacheEntry struct {
	Vector    []float32
	Timestamp time.Time
}

type VectorCache struct {
	mu              sync.RWMutex
	messageCache    map[string]*VectorCacheEntry
	queryCache      map[string]*VectorCacheEntry
	cacheTTL        time.Duration
	maxEntries      int
	cleanupInterval time.Duration
	stop            chan struct{}
	isRunning       bool
	hits            int64
	misses          int64
}

func NewVectorCache(opts ...VectorCacheOption) *VectorCache {
	cache := &VectorCache{
		messageCache:    make(map[string]*VectorCacheEntry),
		queryCache:      make(map[string]*VectorCacheEntry),
		cacheTTL:        defaultCacheTTL,
		maxEntries:      defaultMaxEntries,
		cleanupInterval: defaultCleanupInterval,
		stop:            make(chan struct{}),
	}

	for _, opt := range opts {
		opt(cache)
	}

	cache.startCleanup()
	util.Logger.Info("cortex: vector cache initialized",
		slog.String("ttl", cache.cacheTTL.String()),
		slog.Int("max_entries", cache.maxEntries))

	return cache
}

type VectorCacheOption func(*VectorCache)

func WithCacheTTL(ttl time.Duration) VectorCacheOption {
	return func(c *VectorCache) {
		c.cacheTTL = ttl
	}
}

func WithMaxEntries(max int) VectorCacheOption {
	return func(c *VectorCache) {
		c.maxEntries = max
	}
}

func WithCleanupInterval(interval time.Duration) VectorCacheOption {
	return func(c *VectorCache) {
		c.cleanupInterval = interval
	}
}

func (c *VectorCache) startCleanup() {
	if c.isRunning {
		return
	}
	c.isRunning = true

	go func() {
		ticker := time.NewTicker(c.cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.cleanup()
			case <-c.stop:
				return
			}
		}
	}()
}

func (c *VectorCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expired := 0

	for key, entry := range c.messageCache {
		if now.Sub(entry.Timestamp) > c.cacheTTL {
			delete(c.messageCache, key)
			expired++
		}
	}

	for key, entry := range c.queryCache {
		if now.Sub(entry.Timestamp) > c.cacheTTL {
			delete(c.queryCache, key)
			expired++
		}
	}

	if expired > 0 {
		util.Logger.Debug("cortex: vector cache cleanup completed",
			slog.Int("expired", expired),
			slog.Int("message_cache_size", len(c.messageCache)),
			slog.Int("query_cache_size", len(c.queryCache)))
	}
}

func (c *VectorCache) Stop() {
	if !c.isRunning {
		return
	}
	close(c.stop)
	c.isRunning = false

	util.Logger.Info("cortex: vector cache stopped",
		slog.Int64("hits", c.hits),
		slog.Int64("misses", c.misses),
		slog.Float64("hit_rate", c.HitRate()))
}

func (c *VectorCache) GetMessageVector(key string) ([]float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.messageCache[key]
	if !ok {
		c.misses++
		return nil, false
	}

	if time.Since(entry.Timestamp) > c.cacheTTL {
		c.misses++
		return nil, false
	}

	c.hits++
	return entry.Vector, true
}

func (c *VectorCache) SetMessageVector(key string, vector []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.messageCache) >= c.maxEntries {
		c.evictOldest(c.messageCache)
	}

	c.messageCache[key] = &VectorCacheEntry{
		Vector:    vector,
		Timestamp: time.Now(),
	}
}

func (c *VectorCache) GetQueryVector(query string) ([]float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.queryCache[query]
	if !ok {
		c.misses++
		return nil, false
	}

	if time.Since(entry.Timestamp) > c.cacheTTL {
		c.misses++
		return nil, false
	}

	c.hits++
	return entry.Vector, true
}

func (c *VectorCache) SetQueryVector(query string, vector []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.queryCache) >= c.maxEntries {
		c.evictOldest(c.queryCache)
	}

	c.queryCache[query] = &VectorCacheEntry{
		Vector:    vector,
		Timestamp: time.Now(),
	}
}

func (c *VectorCache) evictOldest(cache map[string]*VectorCacheEntry) {
	if len(cache) == 0 {
		return
	}

	var oldestKey string
	var oldestTime time.Time

	for key, entry := range cache {
		if oldestKey == "" || entry.Timestamp.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.Timestamp
		}
	}

	delete(cache, oldestKey)
}

func (c *VectorCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messageCache = make(map[string]*VectorCacheEntry)
	c.queryCache = make(map[string]*VectorCacheEntry)
	c.hits = 0
	c.misses = 0
}

func (c *VectorCache) HitRate() float64 {
	total := c.hits + c.misses
	if total == 0 {
		return 0.0
	}
	return float64(c.hits) / float64(total)
}

func (c *VectorCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"hits":               c.hits,
		"misses":             c.misses,
		"hit_rate":           c.HitRate(),
		"message_cache_size": len(c.messageCache),
		"query_cache_size":   len(c.queryCache),
		"cache_ttl":          c.cacheTTL.String(),
		"max_entries":        c.maxEntries,
	}
}

func (c *VectorCache) GetOrComputeMessageVector(
	ctx context.Context,
	key string,
	text string,
	embedder func(ctx context.Context, texts []string) ([][]float64, error),
) ([]float32, error) {
	if vector, ok := c.GetMessageVector(key); ok {
		return vector, nil
	}

	vecs, err := embedder(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("embed message: %w", err)
	}

	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, nil
	}

	vector := vecToFloat32(vecs[0])
	c.SetMessageVector(key, vector)
	return vector, nil
}

func (c *VectorCache) GetOrComputeQueryVector(
	ctx context.Context,
	query string,
	embedder func(ctx context.Context, texts []string) ([][]float64, error),
) ([]float32, error) {
	if vector, ok := c.GetQueryVector(query); ok {
		return vector, nil
	}

	vecs, err := embedder(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, nil
	}

	vector := vecToFloat32(vecs[0])
	c.SetQueryVector(query, vector)
	return vector, nil
}

func (c *VectorCache) BatchGetOrComputeMessageVectors(
	ctx context.Context,
	items map[string]string,
	embedder func(ctx context.Context, texts []string) ([][]float64, error),
) (map[string][]float32, error) {
	result := make(map[string][]float32)
	var missKeys []string
	var missTexts []string

	c.mu.RLock()
	for key, text := range items {
		if vector, ok := c.messageCache[key]; ok && time.Since(vector.Timestamp) <= c.cacheTTL {
			result[key] = vector.Vector
			c.hits++
		} else {
			missKeys = append(missKeys, key)
			missTexts = append(missTexts, text)
			c.misses++
		}
	}
	c.mu.RUnlock()

	if len(missKeys) == 0 {
		return result, nil
	}

	vecs, err := embedder(ctx, missTexts)
	if err != nil {
		return nil, fmt.Errorf("batch embed messages: %w", err)
	}

	if len(vecs) != len(missKeys) {
		return nil, fmt.Errorf("batch embed returned %d vectors for %d texts",
			len(vecs), len(missKeys))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for i, key := range missKeys {
		if len(vecs[i]) > 0 {
			vector := vecToFloat32(vecs[i])
			result[key] = vector

			if len(c.messageCache) >= c.maxEntries {
				c.evictOldest(c.messageCache)
			}
			c.messageCache[key] = &VectorCacheEntry{
				Vector:    vector,
				Timestamp: time.Now(),
			}
		}
	}

	return result, nil
}
