// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/km269/wukong/internal/util"
)

// ToolSet provides ARD discovery tools.
type ToolSet struct {
	client  *Client
	manager *CatalogManager

	mu           sync.RWMutex
	registryURLs []string // Remote registry URLs for federated search
}

// NewToolSet creates a new ARD tool set.
func NewToolSet(registryURL string, catalogPath string) (*ToolSet, error) {
	ts := &ToolSet{
		client:      NewClient(30),
		registryURLs: []string{},
	}

	var err error
	ts.manager, err = NewCatalogManager(catalogPath)
	if err != nil {
		return nil, err
	}

	// Initialize with built-in entries
	if len(ts.manager.List()) == 0 {
		for _, entry := range WukongBuiltInEntries() {
			ts.manager.GetCatalog().AddEntry(entry)
		}
	}

	// Set initial registry URL if provided.
	if registryURL != "" {
		if err := ts.SetRegistryURL(registryURL); err != nil {
			util.Logger.Warn("ard: invalid initial registry URL",
				slog.String("url", registryURL),
				slog.String("error", err.Error()))
		}
	}

	return ts, nil
}

// SetRegistryURL sets the remote registry URL for federated discovery.
// The URL is validated and appended to the list of known registries.
// Duplicate URLs are silently ignored.
func (ts *ToolSet) SetRegistryURL(rawURL string) error {
	// Validate URL format.
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid registry URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf(
			"unsupported URL scheme %q: must be http or https",
			parsed.Scheme)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Deduplicate.
	for _, existing := range ts.registryURLs {
		if existing == rawURL {
			return nil
		}
	}

	ts.registryURLs = append(ts.registryURLs, rawURL)
	util.Logger.Info("ard: remote registry URL added",
		slog.String("url", rawURL),
		slog.Int("total", len(ts.registryURLs)))
	return nil
}

// RegistryURLs returns the list of configured remote registry URLs.
func (ts *ToolSet) RegistryURLs() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	result := make([]string, len(ts.registryURLs))
	copy(result, ts.registryURLs)
	return result
}

// Search searches the local catalog and optionally remote registries.
// When remote registry URLs are configured, results from federated
// search are merged with local results.
func (ts *ToolSet) Search(
	ctx context.Context, query string, filters map[string]any,
) (*SearchResponse, error) {
	req := &SearchRequest{
		Query: query,
		Limit: 20,
	}

	// Apply filters
	if filters != nil {
		if filters["type"] != nil {
			req.Filters.Type = filters["type"].(string)
		}
		if filters["capabilities"] != nil {
			if caps, ok := filters["capabilities"].([]string); ok {
				req.Filters.Capabilities = caps
			}
		}
		if filters["tags"] != nil {
			if tags, ok := filters["tags"].([]string); ok {
				req.Filters.Tags = tags
			}
		}
	}

	// Search local catalog first.
	catalog := ts.manager.GetCatalog()
	entries := catalog.FilterEntries(EntryFilter{
		Query: req.Query,
		Type:  req.Filters.Type,
	})

	results := make([]SearchResult, 0, len(entries))
	seen := make(map[string]bool) // Deduplicate by identifier string.

	for _, entry := range entries {
		urnStr := entry.Identifier
		seen[urnStr] = true
		results = append(results, SearchResult{
			Identifier:  entry.Identifier,
			DisplayName: entry.DisplayName,
			Type:        entry.Type,
			URL:         entry.URL,
			Description: entry.Description,
			Score:       calculateScore(&entry, req.Query),
		})
	}

	// Federated search across remote registries.
	ts.mu.RLock()
	registryURLs := make([]string, len(ts.registryURLs))
	copy(registryURLs, ts.registryURLs)
	ts.mu.RUnlock()

	for _, remoteURL := range registryURLs {
		resp, err := ts.client.Search(ctx, remoteURL, req)
		if err != nil {
			util.Logger.Warn("ard: remote registry search failed",
				slog.String("url", remoteURL),
				slog.String("error", err.Error()))
			continue
		}
		for _, r := range resp.Results {
			urnStr := r.Identifier
			if seen[urnStr] {
				continue // Skip duplicates.
			}
			seen[urnStr] = true
			results = append(results, r)
		}
	}

	return &SearchResponse{
		Results: results,
		Total:   len(results),
		Query:   query,
	}, nil
}

