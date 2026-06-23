// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
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
		name   string
		entry  CatalogEntry
		valid  bool
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
		DisplayName: "Test Agent",
		Type:        MediaTypeA2AAgentCard,
		Description: "A test agent for testing purposes",
		Tags:        []string{"test", "agent"},
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
