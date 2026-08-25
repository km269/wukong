// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseURN(t *testing.T) {
	tests := []struct {
		name    string
		urn     string
		want    string
		wantErr bool
	}{
		{
			name:    "valid URN with namespace",
			urn:     "urn:air:wukong.local:server:apps",
			want:    "wukong.local",
			wantErr: false,
		},
		{
			name:    "valid URN without namespace",
			urn:     "urn:air:wukong.local:apps",
			want:    "wukong.local",
			wantErr: false,
		},
		{
			name:    "invalid prefix",
			urn:     "urn:invalid:example.com",
			wantErr: true,
		},
		{
			name:    "empty URN",
			urn:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urn, err := ParseURN(tt.urn)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseURN() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && urn.Publisher != tt.want {
				t.Errorf("ParseURN() publisher = %v, want %v", urn.Publisher, tt.want)
			}
		})
	}
}

func TestNewURN(t *testing.T) {
	urn := NewURN("wukong.local", "server", "apps")

	if urn.Publisher != "wukong.local" {
		t.Errorf("NewURN() publisher = %v, want wukong.local", urn.Publisher)
	}

	if urn.Namespace != "server" {
		t.Errorf("NewURN() namespace = %v, want server", urn.Namespace)
	}

	if urn.Name != "apps" {
		t.Errorf("NewURN() name = %v, want apps", urn.Name)
	}

	expected := "urn:air:wukong.local:server:apps"
	if urn.Raw != expected {
		t.Errorf("NewURN() raw = %v, want %v", urn.Raw, expected)
	}
}

func TestCatalogEntry(t *testing.T) {
	entry := CatalogEntry{
		Identifier:  "urn:air:wukong.local:server:apps",
		DisplayName: "Wukong Apps",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://api.wukong.local/mcp/apps",
	}

	if !entry.HasURL() {
		t.Error("CatalogEntry.HasURL() = false, want true")
	}

	if entry.HasData() {
		t.Error("CatalogEntry.HasData() = true, want false")
	}

	if !entry.IsValid() {
		t.Error("CatalogEntry.IsValid() = false, want true")
	}
}

func TestCatalogEntryValidation(t *testing.T) {
	tests := []struct {
		name  string
		entry CatalogEntry
		valid bool
	}{
		{
			name: "valid entry with URL",
			entry: CatalogEntry{
				Identifier:  "urn:air:example.com:agent:test",
				DisplayName: "Test Agent",
				Type:        MediaTypeA2AAgentCard,
				URL:         "https://example.com/agent.json",
			},
			valid: true,
		},
		{
			name: "valid entry with data",
			entry: CatalogEntry{
				Identifier:  "urn:air:example.com:agent:test",
				DisplayName: "Test Agent",
				Type:        MediaTypeA2AAgentCard,
				Data:        []byte(`{"name":"test"}`),
			},
			valid: true,
		},
		{
			name: "invalid - missing identifier",
			entry: CatalogEntry{
				DisplayName: "Test",
				Type:        MediaTypeA2AAgentCard,
			},
			valid: false,
		},
		{
			name: "invalid - both URL and data",
			entry: CatalogEntry{
				Identifier:  "urn:air:example.com:test",
				DisplayName: "Test",
				Type:        MediaTypeA2AAgentCard,
				URL:         "https://example.com",
				Data:        []byte(`{}`),
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.entry.IsValid() != tt.valid {
				t.Errorf("CatalogEntry.IsValid() = %v, want %v", tt.entry.IsValid(), tt.valid)
			}
		})
	}
}

func TestAICatalog(t *testing.T) {
	catalog := NewAICatalog("Test Host", "did:web:test.example.com")

	if catalog.SpecVersion != SpecVersion {
		t.Errorf("NewAICatalog() specVersion = %v, want %v", catalog.SpecVersion, SpecVersion)
	}

	if catalog.Host.DisplayName != "Test Host" {
		t.Errorf("NewAICatalog() host.DisplayName = %v, want Test Host", catalog.Host.DisplayName)
	}

	// Test adding entries
	entry := CatalogEntry{
		Identifier:  "urn:air:test.example.com:server:test",
		DisplayName: "Test Server",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test.example.com/server.json",
	}

	catalog.AddEntry(entry)

	if len(catalog.Entries) != 1 {
		t.Errorf("AICatalog.AddEntry() entries count = %v, want 1", len(catalog.Entries))
	}

	// Test GetEntry
	found := catalog.GetEntry("urn:air:test.example.com:server:test")
	if found == nil {
		t.Error("AICatalog.GetEntry() returned nil for existing entry")
	}

	// Test GetEntry for non-existent
	notFound := catalog.GetEntry("urn:air:missing")
	if notFound != nil {
		t.Error("AICatalog.GetEntry() should return nil for non-existent entry")
	}

	// Test RemoveEntry
	if !catalog.RemoveEntry("urn:air:test.example.com:server:test") {
		t.Error("AICatalog.RemoveEntry() returned false for existing entry")
	}

	if len(catalog.Entries) != 0 {
		t.Errorf("AICatalog.RemoveEntry() entries count = %v, want 0", len(catalog.Entries))
	}
}

