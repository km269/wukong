// Package ard provides ANP-compatible agent discovery endpoints
// that allow Wukong agents to be discovered by ANP search agents
// and other ANP-compatible tooling.
//
// Endpoints:
//   GET /.well-known/agent-descriptions — CollectionPage with all agents
//   GET /agents/{name}/ad.json           — Per-agent ADP document
package ard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/km269/wukong/internal/util"
)

// ============================================================================
// ANP Discovery Handler
// ============================================================================

// ANPDiscoveryHandler serves the ANP-compatible agent discovery
// endpoints alongside the existing ARD ServerMux. It wraps an
// AICatalog and ADPBuilder to expose agents in the ANP format.
type ANPDiscoveryHandler struct {
	registry   *Registry
	adpBuilder *ADPBuilder
	baseURL    string
	pageSize   int
}

// ANPDiscoveryConfig configures the ANP discovery handler.
type ANPDiscoveryConfig struct {
	// Registry is the ARD registry backing agent discovery.
	Registry *Registry

	// ADPBuilder constructs ADP documents from catalog entries.
	ADPBuilder *ADPBuilder

	// BaseURL is the public base URL of the Wukong instance
	// (e.g., "https://wukong.example.com").
	BaseURL string

	// PageSize is the number of items per CollectionPage.
	// Default: 50.
	PageSize int
}

// NewANPDiscoveryHandler creates an ANP discovery handler.
func NewANPDiscoveryHandler(
	config *ANPDiscoveryConfig,
) *ANPDiscoveryHandler {
	if config.PageSize <= 0 {
		config.PageSize = 50
	}

	return &ANPDiscoveryHandler{
		registry:   config.Registry,
		adpBuilder: config.ADPBuilder,
		baseURL:    config.BaseURL,
		pageSize:   config.PageSize,
	}
}

// RegisterRoutes registers ANP discovery routes on an HTTP mux.
// Call this alongside the existing RegistryServer.setupRoutes()
// to add ANP-compatible endpoints without breaking existing ARD routes.
func (h *ANPDiscoveryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(
		ANPWellKnownPath,
		h.handleAgentDescriptions,
	)

	mux.HandleFunc(
		"/agents/",
		h.handleAgentADP,
	)
}

