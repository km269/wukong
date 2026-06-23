// Package ard provides Agentic Resource Discovery (ARD) specification implementation.
// It enables federated discovery and search for AI agents, MCP servers, and other
// agentic resources following the ARD specification v0.9.
//
// Key concepts:
//   - ai-catalog.json: Capability manifest hosted at /.well-known/ai-catalog.json
//   - URN identifiers: urn:air:<publisher>:<namespace>:<agent-name>
//   - Search API: POST /search for semantic resource discovery
//   - Media types: application/a2a-agent-card+json, application/mcp-server-card+json, etc.
package ard

import (
	"encoding/json"
	"time"
)

// SpecVersion is the current ARD specification version.
const SpecVersion = "1.0"

// ARD Media Types (IANA registered).
const (
	MediaTypeA2AAgentCard    = "application/a2a-agent-card+json"
	MediaTypeMCPServerCard    = "application/mcp-server-card+json"
	MediaTypeAICatalog        = "application/ai-catalog+json"
	MediaTypeAIRegistry       = "application/ai-registry+json"
)

// HostInfo represents the host organization information.
type HostInfo struct {
	DisplayName string `json:"displayName"`
	Identifier string `json:"identifier"` // DID or FQDN
}

// CatalogEntry represents a single agentic resource entry in the catalog.
type CatalogEntry struct {
	// Required fields
	Identifier  string `json:"identifier"`            // URN format: urn:air:<publisher>:<namespace>:<name>
	DisplayName string `json:"displayName"`           // Human-readable name
	Type       string `json:"type"`                   // IANA Media Type

	// Exactly one of URL or Data must be present
	URL  string          `json:"url,omitempty"`  // Remote reference
	Data json.RawMessage `json:"data,omitempty"` // Embedded document

	// Optional fields
	Description          string            `json:"description,omitempty"`
	Tags                 []string          `json:"tags,omitempty"`
	Capabilities         []string         `json:"capabilities,omitempty"`         // Tool/skill names for filtering
	RepresentativeQueries []string        `json:"representativeQueries,omitempty"` // 2-5 sample queries for embedding
	Version              string            `json:"version,omitempty"`
	UpdatedAt            string            `json:"updatedAt,omitempty"` // ISO 8601
	Metadata             map[string]any   `json:"metadata,omitempty"`
	TrustManifest        *TrustManifest    `json:"trustManifest,omitempty"`
}

// HasURL returns true if entry uses URL reference.
func (e *CatalogEntry) HasURL() bool {
	return e.URL != ""
}

// HasData returns true if entry uses inline data.
func (e *CatalogEntry) HasData() bool {
	return len(e.Data) > 0
}

// IsValid checks if the entry is valid per ARD spec.
func (e *CatalogEntry) IsValid() bool {
	if e.Identifier == "" || e.DisplayName == "" || e.Type == "" {
		return false
	}
	// Must have exactly one of URL or Data
	return e.HasURL() != e.HasData()
}

// AICatalog represents the capability manifest (ai-catalog.json).
type AICatalog struct {
	SpecVersion string          `json:"specVersion"`
	Host       HostInfo        `json:"host"`
	Entries    []CatalogEntry  `json:"entries"`
}

// NewAICatalog creates a new catalog with the given host info.
func NewAICatalog(displayName, identifier string) *AICatalog {
	return &AICatalog{
		SpecVersion: SpecVersion,
		Host: HostInfo{
			DisplayName: displayName,
			Identifier:  identifier,
		},
		Entries: make([]CatalogEntry, 0),
	}
}

// AddEntry adds a catalog entry.
func (c *AICatalog) AddEntry(entry CatalogEntry) {
	c.Entries = append(c.Entries, entry)
}

// GetEntry finds an entry by identifier.
func (c *AICatalog) GetEntry(identifier string) *CatalogEntry {
	for i := range c.Entries {
		if c.Entries[i].Identifier == identifier {
			return &c.Entries[i]
		}
	}
	return nil
}

