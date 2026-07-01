// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"testing"
)

func TestSemanticIndex(t *testing.T) {
	// Create semantic index without embedder (for testing)
	si := NewSemanticIndex("", "", "")

	entry := &CatalogEntry{
		Identifier:   "urn:air:test.com:agent:test",
		DisplayName:  "Test Agent",
		Description:  "A test agent for browser automation",
		Tags:         []string{"browser", "automation"},
		Capabilities: []string{"navigate", "click"},
	}

	// IndexEntry should work without embedder (no vectors)
	err := si.IndexEntry(context.Background(), entry)
	if err != nil {
		t.Errorf("SemanticIndex.IndexEntry() error = %v", err)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float32
	}{
		{
			name: "identical vectors",
			a:    []float32{1.0, 2.0, 3.0},
			b:    []float32{1.0, 2.0, 3.0},
			want: 1.0,
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1.0, 0.0},
			b:    []float32{0.0, 1.0},
			want: 0.0,
		},
		{
			name: "opposite vectors",
			a:    []float32{1.0, 1.0},
			b:    []float32{-1.0, -1.0},
			want: -1.0,
		},
		{
			name: "different lengths",
			a:    []float32{1.0, 2.0},
			b:    []float32{1.0},
			want: 0.0,
		},
		{
			name: "empty vectors",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			// Allow small tolerance for floating point
			if abs32(got-tt.want) > 0.01 {
				t.Errorf("cosineSimilarity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestBuildEmbeddingTexts(t *testing.T) {
	entry := &CatalogEntry{
		DisplayName:  "Test Agent",
		Description:  "A test agent",
		Tags:         []string{"browser", "automation"},
		Capabilities: []string{"navigate"},
		RepresentativeQueries: []string{
			"help me navigate",
			"automate browser",
		},
	}

	texts := buildEmbeddingTexts(entry)

	if len(texts) != 3 {
		t.Errorf("buildEmbeddingTexts() count = %v, want 3", len(texts))
	}

	// First text should contain all main fields
	mainText := texts[0]
	if !contains(mainText, "Test Agent") {
		t.Errorf("buildEmbeddingTexts() main text missing display name")
	}
	if !contains(mainText, "browser") {
		t.Errorf("buildEmbeddingTexts() main text missing tags")
	}
}

func TestHybridSearch(t *testing.T) {
	// Create registry with entries
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	// Add test entries with valid URNs
	err = registry.Register(CatalogEntry{
		Identifier:   "urn:air:test.com:server:browser",
		DisplayName:  "Browser Controller",
		Type:         MediaTypeMCPServerCard,
		URL:          "https://test.com/browser.json",
		Description:  "Browser automation tool",
		Tags:         []string{"browser", "automation"},
	})
	if err != nil {
		t.Fatalf("Register browser error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:   "urn:air:test.com:server:memory",
		DisplayName:  "Memory Service",
		Type:         MediaTypeMCPServerCard,
		URL:          "https://test.com/memory.json",
		Description:  "Long-term memory storage",
		Tags:         []string{"memory", "knowledge"},
	})
	if err != nil {
		t.Fatalf("Register memory error = %v", err)
	}

	// Create hybrid search with no semantic (alpha=0 means pure lexical)
	hs := NewHybridSearch(registry, nil, 0)

	req := &SearchRequest{
		Query: "browser",
		Limit: 10,
	}

	resp, err := hs.Search(context.Background(), req)
	if err != nil {
		t.Errorf("HybridSearch.Search() error = %v", err)
	}

	if resp.Total == 0 {
		t.Error("HybridSearch.Search() returned no results")
	}

	// Should find browser controller
	found := false
	for _, r := range resp.Results {
		if contains(r.DisplayName, "Browser") {
			found = true
			break
		}
	}

	if !found {
		t.Error("HybridSearch.Search() did not find Browser Controller")
	}
}

func TestExploreOptions(t *testing.T) {
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	// Add test entries with valid URNs
	err = registry.Register(CatalogEntry{
		Identifier:   "urn:air:test.com:agent:browser",
		DisplayName:  "Browser Agent",
		Type:         MediaTypeA2AAgentCard,
		URL:          "https://test.com/browser.json",
		Tags:         []string{"browser", "automation"},
		Capabilities: []string{"navigate", "click"},
	})
	if err != nil {
		t.Fatalf("Register browser agent error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:   "urn:air:test.com:server:memory",
		DisplayName:  "Memory Server",
		Type:         MediaTypeMCPServerCard,
		URL:          "https://test.com/memory.json",
		Tags:         []string{"memory"},
		Capabilities: []string{"store", "recall"},
	})
	if err != nil {
		t.Fatalf("Register memory server error = %v", err)
	}

	// Test type filter
	opts := &ExploreOptions{
		Type: MediaTypeA2AAgentCard,
	}

	result, err := registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Errorf("ExploreWithOptions() type filter count = %v, want 1", len(result.Entries))
	}

	// Test tags filter
	opts = &ExploreOptions{
		Tags: []string{"browser"},
	}

	result, err = registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Errorf("ExploreWithOptions() tags filter count = %v, want 1", len(result.Entries))
	}

	// Test capabilities filter
	opts = &ExploreOptions{
		Capabilities: []string{"store"},
	}

	result, err = registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Errorf("ExploreWithOptions() capabilities filter count = %v, want 1", len(result.Entries))
	}
}

