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
	"net/http"
	"time"

	"github.com/km269/wukong/internal/config"

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
// call fails, it returns the original order as a fallback.
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
		return nil, nil, fmt.Errorf("rerank: http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("rerank: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf(
			"rerank: status %d: %s",
			resp.StatusCode,
			truncatePreview(string(respBytes), 200),
		)
	}

	var rr rerankResponse
	if err := json.Unmarshal(respBytes, &rr); err != nil {
		return nil, nil, fmt.Errorf("rerank: parse: %w", err)
	}

	if len(rr.Results) == 0 {
		return nil, nil, fmt.Errorf("rerank: API returned 0 results")
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