// RemoveEntry removes an entry by identifier.
func (c *AICatalog) RemoveEntry(identifier string) bool {
	for i := range c.Entries {
		if c.Entries[i].Identifier == identifier {
			c.Entries = append(c.Entries[:i], c.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// UpdateEntry updates an existing entry by identifier.
// If the entry does not exist, it is appended as a new entry.
func (c *AICatalog) UpdateEntry(identifier string, entry CatalogEntry) {
	for i := range c.Entries {
		if c.Entries[i].Identifier == identifier {
			c.Entries[i] = entry
			return
		}
	}
	// Entry not found; append as new.
	c.Entries = append(c.Entries, entry)
}

// FilterEntries returns entries matching the given criteria.
func (c *AICatalog) FilterEntries(filter EntryFilter) []CatalogEntry {
	var results []CatalogEntry
	for i := range c.Entries {
		if filter.Matches(&c.Entries[i]) {
			results = append(results, c.Entries[i])
		}
	}
	return results
}

// EntryFilter defines criteria for filtering catalog entries.
type EntryFilter struct {
	Type        string
	Tags        []string
	Capabilities []string
	Query       string // For text search
}

// Matches checks if an entry matches the filter criteria.
func (f *EntryFilter) Matches(entry *CatalogEntry) bool {
	if f == nil {
		return true
	}

	if f.Type != "" && entry.Type != f.Type {
		return false
	}

	if len(f.Tags) > 0 {
		hasTag := false
		for _, tag := range f.Tags {
			for _, et := range entry.Tags {
				if tag == et {
					hasTag = true
					break
				}
			}
			if hasTag {
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	if len(f.Capabilities) > 0 {
		hasCap := false
		for _, cap := range f.Capabilities {
			for _, ec := range entry.Capabilities {
				if cap == ec {
					hasCap = true
					break
				}
			}
			if hasCap {
				break
			}
		}
		if !hasCap {
			return false
		}
	}

	// Text search in description and display name
	if f.Query != "" {
		// Simple substring match
		query := lowercase(f.Query)
		if !contains(lowercase(entry.Description), query) &&
			!contains(lowercase(entry.DisplayName), query) {
			return false
		}
	}

	return true
}

func lowercase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TrustManifest contains verifiable identity and trust metadata.
type TrustManifest struct {
	Identity     string       `json:"identity"`      // SPIFFE URI or similar
	IdentityType string       `json:"identityType"` // spiffe, did, etc.
	Attestations []Attestation `json:"attestations,omitempty"`
}

// Attestation represents a verifiable attestation.
type Attestation struct {
	Type string `json:"type"` // SPIFFE-X509, SOC2-Type2, ISO27001, etc.
	URI  string `json:"uri"`  // URL to verification material
}

// ProvenanceLink represents a provenance record.
type ProvenanceLink struct {
	Type    string `json:"type"`
	URI     string `json:"uri"`
	Version string `json:"version,omitempty"`
}

// SearchRequest represents an ARD search request.
type SearchRequest struct {
	Query    string      `json:"query"`
	Filters  SearchFilters `json:"filters,omitempty"`
	Limit   int         `json:"limit,omitempty"`
	Offset  int         `json:"offset,omitempty"`
}

// SearchFilters defines search filtering criteria.
type SearchFilters struct {
	Type         string   `json:"type,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Publisher    string   `json:"publisher,omitempty"`
}

// SearchResponse represents an ARD search response.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Total  int            `json:"total"`
	Query  string         `json:"query"`
}

// SearchResult represents a single search result.
type SearchResult struct {
	Identifier  string          `json:"identifier"`
	DisplayName string         `json:"displayName"`
	Type       string          `json:"type"`
	URL        string          `json:"url,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Description string         `json:"description,omitempty"`
	Score      float64         `json:"score"` // Relevance score 0-1
}

// ExploreRequest represents an ARD explore request (optional).
type ExploreRequest struct {
	Type   string   `json:"type,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Limit  int      `json:"limit,omitempty"`
	Offset int      `json:"offset,omitempty"`
}

// ExploreResponse represents an ARD explore response (optional).
type ExploreResponse struct {
	Entries []CatalogEntry `json:"entries"`
	Total   int           `json:"total"`
}

// ListResponse represents a list of catalog entries (optional).
type ListResponse struct {
	Entries []CatalogEntry `json:"entries"`
	Total   int           `json:"total"`
}

// ErrorResponse represents an ARD error response.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard error codes per ARD spec.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeNotFound       = -32601
	ErrCodeInternalError  = -32603
)

// StandardError creates a standard ARD error.
func StandardError(code int, message string) *ErrorResponse {
	return &ErrorResponse{
		Code:    code,
		Message: message,
	}
}

// Now returns current time in ISO 8601 format.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