func TestExploreSorting(t *testing.T) {
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:agent:z",
		DisplayName: "Z Agent",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://test.com/z.json",
	})
	if err != nil {
		t.Fatalf("Register z agent error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:agent:a",
		DisplayName: "A Agent",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://test.com/a.json",
	})
	if err != nil {
		t.Fatalf("Register a agent error = %v", err)
	}

	// Test ascending sort
	opts := &ExploreOptions{
		SortBy:   "name",
		SortDesc: false,
	}

	result, err := registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if len(result.Entries) < 2 {
		t.Fatal("Not enough entries to test sorting")
	}

	if result.Entries[0].DisplayName != "A Agent" {
		t.Errorf("ExploreWithOptions() ascending sort first = %v, want A Agent", result.Entries[0].DisplayName)
	}

	// Test descending sort
	opts = &ExploreOptions{
		SortBy:   "name",
		SortDesc: true,
	}

	result, err = registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if result.Entries[0].DisplayName != "Z Agent" {
		t.Errorf("ExploreWithOptions() descending sort first = %v, want Z Agent", result.Entries[0].DisplayName)
	}
}

func TestExploreFacets(t *testing.T) {
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:agent:one",
		DisplayName: "Agent One",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://test.com/one.json",
		Tags:        []string{"browser", "automation"},
	})
	if err != nil {
		t.Fatalf("Register agent one error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:server:two",
		DisplayName: "Server Two",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test.com/two.json",
		Tags:        []string{"browser"},
	})
	if err != nil {
		t.Fatalf("Register server two error = %v", err)
	}

	opts := &ExploreOptions{
		IncludeFacets: true,
	}

	result, err := registry.ExploreWithOptions(opts)
	if err != nil {
		t.Errorf("ExploreWithOptions() error = %v", err)
	}

	if result.Facets == nil {
		t.Fatal("ExploreWithOptions() facets is nil")
	}

	// Check type facet
	if result.Facets.Types[MediaTypeA2AAgentCard] != 1 {
		t.Errorf("Facets.Types[a2a] = %v, want 1", result.Facets.Types[MediaTypeA2AAgentCard])
	}

	if result.Facets.Types[MediaTypeMCPServerCard] != 1 {
		t.Errorf("Facets.Types[mcp] = %v, want 1", result.Facets.Types[MediaTypeMCPServerCard])
	}

	// Check tag facet
	if result.Facets.Tags["browser"] != 2 {
		t.Errorf("Facets.Tags[browser] = %v, want 2", result.Facets.Tags["browser"])
	}
}

func TestBrowseByType(t *testing.T) {
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:agent:one",
		DisplayName: "Agent One",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://test.com/one.json",
	})
	if err != nil {
		t.Fatalf("Register agent one error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:agent:two",
		DisplayName: "Agent Two",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://test.com/two.json",
	})
	if err != nil {
		t.Fatalf("Register agent two error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:test.com:server:one",
		DisplayName: "Server One",
		Type:        MediaTypeMCPServerCard,
		URL:         "https://test.com/server-one.json",
	})
	if err != nil {
		t.Fatalf("Register server one error = %v", err)
	}

	grouped, err := registry.BrowseByType()
	if err != nil {
		t.Errorf("BrowseByType() error = %v", err)
	}

	if len(grouped[MediaTypeA2AAgentCard]) != 2 {
		t.Errorf("BrowseByType() a2a count = %v, want 2", len(grouped[MediaTypeA2AAgentCard]))
	}

	if len(grouped[MediaTypeMCPServerCard]) != 1 {
		t.Errorf("BrowseByType() mcp count = %v, want 1", len(grouped[MediaTypeMCPServerCard]))
	}
}

func TestGetTypesPublishersTags(t *testing.T) {
	config := DefaultRegistryConfig()
	config.DisplayName = "Test Host"
	config.Identifier = "did:web:test.com"
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	err = registry.Register(CatalogEntry{
		Identifier:  "urn:air:example.com:agent:test",
		DisplayName: "Test Agent",
		Type:        MediaTypeA2AAgentCard,
		URL:         "https://example.com/test.json",
		Tags:        []string{"browser", "automation"},
	})
	if err != nil {
		t.Fatalf("Register test agent error = %v", err)
	}

	// GetTypes
	types := registry.GetTypes()
	if len(types) == 0 {
		t.Error("GetTypes() returned empty")
	}

	// GetPublishers
	publishers := registry.GetPublishers()
	if len(publishers) == 0 {
		t.Error("GetPublishers() returned empty")
	}

	// GetTags
	tags := registry.GetTags()
	if len(tags) == 0 {
		t.Error("GetTags() returned empty")
	}
}