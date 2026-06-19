// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the OpenAI-compatible text embedding client
// used for semantic vector search.
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

// Embedder generates text embeddings using an OpenAI-compatible API.
// It implements both the recall.Embedder interface for backward
// compatibility and the embedding needs of CortexStore.
type Embedder struct {
	cfg    *config.CortexConfig
	client *http.Client
}

// NewEmbedder creates a new embedder using the configured
// embedding provider and model.
func NewEmbedder(cfg *config.CortexConfig) *Embedder {
	return &Embedder{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Embed generates embedding vectors for the given texts.
// Implements the recall.Embedder interface.
func (e *Embedder) Embed(
	ctx context.Context, texts []string,
) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("embed: empty texts")
	}

	// Build OpenAI-compatible request.
	reqBody := map[string]interface{}{
		"model": e.cfg.EmbeddingModel,
		"input": texts,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings",
		stringsTrimSuffix(e.cfg.EmbeddingBaseURL, "/"))

	req, err := http.NewRequestWithContext(
		ctx, "POST", url, bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("embed: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization",
		"Bearer "+e.cfg.EmbeddingAPIKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: http request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"embed: status %d: %s",
			resp.StatusCode,
			truncatePreview(string(respBytes), 200),
		)
	}

	// Parse OpenAI embeddings response.
	var embResp embeddingResponse
	if err := json.Unmarshal(respBytes, &embResp); err != nil {
		return nil, fmt.Errorf("embed: parse response: %w", err)
	}

	vectors := make([][]float64, len(embResp.Data))
	for i, d := range embResp.Data {
		vectors[i] = d.Embedding
	}

	log.Debugf("embed: generated %d vectors, dim=%d",
		len(vectors), len(vectors[0]))

	return vectors, nil
}

// embeddingResponse is the OpenAI-compatible embeddings API response.
type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int        `json:"index"`
	} `json:"data"`
}

// stringsTrimSuffix is a helper to avoid importing "strings" just
// for this call site (imported in other files in this package).
func stringsTrimSuffix(s, suffix string) string {
	if len(s) >= len(suffix) &&
		s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}