func TestSearchRequest(t *testing.T) {
	req := &SearchRequest{
		Query: "browser automation",
		Filters: SearchFilters{
			Type: MediaTypeMCPServerCard,
		},
		Limit: 10,
	}

	if req.Query != "browser automation" {
		t.Errorf("SearchRequest.Query = %v, want browser automation", req.Query)
	}

	if req.Filters.Type != MediaTypeMCPServerCard {
		t.Errorf("SearchRequest.Filters.Type = %v, want %v", req.Filters.Type, MediaTypeMCPServerCard)
	}
}

func TestWukongBuiltInEntries(t *testing.T) {
	entries := WukongBuiltInEntries()

	if len(entries) == 0 {
		t.Error("WukongBuiltInEntries() returned empty slice")
	}

	// Check that all entries have valid URNs
	for _, entry := range entries {
		if err := ValidateURN(entry.Identifier); err != nil {
			t.Errorf("WukongBuiltInEntries() entry has invalid URN: %s - %v", entry.Identifier, err)
		}

		if entry.Type != MediaTypeMCPServerCard && entry.Type != MediaTypeA2AAgentCard {
			t.Errorf("WukongBuiltInEntries() entry has invalid type: %s", entry.Type)
		}
	}
}

func TestMediaTypes(t *testing.T) {
	if MediaTypeA2AAgentCard != "application/a2a-agent-card+json" {
		t.Errorf("MediaTypeA2AAgentCard = %v, want application/a2a-agent-card+json", MediaTypeA2AAgentCard)
	}

	if MediaTypeMCPServerCard != "application/mcp-server-card+json" {
		t.Errorf("MediaTypeMCPServerCard = %v, want application/mcp-server-card+json", MediaTypeMCPServerCard)
	}

	if MediaTypeAICatalog != "application/ai-catalog+json" {
		t.Errorf("MediaTypeAICatalog = %v, want application/ai-catalog+json", MediaTypeAICatalog)
	}
}

func TestURNBuilder(t *testing.T) {
	builder, err := NewURNBuilder("wukong.local")
	if err != nil {
		t.Fatalf("NewURNBuilder() error = %v", err)
	}

	// Test without namespace
	urn1 := builder.Build("apps")
	if urn1.String() != "urn:air:wukong.local:apps" {
		t.Errorf("URNBuilder.Build() = %v, want urn:air:wukong.local:apps", urn1.String())
	}

	// Test with namespace
	urn2 := builder.WithNamespace("server").Build("apps")
	if urn2.String() != "urn:air:wukong.local:server:apps" {
		t.Errorf("URNBuilder.Build() = %v, want urn:air:wukong.local:server:apps", urn2.String())
	}

	// Test invalid domain
	_, err = NewURNBuilder("invalid..domain")
	if err == nil {
		t.Error("NewURNBuilder() should fail for invalid domain")
	}
}

func TestCatalogEntryFilter(t *testing.T) {
	entry := &CatalogEntry{
		Identifier:   "urn:air:test.com:agent:test",
		DisplayName:  "Test Agent",
		Type:         MediaTypeA2AAgentCard,
		Description:  "A test agent for testing purposes",
		Tags:         []string{"test", "agent"},
		Capabilities: []string{"tool1", "tool2"},
	}

	tests := []struct {
		name   string
		filter EntryFilter
		match  bool
	}{
		{
			name:   "no filter",
			filter: EntryFilter{},
			match:  true,
		},
		{
			name:   "type match",
			filter: EntryFilter{Type: MediaTypeA2AAgentCard},
			match:  true,
		},
		{
			name:   "type mismatch",
			filter: EntryFilter{Type: MediaTypeMCPServerCard},
			match:  false,
		},
		{
			name:   "query in display name",
			filter: EntryFilter{Query: "test agent"},
			match:  true,
		},
		{
			name:   "query in description",
			filter: EntryFilter{Query: "testing"},
			match:  true,
		},
		{
			name:   "query not found",
			filter: EntryFilter{Query: "nonexistent"},
			match:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.filter.Matches(entry) != tt.match {
				t.Errorf("EntryFilter.Matches() = %v, want %v", tt.filter.Matches(entry), tt.match)
			}
		})
	}
}

