// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Registry manages the ARD catalog and provides discovery services.
type Registry struct {
	mu      sync.RWMutex
	catalog *AICatalog
	config  *RegistryConfig
	
	// Indexes for fast lookup
	byType        map[string][]*CatalogEntry
	byCapability  map[string][]*CatalogEntry
	byTag         map[string][]*CatalogEntry
	
	// HTTP server for serving the API
	server *RegistryServer
}

// RegistryConfig holds configuration for the registry.
type RegistryConfig struct {
	// Host information
	DisplayName string
	Identifier  string // DID or FQDN
	
	// Paths
	CatalogPath string // Path to ai-catalog.json
	CertPath   string // TLS certificate (optional)
	KeyPath    string // TLS key (optional)
	
	// API settings
	Port         int
	EnableSearch bool
	EnableExplore bool
	EnableList   bool
	
	// Search settings
	MaxResults int
	DefaultLimit int
}

// DefaultRegistryConfig returns default registry configuration.
func DefaultRegistryConfig() *RegistryConfig {
	return &RegistryConfig{
		DisplayName:  "Wukong AI Agent",
		Identifier:   "did:web:wukong.local",
		Port:         8080,
		EnableSearch: true,
		EnableExplore: true,
		EnableList:   true,
		MaxResults:   100,
		DefaultLimit: 20,
	}
}

// NewRegistry creates a new ARD registry.
func NewRegistry(config *RegistryConfig) (*Registry, error) {
	if config == nil {
		config = DefaultRegistryConfig()
	}
	
	// Create catalog
	var catalog *AICatalog
	
	// Try to load existing catalog
	if config.CatalogPath != "" {
		data, err := os.ReadFile(config.CatalogPath)
		if err == nil {
			catalog = &AICatalog{}
			if err := json.Unmarshal(data, catalog); err != nil {
				// Invalid catalog, start fresh
				catalog = NewAICatalog(config.DisplayName, config.Identifier)
			}
		} else {
			catalog = NewAICatalog(config.DisplayName, config.Identifier)
		}
	} else {
		catalog = NewAICatalog(config.DisplayName, config.Identifier)
	}
	
	// Create indexes
	indexes := NewIndexes(catalog.Entries)
	
	return &Registry{
		catalog: catalog,
		config:  config,
		byType:  indexes.byType,
		byCapability: indexes.byCapability,
		byTag:   indexes.byTag,
	}, nil
}

// GetCatalog returns the catalog.
func (r *Registry) GetCatalog() *AICatalog {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.catalog
}

// Register registers a new catalog entry.
func (r *Registry) Register(entry CatalogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Validate entry
	if !entry.IsValid() {
		return fmt.Errorf("invalid catalog entry: missing required fields or has both URL and Data")
	}
	
	// Validate URN
	if err := ValidateURN(entry.Identifier); err != nil {
		return fmt.Errorf("invalid URN: %w", err)
	}
	
	// Check for duplicate
	if r.catalog.GetEntry(entry.Identifier) != nil {
		return fmt.Errorf("entry already exists: %s", entry.Identifier)
	}
	
	// Add timestamp if not present
	if entry.UpdatedAt == "" {
		entry.UpdatedAt = Now()
	}
	
	// Add to catalog
	r.catalog.Entries = append(r.catalog.Entries, entry)
	
	// Update indexes
	r.rebuildIndexes()
	
	// Save catalog
	return r.saveCatalog()
}

// Update updates an existing catalog entry.
func (r *Registry) Update(identifier string, entry CatalogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Find existing entry
	existing := r.catalog.GetEntry(identifier)
	if existing == nil {
		return fmt.Errorf("entry not found: %s", identifier)
	}
	
	// Preserve some fields
	entry.Identifier = identifier
	entry.UpdatedAt = Now()
	
	// Update entry
	*r.catalog.GetEntry(identifier) = entry
	
	// Update indexes
	r.rebuildIndexes()
	
	// Save catalog
	return r.saveCatalog()
}

// Unregister removes a catalog entry.
func (r *Registry) Unregister(identifier string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if !r.catalog.RemoveEntry(identifier) {
		return fmt.Errorf("entry not found: %s", identifier)
	}
	
	// Update indexes
	r.rebuildIndexes()
	
	// Save catalog
	return r.saveCatalog()
}

// GetEntry retrieves an entry by identifier.
func (r *Registry) GetEntry(identifier string) *CatalogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.catalog.GetEntry(identifier)
}

