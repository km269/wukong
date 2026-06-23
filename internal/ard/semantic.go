// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// SemanticIndex provides vector-based semantic search for ARD catalog entries.
// It uses OpenAI-compatible embedding API to generate vectors from
// representativeQueries and computes cosine similarity for ranking.
type SemanticIndex struct {
	mu          sync.RWMutex
	entries     map[string]*IndexedEntry // URN -> indexed entry
	vectors     map[string][]float32     // URN -> embedding vector
	dimensions  int                      // Vector dimensions
	embedderURL string                   // Embedding API base URL
	embedderKey string                   // Embedding API key
	embedderModel string                 // Embedding model name
	client      *http.Client
}

// IndexedEntry represents a catalog entry with its embedding vector.
type IndexedEntry struct {
	URN        string
	Entry      *CatalogEntry
	Vector     []float32
	QueryVecs  [][]float32 // Vectors for each representativeQuery
}

// NewSemanticIndex creates a new semantic index.
func NewSemanticIndex(embedderURL, embedderKey, embedderModel string) *SemanticIndex {
	return &SemanticIndex{
		entries:      make(map[string]*IndexedEntry),
		vectors:      make(map[string][]float32),
		embedderURL:  embedderURL,
		embedderKey:  embedderKey,
		embedderModel: embedderModel,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// IndexEntry indexes a catalog entry with its representativeQueries.
func (si *SemanticIndex) IndexEntry(ctx context.Context, entry *CatalogEntry) error {
	if entry == nil || entry.Identifier == "" {
		return fmt.Errorf("semantic: invalid entry")
	}

	// Build text for embedding
	texts := buildEmbeddingTexts(entry)
	if len(texts) == 0 {
		return nil // No text to embed
	}

	// Generate embeddings
	vectors, err := si.embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("semantic: embed: %w", err)
	}

	if len(vectors) == 0 {
		return nil
	}

	si.mu.Lock()
	defer si.mu.Unlock()

	// Store the main vector (first one from combined text)
	si.vectors[entry.Identifier] = vectors[0]

	// Store indexed entry
	indexed := &IndexedEntry{
		URN:   entry.Identifier,
		Entry: entry,
		Vector: vectors[0],
	}

	// Store query vectors if we have representativeQueries
	if len(entry.RepresentativeQueries) > 0 && len(vectors) > 1 {
		indexed.QueryVecs = vectors[1:]
	}

	si.entries[entry.Identifier] = indexed

	if si.dimensions == 0 && len(vectors[0]) > 0 {
		si.dimensions = len(vectors[0])
	}

	return nil
}

// IndexCatalog indexes all entries in a catalog.
func (si *SemanticIndex) IndexCatalog(ctx context.Context, catalog *AICatalog) error {
	for i := range catalog.Entries {
		if err := si.IndexEntry(ctx, &catalog.Entries[i]); err != nil {
			// Log error but continue
			continue
		}
	}
	return nil
}

// RemoveEntry removes an entry from the index.
func (si *SemanticIndex) RemoveEntry(urn string) {
	si.mu.Lock()
	defer si.mu.Unlock()

	delete(si.entries, urn)
	delete(si.vectors, urn)
}

// Search performs semantic search using vector similarity.
func (si *SemanticIndex) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("semantic: empty query")
	}

	// Generate query embedding
	queryVecs, err := si.embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("semantic: embed query: %w", err)
	}

	if len(queryVecs) == 0 {
		return nil, fmt.Errorf("semantic: no query vector")
	}

	queryVec := queryVecs[0]

	si.mu.RLock()
	defer si.mu.RUnlock()

	// Compute similarity scores
	results := make([]SearchResult, 0, len(si.entries))

	for urn, indexed := range si.entries {
		// Compute cosine similarity with main vector
		score := cosineSimilarity(queryVec, indexed.Vector)

		// Also check similarity with representativeQueries vectors
		for _, qv := range indexed.QueryVecs {
			qScore := cosineSimilarity(queryVec, qv)
			if qScore > score {
				score = qScore // Use best matching query
			}
		}

		results = append(results, SearchResult{
			Identifier:  urn,
			DisplayName: indexed.Entry.DisplayName,
			Type:        indexed.Entry.Type,
			URL:         indexed.Entry.URL,
			Description: indexed.Entry.Description,
			Score:       float64(score),
		})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Apply limit
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// GetSimilar finds entries similar to a given URN.
func (si *SemanticIndex) GetSimilar(ctx context.Context, urn string, limit int) ([]SearchResult, error) {
	si.mu.RLock()
	target, ok := si.entries[urn]
	si.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("semantic: entry not found: %s", urn)
	}

	si.mu.RLock()
	defer si.mu.RUnlock()

	results := make([]SearchResult, 0, len(si.entries))

	for otherURN, indexed := range si.entries {
		if otherURN == urn {
			continue // Skip self
		}

		score := cosineSimilarity(target.Vector, indexed.Vector)

		results = append(results, SearchResult{
			Identifier:  otherURN,
			DisplayName: indexed.Entry.DisplayName,
			Type:        indexed.Entry.Type,
			URL:         indexed.Entry.URL,
			Description: indexed.Entry.Description,
			Score:       float64(score),
		})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// embed generates embeddings for the given texts.
func (si *SemanticIndex) embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if si.embedderURL == "" || si.embedderKey == "" {
		// No embedder configured, return empty
		return nil, nil
	}

	reqBody := map[string]any{
		"model": si.embedderModel,
		"input": texts,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", strings.TrimSuffix(si.embedderURL, "/"))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+si.embedderKey)

	resp, err := si.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncatePreview(string(respBytes), 200))
	}

	var embResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respBytes, &embResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	vectors := make([][]float32, len(embResp.Data))
	for i, d := range embResp.Data {
		vectors[i] = float64ToFloat32(d.Embedding)
	}

	return vectors, nil
}

