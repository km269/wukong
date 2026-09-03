// Package clone provides website cloning functionality.
//
// frontier.go: Crawl frontier with URL deduplication and state persistence.
// Manages a seen/visited set that supports safe concurrent access
// and JSON-based resume after interruption.
package clone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// frontier manages the set of URLs that have been enqueued or fully processed.
// It provides thread-safe deduplication and supports state save/load for
// resuming interrupted crawls.
type frontier struct {
	mu      sync.Mutex
	seen    map[string]bool // URLs that have been enqueued (including visited).
	visited map[string]bool // URLs that have been fully written to disk.
}

// newFrontier creates an empty frontier.
func newFrontier() *frontier {
	return &frontier{
		seen:    make(map[string]bool),
		visited: make(map[string]bool),
	}
}

// Offer checks whether a URL key has already been seen. If not, it marks it
// as seen and returns true, indicating the caller should process it.
// Concurrency-safe.
func (f *frontier) offer(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.seen[key] {
		return false
	}
	f.seen[key] = true
	return true
}

// MarkVisited records that a URL has been fully processed and written to disk.
// Concurrency-safe.
func (f *frontier) markVisited(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.visited[key] = true
}

// IsVisited returns true if a URL was fully processed in a previous session.
// Used during resume to skip already-completed pages.
func (f *frontier) isVisited(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.visited[key]
}

// VisitedCount returns the number of fully processed URLs.
func (f *frontier) visitedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.visited)
}

// SeenCount returns the number of URLs that have been enqueued.
func (f *frontier) seenCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// ---------------------------------------------------------------------------
// State persistence for resume support.
// ---------------------------------------------------------------------------

// frontierState is the serializable form of the frontier for JSON persistence.
type frontierState struct {
	Visited []string `json:"visited"`
}

// load restores the frontier state from a JSON file.
// If the file does not exist, this is a no-op.
func (f *frontier) load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No state to restore.
		}
		return fmt.Errorf("read frontier state: %w", err)
	}

	var state frontierState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshal frontier state: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for _, key := range state.Visited {
		f.visited[key] = true
		f.seen[key] = true // Already seen because it was visited.
	}

	return nil
}

// save persists the frontier state to a JSON file using atomic write
// (write to temp file, then rename) to avoid corruption.
func (f *frontier) save(path string) error {
	f.mu.Lock()

	// Collect and sort visited keys for deterministic output.
	keys := make([]string, 0, len(f.visited))
	for k := range f.visited {
		keys = append(keys, k)
	}
	f.mu.Unlock()

	sort.Strings(keys)

	state := frontierState{Visited: keys}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal frontier state: %w", err)
	}

	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create frontier dir: %w", err)
	}

	// Atomic write via temp file.
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write frontier tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename frontier state: %w", err)
	}

	return nil
}

// statePath returns the path to the frontier state file for a host.
func frontierStatePath(outputDir string) string {
	return filepath.Join(outputDir, reservedPrefix, "state.json")
}