// ==========================================================================
// ToolSet Tests
// ==========================================================================

func TestToolSet_NewAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	entries := ts.List()
	if len(entries) == 0 {
		t.Error("ToolSet.List() returned empty, expected built-in entries")
	}

	// All entries should have valid types
	for _, e := range entries {
		if e.Type != MediaTypeMCPServerCard && e.Type != MediaTypeA2AAgentCard {
			t.Errorf("entry %q has invalid type %q", e.Identifier, e.Type)
		}
		if e.Identifier == "" {
			t.Error("entry has empty identifier")
		}
	}
}

func TestToolSet_RegisterAndGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	entry := CatalogEntry{
		Identifier:   "urn:air:test.local:agent:my-agent",
		DisplayName:  "My Test Agent",
		Type:         MediaTypeA2AAgentCard,
		URL:          "https://test.local/agent.json",
		Description:  "A test agent",
		Tags:         []string{"test", "agent"},
		Capabilities: []string{"code_review"},
	}

	if err := ts.Register(entry); err != nil {
		t.Fatalf("ToolSet.Register() error = %v", err)
	}

	found := ts.Get("urn:air:test.local:agent:my-agent")
	if found == nil {
		t.Fatal("ToolSet.Get() returned nil for registered entry")
	}
	if found.DisplayName != "My Test Agent" {
		t.Errorf("ToolSet.Get().DisplayName = %v, want My Test Agent", found.DisplayName)
	}
}

func TestToolSet_Unregister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	entry := CatalogEntry{
		Identifier:  "urn:air:test.local:mcp:temp",
		DisplayName: "Temporary Server",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test.local/mcp.json",
	}
	if err := ts.Register(entry); err != nil {
		t.Fatalf("ToolSet.Register() error = %v", err)
	}

	if err := ts.Unregister("urn:air:test.local:mcp:temp"); err != nil {
		t.Fatalf("ToolSet.Unregister() error = %v", err)
	}

	if ts.Get("urn:air:test.local:mcp:temp") != nil {
		t.Error("ToolSet.Get() returned entry after unregister")
	}
}

func TestToolSet_Search(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	// Register known entries for search
	ts.Register(CatalogEntry{
		Identifier:   "urn:air:test.local:agent:browser",
		DisplayName:  "Browser Agent",
		Type:         MediaTypeA2AAgentCard,
		Description:  "Automates browser interactions",
		Tags:         []string{"browser", "automation"},
		Capabilities: []string{"navigate", "screenshot"},
	})
	ts.Register(CatalogEntry{
		Identifier:   "urn:air:test.local:mcp:filesystem",
		DisplayName:  "Filesystem MCP Server",
		Type:         MediaTypeMCPServerCard,
		Description:  "Read and write files",
		Tags:         []string{"filesystem", "io"},
		Capabilities: []string{"read_file", "write_file"},
	})

	ctx := context.Background()
	resp, err := ts.Search(ctx, "browser", nil)
	if err != nil {
		t.Fatalf("ToolSet.Search() error = %v", err)
	}
	if len(resp.Results) == 0 {
		t.Error("ToolSet.Search() returned no results for 'browser'")
	}
	if resp.Query != "browser" {
		t.Errorf("ToolSet.Search() query = %v, want browser", resp.Query)
	}

	// Search with type filter
	resp2, err := ts.Search(ctx, "file", map[string]any{
		"type": MediaTypeMCPServerCard,
	})
	if err != nil {
		t.Fatalf("ToolSet.Search() with filter error = %v", err)
	}
	for _, r := range resp2.Results {
		if r.Type != MediaTypeMCPServerCard {
			t.Errorf("filtered result has type %q, want %q", r.Type, MediaTypeMCPServerCard)
		}
	}
}

func TestToolSet_ImportCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	imported := AICatalog{
		SpecVersion: SpecVersion,
		Host:        HostInfo{DisplayName: "Remote", Identifier: "did:web:remote"},
		Entries: []CatalogEntry{
			{
				Identifier:  "urn:air:remote.local:mcp:new-server",
				DisplayName: "New Server",
				Type:        MediaTypeMCPServerCard,
				URL:         "https://remote/mcp.json",
			},
		},
	}
	data, err := json.Marshal(imported)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if err := ts.ImportCatalog(data); err != nil {
		t.Fatalf("ToolSet.ImportCatalog() error = %v", err)
	}

	if ts.Get("urn:air:remote.local:mcp:new-server") == nil {
		t.Error("ToolSet.Get() did not find imported entry")
	}
}

