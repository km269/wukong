// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements a Cross-Encoder reranker that re-scores
// retrieval candidates using a dedicated rerank model. It calls
// a Jina/Cohere/SiliconFlow-compatible /rerank endpoint.
package cortex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Reranker re-scores candidate documents against a query using
// a Cross-Encoder model served via an OpenAI-compatible API
// with a /rerank endpoint (Jina/Cohere/SiliconFlow convention).
type Reranker struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewReranker creates a reranker from CortexConfig. Returns nil
// (disabled) when RerankerModel is empty. RerankerBaseURL and
// RerankerAPIKey fall back to the embedding values when not set.
func NewReranker(cfg *config.CortexConfig) *Reranker {
	if cfg == nil || cfg.RerankerModel == "" {
		return nil
	}
	baseURL := cfg.RerankerBaseURL
	if baseURL == "" {
		baseURL = cfg.EmbeddingBaseURL
	}
	apiKey := cfg.RerankerAPIKey
	if apiKey == "" {
		apiKey = cfg.EmbeddingAPIKey
	}
	return &Reranker{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   cfg.RerankerModel,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// rerankRequest is the request body for the /rerank endpoint.
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

// rerankResponse is the response from the /rerank endpoint.
type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// Rerank re-scores the given documents against the query and
// returns indices sorted by relevance (descending). If the API
// call fails for any reason, it falls back to the original order
// (neutral scores) — the reranker is a quality enhancement and its
// failure must not break the retrieval chain (progressive
// degradation, docs/ARCHITECTURE.md §15.5).
func (r *Reranker) Rerank(
	ctx context.Context,
	query string,
	documents []string,
	topN int,
) ([]int, []float64, error) {
	if len(documents) == 0 {
		return nil, nil, nil
	}

	reqBody := rerankRequest{
		Model:     r.model,
		Query:     query,
		Documents: documents,
		TopN:      topN,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("rerank: marshal: %w", err)
	}

	url := fmt.Sprintf("%s/rerank", stringsTrimSuffix(r.baseURL, "/"))

	req, err := http.NewRequestWithContext(
		ctx, "POST", url, bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("rerank: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return fallbackOrder(documents)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallbackOrder(documents)
	}

	if resp.StatusCode != http.StatusOK {
		util.Logger.Warn("rerank: API error, falling back to original order",
			slog.Int("status", resp.StatusCode),
			slog.String("body", truncatePreview(string(respBytes), 200)))
		return fallbackOrder(documents)
	}

	var rr rerankResponse
	if err := json.Unmarshal(respBytes, &rr); err != nil {
		util.Logger.Warn("rerank: unparseable response, falling back",
			slog.String("error", err.Error()))
		return fallbackOrder(documents)
	}

	if len(rr.Results) == 0 {
		util.Logger.Warn("rerank: API returned 0 results, falling back")
		return fallbackOrder(documents)
	}

	indices := make([]int, len(rr.Results))
	scores := make([]float64, len(rr.Results))
	for i, res := range rr.Results {
		indices[i] = res.Index
		scores[i] = res.RelevanceScore
	}

	log.Debugf("rerank: re-scored %d documents, top score=%.4f",
		len(indices), scores[0])

	return indices, scores, nil
}

// fallbackOrder returns the original document order with neutral
// scores — the degraded result used whenever the rerank API is
// unavailable or unparseable. The error is always nil: the fallback
// itself is not a failure.
func fallbackOrder(documents []string) ([]int, []float64, error) {
	indices := make([]int, len(documents))
	scores := make([]float64, len(documents))
	for i := range documents {
		indices[i] = i
		scores[i] = 0
	}
	return indices, scores, nil
}
