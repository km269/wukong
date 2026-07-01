// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"path/filepath"
	"testing"
)

// ==========================================================================
// Integration Tests: Publish → Discover data flow
// ==========================================================================
// These tests validate the complete ARD data flow without requiring
// an actual HTTP server (which can have port binding timing issues
// in CI/Windows environments):
//   1. Registry creation with built-in entries
//   2. MCP server & A2A agent auto-registration
//   3. Search and filter via the Registry
//   4. Import/Export round-trip (simulating remote catalog fetch)
// The HTTP transport layer (RegistryServer) is tested separately.

// TestFullRoundTrip validates the complete ARD lifecycle in-process:
// register → search → discover → export → import.
func TestFullRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	// Step 1: Create a ToolSet (as bootstrap does) with built-in entries.
	ts, err := NewToolSet("https://remote.registry.local", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	// Step 2: Simulate MCP auto-registration (as extension manager does).
	mcpEntry := CatalogEntry{
		Identifier:   "urn:air:wukong.local:mcp:external-analysis",
		DisplayName:  "External Analysis MCP",
		Type:         MediaTypeMCPServerCard,
		URL:          "http://external:3400/mcp",
		Description:  "External data analysis via MCP protocol",
		Tags:         []string{"mcp", "external", "stdio", "analysis"},
		Capabilities: []string{"analyze", "report", "visualize"},
	}
	if err := ts.Register(mcpEntry); err != nil {
		t.Fatalf("Register MCP entry: %v", err)
	}

	// Verify it's registered.
	if ts.Get("urn:air:wukong.local:mcp:external-analysis") == nil {
		t.Fatal("MCP entry not found after Register")
	}

	// Step 3: Simulate A2A auto-registration (as session bootstrap does).
	RegisterA2AAgent(ts, "remote-analyzer",
		"Analyzes code remotely via A2A protocol",
		"http://remote:9090")

	a2aEntry := ts.Get("urn:air:wukong.local:agent:remote-analyzer")
	if a2aEntry == nil {
		t.Fatal("A2A entry not found after RegisterA2AAgent")
	}
	if a2aEntry.Type != MediaTypeA2AAgentCard {
		t.Errorf("A2A type = %v, want %v", a2aEntry.Type, MediaTypeA2AAgentCard)
	}

	// Step 4: Search for entries (as LLM would via ard_search tool).
	ctx := context.Background()
	resp, err := ts.Search(ctx, "analysis", nil)
	if err != nil {
		t.Fatalf("Search('analysis') error = %v", err)
	}
	if resp.Total < 1 {
		t.Errorf("Search('analysis') total = %v, want >= 1", resp.Total)
	}

	// Step 5: Filtered search by type.
	resp2, err := ts.Search(ctx, "", map[string]any{
		"type": MediaTypeA2AAgentCard,
	})
	if err != nil {
		t.Fatalf("Search(filtered) error = %v", err)
	}
	for _, r := range resp2.Results {
		if r.Type != MediaTypeA2AAgentCard {
			t.Errorf("filter result type = %v, want %v",
				r.Type, MediaTypeA2AAgentCard)
		}
	}

	// Step 6: Export catalog (simulating /.well-known/ai-catalog.json).
	data, err := ts.ExportCatalog()
	if err != nil {
		t.Fatalf("ExportCatalog() error = %v", err)
	}
	if len(data) == 0 {
		t.Error("ExportCatalog() returned empty data")
	}

	// Step 7: Simulate remote client importing the catalog.
	ts2, err := NewToolSet("", filepath.Join(dir, "catalog2.json"))
	if err != nil {
		t.Fatalf("NewToolSet(remote) error = %v", err)
	}
	if err := ts2.ImportCatalog(data); err != nil {
		t.Fatalf(" ImportCatalog() error = %v", err)
	}

	// Verify imported entries are searchable.
	if ts2.Get("urn:air:wukong.local:mcp:external-analysis") == nil {
		t.Error("imported MCP entry not found after round-trip")
	}
	if ts2.Get("urn:air:wukong.local:agent:remote-analyzer") == nil {
		t.Error("imported A2A entry not found after round-trip")
	}

	// Step 8: Unregister cleanup.
	if err := ts.Unregister(
		"urn:air:wukong.local:mcp:external-analysis"); err != nil {
		t.Errorf("Unregister MCP: %v", err)
	}
	if ts.Get("urn:air:wukong.local:mcp:external-analysis") != nil {
		t.Error("entry still present after Unregister")
	}
}