// Discover discovers resources from remote registries.
func (ts *ToolSet) Discover(
	ctx context.Context, query string,
) ([]SearchResult, error) {
	// Search local first
	resp, err := ts.Search(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	return resp.Results, nil
}

// List lists all registered resources.
func (ts *ToolSet) List() []CatalogEntry {
	return ts.manager.List()
}

// Get gets a specific resource by identifier.
func (ts *ToolSet) Get(identifier string) *CatalogEntry {
	return ts.manager.Get(identifier)
}

// Register registers a new resource.
func (ts *ToolSet) Register(entry CatalogEntry) error {
	ts.manager.GetCatalog().AddEntry(entry)
	return nil
}

// Unregister unregisters a resource.
func (ts *ToolSet) Unregister(identifier string) error {
	return ts.manager.Remove(identifier)
}

// ExportCatalog exports the catalog as JSON.
func (ts *ToolSet) ExportCatalog() ([]byte, error) {
	return json.MarshalIndent(ts.manager.GetCatalog(), "", "  ")
}

// ANPPublishOptions configures ANP features for registry publishing.
// When ANP is enabled, the registry server additionally exposes:
//   - /.well-known/agent-descriptions (ActivityStreams CollectionPage)
//   - /agents/{name}/ad.json (ADP documents)
type ANPPublishOptions struct {
	// Enabled controls ANP discovery endpoints.
	Enabled bool
	// BaseURL is the public base URL of the registry server,
	// used in ADP document self-references.
	BaseURL string
}

// PublishAndServe creates an ARD Registry, injects Wukong built-in
// entries, and starts a RegistryServer on the configured port so
// that other ARD-compatible AI Agents can discover Wukong.
//
// The returned RegistryServer can be shut down with Shutdown(ctx).
// If publishPort is 0, no server is started and nil is returned.
//
// When anpOpts is non-nil and Enabled=true, the server also exposes
// ANP discovery endpoints (ADP documents + agent descriptions).
func PublishAndServe(ctx context.Context, publishPort int,
	catalogPath string, anpOpts *ANPPublishOptions) (*RegistryServer, error) {
	if publishPort <= 0 {
		return nil, nil
	}

	config := DefaultRegistryConfig()
	config.Port = publishPort
	config.CatalogPath = catalogPath

	r, err := NewRegistryWithEntries(config)
	if err != nil {
		return nil, err
	}

	server := NewRegistryServer(r, config)

	// Register ANP discovery endpoints if enabled.
	if anpOpts != nil && anpOpts.Enabled {
		adpCfg := DefaultADPBuildConfig()
		adpBuilder := NewUnsignedADPBuilder(adpCfg)
		anpCfg := &ANPDiscoveryConfig{
			Registry:   r,
			ADPBuilder: adpBuilder,
			BaseURL:    anpOpts.BaseURL,
			PageSize:   50,
		}
		anpHandler := NewANPDiscoveryHandler(anpCfg)
		server.WithANPDiscovery(anpHandler)
	}

	go func() {
		util.Logger.Info("ard: registry server starting",
			"port", publishPort,
			"endpoint", "/.well-known/ai-catalog.json")
		if err := server.Start(ctx); err != nil &&
			err.Error() != "http: Server closed" {
			util.Logger.Warn("ard: registry server stopped",
				"error", err.Error())
		}
	}()

	return server, nil
}

// NewRegistryWithEntries creates a Registry pre-populated with
// Wukong's built-in entries, making the catalog immediately
// useful when served to other ARD-compatible agents.
func NewRegistryWithEntries(config *RegistryConfig) (*Registry, error) {
	r, err := NewRegistry(config)
	if err != nil {
		return nil, err
	}

	for _, entry := range WukongBuiltInEntries() {
		if r.GetEntry(entry.Identifier) != nil {
			continue // Already registered
		}
		if err := r.Register(entry); err != nil {
			util.Logger.Warn("ard: failed to register built-in entry",
				"identifier", entry.Identifier,
				"error", err.Error())
		}
	}

	util.Logger.Info("ard: registry initialized with built-in entries",
		"count", len(WukongBuiltInEntries()))
	return r, nil
}

// RegisterA2AAgent registers an A2A remote agent in the ARD catalog
// for federated discovery. It is a convenience function that creates
// a properly structured CatalogEntry from A2A connection metadata.
//
// This is called during session bootstrap when A2A remotes are
// configured, enabling other agents to discover them via ARD.
func RegisterA2AAgent(ts *ToolSet, name, description, serverURL string) {
	entry := CatalogEntry{
		Identifier:   "urn:air:wukong.local:agent:" + name,
		DisplayName:  name,
		Type:         MediaTypeA2AAgentCard,
		URL:          serverURL,
		Description:  description,
		Tags:         []string{"a2a", "remote", "agent"},
		Capabilities: []string{"a2a"},
	}
	if err := ts.Register(entry); err != nil {
		util.Logger.Warn("ard: failed to register A2A agent",
			"name", name,
			"error", err.Error())
	} else {
		util.Logger.Info("ard: A2A agent registered to catalog",
			"name", name,
			"identifier", entry.Identifier)
	}
}

// ImportCatalog imports a catalog from JSON and merges entries
// with the existing catalog. Entries with the same URN are
// updated; new entries are appended.
//
// Returns the number of entries imported (new + updated).
func (ts *ToolSet) ImportCatalog(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty catalog data")
	}

	var imported AICatalog
	if err := json.Unmarshal(data, &imported); err != nil {
		return fmt.Errorf("parse catalog JSON: %w", err)
	}

	existing := ts.manager.GetCatalog()

	var importedCount, updatedCount int
	for _, entry := range imported.Entries {
		urnStr := entry.Identifier

		if existing.GetEntry(urnStr) != nil {
			// Update existing entry.
			existing.UpdateEntry(urnStr, entry)
			updatedCount++
		} else {
			// Append new entry.
			existing.AddEntry(entry)
			importedCount++
		}
	}

	util.Logger.Info("ard: catalog imported",
		slog.Int("imported", importedCount),
		slog.Int("updated", updatedCount),
		slog.Int("total_entries", len(existing.Entries)))

	return nil
}