func TestToolSet_SetRegistryURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	// Valid URL
	if err := ts.SetRegistryURL("https://registry.example.com"); err != nil {
		t.Errorf("SetRegistryURL(valid) error = %v", err)
	}
	urls := ts.RegistryURLs()
	if len(urls) != 1 || urls[0] != "https://registry.example.com" {
		t.Errorf("RegistryURLs() = %v, want [https://registry.example.com]", urls)
	}

	// Duplicate URL should be rejected
	if err := ts.SetRegistryURL("https://registry.example.com"); err != nil {
		t.Errorf("SetRegistryURL(duplicate) error = %v", err)
	}
	// Count should still be 1
	urls2 := ts.RegistryURLs()
	if len(urls2) != 1 {
		t.Errorf("RegistryURLs() count = %v after duplicate, want 1", len(urls2))
	}

	// Invalid URL scheme
	if err := ts.SetRegistryURL("ftp://example.com"); err == nil {
		t.Error("SetRegistryURL(invalid scheme) expected error")
	}

	// Malformed URL
	if err := ts.SetRegistryURL("not-a-url"); err == nil {
		t.Error("SetRegistryURL(malformed) expected error")
	}
}

func TestToolSet_ExportCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	data, err := ts.ExportCatalog()
	if err != nil {
		t.Fatalf("ToolSet.ExportCatalog() error = %v", err)
	}
	if len(data) == 0 {
		t.Error("ToolSet.ExportCatalog() returned empty data")
	}

	var cat AICatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatalf("unmarshal exported catalog error = %v", err)
	}
	if cat.SpecVersion != SpecVersion {
		t.Errorf("exported SpecVersion = %v, want %v", cat.SpecVersion, SpecVersion)
	}
}

// ==========================================================================
// Registry Tests
// ==========================================================================