// TestRegisterA2AAgent validates the A2A auto-registration helper.
func TestRegisterA2AAgent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	RegisterA2AAgent(ts, "remote-coder",
		"Reviews code remotely", "http://remote:9090")

	entry := ts.Get("urn:air:wukong.local:agent:remote-coder")
	if entry == nil {
		t.Fatal("A2A agent not registered")
	}
	if entry.Type != MediaTypeA2AAgentCard {
		t.Errorf("type = %v, want %v", entry.Type, MediaTypeA2AAgentCard)
	}
	if entry.URL != "http://remote:9090" {
		t.Errorf("URL = %v, want http://remote:9090", entry.URL)
	}
	if entry.DisplayName != "remote-coder" {
		t.Errorf("DisplayName = %v, want remote-coder", entry.DisplayName)
	}
}

// TestPublishAndServe_NilWhenDisabled validates that PublishAndServe
// returns nil when the port is 0 (disabled).
func TestPublishAndServe_NilWhenDisabled(t *testing.T) {
	ctx := context.Background()
	srv, err := PublishAndServe(ctx, 0, "", nil)
	if err != nil {
		t.Fatalf("PublishAndServe(0) error = %v", err)
	}
	if srv != nil {
		t.Error("PublishAndServe(0) should return nil")
	}
}

// TestNewRegistryWithEntries validates that built-in entries are
// pre-injected into newly created registries.
func TestNewRegistryWithEntries(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = ""

	r, err := NewRegistryWithEntries(config)
	if err != nil {
		t.Fatalf("NewRegistryWithEntries() error = %v", err)
	}

	// All built-in entries should be present.
	for _, want := range WukongBuiltInEntries() {
		if r.GetEntry(want.Identifier) == nil {
			t.Errorf("built-in entry %q not found in registry",
				want.Identifier)
		}
	}

	// Second registration should be a no-op (dedup).
	r2, err := NewRegistryWithEntries(config)
	if err != nil {
		t.Fatalf("second NewRegistryWithEntries() error = %v", err)
	}
	count := len(r2.GetCatalog().Entries)
	expected := len(WukongBuiltInEntries())
	if count != expected {
		t.Errorf("expected %d entries after dedup, got %d",
			expected, count)
	}
}

// TestMCPAutoRegistrationFlow validates the MCP auto-registration
// data flow: CatalogEntry→Registry→Search.
func TestMCPAutoRegistrationFlow(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = ""
	r, err := NewRegistryWithEntries(config)
	if err != nil {
		t.Fatalf("NewRegistryWithEntries() error = %v", err)
	}

	// Build entry exactly as buildARDEntry in extension/manager.go does.
	extEntry := CatalogEntry{
		Identifier:   "urn:air:wukong.local:mcp:test-mcp-server",
		DisplayName:  "test-mcp-server",
		Type:         MediaTypeMCPServerCard,
		URL:          "http://test:3400/mcp",
		Tags:         []string{"mcp", "external", "streamable", "remote"},
		Capabilities: []string{"streamable"},
	}
	if err := r.Register(extEntry); err != nil {
		t.Fatalf("Register MCP entry: %v", err)
	}

	// Search should find it by tags.
	req := &SearchRequest{Query: "test-mcp", Limit: 10}
	resp, err := r.Search(req)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	found := false
	for _, result := range resp.Results {
		if result.Identifier ==
			"urn:air:wukong.local:mcp:test-mcp-server" {
			found = true
			break
		}
	}
	if !found {
		t.Error("MCP entry not found in search results")
	}
}
