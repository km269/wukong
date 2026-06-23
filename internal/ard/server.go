// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// RegistryServer provides the ARD HTTP API endpoints.
type RegistryServer struct {
	registry *Registry
	config   *RegistryConfig
	server   *http.Server
	mux      *http.ServeMux
}

// NewRegistryServer creates a new registry server.
func NewRegistryServer(registry *Registry, config *RegistryConfig) *RegistryServer {
	s := &RegistryServer{
		registry: registry,
		config:   config,
		mux:      http.NewServeMux(),
	}
	
	s.setupRoutes()
	return s
}

// setupRoutes configures HTTP routes.
func (s *RegistryServer) setupRoutes() {
	// Well-known endpoint for ai-catalog.json
	s.mux.HandleFunc("/.well-known/ai-catalog.json", s.handleCatalog)
	
	// API endpoints
	if s.config.EnableSearch {
		s.mux.HandleFunc("/api/v1/search", s.handleSearch)
	}
	
	if s.config.EnableExplore {
		s.mux.HandleFunc("/api/v1/explore", s.handleExplore)
	}
	
	if s.config.EnableList {
		s.mux.HandleFunc("/api/v1/agents", s.handleList)
	}
	
	// Health check
	s.mux.HandleFunc("/health", s.handleHealth)
	
	// CORS middleware
	s.mux.HandleFunc("/", s.handleCORS(s.handleNotFound))
}

// Start starts the registry server.
func (s *RegistryServer) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout:  30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
	}()
	
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *RegistryServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// handleCatalog serves the ai-catalog.json file.
func (s *RegistryServer) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	
	catalog := s.registry.GetCatalog()
	s.writeJSON(w, http.StatusOK, catalog)
}

// handleSearch handles POST /api/v1/search.
func (s *RegistryServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	
	resp, err := s.registry.Search(&req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	s.writeJSON(w, http.StatusOK, resp)
}

// handleExplore handles POST /api/v1/explore.
func (s *RegistryServer) handleExplore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	
	var req ExploreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	
	resp, err := s.registry.Explore(&req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	s.writeJSON(w, http.StatusOK, resp)
}

// handleList handles GET /api/v1/agents.
func (s *RegistryServer) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	
	resp, err := s.registry.List(limit, offset)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	s.writeJSON(w, http.StatusOK, resp)
}

// handleHealth handles GET /health.
func (s *RegistryServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// handleNotFound handles 404 responses.
func (s *RegistryServer) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, http.StatusNotFound, "Not found")
}

// handleCORS wraps a handler with CORS headers.
func (s *RegistryServer) handleCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
		
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		
		next(w, r)
	}
}

// writeJSON writes a JSON response.
func (s *RegistryServer) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes an error response.
func (s *RegistryServer) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(StandardError(status, message))
}

// WriteCatalogFile writes the catalog to a file.
func WriteCatalogFile(catalog *AICatalog, path string) error {
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	
	return nil
}

// ReadCatalogFile reads the catalog from a file.
func ReadCatalogFile(path string) (*AICatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	
	catalog := &AICatalog{}
	if err := json.Unmarshal(data, catalog); err != nil {
		return nil, fmt.Errorf("unmarshal catalog: %w", err)
	}
	
	return catalog, nil
}

// ImportMCPServer imports an MCP Server Card into the catalog.
func (c *AICatalog) ImportMCPServer(card *MCPServerCard) error {
	if card.Identifier == "" {
		return fmt.Errorf("MCPServerCard must have an identifier")
	}
	
	entry := CatalogEntry{
		Identifier:   card.Identifier,
		DisplayName:  card.Name,
		Type:         MediaTypeMCPServerCard,
		Description:  card.Description,
		Tags:         card.Tags,
		Capabilities: card.Tools,
		URL:          card.URL,
		Version:      card.Version,
		UpdatedAt:    Now(),
	}
	
	c.AddEntry(entry)
	return nil
}

// ImportA2AAgent imports an A2A Agent Card into the catalog.
func (c *AICatalog) ImportA2AAgent(card *A2AAgentCard) error {
	if card.Identifier == "" {
		return fmt.Errorf("A2AAgentCard must have an identifier")
	}
	
	entry := CatalogEntry{
		Identifier:   card.Identifier,
		DisplayName:  card.Name,
		Type:         MediaTypeA2AAgentCard,
		Description:  card.Description,
		Tags:         card.Tags,
		Capabilities: card.Capabilities,
		URL:          card.URL,
		Version:      card.Version,
		UpdatedAt:    Now(),
	}
	
	c.AddEntry(entry)
	return nil
}

// MCPServerCard represents an MCP Server Card (simplified).
type MCPServerCard struct {
	Identifier  string   `json:"identifier,omitempty"`
	Name       string   `json:"name,omitempty"`
	Description string  `json:"description,omitempty"`
	URL        string   `json:"url,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Version    string   `json:"version,omitempty"`
}

// A2AAgentCard represents an A2A Agent Card (simplified).
type A2AAgentCard struct {
	Identifier   string   `json:"identifier,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// BuildRepresentativeQueries generates representative queries for an entry.
func BuildRepresentativeQueries(tags []string, capabilities []string) []string {
	var queries []string
	
	// Generate queries based on tags
	for _, tag := range tags {
		queries = append(queries, fmt.Sprintf("find a %s tool", tag))
		queries = append(queries, fmt.Sprintf("I need help with %s", tag))
	}
	
	// Generate queries based on capabilities
	for _, cap := range capabilities {
		capName := strings.Title(strings.ReplaceAll(cap, "Tool", ""))
		queries = append(queries, fmt.Sprintf("can you %s", strings.ToLower(capName)))
		queries = append(queries, fmt.Sprintf("help me %s", strings.ToLower(capName)))
	}
	
	// Limit to 5 queries
	if len(queries) > 5 {
		queries = queries[:5]
	}
	
	return queries
}