// Search performs a search query.
func (r *Registry) Search(req *SearchRequest) (*SearchResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	limit := r.config.DefaultLimit
	if req.Limit > 0 && req.Limit <= r.config.MaxResults {
		limit = req.Limit
	}
	
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	
	// Build filter
	filter := EntryFilter{
		Type:         req.Filters.Type,
		Tags:         req.Filters.Tags,
		Capabilities: req.Filters.Capabilities,
		Query:        req.Query,
	}
	
	// Filter entries
	entries := r.catalog.FilterEntries(filter)
	
	// Calculate total
	total := len(entries)
	
	// Apply pagination
	if offset >= total {
		entries = []CatalogEntry{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		entries = entries[offset:end]
	}
	
	// Build results with simple scoring
	results := make([]SearchResult, len(entries))
	for i, entry := range entries {
		results[i] = SearchResult{
			Identifier:  entry.Identifier,
			DisplayName: entry.DisplayName,
			Type:        entry.Type,
			URL:         entry.URL,
			Description: entry.Description,
			Score:       calculateScore(&entry, req.Query),
		}
	}
	
	// Sort by score
	sortResults(results)
	
	return &SearchResponse{
		Results: results,
		Total:  total,
		Query:  req.Query,
	}, nil
}

// Explore returns entries matching the explore criteria.
func (r *Registry) Explore(req *ExploreRequest) (*ExploreResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	limit := r.config.DefaultLimit
	if req.Limit > 0 {
		limit = req.Limit
	}
	
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	
	// Build filter
	filter := EntryFilter{
		Type: req.Type,
		Tags: req.Tags,
	}
	
	entries := r.catalog.FilterEntries(filter)
	total := len(entries)
	
	// Apply pagination
	if offset >= total {
		entries = []CatalogEntry{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		entries = entries[offset:end]
	}
	
	return &ExploreResponse{
		Entries: entries,
		Total:   total,
	}, nil
}

// List returns all catalog entries.
func (r *Registry) List(limit, offset int) (*ListResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	if limit <= 0 || limit > r.config.MaxResults {
		limit = r.config.DefaultLimit
	}
	
	if offset < 0 {
		offset = 0
	}
	
	entries := r.catalog.Entries
	total := len(entries)
	
	// Apply pagination
	if offset >= total {
		entries = []CatalogEntry{}
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		entries = entries[offset:end]
	}
	
	return &ListResponse{
		Entries: entries,
		Total:   total,
	}, nil
}

// Save saves the catalog to disk.
func (r *Registry) saveCatalog() error {
	if r.config.CatalogPath == "" {
		return nil // No persistence configured
	}
	
	// Ensure directory exists
	dir := filepath.Dir(r.config.CatalogPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create catalog directory: %w", err)
	}
	
	data, err := json.MarshalIndent(r.catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	
	if err := os.WriteFile(r.config.CatalogPath, data, 0644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	
	return nil
}

// rebuildIndexes rebuilds the lookup indexes.
func (r *Registry) rebuildIndexes() {
	r.byType = make(map[string][]*CatalogEntry)
	r.byCapability = make(map[string][]*CatalogEntry)
	r.byTag = make(map[string][]*CatalogEntry)
	
	for i := range r.catalog.Entries {
		entry := &r.catalog.Entries[i]
		
		// Index by type
		r.byType[entry.Type] = append(r.byType[entry.Type], entry)
		
		// Index by capability
		for _, cap := range entry.Capabilities {
			r.byCapability[cap] = append(r.byCapability[cap], entry)
		}
		
		// Index by tag
		for _, tag := range entry.Tags {
			r.byTag[tag] = append(r.byTag[tag], entry)
		}
	}
}

// Indexes holds lookup indexes for fast queries.
type Indexes struct {
	byType        map[string][]*CatalogEntry
	byCapability map[string][]*CatalogEntry
	byTag        map[string][]*CatalogEntry
}

// NewIndexes creates indexes from catalog entries.
func NewIndexes(entries []CatalogEntry) *Indexes {
	indexes := &Indexes{
		byType:        make(map[string][]*CatalogEntry),
		byCapability: make(map[string][]*CatalogEntry),
		byTag:        make(map[string][]*CatalogEntry),
	}
	
	for i := range entries {
		entry := &entries[i]
		
		// Index by type
		indexes.byType[entry.Type] = append(indexes.byType[entry.Type], entry)
		
		// Index by capability
		for _, cap := range entry.Capabilities {
			indexes.byCapability[cap] = append(indexes.byCapability[cap], entry)
		}
		
		// Index by tag
		for _, tag := range entry.Tags {
			indexes.byTag[tag] = append(indexes.byTag[tag], entry)
		}
	}
	
	return indexes
}

// calculateScore calculates a relevance score for an entry given a query.
func calculateScore(entry *CatalogEntry, query string) float64 {
	if query == "" {
		return 1.0 // Default score for no query
	}
	
	queryLower := lowercase(query)
	score := 0.0
	
	// Check display name match (highest weight)
	if contains(lowercase(entry.DisplayName), queryLower) {
		score += 0.4
	}
	
	// Check description match
	if contains(lowercase(entry.Description), queryLower) {
		score += 0.3
	}
	
	// Check tags match
	for _, tag := range entry.Tags {
		if contains(lowercase(tag), queryLower) {
			score += 0.2
			break
		}
	}
	
	// Check capabilities match
	for _, cap := range entry.Capabilities {
		if contains(lowercase(cap), queryLower) {
			score += 0.1
			break
		}
	}
	
	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}
	
	return score
}

// sortResults sorts search results by score descending.
func sortResults(results []SearchResult) {
	// Simple bubble sort for small result sets
	for i := 0; i < len(results)-1; i++ {
		for j := 0; j < len(results)-i-1; j++ {
			if results[j].Score < results[j+1].Score {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}
