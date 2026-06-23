// Package clone provides website cloning functionality.
package clone

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CloneCache provides caching for cloned websites to support incremental updates.
type CloneCache struct {
	mu       sync.RWMutex
	manifest *CacheManifest
	cacheDir string
	httpClient *http.Client
}

// CacheManifest stores metadata about cached pages.
type CacheManifest struct {
	SeedURL    string           `json:"seedURL"`
	Host       string           `json:"host"`
	LastSync   time.Time        `json:"lastSync"`
	ETag       string           `json:"etag,omitempty"`
	Entries    map[string]*CacheEntry `json:"entries"`
}

// CacheEntry represents a cached page or resource.
type CacheEntry struct {
	URL          string    `json:"url"`
	LocalPath    string    `json:"localPath"`
	ContentHash  string    `json:"contentHash"`
	LastFetched  time.Time `json:"lastFetched"`
	LastModified string    `json:"lastModified,omitempty"`
	ETag         string    `json:"etag,omitempty"`
	StatusCode   int       `json:"statusCode"`
	ContentType  string    `json:"contentType"`
	Size         int64     `json:"size"`
}

// NewCloneCache creates a new clone cache.
func NewCloneCache(cacheDir, host string) (*CloneCache, error) {
	if cacheDir == "" {
		homeDir, _ := os.UserHomeDir()
		cacheDir = filepath.Join(homeDir, ".wukong_apps", "clone_cache", host)
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	cache := &CloneCache{
		cacheDir: cacheDir,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		manifest: &CacheManifest{
			Host:    host,
			Entries: make(map[string]*CacheEntry),
		},
	}

	// Load existing manifest
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(data, cache.manifest); err != nil {
			// Invalid manifest, start fresh
			cache.manifest = &CacheManifest{
				Host:    host,
				Entries: make(map[string]*CacheEntry),
			}
		}
	}

	return cache, nil
}

// SetSeedURL sets the seed URL for this cache.
func (c *CloneCache) SetSeedURL(seedURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifest.SeedURL = seedURL
}

// GetEntry returns a cache entry for a URL.
func (c *CloneCache) GetEntry(url string) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.manifest.Entries[url]
	if !ok {
		return nil
	}
	return entry
}

// SetEntry stores a cache entry.
func (c *CloneCache) SetEntry(entry *CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifest.Entries[entry.URL] = entry
}

// CheckNeedsUpdate checks if a URL needs to be re-fetched based on ETag/Last-Modified.
func (c *CloneCache) CheckNeedsUpdate(url string) (bool, string, error) {
	entry := c.GetEntry(url)
	if entry == nil {
		return true, "not cached", nil
	}

	// Create HEAD request to check headers
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return true, "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Wukong-Cloner/1.0)")

	// Add conditional headers
	if entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}
	if entry.LastModified != "" {
		req.Header.Set("If-Modified-Since", entry.LastModified)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, "", err
	}
	defer resp.Body.Close()

	// Check if content changed
	if resp.StatusCode == http.StatusNotModified {
		return false, "not modified", nil
	}

	// Check ETag
	if etag := resp.Header.Get("ETag"); etag != "" && etag == entry.ETag {
		return false, "etag match", nil
	}

	// Check Last-Modified
	if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
		if lastModified == entry.LastModified {
			return false, "last-modified match", nil
		}
	}

	return true, "content changed", nil
}

// UpdateEntry updates a cache entry after fetching.
func (c *CloneCache) UpdateEntry(url string, resp *http.Response, content []byte, localPath string) {
	entry := &CacheEntry{
		URL:          url,
		LocalPath:    localPath,
		LastFetched:  time.Now(),
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		Size:         int64(len(content)),
		ContentHash:  hashContent(content),
	}

	// Store cache headers
	if etag := resp.Header.Get("ETag"); etag != "" {
		entry.ETag = etag
	}
	if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
		entry.LastModified = lastModified
	}

	c.SetEntry(entry)
}

// UpdateLastSync updates the last sync time.
func (c *CloneCache) UpdateLastSync() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifest.LastSync = time.Now()
}

// Save saves the cache manifest to disk.
func (c *CloneCache) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	manifestPath := filepath.Join(c.cacheDir, "manifest.json")
	data, err := json.MarshalIndent(c.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

// GetManifest returns a copy of the manifest.
func (c *CloneCache) GetManifest() *CacheManifest {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy
	manifest := &CacheManifest{
		SeedURL:   c.manifest.SeedURL,
		Host:      c.manifest.Host,
		LastSync:  c.manifest.LastSync,
		ETag:      c.manifest.ETag,
		Entries:   make(map[string]*CacheEntry),
	}

	for k, v := range c.manifest.Entries {
		entryCopy := *v
		manifest.Entries[k] = &entryCopy
	}

	return manifest
}

// GetChangedURLs returns URLs that have changed since last sync.
func (c *CloneCache) GetChangedURLs(urls []string) ([]string, error) {
	var changed []string

	for _, url := range urls {
		needsUpdate, _, err := c.CheckNeedsUpdate(url)
		if err != nil {
			continue
		}
		if needsUpdate {
			changed = append(changed, url)
		}
	}

	return changed, nil
}

// Clear removes all cached entries.
func (c *CloneCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.manifest = &CacheManifest{
		Host:    c.manifest.Host,
		Entries: make(map[string]*CacheEntry),
	}

	return c.Save()
}

// GetCacheDir returns the cache directory path.
func (c *CloneCache) GetCacheDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cacheDir
}

// hashContent generates a hash for content.
func hashContent(content []byte) string {
	hash := md5.Sum(content)
	return fmt.Sprintf("md5:%x", hash)
}

// IsCacheValid checks if the cache is valid for a given seed URL.
func (c *CloneCache) IsCacheValid(seedURL string, maxAge time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.manifest.SeedURL != seedURL {
		return false
	}

	if c.manifest.LastSync.IsZero() {
		return false
	}

	// Check if cache is too old
	if time.Since(c.manifest.LastSync) > maxAge {
		return false
	}

	return true
}