// buildEmbeddingTexts builds texts for embedding from a catalog entry.
func buildEmbeddingTexts(entry *CatalogEntry) []string {
	var texts []string

	// Combine display name, description, and tags
	mainText := entry.DisplayName
	if entry.Description != "" {
		mainText += " " + entry.Description
	}
	if len(entry.Tags) > 0 {
		mainText += " " + strings.Join(entry.Tags, " ")
	}
	if len(entry.Capabilities) > 0 {
		mainText += " " + strings.Join(entry.Capabilities, " ")
	}

	texts = append(texts, mainText)

	// Add representative queries as additional texts
	for _, q := range entry.RepresentativeQueries {
		texts = append(texts, q)
	}

	return texts
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt32(normA) * sqrt32(normB))
}

// sqrt32 computes square root for float32.
func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton's method approximation
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// float64ToFloat32 converts float64 slice to float32.
func float64ToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// truncatePreview truncates a string for preview.
func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// HybridSearch combines lexical and semantic search.
type HybridSearch struct {
	lexical    *Registry
	semantic   *SemanticIndex
	alpha      float64 // Weight for semantic (0-1), lexical is (1-alpha)
}

// NewHybridSearch creates a hybrid search combining lexical and semantic.
func NewHybridSearch(registry *Registry, semanticIndex *SemanticIndex, alpha float64) *HybridSearch {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	return &HybridSearch{
		lexical:  registry,
		semantic: semanticIndex,
		alpha:    alpha,
	}
}

// Search performs hybrid search combining lexical and semantic results.
func (hs *HybridSearch) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	var lexicalResults []SearchResult
	var semanticResults []SearchResult
	var err error

	// Perform lexical search
	lexResp, err := hs.lexical.Search(req)
	if err == nil {
		lexicalResults = lexResp.Results
	}

	// Perform semantic search if available
	if hs.semantic != nil && req.Query != "" {
		semanticResults, err = hs.semantic.Search(ctx, req.Query, req.Limit*2)
		if err != nil {
			semanticResults = nil
		}
	}

	// Combine results
	combined := hs.combineResults(lexicalResults, semanticResults)

	// Apply limit
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(combined) > limit {
		combined = combined[:limit]
	}

	return &SearchResponse{
		Results: combined,
		Total:   len(combined),
		Query:   req.Query,
	}, nil
}

// combineResults combines lexical and semantic results with weighted scoring.
func (hs *HybridSearch) combineResults(lexical, semantic []SearchResult) []SearchResult {
	// Map URN to combined score
	scores := make(map[string]float64)
	entries := make(map[string]*SearchResult)

	// Add lexical results with weight (1-alpha)
	lexicalWeight := 1.0 - hs.alpha
	for _, r := range lexical {
		urn := r.Identifier
		scores[urn] += lexicalWeight * r.Score
		entries[urn] = &r
	}

	// Add semantic results with weight (alpha)
	for _, r := range semantic {
		urn := r.Identifier
		scores[urn] += hs.alpha * r.Score
		if _, ok := entries[urn]; !ok {
			entries[urn] = &r
		}
	}

	// Build combined results
	results := make([]SearchResult, 0, len(scores))
	for urn, score := range scores {
		r := entries[urn]
		r.Score = score
		results = append(results, *r)
	}

	// Sort by combined score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}