func TestRegistry_NewAndList(t *testing.T) {
	config := &RegistryConfig{
		DisplayName:  "Test Registry",
		Identifier:   "did:web:test.local",
		EnableSearch: true,
		EnableList:   true,
		DefaultLimit: 20,
		MaxResults:   100,
	}

	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := r.List(10, 0)
	if err != nil {
		t.Fatalf("Registry.List() error = %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("new registry has %v entries, want 0", resp.Total)
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = "" // No persistence for test
	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	entry := CatalogEntry{
		Identifier:   "urn:air:test.local:agent:registered",
		DisplayName:  "Registered Agent",
		Type:         MediaTypeA2AAgentCard,
		URL:          "https://test.local/agent.json",
		Description:  "A registered test agent",
		Tags:         []string{"regression", "test"},
		Capabilities: []string{"echo", "ping"},
	}

	if err := r.Register(entry); err != nil {
		t.Fatalf("Registry.Register() error = %v", err)
	}

	found := r.GetEntry("urn:air:test.local:agent:registered")
	if found == nil {
		t.Fatal("Registry.GetEntry() returned nil")
	}
	if found.DisplayName != "Registered Agent" {
		t.Errorf("DisplayName = %v, want Registered Agent", found.DisplayName)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = ""
	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	entry := CatalogEntry{
		Identifier:  "urn:air:test.local:mcp:server",
		DisplayName: "Server",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test/mcp.json",
	}
	if err := r.Register(entry); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := r.Register(entry); err == nil {
		t.Error("duplicate Register() expected error")
	}
}

func TestRegistry_Search(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = ""
	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	// Use simple URN format that passes validation.
	entry1 := CatalogEntry{
		Identifier:   "urn:air:wukong.local:agent:search-engine",
		DisplayName:  "Search Agent",
		Type:         MediaTypeA2AAgentCard,
		Description:  "Searches the web",
		URL:          "https://test/search.json",
		Tags:         []string{"search"},
		Capabilities: []string{"web_search"},
	}
	if err := r.Register(entry1); err != nil {
		t.Fatalf("Register(search agent) error = %v", err)
	}

	entry2 := CatalogEntry{
		Identifier:   "urn:air:wukong.local:agent:calculator",
		DisplayName:  "Calculator Agent",
		Type:         MediaTypeA2AAgentCard,
		Description:  "Performs calculations",
		URL:          "https://test/calc.json",
		Tags:         []string{"math"},
		Capabilities: []string{"calculate"},
	}
	if err := r.Register(entry2); err != nil {
		t.Fatalf("Register(calculator) error = %v", err)
	}

	req := &SearchRequest{
		Query: "search",
		Limit: 10,
	}
	resp, err := r.Search(req)
	if err != nil {
		t.Fatalf("Registry.Search() error = %v", err)
	}
	if resp.Total < 1 {
		t.Errorf("Search() total = %v, want >= 1", resp.Total)
	}
	// The search agent should have the highest score.
	if len(resp.Results) > 0 &&
		resp.Results[0].Identifier != "urn:air:wukong.local:agent:search-engine" {
		t.Errorf("top result = %v, want search-engine",
			resp.Results[0].Identifier)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	config := DefaultRegistryConfig()
	config.CatalogPath = ""
	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	entry := CatalogEntry{
		Identifier:  "urn:air:test.local:mcp:temp",
		DisplayName: "Temp",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test/temp.json",
	}
	r.Register(entry)

	if err := r.Unregister("urn:air:test.local:mcp:temp"); err != nil {
		t.Fatalf("Registry.Unregister() error = %v", err)
	}

	if r.GetEntry("urn:air:test.local:mcp:temp") != nil {
		t.Error("GetEntry() returned entry after unregister")
	}

	// Unregister non-existent
	if err := r.Unregister("urn:air:missing"); err == nil {
		t.Error("Unregister(non-existent) expected error")
	}
}

func TestRegistry_CatalogPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	config := DefaultRegistryConfig()
	config.CatalogPath = path
	r, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	r.Register(CatalogEntry{
		Identifier:  "urn:air:test.local:mcp:persisted",
		DisplayName: "Persisted Server",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test/persisted.json",
	})

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Errorf("catalog file not created: %v", err)
	}

	// Load from file
	r2, err2 := NewRegistry(config)
	if err2 != nil {
		t.Fatalf("NewRegistry() from file error = %v", err2)
	}
	found := r2.GetEntry("urn:air:test.local:mcp:persisted")
	if found == nil {
		t.Error("GetEntry() did not find persisted entry")
	}
}

// ==========================================================================
// Federator Tests
// ==========================================================================

func TestCalculateScore(t *testing.T) {
	tests := []struct {
		name     string
		entry    CatalogEntry
		query    string
		minScore float64
		maxScore float64
	}{
		{
			name: "exact display name match",
			entry: CatalogEntry{
				DisplayName: "Browser Agent",
				Type:        MediaTypeA2AAgentCard,
			},
			query:    "browser",
			minScore: 0.3,
			maxScore: 1.0,
		},
		{
			name: "description match",
			entry: CatalogEntry{
				DisplayName: "Agent X",
				Description: "Automates web browser testing",
				Type:        MediaTypeA2AAgentCard,
			},
			query:    "browser",
			minScore: 0.2,
			maxScore: 0.5,
		},
		{
			name: "tag match",
			entry: CatalogEntry{
				DisplayName: "Agent Y",
				Tags:        []string{"browser", "automation"},
				Type:        MediaTypeA2AAgentCard,
			},
			query:    "browser",
			minScore: 0.1,
			maxScore: 0.3,
		},
		{
			name: "no match",
			entry: CatalogEntry{
				DisplayName: "Calculator",
				Tags:        []string{"math"},
				Type:        MediaTypeA2AAgentCard,
			},
			query:    "browser",
			minScore: 0.0,
			maxScore: 0.0,
		},
		{
			name: "empty query",
			entry: CatalogEntry{
				DisplayName: "Anything",
				Type:        MediaTypeA2AAgentCard,
			},
			query:    "",
			minScore: 1.0,
			maxScore: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateScore(&tt.entry, tt.query)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("calculateScore() = %v, want [%v, %v]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestExportCatalog_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	ts, err := NewToolSet("", path)
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}

	ts.Register(CatalogEntry{
		Identifier:   "urn:air:test.local:agent:e2e",
		DisplayName:  "E2E Agent",
		Type:         MediaTypeA2AAgentCard,
		Description:  "End-to-end test agent",
		Tags:         []string{"e2e"},
		Capabilities: []string{"test"},
	})

	data, _ := ts.ExportCatalog()

	// Create new ToolSet and import
	ts2, err := NewToolSet("", filepath.Join(dir, "catalog2.json"))
	if err != nil {
		t.Fatalf("NewToolSet() error = %v", err)
	}
	if err := ts2.ImportCatalog(data); err != nil {
		t.Fatalf("ImportCatalog() error = %v", err)
	}

	if ts2.Get("urn:air:test.local:agent:e2e") == nil {
		t.Error("round-trip import lost entry")
	}
}