// handleAgentDescriptions serves GET /.well-known/agent-descriptions.
// Returns a CollectionPage listing all discoverable agents.
func (h *ANPDiscoveryHandler) handleAgentDescriptions(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(
			w, "Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Parse pagination parameters
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get all catalog entries and filter to only agent-type entries
	entries := h.registry.GetCatalog().Entries

	// Build CollectionPage
	collectionPage := h.buildCollectionPage(entries, page)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	json.NewEncoder(w).Encode(collectionPage)
}

// handleAgentADP serves GET /agents/{name}/ad.json.
// Returns the full ADP document for a specific agent.
func (h *ANPDiscoveryHandler) handleAgentADP(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(
			w, "Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	// Extract agent name from path: /agents/{name}/ad.json
	name := extractAgentName(r.URL.Path)
	if name == "" {
		http.Error(w, "Agent name required", http.StatusBadRequest)
		return
	}

	// Find the agent in the catalog or use the default ADP builder
	adpDoc := h.buildADPForAgent(name)
	if adpDoc == nil {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	json.NewEncoder(w).Encode(adpDoc)
}

// ============================================================================
// Internal Helpers
// ============================================================================

// buildCollectionPage constructs a paginated CollectionPage from
// catalog entries, mapping each to an ANP CollectionItem.
func (h *ANPDiscoveryHandler) buildCollectionPage(
	entries []CatalogEntry, page int,
) *CollectionPage {
	startIdx := (page - 1) * h.pageSize

	collection := &CollectionPage{
		Context: []string{
			"https://www.w3.org/ns/activitystreams",
			"https://schema.org",
		},
		Type:       "CollectionPage",
		URL:        fmt.Sprintf("%s%s", h.baseURL, ANPWellKnownPath),
		Items:      make([]CollectionItem, 0),
		TotalItems: len(entries),
	}

	// Paginate
	endIdx := min(startIdx+h.pageSize, len(entries))

	if startIdx >= len(entries) {
		return collection
	}

	// Build next page URL
	if endIdx < len(entries) {
		collection.Next = fmt.Sprintf(
			"%s%s?page=%d",
			h.baseURL, ANPWellKnownPath, page+1,
		)
	}

	// Build previous page URL
	if page > 1 {
		collection.Prev = fmt.Sprintf(
			"%s%s?page=%d",
			h.baseURL, ANPWellKnownPath, page-1,
		)
	}

	// Map each catalog entry to a CollectionItem
	for i := startIdx; i < endIdx; i++ {
		entry := entries[i]
		agentName := urlize(entry.DisplayName)

		item := CollectionItem{
			Type:    "ad:AgentDescription",
			Name:    entry.DisplayName,
			ID: fmt.Sprintf(
				"%s/agents/%s/ad.json",
				h.baseURL, agentName,
			),
			Summary: entry.Description,
		}

		// Include DID if available in trust manifest
		if entry.TrustManifest != nil &&
			entry.TrustManifest.IdentityType == "did" {
			item.DID = entry.TrustManifest.Identity
		}

		collection.Items = append(collection.Items, item)
	}

	return collection
}

// buildADPForAgent generates an ADP document for a specific agent
// identified by name. Uses the ADP builder when available, otherwise
// falls back to building from the catalog entry.
func (h *ANPDiscoveryHandler) buildADPForAgent(name string) *ADPDocument {
	// Try to find the entry in the catalog
	catalog := h.registry.GetCatalog()
	for i := range catalog.Entries {
		entry := &catalog.Entries[i]
		entryName := urlize(entry.DisplayName)
		if entryName == name {
			return h.adpFromCatalogEntry(entry)
		}
	}

	// Fall back to the default ADP document from the builder
	if h.adpBuilder != nil {
		doc := h.adpBuilder.Build()
		util.Logger.Debug("ADP: serving default agent description",
			slog.String("name", name),
		)
		return doc
	}

	return nil
}

// adpFromCatalogEntry builds an ADP document from a single
// CatalogEntry, converting the ARD format to ANP format.
func (h *ANPDiscoveryHandler) adpFromCatalogEntry(
	entry *CatalogEntry,
) *ADPDocument {
	config := CatalogEntryToADPBuildConfig(entry)
	config.BaseURL = h.baseURL

	// Preserve DID from trust manifest if available
	if entry.TrustManifest != nil &&
		entry.TrustManifest.IdentityType == "did" {
		config.DID = entry.TrustManifest.Identity
	}

	// Build a one-off ADP document without signing
	doc := &ADPDocument{
		ProtocolType:    ANPProtocolType,
		ProtocolVersion: ANPProtocolVersion,
		Type:           "AgentDescription",
		URL: fmt.Sprintf(
			"%s/agents/%s/ad.json",
			h.baseURL, urlize(entry.DisplayName),
		),
		Name:         entry.DisplayName,
		DID:          config.DID,
		Description:  entry.Description,
		Capabilities: entry.Capabilities,
		Tags:         entry.Tags,
		Version:      entry.Version,
		Interfaces: []AgentInterface{
			{
				Type:        "NaturalLanguageInterface",
				Protocol:    "YAML",
				URL:         entry.URL,
				Version:     "1.0",
				Description: entry.Description,
			},
		},
	}

	return doc
}

// ============================================================================
// Utility: Agent Name Extraction
// ============================================================================

// extractAgentName parses the agent name from a URL path like
// "/agents/{name}/ad.json" or "/agents/{name}".
func extractAgentName(path string) string {
	// Strip "/agents/" prefix
	const prefix = "/agents/"
	if len(path) <= len(prefix) {
		return ""
	}
	namePart := path[len(prefix):]

	// Strip "/ad.json" suffix or any trailing path segment
	for i := 0; i < len(namePart); i++ {
		if namePart[i] == '/' {
			return namePart[:i]
		}
	}

	return namePart
}
